---
phase: 32-containerization-local-e2e-rtf-gate
plan: 01
subsystem: infra
tags: [docker, whisper.cpp, ffmpeg, multi-stage-build, cgroup, audio]

# Dependency graph
requires:
  - phase: 31-audio-async-integration
    provides: cmd/audio-worker (Go binary, live-proven E2E), internal/convert/whisper.go (defaultAudioModelPath contract)
provides:
  - Dockerfile.audio-worker (3-stage: Go build, whisper.cpp v1.9.1 source build, slim ffmpeg runtime)
  - Live-measured audio-worker image size (arm64 build, ~682MB)
  - Live-confirmed cgroup v2 cpu.max format under docker --cpus (Assumption A2 resolved)
  - Live-reconfirmed ggml-base.bin SHA-256 pin against HuggingFace LFS metadata
affects: [32-02, 32-03, 32-04, 32-05, 33-keda-audio]

# Tech tracking
tech-stack:
  added: [whisper.cpp v1.9.1 (source-built, -DGGML_NATIVE=OFF)]
  patterns:
    - "Dynamically-linked CLI tools built from source in a throwaway multi-stage builder need their .so files copied + ldconfig'd into the runtime stage when no RPATH is set at compile time (whisper-cli links libwhisper.so.1/libggml*.so with zero RPATH)"

key-files:
  created: [Dockerfile.audio-worker]
  modified: []

key-decisions:
  - "No platform: pin needed for Dockerfile.audio-worker (unlike document-worker's amd64-only veraPDF pin) -- whisper.cpp is source-built with -DGGML_NATIVE=OFF, portable across arm64/amd64"
  - "No tini/init-as-PID-1 needed -- audio engine is a single synchronous two-stage CLI invocation (ffmpeg then whisper-cli) per job, no forking daemon, matching Dockerfile.worker's existing rationale"

patterns-established:
  - "Pattern: whisper.cpp build stage output (build/bin/*.so*) must be explicitly copied to /usr/local/lib + ldconfig'd in the runtime stage -- the drafted skeleton in 32-RESEARCH.md/STACK.md omitted this and it is load-bearing (ldd fails with '=> not found' otherwise)"

requirements-completed: [AUD-06]

# Metrics
duration: 8min
completed: 2026-07-18
---

# Phase 32 Plan 01: Dockerfile.audio-worker + Live cgroup Spot-Check Summary

**Three-stage `Dockerfile.audio-worker` (Go build, whisper.cpp v1.9.1 source-built with `-DGGML_NATIVE=OFF`, slim ffmpeg runtime) built and verified locally on arm64 (682MB), with the mandatory live cgroup v2 `cpu.max` spot-check confirming `200000 100000` under `--cpus=2.0` (Assumption A2 resolved).**

## Performance

- **Duration:** ~8 min (task execution; docker build itself ran twice, ~2-3 min each after cache warm-up)
- **Started:** 2026-07-18T16:27:53Z
- **Completed:** 2026-07-18T16:35:27Z
- **Tasks:** 2/2 completed
- **Files modified:** 1 (Dockerfile.audio-worker, created)

## Accomplishments
- `Dockerfile.audio-worker` written and builds clean: Go build stage (byte-identical pattern to sibling worker Dockerfiles), whisper.cpp v1.9.1 build stage (`-DGGML_NATIVE=OFF`, load-bearing per STATE.md Key Decision), slim `debian:bookworm-slim` runtime stage with `ffmpeg` + baked `ggml-base.bin` model + `USER nobody` + plain `ENTRYPOINT` (no tini, no platform pin)
- Live SHA-256 re-verification of `ggml-base.bin` against HuggingFace's LFS blob metadata API (`60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe`, 147,951,465 bytes) — matches the value pinned in `32-RESEARCH.md`/`STACK.md` exactly; `sha256sum -c -` inside the build also passed (`OK`)
- `ldd` on `whisper-cli` inside the built runtime image shows zero missing shared objects after the shared-lib-copy fix (see Deviations)
- Image size recorded: **681,662,045 bytes (~682 MB / ~650 MiB)** on `linux/arm64` (host: Apple Silicon OrbStack) — input for Phase 33's scale-from-zero sizing decision (bake-vs-volume), not decided in this phase
- Mandatory live cgroup v2 spot-check (Assumption A2) run against the real built image: `docker run --rm --cpus=2.0 --entrypoint cat octoconv-audio-worker:dev /sys/fs/cgroup/cpu.max` → **`200000 100000`** (quota/period = 2, exactly matching the RESEARCH.md's predicted format for `--cpus=2.0`) — Plan 02's `cgroupCPULimit()` threads-detection code can now be written against this confirmed reality, not a hypothesis
- `whisper-cli --help` confirms `-t N, --threads N` flag is present (`[4] number of threads to use during computation`) — the flag Plan 02/RTF measurement will pass

## Task Commits

Each task was committed atomically:

1. **Task 1: Write Dockerfile.audio-worker** - `54fe159` (feat)
2. **Task 2: Build image, verify runtime .so deps, record size, live cgroup spot-check** - `9307500` (fix — captures the two build-blocking bugs discovered while executing Task 2's own build/verify steps)

**Plan metadata:** (this commit, docs: complete plan)

## Files Created/Modified
- `Dockerfile.audio-worker` - Three-stage build: Go build (`cmd/audio-worker`), whisper.cpp v1.9.1 source build (`-DGGML_NATIVE=OFF`, SHA-256-pinned model fetch), slim runtime (ffmpeg + whisper-cli + baked model, `USER nobody`, plain `ENTRYPOINT`)

## Decisions Made
- No `platform: linux/amd64` pin (unlike `document-worker`'s veraPDF constraint) — whisper.cpp is source-built and `-DGGML_NATIVE=OFF` makes the resulting binary portable across the CI runner's likely amd64 host and this session's arm64 OrbStack host without a pin, following `chromium-worker`'s no-pin precedent
- No tini as PID 1 — the audio engine is a single synchronous two-stage CLI invocation per job (ffmpeg then whisper-cli), no forking daemon like LibreOffice's `oosplash`→`soffice.bin` or Chromium's zygote/GPU/renderer subprocess tree, matching `Dockerfile.worker`'s own documented rationale for the image engine

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] `/models` directory did not exist before curl wrote to it**
- **Found during:** Task 2 (first `docker build` attempt)
- **Issue:** The drafted skeleton's `curl -o /models/ggml-base.bin ...` failed with `curl: (23) Failure writing output to destination` / `No such file or directory` — the `whisper-build` stage never creates `/models` before the fetch
- **Fix:** Added `mkdir -p /models &&` before the `curl` invocation
- **Files modified:** `Dockerfile.audio-worker`
- **Verification:** Re-ran `docker build` — the fetch + `sha256sum -c -` step completed with `/models/ggml-base.bin: OK`
- **Committed in:** `9307500` (Task 2 commit)

**2. [Rule 1 - Bug] `whisper-cli` dynamically links against `libwhisper.so.1`/`libggml*.so` with no RPATH set, causing `ldd` to report `=> not found`**
- **Found during:** Task 2's mandatory `ldd` runtime-dependency check (the plan anticipated a possible `libgomp1` gap per RESEARCH.md's "Deviations to verify at execution time" but not this one)
- **Issue:** `readelf -d /whisper/build/bin/whisper-cli` confirmed no RPATH/RUNPATH is embedded by whisper.cpp's cmake build; the runtime stage only copied the `whisper-cli` binary itself, leaving its own shared libraries (built alongside it in `build/bin/`) absent from the runtime image entirely
- **Fix:** Added `COPY --from=whisper-build /whisper/build/bin/*.so* /usr/local/lib/` followed by `RUN ldconfig` in the runtime stage, before the `USER nobody` switch
- **Files modified:** `Dockerfile.audio-worker`
- **Verification:** Rebuilt image; `docker run --rm --entrypoint ldd octoconv-audio-worker:dev /usr/local/bin/whisper-cli` shows all libraries resolved (`libwhisper.so.1`, `libggml.so.0`, `libggml-base.so.0`, `libggml-cpu.so.0` all `=> /usr/local/lib/...`; `libgomp.so.1` already resolved as a transitive apt dependency, no `libgomp1` line needed)
- **Committed in:** `9307500` (Task 2 commit)

---

**Total deviations:** 2 auto-fixed (1 blocking build failure, 1 correctness bug in the runtime linking) — both bundled in a single commit since they were discovered and fixed together during Task 2's build/verify loop
**Impact on plan:** Both fixes are strictly necessary for the plan's own success criteria (image must build; `ldd` must show no missing `.so`s). No scope creep — no other files touched, no architectural change.

## Issues Encountered
None beyond the two auto-fixed deviations above, both resolved within the fix-attempt budget on the first retry each.

## User Setup Required
None - no external service configuration required. All build inputs (apt packages, `git clone --branch v1.9.1`, SHA-256-verified HuggingFace model fetch) are pinned and require no manual setup.

## Next Phase Readiness
- `Dockerfile.audio-worker` builds clean and is ready for Plan 02's `AUDIO_THREADS`/cgroup-detection code, which can now be written against the confirmed live `cpu.max` format (`200000 100000` under `--cpus=2.0`) instead of a hypothesis
- Image size (682MB, arm64) is recorded for Phase 33's scale-from-zero sizing input; this phase does not decide bake-vs-volume (Key Decision 3, deferred to Phase 33)
- `whisper-cli --threads`/`-t` flag confirmed present for the RTF measurement script (Plan 03/04) and Plan 02's runtime thread-sizing code
- No blockers for Plan 02 (compose service wiring + threads-detection) or Plan 03 (RTF measurement)

---
*Phase: 32-containerization-local-e2e-rtf-gate*
*Completed: 2026-07-18*
