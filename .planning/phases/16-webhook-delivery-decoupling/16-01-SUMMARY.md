---
phase: 16-webhook-delivery-decoupling
plan: 01
subsystem: infra
tags: [postgres, pgxpool, advisory-lock, asynq, reconciler, leader-election]

# Dependency graph
requires: []
provides:
  - "AdvisoryLock interface (TryAcquire(ctx) (bool, error)) in internal/reconciler"
  - "PGAdvisoryLock: dedicated pgxpool.Conn, session-level pg_try_advisory_lock impl, fail-safe on error (hard-closes suspect conn instead of Release())"
  - "Sweeper.RunWithLock: lock-gated tick loop — sweeps only when TryAcquire returns (true, nil)"
affects: [16-02-webhook-worker-binary, 16-04-live-verification]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Dedicated long-lived pgxpool.Conn (never Release()'d) for Postgres session-scoped state, held outside the shared repo pool"
    - "Fail-safe-closed lock gating: any TryAcquire error or false result skips the guarded action rather than proceeding unguarded"
    - "Hard-close (Conn().Close(ctx)) instead of Release() when a pooled connection may still be holding session state that must not silently outlive process intent"

key-files:
  created:
    - internal/reconciler/advisorylock_test.go
  modified:
    - internal/reconciler/reconciler.go

key-decisions:
  - "Advisory lock is session-level (pg_try_advisory_lock) on a single dedicated pgxpool.Conn acquired once and never released for process life (D-02) — ties lock release to process/session death, not pool recycling"
  - "TryAcquire's query/scan-error path hard-closes the suspect connection via Conn().Close(ctx), never Release() — a suspect conn must never re-enter the shared pool while possibly still holding the session lock (WR-2)"
  - "RunWithLock is a new method alongside the existing Run — NewSweeper/Run/sweep signatures are untouched so all existing sweep/soak tests keep passing unmodified"
  - "Zero new Go module dependency — pgxpool was already a direct dependency via github.com/jackc/pgx/v5"

patterns-established:
  - "AdvisoryLock interface + concrete PGAdvisoryLock impl, unit-tested via a fakeLock (no live Postgres needed) — live pg_try_advisory_lock behavior is deferred to Plan 16-04's e2e verification"

requirements-completed: [WEBH-01]

# Metrics
duration: 5min
completed: 2026-07-12
---

# Phase 16 Plan 01: Reconciler Advisory-Lock Gate Summary

**Postgres session-level advisory-lock (`pg_try_advisory_lock` on a dedicated `pgxpool.Conn`) added to `internal/reconciler` so exactly one webhook-worker replica sweeps at a time, fail-safe-closed on any lock-check error.**

## Performance

- **Duration:** ~5 min (commit-to-commit)
- **Started:** 2026-07-12T00:14:40+03:00 (Task 1 commit)
- **Completed:** 2026-07-12T00:16:13+03:00 (Task 2 commit)
- **Tasks:** 2
- **Files modified:** 2 (1 modified, 1 created)

## Accomplishments
- Added `AdvisoryLock` interface (`TryAcquire(ctx) (bool, error)`) and `PGAdvisoryLock` — a dedicated-connection, session-level `pg_try_advisory_lock` implementation — to `internal/reconciler/reconciler.go`
- Added `Sweeper.RunWithLock(ctx, lock)`: a new tick loop, structurally identical to the existing `Run`, that gates each `sweep(ctx)` call on `lock.TryAcquire` — sweeps only on `(true, nil)`, skips on `(false, nil)` or any error
- Implemented the fail-safe hard-close: on a `pg_try_advisory_lock` query/scan error, the suspect connection is destroyed via `Conn().Close(ctx)` (not `Release()`), preventing a possibly-still-locked connection from re-entering the shared pool
- Unit-tested the full gate matrix with a fake `AdvisoryLock` (`fakeLock`): sweeps on acquired, skips on not-acquired, fails safe (skips) on lock-check error, and stops cleanly on context cancel
- Left `NewSweeper`/`Run`/`sweep` untouched — all 14 pre-existing tests in `internal/reconciler/reconciler_test.go` and the soak test continue to pass unmodified
- Confirmed zero new Go module dependency (`pgxpool` was already a direct dependency of `github.com/jackc/pgx/v5`)

## Task Commits

Each task was committed atomically:

1. **Task 1: Add AdvisoryLock interface, PGAdvisoryLock impl, and RunWithLock to the reconciler** - `ccd14f9` (feat)
2. **Task 2: Unit-test the lock gating (sweeps only when acquired, fail-safe on error)** - `080f382` (test)

_No plan-metadata commit yet — orchestrator handles STATE.md/ROADMAP.md updates after the wave completes._

## Files Created/Modified
- `internal/reconciler/reconciler.go` - Added `advisoryLockKey` const, `AdvisoryLock` interface, `PGAdvisoryLock` struct + `NewPGAdvisoryLock`/`TryAcquire`, and `Sweeper.RunWithLock`. `NewSweeper`, `Run`, `sweep` unchanged.
- `internal/reconciler/advisorylock_test.go` - New file: `fakeLock` fake + 4 subtests proving the gate (acquired→sweeps, not-acquired→skips, lock-error→fail-safe skip, context-cancel→stops)

## Decisions Made
- **Dedicated connection lifecycle:** `NewPGAdvisoryLock` acquires one `*pgxpool.Conn` via `pool.Acquire(ctx)` at construction and never calls `Release()` on it during process life — the session (and thus the lock) is tied to process death, matching D-02's auto-failover requirement. On a lazy re-acquire path (`l.conn == nil`), a fresh connection is drawn from the pool on the next tick.
- **WR-2 hard-close on error:** `TryAcquire`'s error branch calls `l.conn.Conn().Close(ctx)` rather than `l.conn.Release()`. Rationale documented inline: a plain `Release()` would return a still-protocol-healthy connection to the shared pool while it might still hold the Postgres session-level advisory lock, silently blocking every replica from ever becoming leader until `MaxConnLifetime` recycles that connection — an unbounded, hard-to-diagnose outage. Hard-closing forces Postgres to release the session lock immediately.
- **Test strategy — indirect "sweep ran" signal:** Rather than adding a call-counter to the existing `fakeStore.FindStale` in `reconciler_test.go` (which the plan does not list as a file to modify), the advisory-lock tests observe `fakeEnqueuer.imageCalls` as the "sweep ran" signal — this is the same signal `reconciler_test.go`'s own `TestSweepRecoversUnderCap` etc. already use, so it is consistent with the existing test suite's idiom and required zero changes to `reconciler_test.go`.
- **Live Postgres deferred:** No live-Postgres advisory-lock test was added here — the plan explicitly scopes that to Plan 16-04's e2e verification (SC3: exactly one of two live webhook-workers holds the lock). This plan proves the gate logic in isolation via the `AdvisoryLock` interface + fake.

## Deviations from Plan

None - plan executed exactly as written. Both tasks' acceptance criteria (build green, `pg_try_advisory_lock` present, `RunWithLock`/`NewPGAdvisoryLock` signatures present, `NewSweeper` signature unchanged, no go.mod/go.sum diff, hard-close-not-Release on the error path, ≥3 gate subtests with the error case asserting no sweep, full package test suite green, `go vet` clean) were verified directly and passed without needing any auto-fix.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. This plan is pure Go code; no database migration, no new env vars, no infrastructure change (the dedicated advisory-lock connection is wired up by the caller in Plan 16-02's `cmd/webhook-worker`, not here).

## Next Phase Readiness
- `AdvisoryLock`/`PGAdvisoryLock`/`RunWithLock` are ready for Plan 16-02 to wire into `cmd/webhook-worker/main.go`: construct `pool` via `db.Connect`, build `reconciler.NewPGAdvisoryLock(ctx, pool)`, and call `go sweeper.RunWithLock(ctx, lock)` instead of `go sweeper.Run(ctx)`.
- No blockers. Live pg_try_advisory_lock correctness (auto-failover on leader death, exactly-one-holder under real concurrency) is unverified by this plan and is explicitly the responsibility of Plan 16-04's live e2e (SC3).

---
*Phase: 16-webhook-delivery-decoupling*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: internal/reconciler/reconciler.go
- FOUND: internal/reconciler/advisorylock_test.go
- FOUND: .planning/phases/16-webhook-delivery-decoupling/16-01-SUMMARY.md
- FOUND commit: ccd14f9
- FOUND commit: 080f382
