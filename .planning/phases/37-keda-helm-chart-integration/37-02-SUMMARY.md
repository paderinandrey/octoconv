---
phase: 37-keda-helm-chart-integration
plan: 02
subsystem: infra
tags: [keda, helm, kubernetes, ffmpeg, av, load-proof, autoscaling]

# Dependency graph
requires:
  - phase: 37-keda-helm-chart-integration
    provides: "Plan 01's av-worker Deployment + av KEDA ScaledObject chart templates, avWorker/keda.av values.yaml blocks (grace 783s, non-null stabilization 900s)"
  - phase: 28-autoscale-load-proof
    provides: "SC3/SC4 live load-proof pattern (victim-pod pod-deletion-cost annotation, watch-based terminal-state capture, D-09-style triple-check)"
  - phase: 33-keda-helm-chart-integration
    provides: "scripts/keda-audio-loadproof.sh structural precedent (self-contained KEDA install, compose-up guard, EXIT teardown, event-timeline capture)"
  - phase: 36-containerization-rtf-measured-timeout
    provides: "AV_ENGINE_TIMEOUT=753s, worst measured RTF cell hevc@1080 (p95=4.179133s), AV_MAX_DURATION_SECONDS=90"
provides:
  - "scripts/keda-av-loadproof.sh — av scale-from-zero live-proof gate (SC3, D-07 proof #1), statically verified"
  - "scripts/keda-av-downscale-survival.sh — av N->N-1 downscale-survival live-proof gate (SC4, D-07 proof #2), statically verified"
  - "deploy/chart/octoconv/values-loadproof.yaml keda.av.scaleDownStabilizationSeconds:15 override"
affects: [37-03-keda-helm-chart-integration]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "In-container ffmpeg-lavfi fixture synthesis + docker cp to host (av has no committed video binary, unlike audio's jfk.wav) — throwaway container built FROM the operator-prebuilt octoconv-av-worker:dev image, torn down via the same EXIT trap that guards the k8s resources"
    - "Explicit {\"codec\":\"hevc\",\"resolution_height\":1080} opts forces the AVC-01 full re-encode path (not the AVC-05 stream-copy remux) so the downscale-survival fixture is genuinely in-flight when the observable downscale fires"

key-files:
  created:
    - scripts/keda-av-loadproof.sh
    - scripts/keda-av-downscale-survival.sh
  modified:
    - deploy/chart/octoconv/values-loadproof.yaml

key-decisions:
  - "Two distinct gate scripts (not one), mirroring 37-CONTEXT.md D-07's requirement for two separate live proofs (scale-from-zero vs downscale-survival), each self-contained with its own KEDA install/teardown"
  - "keda-av-loadproof.sh's SC3 trigger fixture is a fast AVC-05 stream-copy remux (mkv h264/aac -> mp4, no opts) — deliberately NOT a heavy re-encode, since SC3 only needs a real job to land on the av queue and trigger 0->1->0, and a fast job keeps the full cycle observable quickly"
  - "keda-av-downscale-survival.sh's long fixture forces a genuine hevc@1080 re-encode via explicit opts (codec=hevc, resolution_height=1080) at 85s duration (under the AV_MAX_DURATION_SECONDS=90 ceiling), yielding a ~hundreds-of-seconds wall-clock transcode at the measured worst-cell RTF (4.179133), comfortably outliving the 15s observable-downscale window while staying under the 783s production grace"
  - "values-loadproof.yaml's keda.av override adds ONLY scaleDownStabilizationSeconds:15 — no terminationGracePeriodSeconds override anywhere, since that value (783s) is exactly what SC4 validates live"
  - "Distinct local port-forward ports per gate script (18093/15437 for scale-from-zero, 18094/15438 for downscale-survival) so no concurrently-running sibling gate can collide"

patterns-established:
  - "Fixture-synthesis containers get their own teardown-trap cleanup slot (FIXTURE_CONTAINER var + docker rm -f), independent of the k8s-resource teardown, so a failure mid-synthesis never leaves a throwaway container running"

requirements-completed: []

# Metrics
duration: 30min
completed: 2026-07-23
---

# Phase 37 Plan 02: av Engine KEDA Live-Proof Instruments Summary

**Two statically-verified live-proof gate scripts (scripts/keda-av-loadproof.sh for scale-from-zero, scripts/keda-av-downscale-survival.sh for N->N-1 downscale survival) plus a values-loadproof.yaml keda.av.scaleDownStabilizationSeconds:15 override, ready for Plan 03's live cluster execution.**

## Performance

- **Duration:** ~30 min
- **Started:** 2026-07-23T19:05Z (approx)
- **Completed:** 2026-07-23T19:35Z
- **Tasks:** 2/2 completed
- **Files modified:** 3 (2 created, 1 edited)

## Accomplishments
- Authored `scripts/keda-av-loadproof.sh`: a structural clone of `scripts/keda-audio-loadproof.sh` for the av engine class — self-contained KEDA install, compose-up-down guard, `asynq:queues` seeding (including `av`), an ffmpeg-lavfi in-container fixture synthesis (`docker run`/`exec`/`cp` against the operator-prebuilt `octoconv-av-worker:dev` image, since av has no committed video fixture), a Phase-28-style timestamped pod event-timeline capture (Scheduled->Pulling->Pulled->Created->Started), and a full 0->1->0 replica-cycle proof. No calibration mode (`AV_ENGINE_TIMEOUT=753s` is already RTF-measured).
- Authored `scripts/keda-av-downscale-survival.sh`: the second D-07 live-proof instrument (SC4) — layers `values-local.yaml` + `values-loadproof.yaml`, submits a LONG hevc@1080 re-encode job first (forcing a genuine re-encode via explicit opts, not a stream-copy remux) then a SHORT job second to scale av-worker 1->2, identifies + annotates the busy pod (Phase-28 SC3 document-precedent pattern, read-only reused), watches for the genuine KEDA/HPA 2->1 downscale, and triple-checks the long job survives gracefully: exactly one queued->active transition, SIGTERM strictly before job completion, and pod exit code/reason proving NOT exit-137/SIGKILL — validating `avWorker.terminationGracePeriodSeconds=783` against a real cluster event.
- Added `keda.av.scaleDownStabilizationSeconds: 15` to `deploy/chart/octoconv/values-loadproof.yaml`, mirroring the existing `keda.document` override's shape/comment style, with an explicit statement that the production grace period is left untouched by this or any overlay.
- Verified: `bash -n` clean on both scripts, both executable; grep gates confirm lavfi fixture synthesis, EXIT-trap teardown with process-group+pkill watcher discipline, `asynq:queues` seeding includes `av`, no calibration mode, `sc3-av-scale-from-zero`/`sc4-av-downscale-survival` evidence-file naming present, the `keda.av` block in `values-loadproof.yaml` carries `scaleDownStabilizationSeconds: 15` with no `terminationGracePeriodSeconds` override anywhere in that file, and `scripts/keda-load-proof.sh`/`scripts/keda-gate.sh`/`scripts/keda-audio-loadproof.sh` remain byte-unchanged (`git diff --quiet` gate passes).

## Task Commits

Each task was committed atomically:

1. **Task 1: Author scripts/keda-av-loadproof.sh** - `8aaab87` (feat)
2. **Task 2: Author scripts/keda-av-downscale-survival.sh + values-loadproof.yaml av override** - `a66bab2` (feat)

**Plan metadata:** (this commit, docs: complete plan)

_Note: No TDD tasks in this plan — plain `type="auto"` shell-script authoring/static-verification, no live cluster interaction (that is Plan 03's scope)._

## Files Created/Modified
- `scripts/keda-av-loadproof.sh` - new av scale-from-zero live-proof gate (SC3)
- `scripts/keda-av-downscale-survival.sh` - new av N->N-1 downscale-survival live-proof gate (SC4)
- `deploy/chart/octoconv/values-loadproof.yaml` - added `keda.av.scaleDownStabilizationSeconds: 15` override

## Decisions Made
All captured in the frontmatter `key-decisions` above; summarized:
- Two separate gate scripts (not one combined script), per 37-CONTEXT.md D-07's explicit two-proof requirement.
- SC3's trigger job is a fast stream-copy remux (mkv h264/aac -> mp4, no opts) — sufficient to trigger and observe 0->1->0, deliberately not a heavy re-encode.
- SC4's long job forces a genuine hevc@1080 re-encode via explicit `{"codec":"hevc","resolution_height":1080}` opts at an 85s fixture duration, landing near the measured worst-cell RTF envelope (hevc@1080, p95=4.179133s) so the transcode is still in-flight when the 15s-stabilized downscale fires.
- `values-loadproof.yaml`'s new `keda.av` block adds only the stabilization override; no grace-period override anywhere, preserving the exact 783s value SC4 validates.
- Distinct local port-forward ports per script, avoiding any collision with `keda-gate.sh` (18090/15434), `keda-load-proof.sh` (18090/15434), and `keda-audio-loadproof.sh` (18092/15436).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] values-loadproof.yaml comment initially repeated the literal `terminationGracePeriodSeconds` token, tripping the plan's own negative-assertion grep gate**
- **Found during:** Task 2 verification (self-caught before running the automated `<verify>` block)
- **Issue:** The first draft of the `keda.av` block's explanatory comment stated the production grace period stays at "783s" by name, using the literal string `terminationGracePeriodSeconds`, which would make `! grep -qi 'terminationGracePeriodSeconds' deploy/chart/octoconv/values-loadproof.yaml` fail (the gate requires that exact token be absent from the whole file, since the file must never carry a grace-period override).
- **Fix:** Reworded the comment to describe the value generically ("the av Deployment's pod-termination grace period stays at its PRODUCTION 783s value") instead of repeating the literal YAML key, preserving the same rationale (and the numeric 783s value) without tripping the grep gate.
- **Files modified:** `deploy/chart/octoconv/values-loadproof.yaml`
- **Verification:** Task 2's full automated `<verify>` command re-run, all assertions pass
- **Committed in:** `a66bab2` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (1 bug — comment wording, self-caught before the task's own verify gate)
**Impact on plan:** No scope creep. Purely a wording fix to satisfy the plan's own negative-assertion gate; no functional change to the deliverable, and the numeric 783s rationale is preserved in the comment.

## Issues Encountered
None beyond the single item documented above (self-caught and resolved before task completion).

## User Setup Required

None - no external service configuration required. This plan only authors and statically verifies shell scripts + a values overlay; no live cluster interaction (that is Plan 03's scope). Both scripts assume `octoconv-av-worker:dev` is already built locally (`docker compose build av-worker`) before they run — both fail loudly and early (Task 1's STEP 1 preflight) if the image is missing, rather than attempting an unverified build or pull.

## Next Phase Readiness

- Plan 03 can run both gates live as-is: `scripts/keda-av-loadproof.sh` for SC3 (scale-from-zero) and `scripts/keda-av-downscale-survival.sh` for SC4 (downscale survival), each self-contained (installs/uninstalls KEDA + octoconv, refuses to run if compose is hot).
- **AVE-05 is intentionally NOT marked complete by this plan** — per the plan's own scope and this plan's explicit instruction, AVE-05 requires Plan 03's LIVE scale-from-zero proof (an actual OrbStack cluster run producing the timestamped evidence these scripts are designed to emit), not merely the existence of statically-verified instruments. `requirements-completed` in this SUMMARY's frontmatter is deliberately empty; `.planning/REQUIREMENTS.md`'s AVE-05 checkbox remains unchecked until Plan 03 closes it.
- Both scripts write their evidence into `.planning/phases/37-keda-helm-chart-integration/evidence/` using the Phase-28 naming convention (`gate-transcript-*.log`, `sc3-av-scale-from-zero-*.txt`, `sc4-av-downscale-survival-*.txt`) — Plan 03 should commit those evidence files alongside its own SUMMARY.
- No further chart-structural or script-authoring changes are expected for the av engine class in this phase; Plan 03 consumes these two scripts and `values-loadproof.yaml`'s `keda.av` override as-is.

---
*Phase: 37-keda-helm-chart-integration*
*Completed: 2026-07-23*

## Self-Check: PASSED

All created/modified files and both task commit hashes verified present.
