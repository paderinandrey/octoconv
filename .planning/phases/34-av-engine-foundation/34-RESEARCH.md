# Phase 34: AV Engine Foundation - Research

**Researched:** 2026-07-19
**Domain:** Standalone ffmpeg-based video Converter (transcode/audio-extract/thumbnail), video container magic-bytes sniffing (ISOBMFF + RIFF + EBML), closed-allowlist AVOpts, and fail-closed content/resource guards — fifth engine class, not yet wired to queue/worker/registry
**Confidence:** HIGH — every argv shape, container byte layout, and guard behavior below was live-verified in this session against a real `ffmpeg 8.1.2`/`ffprobe 8.1.2` binary (Homebrew, arm64/macOS) shelling real commands against synthetic `lavfi`-generated video fixtures; codebase precedents (`whisper.go`, `audiosniff.go`, `sniff.go`, `audioduration.go`, `audioopts.go`, `exec.go`, `dimensions.go`, `cgroup.go`) were read directly. This phase note is scoped to Phase 34 only (AVC-01..05, AVO-01..03, AVE-01, AVE-02) — see `.planning/research/{SUMMARY,STACK,PITFALLS,ARCHITECTURE}.md` for full-milestone context, which this note does not repeat except where a phase-specific decision depends on it.

## Summary

Phase 34 builds a standalone `AVConverter` (transcode mov/avi/mkv/webm→mp4 H.264/AAC+faststart and mp4→webm VP9/Opus; audio-extract video→mp3/wav/m4a with AAC→m4a stream-copy; thumbnail-extract video→jpg/png/webp via fast input-side `-ss`), a new video magic-bytes sniffer set (fixed-offset ISOBMFF/RIFF matchers plus a genuinely new bounded-peek EBML/DocType parser for mkv-vs-webm), a closed `AVOpts` allowlist (thumbnail timecode, resolution-height enum, HEVC codec choice), and fail-closed duration+resolution guards — all built and unit-tested directly against a real `ffmpeg`/`ffprobe` binary, unregistered in `convert.Default`, mirroring Phase 30's audio-engine scope fence exactly. Every new file mirrors an existing sibling 1:1 (`av.go`↔`whisper.go`'s two-stage-pipeline shape minus the second binary; `avopts.go`↔`audioopts.go`; `avsniff.go`↔`audiosniff.go`; `avduration.go`↔`audioduration.go`), so the mechanical pattern-reuse is low-risk and well-precedented — no new architectural layer, no new Go dependency, no new exec-hardening mechanism.

The single highest-uncertainty item is the EBML/DocType bounded-peek parser (mkv vs webm): this session live-verified the exact byte layout of both containers' EBML headers (magic `1A 45 DF A3`, then a chain of `EBMLVersion`/`EBMLReadVersion`/`EBMLMaxIDLength`/`EBMLMaxSizeLength`/`DocType`/`DocTypeVersion`/`DocTypeReadVersion` elements, each using RFC 8794's variable-length-integer ID+size encoding) against real ffmpeg-produced fixtures, giving a concrete, tested vint-parsing algorithm below rather than a spec-only description. A second load-bearing, live-verified finding: ffmpeg's mp4 muxer will silently accept `-c copy` remuxing of VP9/Opus (webm's codecs) into an `.mp4` container without any error — meaning AVC-05's "auto stream-copy fast path" MUST gate remux eligibility on this project's own closed per-target-container codec allowlist (h264/aac for mp4, vp9/opus for webm), never on "did ffmpeg's muxer accept it," or the fast path can silently violate AVC-01/AVC-02's own codec contract. A third live-verified finding, materially sharper than PITFALLS.md's Pitfall 1: `-protocol_whitelist file,crypto` correctly blocks a crafted HLS-embedded `http://` segment reference (confirmed via a live canary against a real internal-IP-shaped URL), but does **not** and cannot block the concat demuxer's own local-file-read mechanism (the `file` protocol is legitimately whitelisted) — the concat demuxer's own `safe=1` default (confirmed live: blocks `..`-relative and absolute paths, but does NOT block a same-level-or-deeper relative sibling path) is the only defense against that distinct vector, and this project does not use the concat demuxer at all in Phase 34's three features, so the residual risk is scoped to future filtergraph-source-filter or subtitle-reference vectors, not concat directly.

**Primary recommendation:** Build `AVConverter` as three argv-builder functions dispatched by target format (mirrors `whisperOutputFlag`'s target-driven dispatch), each function unit-testable in isolation from a real subprocess (mirrors `ffmpegNormalizeArgs`); pass `-protocol_whitelist file,crypto -nostdin` on every single ffmpeg/ffprobe invocation from the first commit; gate the stream-copy fast path on an explicit per-container codec allowlist, never on ffmpeg's own muxer acceptance; build the EBML DocType parser as a small, fully bounded (4 KiB peek), fail-closed vint walker per the byte-exact algorithm verified below.

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-------------------|
| AVC-01 | Transcode mov/avi/mkv/webm → mp4 (H.264/AAC, `-movflags +faststart`) | Pattern 1 (`transcodeToMP4Args`, live-verified argv); Pattern 2 (stream-copy fast-path allowlist, so a legitimate remux never silently violates the H.264/AAC contract) |
| AVC-02 | Transcode mp4 → webm (VP9/Opus), always full re-encode | Pattern 1 (`transcodeToWebMArgs`, live-verified argv) |
| AVC-03 | Extract audio track → mp3/wav/m4a; AAC source + m4a target uses stream-copy | Code Examples "Audio extraction" (live-verified argv incl. `-c:a copy` case) |
| AVC-04 | Extract thumbnail → jpg/png/webp; fast input-side `-ss`, 1.0s default clamped to duration | Code Examples "Thumbnail extraction" (live-verified input-side `-ss`); Pitfall 2 (out-of-range `-ss` exits 0 with no output file — pre-flight bounds-check + post-validation required); Pitfall 3 (explicit `-c:v` per target, webp needs `libwebp`) |
| AVC-05 | Auto stream-copy fast path: ffprobe codec check → remux instead of re-encode when already legal | Pattern 2 (`avStreamCopyLegal`, live-verified this is NOT safe to derive from "did ffmpeg's muxer accept `-c copy`" — VP9/Opus into mp4 succeeds silently) |
| AVO-01 | Thumbnail timecode via typed closed AVOpts allowlist (checkStrictObject) | Don't Hand-Roll (`checkStrictObject` reuse); mirrors `AudioOpts` pattern (`audioopts.go` read directly) |
| AVO-02 | Closed resolution-height enum (480/720/1080), no arbitrary WxH | Architectural pattern reuse of `AudioOpts`/`DocOpts` closed-enum discipline; no raw string ever reaches argv (mirrors `PDFAFilterOptions`'s enum-to-server-constant mapping) |
| AVO-03 | HEVC codec choice via the same closed allowlist, own CRF default (not copied from x264) | Pitfall 4 (live-verified x265 CRF 28 functional, must not reuse x264's CRF 23 constant) |
| AVE-01 | Fail-closed magic-bytes validation: ISO-BMFF ftyp (mp4/mov), EBML DocType (mkv/webm), RIFF AVI — with collision tests | Pattern 3 (byte-exact, live-verified EBML/DocType bounded-peek algorithm); Pattern 4 (fixed-offset mp4/mov/avi matchers, live-verified byte layout); Anti-Pattern 1 (why mkv/webm cannot use the fixed-offset shape); disjointness requirement against `m4aBrands`/`heicBrands` |
| AVE-02 | Duration + resolution guards; `-protocol_whitelist file,crypto` on every ffmpeg/ffprobe call | Don't Hand-Roll (`ProbeDuration`/`EnforceMaxDuration` reuse verbatim); Code Examples "NEW resolution + codec probe"; Pitfall 1 + Code Examples "Protocol-whitelist offline canary" (live-verified SSRF block, and the documented concat/local-read scope boundary) |
</phase_requirements>

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Video transcode (mov/avi/mkv/webm→mp4, mp4→webm) | API/Backend (`internal/convert`, worker-invoked) | — | CPU-bound subprocess shell-out, same tier as every existing `Converter`; no browser/CDN/DB involvement |
| Audio-track extraction (video→mp3/wav/m4a) | API/Backend | — | Same `ffmpeg` subprocess tier as transcode; cheaper operation, same architectural layer |
| Thumbnail/frame extraction (video→jpg/png/webp) | API/Backend | — | Same tier; near-instant subprocess call |
| Video container magic-bytes sniffing (mp4/mov/avi/mkv/webm) | API/Backend (`internal/convert`, called from `internal/api` before storage write) | — | Runs at job-creation time in the API process, before any queue/worker involvement — same tier as `Sniff`/`SniffAudio` today |
| `AVOpts` closed-allowlist validation | API/Backend | — | Parsed both at API write-time (`ParseAVOpts`) and worker read-time (`AVOptsFromMap`) — same dual-path pattern as `AudioOpts`/`DocOpts`, both API/Backend tier, never client/browser |
| Duration + resolution guard (decompression-bomb defense) | API/Backend (`internal/convert`, invoked from the worker's `process()` before the expensive stage) | — | `ffprobe`-based, subprocess tier, mirrors `enforceAudioGuardBeforeConvert`'s placement |
| Protocol whitelist / exec hardening | API/Backend (`internal/convert/exec.go` + argv construction) | — | Enforced entirely in the subprocess invocation layer; no tier above or below it participates |
| Persistence of job/opts/output metadata | Database/Storage | — | Out of Phase 34's scope (standalone, unregistered) — noted only because `AVOpts` will eventually round-trip through `jobs.options` (Phase 35), same as `AudioOpts`/`DocOpts` today |

## Project Constraints (from CLAUDE.md)

- **Go 1.26, `CGO_ENABLED=0`** — `AVConverter` MUST shell out via `os/exec` (the existing `runCommand` in `internal/convert/exec.go`), never a CGo ffmpeg binding.
- **No third-party assertion/mocking library** — all new `*_test.go` files use stdlib `testing` idioms only, mirroring `whisper_test.go`/`audiosniff_test.go`.
- **Package name matches directory; one file per responsibility** — `av.go`, `avopts.go`, `avsniff.go`, `avduration.go`, each with matching `_test.go`, all `package convert`.
- **Exported `New<Type>` constructors; unexported `camelCase` helpers; `ctx` always first, always named `ctx`** — followed by every function signature proposed below.
- **Errors wrapped with `fmt.Errorf("<action>: %w", err)`; package-level sentinel errors (`var Err...`)** — mirrors `ErrAudioDurationExceeded`; this phase needs `ErrAVDurationExceeded` and `ErrAVResolutionExceeded` (or a shared `ErrAVResourceExceeded`, planner's call).
- **`internal/*` never logs; only `cmd/*/main.go` does** — no `log.*` calls anywhere in the new `internal/convert/av*.go` files.
- **No `panic` for control flow** — every ffmpeg/ffprobe failure path returns a wrapped error, matching `runCommand`'s existing contract.
- **Comments: package-level doc comment on exactly one file; exported identifiers documented starting with their own name; non-obvious "why" gets an inline comment** — mirrors the density already present in `whisper.go`/`audioduration.go`/`dimensions.go`; the planner should hold new AV files to the same bar (these files will be read by every future engine-class implementer as the fifth precedent).
- **GSD workflow enforcement** — this research feeds `/gsd:plan-phase 34`; no direct repo edits are made by this research task.

## Standard Stack

### Core

No new Go dependencies. `ffmpeg`/`ffprobe` are OS-level CLI binaries already used by the audio engine (`internal/convert/whisper.go`, `internal/convert/audioduration.go`) — this phase extends their usage, it does not introduce them. Package/version pinning for the `av-worker` container is explicitly **Phase 36 scope** (AVE-04, per ROADMAP.md and STATE.md's "pin ffmpeg ≥8.1.2" Key Decision) — Phase 34 only needs `ffmpeg`/`ffprobe` on the local dev PATH for `exec.LookPath`-gated live tests, mirroring `requireLiveAudioBinaries`.

| Tool | Version (local, live-verified this session) | Purpose | Why Standard |
|------|------|---------|---------------|
| `ffmpeg` | 8.1.2 (Homebrew, arm64/macOS) `[VERIFIED: local binary, this session]` | transcode, audio-extract, thumbnail | Already the project's audio-engine dependency; same shell-out discipline via `runCommand` |
| `ffprobe` | 8.1.2 (same package) `[VERIFIED: local binary, this session]` | duration guard (reuse `ProbeDuration` verbatim), new resolution/codec probes, stream-copy-eligibility codec check | Same package as ffmpeg; `internal/convert/audioduration.go`'s pattern extends directly |

**Version note (flag for planner, not resolved here):** the locally-verified `ffmpeg 8.1.2` (Homebrew) is materially newer than the `7:5.1.9-0+deb12u1` Debian-bookworm package `.planning/research/STACK.md` verified live inside the actual deployment container. Argv/flag names used below (`-crf`, `-preset`, `-movflags +faststart`, `-protocol_whitelist`, `-frames:v`, `-c:a copy`) are stable across this version range and were also cross-checked against STACK.md's own `ffmpeg -h encoder=libx264` findings (`-preset` default `medium`, `-crf` default unset `-1`) — both sessions agree. One concrete divergence found: **this session's Homebrew ffmpeg 8.1.2 build has NO `libwebp` encoder at all** (`ffmpeg -encoders | grep -i webp` returns nothing), whereas STACK.md's live Debian-container verification confirmed `libwebp`/`libwebp_anim` present in the Debian package. This is a build-configuration difference between two ffmpeg distributions, not a version regression — Phase 34's own dev-machine tests for the webp thumbnail target should account for this (skip-gate or `t.Skip` if `libwebp` truly isn't available locally, mirroring `requireLiveAudioBinaries`'s skip philosophy), and the argv MUST always pass `-c:v libwebp` explicitly (never rely on ffmpeg's default-encoder-by-extension selection, which is exactly what failed in this session's local test with a bare `-frames:v 1 thumb.webp` invocation).

**Installation verification (no new packages to install for this phase):**
```bash
# Local dev — already required by the audio engine, no new step:
brew install ffmpeg   # macOS dev, or apt-get install ffmpeg on Linux dev hosts
command -v ffmpeg ffprobe
```

## Package Legitimacy Audit

**Not applicable to this phase.** No new Go modules, npm/pip packages, or other package-manager-installed dependencies are introduced — `ffmpeg`/`ffprobe` are OS-level CLI binaries invoked via `os/exec` (already the case for the audio engine). The `slopcheck`/registry-verification gate in this protocol applies to package-manager dependencies (`go.mod`, `package.json`, `requirements.txt`); Phase 34 adds none. The Debian `apt-get install ffmpeg` version-pin decision (with its own CVE-backport verification against the Debian security tracker) is explicitly deferred to **Phase 36** (`Dockerfile.av-worker`, AVE-04) per ROADMAP.md and STATE.md's already-recorded Key Decision — do not re-litigate it in Phase 34's plan.

## Architecture Patterns

### System Architecture Diagram

```
                         (Phase 34 scope: everything below is
                          standalone/unit-tested; NOT registered
                          into convert.Default, NOT wired to any
                          queue/worker/API route yet)

  client bytes (test-only, or a future job's inPath)
        │
        ▼
┌───────────────────────────┐        ┌──────────────────────────────┐
│ Sniff() / SniffVideo()     │        │  ParseAVOpts(raw json)         │
│ (avsniff.go, sniff.go)     │        │  (avopts.go)                   │
│ mp4/mov/avi: fixed 12-byte │        │  checkStrictObject +           │
│ ftyp/RIFF match            │        │  DisallowUnknownFields +       │
│ mkv/webm: bounded EBML     │        │  closed enum/range checks      │
│ DocType walk (4KiB peek)   │        │  (timecode, height, codec)     │
└─────────────┬──────────────┘        └───────────────┬────────────────┘
              │ detected format                        │ validated AVOpts
              ▼                                         ▼
┌─────────────────────────────────────────────────────────────────────┐
│ AVConverter.Convert(ctx, inPath, outPath, opts)  (av.go)              │
│                                                                         │
│  1. dispatch on NormalizeFormat(ext(outPath)):                        │
│       mp4/webm       -> transcode path                                │
│       mp3/wav/m4a    -> audio-extract path                            │
│       jpg/png/webp   -> thumbnail path                                │
│                                                                         │
│  2. guard stage (avduration.go, BEFORE any ffmpeg decode/encode):      │
│       ProbeDuration (reused verbatim) -> EnforceMaxDuration            │
│       new: probeResolution -> EnforceMaxResolution                    │
│                                                                         │
│  3. (transcode only) stream-copy fast path:                            │
│       ffprobe codec_name -> if already legal in target container's    │
│       OWN closed allowlist -> "-c copy", else full re-encode           │
│                                                                         │
│  4. runCommand(ctx, "ffmpeg"/"ffprobe", argv...)                      │
│       every invocation: -protocol_whitelist file,crypto -nostdin       │
│                                                                         │
│  5. post-validate output: size>0 (mirrors validateAudioOutput);         │
│       thumbnail additionally re-Sniff()s the output image bytes         │
└─────────────────────────────────────────────────────────────────────┘
              │
              ▼
        outPath on local disk (worker's job workDir — S3 upload,
        DB write, queue wiring are ALL Phase 35 scope, not built here)
```

### Recommended Project Structure

```
internal/convert/
├── av.go              # AVConverter: Pairs()/Convert()/Engine()==EngineAV; 3 argv builders (transcodeArgs, extractArgs, thumbnailArgs)
├── av_test.go
├── avopts.go           # AVOpts (Timecode, ResolutionHeight, Codec); ParseAVOpts/AVOptsFromMap/ValidateAVApplicability — mirrors audioopts.go
├── avopts_test.go
├── avduration.go       # probeResolution + EnforceMaxResolution (NEW); re-exports/wraps ProbeDuration+EnforceMaxDuration (REUSED, no new duration logic)
├── avduration_test.go
├── avsniff.go          # matchMP4/matchMOV/matchAVI (added to sniff.go's table) + matchEBML/SniffVideo (new bounded-peek EBML/DocType parser)
├── avsniff_test.go
├── sniff.go            # MODIFIED: signatures table += {"mp4", matchMP4}, {"mov", matchMOV}, {"avi", matchAVI}; MIMEType += video/* entries
├── convert.go           # MODIFIED: += EngineAV = "av" constant (registration itself, Register(AVConverter{}), stays OUT per the scope fence — see Open Questions)
└── (converters.go NOT touched this phase — registration is Phase 35 scope, mirrors Phase 30's own fence)
```

### Pattern 1: Target-driven argv dispatch (mirrors `whisperOutputFlag`)

**What:** `Convert()` inspects `NormalizeFormat(filepath.Ext(outPath))` once and dispatches to exactly one of three isolated, independently-unit-testable argv-builder functions — never a single monolithic argv-construction function with branching flags scattered through it.

**When to use:** Every AV feature (transcode/extract/thumbnail) — this is the same shape `whisperOutputFlag` uses to select whisper-cli's output flag from the target format, applied one layer up (selecting the whole ffmpeg invocation shape, not just one flag).

**Example (live-verified argv, this session):**
```go
// Source: this session's live ffmpeg 8.1.2 invocations, exit 0, output verified
// non-empty and correctly-coded via ffprobe re-check (see "Code Examples" below).

// Transcode: mov/avi/mkv/webm -> mp4 (H.264/AAC, +faststart)
func transcodeToMP4Args(inPath, outPath string, threads int) []string {
	return []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:" + inPath,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		"-threads", strconv.Itoa(threads),
		outPath,
	}
}

// Transcode: mp4 -> webm (VP9/Opus), always full re-encode per AVC-02
func transcodeToWebMArgs(inPath, outPath string, threads int) []string {
	return []string{
		"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:" + inPath,
		"-c:v", "libvpx-vp9", "-b:v", "1M",
		"-c:a", "libopus",
		"-threads", strconv.Itoa(threads),
		outPath,
	}
}
```

### Pattern 2: Stream-copy fast path gated on a PROJECT-owned codec allowlist, never on ffmpeg's own muxer permissiveness

**What:** Before attempting `-c:v copy -c:a copy` (or the AVC-03 audio-only `-c:a copy`), probe the source codec via `ffprobe -select_streams v:0 -show_entries stream=codec_name`, and only remux when the source codec is in **this project's own closed allowlist for the target container** — never merely because ffmpeg's muxer happens to accept the write.

**Why this is load-bearing, not defensive-programming theater:** live-verified this session — `ffmpeg -i file:src.webm -c copy -movflags +faststart out.mp4` (remuxing VP9 video + Opus audio directly into an `.mp4` container) **exits 0 and produces a valid, ffprobe-readable file** with `codec_name: vp9`/`codec_name: opus` inside an `.mp4` container. Nothing about ffmpeg's mp4 muxer rejects this. If the stream-copy fast path (AVC-05) were implemented as "try `-c copy`, fall back to re-encode only if ffmpeg errors," a webm-source job targeting mp4 would silently remux VP9/Opus into an `.mp4` file — violating AVC-01's explicit contract ("mp4 target = H.264 video + AAC audio") with zero error signal, and very likely producing a file many client-side players cannot decode despite the container extension promising H.264/AAC.

**How to avoid:** define the allowlist explicitly and check it BEFORE attempting copy:
```go
// avStreamCopyLegal reports whether srcVideoCodec/srcAudioCodec are already
// legal in targetContainer per THIS PROJECT's own AVC-01/AVC-02 contract —
// never derived from what ffmpeg's muxer happens to accept (live-verified
// this session: ffmpeg's mp4 muxer accepts a VP9/Opus `-c copy` remux
// without error, which would silently violate the mp4-target=H.264/AAC
// contract if this check were skipped).
func avStreamCopyLegal(targetContainer, srcVideoCodec, srcAudioCodec string) bool {
	switch targetContainer {
	case "mp4":
		return srcVideoCodec == "h264" && srcAudioCodec == "aac"
	case "webm":
		return srcVideoCodec == "vp9" && srcAudioCodec == "opus"
	default:
		return false
	}
}
```

**Live-verified example — legitimate fast path (mkv h264/aac source -> mp4 target):**
```bash
# Source: this session, ffmpeg 8.1.2, exit 0, ffprobe-confirmed output codec_name=h264
ffprobe -v error -select_streams v:0 -show_entries stream=codec_name -of csv=p=0 file:src.mkv
# -> h264
ffmpeg -y -nostdin -protocol_whitelist file,crypto -i file:src.mkv -c copy -movflags +faststart out.mp4
# exit 0; ffprobe on out.mp4 confirms codec_name=h264 unchanged (true remux, no re-encode)
```

### Pattern 3: EBML/DocType bounded-peek parser (mkv vs webm) — byte-exact algorithm, live-verified

**What:** Both `.mkv` and `.webm` begin with the identical 4-byte EBML magic `1A 45 DF A3`. The ONLY reliable disambiguator is the `DocType` element (ID `0x4282`) inside the EBML header master element, which sits at a **variable offset** (preceded by a variable number of variable-length preceding elements) — no fixed-offset matcher (the shape every existing sniffer in this codebase uses) can distinguish them.

**Live-verified byte layout (this session, real ffmpeg-produced fixtures, hex-dumped directly):**

```
mkv (src.mkv), first 52 bytes:
1a 45 df a3            EBML magic (4 bytes)
a3                      master-element SIZE vint: 1-byte, marker=0x80, value = 0xA3 & 0x7F = 0x23 = 35 (header body is 35 bytes)
42 86 81 01             ID=0x4286 (EBMLVersion, 2-byte ID) SIZE=1(0x81&0x7F) VALUE=01
42 f7 81 01             ID=0x42F7 (EBMLReadVersion)        SIZE=1           VALUE=01
42 f2 81 04             ID=0x42F2 (EBMLMaxIDLength)        SIZE=1           VALUE=04
42 f3 81 08             ID=0x42F3 (EBMLMaxSizeLength)      SIZE=1           VALUE=08
42 82 88 6d6174726f736b61   ID=0x4282 (DocType) SIZE=8(0x88&0x7F) VALUE="matroska" (8 ASCII bytes)
42 87 81 04             ID=0x4287 (DocTypeVersion)         SIZE=1           VALUE=04
42 85 81 02             ID=0x4285 (DocTypeReadVersion)     SIZE=1           VALUE=02
18 53 80 67 ...          ID=0x18538067 (Segment, 4-byte ID) -- header ends here

webm (src.webm), first 48 bytes: IDENTICAL structure except:
9f                       master-element SIZE = 0x1F = 31 (4 bytes shorter, matches "webm" vs "matroska")
42 82 84 7765626d       ID=0x4282 (DocType) SIZE=4(0x84&0x7F) VALUE="webm" (4 ASCII bytes)
```

**Vint decoding rule (RFC 8794 §4, confirmed against both fixtures above):** for both ID and SIZE vints, the number of bytes is determined by the position of the leading set bit in the first byte (`0x80`-`0xFF`→1 byte, `0x40`-`0x7F`→2 bytes, `0x20`-`0x3F`→3 bytes, `0x10`-`0x1F`→4 bytes, ...). **SIZE vints** mask off the leading length-marker bit(s) to get the numeric value (`0xA3 & 0x7F = 0x23`). **ID vints** keep the marker bits as part of the ID's own value (`0x42 0x86` together IS the 2-byte ID `0x4286` — do not mask when matching against the well-known constant `0x4282` for DocType).

```go
// Source: this session's live hex-dump verification against real
// ffmpeg-produced src.mkv/src.webm fixtures (see algorithm table above).

const avPeekLen = 4 * 1024 // EBML header elements observed here total <60 bytes; 4KiB gives >60x headroom, mirrors dimPeekLen's generous-bound-but-still-bounded discipline

var ebmlMagic = []byte{0x1A, 0x45, 0xDF, 0xA3}

const ebmlDocTypeID = 0x4282

// vintLen returns the byte-length of a vint given its first byte, by the
// position of the leading set bit (RFC 8794 §4). 0 means invalid (first
// byte is 0x00, which cannot start ANY valid EBML vint).
func vintLen(first byte) int {
	for i := 0; i < 8; i++ {
		if first&(0x80>>i) != 0 {
			return i + 1
		}
	}
	return 0
}

// readSizeVint decodes a SIZE vint at buf[pos:], masking the length-marker
// bit(s) out of the returned value. Fails closed (ok=false) if the vint's
// declared length runs past the bounded buffer -- mirrors matchMP3's
// "never grow the buffer or seek further" discipline (audiosniff.go).
func readSizeVint(buf []byte, pos int) (value uint64, length int, ok bool) {
	if pos >= len(buf) {
		return 0, 0, false
	}
	n := vintLen(buf[pos])
	if n == 0 || pos+n > len(buf) {
		return 0, 0, false
	}
	value = uint64(buf[pos]) &^ (0xFF << uint(8-n)) // mask off the marker bit(s) in byte 0
	for i := 1; i < n; i++ {
		value = value<<8 | uint64(buf[pos+i])
	}
	return value, n, true
}

// readIDVint decodes an ELEMENT ID vint at buf[pos:] WITHOUT masking --
// the marker bits are part of the ID's own value per RFC 8794 §5 (verified
// live: 0x42 0x86 together equal the well-known EBMLVersion ID 0x4286).
func readIDVint(buf []byte, pos int) (id uint32, length int, ok bool) {
	if pos >= len(buf) {
		return 0, 0, false
	}
	n := vintLen(buf[pos])
	if n == 0 || n > 4 || pos+n > len(buf) { // EBML IDs are max 4 bytes (RFC 8794 §5)
		return 0, 0, false
	}
	var v uint32
	for i := 0; i < n; i++ {
		v = v<<8 | uint32(buf[pos+i])
	}
	return v, n, true
}

// matchEBML walks a bounded prefix looking for the EBML header's DocType
// element and returns "mkv" for "matroska", "webm" for "webm", or ("", false)
// for anything else -- including a truncated/malformed header, which fails
// CLOSED (never guessed, never defaulted to either format).
func matchEBML(buf []byte) (format string, ok bool) {
	if len(buf) < 5 || !bytes.Equal(buf[:4], ebmlMagic) {
		return "", false
	}
	headerSize, sizeLen, ok := readSizeVint(buf, 4)
	if !ok {
		return "", false
	}
	pos := 4 + sizeLen
	end := pos + int(headerSize)
	if end > len(buf) {
		end = len(buf) // bounded peek: never trust a declared size past what we actually have
	}
	for pos < end {
		id, idLen, ok := readIDVint(buf, pos)
		if !ok {
			return "", false
		}
		pos += idLen
		size, sizeLen, ok := readSizeVint(buf, pos)
		if !ok {
			return "", false
		}
		pos += sizeLen
		if pos+int(size) > len(buf) {
			return "", false // declared element size runs past bounded window: fail closed
		}
		if id == ebmlDocTypeID {
			switch string(buf[pos : pos+int(size)]) {
			case "matroska":
				return "mkv", true
			case "webm":
				return "webm", true
			default:
				return "", false // unrecognized DocType value: fail closed, do not guess
			}
		}
		pos += int(size)
	}
	return "", false // DocType not found within the bounded window: fail closed
}
```

**When to use:** This is the ONLY new sniffer requiring real element-walk parsing in this phase — `matchMP4`/`matchMOV`/`matchAVI` reuse `sniff.go`'s existing fixed-12-byte-window shape verbatim (see Anti-Pattern below for why mkv/webm cannot).

### Pattern 4: Video container fixed-offset matchers (mp4/mov/avi) — reuse `sniff.go`'s exact shape

**Live-verified byte layout (this session, real ffmpeg-produced fixtures):**
```
mp4:  00 00 00 20 66 74 79 70 69 73 6f 6d ...   ftyp @ offset 4-8, major brand "isom" @ offset 8-12
mov:  00 00 00 14 66 74 79 70 71 74 20 20 ...   ftyp @ offset 4-8, major brand "qt  " @ offset 8-12 (two trailing spaces)
avi:  52 49 46 46 ce 99 00 00 41 56 49 20 ...   "RIFF" @ 0-4, size @ 4-8, form-type "AVI " @ 8-12
```
This is byte-for-byte the same shape `matchHEIC`/`matchM4A` (ftyp+brand) and `matchWAV` (RIFF+fourCC) already use — `matchMP4`/`matchMOV`/`matchAVI` are direct copies of that shape with a different (larger, for mp4) brand allowlist.

```go
// Source: this session's live ffmpeg-produced fixture hex-dumps + ftyps.com
// brand registry [CITED: ftyps.com] for the fuller real-world brand set
// (only "isom" and "qt  " were directly reproduced live this session; the
// remaining entries are well-documented ISOBMFF majors, not independently
// live-verified in THIS session -- flag as [ASSUMED]/[CITED] in the
// Assumptions Log, planner should re-verify against a broader real-world
// encoder corpus if that matters for this milestone's actual client base).

// mp4VideoBrands is the closed major-brand allowlist for ordinary MP4
// *video* content -- MUST be disjoint from m4aBrands (audiosniff.go) and
// heicBrands (sniff.go), which share the identical ftyp+brand box shape.
// "qt  " is deliberately EXCLUDED here (it is QuickTime's own major brand,
// routed to matchMOV instead, not folded into the mp4 table).
var mp4VideoBrands = map[string]bool{
	"isom": true, // live-verified this session (ffmpeg lavfi->mp4 default)
	"mp41": true, "mp42": true, "mp4v": true, "avc1": true,
	"iso2": true, "iso3": true, "iso4": true, "iso5": true,
	"iso6": true, "iso7": true, "iso8": true, "iso9": true,
	"3gp4": true, "3gp5": true, "3g2a": true, "dash": true,
}

func matchMP4(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return mp4VideoBrands[string(b[8:12])]
}

func matchMOV(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return string(b[8:12]) == "qt  " // live-verified this session
}

func matchAVI(b []byte) bool {
	// SAME shape as matchWAV (audiosniff.go) -- must check the form-type
	// field, not just the RIFF prefix, or a WAV file would misdetect as AVI.
	return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("AVI "))
}
```

**Disjointness requirement (Phase 34 hard requirement, not optional polish):** a unit test MUST enumerate `mp4VideoBrands`, `m4aBrands` (audiosniff.go), and `heicBrands` (sniff.go) and assert pairwise-empty intersection — mirrors PITFALLS.md Pitfall 7's explicit call-out. Live-verified this session: `isom`/`mp41`/`mp42` (now in `mp4VideoBrands`) are ALREADY excluded from `m4aBrands` by the existing `TestMatchM4A_ForeignBrandNotDetected` test (`audiosniff_test.go:63`) — the disjointness already holds today, the new test makes it an explicit, permanent, cross-file-aware invariant instead of an implicit accident of two independently-written allowlists.

### Anti-Pattern 1: Adding `matchMKV`/`matchWebM` to `sniff.go`'s fixed 12-byte-window table

**What people do:** copy `matchHEIC`'s shape (`ftyp`+fixed-offset-brand) onto mkv/webm because "it's just another magic-bytes check."

**Why it's wrong:** live-verified this session — the EBML magic (`1A 45 DF A3`) is IDENTICAL for both formats; the disambiguating `DocType` element sits at a variable offset (28 bytes into `src.mkv`'s header, 28 bytes into `src.webm`'s header too in this session's fixtures, but this offset is NOT guaranteed constant — it depends on how many optional preceding elements a given encoder chooses to emit, and EBMLMaxIDLength/EBMLMaxSizeLength values could in principle themselves vary in encoded byte-width). A fixed-offset matcher would silently misdetect or reject valid files from encoders that emit a different preceding-element set than this session's ffmpeg build did.

**Do this instead:** the bounded-peek EBML walker (Pattern 3 above) — fail closed on anything outside the bound, never guess.

### Anti-Pattern 2: Trusting ffmpeg's muxer to reject an "illegal" stream-copy combination

**What people do:** implement the stream-copy fast path as "try `-c copy`; if ffmpeg exits non-zero, fall back to re-encode."

**Why it's wrong:** live-verified this session — ffmpeg's mp4 muxer happily accepts a VP9/Opus `-c copy` remux into `.mp4` with exit 0 and a fully valid, ffprobe-readable output. "Did ffmpeg accept it" and "is this legal per AVC-01/AVC-02's own codec contract" are two different questions with different answers for this exact combination.

**Do this instead:** Pattern 2's explicit `avStreamCopyLegal` allowlist, checked via `ffprobe` BEFORE any `-c copy` attempt.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Hardened subprocess exec with timeout+process-group-kill | A new exec wrapper for ffmpeg specifically | `runCommand` (`internal/convert/exec.go`) — reuse verbatim | Already handles `Setpgid`+SIGKILL-on-timeout, stdout/stderr capture, `D-09` exit-code-vs-output semantics; zero AV-specific behavior needed |
| Declared-duration parsing/validation (NaN/Inf/negative/overflow-safe float handling) | A second duration parser for video | `ProbeDuration`/`EnforceMaxDuration` (`internal/convert/audioduration.go`) — reuse verbatim, already format-agnostic (calls `ffprobe -show_entries format=duration`, works identically for video containers, live-verified this session against `src.mp4`) | Already handles the amd64/arm64 float-to-int64 overflow pitfall documented in that file's own comments; re-deriving it for video would silently drop that hardening |
| Strict-JSON opts parsing (reject duplicate keys/trailing bytes/top-level null/unknown fields) | A new strict-decode helper for `AVOpts` | `checkStrictObject` (`internal/convert/opts.go`) — reuse verbatim | Exactly the same strictness `DocOpts`/`AudioOpts`/`HTMLOpts` already require; `AVOpts` is a fourth caller, not a new mechanism |
| Cgroup-aware CPU thread sizing for a container-limited encoder | A new cgroup-quota reader for ffmpeg's `-threads` | `CgroupCPULimit()` (`internal/convert/cgroup.go`) — reuse verbatim, wire via the same `Set<X>Threads`-at-startup pattern as `SetAudioThreads` | Already floor-divides quota/period and fails open to `runtime.NumCPU()`; ffmpeg has the identical host-core-count-default footgun whisper-cli had (PITFALLS.md Pitfall 10) — the thread-count RESOLUTION mechanism is 100% reusable even though the *wiring* (env var name, `SetAVThreads`) is new and belongs to Phase 36 (containerize), not Phase 34 |
| ISOBMFF box-walking primitives | A new box-walker for `ftyp`/RIFF parsing | `sniff.go`'s existing fixed-12-byte-window matcher shape (`matchHEIC`, `matchWAV`) — mp4/mov/avi fit this shape exactly, live-verified this session (all three formats' identifying bytes sit within the first 12 bytes) | No new infrastructure needed for these three; only mkv/webm's EBML shape genuinely differs (Pattern 3) |

**Key insight:** Every non-EBML piece of Phase 34 is a direct extension of an already-hardened, already-tested pattern from the audio/image engines. The EBML DocType parser is the ONE genuinely new algorithm in this phase — budget review/testing time accordingly, and do not let its novelty bleed into over-engineering the other four files, which should be nearly mechanical.

## Common Pitfalls

### Pitfall 1: `-protocol_whitelist file,crypto` blocks remote-protocol SSRF but does NOT block local path-traversal reads via the concat demuxer

**What goes wrong:** A developer might assume `-protocol_whitelist file,crypto` is a complete "block all attacker-directed file/network access" control. Live-verified this session: it correctly blocks a crafted HLS `#EXTINF` segment pointing at `http://169.254.169.254/...` (ffmpeg logs `Protocol 'http' not on whitelist 'file,crypto'!` and the whole demux fails) — but a concat-demuxer directive referencing a local sibling-relative path (`file 'outside/secret.txt'`, no `..`, no leading `/`) is opened and its bytes ARE read (confirmed via ffmpeg's own `AVIOContext... bytes read` diagnostic), because the `file` protocol is legitimately whitelisted — `-protocol_whitelist` operates at the protocol layer, not the path-safety layer.

**Why it happens:** `file` must remain whitelisted for the primary input itself to work at all; `-protocol_whitelist` cannot distinguish "the primary input path" from "a path referenced inside a demuxer directive."

**How to avoid:** This phase's three features (transcode/audio-extract/thumbnail) do not use the concat demuxer at all, so this specific vector is not directly reachable through AVC-01..05 as scoped — but (a) the offline-canary test required by AVE-02/Pitfall 1 (see below) should explicitly prove BOTH halves: remote-protocol blocked AND concat-local-read is a known, separately-scoped residual (document it, don't claim it's closed by `-protocol_whitelist` alone); (b) never pass `-safe 0` to any future ffmpeg invocation this project adds — live-verified this session, the concat demuxer's OWN default (`safe=1`, no flag needed) already blocks `..`-relative and absolute-path directory traversal (`Unsafe file name` in the log), leaving only same-level-or-deeper sibling reads as a residual, and that residual is closed by ensuring each job's workDir has no sibling job data reachable via a relative path (already true today per the existing `filepath.Dir(outPath)`-scoped-workDir convention — worth stating explicitly, not assuming).

**Warning signs:** Any future opt or feature that accepts a client-supplied subtitle/playlist/attachment reference; any code review comment treating `-protocol_whitelist` as sufficient LFI defense on its own.

**Phase to address:** THIS phase (Phase 34) — the offline-canary test (AVE-02's "verified by an offline canary test") must exercise both the blocked-remote-protocol case and explicitly document the concat/local-read distinction as scoped-out-but-understood, not silently omitted.

### Pitfall 2: Out-of-range thumbnail `-ss` exits 0 with NO output file at all (not an empty file — no file)

**What goes wrong:** Live-verified this session — requesting a thumbnail at `-ss 100` against a 2-second source (`src.mp4`) produces `exit 0` and **no output file is created at all** (not a zero-byte file — `ls` reports "No such file or directory"). `validateAudioOutput`'s `os.Stat` + `size==0` check (ported verbatim) correctly catches this via the `os.Stat` error path, but a naive port that only checks `size==0` after confirming the file exists would panic/misbehave on the stat error instead.

**Why it happens:** ffmpeg's `image2` muxer, when the requested seek point is entirely past EOF, has nothing to encode and simply never opens the output file — this is a distinct failure shape from "wrote an empty file," and distinct again from PITFALLS.md Pitfall 11's other named failure mode (silent-clamp-to-nearest-frame), which was NOT observed in this session's specific test (this build errored/no-op'd rather than clamping — but do not assume this generalizes to a different ffmpeg build/version without re-verifying, since PITFALLS.md flags clamping as a real documented behavior elsewhere).

**How to avoid:** (a) pre-flight bounds-check the requested/default timecode against `ProbeDuration`'s result BEFORE invoking ffmpeg at all (reject client-supplied out-of-range timecodes with a clear error rather than letting ffmpeg attempt and silently no-op); (b) post-extraction, treat a missing OR empty output file identically (an `os.Stat` error and a `size==0` result both mean "extraction produced nothing usable") — do not write code that only handles one of the two shapes.

**Warning signs:** A thumbnail post-validation check that only handles `fi.Size() == 0` and lets an `os.Stat` "no such file" error propagate as an unrelated/confusing error instead of a clear "extraction produced no output" message.

**Phase to address:** THIS phase — ship the pre-flight bounds check and the both-shapes post-validation together with the thumbnail feature itself (mirrors `validateAudioOutput` shipping alongside the whisper pipeline it protects, not as a follow-up).

### Pitfall 3: `-c:v libwebp` must be passed EXPLICITLY for the webp thumbnail target — ffmpeg will not auto-select it from the `.webp` extension alone on every build

**What goes wrong:** Live-verified this session — `ffmpeg -ss 1.0 -i file:src.mp4 -frames:v 1 -q:v 2 thumb.webp` (no explicit `-c:v`) failed with `Automatic encoder selection failed... Default encoder for format webp (codec webp) is probably disabled. Please choose an encoder manually.` This particular local build (Homebrew ffmpeg 8.1.2) has NO `libwebp` encoder compiled in at all (confirmed via `ffmpeg -encoders | grep -i webp` returning nothing) — a different symptom from "wrong default," but the fix is the same either way: never rely on implicit extension-to-encoder mapping for the webp target.

**Why it happens:** ffmpeg's automatic encoder selection is a convenience feature, not a guarantee, and its behavior/availability varies by build configuration (STACK.md's live-verified Debian bookworm ffmpeg DOES have `libwebp`/`libwebp_anim` — this is a local-dev-environment gap, not evidence the feature is broken in the actual deployment target).

**How to avoid:** always pass `-c:v libwebp` explicitly in the thumbnail argv builder when the target is webp (same discipline `whisperArgs` already applies by always passing an explicit `-l`/`-t` rather than relying on whisper-cli's own defaults) — never omit the codec flag and trust extension-based auto-selection for ANY target format in this converter, jpg/png included, for consistency and future-proofing even though those two happened to auto-select correctly in this session's test.

**Warning signs:** A thumbnail argv builder with no `-c:v` flag for one or more of jpg/png/webp; a local dev test suite that only exercises jpg/png (the two that happened to work in this session without an explicit codec) and never actually runs the webp path against a real binary.

**Phase to address:** THIS phase — the thumbnail argv builder function (Pattern 1) should hardcode `-c:v mjpeg` (jpg), `-c:v png` (png), `-c:v libwebp` (webp) explicitly per target, verified by a live test skip-gated the same way `requireLiveAudioBinaries` skip-gates on `whisper-cli`/model absence (skip webp specifically if `libwebp` isn't in the local `ffmpeg -encoders` output, don't fail the whole test file).

### Pitfall 4: HEVC codec choice (AVO-03) needs its OWN CRF default, not x264's

**What goes wrong:** Live-verified this session — `-c:v libx265 -crf 28` (NOT `-crf 23`, x264's typical default-quality value) produced a valid, ffprobe-confirmed `codec_name: hevc` output. x264 and x265's CRF scales are NOT directly comparable (x265's perceptually-equivalent CRF values run several points higher than x264's for similar visual quality) — copying x264's CRF constant onto the HEVC path would either over-compress (too-low CRF number reused, producing unnecessarily large files) or under-compress if blindly offset the wrong direction.

**Why it happens:** The phase's own description explicitly calls this out ("свой CRF-дефолт x265, не копия x264") — flagged here because it's easy to structurally satisfy ("added an HEVC codec option") while numerically getting it wrong (copy-pasting the x264 CRF constant).

**How to avoid:** define two separate, independently-documented CRF server-constants (e.g. `x264DefaultCRF = 23`, `x265DefaultCRF = 28`, the latter live-verified functional this session, cross-check against current x265 tuning guidance during planning if a more precise value than this session's spot-check matters) — never a single shared `defaultCRF` constant reused across both encoders.

**Warning signs:** A single `crf` constant referenced by both the H.264 and HEVC argv-builder branches.

**Phase to address:** THIS phase — `AVO-03`'s own success criterion explicitly names this ("свой CRF-дефолт x265, не копия x264").

## Code Examples

All verified via live `runCommand`-shaped invocations against `ffmpeg 8.1.2`/`ffprobe 8.1.2` this session (exit codes and `ffprobe` re-checks shown inline).

### Transcode (mov -> mp4, H.264/AAC/+faststart)
```bash
# Source: this session, exit 0
ffmpeg -y -nostdin -protocol_whitelist file,crypto -i file:src.mov \
  -c:v libx264 -preset veryfast -crf 23 \
  -c:a aac -b:a 128k \
  -movflags +faststart \
  out.mp4
```

### Transcode (mp4 -> webm, VP9/Opus, always full re-encode per AVC-02)
```bash
# Source: this session, exit 0
ffmpeg -y -nostdin -protocol_whitelist file,crypto -i file:src.mp4 \
  -c:v libvpx-vp9 -b:v 1M \
  -c:a libopus \
  out.webm
```

### Audio extraction (video -> mp3/wav/m4a; AAC source + m4a target = stream copy)
```bash
# Source: this session, all four exit 0
ffmpeg -y -nostdin -protocol_whitelist file,crypto -i file:src.mp4 -vn -c:a libmp3lame -q:a 2 out.mp3
ffmpeg -y -nostdin -protocol_whitelist file,crypto -i file:src.mp4 -vn -c:a pcm_s16le out.wav
ffmpeg -y -nostdin -protocol_whitelist file,crypto -i file:src.mp4 -vn -c:a aac -b:a 128k out.m4a
# AVC-03's stream-copy case (source audio already AAC, target m4a):
ffmpeg -y -nostdin -protocol_whitelist file,crypto -i file:src.mp4 -vn -c:a copy out_copy.m4a
```

### Thumbnail extraction (fast input-side `-ss`, per-target explicit codec)
```bash
# Source: this session, jpg/png exit 0; webp requires -c:v libwebp explicitly
# (unavailable in THIS session's local build -- Debian deployment target has it, STACK.md)
ffmpeg -y -nostdin -protocol_whitelist file,crypto -ss 1.0 -i file:src.mp4 -frames:v 1 -c:v mjpeg -q:v 2 thumb.jpg
ffmpeg -y -nostdin -protocol_whitelist file,crypto -ss 1.0 -i file:src.mp4 -frames:v 1 -c:v png thumb.png
ffmpeg -y -nostdin -protocol_whitelist file,crypto -ss 1.0 -i file:src.mp4 -frames:v 1 -c:v libwebp thumb.webp
```

### ffprobe: duration guard (reuse `ProbeDuration` verbatim, no new function needed)
```bash
# Source: this session, existing internal/convert/audioduration.go's exact argv shape,
# confirmed unchanged-and-correct against a video source:
ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 file:src.mp4
# -> 2.000000
```

### ffprobe: NEW resolution + codec probe (extends `ProbeDuration`'s call shape)
```bash
# Source: this session, exit 0, JSON parseable
ffprobe -v error -select_streams v:0 \
  -show_entries stream=codec_name,width,height \
  -of json file:src.mp4
# -> {"streams":[{"codec_name":"h264","width":64,"height":64}]}

# Separate audio-codec probe for the stream-copy eligibility check (AVC-05):
ffprobe -v error -select_streams a:0 -show_entries stream=codec_name -of csv=p=0 file:src.mp4
# -> aac
```

```go
// Source: this session's live JSON shape above; mirrors ProbeDuration's
// runCommand+parse split (audioduration.go) for independent unit-testability.
type avStreamProbe struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	} `json:"streams"`
}

func ffprobeStreamArgs(path string) []string {
	// IN-01 "file:" prefix precedent, same as ffprobeDurationArgs (audioduration.go).
	return []string{"-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height",
		"-of", "json", "file:" + path}
}

func probeVideoStream(ctx context.Context, path string) (codec string, width, height int, err error) {
	out, err := runCommand(ctx, "ffprobe", ffprobeStreamArgs(path)...)
	if err != nil {
		return "", 0, 0, fmt.Errorf("ffprobe: %w", err)
	}
	var probe avStreamProbe
	if err := json.Unmarshal(out, &probe); err != nil || len(probe.Streams) == 0 {
		return "", 0, 0, fmt.Errorf("ffprobe: no video stream found or unparseable output")
	}
	s := probe.Streams[0]
	if s.Width <= 0 || s.Height <= 0 {
		return "", 0, 0, fmt.Errorf("ffprobe: implausible resolution %dx%d", s.Width, s.Height)
	}
	return s.CodecName, s.Width, s.Height, nil
}
```

### Protocol-whitelist offline canary (AVE-02's required verification)
```bash
# Source: this session, live-verified BOTH halves:

# (a) remote-protocol SSRF attempt via a crafted HLS m3u8 -- BLOCKED
cat > evil.m3u8 <<'EOF'
#EXTM3U
#EXT-X-TARGETDURATION:10
#EXTINF:10.0,
http://169.254.169.254/latest/meta-data/evil.ts
#EXT-X-ENDLIST
EOF
ffmpeg -y -protocol_whitelist file,crypto -i file:evil.m3u8 -c copy out.mp4
# -> "[http] Protocol 'http' not on whitelist 'file,crypto'!"
# -> "Error opening input: Invalid data found when processing input"
# -> exit 183 (non-zero) -- CORRECTLY BLOCKED, zero outbound connection attempted

# (b) local sibling-relative read via concat -- NOT blocked by -protocol_whitelist
# (file protocol is legitimately whitelisted); blocked instead by NEVER passing
# -safe 0 (concat demuxer's own safe=1 default blocks ../absolute, confirmed;
# a same-level sibling read like 'outside/secret.txt' with no ".." is NOT
# blocked by safe=1 either -- this vector is scoped-out by NOT using the
# concat demuxer at all in this phase's three features, see Pitfall 1)
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| Output-side seeking for thumbnails (`-i input -ss timecode`) | Input-side seeking (`-ss timecode -i input`) | Long-standing ffmpeg guidance, re-confirmed live this session | Input-side seeks near the target keyframe instead of decoding from file start — critical for both correctness (matches AVC-04's "fast input-side `-ss`" requirement) and DoS defense (Pitfall 4 in `.planning/research/PITFALLS.md`) |
| Trusting a fixed-offset magic-bytes table for every container format | Bounded-peek, declared-length-aware parsing for variable-offset formats (EBML DocType here, ID3v2 already for mp3) | This project's own established precedent (`matchMP3`, Phase 30) — Phase 34 extends the SAME discipline, not a new one | mkv/webm genuinely cannot fit the fixed-12-byte-window shape every other sniffer in this codebase uses |
| "Try `-c copy`, catch the ffmpeg error as the legality signal" | Explicit, project-owned per-target-container codec allowlist checked via `ffprobe` BEFORE attempting copy | Live-verified this session (ffmpeg's mp4 muxer silently accepts an "illegal" VP9/Opus remux) | Prevents a silent AVC-01/AVC-02 codec-contract violation that would otherwise ship with zero error signal |

**Deprecated/outdated:** None specific to this phase — ffmpeg's core CLI shape (`-i`, `-c:v`/`-c:a`, `-ss`, `-frames:v`, `-protocol_whitelist`) has been stable across the 5.1.x (Debian target)–8.1.x (this session's local verification) version range; no flag used above has been renamed or removed in that span.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Full real-world MP4 major-brand allowlist (`iso2`-`iso9`, `3gp4`/`3gp5`/`3g2a`, `dash`, etc. beyond the live-verified `isom`) — sourced from `ftyps.com`/ISOBMFF documentation, not independently reproduced against a real encoder in this session | Pattern 4 (`mp4VideoBrands`) | If a real client's video-source encoder emits a major brand not in this list, that file fails to sniff as mp4 at all (fail-closed rejection, not misdetection) — safe failure mode, but may need the allowlist widened against real client traffic; planner should treat this table as a starting point, not a closed final answer |
| A2 | The EBML header's DocType offset (28 bytes into both `src.mkv`/`src.webm` in this session) is representative of typical real-world encoders, not a ffmpeg-lavfi-specific artifact | Pattern 3 | The bounded-peek WALKER algorithm itself does not depend on this offset being constant (it walks element-by-element) — this is a low-risk assumption because the parser is designed to be offset-independent by construction; flagged only because the specific fixture used for live verification was ffmpeg-generated, not sourced from a third-party encoder (e.g. a phone camera, OBS, a different muxer) |
| A3 | x265 CRF 28 is a reasonable "own default, not copied from x264" starting value (Pitfall 4) | Pitfall 4, Code Examples | This session only confirmed CRF 28 PRODUCES valid HEVC output, not that it is the objectively "right" perceptual-quality-matched value relative to x264's CRF 23 — planner/executor should treat this as a starting point for AVO-03's default, refine against real encode-quality comparison if the milestone cares about exact quality parity, not just "it works" |
| A4 | `-nostdin` is a net-positive defense-in-depth addition (not required by any live-observed failure this session, ffmpeg behaved identically with/without it in every test run) | Pattern 1 Code Examples (added to every invocation) | Low risk — `-nostdin` is FFmpeg's own documented flag for exactly this purpose (prevent an invocation from blocking on an interactive prompt in a non-interactive/server context); worst case if "wrong" is a no-op flag, not a functional regression |

**A1/A2/A3 risk note:** none of these are compliance/security-critical claims (unlike, e.g., a retention policy or crypto standard) — they are encoder-behavior claims with safe failure modes (fail-closed rejection for A1, no functional dependency for A2, a tunable constant for A3). Confidence remains HIGH overall because the CORE mechanisms (vint parsing algorithm, protocol-whitelist blocking behavior, stream-copy muxer permissiveness) were all directly, repeatedly reproduced against a real binary this session.

## Open Questions (RESOLVED)

1. **Does `AVConverter.Pairs()` need to include audio-extract/thumbnail sources for ALL FIVE detected video formats (mp4/mov/avi/mkv/webm), or only a subset?**
   - What we know: AVC-01 names exactly `mov/avi/mkv/webm → mp4`; AVC-02 names exactly `mp4 → webm`; AVC-03/AVC-04 say "video" generically for audio-extract/thumbnail sources, without enumerating which containers.
   - What's unclear: whether `mp4`/`mov`/`avi`/`mkv`/`webm` are ALL valid audio-extract/thumbnail sources, or only the four transcode-source formats (excluding `mp4`, which is a transcode TARGET not source, in AVC-01's framing).
   - Recommendation: default to all five detected/sniffed video formats being valid audio-extract/thumbnail sources (the milestone brief's "video" wording reads as source-format-agnostic for these two features) — but this is a `Pairs()` definition the planner should lock down explicitly as a named decision, since it directly shapes the pair-count and the pair-disjointness test surface.
   - RESOLVED (34-03 Task 1): all five detected video formats are valid audio-extract/thumbnail sources. Pairs() is locked as transcode {mov,avi,mkv,webm}->mp4 + mp4->webm; audio-extract {mp4,mov,avi,mkv,webm}->{mp3,wav,m4a}; thumbnail {mp4,mov,avi,mkv,webm}->{jpg,png,webp}, with a self-disjointness test.

2. **Should `AV_MAX_DURATION_SECONDS` and the new resolution ceiling be Phase-34-scoped constants (server-constant, unconfigurable) or Phase-34-built-but-Phase-35/36-env-wired (mirrors `AudioOpts`'s `max` being a plain parameter, with the actual `AUDIO_MAX_DURATION_SECONDS` env only wired in a later phase)?**
   - What we know: `EnforceMaxDuration`'s existing signature takes `max time.Duration` as a plain parameter, NOT read from env internally (`internal/convert` never calls `os.Getenv`) — the audio engine's own env wiring happened in `cmd/audio-worker/main.go`, not in Phase 30.
   - What's unclear: whether Phase 34's own success criteria expect a concrete numeric ceiling chosen/tested in THIS phase, or just the guard FUNCTION shape (parameterized, unit-tested with arbitrary test ceilings).
   - Recommendation: mirror Phase 30 exactly — build `EnforceMaxDuration`(reused)/`EnforceMaxResolution`(new) as plain-parameter functions, unit-test with arbitrary test values, defer the actual env-var name/default/wiring to Phase 35/36 (mirrors `AUDIO_ENGINE_TIMEOUT`'s "Phase 32 re-derives from RTF measurement" pattern already recorded in `cmd/audio-worker/main.go`'s own comments).
   - RESOLVED (34-02/34-03): EnforceMaxDuration (reused) and EnforceMaxResolution (new) are built as plain-parameter functions, unit-tested with arbitrary ceilings; the actual env-var name/default/wiring is deferred to Phase 36 (mirrors Phase 30). No os.Getenv in internal/convert.

3. **Cross-converter pair-disjointness (`AVConverter.Pairs()` vs `AudioConverter.Pairs()`) — is any part of this Phase 34's responsibility, or entirely Phase 35's (per ROADMAP.md's explicit assignment)?**
   - What we know: ROADMAP.md's Phase 35 success criteria #2 explicitly owns "a dedicated pair-disjointness unit test proving zero overlap between `AVConverter.Pairs()` and `AudioConverter.Pairs()`" — this is because `AudioConverter.Pairs()` isn't extended with video-container×transcript-target pairs until Phase 35 (AVT-01).
   - What's unclear: whether Phase 34 should still add a SELF-disjointness test (no duplicate `(from,to)` pairs within `AVConverter.Pairs()` alone) as good hygiene, given the cross-converter test can't exist yet (its other operand, the extended `AudioConverter.Pairs()`, doesn't exist until Phase 35).
   - Recommendation: yes, Phase 34 should include a same-converter self-disjointness test (trivial, mirrors how `Registry.Register`'s map-based storage would silently accept a duplicate pair with "last wins" semantics) — this is cheap insurance and does not conflict with or duplicate Phase 35's cross-converter test.
   - RESOLVED (34-03 Task 1): Phase 34 adds a same-converter self-disjointness test (TestAVConverter_PairsSelfDisjoint) only; the cross-converter AVConverter-vs-AudioConverter disjointness test remains Phase 35 (its other operand does not exist until AVT-01).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `ffmpeg` | All three AV features (transcode/extract/thumbnail argv construction + live-gated tests) | ✓ (this research session, local dev host) | 8.1.2 (Homebrew, arm64) `[VERIFIED: local binary]` | `exec.LookPath`-gated test skip, mirrors `requireLiveAudioBinaries` — CI/dev hosts without ffmpeg skip live tests, argv-builder unit tests (no subprocess) still run |
| `ffprobe` | Duration guard (reused), NEW resolution/codec probe, stream-copy eligibility check | ✓ (same package as ffmpeg) `[VERIFIED: local binary]` | 8.1.2 | Same skip-gate as ffmpeg |
| `libwebp` encoder (inside the local ffmpeg build) | Thumbnail webp target, LOCAL DEV ONLY | ✗ (this session's Homebrew build lacks it) | — | Local dev test for the webp thumbnail path should skip-gate on `ffmpeg -encoders` containing `libwebp` specifically, separate from the general ffmpeg-on-PATH gate; the actual Debian deployment target (Phase 36's `Dockerfile.av-worker`) DOES have `libwebp` per `.planning/research/STACK.md`'s live-verified Debian bookworm check — no production impact, dev-environment-only gap |

**Missing dependencies with no fallback:** None — every dependency this phase needs is already present in the project's existing dev/CI toolchain assumptions (ffmpeg/ffprobe are already required for the audio engine).

**Missing dependencies with fallback:** `libwebp` locally (see table) — covered by a narrower skip-gate, not a blocker for the rest of the phase.

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-------------------|
| V1 Architecture, Design and Threat Modeling | Yes | `Converter`/`Registry` pattern reuse (process isolation via subprocess shell-out, never in-process decode); scope fence (unregistered until Phase 35) limits blast radius during development |
| V5 Input Validation | Yes | `checkStrictObject`+`DisallowUnknownFields`+closed enum/range validation for `AVOpts` (mirrors `AudioOpts`/`DocOpts`); fail-closed magic-bytes sniffing (mp4/mov/avi fixed-offset, mkv/webm bounded-peek EBML) before any byte reaches ffmpeg |
| V8 Data Protection | Partial | Fail-closed content validation prevents a misdetected/spoofed file from being written to S3 under the wrong content-type; full data-protection scope (encryption at rest, etc.) is out of this phase's scope, unchanged from existing project posture |
| V11 Cryptography | No | No new crypto surface introduced this phase |
| V12 File and Resources | Yes | `-protocol_whitelist file,crypto` (blocks remote SSRF via HLS/subtitle-embedded URLs, live-verified); duration guard (`EnforceMaxDuration`, reused) + NEW resolution guard (`EnforceMaxResolution`) as multi-axis decompression-bomb defense; `runCommand`'s process-group timeout kill |
| V14 Configuration | Partial (deferred) | ffmpeg version pinning against known decoder RCEs (e.g. PixelSmash/CVE-2026-8461, per PITFALLS.md) is explicitly Phase 36 scope (`Dockerfile.av-worker`), not Phase 34 — Phase 34 has no Docker/deployment surface at all |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|----------------------|
| SSRF via HLS/subtitle-embedded remote URL inside an otherwise-valid video file | Tampering / Information Disclosure | `-protocol_whitelist file,crypto` on every ffmpeg/ffprobe invocation — live-verified this session to correctly block an `http://` HLS segment reference with a clear log signature and non-zero exit |
| Local arbitrary-file-read via the concat demuxer's directive parsing | Information Disclosure | This phase does not use the concat demuxer at all (scoped out); if a future phase adds it, never pass `-safe 0` — live-verified this session, the demuxer's own `safe=1` default blocks `..`-relative and absolute paths (does not block same-level sibling reads, so workDir isolation remains load-bearing) |
| Filtergraph source-filter injection (`movie=`/`amovie=`) via a free-form client opt | Tampering / Elevation of Privilege (RCE-adjacent) | `AVOpts` is a closed, typed allowlist (`checkStrictObject`+enum/range validation) mapped server-side to fixed argv templates — no client string is ever concatenated into a `-vf`/`-filter_complex` argument in this phase's three features (none of transcode/extract/thumbnail use a filtergraph string at all in the argv shapes verified above) |
| Multi-axis decompression bomb (small-resolution/huge-duration or huge-resolution/small-duration) | Denial of Service | `EnforceMaxDuration` (reused) AND the new resolution ceiling (`probeVideoStream`+`EnforceMaxResolution`), both fail-closed, both required independently — verified via this session's live `ffprobe -show_entries stream=width,height` call shape |
| Decoder memory-corruption RCE (PixelSmash-class, CVE-2026-8461) | Elevation of Privilege | Out of Phase 34's scope entirely — no Docker image is built in this phase; version pinning is Phase 36's responsibility, already flagged as a Key Decision in STATE.md |
| Cross-engine sniffer misdetection (a video file silently accepted as audio input, or vice versa) | Spoofing / Tampering | Explicit disjointness unit test across `mp4VideoBrands`/`m4aBrands`/`heicBrands` (this phase); cross-converter `Pairs()` disjointness is Phase 35's explicit responsibility (see Open Question 3) |

## Sources

### Primary (HIGH confidence)
- Live-verified in this session (2026-07-19), real `ffmpeg 8.1.2`/`ffprobe 8.1.2` binaries (Homebrew, arm64/macOS), against synthetic `lavfi`-generated fixtures (`testsrc`+`sine`) in mp4/mov/avi/mkv/webm containers: transcode argv (mov→mp4 H.264/AAC/+faststart, mp4→webm VP9/Opus), audio-extract argv (mp3/wav/m4a + AAC→m4a stream-copy), thumbnail argv (input-side `-ss`, per-target codec requirement), `ffprobe` JSON stream/duration output shape, stream-copy fast-path muxer permissiveness (VP9/Opus into mp4 accepted without error), `-protocol_whitelist file,crypto` blocking a crafted HLS `http://` segment, concat demuxer `safe=1` default's traversal-blocking behavior, hex-dump-verified EBML header byte layout for mkv (`matroska` DocType) and webm (`webm` DocType) including exact vint encodings for every header element
- This repository's own code, read directly: `internal/convert/{whisper,audiosniff,sniff,audioduration,audioopts,opts,exec,convert,converters,dimensions,cgroup}.go` and their `_test.go` siblings, `.planning/PROJECT.md`, `.planning/STATE.md`, `.planning/ROADMAP.md`, `.planning/REQUIREMENTS.md`, `.planning/config.json`, `CLAUDE.md`
- `.planning/research/{SUMMARY,STACK,PITFALLS,ARCHITECTURE}.md` — milestone-level research this phase note is scoped underneath and does not repeat except where a Phase-34-specific decision required a live re-verification (EBML byte layout, stream-copy muxer behavior, protocol-whitelist canary) beyond what the milestone research already established

### Secondary (MEDIUM confidence)
- [ftyps.com](https://www.ftyps.com/) — mp4 major-brand registry, used for the `mp4VideoBrands` entries beyond the one (`isom`) directly reproduced live this session [CITED]
- RFC 8794 (EBML) — vint encoding rules cross-referenced against this session's own live byte-level verification (both agree); `.planning/research/ARCHITECTURE.md` independently cites the same RFC

### Tertiary (LOW confidence)
- None — every claim in this phase note is either live-verified this session or directly sourced from this repository's own code/planning docs; no unverified WebSearch-only claim was retained in the final note (A1's brand-table entries beyond `isom` are marked [CITED]/[ASSUMED] in the Assumptions Log, not presented as verified fact)

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — no new dependency; ffmpeg/ffprobe already project-standard, this session's local verification (8.1.2) cross-checked against the milestone research's independent Debian-container verification (5.1.9), both agree on every flag/behavior used
- Architecture: HIGH — every new file mirrors an existing, already-shipped codebase pattern; the one genuinely novel piece (EBML parser) has a byte-exact, live-verified algorithm above, not a spec-only description
- Pitfalls: HIGH — every pitfall in this note was either directly reproduced live this session (protocol-whitelist canary, stream-copy muxer permissiveness, out-of-range `-ss` behavior, missing webp encoder) or is a direct, already-documented codebase/milestone-research precedent (checkStrictObject strictness, runCommand hardening)

**Research date:** 2026-07-19
**Valid until:** 30 days for the argv/pattern guidance (ffmpeg CLI surface is stable); the ffmpeg-version-specific findings (local build lacking `libwebp`, exact CRF defaults) should be re-spot-checked if the local dev ffmpeg version changes materially before Phase 34 executes
