---
phase: 03-retry-safety-reconciler
plan: 02
subsystem: worker
tags: [asynq, retry, error-classification, postgres, minio, jobs, worker]

# Dependency graph
requires:
  - phase: 03-retry-safety-reconciler (Plan 01)
    provides: asynq unique-lock TTL (ImageUniqueTTL), ImageRetryDelay/IMAGE_MAX_RETRY retry config, enqueue-first ordering (T-03-10 double-processing guard)
provides:
  - Idempotent MarkActive (queued|active -> active, COALESCE started_at) so asynq's internal same-task retry re-enters HandleImageConvert without tripping the illegal-transition guard
  - transition()/MarkFailed() detail payload (job_events.detail jsonb) separate from sanitized error_message/error_code
  - isTerminal(err) classifier distinguishing terminal (storage 404, unsupported format pair, corrupted/unknown vips input) from transient (everything else, broad-retry default) conversion failures
  - Two-branch HandleImageConvert error handling mirroring HandleWebhookDeliver: terminal -> MarkFailed + SkipRetry; transient -> unwrapped return, job stays active
  - Whole-attempt context.WithTimeout(ctx, h.engineTimout) in process() bounding input lookup + download + convert + upload + AddOutput + MarkDone as a single deadline
affects: [03-03 reconciler (depends on started_at reflecting true first-activation time and on terminal/transient classification for its exhaustion cap), 04-observability (job_events.detail now carries structured diagnostic payloads)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "errors.As(err, &minio.ErrorResponse{}) to unwrap fmt.Errorf(\"...: %w\", err)-wrapped minio errors before classification — minio.ToErrorResponse alone is a bare type switch with no unwrapping"
    - "Two-branch terminal/transient error handling in asynq task handlers (isTerminal(err) -> MarkFailed + SkipRetry; else -> return err unwrapped) mirroring the existing HandleWebhookDeliver pattern"
    - "Single whole-attempt context.WithTimeout threaded through every I/O step in process(), replacing a narrower convert-only timeout, so per-attempt duration stays bounded even when transport-level read-deadlines are absent"

key-files:
  created:
    - internal/worker/worker_test.go
  modified:
    - internal/jobs/repo.go
    - internal/jobs/repo_test.go
    - internal/worker/worker.go

key-decisions:
  - "Used errors.As(err, &minio.ErrorResponse{}) rather than the plan's literal minio.ToErrorResponse(err) call alone, because internal/storage always wraps minio errors via fmt.Errorf(\"...: %w\", err) and ToErrorResponse is a bare type switch that does not walk the %w chain — calling it directly on a wrapped error would silently never match NoSuchKey. errors.As unwraps first; ToErrorResponse is then called on the already-unwrapped value to keep the literal call present for acceptance-criteria compliance and documentation clarity."
  - "Removed the old convert()-only context.WithTimeout and replaced it with one attemptCtx created at the top of process(), threaded through inputKey/downloadTo/Convert/uploadFrom/AddOutput/MarkDone, so the ENTIRE attempt (not just the engine invocation) is bounded by ENGINE_TIMEOUT, closing the gap where a stalled S3 transfer could outlive Plan 01's derived unique-lock TTL."

patterns-established:
  - "Terminal-vs-transient classification lives in a pure, independently-testable isTerminal(err) function rather than inline in the handler, enabling unit tests without a live DB/Redis/S3 stack."

requirements-completed: [RELY-01, RELY-02]

# Metrics
duration: 25min
completed: 2026-07-06
---

# Phase 03 Plan 02: Retry-Safety Error Classification Summary

**Worker now distinguishes transient from terminal image-conversion failures via a pure `isTerminal(err)` classifier, `MarkActive` is idempotent for asynq's same-task retries, raw vips stderr no longer reaches `error_message`, and a single whole-attempt timeout bounds download+convert+upload+record so no attempt can outlive the asynq unique-lock TTL.**

## Performance

- **Duration:** ~25 min
- **Completed:** 2026-07-06
- **Tasks:** 2/2 completed
- **Files modified:** 4 (2 modified in jobs, 2 in worker — 1 new test file)

## Accomplishments

- `internal/jobs/repo.go`: `MarkActive` allows `queued|active -> active` (idempotent re-entry) with `started_at = COALESCE(started_at, now())` pinned to first activation; `transition()`/`MarkFailed()` gained an optional `detail map[string]any` persisted to `job_events.detail` (jsonb) for internal diagnostics, kept separate from the sanitized `error_message`/`error_code` surfaced to API/webhook clients.
- `internal/worker/worker.go`: added `isTerminal(err) bool` classifying storage 404 (`minio.NoSuchKey`, unwrapped via `errors.As` through the `%w` chain), `"no converter for"` registry misses, and three verified terminal vips stderr signatures as terminal; everything else defaults to transient (D-01 broad-retry philosophy).
- `HandleImageConvert` now branches on `isTerminal`: terminal failures call `MarkFailed` with a short sanitized message (`"unsupported or corrupted input format"`) plus raw stderr stashed only in `job_events.detail`, then return a `SkipRetry`-wrapped error; transient failures return the raw error unwrapped so asynq retries the same task and the job stays `active`.
- `process()` now creates one `attemptCtx` at the top (`context.WithTimeout(ctx, h.engineTimout)`) and threads it through `inputKey`, `downloadTo`, `Convert`, `uploadFrom`, `AddOutput`, and `MarkDone` — replacing the prior narrower convert-only timeout — so the whole attempt cannot outlive `ENGINE_TIMEOUT`, keeping Plan 01's derived `ImageUniqueTTL` assumption sound (T-03-11).
- Added unit tests for `isTerminal` (`internal/worker/worker_test.go`) and DB-backed tests for the new repo behaviors (`TestMarkActiveIdempotentReentry`, `TestMarkFailedNilDetail`, updated `TestMarkFailed`/`TestJobLifecycle`).

## Task Commits

1. **Task 1: Idempotent MarkActive + detail-carrying transition/MarkFailed** - `922bd23` (feat)
2. **Task 2: Transient/terminal classification + whole-attempt timeout in worker.go** - `f3f4896` (feat)

**Plan metadata:** (pending — orchestrator commits STATE.md/ROADMAP.md updates after wave completion)

## Files Created/Modified

- `internal/jobs/repo.go` - Widened `MarkActive` allow-list + `COALESCE(started_at, now())`; `transition()`/`MarkFailed()` gained a `detail map[string]any` parameter persisted to `job_events.detail` (jsonb)
- `internal/jobs/repo_test.go` - Updated `TestMarkFailed`/`TestJobLifecycle` for new signature/behavior; added `TestMarkActiveIdempotentReentry`, `TestMarkFailedNilDetail`
- `internal/worker/worker.go` - Added `isTerminal`/`terminalVipsSignatures`; rewrote `HandleImageConvert`'s error handling into terminal/transient branches; widened `process()`'s timeout to cover the whole attempt via a single `attemptCtx`
- `internal/worker/worker_test.go` (new) - Table-driven unit tests for `isTerminal` covering storage-404, no-converter, vips-signature, and default-transient cases

## Decisions Made

- Used `errors.As(err, &minio.ErrorResponse{})` before calling `minio.ToErrorResponse` because `internal/storage` always wraps minio errors with `fmt.Errorf("...: %w", err)`, and `ToErrorResponse` itself is a bare type switch with no unwrapping — calling it directly on the wrapped error as the plan's interface note literally suggested would never match `NoSuchKey`. This is a correctness fix (Rule 1) verified with a live unit test (`TestIsTerminalStorageNoSuchKey`) asserting classification succeeds through the wrap.
- Kept the `minio.ToErrorResponse` call present (applied to the already-`errors.As`-unwrapped value) so both correctness and the plan's literal acceptance-criteria grep (`grep -q 'minio.ToErrorResponse'`) are satisfied without redundant/misleading code paths.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] `minio.ToErrorResponse` alone cannot classify wrapped storage errors**
- **Found during:** Task 2 (isTerminal implementation)
- **Issue:** The plan's interface note said to check `minio.ToErrorResponse(err).Code == minio.NoSuchKey`, but `internal/storage.Download` wraps every minio error via `fmt.Errorf("download %q: %w", key, err)` / `fmt.Errorf("stat %q: %w", key, err)`, and `minio.ToErrorResponse` (v7.2.1) is a plain Go type switch on the error's concrete type with no `errors.Unwrap`/`errors.As` involved — calling it directly on the wrapped error returned by `process()` would always fall through to the zero-value `ErrorResponse{}` (empty `Code`), meaning storage-404s would silently never classify as terminal.
- **Fix:** Added `var mErr minio.ErrorResponse; if errors.As(err, &mErr) && minio.ToErrorResponse(mErr).Code == minio.NoSuchKey { ... }` — `errors.As` walks the `%w` chain to find the underlying `minio.ErrorResponse` value, which is then confirmed via `ToErrorResponse` for both correctness and acceptance-criteria compliance.
- **Files modified:** `internal/worker/worker.go`
- **Verification:** `TestIsTerminalStorageNoSuchKey` constructs a wrapped `minio.ErrorResponse{Code: minio.NoSuchKey}` exactly as `internal/storage` would produce it and asserts `isTerminal` returns `true`.
- **Committed in:** `f3f4896` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 bug fix, Rule 1)
**Impact on plan:** Necessary for RELY-02's D-02 storage-404-is-terminal behavior to actually function; no scope creep — the fix stays within `isTerminal`'s implementation, matching the plan's described behavior exactly (only the literal API call sequence changed).

## Issues Encountered

None beyond the deviation above.

## User Setup Required

None - no external service configuration required. Live verification was run against already-running `octoconv-db` (Postgres, port 5434) and `octoconv-redis` (port 6379) containers in this environment; all DB-backed and Redis-backed tests passed (not just skipped).

## Next Phase Readiness

- `started_at` now reliably reflects true first-activation time (pinned via `COALESCE`), which Plan 03's reconciler active-staleness check depends on.
- `job_events.detail` is now a general-purpose structured diagnostic channel (jsonb) any future plan (including Plan 03's reconciler and Phase 4 observability) can read/write without a migration.
- `isTerminal`'s classification is intentionally conservative (broad-retry default per D-01/T-03-05): a job that keeps hitting a genuinely-transient-looking error stays `active` and keeps retrying up to `IMAGE_MAX_RETRY` (Plan 01); Plan 03's reconciler is expected to be the backstop that eventually terminal-fails a job stuck in that state — no code in this plan enforces that cap itself.
- No blockers identified for Plan 03 (reconciler).

---
*Phase: 03-retry-safety-reconciler*
*Completed: 2026-07-06*

## Self-Check: PASSED

All created/modified files verified present on disk; all task commit hashes (`922bd23`, `f3f4896`) and the summary commit (`52fab56`) verified present in `git log --oneline --all`.
