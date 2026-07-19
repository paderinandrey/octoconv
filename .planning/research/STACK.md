# Stack Research

**Domain:** Offline video-processing engine class (ffmpeg) for OctoConv v1.8 — transcode, audio extraction, thumbnail extraction, video→transcript
**Researched:** 2026-07-19
**Confidence:** HIGH for packaging/codec-availability facts (verified live inside a real `debian:bookworm-slim` container in this session — `ffmpeg -version`, `-encoders`, `-hwaccels`, `-muxers`, `apt-cache policy`, install-size dry run); MEDIUM for CPU-only encode-speed sizing (no first-party benchmark run yet — this milestone should reuse the RTF-gate *methodology*, not borrow audio's numbers)

This is a **replacement** stack note for OctoConv's v1.8 milestone. It supersedes the previous milestone's STACK.md content (v1.7 — whisper.cpp/audio engine, already shipped and merged, not revisited here except where the audio engine's existing `ffmpeg` usage is directly reused). Go/chi/asynq/Postgres/MinIO/Helm/KEDA/whisper.cpp are already validated (see PROJECT.md) and are not re-researched here. This note covers only the NEW av/video engine class (`cmd/av-worker`, `Dockerfile.av-worker`, `queue.TypeAVConvert`/`QueueAV` or equivalent).

**Key existing fact this note builds on:** the audio engine (Phase 30-33) already shells out to `ffmpeg` for its normalize stage (`internal/convert/whisper.go:ffmpegNormalizeArgs`, `Dockerfile.audio-worker`) and to `ffprobe` for its duration guard (`internal/convert/audioduration.go`). The new av/video engine class is not introducing ffmpeg to the codebase — it is giving ffmpeg its own dedicated engine class, container, and much larger surface of ffmpeg functionality (encode, not just decode-to-WAV).

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `ffmpeg` (apt, `debian:bookworm-slim`) | **7:5.1.9-0+deb12u1** (live-verified in this session, 2026-07-19, matches the exact pin already recorded in `.planning/research` for v1.7's audio engine — same package, same base image) | Video transcode, audio-track extraction, single-frame thumbnail extraction, and the ffmpeg half of video→transcript | Same binary already vetted and running in production (`Dockerfile.audio-worker`). Debian's `ffmpeg` build is **not** a bare-bones build: live `ffmpeg -version` inside `debian:bookworm-slim` shows `--enable-gpl --enable-libx264 --enable-libx265 --enable-libvpx --enable-libwebp --enable-libmp3lame --enable-libopus --enable-libvorbis --enable-libxvid` etc. — everything the four target features need ships in the *same* `apt-get install ffmpeg` this project already runs, zero extra apt packages, zero non-free/contrib components. |
| `ffprobe` (same apt package as `ffmpeg`, no separate install) | 5.1.9-0+deb12u1 | Container/stream validation before any encode/decode: confirm a real video stream exists, read `duration`, `codec_name`, `width`/`height`, `nb_streams` before committing to an expensive transcode/whisper job | `dpkg -L ffmpeg` (live-verified) shows `ffprobe` ships in the *same* Debian package as `ffmpeg` — `apt-get install ffmpeg` is sufficient, there is no `ffprobe`-only package to separately track. This is a direct extension of the pattern already proven in `internal/convert/audioduration.go` (`ProbeDuration`/`EnforceMaxDuration`, `runCommand(ctx, "ffprobe", ...)`) — that file's shape (short bounded ctx distinct from the full engine timeout, `-of default=noprint_wrappers=1:nokey=1` machine-parseable output, float-space NaN/Inf/negative/overflow validation) should be extended for video's richer stream inspection (`-show_streams -select_streams v:0 -show_entries stream=codec_name,width,height,duration`), not reinvented. |
| `libx264` (statically linked into ffmpeg's `libx264` encoder, via `--enable-gpl --enable-libx264`) | whatever version ffmpeg 5.1.9's Debian build links against (not independently apt-pinned; re-verify with `ffmpeg -h encoder=libx264` / `ffmpeg -version` at build time, same discipline the audio STACK.md already used for ffmpeg itself) | H.264/AVC encoding — the target codec for the "mov/avi/mkv/webm → mp4 H.264" transcode feature | Confirmed live: Debian's ffmpeg is built with `--enable-gpl --enable-libx264`, so `libx264` is available with zero extra packaging. `mp4`/`mov`/`ipod`/`psp` muxers are all present (`ffmpeg -muxers`, live-verified) for the mp4 container output. |
| Native `aac` encoder (built into ffmpeg core, no `--enable-libfdk-aac`) | ships with ffmpeg 5.1.9 | AAC audio encoding for the mp4 transcode's audio track and for the `video → m4a` audio-extraction feature | Debian's ffmpeg does **not** and legally **cannot** ship `libfdk-aac` (Fraunhofer's license is GPL-incompatible/non-free, so Debian's `main` archive excludes it entirely — confirmed: it is absent from the live `-encoders` list). The **native** `aac` encoder (`A..... aac`, live-verified present) has been "good enough" quality for years and requires zero extra packaging or non-free apt components — exactly matching this project's existing zero-third-party-repo discipline. |

### Supporting Libraries (already present in the `ffmpeg` apt package, zero extra install)

| Library | Purpose | When to Use |
|---------|---------|-------------|
| `libmp3lame` (`--enable-libmp3lame`, live-verified) | MP3 encoding | `video → mp3` audio extraction feature |
| `libvpx` / `libvpx-vp9` (`--enable-libvpx`, live-verified: `libvpx` VP8 and `libvpx-vp9` VP9 encoders both present) | VP8/VP9 encoding | `... → webm` transcode target (webm muxer confirmed present) |
| `libwebp` / `libwebp_anim` (`--enable-libwebp`, live-verified) | WebP still-image encoding | Thumbnail-extraction feature's `jpg/png/webp` targets — `webp` needs no extra tooling, ffmpeg's own `libwebp` encoder handles it directly (no separate shell-out to `cwebp`/libvips for this path) |
| `mjpeg`/`png` internal codecs (ffmpeg built-ins, no `lib*` dependency) | JPEG/PNG still-image encoding | Thumbnail-extraction feature's `jpg`/`png` targets |
| `libx265` (`--enable-libx265`, live-verified present but not requested by any v1.8 target feature) | HEVC encoding | Not needed for the four listed v1.8 features (target is explicitly "mp4 H.264 etc.") — noted here only because it ships for free in the same package, so it is a trivial future `Pair` addition (e.g. an `hevc` target) with zero new packaging if a later milestone wants it. Do not wire it up speculatively now. |

No new **Go** dependencies are needed. This engine class follows the exact same shape as libvips/LibreOffice/chromium/whisper.cpp: a `Converter` implementation in `internal/convert/` that shells out via the existing `runCommand` hardened-exec helper (`internal/convert/exec.go`) — process-group `Setpgid` + SIGKILL-on-timeout, unchanged. Do not add a Go ffmpeg binding (see "What NOT to Use").

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| `ffmpeg -h encoder=libx264` / `ffmpeg -h encoder=libx265` / `ffmpeg -h muxer=mp4` | Enumerate exact option names/defaults for the pinned Debian ffmpeg build before writing `internal/convert/ffmpeg.go`'s argv-builder functions | Live-verified in this session: `-preset` defaults to `"medium"`, `-crf` defaults to `-1` (unset — CRF mode is opt-in, must be set explicitly), full x264 option surface confirmed against the exact 5.1.9 binary rather than trusted from generic online docs, mirroring the audio STACK.md's own "re-run `-h` against the exact pinned binary" discipline. |
| `ffprobe -show_streams -show_format -print_format json` | Reference for the exact JSON shape the worker-side stream/container validator should parse | Machine-parseable JSON output (vs. the `default=noprint_wrappers=1:nokey=1` plain format `audioduration.go` currently uses for the single-scalar duration case) — recommended for video because the validator needs multiple fields at once (has-video-stream, codec_name, width, height), not a single scalar. |
| `apt-cache policy ffmpeg`, `dpkg -L ffmpeg` (against a real `debian:bookworm-slim` container) | Re-verify package contents/version before writing the Dockerfile | Live-verified in this session: single `ffmpeg` apt package provides `/usr/bin/ffmpeg`, `/usr/bin/ffprobe`, `/usr/bin/ffplay`, `/usr/bin/qt-faststart` — no separate `ffprobe` package exists to omit or forget. |

## Installation

```dockerfile
# Dockerfile.av-worker — mirrors Dockerfile.audio-worker's shape, but is
# SIMPLER: no from-source build stage is needed (unlike whisper.cpp, ffmpeg
# has a first-party Debian apt package with active security tracking).

# --- Go build stage ---
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/av-worker ./cmd/av-worker

# --- runtime stage ---
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      ffmpeg \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/av-worker /usr/local/bin/av-worker
# Single synchronous CLI invocation per job (ffmpeg for transcode/extract/
# thumbnail; ffmpeg-then-cross-queue for video->transcript -- see "Stack
# Patterns by Variant" below) -- no forking daemon, matching Dockerfile.worker/
# Dockerfile.audio-worker's own documented rationale for skipping tini/init.
USER nobody
ENTRYPOINT ["/usr/local/bin/av-worker"]
```

```bash
# Example runCommand-shaped invocations the worker performs per job (all
# bounded by the class's own AV_ENGINE_TIMEOUT, once RTF-measured per
# feature -- see "What Needs Measurement" below):

# 1. Transcode (mov/avi/mkv/webm -> mp4 H.264 + AAC)
ffmpeg -y -i file:input.mov \
  -c:v libx264 -preset veryfast -crf 23 \
  -c:a aac -b:a 128k \
  -movflags +faststart \
  output.mp4

# 2. Audio extraction (video -> mp3/wav/m4a) -- same shape as whisper.go's
#    existing ffmpegNormalizeArgs, different output codec per target
ffmpeg -y -i file:input.mp4 -vn -c:a libmp3lame -q:a 2 output.mp3

# 3. Thumbnail extraction (frame at timecode -> jpg/png/webp)
ffmpeg -y -ss 00:00:05 -i file:input.mp4 -frames:v 1 -q:v 2 thumb.jpg

# 4. ffprobe stream/container validation (worker-side, BEFORE any of the above)
ffprobe -v error -select_streams v:0 \
  -show_entries stream=codec_name,width,height \
  -show_entries format=duration \
  -of json file:input.mp4
```

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|--------------------------|
| `apt-get install ffmpeg` on `debian:bookworm-slim` (Debian's own package, 5.1.9) | Static ffmpeg builds (johnvansickle.com, BtbN/FFmpeg-Builds GitHub Actions releases — currently ffmpeg 7.x/8.x upstream) | Only if a specific codec/filter genuinely missing from Debian's 5.1.x build blocks a real feature. Rejected as the default here: static builds get **no** Debian security-tracker coverage (Debian actively backports CVE fixes into `5.1.x-0+deb12uN` — live-verified this session: `5.1.6` fixed CVE-2024-7055/7272, `5.1.7` fixed nine more CVEs including 2025-7700/2025-22919) and would require the project to self-track ffmpeg upstream security advisories forever, breaking the "rebuild the image, get the patch" model every other worker container already relies on. |
| `apt-get install ffmpeg` (official Debian archive) | `deb-multimedia.org` third-party apt repo (offers a newer ffmpeg, ~6.1.2, for bookworm) | Only if a newer-than-5.1.x feature is a hard blocker. Rejected as default: adds an unofficial, unvetted third-party apt source with its own trust/signing model — a real departure from every other Dockerfile in this repo, which installs exclusively from Debian's own archive or fetches artifacts with an explicit pinned SHA-256 (whisper.cpp model, veraPDF jar, MinIO release tag). |
| Native ffmpeg `aac` encoder | `libfdk-aac` | Never, on this stack: Debian's `main` archive cannot legally ship `libfdk-aac` (Fraunhofer's non-free license), so using it would require adding Debian's `non-free`/`non-free-firmware` apt components — a licensing-posture change with no internal-tooling justification. Native `aac` is adequate for internal-service audio-extraction use (not audiophile mastering). |
| `libx264` (H.264, default target per the milestone's own "mp4 H.264" wording) | `libx265` (HEVC) | Ships for free in the same apt package (verified present) — reasonable to expose as an additional target `Pair` later if a client needs smaller files at higher CPU cost, but do not default to it: HEVC has materially worse playback compatibility outside modern browsers/mobile, and slower CPU-only encode time, for an internal-tooling use case that doesn't need it yet. |
| Single-pass CRF-mode `libx264` encoding | Two-pass encoding (`-pass 1`/`-pass 2`, `-passlogfile`) | Only if a target bitrate ceiling (not quality) is a hard product requirement. Rejected as default: two-pass requires a full extra decode+analyze pass before the real encode, roughly doubling wall-clock time and making a single `AV_ENGINE_TIMEOUT` budget much harder to reason about — the same "keep it a single bounded pass" reasoning that led the audio engine to greedy decoding (`-bs 1 -bo 1`) over beam search. |
| Input seeking (`-ss` **before** `-i`) for thumbnail extraction | Output seeking (`-ss` **after** `-i`) | Output seeking is more universally frame-exact but decodes every frame from file start to the target timecode — on a long video that is minutes of wasted decode for one still frame. Input seeking is fast (seeks near the target keyframe) and, in modern ffmpeg, still decodes forward to the exact requested frame when followed by `-frames:v 1`, so it is both fast and accurate enough for a thumbnail feature — reserve output seeking only if a future feature needs frame-exact alignment to e.g. subtitle cues. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|--------------|
| Hardware/GPU encoders — `h264_nvenc`, `hevc_nvenc`, `h264_vaapi`, `hevc_vaapi`, `vp8_vaapi`, `vp9_vaapi`, `mjpeg_vaapi`, `*_v4l2m2m` | **Compiled into** the very same Debian ffmpeg binary this project will use (live-verified: all of these appear in `ffmpeg -encoders` and `vdpau`/`cuda`/`vaapi`/`opencl`/`vulkan` all appear in `ffmpeg -hwaccels`) — but the deployment target (docker-compose today, k8s/KEDA per `PROJECT.md`) has **no GPU device passthrough anywhere in the stack**, matching every other worker's explicit CPU-only resource-limit pattern (`cpus: "2.0"` in compose). Selecting any of these encoder names on a container with no `/dev/dri`/NVIDIA runtime fails hard at encode time. | Software encoders only: `libx264`/`libx265`/`libvpx`/`libvpx-vp9`/native `aac`/`libmp3lame`/`libwebp`. If a future `VideoOpts` allowlist (mirroring `AudioOpts`/`DocOpts`/`HTMLOpts`'s closed-allowlist convention, OPTS-01/02) ever exposes a codec-name field to clients, it must hard-exclude every `*_nvenc`/`*_vaapi`/`*_v4l2m2m`/`*_qsv` name — never pass a client-influenced codec string straight into ffmpeg's `-c:v` argv unchecked. |
| Cloud/external transcoding APIs (AWS Elemental MediaConvert, Google Cloud Transcoder API, Mux, Cloudinary, Coconut.co, etc.) | Directly violates the milestone's own stated constraint ("воркеры остаются офлайн — ffmpeg локальный, без внешних API") and the pattern every prior engine class has followed (libvips/LibreOffice/chromium/whisper.cpp are all fully offline, no runtime network calls). Would also add outbound network egress, per-vendor credentials/secrets, and per-request billing to a service whose whole value proposition is "internal, self-hosted, no external dependency." | Local `ffmpeg`/`ffprobe` CLI via the existing hardened `runCommand` exec pattern — zero network calls, zero new secrets. |
| Static/self-built ffmpeg binaries (any source) | See "Alternatives Considered" — no Debian security-tracker coverage, manual CVE-tracking burden the project doesn't currently carry for any apt-packaged tool. | Debian's own `apt-get install ffmpeg` (5.1.9-0+deb12u1) — same discipline as `ca-certificates`, and matches `Dockerfile.audio-worker`'s existing choice for ffmpeg specifically. |
| `libfdk-aac` / any Debian `non-free`/`non-free-firmware` apt component | Requires changing this project's apt sources to include Debian's non-free archive component — a licensing-posture change (Fraunhofer's proprietary-ish AAC license terms) with no feature need, since the native encoder already covers the target formats. | Native ffmpeg `aac` encoder (already present, zero extra packaging). |
| Two-pass / lookahead-heavy `libx264` tunings for the default transcode path | Doubles wall-clock time (full extra analysis pass) for a background batch job that needs a predictable, timeout-boundable single attempt — same reasoning that led the audio engine to greedy whisper-cli decoding over beam search for predictability. | Single-pass CRF mode (`-crf 23` baseline, tune per RTF-style measurement — see below). |
| Go ffmpeg bindings/wrappers (e.g. community CGo bindings around `libavcodec`/`libavformat`) | Would introduce CGo into a codebase explicitly built `CGO_ENABLED=0` everywhere (`Dockerfile.api:7`, `Dockerfile.worker:7`, `Dockerfile.audio-worker`, etc.) for one engine class only, and re-couples the Go binary in-process to a native library the whole `Converter`/CLI-shell-out architecture was designed to keep at arm's length (killable, sandboxable, process-group-isolated). | Plain CLI shell-out via `internal/convert/exec.go`'s existing `runCommand` — zero new build modes, consistent with every other engine class including the ffmpeg calls the audio engine already makes. |

## Stack Patterns by Variant

**If the target feature is video transcode (mov/avi/mkv/webm → mp4 H.264):**
- Use `libx264` (video) + native `aac` (audio) + `-movflags +faststart` (mp4 container, progressive-download-friendly) + a single-pass CRF preset.
- Because this is the exact codec combination the milestone spec names ("mp4 H.264 и т.п."), all three ship for free in the pinned Debian ffmpeg package, and CRF mode gives predictable, tunable quality/size without the two-pass timing cost.
- This is the **most expensive** operation in the new engine class (full video decode + re-encode of every frame) — it is the one that needs its own RTF-style measured gate (see "What Needs Measurement" below), exactly as `AUDIO_ENGINE_TIMEOUT` was derived from a measured p95 RTF rather than copied from a neighboring class (Key Decision, v1.7).

**If the target feature is audio extraction (video → mp3/wav/m4a):**
- Use `-vn` (drop video stream entirely) + the matching audio encoder (`libmp3lame`/PCM/native `aac`) — structurally the *same* single-ffmpeg-invocation shape as `internal/convert/whisper.go`'s existing `ffmpegNormalizeArgs`, just with a different output codec per target instead of the fixed 16kHz-mono-WAV normalize target.
- Because dropping the video stream makes this dramatically cheaper than a full transcode (no video encode at all) — do not budget it under the same timeout ceiling as the transcode feature; it deserves its own (likely much smaller) measured budget.

**If the target feature is thumbnail extraction (frame at timecode → jpg/png/webp):**
- Use input seeking (`-ss` before `-i`) + `-frames:v 1` + a codec-appropriate quality flag (`-q:v 2` for jpg; ffmpeg's built-in `png`/`libwebp` encoders need no extra tuning for a single still frame).
- The timecode itself must go through a typed, validated opts field (mirroring the closed-allowlist `AudioOpts`/`DocOpts`/`HTMLOpts` convention from OPTS-01/02) — parse/bound-check it server-side (e.g. reject negative, reject timecodes beyond the ffprobe-reported duration) before it ever becomes an ffmpeg argv element; never interpolate a raw client string into `-ss`.
- This is the **cheapest** new operation (near-instant — no full-file decode when input-seeking to a nearby keyframe) — likely does not need its own RTF-style gate the way transcode does, but should still get a short, explicit timeout distinct from the transcode budget (mirrors `audioduration.go`'s existing "ffprobe gets a SHORT bound distinct from the full engine timeout" precedent).

**If the target feature is video → transcript (ffmpeg extraction + existing whisper pipeline in one job):**
- **This is an architecture decision, not just a stack decision — flag it explicitly for `ARCHITECTURE.md`/planning, do not resolve it silently here.** Two stack-level options exist:
  - **Option A — bake whisper.cpp + model into `Dockerfile.av-worker` too.** Reuses the exact two-stage `ffmpeg-normalize → whisper-cli-transcribe` pipeline already proven in `internal/convert/whisper.go`, entirely inside one container/one job. Cost: duplicates ~400+ MB of whisper.cpp binary + baked model weight into a second image, and couples the av-worker's resource/scaling profile (CPU-bound video encode) to whisper's (also CPU-bound) — the two most expensive operations in the whole system would now compete for the same container's CPU ceiling.
  - **Option B — av-worker only extracts/normalizes the audio track via ffmpeg, then hands off to the existing `audio` queue/pipeline for the whisper stage** (cross-queue chaining: av-worker's job produces a normalized audio artifact and enqueues (or the API enqueues) an audio-class job against it). Reuses the already-shipped, already-measured (RTF p95=0.206, `AUDIO_ENGINE_TIMEOUT=742s`) audio-worker container completely unchanged — no whisper.cpp/model duplication, no new resource-contention risk, and matches this project's established "one container per engine class, no fat multi-engine containers" pattern (`Dockerfile.worker`/`Dockerfile.document-worker`/`Dockerfile.chromium-worker`/`Dockerfile.audio-worker` are all single-purpose).
  - **Stack-level recommendation: prefer Option B.** It keeps `Dockerfile.av-worker` lightweight (~500 MB vs. ~900MB+ for Option A, see Container Size Budget below), avoids re-litigating whisper.cpp's already-closed sizing/pinning decisions, and follows the container-per-engine-class precedent with zero exceptions so far. The concrete job-orchestration mechanism (single Postgres job with two engine "stages," vs. two chained jobs, vs. something else) is a job-model/architecture question for planning, not a library/packaging one.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|------------------|-------|
| `ffmpeg 7:5.1.9-0+deb12u1` | `debian:bookworm-slim` runtime base — **identical** base image and package version already used by `Dockerfile.audio-worker` | Zero new base-image risk: this is literally the same apt package the audio engine already depends on, just given its own dedicated container. |
| `libx264`/`libx265`/`libvpx`/`libwebp`/`libmp3lame` encoder versions | Whatever ffmpeg 5.1.9's Debian build links against | Not independently apt-pinned by this project (Debian's `ffmpeg` package pulls in matching library versions as dependencies automatically) — re-verify with `ffmpeg -encoders`/`-h encoder=<name>` at Dockerfile-build time if a specific flag's availability matters, same discipline already used for `whisper-cli -h` in the audio STACK.md. |
| `ffprobe 5.1.9-0+deb12u1` | Same apt package as `ffmpeg`, so version lockstep is automatic (they ship from one Debian source package and cannot skew relative to each other) | Extend `internal/convert/audioduration.go`'s existing `ProbeDuration`/argv-building pattern for the richer video stream-inspection needs, rather than introducing a second, differently-shaped ffprobe wrapper. |
| `linux/amd64` and `linux/arm64` | Both supported — Debian's `ffmpeg` package is multi-arch (this session's live verification ran on `arm64`; the existing CI 4-level pipeline already bakes multi-target images per the project's established bake matrix) | No `GGML_NATIVE`-style native-CPU-instruction footgun exists here (unlike whisper.cpp's ggml backend) — Debian's ffmpeg build is a normal portable distro package, not compiled with `-march=native`. |

## Container Size Budget

**Confidence: HIGH for the ffmpeg-apt-install number (measured live via `apt-get install` dry-run output in this session, not estimated); MEDIUM for the final image total (base + ffmpeg is measured, but real built-image overhead from Go binary/layers is a small, well-understood constant based on every other Dockerfile in this repo).**

| Component | Size | Basis |
|---|---|---|
| `debian:bookworm-slim` base | ~80 MB | Same base every other worker image already uses |
| `ffmpeg` + full apt dependency chain (`--no-install-recommends`) | **416 MB additional disk space** (live-measured this session: `apt-get install --no-install-recommends ffmpeg` on a fresh `debian:bookworm-slim` reports "Need to get 118 MB of archives. After this operation, 416 MB of additional disk space will be used.") | This is meaningfully larger than the ~150-200 MB *estimate* written in the prior (v1.7, now-superseded) STACK.md — that number was an unverified guess; this one is a live measurement and should be treated as the more reliable planning input going forward. |
| Go binary (`av-worker`) | a few MB | `CGO_ENABLED=0` static binary, same as every other `cmd/*-worker` |
| **Estimated total, Option B (ffmpeg only, no whisper.cpp)** | **~500 MB** | Comparable to `Dockerfile.document-worker`'s LibreOffice-suite footprint; smaller than `Dockerfile.audio-worker`'s ~682 MB (arm64, per PROJECT.md) since there is no ~150 MB baked model to carry |
| **If Option A were chosen instead (whisper.cpp + model baked into av-worker too)** | **~900 MB+** | ~500 MB (ffmpeg) + ~400 MB (whisper.cpp binary + libs + `ggml-base.bin`, per the v1.7 STACK.md's own component breakdown) — reinforces the "prefer Option B" recommendation above on pure image-size/cold-start-latency grounds alone, before even counting the CPU-resource-contention argument |

## What Needs Measurement (not resolved by this stack research)

Per this project's own established precedent (a measured go/no-go gate for veraPDF's JVM startup in Phase 23, and the audio engine's own RTF-measured `AUDIO_ENGINE_TIMEOUT` in Phase 32 rather than a copied/guessed constant — Key Decision, v1.7): **do not copy `AUDIO_ENGINE_TIMEOUT`'s value or its RTF number for video transcode.** Transcode is a fundamentally different compute shape (full-frame video encode vs. audio-only inference) and needs its own empirical encode-time-factor measurement (processing_time / video_duration, analogous to RTF) on representative sample footage at whatever `-preset`/`-crf` the roadmap settles on, run against the same CPU ceiling the container will actually get (mirrors `cpus: "2.0"` per-worker pattern). This stack research deliberately does not invent a number — that is a phase-execution measurement task, not a library-selection one.

## Sources

- Live-verified in this session (2026-07-19) inside a real `debian:bookworm-slim` Docker container: `ffmpeg -version` (full `configure` flags, confirms `--enable-gpl --enable-libx264 --enable-libx265 --enable-libvpx --enable-libwebp --enable-libmp3lame`), `ffmpeg -encoders` (confirms `libx264`/`libx265`/`libvpx`/`libvpx-vp9`/`libwebp`/native `aac`/`libmp3lame` present; confirms `h264_nvenc`/`h264_vaapi`/`hevc_vaapi`/`*_v4l2m2m` also compiled in but require unavailable hardware), `ffmpeg -hwaccels` (confirms `vdpau`/`cuda`/`vaapi`/`opencl`/`vulkan` methods compiled in, none usable in this deployment target), `ffmpeg -muxers` (confirms `mp4`/`mov`/`webm`/`matroska` present), `apt-cache policy ffmpeg` (confirms exact version `7:5.1.9-0+deb12u1`), `dpkg -L ffmpeg` (confirms `ffprobe`/`ffplay`/`qt-faststart` ship in the same package), `apt-get install --no-install-recommends ffmpeg` dry-run output (confirms 118 MB download / 416 MB installed size) — HIGH confidence, directly reproduced, not taken on faith from documentation
- `https://packages.debian.org/bookworm/ffmpeg` — HIGH confidence, confirms `5.1.9-0+deb12u1` is the current bookworm package version and that no official `bookworm-backports` ffmpeg package exists
- `https://lists.debian.org/debian-security-announce/2025/msg00149.html` (DSA 5985-1) and Debian security tracker news for `ffmpeg 7:5.1.6-0+deb12u1` / `7:5.1.7-0+deb12u1` — MEDIUM-HIGH confidence (via WebSearch, cross-referencing Debian's own tracker/mailing-list domains), confirms Debian actively backports CVE fixes into the 5.1.x branch (CVE-2024-7055, CVE-2024-7272, CVE-2023-49502, CVE-2023-50007/50008, CVE-2024-31582, CVE-2024-35367/35368, CVE-2025-0518, CVE-2025-7700, CVE-2025-22919), supporting the "apt over static build" recommendation
- WebSearch, ffmpeg.org release-branch summaries (via secondary aggregators — `endoflife.date`, `gyan.dev`, `free-codecs.com`, since `ffmpeg.org`/`trac.ffmpeg.org` blocked automated WebFetch with an Anubis access-control challenge during this research) — MEDIUM confidence, used only to establish that upstream ffmpeg has moved to the 7.x/8.x branch while Debian bookworm stays on 5.1.x by design (stable-release policy), not to source any technical/flag claim
- WebSearch on `-ss` input-vs-output seeking semantics, cross-referenced against this session's own live `ffmpeg -h encoder=libx264` output for preset/crf option names/defaults — MEDIUM confidence (community sources agree consistently; the specific flag names/defaults were independently confirmed live against the pinned binary, not trusted from the search results alone)
- This repository's own `Dockerfile.audio-worker`, `internal/convert/whisper.go`, `internal/convert/audioduration.go`, `internal/convert/exec.go`, `.planning/research/STACK.md` (v1.7, now superseded), `.planning/PROJECT.md` — HIGH confidence, direct inspection of the existing, already-shipped conventions this new engine class must follow

---
*Stack research for: OctoConv v1.8 av/video engine class (ffmpeg)*
*Researched: 2026-07-19*
