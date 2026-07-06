---
phase: 03-retry-safety-reconciler
verified: 2026-07-06T19:38:24Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
---

# Phase 3: Retry-Safety & Reconciler Verification Report

**Phase Goal:** The worker correctly distinguishes transient from terminal failures so asynq retry actually functions, and jobs stranded by infrastructure hiccups (lost enqueue, crashed worker) are automatically recovered without duplicating work.
**Verified:** 2026-07-06T19:38:24Z (against `main` @ `83b17b4`)
**Status:** passed
**Re-verification:** No — initial verification

## Method

This verification did not rely on SUMMARY.md claims. For each success criterion I: (1) read the actual implementation in `internal/queue/queue.go`, `internal/queue/client.go`, `internal/worker/worker.go`, `internal/jobs/repo.go`, `internal/reconciler/reconciler.go`, `cmd/worker/main.go`; (2) ran the existing unit/DB test suites myself against the live `octoconv-db` (Postgres, :5434) and `octoconv-redis` (:6379) containers; (3) additionally wrote and ran two throwaway, non-committed live integration tests directly exercising `reconciler.Sweeper.sweep` against real Postgres rows and a real asynq/Redis client — to close the gap 03-03-SUMMARY.md explicitly flagged ("the plan's fully-manual, multi-minute live scenarios... were not executed in this session"). Those temporary test files were deleted after running (working tree is clean — verified with `git status`).

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A transient conversion failure (network/timeout) triggers a real asynq retry rather than being marked terminally failed after one attempt | ✓ VERIFIED | `internal/worker/worker.go:128-145` — `HandleImageConvert` returns the raw (unwrapped) error on any non-terminal `process()` failure, doing nothing else (no `MarkFailed`), so asynq applies its own retry. This requires `MarkActive` to tolerate re-entry, which it does: `internal/jobs/repo.go:104-110` widens `allowedFrom` to `[]string{StatusQueued, StatusActive}` with `started_at = COALESCE(started_at, now())`. The image queue gets its own fast, bounded schedule (`imageRetrySchedule = 2s/5s/15s`, `IMAGE_MAX_RETRY` default 4) via a queue-aware `RetryDelayFunc` dispatcher (`internal/queue/queue.go:136-175`) wired into `cmd/worker/main.go:85` (`RetryDelayFunc: queue.RetryDelayFunc`, confirmed by reading the file — no `queue.WebhookRetryDelay` remains in the file). Unit tests `TestImageRetryDelaySchedule`, `TestRetryDelayFuncDispatch`, `TestMarkActiveIdempotentReentry`, and `TestIsTerminalTransientDefault` all pass (re-run live, not just trusted from SUMMARY). |
| 2 | A terminal conversion failure (invalid input, unsupported format) marks the job failed immediately without wasted retries | ✓ VERIFIED | `isTerminal(err)` (`internal/worker/worker.go:40-65`) classifies storage-404 (`minio.NoSuchKey`, correctly unwrapped via `errors.As` through the `%w` chain — verified this is necessary and correctly done, since `internal/storage` always wraps minio errors), `"no converter for"` registry misses, and three verified vips terminal stderr signatures as terminal. On a terminal classification, `HandleImageConvert` calls `MarkFailed` with a short sanitized message ("unsupported or corrupted input format") — raw stderr goes only into `job_events.detail`, never `error_message` (confirmed by reading the code: `grep -c 'MarkFailed(ctx, jobID, "engine_error", err.Error())'` returns 0) — then returns an `asynq.SkipRetry`-wrapped error, so asynq does not retry. Re-ran `TestIsTerminalStorageNoSuchKey`, `TestIsTerminalNoConverter`, `TestIsTerminalVipsSignatures` — all pass. |
| 3 | A job stuck in `queued` with no corresponding task in the asynq queue is automatically re-enqueued exactly once — no duplicate tasks | ✓ VERIFIED | `internal/reconciler/reconciler.go` sweep logic calls `EnqueueImageConvert` BEFORE `RequeueStale` (enqueue-first ordering, confirmed by reading the code), and every image task carries a per-job `asynq.Unique(uniqueTTL)` lock (`internal/queue/queue.go:56-60`) so a second enqueue for a job whose task/lock is still live returns `asynq.ErrDuplicateTask`, which sweep explicitly treats as a no-op (`errors.Is(err, asynq.ErrDuplicateTask) { continue }`, no status change, no recovery event). I wrote and ran a throwaway live test (`TestLive_SweepRecoversTrulyStrandedActiveJob`, deleted after running) against real Postgres + Redis: a genuinely stranded job was recovered exactly once (`recovery_count == 1`), and a second sweep against the SAME job (now holding a live task/lock from the first recovery's real enqueue) did NOT create a duplicate — `recovery_count` stayed at 1 and the job did not flip status again. This is real `asynq.ErrDuplicateTask` behavior against live Redis, not a mocked fake. Unit test `TestSweepSkipsDuplicateEnqueue` also passes. |
| 4 | A job stuck in `active` past a defined staleness threshold (worker crashed) is recovered without re-processing a job that is merely slow but healthy | ✓ VERIFIED | `FindStale` (`internal/jobs/repo.go:170-195`) scans `status='active' AND started_at < activeStaleAfter` (default 5m, configurable via `RECONCILER_ACTIVE_STALE_AFTER`, comfortably above `ENGINE_TIMEOUT`'s 120s default). `started_at` is pinned to first activation via `COALESCE` (Plan 02), so a job that keeps legitimately retrying doesn't get a moving staleness window. The "merely slow but healthy" guarantee is enforced by the same `asynq.Unique` + enqueue-first + `ErrDuplicateTask` mechanism verified in SC3 above — a job whose asynq task is still genuinely in flight (retrying or executing) is protected because its lock has not lapsed, so the reconciler's `EnqueueImageConvert` returns `ErrDuplicateTask` rather than creating a competing task. My live test confirmed this mechanism operates correctly against real Redis (not merely asserted by a plan comment). The plan's own soundness dependency — that the WHOLE conversion attempt (not just `conv.Convert()`) is bounded by a single `ENGINE_TIMEOUT` deadline so a stalled S3 transfer cannot outlive the lock — is also verified in code: `process()` creates one `attemptCtx` at the top and threads it through `inputKey`/`downloadTo`/`Convert`/`uploadFrom`/`AddOutput`/`MarkDone` (`internal/worker/worker.go:250-298`); no raw `ctx` is passed to any of these calls (confirmed by reading). |
| 5 | Every reconciler action (job recovered, job terminal-failed) is recorded in `job_events` | ✓ VERIFIED | `RequeueStale` and `MarkFailed` both route through the shared `transition()` helper, which always inserts a `job_events` row with a `detail` jsonb payload — recovery events tagged `{"action":"reconciler_recovery","reason":...}` (constant `detailActionRecovery`, referenced by both writer `RequeueStale` and reader `RecoveryCount` so there is no literal-string drift), exhaustion events tagged `{"action":"reconciler_exhausted"}`. Live-verified with a throwaway test (`TestLive_SweepExhaustsAtCap`, deleted after running): a job exceeding the recovery cap was marked `failed` with `error_code = reconciler_exhausted`, and the corresponding `job_events` row's `detail->>'action'` was confirmed as `reconciler_exhausted` via a direct SQL query against the real database. |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/queue/queue.go` | `imageRetrySchedule`, `ImageRetryDelay`, `RetryDelayFunc` dispatcher, `ImageUniqueTTL`, `NewImageConvertTask(jobID, maxRetry, uniqueTTL)` with `asynq.MaxRetry`+`asynq.Unique` | ✓ VERIFIED | All present, substantive (not stubs), matches plan spec exactly — read in full. |
| `internal/queue/client.go` | `imageMaxRetry`/`imageUniqueTTL` fields, `IMAGE_MAX_RETRY`/`ENGINE_TIMEOUT` env reads, `EnqueueImageConvert` signature unchanged | ✓ VERIFIED | Confirmed; `func (c *Client) EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error` signature unchanged (api.Enqueuer interface still compiles). |
| `cmd/worker/main.go` | `RetryDelayFunc: queue.RetryDelayFunc`, `signal.NotifyContext`, `srv.Start`/`srv.Shutdown`, `reconciler.NewSweeper` + `go sweeper.Run(ctx)` | ✓ VERIFIED | All present and wired; `srv.Run(mux)` fully replaced by non-blocking `srv.Start(mux)` + `<-ctx.Done()` + `srv.Shutdown()`. |
| `internal/jobs/repo.go` | Idempotent `MarkActive` (`COALESCE(started_at, now())`), `transition`/`MarkFailed` with `detail` param, `RequeueStale`/`RecoveryCount`/`FindStale`, `StaleJob` type | ✓ VERIFIED | All present, read in full; guarded transitions correctly go through the row-locked `transition()` helper — no ad-hoc UPDATEs found. |
| `internal/worker/worker.go` | `isTerminal` classifier, terminal/transient branching in `HandleImageConvert`, sanitized `error_message`, whole-attempt `attemptCtx` | ✓ VERIFIED | Confirmed exactly one `context.WithTimeout(ctx, h.engineTimout)` call, threaded through every I/O step; no raw-`ctx` calls to `downloadTo`/`uploadFrom` remain. |
| `internal/reconciler/reconciler.go` | `Sweeper`, `Config`, `NewSweeper`, `Run(ctx)` ticker loop, `sweep()` with duplicate-guard + cap + act | ✓ VERIFIED | New package, fully implemented per plan; `go vet`/`go build`/`go test` all pass; zero `log.` calls (D-15 respected). |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `cmd/worker/main.go` | `queue.RetryDelayFunc` | `asynq.Config.RetryDelayFunc` field | ✓ WIRED | Confirmed line 85. |
| `queue.Client.EnqueueImageConvert` | `queue.NewImageConvertTask` | passes `imageMaxRetry`/`imageUniqueTTL` | ✓ WIRED | Confirmed `client.go:51`. |
| `queue.NewImageConvertTask` | asynq unique-task lock | `asynq.Unique(uniqueTTL)` | ✓ WIRED | Confirmed `queue.go:59`; live-tested — real `asynq.ErrDuplicateTask` returned on collision. |
| `worker.HandleImageConvert` | `isTerminal` | classify before terminal/transient branch | ✓ WIRED | Confirmed `worker.go:129`. |
| `jobs.Repo.MarkActive` | jobs table | guarded transition allowing `queued\|active` | ✓ WIRED | Confirmed `repo.go:104-110`. |
| `worker.process()` | download/convert/upload/record | single `attemptCtx` threaded through all steps | ✓ WIRED | Confirmed by reading; no raw `ctx` remains on any I/O call inside `process()`. |
| `reconciler.sweep` | `jobs.Repo.RequeueStale`/`RecoveryCount`/`MarkFailed` | guarded transitions, cap check | ✓ WIRED | Confirmed and live-tested end to end. |
| `reconciler.sweep` | `queue.Client.EnqueueImageConvert`/`EnqueueWebhookDeliver` | enqueue-first recovery, webhook on exhaustion | ✓ WIRED | Confirmed and live-tested (webhook path verified via `CallbackURL != ""` branch reached in exhaustion test). |
| `reconciler.sweep` | `asynq.ErrDuplicateTask` | duplicate enqueue treated as no-op | ✓ WIRED | Confirmed in code AND live-tested against real Redis (not just a mock). |
| `cmd/worker/main.go` | `reconciler.Sweeper.Run` | `go sweeper.Run(ctx)` under `signal.NotifyContext` | ✓ WIRED | Confirmed line 92; SUMMARY 03-03 additionally reports a live SIGINT shutdown test with the compiled binary. |

### Data-Flow Trace (Level 4)

Not applicable in the traditional UI/API-response sense — this is backend reliability/queue-plumbing code, not a rendering surface. The equivalent check (does the reconciler's decision data — `FindStale`, `RecoveryCount` — come from real Postgres queries rather than static/mocked values feeding into the sweep's real actions) was verified directly: `FindStale`/`RecoveryCount`/`RequeueStale`/`MarkFailed` are real parameterized SQL queries against the live `jobs`/`job_events` tables (confirmed by reading `internal/jobs/repo.go`, and by observing real row changes in my live tests).

### Behavioral Spot-Checks / Live Verification (Step 7b, closing the SUMMARY-flagged gap)

| Behavior | Method | Result | Status |
|----------|--------|--------|--------|
| Genuinely stranded active job is recovered exactly once | Throwaway live test against real Postgres+Redis: create job, mark active, backdate `started_at`, call `sweep()` directly with a 1s active-staleness threshold | Job flipped to `queued`, `RecoveryCount == 1` | ✓ PASS |
| A job whose recovery already created a live task is NOT double-recovered on the next sweep | Same test, immediately re-swept with a 1s queued-staleness threshold | `RecoveryCount` stayed `1`, no second status flip — real `asynq.ErrDuplicateTask` from live Redis prevented the duplicate | ✓ PASS |
| A job exceeding the recovery cap is terminally failed with a webhook-eligible callback and a `job_events` audit row | Throwaway live test: 3 pre-seeded recovery events, `sweep()` with `MaxRecoveries=3` | Job status `failed`, `error_code=reconciler_exhausted`, matching `job_events` row with `detail->>'action' = 'reconciler_exhausted'` | ✓ PASS |
| Full existing test suites | `go test ./internal/reconciler/... ./internal/queue/... ./internal/worker/... ./internal/jobs/...` (re-run by me, live DB/Redis) | All pass (incl. `TestMarkActiveIdempotentReentry`, `TestRequeueStale`, `TestRecoveryCount`, `TestFindStale`, 7 reconciler unit tests, `isTerminal`/queue-dispatch tests) | ✓ PASS |
| Anti-pattern scan on all phase-3-modified files | `grep` for `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER`/empty-return stubs | No matches in any of the 11 modified/created files | ✓ PASS (no debt markers) |

Note: the multi-minute wall-clock live scenarios using the actual default thresholds (90s queued / 5m active / 1m sweep interval) that 03-03-SUMMARY.md flagged as not run were not literally reproduced at their real durations either — that would take 5+ minutes of wall-clock waiting with no additional verification value over calling `sweep()` directly with a shrunk threshold against the real Sweeper/Repo/queue.Client code (which is what I did). This exercises the exact same code path; only the ticker's wall-clock cadence itself (`time.NewTicker`/`Run`) was already covered by `TestRunStopsOnContextCancel` (context-cancel) and 03-03-SUMMARY.md's own live worker-binary run with `RECONCILER_SWEEP_INTERVAL=1s`. I consider this an acceptable substitution, not a gap.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| RELY-01 | 03-02 | Worker distinguishes transient vs. terminal errors | ✓ SATISFIED | `isTerminal` classifier + two-branch `HandleImageConvert` |
| RELY-02 | 03-01, 03-02 | Transient error → real asynq retry, not marked terminal after one attempt | ✓ SATISFIED | Idempotent `MarkActive`, queue-aware `RetryDelayFunc`, bounded `IMAGE_MAX_RETRY` |
| RECON-01 | 03-03 | Reconciler finds stranded `queued` jobs, re-enqueues idempotently, no duplicates | ✓ SATISFIED | `FindStale` + enqueue-first + `asynq.Unique`/`ErrDuplicateTask` guard, live-verified |
| RECON-02 | 03-03 | Reconciler finds stranded `active` jobs past threshold, doesn't duplicate a merely-slow-but-healthy job | ✓ SATISFIED | Staleness threshold on pinned `started_at`, same duplicate-guard mechanism, live-verified |
| RECON-03 | 03-03 | Reconciler actions recorded in `job_events` | ✓ SATISFIED | `RequeueStale`/`MarkFailed` route through `transition()`, live-verified |

**Note (informational, not a phase-3 gap):** `.planning/REQUIREMENTS.md` still shows `[ ]` / "Pending" for all five of these requirement IDs even though the code satisfies them and `ROADMAP.md` marks Phase 3 complete. This is a pre-existing project-wide documentation-sync gap, not specific to this phase — `WEBHOOK-01` (Phase 2, also marked complete in ROADMAP.md) shows the identical pattern of an unchecked REQUIREMENTS.md checkbox. It does not affect the phase-3 goal achievement and is not blocking, but the developer may want to reconcile REQUIREMENTS.md's checkboxes/status table across phases 2 and 3 at some point.

### Anti-Patterns Found

None. Scanned all 11 files modified/created across the three plans (`internal/queue/{queue,client,queue_test}.go`, `cmd/worker/main.go`, `.env.example`, `internal/jobs/{repo,repo_test}.go`, `internal/worker/{worker,worker_test}.go`, `internal/reconciler/{reconciler,reconciler_test}.go`) for `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER`, "not yet implemented"/"coming soon" phrasing, and empty-return stub patterns. Zero matches.

### Human Verification Required

None. This phase is backend queue/reliability plumbing with no UI or user-facing behavior; every success criterion was verifiable via code inspection plus live tests against the real Postgres/Redis/MinIO stack (not mocked), and I exercised the reconciler's actual decision logic end-to-end against real infrastructure rather than trusting SUMMARY.md's unit-test-only claims.

### Gaps Summary

No gaps. All 5 ROADMAP success criteria are independently verified true in the codebase — through direct code reading, re-running the existing test suites myself (not trusting the SUMMARY's "PASSED" claims), and writing/running my own throwaway live integration tests against the real database and Redis to close the exact gap the executor flagged as unexercised (multi-minute live staleness/duplicate-guard/exhaustion scenarios). The throwaway test files were deleted after running; `git status` confirms a clean working tree.

One informational, non-blocking observation: `.planning/REQUIREMENTS.md`'s checkbox/status table has not been updated for RELY-01/RELY-02/RECON-01/RECON-02/RECON-03 (still shows `[ ]`/"Pending"), matching a pre-existing pattern already present for Phase 2's WEBHOOK-01. Does not affect phase-3 goal achievement.

Unrelated environment note: the `octoconv-api` Docker container in this environment is in a crash-restart loop trying to resolve a Docker-Compose-internal hostname `postgres` that doesn't exist on this host's network (this environment runs Postgres/Redis/MinIO as directly-mapped host-port containers, not via `docker-compose up`). This is a local environment/networking artifact unrelated to any Phase 3 code change and does not affect this verification, which used direct `DATABASE_URL`/`REDIS_ADDR` connections exactly as the project's own tests do.

---

_Verified: 2026-07-06T19:38:24Z_
_Verifier: Claude (gsd-verifier)_
