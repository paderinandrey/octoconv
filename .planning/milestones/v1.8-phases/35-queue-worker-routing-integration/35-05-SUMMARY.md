---
phase: 35-queue-worker-routing-integration
plan: 05
subsystem: reconciler
tags: [reconciler, av-engine, routing, go, completeness-test]

# Dependency graph
requires:
  - phase: 35-queue-worker-routing-integration
    plan: "02"
    provides: "queue.TypeAVConvert/QueueAV, AVUniqueTTL, (*queue.Client).EnqueueAVConvert -- the concrete producer method the reconciler's enqueuer interface now requires"
provides:
  - "reconciler enqueuer interface +EnqueueAVConvert method"
  - "sweep() routing case convert.EngineAV -> s.enq.EnqueueAVConvert"
  - "TestSweepRoutesAVJobsToAVQueue -- proves stranded av jobs recover onto the av queue"
  - "TestSweepRoutesEveryEngineConstant -- D-06 completeness guard, table-driven over convert.Engine* constants"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "D-06 completeness test as the sanctioned structural replacement for a queueForEngine refactor (declined this phase) -- proven to actually guard the seam by temporary removal + restore, not just written defensively"

key-files:
  created: []
  modified:
    - internal/reconciler/reconciler.go
    - internal/reconciler/reconciler_test.go

key-decisions:
  - "TestSweepSkipsUnknownEngine's fixture engine changed from \"av\" to \"cad\", since av is no longer unroutable after this plan -- cad remains genuinely out of scope per the fail-closed default's own comment"
  - "TestSweepRoutesEveryEngineConstant proves 'zero unroutable_engine outcomes' indirectly via store.requeueStaleCalls == 1 (RequeueStale only runs after a successful enqueue past the switch, never from the default arm) rather than reading the Prometheus counter directly -- avoids importing prometheus/client_golang/testutil into the reconciler test package for a property already provable from existing fakeStore call-counting idioms"
  - "The completeness test's table is driven by convert.Engine* symbol references (not string literals), so a rename of any engine constant fails to compile rather than silently drifting; extending it for a future 6th engine requires one added table entry (documented in the test's own doc comment)"

requirements-completed: [AVE-03]

# Metrics
duration: 25min
completed: 2026-07-21
---

# Phase 35 Plan 05: Reconciler AV Routing + D-06 Completeness Guard Summary

**Adds the `av` routing case to the reconciler's `enqueuer` interface and `sweep()` switch (mirroring audio by hand per D-06), plus a table-driven completeness test proving every `convert.Engine*` constant is routable -- verified to actually fail when the routing case is removed, not just written defensively.**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-21 (worktree spawn)
- **Completed:** 2026-07-21
- **Tasks:** 2 completed
- **Files modified:** 2 (`internal/reconciler/reconciler.go`, `internal/reconciler/reconciler_test.go`)

## Accomplishments
- `enqueuer` interface gained `EnqueueAVConvert(ctx, id) error`, positioned after `EnqueueAudioConvert` -- satisfied at compile time by the concrete `*queue.Client.EnqueueAVConvert` built in plan 02, so `go build ./...` immediately proved the interface/implementation pairing was correct.
- `sweep()`'s routing switch gained `case convert.EngineAV: enqueueErr = s.enq.EnqueueAVConvert(ctx, j.ID)`, placed after `EngineAudio` and before `default:`. The `default:` arm's comment, which previously named `av` as the next engine to add, now names only the remaining out-of-scope engines (`cad`/`archive`/`probe`) so it does not go stale.
- `TestSweepRoutesAVJobsToAVQueue` mirrors `TestSweepRoutesAudioJobsToAudioQueue` exactly: one stale `engine="av"` job produces exactly one `EnqueueAVConvert` call and zero calls on the other four `Enqueue*` methods.
- `TestSweepRoutesEveryEngineConstant` is the D-06 completeness guard: a table driven by all five `convert.Engine*` constants, each run through its own sweep, asserting the matching accessor recorded exactly one call, every other accessor recorded zero, and `RequeueStale` fired exactly once (the indirect proof the switch did not fall through to the fail-closed `default:`).
- Fixed a now-stale test fixture: `TestSweepSkipsUnknownEngine` previously used `Engine: "av"` as its "genuinely unroutable" example; since `av` is routable after this plan, it now uses `"cad"` instead, with a doc comment explaining the change.
- Both new av-specific tests were verified to actually fail (not just theoretically) when `case convert.EngineAV` is temporarily removed from the routing switch, then the file was restored byte-for-byte (confirmed via `diff` against a pre-edit backup).
- No `queueForEngine` helper was introduced (`grep -c 'queueForEngine' internal/reconciler/reconciler.go` == 0), respecting D-06's explicit deferral of that refactor.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the av routing case to the reconciler** - `c7a8f69` (feat)
2. **Task 2: Add the av fake, routing test, and engine-routing completeness test (D-06)** - `b559eed` (test)

## Files Created/Modified
- `internal/reconciler/reconciler.go` - `enqueuer` interface `+EnqueueAVConvert`, `case convert.EngineAV` in the routing switch, updated `default:` comment
- `internal/reconciler/reconciler_test.go` - `fakeEnqueuer.enqueueAVErr`/`avCalls`/`EnqueueAVConvert`/`avCallIDs()`, `TestSweepRoutesAVJobsToAVQueue`, `TestSweepRoutesEveryEngineConstant`, `TestSweepSkipsUnknownEngine` fixture change (`"av"` -> `"cad"`)

## Decisions Made
- Kept Task 1 (implementation) and Task 2 (fake + tests) as two separate commits per the plan's file assignment, even though `go vet ./internal/reconciler/...` cannot fully pass on Task 1 alone (the test package's `NewSweeper(store, enq, ...)` call site requires `fakeEnqueuer` to satisfy the extended `enqueuer` interface, which only happens in Task 2). `go build ./...` (non-test compilation) passed cleanly after Task 1, and all grep-based acceptance criteria for Task 1 were satisfied independently; the full `go vet`/`go test` pass is only achievable -- and was achieved -- after Task 2 landed.
- Proved "zero unroutable_engine outcomes" indirectly via `store.requeueStaleCalls == 1` rather than reading the Prometheus counter, keeping the test package's import set unchanged (no `prometheus/client_golang/testutil` dependency added).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `TestSweepSkipsUnknownEngine` fixture used `Engine: "av"`, which stopped being unroutable after Task 1**
- **Found during:** Task 2, running `go test ./internal/reconciler/ -run 'TestSweepRoutes' -v -count=1` after adding the completeness test
- **Issue:** The pre-existing `TestSweepSkipsUnknownEngine` (written before this phase, when `av` was the reconciler's canonical "not yet wired" example) asserted `store.requeueStaleCalls == 0` for `Engine: "av"`. After Task 1 added `case convert.EngineAV`, that assertion became factually wrong -- an `av` job now legitimately routes and requeues, so the test would silently start asserting the opposite of what the new routing case does.
- **Fix:** Changed the fixture engine to `"cad"` (still genuinely out of scope per the `default:` arm's own comment) and added a doc comment explaining why `"av"` was replaced.
- **Files modified:** `internal/reconciler/reconciler_test.go`
- **Commit:** `b559eed`

No other deviations -- plan executed exactly as written otherwise.

## Issues Encountered
None outside the one auto-fixed test-fixture staleness above. `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` (full repo) all pass cleanly at the final commit.

## Verification detail: seam-failure proof

Per the plan's Task 2 acceptance criteria, the routing case's guarding tests were verified to actually fail when the seam is removed:

- With `case convert.EngineAV: ...` temporarily deleted from `sweep()`'s switch (2-line removal via a scripted edit): `TestSweepRoutesAVJobsToAVQueue` failed with `EnqueueAVConvert calls = [], want [<uuid>]`, and `TestSweepRoutesEveryEngineConstant/av` failed with `engine "av": matching Enqueue* calls = [], want [<uuid>]` -- exactly the failure mode the `default:` fail-closed path produces (no enqueue, no requeue).
- The file was restored byte-for-byte immediately after, confirmed with `diff` against a pre-edit backup showing zero differences.
- `grep -c 'queueForEngine' internal/reconciler/reconciler.go` == 0, `grep -c 'convert.Engine' internal/reconciler/reconciler_test.go` == 6 (>= 5 required).

## User Setup Required

None -- no external service configuration required, no new dependencies, no env vars.

## Next Phase Readiness
- The reconciler now routes stranded `jobs.engine='av'` rows to `EnqueueAVConvert`, closing the second of D-06's three seams (API enqueue switch is plan 04's responsibility; the queue-depth collector list was closed in plan 02's `AllConvertQueues()`).
- No blockers for downstream plans. `TestSweepRoutesEveryEngineConstant` is now a standing regression guard: any future engine class added to `convert.go` without a corresponding `case`/table entry here will need an explicit table addition, and omitting the reconciler's own switch case will fail the existing table entries' `RequeueStale` assertions the moment that engine's constant is added to the table.

## Self-Check: PASSED

- FOUND: internal/reconciler/reconciler.go
- FOUND: internal/reconciler/reconciler_test.go
- FOUND commit: c7a8f69 (Task 1)
- FOUND commit: b559eed (Task 2)

---
*Phase: 35-queue-worker-routing-integration*
*Completed: 2026-07-21*
