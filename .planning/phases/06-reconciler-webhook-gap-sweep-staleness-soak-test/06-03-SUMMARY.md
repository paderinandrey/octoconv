---
phase: 06-reconciler-webhook-gap-sweep-staleness-soak-test
plan: 03
subsystem: reconciler
tags: [go, asynq, reconciler, webhook, prometheus, sweep]

# Dependency graph
requires:
  - phase: 06-reconciler-webhook-gap-sweep-staleness-soak-test
    provides: "Plan 01 (WebhookUniqueTTL/asynq.Unique on the webhook queue) and Plan 02 (FindWebhookGaps/RecordWebhookGapRecovered on *jobs.Repo)"
provides:
  - "Sweeper.sweep() second scan: enqueue-first webhook-gap recovery guarded by asynq.ErrDuplicateTask"
  - "jobStore interface extended with FindWebhookGaps + RecordWebhookGapRecovered"
  - "octoconv_reconciler_actions_total metric documents the webhook_gap_recovered action value"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Second independent enqueue-first + asynq.ErrDuplicateTask-guarded scan appended to an existing sweep() loop, sharing the same best-effort/no-panic error discipline as the first scan"

key-files:
  created: []
  modified:
    - internal/reconciler/reconciler.go
    - internal/reconciler/reconciler_test.go
    - internal/metrics/metrics.go

key-decisions:
  - "Webhook-gap recovery is implemented as a fully independent second loop appended after the existing queued/active loop in sweep(), rather than interleaved with it — matches RESEARCH.md Pattern 4 and keeps the two staleness domains (queued/active vs done/failed-with-no-webhook) decoupled"
  - "FindWebhookGaps error is best-effort: sweep() returns silently (no panic, no propagated error) so a single tick's finder failure never blocks the queued/active recovery work already completed earlier in the same call"

patterns-established:
  - "Enqueue-first + asynq.ErrDuplicateTask guard reused verbatim for a second job class (webhook gaps) within the same sweep() function"

requirements-completed: [RECON-04]

# Metrics
duration: ~15min
completed: 2026-07-08
---

# Phase 6 Plan 3: Reconciler Webhook-Gap Sweep Integration Summary

**`Sweeper.sweep()` gains a second enqueue-first scan over `FindWebhookGaps`, combining Plan 01's `asynq.Unique` lock and Plan 02's gap-finder into the working RECON-04 behavior**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-07-08T20:45:00Z (approx.)
- **Completed:** 2026-07-08T20:58:53Z
- **Tasks:** 2/2 completed
- **Files modified:** 3

## Accomplishments
- Extended the `jobStore` interface with `FindWebhookGaps`/`RecordWebhookGapRecovered`, satisfied by `*jobs.Repo` (Plan 02) with no other production wiring changes needed
- Appended a second scan to `sweep()`: for each `done`/`failed` job with a silently-dropped webhook enqueue, attempts `EnqueueWebhookDeliver` first; `asynq.ErrDuplicateTask` is treated as "delivery already live, not a gap" and skipped silently; any other enqueue error is best-effort/retry-next-tick
- Successful, non-duplicate enqueues record a `job_events` row via `RecordWebhookGapRecovered` and increment `octoconv_reconciler_actions_total{action="webhook_gap_recovered"}` — uncapped/single-shot, never counted toward `MaxRecoveries`
- Updated `reconcilerActions`' Prometheus Help text and `RecordReconcilerAction`'s doc comment to document the new action value
- Extended `fakeStore`/`fakeEnqueuer` in the existing in-memory unit-test harness and added 3 new tests covering the recovery, duplicate-skip, and finder-error-best-effort paths — all pass with no live DB/Redis required

## Task Commits

Each task was committed atomically:

1. **Task 1: Extend jobStore and add the webhook-gap sweep loop** - `05c2bfd` (feat)
2. **Task 2: Extend fakes and add webhook-gap sweep unit tests** - `b3525db` (test)

## Files Created/Modified
- `internal/reconciler/reconciler.go` - `jobStore` interface gains `FindWebhookGaps`/`RecordWebhookGapRecovered`; `sweep()` gains a second enqueue-first webhook-gap scan appended after the existing queued/active loop, with a doc comment explaining the one-shot/uncapped nature of webhook-gap recovery
- `internal/reconciler/reconciler_test.go` - `fakeStore` gains `webhookGaps`/`findWebhookGapsErr`/`webhookGapRecoveredCalls` fields + `FindWebhookGaps`/`RecordWebhookGapRecovered` methods; `fakeEnqueuer` gains `enqueueWebhookErr` (now returned by `EnqueueWebhookDeliver`, previously always `nil`); added `TestSweepRecoversWebhookGap`, `TestSweepSkipsDuplicateWebhookGap`, `TestSweepWebhookGapFindErrorBestEffort`
- `internal/metrics/metrics.go` - `reconcilerActions` CounterVec Help text and `RecordReconcilerAction` doc comment updated to list `webhook_gap_recovered` alongside `recovered`/`exhausted`

## Decisions Made
- Followed the plan's exact code shape from RESEARCH.md Pattern 4 for the second scan (enqueue-first, `errors.Is(err, asynq.ErrDuplicateTask)` guard, `continue` on any other error) — no deviation from the research-provided implementation
- Placed the new scan's finder-error check as an early `return` from `sweep()` rather than wrapping it in an `if err == nil` block, since it is the last statement in the function — behaviorally identical to the research's `if err == nil { ... }` shape but reads more consistently with the existing `FindStale` early-return at the top of `sweep()`

## Deviations from Plan

None - plan executed exactly as written. All acceptance criteria (build/vet clean, `FindWebhookGaps` present in both interface and `sweep()`, `RecordReconcilerAction("webhook_gap_recovered")` after the enqueue call, `errors.Is(err, asynq.ErrDuplicateTask)` count of 2, metrics Help text updated, all reconciler unit tests passing without a live DB) verified directly.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- RECON-04 (webhook-gap sweep) is now fully implemented end-to-end: Plan 01's `asynq.Unique` lock + Plan 02's `FindWebhookGaps`/`RecordWebhookGapRecovered` + this plan's `sweep()` integration all land together, closing the Redis-blip webhook-loss race described in the phase context.
- Full repo test suite (`go test ./...`) passes with this change in place; no regressions in any other package.
- No blockers for Plan 04 (RECON-05 staleness soak test), which is independent of this plan's webhook-gap work and only needs `Sweeper`/`Config`/`jobs.Repo` as already wired.

---
*Phase: 06-reconciler-webhook-gap-sweep-staleness-soak-test*
*Completed: 2026-07-08*

## Self-Check: PASSED

- FOUND: internal/reconciler/reconciler.go
- FOUND: internal/reconciler/reconciler_test.go
- FOUND: internal/metrics/metrics.go
- FOUND: .planning/phases/06-reconciler-webhook-gap-sweep-staleness-soak-test/06-03-SUMMARY.md
- FOUND: 05c2bfd (Task 1: feat commit)
- FOUND: b3525db (Task 2: test commit)
- FOUND: 9de7c51 (docs: summary commit)
