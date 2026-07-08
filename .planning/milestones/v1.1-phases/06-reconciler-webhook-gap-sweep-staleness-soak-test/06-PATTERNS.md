# Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test - Pattern Map

**Mapped:** 2026-07-08
**Files analyzed:** 9 (6 modified, 3 new)
**Analogs found:** 9 / 9 (every file has a same-file or same-package existing pattern to mirror — this phase is a pure extension of Phase 3/4 conventions, confirmed by RESEARCH.md's own source-verified analysis)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/queue/queue.go` (modify: `NewWebhookDeliverTask`, + `WebhookUniqueTTL`, `webhookBackoffSum`, `webhookMaxRetry`, `webhookPerAttemptTimeout`) | utility (task-shape/derivation) | transform | same file, `ImageUniqueTTL`/`imageBackoffSum`/`NewImageConvertTask` (lines 51-61, 182-228) | exact (same file, same shape, sibling queue) |
| `internal/queue/client.go` (modify: `Client` struct, `NewClient`) | service/config | event-driven (producer) | same file, `imageMaxRetry`/`imageUniqueTTL` fields + derivation in `NewClient` (lines 15-44) | exact |
| `internal/queue/queue_test.go` (modify: + `TestWebhookUniqueTTL`, `TestEnqueueWebhookDeliverDuplicate`) | test | request-response | same file, `TestImageUniqueTTL` (lines 117-154), `TestEnqueueImageConvert` (lines 156-198) | exact |
| `internal/jobs/repo.go` (modify: + `WebhookGapJob` type, `FindWebhookGaps`, `RecordWebhookGapRecovered`, `detailActionWebhookGapRecovered`) | model/repository | CRUD (read) + event-driven (write) | same file, `StaleJob`/`FindStale` (lines 25-31, 164-195) for the finder; `transition`'s event-insert half (lines 333-337) for the recorder | exact (finder), role-match (recorder — deliberately bypasses `transition`, see Pattern Assignments) |
| `internal/jobs/repo_test.go` (modify: + `TestFindWebhookGaps`) | test | request-response | same file, `TestFindStale` (lines 354-427) | exact |
| `internal/reconciler/reconciler.go` (modify: `jobStore` interface, `sweep()`) | service (orchestrator) | event-driven | same file, existing `sweep()` image-recovery loop (lines 81-184) | exact |
| `internal/reconciler/reconciler_test.go` (modify: extend `fakeStore`/`fakeEnqueuer`, + 2-3 new unit tests) | test | event-driven | same file, `TestSweepRecoversUnderCap`/`TestSweepSkipsDuplicateEnqueue`/`TestSweepExhaustsAtCap` (lines 91-231) | exact |
| `internal/reconciler/reconciler_soak_test.go` (NEW FILE) | test | event-driven / batch (real wall-clock) | `internal/jobs/repo_test.go`'s `newTestRepo`/`createTestClient` (lines 14-43) for live-DB setup + same-package `fakeEnqueuer` (`reconciler_test.go` lines 65-80) for the enqueue side | role-match (composed from two existing analogs, no single direct precedent for the soak-style polling loop) |
| `internal/db/migrations/0004_webhook_deliveries_job_idx.sql` (NEW FILE, optional per RESEARCH.md A2) | migration | batch (DDL) | `internal/db/migrations/0003_webhook_dead_letter.sql` (full file, 12 lines) | exact |

## Pattern Assignments

### `internal/queue/queue.go` (utility, transform)

**Analog:** same file — `ImageUniqueTTL` / `imageBackoffSum` / `NewImageConvertTask` (lines 43-61, 182-228)

**Imports pattern** (lines 1-14, unchanged — no new imports needed; `math/rand` already imported for `WebhookRetryDelay`'s existing jitter):
```go
package queue

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

**Core pattern to copy — add `asynq.Unique` to task creation** (mirror lines 51-61, currently applied only to `NewImageConvertTask`):
```go
// EXISTING shape to mirror (lines 51-61):
func NewImageConvertTask(jobID uuid.UUID, maxRetry int, uniqueTTL time.Duration) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeImageConvert, b,
		asynq.Queue(QueueImage),
		asynq.MaxRetry(maxRetry),
		asynq.Unique(uniqueTTL),
	), nil
}

// CURRENT NewWebhookDeliverTask (lines 75-81) — D-01 target, add asynq.Unique
// exactly like the image task does; signature must gain a uniqueTTL param
// (mirroring maxRetry/uniqueTTL both being parameters on the image side, not
// hardcoded) — MaxRetry stays the existing literal 6 unless the planner also
// parameterizes it (RESEARCH.md keeps it a named const `webhookMaxRetry`):
func NewWebhookDeliverTask(jobID uuid.UUID) (*asynq.Task, error) {
	b, err := json.Marshal(WebhookPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook payload: %w", err)
	}
	return asynq.NewTask(TypeWebhookDeliver, b, asynq.Queue(QueueWebhook), asynq.MaxRetry(6)), nil
}
```

**Derivation-function pattern to copy** (lines 177-228 — `uniqueTTLSafetyMargin`, `imageBackoffSum`, `ImageUniqueTTL` doc-comment style and worst-case-formula comment block):
```go
// uniqueTTLSafetyMargin already exists at package scope (line 180) — REUSE
// it verbatim per RESEARCH.md Assumption A1; do not add a webhook-specific
// margin constant.
const uniqueTTLSafetyMargin = 2 * time.Minute

// imageBackoffSum (lines 185-191) is the shape to mirror STRUCTURALLY, but
// NOT by calling ImageRetryDelay/WebhookRetryDelay in the loop — see the
// CRITICAL DEVIATION below (Pitfall 1 in RESEARCH.md).
func imageBackoffSum(maxRetry int) time.Duration {
	var sum time.Duration
	for i := 0; i < maxRetry; i++ {
		sum += ImageRetryDelay(i, nil, nil) // safe here: ImageRetryDelay has NO jitter
	}
	return sum
}

// ImageUniqueTTL (lines 226-228) is the exact function shape/doc-comment
// convention WebhookUniqueTTL must follow: same param order (maxRetry,
// perAttemptBound), same one-line formula, same "Worst-case formula:" doc
// comment line with a worked numeric example.
func ImageUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + imageBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}
```

**CRITICAL DEVIATION from the mirrored pattern (must NOT copy verbatim):** `webhookBackoffSum` must NOT call `WebhookRetryDelay` in its loop the way `imageBackoffSum` calls `ImageRetryDelay` — `WebhookRetryDelay` (lines 111-129) has `±25%` `rand.Float64()` jitter baked in (`imageRetryDelay` does not). RESEARCH.md Pattern 1 gives the exact corrected implementation to use instead (sum `webhookRetrySchedule[i] * 1.25` directly against the existing `webhookRetrySchedule` var, lines 95-102). Full recommended code already produced in `06-RESEARCH.md` lines 154-228 — copy that block, not the naive `imageBackoffSum` port.

---

### `internal/queue/client.go` (service, event-driven producer)

**Analog:** same file — `imageMaxRetry`/`imageUniqueTTL` fields + `NewClient` derivation (lines 15-44)

**Core pattern to copy** (lines 15-44 — add a `webhookUniqueTTL` field computed once at construction, same discipline as `imageUniqueTTL`):
```go
type Client struct {
	c *asynq.Client

	imageMaxRetry  int
	imageUniqueTTL time.Duration
	// ADD: webhookUniqueTTL time.Duration — computed once via
	// WebhookUniqueTTL(webhookMaxRetry, webhookPerAttemptTimeout), mirroring
	// imageUniqueTTL's derive-once-at-construction discipline. No new env var
	// needed (D-05/Phase 2 already fixed webhookMaxRetry/perAttemptTimeout as
	// constants, not env-configurable) — see queue.go's webhookMaxRetry /
	// webhookPerAttemptTimeout constants (RESEARCH.md Pattern 1).
}

func NewClient() (*Client, error) {
	opt, err := RedisOpt()
	if err != nil {
		return nil, err
	}
	imageMaxRetry := envInt("IMAGE_MAX_RETRY", 4)
	engineTimeout := envDuration("ENGINE_TIMEOUT", 120*time.Second)
	return &Client{
		c:              asynq.NewClient(opt),
		imageMaxRetry:  imageMaxRetry,
		imageUniqueTTL: ImageUniqueTTL(imageMaxRetry, engineTimeout),
		// ADD: webhookUniqueTTL: WebhookUniqueTTL(webhookMaxRetry, webhookPerAttemptTimeout),
	}, nil
}

// EnqueueWebhookDeliver (lines 62-71) passes c.webhookUniqueTTL into the
// updated NewWebhookDeliverTask(jobID, c.webhookUniqueTTL) call, mirroring
// EnqueueImageConvert's existing c.imageUniqueTTL pass-through (line 51).
func (c *Client) EnqueueWebhookDeliver(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewWebhookDeliverTask(jobID) // ADD uniqueTTL arg here
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue webhook deliver %s: %w", jobID, err)
	}
	return nil
}
```

**Error handling pattern** (unchanged, already established, line 67-69): wrap with `fmt.Errorf("enqueue webhook deliver %s: %w", jobID, err)` — do NOT special-case `asynq.ErrDuplicateTask` here; the caller (reconciler `sweep()`) is the layer that branches on it via `errors.Is`.

---

### `internal/queue/queue_test.go` (test, request-response)

**Analog:** same file — `TestImageUniqueTTL` (lines 117-154), `TestEnqueueImageConvert` (lines 156-198)

**Core pattern to copy for `TestWebhookUniqueTTL`** (mirror `TestImageUniqueTTL`'s structure: exact-value assertion + worst-case-lifetime-exceeded assertion + monotonicity assertions):
```go
func TestImageUniqueTTL(t *testing.T) {
	maxRetry := 4
	engineTimeout := 120 * time.Second
	backoffSum := 2*time.Second + 5*time.Second + 15*time.Second + 15*time.Second

	want := time.Duration(maxRetry+1)*engineTimeout + backoffSum + uniqueTTLSafetyMargin
	got := ImageUniqueTTL(maxRetry, engineTimeout)
	if got != want {
		t.Errorf("ImageUniqueTTL(%d, %v) = %v, want %v", maxRetry, engineTimeout, got, want)
	}

	worstCaseRetryLifetime := time.Duration(maxRetry+1)*engineTimeout + backoffSum
	if got <= worstCaseRetryLifetime {
		t.Errorf("... want strictly greater than worst-case retry lifetime %v", worstCaseRetryLifetime)
	}
	// ... monotonicity checks (lines 147-153)
}
```
For `TestWebhookUniqueTTL`, compute the expected `backoffSum` using the jitter-inflated values (`30s*1.25=37.5s`, `60s*1.25=75s`, `120s*1.25=150s`, `240s*1.25=300s`, `480s*1.25=600s`, `900s*1.25=1125s` — matching RESEARCH.md's worked example of `~41m17.5s` total for `maxRetry=6`), NOT by calling `WebhookRetryDelay` in the test either (same non-determinism trap applies to the test as to the implementation).

**Live-Redis integration test pattern to copy for `TestEnqueueWebhookDeliverDuplicate`** (mirror `TestEnqueueImageConvert`, lines 156-198 — skip guard, `NewClient`, `asynq.NewInspector` cleanup idiom):
```go
func TestEnqueueImageConvert(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("REDIS_ADDR not set; skipping integration test")
	}
	cl, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cl.Close()

	id := uuid.New()
	if err := cl.EnqueueImageConvert(context.Background(), id); err != nil {
		t.Fatalf("EnqueueImageConvert: %v", err)
	}

	opt, _ := RedisOpt()
	insp := asynq.NewInspector(opt)
	defer insp.Close()
	// ... ListPendingTasks(QueueImage) + DeleteTask cleanup (lines 178-197)
}
```
For the duplicate test, call `EnqueueWebhookDeliver` twice with the same `id` and assert the second call returns `errors.Is(err, asynq.ErrDuplicateTask)` — full skeleton already given verbatim in `06-RESEARCH.md` lines 416-439 (`TestEnqueueWebhookDeliverDuplicate`); copy that directly, including its cleanup-via-Inspector comment.

---

### `internal/jobs/repo.go` (model/repository, CRUD + event-driven)

**Analog (finder):** same file — `StaleJob` / `FindStale` (lines 25-31, 164-195)

**Imports pattern** (lines 1-13, unchanged — `context`, `encoding/json`, `errors`, `fmt`, `time`, `uuid`, `pgx`, `pgxpool` already cover everything needed):
```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)
```

**Core CRUD (finder) pattern to copy** (lines 164-195 — `FindStale`'s Go-computed-cutoff + `pool.Query`/`rows.Next`/`rows.Scan`/`rows.Err` idiom):
```go
func (r *Repo) FindStale(ctx context.Context, queuedStaleAfter, activeStaleAfter time.Duration) ([]StaleJob, error) {
	now := time.Now()
	queuedCutoff := now.Add(-queuedStaleAfter)
	activeCutoff := now.Add(-activeStaleAfter)

	rows, err := r.pool.Query(ctx, `
		SELECT id, status FROM jobs
		WHERE (status = 'queued' AND created_at < $1)
		   OR (status = 'active' AND started_at < $2)`,
		queuedCutoff, activeCutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query stale jobs: %w", err)
	}
	defer rows.Close()

	var out []StaleJob
	for rows.Next() {
		var j StaleJob
		if err := rows.Scan(&j.ID, &j.Status); err != nil {
			return nil, fmt.Errorf("scan stale job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
```
`FindWebhookGaps` mirrors this shape exactly but with a single Go-computed cutoff (`activeStaleAfter` reused per D-04, bound against `finished_at` not `created_at`/`started_at`) and a `NOT EXISTS` anti-join instead of an `OR` on two status branches. Full implementation already produced in `06-RESEARCH.md` lines 236-278 (`WebhookGapJob` type + `FindWebhookGaps` method) — copy that block directly; it already follows this file's exact error-wrapping (`fmt.Errorf("query webhook gaps: %w", err)`) and doc-comment conventions.

**Analog (recorder) — deliberate deviation from `transition`:** `transition`'s event-insert half (lines 333-337) and the `detailActionRecovery` const pattern (lines 18-23).

**Why `RecordWebhookGapRecovered` does NOT call `r.transition`:** `transition` (lines 298-341) is built around actual `jobs.status` *changes* — it takes a `to` status, an allow-list of valid `from` statuses, and an `apply` closure that mutates the row, all under `SELECT ... FOR UPDATE`. The gap-recovery event does not change `jobs.status` (the job is already `done`/`failed` and stays that way — D-06 explicitly says "to_status unchanged"), so forcing it through `transition` would mean inventing a fake self-transition (`from == to`) that adds row-locking overhead with zero correctness benefit. This is NOT the anti-pattern "bypassing the guarded transition helper" (which is specifically about ad-hoc UPDATEs to `jobs.status` outside `transition`) — no status is being updated here at all. See RESEARCH.md Pattern 3 for the full reasoning and correctness argument (the actual duplicate-guard is the `asynq.Unique` lock, checked enqueue-first, before this method is ever called).

**Pattern to copy** (const pattern from lines 18-23, insert pattern adapted from lines 333-337):
```go
// EXISTING const pattern to mirror (lines 18-23):
const detailActionRecovery = "reconciler_recovery"

// NEW const, same convention:
const detailActionWebhookGapRecovered = "webhook_gap_recovered"

// RecordWebhookGapRecovered — plain insert, NOT wrapped in transition():
func (r *Repo) RecordWebhookGapRecovered(ctx context.Context, id uuid.UUID, status string) error {
	detail := map[string]any{"action": detailActionWebhookGapRecovered}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal webhook gap detail: %w", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO job_events (job_id, from_status, to_status, detail) VALUES ($1, $2, $2, $3)`,
		id, status, detailJSON,
	); err != nil {
		return fmt.Errorf("record webhook gap recovery for job %s: %w", id, err)
	}
	return nil
}
```
Error handling pattern matches every other method in this file: `fmt.Errorf("<action> <id/key>: %w", err)`.

---

### `internal/jobs/repo_test.go` (test, request-response)

**Analog:** same file — `TestFindStale` (lines 354-427), `newTestRepo`/`createTestClient` helpers (lines 14-43)

**Live-DB setup pattern to copy verbatim** (lines 14-43 — already the exact convention `TestFindWebhookGaps` must reuse, no changes needed):
```go
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
```

**Core test-data-setup pattern to copy** (lines 354-427 — `TestFindStale`'s "create + backdate via direct SQL `UPDATE ... interval` + call finder + assert map membership" shape):
```go
func TestFindStale(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	clientID := createTestClient(t, r)

	oldQueued, err := r.Create(ctx, CreateParams{ /* ... */ })
	// ...
	if _, err := r.pool.Exec(ctx, `UPDATE jobs SET created_at = now() - interval '1 hour' WHERE id = $1`, oldQueued); err != nil {
		t.Fatalf("backdate oldQueued created_at: %v", err)
	}
	// ... repeat for fresh/negative cases ...

	stale, err := r.FindStale(ctx, 90*time.Second, 5*time.Minute)
	// ... assert map[uuid.UUID]string membership for each case
}
```
`TestFindWebhookGaps` mirrors this exactly: create a `done`/`failed` job with `callback_url` set and no `webhook_deliveries` row, backdate `finished_at` via direct SQL (`UPDATE jobs SET finished_at = now() - interval '1 hour' WHERE id = $1`), and assert it appears; create a second job with a `webhook_deliveries` row inserted directly via SQL and assert it does NOT appear (D-05 case); create a third fresh (`finished_at` within threshold) job and assert it does NOT appear (D-04 case).

---

### `internal/reconciler/reconciler.go` (service, event-driven)

**Analog:** same file — `sweep()`'s existing image-recovery loop (lines 81-184), `jobStore` interface (lines 28-37)

**Imports pattern** (lines 1-16, unchanged):
```go
package reconciler

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/metrics"
)
```

**Interface extension pattern to copy** (lines 28-37 — `jobStore` gains two new methods, same interface-segregation discipline noted in the file's own doc comment):
```go
type jobStore interface {
	FindStale(ctx context.Context, queuedStaleAfter, activeStaleAfter time.Duration) ([]jobs.StaleJob, error)
	RecoveryCount(ctx context.Context, id uuid.UUID) (int, error)
	RequeueStale(ctx context.Context, id uuid.UUID, reason string) error
	MarkFailed(ctx context.Context, id uuid.UUID, code, message string, detail map[string]any) error
	Get(ctx context.Context, id uuid.UUID) (*jobs.Job, error)
	// ADD:
	// FindWebhookGaps(ctx context.Context, activeStaleAfter time.Duration) ([]jobs.WebhookGapJob, error)
	// RecordWebhookGapRecovered(ctx context.Context, id uuid.UUID, status string) error
}
```

**Core enqueue-first + `asynq.ErrDuplicateTask`-guard pattern to copy** (lines 109-126 — the exact idiom D-03 mirrors for webhook gaps):
```go
if err := s.enq.EnqueueImageConvert(ctx, j.ID); err != nil {
	if errors.Is(err, asynq.ErrDuplicateTask) {
		continue
	}
	continue // transient enqueue error: best-effort, retry next tick
}
// only after a successful, non-duplicate enqueue: record + metrics
```

**Full new second-scan block to add inside `sweep()`** (append after the existing `for _, j := range stale { ... }` loop, same best-effort-on-finder-error discipline as line 82-86):
```go
gaps, err := s.store.FindWebhookGaps(ctx, s.cfg.ActiveStaleAfter)
if err == nil {
	for _, g := range gaps {
		if err := s.enq.EnqueueWebhookDeliver(ctx, g.ID); err != nil {
			if errors.Is(err, asynq.ErrDuplicateTask) {
				continue
			}
			continue
		}
		_ = s.store.RecordWebhookGapRecovered(ctx, g.ID, g.Status)
		metrics.RecordReconcilerAction("webhook_gap_recovered")
	}
}
```
(Verbatim from `06-RESEARCH.md` Pattern 4, lines 319-339 — copy directly.)

**Metrics/observability pattern already established** (line 102, 183): `metrics.RecordReconcilerAction("<action>")` — add `"webhook_gap_recovered"` as a new label value, no signature change to `RecordReconcilerAction` needed.

**Error handling pattern:** every store/enqueuer call in `sweep()` is best-effort (`continue`/discard on error, next tick retries) — no `panic`, no propagated error from `sweep()` itself (it has no return value). Follow this exactly for the new loop; do not introduce a different error-handling style for the webhook-gap branch.

---

### `internal/reconciler/reconciler_test.go` (test, event-driven)

**Analog:** same file — `fakeStore`/`fakeEnqueuer` (lines 15-80), `TestSweepSkipsDuplicateEnqueue` (lines 113-136), `TestSweepExhaustsAtCap` (lines 190-211)

**Fake extension pattern to copy:**
```go
// fakeEnqueuer already has webhookCalls tracking (lines 66-80) — reuse as-is:
type fakeEnqueuer struct {
	enqueueImageErr error
	imageCalls      []uuid.UUID
	webhookCalls    []uuid.UUID
}
func (f *fakeEnqueuer) EnqueueWebhookDeliver(ctx context.Context, id uuid.UUID) error {
	f.webhookCalls = append(f.webhookCalls, id)
	return nil
}
// ADD to fakeEnqueuer: enqueueWebhookErr error (mirrors enqueueImageErr) so
// new tests can inject asynq.ErrDuplicateTask for the webhook-gap path.

// fakeStore needs two ADDED methods:
//   FindWebhookGaps(ctx, activeStaleAfter) ([]jobs.WebhookGapJob, error)
//   RecordWebhookGapRecovered(ctx, id, status) error
// mirroring the existing FindStale/RequeueStale fields+methods shape (lines
// 18-51): add `webhookGaps []jobs.WebhookGapJob`, `findWebhookGapsErr error`,
// `webhookGapRecoveredCalls []uuid.UUID` fields.
```

**Test pattern to copy** (mirror `TestSweepSkipsDuplicateEnqueue`, lines 113-136 — construct fake with one stale/gap job, inject `asynq.ErrDuplicateTask`, assert no recovery event recorded):
```go
func TestSweepSkipsDuplicateEnqueue(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusQueued}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{enqueueImageErr: asynq.ErrDuplicateTask}
	s := NewSweeper(store, enq, testConfig())
	s.sweep(context.Background())
	// assert enq.imageCalls == 1, store.requeueStaleCalls == 0, recoveryCount unchanged
}
```
New tests to add (2-3, per RESEARCH.md's project structure list): `TestSweepRecoversWebhookGap` (successful enqueue -> `RecordWebhookGapRecovered` called + `metrics.RecordReconcilerAction("webhook_gap_recovered")` fires — note: metrics assertions aren't done elsewhere in this file since `metrics` has no test hook; follow existing precedent of NOT asserting metrics calls directly, only store/enqueuer side effects), `TestSweepSkipsDuplicateWebhookGap` (mirrors `TestSweepSkipsDuplicateEnqueue` but for `EnqueueWebhookDeliver`+`asynq.ErrDuplicateTask`), and optionally `TestSweepWebhookGapFindErrorBestEffort` (mirrors the implicit `findStaleErr` best-effort handling, lines 32-37).

---

### `internal/reconciler/reconciler_soak_test.go` (NEW FILE, test, event-driven / batch)

**Analog:** composed from `internal/jobs/repo_test.go`'s `newTestRepo`/`createTestClient` (lines 14-43) + same-package `fakeEnqueuer` (`reconciler_test.go` lines 65-80) + `reconciler.go`'s `Run`/`NewSweeper` (lines 45-72)

**Why this is a NEW file, not an extension of `reconciler_test.go` (D-07):** the existing `reconciler_test.go` tests are pure in-memory-fake unit tests calling `s.sweep(ctx)` directly (synchronous, no real ticker). The soak test requires a REAL `Sweeper.Run` goroutine ticking on a REAL `time.Duration` interval against REAL elapsed Postgres time (`jobs.Repo`, live DB) — a fundamentally different test style (real clock, real DB, background goroutine + polling) that belongs in its own file per CONTEXT.md's explicit framing ("D-07's soak test is a NEW, separate test file/style").

**Critical design constraint (do NOT deviate):** use a REAL `jobs.Repo` (live Postgres) paired with the EXISTING in-memory `fakeEnqueuer` (no live Redis, no real `queue.Client`) — never a real `queue.Client`. Reason: `ImageUniqueTTL`'s `uniqueTTLSafetyMargin` is a hardcoded 2-minute floor (line 180 of `queue.go`), so a real-Redis `asynq.Unique` lock would make every recovery cycle take at least 2 minutes to naturally expire — incompatible with the "well under a minute" test budget (RESEARCH.md Pitfall 3). The soak test's job is to prove `Sweeper.Run`'s real ticker against real elapsed Postgres time, not to re-prove asynq's own locking semantics (already covered by `TestEnqueueImageConvert`-style tests).

**Full skeleton to copy** (already produced verbatim in `06-RESEARCH.md` lines 447-489, `TestSoakRecoversStrandedQueuedJob`):
```go
func TestSoakRecoversStrandedQueuedJob(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping soak test")
	}
	pool := newSoakTestPool(t) // local helper mirroring newTestRepo, package reconciler
	repo := jobs.NewRepo(pool)
	clientID := createSoakTestClient(t, pool)

	jobID, err := repo.Create(context.Background(), jobs.CreateParams{
		ClientID: clientID, Operation: "convert", Engine: "image",
		SourceFormat: "png", TargetFormat: "webp",
		Input: jobs.Input{ObjectKey: "uploads/soak/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	enq := &fakeEnqueuer{} // reused from reconciler_test.go, no live Redis
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
		if j.Status == jobs.StatusQueued && len(enq.imageCalls) >= 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job was not recovered within 10s of real elapsed time")
}
```
Local helpers `newSoakTestPool`/`createSoakTestClient` should mirror `internal/jobs/repo_test.go`'s `newTestRepo`/`createTestClient` (same `t.Skip` guard on `DATABASE_URL`, same `db.Connect`/`db.Migrate`/`t.Cleanup(pool.Close)` sequence) since this new file lives in package `reconciler`, not `jobs`, and cannot import the unexported test helpers directly.

**Pitfalls to avoid (from RESEARCH.md, load-bearing for correctness):**
- Pitfall 3: never wire a real `queue.Client`/live Redis into this test — see constraint above.
- Pitfall 4: `RequeueStale` never resets `created_at`; a job recovered via the `active` branch once will likely trip the `queued`-branch staleness check on the NEXT tick (since `created_at` is already older than `QueuedStaleAfter` by then). Assert on cumulative `RecoveryCount`/final terminal status, not on a specific sequence of `reason` values, when writing the D-08 exhaustion-path test.
- Pitfall 5: use generous polling windows (10-15s wall-clock budget), not tight exact-boundary sleeps, to absorb any Go-process/Postgres-server clock skew.

For the D-08 exhaustion-path test (`MaxRecoveries` exceeded -> `MarkFailed` + `job_events`), reuse the same real-Repo + `fakeEnqueuer` pairing, set `MaxRecoveries` to a small number (e.g. 2) and poll until `repo.Get(...).Status == jobs.StatusFailed`, then assert a `job_events` row exists (direct SQL query against `r.pool`/`repo`'s exported methods, following `TestMarkFailed`'s detail-round-trip-via-`job_events`-query pattern in `internal/jobs/repo_test.go` lines 147-160 for the query style, even though this is in a different package — the SQL idiom, not the exact code, is what to mirror since `pool` is unexported).

---

### `internal/db/migrations/0004_webhook_deliveries_job_idx.sql` (NEW FILE, migration)

**Analog:** `internal/db/migrations/0003_webhook_dead_letter.sql` (full file, 12 lines)

**Full pattern to copy (comment style + single `CREATE INDEX` statement):**
```sql
-- Add dead-letter tracking to webhook_deliveries (D-10).
--
-- Set true on the row for the final delivery attempt once asynq exhausts
-- MaxRetry (~30 min backoff window, see internal/queue/queue.go). Operators
-- investigate dead-lettered rows via direct SQL in v1 — no CLI/API tooling
-- yet (see WEBHOOK-V2-02 in REQUIREMENTS.md for the planned v2 replay tool).
ALTER TABLE webhook_deliveries
    ADD COLUMN dead_letter boolean NOT NULL DEFAULT false;

CREATE INDEX webhook_deliveries_dead_letter_idx
    ON webhook_deliveries (job_id) WHERE dead_letter = true;
```
The new migration needs a plain (non-partial) index since `FindWebhookGaps`'s `NOT EXISTS` subquery must prove "no row at all," which the existing partial `webhook_deliveries_pending_idx` (`WHERE delivered = false`) cannot serve:
```sql
-- Add a supporting index for the reconciler's webhook-gap sweep (RECON-04).
--
-- FindWebhookGaps' NOT EXISTS subquery must prove "no row exists for this
-- job_id at all" (delivered, undelivered, AND dead-lettered rows all count
-- as "not a gap", per D-05) — the existing webhook_deliveries_pending_idx
-- is a PARTIAL index (WHERE delivered = false) and cannot serve a query
-- that must also see delivered=true/dead-lettered rows.
CREATE INDEX webhook_deliveries_job_id_idx
    ON webhook_deliveries (job_id);
```
Follow `0001_init.sql`/`0003_webhook_dead_letter.sql`'s naming convention: `NNNN_snake_case_description.sql`, next unused number is `0004`.

---

## Shared Patterns

### Enqueue-first + `asynq.ErrDuplicateTask` guard
**Source:** `internal/reconciler/reconciler.go` lines 109-126 (existing image-recovery loop)
**Apply to:** `internal/reconciler/reconciler.go`'s new webhook-gap loop (D-03) — attempt the enqueue BEFORE writing any recovery event; treat `asynq.ErrDuplicateTask` as "not a gap, skip silently," and any other enqueue error as "transient, best-effort, retry next tick."
```go
if err := s.enq.EnqueueImageConvert(ctx, j.ID); err != nil {
	if errors.Is(err, asynq.ErrDuplicateTask) {
		continue
	}
	continue
}
```

### Derived (not hardcoded) unique-lock TTL
**Source:** `internal/queue/queue.go` lines 193-228 (`ImageUniqueTTL`)
**Apply to:** `WebhookUniqueTTL` (D-02) — same doc-comment convention (worst-case formula, worked numeric example, "SOUNDNESS DEPENDENCY"/"SOUNDNESS CAVEAT" section documenting what assumption the formula depends on), same one-line return-expression shape.

### Dual observability: `job_events` row + `metrics.RecordReconcilerAction`
**Source:** `internal/reconciler/reconciler.go` lines 101-102 (exhaustion path) and line 183 (recovery path); `internal/metrics/metrics.go` lines 52-56
**Apply to:** the new webhook-gap-recovered path — write a `job_events` row via `RecordWebhookGapRecovered` (Postgres) AND call `metrics.RecordReconcilerAction("webhook_gap_recovered")` (Prometheus), both on the same successful-enqueue path, exactly mirroring how `"recovered"` and `"exhausted"` are each paired with their own `job_events` write.
```go
metrics.RecordReconcilerAction("recovered") // existing pattern (line 183)
// mirror as:
// _ = s.store.RecordWebhookGapRecovered(ctx, g.ID, g.Status)
// metrics.RecordReconcilerAction("webhook_gap_recovered")
```

### Live-DB / live-Redis test skip guards
**Source:** `internal/jobs/repo_test.go` lines 14-29 (`newTestRepo`, `DATABASE_URL` skip); `internal/queue/queue_test.go` lines 158-161 (`REDIS_ADDR` skip)
**Apply to:** every new/modified test file in this phase (`queue_test.go`'s new tests, `repo_test.go`'s `TestFindWebhookGaps`, `reconciler_soak_test.go`) — always `t.Skip("<VAR> not set; skipping integration test")` at the top of any test requiring a live dependency, never fail hard when the env var is absent.

### Error wrapping convention
**Source:** `internal/jobs/repo.go` (every method), `internal/queue/client.go` lines 56/68
**Apply to:** `FindWebhookGaps`/`RecordWebhookGapRecovered` — `fmt.Errorf("<action> <context>: %w", err)`, e.g. `fmt.Errorf("query webhook gaps: %w", err)`, `fmt.Errorf("record webhook gap recovery for job %s: %w", id, err)`.

## No Analog Found

None. Every file in this phase's scope has at least a role-match analog in the existing codebase (RESEARCH.md independently confirms this: "every piece of this phase already has a direct precedent somewhere in the existing reconciler/webhook code").

## Metadata

**Analog search scope:** `internal/queue/`, `internal/jobs/`, `internal/reconciler/`, `internal/metrics/`, `internal/db/migrations/` (entire directories read directly; no Glob/Grep-based broad search was necessary since RESEARCH.md had already identified exact target files and this phase introduces zero new packages/directories)
**Files scanned:** `internal/queue/queue.go`, `internal/queue/client.go`, `internal/queue/queue_test.go`, `internal/jobs/repo.go`, `internal/jobs/jobs.go`, `internal/jobs/repo_test.go`, `internal/reconciler/reconciler.go`, `internal/reconciler/reconciler_test.go`, `internal/metrics/metrics.go`, `internal/db/migrations/0001_init.sql`, `internal/db/migrations/0003_webhook_dead_letter.sql` (11 files read in full)
**Pattern extraction date:** 2026-07-08
