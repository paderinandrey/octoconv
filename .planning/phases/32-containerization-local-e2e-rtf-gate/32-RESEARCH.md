# Phase 32: Containerization & Local E2E + RTF Gate - Research

**Researched:** 2026-07-18
**Domain:** Multi-stage Docker packaging of a from-source C/C++ build (whisper.cpp) into an existing Go worker-image family; live compose E2E; measured realtime-factor (RTF) go/no-go gate sizing a production timeout; CI bake-matrix extension
**Confidence:** HIGH for OctoConv-internal patterns (5 existing Dockerfiles, docker-compose.yml, docker-compose.e2e.yml, ci.yml, internal/e2e/e2e_test.go, internal/queue/client.go, cmd/audio-worker/main.go, internal/convert/whisper.go all read directly this session); MEDIUM for whisper.cpp cgroup-CPU-detection mechanics and exact RTF numbers (no first-party benchmark exists on directly comparable hardware — this phase's own measurement gate is the resolution mechanism, mirroring Phase 23's veraPDF JVM cold-start gate)

## Summary

This phase has zero new *architectural* ground to break — every piece (multi-stage Dockerfile, compose service block, CI bake target, `internal/e2e` test) has 3-5 existing siblings to copy verbatim and adapt. What makes it non-trivial is that three artifacts (`AUDIO_ENGINE_TIMEOUT`, `--threads`, `AUDIO_WORKER_CONCURRENCY`) cannot be filled in from research alone — Phase 30/31's own research explicitly deferred them here as "measure, don't assume" (STATE.md Key Decisions), and the container is the first environment where the real cgroup CPU/memory ceiling exists to measure against. The phase's own success criteria are explicit about this: SC3 is a go/no-go gate, not a target to hit.

Three concrete, already-diagnosed gaps must close in this phase, found by direct code reads in this session, not by hypothesis: (1) **IN-04** (31-REVIEW.md) — `docker-compose.yml` has zero `audio-worker` service; the API already accepts and enqueues audio jobs (`AudioConverter` is registered in `convert.Default` via `converters.go`'s `init()`) that currently sit queued forever in the compose stack. (2) **IN-02** (31-REVIEW.md) — `queue.NewClient()` (`internal/queue/client.go:70-95`) reads `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT` from env **unconditionally in every process that constructs a `queue.Client`** (api, worker, document-worker, chromium-worker, both webhook-workers, and the new audio-worker) — this is the existing, accepted `DEBT-05` pattern already visible for `DOCUMENT_*`/`HTML_*` vars in `docker-compose.yml`'s `api`/`worker`/`document-worker`/`chromium-worker` blocks (webhook-worker-1/2 currently omit them, relying on matching Go defaults). **Once this phase changes `AUDIO_ENGINE_TIMEOUT` off its 600s placeholder, the new value must be added identically to every one of those seven service blocks, not just the new `audio-worker` service** — omitting it anywhere reopens the exact T-03-10 double-processing race `AudioUniqueTTL` exists to close (the API/webhook-worker would derive a lock TTL from a stale 600s while the real attempt runs the measured, larger timeout). (3) `--threads`/cgroup-CPU sizing has no existing OctoConv precedent (the other three engines are single-threaded-by-nature CLI invocations) — `internal/convert/whisper.go`'s `whisperArgs` function is the exact, already-identified injection point (no `-t`/`--threads` flag is currently passed).

The RTF measurement itself has a strong, directly reusable in-repo template: `scripts/verapdf-measure.sh` (Phase 23) already solved "measure a real subprocess's wall-clock cost inside a resource-limited container, compute nearest-rank p95, assert a budget, exit non-zero on NO-GO" for veraPDF's JVM cold start. The audio RTF gate is structurally identical (build the real image → run N timed invocations inside a `--cpus`/`--memory`-limited container matching `docker-compose.yml`'s eventual limits → nearest-rank p95 → GO/NO-GO verdict written into the phase's SUMMARY), with two audio-specific additions: (a) the metric is `wall_clock / audio_duration` (RTF), not raw wall-clock, so a **known-duration** audio fixture is needed — the only committed fixture close to production-realistic length is `jfk.wav` (11s); a synthetic, deterministic longer fixture (ffmpeg `sine`/spoken-word concat) should be generated for a meaningful RTF measurement, and (b) the container's actual cgroup `cpu.max` must be read and cross-checked against the `--threads` value actually passed to `whisper-cli`, closing the loop between SC3 (timeout sizing) and SC4 (thread/concurrency sizing) in one measurement pass.

**Primary recommendation:** Build `Dockerfile.audio-worker` as a three-stage build (Go build stage — verbatim; whisper.cpp build stage — new, `-DGGML_NATIVE=OFF` mandatory; slim runtime stage) following `.planning/research/STACK.md`'s already-validated Dockerfile skeleton almost verbatim, wire the compose service + CI bake target by direct structural copy of `document-worker`'s pattern, then run a `verapdf-measure.sh`-shaped RTF gate script against the real built image on a real synthetic longer-duration fixture before writing a final (non-placeholder) `AUDIO_ENGINE_TIMEOUT` anywhere — and when that value changes, propagate it to all seven `queue.NewClient()`-constructing compose services in the same commit, not just `audio-worker`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| whisper.cpp/ffmpeg binary packaging (multi-stage Dockerfile) | Build / CI | — | Pure build-time artifact assembly, mirrors `Dockerfile.document-worker`'s veraPDF-stage pattern exactly |
| Model bake-in (SHA-256-pinned `ggml-base.bin`) | Build / CI | — | Build-time `COPY`, no runtime fetch (offline constraint); Phase 33 revisits bake-vs-volume based on this phase's Container Size Budget measurement, not this phase's job to decide |
| `audio-worker` compose service definition | Database / Storage-adjacent orchestration (compose is infra, not app tier) | — | Same tier as the six existing worker/api compose service blocks; no new tier introduced |
| `--threads` sizing (cgroup CPU-limit read) | API / Backend (`cmd/audio-worker` process, Go) | — | The Go process, not whisper-cli itself, must read the container's real cgroup limit and pass an explicit flag — whisper-cli's own default (host core count) is documented-wrong under any cgroup CPU quota (PITFALLS.md Pitfall 5) |
| `AUDIO_WORKER_CONCURRENCY` sizing (measured RSS/CPU) | API / Backend (asynq server config in `cmd/audio-worker/main.go`) | — | Process-level concurrency knob, same tier as the existing `DOCUMENT_WORKER_CONCURRENCY`/`HTML_WORKER_CONCURRENCY` precedent |
| `AUDIO_ENGINE_TIMEOUT` propagation across enqueuing processes | API / Backend (cross-process env consistency: api, worker, document-worker, chromium-worker, webhook-worker-1/2, audio-worker) | — | `queue.NewClient()`'s unconditional-read pattern (DEBT-05) makes this a cross-process consistency concern, not a single-service config value (IN-02) |
| RTF measurement script | Build / CI (offline, `scripts/`, not shipped in any runtime image) | — | Mirrors `scripts/verapdf-measure.sh`'s tier exactly — a one-shot measurement tool run at phase-execution/CI time, never part of the served application |
| `TestAudioConversionE2E` | API / Backend test (drives the same live HTTP surface `TestImageConversionE2E`/`TestDocumentConversionE2E` already exercise) | — | Lives in `internal/e2e`, exercises the full stack through the public API — same tier as every other E2E test in the suite |
| CI bake matrix entry | Build / CI | — | `.github/workflows/ci.yml`'s `docker-build`/`e2e` jobs, `docker/bake-action` reading `docker-compose.yml` directly — no separate Dockerfile-specific CI config exists in this repo |

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| AUD-06 | `cmd/audio-worker` + `Dockerfile.audio-worker` (whisper.cpp v1.9.1 from source multi-stage, `-DGGML_NATIVE=OFF`, `base` model baked with pinned SHA-256, apt ffmpeg) + `AUDIO_ENGINE_TIMEOUT`/`AUDIO_WORKER_CONCURRENCY`/ShutdownTimeout env + compose service + CI bake matrix | `cmd/audio-worker` already exists and is live-proven (Phase 31 SUMMARY) — this phase's remaining scope is purely the Dockerfile + compose + CI wiring; see "Dockerfile.audio-worker" and "CI Bake Matrix" sections below |
| AUD-07 | RTF measured on a real resource-limited container (measured go/no-go, veraPDF Phase 23 precedent) before finalizing `AUDIO_ENGINE_TIMEOUT`/KEDA cooldown/stabilization | "RTF Measurement Methodology" section — direct reuse of `scripts/verapdf-measure.sh`'s structure, adapted for RTF (not raw wall-clock) and a synthetic longer fixture |
</phase_requirements>

## Standard Stack

### Core

| Tool | Version | Purpose | Why Standard |
|------|---------|---------|---------------|
| `ggml-org/whisper.cpp` (`whisper-cli`) | **v1.9.1** — LOCKED, already pinned and live-proven in Phase 30/31 | Offline CPU speech-to-text | `[CITED: .planning/research/STACK.md, live-verified GitHub Releases API 2026-07-17]` No re-verification needed this phase — the binary/version decision was already made and is already running successfully in `cmd/audio-worker`'s live E2E (31-04-SUMMARY.md) |
| `ffmpeg` | Debian bookworm apt-pinned: `7:5.1.9-0+deb12u1` | Stage-1 normalize (arbitrary container → 16kHz mono s16 WAV) | `[CITED: .planning/research/STACK.md, live apt-cache policy check]` Already the exact version every local dev/E2E run has used |
| `ggml-base.bin` model | SHA-256 `60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe`, 147,951,465 bytes | Baked-in acoustic/language model | `[CITED: .planning/research/STACK.md]` Confirmed byte-identical to the file already used in Phase 31's live E2E (`31-04-SUMMARY.md`: `~/.cache/whisper/ggml-base.bin`, 147,951,465 bytes) — **re-verify this SHA-256 live against HuggingFace at execution time** (STACK.md's own caution: "HuggingFace file content is stable but re-verify rather than trust a value transcribed days earlier") |
| `debian:bookworm-slim` | Same base as all 5 existing worker images | Runtime stage base | `[VERIFIED: direct read of Dockerfile.worker/.document-worker/.chromium-worker/.webhook-worker]` |

No new Go dependencies. No npm/pip packages. The Package Legitimacy Gate protocol (slopcheck, registry verification) has no target this phase — every new artifact is a system/source-built binary via `apt-get`/`cmake`/`git clone --branch`, identical in kind to what `Dockerfile.document-worker`/`Dockerfile.chromium-worker` already do for veraPDF/chromium-headless-shell.

### Package Legitimacy Audit

Not applicable — no `npm install`/`pip install`/`go get` targets exist in this phase's scope. All new build inputs are apt packages (`build-essential`, `cmake`, `git`, `ffmpeg`, `ca-certificates`) or a `git clone --branch v1.9.1` of the already-pinned upstream repo, plus a `curl`-fetched, SHA-256-verified model file — none of these route through a language package manager the slopcheck/registry-verification gate is designed for.

## Dockerfile.audio-worker

### Recommended structure (3-stage, adapted from `.planning/research/STACK.md`'s already-drafted skeleton, cross-checked against `Dockerfile.document-worker`'s veraPDF-stage precedent)

```dockerfile
# Stage 1: Go build (byte-identical pattern to every existing *-worker Dockerfile)
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/audio-worker ./cmd/audio-worker

# Stage 2: whisper.cpp build (new — mirrors document-worker's verapdf stage
# in SHAPE: a separate, throwaway build stage whose only job is producing an
# artifact the runtime stage COPYs, never rebuilding the toolchain there)
FROM debian:bookworm-slim AS whisper-build
RUN apt-get update && apt-get install -y --no-install-recommends \
      build-essential cmake git ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch v1.9.1 \
      https://github.com/ggml-org/whisper.cpp.git /whisper
WORKDIR /whisper
# -DGGML_NATIVE=OFF is LOAD-BEARING (STACK.md "What NOT to Use"): the
# default (native ON) compiles -march=native for the BUILD host's exact CPU
# (CI runner / Apple Silicon OrbStack host cross-building linux/amd64), and
# whisper-cli SIGILLs on any runtime host lacking those exact instruction
# extensions. Do not flip this to chase perf.
RUN cmake -B build -DGGML_NATIVE=OFF -DCMAKE_BUILD_TYPE=Release \
    && cmake --build build -j --target whisper-cli --config Release
# Pin the model by content hash, never trust HuggingFace's mutable `main`
# pointer at build time (mirrors verapdf/chromium/MinIO pinned-tag discipline
# already used elsewhere in this repo). RE-VERIFY this hash live at
# execution time before committing it -- see Assumptions Log A1.
RUN curl -L --fail --retry 5 --retry-delay 5 \
      -o /models/ggml-base.bin \
      https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin \
    && echo "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe  /models/ggml-base.bin" | sha256sum -c -

# Stage 3: runtime
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*
COPY --from=whisper-build /whisper/build/bin/whisper-cli /usr/local/bin/whisper-cli
COPY --from=whisper-build /models/ggml-base.bin /models/ggml-base.bin
COPY --from=build /out/audio-worker /usr/local/bin/audio-worker
# Single synchronous two-stage CLI invocation per job (ffmpeg, then
# whisper-cli) -- no forking daemon like soffice.bin/chromium, so (matching
# Dockerfile.worker's own documented rationale for the image engine) no
# tini/init-as-PID-1 is needed here.
USER nobody
ENTRYPOINT ["/usr/local/bin/audio-worker"]
```

**Deviations to verify at execution time, not assume:**
- `ldd /whisper/build/bin/whisper-cli` inside the whisper-build stage (or a throwaway intermediate `RUN` before the final COPY) to confirm which runtime `.so`s beyond libc/libstdc++ it needs — STACK.md flags `libgomp1` as the likely OpenMP dependency but this was not empirically confirmed against a real build in any session to date. If `ldd` shows `libgomp.so.1 => not found` in the runtime stage, add `libgomp1` to the runtime `apt-get install` line.
- No arm64/amd64 cross-build pin is needed here (unlike `document-worker`'s `platform: linux/amd64` veraPDF constraint) — whisper.cpp builds natively for whatever platform the build stage targets, and `-DGGML_NATIVE=OFF` is specifically what makes this safe across CI (likely amd64 GitHub Actions runners) vs. local OrbStack (arm64 Apple Silicon) without a platform pin. Confirm this holds by building on both if feasible, but do not add a `platform:` pin preemptively — nothing in this stack requires one, unlike the veraPDF case.

### Container Size Budget — MEASURE, do not trust the additive estimate

`.planning/research/STACK.md` estimated ~400-450MB total (base model only) by adding verified component sizes, explicitly flagged LOW-MEDIUM confidence ("not measured against an actually-built image"). This phase must run `docker image inspect octoconv-audio-worker --format='{{.Size}}'` (or `docker images`) against the real built image and record the actual number — this is a direct input to Pitfall 8 (scale-from-zero risk, Phase 33's concern) and should be captured in this phase's SUMMARY even though the scale-from-zero decision itself is out of this phase's scope (compose-only, no KEDA/Helm per the scope fence).

## RTF Measurement Methodology (AUD-07)

### Direct precedent: `scripts/verapdf-measure.sh` (Phase 23, `.planning/milestones/v1.5-phases/23-verapdf-validation/23-01-SUMMARY.md`)

That script (read in full this session) already solved the exact shape of problem this phase needs: build the real image → start a long-lived container with the SAME resource limits `docker-compose.yml` will eventually use (`--memory=1g --cpus=2.0`, matching this repo's existing worker ceiling convention) → run N timed invocations of the real subprocess **inside** the container via `docker exec` (not from the host, to avoid inflating the measurement with docker-exec IPC overhead) → sort ascending → nearest-rank p95 (`rank = ceil(0.95 * N)`) → assert against a budget → exit non-zero on NO-GO. It also captured peak memory via `/sys/fs/cgroup/memory.peak` (cgroup v2) as a companion observation, not a hard gate.

### Audio-specific adaptations

1. **Metric is RTF, not raw wall-clock.** `RTF = wall_clock_seconds / audio_duration_seconds`. This requires a fixture of **known, non-trivial duration** — `jfk.wav` (11s, the only long-ish existing fixture) is too short to produce a stable, representative RTF measurement (model load time / process-spawn overhead dominates a sub-15-second clip disproportionately, per STACK.md's own sizing caveat: "several hundred ms-seconds" of load time). Recommend generating a synthetic, deterministic fixture via ffmpeg (not committed audio content that might carry licensing/PII concerns for a longer clip) — two viable approaches:
   - **Silence/tone** (fastest to generate, deterministic, but does not exercise real ASR compute the way spoken content does — whisper.cpp's compute cost is not silence-invariant in every implementation, but is a reasonable *lower-bound* RTF signal): `ffmpeg -f lavfi -i "sine=frequency=440:duration=300" -ar 16000 -ac 1 -c:a pcm_s16le rtf_fixture.wav` for a 5-minute synthetic fixture.
   - **Concatenated real speech** (more representative of production audio, still deterministic): loop/concat the existing `jfk.wav`/`sample.wav` fixtures N times via `ffmpeg -f concat` to reach several minutes, accepting the repeated-content artifact as acceptable for a *timing* measurement (not a transcript-quality measurement).
   - **Recommendation:** use the concatenated-speech approach for the primary RTF number (closer to a real meeting/lecture RTF profile) and optionally cross-check with a silence-only run as a floor sanity check — both are cheap (`ffmpeg` is already a build/runtime dependency, no new tooling).
2. **`--threads` must be explicitly set and recorded as part of the same measurement run** — RTF is meaningless without knowing the thread count that produced it. Run the measurement with `-t <container-cgroup-derived-thread-count>` (see next section), not whisper-cli's undocumented-in-this-container default.
3. **Container CPU/memory limits must match `docker-compose.yml`'s eventual `audio-worker` block** — decide the `cpus`/`memory` ceiling for the audio service (existing precedent: `document-worker`/`chromium-worker` both use `cpus: "2.0"`, `memory: 1g`/`2g` respectively) BEFORE running the measurement, since RTF is CPU-count-sensitive; measuring against different limits than what ships in compose invalidates the sizing conclusion.
4. **GO/NO-GO budget derivation:** unlike veraPDF's fixed 10s D-01 budget (an externally-given constraint), `AUDIO_ENGINE_TIMEOUT`'s budget is *derived from* the RTF measurement, not compared against a pre-existing number — the phase's own job is to compute `AUDIO_ENGINE_TIMEOUT ≈ measured_RTF_p95 × MAX_UPLOAD-implied_max_audio_duration × safety_margin`, then decide whether that resulting timeout is *operationally acceptable* (i.e., does the implied worst-case job duration break other invariants — `RECONCILER_ACTIVE_STALE_AFTER` must stay below it, `ShutdownTimeout` must accommodate it, `AudioUniqueTTL` scales with it). A NO-GO outcome here looks like "the RTF is bad enough that the resulting timeout is operationally absurd (multi-hour) for the current `AUDIO_MAX_DURATION_SECONDS=4h` ceiling" — which would be a signal to revisit `AUDIO_MAX_DURATION_SECONDS` (lower it) rather than to accept an unbounded timeout, a decision this phase should surface explicitly (Open Questions below), not silently resolve either direction.
5. **Record raw per-run numbers in the SUMMARY**, exactly as `23-01-SUMMARY.md` did (`Raw per-run wall-clock (ms), in execution order: ...` / `Sorted ascending: ...` / `p95 (rank N) = ...`) — this is the auditable evidence trail the go/no-go verdict rests on, and Phase 33 will cite this number directly for KEDA cooldown/stabilization tuning (ROADMAP: "this measured timeout is the hard input to Phase 33's KEDA tuning").

### `--threads` / cgroup CPU-limit detection (AUD-06/AUD-07 SC4)

**No existing OctoConv precedent** — the other three engines (libvips, LibreOffice, chromium) are each single CLI invocations that don't expose a comparable thread-count knob the worker process must compute (LibreOffice/chromium are each internally single-process-per-conversion regardless of host cores; libvips' own internal threading was never tuned in this codebase). This is genuinely new territory for the project.

**Mechanism (cgroup v2, MEDIUM confidence — mechanism is well-documented cgroup v2 API, not independently live-tested against a running OctoConv container in this session):**

```
$ cat /sys/fs/cgroup/cpu.max
200000 100000
```

Format is `$QUOTA $PERIOD` in microseconds (or literal `max $PERIOD` if unlimited). `cpus_available = quota / period` — for Docker's `--cpus=2.0` (matching `docker-compose.yml`'s existing `cpus: "2.0"` convention on `document-worker`/`chromium-worker`), this resolves to `200000/100000 = 2`. Round up or down deliberately (recommend floor, not ceil, to avoid over-subscribing threads beyond the quota — matches the "don't request more than you're given" caution PITFALLS.md Pitfall 5 raises about CFS throttling from over-threading).

```go
// Illustrative shape only — not verified against a running container this
// session. Verify at Wave 0 by building the image and running
// `cat /sys/fs/cgroup/cpu.max` inside it (docker exec) before trusting this
// function in production.
func cgroupCPULimit() (int, bool) {
	b, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 || fields[0] == "max" {
		return 0, false // unlimited or unparseable — caller must fall back
	}
	quota, err1 := strconv.ParseFloat(fields[0], 64)
	period, err2 := strconv.ParseFloat(fields[1], 64)
	if err1 != nil || err2 != nil || period == 0 {
		return 0, false
	}
	n := int(quota / period) // floor, deliberately not ceil
	if n < 1 {
		n = 1
	}
	return n, true
}
```

**Design recommendation (mirrors the project's `env-only-in-main` + setter convention already used for `AUDIO_MODEL_PATH`/`VERAPDF_TIMEOUT`):** compute this ONCE at `cmd/audio-worker/main.go` startup (not per-job — the cgroup limit does not change mid-process for a fixed compose/k8s resource block), with an explicit `AUDIO_THREADS` env override taking precedence when set (operational escape hatch, matches the project's existing pattern of every tunable having an env override), falling back to the cgroup-detected value, falling back to `runtime.NumCPU()` if cgroup detection fails (e.g., cgroup v1 host, or running outside any container — local `go run` dev flow, which Phase 30/31's local E2E already exercises). Thread the result through a new `convert.SetAudioThreads(n int)` setter (same shape as `SetAudioModelPath`), consumed by a new `-t <n>` argument inside `whisperArgs` (`internal/convert/whisper.go`) — **the exact, already-identified injection point**: `whisperArgs` currently builds `[]string{"-m", model, "-f", normPath, "-of", outBase}` plus output/language/translate flags with no thread flag at all (confirmed by direct read this session).

**Verify at Wave 0, not assume:** cgroup v2 availability inside the target containers. OrbStack (this project's documented dev/gate environment) and modern Docker Desktop/Compose default to cgroup v2 on the host Linux VM, but this was not independently re-confirmed against a real `docker compose up` container in this research session — a 5-minute `docker exec octoconv-audio-worker cat /sys/fs/cgroup/cpu.max` spot-check should be the very first Wave 0 task before writing any detection code, exactly mirroring how Phase 23's Plan 01 verified the musl/glibc JRE boundary live before committing to a packaging approach.

### `AUDIO_WORKER_CONCURRENCY` sizing (AUD-06/AUD-07 SC4)

PITFALLS.md Pitfall 5 (already-researched, HIGH confidence on the *shape* of the risk, MEDIUM on WebSearch-sourced specifics) predicts concurrency should land on **1** for audio: whisper-cli will want to consume most/all of the container's CPU budget per job via `--threads`, so running 2+ concurrent transcriptions on the same `cpus: 2.0` container would starve both and blow the RTF assumption the timeout was sized against. `.env.example` currently ships `AUDIO_WORKER_CONCURRENCY=2` as a placeholder (explicitly flagged "Phase 32 re-sizes from measured per-job RSS"). Measure actual peak RSS for a single `base`-model transcription (via the same `/sys/fs/cgroup/memory.peak` technique `verapdf-measure.sh` already uses) and cross-check: does `AUDIO_WORKER_CONCURRENCY × peak_RSS_per_job` fit under the container's `memory` limit with margin, AND does `AUDIO_WORKER_CONCURRENCY × threads_per_job` fit under the `cpus` limit without oversubscription? Given `--threads` is sized to consume the FULL cgroup CPU quota per job (previous section), concurrency > 1 is very likely to fail the second check by construction — record this reasoning explicitly in the SUMMARY rather than treating "1" as a given without the measurement, since the phase's own goal is a *measured*, not assumed, decision (STATE.md's whisper.cpp threads/concurrency Key Decision: "size `AUDIO_WORKER_CONCURRENCY` from measured per-job RSS (likely 1)").

## Compose Service Wiring

### `audio-worker` service block — structural copy of `document-worker`'s shape (no `platform:` pin needed, see above)

```yaml
  audio-worker:
    build:
      context: .
      dockerfile: Dockerfile.audio-worker
    container_name: octoconv-audio-worker
    restart: always
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      minio:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://octo:octo-pass@postgres:5432/octo_db
      REDIS_ADDR: redis:6379
      S3_ENDPOINT: minio:9000
      S3_ACCESS_KEY: minioadmin
      S3_SECRET_KEY: minioadmin
      S3_BUCKET: octoconv
      S3_USE_SSL: "false"
      AUDIO_WORKER_CONCURRENCY: "<measured, likely 1>"
      AUDIO_ENGINE_TIMEOUT: "<measured RTF-derived value, NOT 600s>"
      AUDIO_MAX_RETRY: "3"
      AUDIO_MAX_DURATION_SECONDS: "14400"
      AUDIO_MODEL_PATH: "/models/ggml-base.bin"
      # AUDIO_THREADS: left unset -- cgroup auto-detection is the default
      # path; only set explicitly if the measurement gate recommends an
      # override different from the auto-detected value.
      # DEBT-05 (IN-02): queue.NewClient() reads ALL engine-class MAX_RETRY/
      # TIMEOUT vars unconditionally -- also set the image/document/html
      # vars here so this process's derived TTLs match every sibling
      # service, same as document-worker/chromium-worker's existing blocks.
      IMAGE_MAX_RETRY: "4"
      ENGINE_TIMEOUT: "120s"
      DOCUMENT_MAX_RETRY: "3"
      DOCUMENT_ENGINE_TIMEOUT: "300s"
      HTML_MAX_RETRY: "3"
      HTML_ENGINE_TIMEOUT: "60s"
      METRICS_ADDR: "127.0.0.1:9090"
    deploy:
      resources:
        limits:
          cpus: "<matches the RTF measurement container's cpus>"
          memory: "<matches the RTF measurement container's memory>"
```

### CRITICAL — IN-02 propagation (31-REVIEW.md, verified this session against the live `docker-compose.yml`)

`AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY` are **currently absent from every service block** in `docker-compose.yml` (`api`, `worker`, `document-worker`, `chromium-worker`, `webhook-worker-1`, `webhook-worker-2` — confirmed by direct read this session, none of the six existing blocks mention any `AUDIO_*` variable). Every one of those six processes constructs a `queue.Client` via `queue.NewClient()`, which reads `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT` from env unconditionally (confirmed by direct read of `internal/queue/client.go:70-95`) and derives `audioUniqueTTL` from whatever it finds (falling back to the 600s/3 code defaults when unset). **This currently causes no drift only because nothing has changed the default yet.** The moment this phase sets a real, measured `AUDIO_ENGINE_TIMEOUT` (almost certainly ≠ 600s) on the new `audio-worker` service alone, the other six processes silently keep deriving `audioUniqueTTL` from the stale 600s default — reopening the T-03-10 double-processing race for exactly the class `AudioUniqueTTL` was built to protect. **Action: add the SAME measured `AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY` values to `api`, `worker`, `document-worker`, `chromium-worker`, `webhook-worker-1`, `webhook-worker-2`, AND `audio-worker` in one commit** — mirroring exactly how `DOCUMENT_ENGINE_TIMEOUT`/`HTML_ENGINE_TIMEOUT` are already explicitly duplicated across `api`/`worker`/`document-worker`/`chromium-worker` (though notably NOT yet across `webhook-worker-1`/`webhook-worker-2`, which currently rely on matching code defaults for those two classes too — this phase is a natural point to also close that pre-existing gap for `AUDIO_*`, even if it doesn't fix the older `DOCUMENT_*`/`HTML_*` omission on webhook-workers, which is out of this phase's scope).

## CI Bake Matrix

`.github/workflows/ci.yml`'s `docker-build` and `e2e` jobs both use `docker/bake-action@v7` with `files: docker-compose.yml` — bake auto-derives targets from compose service `build:` blocks, so **adding the `audio-worker` service to `docker-compose.yml` is sufficient for bake to discover it as a new target; no separate bake config file exists in this repo.** The only manual wiring needed is adding cache scope lines to both jobs' `set:` blocks, following the exact per-service pattern already used for `document-worker`/`chromium-worker`:

```yaml
# docker-build job (cache-to AND cache-from):
audio-worker.cache-to=type=gha,mode=max,scope=audio-worker
audio-worker.cache-from=type=gha,scope=audio-worker

# e2e job (cache-from only, matching the existing 6-line pattern):
audio-worker.cache-from=type=gha,scope=audio-worker
```

No change needed to the `e2e` job's `docker compose ... up -d` step — it brings up every service defined in `docker-compose.yml` (no explicit service-name list is passed), so `audio-worker` is automatically included once the service block exists. `docker-build`'s 20-minute `timeout-minutes` and the "Free disk space" step (already present for GHA runner disk-pressure reasons, per the tier-3 comment) may need headroom reassessment once the audio image's real size is measured — a multi-hundred-MB `cmake --build` compile stage adds real CI time; consider whether GHA's layer cache (`type=gha,mode=max`) sufficiently amortizes this after the first run, and flag if the 20-minute budget looks tight after a live timing observation, rather than pre-emptively raising it without evidence.

**No `platform: linux/amd64` pin needed** for `audio-worker` (unlike `document-worker`'s veraPDF-forced pin) — GitHub Actions runners are amd64-native, and OrbStack (arm64 local dev) both build the same multi-stage Dockerfile without cross-platform binary copying, since whisper.cpp is compiled from source in both cases with `-DGGML_NATIVE=OFF` making the result portable.

## `TestAudioConversionE2E` (USER-AGREED MUST-HAVE, not on the original ROADMAP success-criteria list but explicitly required per chat)

### Nearest template: `TestImageConversionE2E` (`internal/e2e/e2e_test.go:1096-1121`)

Chosen over `TestDocumentConversionE2E`/`TestHTMLConversionE2E` because it is the smallest, single-fixture-plus-webhook shape (SC2's requirement — "upload → poll → presigned transcript download... with a signed webhook confirmed" — maps 1:1 onto `TestImageConversionE2E`'s exact structure, not the larger fixture-table shape `TestDocumentConversionE2E` uses for its 6 format pairs).

```go
// TestAudioConversionE2E drives the audio engine (whisper.cpp) happy path
// through the LIVE pipeline: multipart upload -> poll to done -> presigned
// download -> non-empty transcript check -> a signed webhook arrives
// (AUD-06/AUD-07 SC2, Phase 32), mirroring TestImageConversionE2E's shape
// for the fourth (and final v1.7) engine class. Uses jfk.wav (already
// committed at internal/convert/testdata/audio/jfk.wav, reused here via a
// symlink/copy into internal/e2e/testdata/ per the existing per-suite
// fixture-duplication convention -- e2e's testdata/ is its own directory,
// distinct from internal/convert/testdata/).
func TestAudioConversionE2E(t *testing.T) {
	cfg := e2eSetup(t)
	apiKey := provisionClient(t)

	data, err := os.ReadFile(filepath.Join("testdata", "jfk.wav"))
	if err != nil {
		t.Fatalf("read fixture jfk.wav: %v", err)
	}

	callbackURL, received := startWebhookReceiver(t, cfg.webhookHost)

	jobID := postJob(t, cfg.baseURL, apiKey, "jfk.wav", data, "txt", callbackURL, "")

	// Generous bound: whisper.cpp cold start (model load) in a fresh
	// container plus real transcription time -- mirrors the document/html
	// engines' 5-minute cold-start allowance, NOT the image engine's tighter
	// 2-minute bound (audio's per-job cost is closer to document/html's than
	// to libvips' near-instant resize).
	body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)

	downloadURL, _ := body["download_url"].(string)
	if downloadURL == "" {
		t.Fatalf("job %s done but download_url missing/empty: %v", jobID, body)
	}
	assertDownloadIsNonEmptyTranscript(t, downloadURL)

	assertSignedWebhook(t, received, jobID)
}

// assertDownloadIsNonEmptyTranscript fetches the presigned transcript URL
// and asserts non-empty content -- NOT an exact-string match (Pitfall 9,
// .planning/research/PITFALLS.md: ASR output is this project's first
// genuinely non-deterministic engine output; ONLY structural/non-empty
// assertions belong in this test, content/substring checks are the unit
// test suite's job per 30-RESEARCH.md's "Anti-Patterns to Avoid").
func assertDownloadIsNonEmptyTranscript(t *testing.T, downloadURL string) {
	t.Helper()
	resp, err := downloadClient().Get(downloadURL)
	if err != nil {
		t.Fatalf("GET download_url: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		t.Fatalf("GET download_url status = %d, want 200; body=%s", resp.StatusCode, b)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		t.Fatalf("downloaded transcript is empty")
	}
}
```

**Fixture note:** `internal/e2e/testdata/` is a directory distinct from `internal/convert/testdata/audio/` (confirmed — `internal/e2e/testdata/` currently has no audio files at all, only document/html/image fixtures). Copy `jfk.wav` (not symlink — every existing e2e fixture is a real committed file, no symlinks used anywhere in `internal/e2e/testdata/`) into `internal/e2e/testdata/jfk.wav` as part of this phase.

**E2E_WEBHOOK_HOST / SSRF-guard plumbing:** No new `docker-compose.e2e.yml` override is needed for the audio-worker's webhook path — audio jobs don't deliver their own webhooks (webhook delivery is fully decoupled to `webhook-worker-1/2`, per the existing D-03/D-06 architecture confirmed in `docker-compose.e2e.yml`'s comments: "Neither the image worker nor document-worker deliver webhooks anymore"), so `audio-worker` needs no `extra_hosts: host.docker.internal:host-gateway` entry — only `webhook-worker-1`/`webhook-worker-2` (which already have it) actually dial the E2E receiver.

## CI Timing Budget Impact

`.github/workflows/ci.yml`'s `e2e` job runs `go test ./internal/e2e/... -timeout 30m` — adding one more `t.Run`-free top-level test with a 5-minute `pollUntilDone` bound adds real wall-clock time to an already-tight-looking 30-minute ceiling (6 document format pairs × up to 5min each is already the dominant cost). Verify the actual E2E suite's real observed runtime after adding `TestAudioConversionE2E` (Wave 0/close-out check) rather than assuming 30m has comfortable headroom — flag for a `-timeout` bump if the live run approaches it.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| RTF/timing measurement harness | A new bespoke Go/Python measurement tool | `scripts/verapdf-measure.sh`'s exact shape (bash, `docker run --cpus/--memory`, `docker exec` for in-container timing, nearest-rank p95 via `sort -n` + `awk`) | Already proven, already produces auditable evidence exactly matching the project's existing go/no-go documentation convention (23-01-SUMMARY.md's raw-numbers table) |
| cgroup CPU-limit detection | A CGo/syscall wrapper, or a third-party Go library (e.g. `uber-go/automaxprocs`) | Direct `/sys/fs/cgroup/cpu.max` file read (plain `os.ReadFile` + `strings.Fields`) | Zero new dependencies (this codebase has never added a Go dependency for a single-file syscall-adjacent read); `automaxprocs` solves a DIFFERENT problem (Go runtime's own GOMAXPROCS) — this phase needs to size an EXTERNAL subprocess's `--threads` flag, which automaxprocs does not do |
| Synthetic long-duration audio fixture generation | Recording/sourcing a real long audio file (licensing/PII risk, non-deterministic) | `ffmpeg -f lavfi -i "sine=..."` or `ffmpeg -f concat` looping existing committed fixtures | Deterministic, reproducible, already-available tool (ffmpeg is a hard dependency of this phase regardless) |
| Compose service / CI bake wiring | A new bake config file, a separate GitHub Actions workflow | Add one `docker-compose.yml` service block + 2 lines per CI job's existing `set:` block | `docker/bake-action` already auto-derives targets from `docker-compose.yml`; no separate config exists or is needed for the 6 existing services |

**Key insight:** every "don't hand-roll" temptation in this phase already has a proven in-repo precedent (verapdf-measure.sh, the 5 existing Dockerfiles, the CI bake pattern, `TestImageConversionE2E`) — the only genuinely novel work is the cgroup-CPU-detection function and the synthetic-fixture generation, both single-function/single-command scope.

## Common Pitfalls

(Full detail in `.planning/research/PITFALLS.md` Pitfalls 4/5/6/8 and `31-REVIEW.md` WR-05/WR-06/IN-01..IN-04 — summarized here scoped to what THIS phase must act on.)

### Pitfall: `AUDIO_ENGINE_TIMEOUT` changed on the audio-worker service alone (IN-02)
**What goes wrong:** Reopens T-03-10 double-processing — see "CRITICAL — IN-02 propagation" section above for the full mechanism.
**How to avoid:** Single commit updates all 7 `queue.NewClient()`-constructing compose service blocks identically.
**Warning signs:** `grep AUDIO_ENGINE_TIMEOUT docker-compose.yml` returns fewer than 7 matches after this phase.

### Pitfall: whisper-cli invoked with no `--threads` flag (Pitfall 5, PITFALLS.md)
**What goes wrong:** Defaults to host core count, not cgroup quota — CFS throttling, unpredictable wall time, RTF measurement invalidated by whatever thread count the measurement host happened to expose.
**How to avoid:** `whisperArgs` must always pass an explicit `-t <n>`, sourced from cgroup detection with an env override and a `runtime.NumCPU()` fallback — see "`--threads` / cgroup CPU-limit detection" above.
**Warning signs:** RTF measurement run without an explicit `-t` flag, or with a hardcoded value not tied to the actual container's `cpus` limit.

### Pitfall: RTF measured on a fixture too short to be representative
**What goes wrong:** `jfk.wav` (11s) is dominated by whisper-cli's model-load/process-spawn overhead, not steady-state transcription throughput — an RTF computed from it would understate real per-minute cost on a genuinely long recording.
**How to avoid:** Generate a multi-minute synthetic fixture (ffmpeg `sine`/`concat`) specifically for the RTF measurement — see "RTF Measurement Methodology" above.
**Warning signs:** The measurement script's only input fixture is `jfk.wav` or another sub-30-second file.

### Pitfall: KEDA/Helm scope creep (explicit scope fence violation)
**What goes wrong:** The temptation to "just also size `terminationGracePeriodSeconds`/`ScaledObject` cooldown while I'm here, since I have the RTF number" — but ROADMAP explicitly scopes Phase 33 to consume this phase's measured timeout, not for this phase to pre-empt that work.
**How to avoid:** This phase's deliverable is the MEASURED NUMBER + a compose-level, non-KEDA `AUDIO_ENGINE_TIMEOUT`. Do not touch `deploy/chart/` at all.
**Warning signs:** Any diff touching `deploy/chart/octoconv/templates/*.yaml` or `values.yaml` in this phase's plan.

### Pitfall: baking the model without measuring the resulting image size (Pitfall 8, PITFALLS.md)
**What goes wrong:** Silently defeats scale-from-zero (a Phase 33 concern) with no evidence trail to act on later.
**How to avoid:** This phase must MEASURE and RECORD the built image size (`docker image inspect`), even though the bake-vs-volume decision itself is explicitly Phase 33's job (STATE.md Key Decision 3).
**Warning signs:** SUMMARY.md has no image-size number recorded.

### Pitfall: OrbStack compose/k8s mutual exclusion (operational discipline, per STATE.md Blockers)
**What goes wrong:** Running compose and k8s hot simultaneously has caused 4 confirmed daemon wedges on record (per STATE.md).
**How to avoid:** Verify k8s is stopped (`orb stop k8s` if needed, mirroring Phase 31's own live-E2E checkpoint procedure) before any `docker compose up` in this phase's execution. Pre-build images sequentially with non-`latest` tags.
**Warning signs:** Any step in the plan brings up compose without first checking `kubectl get nodes` fails / `orb` cluster status.

## Code Examples

### Existing `whisperArgs` — the exact injection point for `--threads` (verified, no flag currently present)

```go
// Source: internal/convert/whisper.go:154-166 (read directly this session)
func whisperArgs(model, normPath, outBase string, outFlags []string, o AudioOpts) []string {
	args := []string{"-m", model, "-f", normPath, "-of", outBase}
	args = append(args, outFlags...)
	lang := o.Language
	if lang == "" {
		lang = "auto"
	}
	args = append(args, "-l", lang)
	if o.Translate {
		args = append(args, "-tr")
	}
	return args
}
```

A new `threads int` parameter (sourced from a new `SetAudioThreads`/`audioThreads` package var mirroring `audioModelPath`'s exact shape) should insert `"-t", strconv.Itoa(threads)` — following the exact 3-tier fallback pattern `model()` already uses (test-injected override → process-wide setter value → compile-time default, here `runtime.NumCPU()` as the final fallback rather than a fixed constant).

### `scripts/verapdf-measure.sh`'s nearest-rank p95 computation (directly reusable, language-agnostic bash)

```bash
# Source: scripts/verapdf-measure.sh:103-112 (read directly this session)
SORTED=$(echo "$RAW_MILLIS" | sort -n)
N=$(echo "$SORTED" | wc -l | tr -d ' ')
RANK=$(awk -v n="$N" 'BEGIN { r = n * 0.95; rr = (r == int(r)) ? r : int(r) + 1; print rr }')
P95_MS=$(echo "$SORTED" | sed -n "${RANK}p")
```

For RTF (not raw ms), divide each per-run wall-clock by the fixture's known duration BEFORE sorting: `RTF_i = wall_clock_seconds_i / fixture_duration_seconds` (fixture duration is constant across all N runs, so this is a per-run scalar division, not a re-derivation).

### Peak memory observation (cgroup v2, directly reusable)

```bash
# Source: scripts/verapdf-measure.sh:144-151 (read directly this session)
PEAK_BYTES=$(docker exec "$CONTAINER" cat /sys/fs/cgroup/memory.peak 2>/dev/null || echo "unavailable")
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| `AUDIO_ENGINE_TIMEOUT=600s` copy-pasted placeholder | RTF-measured, derived value | This phase (AUD-07) | Direct hard input to Phase 33's KEDA cooldown/stabilization tuning — do not treat this phase's number as provisional once shipped |
| No `audio-worker` compose service (IN-04) | Full 7th worker service, structurally identical to `document-worker`/`chromium-worker` | This phase (AUD-06) | Closes the "API accepts audio jobs it cannot complete in the compose deployment" gap flagged in 31-REVIEW.md |
| whisper-cli invoked with no `--threads` flag anywhere in the codebase | Explicit `-t <cgroup-derived-n>` on every invocation | This phase | First OctoConv engine to require runtime container-resource introspection — no prior precedent to deviate from, but also no prior precedent's assumptions to inherit incorrectly |

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `ggml-base.bin` SHA-256 (`60ed5bc3...fba2efe`) captured in `.planning/research/STACK.md` on 2026-07-17 is still the byte-identical file HuggingFace serves at execution time | Standard Stack / Dockerfile.audio-worker | LOW — HuggingFace file content for a pinned model release is documented as stable; STACK.md itself already flagged "re-verify rather than trust a value transcribed days earlier". If wrong, the Dockerfile build fails LOUDLY at the `sha256sum -c` step (fail-closed by construction), not silently — cheap to detect, cheap to fix (re-fetch and re-pin) |
| A2 | cgroup v2 (`/sys/fs/cgroup/cpu.max` single-file format) is available inside both the OrbStack local-dev compose containers and the GitHub Actions CI runners' compose containers | `--threads` / cgroup CPU-limit detection | MEDIUM — if a target environment runs cgroup v1 instead, `/sys/fs/cgroup/cpu.max` won't exist and the detection function must fall back to `runtime.NumCPU()` (host core count), silently reintroducing PITFALLS.md Pitfall 5's exact risk (host core count ≠ real cgroup quota) on that specific environment only. NOT independently verified in this research session — flagged as the FIRST Wave 0 task specifically because of this uncertainty |
| A3 | `libgomp1` is the only additional runtime `.so` whisper.cpp's compiled `whisper-cli` needs beyond what `ca-certificates`/`ffmpeg`/libc/libstdc++ already provide in the runtime stage | Dockerfile.audio-worker | LOW-MEDIUM — carried over from STACK.md's own MEDIUM-confidence claim, not independently re-verified against an actual built binary in this session. If wrong, the built image fails at `whisper-cli`'s first invocation with a dynamic-linker error (loud, not silent) — cheap to detect via `ldd` inspection during the build, cheap to fix (add the missing apt package) |
| A4 | A synthetic ffmpeg-generated (sine-tone or concatenated-speech) fixture produces a representative-enough RTF for sizing `AUDIO_ENGINE_TIMEOUT`, without needing a genuinely diverse (multi-speaker, accented, noisy) longer real-world recording | RTF Measurement Methodology | MEDIUM — a synthetic/repeated fixture's RTF may not capture worst-case compute cost if whisper.cpp's per-segment decode cost varies meaningfully with content complexity (some community sources suggest it does, some suggest RTF is roughly content-invariant for a fixed model/thread-count — not independently resolved in this session). Mitigate by budgeting a safety margin above the measured p95 when deriving `AUDIO_ENGINE_TIMEOUT`, the same margin discipline `23-01-SUMMARY.md` applied (2.15x headroom, not a razor-thin pass) |
| A5 | `AUDIO_WORKER_CONCURRENCY=1` will be the measurement's actual conclusion (STATE.md's own "(likely 1)" language) | `AUDIO_WORKER_CONCURRENCY` sizing section | LOW — even if the measured conclusion differs (e.g., 2 is safe on a larger container), the phase's own methodology (measure RSS/CPU headroom, don't assume) is unaffected; only the SPECIFIC number "1" used as a working example throughout this document might need updating post-measurement |

**Note:** every claim above the Assumptions Log line not explicitly marked `[ASSUMED]`/`[CITED]` inline was `[VERIFIED]` via direct code/file read in this session (Dockerfiles, docker-compose.yml, ci.yml, e2e_test.go, whisper.go, client.go, cmd/audio-worker/main.go, verapdf-measure.sh, 31-REVIEW.md, 30/31-RESEARCH.md, STACK.md, PITFALLS.md all read directly).

## Open Questions

1. **Should a NO-GO RTF outcome lower `AUDIO_MAX_DURATION_SECONDS` (currently 14400s/4h) rather than accept a very large `AUDIO_ENGINE_TIMEOUT`?**
   - What we know: `AUDIO_ENGINE_TIMEOUT` is derived from RTF × worst-case-duration; if RTF measures poorly (e.g., close to or above 1.0 on the real container), the implied timeout for a 4-hour input becomes operationally absurd (many hours).
   - What's unclear: whether this phase's scope includes revisiting `AUDIO_MAX_DURATION_SECONDS` (an AUD-04/Phase 30 value) as part of the go/no-go response, or whether that's explicitly out of scope (the phase's stated success criteria only mention `AUDIO_ENGINE_TIMEOUT` and KEDA cooldown, not the duration ceiling).
   - Recommendation: surface this explicitly as a decision point in the phase's CONTEXT/discuss-phase step rather than resolving it silently either direction — the planner should treat "lower `AUDIO_MAX_DURATION_SECONDS` if RTF is bad" as an explicit, documented option, not a default.

2. **Does the RTF measurement container need to match OrbStack's arm64 or a genuinely amd64 target (mirroring Phase 23's emulation caveat)?**
   - What we know: unlike `document-worker` (hard-pinned `linux/amd64` because `verapdf/cli` publishes no arm64 manifest), `audio-worker` has NO such constraint — whisper.cpp builds natively on both architectures.
   - What's unclear: whether the eventual production/k8s deployment target is amd64 or arm64 (not stated in any read document this session), and whether an OrbStack arm64-native measurement is representative enough, or whether the measurement should also be cross-checked under `--platform linux/amd64` emulation (Rosetta-class, per 23-01-SUMMARY.md's own caveat) if the production target differs from the measurement host's native architecture.
   - Recommendation: measure natively on whatever the execution host is (fastest, most reliable), but explicitly record the measurement host's architecture in the SUMMARY (mirroring 23-01-SUMMARY.md's "Emulation caveat on the raw numbers" section) so a future re-measurement need is flagged, not silently assumed away.

3. **Is `internal/e2e/testdata/jfk.wav` (an 11-second, well-known public-domain historical recording) an acceptable committed fixture, or does the E2E suite's existing "no exact-transcript assertions" discipline (Pitfall 9) already fully cover the risk of relying on it?**
   - What we know: `jfk.wav` is already committed at `internal/convert/testdata/audio/jfk.wav` and already used in Phase 31's live E2E without incident; `TestAudioConversionE2E`'s design above deliberately asserts only non-emptiness, not transcript content, sidestepping Pitfall 9 entirely.
   - What's unclear: nothing substantive — this is a low-risk question included only because it's the one place this phase's E2E design touches audio *content* (as opposed to structural/timing concerns everywhere else in this document).
   - Recommendation: proceed with the copy-into-`internal/e2e/testdata/` plan as designed; no further research needed.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Docker + `docker buildx`/`bake` | Building `Dockerfile.audio-worker`, RTF measurement script | ✓ (already required by every prior phase's live-gate work) | — | — |
| `ffmpeg`/`ffprobe` | Synthetic RTF fixture generation | ✓ | 8.1.2 (Homebrew, local dev; `7:5.1.9-0+deb12u1` inside the built container) | — |
| OrbStack (or equivalent Docker runtime) | Live compose E2E, RTF measurement container | ✓ (per STATE.md, k8s currently DOWN — compose work is safe) | — | — |
| `whisper-cli` v1.9.1 local binary | Not required for THIS phase's Dockerfile/measurement work (measurement runs INSIDE the built container, not against the local Phase-30-built binary) | N/A this phase | — | — |

**Missing dependencies with no fallback:** none — every tool this phase needs was already required and available for Phase 23's (structurally identical) measurement gate and Phase 31's live E2E.

**Missing dependencies with fallback:** none identified.

## Validation Architecture

`.planning/config.json` was not found/read as part of this research session's file list (not in the explicit `<files_to_read>` set); assuming `workflow.nyquist_validation` is absent-or-true per the default, this section is included.

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go stdlib `testing`, no third-party assertion library (project convention, confirmed CLAUDE.md) |
| Config file | none — `go test` invoked directly per `.github/workflows/ci.yml` |
| Quick run command | `go build ./cmd/audio-worker/... && docker build -f Dockerfile.audio-worker -t octoconv-audio-worker:dev .` (Dockerfile syntax/build-only check, no live run) |
| Full suite command | `go test ./internal/e2e/... -timeout 30m` with `E2E_BASE_URL` set against a live `docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d` stack (mirrors `ci.yml`'s `e2e` job exactly) |

### Phase Requirements → Test Map
| Req ID | Behavior | Test Type | Automated Command | File Exists? |
|--------|----------|-----------|-------------------|-------------|
| AUD-06 | `Dockerfile.audio-worker` builds successfully, all 3 stages resolve | build/smoke | `docker build -f Dockerfile.audio-worker -t octoconv-audio-worker:dev .` | ❌ Wave 0 (new Dockerfile) |
| AUD-06 | `audio-worker` compose service starts healthy and consumes real audio jobs end-to-end | e2e (live) | `go test ./internal/e2e/... -run TestAudioConversionE2E -timeout 10m` | ❌ Wave 0 (new test, `internal/e2e/e2e_test.go`) |
| AUD-06 | CI bake matrix builds `audio-worker` alongside the other 6 targets | CI-only (GHA) | (no local equivalent — verified by a real `push`/PR run of `.github/workflows/ci.yml`) | N/A — CI config change, not a test file |
| AUD-07 | RTF measured on a real resource-limited container, go/no-go documented | script/manual-gate | `scripts/audio-rtf-measure.sh` (new, mirrors `scripts/verapdf-measure.sh`) | ❌ Wave 0 (new script) |
| AUD-06/AUD-07 SC4 | `--threads` reflects cgroup CPU limit, not host core count | unit + live spot-check | `go test ./internal/convert/... -run TestCgroupCPULimit` (new) + a `docker exec` spot-check of the real container's effective `-t` value | ❌ Wave 0 (new unit test + manual verification step) |

### Sampling Rate
- **Per task commit:** `go build ./... && go vet ./...` (unchanged project convention)
- **Per wave merge:** `go test ./... -timeout 5m` (offline suite) + a live compose smoke check for the new service
- **Phase gate:** Full `internal/e2e` suite green (including the new `TestAudioConversionE2E`) + the RTF measurement script's GO verdict recorded in the phase SUMMARY, before `/gsd:verify-work`

### Wave 0 Gaps
- [ ] `Dockerfile.audio-worker` — does not exist yet, this phase's primary deliverable
- [ ] `docker-compose.yml`'s `audio-worker` service block — does not exist yet (IN-04)
- [ ] `internal/e2e/testdata/jfk.wav` — needs copying from `internal/convert/testdata/audio/jfk.wav`
- [ ] `TestAudioConversionE2E` in `internal/e2e/e2e_test.go` — does not exist yet
- [ ] `scripts/audio-rtf-measure.sh` — does not exist yet, new script mirroring `verapdf-measure.sh`
- [ ] A synthetic multi-minute RTF fixture (ffmpeg-generated, not yet committed anywhere)
- [ ] cgroup CPU-limit detection function + `TestCgroupCPULimit` unit test — does not exist yet
- [ ] A live `docker exec ... cat /sys/fs/cgroup/cpu.max` spot-check against a real built container — the FIRST task, before any detection code is written (per Assumption A2's flagged uncertainty)

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-------------------|
| V6 Cryptography | yes | Model integrity: SHA-256 pin + `sha256sum -c` fail-closed check at Docker build time (not a runtime concern, but the correct place to enforce it — matches the project's existing MinIO/verapdf/chromium pinned-artifact discipline) |
| V4 Access Control | no | `USER nobody` (unprivileged runtime) is already an established, unchanged pattern across all 5 existing worker Dockerfiles — this phase applies it identically, no new decision |
| V1 Architecture | yes (indirectly) | The IN-02 cross-process env-consistency requirement is itself a security-adjacent architectural control — an inconsistent `AUDIO_ENGINE_TIMEOUT` doesn't leak data, but it reopens a documented double-processing race with real operational (cost/correctness) consequences |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|-----------------------|
| Supply-chain: HuggingFace model swap between research-time SHA-256 capture and build-time fetch | Tampering | `sha256sum -c` fail-closed check already specified in the Dockerfile skeleton (Installation section, STACK.md-derived) — build fails loudly, never silently accepts a mismatched model |
| Whisper.cpp source pulled via `git clone --branch v1.9.1` — a tag can theoretically be force-moved upstream (low likelihood for a released, widely-used project, but not cryptographically pinned the way the model SHA-256 is) | Tampering | Lower-priority residual risk, not mitigated by this phase's design (no commit-SHA pin used, matching the project's existing tag-based pinning convention for `verapdf/cli:v1.30.2`/`hibiken/asynqmon:0.7.2` — none of those use commit-SHA pins either); acceptable given the project's existing precedent, not a new gap this phase introduces |
| Resource exhaustion via an over-permissive `--threads`/`AUDIO_WORKER_CONCURRENCY` combination starving the shared container | Denial of Service | The entire "RTF Measurement Methodology" + "`AUDIO_WORKER_CONCURRENCY` sizing" sections above ARE the mitigation — measured, not assumed, sizing |

## Sources

### Primary (HIGH confidence)
- Direct reads this session: `Dockerfile.document-worker`, `Dockerfile.chromium-worker`, `Dockerfile.worker`, `Dockerfile.webhook-worker`, `docker-compose.yml`, `docker-compose.e2e.yml`, `.github/workflows/ci.yml`, `internal/e2e/e2e_test.go` (full file), `internal/convert/whisper.go` (full file), `internal/queue/client.go` (relevant sections), `cmd/audio-worker/main.go` (full file), `scripts/verapdf-measure.sh` (full file), `.env.example` (full file)
- `.planning/phases/31-queue-worker-routing-integration/31-04-SUMMARY.md`, `31-REVIEW.md` — live E2E evidence, IN-01..IN-04/WR-01..WR-06 findings
- `.planning/phases/30-audio-engine-foundation/30-RESEARCH.md` — whisper-cli v1.9.1 source-verified CLI contract, model SHA-256 pin
- `.planning/milestones/v1.5-phases/23-verapdf-validation/23-01-SUMMARY.md` — the direct measurement-gate methodology template
- `.planning/REQUIREMENTS.md`, `.planning/STATE.md`, `.planning/ROADMAP.md` — phase scope, Key Decisions, requirement IDs

### Secondary (MEDIUM confidence)
- `.planning/research/STACK.md`, `.planning/research/PITFALLS.md` (v1.7 milestone research, 2026-07-17) — Dockerfile skeleton, RTF sizing triangulation (explicitly flagged MEDIUM by that document itself), thread/cgroup pitfall (WebSearch-sourced, no Context7 entry for whisper.cpp)

### Tertiary (LOW confidence)
- cgroup v2 `/sys/fs/cgroup/cpu.max` availability inside OrbStack/GHA compose containers specifically — mechanism is well-documented cgroup v2 API generally, but not independently live-tested against a real OctoConv container in any session to date (flagged as the mandatory first Wave 0 spot-check, Assumption A2)

## Metadata

**Confidence breakdown:**
- Dockerfile/compose/CI structural patterns: HIGH — 5 existing sibling implementations read directly, zero architectural novelty
- IN-02/IN-04 cross-process consistency findings: HIGH — verified by direct code read against the live `docker-compose.yml` and `internal/queue/client.go` this session, not inherited from prior research
- RTF measurement methodology: HIGH on the mechanism (direct reuse of a proven in-repo script), MEDIUM on the specific numbers it will produce (that's the point of the gate — it's designed to resolve a currently-unknown value)
- `--threads`/cgroup detection: MEDIUM — mechanism is standard cgroup v2 API, genuinely new to this codebase, not independently live-tested this session

**Research date:** 2026-07-18
**Valid until:** 14 days (contains a live-fetched hash that should be re-verified at execution time regardless; the compose/CI patterns are stable indefinitely, but the RTF numbers this research explicitly defers to measurement should not be treated as stale-tolerant)
