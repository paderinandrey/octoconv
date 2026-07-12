---
phase: 10-document-worker-reconciler-integration
plan: 02
subsystem: reconciler
tags: [asynq, reconciler, engine-routing, postgres, fail-closed, prometheus]

requires:
  - phase: 10-document-worker-reconciler-integration (plan 01)
    provides: "*queue.Client.EnqueueDocumentConvert(ctx, jobID) — document engine-class queue producer"
provides:
  - "StaleJob.Engine field populated from jobs.engine by FindStale (no migration — column already existed)"
  - "reconciler enqueuer interface extended with EnqueueDocumentConvert"
  - "sweep() dispatches stranded-job recovery by engine: image -> EnqueueImageConvert, document -> EnqueueDocumentConvert, any other value -> fail-closed metric-visible skip (no enqueue, no RequeueStale)"
  - "metrics.RecordReconcilerAction(\"unroutable_engine\") counter label + updated Help text"
affects: [phase-11-api-routing-e2e, document-worker-cmd]

tech-stack:
  added: []
  patterns:
    - "Engine-aware recovery routing: sweep switches on StaleJob.Engine instead of hardcoding one queue, mirroring the existing engine-class queue routing pattern (internal/queue/queue.go)"
    - "Fail-closed default arm: an unrecognized/corrupted engine value is a non-fatal, metric-visible skip (continue, no enqueue, no RequeueStale) rather than a guessed misroute or a panic"

key-files:
  created: []
  modified:
    - internal/jobs/repo.go
    - internal/reconciler/reconciler.go
    - internal/reconciler/reconciler_test.go
    - internal/metrics/metrics.go

key-decisions:
  - "No DB migration needed — jobs.engine column has existed since 0001_init.sql with a CHECK constraint; FindStale simply started selecting it."
  - "Fail-closed default in the sweep's engine switch: default case records metrics.RecordReconcilerAction(\"unroutable_engine\") and continues WITHOUT calling any Enqueue*Convert or RequeueStale, so a stranded job with an out-of-scope engine (av/cad/archive/probe) or a corrupted value is never misrouted and never crashes the sweep loop — it stays stranded and is re-evaluated (unrecovered) each tick."
  - "WebhookGapJob/FindWebhookGaps and the exhaustion path (EnqueueWebhookDeliver on cap exceeded) deliberately left engine-agnostic and untouched, per the plan's explicit scope boundary."

requirements-completed: [DOC-09]

duration: 12min
completed: 2026-07-09
---

# Phase 10 Plan 02: Engine-Aware Reconciler Recovery Routing Summary

**Reconciler sweep now dispatches stranded-job recovery by `jobs.engine` (image -> image queue, document -> document queue) with a fail-closed, metric-visible skip for any other engine value, closing the launch-blocking image-only hardcode (DOC-09).**

## Performance

- **Duration:** 12 min
- **Started:** 2026-07-09T19:05:57Z (session start reference from STATE.md); this plan's active work began after reading context, ~19:13Z
- **Completed:** 2026-07-09T19:24:52Z
- **Tasks:** 2/2 completed
- **Files modified:** 4

## Accomplishments

- `StaleJob` now carries the job's engine class, sourced from the pre-existing `jobs.engine` column with zero migration
- The reconciler's under-cap recovery path routes document jobs through `EnqueueDocumentConvert` and image jobs through `EnqueueImageConvert` — no regression on the existing image path
- Any stale job whose engine is neither `image` nor `document` (a valid-but-out-of-scope CHECK value like `av`, or a corrupted value) is skipped cleanly: no enqueue onto either queue, no `RequeueStale`, no crash, and a new `unroutable_engine` reconciler metric records the event for operational visibility
- `octoconv_reconciler_actions_total` Prometheus Help text updated to document the new `unroutable_engine` label

## Task Commits

Each task was committed atomically:

1. **Task 1: Add Engine to StaleJob and select it in FindStale** - `f4bd49e` (feat)
2. **Task 2: Make the reconciler sweep route recovery by engine with a fail-closed default** (TDD):
   - RED: `27fbfde` (test) — added failing `TestSweepRoutesDocumentJobsToDocumentQueue` and `TestSweepSkipsUnknownEngine`, plus `fakeEnqueuer.EnqueueDocumentConvert`/`documentCalls` and `Engine: "image"` on existing fixtures (no behavior change)
   - GREEN: `51b0fd5` (feat) — extended `enqueuer` interface, added the `switch j.Engine` dispatch with fail-closed default, updated `metrics.go` Help text; all tests pass

_No separate REFACTOR commit was needed — the GREEN implementation was already clean._

**Plan metadata:** SUMMARY.md written directly to the main-repo absolute path per worktree isolation instructions (no separate metadata commit from this worktree — orchestrator owns STATE.md/ROADMAP.md).

## Files Created/Modified

- `internal/jobs/repo.go` - Added `Engine string` field to `StaleJob`; `FindStale` now selects/scans `engine` alongside `id`/`status`. `WebhookGapJob`/`FindWebhookGaps` untouched.
- `internal/reconciler/reconciler.go` - `enqueuer` interface gained `EnqueueDocumentConvert`; the under-cap recovery block now dispatches via `switch j.Engine` (`case "image"`, `case "document"`, fail-closed `default`) instead of hardcoding `EnqueueImageConvert`. Exhaustion path and webhook-gap scan left unchanged (engine-agnostic).
- `internal/reconciler/reconciler_test.go` - `fakeEnqueuer` gained `EnqueueDocumentConvert`/`documentCalls`/`enqueueDocumentErr`; existing `StaleJob` fixtures set `Engine: "image"`; added `TestSweepRoutesDocumentJobsToDocumentQueue` and `TestSweepSkipsUnknownEngine`; extended `TestSweepRecoversUnderCap` with an image-no-regression assertion (`documentCalls` empty).
- `internal/metrics/metrics.go` - `reconcilerActions` Help text and `RecordReconcilerAction` doc comment updated to include the `unroutable_engine` label (signature unchanged).

## Decisions Made

- No migration: `jobs.engine` has existed since `0001_init.sql` with `CHECK (engine IN ('image','document','av','cad','archive','probe'))`; this plan only started reading it in `FindStale`.
- Fail-closed default is a hard non-negotiable per the plan's threat model (T-10-03): the default arm never calls any `Enqueue*Convert` and never calls `RequeueStale`, so an unrecognized/corrupted engine value can neither be misrouted onto the wrong queue nor silently accrue/lose recovery-cap accounting. The job simply remains stranded and is re-evaluated on the next sweep tick (T-10-04, accepted residual — bounded, non-fatal, metric-visible).
- Extended the existing `TestSweepRecoversUnderCap` (rather than adding a separate `TestSweepRoutesImageJobsToImageQueue`) to assert the image no-regression behavior, since the plan explicitly allowed either approach and this avoided a near-duplicate test.

## Deviations from Plan

None - plan executed exactly as written. Task 1 and Task 2 (including the TDD RED/GREEN split) matched the plan's `<action>`/`<behavior>` specifications directly; all acceptance criteria greps and `go build`/`go test`/`go vet` checks passed with no rework needed.

## Known Stubs

None. No new stubs introduced — this plan only changed routing logic in an already-live code path (the reconciler sweep), backed by real tests exercising the new switch and fail-closed default.

## Threat Flags

None. The only new surface (the engine-routing switch reading `jobs.engine`) is explicitly covered by the plan's own `<threat_model>` (T-10-03/T-10-04) and mitigated as specified — no additional untracked surface was introduced.

## Verification

```
go build ./...                                    # PASS
go test ./internal/reconciler/ ./internal/jobs/    # PASS (all reconciler + jobs tests, soak tests skipped without DATABASE_URL as expected)
go vet ./internal/reconciler/ ./internal/jobs/     # clean
```

Acceptance-criteria greps for Task 1 and Task 2 (StaleJob.Engine, FindStale SQL/scan, enqueuer interface + switch, unroutable_engine label) all matched as specified in the plan.

## Self-Check: PASSED

- FOUND: `/Users/apaderin/dev/octoconv/.claude/worktrees/agent-ac63e40229def440f/internal/jobs/repo.go` (Engine field + FindStale change present)
- FOUND: `/Users/apaderin/dev/octoconv/.claude/worktrees/agent-ac63e40229def440f/internal/reconciler/reconciler.go` (EnqueueDocumentConvert + switch j.Engine present)
- FOUND: `/Users/apaderin/dev/octoconv/.claude/worktrees/agent-ac63e40229def440f/internal/reconciler/reconciler_test.go` (documentCalls, new tests present)
- FOUND: `/Users/apaderin/dev/octoconv/.claude/worktrees/agent-ac63e40229def440f/internal/metrics/metrics.go` (unroutable_engine label present)
- Commit `f4bd49e`: `git log --oneline --all | grep f4bd49e` -> FOUND
- Commit `27fbfde`: `git log --oneline --all | grep 27fbfde` -> FOUND
- Commit `51b0fd5`: `git log --oneline --all | grep 51b0fd5` -> FOUND

No missing items.
