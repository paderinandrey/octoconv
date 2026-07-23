---
phase: 34-av-engine-foundation
reviewed: 2026-07-20T00:00:00Z
depth: standard
files_reviewed: 12
files_reviewed_list:
  - internal/convert/av.go
  - internal/convert/av_test.go
  - internal/convert/avopts.go
  - internal/convert/avopts_test.go
  - internal/convert/avduration.go
  - internal/convert/avduration_test.go
  - internal/convert/avsniff.go
  - internal/convert/avsniff_test.go
  - internal/convert/sniff.go
  - internal/convert/convert.go
  - internal/convert/audioduration.go
  - internal/convert/audioduration_test.go
findings:
  critical: 4
  warning: 11
  info: 2
  total: 17
status: issues_found
---

# Phase 34: Code Review Report

**Reviewed:** 2026-07-20
**Depth:** standard
**Files Reviewed:** 12
**Status:** issues_found

## Summary

Phase 34 adds video-container sniffing (`avsniff.go`), a closed AV opts allowlist
(`avopts.go`), a resolution guard (`avduration.go`), and the `AVConverter`
ffmpeg/ffprobe shell-out (`av.go`). The hardening posture on the **input** side is
genuinely good: every ffmpeg/ffprobe argv built in these files carries
`-protocol_whitelist file,crypto` and a `file:`-prefixed `-i`/probe path, the EBML
parser is bounded and fails closed on 64-bit, and `avStreamCopyLegal` is correctly a
project-owned codec table rather than a muxer-acceptance test. `go vet ./internal/convert/`
is clean.

The defects are concentrated in the **conversion contract**, not the sniffer. Two
client-visible options (`resolution_height`, `codec`) are accepted, validated, and
then silently dropped or overridden. The stream-copy gate inspects only stream 0 of
each type while `-c copy` copies every stream, which reopens exactly the AVC-05 hole
`avStreamCopyLegal` was written to close. And the thumbnail path makes any source
shorter than one second permanently unconvertible.

Several findings are latent rather than live because `AVConverter` is deliberately not
registered into `convert.Default` this phase — but the mp4/mov/avi entries added to
`signatures` (sniff.go) **are** live in the API upload path today, while `SniffVideo`
is not wired anywhere at all. That asymmetry is worth resolving before Phase 35
registers the engine.

## Critical Issues

### CR-01: `resolution_height` is validated then silently discarded

**File:** `internal/convert/avopts.go:76`, `internal/convert/av.go:85-117,323-357`
**Issue:** `AVOpts.ResolutionHeight` is parsed, range-checked against
`avResolutionHeights`, and applicability-checked by `ValidateAVApplicability` — but no
code path ever reads it. `transcodeToMP4Args` and `transcodeToWebMArgs` take only
`(inPath, outPath, codec, threads)`; neither emits a `-vf scale=...` filter, and
`convertTranscode` never passes `o.ResolutionHeight` anywhere. A client that requests
`{"resolution_height": 480}` receives a full-resolution transcode with **no error and
no warning**. Repo-wide grep confirms zero non-test, non-comment reads of the field.

This directly contradicts the type's own doc comment ("once validated,
ResolutionHeight/Codec select a server-side constant (scale filter, CRF)") — the CRF
half was implemented, the scale half was not.

Silently ignoring an accepted client option is a correctness failure, not a missing
feature: the option was advertised through the parse/validate surface, so callers have
every reason to believe it took effect.

**Fix:** Thread the height into the transcode builders and emit a server-constructed
scale filter (width `-2` preserves aspect ratio and keeps the dimension even, which
libx264/libx265/libvpx-vp9 all require):

```go
func transcodeToMP4Args(inPath, outPath, codec string, height, threads int) []string {
	videoCodec := "libx264"
	crf := x264DefaultCRF
	if codec == "hevc" {
		videoCodec = "libx265"
		crf = x265DefaultCRF
	}
	args := []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:" + inPath,
	}
	if height != 0 {
		// height is already constrained to the closed avResolutionHeights
		// enum by ParseAVOpts; it is never a raw client string.
		args = append(args, "-vf", "scale=-2:"+strconv.Itoa(height))
	}
	return append(args,
		"-c:v", videoCodec, "-preset", "veryfast", "-crf", strconv.Itoa(crf),
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-threads", strconv.Itoa(threads),
		outPath)
}
```

Apply the same to `transcodeToWebMArgs`. If shipping the scale filter is genuinely
out of scope for Phase 34, then `ResolutionHeight` must be **rejected** at parse time
rather than accepted-and-ignored.

### CR-02: stream-copy fast path silently overrides an explicit `codec: "hevc"` request

**File:** `internal/convert/av.go:334-351`
**Issue:** `convertTranscode` evaluates `avStreamCopyLegal(target, srcVideoCodec,
srcAudioCodec)` **before** consulting `o.Codec`. For `target == "mp4"`, a source that
is already h264+aac takes the `-c copy` branch unconditionally. A client that
explicitly asked for `{"codec": "hevc"}` therefore receives an **H.264** output —
validated, applicability-checked, and then silently overridden. `o` is not referenced
at all inside the stream-copy branch.

This is the same class of defect as CR-01 (accepted option silently not honored) but
with an extra sharp edge: it only manifests for h264/aac sources, so it will pass any
test using a VP9 or non-AAC fixture. `TestAVConverter_VP9SourceToMP4_ReEncodes` covers
the re-encode branch and therefore cannot catch this.

**Fix:** An explicit codec request must disqualify the fast path:

```go
// An explicit client codec request is a re-encode request: a stream copy
// cannot satisfy it, so it must never win over o.Codec.
copyLegal := o.Codec == "" && avStreamCopyLegal(target, srcVideoCodec, srcAudioCodec)
if copyLegal {
	...
}
```

(Or, if `codec: "h264"` should still permit a copy of an h264 source, gate on
`o.Codec == "" || o.Codec == srcVideoCodec`.)

### CR-03: `-c copy` copies every stream, but the legality gate inspects only `v:0`/`a:0`

**File:** `internal/convert/av.go:334-343`, `internal/convert/avduration.go:37,47-61`
**Issue:** `avStreamCopyLegal` is documented as the guarantee that a `-c copy` remux
can never violate the mp4=H.264/AAC and webm=VP9/Opus contract (AVC-05/T-34-11). It
inspects exactly two streams: `probeVideoStream` uses `-select_streams v:0` and
`probeAudioCodec` uses `-select_streams a:0`. The emitted argv, however, is a bare
`-c copy` with **no `-map`**, so ffmpeg's default stream selection copies additional
streams beyond those two.

Two concrete bypasses:

1. **Multi-stream smuggling.** A source with `v:0 = h264`, `a:0 = aac`, and `a:1 =
   opus` (or a second video stream) passes the gate and is copied wholesale into the
   mp4 — the exact silent contract violation the check exists to prevent. `-c copy`
   into mp4 will happily carry codecs the project's own contract forbids.
2. **Cover-art aliasing.** ffprobe reports embedded cover art / attached pictures as
   video streams. A file whose `v:0` is a tiny `mjpeg` thumbnail and whose real video
   stream is `v:1` will (a) report `mjpeg` to `avStreamCopyLegal` and (b) — more
   seriously — report the *thumbnail's* height to `EnforceMaxResolution`, letting an
   8K+ real video stream pass the decode-bomb guard at `av.go:305`.

Bypass (2) makes this a security finding, not only a contract finding: it defeats the
resolution axis of the multi-axis decompression-bomb defense.

**Fix:** Pin stream selection explicitly on both the copy path and the probe path so
the streams that are inspected are exactly the streams that are muxed:

```go
args = []string{
	"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
	"-i", "file:" + inPath,
	// AVC-05: copy EXACTLY the two streams avStreamCopyLegal inspected --
	// a bare "-c copy" would carry additional streams past the gate.
	"-map", "0:v:0", "-map", "0:a:0",
	"-c", "copy",
}
```

and, for the guard, either reject sources whose video streams disagree, or select the
largest video stream rather than `v:0`. At minimum, exclude attached pictures via
`-show_entries stream=codec_name,width,height,disposition=attached_pic` and skip
streams with `attached_pic == 1` in `probeVideoStream`. The transcode branches should
likewise carry explicit `-map` so the re-encode path and the copy path agree on what
"the video" means.

### CR-04: every source shorter than 1.0s is permanently unconvertible to a thumbnail

**File:** `internal/convert/av.go:381-392`
**Issue:** `convertThumbnail` substitutes `timecode = 1.0` whenever `o.Timecode == 0`,
then rejects with `ErrAVOutputMissingOrEmpty` if `timecode >= dur.Seconds()`. For any
legitimate source shorter than one second — a short clip, a GIF-style loop, a
generated fixture — the default seek point is unconditionally past the end, so the
request **always** fails with no client-side remedy other than guessing a smaller
timecode.

Two compounding bugs in the same three lines:

- The `== 0` sentinel conflates "unset" with an explicit request for the first frame.
  `Timecode` is `float64` with `omitempty`, so `{"timecode": 0}` is indistinguishable
  from an absent field — and `isZeroAVOpts` (avopts.go:161) additionally treats
  `{"timecode": 0}` as "no options requested", short-circuiting
  `ValidateAVApplicability` entirely. A client cannot ask for frame 0.
- The bound is exclusive (`>=`) against the container's *declared* duration, which is
  stricter than necessary; a seek to exactly `dur` is the only genuinely invalid case,
  and even then clamping is friendlier than rejecting.

**Fix:** Use a pointer or an explicit "requested" flag to distinguish unset from zero,
and clamp the default against the actual duration instead of failing:

```go
dur, err := ProbeDuration(ctx, inPath)
if err != nil {
	return fmt.Errorf("av: ffprobe: %w", err)
}
timecode := o.Timecode
if !o.TimecodeSet { // or: o.Timecode == nil
	// Default to 1.0s, but never past the source: a sub-second clip must
	// still yield a thumbnail rather than fail closed.
	timecode = math.Min(1.0, dur.Seconds()/2)
}
if timecode >= dur.Seconds() {
	// Only an EXPLICIT out-of-range request is a client error.
	return fmt.Errorf("%w: timecode %.3fs exceeds source duration %.3fs",
		ErrAVTimecodeOutOfRange, timecode, dur.Seconds())
}
```

See WR-04 for the separate sentinel-class problem in the error returned here.

## Warnings

### WR-01: output path argv element is not `file:`-prefixed

**File:** `internal/convert/av.go:99,116,142,166,343`
**Issue:** Every builder hardens the *input* (`-i "file:"+inPath`) but appends the
*output* as a bare `outPath`. `-protocol_whitelist` constrains input protocols only;
ffmpeg resolves the output URL through the same protocol dispatcher, so an outPath
that ever began with a protocol specifier (`tcp:`, `file,crypto` aside) or a leading
`-` would be reinterpreted as a protocol or an option rather than a filename. The
hardening is asymmetric with the file's own stated invariant ("file arguments must be
`file:`-prefixed").

Not currently exploitable: `internal/worker/worker.go:889-891` builds outPath as
`filepath.Join(workDir, "out."+job.TargetFormat)` from a registry-validated format, so
no client bytes reach it today. This is exactly the "future caller threads a
client-influenced filename through here" scenario that `audioduration.go:66-74`
documents as the reason the prefix exists at all.

**Fix:** `"file:" + outPath` in all five sites, matching the input-side discipline.

### WR-02: `SniffVideo` has zero production callers; video detection shipped half-wired

**File:** `internal/convert/avsniff.go:190`, `internal/convert/sniff.go:44-46`
**Issue:** `matchMP4`/`matchMOV`/`matchAVI` were added to the `signatures` table, which
means the API upload path (`internal/api/handlers.go:280` chain, via `Sniff`) detects
mp4/mov/avi **today**. `SniffVideo` — the only path that can ever detect mkv/webm — is
called from nothing outside its own test file. Repo-wide grep confirms: `handlers.go`
chains `Sniff` → `SniffAudio` and never `SniffVideo`.

Net effect: an mkv or webm upload is rejected as `unrecognized_content` (fail-closed,
so not a security issue), while mp4/mov/avi are accepted and then rejected one step
later at pair validation. Half of the phase's detection surface is live and half is
dead export. When Phase 35 registers `AVConverter`, mkv/webm will be advertised as
supported sources by `GET /v1/formats` while remaining undetectable at upload.

**Fix:** Either wire `SniffVideo` into the `handlers.go` chain now (chained off `rest`,
with the same "chain off rest, not file" caution the `SniffAudio` call site documents),
or hold the `signatures` additions back until Phase 35 so the two halves land together.

### WR-03: `int(size)` / `int(headerSize)` truncate on 32-bit; fail-closed is accidental

**File:** `internal/convert/avsniff.go:150,165,169,178`
**Issue:** `readSizeVint` returns a `uint64` whose maximum is `2^56-1` (8-byte vint
with the marker bits masked). Converting that to `int` is safe on 64-bit, where
`pos + int(size)` cannot overflow and the `> len(buf)` check correctly fails closed —
but on a 32-bit build `int` is 32 bits and the conversion is implementation-defined
truncation:

- `size == 0x100000000` truncates to `0`, so `pos+0 > len(buf)` is false and the
  bounds check passes with a wrong (empty) slice.
- `size == 0x80000000` truncates to a **negative** int, `pos + negative > len(buf)` is
  false, and `buf[pos : pos+int(size)]` at line 169 panics with a slice-bounds error
  on attacker-controlled input.

The codebase already has precedent for treating implementation-defined numeric
conversion as a real hazard — `audioduration.go:21-31` documents exactly this class of
platform-dependent bug ("invisible on dev machines and live in production"). The same
reasoning applies here.

**Fix:** Compare in `uint64` space before any narrowing:

```go
if size > uint64(len(buf)) || uint64(pos)+size > uint64(len(buf)) {
	return "", false // declared element size runs past bounded window
}
```

Apply the same to `headerSize` at line 150. A regression test with an 8-byte size vint
declaring `>= 2^31` would pin this.

### WR-04: `ErrAVOutputMissingOrEmpty` conflates a client input error with an engine failure

**File:** `internal/convert/av.go:391`
**Issue:** The sentinel is documented as classifying ffmpeg's "exit 0 but produced
nothing usable" failure — an *engine* fault. `convertThumbnail` reuses it for
"timecode exceeds source duration", which is a *client input* fault detected
pre-flight, before ffmpeg is invoked at all. The doc comment acknowledges the reuse but
the justification ("so a caller can errors.Is-match 'no usable output'") works against
the caller: the API layer cannot distinguish 422-worthy bad input from 500-worthy
engine failure, and the worker cannot distinguish retryable from terminal.

This matters concretely given the project's `asynq.SkipRetry` discipline
(CLAUDE.md, "Error Handling"): an out-of-range timecode is deterministically
non-retryable, an ffmpeg no-output fault may not be.

**Fix:** Introduce a distinct sentinel and keep `ErrAVOutputMissingOrEmpty` for
post-hoc output validation only:

```go
// ErrAVTimecodeOutOfRange classifies a CLIENT-supplied thumbnail timecode
// past the source's declared duration -- a 4xx-class input fault detected
// pre-flight, deliberately NOT folded into ErrAVOutputMissingOrEmpty's
// engine-fault class so the API/worker can route them differently.
var ErrAVTimecodeOutOfRange = errors.New("av: timecode exceeds source duration")
```

### WR-05: up to five ffprobe subprocesses per job, each inheriting the full engine timeout

**File:** `internal/convert/av.go:302-306,324-331,386`
**Issue:** A single thumbnail conversion spawns `ProbeDuration` (via
`EnforceMaxDuration`), `probeVideoStream` (via `EnforceMaxResolution`), then
`ProbeDuration` **again** at line 386 — the same probe, on the same file, twice. A
transcode spawns `ProbeDuration` + `probeVideoStream` in the guard stage, then
`probeVideoStream` **again** at line 324 plus `probeAudioCodec`. Beyond the redundancy,
two correctness concerns:

1. Every probe receives the caller's `ctx` unchanged. `ProbeDuration`'s own doc comment
   (`audioduration.go:52-55`) explicitly states "ctx should carry a SHORT bound
   distinct from the full engine timeout ... it must never be allowed to run for the
   full AUDIO_ENGINE_TIMEOUT budget". `Convert` violates that contract for all five
   invocations — a hung ffprobe on a malformed container burns the entire
   `ENGINE_TIMEOUT` budget.
2. Re-probing the same file twice invites divergent results if the file is mutated
   between probes, and makes the guard's decision and the conversion's decision
   independently derived rather than provably consistent.

**Fix:** Probe once into a small struct at the top of `Convert` and thread the result
through, and derive a short child context for the probe stage:

```go
probeCtx, cancel := context.WithTimeout(ctx, avProbeTimeout) // e.g. 15s
defer cancel()
dur, err := ProbeDuration(probeCtx, inPath)
...
codec, _, height, err := probeVideoStream(probeCtx, inPath)
```

### WR-06: `avThreadCount()` ignores the in-repo cgroup CPU limit

**File:** `internal/convert/av.go:239-241`
**Issue:** `runtime.NumCPU()` reports the **host's** CPU count inside a container; it
does not honor a cgroup CPU quota. `docker-compose.yml` limits the worker to
`cpus: "2.0"`, so `-threads` will be set to (for example) 16 on a 16-core host while
the container may use 2 — CPU oversubscription and thrashing under concurrent jobs.

The project already solved this: `convert.CgroupCPULimit()` exists in
`internal/convert/cgroup.go:59` and is used by `cmd/audio-worker/main.go:174`. The doc
comment here acknowledges the mechanism is "reusable verbatim" and defers it as
"premature for a converter that is not yet registered/queued" — but the deferral costs
one function call, and the constant will ship as-is into Phase 35 unless flagged.

**Fix:**

```go
func avThreadCount() int {
	if n, ok := CgroupCPULimit(); ok {
		return n
	}
	return runtime.NumCPU()
}
```

### WR-07: `probeVideoStream` discards the underlying JSON error

**File:** `internal/convert/avduration.go:53-55`
**Issue:** `if err := json.Unmarshal(out, &probe); err != nil || len(probe.Streams) == 0`
collapses two distinct failures into one opaque message and drops `err` on the floor —
a malformed-JSON fault and a valid-JSON-but-no-video-stream fault become
indistinguishable in logs. This violates the project's stated convention: "Wrap errors
with `fmt.Errorf("<action>: %w", err)` to preserve context and chain" (CLAUDE.md, Error
Handling), followed consistently elsewhere in this package.

**Fix:**

```go
if err := json.Unmarshal(out, &probe); err != nil {
	return "", 0, 0, fmt.Errorf("ffprobe: unparseable stream probe output: %w", err)
}
if len(probe.Streams) == 0 {
	return "", 0, 0, fmt.Errorf("ffprobe: no video stream found")
}
```

### WR-08: the AVE-02 "every invocation, no exception" invariant is false repo-wide

**File:** `internal/convert/av.go:81-84`, `internal/convert/audioduration.go:59-65`,
`internal/convert/whisper.go:172`
**Issue:** Both files assert the hardening invariant holds universally —
audioduration.go states it "holds with no exception, closing T-34-08b". It does not:
`whisper.go:172`'s `ffmpegNormalizeArgs` returns
`[]string{"-y", "-i", "file:" + inPath, "-ar", "16000", ...}` with **neither**
`-protocol_whitelist file,crypto` **nor** `-nostdin`, and it runs ffmpeg on untrusted
client audio uploads. That invocation is outside this phase's file set but inside the
invariant's stated scope, so the comment is actively misleading to a future reader
auditing the claim.

**Fix:** Add the two flags to `ffmpegNormalizeArgs` (a one-line change that makes the
comment true), or narrow the comments to the AV engine's own invocations. Do not leave
a false universal claim in a security-relevant doc comment.

### WR-09: argv builders have no `default` case; an unknown target yields codec-less argv

**File:** `internal/convert/av.go:133-141,158-165`
**Issue:** The `switch target` in `extractAudioArgs` and `thumbnailArgs` has no
`default`. An unrecognized target falls through and returns an argv with **no** `-c:a`
/ `-c:v` flag at all, silently handing stream-selection back to ffmpeg's
extension-based auto-selection — precisely the behavior `thumbnailArgs`'s own doc
comment says must never be relied on ("never relying on ffmpeg's extension-based
auto-selection, which is known to fail for at least one target on at least one real
ffmpeg build, Pitfall 3").

Currently unreachable because `Convert` dispatches on a closed target set first, but
these are exported-in-spirit pure builders with their own unit tests, and the guard
lives in a different function than the assumption.

**Fix:** Make the builders fail closed rather than depending on a caller-side
invariant — return `([]string, error)`, or at minimum panic-free explicit handling:

```go
default:
	return nil // caller MUST treat a nil argv as a programming error
```

### WR-10: the protocol-whitelist canary tests ffmpeg, not the production code

**File:** `internal/convert/av_test.go:547-566`
**Issue:** `TestProtocolWhitelist_BlocksHTTP_Canary` hand-builds its argv inline
(`runCommand(ctx, "ffmpeg", "-y", "-nostdin", "-protocol_whitelist", "file,crypto",
"-i", "file:"+evilPath, "-c", "copy", outPath)`) rather than calling any of the five
production builders. It therefore proves that *ffmpeg's* `-protocol_whitelist` flag
works — which was never in doubt — and would continue passing if someone deleted the
flag from `transcodeToMP4Args` tomorrow. The test named as AVE-02's required security
canary provides no regression protection for the code it is meant to guard.

**Fix:** Drive the canary through the real entry point:

```go
err := (AVConverter{}).Convert(ctx, evilPath, outPath, nil)
```

or, if the guard stage rejects the m3u8 before ffmpeg runs, at minimum feed
`transcodeToMP4Args(evilPath, outPath, "h264", 2)` into `runCommand` so the assertion
is anchored to the builder under test. Complement with a table test asserting every
builder's output contains the `-protocol_whitelist file,crypto` pair.

### WR-11: `signatures` doc comment is stale after the video additions

**File:** `internal/convert/sniff.go:35-37`
**Issue:** The comment still reads "the hardcoded, closed detection table (D-03) scoped
to exactly the formats registered in convert.Default (imageFormats in libvips.go): png,
jpg, webp, heic, tiff" — but the table now carries `mp4`, `mov`, and `avi`, none of
which are registered in `convert.Default` (AVConverter is deliberately unregistered).
The comment's stated scoping rule is now false in both directions, which matters because
that rule is the documented reason the table is closed.

**Fix:** Update to describe the actual scope, and state explicitly that mp4/mov/avi are
detected ahead of engine registration (with a pointer to Phase 35) so the temporary
inconsistency is deliberate and legible.

## Info

### IN-01: latent m4a/mp4 detection collision once AVConverter registers

**File:** `internal/convert/avsniff.go:15-21`, `internal/convert/audiosniff.go:23-27`
**Issue:** `TestVideoBrandDisjointness` correctly proves `mp4VideoBrands`,
`m4aBrands`, and `heicBrands` are disjoint *as tables*. Real-world files are less
tidy: some encoders write `.m4a` audio with a generic major brand (`isom`, `mp42`)
rather than `M4A `. Because `Sniff` runs before `SniffAudio` in the handlers chain,
such a file now resolves to `mp4` and never reaches `matchM4A`.

Harmless today (both paths end in a 422, only the message differs), but once
`AVConverter` registers, an audio-only file will route to the AV engine and its
`EnforceMaxResolution` guard will fail on a container with no video stream. Worth a
note in the Phase 35 plan.

**Fix:** Consider a post-`Sniff` disambiguation for ftyp containers — e.g. if `Sniff`
returned `mp4` but `probeVideoStream` finds no video stream, reclassify as `m4a`
rather than failing.

### IN-02: `probeVideoStream` returns a `width` no caller uses

**File:** `internal/convert/avduration.go:47`
**Issue:** `width` is returned by the named-result signature but every call site
discards it (`_, _, height, err` in `EnforceMaxResolution`; `codec, _, _, err` in
`convertTranscode`). It is only ever used for the `<= 0` plausibility check inside the
function itself.

**Fix:** Harmless as-is — a width-based guard is a plausible near-term addition. If it
stays unused past Phase 35, drop it from the signature.

---

_Reviewed: 2026-07-20_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
