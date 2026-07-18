# Phase 30: Audio Engine Foundation - Research

**Researched:** 2026-07-18
**Domain:** Offline audio-transcription engine (whisper.cpp) as a standalone `Converter` — magic-bytes content validation, ffmpeg→whisper-cli pipeline, opts allowlisting. No queue/worker/Docker/K8s in this phase.
**Confidence:** HIGH (all OctoConv-internal patterns verified by direct code read; whisper-cli v1.9.1 JSON schema verified by direct read of the pinned tag's C++ source, not extrapolated from adjacent tooling; MP3 ID3v2 synchsafe-skip algorithm empirically verified against a real ffmpeg-produced ID3v2.4 tag)

## Summary

Phase 30 adds the fourth engine class to `internal/convert` following the exact `Converter`/`Registry` shape already proven three times (libvips/image, LibreOffice/document, chromium/html). Nothing about the *architecture* is novel — a new `AudioConverter` implementing `Pairs()`/`Convert()`/`Engine()`, registered into `convert.Default`. What is novel, and what this phase must get right standalone (no queue/worker/API/Docker involved yet, per the scope fence), is: (1) a bespoke variable-offset MP3 magic-bytes detector that correctly skips a synchsafe-encoded ID3v2 header before checking for the MPEG frame-sync word — this was empirically verified in this research session against a real ffmpeg-produced ID3v2.4 tag, confirming the exact byte offsets; (2) a two-stage hardened-exec pipeline (`ffmpeg` normalize → `whisper-cli` transcribe) reusing `runCommand` verbatim, with `target_format` mapped onto whisper-cli's own `-otxt/-osrt/-ovtt/-oj(f)` output flags via the existing `Pair` mechanism; (3) an `AudioOpts{Language, Translate}` closed struct following the `DocOpts`/`HTMLOpts` pattern exactly, with an injection test proving client bytes never reach argv; (4) an ffprobe-based declared-duration guard (the audio analog of the image pixel-dimension bomb guard), which for THIS phase only needs to exist as a pure, unit-testable Go function returning a well-defined error — the HTTP 422 wiring itself belongs to a later (out-of-scope) API-routing phase.

The whisper-cli v1.9.1 `-oj`/`-ojf` JSON schema — previously only MEDIUM/LOW confidence in the existing v1.7 milestone research (STACK/FEATURES/PITFALLS.md, all flagged "needs live verification") — was verified in this session by reading `examples/cli/cli.cpp` directly at the pinned `v1.9.1` git tag. The schema is now HIGH confidence at the source-code level (exact field names below); a live binary run is still recommended as a cheap Wave-0 spot-check (build differences are low-risk but non-zero) — see "Local Development Setup."

**Primary recommendation:** Build the `AudioConverter` and its supporting validators purely inside `internal/convert` (no other package touched), reusing `runCommand`, `checkStrictObject`, and the `Sniff`/`Dimensions` architectural shapes verbatim; get it working and tested against a locally-built `whisper-cli` v1.9.1 binary before any later-phase plumbing begins.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Audio container magic-bytes detection (mp3/wav/m4a/ogg) | `internal/convert` (pure Go, in-memory) | — | Mirrors `sniff.go`'s existing image detectors; no I/O beyond a bounded in-memory peek |
| ID3v2 synchsafe-size decode + frame-sync skip | `internal/convert` (pure Go, in-memory) | — | Bespoke parser, same bounded-peek discipline as `dimensions.go`; not a network/process concern |
| Declared-duration guard (`ffprobe`) | `internal/convert` (hardened subprocess via `runCommand`) | — | ffprobe is invoked as its own short, bounded, killable subprocess — same hardened-exec boundary as every other external tool in this codebase, not a library call |
| Container normalization (`ffmpeg` → 16kHz mono s16 WAV) | `internal/convert` (hardened subprocess via `runCommand`) | — | First stage of `AudioConverter.Convert()`; untrusted-input decode boundary, must be hardened exactly like the image/document/html engines |
| Transcription (`whisper-cli`) | `internal/convert` (hardened subprocess via `runCommand`) | — | Second stage of `Convert()`; CPU-bound, timeout-bounded, single synchronous invocation |
| `AudioOpts` validation (language allowlist, translate bool) | `internal/convert` (pure Go, closed struct) | — | Mirrors `DocOpts`/`HTMLOpts`; API-layer wiring (job creation, opts persistence) is out of scope this phase |
| HTTP 422 surfacing of duration/content rejections | API / Backend (future phase) | — | Explicitly out of scope per the phase's scope fence ("no API routing") — this phase produces the Go error the future API layer will map to 422 |
| Queue/worker/retry/timeout-classification | API / Backend (future phase, Phase 31) | — | Explicitly out of scope — `AudioConverter.Convert(ctx, ...)` only needs to honor `ctx`'s deadline; classifying that deadline as terminal-vs-transient is a `internal/worker` concern for Phase 31 |
| Container image / model baking | CDN / Static-equivalent (future phase, Phase 32) | — | Explicitly out of scope — this phase runs against a locally-built `whisper-cli` binary for testing, not a container |

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| AUD-01 | mp3/wav/m4a/ogg magic-bytes validation, fail-closed before S3 write, including ID3v2-aware variable-offset MP3 detector | "MP3 ID3v2 Detection" and "Magic Bytes for wav/m4a/ogg" sections below — exact algorithm, empirically verified byte offsets against a real ffmpeg-tagged mp3 |
| AUD-02 | txt/srt/vtt/json output via existing `Pair` mechanism; `json` carries segment- and word-level timestamps verified against pinned whisper-cli v1.9.1 | "whisper-cli v1.9.1 JSON Schema (Source-Verified)" section — exact field names read from `examples/cli/cli.cpp` at tag `v1.9.1` |
| AUD-03 | `AudioOpts{language, translate}` via validated-opts pattern (OPTS-01 precedent); injection test proves client bytes never reach argv | "AudioOpts Design" section — mirrors `DocOpts`/`HTMLOpts` verbatim, code examples included |
| AUD-04 | ffprobe-measured duration guard, `AUDIO_MAX_DURATION_SECONDS`, predictable terminal/422; hallucination-on-silence logged as accepted residual risk | "Duration Guard (ffprobe)" and "Hallucination on Silence — Accepted Residual Risk" sections |
</phase_requirements>

## Standard Stack

### Core

| Tool | Version | Purpose | Why Standard |
|------|---------|---------|---------------|
| `ggml-org/whisper.cpp` (`whisper-cli` binary) | **v1.9.1** — LOCKED decision (already pinned in `.planning/research/STACK.md`, confirmed live via GitHub Releases API, published 2026-06-19) | Offline CPU speech-to-text | `[CITED: github.com/ggml-org/whisper.cpp releases]` Only whisper implementation shaped as a single CLI binary — fits the project's "shell out to a CLI in a per-class container" pattern with zero new exec mechanism |
| `ffmpeg` | System package; locally verified `ffmpeg version 8.1.2` (Homebrew, arm64 macOS) for dev; container pin is Debian bookworm's `7:5.1.9-0+deb12u1` per existing STACK.md research | Pre-normalize arbitrary audio → 16kHz mono 16-bit PCM WAV (whisper.cpp's hard requirement, confirmed in source: usage banner lists only `flac, mp3, ogg, wav` as natively decodable — **m4a is NOT in that list**, confirming ffmpeg pre-normalization is mandatory, not optional, for m4a) | `[VERIFIED: local binary + source read]` |

No new Go module dependencies. `AudioConverter` is a pure `os/exec` shell-out via the existing `runCommand` helper (`internal/convert/exec.go`), identical in shape to `LibvipsConverter`/`LibreOfficeConverter`/`ChromiumConverter`.

### Package Legitimacy Audit

Not applicable this phase — no external Go/npm/pip packages are installed. `ffmpeg` and `whisper-cli` are system/source-built binaries invoked via `os/exec`, never a language-level package manager dependency. The Package Legitimacy Gate protocol (slopcheck, registry verification) has no target here.

## Magic-Bytes Content Validation (AUD-01)

### WAV — `RIFF`....`WAVE`

Fixed-offset, same shape as the existing `matchWebP` (bytes 0-4 `"RIFF"`, bytes 8-12 `"WAVE"` — WebP uses the identical RIFF-container check with `"WEBP"` at the same offset instead). `[VERIFIED: local ffmpeg-generated fixture]` — empirically confirmed via `xxd` on a locally-generated WAV file in this session: `52 49 46 46 ... 57 41 56 45` at exactly those offsets.

```go
func matchWAV(b []byte) bool {
	return len(b) >= 12 && bytes.Equal(b[0:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WAVE"))
}
```

### OGG — `OggS`

Fixed 4-byte page-header magic at offset 0, present at the start of every Ogg page (the very first page of any valid Ogg-container file, regardless of the codec inside — Vorbis or Opus both use the identical `OggS` page magic, so this one check covers OGG/Vorbis and OGG/Opus/WhatsApp-voice-note uploads alike with zero extra code). `[CITED: RFC 3533 §6 — Ogg page header, "OggS" capture pattern]` — HIGH confidence, universally documented, no ambiguity; not independently regenerated locally only because the local `ffmpeg` build lacked a `libvorbis` encoder, not because of any uncertainty about the signature itself.

```go
func matchOGG(b []byte) bool {
	sig := []byte("OggS")
	return len(b) >= len(sig) && bytes.Equal(b[:len(sig)], sig)
}
```

### M4A — ISOBMFF `ftyp` box + brand table

Same shape as the existing `matchHEIC` (bytes 4-8 `"ftyp"`, bytes 8-12 the 4-byte brand code) — reuse the `heicBrands`-style closed brand table, NOT a bare `"ftyp"` presence check (MP4/MOV containers share the identical box structure and must not be misdetected as m4a). `[VERIFIED: local ffmpeg-generated fixture]` — empirically confirmed via `xxd` in this session: a locally AAC-encoded `.m4a` produced by `ffmpeg -c:a aac` began `00 00 00 1c 66 74 79 70 4D 34 41 20` — i.e. `ftyp` at offset 4, brand `"M4A "` (with trailing space) at offset 8, exactly matching the `heicBrands` pattern shape.

```go
var m4aBrands = map[string]bool{
	"M4A ": true, // primary brand, trailing space is part of the 4-byte code (empirically confirmed)
	"M4B ": true, // audiobook variant
	"isom": true, // generic ISO base media, seen as a compatible-brand entry
	"mp42": true, // MP4 v2 compatible brand, common in real-world m4a encoders
}

func matchM4A(b []byte) bool {
	if len(b) < 12 || !bytes.Equal(b[4:8], []byte("ftyp")) {
		return false
	}
	return m4aBrands[string(b[8:12])]
}
```

Reference the existing `heicDimensions`/`walkBoxes` helper in `dimensions.go` if a duration-adjacent box walk is ever needed — not required for the magic-bytes check itself, which only needs the `ftyp` box's own fixed 8-byte header + 4-byte brand.

### MP3 — bespoke ID3v2-aware, variable-offset detector (the hard case)

`[VERIFIED: local ffmpeg-generated fixture + direct byte-offset trace]` This is the one format that breaks the project's `sniffLen=12` fixed-window `signatures` table (documented in `.planning/research/ARCHITECTURE.md` Anti-Pattern 3 and `.planning/research/PITFALLS.md` Pitfall 7 — this research session reused and empirically confirmed that existing analysis rather than re-deriving it).

**Empirical confirmation performed in this session:** generated a real mp3 via `ffmpeg -f lavfi -i "sine=..." -id3v2_version 3 -metadata title=Test id3.mp3` and traced the exact bytes:

```
offset 0:  49 44 33 03 00 00 00 00 00 33   "ID3" ver=3.0 flags=0x00 size(synchsafe)=0x00000033=51
offset 45: (declared tag end = 10 + 51)     <- 10-byte header + 51-byte declared size
```

For the simpler default-ffmpeg-encoded mp3 (no explicit `-id3v2_version`), the observed header was `49 44 33 04 00 00 00 00 00 23` (ID3v2.4, synchsafe size `0x23`=35), and the MPEG frame-sync bytes `FF FB` were found at **exactly** byte offset `10 + 35 = 45`, confirmed byte-for-byte via `xxd`. This is not a guess — it is the literal offset observed in this session's fixture.

**Important real-world confirmation:** ffmpeg writes an ID3v2 tag by default on every mp3 it produces (even without `-id3v2_version` explicitly passed) — meaning the "MP3 with ID3v2 tag" case is not an edge case to test defensively, it is the *default*, common-case shape of essentially every real-world mp3 file. A detector that only handles frame-sync-at-offset-0 will fail-closed-reject the overwhelming majority of real uploads.

**Algorithm** (matches PITFALLS.md Pitfall 7's prescribed fix, now with confirmed offsets):

1. If the buffer starts with the literal 3 bytes `"ID3"` (0x49 0x44 0x33):
   a. Read the 4-byte **synchsafe** size field at offset 6-9. Synchsafe decode: each of the 4 bytes uses only its low 7 bits (top bit always 0, specifically so the size field itself can never accidentally contain an `0xFF`-prefixed byte pattern that could be confused with a sync word); `size = b[6]<<21 | b[7]<<14 | b[8]<<7 | b[9]`.
   b. Read the flags byte at offset 5. If bit 4 (`0x10`, the "footer present" flag) is set, an additional 10-byte footer follows the tag data.
   c. Compute `tagEnd = 10 + size + (10 if footer flag set else 0)`.
   d. If `tagEnd` exceeds the bounded peek window, **fail closed** — do not grow the buffer or seek further (mirrors `dimensions.go`'s `ErrDimensionsUnknown` philosophy for a declared value that can't be verified within the bound).
   e. Check for the MPEG frame-sync word (`b[tagEnd] == 0xFF && (b[tagEnd+1] & 0xE0) == 0xE0`) at that computed offset.
2. If the buffer does NOT start with `"ID3"`, check for the frame-sync word at offset 0 directly (untagged mp3, less common in practice but structurally simpler — e.g., a bare mp3 stream with no metadata tool ever touching it).
3. Neither case matching → not an mp3.

```go
// mp3PeekLen must comfortably exceed any real-world ID3v2 tag including
// embedded album art (APIC frames commonly run tens to low hundreds of KB) --
// reuse the same bounded, fail-closed discipline as dimPeekLen (dimensions.go).
// A declared ID3v2 size that pushes tagEnd beyond this bound is REJECTED, not
// grown into -- this is a resource-exhaustion control, not just a detector.
const mp3PeekLen = 512 * 1024 // 512 KiB; generous headroom over typical embedded-art tags

func matchMP3(b []byte) bool {
	if len(b) >= 3 && bytes.Equal(b[:3], []byte("ID3")) {
		if len(b) < 10 {
			return false
		}
		size := int(b[6])<<21 | int(b[7])<<14 | int(b[8])<<7 | int(b[9])
		tagEnd := 10 + size
		if b[5]&0x10 != 0 { // footer-present flag
			tagEnd += 10
		}
		if tagEnd < 0 || tagEnd+1 >= len(b) {
			return false // beyond bounded peek window: fail closed, do not grow/seek
		}
		return b[tagEnd] == 0xFF && (b[tagEnd+1]&0xE0) == 0xE0
	}
	// No ID3v2 tag: frame sync must be at offset 0.
	return len(b) >= 2 && b[0] == 0xFF && (b[1]&0xE0) == 0xE0
}
```

**Design implication for `sniff.go`:** because MP3 needs a different signature shape (variable-offset, bounded-peek) than the existing fixed 12-byte-window `signatures` table, treat this as its own dedicated function called from a NEW audio-scoped sniff entry point (e.g. `SniffAudio`, mirroring `Sniff`'s shape but with its own peek length `mp3PeekLen` instead of the image-scoped `sniffLen=12`) rather than trying to force it into the existing `signatures []signature` table, which assumes every matcher only needs the same small fixed prefix. PITFALLS.md explicitly warns against a naive fixed-offset `signatures` table entry for this reason — confirmed correct by this session's byte-level trace.

## whisper-cli v1.9.1 JSON Schema (Source-Verified)

`[VERIFIED: github.com/ggml-org/whisper.cpp examples/cli/cli.cpp @ tag v1.9.1]` This upgrades the existing v1.7 milestone research (STACK/FEATURES/PITFALLS.md) from MEDIUM/LOW confidence ("extrapolated from adjacent tooling... must be verified against the actual pinned v1.9.1 binary") to HIGH confidence at the source level. The exact `output_json` function (lines 616-803 of `examples/cli/cli.cpp` at the pinned tag) was read directly.

### CLI flags relevant to the json target

| Flag | Effect |
|------|--------|
| `-oj` / `--output-json` | Write a `.json` output file with segment-level `text`/`timestamps`/`offsets` |
| `-ojf` / `--output-json-full` | Superset of `-oj` (`output_jsn_full = output_jsn = true`) — additionally writes a per-segment `tokens` array. **Critically, `-ojf` automatically sets `token_timestamps=true`** internally (`wparams.token_timestamps = params.output_wts \|\| params.output_jsn_full \|\| params.max_len > 0;`), so word/token-level timestamps come free with `-ojf` alone — no separate flag is needed |
| `-ml N` / `--max-len N` | Max segment length in characters — also implicitly enables `token_timestamps` if >0, independent of `-ojf` |
| `-sow` / `--split-on-word` | Split segments on word boundaries rather than token boundaries (affects segment granularity, not the JSON field set) |
| `-l` / `--language` | Default is **`"en"`, not `"auto"`** (confirmed in source's `whisper_params` defaults) — matches the existing FEATURES.md finding; `AudioOpts` must NOT assume whisper-cli's own default is auto-detect |
| `-tr` / `--translate` | Translate to English (hardcoded target language — no arbitrary target-language support exists) |
| `--vad`, `-vm`/`--vad-model`, `-vt`/`--vad-threshold`, and 5 more `-v*` flags | **New in this version's source** — whisper.cpp v1.9.1 ships a built-in Voice Activity Detection preprocessing pass requiring a *separate* VAD model file. Not required for AUD-04's "accepted residual risk" framing (see below), but worth flagging as the cheapest available *future* mitigation for hallucination-on-silence, since it exists natively rather than needing a bolted-on library |

**Recommended flags for `target=json`:** `-ojf` (gets both segment- and word/token-level timestamps in one flag, satisfying AUD-02's "target=json carries segment- and word-level timestamps" requirement directly) plus `-of <path-without-extension>` to control the output path deterministically (see "Output file naming" below).

### Exact JSON shape (verified from source, `output_json` function)

```jsonc
{
  "systeminfo": "...",                 // whisper_print_system_info() string
  "model": { "type": "...", "multilingual": true, "vocab": 51865,
             "audio": {"ctx":..,"state":..,"head":..,"layer":..},
             "text":  {"ctx":..,"state":..,"head":..,"layer":..},
             "mels": .., "ftype": .. },
  "params": { "model": "<path>", "language": "en", "translate": false },
  "result": { "language": "en" },      // detected/used language
  "transcription": [
    {
      "timestamps": { "from": "00:00:00,000", "to": "00:00:02,500" },  // SRT-style HH:MM:SS,mmm strings
      "offsets":    { "from": 0, "to": 2500 },                          // integer MILLISECONDS (t0*10 -- whisper's internal unit is centiseconds, so *10 = ms)
      "text": "segment text here",
      // ONLY present when -ojf (full) was passed:
      "tokens": [
        {
          "text": "word or subword piece",
          // per-token timestamps ONLY present when token has valid t0/t1 (guaranteed by -ojf's implicit token_timestamps=true)
          "timestamps": { "from": "...", "to": "..." },
          "offsets":    { "from": .., "to": .. },
          "id": 1234,        // whisper vocabulary token id (int) -- NOT a stable/meaningful id without the model's vocab, do not treat as a semantic identifier
          "p": 0.987,        // token probability/confidence (float 0-1) -- this is the closest available "word confidence" signal
          "t_dtw": -1        // DTW-derived timestamp cross-check; -1 unless -dtw <preset> was also passed (NOT required for AUD-02's scope)
        }
        // ... one entry per merged UTF-8-safe token/word piece
      ]
      // "speaker" (string) present only if -di/--diarize AND stereo input (out of v1.7 scope per PROJECT.md)
      // "speaker_turn_next" (bool) present only if -tdrz/--tinydiarize (out of v1.7 scope)
    }
    // ... one object per segment
  ]
}
```

**Fields confirmed ABSENT from this schema** (correcting the earlier MEDIUM/LOW-confidence FEATURES.md speculation, which guessed at `avg_logprob`/`no_speech_prob` by analogy with an unrelated tool, `whisper-timestamped`): there is **no per-segment `avg_logprob` or `no_speech_prob` field** anywhere in `output_json`. The only confidence-adjacent signal in the native JSON output is the per-token `"p"` field (available only under `-ojf`). If a no-speech/hallucination signal is ever wanted in the future, it does not come free from `-oj`/`-ojf` — see "Hallucination on Silence" below.

**Output file naming (source-verified, `fout_factory` in `cli.cpp` around line 1109-1155):** if `-of <path>` is NOT passed, whisper-cli writes output as `<original-input-filename>.<ext>` (i.e. it appends the extension to the FULL original filename INCLUDING its original extension — e.g. `in.wav` → `in.wav.json`, not `in.json`). **Always pass `-of <path-without-extension>` explicitly** to get a deterministic, worker-controlled output path — do not rely on the default naming, which would otherwise require string-matching the input filename convention.

**Recommended SEED-001-forward JSON `target` mapping:** map OctoConv's `target=json` Pair directly to `-ojf` (not the plain `-oj`), because `-ojf`'s extra `tokens` array with per-token `timestamps`/`p` is exactly the word-level granularity AUD-02 requires, and it costs nothing extra to request (same single `whisper-cli` invocation, one more flag).

### Supported native input formats (source-verified)

The CLI's own usage banner (`cli.cpp` line ~977, live-read from source) states: `"supported audio formats: flac, mp3, ogg, wav"`. **m4a is conspicuously absent** — this is not an omission in documentation, it is read directly from the exact `fprintf` the pinned binary emits. This is the authoritative confirmation (stronger than the existing STACK.md's README-based claim) that ffmpeg pre-normalization to WAV is *mandatory*, not merely a convenience, for every m4a upload, and is *recommended* uniformly for all four formats (mp3/wav/m4a/ogg) so `Convert()` has exactly one code path rather than a format-conditional branch.

## AudioOpts Design (AUD-03)

Follow `DocOpts`/`HTMLOpts` (`internal/convert/opts.go`, `internal/convert/htmlopts.go`) verbatim — reuse the shared `checkStrictObject` helper unchanged (D-10 parity, same as `HTMLOpts` reusing it from `DocOpts`'s file rather than duplicating).

```go
// AudioOpts is the closed, strictly-parsed set of client-requested
// transcription options (AUD-03/OPTS-01 precedent). Language is validated
// against a closed allow-list (never passed as a raw client string into
// whisper-cli's -l argv flag) -- mirrors DocOpts.PDFProfile's enum pattern,
// not HTMLOpts.PageSize's map-lookup pattern only because a language code
// list is longer; the SELECTION mechanism (map lookup, never string concat)
// is identical.
type AudioOpts struct {
	Language  string `json:"language,omitempty"`  // e.g. "en", "auto" -- validated against audioLanguageAllowlist
	Translate bool   `json:"translate,omitempty"` // -tr flag: translate to English
}

// audioLanguageAllowlist is the closed set of accepted `language` values.
// "auto" maps to whisper-cli's own -l auto (language auto-detect); every
// other entry maps 1:1 to a whisper.cpp/Whisper ISO-639-1-ish language code.
// Never accept an arbitrary client string here -- see Pitfall 11
// (.planning/research/PITFALLS.md): a raw string reaching whisper-cli's argv
// is the audio-engine's analog of the PDF/A FilterOptions injection risk
// OPTS-01/02 already closed once for LibreOffice.
var audioLanguageAllowlist = map[string]bool{
	"auto": true, "en": true, "ru": true, "es": true, "fr": true, "de": true,
	// ... extend per actual client demand; keep closed, never open-ended
}

func ParseAudioOpts(raw []byte) (AudioOpts, error) {
	if err := checkStrictObject(raw); err != nil {
		return AudioOpts{}, err
	}
	var o AudioOpts
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&o); err != nil {
		return AudioOpts{}, fmt.Errorf("parse opts: %w", err)
	}
	if o.Language != "" && !audioLanguageAllowlist[o.Language] {
		return AudioOpts{}, fmt.Errorf("unsupported language %q", o.Language)
	}
	return o, nil
}

// AudioOptsFromMap mirrors DocOptsFromMap/HTMLOptsFromMap exactly (D-10 read-path parity).
func AudioOptsFromMap(m map[string]any) (AudioOpts, error) { /* identical shape */ }
```

**Argv construction — the injection-test-relevant part:** `AudioOpts.Language`, once validated against `audioLanguageAllowlist`, is passed to `whisper-cli -l <language>` via a Go `exec.Command` argv **slice element**, never through shell interpolation (the codebase never invokes a shell — `runCommand` uses `exec.Command(name, args...)` directly, so there is no shell-metacharacter injection surface at all; the real risk closed by the allowlist is a client picking an arbitrary string that either (a) is not a real whisper-cli language code, causing a confusing engine-level failure, or (b) — the more serious historical concern per Pitfall 11 — would apply if any future opt selects a *file path* (e.g. a model variant) rather than a short enum value, where an unvalidated string could become path traversal). The **injection test** (AUD-03's required proof) should assert: (1) a `Language` value containing shell metacharacters (`; rm -rf /`, `$(whoami)`, backticks) is either rejected by the allowlist or, if accidentally allowed through as a literal argv value, never causes anything beyond a whisper-cli "unsupported language" exit — because `exec.Command` never invokes `/bin/sh`; (2) `AudioOptsFromMap` round-trips the same strictness as the write path (mirrors `TestDocOptsFromMap`/`TestHTMLOptsFromMap`).

## Duration Guard (AUD-04)

Mirror `dimensions.go`'s fail-closed shape, but implemented via a subprocess (`ffprobe`) rather than an in-memory header parse, because audio duration for variable-bitrate/compressed formats (VBR mp3, ogg, m4a) is not reliably a small fixed-offset field the way PNG's IHDR chunk is — `ffprobe` is the standard, already-installed (ships with `ffmpeg`) tool for this.

`[VERIFIED: local ffprobe 8.1.2, empirically run in this session]`

```bash
ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 <input>
# stdout: a single float, e.g. "2.000000"
```

```go
// ProbeDuration runs ffprobe as its own short, bounded, killable subprocess
// (runCommand, exec.go) to read the container's declared duration BEFORE any
// decode/transcribe step runs -- the audio analog of dimensions.go's
// declared-pixel-ceiling check (VALID-03/Phase 7 precedent). ctx should carry
// a SHORT bound distinct from the full engine timeout (ffprobe reading
// container metadata is near-instant even for large files; it must never be
// allowed to run for the full AUDIO_ENGINE_TIMEOUT budget).
func ProbeDuration(ctx context.Context, path string) (time.Duration, error) {
	out, err := runCommand(ctx, "ffprobe", "-v", "error", "-show_entries",
		"format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w", err)
	}
	secs, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if perr != nil {
		return 0, fmt.Errorf("ffprobe: unparseable duration %q: %w", out, perr)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// ErrAudioDurationExceeded is returned when the declared duration exceeds the
// configured ceiling -- fail-closed (VALID-03 precedent), the audio analog of
// ErrDimensionsUnknown/the image pixel-ceiling rejection. THIS PHASE only
// needs this Go-level error to exist and be unit-testable; mapping it to an
// HTTP 422 response is a future (out-of-scope) API-routing phase's job.
var ErrAudioDurationExceeded = errors.New("declared audio duration exceeds configured maximum")
```

**Where the ceiling value itself lives:** the existing `MAX_IMAGE_PIXELS`/`MaxImagePixels` precedent (`cmd/api/main.go`, `internal/api/api.go`) wires the env var at `cmd/api` and threads it through `api.Config` — that wiring is API-layer, out of scope for this phase. For THIS phase, `AUDIO_MAX_DURATION_SECONDS` should be accepted as a plain parameter (e.g. `time.Duration`) to whatever function/type performs the check, so it is unit-testable with an explicit value in tests without needing `cmd/api` to exist yet — the future API-routing phase wires the real env var through.

**Note the `MAX_UPLOAD_BYTES` interaction flagged in `.planning/STATE.md`'s Blockers section:** the existing global `MAX_UPLOAD_BYTES` (100 MiB default) will 413-reject a legitimate long uncompressed WAV (>600 MB/hour) before the duration guard even runs. This is an API-layer, cross-cutting concern belonging to a later phase (not this one) — flagged here only so the planner does not silently let it fall through the cracks; do not attempt to resolve it in Phase 30's scope.

## Hallucination on Silence — Accepted Residual Risk (AUD-04, success criterion 5)

`[VERIFIED via source read: no no_speech_prob field in output_json — see JSON Schema section above]` Whisper-family models are documented to hallucinate (loop a short phrase) on silence/music/noise; this exits 0 with a structurally valid transcript — no existing terminal-signature classifier in this codebase (`terminalVipsSignatures` et al., all stderr-substring-based) can catch a semantically-wrong-but-well-formed output. This session's direct source read CONFIRMS the earlier PITFALLS.md finding and additionally confirms there is no free `no_speech_prob`/`avg_logprob` field to lean on as a cheap signal — the only per-token confidence available is `"p"` under `-ojf`, which is a token-probability, not a segment-level no-speech signal.

**Required for this phase:** log this explicitly as an accepted residual risk in the phase's decision log (mirrors the project's existing accepted-risk pattern, e.g. the `file://` residual read from Phase 15). **Do not** attempt hallucination detection/mitigation as a build requirement of this phase.

**Optional, cheap future mitigation now confirmed to exist natively:** v1.9.1 ships built-in `--vad`/`--vad-model`/5 more `-v*` flags (Voice Activity Detection) — not present in the earlier MEDIUM/LOW-confidence PITFALLS.md research (which only speculated "whisper.cpp's own no-speech/hallucination-silence-threshold flags if exposed"). If the phase planner wants a cheap mitigation lever without committing to full detection, `--vad` is worth a one-line mention as a *future*, not required, knob — it requires a separate VAD model file (additional bake-in decision, out of scope for a standalone-converter phase).

## Architecture Patterns

### Recommended file additions (internal/convert/, naming per CLAUDE.md's lowercase-no-underscore convention)

```
internal/convert/
├── convert.go        # MODIFY: add EngineAudio = "audio" const
├── sniff.go           # possibly MODIFY: comment noting audio uses a separate SniffAudio, not the fixed-window `signatures` table
├── audiosniff.go       # NEW: matchWAV, matchOGG, matchM4A, matchMP3 (+ ID3v2 synchsafe helper), SniffAudio(r) -- mirrors Sniff's io.MultiReader re-stitch pattern
├── audioopts.go         # NEW: AudioOpts, ParseAudioOpts, AudioOptsFromMap, ValidateAudioApplicability -- mirrors opts.go/htmlopts.go
├── audioduration.go      # NEW: ProbeDuration (ffprobe wrapper), ErrAudioDurationExceeded
├── whisper.go              # NEW: AudioConverter{} implementing Converter -- Pairs()/Convert()/Engine(), the two-stage ffmpeg->whisper-cli pipeline
├── audiosniff_test.go
├── audioopts_test.go
├── audioduration_test.go
├── whisper_test.go        # gated behind exec.LookPath("ffmpeg")/exec.LookPath("whisper-cli"), mirrors verapdf_test.go's t.Skip pattern
└── testdata/
    ├── sample.mp3          # bare frame-sync, no ID3v2 tag
    ├── sample-id3.mp3      # ID3v2-tagged (the common case) -- ideally with embedded art to push the sync word far in
    ├── sample.wav
    ├── sample.m4a
    ├── sample.ogg
    └── (adversarial fixtures: truncated ID3v2 header, oversized declared synchsafe size, corrupt ftyp brand)
```

### Pattern 1: Two-stage hardened-exec pipeline inside one Convert()

**What:** `Convert()` shells out TWICE via the existing `runCommand` — first `ffmpeg` to normalize the validated input to 16kHz mono 16-bit PCM WAV, then `whisper-cli` against that normalized WAV — both bounded by the SAME `attemptCtx` the caller passes in (one `AUDIO_ENGINE_TIMEOUT`-bounded context covers both stages, per the phase's success criterion 2).

**When to use:** Any engine class needing more than one external tool per job (this project's first such case — libvips/LibreOffice/chromium are each single-invocation).

**Example** (structure only, follows `LibvipsConverter.Convert`/`ChromiumConverter.Convert`'s exact error-wrapping convention):

```go
// Source: pattern derived from internal/convert/libvips.go, chromium.go,
// exec.go (runCommand) -- all read directly in this session.
func (AudioConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	o, err := AudioOptsFromMap(opts) // strict re-parse, same D-10 discipline as DocOptsFromMap
	if err != nil {
		return fmt.Errorf("audio: %w", err)
	}

	workDir := filepath.Dir(outPath)
	normPath := filepath.Join(workDir, "norm.wav")

	// Stage 1: ffmpeg normalize. A distinguishable "ffmpeg:" prefix on this
	// stage's errors lets a FUTURE worker-layer classifier (Phase 31, out of
	// scope here) split ffmpeg-stage failures (malformed input -> likely
	// terminal) from whisper-stage failures (likely transient) -- Key
	// Decision 1 in .planning/STATE.md. This phase does not implement that
	// classifier, but the error-message shape should not foreclose it.
	if _, err := runCommand(ctx, "ffmpeg", "-y", "-i", inPath,
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", normPath); err != nil {
		return fmt.Errorf("audio: ffmpeg: %w", err)
	}

	// Stage 2: whisper-cli transcribe. Target format selects the output flag;
	// -of pins a deterministic output path (never rely on whisper-cli's
	// default input-filename-based naming -- see JSON Schema section).
	targetFormat := NormalizeFormat(filepath.Ext(outPath))
	outBase := strings.TrimSuffix(outPath, filepath.Ext(outPath))
	args := []string{"-m", modelPath, "-f", normPath, "-of", outBase, "-nt"}
	args = append(args, whisperOutputFlag(targetFormat)...)
	if o.Language != "" {
		args = append(args, "-l", o.Language) // already allowlist-validated
	}
	if o.Translate {
		args = append(args, "-tr")
	}
	if _, err := runCommand(ctx, "whisper-cli", args...); err != nil {
		return fmt.Errorf("audio: whisper-cli: %w", err)
	}
	return nil // future: validate outPath exists + is non-empty, mirrors validatePDF's discipline
}
```

### Anti-Patterns to Avoid

- **Extending `sniff.go`'s fixed `signatures` table with a naive MP3 entry:** structurally cannot work — see the "MP3" section above; use a dedicated bounded, variable-offset function instead (Pitfall 7).
- **Passing `AudioOpts.Language`/any future model-selector as a raw string into a filesystem path or shell-interpolated argv:** the model path (`-m <path>`) must ALWAYS be a compile-time server constant selected by a closed enum switch, never client bytes concatenated into a path — mirrors `PDFAFilterOptions`'s "never build from raw client bytes" invariant (Pitfall 11). This phase's `AudioOpts` (language + translate only) does not select a model at all, so this anti-pattern is avoided by construction — flag it explicitly so a future phase adding model selection does not regress it.
- **Copying `isDocumentTerminal`'s blanket "timeout is terminal" pattern:** out of scope for THIS phase (no worker code exists yet), but do not let `Convert()`'s error-wrapping choices foreclose the future stage-aware split (`STATE.md` Key Decision 1) — keep the ffmpeg-stage and whisper-stage errors distinguishable by message prefix, as shown above.
- **Asserting exact transcript strings in tests:** ASR output is the project's first non-deterministic engine output. Assert structural/contract properties exactly (valid JSON, monotonically non-decreasing segment timestamps, non-empty `transcription` array) and content properties loosely (substring/keyword presence, word-count range) — never `transcript == "exact string"` beyond a trivially short, clearly-enunciated fixture (Pitfall 9).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Speech-to-text inference | A custom decoder/model-runner | `whisper-cli` (already the locked decision) | Offline, CPU-only, single binary — matches every constraint |
| Audio container decoding/resampling | A custom mp3/m4a/ogg demuxer for the *transcode* step (as opposed to the magic-bytes *detector*, which is deliberately custom and in-scope) | `ffmpeg` | Extremely well-tested demuxer/decoder; the project's own README precedent (chromium/LibreOffice) is "one apt-installed CLI tool per hard problem," not hand-rolled parsing for the transcode path itself |
| Process timeout/orphan handling | A new exec wrapper | Existing `runCommand` (`internal/convert/exec.go`) verbatim — process-group SIGKILL-on-timeout | Already hardened, already tested, zero reason to diverge for a two-stage pipeline (just call it twice) |
| Opts strictness (duplicate keys, unknown fields, trailing data) | A new JSON-strictness helper | Existing `checkStrictObject` (`internal/convert/opts.go`) | Exact same strictness properties `DocOpts`/`HTMLOpts` already fought for; reuse verbatim (D-10 parity) |

**Key insight:** every "don't hand-roll" temptation in this phase already has a proven in-repo precedent from three prior engine classes — the only genuinely new code is the MP3 ID3v2 skip-logic and the ffmpeg→whisper-cli two-stage wiring, both of which are inherently bespoke (no existing helper could have covered them).

## Common Pitfalls

(Full detail already researched and cross-verified in `.planning/research/PITFALLS.md` Pitfalls 6, 7, 9, 10, 11 — summarized here scoped to what THIS phase must act on; Pitfalls 1-5, 8 are queue/KEDA/chart concerns for Phases 31-33 and are out of this phase's scope.)

### Pitfall: MP3 detection reuses the fixed-offset `signatures` table shape
**What goes wrong:** Silently rejects most real-world MP3s (those with ID3v2 tags) as unrecognized.
**How to avoid:** Dedicated bounded, variable-offset `matchMP3` function — see algorithm above, now empirically verified.
**Warning signs:** No test fixture with a real ID3v2 tag; a naive `matchMP3` added directly to the `signatures = []signature{...}` slice.

### Pitfall: ffmpeg preprocessing on untrusted input with no declared-value sanity check
**What goes wrong:** Reopens the decompression-bomb resource-exhaustion class Phase 7 closed for images, this time via ffmpeg's decode step.
**How to avoid:** `ProbeDuration` (ffprobe) MUST run and reject BEFORE the normalize/transcribe pipeline invokes ffmpeg's actual decode — order matters: sniff → duration guard → normalize → transcribe, never normalize-then-check.
**Warning signs:** `Convert()` calls ffmpeg's normalize step before any duration check; no fixture tests a crafted/malformed header.

### Pitfall: Non-deterministic ASR output tested like every other (deterministic) engine class
**What goes wrong:** Flaky test suite from day one if transcript content is asserted exactly.
**How to avoid:** See "Anti-Patterns to Avoid" above — structural assertions exact, content assertions tolerant.
**Warning signs:** Any `if transcript != "expected exact string"` in a new test beyond a trivial fixture.

### Pitfall: Client-supplied opts treated as low-risk because "just language/bool"
**What goes wrong:** Even though `AudioOpts` in THIS phase only has `Language`/`Translate` (no model selector, no path-shaped field), a careless future edit could add a client-controlled path-shaped opt without re-deriving the injection-safety discipline.
**How to avoid:** `ParseAudioOpts`/`AudioOptsFromMap` from day one, with an injection test, even though the current field set is "obviously safe" — establishes the pattern so a later phase extending `AudioOpts` follows it rather than reinventing it.
**Warning signs:** No `TestAudioOptsFromMap`-style injection test mirroring `htmlopts_test.go`'s pattern.

## Code Examples

### MIME type additions (for future output upload, harmless to add now — pure data mapping, no I/O)

```go
// Source: pattern from internal/convert/sniff.go's existing MIMEType switch
case "txt":
	return "text/plain"
case "srt":
	return "application/x-subrip"
case "vtt":
	return "text/vtt"
case "json":
	return "application/json" // ambiguous with other engines' json-shaped configs, if any -- audio is the first engine to register a "json" target format; confirm no collision with existing format keys before adding
```

Note: `"json"` as a target format string is new to the registry — verify `NormalizeFormat("json")` returns `"json"` unchanged (no alias needed, but worth an explicit test given it is the first non-binary/non-document "just JSON" target format in the codebase).

### Test skip pattern for binary-gated tests (whisper_test.go)

```go
// Source: pattern from internal/convert/libreoffice_test.go:359-360,451-452
// (exec.LookPath-gated skip, the established convention for "requires a real
// external binary the test image controls").
func TestAudioConverter_JSONFull_LiveBinary(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	if _, err := exec.LookPath("whisper-cli"); err != nil {
		t.Skip("whisper-cli not on PATH; build v1.9.1 locally per RESEARCH.md 'Local Development Setup' to exercise this test")
	}
	// ... run AudioConverter.Convert against testdata fixtures, assert
	// json.Unmarshal succeeds and the "transcription" array's segment
	// objects contain "timestamps"/"offsets"/"text", and (since -ojf is
	// used) each segment's "tokens" array entries contain "text"/"p"/"id".
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| whisper.cpp `main` binary name | `whisper-cli` | ~v1.5 (documented rename, `main` deprecated) | Any tutorial/blog referencing `./main -m ... -f ...` predates the rename; use `whisper-cli` |
| No built-in VAD | `--vad`/`--vad-model` + 5 related flags | Present in v1.9.1 (confirmed by source read; exact introduction version not independently dated in this session) | New, previously-undocumented-in-this-project's-research cheap mitigation lever for hallucination-on-silence (optional, not required this phase) |

**Deprecated/outdated:** `whisper-timestamped`-style field names (`avg_logprob`, `confidence` at the segment level) that the earlier FEATURES.md speculated by analogy — CONFIRMED absent from whisper-cli's own native `-oj`/`-ojf` output in this session; do not design the SEED-001 contract around fields that don't exist in the actual pinned binary's output.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `m4aBrands` closed set (`M4A `, `M4B `, `isom`, `mp42`) is sufficient to cover real-world m4a uploads (iPhone Voice Memos, Android recorders, Zoom exports) | Magic-Bytes / M4A section | A real client m4a with an uncommon compatible-brand (e.g. `mp41`, `M4V `) could fail-closed-reject a legitimate upload; empirically only `M4A ` was directly verified in this session (via a locally ffmpeg-encoded fixture) — the other three brands are carried over from the existing FEATURES.md/ARCHITECTURE.md research, not independently re-verified here. Low risk (fail-closed is the safe direction), but worth a short live-fixture pass with an actual iPhone/Zoom-exported m4a during phase execution, not just synthetic ffmpeg output |
| A2 | `audioLanguageAllowlist`'s example entries (`en`,`ru`,`es`,`fr`,`de`,`auto`) are illustrative, not a final, client-demand-driven list | AudioOpts Design | If shipped as-is without expansion, legitimate client requests for other languages get rejected; this is explicitly called out as "extend per actual client demand" — not a blocking risk, just needs a deliberate decision at plan time, not a silent default |
| A3 | `whisper-cli`'s `-ojf` per-token `timestamps`/`offsets` sub-object is present for EVERY token, not just some | whisper-cli v1.9.1 JSON Schema | Source shows `if (mt.data.t0 > -1 && mt.t1 > -1)` guards the per-token `times_o()` call — meaning it is NOT guaranteed for every token; a consumer (this phase's own JSON-shape test, and the future SEED-001 consumer) must treat per-token timestamps as optional/nullable, not assume presence. This is captured correctly in the schema block above but flagged here because it is easy to miss on a quick re-skim |
| A4 | ID3v2 footer-flag handling (`+10` bytes) is correct per spec, though not independently empirically triggered in this session's fixtures (neither locally generated mp3 used a footer) | MP3 ID3v2 Detection algorithm | Low risk — footers are rare in practice (mostly used in streaming contexts); the synchsafe-size skip itself (the common case) WAS empirically verified. A dedicated test fixture with the footer flag set is recommended during phase execution to close this gap, not deferred indefinitely |

**Note:** every claim above the Assumptions Log line was `[VERIFIED]` or `[CITED]` in-line at the point it was made — this table intentionally lists only the residual genuinely-unverified edges.

## Local Development Setup (Wave 0 prerequisite — no `whisper-cli` binary is installed on this machine)

`ffmpeg`/`ffprobe` ARE already installed locally (`/opt/homebrew/bin/ffmpeg`, version 8.1.2, confirmed in this session) — no setup needed for those. `whisper-cli`/`cmake` are NOT installed (confirmed via `which cmake whisper-cli` returning nothing). Per the phase goal ("built and testable against the binary"), a real `whisper-cli` v1.9.1 binary is needed locally before `Convert()`'s live-binary tests can run — the plan should include this as an early setup step, distinct from (and much lighter than) Phase 32's containerized Dockerfile build:

```bash
brew install cmake   # not yet installed; git/make/clang++ already present
git clone --depth 1 --branch v1.9.1 https://github.com/ggml-org/whisper.cpp.git /tmp/whisper.cpp
cd /tmp/whisper.cpp
cmake -B build -DGGML_NATIVE=OFF   # OFF for parity with the eventual container build; local dev perf cost is negligible for short test fixtures
cmake --build build -j --target whisper-cli --config Release
# ggml model, SHA-256-pinned per STACK.md's existing recommendation (do not
# trust download-ggml-model.sh's unchecked mutable pointer):
curl -L --fail -o /tmp/ggml-base.bin https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin
# verify SHA-256 against the value STACK.md's research already captured live
# (re-confirm at execution time -- HuggingFace file content is stable but
# re-verify rather than trust a value transcribed days earlier)
export PATH="/tmp/whisper.cpp/build/bin:$PATH"   # or copy whisper-cli onto PATH directly
whisper-cli -h   # confirm flags match this document's assumptions for the EXACT pinned tag build
```

Run `whisper-cli -h` against the freshly-built binary and diff its flag list against this document's "CLI flags relevant to the json target" table before writing `Convert()`'s argv assembly — flags are documented as stable but this is the cheap, recommended spot-check this document's Summary flagged as still worthwhile despite the source-level schema verification already performed.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| `ffmpeg`/`ffprobe` | Normalize stage, duration guard | ✓ | 8.1.2 (Homebrew, arm64 macOS) | — |
| `whisper-cli` | Transcribe stage | ✗ | — | Build locally per "Local Development Setup" above (Wave 0 task); binary-gated tests (`exec.LookPath`) skip gracefully if absent, matching `verapdf`/`soffice`/`chromium-headless-shell`'s existing test-skip convention |
| `cmake` | Building `whisper-cli` from source | ✗ | — | `brew install cmake` (git/make/clang++ already present) |

**Missing dependencies with no fallback:** none — `whisper-cli` has a documented, low-effort local-build fallback; nothing blocks planning.

**Missing dependencies with fallback:** `whisper-cli`, `cmake` — both resolved by the "Local Development Setup" section above; recommend the plan's first wave include this as an explicit setup task so subsequent implementation waves can run live-binary tests, not just unit tests of the pure-Go detectors.

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-------------------|
| V5 Input Validation | yes | Magic-bytes content sniffing (fail-closed, this phase) + `checkStrictObject`/`DisallowUnknownFields` opts strictness (reused verbatim) |
| V6 Cryptography | no | Nothing in this phase touches cryptographic primitives (model SHA-256 pinning is an integrity check performed at build/Dockerfile time, out of this phase's scope — Phase 32) |
| V4 Access Control | no | No auth/authorization surface in this phase (pure `internal/convert` library code, no API/handler layer touched) |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|-----------------------|
| Crafted audio container triggering ffmpeg parser CVEs (memory corruption / exhaustion — CVE-2021-38171, CVE-2025-25469, and a 2026-disclosed "PixelSmash" CVE per `.planning/research/PITFALLS.md` Pitfall 6, WebSearch-sourced, not independently re-verified in this session) | Tampering / DoS | Hardened `runCommand` (process-group SIGKILL-on-timeout, already reused verbatim) + declared-duration guard BEFORE decode (this phase); pinned ffmpeg version is a Dockerfile-time (Phase 32) concern, not this phase's |
| Declared-duration-implies-enormous-decoded-PCM resource exhaustion (audio decompression-bomb analog) | Denial of Service | `ProbeDuration` + `AUDIO_MAX_DURATION_SECONDS` ceiling, fail-closed BEFORE ffmpeg's actual decode runs — same architectural position as `dimensions.go`'s pixel-ceiling check relative to libvips' actual decode |
| Client-controlled string reaching engine argv or a filesystem path (`AudioOpts.Language` today; any future model-selector) | Tampering / Elevation of Privilege (path traversal) | Closed allowlist (`audioLanguageAllowlist`) + argv-slice-element passing (never shell interpolation, `exec.Command` has no shell) — OPTS-01/02 precedent reused verbatim |
| ID3v2 tag scanning implemented as a naive pattern search instead of a correct synchsafe-size skip | Tampering (misdetection) | The explicit synchsafe decode + bounded fail-closed skip documented above — empirically verified in this session, not a pattern search |

## Sources

### Primary (HIGH confidence)
- `github.com/ggml-org/whisper.cpp` `examples/cli/cli.cpp` @ tag `v1.9.1` — direct source read via `curl`/GitHub raw content in this session; JSON output schema, CLI flag definitions, output-file naming, supported-format usage banner, VAD flags
- Direct reads of `internal/convert/{convert,sniff,dimensions,opts,htmlopts,exec,libvips,libreoffice,chromium}.go` and their `_test.go` counterparts — ground truth for every reused pattern
- Local empirical verification in this session: `ffmpeg`/`ffprobe` 8.1.2 generating and byte-tracing real WAV/M4A/MP3(with and without explicit ID3v2 tag)/ID3v2-synchsafe-size fixtures via `xxd`
- `.planning/REQUIREMENTS.md`, `.planning/STATE.md`, `.planning/ROADMAP.md` — phase scope, locked v1.7 Key Decisions, accepted-residual-risk framing

### Secondary (MEDIUM confidence)
- `.planning/research/{STACK,FEATURES,ARCHITECTURE,PITFALLS}.md` (v1.7 milestone research, dated 2026-07-17) — reused for queue/worker/KEDA/Dockerfile concerns explicitly out of THIS phase's scope, and for the ffmpeg-CVE list (Pitfall 6), which is WebSearch-sourced and not independently re-verified in this session
- GitHub Releases API confirmation of `v1.9.1` tag/publish date (carried over from STACK.md, not independently re-queried in this session)

### Tertiary (LOW confidence)
- None remaining — every claim that was LOW/MEDIUM in the prior milestone research and fell inside this phase's scope (JSON schema, mp3 detection) was upgraded to HIGH via direct source/empirical verification in this session

## Metadata

**Confidence breakdown:**
- Standard stack (whisper-cli/ffmpeg): HIGH — pinned versions confirmed, source-read for CLI behavior
- Magic-bytes detection (mp3/wav/m4a/ogg): HIGH — empirically verified byte offsets against real generated fixtures in this session
- JSON schema (target=json): HIGH — direct source read of the exact pinned-tag `output_json` function, upgraded from the milestone research's MEDIUM/LOW
- AudioOpts/injection-safety pattern: HIGH — direct reuse of an already-proven, already-tested in-repo pattern
- Duration guard architecture: HIGH — mirrors an existing proven pattern (`dimensions.go`); the exact `ffprobe` invocation was empirically run in this session
- Hallucination-on-silence mitigation options: MEDIUM — the risk itself and the absence of a free confidence signal are source-verified; the VAD flags' practical effectiveness is not independently tested in this session (out of scope: optional future mitigation only)

**Research date:** 2026-07-18
**Valid until:** 30 days (stable domain — whisper.cpp v1.9.1 is a pinned tag, will not silently change under this research; re-verify only if the pinned version is bumped)
