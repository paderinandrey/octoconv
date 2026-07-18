---
phase: 30-audio-engine-foundation
plan: 03
subsystem: convert
tags: [whisper-cli, ffmpeg, transcription, converter, audio]

# Dependency graph
requires:
  - phase: 30-audio-engine-foundation (Plan 01)
    provides: SniffAudio/ProbeDuration/audio testdata fixtures, local whisper-cli v1.9.1 + ggml-base.bin toolchain
  - phase: 30-audio-engine-foundation (Plan 02)
    provides: EngineAudio const, AudioOpts/AudioOptsFromMap/ValidateAudioApplicability, audioLanguageAllowlist
provides:
  - AudioConverter implementing Converter (Pairs/Convert/Engine) -- the fourth engine class
  - Two-stage ffmpeg-normalize -> whisper-cli-transcribe pipeline sharing one caller ctx
  - whisperOutputFlag mapping txt/srt/vtt/json onto whisper-cli's -otxt/-osrt/-ovtt/-ojf flags
  - MIMEType cases for the four audio output formats (text/plain, application/x-subrip, text/vtt, application/json)
  - Live-verified whisper-cli v1.9.1 JSON schema proof (segment + token timestamps)
affects: [phase-31-audio-registration-and-api-routing]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Two-stage hardened-exec pipeline: two runCommand calls (ffmpeg then whisper-cli) inside one Convert(), sharing a single caller-supplied ctx"
    - "Injected-field-for-testability: unexported modelPath struct field + model() accessor keeps Convert's signature env-free while letting tests inject a local model path"

key-files:
  created:
    - internal/convert/whisper.go
    - internal/convert/whisper_test.go
  modified:
    - internal/convert/sniff.go

key-decisions:
  - "AudioConverter is deliberately NOT registered into convert.Default in this plan -- registration is deferred to Phase 31 because it would flip the live API's EngineFor/GET /v1/formats/create-job-enqueue switch and the reconciler recovery switch with no audio queue case yet, which would make the API accept audio uploads and then 500 at the enqueue default: branch, orphaning queued rows"
  - "target=json maps to whisper-cli's -ojf (not the plain -oj) so segment- AND token-level timestamps come from a single invocation, satisfying AUD-02's word-level timestamp requirement directly"
  - "Hallucination-on-silence (whisper exits 0 with structurally-valid garbage on silence/music, no no_speech_prob field exists in the pinned binary) is recorded as an accepted residual risk (SC5) in Convert's doc comment, not mitigated in this phase"

patterns-established:
  - "Pattern 1: Two-stage hardened-exec pipeline inside one Convert() -- the project's first converter needing more than one external tool per job"

requirements-completed: [AUD-02]

# Metrics
duration: ~20min
completed: 2026-07-18
---

# Phase 30 Plan 03: AudioConverter Transcription Pipeline Summary

**AudioConverter's two-stage ffmpeg-normalize -> whisper-cli-transcribe pipeline, live-verified against the pinned whisper-cli v1.9.1 binary to emit segment- and token-level JSON timestamps, deliberately left unregistered pending Phase 31's API/queue wiring**

## Performance

- **Duration:** ~20 min
- **Tasks:** 2 completed
- **Files modified:** 3 (2 created, 1 modified)

## Accomplishments
- `AudioConverter` implements the `Converter` interface (`Pairs()`/`Convert()`/`Engine()`) as the fourth engine class (`EngineAudio`), with 16 Pairs (4 source formats x 4 target formats)
- `Convert()` runs `ffmpeg` (16kHz mono s16 PCM WAV normalize) then `whisper-cli` (transcribe), both bounded by a single caller-supplied `ctx`, with distinguishable `"audio: ffmpeg:"` / `"audio: whisper-cli:"` error prefixes for a future stage-aware classifier
- `whisperOutputFlag` maps txt/srt/vtt/json onto whisper-cli's `-otxt`/`-osrt`/`-ovtt`/`-ojf` flags via the existing `Pair` mechanism
- `target=json` (`-ojf`) live-verified against the pinned whisper-cli v1.9.1 binary: output carries both segment-level (`timestamps`/`offsets`/`text`) AND token-level (`text`/`id`/`p`, optional `timestamps`/`offsets`) fields, exactly matching the source-read schema in 30-RESEARCH.md
- `MIMEType` extended with the four audio output formats, colliding with no existing key
- `AudioConverter` intentionally NOT registered into `convert.Default` -- the deferral rationale is documented inline in `whisper.go` and here

## Task Commits

Each task was committed atomically:

1. **Task 1: whisper.go — AudioConverter two-stage pipeline + MIMEType cases** - `61ade5b` (feat)
2. **Task 2: whisper_test.go — live-gated JSON schema + timestamp + argv proof** - `5a9a4ce` (test)

## Files Created/Modified
- `internal/convert/whisper.go` - `AudioConverter{modelPath string}`, `Pairs()`/`Engine()`/`Convert()`, `whisperOutputFlag()`, `validateAudioOutput()` (size>0 guard, the applicable subset of `validatePDF`'s shape)
- `internal/convert/whisper_test.go` - live-binary-gated JSON schema/timestamp proof, txt/srt/vtt smoke, unconditional Converter-contract assertions, garbage-opts rejection test
- `internal/convert/sniff.go` - `MIMEType` cases for txt/srt/vtt/json; updated doc comment to mention all four engine classes now covered

## Decisions Made
- **Registration deferral to Phase 31** (per plan's explicit instruction): registering `AudioConverter{}` into `convert.Default` now would make the live API accept audio uploads at the create-job endpoint but then fail at the enqueue `default:` switch (no `image`-analog audio queue case exists yet) and at the reconciler's recovery-routing switch -- a real regression producing orphaned `queued` rows. `converters.go` was left completely untouched (verified via `grep -c 'Default.Register(AudioConverter' converters.go` = 0). AUD-02 is still fully covered because Task 2's tests instantiate `AudioConverter{}` directly rather than going through the registry.
- **`-ojf` over `-oj` for target=json**: per RESEARCH.md's "Recommended SEED-001-forward JSON target mapping" -- `-ojf` implicitly sets `token_timestamps=true` internally, so word/token-level timestamps come free in the same single invocation that already produces segment-level output, satisfying AUD-02's dual-granularity requirement without a second whisper-cli call.
- **Model path via injected struct field, not env var**: `AudioConverter.modelPath` (unexported) + `model()` accessor keeps `Convert`'s signature consistent with every other converter in the codebase (no `os.Getenv` inside `internal/convert`), while letting `whisper_test.go` inject the local Plan-01-installed model path (`~/.cache/whisper/ggml-base.bin`, overridable via `AUDIO_MODEL_PATH`).
- **Per-token timestamps treated as optional, not required** (Assumption A3 in RESEARCH.md): the live test asserts at least one token across the whole transcript carries `timestamps`, not that every token does -- whisper-cli's own source only populates them when a token's `t0`/`t1` pass a validity guard.

## Deviations from Plan

None - plan executed exactly as written. The optional `os.Stat(outPath)` size>0 guard mentioned in the plan's action block was included as `validateAudioOutput` (mirroring the applicable subset of `libreoffice.go`'s `validatePDF`, as instructed).

## Issues Encountered
None. The full local toolchain from Plan 01 (`ffmpeg`, `whisper-cli` v1.9.1, `ggml-base.bin`, all on `PATH`/`~/.cache/whisper/`) was already present in this worktree, so both live-gated tests (`TestAudioConverter_JSONFull_LiveBinary`, `TestAudioConverter_TextFormats_LiveBinary`) ran and passed against the real binary rather than skipping -- the SC3 JSON schema proof is genuinely live-verified, not merely skip-gated.

## User Setup Required
None - no external service configuration required. The local whisper-cli toolchain was already installed by Plan 01; this plan added no new setup requirements.

## Threat Flags

None - all threat model entries (T-30-07 through T-30-10) were already dispositioned by the plan and are addressed as designed: both subprocess stages share the caller's bounded `ctx` (T-30-07), the duration-guard ordering is documented in `Convert`'s scope (T-30-08, actual guard wiring is Phase 31's job), hallucination-on-silence is explicitly logged as accepted residual risk in `Convert`'s doc comment (T-30-09), and the model path is a compile-time constant or test-injected field, never client bytes (T-30-10).

## Next Phase Readiness
- `AudioConverter` is fully built and tested standalone against the live toolchain; AUD-02 is complete
- Phase 31 can register `AudioConverter{}` into `convert.Default` with the real baked-in model path, wire the `EngineAudio` case into the API's `EngineFor`/create-job/GET-formats paths, add an `audio` asynq queue, and extend the reconciler's recovery-routing switch -- none of that plumbing exists yet by design (scope fence held)
- No blockers

---
*Phase: 30-audio-engine-foundation*
*Completed: 2026-07-18*

## Self-Check: PASSED
