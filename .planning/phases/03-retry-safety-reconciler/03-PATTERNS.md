# Phase 3: Retry-Safety & Reconciler - Pattern Map

**Mapped:** 2026-07-05
**Files analyzed:** 9 (5 modified, 1 modified-config, 1 new package [2 files], 1 modified test-adjacent)
**Analogs found:** 8 / 9 (reconciler package is greenfield — no in-repo analog, documented below)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/worker/worker.go` (`HandleImageConvert` rewrite + new `isTerminal`/error classifier) | controller (asynq task handler) | event-driven | `internal/worker/worker.go` `HandleWebhookDeliver` (same file, lines 103-179) | exact — same file, same role, the "unwrap error, let asynq retry" pattern already lives here |
| `internal/jobs/repo.go` (`MarkActive` widened, new `RequeueStale`, new `RecoveryCount`, `transition` extended with optional `detail`) | model / repository | CRUD | `internal/jobs/repo.go` `MarkFailed`/`transition` (same file, lines 100-108, 207-244) | exact — same file, same guarded-transition mechanism to extend |
| `internal/queue/queue.go` (new `ImageRetryDelay`, `imageRetrySchedule`, `RetryDelayFunc` dispatcher, `NewImageConvertTask` gains `maxRetry` param) | utility / queue producer config | request-response (sync function, no I/O) | `internal/queue/queue.go` `WebhookRetryDelay`/`webhookRetrySchedule`/`NewWebhookDeliverTask` (same file, lines 62-119) | exact — same file, direct template per RESEARCH.md Pattern 3/4 |
| `internal/queue/client.go` (`EnqueueImageConvert` signature/behavior if `maxRetry` threaded through `Client`) | service (producer wrapper) | request-response | `internal/queue/client.go` `EnqueueWebhookDeliver` (lines 40-50) | exact — same file, mirrors existing producer method shape |
| `cmd/worker/main.go` (switch `srv.Run`→`srv.Start`/`Shutdown`, add `signal.NotifyContext`, wire `RetryDelayFunc` dispatcher, start reconciler goroutine, new `envDuration`/`envInt` reads for reconciler config) | config / entry point | event-driven | `cmd/api/main.go` (whole file — `signal.NotifyContext` + goroutine + `<-ctx.Done()` graceful shutdown shape) | role-match — cmd/worker/main.go itself is the base file being edited; cmd/api/main.go is the analog for the *shutdown pattern* it currently lacks |
| `internal/convert/exec.go` / `internal/convert/libvips.go` (no structural change expected; classifier reads stderr content produced here) | service / hardened process exec | file I/O | itself (already read for stderr-format verification, no changes required beyond what error classification in `worker.go` consumes) | exact — these are the source of the terminal-error stderr signatures, not files this phase edits |
| `internal/storage/storage.go` (no structural change expected; classifier calls `minio.ToErrorResponse` on the wrapped errors returned here) | service / storage client | file I/O | itself (`Download`/`Upload`, lines 56-79) | exact — error-wrapping shape (`fmt.Errorf("download %q: %w", key, err)`) is what the classifier unwraps via `errors.As`/`minio.ToErrorResponse` |
| `internal/reconciler/reconciler.go` + `internal/reconciler/reconciler_test.go` (NEW package: `Sweeper`, `NewSweeper`, `Run(ctx)` ticker loop, `sweep()`) | service (background sweeper) + test | batch / event-driven | **No exact analog in repo.** Closest partial matches: `internal/jobs/repo.go` (`transition`/`RequeueStale` reuse for the state-change half) + `cmd/worker/main.go`'s asynq server lifecycle (for the goroutine/ticker + graceful-shutdown half) + `internal/webhook/repo.go` (`RecordAttempt`/`MarkDeadLetter` — closest existing "count prior attempts, cap, then terminal-mark" precedent) | partial (composed from 3 analogs, no single existing sweeper/ticker file) |
| `.env.example` (new vars: `IMAGE_MAX_RETRY`, `RECONCILER_QUEUED_STALE_AFTER`, `RECONCILER_ACTIVE_STALE_AFTER`, `RECONCILER_SWEEP_INTERVAL`, `RECONCILER_MAX_RECOVERIES`) | config | — | itself (existing `WORKER_CONCURRENCY`/`ENGINE_TIMEOUT`/`WEBHOOK_*` block, lines 19-23) | exact — same file, same append-a-block convention |

## Pattern Assignments

### `internal/worker/worker.go` — `HandleImageConvert` rewrite (controller, event-driven)

**Analog:** `HandleWebhookDeliver` in the same file (already the "correct" example per CONTEXT.md canonical_refs).

**Imports pattern** (lines 1-21) — no new imports needed for classification logic beyond `errors` and `strings`; `minio` import needed for `ToErrorResponse`:
```go
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/storage"
	"github.com/apaderin/octoconv/internal/webhook"
)
```
Add: `"errors"`, `"strings"`, `"github.com/minio/minio-go/v7"`.

**Current (buggy) unconditional-MarkFailed pattern to replace** (lines 62-95):
```go
func (h *Handler) HandleImageConvert(ctx context.Context, t *asynq.Task) error {
	payload, err := queue.ParseConvertPayload(t.Payload())
	if err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	jobID := payload.JobID

	job, err := h.repo.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job %s: %w", jobID, err)
	}

	if err := h.repo.MarkActive(ctx, jobID); err != nil {
		// Already active/done/canceled — let asynq drop it rather than loop.
		return fmt.Errorf("%w: mark active: %v", asynq.SkipRetry, err)
	}

	if err := h.process(ctx, job); err != nil {
		_ = h.repo.MarkFailed(ctx, jobID, "engine_error", err.Error())
		if job.CallbackURL != "" {
			_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
		}
		return err
	}
	if job.CallbackURL != "" {
		_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
	}
	return nil
}
```
**Target shape (mirrors `HandleWebhookDeliver`'s "unwrap error → let asynq retry" pattern, lines 103-119, 170-178):** the `job.CallbackURL == ""` / terminal-vs-transient branch structure in `HandleWebhookDeliver` is exactly the shape to copy — a terminal condition returns `%w: <reason>` wrapped in `asynq.SkipRetry`, everything else (`derr` from `Deliver`) returns unwrapped so "asynq applies its own retry/backoff." Apply the same two-branch shape to `process()`'s error using a new `isTerminal(err)` classifier: terminal → `MarkFailed` + `SkipRetry`-wrapped return; transient → return `err` unwrapped, job stays `active` (no `MarkFailed` call).

**Error classification pattern to add (RESEARCH.md Pattern 2, verified against actual dependencies):**
```go
func isTerminal(err error) bool {
	if resp := minio.ToErrorResponse(err); resp.Code == minio.NoSuchKey {
		return true // D-02: storage input genuinely missing
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no converter for") {
		return true // D-01: registry.Lookup miss, mirrors process()'s existing
		            // fmt.Errorf("no converter for %s -> %s", ...) at worker.go:189
	}
	for _, sig := range terminalVipsSignatures {
		if strings.Contains(msg, sig) {
			return true // D-01: engine explicitly signals bad/corrupted format
		}
	}
	return false // everything else transient by default (D-01 broad-retry philosophy)
}

var terminalVipsSignatures = []string{
	"is not a known file format",
	"premature end of jpeg file",
	"jpeg datastream contains no image",
}
```
Note: `process()`'s existing "no converter" error at `worker.go:189` (`fmt.Errorf("no converter for %s -> %s", job.SourceFormat, job.TargetFormat)`) is a plain string, not a typed sentinel — the classifier must string-match it (or the "no converter" check must be refactored into a small sentinel error type for a more robust `errors.As` check; either is acceptable, plain string-match is the lower-risk/smaller diff).

**Auth/Guard pattern (idempotent re-entry guard) — none needed in `worker.go` itself**; this is entirely `MarkActive`'s job (see `repo.go` section below). `HandleImageConvert` just calls `h.repo.MarkActive(ctx, jobID)` as before; after the repo change it silently succeeds on `active -> active` internal retries instead of erroring.

**Pitfall 3 (error-message sanitization) — apply at the `MarkFailed` call site:**
```go
// Current: _ = h.repo.MarkFailed(ctx, jobID, "engine_error", err.Error())
// err.Error() can contain raw vips stderr with local temp paths (workDir at
// worker.go:192, built from job.ID.String()) — this is already returned
// verbatim via GET /jobs/{id} (internal/api/handlers.go:190-191) and webhook
// payloads (worker.go:145-147). Store a short classified reason instead:
_ = h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format")
// Full err.Error() (raw stderr) should go into job_events.detail via the
// extended transition()/MarkFailed signature (see repo.go section) for
// internal diagnostics only.
```

---

### `internal/jobs/repo.go` — `MarkActive` widen + `RequeueStale` + `RecoveryCount` (model, CRUD)

**Analog:** `MarkFailed`/`transition` in the same file.

**Imports pattern** (lines 1-11) — no new imports required (`errors`, `fmt`, `uuid`, `pgx`, `pgxpool` already cover everything):
```go
import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)
```

**Current `MarkActive` to widen** (lines 81-89):
```go
func (r *Repo) MarkActive(ctx context.Context, id uuid.UUID) error {
	return r.transition(ctx, id, StatusActive, []string{StatusQueued}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE jobs SET status = 'active', started_at = now(), attempts = attempts + 1 WHERE id = $1`, id)
		return err
	})
}
```
**Target (RESEARCH.md Pattern 1 — idempotent re-entry, `started_at` pinned via `COALESCE`):**
```go
func (r *Repo) MarkActive(ctx context.Context, id uuid.UUID) error {
	return r.transition(ctx, id, StatusActive, []string{StatusQueued, StatusActive}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE jobs SET status = 'active', started_at = COALESCE(started_at, now()), attempts = attempts + 1 WHERE id = $1`, id)
		return err
	})
}
```

**`RequeueStale` — new transition modeled directly on `MarkFailed`'s shape (lines 100-108):**
```go
func (r *Repo) MarkFailed(ctx context.Context, id uuid.UUID, code, message string) error {
	return r.transition(ctx, id, StatusFailed, []string{StatusQueued, StatusActive}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE jobs SET status = 'failed', finished_at = now(), error_code = $2, error_message = $3 WHERE id = $1`,
			id, code, message)
		return err
	})
}
```
New method follows the identical three-part shape (target status, allowed-from list, apply closure) — see RESEARCH.md Pattern 6 for the exact `RequeueStale(ctx, id, reason string)` signature and body (`UPDATE jobs SET status = 'queued' WHERE id = $1`, allowed-from `[]string{StatusQueued, StatusActive}`).

**`transition` helper to extend with optional `detail` (lines 207-244) — the shared mechanism every new/modified repo method routes through:**
```go
func (r *Repo) transition(
	ctx context.Context,
	id uuid.UUID,
	to string,
	allowedFrom []string,
	apply func(ctx context.Context, tx pgx.Tx) error,
) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var from string
		if err := tx.QueryRow(ctx,
			`SELECT status FROM jobs WHERE id = $1 FOR UPDATE`, id,
		).Scan(&from); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock job: %w", err)
		}

		if !contains(allowedFrom, from) {
			return fmt.Errorf("illegal transition %s -> %s for job %s", from, to, id)
		}

		if err := apply(ctx, tx); err != nil {
			return fmt.Errorf("apply transition: %w", err)
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO job_events (job_id, from_status, to_status) VALUES ($1, $2, $3)`,
			id, from, to,
		); err != nil {
			return fmt.Errorf("insert job_event: %w", err)
		}
		return nil
	})
}
```
Per RESEARCH.md Open Question 2 / Pattern 6: add a `detail any` (or `map[string]any`) parameter, marshal to jsonb in the `INSERT INTO job_events` (`detail`) column, default existing three callers (`MarkActive`, `MarkDone`, `MarkFailed`) to `nil` — a small, mechanical, backward-compatible signature change. This is the **row-locked, event-logged discipline** (`SELECT ... FOR UPDATE` + apply + `job_events` insert, all in one `pgx.BeginFunc` transaction) that the reconciler's `RequeueStale` call and any exhaustion `MarkFailed` call must go through — never an ad-hoc `UPDATE`.

**`RecoveryCount` — new read-only query, modeled on `Outputs`'s simple-query shape (lines 180-205), but scalar not row-set:**
```go
func (r *Repo) RecoveryCount(ctx context.Context, id uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM job_events
		WHERE job_id = $1 AND detail->>'action' = 'reconciler_recovery'`, id,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count recoveries for job %s: %w", id, err)
	}
	return n, nil
}
```

**Test pattern to extend** (`internal/jobs/repo_test.go`, lines 1-149): follow `TestJobLifecycle`/`TestMarkFailed`'s exact shape — `newTestRepo(t)` (skips if `DATABASE_URL` unset), `createTestClient(t, r)`, `r.Create(...)`, then assert guarded-transition behavior. `TestJobLifecycle` (lines 74-80) already demonstrates the "re-activating a non-queued job must fail" assertion style — mirror this for a **new** `TestMarkActiveIdempotentReentry` (assert `active -> active` now succeeds and `started_at` is unchanged across two calls) and `TestRequeueStale`/`TestRecoveryCount`.

---

### `internal/queue/queue.go` — `ImageRetryDelay` + `RetryDelayFunc` dispatcher + `MaxRetry` option (utility, request-response)

**Analog:** `WebhookRetryDelay`/`webhookRetrySchedule`/`NewWebhookDeliverTask` in the same file.

**Imports pattern** (lines 1-14) — no new imports needed (`math/rand`, `time` already present):
```go
import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)
```

**Core backoff-schedule pattern to mirror** (lines 82-119):
```go
var webhookRetrySchedule = []time.Duration{
	30 * time.Second, 1 * time.Minute, 2 * time.Minute,
	4 * time.Minute, 8 * time.Minute, 15 * time.Minute,
}

func WebhookRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	idx := n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(webhookRetrySchedule) {
		idx = len(webhookRetrySchedule) - 1
	}
	base := webhookRetrySchedule[idx]
	jitterRange := float64(base) * 0.25
	jitter := (rand.Float64()*2 - 1) * jitterRange
	delay := time.Duration(float64(base) + jitter)
	if delay < 0 {
		delay = 0
	}
	return delay
}
```
**New `imageRetrySchedule`/`ImageRetryDelay` (D-06: fast seconds-scale, distinct from webhook's 30s→15m) — identical clamp+jitter shape, only the schedule slice and function name differ:**
```go
var imageRetrySchedule = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}

func ImageRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	idx := n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(imageRetrySchedule) {
		idx = len(imageRetrySchedule) - 1
	}
	return imageRetrySchedule[idx] // seconds-scale; jitter optional, D-06 doesn't require it
}
```

**New `RetryDelayFunc` dispatcher (fixes the confirmed server-wide `RetryDelayFunc` defect, D-06/RESEARCH.md Pattern 3):**
```go
func RetryDelayFunc(n int, e error, t *asynq.Task) time.Duration {
	switch t.Type() {
	case TypeImageConvert:
		return ImageRetryDelay(n, e, t)
	case TypeWebhookDeliver:
		return WebhookRetryDelay(n, e, t)
	default:
		return asynq.DefaultRetryDelayFunc(n, e, t)
	}
}
```

**`NewImageConvertTask` — currently no `MaxRetry` option (lines 45-51), mirror `NewWebhookDeliverTask`'s explicit `asynq.MaxRetry(6)` (lines 65-71):**
```go
// Current:
func NewImageConvertTask(jobID uuid.UUID) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeImageConvert, b, asynq.Queue(QueueImage)), nil
}

// Target (D-05, mirrors NewWebhookDeliverTask's asynq.MaxRetry(6) pattern
// exactly, just parameterized so IMAGE_MAX_RETRY is configurable):
func NewImageConvertTask(jobID uuid.UUID, maxRetry int) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeImageConvert, b, asynq.Queue(QueueImage), asynq.MaxRetry(maxRetry)), nil
}
```

**Test pattern to extend** (`internal/queue/queue_test.go`, lines 31-61): `TestWebhookRetryDelaySchedule` is the exact template — copy its schedule-slice + `lo`/`hi` jitter-band-check-per-case loop for a new `TestImageRetryDelaySchedule` (image schedule has no jitter per the recommended implementation, so the band check simplifies to exact equality, or keep the band check if jitter is added). Also add a `TestRetryDelayFuncDispatch` asserting `RetryDelayFunc` routes `TypeImageConvert` tasks through the fast schedule and `TypeWebhookDeliver` tasks through the slow schedule (construct minimal `*asynq.Task` via `asynq.NewTask(TypeImageConvert, nil)` / `asynq.NewTask(TypeWebhookDeliver, nil)` and call `t.Type()` — matches `TestConvertPayloadRoundTrip`'s pattern of constructing a real task and asserting on `task.Type()`, lines 13-29).

---

### `internal/queue/client.go` — `EnqueueImageConvert` gains configured `maxRetry` (service, request-response)

**Analog:** `EnqueueWebhookDeliver` in the same file (lines 40-50).

**Full current pattern to mirror** (lines 1-50):
```go
type Client struct {
	c *asynq.Client
}

func NewClient() (*Client, error) {
	opt, err := RedisOpt()
	if err != nil {
		return nil, err
	}
	return &Client{c: asynq.NewClient(opt)}, nil
}

func (c *Client) EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewImageConvertTask(jobID)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue image convert %s: %w", jobID, err)
	}
	return nil
}
```
Per RESEARCH.md Pattern 4 / Assumption A2 (recommended, not mandatory): store `IMAGE_MAX_RETRY` (read once via `os.Getenv`, mirroring `RedisOpt()`'s `REDIS_ADDR` read at `queue.go:122-128`) as a field on `Client` at `NewClient()` construction time, so `EnqueueImageConvert(ctx, jobID)`'s call signature stays unchanged at every call site (`cmd/api/main.go`, the worker's re-enqueue path, and the new reconciler).

---

### `cmd/worker/main.go` — signal-driven shutdown + reconciler wiring (config/entry point, event-driven)

**Analog:** `cmd/api/main.go` (whole file) for the `signal.NotifyContext` + goroutine + graceful-shutdown shape currently missing from `cmd/worker/main.go`.

**Analog pattern to copy** (`cmd/api/main.go` lines 25-27, 72-87):
```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
// ...
go func() {
	log.Printf("🚀 API listening on %s", addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
}()

<-ctx.Done()
log.Println("🛑 shutting down API...")

shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
defer cancel()
if err := httpSrv.Shutdown(shutdownCtx); err != nil {
	log.Printf("graceful shutdown failed: %v", err)
}
log.Println("bye 👋")
```

**Current worker main to replace** (`cmd/worker/main.go` lines 23-81 — currently `ctx := context.Background()` with no signal handling, and blocking `srv.Run(mux)`):
```go
func main() {
	ctx := context.Background()
	// ... pool/store/redisOpt/signingSecret/qc/h construction unchanged ...
	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeImageConvert, h.HandleImageConvert)
	mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("WORKER_CONCURRENCY", 4),
		Queues:         map[string]int{queue.QueueImage: 2, queue.QueueWebhook: 1},
		RetryDelayFunc: queue.WebhookRetryDelay,
	})

	log.Printf("🐙 worker starting (queues=%s,%s)", queue.QueueImage, queue.QueueWebhook)
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker: %v", err)
	}
	log.Println("bye 👋")
}
```
**Target: `signal.NotifyContext` (copied from `cmd/api/main.go:26`) + `queue.RetryDelayFunc` dispatcher (fixes the confirmed defect) + `srv.Start`/`srv.Shutdown` instead of blocking `srv.Run` (RESEARCH.md Pattern 5, needed so the reconciler ticker goroutine and the asynq server share one coordinated shutdown):**
```go
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// ... pool/store/redisOpt/signingSecret/qc/h construction unchanged ...

	sweeper := reconciler.NewSweeper(jobs.NewRepo(pool), qc, reconciler.Config{
		QueuedStaleAfter: envDuration("RECONCILER_QUEUED_STALE_AFTER", 90*time.Second),
		ActiveStaleAfter: envDuration("RECONCILER_ACTIVE_STALE_AFTER", 5*time.Minute),
		SweepInterval:    envDuration("RECONCILER_SWEEP_INTERVAL", 1*time.Minute),
		MaxRecoveries:    envInt("RECONCILER_MAX_RECOVERIES", 3),
	})

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeImageConvert, h.HandleImageConvert)
	mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("WORKER_CONCURRENCY", 4),
		Queues:         map[string]int{queue.QueueImage: 2, queue.QueueWebhook: 1},
		RetryDelayFunc: queue.RetryDelayFunc, // was queue.WebhookRetryDelay — confirmed defect fixed
	})

	log.Printf("🐙 worker starting (queues=%s,%s)", queue.QueueImage, queue.QueueWebhook)
	if err := srv.Start(mux); err != nil {
		log.Fatalf("worker: %v", err)
	}
	go sweeper.Run(ctx)

	<-ctx.Done()
	log.Println("🛑 shutting down worker...")
	srv.Shutdown()
	log.Println("bye 👋")
}
```
`envInt`/`envDuration`/`firstField` helpers already exist in `cmd/worker/main.go` (lines 83-108) — reuse as-is, no changes needed.

---

### `internal/reconciler/` — NEW package (service, batch/event-driven)

**No exact analog exists in the repo.** This is the one genuinely greenfield file set. Compose the implementation from three partial analogs:

1. **State-change discipline** — reuse `Repo.transition`/`Repo.RequeueStale` (see `internal/jobs/repo.go` section above) for every status change the sweeper makes. Do not write ad-hoc `UPDATE jobs SET ...` in the reconciler package — this is an explicit anti-pattern called out in RESEARCH.md.

2. **Ticker/goroutine lifecycle shape** — no existing file runs a `time.Ticker` loop today; the closest structural precedent is the goroutine + `<-ctx.Done()` shutdown shape already established in `cmd/api/main.go` (lines 72-87, copied into `cmd/worker/main.go` per above). Apply the same `ctx`-driven cancellation to the sweeper's `Run(ctx)` method:
```go
// New pattern (no direct repo precedent) — ticker loop bounded by ctx,
// mirroring the ctx.Done()-driven shutdown already used in cmd/api/main.go.
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}
```

3. **Count-prior-attempts-then-cap-out pattern** — closest existing precedent is `internal/webhook/repo.go`'s `RecordAttempt`/`MarkDeadLetter` pair (attempt counting + a terminal "dead letter" flag once exhausted) combined with `HandleWebhookDeliver`'s own cap check in `worker.go`:
```go
// Source: internal/worker/worker.go:170-176 (HandleWebhookDeliver) — the
// existing "check exhaustion, then mark terminal" shape to mirror for the
// reconciler's D-12 recovery cap:
retryCount, _ := asynq.GetRetryCount(ctx)
maxRetry, _ := asynq.GetMaxRetry(ctx)
// ...
if derr != nil {
	if recErr == nil && retryCount >= maxRetry {
		_ = h.webhookRepo.MarkDeadLetter(ctx, deliveryID)
	}
	return derr
}
```
Reconciler equivalent (per RESEARCH.md Pattern 6): `n, err := repo.RecoveryCount(ctx, jobID)`; if `n < cfg.MaxRecoveries`, call `repo.RequeueStale(ctx, jobID, reason)` + `qc.EnqueueImageConvert(ctx, jobID)`; else call `repo.MarkFailed(ctx, jobID, "reconciler_exhausted", "...")` and, per D-14, `if job.CallbackURL != "" { qc.EnqueueWebhookDeliver(ctx, jobID) }` — this last step mirrors the existing "Postgres-first, best-effort webhook enqueue" pattern already used twice in `worker.go` (lines 84-86, 91-93).

**Stale-job scan query** — no existing query scans by staleness threshold; the closest structural precedent is the simple parameterized `SELECT`s in `Inputs`/`Outputs` (`repo.go` lines 154-178, 181-205) and the existing `jobs_inflight_idx` index (`0001_init.sql:71`, `CREATE INDEX jobs_inflight_idx ON jobs (created_at) WHERE status IN ('queued', 'active')`) — this index already exists and is directly usable by the reconciler's scan query without a new migration:
```sql
SELECT id, status FROM jobs
WHERE (status = 'queued' AND created_at < now() - $1::interval)
   OR (status = 'active' AND started_at < now() - $2::interval)
```

**Package doc-comment convention to follow** (every package has exactly one file-level doc comment, e.g. `internal/jobs/jobs.go:1-3`, `internal/worker/worker.go:1`):
```go
// Package reconciler periodically sweeps Postgres for jobs stranded in
// queued/active past a staleness threshold (lost enqueue, crashed worker)
// and requeues or terminally fails them, bounded by a recovery cap.
package reconciler
```

**Test file** — no analog exists for a ticker-based sweeper test; follow `internal/jobs/repo_test.go`'s `newTestRepo(t)` skip-if-`DATABASE_URL`-unset convention (lines 12-27) for any integration test that exercises `sweep()` against a real Postgres, and keep pure-logic tests (staleness-threshold comparison, cap arithmetic) dependency-free per Go stdlib `testing` idioms used throughout the repo (no assertion library).

## Shared Patterns

### Guarded, row-locked, event-logged state transitions
**Source:** `internal/jobs/repo.go` `transition` (lines 207-244)
**Apply to:** `MarkActive` (widened), new `RequeueStale`, and any reconciler-driven `MarkFailed` call. This is the single mechanism enforcing "no ad-hoc UPDATEs" across this entire phase — every status change, whether from the worker or the reconciler, must go through it.

### Unwrap-and-let-asynq-retry vs SkipRetry-wrap
**Source:** `internal/worker/worker.go` `HandleWebhookDeliver` (lines 103-179), specifically the `asynq.SkipRetry`-wrapped terminal branches (lines 107, 118) vs the unwrapped transient return (line 176)
**Apply to:** `HandleImageConvert`'s new terminal/transient branch — this is the exact two-shape template (`fmt.Errorf("%w: <reason>", asynq.SkipRetry)` for terminal, bare `return err`/`return derr` for transient) to replicate.

### Postgres-first, best-effort webhook enqueue after a status change
**Source:** `internal/worker/worker.go` lines 84-86 and 91-93 (`if job.CallbackURL != "" { _ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID) }`, discarded error, comment explains why)
**Apply to:** The reconciler's `reconciler_exhausted` path (D-14) — after `MarkFailed`, conditionally enqueue a webhook exactly the same way, discarding the enqueue error for the same "Postgres write already committed, don't fail on a best-effort side effect" reason.

### Env-var-only config with inline-comment tolerance
**Source:** `cmd/worker/main.go` `envInt`/`envDuration`/`firstField` (lines 83-108), `.env.example` existing block (lines 19-23)
**Apply to:** All five new env vars this phase introduces (`IMAGE_MAX_RETRY`, `RECONCILER_QUEUED_STALE_AFTER`, `RECONCILER_ACTIVE_STALE_AFTER`, `RECONCILER_SWEEP_INTERVAL`, `RECONCILER_MAX_RECOVERIES`) — reuse the existing helpers verbatim, no new config-loading mechanism.

### Hardened backoff schedule with clamp-to-last-entry
**Source:** `internal/queue/queue.go` `WebhookRetryDelay` (lines 94-119)
**Apply to:** `ImageRetryDelay` — same clamp-index-to-schedule-length shape, shorter/faster schedule per D-06.

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `internal/reconciler/reconciler.go` (ticker-driven sweep loop itself) | service | batch/event-driven | No existing file runs a periodic `time.Ticker` loop scanning Postgres; composed from three partial analogs (guarded transitions, cmd/api's ctx-driven shutdown shape, webhook's attempt-cap pattern) as detailed above — planner should treat this file as new design, not a copy, while reusing the sub-patterns listed |
| `internal/reconciler/reconciler_test.go` | test | — | No sweeper/ticker test precedent exists; follow `internal/jobs/repo_test.go`'s DB-integration-test skip convention for the Postgres-touching parts, plain stdlib `testing` for pure staleness/cap-arithmetic logic |

## Metadata

**Analog search scope:** `internal/worker/`, `internal/jobs/`, `internal/queue/`, `internal/webhook/`, `internal/convert/`, `internal/storage/`, `internal/db/migrations/`, `cmd/api/`, `cmd/worker/`, `.env.example`
**Files scanned:** 17 (worker.go, repo.go, jobs.go, repo_test.go, queue.go, client.go, queue_test.go, deliver.go, exec.go, libvips.go, convert.go, storage.go, 0001_init.sql, cmd/api/main.go, cmd/worker/main.go, .env.example, plus `ls` of internal/webhook and internal/worker directories)
**Pattern extraction date:** 2026-07-05
