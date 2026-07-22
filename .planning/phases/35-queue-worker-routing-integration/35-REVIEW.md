---
phase: 35-queue-worker-routing-integration
reviewed: 2026-07-22T00:00:00Z
depth: standard
files_reviewed: 12
files_reviewed_list:
  - internal/convert/av.go
  - internal/convert/avduration.go
  - internal/convert/whisper.go
  - internal/convert/converters.go
  - internal/queue/queue.go
  - internal/queue/client.go
  - internal/worker/worker.go
  - internal/api/api.go
  - internal/api/handlers.go
  - internal/reconciler/reconciler.go
  - cmd/av-worker/main.go
  - cmd/api/main.go
findings:
  critical: 1
  warning: 1
  info: 0
  total: 2
status: issues_found
---

# Phase 35: Code Review Report

**Reviewed:** 2026-07-22T00:00:00Z
**Depth:** standard
**Files Reviewed:** 12
**Status:** issues_found

## Summary

This phase wires the already-built `AVConverter` (Phase 34) into the async pipeline: a new `av` asynq queue/task type, `cmd/av-worker`, API + reconciler routing, a stage-aware terminal classifier (`isAVTerminal`), `AVUniqueTTL`, and video-container source formats added to `AudioConverter`. The wiring itself is unusually thorough and internally consistent — every one of the four routing seams called out in the task brief (API enqueue switch, reconciler routing switch, `AllConvertQueues()`/queue-depth collector, `RetryDelayFunc`) correctly includes an `av` arm, and none of the four pre-existing engine classes lost their queue coverage. `isAVTerminal` correctly implements the D-02 split (transcode timeout transient, audio-extract/thumbnail timeout terminal, deterministic guard/validation errors always terminal) and is proven to genuinely diverge from `isAudioTerminal` by a dedicated contrast test. `AVUniqueTTL` correctly applies the `maxRetry+1` correction and reuses `uniqueTTLSafetyMargin`. The three-way sentinel refactor in `av.go` is complete — no call site still emits the old ambiguous `"av: ffmpeg: %w"` wrapper (confirmed by full-repo grep; the only remaining occurrence is in a test doc comment). The `-map 0:a:0` addition to `ffmpegNormalizeArgs` in `whisper.go` does not disturb the AVE-02 `-nostdin`/`-protocol_whitelist file,crypto` hardening pair, which remains present before `-i` on every ffmpeg/ffprobe call site touched by this phase (verified by grep count and by reading every argv builder). The registration-collision hazard in `converters.go` is genuinely guarded by `TestAVAudioPairDisjointness` (confirmed by reading the test: it checks both pairwise identity and a union-cardinality invariant, so a silent target-format collision would fail it). `go build ./...` and `go vet ./...` are both clean.

Two issues were found. One is a genuine resource-exhaustion risk directly introduced by this phase's change to the global upload ceiling; the other is a pre-existing (Phase 34) design inconsistency inside a file this phase's task brief specifically asked to be re-examined.

## Critical Issues

### CR-01: Raising MAX_UPLOAD_BYTES to 2 GiB turns `ParseMultipartForm`'s memory-buffering behavior into a real DoS amplifier

**File:** `internal/api/handlers.go:93-94`, `cmd/api/main.go:139`
**Issue:**

```go
r.Body = http.MaxBytesReader(w, r.Body, s.maxUploadByte)
if err := r.ParseMultipartForm(s.maxUploadByte); err != nil {
```

`s.maxUploadByte` is passed as the `maxMemory` argument to `http.Request.ParseMultipartForm`. Per the Go stdlib contract (`net/http.Request.ParseMultipartForm` / `mime/multipart.Reader.ReadForm`): *"up to a total of maxMemory bytes of ... file parts are stored in memory, with the remainder stored on disk in temporary files."* Before this phase, `MAX_UPLOAD_BYTES` defaulted to 100 MiB (`cmd/api/main.go`, prior value), so the worst case was ~100 MiB of in-process RAM per concurrent upload. This phase's D-07 change (`cmd/api/main.go:139`) raises the default to `2<<30` (2 GiB) specifically to admit video uploads, and that same value still flows straight into `ParseMultipartForm`'s `maxMemory` parameter unchanged. The practical effect: a single legitimately-sized (spec-compliant, under-ceiling) video upload can now be held **entirely in the API process's heap** rather than spilled to a temp file, and this multiplies linearly with concurrent in-flight requests — a handful of concurrent large-video uploads from internal clients (no adversarial input required, just normal legitimate use of the new video feature) can exhaust the API host's memory and take the whole process down, directly contradicting the project's stated core value ("without risk to stability... of production"). The per-engine ceiling (`s.maxEngineBytes`, D-07's second tier) does not help here: it is only checked *after* `ParseMultipartForm` has already buffered the file.

This is a direct, provable consequence of this phase's own config change (the `maxMemory` argument was already `s.maxUploadByte` before Phase 35, but the amplification only becomes dangerous once that value is raised 20x specifically to admit large video files).

**Fix:** Decouple the `ParseMultipartForm` in-memory budget from the total-body-size ceiling — the total size is already bounded pre-parse by `http.MaxBytesReader`, so `ParseMultipartForm` only needs a small, fixed in-memory allowance (form fields, not file bytes) and should let every file part spill to disk:

```go
// maxMultipartMemory bounds how much of the multipart body ParseMultipartForm
// buffers in RAM before spilling file parts to disk -- deliberately NOT tied
// to s.maxUploadByte (the total-body ceiling, already enforced above via
// http.MaxBytesReader): a 2 GiB video upload must be spooled to a temp file,
// never held resident in the API process's heap.
const maxMultipartMemory = 32 << 20 // 32 MiB

r.Body = http.MaxBytesReader(w, r.Body, s.maxUploadByte)
if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
```

## Warnings

### WR-01: `convertAudioExtract` re-probes the audio codec instead of reusing the guard stage's already-probed value, violating the documented "probed exactly once" invariant

**File:** `internal/convert/av.go:428-485` (`avSourceProbe`/`avProbeSource`), `internal/convert/av.go:397-436` (`Convert`), `internal/convert/av.go:540-557` (`convertAudioExtract`)
**Issue:** `avSourceProbe`'s doc comment (av.go:438-445) states the guard stage is "probed EXACTLY ONCE and threaded through to the conversion stage," specifically to avoid the redundant-subprocess problem WR-05 closed for the transcode and thumbnail paths. `avProbeSource` does probe `audioCodec` once via `probeAudioCodec` (av.go:475) and stores it on `avSourceProbe.audioCodec`. `convertTranscode` correctly reuses it (`avStreamCopyEligible(target, o, src)` reads `src.audioCodec`). However, `Convert`'s dispatch to the audio-extract path does not pass `src` at all:

```go
case "mp3", "wav", "m4a":
    return c.convertAudioExtract(ctx, inPath, outPath, targetFormat)
```

and `convertAudioExtract` independently re-invokes `probeAudioCodec(ctx, inPath)` for every `m4a` target (av.go:543), on the same already-probed input file, to decide the AAC-source stream-copy eligibility — a second, redundant `ffprobe` subprocess on every audio-extract-to-m4a job. This is the exact class of duplication `avSourceProbe`'s own doc comment says was eliminated. It is also inconsistent with the review focus on sentinel soundness: this second probe's failure path returns the un-sentineled `fmt.Errorf("av: ffprobe: %w", err)` (av.go:545), which — unlike the three ffmpeg call sites this phase gave typed sentinels — falls through `isAVTerminal` to the generic `isTerminal` fallback and classifies transient by default, with no dedicated test coverage for that path's classification.

**Fix:** Thread `src avSourceProbe` through to `convertAudioExtract` (mirroring `convertTranscode`/`convertThumbnail`) and use `src.audioCodec` directly instead of re-probing:

```go
case "mp3", "wav", "m4a":
    return c.convertAudioExtract(ctx, inPath, outPath, targetFormat, src)
...
func (c AVConverter) convertAudioExtract(ctx context.Context, inPath, outPath, target string, src avSourceProbe) error {
    streamCopy := target == "m4a" && src.audioCodec == "aac"
    args := extractAudioArgs(inPath, outPath, target, streamCopy)
    ...
}
```

---

_Reviewed: 2026-07-22T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
