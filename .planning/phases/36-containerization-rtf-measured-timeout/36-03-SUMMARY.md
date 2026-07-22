---
phase: 36-containerization-rtf-measured-timeout
plan: 03
subsystem: infra
tags: [docker-compose, ci, env-parity, av-worker, in-02]

# Dependency graph
requires:
  - phase: 36-containerization-rtf-measured-timeout
    plan: 01
    provides: "AVConverter config-threading (AV_MAX_DURATION_SECONDS/AV_DISK_SAFETY_FACTOR env vars read by cmd/av-worker)"
  - phase: 36-containerization-rtf-measured-timeout
    plan: 02
    provides: "Dockerfile.av-worker (from-source ffmpeg n8.1.2) + scripts/av-rtf-measure.sh"
provides:
  - "av-worker compose service (builds Dockerfile.av-worker, provisional 600s timeout/620s grace-period pairing)"
  - "IN-02 AV_* env parity: AV_ENGINE_TIMEOUT + AV_MAX_RETRY byte-identical across all 8 queue.NewClient()-constructing services"
  - "CI docker-build + e2e jobs bake the av-worker image (isolated gha cache scope)"
  - ".env.example documents AV_MAX_DURATION_SECONDS (NO-GO lever) + AV_DISK_SAFETY_FACTOR"
affects: [36-04-PLAN, phase-37-keda-integration]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Runnable-but-provisional compose wiring: every AV_* value in this plan is labelled '[Plan 04 finalizes]' so a later measurement-only plan can update a single number in 8 places without touching structure"
    - "IN-02 env-parity requirement extended to a 5th engine class (AV), same byte-identical grep-count-8 pattern as AUDIO_* (Phase 32)"

key-files:
  created: []
  modified:
    - docker-compose.yml
    - .github/workflows/ci.yml
    - .env.example

key-decisions:
  - "Discovered mid-task-2: the plan's interfaces section lists audio-worker itself as one of the '7 existing' queue-client services needing AV_* parity (docker-compose.yml:393-394 anchor) -- audio-worker's own env block also constructs queue.NewClient() and was missing from my first pass at Task 2 (grep count landed at 7, not 8, immediately surfacing the gap before commit). Added AV_MAX_RETRY/AV_ENGINE_TIMEOUT to audio-worker's block as well, bringing the total to the required 8."
  - "AV_MAX_DURATION_SECONDS and AV_DISK_SAFETY_FACTOR are deliberately NOT added to docker-compose.yml's 7 non-av-worker services -- only av-worker's own process reads them (queue.NewClient() never touches AV_MAX_DURATION_SECONDS/AV_DISK_SAFETY_FACTOR), so IN-02 parity does not apply to either var."
  - "stop_grace_period=620s / ShutdownTimeout=610s (AV_ENGINE_TIMEOUT+10s) pairing mirrors audio-worker's 762s/752s precedent exactly -- both values are provisional and explicitly labelled for Plan 04's RTF measurement to update in lockstep."

patterns-established: []

requirements-completed: [AVE-04]

# Metrics
duration: ~20min
completed: 2026-07-22
---

# Phase 36 Plan 03: Deployment Surface — av-worker Compose Service, IN-02 Parity, CI Bake Summary

**Wired the full runnable-but-provisional deployment surface for the av engine class: a new `av-worker` compose service, `AV_ENGINE_TIMEOUT`/`AV_MAX_RETRY` byte-identical across all 8 `queue.NewClient()`-constructing services (IN-02), the CI bake matrix entry, and `.env.example` documentation for the two net-new `AV_*` knobs — all measured values remain explicitly labelled placeholders pending Plan 04's supervised RTF run.**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-07-22
- **Tasks:** 3/3 completed
- **Files modified:** 3 (docker-compose.yml, .github/workflows/ci.yml, .env.example)

## Accomplishments

- `av-worker` compose service builds `Dockerfile.av-worker`, `container_name: octoconv-av-worker`, `restart: always`, `stop_grace_period: 620s` correctly paired above the code's `ShutdownTimeout` (`AV_ENGINE_TIMEOUT` 600s + 10s = 610s), `depends_on` postgres/redis/minio healthy, `cpus: "2.0"` fleet baseline, no `platform:` pin (ffmpeg runtime CPU dispatch)
- `AV_ENGINE_TIMEOUT="600s"` + `AV_MAX_RETRY="2"` now present and byte-identical across all 8 queue-client services (api, worker, webhook-worker-1, webhook-worker-2, document-worker, chromium-worker, audio-worker, av-worker) — `AVUniqueTTL` can no longer silently diverge between the enqueuer and the consumer (D-08/IN-02, T-36-08 mitigated)
- `AV_MAX_DURATION_SECONDS="14400"` (the NO-GO lever) is present ONLY on av-worker — confirmed `queue.NewClient()` never reads it, so it is correctly excluded from the 8-way parity requirement
- `docker compose config` validates the full stack with all changes applied
- CI `docker-build` job bakes `av-worker` with its own isolated `gha` cache scope (cache-to + cache-from); `e2e` job adds the matching `cache-from` — same two-job pattern as every other engine image, no platform pin
- `.env.example` documents `AV_MAX_DURATION_SECONDS` (bare-integer-seconds tolerant, ffprobe-enforced ceiling, explicitly named as the NO-GO lever) and `AV_DISK_SAFETY_FACTOR` (float default 3.0, `[ASSUMED]` per 36-CONTEXT.md); existing `AV_ENGINE_TIMEOUT`/`AV_WORKER_CONCURRENCY` comments updated to note Plan 04 finalizes the real measured values

## Task Commits

Each task was committed atomically:

1. **Task 1: av-worker compose service block** - `eac9d2c` (feat)
2. **Task 2: IN-02 AV_* env parity across the 7 existing queue-client services** - `8db0c23` (feat)
3. **Task 3: CI bake matrix entry + .env.example documentation** - `e929d74` (feat)

## Files Created/Modified

- `docker-compose.yml` - new `av-worker` service (build/env/deploy blocks mirroring audio-worker's shape); `AV_ENGINE_TIMEOUT`/`AV_MAX_RETRY` added to api, worker, webhook-worker-1, webhook-worker-2, document-worker, chromium-worker, and audio-worker
- `.github/workflows/ci.yml` - `av-worker.cache-to`/`av-worker.cache-from` in the `docker-build` job; `av-worker.cache-from` in the `e2e` job
- `.env.example` - `AV_MAX_DURATION_SECONDS` + `AV_DISK_SAFETY_FACTOR` documented (net-new); `AV_ENGINE_TIMEOUT`/`AV_WORKER_CONCURRENCY` comments updated to flag Plan 04 finalization

## Decisions Made

- audio-worker's own env block needed the IN-02 `AV_*` parity pair too (it is itself a `queue.NewClient()`-constructing process) — this was surfaced by the plan's own acceptance criterion (`grep -c "AV_ENGINE_TIMEOUT:"` == 8) failing at 7 on first pass, not missed silently.
- Kept the provisional `AV_ENGINE_TIMEOUT=600s`/`AV_MAX_DURATION_SECONDS=14400` values exactly matching the existing `.env.example`/`cmd/av-worker/main.go` defaults already established in Phase 35/36 Plan 01, rather than inventing new placeholder numbers — minimizes churn when Plan 04 finalizes the real values.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue] audio-worker service missing from Task 2's IN-02 parity edit**

- **Found during:** Task 2 acceptance-criteria verification (`grep -c "AV_ENGINE_TIMEOUT:" docker-compose.yml` returned 7, not the required 8)
- **Issue:** My first pass at Task 2 edited the six services explicitly named in the task's action prose (api, worker, webhook-worker-1, webhook-worker-2, document-worker, chromium-worker) but missed that the plan's `<interfaces>` section separately lists audio-worker (docker-compose.yml:393-394 anchor) as the 7th existing queue-client service requiring the same parity pair — audio-worker's `cmd/audio-worker/main.go` also constructs `queue.NewClient()` unconditionally.
- **Fix:** Added `AV_MAX_RETRY: "2"` / `AV_ENGINE_TIMEOUT: "600s"` to audio-worker's environment block, using the same IN-02 comment style as the other six.
- **Files modified:** `docker-compose.yml`
- **Verification:** `grep -c "AV_ENGINE_TIMEOUT:" docker-compose.yml` == 8, `grep -c "AV_MAX_RETRY:" docker-compose.yml` == 8, both value sets `sort -u` to exactly one line each, `docker compose config -q` validates.
- **Committed in:** `8db0c23` (caught before commit, so the fix is folded into the single Task 2 commit rather than a separate follow-up)

---

**Total deviations:** 1 (Rule 3, caught by the plan's own acceptance criteria before commit — no separate fix commit needed)
**Impact on plan:** None on scope or timeline — the acceptance-criteria grep count is exactly the mechanism the plan specifies for catching this class of gap, and it worked as designed.

## Issues Encountered

None beyond the deviation above, caught and resolved during the plan's own mandated verification step.

## User Setup Required

None — no external service configuration required. All values in this plan are provisional and clearly labelled; Plan 04's supervised RTF measurement run is the next required human/operator action (running `scripts/av-rtf-measure.sh` against the real matrix and accepting the derived numbers), not part of this plan's scope.

## Next Phase Readiness

- `docker compose config -q` validates the full stack with the new `av-worker` service and 8-way `AV_*` parity in place — the stack is runnable end-to-end today at the provisional 600s timeout, ready for Plan 04 to swap in the measured value.
- CI bakes `av-worker` in both the `docker-build` and `e2e` jobs with an isolated cache scope, matching every other engine image's pattern — no CI changes needed when Plan 04 updates the timeout value itself (env-only change).
- `.env.example` fully documents every `AV_*` key `cmd/av-worker` reads, including the two net-new ones from Plan 01 (`AV_MAX_DURATION_SECONDS`, `AV_DISK_SAFETY_FACTOR`) — operators have a complete reference before Plan 04's measurement run.
- No blockers for Plan 04. Plan 04 must update `AV_ENGINE_TIMEOUT` in all 8 docker-compose.yml locations plus `.env.example` (and possibly invoke the `AV_MAX_DURATION_SECONDS` NO-GO lever if the measured RTF would otherwise breach the 900s/15m reconciler cap) — the exact same update pattern Phase 32 followed for `AUDIO_ENGINE_TIMEOUT`.

---
*Phase: 36-containerization-rtf-measured-timeout*
*Completed: 2026-07-22*

## Self-Check: PASSED

- FOUND: docker-compose.yml
- FOUND: .github/workflows/ci.yml
- FOUND: .env.example
- FOUND: .planning/phases/36-containerization-rtf-measured-timeout/36-03-SUMMARY.md
- FOUND commit: eac9d2c
- FOUND commit: 8db0c23
- FOUND commit: e929d74
