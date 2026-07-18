---
phase: 30-audio-engine-foundation
reviewed: 2026-07-18T02:09:23Z
depth: standard
files_reviewed: 10
files_reviewed_list:
  - internal/convert/audiosniff.go
  - internal/convert/audiosniff_test.go
  - internal/convert/audioduration.go
  - internal/convert/audioduration_test.go
  - internal/convert/audioopts.go
  - internal/convert/audioopts_test.go
  - internal/convert/whisper.go
  - internal/convert/whisper_test.go
  - internal/convert/convert.go
  - internal/convert/sniff.go
findings:
  critical: 1
  warning: 4
  info: 2
  total: 7
status: issues_found
---

# Phase 30: Code Review Report

**Reviewed:** 2026-07-18T02:09:23Z
**Depth:** standard
**Files Reviewed:** 10
**Status:** issues_found

## Summary

Reviewed the Phase 30 audio-engine foundation: ID3v2-aware audio sniffing (`SniffAudio`), the ffprobe duration guard (`ProbeDuration`/`EnforceMaxDuration`), validated `AudioOpts`, the two-stage `AudioConverter` (ffmpeg → whisper-cli), and the touched shared files (`convert.go`, `sniff.go`), plus their stdlib tests. Verified against the existing engine-class patterns (`opts.go`, `htmlopts.go`, `libvips.go`, `exec.go`) and CLAUDE.md conventions.

The strongest parts hold up under adversarial input: `matchMP3`'s synchsafe/offset arithmetic is overflow-safe on both 32- and 64-bit `int` (max computed `tagEnd` ≈ 0x1FFFFFFF + 20), every slice access is bounds-guarded, oversized declared ID3 sizes fail closed inside the bounded 512 KiB peek window, and no client byte can reach subprocess argv (closed language allowlist, argv-slice construction, no shell). Opts parsing correctly reuses `checkStrictObject` (D-10 parity). Tests reference the binary fixtures at `internal/convert/testdata/audio/` correctly (verified present: `jfk.wav`, `sample.wav`, `sample.m4a`, `sample-id3.mp3`), skip-gates mirror the `exec.LookPath` convention, and `go vet` / `go build` / `go test ./internal/convert/` all pass.

However, the duration guard — the phase's headline fail-closed control — is bypassable by adversarial declared durations via unvalidated float→`time.Duration` conversion (CR-01), the m4a brand allowlist contradicts its own stated T-30-04 requirement by admitting plain MP4 video (WR-01), and there are three further robustness gaps detailed below.

## Critical Issues

### CR-01: Duration ceiling guard bypassable via float→Duration overflow, NaN, and negative declared durations

**File:** `internal/convert/audioduration.go:37` (conversion), `internal/convert/audioduration.go:61` (comparison)
**Issue:** `ProbeDuration` converts the ffprobe-reported (attacker-influenced, container-declared) duration with `time.Duration(secs * float64(time.Second))` and `EnforceMaxDuration` only checks `d > max`. Three adversarial inputs defeat the guard:

1. **Overflow (production-arch bypass).** ffmpeg's internal duration is int64 microseconds, so a crafted container can make ffprobe print up to ~9.2e12 seconds. `secs * 1e9` then exceeds `math.MaxInt64`, and Go's out-of-range float→int conversion is *implementation-defined* (Go spec §Conversions): on amd64 — the likely production Docker architecture — it yields `math.MinInt64` (negative), so `d > max` is **false** and an absurdly-long-declared file **passes** the guard. (Verified empirically that darwin/arm64 saturates to `MaxInt64` and rejects — so local tests will never catch the amd64 bypass.)
2. **Negative durations.** ffprobe can emit negative durations for malformed containers; `d` becomes negative, `d > max` is false, guard passes on every platform.
3. **NaN.** `ParseFloat` accepts `"nan"`/`"inf"`; NaN propagates through the multiplication into an implementation-defined conversion result (0 on arm64), passing the guard.

The guard exists precisely to reject oversized *declared* durations from untrusted input (T-30-02, the audio analog of the pixel ceiling), and it is defeated by exactly that adversarial class. Residual blast radius is bounded by the engine-timeout ctx, but the control itself is broken, and the bypass is architecture-dependent (invisible on dev machines, live in production).
**Fix:** Validate in float space before any conversion:

```go
secs, perr := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
if perr != nil {
    return 0, fmt.Errorf("ffprobe: unparseable duration %q: %w", out, perr)
}
if math.IsNaN(secs) || math.IsInf(secs, 0) || secs < 0 || secs > maxSaneSeconds {
    return 0, fmt.Errorf("ffprobe: implausible duration %v", secs)
}
return time.Duration(secs * float64(time.Second)), nil
```

where `maxSaneSeconds` is a server constant safely below `float64(math.MaxInt64) / 1e9` (e.g. `1 << 31` seconds ≈ 68 years — any real ceiling is far below this). Alternatively (or additionally), compare in float space in `EnforceMaxDuration`: `if secs > max.Seconds() { ... }`. Add a unit test that feeds a synthetic oversized/negative/NaN value through the validation path.

## Warnings

### WR-01: m4a brand allowlist admits plain MP4 video, contradicting its own T-30-04 requirement

**File:** `internal/convert/audiosniff.go:20-25` (allowlist), `internal/convert/audiosniff.go:43-48` (matcher)
**Issue:** The `m4aBrands` doc comment states "MP4, MOV, and other ISOBMFF containers … must NOT be misdetected as m4a (T-30-04)", but the allowlist includes `"isom"` and `"mp42"` — which are the *most common major brands of ordinary MP4 video files*. `matchM4A` only reads bytes 8–12 (the **major** brand), so any standard `.mp4` video (major brand `isom` or `mp42`) sniffs as `m4a` and would be routed to the audio engine once Phase 31 wires the flow. The comment also mis-describes the code: it calls this a "major/compatible-brand allowlist" and annotates `isom` as "seen as a compatible-brand entry", but the compatible-brands list (bytes 16+) is never scanned. Meanwhile the shipped fixture (`sample.m4a`) carries major brand `M4A ` (verified: `ftypM4A `), and `TestMatchM4A_ForeignBrandNotDetected` only tests `qt ` and `mp41` — the exact brands that make this a video-misdetection hole are the ones allowlisted.
**Fix:** Either (a) remove `isom`/`mp42` from the major-brand allowlist and instead scan the compatible-brands entries (bytes 16..min(boxSize, len(b)) in 4-byte steps) for `M4A `/`M4B ` when the major brand is generic — matching what the comment already claims; or (b) if `isom`/`mp42` majors must be accepted for real-world m4a encoders, update the comment to explicitly accept the MP4-video-misdetection tradeoff and add a downstream note that the audio pipeline may receive video-bearing ISOBMFF files. Add a test with major brand `isom` + a video-style compatible-brand list asserting the intended behavior either way.

### WR-02: Convert runs the full expensive pipeline for unsupported targets before failing

**File:** `internal/convert/whisper.go:140-143` (flag selection), `internal/convert/whisper.go:75-88` (nil default)
**Issue:** `whisperOutputFlag` returns `nil` for any target outside {txt, srt, vtt, json}, and `Convert` never checks for that. For an `outPath` with an unrecognized or missing extension, `Convert` still runs stage 1 (full ffmpeg decode/normalize of the input) and stage 2 (a full whisper-cli transcription — the most expensive operation in the system) with *no output flag*, then fails only at `validateAudioOutput`'s `os.Stat` with a misleading "stat output" error. Registry routing makes this unreachable in the wired flow, but `Convert` is an exported method on an exported type and the class-sibling converters fail fast on their invalid-input paths; here a caller bug burns a full engine-timeout budget before surfacing.
**Fix:** Fail fast before stage 1:

```go
outFlags := whisperOutputFlag(targetFormat)
if outFlags == nil {
    return fmt.Errorf("audio: unsupported target format %q", targetFormat)
}
```

(compute `targetFormat` above the ffmpeg stage; append `outFlags` in stage 2). Add an ungated unit test asserting `Convert(ctx, in, "out.xyz", nil)` errors without invoking any subprocess.

### WR-03: Absent language silently defaults to whisper-cli's English default, not auto-detect

**File:** `internal/convert/whisper.go:149-151`
**Issue:** When `o.Language == ""` (the default for absent/empty opts), no `-l` flag is passed and whisper-cli falls back to its built-in default, `-l en`. For a Russian-first internal client base (per project constraints), the *default* behavior of the audio engine mis-transcribes Russian audio into English-token garbage while exiting 0 with a structurally valid transcript — indistinguishable downstream from the documented hallucination risk, but entirely avoidable. The allowlist already contains `"auto"`, and nothing in the code or comments records "default = English" as a deliberate choice.
**Fix:** Make the no-opts default explicit — either pass auto-detect by default:

```go
lang := o.Language
if lang == "" {
    lang = "auto"
}
args = append(args, "-l", lang)
```

or, if English-default is intentional, document it at the flag-append site and in `AudioOpts.Language`'s doc comment so Phase 31's API docs can surface it to clients.

### WR-04: MIMEType lacks audio input formats while its doc comment claims full audio coverage

**File:** `internal/convert/sniff.go:104-146`
**Issue:** This phase extended `MIMEType`'s doc comment to claim it covers "the four audio transcription output targets (whisper, AUD-02) — so every job type is served with the same Content-Type correctness guarantee," and added `txt`/`srt`/`vtt`/`json`. But the four audio *input* formats (`mp3`, `wav`, `m4a`, `ogg`) are absent. `internal/api/handlers.go:426` stores `convert.MIMEType(detected)` as the uploaded input's Content-Type, so the moment Phase 31 wires audio uploads, every audio input is stored as `application/octet-stream` — silently breaking the "same Content-Type correctness guarantee" the comment asserts, in the file that was edited to assert it.
**Fix:** Add the input formats now, alongside the outputs added in this phase:

```go
case "mp3":
    return "audio/mpeg"
case "wav":
    return "audio/wav"
case "m4a":
    return "audio/mp4"
case "ogg":
    return "audio/ogg"
```

and extend `TestAudioConverter_Contract`'s MIMEType assertions to cover them.

## Info

### IN-01: ffmpeg/ffprobe treat path arguments as URLs — cheap "file:" hardening available for Phase 31

**File:** `internal/convert/audioduration.go:28-29`, `internal/convert/whisper.go:131-132`
**Issue:** ffprobe receives `path` as a positional argument and ffmpeg receives `inPath` via `-i`; both tools interpret these as URL specifiers (`concat:`, `http:`, `pipe:`, etc.) and treat leading-`-` values as options. Today all paths are server-generated workdir paths, so there is no current vulnerability — but the Phase 31 worker wiring is where a client-influenced filename could first leak into these positions.
**Fix:** Defense-in-depth for the Phase 31 wiring: prefix with the explicit `file:` protocol (e.g. `"file:" + path`) or assert the path is absolute and workdir-rooted before invoking; at minimum, record this constraint in a comment on both call sites.

### IN-02: sniff → duration-guard → normalize ordering is documented but not enforced by any code path

**File:** `internal/convert/audioduration.go:41-46`, `internal/convert/whisper.go:112`
**Issue:** `EnforceMaxDuration`'s comment mandates the order "sniff -> duration guard -> normalize -> transcribe, never normalize-then-check," but `AudioConverter.Convert` neither calls the guard nor asserts it ran — the ordering exists only as a convention the future Phase 31 worker must remember to wire. This mirrors the existing dimensions-guard precedent (worker-level wiring), so it is consistent, but the invariant currently lives in comments alone and a wiring omission would silently skip the ceiling entirely.
**Fix:** When Phase 31 wires the worker, add an integration test asserting an over-ceiling file is rejected *before* ffmpeg runs (e.g. no `norm.wav` created), pinning the ordering the comment promises.

---

_Reviewed: 2026-07-18T02:09:23Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
