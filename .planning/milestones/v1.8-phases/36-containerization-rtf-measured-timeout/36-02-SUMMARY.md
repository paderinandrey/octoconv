---
phase: 36-containerization-rtf-measured-timeout
plan: 02
subsystem: infra
tags: [docker, ffmpeg, cve-pin, rtf-measurement, av-worker, lavfi]

# Dependency graph
requires:
  - phase: 34-av-engine-foundation
    provides: "AVConverter with av.go's closed AVOpts allowlist (transcodeToMP4Args/transcodeToWebMArgs/thumbnailArgs/extractAudioArgs argv shapes, x264DefaultCRF/x265DefaultCRF constants)"
  - phase: 35-queue-worker-routing-integration
    provides: "cmd/av-worker entrypoint, av queue registration"
  - phase: 32-containerization-local-e2e-rtf-gate (v1.7)
    provides: "Dockerfile.audio-worker 3-stage shape + scripts/audio-rtf-measure.sh skeleton, both cloned structurally"
provides:
  - "Dockerfile.av-worker: from-source ffmpeg n8.1.2 (CVE-2026-8461-clean) with fail-loud commit-pin guard, minimal codec build, USER nobody runtime"
  - "scripts/av-rtf-measure.sh: operator-run RTF matrix gate (VP9/HEVC/H264 x 480/720/1080 + 2160p passthrough), measurement-integrity-only exit code"
  - "Live-verified functional coverage: all production argv shapes (mp4 transcode, webm transcode, thumbnail jpg/png/webp, audio extract mp3) work against the built binary"
affects: [36-03-PLAN, 36-04-PLAN, 37-keda-integration]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "From-source engine build with fail-loud commit-pin guard (git checkout --detach + rev-parse HEAD equality), second instance of the Dockerfile.audio-worker pattern"
    - "Minimal --disable-everything ffmpeg build restricting compiled-in codecs/muxers/protocols/filters to exactly the closed AVOpts surface"
    - "RTF matrix measurement script (codec x resolution sweep) as a structural evolution of the single-fixture RTF script pattern"

key-files:
  created:
    - Dockerfile.av-worker
    - scripts/av-rtf-measure.sh
  modified: []

key-decisions:
  - "ROADMAP SC1's 'Debian apt 5.1.x with CVE backports' wording is superseded by D-01/D-10 (from-source n8.1.2); the from-source build satisfies SC1's intent (version-pinned, CVE-clean, fail-loud), not treated as a deviation"
  - "lavfi indev + testsrc/sine filters + wrapped_avframe decoder added to the minimal ffmpeg build (beyond the RESEARCH.md flag list) because Task 2's RTF measurement script structurally requires ffmpeg lavfi fixture synthesis -- confirmed live that the RESEARCH.md-recommended flag list alone rejects '-f lavfi' entirely"
  - "format/aformat/aresample filters + --enable-zlib + webp muxer added after live smoke-testing the full production argv suite (transcodeToMP4Args/transcodeToWebMArgs/thumbnailArgs/extractAudioArgs) against the built binary -- these are PRODUCTION-PATH load-bearing, not measurement-only: without them, any audio resample/format conversion breaks, and png/webp thumbnail targets fail entirely"
  - "RTF matrix cells use a fixture synthesized directly at the target resolution (no -vf scale in the timed argv) for both the enum cells (480/720/1080) and the passthrough cell (2160p) -- matches 36-PATTERNS.md's own worked example; the passthrough cell's distinguishing feature is the height value being outside the {480,720,1080} enum, not a code-path difference"

patterns-established:
  - "Second from-source-engine-in-a-throwaway-Docker-stage instance (after whisper.cpp) — confirms this is now a repeatable fleet convention for CVE-pinned CLI-tool engines"
  - "RTF measurement script matrix-sweep extension: audio's single-fixture RTF script generalizes cleanly to a codec x resolution nested loop with per-cell RESULT tagging"

requirements-completed: [AVE-04]

# Metrics
duration: 30min
completed: 2026-07-22
---

# Phase 36 Plan 02: Containerization & RTF Matrix Script Summary

**From-source ffmpeg n8.1.2 av-worker image (CVE-2026-8461-clean, fail-loud pin guard) plus a VP9/HEVC/H264 x 480/720/1080/passthrough RTF measurement script, both live-verified against the full production argv suite**

## Performance

- **Duration:** ~30 min
- **Started:** 2026-07-22T14:41:00Z (approx, continuation from 36-01)
- **Completed:** 2026-07-22T15:06:16Z
- **Tasks:** 2 completed
- **Files modified:** 2 created (Dockerfile.av-worker, scripts/av-rtf-measure.sh)

## Accomplishments

- `Dockerfile.av-worker` builds ffmpeg from pinned source (tag `n8.1.2`, peeled commit `38b88335f99e76ed89ff3c93f877fdefce736c13`) in a throwaway 3-stage Docker build, with a fail-loud `git rev-parse HEAD` guard proven live (a deliberately wrong `FFMPEG_COMMIT` build-arg fails the build at the `git checkout --detach` step) and a belt-and-suspenders runtime `ffmpeg -version` assertion
- Minimal `--disable-everything` codec build covers the full closed AVOpts surface (h264/hevc/vp9 encode, aac/opus/mp3/webp, mp4/webm/image2/webp muxers, `file`/`crypto` protocol only — no network protocols compiled in at all, second structural layer beneath AVE-02's `-protocol_whitelist` argv flag)
- Runtime stage is `debian:bookworm-slim`, `USER nobody`, apt-installs only the codec runtime shared libs, COPYs only the compiled ffmpeg/ffprobe binaries — zero `apt-get install ffmpeg` anywhere
- `scripts/av-rtf-measure.sh` sweeps VP9 (first, per D-09), HEVC, and H.264 across 480/720/1080 plus a 2160p passthrough cross-check cell (no `-vf scale`), synthesizing all fixtures in-container via ffmpeg `lavfi`, timing the actual `transcodeToMP4Args`/`transcodeToWebMArgs` argv shapes from `internal/convert/av.go`
- Live smoke-verified the built image against the FULL production argv suite: mp4 transcode (h264/hevc), webm re-encode, thumbnail extraction (jpg/png/webp), audio extraction (mp3), and the `-vf scale` resize path — all work correctly against real generated video, not just `ffmpeg -version`
- Ran a live smoke-scale execution of `scripts/av-rtf-measure.sh` itself (tiny fixtures, 1 run) for all three codecs — confirmed the script's full pipeline (build, container, cgroup thread derivation, fixture synth, timed loop, p95, memory, image size) completes with exit 0, and incidentally reproduced the RESEARCH.md-flagged concern live: VP9 and HEVC RTF land near/above 1.0 even on tiny fixtures, while H.264 stays comfortably fast

## Task Commits

Each task was committed atomically:

1. **Task 1: Dockerfile.av-worker — from-source ffmpeg with fail-loud pin guard + minimal codec build** - `1771f3e` (feat)
2. **Task 2: scripts/av-rtf-measure.sh — RTF matrix gate (VP9 + HEVC sweep, lavfi fixtures)** - `5ee5fda` (feat)

**Plan metadata:** (this commit, docs)

## Files Created/Modified

- `Dockerfile.av-worker` - 3-stage from-source ffmpeg n8.1.2 build (build / ffmpeg-build / runtime), fail-loud commit-pin guard, minimal `--disable-everything` codec build, `USER nobody`
- `scripts/av-rtf-measure.sh` - operator-run RTF matrix measurement script (codec x resolution sweep + passthrough cross-check), measurement-integrity-only exit code

## Decisions Made

- Used the struct-field-free, zero-code-change approach for this plan: both tasks are pure infra artifacts (Dockerfile + shell script), no `internal/convert`/`cmd/av-worker` changes were needed here (those are 36-01's/36-03's territory per the phase's wave split)
- Kept the fixture-at-target-resolution approach for RTF matrix cells (no `-vf scale` applied even for the 480/720/1080 enum cells) — matches `36-PATTERNS.md`'s own worked script example exactly, and correctly isolates encode-cost-at-resolution-N as the measured variable rather than conflating it with scale-filter overhead
- See also "key-decisions" in frontmatter for the three build-flag deviations (lavfi/testsrc/sine/wrapped_avframe, format/aformat/aresample/zlib/webp-muxer)

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue] ffmpeg build rejected `-f lavfi` entirely**

- **Found during:** Task 1 live build/smoke verification (before Task 2 could even be authored against a working image)
- **Issue:** The RESEARCH.md/PATTERNS.md-recommended `--enable-*` flag list does not include the `lavfi` input device or the `testsrc`/`sine` source filters. Task 2 (`scripts/av-rtf-measure.sh`) is explicitly required to synthesize fixtures via `ffmpeg -f lavfi -i testsrc=... -f lavfi -i sine=...` — a build without these flags fails immediately with `Unknown input format: 'lavfi'`.
- **Fix:** Added `--enable-indev=lavfi` (lavfi is registered as an INPUT DEVICE in `libavdevice/alldevices.c`, not a demuxer, despite the internal struct name `ff_lavfi_demuxer`) and added `testsrc`/`sine` to `--enable-filter` (verified against n8.1.2's `libavfilter/allfilters.c`: `ff_vsrc_testsrc`, `ff_asrc_sine`). Also had to add `wrapped_avframe` to `--enable-decoder` after a second live failure (`Decoding requested, but no decoder found for: wrapped_avframe` — the lavfi testsrc source's internal raw-frame passthrough decoder).
- **Files modified:** `Dockerfile.av-worker`
- **Verification:** Live `docker exec` smoke test: `ffmpeg -f lavfi -i "testsrc=duration=2:size=320x240:rate=15" -f lavfi -i "sine=..." ...` succeeds and produces a valid mp4/webm output for all three codecs (h264/hevc/vp9)
- **Committed in:** `1771f3e`

**2. [Rule 1 - Bug] Production audio/thumbnail argv paths were broken by the minimal build**

- **Found during:** Task 1 live smoke verification, running the FULL production argv suite (`transcodeToMP4Args`/`transcodeToWebMArgs`/`thumbnailArgs`/`extractAudioArgs` shapes from `av.go`) against the built binary, per RESEARCH.md Pattern 2's explicit flagged requirement ("ship the build behind the existing live-binary suite... treat 'expand the allowlist' as the safe failure mode")
- **Issue:** Three separate live failures: (a) `'aresample' filter not present, cannot convert formats` on ANY audio path needing resample/format conversion (not just the lavfi fixture path — this would have broken real production audio-extract/transcode jobs too); (b) `Unknown encoder 'png'` — the png/apng encoders require zlib (`png_encoder_select=deflate_wrapper` → `deflate_wrapper_deps=zlib`), which `./configure` only autodetects when `zlib1g-dev` headers are present, silently dropping the encoder without it; (c) `Unable to choose an output format for '....webp'` — the `image2` muxer's own extension table does not claim `.webp` (verified against `img2enc.c`), so thumbnail requests for `target=webp` failed even with `libwebp` itself enabled.
- **Fix:** Added `format`/`aformat`/`aresample` to `--enable-filter` (verified against `fftools/ffmpeg_filter.c`'s explicit `format`/`aformat` output-filter insertion and `libavfilter/formats.c`'s `conversion_filter = "aresample"` table); added `zlib1g-dev` to the build-stage apt-install and `--enable-zlib` to configure; added the dedicated `webp` muxer to `--enable-muxer` (distinct from `image2`).
- **Files modified:** `Dockerfile.av-worker`
- **Verification:** Live re-run of the full production argv suite after each fix — all of mp4 transcode (h264+hevc), webm re-encode from a real mp4 source, `-vf scale` resize, thumbnail extraction (jpg/png/webp), and mp3 audio-extract now succeed against real generated video/audio content
- **Committed in:** `1771f3e`

**3. [Rule 1 - Bug, cosmetic] `libvpx_vp9` vs `libvpx-vp9` naming in the task's decoder smoke-gate acceptance criterion**

- **Found during:** Task 1's decoder smoke gate verification
- **Issue:** The plan's acceptance-criteria grep pattern for the decoder smoke gate listed `libvpx_vp9` (matching the `--enable-encoder=` configure flag spelling), but ffmpeg's actual `-encoders` listing displays the encoder as `libvpx-vp9` (hyphen, not underscore) — a purely cosmetic naming difference between the configure-flag spelling and the runtime display name, not a functional gap.
- **Fix:** No code change needed; confirmed via `ffmpeg -encoders | grep -i vp9` that the encoder is present and functional (verified end-to-end via a real VP9/webm encode in the follow-up smoke tests).
- **Files modified:** none
- **Verification:** `docker run ... ffmpeg -hide_banner -encoders | grep -i vp9` shows `libvpx-vp9`; a full VP9 encode via `scripts/av-rtf-measure.sh`'s smoke run succeeded
- **Committed in:** n/a (no code change, verification-only)

---

**Total deviations:** 3 (2 build-flag Rule 3/Rule 1 fixes bundled into Task 1's single commit, 1 cosmetic non-fix)
**Impact on plan:** All fixes were necessary for correctness — without them either Task 2's measurement script would not run at all (lavfi/testsrc/sine/wrapped_avframe) or real production jobs would silently fail (aresample/zlib/webp-muxer, a genuine regression vs. a hypothetical full-featured build, exactly the risk RESEARCH.md Pattern 2 flagged and required a live smoke-test gate to catch). No scope creep — no application code (`internal/convert`, `cmd/av-worker`) was touched, only the Dockerfile's configure flag list.

## Issues Encountered

None beyond the deviations above, which were caught and resolved during the plan's own mandated live-build/live-smoke-test verification steps (not surprises found later).

## User Setup Required

None — no external service configuration required. The image build and RTF script are both fully autonomous artifacts; the SUPERVISED RTF measurement RUN (executing `scripts/av-rtf-measure.sh` for real against the 480/720/1080/passthrough matrix at production-realistic fixture durations, and accepting the derived `AV_ENGINE_TIMEOUT`/`AV_MAX_DURATION_SECONDS` numbers) is explicitly deferred to Plan 04 per D-05.

## Next Phase Readiness

- `Dockerfile.av-worker` builds successfully and is proven to (a) fail loud on a moved/wrong pin, (b) report the correct ffmpeg version, (c) expose every AVOpts-required encoder, and (d) correctly execute every production argv shape this codebase generates (transcode, thumbnail, audio-extract, resize) — ready for Plan 03/04 to wire compose/CI/env-parity and run the real supervised RTF measurement.
- `scripts/av-rtf-measure.sh` is authored, executable, shellcheck-clean, and live-smoke-tested end-to-end (small-scale) for all three codecs with exit 0 — ready for Plan 04's supervised full-scale run (realistic fixture durations, full `RUNS` count) to produce the numbers that feed the `AV_ENGINE_TIMEOUT`/`AV_MAX_DURATION_SECONDS` go/no-go decision.
- Smoke-scale RTF numbers observed this session (NOT the real measurement — tiny 1-2s fixtures, 1 run, informational only) reproduce the RESEARCH.md-flagged risk live: VP9 (~0.84-1.31 RTF) and HEVC (~1.3 RTF) both land near/above real-time even at small scale, while H.264 stays fast (~0.08 RTF) — Plan 04 should expect the NO-GO lever (lowering `AV_MAX_DURATION_SECONDS`) to be a live possibility, not a remote one, consistent with the phase's own research.
- No blockers for Plan 03/04.

## Self-Check: PASSED

- FOUND: Dockerfile.av-worker
- FOUND: scripts/av-rtf-measure.sh
- FOUND: .planning/phases/36-containerization-rtf-measured-timeout/36-02-SUMMARY.md
- FOUND commit: 1771f3e
- FOUND commit: 5ee5fda
