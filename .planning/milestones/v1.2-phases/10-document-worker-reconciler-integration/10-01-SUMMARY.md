---
phase: 10-document-worker-reconciler-integration
plan: 01
subsystem: queue
tags: [asynq, redis, go, engine-class-routing, retry, ttl]

# Dependency graph
requires:
  - phase: 03-worker-retry-reconciler
    provides: image engine-class queue routing pattern (TypeImageConvert/QueueImage, ImageRetryDelay, ImageUniqueTTL) that this plan mirrors for the document engine class
provides:
  - "document asynq queue (QueueDocument) and document:convert task type (TypeDocumentConvert)"
  - "NewDocumentConvertTask task constructor reusing ConvertPayload/ParseConvertPayload"
  - "documentRetrySchedule (5s/15s/30s, no jitter) + DocumentRetryDelay, dispatched from RetryDelayFunc"
  - "documentBackoffSum + DocumentUniqueTTL, derived from DOCUMENT_MAX_RETRY/DOCUMENT_ENGINE_TIMEOUT, reusing the shared uniqueTTLSafetyMargin"
  - "queue.Client.EnqueueDocumentConvert producer method, with documentMaxRetry/documentUniqueTTL wired into NewClient (DOCUMENT_MAX_RETRY default 3, DOCUMENT_ENGINE_TIMEOUT default 300s)"
affects: [10-02-reconciler-document-routing, 10-03-document-worker-binary, 10-04-e2e-verification]

# Tech tracking
tech-stack:
  added: []
  patterns: ["Engine-class queue routing extended to a third engine (document), mirroring the image queue's exact shape (no-jitter retry schedule, derived unique-lock TTL, shared safety margin)"]

key-files:
  created: []
  modified:
    - internal/queue/queue.go
    - internal/queue/client.go
    - internal/queue/queue_test.go

key-decisions:
  - "documentRetrySchedule uses 5s/15s/30s (no jitter) — proportionally scaled up from imageRetrySchedule's 2s/5s/15s to match DOCUMENT_ENGINE_TIMEOUT being ~2.5x ENGINE_TIMEOUT (300s vs 120s)"
  - "DOCUMENT_MAX_RETRY defaults to 3 (lower than IMAGE_MAX_RETRY's 4) since each document attempt is expensive at up to 300s and DOC-08 requires bounded retries, not infinite retry"
  - "documentBackoffSum calls DocumentRetryDelay directly (safe, no jitter) rather than following webhookBackoffSum's jitter-ceiling shape — matches imageBackoffSum's precedent"

patterns-established: []

requirements-completed: [DOC-08]

# Metrics
duration: 20min
completed: 2026-07-09
---

# Phase 10 Plan 01: Document Engine-Class Queue Plumbing Summary

**Document conversion tasks now route to a dedicated `document` asynq queue with a genuinely-derived (not hardcoded) per-job unique-lock TTL and a no-jitter 5s/15s/30s retry schedule, mirroring the existing image-queue pattern exactly.**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-07-09T19:05:57Z (approx, per STATE.md)
- **Completed:** 2026-07-09T19:16:02Z
- **Tasks:** 2 completed
- **Files modified:** 3 (`internal/queue/queue.go`, `internal/queue/client.go`, `internal/queue/queue_test.go`)

## Accomplishments
- Added `TypeDocumentConvert`/`QueueDocument` constants and `NewDocumentConvertTask` (reusing the existing `ConvertPayload`/`ParseConvertPayload` — no new payload type)
- Added `documentRetrySchedule`/`DocumentRetryDelay` (no-jitter, 5s/15s/30s) and wired a `TypeDocumentConvert` arm into `RetryDelayFunc`
- Added `documentBackoffSum`/`DocumentUniqueTTL`, deriving the per-job unique-lock TTL from `DOCUMENT_MAX_RETRY`+`DOCUMENT_ENGINE_TIMEOUT` and reusing the shared `uniqueTTLSafetyMargin` (no new margin constant)
- Wired `documentMaxRetry`/`documentUniqueTTL` into `queue.Client` and added `EnqueueDocumentConvert(ctx, jobID)`, reading `DOCUMENT_MAX_RETRY` (default 3) and `DOCUMENT_ENGINE_TIMEOUT` (default 300s, Phase 9 D-01)
- Full TDD cycle: failing tests committed first (compile-error RED since the new identifiers didn't exist yet), then the implementation (GREEN)

## Task Commits

Each task was committed atomically:

1. **Task 1: Add document task type, queue, no-jitter retry schedule, and derived unique TTL to queue.go (with tests)** - RED `e0c93c2` (test), GREEN `9b94ca0` (feat)
2. **Task 2: Wire documentMaxRetry/documentUniqueTTL into queue.Client and add EnqueueDocumentConvert** - `e7a7ec6` (feat)

_Note: Task 1 is a TDD task with two commits (test → feat); no refactor commit was needed._

## TDD Gate Compliance

RED gate: `e0c93c2` — `test(10-01): add failing tests for document queue plumbing` (confirmed to fail the build with `undefined: TypeDocumentConvert` etc. before the implementation existed).
GREEN gate: `9b94ca0` — `feat(10-01): add document engine-class queue plumbing` (all Document* tests pass after this commit).
No REFACTOR commit was needed — the implementation matched the target shape on the first pass.

## Files Created/Modified
- `internal/queue/queue.go` - Added `TypeDocumentConvert`, `QueueDocument`, `NewDocumentConvertTask`, `documentRetrySchedule`, `DocumentRetryDelay`, `documentBackoffSum`, `DocumentUniqueTTL`, and a `RetryDelayFunc` dispatch arm
- `internal/queue/client.go` - Added `documentMaxRetry`/`documentUniqueTTL` fields, wired into `NewClient`, added `EnqueueDocumentConvert`
- `internal/queue/queue_test.go` - Added `TestDocumentConvertTaskRoundTrip`, `TestDocumentRetryDelaySchedule`, `TestDocumentUniqueTTL`, extended `TestRetryDelayFuncDispatch` with document-task assertions

## Decisions Made
- `documentRetrySchedule` = 5s/15s/30s (no jitter), proportionally scaled from `imageRetrySchedule` (2s/5s/15s) given `DOCUMENT_ENGINE_TIMEOUT` (300s) being ~2.5x `ENGINE_TIMEOUT` (120s)
- `DOCUMENT_MAX_RETRY` defaults to 3 (vs image's 4) — a tighter, cheaper-to-exhaust retry budget since each document attempt is expensive (up to 300s)
- Reused the shared `uniqueTTLSafetyMargin` constant verbatim for `DocumentUniqueTTL` — no document-specific margin introduced, consistent with the plan's explicit instruction and the existing `WebhookUniqueTTL` precedent

## Deviations from Plan

None - plan executed exactly as written. Both tasks' acceptance criteria (grep checks, exact `1370s` TTL value for `(3, 300s)`, monotonicity, `go build`/`go vet`/`go test` all clean) were verified before each commit.

## Issues Encountered

None blocking. `gofmt` flagged a pre-existing, out-of-scope formatting drift in `internal/queue/queue_test.go` (a double-space before an inline comment in the existing `TestWebhookRetryDelaySchedule`, untouched by this plan) — left as-is per the scope boundary rule rather than fixed, since it predates this plan's changes and is unrelated to the files/behavior this plan touches.

## User Setup Required

None - no external service configuration required. `DOCUMENT_MAX_RETRY`/`DOCUMENT_ENGINE_TIMEOUT` env vars are optional (both have sane defaults: 3 and 300s respectively) and will be documented in `.env.example` by a later plan in this phase (per the pattern map, `.env.example` changes belong to Plan 10-03/10-04's scope, not this plan's `files_modified` list).

## Note on `.planning/` Tracking

This worktree's `.planning/` directory is git-ignored (the repository's tracked `.gitignore` excludes `/.planning/`, added in commit `11bc7c3`, "internal GSD planning artifacts, not part of the product") and was not checked out by `git worktree`. This SUMMARY.md was created directly in the worktree's filesystem so the executor's sandbox would permit the write, but it is **untracked and will NOT survive worktree removal** — the orchestrator must copy this file (and any sibling phase docs) out to the main checkout's `.planning/phases/10-document-worker-reconciler-integration/` directory before discarding this worktree.

## Next Phase Readiness
- `queue.Client.EnqueueDocumentConvert` is ready for Plan 02 (reconciler) to call when routing recovery by `job.Engine`
- `queue.TypeDocumentConvert`/`queue.QueueDocument` are ready for Plan 03 (`cmd/document-worker`) to register on its asynq `ServeMux`
- No blockers identified for downstream plans in this phase

---
*Phase: 10-document-worker-reconciler-integration*
*Completed: 2026-07-09*


## Self-Check: PASSED

- Commit e0c93c2 (test): FOUND
- Commit 9b94ca0 (feat): FOUND
- Commit e7a7ec6 (feat): FOUND
- internal/queue/queue.go contains TypeDocumentConvert: FOUND
- internal/queue/client.go contains EnqueueDocumentConvert: FOUND
