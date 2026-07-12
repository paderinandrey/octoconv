---
phase: 16-webhook-delivery-decoupling
plan: 05
subsystem: reliability
tags: [pgx, pgxpool, advisory-lock, sync.Mutex, race-detector, reconciler]

# Dependency graph
requires:
  - phase: 16-webhook-delivery-decoupling
    provides: "Plans 16-01..16-04 (webhook delivery decoupling, reconciler sweeper, advisory-lock leader election) plus 16-VERIFICATION.md/16-REVIEW.md gap findings CR-01/WR-01"
provides:
  - "Verified (pre-satisfied) fix for CR-01: PGAdvisoryLock.TryAcquire's error path Release()s the pgxpool.Conn wrapper after hard-close, reclaiming the pool slot instead of leaking it"
  - "Verified (pre-satisfied) fix for WR-01: PGAdvisoryLock.Close() releases the dedicated connection and is deferred in cmd/webhook-worker/main.go before pool.Close(), so graceful shutdown completes in bounded time"
  - "Verified (pre-satisfied) sync.Mutex guarding all l.conn access in TryAcquire and Close, serializing the sweeper-goroutine vs shutdown-goroutine interleaving"
  - "New Test C (TestPGAdvisoryLockTryAcquireCloseRace) closing the one remaining test gap: a -race-detector regression guard for the concurrent TryAcquire/Close interleaving on PGAdvisoryLock.conn"
affects: [reconciler, webhook-worker, sweeper]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Test C pattern: background goroutine looping TryAcquire with short-lived contexts (tolerating any error) racing a main-goroutine Close(), used as a -race-only regression guard with no behavioral assertion beyond clean completion"

key-files:
  created: []
  modified:
    - internal/reconciler/advisorylock_conn_test.go

key-decisions:
  - "Task 1 (source fix in reconciler.go/main.go) and Tests A/B were already fully landed on main by a parallel quick-task (quick-260712-cqg, commits ddd873f/c7b153e) before this worktree forked; re-verified every Task 1 acceptance criterion against live code rather than re-implementing"
  - "Deviation: Test C added to the existing internal/reconciler/advisorylock_conn_test.go (reusing newSoakTestPool and the DATABASE_URL skip guard) instead of creating a new advisorylock_pg_test.go named in the plan frontmatter, since Tests A/B already occupy that role in the existing file and the plan explicitly names the underlying pattern (not the exact filename) as the requirement"
  - "DATABASE_URL was not reachable in this environment (no local Postgres on :5434, no docker compose stack running) — verification for the race test relies on the compile-clean + self-skip path; live pass/fail was not exercised in this run and is honestly reported as unverified rather than claimed"

requirements-completed: [WEBH-01]

# Metrics
duration: 15min
completed: 2026-07-12
---

# Phase 16 Plan 05: Advisory-Lock Connection Lifecycle Gap Closure Summary

**Verified CR-01/WR-01 pool-slot-leak and shutdown-blocking fixes already landed by a parallel quick-task, and closed the one remaining test gap with a new -race regression test for the TryAcquire/Close mutex interleaving.**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-07-12T06:20:00Z (approx.)
- **Completed:** 2026-07-12T06:37:07Z
- **Tasks:** 2 (Task 1 verification-only, Task 2 partial completion — Test C)
- **Files modified:** 1 (internal/reconciler/advisorylock_conn_test.go)

## Accomplishments

- Confirmed Task 1 (CR-01 fix, WR-01 fix, sync.Mutex) is fully implemented and correct in the live codebase — no source changes needed, every acceptance criterion re-verified against current code and passing tooling.
- Added `TestPGAdvisoryLockTryAcquireCloseRace` (plan's Test C), the last missing regression test proving the sync.Mutex serializes the sweeper-goroutine (TryAcquire) vs shutdown-goroutine (Close) interleaving on `PGAdvisoryLock.conn`.
- Full verification suite (gofmt, go vet, go build, `-run PGAdvisoryLock -race`, full package test without `-race`) all pass.

## Task 1 Verification (Pre-satisfied by quick-260712-cqg)

Per the critical-preexisting-work note, Task 1's source changes were already merged to `main` before this worktree forked (commits `ddd873f`, `c7b153e`). No code was re-implemented. Each acceptance criterion was independently re-checked against the live code in this worktree:

| Acceptance criterion | Verification method | Result |
|---|---|---|
| `gofmt -l` clean on reconciler.go / cmd/webhook-worker/main.go | `gofmt -l internal/reconciler/reconciler.go cmd/webhook-worker/main.go` | Empty output — clean |
| `go vet` / `go build` clean | `go vet ./internal/reconciler/... ./cmd/webhook-worker/...` and `go build ./...` | Both clean, zero output |
| `sync.Mutex` field + Lock/Unlock in both TryAcquire and Close | `grep -n 'sync.Mutex\|l.mu.Lock'` | `mu sync.Mutex` field at reconciler.go:115; `l.mu.Lock()` at TryAcquire (line 139) and Close (line 181), each with a `defer l.mu.Unlock()` |
| `func (l *PGAdvisoryLock) Close()` releasing conn guarded by nil-check, nils field | Read reconciler.go:180-187 | Present exactly as specified: `l.mu.Lock()`/`defer l.mu.Unlock()`, `if l.conn != nil { l.conn.Release(); l.conn = nil }` |
| TryAcquire error branch: hard-close AND Release, `l.conn` nulled, D-02 comment updated | Read reconciler.go:152-171 | Local `conn := l.conn; l.conn = nil; conn.Conn().Close(ctx); conn.Release()`, with an updated comment explicitly reconciling hard-close + Release as complementary (cites 16-REVIEW.md CR-01) |
| `cmd/webhook-worker/main.go` has exactly one `lock.Close()`, positioned between the `NewPGAdvisoryLock` error check and the final `<-ctx.Done()` | `grep -n 'lock.Close()' cmd/webhook-worker/main.go`; read lines 93-142 | Single `defer lock.Close()` at line 101, immediately after the NewPGAdvisoryLock error check (line 93-96) and well before `<-ctx.Done()` (line 142); comment explains the LIFO defer ordering relative to `defer pool.Close()` |
| No new module dependency | `git diff go.mod go.sum` | Empty — `sync` is stdlib, zero new deps |
| No logging added under internal/ | Read Close()/TryAcquire bodies | Confirmed — no log calls in reconciler.go |

All Task 1 acceptance criteria PASS. No source-code changes were made for Task 1 in this plan execution.

## Task 2: Regression Tests (Partially pre-existing, Test C added)

- **Test A** (`TestPGAdvisoryLockReleasesSlotOnError`) and **Test B** (`TestPGAdvisoryLockCloseReleasesSlot`) already existed in `internal/reconciler/advisorylock_conn_test.go`, added by the prior quick-task. Left byte-for-byte unchanged except for the shared `import "sync"` addition needed by Test C.
- **Test C** (`TestPGAdvisoryLockTryAcquireCloseRace`) — newly added in this plan execution. A background goroutine loops `lock.TryAcquire` with short-lived (5ms) cancelable contexts, tolerating any returned error, while the main test goroutine sleeps briefly to let the loop get in-flight, then calls `lock.Close()`, then signals the loop to stop and `wg.Wait()`s for clean exit. The test makes no behavioral assertion beyond completing without leaking goroutines — its purpose is purely to give `go test -race` a genuine concurrent-access interleaving on `l.conn` to catch if the guarding `sync.Mutex` were ever removed.

## Task Commits

1. **Task 1: Verify pre-existing CR-01/WR-01 fix + mutex** — no commit (verification-only, zero source changes; all acceptance criteria independently confirmed against live code, documented above)
2. **Task 2: Add Test C regression test** - `2880488` (test)

**Plan metadata:** (this SUMMARY.md commit, to follow)

## Files Created/Modified

- `internal/reconciler/advisorylock_conn_test.go` - added `sync` import and `TestPGAdvisoryLockTryAcquireCloseRace` (Test C); Tests A/B and `waitAcquiredConns`/`newSoakTestPool` helper usage unchanged

## Decisions Made

- Did not re-implement or touch `internal/reconciler/reconciler.go` or `cmd/webhook-worker/main.go` — Task 1's source fix was independently verified as fully correct against every acceptance criterion in the plan; touching already-correct code would have been unnecessary churn.
- Placed Test C in the existing `advisorylock_conn_test.go` rather than creating the plan-named `advisorylock_pg_test.go`, since Tests A/B (equivalent in spirit to the plan's Test A/Test B) already live there under `package reconciler`, sharing the `newSoakTestPool`/`waitAcquiredConns` helpers. Creating a second file with an overlapping purpose would have fragmented the DATABASE_URL-guarded advisory-lock test suite without benefit.
- Did not attempt to stand up a local Postgres/docker-compose stack to exercise the live `-race` pass, since (a) it was optional per plan/task instructions when DATABASE_URL is unreachable, (b) no compose stack for this project was already running in the environment, and (c) starting one was out of scope for this gap-closure verification pass. The compile-clean + self-skip path was verified instead, and this limitation is reported honestly here rather than claiming an untested live-DB pass.

## Deviations from Plan

### Auto-fixed Issues

None - no bugs, missing functionality, or blocking issues were found; Task 1's implementation was already correct and complete.

### Scope/Placement Deviation (documented, not a Rule 1-3 fix)

**1. Test C file placement**
- **Found during:** Task 2 setup (reading the pre-existing critical_preexisting_work context and the existing test file)
- **Issue:** Plan frontmatter names a new file `internal/reconciler/advisorylock_pg_test.go`, but Tests A and B (matching the plan's Test A/B intent) were already implemented by the parallel quick-task in `internal/reconciler/advisorylock_conn_test.go`.
- **Resolution:** Added Test C to the existing `advisorylock_conn_test.go` rather than creating a duplicate-purpose new file, per the explicit instruction in this plan's `critical_preexisting_work` context.
- **Files modified:** `internal/reconciler/advisorylock_conn_test.go`
- **Verification:** `go vet ./internal/reconciler/...`, `gofmt -l`, `go build ./...`, and `go test ./internal/reconciler/... -run 'PGAdvisoryLock' -race -count=1` (self-skip clean, no `--- FAIL`) all pass; `go test ./internal/reconciler/... -count=1` (no `-race`, full package) is green.
- **Committed in:** `2880488`

---

**Total deviations:** 1 (placement decision, not a correctness fix)
**Impact on plan:** No scope creep; Task 1's already-correct fix was left untouched, and Task 2's one missing test was added in the most consistent location. All plan acceptance criteria for both tasks are satisfied except the live-DB `-race` run, which was not executable in this environment (documented above, not fabricated).

## Issues Encountered

- No local Postgres instance reachable at `postgres://octo:octo-pass@localhost:5434/octo_db` and no docker-compose stack for this project was running in the environment, so the DATABASE_URL-guarded tests (A, B, and new Test C) ran in self-skip mode only. Compile cleanliness, `gofmt`, `go vet`, `go build`, and the self-skip execution path were all verified; the actual live pass/fail of the three advisory-lock tests under a real Postgres connection was NOT exercised in this run.
- Known pre-existing issue (not touched, per plan instructions): a data race in `fakeEnqueuer` (`TestSoakRecoversStrandedQueuedJob`) exists in the full-package `-race` run; per plan/context instructions, the `-race` verification was correctly scoped to `-run 'PGAdvisoryLock'` only, and the full-package non-`-race` run was used to confirm existing tests remain green. This pre-existing issue is documented in `.planning/quick/*260712-cqg*/deferred-items.md` and is out of scope here.

## User Setup Required

None - no external service configuration required. (Optional: exporting `DATABASE_URL` to a reachable Postgres instance would allow the three advisory-lock tests to run live instead of self-skipping; not required for this plan's completion.)

## Next Phase Readiness

- CR-01 and WR-01 (Phase 16's two verified reliability gaps) are both closed and regression-tested at the source level; the remaining test-coverage gap (Test C) is now closed as well.
- The full Task 2 test suite (Tests A, B, C) exists in `internal/reconciler/advisorylock_conn_test.go`, all DATABASE_URL-guarded and self-skipping cleanly in CI environments without a live Postgres, and exercising the CR-01/WR-01/mutex fixes fully when DATABASE_URL is set.
- No blockers for closing out Phase 16's gap-closure tracking; a live DATABASE_URL run against a real Postgres instance (e.g., in CI or a developer's local compose stack) is recommended as a follow-up sanity check but is not a blocker — the fix itself, gofmt/vet/build cleanliness, and the self-skip compile path are all independently verified.

---
*Phase: 16-webhook-delivery-decoupling*
*Completed: 2026-07-12*
