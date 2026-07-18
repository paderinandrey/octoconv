---
phase: 31-queue-worker-routing-integration
plan: 02
subsystem: worker
tags: [asynq, whisper-cli, ffmpeg, ffprobe, retry-classification, security-hardening]

# Dependency graph
requires:
  - phase: 31-01
    provides: migration 0006, AudioConverter registered in converters.go, SetAudioModelPath, queue.go audio block (TypeAudioConvert/QueueAudio/AudioUniqueTTL/audio retry schedule), client.go EnqueueAudioConvert
  - phase: 30
    provides: AudioConverter (ffmpeg->whisper-cli pipeline), EnforceMaxDuration/ErrAudioDurationExceeded, AudioOptsFromMap, whisper.go stage-prefixed errors ("audio: ffmpeg:"/"audio: whisper-cli:")
provides:
  - isAudioTerminal — stage-aware terminal/transient classifier implementing Key Decision 1 (BINDING)
  - HandleAudioConvert — asynq handler wiring the audio queue into the worker pipeline
  - EnforceMaxDuration actually invoked in process() (T-30-08/IN-02 closed)
  - NewHandler 10th param (audioMaxDuration) threaded to all four existing worker cmds
  - file: protocol prefix on ffprobe/ffmpeg path args (IN-01 closed)
  - RECONCILER_ACTIVE_STALE_AFTER default raised 5m -> 15m (SC4 env precondition)
affects: [31-03, 31-04, 32-rtf-go-no-go, 33-audio-keda]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Stage-aware terminal classifier (isAudioTerminal) — a genuinely new classifier body, NOT delegating to the shared timeoutIsTerminal blanket-terminal shape used by isDocumentTerminal/isHTMLTerminal"
    - "Guard-before-Convert extraction (enforceAudioGuardBeforeConvert) — a security-relevant ordering guarantee factored into its own package-level function specifically so it is unit-testable without a live Postgres/S3 Handler"
    - "argv-builder extraction (ffprobeDurationArgs/ffmpegNormalizeArgs) — mirrors the existing whisperArgs shape so a defense-in-depth argv change (file: prefix) is pinned by a pure unit test"

key-files:
  created: []
  modified:
    - internal/worker/worker.go
    - internal/worker/worker_test.go
    - internal/convert/audioduration.go
    - internal/convert/audioduration_test.go
    - internal/convert/whisper.go
    - internal/convert/whisper_test.go
    - cmd/worker/main.go
    - cmd/document-worker/main.go
    - cmd/chromium-worker/main.go
    - cmd/webhook-worker/main.go

key-decisions:
  - "isAudioTerminal implements Key Decision 1 exactly as specified in STATE.md (BINDING, not re-litigated): ffmpeg-stage failure/timeout terminal, whisper-stage timeout transient, ErrAudioDurationExceeded always terminal, everything else falls through to the shared isTerminal base classifier."
  - "Non-timeout whisper-cli failures default transient (31-RESEARCH.md A2, adopted as-is) — no dedicated terminalWhisperSignatures list introduced this phase, since whisper-cli's input is always a server-produced normalized WAV."
  - "A duration-guard rejection (ErrAudioDurationExceeded) gets its own distinct client-facing error_code \"duration_exceeded\" rather than reusing the generic \"engine_error\" (31-RESEARCH.md A3)."
  - "The duration guard was extracted into a standalone package-level function (enforceAudioGuardBeforeConvert) rather than left inline in process() — process()'s h.repo/h.store are concrete types, not interfaces, so this was the only way to make the guard-runs-before-Convert ordering unit-testable without live infra."
  - "RECONCILER_ACTIVE_STALE_AFTER default raised globally (all engine classes, not audio-scoped) — the actual double-processing safety property is held by AudioUniqueTTL + asynq.ErrDuplicateTask, not this threshold; the raise only accepts longer image/document/html staleness-detection latency in exchange."

patterns-established:
  - "Pattern: security-relevant ordering guarantees inside process() (duration guard, future similar guards) get extracted as standalone package-level functions taking a convertFn closure, so they remain unit-testable without constructing a full Handler."
  - "Pattern: subprocess argv construction gets isolated into a small pure-function per invocation site (ffprobeDurationArgs, ffmpegNormalizeArgs, whisperArgs) so defense-in-depth argv changes are pinned by fast unit tests instead of requiring a live binary."

requirements-completed: [AUD-05]

# Metrics
duration: 35min
completed: 2026-07-18
---

# Phase 31 Plan 02: Audio Queue/Worker Routing Integration Summary

**Stage-aware terminal/transient classifier (Key Decision 1) wired into a new HandleAudioConvert handler, with the previously-dormant duration guard actually spliced into the pipeline and ffprobe/ffmpeg path args hardened with the `file:` protocol prefix.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-07-18T17:45:00+03:00 (approx, from first commit gap)
- **Completed:** 2026-07-18T17:53:33+03:00
- **Tasks:** 3
- **Files modified:** 10

## Accomplishments

- `isAudioTerminal` implements Key Decision 1's stage-aware split exactly: ffmpeg-stage failure/timeout terminal, whisper-stage timeout transient, `ErrAudioDurationExceeded` always terminal, shared `isTerminal` fallthrough for everything else — proven by `TestIsAudioTerminal`, which pins the distinguishing SC2 case (ffmpeg-timeout terminal vs whisper-timeout transient).
- `HandleAudioConvert` wires the audio queue into the worker pipeline, mirroring `HandleDocumentConvert`'s shape with three deliberate divergences: `isAudioTerminal` classification, a distinct `duration_exceeded` error_code for `ErrAudioDurationExceeded` failures (not the generic `engine_error`), and `queue.QueueAudio`-tagged metrics.
- The `EnforceMaxDuration` guard (built in Phase 30 but never invoked) is now actually spliced into `process()` after download and before `Convert`, gated on `job.Engine == convert.EngineAudio` — closes T-30-08/IN-02. Proven by `TestEnforceAudioGuardBeforeConvert_IN02`, run against a real ffprobe binary and a real 11-second audio fixture, asserting the guard fires and the (fake) Convert function is never called.
- `NewHandler` gained a 10th `audioMaxDuration time.Duration` parameter; all four existing worker cmds (`cmd/worker`, `cmd/document-worker`, `cmd/chromium-worker`, `cmd/webhook-worker`) pass `0` (inert for their non-audio handlers).
- `RECONCILER_ACTIVE_STALE_AFTER` default raised from 5m to 15m in `cmd/webhook-worker/main.go`, comfortably exceeding the `AUDIO_ENGINE_TIMEOUT` placeholder (600s) — SC4's env precondition.
- The ffprobe path argument and the ffmpeg `-i` path argument are both prefixed with `file:` (IN-01, defense-in-depth), verified live against local ffprobe/ffmpeg binaries and pinned by pure unit tests on the extracted `ffprobeDurationArgs`/`ffmpegNormalizeArgs` argv-builder functions.

## Task Commits

Each task was committed atomically:

1. **Task 1: Stage-aware isAudioTerminal + HandleAudioConvert** - `163fa83` (feat)
2. **Task 2: Duration-guard splice in process() + NewHandler 10th param + stale-after default raise** - `03fba94` (feat)
3. **Task 3: IN-01 file: protocol hardening on ffprobe/ffmpeg path args** - `7b868d0` (fix)

**Plan metadata:** (this commit) (docs: complete plan)

## Files Created/Modified

- `internal/worker/worker.go` - `isAudioTerminal`, `HandleAudioConvert`, `enforceAudioGuardBeforeConvert`, `Handler.audioMaxDuration` field, `NewHandler` 10th param, duration-guard splice in `process()`
- `internal/worker/worker_test.go` - `TestIsAudioTerminal` (SC2), `TestEnforceAudioGuardBeforeConvert_IN02`
- `internal/convert/audioduration.go` - `ffprobeDurationArgs` extracted, `file:` prefix on the ffprobe path argv element
- `internal/convert/audioduration_test.go` - `TestFfprobeDurationArgs_FilePrefix`
- `internal/convert/whisper.go` - `ffmpegNormalizeArgs` extracted, `file:` prefix on ffmpeg's `-i` path argv element
- `internal/convert/whisper_test.go` - `TestFfmpegNormalizeArgs_FilePrefix`
- `cmd/worker/main.go` - `NewHandler` call updated with 10th `0` arg
- `cmd/document-worker/main.go` - `NewHandler` call updated with 10th `0` arg
- `cmd/chromium-worker/main.go` - `NewHandler` call updated with 10th `0` arg
- `cmd/webhook-worker/main.go` - `NewHandler` call updated with 10th `0` arg; `RECONCILER_ACTIVE_STALE_AFTER` default 5m -> 15m

## Decisions Made

- Extracted the duration guard into `enforceAudioGuardBeforeConvert` (a standalone package-level function taking a `convertFn` closure) instead of leaving it inline in `process()`. `process()`'s `h.repo`/`h.store` are concrete types (per the codebase's documented "Key Abstractions" convention — worker deliberately has no interface layer), so a full `Handler` cannot be constructed in a pure unit test. This extraction was the only way to satisfy the plan's IN-02 pinning-test requirement without live Postgres/S3 infrastructure; behavior of `process()` is unchanged (verified by full test suite + `go build ./...`).
- Similarly extracted `ffprobeDurationArgs`/`ffmpegNormalizeArgs` as pure argv-builder functions (mirroring the existing `whisperArgs` shape from Phase 30) so the IN-01 `file:` prefix change is pinned by a fast unit test rather than only exercisable via a live-binary-gated test.
- Verified the `file:` prefix does not break real ffprobe/ffmpeg invocations on local file paths by running both commands manually against the `jfk.wav` fixture before committing (see Task 3 verification below).

## Deviations from Plan

None - plan executed exactly as written. The two argv-extraction refactors (`enforceAudioGuardBeforeConvert`, `ffprobeDurationArgs`/`ffmpegNormalizeArgs`) were explicitly anticipated by the plan's own task language ("or use a seam that lets the ordering be asserted without a real subprocess" / "assert on a small helper that builds the arg"), not unplanned scope.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `HandleAudioConvert` and `isAudioTerminal` are ready for Plan 31-04 (audio worker cmd) to wire into an `asynq.ServeMux` and pass a real `audioMaxDuration` value.
- `NewHandler`'s 10-param signature is now stable for the audio worker cmd's construction call.
- `RECONCILER_ACTIVE_STALE_AFTER`'s 15m default and the still-open `AUDIO_ENGINE_TIMEOUT` placeholder (600s) are consistent; Plan 04's `.env.example` note (mentioned in the source comment) is the next place this gets surfaced to operators.
- No blockers for 31-03 (routes in api/reconciler files, running concurrently with zero file overlap) or 31-04.

---
*Phase: 31-queue-worker-routing-integration*
*Completed: 2026-07-18*

## Self-Check: PASSED

All created/modified files verified present on disk; all 4 task/summary commits (163fa83, 03fba94, 7b868d0, c6c9910) verified present in git log.
