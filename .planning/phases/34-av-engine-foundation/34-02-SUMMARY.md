---
phase: 34-av-engine-foundation
plan: 02
subsystem: convert
tags: [ffmpeg, ffprobe, av-engine, validation, allowlist, ssrf-defense]

# Dependency graph
requires:
  - phase: 30-audio-engine
    provides: checkStrictObject, ParseAudioOpts/AudioOptsFromMap shape, ProbeDuration/EnforceMaxDuration, runCommand hardened exec
provides:
  - AVOpts closed allowlist (timecode/resolution_height/codec) with ParseAVOpts/AVOptsFromMap/ValidateAVApplicability
  - EngineAV = "av" engine-class constant
  - probeVideoStream + EnforceMaxResolution video-resolution decode-bomb guard
  - Protocol-whitelisted ffprobe on both the new resolution probe and the reused duration probe
affects: [34-av-engine-foundation plan 03 (AVConverter Convert path), 35-av-engine-registration]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "AVOpts strict-parse mirrors AudioOpts/DocOpts (checkStrictObject + DisallowUnknownFields + per-field allowlist/range check)"
    - "Closed resolution-height enum and codec allowlist select server-side constants (CRF), never pass raw client bytes into ffmpeg argv"
    - "avStreamProbe/ffprobeStreamArgs/probeVideoStream mirrors ProbeDuration's runCommand+parse split for independent testability"
    - "-protocol_whitelist file,crypto required on every ffmpeg/ffprobe invocation, including reused probes invoked on new untrusted input types"

key-files:
  created:
    - internal/convert/avopts.go
    - internal/convert/avopts_test.go
    - internal/convert/avduration.go
    - internal/convert/avduration_test.go
  modified:
    - internal/convert/convert.go
    - internal/convert/audioduration.go
    - internal/convert/audioduration_test.go

key-decisions:
  - "x264DefaultCRF=23 and x265DefaultCRF=28 kept as two distinct compile-time constants, never shared (AVO-03/Pitfall 4)"
  - "AVOpts.Timecode is a plain float64 range-checked >=0 here; duration-relative clamping deferred to Plan 03's Convert"
  - "ffprobeDurationArgs hardened with -protocol_whitelist file,crypto in addition to ffprobeStreamArgs, since Plan 03 invokes the reused duration probe FIRST on untrusted video input (AVE-02/ROADMAP SC5 'every ffprobe invocation')"
  - "EnforceMaxResolution's maxHeight stays a plain function parameter, no os.Getenv read in internal/convert (env wiring deferred to a later phase, mirrors EnforceMaxDuration's own note)"

patterns-established:
  - "AVOpts.go / avduration.go are the fifth 1:1-mirrored precedent pair (after DocOpts/audioopts/opts.go/audioduration.go) for future engine-class implementers"

requirements-completed: [AVO-01, AVO-02, AVO-03, AVE-02]

# Metrics
duration: ~15min
completed: 2026-07-19
---

# Phase 34 Plan 02: AV Opts, Resolution Guard, Probe Hardening Summary

**Closed AVOpts allowlist (timecode/resolution-height enum/HEVC codec) with distinct x264/x265 CRF constants, a new protocol-whitelisted ffprobe resolution guard, and hardening of the reused duration probe with `-protocol_whitelist file,crypto`.**

## Performance

- **Duration:** ~15 min
- **Completed:** 2026-07-19
- **Tasks:** 3
- **Files modified:** 7 (4 created, 3 modified)

## Accomplishments
- `AVOpts` strict-parses `timecode`/`resolution_height`/`codec` through `checkStrictObject` + `DisallowUnknownFields`, rejecting unknown fields, duplicate keys, trailing bytes, top-level null, negative timecode, out-of-enum resolution height, and unknown codec
- `ValidateAVApplicability` scopes `Timecode` to thumbnail targets (jpg/png/webp), `ResolutionHeight` to transcode targets (mp4/webm), and `Codec=="hevc"` to mp4, all gated on the new `EngineAV` constant
- Two distinct CRF server constants (`x264DefaultCRF=23`, `x265DefaultCRF=28`) with a dedicated test proving they never collapse into one shared value
- `probeVideoStream`/`EnforceMaxResolution` reject an over-ceiling declared video resolution before the expensive decode stage, using a protocol-whitelisted ffprobe call
- The reused `ffprobeDurationArgs` (invoked first on untrusted video by Plan 03) now also carries `-protocol_whitelist file,crypto`, closing the "every ffmpeg/ffprobe invocation" gap for that probe

## Task Commits

Each task was committed atomically:

1. **Task 1: AVOpts closed allowlist and EngineAV constant** - `bd6416e` (feat)
2. **Task 2: Video resolution guard (probeVideoStream + EnforceMaxResolution)** - `6783e5a` (feat)
3. **Task 3: Harden the reused duration probe (ffprobeDurationArgs protocol-whitelist)** - `d34be7a` (fix)

_Note: this plan's tasks were `tdd="true"` but executed as tests-and-implementation-together commits (test file + implementation file committed in the same task commit), consistent with how the analogous `audioopts.go`/`audioduration.go` precedent files were built in Phase 30 -- no separate RED-only commit was made._

## Files Created/Modified
- `internal/convert/avopts.go` - AVOpts struct, ParseAVOpts, AVOptsFromMap, ValidateAVApplicability, isZeroAVOpts, closed enum/codec allowlists, x264/x265 CRF constants
- `internal/convert/avopts_test.go` - table-driven strict-parse, round-trip, applicability, and CRF-distinctness tests
- `internal/convert/avduration.go` - ErrAVResolutionExceeded, avStreamProbe, ffprobeStreamArgs (protocol-whitelisted), probeVideoStream, EnforceMaxResolution
- `internal/convert/avduration_test.go` - argv-pinning hardening test (ungated) plus live-gated tests using ffmpeg-lavfi-generated synthetic fixtures
- `internal/convert/convert.go` - added `EngineAV = "av"` to the engine-class const block (no Register/Default/Classes change)
- `internal/convert/audioduration.go` - `ffprobeDurationArgs` now prepends `-protocol_whitelist file,crypto` before the `file:`-prefixed path element; `ProbeDuration`/`EnforceMaxDuration` signatures/behavior unchanged
- `internal/convert/audioduration_test.go` - `TestFfprobeDurationArgs_FilePrefix` extended to assert the new whitelist elements

## Decisions Made
- Kept `ValidateAVApplicability` as its own function scoped to `EngineAV` rather than merging into the shared `ValidateApplicability` (opts.go), mirroring `ValidateAudioApplicability`'s engine-scoped precedent.
- Chose to prepend `-protocol_whitelist file,crypto` immediately before the `file:`-prefixed path element in both `ffprobeStreamArgs` and `ffprobeDurationArgs`, so both hardened probes share an identical argv shape/position.
- Live-gated resolution-guard tests generate tiny synthetic video fixtures on the fly via `ffmpeg -f lavfi -i color=...` rather than committing a binary video fixture to the repo, since ffmpeg was confirmed present locally.

## Deviations from Plan

None - plan executed exactly as written. All `must_haves` truths, artifacts, and key_links from the plan frontmatter are satisfied:
- Client-supplied timecode/resolution/codec validated through the closed AVOpts allowlist before reaching any ffmpeg argv
- Out-of-enum resolution height and unknown codec rejected with distinct errors
- Unknown fields, duplicate keys, trailing bytes, and top-level null all rejected
- `EnforceMaxResolution` rejects an over-ceiling declared video resolution before the expensive decode stage
- x264/x265 CRF constants are distinct, verified by `TestCRFConstantsDistinct`
- `ffprobeDurationArgs` (the reused probe Plan 03 invokes first on untrusted video) carries `-protocol_whitelist file,crypto`

## Issues Encountered
- The plan's `<verify>` blocks specify `cd /Users/apaderin/dev/octoconv && go test ...`, which is the main repo path, not this worktree's path -- running that literally would have tested the wrong checkout. Ran `go test`/`gofmt`/`go vet` from the worktree's own working directory instead (already the correct cwd), which exercised this plan's actual changes. No code deviation, only an execution-environment adjustment for the worktree isolation model.

## User Setup Required

None - no external service configuration required. `ffmpeg`/`ffprobe` were already required by the audio engine (Phase 30) and are present on this dev machine; no new dependency introduced.

## Next Phase Readiness
- Plan 03 can now call `AVOptsFromMap`/`ValidateAVApplicability`/`EnforceMaxResolution` (alongside the already-reused `EnforceMaxDuration`) to build `AVConverter`'s guard stage before wiring the actual transcode/thumbnail/audio-extract ffmpeg invocations.
- `EngineAV` constant exists for Plan 03/35's registration and applicability checks; `converters.go`/`Register`/`Default` remain untouched (scope fence intact for Phase 35).
- No blockers.

## Self-Check: PASSED

- FOUND: internal/convert/avopts.go
- FOUND: internal/convert/avopts_test.go
- FOUND: internal/convert/avduration.go
- FOUND: internal/convert/avduration_test.go
- FOUND: internal/convert/convert.go (EngineAV present)
- FOUND: internal/convert/audioduration.go (protocol_whitelist present)
- FOUND: internal/convert/audioduration_test.go (updated argv assertion)
- FOUND commit bd6416e
- FOUND commit 6783e5a
- FOUND commit d34be7a

---
*Phase: 34-av-engine-foundation*
*Completed: 2026-07-19*
