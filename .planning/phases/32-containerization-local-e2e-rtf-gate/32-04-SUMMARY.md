---
phase: 32-containerization-local-e2e-rtf-gate
plan: 04
subsystem: infra
tags: [docker-compose, ci, audio-worker, whisper.cpp, reconciler, env-consistency]

# Dependency graph
requires:
  - phase: 32-containerization-local-e2e-rtf-gate
    plan: 01
    provides: Dockerfile.audio-worker (built image, no platform pin)
  - phase: 32-containerization-local-e2e-rtf-gate
    plan: 02
    provides: cgroup-based AUDIO_THREADS auto-detection (cgroupCPULimit())
  - phase: 32-containerization-local-e2e-rtf-gate
    plan: 03
    provides: "RTF-measured AUDIO_ENGINE_TIMEOUT=742s, AUDIO_WORKER_CONCURRENCY=1, AUDIO_MAX_DURATION_SECONDS=1800 (32-03-SUMMARY.md)"
provides:
  - "docker-compose.yml audio-worker service (closes IN-04 -- audio jobs previously had no compose consumer)"
  - "IN-02 closed: AUDIO_ENGINE_TIMEOUT/AUDIO_MAX_RETRY identical across all 7 queue.NewClient()-constructing services in one commit"
  - "IN-16 closed: stale Phase-16 RECONCILER_ACTIVE_STALE_AFTER 5m override on webhook-worker-1/2 corrected to 15m, restoring the Phase-31 code default"
  - ".github/workflows/ci.yml bake matrix builds audio-worker (docker-build cache-to/from + e2e cache-from)"
  - ".env.example ships the RTF-measured AUDIO_ENGINE_TIMEOUT/AUDIO_WORKER_CONCURRENCY/AUDIO_MAX_DURATION_SECONDS, no [ASSUMED] placeholder"
affects: [33-keda-audio]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "DEBT-05 cross-engine env block extension: when a new engine class's timeout/retry vars are introduced, they must be added identically to every queue.NewClient()-constructing service block in the same commit, not just the new service's own block"

key-files:
  created: []
  modified: [docker-compose.yml, .github/workflows/ci.yml, .env.example]

key-decisions:
  - "webhook-worker-1/2 previously omitted ALL engine-class MAX_RETRY/TIMEOUT vars (relying on code defaults); this phase closes that gap for AUDIO_* only on those two services -- the pre-existing DOCUMENT_*/HTML_* omission on webhook-worker is out of scope per the plan and left as pre-existing debt"
  - "RECONCILER_ACTIVE_STALE_AFTER changed via plain literal (5m -> 15m), not a ${VAR:-default} passthrough, matching the file's existing env-literal style on those two blocks"
  - "AUDIO_THREADS left unset in the audio-worker compose block -- cgroup auto-detection (Plan 02) derives -t 2 at runtime from the service's own --cpus=2.0 limit, matching the RTF measurement container exactly; no override recommended by the measurement"

patterns-established: []

requirements-completed: [AUD-06, AUD-07]

# Metrics
duration: 25min
completed: 2026-07-18
---

# Phase 32 Plan 04: Wire RTF-Measured Audio Config into Compose + CI Summary

**Added the audio-worker compose service with RTF-measured values (742s timeout, concurrency=1, cpus=2.0/memory=1g), propagated AUDIO_ENGINE_TIMEOUT/AUDIO_MAX_RETRY identically across all 7 queue.NewClient()-constructing services (IN-02), corrected the stale Phase-16 5m reconciler-CAP override on both webhook-workers to 15m (IN-16), added audio-worker to the CI bake matrix, and replaced .env.example's [ASSUMED] placeholder with the measured values.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-07-18T20:05:00Z (approx, first file read)
- **Completed:** 2026-07-18T20:30:00Z
- **Tasks:** 3/3 completed
- **Files modified:** 3 (docker-compose.yml, .github/workflows/ci.yml, .env.example)

## Accomplishments
- `audio-worker` compose service added: structural copy of `document-worker`'s shape without a `platform:` pin (whisper.cpp is source-built portable, chromium-worker precedent); `AUDIO_WORKER_CONCURRENCY=1`, `AUDIO_ENGINE_TIMEOUT=742s`, `AUDIO_MAX_DURATION_SECONDS=1800`, `AUDIO_MODEL_PATH=/models/ggml-base.bin`, `cpus=2.0`/`memory=1g` matching the exact RTF-measurement container. This closes IN-04: audio jobs the API already enqueues previously had no consumer.
- `AUDIO_ENGINE_TIMEOUT: "742s"` + `AUDIO_MAX_RETRY: "3"` added identically to all six pre-existing `queue.NewClient()`-constructing services (`api`, `worker`, `document-worker`, `chromium-worker`, `webhook-worker-1`, `webhook-worker-2`), each with the same DEBT-05-style inline comment explaining why. Closes IN-02: `queue.NewClient()` (`internal/queue/client.go:87-88`) reads these unconditionally in every process and derives `audioUniqueTTL` from them — a stale value anywhere reopens the T-03-10 double-processing race.
- `RECONCILER_ACTIVE_STALE_AFTER: "5m"` (stale Phase-16 override, lines 185/215 of the pre-edit file) corrected to `"15m"` on both `webhook-worker-1` and `webhook-worker-2`, restoring the Phase-31 code default. This closes IN-16: `742s > 300s` (the stale value) but `742s < 900s` (the correct 15m CAP) — deploying the RTF-measured timeout against the un-fixed stale CAP would have made the reconciler recover still-running audio jobs.
- CI bake matrix (`docker/bake-action@v7`, auto-derives targets from compose `build:` blocks) extended with `audio-worker.cache-to`/`audio-worker.cache-from` in the `docker-build` job and `audio-worker.cache-from` in the `e2e` job — no other CI change needed since `docker compose up -d` in the e2e job has no explicit service list.
- `.env.example`'s `AUDIO_ENGINE_TIMEOUT=600s # [ASSUMED] placeholder` replaced with `742s` plus the full derivation formula in the comment; `AUDIO_WORKER_CONCURRENCY` updated from the guessed `2` to the measured `1`; `AUDIO_MAX_DURATION_SECONDS` lowered from the `14400` placeholder to the lever-derived `1800`; both `AUDIO_ENGINE_TIMEOUT` and `AUDIO_MAX_RETRY` comments now state the IN-02 cross-process-identical requirement explicitly.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the audio-worker service block to docker-compose.yml** - `905b3e7` (feat)
2. **Task 2: IN-02 propagation + webhook-worker stale-CAP fix; update .env.example** - `137c6d0` (fix)
3. **Task 3: Add audio-worker to the CI bake matrix** - `6fc955e` (feat)

## Files Created/Modified
- `docker-compose.yml` - New `audio-worker` service block (measured values, no platform pin); `AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY` added to `api`/`worker`/`document-worker`/`chromium-worker`/`webhook-worker-1`/`webhook-worker-2`; `RECONCILER_ACTIVE_STALE_AFTER` corrected from `"5m"` to `"15m"` on both webhook-workers
- `.github/workflows/ci.yml` - `audio-worker` cache-to/cache-from scope lines added to `docker-build` and `e2e` job `set:` blocks
- `.env.example` - `AUDIO_ENGINE_TIMEOUT`, `AUDIO_WORKER_CONCURRENCY`, `AUDIO_MAX_DURATION_SECONDS` updated to measured values with derivation comments; `AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY` comments note the IN-02 cross-process-identical requirement

## Decisions Made
- webhook-worker-1/2 previously omitted ALL engine-class `MAX_RETRY`/`TIMEOUT` vars (relying on code defaults); this phase closes the gap for `AUDIO_*` only on those two services — the plan explicitly scoped out retrofitting the pre-existing `DOCUMENT_*`/`HTML_*` omission there.
- `RECONCILER_ACTIVE_STALE_AFTER` was changed via a plain literal (`"5m"` → `"15m"`), not a `${VAR:-default}` passthrough, matching the existing env-literal style on those two blocks (the plan's explicit instruction).
- `AUDIO_THREADS` left unset in the compose block — cgroup auto-detection (Plan 02) derives `-t 2` at runtime from the service's own `--cpus=2.0` limit, matching the RTF measurement container exactly; the measurement gave no reason to override.

## Reconciler Invariant Cross-Check (closed, per plan's required output)

Re-validated grep-wise against the **actual deployed** `docker-compose.yml` after this plan's edits (not just Plan 03's noted assumption):

```
$ grep -c 'RECONCILER_ACTIVE_STALE_AFTER: "5m"' docker-compose.yml
0
$ grep -c 'RECONCILER_ACTIVE_STALE_AFTER: "15m"' docker-compose.yml
2
$ grep -c 'AUDIO_ENGINE_TIMEOUT:' docker-compose.yml
7
```

**AUDIO_ENGINE_TIMEOUT (742s) is strictly below every deployed RECONCILER_ACTIVE_STALE_AFTER (15m/900s) in the file** — margin = 158s (17.6% headroom), matching Plan 03's GO-decision math exactly. Plan 03's GO decision explicitly depended on this fix landing; it is now closed against the real deployed file, not just asserted.

## Deviations from Plan

None - plan executed exactly as written. All three tasks' acceptance gates passed on the first attempt:
- `grep -c AUDIO_ENGINE_TIMEOUT: docker-compose.yml` == 7
- `grep -c AUDIO_MAX_RETRY: docker-compose.yml` == 7
- `grep -c 'RECONCILER_ACTIVE_STALE_AFTER: "5m"' docker-compose.yml` == 0
- `grep -c 'RECONCILER_ACTIVE_STALE_AFTER: "15m"' docker-compose.yml` == 2
- `docker compose config` validates and shows `octoconv-audio-worker`
- `audio-worker.cache-from=type=gha,scope=audio-worker` appears exactly twice in `ci.yml`; `cache-to` appears once
- No `ASSUMED` string remains in `.env.example`

## Issues Encountered
None.

## User Setup Required
None. All changes are declarative config (compose/CI/env-example); no external service configuration needed. The next live verification (bringing up the full stack with `docker compose up`, confirming `audio-worker` reaches healthy status, and running an end-to-end audio conversion) is deferred to the phase's own verification step, not this plan's scope.

## Next Phase Readiness
- IN-02 and IN-04 (31-REVIEW.md findings) are closed for the audio engine class.
- The reconciler invariant (`AUDIO_ENGINE_TIMEOUT` strictly below `RECONCILER_ACTIVE_STALE_AFTER`) holds against the actual deployed `docker-compose.yml`, with 17.6% margin.
- CI's `docker-build` job now builds 7 targets instead of 6; the plan's own note flagged the 20-minute `docker-build` timeout and 30-minute `e2e` `-timeout` as worth re-evaluating "if evidence shows it tight after the first live run" — no such evidence exists yet from this plan (no live CI run was triggered), so no timeout bump was made pre-emptively, per the plan's explicit instruction not to.
- Phase 33 (KEDA audio scaling) inherits the same `AUDIO_ENGINE_TIMEOUT=742s`/`AUDIO_WORKER_CONCURRENCY=1` values now propagated through compose.
- No blockers.

---
*Phase: 32-containerization-local-e2e-rtf-gate*
*Completed: 2026-07-18*

## Self-Check: PASSED
- FOUND: docker-compose.yml
- FOUND: .github/workflows/ci.yml
- FOUND: .env.example
- FOUND: .planning/phases/32-containerization-local-e2e-rtf-gate/32-04-SUMMARY.md
- FOUND: 905b3e7 (Task 1 commit)
- FOUND: 137c6d0 (Task 2 commit)
- FOUND: 6fc955e (Task 3 commit)
