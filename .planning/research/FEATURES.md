# Feature Research

**Domain:** Video/AV conversion engine (fifth engine class: transcode, audio extraction, thumbnail, video→transcript) for an internal async file-conversion service
**Researched:** 2026-07-19
**Confidence:** MEDIUM-HIGH (ffmpeg CLI behavior is well-documented and stable; internal-service scoping judgments are informed synthesis, not verified against a specific competitor spec)

## Feature Landscape

### Table Stakes (Users Expect These)

Features internal callers will assume exist the moment "video" shows up as an engine class — modeled directly on OctoConv's existing engine classes (image/document/html/audio) and on how every general-purpose video conversion API (Cloudinary, Transloadit, Coconut, Mux, AWS MediaConvert) scopes a "convert a video file" feature.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Container/codec transcode: mov/avi/mkv/webm → mp4 (H.264 video + AAC audio) | MP4/H.264/AAC is the universal-compatibility target; every video conversion service treats "give me an mp4 that plays everywhere" as the default ask (Creatomate, RenderIO, FFmpeg-micro guides all converge on this) | MEDIUM | `ffmpeg -i in -c:v libx264 -preset medium -crf 23 -c:a aac -movflags +faststart out.mp4`. `+faststart` (moov atom at front) matters for an S3/presigned-URL delivery model — without it the player must download the whole file before playback starts |
| mp4 → webm (VP9/Opus) as a secondary table-stakes target | Second most requested target after mp4; needed for the "any container in, mp4 or webm out" symmetry pattern already established for images/documents (N formats × M formats via explicit Pair table) | MEDIUM | WebM only accepts VP8/VP9/AV1 video + Vorbis/Opus audio — stream copy is *never* valid mp4→webm (codec mismatch), so this is always full re-encode. More CPU-expensive than mp4 target; factors into RTF-gate timeout sizing |
| Audio extraction: video → mp3/wav/m4a | Directly requested in this milestone's target features; "get me just the audio" is one of the top 3 use cases of every video API (Cloudinary, Transloadit thumbnail/audio docs) | LOW-MEDIUM | `ffmpeg -i in -vn -c:a copy out.m4a` when the source audio codec (AAC) is already container-legal in the target (m4a/mp4-audio) — near-instant, no re-encode. `-vn -c:a libmp3lame -q:a 2 out.mp3` / `-c:a pcm_s16le out.wav` when target format requires re-encode (mp3, wav) — see Anti-Pattern note below |
| Thumbnail/frame extraction: video → jpg/png/webp at a client-specified timecode | Directly requested; every video platform exposes "give me a poster frame at time T" as a first-class, near-zero-cost operation | LOW | `ffmpeg -ss <timecode> -i in -frames:v 1 -q:v 2 out.jpg`. Input-side `-ss` (before `-i`) is fast keyframe-seek — correct default for a thumbnail (frame-perfect accuracy is not a stated requirement here); output-side `-ss` is slower/frame-accurate and should NOT be the default given cost sensitivity |
| Default thumbnail timecode when none supplied | Every thumbnail API needs a sane no-opts default; "10% into the video" or a fixed early offset (e.g. 1s) are the two conventional defaults — an all-black/blank frame 0 is explicitly what services avoid | LOW | Recommend a fixed small offset (e.g. 1.0s, clamped to duration) rather than percentage-of-duration — percentage requires an ffprobe duration call before the ffmpeg call (extra RTT + failure mode); a fixed offset needs no pre-probe and mirrors the "keep it minimal" opts philosophy already used for audio (`AudioOpts` — 2 fields) |
| Video → transcript in one job (txt/srt/vtt/json, word timestamps) | Explicit v1.8 target feature; audio class already delivers this exact output contract (whisper.cpp pipeline, Phase 30-31) — video callers expect format parity, not a diminished subset | MEDIUM-HIGH | Reuses the *existing* two-stage `ffmpeg-normalize → whisper-cli` pipeline (`internal/convert/whisper.go`) unchanged for the whisper half; only the extraction stage differs (video container in, not audio container). This is the single feature in this milestone with a real architectural decision still open (see Feature Dependencies below) |
| Content-type/magic-byte validation of video containers before storage write | Every existing engine class (image/document/html/audio) fail-closed validates by magic bytes before S3 write (`internal/convert/sniff.go`, `audiosniff.go`, `docsniff.go`) — the pattern is load-bearing project convention, not optional | MEDIUM | mp4/mov share the ISO-BMFF `ftyp` box signature (need brand-string disambiguation, same family as existing m4a/mp4 audio brand logic in `audiosniff.go`); mkv/webm share the EBML header (`0x1A45DFA3`) and need `DocType` (`matroska` vs `webm`) disambiguation; avi is RIFF+`AVI ` (same RIFF family as existing wav detection — reuse the RIFF-parsing precedent) |
| Duration guard before expensive stage (ffprobe-based) | Directly reuses the exact `audioduration.go` precedent (NaN/Inf/negative/overflow-hardened ffprobe parse) that already exists and is proven; video duration is the same risk surface (a malicious/malformed file lying about a 100-hour duration must not be silently accepted) | LOW (reuse) | `ffprobeDurationArgs` pattern generalizes directly — same binary (`ffprobe`), same parse hardening, same fail-closed posture. This is near-zero new design cost, only a new `MAX_VIDEO_DURATION_SECONDS`-style config knob |
| RTF-measured `AV_ENGINE_TIMEOUT` (or per-operation timeout) | Milestone context explicitly commits to "своим RTF-гейтом по образцу аудио" — this is a decided constraint, not a discovery | MEDIUM-HIGH | Video transcode does NOT have a single clean "real-time factor" the way audio-to-text does (whisper's RTF is speech-length-bound); video encode speed instead scales with **resolution × bitrate × preset**, not just duration. Table-stakes here is *measuring* an empirical worst-case (largest supported resolution, slowest allowed preset, longest allowed duration) exactly as Phase 32 measured audio RTF empirically rather than assuming a number |
| Separate `av` queue / `cmd/av-worker` / own container / own retry schedule | Directly requested; identical shape to the audio-engine and document-engine precedents already shipped (Phase 10, Phase 31) | LOW (reuse of established pattern) | ffmpeg binary in its own Dockerfile stage (mirrors `Dockerfile.audio-worker`'s ffmpeg+whisper.cpp multi-stage build) — resource isolation matches existing "heavy binary gets its own container" rule (LibreOffice, chromium, whisper.cpp all isolated this way) |

### Differentiators (Competitive Advantage)

Not required for MVP of this milestone, genuinely valued by internal callers, and cheap enough to be worth flagging as fast-follow candidates rather than deferring indefinitely.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| H.265/HEVC output target | ~30-50% smaller files than H.264 at equal quality — meaningful for an internal service storing/moving large video assets through S3 | MEDIUM | `-c:v libx265 -crf 28 -preset medium` (x265's default CRF differs from x264's 23 — needs its own constant, not a copy-paste). Licensing/patent concerns are irrelevant for an internal-only, non-distributed service, removing the usual blocker large SaaS video APIs face |
| Auto stream-copy fast path (skip re-encode when source codec/container pair is already target-legal) | Turns a multi-minute re-encode into a few-second remux for the common case (e.g. mp4 H.264→mp4 H.264 container-only fix, or extracting AAC audio that's already AAC) — big win for the RTF-gate/timeout budget and CPU cost | MEDIUM | Requires an ffprobe pre-check of source codec before deciding the ffmpeg argv (copy vs re-encode) — same "probe before expensive stage" shape as the duration guard, just branching on codec name instead of duration value. Natural v1.8-or-fast-follow scope, not core MVP: audio-extraction table-stakes entry above only requires *considering* copy for m4a; generalizing the decision to all three audio targets and to transcode is where the differentiator value lives |
| Resolution-capping opt (e.g. `max_height: 720`) | Common, low-risk client ask — "give me a smaller file for archival/preview" — without exposing raw ffmpeg scale-filter strings | LOW-MEDIUM | Closed enum of allowed heights (e.g. 480/720/1080), same `checkStrictObject`+closed-allowlist pattern as `AudioOpts.Language` — never accept an arbitrary WxH string from the client (injection/DoS surface: a client-chosen absurd upscale target could blow the timeout budget) |

### Anti-Features (Commonly Requested, Often Problematic)

Features that look like natural extensions of "video conversion" but would turn OctoConv from a file-conversion service into a video-hosting/streaming/editing platform — well outside its stated Core Value and its internal-only, no-multi-tenancy, no-billing constraints.

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|------------------|-------------|
| Adaptive bitrate streaming output (HLS/DASH manifests + multi-rendition segments) | "We already convert video, so give us a player-ready stream" is the natural next ask once transcode exists | This is categorically different work: N renditions × segmenting × manifest generation × (usually) a CDN/origin in front of the segments. It turns a single async job into a multi-output packaging pipeline and pulls in a whole new consumer (a video player), which the internal-services client base has not asked for. It would also break the project's single-input/single-output-per-job assumption still hard-coded in `internal/worker/worker.go` and `internal/api` (ordinal 0 only) | If a caller genuinely needs adaptive streaming, that belongs to a dedicated streaming platform (or a future explicit milestone), not bolted onto a generic file-conversion job. OctoConv keeps producing single deliverable files; a caller can pipeline multiple OctoConv jobs (different resolutions) themselves if they need renditions |
| In-service video editing (trim, crop, concat, filters, overlays, watermarking) | Once ffmpeg is in the stack, "can you also cut the first 5 seconds" or "burn our logo in the corner" feels like a tiny incremental ask | ffmpeg *can* do all of this, which is exactly the trap — each one is a new client-controlled opt surface that must be validated as strictly as `AudioOpts`/`DocOpts` (closed allowlists, no raw filter strings ever reaching argv — a raw `-vf` string from a client is a direct command-injection-adjacent risk even without a shell, since arbitrary filter graphs can be used to exhaust CPU/memory). It also has no natural stopping point — "just one more filter opt" is scope creep with no ceiling | Keep this milestone to the four stated operations (transcode, audio extraction, thumbnail, video→transcript). If trim/crop is later validated as real demand, scope it as its own opts-allowlist feature (e.g. a validated `start`/`end` timecode pair, mirrors the thumbnail timecode pattern) rather than open-ended editing |
| Watermarking / branding overlay | Reasonable-sounding "add our logo" ask from a company that has many internal services | Same class of risk as editing above (filter-graph injection surface) plus it couples a generic conversion service to a specific brand/asset dependency (where does the watermark image live? per-client? versioned?) that has nothing to do with "convert format A to format B" | If needed, it is a product for a specific consuming service to build on top of OctoConv's plain transcode output, not a feature of the conversion service itself |
| Full-resolution/bitrate free-form opts (arbitrary `crf`, `bitrate`, `preset`, `resolution` strings from the client) | Power users always want fine-grained control once they see ffmpeg is under the hood | Directly contradicts the project's established "closed allowlist, never raw client bytes into engine argv" discipline (`OPTS-01/02` precedent, `AudioOpts` comment: "Never accept an arbitrary client string here"). An open bitrate/CRF number field is also a timeout/DoS surface — a client requesting CRF 0 (near-lossless) at max resolution could blow any RTF-style timeout budget | Keep opts minimal exactly as audio did: a target-format selection plus, at most, a small closed enum (e.g. resolution cap from a fixed list) — "keep it minimal" per the milestone's own framing, not a general-purpose ffmpeg passthrough |
| Live/real-time transcoding, RTMP ingest, streaming endpoints | Sounds like a natural "video" feature once ffmpeg is available | OctoConv's entire architecture (async job, S3-in/S3-out, asynq queue, presigned download) is a batch/file model; live streaming needs long-lived stateful connections and a completely different transport, which is a different product | Out of scope permanently, not just for this milestone — flag explicitly if ever requested |
| Multi-input video jobs (concat multiple uploads into one output) | Natural extension once "video" implies "editing-adjacent" use cases | The schema supports multiple `job_inputs`/`job_outputs` via `ordinal`, but no code path populates/consumes more than one today (documented architectural constraint) — enabling this for video specifically, first, would be an inconsistent precedent across engine classes | Stays out of scope for all engine classes uniformly until there's a cross-cutting reason to lift the single-input/output assumption, not introduced as a video-specific special case |

## Feature Dependencies

```
Duration guard (ffprobe, reused from audioduration.go)
    └──requires──> ffprobe binary in av-worker image (same install as ffmpeg package)

RTF-measured AV_ENGINE_TIMEOUT
    └──requires──> Duration guard (need a bounded worst-case duration to measure against)
    └──requires──> A representative "worst case" transcode profile (resolution/codec/preset choice)
                       (mirrors Phase 32: must MEASURE empirically, not assume, before shipping)

Video → transcript (one job)
    └──requires──> Audio extraction stage (video → intermediate audio, ffmpeg)
    └──requires──> Existing whisper.cpp pipeline (internal/convert/whisper.go, UNCHANGED)
    └──requires──> Fail-closed video-container sniffing (must validate video input before
                    the extraction stage runs, same "sniff before storage write" pattern audio uses)
    ⚠ Open architectural question (per milestone context, explicitly deferred to planning):
      "whisper INSIDE the av-worker container" vs "av-worker extracts audio → re-enqueues
      onto the EXISTING audio queue for whisper" — these are mutually exclusive
      implementation paths with different container/dependency footprints:
        - In-container: av-worker image bundles ffmpeg AND whisper.cpp (duplicates the
          audio-worker's whisper.cpp build stage; larger image; no cross-queue hop)
        - Cross-queue: av-worker only needs ffmpeg; extracted audio is handed to the
          audio engine via an internal job chain (adds a second job's worth of queue
          latency and status-tracking complexity, but keeps images single-purpose and
          reuses the exact whisper stage — zero duplicated code)
      This is a genuine differentiator-affecting decision (image size / KEDA scale
      profile / stage-aware-retry semantics all differ) that belongs in
      architecture/roadmap planning, not resolved here.

Auto stream-copy fast path (differentiator)
    └──enhances──> Transcode (table stakes) and Audio extraction (table stakes)
    └──requires──> ffprobe codec inspection (new probe call, distinct from the duration probe
                    but same "probe before expensive stage" shape)

Resolution-capping opt (differentiator)
    └──enhances──> Transcode (table stakes)
    └──requires──> Closed allowlist validation (opts.go pattern, mirrors AudioOpts.Language)

Thumbnail extraction (table stakes)
    ──conflicts with──> percentage-of-duration default timecode
      (percentage default would require an extra ffprobe duration call on the hot path
      for the simplest, cheapest operation in the whole engine class — a fixed-offset
      default avoids this; only becomes relevant if a client explicitly requests
      percentage-based timecode as an opt, which should NOT be table stakes)
```

### Dependency Notes

- **RTF-measured timeout requires the duration guard first:** you cannot bound worst-case encode time without first bounding worst-case input duration — same ordering the audio milestone already proved out (duration guard existed before the RTF measurement phase, Phase 30 vs Phase 32).
- **Video→transcript requires an explicit architecture decision before implementation can start** (in-container whisper vs cross-queue chaining to the existing audio engine). This is the one feature in the milestone that cannot be scoped by "reuse the existing pattern verbatim" — flag for roadmap/architecture research, not deferred silently.
- **Auto stream-copy conflicts with simplicity, not correctness:** it is deliberately scoped as a differentiator (fast-follow) rather than table stakes so that the MVP transcode path has exactly one code path (always re-encode) to validate and harden first, matching the project's incremental-hardening-per-phase history (every prior engine class shipped a plain path before an optimization path).
- **Anti-features (editing/watermarking/HLS) conflict with the closed-opts-allowlist discipline** that is a hard project convention (`OPTS-01/02`), not a video-specific choice — any opt design for this milestone must pass the same "closed enum only, never raw client bytes into argv" bar `AudioOpts` already set.

## MVP Definition

### Launch With (v1.8)

Matches the milestone's own "Target features" list exactly — no more, no less.

- [ ] Transcode: mov/avi/mkv/webm → mp4 (H.264/AAC, `+faststart`) — the highest-value, most-expected operation; needs its own RTF-measured timeout
- [ ] mp4 → webm (VP9/Opus) as the secondary transcode target, for format-pair symmetry with the rest of the engine registry
- [ ] Audio extraction: video → mp3/wav/m4a (stream-copy to m4a when source is already AAC; re-encode for mp3/wav)
- [ ] Thumbnail: frame → jpg/png/webp at a client-specified or fixed-default timecode (fast input-side `-ss` seek)
- [ ] Video → transcript: mp4/mov → txt/srt/vtt/json (reuses existing whisper.cpp output contract verbatim — implementation path TBD in architecture research)
- [ ] `cmd/av-worker`, `av` asynq queue, fail-closed video-container magic-byte validation, stage-aware transient/terminal retry, ffprobe-based duration guard, compose + chart + KEDA ScaledObject — full production-parity wiring matching every prior engine class

### Add After Validation (v1.8.x or v1.9)

- [ ] H.265/HEVC output target — add once mp4/H.264 baseline is proven and there's a concrete storage-size pressure signal
- [ ] Auto stream-copy fast path for transcode (not just audio-extraction-to-m4a) — add once the plain re-encode path's timeout/cost profile is measured and a clear win is quantifiable
- [ ] Resolution-capping opt — add if/when a real internal caller asks for smaller output files, following the same "extend allowlist on real demand" discipline already used for `audioLanguageAllowlist`

### Future Consideration (v2+, or never)

- [ ] Adaptive bitrate streaming (HLS/DASH) — fundamentally different product shape (multi-rendition, manifest, likely CDN); revisit only if an internal service explicitly needs player-ready streaming, as its own milestone
- [ ] Video editing primitives (trim/crop/concat/filters) — explicitly deferred; if ever pursued, scope each op as its own validated closed-opts feature, never a raw filter passthrough
- [ ] Watermarking/branding overlay — belongs to a consuming service layered on top of OctoConv output, not to OctoConv itself
- [ ] Live/streaming ingest (RTMP etc.) — incompatible with the async batch S3-in/S3-out architecture; not a "later phase" of this milestone, a different product

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|----------------------|----------|
| Transcode → mp4 (H.264/AAC) | HIGH | MEDIUM | P1 |
| Transcode → webm (VP9/Opus) | MEDIUM | MEDIUM | P1 |
| Audio extraction (mp3/wav/m4a) | HIGH | LOW-MEDIUM | P1 |
| Thumbnail extraction | HIGH | LOW | P1 |
| Video → transcript | HIGH | MEDIUM-HIGH | P1 |
| RTF-measured timeout + duration guard | HIGH (safety-critical) | MEDIUM-HIGH | P1 |
| av-worker/queue/chart/KEDA wiring | HIGH (production-parity requirement) | LOW (reuse of established pattern) | P1 |
| H.265/HEVC target | MEDIUM | MEDIUM | P2 |
| Auto stream-copy fast path (general) | MEDIUM (cost/speed win) | MEDIUM | P2 |
| Resolution-capping opt | LOW-MEDIUM | LOW-MEDIUM | P2 |
| HLS/DASH adaptive streaming | LOW (no stated internal demand) | HIGH | P3 / reject |
| Video editing (trim/crop/filters) | LOW (no stated internal demand) | HIGH (open-ended) | P3 / reject |
| Watermarking | LOW (no stated internal demand) | MEDIUM | P3 / reject |

## Competitor Feature Analysis

Not directly applicable in the usual sense (OctoConv has no external competitors — it is an internal platform service), but the "what do general-purpose video conversion APIs treat as their baseline feature set" comparison is useful to calibrate table-stakes scope, since Cloudinary/Transloadit/Coconut-class services define the de facto industry baseline for "video conversion API":

| Feature | Cloudinary / Transloadit (typical) | Our Approach |
|---------|-------------------------------------|--------------|
| Format/codec transcode | Broad matrix of containers/codecs, quality presets exposed as named transformation params | Narrower, explicit `(source,target)` Pair table exactly like every other OctoConv engine class — mov/avi/mkv/webm → mp4/webm, not an open matrix |
| Thumbnail at timecode | `start_offset` param, arbitrary timestamp, chainable with image transformations | Timecode as a validated opt (closed to a numeric range, not a filter chain); no post-thumbnail image transformation chaining — that's the existing image engine's job if ever composed, not this engine's |
| Audio extraction | Offered as a transformation flag (`f_mp3` etc.) alongside video, same asset pipeline | Standalone (source,target) pairs video→mp3/wav/m4a in the same converter registry pattern as everything else |
| Adaptive streaming (HLS/DASH) | Core offering for these platforms — that's their primary business | Explicit anti-feature for OctoConv (see above) — internal file-conversion service, not a video hosting/streaming platform |
| Transcript from video | Not a native feature of Cloudinary/Transloadit (video AI/transcription is usually a separate add-on product for them) | A genuine differentiator for OctoConv specifically, because the whisper.cpp pipeline already exists in-house (audio engine) — cheap to extend, not cheap for a pure video-transform SaaS to add |

## Sources

- [FFmpeg Micro Blog — Convert Video Format (MP4/WebM/MKV/AVI)](https://www.ffmpeg-micro.com/blog/ffmpeg-convert-video-format) — MEDIUM confidence (WebSearch, single-source blog, but claims consistent with FFmpeg's own documented codec/container support and cross-checked against multiple other sources below)
- [RenderIO Blog — FFmpeg Transcode Video: Complete Codec Conversion Guide](https://renderio.dev/blogs/ffmpeg-transcode-video/) — MEDIUM confidence
- [RenderIO Blog — FFmpeg Formats: Containers, Codecs & File Types](https://renderio.dev/blogs/ffmpeg-formats/) — MEDIUM confidence, container/codec compatibility matrix (MP4 vs WebM legal codec sets) cross-checked against FFmpeg's own muxer documentation conventions
- [Creatomate — How to Convert a Video File to a Different Format using FFmpeg](https://creatomate.com/blog/how-to-convert-a-video-file-to-a-different-format-using-ffmpeg) — MEDIUM confidence
- [FFmpeg Cookbook — `-c copy` vs Re-encode](https://ffmpeg-cookbook.com/en/articles/ffmpeg-copy-codec-vs-reencode/) — MEDIUM confidence, stream-copy vs re-encode tradeoffs (used for audio-extraction default reasoning) consistent with general ffmpeg documentation
- [Sebastian Aigner — Generating thumbnails 3.8x faster by using ffmpeg seeking instead of fps filtering](https://sebi.io/posts/2024-12-21-faster-thumbnail-generation-with-ffmpeg-seeking/) — MEDIUM-HIGH confidence, specific `-ss` before/after `-i` speed-vs-accuracy tradeoff is well-established, widely corroborated ffmpeg behavior
- [FFmpeg Micro Blog — Pick the Right FFmpeg CRF: libx264 + libx265 Reference](https://www.ffmpeg-micro.com/blog/ffmpeg-crf-explained) — MEDIUM confidence, CRF 23/preset medium as x264 default is a well-known, stable FFmpeg documented default (HIGH confidence on the "23 is x264's own default" fact specifically, since that is FFmpeg project documentation, not a blog claim)
- [Mux — HLS vs. DASH: What's The Difference?](https://www.mux.com/articles/hls-vs-dash-what-s-the-difference-between-the-video-streaming-protocols) — MEDIUM confidence, used only to characterize adaptive streaming's added scope (multi-rendition + manifest + packaging) for the anti-feature rationale
- [Transloadit — Video Encoding API & Transcoding Service](https://transloadit.com/services/video-encoding/) — MEDIUM confidence, general-purpose video API baseline feature comparison
- [Cloudinary — Video Transformations documentation](https://cloudinary.com/documentation/video_manipulation_and_delivery) — MEDIUM confidence, thumbnail `start_offset` pattern
- Existing codebase precedent (HIGH confidence, direct read): `internal/convert/audioopts.go`, `internal/convert/whisper.go`, `internal/convert/audioduration.go`, `internal/convert/convert.go`, `.planning/PROJECT.md` — used for all "reuse existing pattern" claims (RTF-gate, closed-opts-allowlist discipline, stage-aware retry, engine-class queue routing, sniff-before-storage convention)

---
*Feature research for: Video/AV conversion engine class (ffmpeg) — OctoConv v1.8 milestone*
*Researched: 2026-07-19*
