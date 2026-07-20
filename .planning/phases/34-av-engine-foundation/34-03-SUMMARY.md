---
phase: 34-av-engine-foundation
plan: 03
subsystem: convert
tags: [ffmpeg, ffprobe, av-engine, transcode, thumbnail, ssrf-defense, codec-allowlist]

# Dependency graph
requires:
  - phase: 34-av-engine-foundation (plan 02)
    provides: AVOpts (Timecode/ResolutionHeight/Codec), EngineAV constant, EnforceMaxResolution, protocol-whitelisted ProbeDuration/EnforceMaxDuration
provides:
  - "AVConverter (Pairs/Convert/Engine) -- the standalone, unregistered fifth engine class"
  - "transcodeToMP4Args/transcodeToWebMArgs/extractAudioArgs/thumbnailArgs argv builders, every one hardened with -protocol_whitelist file,crypto + file:-prefixed -i"
  - "avStreamCopyLegal project-owned codec allowlist gating the stream-copy fast path (never ffmpeg muxer acceptance)"
  - "validateAVOutput fail-closed output post-validation, thumbnail-aware (re-Sniff())"
  - "Live-verified argv/behavior against real ffmpeg 8.1.2/ffprobe 8.1.2, including the AVC-05 VP9->mp4 re-encode contract and the AVE-02 protocol-whitelist offline canary"
affects: [35-av-engine-registration]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Target-driven argv dispatch mirroring whisperOutputFlag: Convert() inspects NormalizeFormat(ext(outPath)) once, dispatches to one of three isolated pure argv-builder functions"
    - "Stream-copy fast path gated on a project-owned per-container codec allowlist (avStreamCopyLegal), checked via a hardened ffprobe probe BEFORE any -c copy attempt -- never on ffmpeg's own muxer permissiveness"
    - "Fail-closed output post-validation: missing file and zero-byte file map to the same ErrAVOutputMissingOrEmpty sentinel; thumbnail targets additionally re-Sniff() the output bytes"

key-files:
  created:
    - internal/convert/av.go
    - internal/convert/av_test.go
  modified: []

key-decisions:
  - "Pairs() locked per 34-RESEARCH.md Open Question 1: transcode {mov,avi,mkv,webm}->mp4 + mp4->webm; audio-extract and thumbnail from all five detected video formats -- shapes Phase 35's cross-converter disjointness test"
  - "avMaxSourceDuration (4h) and avMaxSourceResolutionHeight (4320/8K) are plain package constants, not env-read -- mirrors EnforceMaxDuration's own contract; real ceilings are wired in Phase 36"
  - "Thumbnail out-of-range timecode is rejected pre-flight (before invoking ffmpeg at all), reusing the SAME ErrAVOutputMissingOrEmpty sentinel validateAVOutput's missing/empty checks use, so callers can errors.Is-match regardless of which guard caught it"
  - "avThreadCount() is a plain runtime.NumCPU() read, no env wiring or cgroup-aware sizing -- deferred, mirrors 34-PATTERNS.md's note that wiring threads is premature for an unregistered/unqueued converter"

patterns-established:
  - "av.go/av_test.go is the fifth 1:1-mirrored engine-converter pair (after whisper.go/whisper_test.go) for future engine-class implementers"

requirements-completed: [AVC-01, AVC-02, AVC-03, AVC-04, AVC-05, AVE-02]

# Metrics
duration: ~35min
completed: 2026-07-20
---

# Phase 34 Plan 03: AVConverter (transcode/extract/thumbnail) Summary

**Standalone AVConverter shelling out to ffmpeg/ffprobe: mov/avi/mkv/webm->mp4 (H.264/AAC/+faststart) and mp4->webm (VP9/Opus) transcode with a codec-allowlist-gated stream-copy fast path, audio-extract to mp3/wav/m4a with AAC->m4a stream-copy, and input-side-seek thumbnail extraction to jpg/png/webp -- every ffmpeg/ffprobe invocation hardened with `-protocol_whitelist file,crypto`, live-verified against a real ffmpeg 8.1.2 binary, deliberately unregistered.**

## Performance

- **Duration:** ~35 min
- **Started:** 2026-07-20T11:20:00Z (approx)
- **Completed:** 2026-07-20T11:57:00Z
- **Tasks:** 3 completed
- **Files modified:** 2 (both created)

## Accomplishments
- `AVConverter` (struct, `Pairs()`/`Engine()`==`EngineAV`) with the locked pair set: transcode {mov,avi,mkv,webm}->mp4 + mp4->webm; audio-extract and thumbnail from all five detected video formats -- self-disjointness proven, zero duplicate pairs
- Three isolated, pure ffmpeg argv builders (`transcodeToMP4Args` HEVC-aware, `transcodeToWebMArgs`, `extractAudioArgs`, `thumbnailArgs`), every one leading with `-y -nostdin -protocol_whitelist file,crypto` and a `file:`-prefixed `-i` path -- argv-pinned by unconditional tests, including proof the HEVC branch uses `x265DefaultCRF` (28) and never `x264DefaultCRF` (23)
- `Convert()` orchestration: `AVOptsFromMap` -> fail-fast target dispatch (before any subprocess) -> duration+resolution guard stage (`EnforceMaxDuration`+`EnforceMaxResolution`, reused/Plan-02) BEFORE any decode/encode -> one of three ffmpeg pipelines, each error-prefixed `"av: <stage>:"`
- `avStreamCopyLegal`: a project-owned per-container codec allowlist (mp4<-h264/aac, webm<-vp9/opus) checked via a hardened ffprobe audio-codec probe BEFORE any `-c copy` attempt -- live-proven this session that a VP9/Opus source targeting mp4 is correctly re-encoded to h264/aac, NOT silently remuxed (the AVC-05/T-34-11 load-bearing contract)
- `validateAVOutput` fails closed identically on a missing output file and a zero-byte one (`ErrAVOutputMissingOrEmpty`); thumbnail targets are additionally re-`Sniff()`'d to confirm the bytes actually decode as the requested image format
- Thumbnail out-of-range `-ss` is rejected pre-flight against `ProbeDuration`, live-proven to fail closed with no output file created (Pitfall 2)
- `TestProtocolWhitelist_BlocksHTTP_Canary`: a crafted HLS `http://` segment reference is live-proven blocked by `-protocol_whitelist file,crypto`, zero outbound connection, no output file (AVE-02's required offline canary)
- Full live-binary suite passes against a real `ffmpeg 8.1.2`/`ffprobe 8.1.2` (mov->mp4, mp4->webm, VP9/Opus->mp4 re-encode, mp3/wav/m4a extract incl. AAC->m4a stream-copy, jpg/png thumbnails); the webp thumbnail subtest skips cleanly (this dev machine's ffmpeg build lacks the `libwebp` encoder, matching 34-RESEARCH.md's own documented finding)

## Task Commits

Each task was committed atomically:

1. **Task 1: AVConverter struct, Pairs(), and the three ffmpeg argv builders** - `1a97116` (feat)
2. **Task 2: Convert() orchestration -- guard stage, stream-copy gating, codec probe** - `bae30c3` (feat)
3. **Task 3: Output post-validation + live/canary test suite** - `fba9fb0` (feat)

**Plan metadata:** (this commit) - docs: complete plan

## Files Created/Modified
- `internal/convert/av.go` - `AVConverter` (Pairs/Engine/Convert), argv builders, `avStreamCopyLegal`, `ffprobeAudioCodecArgs`/`probeAudioCodec`, `validateAVOutput`, `avMaxSourceDuration`/`avMaxSourceResolutionHeight`, `ErrAVOutputMissingOrEmpty`
- `internal/convert/av_test.go` - argv-pinning unit tests, `requireLiveAVBinaries`/`requireLibwebpEncoder` skip-gates, fail-fast/table tests, live-binary transcode/extract/thumbnail suite, VP9->mp4 re-encode contract test, out-of-range-`-ss`/Sniff-mismatch tests, protocol-whitelist offline canary

## Decisions Made
- Followed 34-RESEARCH.md's Pattern 1/Pattern 2 argv shapes and stream-copy-allowlist algorithm verbatim (both already live-verified against a real ffmpeg/ffprobe binary in that research session).
- Chose plain-parameter guard ceilings (`avMaxSourceDuration = 4h`, `avMaxSourceResolutionHeight = 4320`) rather than any env read, per the plan's explicit "no os.Getenv in internal/convert" constraint and 34-RESEARCH.md Open Question 2's resolution -- real ceilings are Phase 36 scope.
- Reused the same `ErrAVOutputMissingOrEmpty` sentinel for both the thumbnail path's pre-flight out-of-range rejection and `validateAVOutput`'s post-hoc missing/empty check, so callers get one `errors.Is`-matchable class regardless of which guard fired -- not explicitly specified by the plan text but the natural implementation of Pitfall 2's "handle both shapes" requirement.
- Did not implement a package-level `SetAVThreads`/cgroup-wiring mechanism this phase (34-PATTERNS.md flags it as reusable but not required); `avThreadCount()` is a bare `runtime.NumCPU()` call, consistent with "wiring is premature for an unregistered/unqueued converter."

## Deviations from Plan

None - plan executed exactly as written. All `must_haves` truths, artifacts, and key_links from the plan frontmatter are satisfied:
- AVConverter transcodes mov/avi/mkv/webm to mp4 (H.264/AAC, +faststart) and mp4 to webm (VP9/Opus, always full re-encode), both live-verified
- Audio extraction to mp3/wav/m4a with AAC-source->m4a stream-copy, live-verified
- Thumbnail extraction via input-side `-ss`, 1.0s default, bounds-checked against duration, fails closed on out-of-range, live-verified
- The stream-copy fast path is gated on `avStreamCopyLegal`'s project-owned allowlist, never ffmpeg muxer acceptance -- proven by the VP9/Opus->mp4 re-encode contract test
- `-protocol_whitelist file,crypto` present on every ffmpeg/ffprobe argv builder, proven by the offline HTTP canary
- The duration+resolution guard stage runs before any ffmpeg encode/decode
- `AVConverter` remains unregistered: `converters.go` untouched (confirmed via `git diff` against the pre-plan commit), `grep -c 'converters\|Default.Register\|Register(' internal/convert/av.go` returns 0
- `grep -c 'os.Getenv' internal/convert/av.go` returns 0

## Issues Encountered
None. `ffmpeg`/`ffprobe` were already present on this dev machine (8.1.2, Homebrew arm64); the `libwebp` encoder is absent locally, exactly as 34-RESEARCH.md documented, and the webp thumbnail live subtest skips cleanly rather than failing.

## User Setup Required

None - no external service configuration required. `ffmpeg`/`ffprobe` are already required by earlier phases and present on this dev machine; no new dependency introduced.

## Next Phase Readiness
- `AVConverter` is fully built and live-tested; Phase 35 can register it into `convert.Default` (`converters.go`), wire the `av` queue/worker, and add the cross-converter `Pairs()` disjointness test against `AudioConverter.Pairs()`.
- The stream-copy allowlist (`avStreamCopyLegal`), guard-stage ceilings, and `ErrAVOutputMissingOrEmpty` sentinel are all available for Phase 35's worker-layer terminal/transient classifier to build on (mirrors `isAudioTerminal`'s `"audio: ffmpeg:"`-prefix convention via this plan's `"av: ffmpeg:"`/`"av: ffprobe:"` prefixes).
- `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./internal/convert/` all pass cleanly at HEAD (full package, live tests included -- webp thumbnail skips cleanly on this dev machine).
- No blockers.

---
*Phase: 34-av-engine-foundation*
*Completed: 2026-07-20*
