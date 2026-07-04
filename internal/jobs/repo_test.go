package jobs

import (
	"context"
	"os"
	"testing"

	"github.com/apaderin/octoconv/internal/db"
	"github.com/google/uuid"
)

func newTestRepo(t *testing.T) *Repo {
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
	return NewRepo(pool)
}

// createTestClient inserts a minimal clients row (name only, leaving
// api_key_hash NULL so no UNIQUE constraint on the key-hash columns is
// touched) and returns its id, so integration tests can satisfy the
// jobs.client_id foreign key without a cross-package import.
func createTestClient(t *testing.T, r *Repo) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := r.pool.QueryRow(context.Background(), "INSERT INTO clients (name) VALUES ($1) RETURNING id", "jobs-test-client").Scan(&id)
	if err != nil {
		t.Fatalf("create test client: %v", err)
	}
	return id
}

func TestJobLifecycle(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	id, err := r.Create(ctx, CreateParams{
		ClientID:     createTestClient(t, r),
		Operation:    "convert",
		Engine:       "image",
		SourceFormat: "png",
		TargetFormat: "webp",
		Input: Input{
			Ordinal:     0,
			ObjectKey:   "uploads/x/0-in.png",
			Filename:    "in.png",
			Format:      "png",
			SizeBytes:   1234,
			ContentType: "image/png",
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := r.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusQueued || got.SourceFormat != "png" || got.TargetFormat != "webp" {
		t.Fatalf("unexpected job after create: %+v", got)
	}

	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	// Re-activating a non-queued job must fail (guard).
	if err := r.MarkActive(ctx, id); err == nil {
		t.Fatal("expected illegal transition on second MarkActive")
	}

	if err := r.AddOutput(ctx, id, Output{
		Ordinal:     0,
		ObjectKey:   "results/x/0-out.webp",
		Filename:    "out.webp",
		Format:      "webp",
		SizeBytes:   567,
		ContentType: "image/webp",
	}); err != nil {
		t.Fatalf("AddOutput: %v", err)
	}

	if err := r.MarkDone(ctx, id); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	got, err = r.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after done: %v", err)
	}
	if got.Status != StatusDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	if got.StartedAt == nil || got.FinishedAt == nil {
		t.Fatalf("expected started_at and finished_at to be set: %+v", got)
	}

	outs, err := r.Outputs(ctx, id)
	if err != nil {
		t.Fatalf("Outputs: %v", err)
	}
	if len(outs) != 1 || outs[0].ObjectKey != "results/x/0-out.webp" || outs[0].SizeBytes != 567 {
		t.Fatalf("unexpected outputs: %+v", outs)
	}
}

func TestMarkFailed(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	id, err := r.Create(ctx, CreateParams{
		ClientID:  createTestClient(t, r),
		Operation: "convert", Engine: "image", SourceFormat: "png", TargetFormat: "webp",
		Input: Input{ObjectKey: "uploads/y/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	if err := r.MarkFailed(ctx, id, "engine_error", "boom"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, err := r.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusFailed || got.ErrorCode != "engine_error" || got.ErrorMessage != "boom" {
		t.Fatalf("unexpected failed job: %+v", got)
	}
}

func TestGetNotFound(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.Get(context.Background(), uuid.New()); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
