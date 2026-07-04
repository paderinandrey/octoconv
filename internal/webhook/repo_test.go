package webhook

import (
	"context"
	"os"
	"testing"

	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
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

// createTestJob inserts a minimal clients row and a jobs row (via
// jobs.Repo.Create) so RecordAttempt can satisfy the
// webhook_deliveries.job_id foreign key.
func createTestJob(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var clientID uuid.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO clients (name) VALUES ($1) RETURNING id", "webhook-test-client").Scan(&clientID); err != nil {
		t.Fatalf("create test client: %v", err)
	}

	jobID, err := jobs.NewRepo(pool).Create(ctx, jobs.CreateParams{
		ClientID:     clientID,
		Operation:    "convert",
		Engine:       "image",
		SourceFormat: "png",
		TargetFormat: "webp",
		Input: jobs.Input{
			Ordinal:     0,
			ObjectKey:   "uploads/webhook-test/0-in.png",
			Filename:    "in.png",
			Format:      "png",
			SizeBytes:   1234,
			ContentType: "image/png",
		},
	})
	if err != nil {
		t.Fatalf("create test job: %v", err)
	}
	return jobID
}

func TestRecordAttemptAndMarkDeadLetter(t *testing.T) {
	pool := newTestPool(t)
	r := NewRepo(pool)
	ctx := context.Background()

	jobID := createTestJob(t, pool)

	failCode := 500
	firstID, err := r.RecordAttempt(ctx, jobID, "https://example.com/callback", 1, &failCode, false)
	if err != nil {
		t.Fatalf("RecordAttempt (attempt 1): %v", err)
	}

	okCode := 200
	secondID, err := r.RecordAttempt(ctx, jobID, "https://example.com/callback", 2, &okCode, true)
	if err != nil {
		t.Fatalf("RecordAttempt (attempt 2): %v", err)
	}

	if firstID == uuid.Nil || secondID == uuid.Nil {
		t.Fatalf("expected non-nil delivery ids, got %s and %s", firstID, secondID)
	}
	if firstID == secondID {
		t.Fatalf("expected two distinct delivery ids, got the same id %s for both attempts", firstID)
	}

	if err := r.MarkDeadLetter(ctx, firstID); err != nil {
		t.Fatalf("MarkDeadLetter: %v", err)
	}

	var deadLetter bool
	if err := pool.QueryRow(ctx, "SELECT dead_letter FROM webhook_deliveries WHERE id = $1", firstID).Scan(&deadLetter); err != nil {
		t.Fatalf("select dead_letter: %v", err)
	}
	if !deadLetter {
		t.Fatalf("expected dead_letter = true for id %s, got false", firstID)
	}

	var secondDeadLetter bool
	if err := pool.QueryRow(ctx, "SELECT dead_letter FROM webhook_deliveries WHERE id = $1", secondID).Scan(&secondDeadLetter); err != nil {
		t.Fatalf("select dead_letter (second): %v", err)
	}
	if secondDeadLetter {
		t.Fatalf("expected dead_letter = false for untouched id %s, got true", secondID)
	}
}
