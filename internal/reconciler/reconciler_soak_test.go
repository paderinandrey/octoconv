package reconciler

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
)

// newSoakTestPool mirrors internal/jobs/repo_test.go's newTestRepo helper
// (same DATABASE_URL skip guard, same db.Connect/db.Migrate/t.Cleanup
// sequence). It lives here, duplicated rather than imported, because this
// file is in package reconciler and cannot reach jobs' unexported test
// helpers across package boundaries.
func newSoakTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping soak test")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// createSoakTestClient mirrors internal/jobs/repo_test.go's createTestClient
// helper: inserts a minimal clients row (name only) so a soak-test job can
// satisfy the jobs.client_id foreign key.
func createSoakTestClient(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		"INSERT INTO clients (name) VALUES ($1) RETURNING id", "reconciler-soak-test-client",
	).Scan(&id)
	if err != nil {
		t.Fatalf("create soak test client: %v", err)
	}
	return id
}

// TestSoakRecoversStrandedQueuedJob proves RECON-05 SC3 (D-07): a job
// genuinely stranded in queued past QueuedStaleAfter is recovered by a live,
// running Sweeper.Run process within the expected sweep cadence, using REAL
// elapsed wall-clock time. No SQL backdating of created_at is used — the
// staleness threshold must be crossed by genuinely waiting.
//
// This pairs a REAL jobs.Repo (live Postgres) with the existing in-memory
// fakeEnqueuer (NOT a real Redis-backed producer): a real asynq.Unique lock
// on the image queue has a hardcoded 2-minute uniqueTTLSafetyMargin floor
// (internal/queue/queue.go), which would blow this test's "well under a
// minute" time budget (06-RESEARCH.md Pitfall 3).
func TestSoakRecoversStrandedQueuedJob(t *testing.T) {
	pool := newSoakTestPool(t)
	repo := jobs.NewRepo(pool)
	clientID := createSoakTestClient(t, pool)

	jobID, err := repo.Create(context.Background(), jobs.CreateParams{
		ClientID:     clientID,
		Operation:    "convert",
		Engine:       "image",
		SourceFormat: "png",
		TargetFormat: "webp",
		Input:        jobs.Input{ObjectKey: "uploads/soak/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	enq := &fakeEnqueuer{} // in-memory, no live Redis (reconciler_test.go)
	cfg := Config{
		QueuedStaleAfter: 1 * time.Second,
		ActiveStaleAfter: 1 * time.Second,
		SweepInterval:    300 * time.Millisecond,
		MaxRecoveries:    2,
	}
	s := NewSweeper(repo, enq, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := repo.Get(context.Background(), jobID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if j.Status == jobs.StatusQueued && len(enq.imageCallIDs()) >= 1 {
			return // recovered under genuine elapsed wall-clock time
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job was not recovered within 10s of real elapsed time")
}

// TestSoakExhaustsAtCap proves RECON-05 SC4 (D-08): a job exceeding
// MaxRecoveries under real elapsed time is terminally failed, with the
// failure recorded in job_events. Same real-Repo + fakeEnqueuer pairing as
// above.
//
// Per 06-RESEARCH.md Pitfall 4, RequeueStale never resets created_at, so a
// job recovered via the active branch trips the queued-branch staleness
// check on the very next tick — the sequence of recorded recovery `reason`
// values is not fixed across runs. This test therefore asserts only the
// final terminal state (status=failed) and that at least one exhaustion
// job_events row exists, never a specific reason sequence.
func TestSoakExhaustsAtCap(t *testing.T) {
	pool := newSoakTestPool(t)
	repo := jobs.NewRepo(pool)
	clientID := createSoakTestClient(t, pool)

	jobID, err := repo.Create(context.Background(), jobs.CreateParams{
		ClientID:     clientID,
		Operation:    "convert",
		Engine:       "image",
		SourceFormat: "png",
		TargetFormat: "webp",
		Input:        jobs.Input{ObjectKey: "uploads/soak/0-exhaust.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	enq := &fakeEnqueuer{}
	cfg := Config{
		QueuedStaleAfter: 1 * time.Second,
		ActiveStaleAfter: 1 * time.Second,
		SweepInterval:    300 * time.Millisecond,
		MaxRecoveries:    2,
	}
	s := NewSweeper(repo, enq, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, err := repo.Get(context.Background(), jobID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if j.Status == jobs.StatusFailed {
			// Terminal state reached under real elapsed time. Now confirm the
			// exhaustion was recorded in job_events (mirrors TestMarkFailed's
			// detail-round-trip query style, internal/jobs/repo_test.go).
			var count int
			if err := pool.QueryRow(context.Background(),
				`SELECT count(*) FROM job_events WHERE job_id = $1 AND to_status = 'failed' AND detail->>'action' = 'reconciler_exhausted'`,
				jobID,
			).Scan(&count); err != nil {
				t.Fatalf("query job_events: %v", err)
			}
			if count < 1 {
				t.Fatalf("expected at least one reconciler_exhausted job_events row, got %d", count)
			}
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("job did not reach terminal failed state within 15s of real elapsed time")
}
