# Phase 36: Containerization & RTF-Measured Timeout - Research

**Researched:** 2026-07-22
**Domain:** Docker multi-stage build (from-source ffmpeg, CVE-pinned), cgroup-aware Go resource guards, RTF-based timeout derivation for video transcode
**Confidence:** MEDIUM-HIGH (ffmpeg pin/CVE verified live via git; codec-flag minimal-build list verified against the pinned source tree; RTF matrix shape and disk-guard formula are reasoned recommendations, not yet measured)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **D-01: Build the latest stable ffmpeg 8.x from pinned source in a dedicated Docker build stage — NOT `apt-get install ffmpeg`.** Mirror `Dockerfile.audio-worker`'s whisper.cpp stage shape exactly (commit-hash pin, `rev-parse` fail-loud guard, throwaway build stage COPYing one artifact into the slim runtime).
- **D-02: CVE-2026-8461 (PixelSmash) posture — closed by building patched upstream.** The plan must still verify (fail-loud) that the pinned release is at/after the fix.
- **D-03: Keep the runtime base image `debian:bookworm-slim`.** A fleet-wide base bump (bookworm → trixie) is explicitly deferred, out of scope.
- **D-04: Clone `scripts/audio-rtf-measure.sh` → `scripts/av-rtf-measure.sh`.** Matrix spans codec × resolution × preset combinations the closed `AVOpts` allowlist exposes (H.264/HEVC × 480/720/1080). Fixtures synthesized in-container via ffmpeg `lavfi`. Script gates measurement integrity only; GO/NO-GO on the derived timeout is separate. NO-GO lever: lower `AV_MAX_DURATION_SECONDS`, never inflate past the 900s `RECONCILER_ACTIVE_STALE_AFTER` cap.
- **D-05 (SUPERVISED): the RTF measurement run and go/no-go acceptance require the operator at the Docker daemon.** Pre-build sequentially with non-`latest` tags; never run compose + k8s simultaneously (4 confirmed OrbStack daemon wedges on record). Everything up to and including a *built* image is autonomous; running the measurement and accepting the number is operator-gated.
- **D-06: Disk-space/ephemeral-storage guard — genuinely new, no codebase precedent.** Must fail-closed BEFORE transcode, sized from the container's ephemeral storage and the 2 GiB upload ceiling.
- **D-07: cgroup-derived thread/RAM sizing reuses `CgroupCPULimit()`.** Already wired into `av.go:343-348`. This phase wires the container's real ceiling through end-to-end and adds RAM/concurrency sizing (peak RSS from cgroup v2 `memory.peak`).
- **D-08: The `av-worker` compose service + CI bake matrix entry land here, and `AV_ENGINE_TIMEOUT`/`AV_MAX_RETRY` propagate identically across every `queue.NewClient()`-constructing service (IN-02).** ShutdownTimeout = measured `AV_ENGINE_TIMEOUT + 10s`.

### Claude's Discretion

- Exact ffmpeg configure flags / codec libraries to enable (must cover the AVOpts allowlist: libx264, libx265, libvpx-vp9, libopus, aac/faststart; disable what's not needed).
- The disk-guard threshold formula and env var name.
- The RTF matrix's exact fixture durations and run count (mirror the audio script's `RUNS`/`CPUS` knobs).

### Deferred Ideas (OUT OF SCOPE)

- Fleet-wide base-image bump (bookworm → trixie) for general security hygiene — separate cross-cutting task.
- A dependency-advisory-tracking process — the ffmpeg pin is a point-in-time fix, not a durable process.
- Helm chart / KEDA ScaledObject — **Phase 37**, one-line forward dependency: Phase 37's KEDA cooldown/stabilization/grace-period tuning consumes this phase's measured `AV_ENGINE_TIMEOUT` as a hard input.

</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| AVE-04 | av-worker containerized (Debian ffmpeg with CVE-backport, pinned version); `AV_ENGINE_TIMEOUT` measured via worst-case RTF matrix (max resolution × most expensive codec × max duration) per Phase 32 methodology, with NO-GO levers | Priorities 1-5 below cover: exact ffmpeg pin + CVE verification (§Package Legitimacy / Sources), minimal-codec build (§Standard Stack, §Code Examples), cross-arch portability (§Architecture Patterns), disk guard (§Don't Hand-Roll, §Code Examples), RTF matrix shape + derivation formula (§Architecture Patterns, §Common Pitfalls) |

</phase_requirements>

## Summary

The five concrete unknowns this phase must resolve are now grounded in verified evidence, not guesses. **FFmpeg pin:** `n8.1.2` (commit `38b88335f99e76ed89ff3c93f877fdefce736c13`) is confirmed via `git ls-remote`/`git merge-base --is-ancestor` to be both the latest stable 8.x tag (no `n8.1.3`/`n8.2` stable exists yet, only an `n8.2-dev` pre-release tag) **and** to already contain the CVE-2026-8461 (PixelSmash) fix commit — the same version already installed on this dev host. **Minimal codec build:** `--disable-everything` plus a verified `--enable-*` list (cross-checked against the actual `n8.1.2` `libavcodec/allcodecs.c`/`libavformat/allformats.c` source and live Debian bookworm `apt-cache` package names) covers the closed AVOpts surface with real, existing component names — full completeness still needs a live build+smoke-test pass, flagged below. **Cross-arch:** ffmpeg's `runtime_cpudetect` is enabled by default in `n8.1.2`'s configure script (verified by reading `configure` directly) — no `-DGGML_NATIVE=OFF`-equivalent flag is needed; the risk is `--cpu=host`/`--disable-runtime-cpudetect`, which the Dockerfile must simply never pass. **Disk guard:** genuinely novel, no in-repo precedent; `golang.org/x/sys/unix` is already resolved in `go.sum` (indirect), so adding `unix.Statfs` costs nothing new to pin. **RTF matrix:** the single most important finding is that **prior live measurement data already exists** (`35-RESEARCH.md`) showing the `webm`/VP9 path — which has no `-cpu-used`/`-deadline` speed tuning today — ran at roughly RTF≈1.4 (slower than real time) on a fast Apple M3 Pro with only 2 threads for a tiny 15s/1080p fixture. This strongly suggests VP9, not HEVC, is the true worst-case matrix cell, and that `AV_MAX_DURATION_SECONDS` will likely need to land far lower than audio's 1800s ceiling — or the transcode code itself may need a speed-tuning flag added, which is a product/quality tradeoff requiring explicit operator sign-off, not a silent code change.

**Primary recommendation:** Pin ffmpeg to commit `38b88335f99e76ed89ff3c93f877fdefce736c13` (tag `n8.1.2`) with a whisper-style `rev-parse` fail-loud guard; build it `--disable-everything` with the verified minimal `--enable-*` list below (runtime shared libs: `libx264-164`, `libx265-199`, `libvpx7`, `libopus0`, `libmp3lame0`, `libwebp7`); do not pass `--cpu=host`/`--disable-runtime-cpudetect` (no per-arch build needed, mirrors whisper's arm64-build/amd64-run portability but via a different mechanism); measure RTF for BOTH the mp4 (h264/hevc) and webm (vp9) paths across the 480/720/1080 enum before assuming which is worst-case; and be prepared for the NO-GO lever to fire hard given VP9's default-slow encode mode.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| ffmpeg-from-source build + CVE-pin verification | Build stage (Docker, throwaway) | — | Mirrors `Dockerfile.audio-worker`'s whisper-build stage; artifact-only COPY into runtime, no build toolchain ships |
| Codec/muxer/protocol minimal-enable surface | Build stage (Docker) | API/Backend (AVOpts closed allowlist, already exists) | The build-time surface reduction is a second, structural layer of the same defense AVE-02's `-protocol_whitelist` argv flag provides at the CLI-arg layer — excluding network protocols/codecs at compile time makes the SSRF/decode-bomb class structurally unreachable, not just argv-blocked |
| Disk-space/ephemeral guard | API/Backend (`internal/convert`, called from `AVConverter.Convert`) | — | Same tier as the existing duration/resolution guards (`avduration.go`) — fail-closed before the expensive ffmpeg subprocess, not an infra-only concern |
| cgroup CPU/RAM sizing | API/Backend (`internal/convert/cgroup.go`) + Container runtime (docker-compose `deploy.resources.limits`) | — | `CgroupCPULimit()` reads the container's real ceiling; the compose block sets the ceiling the code reads — both halves must agree, same as `audio-worker`'s `--cpus=2.0`/`AUDIO_THREADS` pairing |
| RTF measurement / timeout derivation | Build/Ops tooling (`scripts/av-rtf-measure.sh`, operator-run) | — | Not application code — a one-time (or re-run-on-demand) measurement script whose OUTPUT (an env var value) becomes application configuration |
| `av-worker` compose service + CI bake entry | Container runtime / CI | API/Backend (env parity across all `queue.NewClient()` callers) | D-08 — infra wiring, but the env values it carries (`AV_ENGINE_TIMEOUT`) are a correctness-critical input to the backend's `AVUniqueTTL` derivation, not just an ops convenience |

## Package Legitimacy Audit

This phase's only new "package" install is the ffmpeg source build itself (not a language package manager dependency) plus standard Debian `apt` codec dev libraries — `slopcheck`/npm-style registry verification does not apply. Verification was instead done directly against the primary sources: the FFmpeg git repository (commit ancestry) and the live Debian bookworm `apt-cache` index (package existence). No Go module additions are required (`golang.org/x/sys` is already resolved in `go.sum` as an indirect dependency at `v0.44.0`; importing `golang.org/x/sys/unix` promotes it to direct without a new version resolution).

| Component | Registry/Source | Verification Method | Disposition |
|-----------|------------------|---------------------|-------------|
| FFmpeg `n8.1.2` (commit `38b88335f99e76ed89ff3c93f877fdefce736c13`) | github.com/FFmpeg/FFmpeg | `git ls-remote --tags` (latest stable 8.x) + `git merge-base --is-ancestor` (CVE fix ancestry) — both run live this session | Approved — `[VERIFIED: git ls-remote + git merge-base, github.com/FFmpeg/FFmpeg]` |
| `libx264-dev`/`libx264-164`, `libx265-dev`/`libx265-199`, `libvpx-dev`/`libvpx7`, `libopus-dev`/`libopus0`, `libmp3lame-dev`/`libmp3lame0`, `libwebp-dev`/`libwebp7`, `nasm`, `yasm` | Debian bookworm `apt` | `apt-cache search`/`apt-cache policy` run live inside a `debian:bookworm-slim` container this session | Approved — `[VERIFIED: apt-cache, live debian:bookworm-slim container]` |
| `golang.org/x/sys/unix` (for `Statfs`) | Go module proxy, already in `go.sum` | `go.sum` inspection (`golang.org/x/sys v0.44.0`, currently indirect) | Approved — `[VERIFIED: go.sum]` — no new module resolution, only promotion to direct |

**Packages removed due to slopcheck [SLOP] verdict:** none — slopcheck is not applicable to this phase's dependency shape (OS packages + a source-built C project, not a language package manager).
**Packages flagged as suspicious [SUS]:** none.

## Standard Stack

### Core

| Component | Version/Pin | Purpose | Why Standard |
|-----------|-------------|---------|---------------|
| FFmpeg | `n8.1.2`, commit `38b88335f99e76ed89ff3c93f877fdefce736c13` | Transcode/extract/thumbnail engine | Latest stable 8.x, verified to contain the CVE-2026-8461 fix; same major.minor.patch already validated by the whole Phase 34/35 test suite on this dev host (`ffmpeg 8.1.2` locally, confirmed live) — RTF validity depends on same-version parity (D-01 rationale) |
| `golang.org/x/sys/unix` | `v0.44.0` (already resolved in `go.sum`) | `Statfs`/`Statfs_t.Bavail` for the disk-space guard | Only cross-platform-correct way to read free-space in Go without hand-rolling syscall numbers; already an indirect dependency, zero new supply-chain surface |

### Supporting (build-time only, throwaway stage)

| Library (apt dev package → runtime shared-lib package) | Purpose | AVOpts surface covered |
|----------------------------------------------------------|---------|--------------------------|
| `libx264-dev` → `libx264-164` | H.264 encode | `mp4` target, default codec |
| `libx265-dev` → `libx265-199` | HEVC encode | `mp4` target, `AVO-03` `codec:"hevc"` |
| `libvpx-dev` → `libvpx7` | VP9 encode/decode | `webm` target (AVC-02, always full re-encode) |
| `libopus-dev` → `libopus0` | Opus encode/decode | `webm` audio track |
| `libmp3lame-dev` → `libmp3lame0` | MP3 encode | `mp3` audio-extract target |
| `libwebp-dev` → `libwebp7` | WebP encode | `webp` thumbnail target |
| `nasm`, `yasm` | x264/x265/libvpx assembly-optimized codepaths | Build-time only; without these the codec libs fall back to slow C paths, directly hurting RTF |
| `pkg-config`, `build-essential`, `git`, `ca-certificates`, `curl` | Standard C build toolchain + source fetch | Mirrors `Dockerfile.audio-worker`'s whisper-build stage tool list exactly |

All eight `lib*-dev`/`lib*N` names above were confirmed to exist in the live Debian bookworm `apt` index this session (`apt-cache search`/`apt-cache policy` inside a fresh `debian:bookworm-slim` container) — `[VERIFIED: apt-cache, live container, 2026-07-22]`. AAC encode/decode, MP3 decode, PNG/MJPEG encode, PCM encode, and H.264/HEVC/VP8/VP9 **decode** are all FFmpeg-native (no `lib*` external dependency) — confirmed by grepping `ff_aac_encoder`/`ff_png_encoder`/`ff_h264_decoder` etc. directly in the pinned tag's `libavcodec/allcodecs.c` — `[VERIFIED: n8.1.2 source, libavcodec/allcodecs.c]`.

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| `--disable-everything` + selective `--enable-*` | Full default `./configure` (kitchen-sink build) | Full build compiles ~100+ codecs/muxers/protocols the AVOpts allowlist never touches (including network protocols like `http`/`rtmp` that AVE-02's `-protocol_whitelist file,crypto` argv flag already forbids at the CLI layer) — larger image, larger attack surface, and no argv-flag typo could ever re-enable something that was never compiled in. Minimal build is strictly better for this project's threat model |
| From-source ffmpeg build | Debian `apt-get install ffmpeg` (bookworm's 5.1.x) | Superseded — this is the exact D-01 decision already locked; noted here only for completeness. 5.1.x predates and lacks the CVE-2026-8461 fix, and RTF measured against 5.1.x would not validate the 8.1.2-tested Phase 34/35 code paths |
| `golang.org/x/sys/unix.Statfs` | stdlib `syscall.Statfs` | `syscall.Statfs` is frozen/deprecated by the Go team in favor of `x/sys`; field availability/types differ subtly across `GOOS` in the stdlib package, whereas `x/sys/unix` gives a consistent `Statfs_t.Bavail`/`.Bsize` shape across linux (container) and darwin (local `go run` dev flow) build tags — same "works locally, works in-container" property `CgroupCPULimit()` already relies on `os.ReadFile` failing open for |

**Installation:**
```dockerfile
# Build stage deps (throwaway):
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential pkg-config git ca-certificates curl nasm yasm \
      libx264-dev libx265-dev libvpx-dev libopus-dev libmp3lame-dev libwebp-dev

# Runtime stage deps (shared libs only, no -dev/-headers):
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates libx264-164 libx265-199 libvpx7 libopus0 libmp3lame0 libwebp7
```

**Version verification performed this session (git, live, HIGH confidence):**
```
$ git ls-remote --tags https://github.com/FFmpeg/FFmpeg.git | grep -E 'refs/tags/n8\.'
# ... n8.0 .. n8.0.3, n8.1, n8.1.1, n8.1.2  -- n8.1.2 is the newest stable tag
# (n8.2-dev also exists but is an explicit pre-release/dev tag, not stable)

$ git rev-list -n 1 n8.1.2
38b88335f99e76ed89ff3c93f877fdefce736c13   # peeled commit -- THIS is the pin, not the tag object hash

$ git merge-base --is-ancestor 9516e6900a8294c5a9e0da8d4ad88956776d6666 n8.1.2 && echo YES
YES
# 9516e6900a... = "avcodec/magicyuv: reject slice_height misaligned with chroma vshift"
# (2026-05-09) -- the CVE-2026-8461/PixelSmash fix commit, per JFrog's technical
# writeup of the root cause ("inconsistency between how the frame allocator and
# the decoder compute chroma plane heights"), cross-referenced against multiple
# security outlets (BleepingComputer, JFrog) stating "FFmpeg released a patched
# version (8.1.2) containing a fix for CVE-2026-8461" on 2026-06-17.
```

## Architecture Patterns

### System Architecture Diagram

```text
Client upload (up to 2 GiB, av engine)
        |
        v
   API (existing, unchanged this phase)
        |  enqueue job.engine="av"
        v
  ┌─────────────────────────── av-worker container (NEW this phase) ───────────────────────────┐
  │                                                                                              │
  │  asynq consumer (cmd/av-worker, existing)                                                    │
  │        |                                                                                     │
  │        v                                                                                     │
  │  worker.HandleAVConvert (existing)                                                            │
  │        |  download input to os.MkdirTemp() workDir                                            │
  │        v                                                                                      │
  │  [NEW] disk-space guard: Statfs(workDir's filesystem) >= safety_factor * probed input size    │
  │        |  fail-closed if insufficient                                                         │
  │        v                                                                                      │
  │  AVConverter.Convert (existing duration/resolution guards, unchanged)                          │
  │        |                                                                                       │
  │        v                                                                                       │
  │  ffmpeg (from-source, n8.1.2, minimal codec build) -- threads sized via CgroupCPULimit()        │
  │        |  writes output to same workDir                                                        │
  │        v                                                                                        │
  │  upload result to S3, mark job done                                                             │
  │                                                                                                  │
  │  Container resource ceiling: --cpus=N --memory=M (compose, NEW this phase, measured not guessed) │
  └──────────────────────────────────────────────────────────────────────────────────────────────┘
        |
        v
  scripts/av-rtf-measure.sh (operator-run, D-05) -- builds this exact image, runs the codec x
  resolution matrix inside a resource-limited container, prints p95 RTF per cell -- OUTPUT feeds
  the AV_ENGINE_TIMEOUT / AV_MAX_DURATION_SECONDS values baked into docker-compose.yml (D-08)
```

### Recommended Project Structure

```
Dockerfile.av-worker              # NEW -- mirrors Dockerfile.audio-worker's 3-stage shape
scripts/av-rtf-measure.sh         # NEW -- clone of scripts/audio-rtf-measure.sh (D-04)
internal/convert/avdiskguard.go   # NEW -- disk-space guard (D-06), mirrors avduration.go's shape
internal/convert/cgroup.go        # UNCHANGED -- reused verbatim (D-07)
internal/convert/av.go            # MODIFIED -- thread AV_MAX_DURATION_SECONDS/resolution ceiling
                                   #   through (see Open Question 1 below), wire disk guard call
cmd/av-worker/main.go             # MODIFIED -- read AV_MAX_DURATION_SECONDS, disk-guard env,
                                   #   RAM/concurrency sizing
docker-compose.yml                # MODIFIED -- new av-worker service block; AV_ENGINE_TIMEOUT/
                                   #   AV_MAX_RETRY added to ALL 8 queue.NewClient() services
.github/workflows/ci.yml          # MODIFIED -- av-worker cache-to/cache-from in docker-build + e2e
.env.example                      # MODIFIED -- AV_MAX_DURATION_SECONDS (new), measured
                                   #   AV_ENGINE_TIMEOUT/AV_WORKER_CONCURRENCY values
```

### Pattern 1: From-source engine build with fail-loud commit pin

**What:** A throwaway Docker build stage clones ffmpeg at a specific tag, then immediately `checkout --detach`s the exact pinned commit and asserts `git rev-parse HEAD` matches — failing the whole build if the tag was ever force-moved.
**When to use:** Any time an external C/C++ engine is compiled from source rather than installed via a package manager (established convention: `Dockerfile.audio-worker`'s whisper.cpp stage).
**Example (adapted from `Dockerfile.audio-worker`, verified pin values this session):**
```dockerfile
# FFMPEG_COMMIT is the commit tag n8.1.2 PEELS TO (git rev-list -n 1 n8.1.2), resolved via
# git ls-remote/git rev-list on 2026-07-22. NOTE: unlike whisper.cpp's WHISPER_COMMIT (a
# lightweight tag, where the tag hash IS the commit), ffmpeg's n8.1.2 is an ANNOTATED tag --
# `git ls-remote --tags` returns TWO lines for it (the tag object hash AND a "n8.1.2^{}"
# peeled commit hash). This pin uses the PEELED COMMIT hash, matching what `git checkout
# n8.1.2` + `git rev-parse HEAD` actually resolves to. Using the tag-object hash here would
# make the guard below fail-loud unnecessarily (safe, but wrong) on every build.
ARG FFMPEG_COMMIT=38b88335f99e76ed89ff3c93f877fdefce736c13
RUN git clone --depth 1 --branch n8.1.2 \
      https://github.com/FFmpeg/FFmpeg.git /ffmpeg \
 && git -C /ffmpeg checkout --detach "${FFMPEG_COMMIT}" \
 && [ "$(git -C /ffmpeg rev-parse HEAD)" = "${FFMPEG_COMMIT}" ]
```

### Pattern 2: Minimal `--disable-everything` codec build

**What:** Configure ffmpeg to compile in only the codecs/muxers/demuxers/protocols the closed `AVOpts` surface actually needs.
**When to use:** Any time the full default build's surface exceeds what the application uses (true here — the project's entire codec/container contract is closed and enumerable, see `av.go`'s `Pairs()`).
**Example (component names verified against `n8.1.2`'s `libavcodec/allcodecs.c`/`libavformat/allformats.c` this session; the exact full list still needs a live build+run smoke pass, see Common Pitfalls):**
```dockerfile
RUN ./configure \
      --disable-everything \
      --disable-doc --disable-debug \
      --enable-gpl --enable-nonfree \
      --enable-libx264 --enable-libx265 --enable-libvpx --enable-libopus \
      --enable-libmp3lame --enable-libwebp \
      --enable-encoder=libx264,libx265,libvpx_vp9,aac,libopus,libmp3lame,pcm_s16le,mjpeg,png,libwebp \
      --enable-decoder=h264,hevc,vp8,vp9,aac,mp3,mp3float,opus,pcm_s16le,mjpeg,png \
      --enable-muxer=mp4,mov,ipod,webm,matroska,mp3,wav,image2 \
      --enable-demuxer=mov,matroska,avi,wav \
      --enable-protocol=file,crypto \
      --enable-filter=scale \
 && make -j"$(nproc)" \
 && make install
```
`--enable-gpl --enable-nonfree` are required because `libx264`/`libx265` are GPL-licensed and ffmpeg's build gates them behind these flags — confirmed by the `require_pkg_config libx264 ...` gate reading `enabled libx264` in the configure script; without `--enable-gpl` the configure step itself refuses `--enable-libx264`. `--enable-filter=scale` is required for `avScaleFilter`'s `-vf scale=-2:H` (AVO-02 resolution requests) to function at all in a `--disable-everything` build — filters are disabled by `--disable-everything` exactly like codecs/muxers.

**Not yet verified — flag for the plan:** the decoder list above covers the codecs this project's own code paths reference, but real-world `mov`/`avi`/`mkv` uploads can legally carry other legacy audio/video codecs (e.g. `ac3`, `vorbis`, `flac`, `prores`, `dvvideo`) that a full-featured ffmpeg would silently decode and this minimal list would reject. This is a real behavior change vs. today's (hypothetical, never-shipped) full-featured build, not just an image-size optimization — the plan MUST run the full existing `internal/convert/av_test.go`/`avduration_test.go` live-binary suites (which already use `requireLiveAVBinaries`) against the built container's ffmpeg binary as an integration gate before shipping, and should treat "expand the decoder allowlist" as the safe failure mode if a real fixture fails to decode, not "silently fall back to the old apt package."

### Pattern 3: Cross-arch portability via default runtime CPU dispatch (no `-DGGML_NATIVE=OFF` equivalent needed)

**What:** Unlike whisper.cpp/GGML (which defaults to baking in `-march=native` for the build host and requires an explicit `-DGGML_NATIVE=OFF` to stay portable), ffmpeg's `configure` enables `runtime_cpudetect` **by default**.
**Verified this session** by reading the pinned `n8.1.2` `configure` script directly:
```
$ grep -n 'enable runtime_cpudetect' configure
4405:enable runtime_cpudetect
$ grep -n 'disable-runtime-cpudetect' configure
108:  --disable-runtime-cpudetect disable detecting CPU capabilities at runtime (smaller binary)
```
This means the compiled binary dispatches to the best available SIMD codepath (SSE/AVX on x86, NEON on ARM) **at process startup**, rather than baking one instruction set into the binary at compile time. Building on the arm64 OrbStack host and running on amd64 (or vice versa) works with the plain default configure invocation — **no `--platform` pin is needed** (mirrors `audio-worker`'s existing "no cross-arch platform pin" compose comment, for a different underlying reason).
**Flags to NEVER pass:** `--disable-runtime-cpudetect` (defeats the whole mechanism, "smaller binary" tradeoff is not worth the portability loss here) and `--cpu=host` (the configure script itself warns `"--cpu=host makes no sense when cross-compiling"` and `die`s outright with a cross-compiler — confirmed at `configure:5522`/`:5552`). The codec libraries (`libx264`/`libx265`/`libvpx`) use the same cpuid-based runtime-dispatch convention by default, so this property is not undermined by their inclusion.

### Anti-Patterns to Avoid

- **Pinning by the ffmpeg tag's *tag-object* hash instead of the peeled commit hash:** `n8.1.2` is an annotated tag; `git ls-remote --tags` returns two hashes for it. Using the wrong one makes the `rev-parse` guard fail-loud on every build (safe but broken), unlike whisper's lightweight tag where a single hash suffices.
- **Skipping the live smoke-test pass after a minimal `--disable-everything` build:** a plausible-looking `--enable-*` list can still silently reject real-world source codecs (see Pattern 2's flagged gap). Ship the build behind the existing `av_test.go`/`avduration_test.go` live-binary suite, not just a manual `ffmpeg -version` sanity check.
- **Assuming HEVC is automatically the "most expensive codec" for the RTF matrix's worst-case cell** (see Common Pitfalls below — the evidence gathered this session points at VP9/webm instead).
- **Treating `avMaxSourceResolutionHeight` (4320, the decode-bomb ceiling) as also bounding transcode RTF cost.** It bounds INPUT resolution for the decode-bomb guard; it does NOT bound OUTPUT/encode cost when a client requests no `resolution_height` (a legal request — `avScaleFilter` returns `""` and the encode runs at the source's native resolution, up to 4320). The AVOpts enum (480/720/1080) only bounds the *downscale-requested* case.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|--------------|-----|
| Reading free ephemeral disk space | A `df`/`du` shell-out or manual `/proc/mounts` parse | `golang.org/x/sys/unix.Statfs(path, &stat)`, read `stat.Bavail * uint64(stat.Bsize)` | Already-resolved dependency (`go.sum`), no subprocess spawn, no shell-injection surface, works identically whether `path` is the container's overlay root or a mounted volume |
| CPU thread sizing under a cgroup quota | A custom `/proc/cpuinfo`/`nproc` heuristic | `convert.CgroupCPULimit()` (existing, `internal/convert/cgroup.go`) | Already built, already tested, already wired into `av.go:343-348` — this phase only needs to make sure the container's compose block sets a real `--cpus` limit for it to read, not to write new sizing logic |
| RTF measurement / timeout derivation | An ad-hoc benchmark script from scratch | Clone `scripts/audio-rtf-measure.sh` structure verbatim (D-04) | Two prior generations of this exact pattern (`verapdf-measure.sh` → `audio-rtf-measure.sh`) already solved: cgroup-derived thread count, in-container synthetic fixture generation, nearest-rank p95, measurement-integrity-only exit code |

**Key insight:** every piece of infrastructure this phase needs (cgroup CPU reading, RTF measurement scaffolding, guard-before-expensive-work pattern) already has a working, tested precedent in this exact codebase. The only genuinely novel piece of engineering is the disk-space guard (D-06) and the RTF matrix's *shape* for a resolution/codec-sensitive workload (vs. audio's roughly content-invariant one) — everything else is disciplined cloning, not invention.

## Common Pitfalls

### Pitfall 1: Assuming HEVC is the RTF matrix's "most expensive codec" without measuring VP9

**What goes wrong:** `AVE-04`'s requirement text says "worst-case (max resolution × most expensive codec × max duration)," and it is tempting to assume HEVC (`libx265`) is that codec since it's the newer/heavier standard the project explicitly added client-facing support for (AVO-03).
**Why it happens:** `transcodeToWebMArgs` (the `AVC-02` mp4→webm path, which per D-04's context notes is "ALWAYS a full re-encode") currently passes `-c:v libvpx-vp9 -b:v 1M -c:a libopus -threads N` with **no `-cpu-used`/`-deadline` flag at all**. libvpx-vp9's default "good" deadline with unset `-cpu-used` is widely documented (multiple community/vendor VP9 encoding guides, cross-referenced this session) to run dramatically slower than real-time — and this project's own prior research already measured it: `35-RESEARCH.md` recorded a **21.2s wall-clock encode for a 15s/1080p30 source using this exact `transcodeToWebMArgs` argv**, 2 threads, on an Apple M3 Pro — an RTF (wall/duration, this project's own convention) of **≈1.41**, i.e. slower than real time even on fast hardware with a tiny fixture. x264 "veryfast" and even x265 "veryfast" are typically much closer to (or comfortably under) real-time at 1080p on modern hardware, per general encoding-community knowledge (not independently re-verified this session — flag as MEDIUM confidence, needs its own measurement).
**How to avoid:** Measure the webm/VP9 cell FIRST, not last, in the RTF matrix — if it dominates, the "most expensive codec × max resolution" cell for the whole `AV_ENGINE_TIMEOUT` derivation is `webm@1080` (or whatever the enum's top resolution is), not `hevc@1080`.
**Warning signs:** If the measured VP9 p95 RTF is anywhere near or above 1.0, the Phase-32-style formula (`timeout = ceil(max_duration × RTF_p95 × 2.0)`) will force `AV_MAX_DURATION_SECONDS` down to a small fraction of audio's 1800s just to stay under the 900s cap — e.g. a p95 RTF of 1.4 with the 2.0 safety factor allows only `max_duration < 900 / 2.8 ≈ 321s` (~5.3 minutes) before the NO-GO lever must fire. This is a legitimate, foreseeable outcome — not a measurement error — and the plan should budget for it rather than be surprised by it.

### Pitfall 2: Adding a VP9 speed flag to "fix" a bad RTF number without an explicit decision gate

**What goes wrong:** If the measured VP9 RTF forces an uncomfortably small `AV_MAX_DURATION_SECONDS`, the obvious code-level fix is adding `-cpu-used`/`-deadline` tuning to `transcodeToWebMArgs` to trade compression efficiency/quality for speed. Making this change silently, mid-measurement, changes the product's output quality contract for every existing `mp4→webm` client — a decision the D-05 supervised-gate philosophy (operator judgment on safety-relevant numbers) implies should also apply here, even though D-05's text is framed around the *timeout* number specifically.
**Why it happens:** It's the path of least resistance once a NO-GO result is in hand and the RTF script has already surfaced the exact bottleneck.
**How to avoid:** If the plan considers adding VP9 speed flags, treat it as a distinct, explicitly-surfaced decision (new opts default, quality/speed tradeoff) requiring the same operator sign-off as the timeout GO/NO-GO itself — not a silent "we need this to make the number work" patch bundled into the measurement task.
**Warning signs:** A plan task that both (a) modifies `av.go`'s argv builders and (b) derives the timeout number in the same commit, with no explicit checkpoint between them.

### Pitfall 3: No clean byte-bound analog for video (unlike audio's WAV/PCM floor)

**What goes wrong:** Phase 32's `AUDIO_ENGINE_TIMEOUT` derivation used `min(AUDIO_MAX_DURATION_SECONDS, floor(MAX_UPLOAD_BYTES / min_expected_bitrate_bytes_per_s))` — a genuinely useful second constraint, because WAV/PCM has a fixed, predictable bytes-per-second rate. Video has no equivalent: a 2 GiB upload could be a multi-hour low-bitrate screen recording or a 90-second high-bitrate 4K clip — the byte↔duration relationship is not tight enough to produce a meaningful independent ceiling the way it did for audio.
**Why it happens:** Naively copying the audio formula wholesale without checking whether its assumptions hold for the new domain.
**How to avoid:** For AV, treat `AV_MAX_DURATION_SECONDS` alone (ffprobe-enforced, already-existing guard shape in `av.go`) as the primary duration ceiling; do not manufacture an artificial "assumed minimum video bitrate" byte-bound unless the plan explicitly flags it as a soft, low-confidence sanity floor rather than a load-bearing constraint (unlike audio's WAV-rate floor, which WAS load-bearing in the Phase 32 derivation... except it also turned out NOT to be the binding constraint there either — the CAP was).
**Warning signs:** A byte-bound number in the derivation math that nobody can defend the bitrate assumption behind.

### Pitfall 4: No existing threading path for `AV_MAX_DURATION_SECONDS` / resolution ceiling into `AVConverter`

**What goes wrong:** `av.go` currently hardcodes `avMaxSourceDuration = 4 * time.Hour` and `avMaxSourceResolutionHeight = 4320` as **package-level `const`s**, explicitly commented "env wiring deferred to Phase 36." `AVConverter` is a **zero-field struct** (`type AVConverter struct{}`), registered once via `converters.go`'s `init()` — unlike `worker.Handler`, which threads `audioMaxDuration` through as an explicit constructor parameter (and `cmd/av-worker/main.go` explicitly passes `0` for that parameter with a comment that AV's guard is "self-contained inside `AVConverter.Convert`, not spliced through this parameter").
**Why it happens:** Phase 34/35 deliberately deferred this wiring; there is no existing pattern in this codebase for injecting env-derived config into a `Converter` implementation that gets registered via `init()` before `main()` has read any env vars.
**How to avoid — recommended approach (not yet validated against the actual codebase constraints beyond what's read this session, flag as an open question for the planner):** change `AVConverter`'s `const`s to struct fields (`MaxSourceDuration`, `MaxSourceResolutionHeight time.Duration`/`int`, zero-value defaulting to today's `4h`/`4320` for every existing test/dev-flow caller that constructs `AVConverter{}` directly), and have `cmd/av-worker/main.go` construct a configured instance and **re-register** it into `convert.Default` at startup (overriding whatever `init()` registered) — `Register` just re-indexes the pair map, so a second call is safe. This mirrors the `api.Config`-struct pattern (`MaxUploadBytes`/`MaxEngineBytes` passed explicitly via `NewServer(cfg)`) rather than growing a second global-mutable-at-init state alongside `convert.Default` itself (CLAUDE.md's architecture doc explicitly calls out `convert.Default` as "the only global mutable-at-init state in the codebase" today).
**Warning signs:** A plan that tries to read `os.Getenv` directly inside `av.go`'s package-level `var` initializers (breaks testability, and env vars aren't guaranteed set before Go package-level `var` init order in all callers, e.g. `go test`).

## Code Examples

### Disk-space guard (recommended shape, mirrors `avduration.go`'s `EnforceMaxResolution`)

No existing code to cite — this is novel to the project (D-06 confirmed via grep: no `Statfs`/`Bavail` anywhere in `internal/`). Recommended shape, following the established guard-function convention exactly:

```go
// avdiskguard.go (NEW)
package convert

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// ErrAVInsufficientDiskSpace classifies a fail-closed rejection when the
// scratch filesystem's free space is below the sized threshold for the
// probed input size -- mirrors ErrAVResolutionExceeded's shape (avduration.go).
var ErrAVInsufficientDiskSpace = errors.New("av: insufficient free disk space for transcode")

// EnforceMinFreeDisk checks the filesystem containing dir has at least
// safetyFactor * inputSizeBytes bytes free -- fail-closed BEFORE the
// expensive ffmpeg stage, mirroring EnforceMaxDuration/EnforceMaxResolution's
// shape. dir should be the job's workDir (input and output live on the same
// filesystem, os.MkdirTemp("", ...) in worker.go). safetyFactor accounts for
// BOTH the output file (which can approach or exceed input size for some
// codec/bitrate combinations) and headroom for other concurrently-running
// jobs writing to the SAME container filesystem -- reading REAL free space
// at guard time (not a synthetic per-job budget) automatically reflects
// whatever other in-flight jobs have already consumed.
func EnforceMinFreeDisk(dir string, inputSizeBytes int64, safetyFactor float64) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(dir, &stat); err != nil {
		return fmt.Errorf("av: statfs %q: %w", dir, err)
	}
	free := stat.Bavail * uint64(stat.Bsize)
	needed := uint64(float64(inputSizeBytes) * safetyFactor)
	if free < needed {
		return fmt.Errorf("%w: free=%d needed=%d (input=%d, factor=%.1f)",
			ErrAVInsufficientDiskSpace, free, needed, inputSizeBytes, safetyFactor)
	}
	return nil
}
```

Call site (recommended, inside `AVConverter.Convert`, right after the existing duration/resolution guards, before dispatch):
```go
if fi, statErr := os.Stat(inPath); statErr == nil {
    if err := EnforceMinFreeDisk(filepath.Dir(inPath), fi.Size(), avDiskSafetyFactor); err != nil {
        return fmt.Errorf("av: %w", err)
    }
}
```

**Recommended defaults (Claude's Discretion per CONTEXT.md, genuinely novel, no precedent to cite — flag as `[ASSUMED]`, needs operator confirmation):** `avDiskSafetyFactor = 3.0` (input + up-to-input-sized output + ~1x margin for a second concurrent job's partial write observed mid-guard), env var name `AV_DISK_SAFETY_FACTOR` (float, mirrors the `AV_MAX_*`/`AUDIO_*` naming convention). No separate static floor env var is recommended — the proportional-to-actual-input-size check already fail-closes correctly for both tiny and huge inputs, unlike a single static byte constant which would either be too permissive for large inputs or too restrictive for small ones.

### FFmpeg version/CVE guard (recommended, mirrors whisper's `rev-parse` pattern, extended with a runtime `ffmpeg -version` fail-loud check)

```dockerfile
# Fail loud if the built binary doesn't report the expected version string --
# catches a configure/build misconfiguration silently linking a different
# ffmpeg found on PATH, not just a source-pin mismatch (belt-and-suspenders
# beyond the git rev-parse guard, which only proves the SOURCE was pinned
# correctly, not that the COMPILED BINARY is the one that ends up in the
# runtime stage).
RUN /usr/local/bin/ffmpeg -version | grep -q "ffmpeg version n\?8\.1\.2" \
 || { echo "FATAL: built ffmpeg does not report version 8.1.2" >&2; exit 1; }
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|-------------------|---------------|--------|
| `apt-get install ffmpeg` (Debian bookworm 5.1.x) | From-source build pinned to `n8.1.2` | This phase (D-01, operator directive 2026-07-22) | 5.1.x predates the CVE-2026-8461 fix (fixed in 8.1.2, released 2026-06-17); same-major-version parity with the already-validated Phase 34/35 dev-host ffmpeg (8.1.2) also preserves RTF/behavior validity |
| Full default ffmpeg build | `--disable-everything` + minimal `--enable-*` | This phase (research recommendation, not yet a locked decision) | Smaller image, smaller attack surface, structurally excludes network protocols beyond `file`/`crypto` even if the argv `-protocol_whitelist` flag were ever accidentally dropped in a future change |

**Deprecated/outdated:**
- STATE.md's Key Decision "pin ffmpeg ≥8.1.2, not floating apt-get install" and ROADMAP.md's Phase 36 SC1 text ("Debian apt 5.1.x with CVE backports") are both superseded by D-01's from-source decision — already noted in CONTEXT.md, restated here for completeness so this document doesn't accidentally re-cite the stale apt-backport framing.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|----------------|
| A1 | HEVC and x264 "veryfast" preset RTF are comfortably under real-time on the eventual compose CPU ceiling (2-4 cores) | Common Pitfalls / Pitfall 1 | If wrong, BOTH mp4 codec paths AND the webm path could be slow, compounding the NO-GO pressure on `AV_MAX_DURATION_SECONDS` beyond what this research anticipates. Must be measured, not assumed, exactly like VP9 |
| A2 | `avDiskSafetyFactor = 3.0` and no static floor is the right disk-guard formula shape | Code Examples | Under-provisioned: a burst of concurrent jobs on a small ephemeral volume could still exhaust disk between the guard check and job completion (guard is a point-in-time check, not a reservation/lock) — the container-level `deploy.resources` ephemeral-storage limit (Phase 37/Helm territory) is the actual hard backstop; this guard is defense-in-depth, not a complete solution |
| A3 | The recommended `AVConverter` struct-field refactor (Pitfall 4) is the right way to thread `AV_MAX_DURATION_SECONDS` through, vs. some other mechanism this session didn't consider | Common Pitfalls / Pitfall 4 | Architectural — affects task breakdown significantly. Flagged explicitly as needing planner judgment, not presented as settled |
| A4 | The full `--enable-decoder=...` list in Pattern 2 covers real-world client uploads | Architecture Patterns / Pattern 2 | If incomplete, previously-convertible real files start failing (regression vs. a hypothetical full-featured build) — mitigated by requiring the plan to run the live `av_test.go` suite against the built image before shipping, but the list itself is not exhaustively verified this session |
| A5 | `--enable-gpl --enable-nonfree` are required for `libx264`/`libx265` in this exact configure script | Architecture Patterns / Pattern 2 | LOW risk of being wrong (this is extremely standard, well-documented ffmpeg behavior across many major versions), but not independently re-verified by actually running `./configure` this session (only inspected via grep) |

## Open Questions

1. **How does `AV_MAX_DURATION_SECONDS` get threaded into `AVConverter.Convert()`, given the zero-field-struct/`init()`-registration shape?**
   - What we know: current consts are hardcoded, explicitly deferred to this phase; `cmd/av-worker/main.go` deliberately does NOT thread a duration parameter through `worker.NewHandler` the way audio does.
   - What's unclear: whether the recommended struct-field + re-register-at-startup approach (Pitfall 4) is actually the best fit, or whether some other mechanism (e.g., a package-level `SetAVLimits()` function called once from `main()`) is preferred.
   - Recommendation: planner should treat this as an explicit task with its own design note, not bundle it silently into the RTF-measurement task.

2. **Is VP9 (webm) or HEVC (mp4) the true worst-case matrix cell?**
   - What we know: one prior data point (`35-RESEARCH.md`, informal, single-run, 2 threads, Apple M3 Pro, 15s/1080p fixture) shows VP9 at RTF≈1.41, slower than real time. No equivalent data point exists yet for HEVC/x265 "veryfast" at the same settings.
   - What's unclear: whether HEVC is meaningfully faster, comparable, or also slow enough to matter, on THIS project's actual container CPU ceiling (not yet chosen — audio/document/chromium workers use `--cpus=2.0`, a reasonable default to mirror).
   - Recommendation: measure both in the matrix from the start (D-04 already specifies H.264/HEVC × 480/720/1080 as in-scope; webm/VP9 must be added as an explicit additional row, not treated as secondary).

3. **Does the "no resolution_height requested" (passthrough) transcode path need its own RTF measurement, given it's unbounded by the 480/720/1080 enum but bounded by the 4320 decode-bomb guard?**
   - What we know: a client CAN legally request no resize; output resolution then equals input resolution, up to 4320 (8K).
   - What's unclear: whether this is intended to be part of the "worst-case" contract AVE-04 describes, or an accepted residual risk (matching this project's established pattern of explicitly naming and accepting bounded residual risks rather than silently ignoring them).
   - Recommendation: at minimum, run one no-resize cross-check at a realistic large-but-plausible input (e.g. 4K/2160p) to get an empirical sense of the gap, and explicitly decide (with operator input, given D-05's judgment-on-safety-numbers framing) whether the true 4320 ceiling needs a lower, RTF-informed value or stays as today's decode-bomb-only ceiling.

4. **What is the actual production container CPU/RAM ceiling this phase should measure against?**
   - What we know: `document-worker`/`chromium-worker`/`audio-worker` all use `--cpus=2.0`/`memory: 1g` (audio) or `2g` (chromium); `AV_WORKER_CONCURRENCY` defaults to a provisional `2` today.
   - What's unclear: whether video transcode's heavier per-job cost (vs. audio's whisper-cli) argues for a different ceiling (e.g. `--cpus=2.0` but `memory: 2g`, matching chromium's heavier footprint) before RTF measurement even begins — the RTF numbers themselves are only valid at whatever ceiling they were measured against (exactly the caveat `32-03-SUMMARY.md` attaches to its own numbers).
   - Recommendation: mirror the `document-worker`/`audio-worker` `--cpus=2.0` starting point (consistent with the fleet), but treat `memory` as open pending a first measurement pass (mirrors how Phase 32 measured `AUDIO_WORKER_CONCURRENCY` empirically rather than guessing).

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|--------------|-----------|---------|----------|
| Docker | Image build, RTF measurement | ✓ | 29.4.0 | none needed |
| ffmpeg (local, for reference/dev) | Local `go run`/test comparison against the containerized build | ✓ | 8.1.2 (Homebrew, `/opt/homebrew/bin/ffmpeg`) | none needed — confirms the containerized pin target matches what the whole Phase 34/35 test suite was already validated against |
| kubectl / live k8s cluster | OrbStack compose/k8s mutual-exclusion precondition (mirrors `audio-rtf-measure.sh`'s own check) | kubectl present, cluster NOT live (connection refused, confirmed this session) | kubectl installed, no active cluster | Satisfies the precondition today; the RTF script itself re-checks this at run time, no fallback needed |
| Go toolchain | `go.mod` change (add `golang.org/x/sys/unix` import), `go build`/`go vet`/`go test` | ✓ | go1.26.5 (local) vs. `go 1.26.4` directive in `go.mod` | Toolchain directive is a minimum, not exact-match; no action needed |
| `golang.org/x/sys` module | Disk-guard implementation | ✓ (already resolved) | v0.44.0, currently indirect in `go.sum` | Promotes to direct on first import + `go mod tidy`; no new version resolution |

**Missing dependencies with no fallback:** none identified.
**Missing dependencies with fallback:** none identified.

## Security Domain

`security_enforcement` is absent from `.planning/config.json` — treated as enabled per protocol.

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|----------------|---------|--------------------|
| V2 Authentication | No | Unaffected — this phase touches only the worker/build layer, not the API auth path |
| V3 Session Management | No | Unaffected |
| V4 Access Control | No | Unaffected |
| V5 Input Validation | Yes | Already-closed `AVOpts` allowlist (`checkStrictObject`, Phase 34) is unaffected by containerization; the NEW disk-space guard (D-06) is itself an input-adjacent validation control (fail-closed on a resource-exhaustion vector before expensive work runs) |
| V6 Cryptography | No (narrowly) | No new cryptographic primitive is introduced. The commit-hash pin (`git rev-parse` guard) is a **supply-chain integrity** control, not a cryptographic one in the ASVS V6 sense — noted separately below |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|------------------------|
| Supply-chain tampering: ffmpeg source tag force-moved to inject malicious code | Tampering | `git rev-parse HEAD` fail-loud guard after `checkout --detach` to the pinned commit (Pattern 1) — build fails rather than silently compiling different code |
| CVE-2026-8461 (PixelSmash) MagicYUV decoder heap OOB write, RCE-capable | Elevation of Privilege / Tampering | Building `n8.1.2` (verified to contain the fix) rather than the vulnerable apt 5.1.x default. Secondary defense-in-depth: the minimal `--disable-everything` build could go further and explicitly `--disable-decoder=magicyuv` (the vendor-recommended workaround for anyone who CAN'T upgrade) even though this project already upgrades — cheap additional hardening since MagicYUV is not in this project's supported codec list at all |
| Disk exhaustion via large/many concurrent video uploads filling container ephemeral storage | Denial of Service | NEW this phase: `EnforceMinFreeDisk` fail-closed guard before transcode (D-06) — same fail-closed-before-expensive-work discipline as the existing duration/resolution guards |
| SSRF/LFI via ffmpeg's protocol handling (HLS/concat/subtitle references embedded in file content) | Tampering / Information Disclosure | Already mitigated at the argv layer (AVE-02, `-protocol_whitelist file,crypto`, unaffected by this phase). This phase ADDS a second, structural layer: a `--disable-everything` build with only `--enable-protocol=file,crypto` makes protocols like `http`/`https`/`rtmp`/`concat` **not compiled in at all** — even a future code change that accidentally dropped the `-protocol_whitelist` argv flag could not reach a network protocol, because the binary itself doesn't contain one |
| Decode-bomb via oversized/malformed video streams | Denial of Service | Already mitigated (AVE-02, `EnforceMaxDuration`/`EnforceMaxResolution`, unaffected by this phase) — noted only to confirm containerization does not regress it; see Anti-Patterns re: the 4320/no-resize gap |
| CPU/RAM oversubscription under container limits (CFS throttling, OOM) | Denial of Service | `CgroupCPULimit()` (existing, reused verbatim, D-07) sizes `-threads`; this phase additionally measures and sets a real `AV_WORKER_CONCURRENCY` from peak-RSS/cpu-fit checks (mirrors `AUDIO_WORKER_CONCURRENCY`'s Phase 32 methodology) rather than the current provisional `2` |

## Sources

### Primary (HIGH confidence — verified live this session via git/apt/go.sum, not training data)

- `github.com/FFmpeg/FFmpeg` — `git ls-remote --tags` (latest stable 8.x tag enumeration), `git rev-list -n 1 n8.1.2` (peeled commit resolution), `git merge-base --is-ancestor` (CVE fix commit ancestry), `git show n8.1.2:configure` / `:libavcodec/allcodecs.c` / `:libavformat/allformats.c` (component name + runtime-cpudetect-default verification) — all run live, 2026-07-22
- Live Debian `bookworm-slim` container — `apt-cache search`/`apt-cache policy` for `libx264-dev`/`libx264-164`, `libx265-dev`/`libx265-199`, `libvpx-dev`/`libvpx7`, `libopus-dev`/`libopus0`, `libmp3lame-dev`/`libmp3lame0`, `libwebp-dev`/`libwebp7`, `nasm`, `yasm` — run live, 2026-07-22
- `go.sum` (this repo) — confirmed `golang.org/x/sys v0.44.0` already resolved as an indirect dependency
- `.planning/milestones/v1.7-phases/32-containerization-local-e2e-rtf-gate/32-03-SUMMARY.md` — the exact Phase 32 RTF derivation formula, NO-GO lever mechanics, and raw measurement methodology this phase must mirror
- `.planning/phases/35-queue-worker-routing-integration/35-RESEARCH.md` — the existing live VP9/`transcodeToWebMArgs` measurement (21.2s/15s, RTF≈1.41, Apple M3 Pro, 2 threads) that motivates Pitfall 1
- `docker-compose.yml`, `.env.example`, `.github/workflows/ci.yml` (this repo) — confirmed exact current state of AV_* env parity gaps (D-08) and the CI bake matrix pattern to extend

### Secondary (MEDIUM confidence — WebSearch cross-referenced with the primary git verification above)

- [FFmpeg fixes PixelSmash flaw in widely used video decoder — BleepingComputer](https://www.bleepingcomputer.com/news/security/ffmpeg-fixes-pixelsmash-flaw-in-widely-used-video-decoder/)
- [PixelSmash (CVE-2026-8461): Critical FFmpeg Flaw — JFrog](https://jfrog.com/blog/pixelsmash-critical-ffmpeg-vulnerability-turns-media-files-into-weapons/) — root-cause description ("inconsistency between how the frame allocator and the decoder compute chroma plane heights") that semantically matches the git-verified fix commit's message ("reject slice_height misaligned with chroma vshift")
- [Live encoding with VP9 using FFmpeg — Google Developers](https://developers.google.com/media/vp9/live-encoding) — `-speed`/`-cpu-used` recommended values for real-time encoding (5-8), corroborating that the DEFAULT (unset) is far from real-time-tuned

### Tertiary (LOW confidence — general community knowledge, not independently re-verified this session)

- General claim that x264/x265 "veryfast" preset RTF is typically closer to real-time than default-tuned libvpx-vp9 at comparable settings — widely stated across encoding community resources but not measured against this project's actual code paths this session (see Assumption A1, Open Question 2)
- Exact numeric default for libvpx-vp9's `-cpu-used`/`-speed` when left completely unset (multiple sources describe the qualitative "good deadline is slow by default" behavior; none consulted this session pinned the exact default integer value with citation-grade confidence)

## Metadata

**Confidence breakdown:**
- FFmpeg pin + CVE verification: HIGH — verified live via `git` directly against the primary source repository, cross-referenced with independent security-industry writeups
- Minimal codec build flag list: MEDIUM — component names and apt package names verified against the actual pinned source tree and live Debian index; full-list *completeness* against real-world client uploads is explicitly unverified, flagged for a live smoke-test gate in the plan
- Cross-arch portability: HIGH — verified by reading the actual pinned `configure` script's default behavior
- Disk-space guard: MEDIUM — dependency choice (`x/sys/unix`) and general shape are solid; the exact safety-factor formula/threshold is a reasoned recommendation with no precedent to validate against, explicitly flagged `[ASSUMED]`
- RTF matrix shape / worst-case cell identification: MEDIUM-HIGH — grounded in this project's own prior live measurement data (`35-RESEARCH.md`), not a guess, but the full matrix has not yet been run

**Research date:** 2026-07-22
**Valid until:** ~14 days (shorter than the usual 30-day stable-domain default: this research pins a specific just-released ffmpeg security patch version and cites a very recent CVE disclosure — re-verify the "latest stable 8.x" claim via `git ls-remote` at plan/execution time in case a newer point release ships in the interim)
