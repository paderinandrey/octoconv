---
phase: 10-document-worker-reconciler-integration
plan: 03
subsystem: infra
tags: [asynq, worker, libreoffice, document-conversion, queue-routing]

# Dependency graph
requires:
  - phase: 10-document-worker-reconciler-integration
    provides: "queue.TypeDocumentConvert / queue.QueueDocument / queue.RetryDelayFunc document routing (Plan 01)"
provides:
  - "HandleDocumentConvert asynq task handler in internal/worker/worker.go, reusing the shared engine-agnostic process() pipeline"
  - "isDocumentTerminal engine-scoped classifier: a DOCUMENT_ENGINE_TIMEOUT expiry (wrapped context.DeadlineExceeded) is TERMINAL for documents, unlike the image engine's isTerminal which keeps it transient"
  - "terminalLibreOfficeSignatures folded into the shared isTerminal (empty output, missing %PDF- magic bytes, unsupported source ext)"
  - "cmd/document-worker binary: standalone process consuming only the document queue with its own DOCUMENT_ENGINE_TIMEOUT/DOCUMENT_WORKER_CONCURRENCY, no reconciler sweeper, produces (never consumes) webhook-delivery tasks"
affects: [11-document-api-routing, docker-compose-document-worker-service]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Engine-scoped terminal classifier delegating to a shared base classifier (isDocumentTerminal -> isTerminal) rather than forking the whole classification function"
    - "Structural-copy task handler (HandleDocumentConvert mirrors HandleImageConvert) differing only in classifier call and metrics queue label"

key-files:
  created:
    - cmd/document-worker/main.go
  modified:
    - internal/worker/worker.go
    - internal/worker/worker_test.go

key-decisions:
  - "DOC-08's timeout=terminal divergence is implemented as a NEW engine-scoped isDocumentTerminal function that delegates to the existing shared isTerminal for all non-timeout classification, rather than adding a timeout branch to isTerminal itself — keeps the image engine's timeout-is-transient behavior completely unchanged and test-verified (TestIsTerminalTimeoutUnchanged)."
  - "cmd/document-worker registers ONLY queue.TypeDocumentConvert on its asynq mux; it never registers TypeWebhookDeliver, matching D-06 — cmd/worker remains the sole webhook-queue consumer even though document-worker's HandleDocumentConvert produces webhook-delivery tasks via h.enqueuer.EnqueueWebhookDeliver."
  - "No reconciler.Sweeper is wired into cmd/document-worker (D-05) — confirmed via grep -c 'reconciler' returning 0 after rewording an explanatory comment that had accidentally referenced the package name."
  - "WEBHOOK_SIGNING_SECRET is read into worker.NewHandler's required signingSecret param but is NOT log.Fatalf'd on empty in cmd/document-worker, since this binary never signs or delivers webhooks."

requirements-completed: [DOC-07, DOC-08]

# Metrics
duration: 25min
completed: 2026-07-09
---

# Phase 10 Plan 03: Document Worker Handler & Binary Summary

**HandleDocumentConvert task handler plus a standalone cmd/document-worker binary that consumes only the document asynq queue, classifying a DOCUMENT_ENGINE_TIMEOUT expiry as terminal via an engine-scoped isDocumentTerminal while leaving the image engine's timeout-as-transient isTerminal untouched.**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-09T19:00:00Z (approx)
- **Completed:** 2026-07-09T19:25:54Z
- **Tasks:** 2 completed
- **Files modified:** 3 (2 modified, 1 created)

## Accomplishments
- Added `terminalLibreOfficeSignatures` to the shared `isTerminal` classifier (LibreOffice's deterministic-bad-output signatures: empty output, missing `%PDF-` magic bytes, unsupported source extension), reused by both engine handlers.
- Added the engine-scoped `isDocumentTerminal` classifier: treats a wrapped `context.DeadlineExceeded` (a `DOCUMENT_ENGINE_TIMEOUT` expiry, surfaced through `exec.go` -> `libreoffice.go` -> `process()`'s `%w` wrapping chain) as TERMINAL — the deliberate DOC-08 divergence from the image engine, which keeps retrying timeouts.
- Added `HandleDocumentConvert`, a structural copy of `HandleImageConvert` that reuses the already engine-agnostic `process()` method unchanged, branches on `isDocumentTerminal` instead of `isTerminal`, and tags both `metrics.RecordJobOutcome` calls with `queue.QueueDocument`.
- Created `cmd/document-worker/main.go`: a standalone binary mirroring `cmd/worker/main.go`'s wiring, but registering only `queue.TypeDocumentConvert`, using `DOCUMENT_ENGINE_TIMEOUT`/`DOCUMENT_WORKER_CONCURRENCY` env vars (defaults 300s/2), scoping the asynq queue config and Prometheus queue-depth collector to `queue.QueueDocument` only, and omitting the reconciler sweeper entirely.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add HandleDocumentConvert with engine-scoped timeout=terminal classification and LibreOffice terminal signatures** - `411da5a` (feat)
2. **Task 2: Create the cmd/document-worker binary consuming only the document queue** - `23ae8af` (feat)

_Note: both commits are on worktree branch `worktree-agent-a5eca08da07b19dbc`; the orchestrator merges this worktree back into the integration branch._

## Files Created/Modified
- `internal/worker/worker.go` - Added `terminalLibreOfficeSignatures`, `isDocumentTerminal`, and `HandleDocumentConvert`
- `internal/worker/worker_test.go` - Added `TestIsTerminalLibreOfficeSignatures`, `TestIsTerminalTimeoutUnchanged`, `TestIsDocumentTerminal`
- `cmd/document-worker/main.go` - New standalone document-engine worker binary

## Decisions Made
- Implemented DOC-08's timeout divergence as a separate `isDocumentTerminal` function delegating to `isTerminal`, rather than modifying `isTerminal` itself — this is the only way to keep the image path's timeout-as-transient behavior provably unchanged (verified by a new regression test, `TestIsTerminalTimeoutUnchanged`, alongside the pre-existing `TestIsTerminalTransientDefault`).
- `cmd/document-worker` reads but does not fatal on a missing `WEBHOOK_SIGNING_SECRET`, since it never signs or delivers webhooks (D-06); the value is passed through to `worker.NewHandler` purely to satisfy the shared constructor signature.
- Reworded an in-code comment from "reconciler sweep... reconciler.Sweeper" to "stale-job sweep loop... no sweeper of any kind" so the plan's mechanical `grep -c 'reconciler' cmd/document-worker/main.go` acceptance check (expected 0, verifying D-05's no-sweeper invariant) passes cleanly without weakening the explanatory intent.

## Deviations from Plan

None - plan executed exactly as written. One incidental fix: after running `go build ./cmd/document-worker` without `-o`, a stray `document-worker` binary was written to the repo root by the Go toolchain (default build output location); it was removed before staging/committing (never tracked, not part of any commit).

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. (Docker/compose wiring for `cmd/document-worker` — a new `Dockerfile.document-worker` and `docker-compose.yml` service entry — is out of this plan's file scope per its frontmatter `files_modified` list; it belongs to a later plan/wave in this phase.)

## Next Phase Readiness
- `cmd/document-worker` builds and is ready to be wired into `Dockerfile.document-worker` + `docker-compose.yml` (separate plan/task per the phase's pattern map).
- `HandleDocumentConvert` is available for the reconciler (Plan covering D-04/D-05 engine-aware routing) to enqueue against once `EnqueueDocumentConvert` exists on the queue client (delivered by Plan 01, already read as an interface dependency here).
- No blockers for Phase 11's `handleCreateJob` document-queue routing work.

## Self-Check: PASSED

- FOUND: cmd/document-worker/main.go
- FOUND: internal/worker/worker.go
- FOUND: internal/worker/worker_test.go
- FOUND commit: 411da5a
- FOUND commit: 23ae8af

---
*Phase: 10-document-worker-reconciler-integration*
*Completed: 2026-07-09*
