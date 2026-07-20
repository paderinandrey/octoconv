---
phase: 35-queue-worker-routing-integration
plan: 02
subsystem: api
tags: [asynq, redis, queue, go, av-engine, keda]

# Dependency graph
requires:
  - phase: 34-av-engine-foundation
    provides: AVConverter (Convert/Pairs/Engine), convert.EngineAV constant, deliberately unregistered
provides:
  - "TypeAVConvert/QueueAV task-type and queue-name constants aliasing convert.EngineAV"
  - "NewAVConvertTask task builder routed to the av queue"
  - "avRetrySchedule/AVRetryDelay implementing D-03's locked 30s/2m retry budget"
  - "RetryDelayFunc dispatch case for TypeAVConvert (closes Pitfall 1's silent default fallthrough)"
  - "avBackoffSum/AVUniqueTTL deriving the av unique-lock TTL from the shared uniqueTTLSafetyMargin"
  - "AllConvertQueues() -- single source of truth for the KEDA queue-depth collector arg list (D-06)"
  - "Client.EnqueueAVConvert(ctx, jobID) producer method"
affects: [35-03, 35-04, 35-05]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Engine-class wiring by hand (mirror the audio block, not a shared abstraction) -- Pattern 1 from 35-PATTERNS.md"
    - "Completeness test replaces a routing-switch refactor (D-06): AllConvertQueues() + TestAllConvertQueuesCoversEveryEngine close the highest-risk silent seam (variadic collector call) without touching the four other engines' hot paths"

key-files:
  created: []
  modified:
    - internal/queue/queue.go
    - internal/queue/queue_test.go
    - internal/queue/client.go

key-decisions:
  - "AllConvertQueues() deliberately excludes QueueWebhook -- webhook is not an engine class and is never KEDA-scaled per-conversion; plan 04 rewires cmd/api/main.go to spread this helper instead of the hand-listed variadic call"
  - "AV_ENGINE_TIMEOUT defaults to 600s [ASSUMED], mirroring AUDIO_ENGINE_TIMEOUT's original placeholder precedent (600s -> 742s after Phase 32's RTF measurement); Phase 36 re-derives the real value from an RTF matrix, the AVUniqueTTL formula itself is unaffected"
  - "AVUniqueTTL reuses the shared uniqueTTLSafetyMargin verbatim (D-03 locked) -- no per-engine margin constant introduced"

patterns-established:
  - "Every new engine-class queue seam gets a paired doc-comment note tying its provisional numeric default back to its RESEARCH.md precedent, so a future reader can find the coupling (e.g. AV_ENGINE_TIMEOUT vs RECONCILER_ACTIVE_STALE_AFTER) without re-deriving it"

requirements-completed: [AVE-03]

# Metrics
duration: 35min
completed: 2026-07-21
---

# Phase 35 Plan 02: Queue Layer AV Wiring Summary

**Wires the `av` engine class into the queue layer (task type, queue name, task builder, D-03 retry schedule, unique-lock TTL, producer method) and replaces the API's hand-written queue-depth collector list with a derived `AllConvertQueues()` helper guarded by a completeness test (D-06).**

## Performance

- **Duration:** 35 min
- **Started:** 2026-07-21T00:00:00Z (approx, worktree spawn)
- **Completed:** 2026-07-21
- **Tasks:** 3 completed
- **Files modified:** 3 (`internal/queue/queue.go`, `internal/queue/queue_test.go`, `internal/queue/client.go`)

## Accomplishments
- `TypeAVConvert`/`QueueAV` constants, `NewAVConvertTask` builder, `avRetrySchedule`/`AVRetryDelay` (D-03's locked 30s/2m schedule), and the `RetryDelayFunc` dispatch case all landed together in one commit so the Pitfall-1 silent-fallthrough window never existed in git history.
- `avBackoffSum`/`AVUniqueTTL` derive the av unique-lock TTL from the same `(maxRetry+1)*engineTimeout + backoffSum + margin` formula every other engine class uses, reusing the shared `uniqueTTLSafetyMargin` verbatim.
- `AllConvertQueues()` gives `cmd/api/main.go`'s KEDA queue-depth collector a single derived source of truth instead of a hand-maintained variadic arg list, closing the phase's highest-risk silent seam (Pitfall 2) — the seam itself is not yet wired into `cmd/api/main.go` (that is Plan 04's job), but the helper and its completeness guard now exist.
- `Client.EnqueueAVConvert` gives the API/reconciler `Enqueuer` interfaces (Plans 04/05) a concrete method to satisfy.
- All three of the plan's named silent seams (`RetryDelayFunc` default fallthrough, `AllConvertQueues` omission, `uniqueTTLSafetyMargin` zeroing) were verified by temporary edit + restore to actually fail their guarding test — not just written defensively.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the av task type, queue name, task builder, retry schedule and delay dispatch** - `8b0ba41` (feat)
2. **Task 2: Add AllConvertQueues (D-06 structural fix) and the queue-layer test set** - `fc8faeb` (feat)
3. **Task 3: Add EnqueueAVConvert to the producer client** - `f9d60eb` (feat)

_No TDD tasks in this plan; each commit is feat with its own tests added in the same or a paired commit._

## Files Created/Modified
- `internal/queue/queue.go` - `TypeAVConvert`/`QueueAV` constants, `NewAVConvertTask`, `avRetrySchedule`/`AVRetryDelay`, `RetryDelayFunc` case, `avBackoffSum`/`AVUniqueTTL`, `AllConvertQueues()`
- `internal/queue/queue_test.go` - `TestAVConvertTaskRoundTrip`, `TestAVRetryDelaySchedule`, `TestRetryDelayFuncRoutesAVConvert`, `TestAVUniqueTTL`, `TestAllConvertQueuesCoversEveryEngine`
- `internal/queue/client.go` - `avMaxRetry`/`avUniqueTTL` fields, `AV_MAX_RETRY`/`AV_ENGINE_TIMEOUT` env reads with the Pitfall-4 reconciler-coupling comment, `EnqueueAVConvert`

## Decisions Made
- `AllConvertQueues()` excludes `QueueWebhook` by design (it is not an engine class); the plan's own acceptance criteria and `35-CONTEXT.md` D-06 both name this exclusion explicitly, so it is documented in the function's doc comment rather than left implicit.
- Kept the `AV_ENGINE_TIMEOUT`/`RECONCILER_ACTIVE_STALE_AFTER` coupling comment at the `NewClient` env-read site (not just in queue.go), since that is where an operator raising the env var would actually look.
- No architectural deviations; every task matched its `35-PATTERNS.md` analog closely enough that no Rule 4 decision was needed.

## Deviations from Plan

None — plan executed exactly as written. One minor internal correction during execution: I initially wrote `AllConvertQueues()` as part of Task 1's edit (both live in `queue.go`), then moved it out before committing Task 1 so the per-task commit boundaries matched the plan's task/file assignment exactly (Task 1 = task-type/queue/builder/retry/delay only; Task 2 = `AllConvertQueues` + tests). This was caught before any commit, so it left no trace in git history and is not a Rule 1-4 deviation, just noted for completeness.

## Issues Encountered
None. `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` (full repo, not just `internal/queue`) all pass cleanly at HEAD. `git diff --stat go.mod go.sum` shows no changes (zero new dependencies, as required).

## Verification detail: seam-failure proofs

Per the plan's acceptance criteria, each of the three new completeness/regression tests was verified to actually fail when its guarded seam is removed, via a temporary edit that was restored byte-for-byte before committing (confirmed with `diff` against a pre-edit backup):

1. **`TestAVUniqueTTL` vs. zeroed `uniqueTTLSafetyMargin`:** with the margin constant temporarily set to `0 * time.Minute`, both the exact-value assertion (`want != 2070s`) and the strict-lower-bound assertion failed as expected (`AVUniqueTTL(2, 10m0s) = 32m30s must strictly exceed the zero-margin worst-case lifetime 32m30s`).
2. **`TestRetryDelayFuncRoutesAVConvert` vs. removed `case TypeAVConvert`:** with the case arm stripped from `RetryDelayFunc`'s switch, the test failed with values matching asynq's own `DefaultRetryDelayFunc` output (e.g. `RetryDelayFunc(av, 0) = 32s, want 30s`) rather than the locked D-03 schedule — exactly the silent-fallthrough failure mode Pitfall 1 describes.
3. **`TestAllConvertQueuesCoversEveryEngine` vs. dropped `QueueAV`:** with `QueueAV` removed from `AllConvertQueues()`'s return slice, the test failed with `AllConvertQueues() contains engine "av" 0 times, want exactly 1`.

## User Setup Required

None - no external service configuration required. `AV_MAX_RETRY`/`AV_ENGINE_TIMEOUT` are optional env vars with defensible defaults (2 / 600s); no `.env.example` update was in this plan's scope (Plan 03/04/05 own the worker binary and env docs that would reference them operationally).

## Next Phase Readiness
- Plans 03 (worker), 04 (API routing), and 05 (reconciler routing) can now depend on `queue.TypeAVConvert`, `queue.QueueAV`, `queue.AllConvertQueues()`, and `(*queue.Client).EnqueueAVConvert` existing and compiling.
- No blockers. The one thing downstream plans must remember: `AllConvertQueues()` exists but `cmd/api/main.go`'s collector call site has NOT been rewired to use it yet — that rewiring is explicitly Plan 04's task, not done here.

---
*Phase: 35-queue-worker-routing-integration*
*Completed: 2026-07-21*
