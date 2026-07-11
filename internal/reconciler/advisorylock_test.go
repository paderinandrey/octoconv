package reconciler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/jobs"
)

// fakeLock is an in-memory AdvisoryLock implementation for unit tests, with
// a configurable (acquired, err) result and a call counter.
type fakeLock struct {
	acquired bool
	err      error
	calls    int
}

func (l *fakeLock) TryAcquire(ctx context.Context) (bool, error) {
	l.calls++
	return l.acquired, l.err
}

// runWithLockFor starts s.RunWithLock(ctx, lock) in a goroutine, lets it run
// for d, cancels ctx, and waits (bounded) for the goroutine to return —
// mirroring TestRunStopsOnContextCancel's timing pattern so the test stays
// deterministic and never hangs.
func runWithLockFor(t *testing.T, s *Sweeper, lock AdvisoryLock, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunWithLock(ctx, lock)
		close(done)
	}()

	time.Sleep(d)
	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("RunWithLock did not return within 1s of context cancellation")
	}
}

func TestRunWithLockSweepsWhenAcquired(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "image"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{}
	cfg := testConfig()
	cfg.SweepInterval = 5 * time.Millisecond
	s := NewSweeper(store, enq, cfg)

	lock := &fakeLock{acquired: true, err: nil}
	runWithLockFor(t, s, lock, 30*time.Millisecond)

	if lock.calls == 0 {
		t.Fatal("TryAcquire was never called")
	}
	if len(enq.imageCalls) == 0 {
		t.Fatal("EnqueueImageConvert was never called — sweep did not run despite lock acquired")
	}
}

func TestRunWithLockSkipsWhenNotAcquired(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "image"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{}
	cfg := testConfig()
	cfg.SweepInterval = 5 * time.Millisecond
	s := NewSweeper(store, enq, cfg)

	lock := &fakeLock{acquired: false, err: nil}
	runWithLockFor(t, s, lock, 30*time.Millisecond)

	if lock.calls == 0 {
		t.Fatal("TryAcquire was never called")
	}
	if len(enq.imageCalls) != 0 {
		t.Fatalf("EnqueueImageConvert calls = %d, want 0 — not leader, sweep must be skipped", len(enq.imageCalls))
	}
}

func TestRunWithLockFailSafeSkipsOnLockError(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "image"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{}
	cfg := testConfig()
	cfg.SweepInterval = 5 * time.Millisecond
	s := NewSweeper(store, enq, cfg)

	lock := &fakeLock{acquired: false, err: errors.New("connection lost")}
	runWithLockFor(t, s, lock, 30*time.Millisecond)

	if lock.calls == 0 {
		t.Fatal("TryAcquire was never called")
	}
	// Fail-safe: a lock-check error must NOT sweep, even though we cannot
	// distinguish "definitely not leader" from "unknown" — uncertainty must
	// always resolve to skip, never to sweep unguarded (D-01/D-02).
	if len(enq.imageCalls) != 0 {
		t.Fatalf("EnqueueImageConvert calls = %d, want 0 — lock-check error must fail safe (skip), not sweep", len(enq.imageCalls))
	}
}

func TestRunWithLockStopsOnContextCancel(t *testing.T) {
	store := &fakeStore{recoveryCount: map[uuid.UUID]int{}}
	enq := &fakeEnqueuer{}
	cfg := testConfig()
	cfg.SweepInterval = 5 * time.Millisecond
	s := NewSweeper(store, enq, cfg)

	lock := &fakeLock{acquired: true, err: nil}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunWithLock(ctx, lock)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("RunWithLock did not return within 1s of context cancellation")
	}
}
