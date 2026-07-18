---
phase: 30-audio-engine-foundation
plan: 02
subsystem: convert
tags: [go, opts-validation, allowlist, injection-safety, whisper-cli]

requires:
  - phase: 14-document-opts-hardening (v1.3)
    provides: "DocOpts/ParseDocOpts/checkStrictObject closed-struct strict-parse pattern (OPTS-01/02)"
  - phase: 15-html-pdf-engine (v1.3)
    provides: "HTMLOpts/ParseHTMLOpts map-lookup allowlist variant + ValidateHTMLApplicability engine-scoped applicability shape"
provides:
  - "EngineAudio = \"audio\" engine-class constant (single compile-time source of truth, 4th engine class)"
  - "AudioOpts{Language, Translate} closed struct, ParseAudioOpts strict-parse + closed audioLanguageAllowlist (auto/en/ru/es/fr/de)"
  - "AudioOptsFromMap read-path parity with ParseAudioOpts write-path strictness (D-10)"
  - "ValidateAudioApplicability engine-scoped applicability gate"
  - "Injection-safety test proving client bytes never reach whisper-cli argv via shell interpolation"
affects: [30-03-audio-converter, 31-audio-queue-worker, 32-whisper-docker]

tech-stack:
  added: []
  patterns:
    - "AudioOpts follows DocOpts/HTMLOpts closed-struct strict-parse pattern verbatim, reusing checkStrictObject unduplicated (D-10 parity, 3rd reuse)"
    - "Closed allowlist selected via map lookup (not string equality) mirrors HTMLOpts's htmlPageSizeCSS map-lookup style, chosen because the language list is longer than a single enum constant"

key-files:
  created:
    - internal/convert/audioopts.go
    - internal/convert/audioopts_test.go
  modified:
    - internal/convert/convert.go

key-decisions:
  - "audioLanguageAllowlist decided as {auto, en, ru, es, fr, de} -- deliberate minimal closed set (auto-detect + Russian-first company's immediate Latin-script languages), documented as intentionally closed and extended only per real client demand, never open-ended"
  - "AudioConverter registration deliberately deferred to Phase 31 -- this plan is scope-fenced to EngineAudio + AudioOpts only; converters.go NOT touched"
  - "ValidateAudioApplicability gates on engine == EngineAudio only (no target-format restriction), unlike ValidateHTMLApplicability's engine+target=pdf gate -- audio opts apply across all whisper-cli output targets (txt/srt/vtt/json), not a single fixed target format"

patterns-established:
  - "3rd reuse of checkStrictObject across DocOpts/HTMLOpts/AudioOpts -- the shared strict-JSON helper is now proven across three independent engine classes with zero duplication"

requirements-completed: [AUD-03]

duration: 25min
completed: 2026-07-18
---

# Phase 30 Plan 02: Audio Engine Foundation -- AudioOpts Summary

**EngineAudio const + AudioOpts{Language, Translate} validated-opts layer with a closed 6-entry language allowlist and an injection test proving client bytes never reach whisper-cli argv**

## Performance

- **Duration:** ~25 min
- **Tasks:** 2 completed
- **Files modified:** 3 (1 modified, 2 created)

## Accomplishments
- Added `EngineAudio = "audio"` to `convert.go`'s single compile-time source-of-truth const block (4th engine class alongside image/document/html)
- Built `AudioOpts{Language, Translate}` following the exact `DocOpts`/`HTMLOpts` closed-struct, strict-JSON, allowlist discipline: `ParseAudioOpts`, `AudioOptsFromMap`, `ValidateAudioApplicability`, `isZeroAudioOpts`
- Reused `checkStrictObject` from `opts.go` verbatim -- zero duplication (D-10 parity), now proven across three independent engine classes
- Proved the AUD-03 injection-safety requirement with a dedicated test: allowlist rejection of `;`, `$(whoami)`, and backtick payloads, plus a structural assertion that even a hand-constructed bypass value never leaves `AudioOpts` as anything but a plain struct field (never shell-interpolated, since `runCommand`/`exec.Command` never invokes a shell)

## Task Commits

1. **Task 1: EngineAudio const + audioopts.go validated-opts (AUD-03)** - `7681e23` (feat)
2. **Task 2: audioopts_test.go -- injection + strictness + round-trip (AUD-03 proof)** - `e268dd8` (test)

**Plan metadata:** committed alongside this SUMMARY (worktree mode -- STATE.md/ROADMAP.md updates deferred to orchestrator)

## Files Created/Modified
- `internal/convert/convert.go` - Added `EngineAudio = "audio"` to the existing engine-class const block; extended the block's doc comment to enumerate audio
- `internal/convert/audioopts.go` - `AudioOpts` struct, `audioLanguageAllowlist` (closed 6-entry map), `ParseAudioOpts`, `AudioOptsFromMap`, `ValidateAudioApplicability`, `isZeroAudioOpts`
- `internal/convert/audioopts_test.go` - `TestParseAudioOpts` (table-driven valid/invalid/strictness), `TestAudioOptsFromMap` (round-trip + read-path parity), `TestValidateAudioApplicability`, `TestAudioOpts_InjectionCannotReachArgv` (AUD-03 proof)

## Decisions Made
- `audioLanguageAllowlist = {auto, en, ru, es, fr, de}` -- the phase-level decision the orchestrator flagged; a deliberate minimal closed set, documented in-code as extend-per-demand, never open-ended
- `AudioConverter` registration explicitly deferred to Phase 31 per the plan's scope fence -- `converters.go` was not touched, confirmed by `git status --short` showing no changes outside the three planned files
- `ValidateAudioApplicability` gates only on `engine == EngineAudio`, with no target-format restriction (unlike `ValidateHTMLApplicability`'s additional `target == "pdf"` gate) -- correct because audio opts (language/translate) are meaningful across every whisper-cli output target (txt/srt/vtt/json), not a single fixed target the way HTML print options are pdf-only

## Deviations from Plan

None - plan executed exactly as written. Both tasks matched their `<action>`/`<behavior>` specifications; all acceptance criteria verified directly (see Self-Check below).

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. This plan touches only `internal/convert/` pure-Go code, no queue/worker/API/Docker.

## Next Phase Readiness

- `EngineAudio` and `AudioOpts` are ready for Plan 03 to consume when building `AudioConverter` (the ffmpeg->whisper-cli two-stage pipeline) and passing `o.Language`/`o.Translate` into whisper-cli's argv slice.
- No blockers. `AudioConverter` is intentionally NOT registered in `convert.Default` yet (deferred to Phase 31 per the plan's scope fence) -- this is expected, not a gap.
- Phase 30's other plans (magic-bytes sniffing, duration guard, the converter itself) remain independent of this plan's deliverables and can proceed.

---
*Phase: 30-audio-engine-foundation*
*Completed: 2026-07-18*

## Self-Check: PASSED

- FOUND: internal/convert/audioopts.go
- FOUND: internal/convert/audioopts_test.go
- FOUND: .planning/phases/30-audio-engine-foundation/30-02-SUMMARY.md
- FOUND: 7681e23 (Task 1 commit)
- FOUND: e268dd8 (Task 2 commit)
