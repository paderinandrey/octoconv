---
phase: 06-reconciler-webhook-gap-sweep-staleness-soak-test
plan: 02
subsystem: database
tags: [postgres, pgx, sql, reconciler, webhook, jobs-repo]

# Dependency graph
requires:
  - phase: 03-retry-safety-reconciler
    provides: "Repo.FindStale/RequeueStale/RecoveryCount/transition patterns this plan mirrors"
  - phase: 02-webhook-delivery
    provides: "webhook_deliveries table, delivered/dead_letter columns"
provides:
  - "WebhookGapJob type + FindWebhookGaps(ctx, activeStaleAfter) NOT EXISTS anti-join finder"
  - "RecordWebhookGapRecovered(ctx, id, status) plain job_events writer (no status change)"
  - "Migration 0004: non-partial webhook_deliveries(job_id) index"
affects: [06-03-reconciler-sweep-integration]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "NOT EXISTS anti-join for 'no row at all' checks, unconditional on delivered/dead_letter (D-05)"
    - "Event write bypassing Repo.transition when jobs.status is not actually changing"

key-files:
  created:
    - internal/db/migrations/0004_webhook_deliveries_job_idx.sql
  modified:
    - internal/jobs/repo.go
    - internal/jobs/repo_test.go

key-decisions:
  - "FindWebhookGaps' NOT EXISTS subquery matches ANY webhook_deliveries row unconditionally (no delivered/dead_letter filter) so dead-lettered jobs are correctly excluded, per D-05"
  - "RecordWebhookGapRecovered deliberately bypasses Repo.transition since jobs.status is not changing (from_status == to_status); the correctness guard against duplicate deliveries is the asynq.Unique lock checked enqueue-first by the sweeper (Plan 03), not a DB-level lock here"

patterns-established:
  - "Finder methods mirror FindStale's Go-computed-cutoff + pool.Query/rows.Next/Scan/Err idiom"
  - "Event-only writers (no status transition) use a plain r.pool.Exec insert instead of the guarded transition() helper"

requirements-completed: [RECON-04]

# Metrics
duration: 25min
completed: 2026-07-08
---

# Phase 6 Plan 2: Webhook-Gap Finder & Recorder Summary

**`FindWebhookGaps` NOT EXISTS anti-join detects done/failed jobs with a silently-dropped webhook enqueue, with `RecordWebhookGapRecovered` logging recovery without a fake status transition**

## Performance

- **Duration:** ~25 min
- **Tasks:** 2 completed
- **Files modified:** 3 (1 new migration, 2 modified in `internal/jobs/`)

## Accomplishments
- Added migration `0004_webhook_deliveries_job_idx.sql`: a non-partial b-tree index on `webhook_deliveries(job_id)` so the anti-join query stays an index lookup instead of a seq scan as the table grows.
- Implemented `WebhookGapJob` type and `FindWebhookGaps` on `*jobs.Repo`: a `NOT EXISTS` anti-join finding `done`/`failed` jobs with a non-empty `callback_url`, `finished_at` older than a Go-computed cutoff, and zero `webhook_deliveries` rows of any kind.
- Implemented `RecordWebhookGapRecovered`: a plain `job_events` insert with `from_status == to_status` (no `jobs.status` mutation), deliberately not routed through `Repo.transition`.
- `TestFindWebhookGaps` covers all six required cases (done gap, failed gap, delivered-row exclusion, dead-lettered-row exclusion, fresh-job exclusion, empty-callback exclusion) plus the `RecordWebhookGapRecovered` event round-trip, and passes against live Postgres.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the webhook_deliveries(job_id) supporting index migration** - `5fdc0b6` (feat)
2. **Task 2 (RED): Add failing TestFindWebhookGaps** - `c080892` (test)
2. **Task 2 (GREEN): Implement FindWebhookGaps + RecordWebhookGapRecovered** - `d76c960` (feat)

_TDD task: RED (failing compile — methods didn't exist) → GREEN (implementation, test passes). No REFACTOR commit needed — implementation matched the RESEARCH.md-provided code exactly on first pass._

## Files Created/Modified
- `internal/db/migrations/0004_webhook_deliveries_job_idx.sql` - non-partial index on `webhook_deliveries(job_id)` supporting the anti-join
- `internal/jobs/repo.go` - `detailActionWebhookGapRecovered` const, `WebhookGapJob` type, `FindWebhookGaps`, `RecordWebhookGapRecovered`
- `internal/jobs/repo_test.go` - `TestFindWebhookGaps` (6 cases + recovery-event assertion)

## Decisions Made
- Followed the plan's exact SQL/Go shapes as specified in `06-RESEARCH.md` Pattern 2/3 and `06-PATTERNS.md` — no deviation needed since the research/pattern-map already contained verified, copy-ready code.
- Test data setup reused `MarkActive`/`MarkDone`/`MarkFailed` (the guarded transition path) to create realistic jobs, then backdated `finished_at` via direct SQL (`UPDATE jobs SET finished_at = now() - interval '1 hour'`), matching `TestFindStale`'s established convention for this repo.

## Deviations from Plan

None - plan executed exactly as written. All acceptance criteria (build/vet clean, `NOT EXISTS` present, no `delivered`/`dead_letter` filter inside `FindWebhookGaps`'s subquery, `INSERT INTO job_events ... VALUES ($1, $2, $2, $3)` shape, live-DB test passing) verified directly.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `FindWebhookGaps`/`RecordWebhookGapRecovered` are ready for Plan 03 (reconciler `sweep()` integration) to wire into the `jobStore` interface and the enqueue-first + `asynq.ErrDuplicateTask`-guard loop.
- Migration 0004 is applied transitively by every `newTestRepo`/`db.Migrate` call already exercised by this plan's own test run — no separate migration-apply step needed for Plan 03.
- No blockers. Plan 03 still needs the `asynq.Unique` addition to `NewWebhookDeliverTask` (D-01/D-02, tracked in a sibling plan) before the sweep loop can be turned on safely in production — this data-layer half is independent of that and does not block on it structurally, but functional correctness of the full sweep requires both.

---
*Phase: 06-reconciler-webhook-gap-sweep-staleness-soak-test*
*Completed: 2026-07-08*

## Self-Check: PASSED

- FOUND: internal/db/migrations/0004_webhook_deliveries_job_idx.sql
- FOUND: FindWebhookGaps in internal/jobs/repo.go
- FOUND: RecordWebhookGapRecovered in internal/jobs/repo.go
- FOUND: commit 5fdc0b6 (Task 1: migration)
- FOUND: commit c080892 (Task 2 RED: failing test)
- FOUND: commit d76c960 (Task 2 GREEN: implementation)
- FOUND: commit 162856f (docs: summary)
