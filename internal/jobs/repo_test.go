package jobs

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

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
	// Re-activating an already-active job must succeed idempotently (asynq's
	// internal same-task retry re-enters the handler at MarkActive).
	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("expected idempotent re-entry on second MarkActive, got: %v", err)
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
	detail := map[string]any{"engine_stderr": "boom: /tmp/octoconv-x/in.png is not a known file format"}
	if err := r.MarkFailed(ctx, id, "engine_error", "boom", detail); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	got, err := r.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusFailed || got.ErrorCode != "engine_error" || got.ErrorMessage != "boom" {
		t.Fatalf("unexpected failed job: %+v", got)
	}

	// The detail payload must round-trip via job_events.detail (jsonb).
	var detailJSON []byte
	if err := r.pool.QueryRow(ctx,
		`SELECT detail FROM job_events WHERE job_id = $1 AND to_status = 'failed' ORDER BY id DESC LIMIT 1`, id,
	).Scan(&detailJSON); err != nil {
		t.Fatalf("query job_events detail: %v", err)
	}
	var got2 map[string]any
	if err := json.Unmarshal(detailJSON, &got2); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if got2["engine_stderr"] != detail["engine_stderr"] {
		t.Fatalf("detail = %+v, want %+v", got2, detail)
	}
}

func TestMarkFailedNilDetail(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	id, err := r.Create(ctx, CreateParams{
		ClientID:  createTestClient(t, r),
		Operation: "convert", Engine: "image", SourceFormat: "png", TargetFormat: "webp",
		Input: Input{ObjectKey: "uploads/z/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	if err := r.MarkFailed(ctx, id, "engine_error", "boom", nil); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	var detailJSON *string
	if err := r.pool.QueryRow(ctx,
		`SELECT detail FROM job_events WHERE job_id = $1 AND to_status = 'failed' ORDER BY id DESC LIMIT 1`, id,
	).Scan(&detailJSON); err != nil {
		t.Fatalf("query job_events detail: %v", err)
	}
	if detailJSON != nil {
		t.Fatalf("expected NULL detail, got %q", *detailJSON)
	}
}

func TestMarkActiveIdempotentReentry(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	id, err := r.Create(ctx, CreateParams{
		ClientID:  createTestClient(t, r),
		Operation: "convert", Engine: "image", SourceFormat: "png", TargetFormat: "webp",
		Input: Input{ObjectKey: "uploads/w/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("first MarkActive: %v", err)
	}
	first, err := r.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after first MarkActive: %v", err)
	}
	if first.Status != StatusActive || first.StartedAt == nil {
		t.Fatalf("unexpected job after first MarkActive: %+v", first)
	}

	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("second MarkActive (idempotent re-entry): %v", err)
	}
	second, err := r.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get after second MarkActive: %v", err)
	}
	if second.Status != StatusActive {
		t.Fatalf("status after second MarkActive = %q, want active", second.Status)
	}
	if second.StartedAt == nil || !second.StartedAt.Equal(*first.StartedAt) {
		t.Fatalf("started_at changed across re-entry: first=%v second=%v", first.StartedAt, second.StartedAt)
	}

	var attempts int
	if err := r.pool.QueryRow(ctx, `SELECT attempts FROM jobs WHERE id = $1`, id).Scan(&attempts); err != nil {
		t.Fatalf("query attempts: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestGetNotFound(t *testing.T) {
	r := newTestRepo(t)
	if _, err := r.Get(context.Background(), uuid.New()); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRequeueStale(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	id, err := r.Create(ctx, CreateParams{
		ClientID:  createTestClient(t, r),
		Operation: "convert", Engine: "image", SourceFormat: "png", TargetFormat: "webp",
		Input: Input{ObjectKey: "uploads/rq/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}

	if err := r.RequeueStale(ctx, id, "stale_active"); err != nil {
		t.Fatalf("RequeueStale (active): %v", err)
	}
	got, err := r.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusQueued {
		t.Fatalf("status = %q, want queued", got.Status)
	}

	var action, reason string
	if err := r.pool.QueryRow(ctx,
		`SELECT detail->>'action', detail->>'reason' FROM job_events
		 WHERE job_id = $1 AND from_status = 'active' AND to_status = 'queued'
		 ORDER BY id DESC LIMIT 1`, id,
	).Scan(&action, &reason); err != nil {
		t.Fatalf("query job_events detail: %v", err)
	}
	if action != detailActionRecovery || reason != "stale_active" {
		t.Fatalf("detail action/reason = %q/%q, want %q/%q", action, reason, detailActionRecovery, "stale_active")
	}

	// Lost-enqueue case: RequeueStale on an already-queued job also succeeds
	// (allowedFrom includes queued).
	if err := r.RequeueStale(ctx, id, "stale_queued"); err != nil {
		t.Fatalf("RequeueStale (queued): %v", err)
	}

	// A done/failed job cannot be requeued (illegal transition).
	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("MarkActive before MarkDone: %v", err)
	}
	if err := r.MarkDone(ctx, id); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}
	if err := r.RequeueStale(ctx, id, "stale_active"); err == nil {
		t.Fatalf("expected illegal-transition error requeuing a done job, got nil")
	}
}

func TestRecoveryCount(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	id, err := r.Create(ctx, CreateParams{
		ClientID:  createTestClient(t, r),
		Operation: "convert", Engine: "image", SourceFormat: "png", TargetFormat: "webp",
		Input: Input{ObjectKey: "uploads/rc/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	n, err := r.RecoveryCount(ctx, id)
	if err != nil {
		t.Fatalf("RecoveryCount (before any recovery): %v", err)
	}
	if n != 0 {
		t.Fatalf("RecoveryCount = %d, want 0", n)
	}

	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("MarkActive: %v", err)
	}
	if err := r.RequeueStale(ctx, id, "stale_active"); err != nil {
		t.Fatalf("RequeueStale #1: %v", err)
	}
	n, err = r.RecoveryCount(ctx, id)
	if err != nil {
		t.Fatalf("RecoveryCount (after 1 recovery): %v", err)
	}
	if n != 1 {
		t.Fatalf("RecoveryCount = %d, want 1", n)
	}

	if err := r.MarkActive(ctx, id); err != nil {
		t.Fatalf("MarkActive #2: %v", err)
	}
	if err := r.RequeueStale(ctx, id, "stale_active"); err != nil {
		t.Fatalf("RequeueStale #2: %v", err)
	}
	n, err = r.RecoveryCount(ctx, id)
	if err != nil {
		t.Fatalf("RecoveryCount (after 2 recoveries): %v", err)
	}
	if n != 2 {
		t.Fatalf("RecoveryCount = %d, want 2", n)
	}
}

func TestFindStale(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	clientID := createTestClient(t, r)

	oldQueued, err := r.Create(ctx, CreateParams{
		ClientID:  clientID,
		Operation: "convert", Engine: "image", SourceFormat: "png", TargetFormat: "webp",
		Input: Input{ObjectKey: "uploads/fs1/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create oldQueued: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE jobs SET created_at = now() - interval '1 hour' WHERE id = $1`, oldQueued); err != nil {
		t.Fatalf("backdate oldQueued created_at: %v", err)
	}

	freshQueued, err := r.Create(ctx, CreateParams{
		ClientID:  clientID,
		Operation: "convert", Engine: "image", SourceFormat: "png", TargetFormat: "webp",
		Input: Input{ObjectKey: "uploads/fs2/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create freshQueued: %v", err)
	}

	oldActive, err := r.Create(ctx, CreateParams{
		ClientID:  clientID,
		Operation: "convert", Engine: "image", SourceFormat: "png", TargetFormat: "webp",
		Input: Input{ObjectKey: "uploads/fs3/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create oldActive: %v", err)
	}
	if err := r.MarkActive(ctx, oldActive); err != nil {
		t.Fatalf("MarkActive oldActive: %v", err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE jobs SET started_at = now() - interval '1 hour' WHERE id = $1`, oldActive); err != nil {
		t.Fatalf("backdate oldActive started_at: %v", err)
	}

	freshActive, err := r.Create(ctx, CreateParams{
		ClientID:  clientID,
		Operation: "convert", Engine: "image", SourceFormat: "png", TargetFormat: "webp",
		Input: Input{ObjectKey: "uploads/fs4/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create freshActive: %v", err)
	}
	if err := r.MarkActive(ctx, freshActive); err != nil {
		t.Fatalf("MarkActive freshActive: %v", err)
	}

	stale, err := r.FindStale(ctx, 90*time.Second, 5*time.Minute)
	if err != nil {
		t.Fatalf("FindStale: %v", err)
	}
	got := map[uuid.UUID]string{}
	for _, j := range stale {
		got[j.ID] = j.Status
	}
	if got[oldQueued] != StatusQueued {
		t.Fatalf("expected oldQueued in stale results as %q, got %+v", StatusQueued, stale)
	}
	if got[oldActive] != StatusActive {
		t.Fatalf("expected oldActive in stale results as %q, got %+v", StatusActive, stale)
	}
	if _, ok := got[freshQueued]; ok {
		t.Fatalf("freshQueued should not be stale: %+v", stale)
	}
	if _, ok := got[freshActive]; ok {
		t.Fatalf("freshActive should not be stale: %+v", stale)
	}
}
