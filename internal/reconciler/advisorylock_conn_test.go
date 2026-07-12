package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// waitAcquiredConns polls pool.Stat().AcquiredConns() until it equals want or
// a bounded deadline elapses. This is required (not a single synchronous
// check) because puddle/v2's Resource.Destroy() — reached via
// pgxpool.Conn.Release() on an already-closed connection, exactly the CR-01
// error path — reclaims the pool slot on a background goroutine
// (`go res.pool.destroyAcquiredResource(res)`, puddle/v2 pool.go), not
// synchronously before Release() returns.
func waitAcquiredConns(t *testing.T, pool *pgxpool.Pool, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := pool.Stat().AcquiredConns(); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("AcquiredConns did not reach %d within 2s (still %d)", want, pool.Stat().AcquiredConns())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestPGAdvisoryLockReleasesSlotOnError proves acceptance criterion (a)
// (CR-01): a forced TryAcquire query error reclaims the dedicated pgxpool
// slot rather than leaking it. Before the fix, the query-error path only
// hard-closed the connection without Release()ing it, so AcquiredConns
// stayed pinned at baseline+1 forever — this test genuinely gates that
// regression.
func TestPGAdvisoryLockReleasesSlotOnError(t *testing.T) {
	pool := newSoakTestPool(t)
	ctx := context.Background()

	baseline := pool.Stat().AcquiredConns()

	lock, err := NewPGAdvisoryLock(ctx, pool)
	if err != nil {
		t.Fatalf("NewPGAdvisoryLock: %v", err)
	}
	if got := pool.Stat().AcquiredConns(); got != baseline+1 {
		t.Fatalf("AcquiredConns after NewPGAdvisoryLock = %d, want %d (dedicated conn pinned)", got, baseline+1)
	}

	// Force a deterministic TryAcquire query error without real DB fault
	// injection: hard-close the dedicated conn out from under the lock
	// (accessible — same package). The next SELECT pg_try_advisory_lock then
	// fails on a closed connection.
	lock.conn.Conn().Close(context.Background())

	ok, err := lock.TryAcquire(ctx)
	if err == nil {
		t.Fatal("TryAcquire err = nil, want non-nil after forcing a closed connection")
	}
	if ok {
		t.Fatal("TryAcquire ok = true, want false on query error")
	}

	// Polled, not a single synchronous check: Release() on this now-closed
	// connection routes through puddle's Destroy(), which reclaims the slot
	// on a background goroutine (see waitAcquiredConns doc comment).
	waitAcquiredConns(t, pool, baseline)
}

// TestPGAdvisoryLockCloseReleasesSlot is the WR-01 unit-level proof:
// PGAdvisoryLock.Close() releases the dedicated connection, reclaiming its
// pgxpool slot, and is idempotent (a second call is a no-op, no panic).
// Acceptance criterion (b) — bounded-time SIGTERM process exit — was
// reproduced live in 16-VERIFICATION.md and is structurally guaranteed by
// the defer ordering asserted in Task 1's verify step (defer lock.Close()
// registered after defer pool.Close(), so it runs first under LIFO); it is
// not unit-testable here without a live container.
func TestPGAdvisoryLockCloseReleasesSlot(t *testing.T) {
	pool := newSoakTestPool(t)
	ctx := context.Background()

	baseline := pool.Stat().AcquiredConns()

	lock, err := NewPGAdvisoryLock(ctx, pool)
	if err != nil {
		t.Fatalf("NewPGAdvisoryLock: %v", err)
	}
	if got := pool.Stat().AcquiredConns(); got != baseline+1 {
		t.Fatalf("AcquiredConns after NewPGAdvisoryLock = %d, want %d (dedicated conn pinned)", got, baseline+1)
	}

	lock.Close()
	// A healthy (non-closed) connection's Release() takes puddle's
	// synchronous res.Release() path, but poll here too for robustness/
	// consistency with TestPGAdvisoryLockReleasesSlotOnError's async path.
	waitAcquiredConns(t, pool, baseline)

	// Idempotency: a second Close() must not panic (l.conn is already nil).
	lock.Close()
}
