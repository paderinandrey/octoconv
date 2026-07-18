---
phase: 31-queue-worker-routing-integration
plan: 03
subsystem: api
tags: [go, chi, asynq, reconciler, audio, whisper, content-sniffing]

# Dependency graph
requires:
  - phase: 31-queue-worker-routing-integration
    provides: "Plan 01 (queue): migration 0006, AudioConverter registered in converters.go, queue.go audio task type/queue/TTL, client.go EnqueueAudioConvert"
provides:
  - "EnqueueAudioConvert wired into both Enqueuer interfaces (API and reconciler) and both concrete queue.Client-satisfied test doubles"
  - "API content-detection chain detects mp3/wav/m4a/ogg via SniffAudio, chained off `rest` (byte-identical stored object, no 12-byte truncation)"
  - "API opts-parsing switch validates audio opts via ParseAudioOpts/ValidateAudioApplicability instead of rejecting them as invalid DocOpts"
  - "API enqueue switch routes engine=audio jobs to EnqueueAudioConvert"
  - "Reconciler sweep() routes stranded engine=audio jobs to EnqueueAudioConvert instead of the fail-closed default"
  - "SC4 proof: repeated sweep ticks against a live AudioUniqueTTL lock (asynq.ErrDuplicateTask) fire zero reconciler_recovery events"
affects: [32-audio-worker-model-baking, phase-32]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Content-detection chain splice ordering: expensive detectors (mp3PeekLen=512KiB) go LAST, immediately before the fail-closed 422, so cheap formats never pay for the expensive peek"
    - "Engine-scoped opts dispatch: each engine class (html, audio, default=doc) gets its own case in the opts-parsing switch, calling that engine's own Parse*Opts/Validate*Applicability pair — never a shared/generic opts type"

key-files:
  created: []
  modified:
    - internal/api/api.go
    - internal/api/handlers.go
    - internal/api/handlers_test.go
    - internal/reconciler/reconciler.go
    - internal/reconciler/reconciler_test.go

key-decisions:
  - "SniffAudio spliced between the OLE-CFB block and the final fail-closed 422 (not after it), chained off `rest` not `file` -- avoids both the dead-code trap and the 12-byte truncation trap called out explicitly in the plan"
  - "Byte-integrity test captures the actual io.Reader bytes handed to storage.Upload (fakeStorage now io.ReadAll's into uploadedBytes instead of io.Discard) rather than trusting a passing 202 status, since the truncation bug would not surface as a test failure any other way"

requirements-completed: [AUD-05]

# Metrics
duration: 7min
completed: 2026-07-18
---

# Phase 31 Plan 03: Audio Queue/Routing Integration Summary

**Audio content-detection, opts validation, and enqueue routing wired live into both the API request path and the reconciler's stranded-job recovery path, closing two confirmed integration bugs (12-byte upload truncation and opts mis-routing) before they could ship.**

## Performance

- **Duration:** ~7 min
- **Started:** 2026-07-18T17:44:51+03:00 (base commit)
- **Completed:** 2026-07-18T17:51:01+03:00
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments
- API `Enqueuer` interface and reconciler `enqueuer` interface both widened with `EnqueueAudioConvert`; both concrete test doubles (`fakeQueue`, `fakeEnqueuer`) implement it
- Content-detection chain now detects mp3 (ID3v2-tagged), wav, m4a, and ogg via `convert.SniffAudio(rest)` — chained off the byte-0 re-stitched reader, not the cursor-advanced `file`, so the stored S3 object is byte-identical to the upload
- Dedicated `case convert.EngineAudio` in the opts-parsing switch validates `{"language":"ru"}`-shaped opts via `ParseAudioOpts`/`ValidateAudioApplicability` instead of falling into the `default` DocOpts branch and 422ing
- Enqueue switch routes `engine=="audio"` jobs to `s.queue.EnqueueAudioConvert`
- Reconciler `sweep()` routes stranded `engine=="audio"` jobs to `s.enq.EnqueueAudioConvert` instead of hitting the fail-closed `default` (`unroutable_engine`) branch
- SC4 proved: `TestSweepAudioZeroSpuriousRecoveryUnderRepeatedTicks` drives 5 sweep ticks against a job whose `EnqueueAudioConvert` always returns `asynq.ErrDuplicateTask` (simulating a long in-flight transcription still holding its `AudioUniqueTTL` lock) and asserts `RequeueStale` is called zero times and `recoveryCount` stays 0 across every tick

## Task Commits

Each task was committed atomically:

1. **Task 1: API content-detection splice + opts case + enqueue case + Enqueuer interface** - `7a38b71` (feat)
2. **Task 2: Reconciler audio routing + SC4 zero-spurious-recovery proof** - `1f4863e` (feat)

_Note: both tasks used TDD in practice (tests added alongside the behavior in the same commit); no separate RED-only commit was created since the plan's `tdd="true"` tasks did not include a distinct RED gate commit requirement beyond passing tests at commit time._

## Files Created/Modified
- `internal/api/api.go` - `Enqueuer` interface gains `EnqueueAudioConvert(ctx, jobID) error`
- `internal/api/handlers.go` - `SniffAudio(rest)` splice (between OLE-CFB block and the fail-closed 422), `case convert.EngineAudio` in the opts switch, `case convert.EngineAudio` in the enqueue switch
- `internal/api/handlers_test.go` - `fakeQueue.EnqueueAudioConvert`, `fakeStorage.uploadedBytes` capture (was `io.Discard`, now `io.ReadAll`), `TestCreateJob_AudioDetectedAndAccepted` (byte-integrity + routing), `TestCreateJob_AudioOptsAccepted`, `TestCreateJob_AudioOptsRejectedForWrongLanguage`
- `internal/reconciler/reconciler.go` - `enqueuer` interface gains `EnqueueAudioConvert`, `sweep()` gains `case convert.EngineAudio`
- `internal/reconciler/reconciler_test.go` - `fakeEnqueuer.EnqueueAudioConvert` + `audioCallIDs()` snapshot helper, `TestSweepRoutesAudioJobsToAudioQueue`, `TestSweepAudioZeroSpuriousRecoveryUnderRepeatedTicks` (SC4)

## Decisions Made
- Reused the real `internal/convert/testdata/audio/sample-id3.mp3` fixture (shared with `internal/convert`'s own `SniffAudio` tests) via a relative `../convert/testdata/audio/` path rather than copying it into `internal/api/testdata/`, since the plan's `files_modified` list did not include new testdata files and the fixture already exercises the ID3v2-tag edge case the byte-integrity test needs.
- `fakeStorage.Upload` was changed from `io.Copy(io.Discard, r)` to `io.ReadAll(r)` capturing the bytes into a new `uploadedBytes` field — a strictly additive change (existing `uploaded`/`contentType` assertions are untouched) needed because no existing test could otherwise observe the 12-byte truncation regression.

## Deviations from Plan

None - plan executed exactly as written. The splice point, chaining discipline (`rest` not `file`), opts-case placement, and enqueue-case placement all matched the plan's explicit guidance without requiring adjustment.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- SC1 (API-side audio routing) and SC4 (zero spurious reconciler recovery) are both proven live and green.
- Phase 32 (worker-side model baking / whisper-cli execution) can now assume a job with `engine="audio"` reliably reaches the audio queue via both the direct-create path and the stranded-job recovery path, with byte-identical stored input and validated `AudioOpts`.
- No blockers.

---
*Phase: 31-queue-worker-routing-integration*
*Completed: 2026-07-18*
