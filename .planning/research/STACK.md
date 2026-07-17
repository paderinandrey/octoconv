# Stack Research

**Domain:** Offline audio-transcription engine class (whisper.cpp) for OctoConv v1.7
**Researched:** 2026-07-17
**Confidence:** HIGH for versions/CLI/models (verified live against GitHub API, live README, live HuggingFace HEAD requests); MEDIUM for CPU realtime-factor sizing (no first-party benchmark on comparable modest cloud/OrbStack CPU cores — see caveats)

This is a **replacement** stack note for OctoConv's v1.7 milestone. It supersedes the previous milestone's STACK.md content (v1.6 — KEDA/Helm/OrbStack Kubernetes, all already shipped and merged, not revisited here). It covers only the NEW audio engine class. Go/chi/asynq/Postgres/MinIO/Helm/KEDA are already validated (see PROJECT.md) and are not re-researched here.

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `ggml-org/whisper.cpp` | **v1.9.1** (tag, released 2026-06-19, confirmed live via GitHub Releases API — `published_at: 2026-06-19T05:53:19Z`) | CPU-only offline speech-to-text inference engine | Already the LOCKED decision. Header-only C/C++ port of OpenAI Whisper, zero Python runtime, ships a single static-ish CLI binary — this is the only whisper implementation that fits OctoConv's "shell out to a CLI binary in a per-class container" pattern used by libvips/LibreOffice/chromium. No runtime network calls needed once the binary + model are baked into the image, satisfying the "воркеры остаются офлайн" constraint. |
| `whisper-cli` (built from source, part of whisper.cpp `examples/cli`) | ships with v1.9.1 | The actual binary the worker shells out to | Renamed from the old `main` binary (deprecated) in whisper.cpp ~v1.5. `whisper-cli -h` confirms binary name and full flag set live. Fits `internal/convert/exec.go`'s `runCommand` (process-group kill on timeout) with zero changes needed — it is a single synchronous invocation, exactly like the libvips converter, not a forking daemon like `soffice.bin`. |
| ggml model file (`ggml-base.bin`, `ggml-small.bin`) | pin an exact model file by SHA-256, not `download-ggml-model.sh`'s mutable `main` branch pointer | Acoustic + language model weights whisper-cli loads via `-m` | Format is **ggml (`.bin`), NOT GGUF** — do not confuse with llama.cpp's GGUF ecosystem; whisper.cpp's own converters (`convert-pt-to-ggml.py`) only emit `.bin`. Hosted on HuggingFace (`https://huggingface.co/ggerganov/whisper.cpp`), which is itself a mirror the whisper.cpp project treats as canonical (the old `ggml.ggerganov.com` CDN is commented out in `download-ggml-model.sh` as of v1.9.1 — dead). |
| `ffmpeg` | Debian bookworm's apt-pinned build: **7:5.1.9-0+deb12u1** (verified live via `apt-cache policy` against `debian:bookworm-slim`) | Pre-conversion of arbitrary input audio → 16kHz mono 16-bit PCM WAV | whisper.cpp's own README states plainly: *"the whisper-cli example currently runs only with 16-bit WAV files, so make sure to convert your input before running the tool"* — this is a **hard, confirmed requirement**, not an optimization. Installing system ffmpeg via apt (matching the existing `apt-get install -y --no-install-recommends` pattern in `Dockerfile.document-worker`/`Dockerfile.chromium-worker`) and running it as an explicit pre-processing step keeps the pipeline as two separate, independently-hardened `runCommand` invocations (ffmpeg, then whisper-cli) rather than relying on whisper.cpp's optional built-in `WHISPER_COMMON_FFMPEG` cmake flag (see "What NOT to Use"). |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `build-essential`, `cmake`, `git` (apt, Debian bookworm build stage only) | bookworm apt defaults | Compile whisper.cpp from source | Needed only in the Dockerfile's `build` stage (mirrors `golang:1.26-bookworm` builder pattern already used for `cmd/*-worker`). Not present in the runtime image. |
| `libgomp1` (apt) | bookworm apt default | OpenMP runtime whisper.cpp's ggml CPU backend links against for multi-threading | Required in the **runtime** image if whisper.cpp is built with its default threading backend; confirm at build time with `ldd build/bin/whisper-cli` and add whatever `.so` it resolves to system libs beyond libc/libstdc++ (typically just `libgomp1`). |

No new **Go** dependencies are needed — this engine class follows the exact same shape as libvips/LibreOffice/chromium: a `Converter` implementation in `internal/convert/` that shells out via the existing `runCommand` hardened-exec helper. Do not add a Go whisper binding (see "What NOT to Use").

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| `whisper-cli -h` | Enumerate exact flags for the pinned tag before writing `internal/convert/whispercpp.go` | Flags are stable but do change across minor versions (e.g. `--version` flag only added in v1.8.7) — re-run `-h` against the exact pinned v1.9.1 binary rather than trusting older blog posts. |
| `models/download-ggml-model.sh` (from the pinned v1.9.1 source tree) | Reference for the exact HuggingFace URL shape (`https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-<model>.bin`) | Do not shell this script directly into the Dockerfile unmodified — it has no checksum verification (confirmed by reading the script live). Use `curl -L --fail` against the same URL plus your own pinned SHA-256 check (see Installation below). |

## Installation

```dockerfile
# Dockerfile.audio-worker — mirrors Dockerfile.document-worker's shape
# (Go build stage -> engine build stage -> slim runtime stage)

# --- Go build stage ---
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/audio-worker ./cmd/audio-worker

# --- whisper.cpp build stage: compile from source, pinned tag ---
FROM debian:bookworm-slim AS whisper-build
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential cmake git ca-certificates \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch v1.9.1 https://github.com/ggml-org/whisper.cpp.git /whisper
WORKDIR /whisper
# GGML_NATIVE=OFF is load-bearing: without it, ggml compiles with -march=native
# for whatever CPU the image builder happens to run on (CI runner / OrbStack
# host), and whisper-cli SIGILLs on any runtime host lacking those exact
# instruction extensions -- a well-documented whisper.cpp/llama.cpp Docker
# pitfall (see Sources). Do NOT flip this to ON to "optimize."
RUN cmake -B build -DGGML_NATIVE=OFF -DCMAKE_BUILD_TYPE=Release \
    && cmake --build build -j --target whisper-cli --config Release

# Pin the model by content hash, not by trusting HuggingFace's mutable `main`
# branch pointer at build time (mirrors the project's existing discipline of
# pinning exact MinIO RELEASE tags / veraPDF v1.30.2 rather than `:latest`).
# SHA-256 verified live 2026-07-17 against
# https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin
RUN curl -L --fail --retry 5 --retry-delay 5 \
      -o /models/ggml-base.bin \
      https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin \
    && echo "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe  /models/ggml-base.bin" | sha256sum -c -

# --- runtime stage ---
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*
COPY --from=whisper-build /whisper/build/bin/whisper-cli /usr/local/bin/whisper-cli
COPY --from=whisper-build /models/ggml-base.bin /models/ggml-base.bin
COPY --from=build /out/audio-worker /usr/local/bin/audio-worker
# Single synchronous CLI invocation per job (ffmpeg, then whisper-cli) --
# no forking daemon like soffice.bin/chromium, so (matching Dockerfile.worker's
# own documented rationale) no tini/init-as-PID-1 is needed here.
USER nobody
ENTRYPOINT ["/usr/local/bin/audio-worker"]
```

```bash
# Example runCommand-shaped invocation sequence the worker performs per job
# (two hardened exec.Command calls, both bounded by AUDIO_ENGINE_TIMEOUT):

# 1. Preprocess: decode arbitrary container/codec to whisper.cpp's required format
ffmpeg -i input.<ext> -ar 16000 -ac 1 -c:a pcm_s16le -y /tmp/<job>/audio.wav

# 2. Transcribe: JSON output carries per-segment timestamps (needed for
#    SEED-001's future lesson-parsing use case); -np keeps stdout clean since
#    the worker doesn't need whisper-cli's live console progress.
whisper-cli \
  -m /models/ggml-base.bin \
  -f /tmp/<job>/audio.wav \
  -of /tmp/<job>/result \
  -oj \
  -l auto \
  -np \
  -t 2 \
  -bs 1 -bo 1
```

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|--------------------------|
| whisper.cpp `whisper-cli` (CLI, one-shot per job) | whisper.cpp's own `whisper-server` (persistent HTTP daemon, also in the same repo) | Never for this project's current shape — a persistent daemon breaks the "one hardened `runCommand` invocation per job, per-class container, killed by process-group SIGKILL on timeout" pattern that every other engine class uses. Would require a genuinely different worker architecture (HTTP client instead of `os/exec`). Reconsider only if per-job process-spawn overhead (model load time, several hundred ms–seconds for base/small) becomes the dominant cost at very high job throughput. |
| whisper.cpp (C++, CPU) | `faster-whisper` (Python + CTranslate2, GPU/CPU) | Only if a GPU becomes available in the deployment target and higher-than-whisper.cpp throughput is required. Rejected here: pulls in a Python runtime + pip dependency tree, which is a new language/toolchain axis this Go-only, CLI-shell-out project has deliberately avoided for every other engine (libvips/LibreOffice/chromium are all apt-installed native binaries, not language runtimes). |
| ggml `base`/`small` models, CPU-only | `large-v3-turbo` or GPU-accelerated inference | Only if transcription quality on accented/noisy/technical speech proves inadequate with base/small in practice — large models are 3-10x slower on CPU (see Realtime-Factor Sizing below) and OctoConv's current infra (OrbStack k8s, KEDA scale-from-zero CPU pods, `cpus: 2.0` per-worker ceiling in compose) has no GPU path. |
| Bake exactly one default model (`base`) into the image | Support multiple selectable models per job via `preset`/opts | OctoConv already has a `preset`/`opts` mechanism (Phase 18/14) for per-job engine tuning — extending it to a `model=base|small` opt is a reasonable **later** step once the audio class ships, not a day-one requirement. Bundling every model size into one image (`tiny`+`base`+`small`+`medium` = ~2.1 GiB of model weights alone) blows the container-size budget for no proven need. |
| `-bs 1 -bo 1` (greedy decoding) | whisper-cli's defaults (`-bs 5 -bo 5`, beam search) | Beam search with beam/best-of = 5 materially improves transcript quality at the cost of several times more compute per segment (well-documented whisper.cpp community tuning knob). Given this is a CPU-only, timeout-bounded, offline background job (not a live dictation UI), greedy decoding is the safer default to keep `AUDIO_ENGINE_TIMEOUT` predictable; raise `-bs`/`-bo` later as an opt-in preset if transcript quality demands it. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|--------------|
| `-DGGML_NATIVE=ON` (ggml's default when unset) | Compiles with `-march=native` for whatever CPU the *builder* happens to run on (CI runner, or an Apple Silicon OrbStack build host cross-building `linux/amd64`). Any runtime host lacking those exact instruction extensions gets a SIGILL crash — a well-documented, recurring whisper.cpp/llama.cpp Docker footgun. | `-DGGML_NATIVE=OFF` explicitly in the Dockerfile build stage (see Installation). Accept the modest perf cost of non-native tuning; do not chase `GGML_CPU_ALL_VARIANTS` (requires `GGML_BACKEND_DL`, adds real build complexity) unless a measured need appears. |
| Downloading the ggml model at container **runtime** (e.g. `wget` in an entrypoint script, or bind-mounting from a shared volume fetched on first boot) | Violates the "воркеры остаются офлайн" milestone constraint and KEDA scale-from-zero: a freshly-scaled-up pod with no cached model would either fail closed or make an outbound network call on the hot path of every cold start. Also non-deterministic — HuggingFace's `main` branch pointer can move. | Bake the model into the image at **build** time with a pinned SHA-256 (see Installation). This matches the project's existing precedent (LibreOffice fonts/filters baked in, veraPDF app tree copied in at build time, chromium-headless-shell baked in — nothing engine-related is fetched at runtime anywhere in the current codebase). |
| `WHISPER_COMMON_FFMPEG` cmake flag (whisper.cpp's own optional built-in FFmpeg decoding path, letting `whisper-cli` accept mp3/ogg/opus directly) | Couples the ffmpeg *library* linkage into the whisper.cpp build itself (extra `libavcodec`/`libavformat`/`libswresample`-dev build dependencies, a second surface for CVEs to land on inside the whisper binary) and — per the project's own hardened-exec pattern — every external format-parsing step should be its own bounded, killable `runCommand` invocation, not baked into a single monolithic binary. | System `ffmpeg` CLI (apt-installed) invoked as its own explicit preprocessing `runCommand` step before `whisper-cli` runs, exactly as the README's own documented workaround already recommends (`ffmpeg -i input.mp3 -ar 16000 -ac 1 -c:a pcm_s16le output.wav`). |
| Go whisper.cpp CGo bindings (e.g. community `go-whisper`/`whisper.go` wrappers around `libwhisper.so`) | Introduces CGo into a codebase that is explicitly built `CGO_ENABLED=0` everywhere (`Dockerfile.api:7`, `Dockerfile.worker:7`, etc.) for static, minimal binaries; breaks that convention for one engine class only, and re-introduces exactly the kind of tight in-process coupling to an external native library that the whole `Converter`/CLI-shell-out architecture was designed to avoid. | Plain CLI shell-out via the existing `internal/convert/exec.go` `runCommand` helper — zero new build modes, consistent with every other engine. |
| `models/download-ggml-model.sh` run unmodified inside the Dockerfile | No checksum verification exists in the script (confirmed by reading it live) — it just curls the file and trusts it. | Curl the same URL directly and pin a SHA-256 (see Installation) — five extra lines, closes the gap. |
| Bundling `medium`/`large*` models by default | 1.5-2.9 GiB per model file alone; on CPU-only 2-core containers these are 5-10x slower than base/small (see sizing below) and blow both the container-size and `ENGINE_TIMEOUT` budgets for marginal accuracy gain on typical internal-service audio (meeting recordings, lecture audio — not adversarial/noisy). | `base` as the shipped default; `small` as an optional build-arg/preset variant if quality complaints arise. |

## Stack Patterns by Variant

**If the job payload is short, clear-audio internal recordings (meetings, lectures) — the expected common case:**
- Use the `base` model (142 MiB, ~388 MB RAM) as the default.
- Because it is the smallest model with acceptable general-purpose accuracy, keeps the container image and RAM footprint small, and (per the sizing evidence below) comfortably beats real-time on 2 modern CPU cores.

**If transcript quality complaints emerge for accented/technical/noisy audio:**
- Offer `small` (466 MiB, ~852 MB RAM) as an explicit opt-in (preset or build variant), not the default.
- Because `small` is meaningfully slower — budget roughly 2-3x more wall-clock time per job than `base` on the same core count — and the container/`ENGINE_TIMEOUT` math must account for that before it's the default.

**If English-only audio is a hard guarantee for a given client population:**
- Use the `.en`-suffixed model variant (`ggml-base.en.bin`, `ggml-small.en.bin`) instead of the multilingual model.
- Because English-only models are measurably more accurate on English audio at the same size — this is documented, uncontroversial whisper.cpp guidance, not a novel claim — and `-l auto`/language auto-detection can be dropped entirely, removing one source of transcription-time variance.

**If future language-detection/multi-language support is required (not currently in scope):**
- Keep the multilingual model and pass `-l auto`.
- Because switching between `.en` and multilingual models later is a model-file swap only, not a code change — no need to design for it prematurely.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|------------------|-------|
| `whisper.cpp v1.9.1` | `debian:bookworm-slim` (glibc), built from source with `-DGGML_NATIVE=OFF` | No official Debian/apt package exists for whisper.cpp — it must be built from source in the Dockerfile's build stage, mirroring how the project already builds Go binaries from source rather than relying on distro packages for anything project-specific. |
| ggml model files (`ggml-*.bin`) | Any whisper.cpp version from roughly v1.5+ | The ggml model format has been stable across recent whisper.cpp releases; no version-lockstep concern like there is between e.g. veraPDF's CLI jar and its own musl-linked JRE (documented gotcha elsewhere in this repo's `Dockerfile.document-worker`). Still, re-verify the model loads cleanly against the exact pinned v1.9.1 binary before shipping — don't assume forward/backward compatibility with an untested tag. |
| `ffmpeg 7:5.1.9-0+deb12u1` (bookworm apt) | Any whisper.cpp version (ffmpeg is an external preprocessing step, not linked into whisper.cpp at all in this design) | No coupling — this is the entire point of keeping ffmpeg as a separate `runCommand` step rather than a compiled-in whisper.cpp feature. |
| `linux/amd64` and `linux/arm64` | Both supported: whisper.cpp's own official `ghcr.io/ggml-org/whisper.cpp:main` image publishes both platforms; OrbStack's k8s runs natively on Apple Silicon (arm64) | Building the audio-worker image via a from-source multi-stage build (as recommended, not the official prebuilt image — see below) means `GGML_NATIVE=OFF` must be set identically regardless of which arch is building/running, so this compatibility is self-managed rather than inherited from an upstream image. If CI (GitHub Actions, currently x86_64 runners per existing 4-level CI pipeline) builds the amd64 image and OrbStack separately builds/runs arm64 locally, both paths hit the same Dockerfile with no platform-specific branching needed. |

## Docker Packaging: Build-from-Source vs. Prebuilt Image

**Recommendation: build from source in a multi-stage Dockerfile, do not use `ghcr.io/ggml-org/whisper.cpp:main`.**

- The official image bundles `curl` and `ffmpeg` already and supports both `linux/amd64`/`linux/arm64` — it is a legitimate option and worth knowing about.
- But it does not follow this project's established "build-essential toolchain in a throwaway build stage → copy only the compiled artifact into a slim runtime stage, with an explicit pinned `USER nobody`" pattern used by `Dockerfile.worker`/`Dockerfile.document-worker`/`Dockerfile.chromium-worker` — the official image's entrypoint (`ENTRYPOINT ["bash", "-c"]` on their own `main.Dockerfile`) is designed for ad-hoc interactive use, not as a base for a Go binary + hardened-exec worker.
- Building from source also lets the project pin `-DGGML_NATIVE=OFF` explicitly (the official image's own build flags for this are not something this research could verify are portability-safe across arbitrary runtime hosts) and control exactly which model gets baked in with a verified SHA-256, rather than trusting an upstream multi-purpose image's model-fetch conventions.

## Realtime-Factor Sizing (affects `AUDIO_ENGINE_TIMEOUT` / KEDA cooldown)

**Confidence: MEDIUM — no first-party whisper.cpp benchmark exists on hardware directly comparable to a 2-core OrbStack/cloud container; the numbers below are triangulated from multiple third-party sources and should be treated as planning inputs to validate empirically during phase execution (matching this project's own precedent of a measured go/no-go gate for veraPDF's JVM startup cost in Phase 23), not as ship-blocking guarantees.**

Realtime factor (RTF) defined here as `processing_time / audio_duration` — lower is faster; RTF < 1.0 means faster than real time.

| Data point | Model | Hardware | RTF (this def.) | Source confidence |
|---|---|---|---|---|
| Community discussion benchmark | base, q4_0 quantized | Intel Core i5-460M (2010, 2c/4t, no AVX) | ~1.17 (slower than real time) | LOW — legacy/atypical hardware, but useful as a worst-case floor |
| Community discussion benchmark | small, q4_0 quantized | same legacy CPU | ~3.6 | LOW |
| Third-party blog benchmark | small, CPU-only | Apple M2 (arm64, NEON) | ~0.35 | MEDIUM |
| General community guidance | small | "modern CPU" | ~0.17 (cited as "6x real-time") | LOW-MEDIUM, unspecified core count |

**Working assumption for planning** (base model, greedy decoding `-bs 1 -bo 1`, 2 CPU cores, matching the project's existing `cpus: "2.0"` per-worker ceiling): expect RTF in the **0.2-0.5** range on modern arm64 (OrbStack/Apple Silicon) hardware, and plan for it to be **worse** (potentially approaching or exceeding 1.0) on generic x86_64 CI/cloud cores without AVX2, especially if beam search defaults are left on. `small` should be budgeted at roughly 2-3x `base`'s wall-clock time on the same core count.

**Practical implication:** `AUDIO_ENGINE_TIMEOUT` cannot be a single-digit-minute constant like `HTML_ENGINE_TIMEOUT=60s` or even `DOCUMENT_ENGINE_TIMEOUT=300s` — it must account for realistic audio *duration*, not just engine complexity, because `MAX_UPLOAD_BYTES` (currently 100 MiB, shared across all engine classes) could admit an hour-plus recording at typical lossy-compressed bitrates. Recommend the roadmap treat the audio-duration-vs-timeout relationship as its own explicit design question (e.g. a duration cap probed via `ffprobe` before enqueueing, separate from the byte-size cap) rather than assuming a fixed timeout constant covers all inputs — this is a sizing/architecture decision for a later phase, not resolved by this stack research.

## Container Size Budget

**Confidence: LOW-MEDIUM — estimated additively from verified component sizes, not measured against an actually-built image; recommend measuring the real built image size during phase execution and treating this as a planning estimate only.**

| Component | Estimated size | Basis |
|---|---|---|
| `debian:bookworm-slim` base | ~80 MB | Same base every other worker image already uses |
| `ffmpeg` + its apt dependency chain (`--no-install-recommends`) | ~150-200 MB | ffmpeg's shared-library dependency tree (libavcodec/libavformat/etc.) is substantial even with recommends trimmed; not independently measured here |
| `whisper-cli` binary + `libwhisper`/`libggml` shared libs | ~10-30 MB | Small C/C++ binary, no heavyweight dependency tree unlike LibreOffice/chromium |
| `ggml-base.bin` (default, baked in) | 141 MiB (verified via live HEAD request, 147,951,465 bytes) | This is the dominant image-size line item if `small` is also bundled |
| **Estimated total (base model only)** | **~400-450 MB** | Comparable to or smaller than `Dockerfile.document-worker`'s LibreOffice-suite footprint; well under `Dockerfile.chromium-worker`'s likely size |
| If `small` model is bundled instead of/alongside `base` | +466 MiB (verified, 487,601,967 bytes) | Pushes total toward ~850 MB-1 GB if both are baked in — reinforces the "ship one default model" recommendation above |

## Sources

- `https://github.com/ggml-org/whisper.cpp/releases` and `https://api.github.com/repos/ggml-org/whisper.cpp/releases` — HIGH confidence, live-verified 2026-07-17: current tag `v1.9.1`, published `2026-06-19T05:53:19Z`
- `https://raw.githubusercontent.com/ggml-org/whisper.cpp/v1.9.1/examples/cli/README.md` — HIGH confidence, live-fetched full `whisper-cli -h` output at the exact pinned tag (flags, output formats, model path default)
- `https://raw.githubusercontent.com/ggml-org/whisper.cpp/v1.9.1/README.md` — HIGH confidence, live-fetched: 16-bit-WAV requirement, exact `ffmpeg -ar 16000 -ac 1 -c:a pcm_s16le` conversion command, `WHISPER_COMMON_FFMPEG` opt-in flag, official Docker image list/platforms
- `https://raw.githubusercontent.com/ggml-org/whisper.cpp/v1.9.1/models/download-ggml-model.sh` — HIGH confidence, live-fetched: confirms ggml (not GGUF) format, HuggingFace as canonical source, no built-in checksum verification
- `https://raw.githubusercontent.com/ggml-org/whisper.cpp/v1.9.1/.devops/main.Dockerfile` and `ggml/CMakeLists.txt` — HIGH confidence, live-fetched: official Docker approach, `GGML_NATIVE`/`GGML_CPU_ALL_VARIANTS` cmake options
- `https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-{tiny,base,small,medium}.bin` — HIGH confidence, live HEAD/GET requests 2026-07-17 confirming exact byte sizes and SHA-256 for `base`/`small`
- `debian:bookworm-slim` live `apt-cache policy ffmpeg` — HIGH confidence, run live 2026-07-17: `ffmpeg 7:5.1.9-0+deb12u1`
- `https://github.com/ggml-org/llama.cpp/discussions/10230` and related llama.cpp/ggml issues on `GGML_NATIVE`/`-march=native` Docker illegal-instruction failures — MEDIUM confidence (community discussion, but consistent across multiple independent issue reports and matches ggml's own CMakeLists documentation of the flag's behavior)
- `https://github.com/ggml-org/whisper.cpp/discussions/3752` (legacy-hardware benchmark) and third-party benchmark blogs (Apple Silicon M1-M4 whisper.cpp benchmarks, general "6x real-time" community claims) — LOW-MEDIUM confidence, used only as directional RTF triangulation, explicitly flagged for empirical validation during phase execution
- This repository's own `Dockerfile.worker`, `Dockerfile.document-worker`, `Dockerfile.chromium-worker`, `internal/convert/exec.go`, `.env.example` — HIGH confidence, direct inspection of the existing, validated per-engine-class conventions this new engine class must follow

---
*Stack research for: OctoConv v1.7 audio-transcription engine class (whisper.cpp)*
*Researched: 2026-07-17*
