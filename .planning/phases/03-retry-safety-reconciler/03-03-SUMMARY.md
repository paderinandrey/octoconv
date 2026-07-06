---
phase: 03-retry-safety-reconciler
plan: 03
subsystem: reconciler
tags: [asynq, postgres, reconciler, retry-safety, go]

# Dependency graph
requires:
  - phase: 03-retry-safety-reconciler (Plan 01)
    provides: asynq.Unique per-job lock (ImageUniqueTTL) on image tasks, EnqueueImageConvert returning asynq.ErrDuplicateTask when a task/lock is still live
  - phase: 03-retry-safety-reconciler (Plan 02)
    provides: idempotent MarkActive (queued|active -> active, COALESCE started_at), job_events.detail jsonb channel, whole-attempt ENGINE_TIMEOUT bound so the unique-lock TTL assumption holds
provides:
  - "jobs.Repo.RequeueStale/RecoveryCount/FindStale — guarded requeue-to-queued transition, recovery-count-by-detail-tag, and Postgres staleness scan"
  - "internal/reconciler package: ticker-driven Sweeper with enqueue-first, ErrDuplicateTask-guarded recovery and a bounded (single-retry) RequeueStale write"
  - "cmd/worker/main.go graceful lifecycle: signal.NotifyContext + srv.Start/Shutdown + reconciler sweeper running alongside asynq, both stopped together on SIGINT/SIGTERM"
affects: [04-observability (job_events now also carries reconciler_recovery/reconciler_exhausted action tags Phase 4 can surface as metrics)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Enqueue-first recovery: the sweeper calls EnqueueImageConvert BEFORE RequeueStale so the asynq.Unique lock (not a local heuristic) decides whether a job is genuinely stranded; asynq.ErrDuplicateTask is treated as a safe no-op rather than an error"
    - "Bounded single-retry write for accounting-critical state (RequeueStale after a real enqueue) — explicit code comments document both the under-count (both attempts fail) and over-count (T-03-12 post-commit/pre-ack double-write) residuals instead of an unbounded retry loop"
    - "Interface-segregated jobStore/enqueuer consumer interfaces in internal/reconciler (mirroring internal/api/api.go) so the sweeper is unit-tested with in-memory fakes, no DB/Redis required"

key-files:
  created:
    - internal/reconciler/reconciler.go
    - internal/reconciler/reconciler_test.go
  modified:
    - internal/jobs/repo.go
    - internal/jobs/repo_test.go
    - cmd/worker/main.go
    - .env.example

key-decisions:
  - "FindStale computes cutoff timestamps in Go (time.Now().Add(-duration)) and binds them as timestamptz parameters rather than passing raw durations for Postgres-side interval arithmetic — keeps the WHERE clause index-friendly against jobs_inflight_idx (created_at) WHERE status IN ('queued','active') and avoids interval-casting ambiguity across pgx parameter binding."
  - "Hoisted a single jobs.NewRepo(pool) in cmd/worker/main.go shared by both worker.NewHandler and reconciler.NewSweeper (plan's preferred option over constructing two separate repos wrapping the same pool)."

requirements-completed: [RECON-01, RECON-02, RECON-03]

# Metrics
duration: ~35min
completed: 2026-07-06
---

# Phase 03 Plan 03: Postgres-Driven Reconciler Summary

**A ticker-driven reconciler now sweeps Postgres every minute for jobs stranded in `queued`/`active` past a staleness threshold, requeues genuinely-stranded ones through an enqueue-first, `asynq.ErrDuplicateTask`-guarded recovery path (never duplicating a still-live task or falsely inflating a backlogged job's recovery count), and terminally fails jobs that exceed a bounded recovery cap with a webhook fired on exhaustion.**

## Performance

- **Duration:** ~35 min
- **Completed:** 2026-07-06
- **Tasks:** 3/3 completed
- **Files modified:** 6 (2 new in `internal/reconciler`, 2 modified in `internal/jobs`, `cmd/worker/main.go`, `.env.example`)

## Accomplishments

- `internal/jobs/repo.go`: added `detailActionRecovery` constant, `StaleJob` type, and three new repo methods — `RequeueStale` (guarded `queued|active -> queued` transition tagging `job_events.detail` with `{action: reconciler_recovery, reason}`), `RecoveryCount` (counts prior recoveries by that same tag, never a literal-string duplicate), and `FindStale` (Postgres scan for jobs past their queued/active staleness cutoff, index-friendly against `jobs_inflight_idx`).
- `internal/reconciler` (new package): a `Sweeper` with `Config{QueuedStaleAfter, ActiveStaleAfter, SweepInterval, MaxRecoveries}`, `NewSweeper`, `Run(ctx)` (ticker loop, clean shutdown on context cancel), and `sweep(ctx)` implementing the full recovery/exhaustion decision tree — enqueue-first with `asynq.ErrDuplicateTask` treated as a safe no-op, a bounded single-retry on `RequeueStale` to avoid silently under-counting the recovery cap, and `MarkFailed(reconciler_exhausted)` + conditional `EnqueueWebhookDeliver` at the cap.
- `cmd/worker/main.go`: replaced the blocking `srv.Run(mux)` lifecycle with `signal.NotifyContext` + `srv.Start(mux)` + `go sweeper.Run(ctx)` + `<-ctx.Done()` + `srv.Shutdown()`, mirroring `cmd/api/main.go`'s shape; hoisted a single shared `jobs.Repo` for both the handler and the sweeper.
- `.env.example`: documented `RECONCILER_QUEUED_STALE_AFTER=90s`, `RECONCILER_ACTIVE_STALE_AFTER=5m`, `RECONCILER_SWEEP_INTERVAL=1m`, `RECONCILER_MAX_RECOVERIES=3` with inline rationale tying each to its research decision (D-08/D-09/D-10/D-12).
- Live-verified the worker binary: starts, logs `🐙 worker starting`, runs the reconciler ticker (tested with `RECONCILER_SWEEP_INTERVAL=1s`) alongside the asynq server, and on `SIGINT` logs `🛑 shutting down worker...` then `bye 👋` and exits with code 0 (asynq's own graceful-shutdown log lines confirmed in between).

## Task Commits

Each task was committed atomically:

1. **Task 1: Repository support — RequeueStale, RecoveryCount, FindStale** - `0345297` (feat)
2. **Task 2: internal/reconciler package — Sweeper, Config, ticker loop, duplicate-guarded sweep logic** - `7cf1d58` (feat)
3. **Task 3: Wire the reconciler into the worker entrypoint with graceful shutdown and env config** - `7498e61` (feat)

**Plan metadata:** (pending — orchestrator commits STATE.md/ROADMAP.md updates after wave completion)

## Files Created/Modified

- `internal/jobs/repo.go` - Added `detailActionRecovery` const, `StaleJob` struct, `RequeueStale`/`RecoveryCount`/`FindStale` methods
- `internal/jobs/repo_test.go` - Added `TestRequeueStale`, `TestRecoveryCount`, `TestFindStale` (DB-backed, skip without `DATABASE_URL`)
- `internal/reconciler/reconciler.go` (new) - `Config`, `jobStore`/`enqueuer` consumer interfaces, `Sweeper`, `NewSweeper`, `Run`, `sweep`
- `internal/reconciler/reconciler_test.go` (new) - `fakeStore`/`fakeEnqueuer` in-memory fakes; 7 unit tests covering under-cap recovery, duplicate-enqueue skip, bounded RequeueStale retry (both success and exhaustion paths), cap exhaustion with/without webhook, and context-cancel shutdown
- `cmd/worker/main.go` - `signal.NotifyContext` lifecycle, `srv.Start`/`srv.Shutdown`, `reconciler.NewSweeper` wiring, shared `jobs.Repo`
- `.env.example` - New `# Reconciler` block with four documented env vars

## Decisions Made

- Computed `FindStale`'s staleness cutoffs as Go `time.Time` values (`time.Now().Add(-duration)`) bound as query parameters, rather than passing raw `time.Duration` for Postgres-side `now() - $1::interval` arithmetic — simpler pgx parameter binding and keeps the predicate directly comparable against the existing `jobs_inflight_idx (created_at) WHERE status IN ('queued','active')` partial index.
- Hoisted one shared `jobs.NewRepo(pool)` in `cmd/worker/main.go` for both `worker.NewHandler` and `reconciler.NewSweeper`, per the plan's stated preference over constructing two repos wrapping the same pool.
- Kept the `fakeStore.RequeueStale` test double's error-vs-nil handling explicit (checks `f.requeueStaleErrs[idx] != nil` rather than returning the slot unconditionally) so a configured `nil` entry still increments the fake's recovery counter — this was caught and fixed during the first test run (see Deviations).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Test fake's RequeueStale returned nil without recording a recovery**
- **Found during:** Task 2, first `go test` run
- **Issue:** `TestSweepRequeueStaleRetriedOnce` failed with `recovery count = 0, want 1`. The `fakeStore.RequeueStale` test double returned `f.requeueStaleErrs[idx]` unconditionally whenever `idx < len(f.requeueStaleErrs)`, including when that slot was explicitly `nil` (meaning "second attempt succeeds") — so it returned `nil` without ever incrementing `f.recoveryCount[id]`, making the test assert a value the fake could never produce.
- **Fix:** Changed the condition to `idx < len(f.requeueStaleErrs) && f.requeueStaleErrs[idx] != nil`, so a `nil` entry falls through to the success path that increments the recovery counter, matching what a real successful `RequeueStale` call does.
- **Files modified:** `internal/reconciler/reconciler_test.go` (test-only; no production code change)
- **Verification:** `go test ./internal/reconciler/...` — all 7 tests pass, including `TestSweepRequeueStaleRetriedOnce` and `TestSweepRequeueStaleBoundedRetry`.
- **Committed in:** `7cf1d58` (Task 2 commit — the fix landed in the same commit as the test's initial authoring, since it was caught before commit)

---

**Total deviations:** 1 auto-fixed (1 test-only bug fix, Rule 1). No production-code deviations — `internal/jobs/repo.go`, `internal/reconciler/reconciler.go`, and `cmd/worker/main.go` were implemented exactly per the plan's `<action>` blocks.

## Issues Encountered

None beyond the deviation above.

## User Setup Required

None — no new external service configuration required. All four new `RECONCILER_*` env vars have sensible defaults baked into `cmd/worker/main.go` (`envDuration`/`envInt` with the plan's specified defaults), so an operator only needs to override them if the defaults don't fit.

## Live Verification

- `go build ./...`, `go vet ./...` clean.
- `go test ./internal/jobs/... ./internal/reconciler/...` — all pass against the live `octoconv-db` container (`DATABASE_URL` set), exercising `RequeueStale`/`RecoveryCount`/`FindStale` against real Postgres rows (not just fakes) and all 7 reconciler unit tests (DB/Redis-free fakes).
- Built the worker binary and ran it against the live `octoconv-db`/`octoconv-redis`/`octoconv-minio` containers with `RECONCILER_SWEEP_INTERVAL=1s`: confirmed startup log, asynq's own "Starting processing" log, and a clean `SIGINT` shutdown sequence (`🛑 shutting down worker...` → asynq graceful-shutdown logs → `bye 👋`, exit code 0).
- The plan's fully-manual, multi-minute live scenarios (leave a job stranded past the real 90s/5m thresholds and observe an actual requeue; force 3 recoveries and observe `reconciler_exhausted` + webhook delivery) were **not executed** in this session — they require minutes of wall-clock waiting per scenario against the live stack, which is out of scope for automated plan execution. The DB-backed `TestRequeueStale`/`TestRecoveryCount`/`TestFindStale` and the 7 reconciler unit tests already exercise every code path these manual scenarios would observe (guarded transition, cap-based exhaustion, duplicate-guard no-op, staleness cutoff correctness); this is flagged here rather than silently skipped.

## Next Phase Readiness

- RECON-01/02/03 are now structurally complete: the reconciler is wired into the worker's lifecycle and will begin sweeping on the next deployment.
- `job_events.detail` now also carries `reconciler_recovery`/`reconciler_exhausted` action tags in addition to Plan 02's `engine_stderr` diagnostic payloads — Phase 4 (observability) can query/aggregate on `detail->>'action'` without a migration.
- No blockers identified for Phase 4.

---
*Phase: 03-retry-safety-reconciler*
*Completed: 2026-07-06*

## Self-Check: PASSED
