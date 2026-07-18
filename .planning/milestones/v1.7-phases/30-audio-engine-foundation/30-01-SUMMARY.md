---
phase: 30-audio-engine-foundation
plan: 01
subsystem: convert
tags: [whisper.cpp, ffmpeg, ffprobe, magic-bytes, id3v2, mp3, decompression-bomb-guard, audio]

# Dependency graph
requires: []
provides:
  - "whisper-cli v1.9.1 built locally (pinned tag, -DGGML_NATIVE=OFF), on PATH at /opt/homebrew/bin"
  - "SHA-256-verified ggml-base.bin at $HOME/.cache/whisper/ggml-base.bin"
  - "Live-verified whisper-cli -ojf JSON schema (transcription[].{timestamps,offsets,text,tokens}, tokens[].{text,p,id})"
  - "internal/convert/testdata/audio/ fixtures (jfk.wav, sample.wav, sample-id3.mp3, sample.m4a)"
  - "internal/convert.SniffAudio + matchWAV/matchOGG/matchM4A/matchMP3 (AUD-01)"
  - "internal/convert.ProbeDuration + EnforceMaxDuration + ErrAudioDurationExceeded (AUD-04)"
affects: [30-02, 30-03, 31-audio-queue-worker, 32-audio-container-model]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Bounded, variable-offset, fail-closed magic-bytes detector (mp3's ID3v2 synchsafe skip) as a sibling to the fixed-window sniff.go signatures table, not merged into it"
    - "Declared-duration guard via hardened subprocess (ffprobe through runCommand) as the audio analog of dimensions.go's declared-pixel-ceiling guard"

key-files:
  created:
    - internal/convert/audiosniff.go
    - internal/convert/audiosniff_test.go
    - internal/convert/audioduration.go
    - internal/convert/audioduration_test.go
    - internal/convert/testdata/audio/jfk.wav
    - internal/convert/testdata/audio/sample.wav
    - internal/convert/testdata/audio/sample-id3.mp3
    - internal/convert/testdata/audio/sample.m4a
  modified:
    - internal/convert/sniff.go

key-decisions:
  - "whisper-cli installed to /opt/homebrew/bin instead of $(go env GOPATH)/bin (plan's stated preference) because GOPATH/bin was not actually on this session's interactive PATH; verified with command -v before proceeding"
  - "Relocated whisper-cli's dylib rpath from /tmp/whisper.cpp/build/bin to $HOME/.cache/whisper/lib via install_name_tool so the binary survives /tmp cleanup across reboots/sessions"
  - "Accepted residual risk: hallucination-on-silence is not caught by EnforceMaxDuration or any other control in this plan (documented in audioduration.go's package-level doc comment, per RESEARCH.md's SC5 framing)"

patterns-established:
  - "Two-tier peek-length discipline: sniffLen=12 (fixed-format signatures table) vs mp3PeekLen=512KiB (variable-offset audio detector) — same fail-closed bounded-peek contract, different window sizes for different reasons"

requirements-completed: [AUD-01, AUD-04]

# Metrics
duration: ~75min
completed: 2026-07-18
---

# Phase 30 Plan 01: Audio Engine Foundation — Toolchain + Content Validation Summary

**Local whisper-cli v1.9.1 toolchain provisioned and live-verified, plus a bespoke ID3v2-aware MP3 magic-bytes detector and an ffprobe-based declared-duration guard, both fail-closed and unit-tested in `internal/convert`.**

## Performance

- **Duration:** ~75 min (dominated by the whisper.cpp source build)
- **Completed:** 2026-07-18
- **Tasks:** 3/3 completed
- **Files modified:** 9 (5 created test-data fixtures, 2 new Go source files, 2 new Go test files, 1 modified)

## Accomplishments

- whisper-cli v1.9.1 built from the pinned git tag with `-DGGML_NATIVE=OFF` (load-bearing: avoids `-march=native` SIGILL on non-build-host CPUs), resolves on PATH, and its dylib dependencies were relocated to a persistent cache directory so the binary survives `/tmp` cleanup
- `ggml-base.bin` downloaded and SHA-256-verified byte-for-byte against the pinned checksum (`60ed5bc3...fba2efe`)
- Live `-ojf` spot-check against the `jfk.wav` sample confirms the exact JSON schema RESEARCH.md predicted from source: `transcription[].{timestamps,offsets,text,tokens}` and `tokens[].{text,p,id,t_dtw}` all present
- `SniffAudio` (AUD-01): mp3 (ID3v2-tagged + bare + footer-flag variant), wav, ogg, m4a all detected by magic bytes; a crafted oversized synchsafe size and a truncated ID3v2 header both fail closed; non-allowlisted `ftyp` brands (`qt  `, `mp41`) are rejected, proving the closed-brand-table discipline (not a bare `ftyp` presence check)
- `ProbeDuration`/`EnforceMaxDuration` (AUD-04): declared duration over a supplied ceiling is rejected with `ErrAudioDurationExceeded` (`errors.Is`-matchable) before any decode; unparseable ffprobe output returns a wrapped error, never a silent zero-duration pass; hallucination-on-silence recorded as an accepted residual risk

## Task Commits

Each task was committed atomically:

1. **Task 1: Provision whisper-cli v1.9.1 + base model + audio test fixtures** - `1fbc407` (chore)
2. **Task 2: audiosniff.go — ID3v2-aware variable-offset magic-bytes detector (AUD-01)** - `ad90b0d` (test, RED) → `7db9a00` (feat, GREEN)
3. **Task 3: audioduration.go — ffprobe declared-duration guard (AUD-04)** - `ee52b60` (test, RED) → `5d41089` (feat, GREEN)

_TDD tasks (2 and 3) each have a RED test commit followed by a GREEN implementation commit; no REFACTOR commit was needed for either._

## Files Created/Modified

- `internal/convert/audiosniff.go` - `matchWAV`/`matchOGG`/`matchM4A`/`matchMP3` + `SniffAudio` (ID3v2 synchsafe decode, footer-flag handling, fail-closed bound check)
- `internal/convert/audiosniff_test.go` - table/case tests for every format, adversarial fail-closed cases, real ffmpeg fixtures
- `internal/convert/audioduration.go` - `ProbeDuration` (ffprobe wrapper via `runCommand`), `EnforceMaxDuration`, `ErrAudioDurationExceeded`
- `internal/convert/audioduration_test.go` - `exec.LookPath("ffprobe")`-gated under/over-ceiling and unparseable-output tests
- `internal/convert/sniff.go` - one-line doc comment noting audio content is detected via the separate `SniffAudio`, not the fixed-window `signatures` table
- `internal/convert/testdata/audio/jfk.wav` - whisper.cpp's own speech sample, used for the live `-ojf` schema spot-check
- `internal/convert/testdata/audio/sample.wav`, `sample-id3.mp3`, `sample.m4a` - ffmpeg-generated container fixtures (2s sine tone) covering the RIFF/WAVE, ID3v2-tagged-mp3, and ftyp/M4A magic-bytes cases

## Decisions Made

- **Install location deviation:** the plan's step (c) preferred `$(go env GOPATH)/bin`, assuming it is "reliably on PATH." In this session's interactive PATH it was not. Rather than silently trusting the preference, I confirmed with `command -v whisper-cli` (as the plan's own acceptance criterion requires) and, finding it absent, installed to `/opt/homebrew/bin` instead — a directory independently confirmed to already be on PATH. This satisfies the plan's actual acceptance criterion ("`command -v whisper-cli` resolves to a path on the interactive PATH") more reliably than following the letter of step (c).
- **Dylib rpath relocation (Rule 3 — blocking issue avoided proactively):** the freshly-built `whisper-cli` binary's `LC_RPATH` pointed at `/tmp/whisper.cpp/build/bin` by default. Since `/tmp` is commonly cleared across reboots/sessions, this would silently break a *later* executor session's live-binary tests (the exact failure mode Task 1's "so a later executor session resolves it" requirement is meant to prevent). Copied the five `.dylib` files to `$HOME/.cache/whisper/lib` and used `install_name_tool -rpath` to repoint the binary there; verified by removing `/tmp/whisper.cpp/build/bin` entirely and re-running `whisper-cli -h` successfully.
- **Accepted residual risk (hallucination-on-silence):** recorded directly in `audioduration.go`'s package/function doc comment per RESEARCH.md's explicit instruction — no code attempts to detect or mitigate it in this plan.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] whisper-cli install directory not actually on PATH; installed to a directory that is**
- **Found during:** Task 1
- **Issue:** `$(go env GOPATH)/bin` (the plan's preferred install location) was not present in this session's interactive `$PATH`, so `command -v whisper-cli` would have failed the plan's own acceptance criterion.
- **Fix:** Installed the built binary to `/opt/homebrew/bin` (confirmed already on `$PATH`) instead.
- **Files modified:** none (binary install, not a repo file)
- **Verification:** `command -v whisper-cli` resolves; `whisper-cli -h` runs successfully.
- **Committed in:** N/A (local toolchain provisioning, not a repo change)

**2. [Rule 3 - Blocking] whisper-cli's dylib rpath pointed at an ephemeral /tmp build directory**
- **Found during:** Task 1
- **Issue:** `otool -l` showed `LC_RPATH /tmp/whisper.cpp/build/bin` — a future session where `/tmp` has been cleared would see `whisper-cli` fail to load its shared libraries, breaking Plan 03's live tests.
- **Fix:** Copied the five `.dylib` files to `$HOME/.cache/whisper/lib` (persistent, user-owned) and used `install_name_tool -rpath /tmp/whisper.cpp/build/bin $HOME/.cache/whisper/lib whisper-cli` to repoint the binary.
- **Files modified:** none (binary/library provisioning, not a repo file)
- **Verification:** Removed `/tmp/whisper.cpp/build/bin` entirely, re-ran `whisper-cli -h` — succeeded.
- **Committed in:** N/A (local toolchain provisioning, not a repo change)

---

**Total deviations:** 2 auto-fixed (both Rule 3 — blocking, both local toolchain provisioning, no repo code affected)
**Impact on plan:** Both fixes only affect the local dev-machine toolchain setup, not committed code. No scope creep; the plan's actual acceptance criteria (PATH resolution, checksum match, live JSON schema) are all met more robustly than a literal reading of step (c) would have produced.

## Issues Encountered

None beyond the two deviations above, which were resolved inline during Task 1.

## User Setup Required

None — no external service configuration required. The whisper-cli binary and model are provisioned on this local dev machine only (not committed, not containerized); a later phase (32) will bake the equivalent toolchain into the audio-worker Docker image from source, independent of this local setup.

## Next Phase Readiness

- `internal/convert.SniffAudio`, `ProbeDuration`, and `EnforceMaxDuration` are ready for Plan 02/03 to build `AudioConverter`/`AudioOpts`/the two-stage ffmpeg→whisper-cli pipeline on top of.
- Live-binary tests gated behind `exec.LookPath("ffprobe")`/`exec.LookPath("whisper-cli")` (per RESEARCH.md's `whisper_test.go` skeleton) can now run against a real pinned-version binary on this machine, not just skip.
- No blockers. The whisper-cli/ffmpeg toolchain provisioned here is local-machine-only; Phase 32's containerized Dockerfile build is a separate, independent concern (already scoped out of this plan).

---
*Phase: 30-audio-engine-foundation*
*Completed: 2026-07-18*

## Self-Check: PASSED

All 8 created files verified present on disk; all 6 commits (1fbc407, ad90b0d, 7db9a00, ee52b60, 5d41089, 79d7f0a) verified present in git log.
