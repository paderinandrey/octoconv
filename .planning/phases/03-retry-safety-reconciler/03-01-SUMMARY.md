---
phase: 03-retry-safety-reconciler
plan: 01
subsystem: queue
tags: [asynq, redis, retry, backoff, idempotency, go]

# Dependency graph
requires:
  - phase: 02-webhook-delivery
    provides: webhookRetrySchedule / WebhookRetryDelay pattern (30s->15m, MaxRetry=6) mirrored here for the image queue
provides:
  - "queue-aware asynq.Config.RetryDelayFunc dispatcher (queue.RetryDelayFunc) routing image tasks to a fast 2s/5s/15s schedule and webhook tasks to their existing schedule"
  - "configurable per-task MaxRetry for image tasks (IMAGE_MAX_RETRY, default 4) stored on queue.Client"
  - "per-job asynq.Unique lock on image tasks with a TTL derived from IMAGE_MAX_RETRY + ENGINE_TIMEOUT (queue.ImageUniqueTTL), enabling the Plan 03 reconciler's duplicate-enqueue safety guarantee"
affects: [03-02-error-classification-and-timeout, 03-03-reconciler]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Queue-aware RetryDelayFunc dispatch by asynq.Task.Type() instead of a single server-wide RetryDelayFunc"
    - "Derived (not hardcoded) TTL: ImageUniqueTTL computes the uniqueness-lock TTL from the actual retry budget so it can never silently drift under asynq's worst-case retry lifetime if env vars change"

key-files:
  created: []
  modified:
    - internal/queue/queue.go
    - internal/queue/client.go
    - internal/queue/queue_test.go
    - cmd/worker/main.go
    - .env.example

key-decisions:
  - "asynq's archive condition (msg.Retried >= msg.Retry, checked AFTER each failed attempt) means MaxRetry=N yields N+1 total engine executions before archival, not N — ImageUniqueTTL uses (maxRetry+1) to avoid undercounting the worst-case lock lifetime"
  - "Image retry schedule (2s/5s/15s) has no jitter, unlike webhook's ±25% jitter band, since image tasks aren't competing to avoid a thundering herd on a shared external endpoint the way webhook deliveries are"
  - "Webhook tasks are NOT given an asynq.Unique lock — only image tasks, per the plan's threat model (T-03-03)"

requirements-completed: [RELY-02, RECON-01]

# Metrics
duration: ~20min
completed: 2026-07-06
---

# Phase 3 Plan 1: Image Retry Schedule, Dispatcher, and Per-Job Uniqueness Lock Summary

**Image conversion tasks now retry on their own fast 2s/5s/15s schedule with a bounded MaxRetry (default 4) via a queue-aware RetryDelayFunc dispatcher, and carry a per-job asynq.Unique lock whose TTL is derived from IMAGE_MAX_RETRY + ENGINE_TIMEOUT so duplicate enqueues collide safely instead of double-processing.**

## Performance

- **Duration:** ~20 min
- **Tasks:** 2 completed
- **Files modified:** 5

## Accomplishments

- Replaced the confirmed defect where the server-wide `asynq.Config.RetryDelayFunc` (`queue.WebhookRetryDelay`) was silently applied to image tasks too, and image tasks carried no `MaxRetry` (defaulting to asynq's 25 attempts)
- Added a queue-aware `RetryDelayFunc` dispatcher that routes by `asynq.Task.Type()`: image tasks get a fast, non-jittered 2s/5s/15s schedule (D-06), webhook tasks keep their existing 30s->15m jittered schedule, and any future task type falls back to asynq's own default
- Added `ImageUniqueTTL(maxRetry, engineTimeout)` deriving the per-job uniqueness-lock TTL from the actual retry budget, correcting a prior off-by-one in worst-case-lifetime reasoning (asynq's archive check happens AFTER each failed attempt, so `MaxRetry=N` yields `N+1` total attempts, not `N`)
- `NewImageConvertTask` now applies both `asynq.MaxRetry(maxRetry)` and `asynq.Unique(uniqueTTL)`, giving image tasks a per-job lock keyed on queue+type+job-id — a duplicate enqueue for a job whose task/lock is still live now returns `asynq.ErrDuplicateTask` instead of creating a second concurrent task
- `queue.Client` reads `IMAGE_MAX_RETRY` (default 4) and `ENGINE_TIMEOUT` (default 120s, same env var the worker reads) once at construction to derive and store `imageUniqueTTL`
- `EnqueueImageConvert`'s public signature is unchanged, so `internal/api`'s `Enqueuer` interface and `fakeQueue` in `handlers_test.go` keep compiling untouched
- Wired `cmd/worker/main.go`'s `asynq.Config.RetryDelayFunc` to the new dispatcher and documented `IMAGE_MAX_RETRY=4` in `.env.example`

## Task Commits

Each task was committed atomically:

1. **Task 1: Image retry schedule, queue-aware dispatcher, configurable MaxRetry, and a derived per-job uniqueness-lock TTL** - `d690ba4` (feat)
2. **Task 2: Wire the dispatcher into the worker server and document IMAGE_MAX_RETRY** - `ca9cd24` (feat)

_No TDD RED/GREEN split was used — Task 1 was `tdd="true"` in the plan but the plan's `<action>` prescribed writing tests alongside the implementation in a single pass (new pure functions with no pre-existing behavior to fail against); tests and implementation landed in one commit per the plan's own task structure._

## Files Created/Modified

- `internal/queue/queue.go` - Added `imageRetrySchedule`, `ImageRetryDelay`, `RetryDelayFunc` dispatcher, `uniqueTTLSafetyMargin`, `imageBackoffSum`, `ImageUniqueTTL`; changed `NewImageConvertTask` signature to accept `maxRetry` and `uniqueTTL`, applying `asynq.MaxRetry` + `asynq.Unique`
- `internal/queue/client.go` - Added `imageMaxRetry`/`imageUniqueTTL` fields to `Client`; `NewClient` reads `IMAGE_MAX_RETRY`/`ENGINE_TIMEOUT` and derives the TTL; added `envInt`/`envDuration`/`firstField` helpers mirroring `cmd/worker/main.go`'s convention; `EnqueueImageConvert` now passes both new args to `NewImageConvertTask` with its own signature unchanged
- `internal/queue/queue_test.go` - Updated `TestConvertPayloadRoundTrip`'s `NewImageConvertTask` call; added `TestImageRetryDelaySchedule`, `TestRetryDelayFuncDispatch`, `TestImageUniqueTTL`
- `cmd/worker/main.go` - Changed `RetryDelayFunc: queue.WebhookRetryDelay` to `RetryDelayFunc: queue.RetryDelayFunc`
- `.env.example` - Documented `IMAGE_MAX_RETRY=4` under the Worker block

## Decisions Made

- Corrected the retry-attempt-count assumption from `maxRetry` to `maxRetry+1` when deriving `ImageUniqueTTL`, per asynq's verified archive condition (`msg.Retried >= msg.Retry`, checked after each failure) — this keeps the derived TTL always strictly above asynq's true worst-case retry lifetime rather than potentially matching or falling short of it
- Kept the image retry schedule jitter-free, unlike webhook's ±25% jitter, since D-06 does not require it and image tasks don't share the webhook thundering-herd concern
- No `asynq.Unique` lock added to webhook tasks — scoped strictly to image tasks per the plan's threat model entry T-03-03

## Deviations from Plan

None - plan executed exactly as written. All acceptance criteria greps and `go build`/`go vet`/`go test` checks pass as specified in the plan's `<verification>` block.

## Known Stubs

None - no stub patterns introduced. Both files are queue infrastructure with no UI or data-fetching surface.

## Threat Flags

None - the only new surface (the `asynq.Unique` lock) is explicitly called out and mitigated in the plan's own `<threat_model>` (T-03-03); no other new network endpoints, auth paths, file access, or schema changes were introduced.

## Self-Check: PASSED
