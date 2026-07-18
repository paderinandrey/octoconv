---
phase: 32-containerization-local-e2e-rtf-gate
plan: 03
subsystem: infra
tags: [whisper.cpp, ffmpeg, docker, cgroup, rtf, go-no-go, audio]

# Dependency graph
requires:
  - phase: 32-containerization-local-e2e-rtf-gate
    plan: 01
    provides: Dockerfile.audio-worker (built image), live-confirmed cgroup v2 cpu.max format under docker --cpus
provides:
  - scripts/audio-rtf-measure.sh (reusable RTF go/no-go measurement gate, structural clone of scripts/verapdf-measure.sh)
  - Measured p95 RTF = 0.205853 on the real resource-limited audio-worker container (arm64, 2 cpus, base model, N=10)
  - Derived AUDIO_ENGINE_TIMEOUT=742s with a NO-GO lever applied (AUDIO_MAX_DURATION_SECONDS lowered 14400s -> 1800s)
  - Measured AUDIO_WORKER_CONCURRENCY=1 decision (concurrency=2 fails both the memory and cpu fit checks)
affects: [32-04, 33-keda-audio]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "RTF go/no-go gate pattern (scripts/audio-rtf-measure.sh): build real image -> long-lived --cpus/--memory container -> read cgroup cpu.max for -t -> generate a deterministic ffmpeg-looped fixture inside the container -> timed N-run loop of the FULL production pipeline -> nearest-rank p95 -> derive a timeout budget FROM the measurement (not assert against a pre-existing one, unlike veraPDF's fixed D-01 budget)"

key-files:
  created: [scripts/audio-rtf-measure.sh]
  modified: []

key-decisions:
  - "min_expected_bitrate_bytes_per_s = 32000 bytes/s (16kHz/16-bit/mono PCM, the ffmpeg normalization target and WAV's own uncompressed rate) used for the MAX_UPLOAD_BYTES-implied duration bound -- the most conservative (smallest resulting duration) of the four supported source formats, matching .env.example line 16's own WAV illustration"
  - "NO-GO lever APPLIED: the RTF-derived timeout for the original AUDIO_MAX_DURATION_SECONDS=14400s (4h, even after the MAX_UPLOAD_BYTES byte-bound already reduced it to 3276s) is 1349s, which exceeds the asserted 900s/15m CAP -- AUDIO_MAX_DURATION_SECONDS is lowered to 1800s (30min) per the documented Open-Question-1 lever, yielding AUDIO_ENGINE_TIMEOUT=742s (17.6% margin under CAP)"
  - "AUDIO_WORKER_CONCURRENCY=1, measured not assumed: single-job peak RSS (~728.4 MiB of the 1g limit) makes concurrency=2 exceed the memory limit (2x728.4=1456.8 MiB > 1024 MiB), and -t is sized to consume the full 2-cpu quota per job so concurrency=2 would also oversubscribe cpu 2x -- both checks fail independently, confirming Pitfall 5's prediction"
  - "GO decision explicitly DEPENDS on Plan 04 raising docker-compose.yml's webhook-worker-1/2 RECONCILER_ACTIVE_STALE_AFTER from the stale 5m to 15m -- the derived 742s AUDIO_ENGINE_TIMEOUT is below the asserted 900s/15m CAP but ABOVE the currently-deployed stale 300s/5m value"

patterns-established:
  - "Pattern: RTF-derivation formula ties AUDIO_ENGINE_TIMEOUT to AUDIO_MAX_DURATION_SECONDS via timeout = ceil(effective_max_duration_s x RTF_p95 x safety_factor(2.0)), with an explicit inverse-solve lever (max_duration < CAP / (RTF_p95 x safety_factor)) when the forward direction breaches the reconciler CAP -- this pattern generalizes to any future engine-class RTF gate"

requirements-completed: [AUD-07]

# Metrics
duration: 22min
completed: 2026-07-18
---

# Phase 32 Plan 03: audio-rtf-measure.sh RTF Go/No-Go Gate Summary

**`scripts/audio-rtf-measure.sh` measured p95 RTF=0.2059 (N=10, arm64, base model, 2-cpu container) and the NO-GO lever fired: AUDIO_MAX_DURATION_SECONDS is lowered from the placeholder 14400s (4h) to 1800s (30min), yielding a derived AUDIO_ENGINE_TIMEOUT=742s (12.4min, 17.6% margin under the asserted 900s/15m CAP) and a measured AUDIO_WORKER_CONCURRENCY=1.**

## Performance

- **Duration:** ~22 min (script authoring + verification: ~5 min; measurement run itself: ~12 min background wait; analysis/derivation/SUMMARY: ~5 min)
- **Started:** 2026-07-18T16:37:00Z (approx, first file read)
- **Completed:** 2026-07-18T16:58:00Z
- **Tasks:** 2/2 completed
- **Files modified:** 1 (`scripts/audio-rtf-measure.sh`, created)

## Accomplishments
- `scripts/audio-rtf-measure.sh` written as a structural clone of `scripts/verapdf-measure.sh` (Phase 23 precedent), adapted for RTF instead of raw wall-clock: builds the real `octoconv-audio-worker` image (no cross-arch platform pin), starts a long-lived `--cpus=2.0 --memory=1g` container matching the eventual compose block, reads the container's own `/sys/fs/cgroup/cpu.max` to derive `-t 2` (floor of quota/period, matching Plan 02's `cgroupCPULimit()`), generates a deterministic ~5-minute fixture via `ffmpeg -stream_loop` over the committed `jfk.wav` (never committing a large binary), plus a sine-tone floor cross-check, then times 10 runs of the FULL production pipeline (`ffmpeg normalize` -> `whisper-cli -l auto -otxt`) inside the container.
- Live measurement executed successfully end-to-end (script exit code 0 -- measurement integrity gate passed): **p95 RTF = 0.205853** (nearest-rank, N=10, rank=10).
- `AUDIO_ENGINE_TIMEOUT` derived by the explicit documented formula; the **NO-GO lever fired** on the first pass (original 4h ceiling implied a 1349s timeout against a 900s CAP) -- `AUDIO_MAX_DURATION_SECONDS` is lowered to 1800s (30min), yielding a final derived `AUDIO_ENGINE_TIMEOUT = 742s` with 17.6% headroom under the CAP.
- `AUDIO_WORKER_CONCURRENCY = 1` decided from measured peak RSS (~728.4 MiB of the 1g limit) -- concurrency=2 fails both the memory-fit and cpu-fit checks by a wide margin, confirming PITFALLS.md Pitfall 5's prediction with a real number instead of an assumed constant.

## Task Commits

Each task was committed atomically:

1. **Task 1: scripts/audio-rtf-measure.sh — build image, generate synthetic fixture, timed RTF loop, p95, memory/cpu observation** - `9f61296` (feat)
2. **Task 2: Run the measurement and derive AUDIO_ENGINE_TIMEOUT** - no additional file changes (analysis-only task; the live measurement run and derivation are recorded below and consumed by Plan 04). No separate commit — nothing in the working tree changed beyond what Task 1 already committed.

**Plan metadata:** (this commit, docs: complete plan)

## Files Created/Modified
- `scripts/audio-rtf-measure.sh` - RTF go/no-go measurement gate: build -> long-lived resource-limited container -> cgroup-derived `-t` -> deterministic ffmpeg fixture -> timed full-pipeline loop -> nearest-rank p95 RTF -> peak memory/image-size/arch observation. Exit code gates measurement integrity only (build/container/fixture/pipeline must succeed); the timeout GO/NO-GO decision itself is made in this SUMMARY, not asserted inside the script (unlike veraPDF's fixed 10s D-01 budget, there is no pre-existing RTF budget to compare against).

## Raw Measurement Evidence (auditable trail, 23-01-SUMMARY.md style)

**Environment:**
- Image: `octoconv-audio-worker:rtf-measure`, built fresh from `Dockerfile.audio-worker` (all layers cache-hit against the identical `octoconv-audio-worker:dev` build from Plan 01 — Dockerfile/context unchanged) — `sha256:d57f4b64fd7ce2d24fe093275cce5c2f7c4829352d6cf5db81ed484d114c0385`
- Image size: **681,662,045 bytes (~650.1 MiB)**
- Container limits: `--cpus=2.0 --memory=1g` (matches `document-worker`/`chromium-worker`'s existing compose worker-ceiling convention)
- `cpu.max` read live inside the container: `200000 100000` → derived `-t 2` (floor of quota/period)
- Measurement-host architecture: **arm64** (Apple Silicon, OrbStack) — **Phase-23-style caveat**: this number was measured natively on arm64; production amd64 RTF is expected to differ (could be faster or slower depending on target CPU generation) and was NOT cross-measured under emulation this phase. Phase 33 consumes this RTF number WITH this caveat explicitly attached (Open Question 2, RESOLVED per 32-RESEARCH.md).

**Fixture:**
- Primary: deterministic ~5-minute concatenated-speech fixture generated INSIDE the container via `ffmpeg -stream_loop -1 -i jfk.wav -t 300 ...` (never committed as a binary) — requested 300s, ffprobe-measured **300.000000s** exactly.
- Sanity cross-check (floor signal, not used in the timeout decision): a 300s sine-tone (440Hz) fixture, single run = 33954ms → **RTF=0.113180**. This confirms real speech content costs meaningfully more compute than a silence-adjacent tone (0.206 vs 0.113), consistent with RESEARCH.md's caveat that whisper.cpp's compute cost is not silence-invariant.

**Timed loop — 10 runs of the FULL production pipeline (`ffmpeg -y -i file:fixture.wav -ar 16000 -ac 1 -c:a pcm_s16le norm.wav` then `whisper-cli -m /models/ggml-base.bin -f norm.wav -of out -otxt -l auto -t 2`), inside the container via a single `docker exec`:**

Raw per-run wall-clock (ms), execution order:
```
60554, 60381, 60208, 60421, 61382, 60886, 61756, 60040, 59984, 61536
```

Per-run RTF (`ms/1000 / 300.0`), execution order:
```
0.201847, 0.201270, 0.200693, 0.201403, 0.204607, 0.202953, 0.205853, 0.200133, 0.199947, 0.205120
```

Sorted ascending RTF:
```
0.199947, 0.200133, 0.200693, 0.201270, 0.201403, 0.201847, 0.202953, 0.204607, 0.205120, 0.205853
```

N=10, nearest-rank p95 rank = ceil(0.95×10) = 10 → **p95 RTF = 0.205853**

**Peak memory:** cgroup v2 `memory.peak` = 763,793,408 bytes (**~728.4 MiB**) of the 1024 MiB (`1g`) limit — cumulative peak across the container's session (fixture generation + floor cross-check + 10 production runs), representing the single-job (whisper-cli + ffmpeg) RSS footprint including the 147 MB baked model.

## AUDIO_ENGINE_TIMEOUT Derivation (explicit formula, every substitution shown)

**Formula (per plan):**
```
effective_max_duration_s = min(AUDIO_MAX_DURATION_SECONDS, floor(MAX_UPLOAD_BYTES / min_expected_bitrate_bytes_per_s))
AUDIO_ENGINE_TIMEOUT = ceil(effective_max_duration_s × RTF_p95 × SAFETY_FACTOR)   [SAFETY_FACTOR = 2.0]
```

**Inputs:**
- `AUDIO_MAX_DURATION_SECONDS` (current `.env.example` placeholder) = 14400s (4h)
- `MAX_UPLOAD_BYTES` (`.env.example` line 16) = 104,857,600 bytes (100 MiB)
- `min_expected_bitrate_bytes_per_s` = **32,000 bytes/s** — the 16kHz/16-bit/mono PCM rate, i.e. WAV's own uncompressed bitrate (the ffmpeg normalization target every source format is converted to at stage 1). WAV is one of the four directly-supported source formats (`mp3`, `wav`, `m4a`, `ogg`, `internal/convert/whisper.go:36`) and is by far the highest-byte-density of the four, so it produces the most CONSERVATIVE (smallest) byte-derived duration bound — matching `.env.example` line 16's own illustration ("an uncompressed 1-hour WAV (~635 MiB) exceeds this").
- `RTF_p95` = 0.205853 (measured above)
- `SAFETY_FACTOR` = 2.0 (Phase-23 ~2.15x margin discipline, not a razor-thin pass)

**Pass 1 (before the lever):**
```
byte_bound = floor(104857600 / 32000) = 3276s
effective_max_duration_s = min(14400, 3276) = 3276s
AUDIO_ENGINE_TIMEOUT = ceil(3276 × 0.205853 × 2.0) = ceil(1348.75) = 1349s (~22.5 min)
```

**CAP (read from the DEPLOYED docker-compose.yml, source of truth, per plan instruction):**
```
$ grep -n 'RECONCILER_ACTIVE_STALE_AFTER' docker-compose.yml
185:      RECONCILER_ACTIVE_STALE_AFTER: "5m"
215:      RECONCILER_ACTIVE_STALE_AFTER: "5m"
```
Both `webhook-worker-1`/`webhook-worker-2` currently carry a **stale Phase-16 `5m` override** in the deployed file (no `audio-worker` service block exists in `docker-compose.yml` yet — that is Plan 02/04's job). **CAP assumption stated explicitly: CAP = 15m/900s**, the Phase-31 code default (`.env.example` line 69) that **Plan 04 (wave 3) restores across the file** by correcting the stale 5m webhook-worker override; the 5m currently in `docker-compose.yml` is a known Phase-16 leftover, **NOT** the intended CAP.

**Decision (Pass 1):** `1349s ≥ 900s (CAP)` → **NO-GO** for the 4h ceiling (even after the byte-bound already cut it to 3276s). Applying the documented LEVER (Open Question 1, RESOLVED): lower `AUDIO_MAX_DURATION_SECONDS` rather than inflate the timeout or raise the global `RECONCILER_ACTIVE_STALE_AFTER`.

**Lever solve:**
```
required: new_max × 0.205853 × 2.0 < 900
new_max < 900 / 0.411706 = 2185.6s (~36.4 min)
```
**Chosen: `AUDIO_MAX_DURATION_SECONDS = 1800s` (30 min)** — a round, operationally meaningful ceiling (covers the large majority of internal calls/meetings) comfortably below the 2185.6s solve-point.

**Pass 2 (after the lever):**
```
effective_max_duration_s = min(1800, 3276) = 1800s
AUDIO_ENGINE_TIMEOUT = ceil(1800 × 0.205853 × 2.0) = ceil(741.07) = 742s (~12.4 min)
```

**Decision (Pass 2):** `742s < 900s (CAP)` → **GO**. Margin = 158s (**17.6% headroom**), not razor-thin.

**Flag (per plan instruction, since the derived value falls between the stale 300s and the asserted 900s):** `742s > 300s` (the CURRENTLY-deployed stale `5m` webhook-worker override). **The GO decision explicitly DEPENDS on Plan 04 raising `docker-compose.yml`'s `RECONCILER_ACTIVE_STALE_AFTER` from `5m` to `15m` across `webhook-worker-1`/`webhook-worker-2`** (Plan 04 Task 2 re-validates this invariant grep-wise against the deployed compose after its edit, per the plan's own design). Until that fix lands, deploying `AUDIO_ENGINE_TIMEOUT=742s` against the STALE 300s CAP would violate the Phase-31 invariant (stale-after > engine timeout) and reopen spurious crashed-worker recovery of legitimate in-flight audio jobs.

## AUDIO_WORKER_CONCURRENCY Derivation

- Measured single-job peak RSS: **~728.4 MiB** (cgroup `memory.peak`, container limit 1024 MiB / `1g`)
- **Memory-fit check** (concurrency × peak_RSS ≤ memory limit, with margin): `2 × 728.4 MiB = 1456.8 MiB > 1024 MiB` → **FAILS** for concurrency=2
- **CPU-fit check** (concurrency × threads-per-job ≤ cpu quota, no oversubscription): `-t 2` is already sized to consume the FULL 2.0-cpu quota per job; `2 × 2 threads = 4` on a `2.0`-cpu quota → **oversubscribes 2x** → **FAILS** for concurrency=2
- **Decision: `AUDIO_WORKER_CONCURRENCY = 1`** — both independent checks fail hard for concurrency=2 (not a marginal call), confirming PITFALLS.md Pitfall 5's prediction with a measured number rather than the `.env.example` placeholder's assumed `2`.

## Decisions Made

- **`AUDIO_ENGINE_TIMEOUT = 742s`** (derived; NO-GO lever applied; GO decision depends on Plan 04's stale-5m→15m fix — see flag above)
- **`AUDIO_MAX_DURATION_SECONDS = 1800s`** (lowered from the 14400s/4h placeholder via the documented lever, not the byte-bound alone — the byte-bound (3276s) was never the true binding constraint once the CAP math is worked through; the CAP itself forced the ceiling down to ~36.4 min regardless of which reasonable bitrate assumption is used for the byte-bound step)
- **`AUDIO_WORKER_CONCURRENCY = 1`** (measured from peak RSS + cpu-quota fit checks, both fail independently for 2)
- `-t 2`, `--cpus=2.0`, `--memory=1g` (unchanged from Plan 01/02's compose-block convention, confirmed live via the same container this measurement ran against)
- `min_expected_bitrate_bytes_per_s = 32000` (WAV/PCM rate) chosen for the byte-bound calculation, documented as the conservative choice; noted that this specific choice ended up NOT mattering for the final decision since the CAP constraint (not the byte-bound) is what actually forced `AUDIO_MAX_DURATION_SECONDS` down to 1800s

**These values (`AUDIO_ENGINE_TIMEOUT=742s`, `AUDIO_MAX_DURATION_SECONDS=1800s`, `AUDIO_WORKER_CONCURRENCY=1`, `-t 2`) are the hard inputs Plan 04 propagates into `docker-compose.yml`/`.env.example` (IN-02), alongside the mandatory `RECONCILER_ACTIVE_STALE_AFTER` 5m→15m fix this SUMMARY's GO decision depends on.**

## Deviations from Plan

None - plan executed exactly as written. The NO-GO lever firing on the first derivation pass was an anticipated, documented decision branch in the plan itself (Open Question 1, RESOLVED), not a deviation — Task 2's own `<action>` explicitly describes this exact branch ("If derived timeout ≥ CAP → NO-GO ... apply the documented LEVER").

## Issues Encountered
None. The measurement ran cleanly end-to-end on the first attempt (script exit code 0); no auto-fixes were needed.

## User Setup Required
None - no external service configuration required. The measurement runs entirely against the already-built local image and committed fixtures.

## Next Phase Readiness
- Plan 04 has every number it needs to propagate `AUDIO_ENGINE_TIMEOUT=742s`, `AUDIO_MAX_DURATION_SECONDS=1800s`, `AUDIO_WORKER_CONCURRENCY=1`, and the audio-worker compose block's `--cpus=2.0 --memory=1g` (with `AUDIO_THREADS`/cgroup auto-detection producing `-t 2` at runtime, per Plan 02).
- **Hard dependency for Plan 04:** the GO decision on 742s is conditioned on Plan 04 correcting `docker-compose.yml`'s stale `RECONCILER_ACTIVE_STALE_AFTER: "5m"` (lines 185, 215, webhook-worker-1/2) to `15m`, restoring the Phase-31 code default. Plan 04 Task 2 must re-validate this invariant grep-wise post-edit before shipping.
- `scripts/audio-rtf-measure.sh` is reusable for any future re-measurement (e.g. an amd64 cross-check, or after a model/whisper.cpp version bump) via `AUDIO_RTF_MEASURE_RUNS`/`AUDIO_RTF_IMAGE_TAG`/`AUDIO_RTF_CPUS`/`AUDIO_RTF_MEMORY`/`AUDIO_RTF_FIXTURE_DURATION_S` env overrides.
- No blockers for Plan 04.

---
*Phase: 32-containerization-local-e2e-rtf-gate*
*Completed: 2026-07-18*

## Self-Check: PASSED
- FOUND: scripts/audio-rtf-measure.sh
- FOUND: 9f61296 (Task 1 commit)
- FOUND: .planning/phases/32-containerization-local-e2e-rtf-gate/32-03-SUMMARY.md
