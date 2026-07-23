---
phase: 34-av-engine-foundation
fixed_at: 2026-07-20T00:00:00Z
review_path: .planning/phases/34-av-engine-foundation/34-REVIEW.md
iteration: 1
findings_in_scope: 15
fixed: 13
skipped: 2
status: partial
---

# Phase 34: Code Review Fix Report

**Fixed at:** 2026-07-20
**Source review:** `.planning/phases/34-av-engine-foundation/34-REVIEW.md`
**Iteration:** 1

**Summary:**
- Findings in scope (Critical + Warning): 15
- Fixed: 13
- Skipped: 2 (both deliberate Phase 34/35 scope fences)

**Verification bar met:** `gofmt -l .` clean, `go vet ./...` clean, `go test ./...`
fully green including the live ffmpeg/ffprobe tests in `internal/convert/av_test.go`
(ffmpeg 8.1.2 present on this machine). The webp thumbnail subtest did NOT skip on
this run — the local build carries libwebp — so all three thumbnail targets were
exercised end to end.

## Commits

| Hash | Scope | Findings |
|------|-------|----------|
| `d60ac84` | `avsniff.go`, `avsniff_test.go` | WR-03 |
| `123189a` | `sniff.go` | WR-11 |
| `5ceb898` | `av.go`, `avduration.go`, `avopts.go`, `audioduration.go` + tests | CR-01, CR-02, CR-03, CR-04, WR-01, WR-04, WR-05, WR-06, WR-07, WR-09, WR-10 |
| `64386de` | `whisper.go`, `whisper_test.go` | WR-08 (out-of-phase, see below) |

**Note on commit granularity:** CR-01 through CR-04 and most warnings are genuinely
coupled — they land in the same function signatures (`transcodeToMP4Args` gained both
CR-01's `height` and CR-03's `videoIndex`), and the `AVOpts.Timecode` pointer change
(CR-04) is consumed by `av.go` in the same change. Splitting them into one commit per
finding would have produced non-compiling intermediate commits, so they are grouped
into one coherent commit rather than faking atomicity. WR-03, WR-11 and WR-08 were
genuinely independent and are committed separately.

## Fixed Issues

### CR-01: `resolution_height` validated then silently discarded

**Files modified:** `internal/convert/av.go`, `internal/convert/av_test.go`
**Commit:** `5ceb898`
**Applied fix:** Added `avScaleFilter(height)` emitting `scale=-2:<h>` (width `-2`
preserves aspect ratio and keeps the dimension even, required by
libx264/libx265/libvpx-vp9), threaded `height` through both `transcodeToMP4Args` and
`transcodeToWebMArgs`, and passed `o.ResolutionHeight` from `convertTranscode`. Height
0 emits no `-vf` at all rather than `scale=-2:0`. New test
`TestTranscodeArgs_ResolutionHeightEmitsScaleFilter` pins both directions.

### CR-02: stream-copy fast path silently overrode an explicit `codec` request

**Files modified:** `internal/convert/av.go`, `internal/convert/av_test.go`
**Commit:** `5ceb898`
**Applied fix:** Extracted `avStreamCopyEligible(target, o, src)`. Container-codec
legality (`avStreamCopyLegal`) is now necessary but not sufficient: a resize
(`ResolutionHeight != 0`) always disqualifies the copy, and an explicit `Codec` only
permits a copy when it matches the source's actual codec. I took the review's second
option (`o.Codec == "" || o.Codec == srcVideoCodec`) over the stricter `o.Codec == ""`
because it honors the client's request correctly in both cases — asking for `h264` on
an already-h264 source genuinely *is* satisfied by a copy — without a pointless
re-encode. New table test `TestAVStreamCopyEligible` covers all six combinations.

### CR-03: `-c copy` copied every stream while the gate inspected only `v:0`/`a:0`

**Files modified:** `internal/convert/av.go`, `internal/convert/avduration.go`,
`internal/convert/av_test.go`, `internal/convert/avduration_test.go`
**Commit:** `5ceb898`
**Applied fix:** Both bypasses closed.

*Bypass 1 (multi-stream smuggling):* the copy path is now built by `streamCopyArgs`,
which emits `-map 0:<videoIndex> -map 0:a:0` — mapping exactly the two streams the
gate inspected, by absolute index. The transcode builders carry the same explicit map
(with `-map 0:a:0?` so an audio-less source still converts) so the re-encode and copy
paths provably agree on what "the video" means.

*Bypass 2 (cover-art aliasing — the security half):* `ffprobeStreamArgs` now selects
**every** video stream (`-select_streams v`) and requests `disposition=attached_pic`.
`avPrimaryVideoStream` picks the largest-area non-cover-art stream, and
`avMaxVideoHeight` feeds the resolution guard the tallest height across *all* streams
(fail-closed: a bomb hidden in any stream trips the ceiling).

I verified live that both mechanisms are needed: in MP4 the muxer sets `attached_pic`
but reorders streams so cover art rarely lands at `v:0`; in **MKV** the cover art stays
at `v:0` and `attached_pic` is *not* set, so only the largest-area selection saves you.
`TestProbeVideoStream_IgnoresCoverArt` therefore uses an MKV fixture (with a
self-check that skips if a future ffmpeg starts reordering MKV too). **I confirmed
this test genuinely pins the regression** by temporarily reverting the probe to
`v:0`: it fails with `codec = "mjpeg"`, `height = 32`, and
`EnforceMaxResolution(ceiling 64) = <nil>` — i.e. a 480px stream sailing past the
guard behind a 32px thumbnail.

### CR-04: sources shorter than 1.0s were permanently unconvertible to a thumbnail

**Files modified:** `internal/convert/av.go`, `internal/convert/avopts.go`,
`internal/convert/av_test.go`, `internal/convert/avopts_test.go`
**Commit:** `5ceb898`
**Applied fix:** `AVOpts.Timecode` changed from `float64` to `*float64` (see the
contract-change note below). `convertThumbnail` now clamps an *unset* timecode to
`math.Min(avDefaultThumbnailTimecode, duration/2)` so a sub-second clip still yields a
thumbnail, while an *explicit* out-of-range timecode remains a hard client error.
`isZeroAVOpts` checks `Timecode == nil` rather than `== 0`, so `{"timecode": 0}` is now
a real request that gets applicability-checked. Three new tests: sub-second source
(live 0.4s fixture), explicit-zero timecode, and the parse-level unset-vs-zero
distinction.

### WR-01: output argv element not `file:`-prefixed

**Files modified:** `internal/convert/av.go`, `internal/convert/av_test.go`
**Commit:** `5ceb898`
**Applied fix:** All five sites now emit `"file:" + outPath`. Added `avInputArgs()` so
the `-y -nostdin -protocol_whitelist file,crypto -i file:<in>` prefix is constructed in
exactly one place. Verified live that the `file:` prefix does not disturb ffmpeg's
extension-based *muxer* selection (`file:out.wav` still selects the wav muxer;
jpg/png/webp thumbnails still Sniff correctly). New
`TestAVBuildersHardenEveryInvocation` table-asserts the hardening pair plus both path
prefixes across all five builders.

### WR-04: `ErrAVOutputMissingOrEmpty` conflated client input with engine fault

**Files modified:** `internal/convert/av.go`, `internal/convert/av_test.go`
**Commit:** `5ceb898`
**Applied fix:** Added `ErrAVTimecodeOutOfRange`. `ErrAVOutputMissingOrEmpty` is now
strictly an engine-fault class used only by post-hoc output validation.
`TestAVConverter_Thumbnail_OutOfRangeSS` was updated to assert the new sentinel *and*
to assert the error is **not** folded into the engine-fault class.

### WR-05: up to five ffprobe subprocesses per job on the full engine timeout

**Files modified:** `internal/convert/av.go`, `internal/convert/audioduration.go`,
`internal/convert/avduration.go`
**Commit:** `5ceb898`
**Applied fix:** Added `avSourceProbe` + `avProbeSource`, which probes duration, video
streams and audio codec **once** under a dedicated `avProbeTimeout` (15s) child context,
honoring `ProbeDuration`'s documented "short bound distinct from the engine timeout"
contract. The result is threaded into `convertTranscode`/`convertThumbnail`. Probe
count drops from 4–5 to 3, and the guard's decision and the conversion's decision are
now derived from the same probe rather than independently. Split
`enforceMaxDurationOf` / `enforceMaxResolutionOf` out of the exported `EnforceMax*`
helpers so the ceilings apply to already-probed values without re-spawning ffprobe;
the exported functions keep their existing signatures and callers.

### WR-06: `avThreadCount()` ignored the in-repo cgroup CPU limit

**Files modified:** `internal/convert/av.go`
**Commit:** `5ceb898`
**Applied fix:** Now prefers `CgroupCPULimit()` and falls back to `runtime.NumCPU()`,
matching `cmd/audio-worker`. Relevant because `docker-compose.yml` limits the worker to
`cpus: "2.0"`.

### WR-07: `probeVideoStream` discarded the underlying JSON error

**Files modified:** `internal/convert/avduration.go`
**Commit:** `5ceb898`
**Applied fix:** Split into two branches — `fmt.Errorf("ffprobe: unparseable stream
probe output: %w", err)` and a distinct `ffprobe: no video stream found` — restoring
the project's `%w` wrapping convention.

### WR-09: argv builders had no `default` case

**Files modified:** `internal/convert/av.go`, `internal/convert/av_test.go`
**Commit:** `5ceb898`
**Applied fix:** `extractAudioArgs` and `thumbnailArgs` return `nil` for an out-of-set
target; both call sites treat `nil` as a programming error and return
`av: unsupported <stage> target %q` without invoking ffmpeg. `convertTranscode` gained
a matching `default`. I kept the `nil`-return shape rather than changing to
`([]string, error)` to avoid churning every argv-pinning test for an unreachable path.
New `TestAVBuildersFailClosedOnUnknownTarget`.

### WR-10: the protocol-whitelist canary tested ffmpeg, not the production code

**Files modified:** `internal/convert/av_test.go`
**Commit:** `5ceb898`
**Applied fix:** `TestProtocolWhitelist_BlocksHTTP_Canary` now drives the crafted
m3u8 through the real builders (`transcodeToMP4Args`, `streamCopyArgs`,
`thumbnailArgs`) as subtests, plus the full `AVConverter{}.Convert` entry point —
so deleting the flag from a builder now fails the canary. Complemented by
`TestAVBuildersHardenEveryInvocation`.

### WR-11: stale `signatures` doc comment

**Files modified:** `internal/convert/sniff.go`
**Commit:** `123189a`
**Applied fix:** Rewrote the comment to describe the actual scope, state explicitly
that mp4/mov/avi are detected ahead of engine registration, explain why mkv/webm are
absent (variable-offset EBML walk, not a fixed 12-byte window), and point at Phase 35.
This is the documentation half of skipped finding WR-02 — it makes the temporary
asymmetry deliberate and legible rather than accidental.

### WR-03: `int(size)` / `int(headerSize)` truncation on 32-bit

**Files modified:** `internal/convert/avsniff.go`, `internal/convert/avsniff_test.go`
**Commit:** `d60ac84`
**Applied fix:** Both bounds checks now compare in `uint64` space before any narrowing.
Added `TestMatchEBML_RejectsHugeSizeVint` covering `0x80000000`, `0x100000000` and
`0x00FFFFFFFFFFFFFF` in both the element-size and header-size positions.

One correction to the review's framing worth recording: the *header-size* position was
never a panic risk — the original code already clamped `end` to `len(buf)`. A truncated
value there would have silently abandoned the walk (misreporting a real mkv as
unrecognized), not over-read. The test asserts the clamped bounded scan still finds a
DocType that genuinely sits inside the peeked window. The *element-size* position is
the one that could have produced the negative-index slice panic the review describes.

### WR-08: false "every invocation, no exception" hardening claim (OUT OF PHASE)

**Files modified:** `internal/convert/whisper.go`, `internal/convert/whisper_test.go`
**Commit:** `64386de` — deliberately separate, prefixed `fix(34-review):`
**Applied fix:** `ffmpegNormalizeArgs` now carries `-nostdin` and
`-protocol_whitelist file,crypto`, and `file:`-prefixes the output path.

**This is an out-of-phase fix and should be reviewed as such.** It lives in Phase 30's
`whisper.go`, not Phase 34's file set. It was fixed rather than deferred because it is
a genuine, currently-shipping hardening hole: that invocation runs ffmpeg on
**untrusted client audio uploads** with no protocol whitelist, so a crafted container
(e.g. an HLS playlist referencing `http://169.254.169.254/latest/meta-data/`) could
make ffmpeg open a network protocol during demux. The change is small and
self-contained, and it makes the existing universal claim in `audioduration.go` true
rather than requiring the claim to be narrowed.

I verified the hardened argv against real ffmpeg 8.1.2 — the wav muxer is still
selected correctly through the `file:` prefix (`pcm_s16le,16000,1`), and `normPath` is
still passed unprefixed to `whisperArgs` and to the downstream `os.Stat`, so only the
ffmpeg argv element changed.

## Skipped Issues

### WR-02: `SniffVideo` has zero production callers

**File:** `internal/convert/avsniff.go:190`, `internal/convert/sniff.go:44-46`
**Reason:** Skipped — deliberate Phase 34/35 scope fence, per the phase constraints.
Both remedies the review offers are out of scope: wiring `SniffVideo` into the
`handlers.go` chain adds an `internal/api` call site (explicitly fenced off, since
API-side wiring is Phase 35), and holding the `signatures` additions back would regress
a Phase 34 deliverable.
**Mitigation applied:** WR-11's comment rewrite (`123189a`) documents the asymmetry
explicitly — that mp4/mov/avi are live ahead of registration, that mkv/webm are not,
that both halves are fail-closed in the interim, and that Phase 35 lands them together.
**Original issue:** mkv/webm uploads are rejected as `unrecognized_content` while
mp4/mov/avi are accepted and rejected one step later at pair validation; once Phase 35
registers `AVConverter`, mkv/webm would be advertised by `GET /v1/formats` while
remaining undetectable at upload.
**Phase 35 must:** wire `SniffVideo` into the handlers chain (chained off `rest`, not
`file`) in the same change that registers `AVConverter`.

### IN-01 / IN-02

**Reason:** Out of scope — `fix_scope` is `critical_warning`, so Info findings were not
attempted. IN-01 (latent m4a/mp4 detection collision once `AVConverter` registers) is
worth carrying into the Phase 35 plan; the review's own suggestion (reclassify to `m4a`
when `Sniff` says `mp4` but no video stream is found) composes cleanly with the
`probeVideoStreams` refactor landed here.

## Contract Change Requiring Review

Per the phase constraint on `AVOpts` contract changes, flagging this explicitly:

**`AVOpts.Timecode` changed from `float64` to `*float64`.**

The review's CR-04 required distinguishing "unset" from "explicitly zero", and offered
either a pointer or a companion `TimecodeSet bool`. I chose the pointer because a
separate bool cannot be populated by `encoding/json` without custom `UnmarshalJSON`,
which would undercut the strict-decode discipline (`checkStrictObject` +
`DisallowUnknownFields`) that `ParseAVOpts` is built on.

Semantics chosen, deliberately conservative and fail-closed:

- `nil` (field absent) → default seek point, **clamped** to `min(1.0, duration/2)`. A
  sub-second source now succeeds instead of failing unconditionally.
- non-nil pointer to `0` → an explicit first-frame request. Honored, and now correctly
  subject to `ValidateAVApplicability` (previously it short-circuited as "no options").
- non-nil and `>= duration` → hard rejection with `ErrAVTimecodeOutOfRange`. I did
  **not** clamp explicit out-of-range values, even though the review noted clamping is
  "friendlier" — silently retargeting an explicit client request is the same class of
  defect as CR-01/CR-02, so an explicit bad value stays an error.

Existing tests remain meaningful: the ~2s fixture yields `min(1.0, 1.0) = 1.0`, i.e.
byte-identical behavior to the old hardcoded default.

Also tightened while in `ParseAVOpts`: `NaN`/`±Inf` timecodes are now rejected. The old
`o.Timecode < 0` check let `NaN` through (all NaN comparisons are false) and it would
have reached ffmpeg's `-ss` argv as `NaN`.

## Residual Risk / Follow-ups for Phase 35

1. **`-map 0:a:0` on the copy path assumes an audio stream exists.** Safe today because
   `avStreamCopyLegal` cannot match an empty audio codec, so an audio-less source never
   reaches `streamCopyArgs`. The transcode path uses the optional `-map 0:a:0?` and is
   unaffected. Worth a regression test when the engine is registered.
2. **`avProbeSource` requires a video stream.** A container with audio only fails at the
   guard stage. This is correct for the AV engine but interacts with IN-01: once
   registered, a generic-brand `.m4a` misdetected as `mp4` would route here and fail on
   the missing video stream. IN-01's reclassification should land in the same phase.
3. **Cover-art selection is largest-area, not "reject on disagreement".** The review
   offered both. Largest-area was chosen because rejection would break legitimate
   multi-stream files, and the resolution guard independently uses the *max* height
   across all streams, so the decode-bomb axis is fail-closed regardless of which stream
   is selected for conversion.

---

_Fixed: 2026-07-20_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
