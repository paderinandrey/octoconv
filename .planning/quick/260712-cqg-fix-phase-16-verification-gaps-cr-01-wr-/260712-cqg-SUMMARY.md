---
phase: quick-260712-cqg
plan: 01
subsystem: infra
tags: [postgres, pgxpool, advisory-lock, graceful-shutdown, reconciler, webhook-worker]

# Dependency graph
requires:
  - phase: 16-webhook-delivery-decoupling
    provides: PGAdvisoryLock / fleet-wide sweeper election (D-01/D-02) and webhook-worker binary
provides:
  - "TryAcquire error path reclaims its pgxpool slot on every transient Postgres fault (CR-01 closed)"
  - "PGAdvisoryLock.Close(), wired into webhook-worker shutdown so SIGTERM exits in bounded time (WR-01 closed)"
  - "sync.Mutex guarding TryAcquire/Close against the shutdown-time goroutine race"
  - "DATABASE_URL-gated regression tests proving both fixes (skip cleanly without a DB)"
affects: [webhook-delivery-decoupling, reconciler, deployment/graceful-shutdown]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Dedicated pgxpool.Conn lifecycle: hard-close AND Release() are complementary on the error path — hard-close releases the Postgres session lock immediately, Release() reclaims the pgxpool slot (previously only the former was done, leaking the slot)."
    - "defer ordering for LIFO shutdown sequencing: register a resource's release defer AFTER a dependent resource's close defer so it runs first"

key-files:
  created:
    - internal/reconciler/advisorylock_conn_test.go
  modified:
    - internal/reconciler/reconciler.go
    - cmd/webhook-worker/main.go

key-decisions:
  - "A sync.Mutex guards the full TryAcquire body (not just the l.conn assignment) because Close() may run concurrently at shutdown mid-query; contention is near-zero (once per sweep interval vs. once at shutdown), so holding the lock across the query is the minimal correct fix."
  - "Test A polls pool.Stat().AcquiredConns() with a bounded 2s timeout instead of a single synchronous check, because puddle/v2's Resource.Destroy() (reached via Release() on an already-closed conn) reclaims the pool slot on a background goroutine, not synchronously before Release() returns."

requirements-completed: [WEBH-01]

# Metrics
duration: 12min
completed: 2026-07-12
---

# Quick Task 260712-cqg: Advisory-Lock Connection Lifecycle Fix Summary

**Fixed the dedicated Postgres advisory-lock connection's write-only lifecycle: TryAcquire's error path now Release()s the pgxpool slot it hard-closes (CR-01), and a new PGAdvisoryLock.Close() wired into webhook-worker shutdown bounds SIGTERM exit time instead of hanging inside pool.Close() (WR-01), both guarded by a sync.Mutex against the shutdown-time goroutine race.**

## Performance

- **Duration:** ~12 min (commits 09:15 → 09:27 local)
- **Started:** 2026-07-12T06:15:13Z
- **Completed:** 2026-07-12T06:26:34Z
- **Tasks:** 2/2 completed
- **Files modified:** 3 (2 modified, 1 created)

## Accomplishments
- CR-01 closed: `TryAcquire`'s query-error path now `Release()`s the dedicated `pgxpool.Conn` after hard-closing it, reclaiming the pgxpool slot on every transient Postgres fault instead of leaking it permanently.
- WR-01 closed: `PGAdvisoryLock.Close()` added and wired into `webhook-worker`'s shutdown sequence via `defer lock.Close()` registered after `defer pool.Close()`, so under LIFO it runs first and `pool.Close()` no longer blocks forever on the never-released advisory-lock connection.
- Shutdown-time data race on `l.conn` (Close() vs. a final in-flight TryAcquire from the sweeper goroutine) eliminated with a `sync.Mutex` held across the whole `TryAcquire` body.
- Added `internal/reconciler/advisorylock_conn_test.go` with two `DATABASE_URL`-gated tests proving both fixes via `pool.Stat().AcquiredConns()`; verified live against a throwaway `postgres:18` Docker container that Test A genuinely regresses (fails) against the pre-fix code path and passes against the fix.

## Task Commits

Each task was committed atomically:

1. **Task 1: Fix advisory-lock connection lifecycle (CR-01 Release, WR-01 Close, mutex guard)** - `1f8b22b` (fix)
2. **Task 2: DATABASE_URL-gated test proving pool-slot reclaim and Close release** - `4d47f30` (test)

**Plan metadata:** committed separately by the orchestrator (this executor does not commit docs artifacts per its constraints)

## Files Created/Modified
- `internal/reconciler/reconciler.go` - Added `sync.Mutex` field to `PGAdvisoryLock`; `TryAcquire` now holds the mutex across its whole body and its error path calls `conn.Release()` after `conn.Conn().Close(ctx)`; added `Close()` (idempotent, nil-guarded, mutex-guarded)
- `cmd/webhook-worker/main.go` - Added `defer lock.Close()` immediately after `NewPGAdvisoryLock` succeeds, registered after `defer pool.Close()` so it runs first under LIFO
- `internal/reconciler/advisorylock_conn_test.go` - New: `TestPGAdvisoryLockReleasesSlotOnError` (CR-01 gate) and `TestPGAdvisoryLockCloseReleasesSlot` (WR-01 gate + idempotency), both `DATABASE_URL`-gated via the existing `newSoakTestPool` helper

## Decisions Made
- Held the mutex across the entire `TryAcquire` body (lazy re-acquire + query + error path), not just around the `l.conn` pointer swap, per the plan's explicit concurrency-decision rationale: `Close()` must not `Release()` mid-query, and contention is negligible.
- Test polling instead of a single synchronous assertion for `AcquiredConns()` after the error-path `Release()` — this was discovered live (see Deviations) rather than assumed from the plan.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test A needed to poll `AcquiredConns()` instead of asserting synchronously**
- **Found during:** Task 2, live verification against a throwaway `postgres:18` Docker container
- **Issue:** The plan's literal test design (single synchronous `pool.Stat().AcquiredConns()` check immediately after `TryAcquire`'s error return) is flaky/fails even against the *fixed* code: `pgx/v5/pgxpool.Conn.Release()` on an already-closed connection routes through `puddle/v2`'s `Resource.Destroy()`, which reclaims the pool slot on a background goroutine (`go res.pool.destroyAcquiredResource(res)`), not synchronously before `Release()` returns. A live run against the fixed code failed with `AcquiredConns = 1, want 0`.
- **Fix:** Added a `waitAcquiredConns` helper that polls `pool.Stat().AcquiredConns()` up to a bounded 2s deadline (10ms interval) instead of a single check. Applied to both new tests for consistency (Test B's path is synchronous in practice — healthy-conn `Release()` — but polling is harmless and future-proof).
- **Files modified:** `internal/reconciler/advisorylock_conn_test.go`
- **Verification:** Re-ran against the same live Postgres container: both tests pass in ~0.1s each; confirmed `TestPGAdvisoryLockReleasesSlotOnError` genuinely fails (`AcquiredConns` never reaches baseline within the timeout) when manually re-testing an isolated revert of just the `Release()` line, proving the test is a real regression gate for CR-01.
- **Committed in:** `4d47f30` (part of Task 2's single test-file commit — the polling fix was applied before commit, not as a separate follow-up commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — test correctness bug, not production code)
**Impact on plan:** No scope creep. The production code changes (Task 1) exactly match the plan's spec; only the Task 2 test's assertion mechanism needed correction to account for `pgxpool`/`puddle` async-destroy semantics that the plan's interface notes did not anticipate.

## Issues Encountered

During live verification (not part of the committed deliverable), an ad-hoc attempt to reproduce the CR-01 regression by temporarily reverting `TryAcquire`'s error path back to "hard-close only, no `Release()`" and re-running `go test -race` hit an unrelated ~230s hang, eventually crashed via `SIGQUIT` stack dump. This was caused by double-closing an already-closed `pgx.Conn` in the test's manual fault-injection step interacting with the reverted code's own `Conn().Close(ctx)` call — not a bug in the shipped fix. The working tree was restored byte-for-byte to the committed fixed version (verified with `diff`, zero delta) immediately after. This exploration is out of scope and not reflected in any commit.

Separately, `go test ./internal/reconciler/... -race -count=1` (full package, not scoped to the new tests) fails on a **pre-existing** data race in `fakeEnqueuer.EnqueueImageConvert` / `reconciler_soak_test.go`'s polling loop (`internal/reconciler/reconciler_test.go:94` vs. `reconciler_soak_test.go:100`), unrelated to this plan's files (verified via `git diff` against the pre-plan commit: neither file was touched). Logged to `deferred-items.md` rather than fixed, per the executor's scope-boundary rule. The plan's actual required command, `go test ./internal/reconciler/... -count=1` (no `-race`), and the Task 2 `-run TestPGAdvisoryLock -race` scoped verify both pass cleanly.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

CR-01 and WR-01 are closed; the pre-production `webhook-worker` binary's advisory-lock connection lifecycle is now fully accounted for (acquire → error-path release → shutdown release), and SIGTERM leads to bounded-time process exit. The pre-existing `fakeEnqueuer` `-race` issue (see `deferred-items.md`) is a small, isolated follow-up candidate (guard the fake's call counters with a mutex) but does not block anything — it only surfaces under a full-package `-race` run, and the codebase's normal test invocation (`go test ./...`, no `-race`) is unaffected.

---
*Phase: quick-260712-cqg*
*Completed: 2026-07-12*

## Self-Check: PASSED

All created/modified files verified present on disk and both task commits
(`1f8b22b`, `4d47f30`) verified present in `git log --oneline --all`.
