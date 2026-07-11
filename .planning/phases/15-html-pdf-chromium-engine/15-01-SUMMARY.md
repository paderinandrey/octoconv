---
phase: 15-html-pdf-chromium-engine
plan: 01
subsystem: database
tags: [postgres, asynq, redis, go, queue-routing, reconciler]

# Dependency graph
requires:
  - phase: 09-document-engine
    provides: "document engine class (queue/producer/reconciler/CHECK-migration shape) mirrored 1:1 for html"
provides:
  - "jobs.engine CHECK constraint accepts 'html' (0005_html_engine.sql)"
  - "convert.EngineHTML single-source-of-truth const + htm->html NormalizeFormat alias"
  - "queue.TypeHTMLConvert/QueueHTML/NewHTMLConvertTask/HTMLRetryDelay/HTMLUniqueTTL"
  - "queue.Client.EnqueueHTMLConvert producer method (HTML_MAX_RETRY/HTML_ENGINE_TIMEOUT env)"
  - "api.Enqueuer + reconciler enqueuer interfaces route EnqueueHTMLConvert"
  - "reconciler sweep has an explicit case convert.EngineHTML (no longer fails closed via default)"
affects: [15-html-pdf-chromium-engine plans 02-05 (converter, worker, container, e2e)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Third engine class (html) scaffolded as a mechanical mirror of the document engine: const -> queue/task-type -> producer -> Enqueuer interface -> reconciler switch case"

key-files:
  created:
    - internal/db/migrations/0005_html_engine.sql
  modified:
    - internal/convert/convert.go
    - internal/convert/convert_test.go
    - internal/queue/queue.go
    - internal/queue/queue_test.go
    - internal/queue/client.go
    - internal/api/api.go
    - internal/api/handlers_test.go
    - internal/reconciler/reconciler.go
    - internal/reconciler/reconciler_test.go

key-decisions:
  - "htmlRetrySchedule/HTMLRetryDelay copy documentRetrySchedule's no-jitter, clamp-to-last-entry shape (5s/15s/30s) rather than webhook's jittered shape, matching the plan's explicit instruction."
  - "HTML_MAX_RETRY defaults to 3 and HTML_ENGINE_TIMEOUT defaults to 60s (chosen to mirror document's bounded-retry-budget reasoning; the shorter default timeout anticipates a one-shot chromium print-to-pdf invocation being faster than a LibreOffice document conversion — final tuning deferred to the worker/container plans)."
  - "Reconciler sweep switch gets an explicit case convert.EngineHTML (not left to the fail-closed default) so a stranded html job can be recovered from day one, per T-15-02's threat-model mitigation."

requirements-completed: [HTML-01]

# Metrics
duration: 25min
completed: 2026-07-11
---

# Phase 15 Plan 01: HTML Engine Class Scaffolding Summary

**Third (html) engine class fully declared end-to-end — CHECK migration, EngineHTML const, htm-alias, dedicated asynq queue/retry/TTL, producer method, and reconciler routing — mirroring the document engine 1:1, with no converter or worker wired yet.**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-11T13:38:00Z
- **Completed:** 2026-07-11T14:03:39Z
- **Tasks:** 3
- **Files modified:** 10 (1 created, 9 modified)

## Accomplishments
- `jobs.engine` CHECK constraint now accepts `'html'` via a new migration (`0005_html_engine.sql`), the hard prerequisite for any `engine="html"` job row.
- `convert.EngineHTML = "html"` added as the single compile-time source of truth, plus a `htm -> html` `NormalizeFormat` alias closing Pitfall D (a `report.htm` upload now routes correctly).
- Full queue/producer plumbing for the html engine class: `TypeHTMLConvert`, `QueueHTML` (tied to `convert.EngineHTML`), `NewHTMLConvertTask`, a no-jitter `htmlRetrySchedule`/`HTMLRetryDelay`, a derived `HTMLUniqueTTL`, and `RetryDelayFunc` dispatch — all mirroring the document engine's shape exactly.
- `queue.Client.EnqueueHTMLConvert` producer method, reading `HTML_MAX_RETRY` (default 3) and `HTML_ENGINE_TIMEOUT` (default 60s).
- `api.Enqueuer` and the reconciler's package-private `enqueuer` interface both gained `EnqueueHTMLConvert`; the reconciler sweep switch has an explicit `case convert.EngineHTML:` so a stranded html job is recovered rather than silently skipped by the fail-closed `default:` branch.

## Task Commits

Each task was committed atomically:

1. **Task 1: jobs.engine CHECK migration + EngineHTML const + htm→html alias** - `6eca905` (feat)
2. **Task 2: Queue task-type/queue constants + retry/TTL funcs + producer method** - `d2552c4` (feat)
3. **Task 3: Enqueuer interface method (API) + reconciler engine switch case** - `5daef77` (feat)

**Plan metadata:** (this SUMMARY.md commit, made by the caller)

## Files Created/Modified
- `internal/db/migrations/0005_html_engine.sql` - drops/re-adds `jobs_engine_check` to include `'html'`
- `internal/convert/convert.go` - `EngineHTML` const, `htm -> html` `NormalizeFormat` alias
- `internal/convert/convert_test.go` - table-driven coverage for `htm`/`.HTM`
- `internal/queue/queue.go` - `TypeHTMLConvert`, `QueueHTML`, `NewHTMLConvertTask`, `htmlRetrySchedule`/`HTMLRetryDelay`, `htmlBackoffSum`/`HTMLUniqueTTL`, `RetryDelayFunc` case
- `internal/queue/queue_test.go` - round-trip, retry-schedule, dispatch, and unique-TTL coverage for the html engine class
- `internal/queue/client.go` - `htmlMaxRetry`/`htmlUniqueTTL` fields, env reads, `EnqueueHTMLConvert`
- `internal/api/api.go` - `Enqueuer` interface gains `EnqueueHTMLConvert`
- `internal/api/handlers_test.go` - `fakeQueue.EnqueueHTMLConvert`
- `internal/reconciler/reconciler.go` - `enqueuer` interface gains `EnqueueHTMLConvert`; sweep switch gains `case convert.EngineHTML:`
- `internal/reconciler/reconciler_test.go` - `fakeEnqueuer.EnqueueHTMLConvert` + `TestSweepRoutesHTMLJobsToHTMLQueue`

## Decisions Made
- Followed the plan's explicit interface spec (from RESEARCH.md's file:line table) verbatim — no deviation from the documented mirror-the-document-engine shape.
- Chose `HTML_ENGINE_TIMEOUT` default of 60s (shorter than document's 300s) anticipating a lighter one-shot chromium print-to-pdf invocation; this is a scaffolding-time default only, not validated against a live chromium invocation yet — later plans (converter/worker) may revisit if live testing shows otherwise.

## Deviations from Plan

None - plan executed exactly as written. The plan's Task 2/3 acceptance criteria only required the production code + `go build`/`go vet`/`go test ./internal/queue/...` to pass; test-fixture updates for `fakeQueue`/`fakeEnqueuer` (adding `EnqueueHTMLConvert` methods so existing interface-typed test helpers still compile) were a direct, mechanical consequence of the plan's own interface-widening tasks (Task 3), not unplanned scope — same discipline as any interface method addition requires updating its fakes.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. This plan is pure Go/SQL scaffolding; no new env vars are mandatory (HTML_MAX_RETRY/HTML_ENGINE_TIMEOUT both have working defaults).

## Next Phase Readiness
- All symbols downstream plans need are now compiled and tested: `convert.EngineHTML`, `queue.TypeHTMLConvert`/`QueueHTML`/`NewHTMLConvertTask`/`HTMLRetryDelay`/`HTMLUniqueTTL`, `queue.Client.EnqueueHTMLConvert`, and both `Enqueuer` interfaces route html jobs.
- No converter, worker, or container exists yet — an `engine="html"` job can be created and enqueued but nothing will ever dequeue/process it until Plan 02+ lands the `ChromiumConverter`, worker wiring, and Dockerfile.
- Live confirmation of the `jobs_engine_check` constraint name (via `\d jobs`) is explicitly deferred to Plan 05 acceptance per the plan's own scope boundary — not a blocker for this plan.

---
*Phase: 15-html-pdf-chromium-engine*
*Completed: 2026-07-11*

## Self-Check: PASSED

All created/modified files confirmed present on disk; all three task commit hashes (6eca905, d2552c4, 5daef77) confirmed present in git log.
