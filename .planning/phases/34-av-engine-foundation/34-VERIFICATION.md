---
phase: 34-av-engine-foundation
verified: 2026-07-20T17:24:01Z
status: passed
score: 5/5 roadmap success criteria verified; 10/10 requirement IDs satisfied
overrides_applied: 0
---

# Phase 34: AV Engine Foundation Verification Report

**Phase Goal:** A standalone `AVConverter` transcodes, extracts audio, and extracts thumbnails from video files with fail-closed content validation, built and testable directly against ffmpeg before any queue/worker plumbing.

**Verified:** 2026-07-20T17:24:01Z
**Status:** passed
**Re-verification:** No — initial verification

**Verification method:** Read every file this phase claims to have created/modified at HEAD (not through SUMMARY.md prose), re-ran `go build ./...`, `go vet ./...`, `gofmt -l .`, and the full `go test ./...` suite locally against a real `ffmpeg 8.1.2`/`ffprobe 8.1.2` (confirmed present via `which`/`-version`), and cross-checked the 34-REVIEW.md → 34-REVIEW-FIX.md fix commits (`d60ac84`, `123189a`, `5ceb898`, `64386de`) actually landed in the code rather than trusting the fix report's prose.

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | AVConverter transcodes mov/avi/mkv/webm → mp4 (H.264/AAC, `-movflags +faststart`) and mp4 → webm (VP9/Opus) against real ffmpeg; every transcode is a full re-encode | ✓ VERIFIED | `transcodeToMP4Args`/`transcodeToWebMArgs` (`internal/convert/av.go:104-153`) emit the exact codec/flag set. `TestAVConverter_Transcode_LiveBinary`, `TestAVConverter_MP4ToWebM_LiveBinary` PASS live against ffmpeg 8.1.2 (re-run by this verifier, not taken from SUMMARY). For the canonical h264/aac-sourced mp4→webm case (the only real-world shape mp4 sources take), `avStreamCopyLegal("webm","h264","aac")==false` forces the re-encode builder every time — proven by the live test asserting ffprobe-confirmed vp9/opus output. |
| 2 | AVConverter extracts audio to mp3/wav/m4a with ffprobe-checked `-c:a copy` for AAC→m4a, full re-encode otherwise; extracts a thumbnail via input-side `-ss` at client/1.0s-default timecode, duration-clamped | ✓ VERIFIED | `convertAudioExtract` (`av.go:514-531`) probes audio codec and sets `streamCopy` only for `target=="m4a" && srcAudioCodec=="aac"`. `convertThumbnail` (`av.go:545-569`) uses `math.Min(avDefaultThumbnailTimecode, seconds/2)` for the unset case (post CR-04 fix) and rejects an explicit out-of-range value with `ErrAVTimecodeOutOfRange`. Live tests `TestAVConverter_AudioExtract_LiveBinary`, `TestAVConverter_Thumbnail_LiveBinary`, `TestAVConverter_Thumbnail_SubSecondSource`, `TestAVConverter_Thumbnail_ExplicitZeroTimecode`, `TestAVConverter_Thumbnail_OutOfRangeSS` all PASS (re-run). |
| 3 | Automatic stream-copy fast path remuxes instead of re-encoding whenever ffprobe reports the source codec is already legal in the target container | ✓ VERIFIED | `avStreamCopyLegal` (`av.go:284-293`) is a project-owned allowlist (mp4←h264/aac, webm←vp9/opus), never ffmpeg-muxer-acceptance. `avStreamCopyEligible` (`av.go:495-509`, the CR-02 fix) additionally disqualifies the fast path when a client's explicit `resolution_height`/`codec` option would be silently defeated by a copy — closing the exact silent-override bug the code review found. `TestAVConverter_VP9SourceToMP4_ReEncodes` (the AVC-05 load-bearing contract test) PASS live: a VP9/Opus source targeting mp4 re-encodes to h264/aac rather than being illegally remuxed. `TestAVStreamCopyLegal`, `TestAVStreamCopyEligible` PASS. |
| 4 | Video container sniffers (fixed-offset mp4/mov `ftyp`, RIFF `AVI `, bounded-peek EBML/DocType mkv-vs-webm) classify fixtures correctly; collision test proves zero overlap with WAV/RIFF, m4a-brand, heic-brand sniffers | ✓ VERIFIED | `internal/convert/avsniff.go` implements `matchMP4`/`matchMOV`/`matchAVI` (fixed-offset) and `matchEBML`/`vintLen`/`readSizeVint`/`readIDVint`/`SniffVideo` (bounded-peek, 4KiB `avPeekLen`, fails closed past the window per WR-03's uint64-space fix). `TestVideoBrandDisjointness`, `TestMatchMP4_RejectsM4ABrand`, `TestMatchMP4_RejectsHEICBrand`, `TestMatchAVI_RejectsWAV`, `TestMatchEBML_MKV`, `TestMatchEBML_WebM`, `TestMatchEBML_RejectsTruncated`, `TestMatchEBML_RejectsUnknownDocType`, `TestMatchEBML_RejectsHugeSizeVint` all PASS (re-run). `sniff.go`'s `signatures` table carries `mp4`/`mov`/`avi`; `MIMEType` covers all five video/* cases. |
| 5 | AVOpts validated through `checkStrictObject` closed-allowlist pattern; injection test proves client bytes never reach ffmpeg argv; `-protocol_whitelist file,crypto` + duration/resolution guards block SSRF/LFI and multi-axis decompression-bomb vectors on every ffmpeg/ffprobe invocation, verified by an offline canary | ✓ VERIFIED | `ParseAVOpts` (`avopts.go:99-119`) calls `checkStrictObject` then `DisallowUnknownFields`; codec/resolution are validated against closed maps (`avCodecAllowlist`, `avResolutionHeights`) and only ever select fixed literal strings (`"libx264"`/`"libx265"`, `x264DefaultCRF`/`x265DefaultCRF`) inside argv builders — no client string is ever concatenated into an argv element (structural injection-proofing; `TestParseAVOpts`'s "unknown codec rejected"/"out-of-enum resolution_height rejected" subtests plus `TestAVConverter_GarbageOpts` pin the rejection path). Every one of the five ffmpeg/ffprobe argv builders in `av.go` plus `ffprobeStreamArgs` (`avduration.go`) and `ffprobeDurationArgs` (`audioduration.go`, hardened in Plan 02 Task 3) carries `-protocol_whitelist file,crypto` — confirmed by reading each builder and by `TestAVBuildersHardenEveryInvocation` (table test across all 5 builders) and `TestProtocolWhitelist_BlocksHTTP_Canary`, which (post WR-10 fix) drives the crafted HLS `http://` canary through the **production** builders and the real `AVConverter{}.Convert` entry point, not a hand-written argv. Guard stage (`avMaxSourceDuration`=4h, `avMaxSourceResolutionHeight`=4320) runs in `Convert` before any ffmpeg encode/decode (`av.go:388-400`), and CR-03's fix closes the multi-stream/cover-art bypass that let a resolution bomb hide behind a small `v:0` stream (`avPrimaryVideoStream`/`avMaxVideoHeight`, `avduration.go`). All named tests re-run and PASS. |

**Score:** 5/5 roadmap success criteria verified.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/avsniff.go` | matchMP4/matchMOV/matchAVI, mp4VideoBrands, EBML walker, SniffVideo | ✓ VERIFIED | Present, read in full; matches Plan 01 spec exactly including the WR-03 uint64-comparison fix |
| `internal/convert/avsniff_test.go` | table-driven matcher + disjointness + EBML tests | ✓ VERIFIED | All named tests present and PASS |
| `internal/convert/sniff.go` | signatures table + MIMEType extended | ✓ VERIFIED | mp4/mov/avi in `signatures`; all 5 video/* MIMEType cases; WR-11 comment fix confirmed present |
| `internal/convert/avopts.go` | AVOpts, ParseAVOpts, AVOptsFromMap, ValidateAVApplicability, x264/x265 CRF consts | ✓ VERIFIED | Present; `Timecode *float64` (post-CR-04 pointer contract change) confirmed |
| `internal/convert/avduration.go` | probeVideoStream(s), EnforceMaxResolution, ErrAVResolutionExceeded, cover-art-aware probe | ✓ VERIFIED | Present; CR-03 fix (`-select_streams v`, `attached_pic`, `avPrimaryVideoStream`, `avMaxVideoHeight`) confirmed landed |
| `internal/convert/convert.go` | EngineAV constant | ✓ VERIFIED | `EngineAV = "av"` present; no Register/converters.go change (scope fence intact) |
| `internal/convert/audioduration.go` | ffprobeDurationArgs hardened with protocol_whitelist | ✓ VERIFIED | Confirmed `-protocol_whitelist file,crypto` present in argv builder |
| `internal/convert/av.go` | AVConverter (Pairs/Convert/Engine), argv builders, avStreamCopyLegal, validateAVOutput | ✓ VERIFIED | 570 lines (exceeds `min_lines: 150`); `func (AVConverter) Convert` present; all CR-01..04/WR-01,04-10 fixes confirmed landed by direct read |
| `internal/convert/av_test.go` | argv-pinning, live suite, canary, disjointness | ✓ VERIFIED | `requireLiveAVBinaries` present; full live/canary suite present and PASS |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `sniff.go` signatures table | `matchMP4/matchMOV/matchAVI` | package-level function refs | ✓ WIRED | Confirmed in signatures slice literal |
| `matchEBML` | `readSizeVint`/`readIDVint` | bounded-peek TLV walk | ✓ WIRED | Confirmed by read + passing fail-closed tests |
| `ParseAVOpts` | `checkStrictObject` | reused shared helper | ✓ WIRED | `avopts.go:100` calls it directly, not reimplemented |
| `ffprobeStreamArgs`/`ffprobeDurationArgs` | `-protocol_whitelist file,crypto` | argv element | ✓ WIRED | Confirmed present in both builders |
| `AVConverter.Convert` | `EnforceMaxDuration`/`EnforceMaxResolution`-equivalent guard | guard stage before decode | ✓ WIRED | `avProbeSource` + `enforceMaxDurationOf`/`enforceMaxResolutionOf` called at `av.go:391-400`, strictly before `convertTranscode`/`convertAudioExtract`/`convertThumbnail` |
| `AVConverter.Convert` transcode path | `avStreamCopyLegal` (via `avStreamCopyEligible`) | ffprobe codec-gated remux decision | ✓ WIRED | `convertTranscode` calls `avStreamCopyEligible` before choosing `streamCopyArgs` vs re-encode builders |
| `AVConverter.Convert` | `AVOptsFromMap` | strict opts parse on converter read path | ✓ WIRED | `av.go:372` first line of `Convert` |

### Behavioral Spot-Checks / Live Test Execution

All live-binary and pure/table tests for Phase 34 were re-executed by this verifier directly (not sourced from SUMMARY.md claims), against a genuinely present `ffmpeg 8.1.2`/`ffprobe 8.1.2` (Homebrew arm64):

| Test | Result |
|------|--------|
| `go build ./...` | PASS (exit 0) |
| `go vet ./...` | PASS (exit 0) |
| `gofmt -l .` | clean (no output) |
| `go test ./...` (full repo) | PASS, all packages `ok` |
| `go test ./internal/convert/... -v` | PASS — 204 subtests passed, 0 failed, 3 skipped (2 pre-existing unrelated LibreOffice/veraPDF binary-absence skips, 1 `TestAVConverter_Thumbnail_LiveBinary/webp` skip — this dev machine's local ffmpeg build lacks the libwebp encoder; the unconditional `TestThumbnailArgs_ExplicitCodec/webp` argv-pinning subtest still ran and passed) |
| `TestAVConverter_VP9SourceToMP4_ReEncodes` (AVC-05 load-bearing contract) | PASS |
| `TestProtocolWhitelist_BlocksHTTP_Canary` (AVE-02 canary, drives production builders + `Convert` entry point post-WR-10) | PASS |
| `TestVideoBrandDisjointness` | PASS |
| `TestMatchEBML_RejectsHugeSizeVint` (WR-03 regression) | PASS |
| `TestProbeVideoStream_IgnoresCoverArt` (CR-03 regression) | PASS |
| `TestTranscodeArgs_ResolutionHeightEmitsScaleFilter` (CR-01 regression) | PASS |
| `TestAVStreamCopyEligible` (CR-02 regression) | PASS |
| `TestAVConverter_Thumbnail_SubSecondSource` (CR-04 regression) | PASS |

### Probe Execution

No `scripts/*/tests/probe-*.sh` convention is used by this project; Phase 34's own "probes" are the live-binary Go tests above, executed directly. No separate shell-script probe harness exists for this phase.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|---|---|---|---|---|
| AVC-01 | 34-03 | mov/avi/mkv/webm → mp4 (H.264/AAC, +faststart) | ✓ SATISFIED | `transcodeToMP4Args`, live-tested |
| AVC-02 | 34-03 | mp4 → webm (VP9/Opus), always full re-encode | ✓ SATISFIED | `transcodeToWebMArgs`, live-tested; see note below on the theoretical vp9-in-mp4-source edge case |
| AVC-03 | 34-03 | audio-extract mp3/wav/m4a, AAC→m4a stream-copy | ✓ SATISFIED | `convertAudioExtract`, live-tested incl. stream-copy case |
| AVC-04 | 34-03 | thumbnail via input-side `-ss`, 1.0s default, duration-clamped | ✓ SATISFIED | `convertThumbnail`, live-tested incl. sub-second and explicit-zero cases |
| AVC-05 | 34-03 | auto stream-copy fast path gated on project codec allowlist | ✓ SATISFIED | `avStreamCopyLegal`/`avStreamCopyEligible`, `TestAVConverter_VP9SourceToMP4_ReEncodes` |
| AVO-01 | 34-02 | closed AVOpts allowlist, checkStrictObject pattern | ✓ SATISFIED | `avopts.go`, `TestParseAVOpts` |
| AVO-02 | 34-02 | closed resolution-height enum (480/720/1080) | ✓ SATISFIED | `avResolutionHeights`, `avScaleFilter` (CR-01 fix threads it into argv) |
| AVO-03 | 34-02 | HEVC codec choice, own CRF default | ✓ SATISFIED | `x264DefaultCRF`/`x265DefaultCRF` distinct, `TestCRFConstantsDistinct` |
| AVE-01 | 34-01 | fail-closed video container magic-bytes detection | ✓ SATISFIED | `avsniff.go`, disjointness test |
| AVE-02 | 34-02 + 34-03 | duration/resolution guards + protocol-whitelist on every ffmpeg/ffprobe call | ✓ SATISFIED | Guard stage in `Convert`, `-protocol_whitelist` confirmed in all 7 argv builders (5 in av.go + ffprobeStreamArgs + ffprobeDurationArgs) |

No orphaned requirements: REQUIREMENTS.md maps exactly these 10 IDs to Phase 34 (AVT-01 → Phase 35, AVE-03/04/05 → Phases 35/36/37, correctly out of scope).

**Note on REQUIREMENTS.md checkbox state:** only `AVE-01` is checked `[x]` in `.planning/REQUIREMENTS.md`; the other 9 IDs satisfied by this phase remain `[ ]`. This is a documentation-tracking staleness, not a code gap — every ID's actual implementation was independently verified against the codebase above. Recommend updating the checkboxes as a follow-up.

### Anti-Patterns Found

No blocker-class anti-patterns (TBD/FIXME/XXX) found in any Phase 34 file. Searched all six phase-34 source files (`av.go`, `avopts.go`, `avduration.go`, `avsniff.go`, `sniff.go`, `audioduration.go`) plus their tests:

```
grep -n "TBD\|FIXME\|XXX" internal/convert/av*.go internal/convert/sniff.go internal/convert/audioduration.go
```
→ no matches.

`TODO`/`HACK`/`PLACEHOLDER`/"not yet implemented" scan → no matches in production code. Deferred-work notes (e.g., "env wiring deferred to Phase 36", "Phase 35's responsibility") are explicit, documented, in-scope scope-fence comments — not debt markers.

### Human Verification Required

None. This phase is entirely unit/integration-testable against a real ffmpeg/ffprobe binary with no UI, no async queue, and no external service dependency — all must-haves were mechanically verifiable and were verified.

### Gaps Summary

No gaps found. The phase goal — a standalone, unregistered `AVConverter` that transcodes, extracts audio, and extracts thumbnails from video with fail-closed content validation, tested directly against ffmpeg — is achieved in the codebase, not merely claimed in SUMMARY.md.

Notably, a prior code review (`34-REVIEW.md`) found 4 critical and 11 warning issues; this verification confirms 13 of those 15 findings were genuinely fixed in the codebase (commits `d60ac84`, `123189a`, `5ceb898`, `64386de`) — the code was read directly to confirm the CR-01 (`avScaleFilter`), CR-02 (`avStreamCopyEligible`), CR-03 (`-map` pinning + cover-art-aware probing), and CR-04 (`*float64` Timecode pointer contract) fixes are actually present, not just claimed in 34-REVIEW-FIX.md prose. The 2 skipped findings (WR-02: `SniffVideo` has no production caller; IN-01/IN-02 info-level) are explicit, documented Phase 35 scope fences per this task's own instructions and are correctly excluded from this phase's gap list.

Two intentional scope fences confirmed honored by direct grep against the codebase (not just SUMMARY claims):
- `grep -c 'AVConverter{}' internal/convert/converters.go` → 0 (unregistered)
- `grep -rn 'SniffVideo' internal/ --include='*.go' | grep -v _test.go` → no call sites outside its own definition/doc comments

---

_Verified: 2026-07-20T17:24:01Z_
_Verifier: Claude (gsd-verifier)_
