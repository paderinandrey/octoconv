---
phase: 37-keda-helm-chart-integration
plan: 03
subsystem: infra
tags: [keda, helm, kubernetes, av, ffmpeg, load-proof, autoscaling, metrics]

# Dependency graph
requires:
  - phase: 37-keda-helm-chart-integration
    provides: "Plan 01's av-worker Deployment + av KEDA ScaledObject chart templates; Plan 02's scripts/keda-av-loadproof.sh + scripts/keda-av-downscale-survival.sh + values-loadproof.yaml keda.av.scaleDownStabilizationSeconds:15 override"
provides:
  - "SC3 live-proven: av scale-from-zero (0->1->0) with timestamped Phase-33-style pod-event-timeline evidence"
  - "SC4 live-proven: a long hevc@1080 av transcode survives a genuine KEDA/HPA 2->1 downscale under production terminationGracePeriodSeconds=783 (SIGTERM before completion, container exit 0, no exit-137/OOMKilled)"
  - "IN-02 AV_* env-parity re-confirmed live across all queue-client Deployments via the shared octoconv-config ConfigMap"
  - "Root cause of the prior BLOCKED run identified and fixed: a stale octoconv-api:dev image predating QueueAV in AllConvertQueues() never emitted octoconv_queue_depth{queue=\"av\"}, so KEDA's ignoreNullValues:\"false\" held fallback.replicas:1 indefinitely"
  - "A second, independent live bug found and fixed: keda-av-downscale-survival.sh's BUSY_POD jsonpath filter inherited the WR-05 defect (absent deletionTimestamp key never matches =="" on kubectl client v1.36.2) -- fixed via -o json | jq since this script (unlike keda-load-proof.sh) is not one of the three frozen scripts"
affects: [phase-37-close, milestone-v1.8-close, av-loadproof-scripts]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "BUSY_POD victim-pod selection via `kubectl get pod -o json | jq 'select(.metadata.deletionTimestamp == null)'` instead of a jsonpath filter against an absent key -- the correct fix for the WR-05 defect class, to be applied to any future non-frozen victim-selection script"

key-files:
  created:
    - .planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-20260723T202158Z.log
    - .planning/phases/37-keda-helm-chart-integration/evidence/sc3-av-scale-from-zero-20260723T202158Z.txt
    - .planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-downscale-20260723T202646Z.log
    - .planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-downscale-20260723T203018Z.log
    - .planning/phases/37-keda-helm-chart-integration/evidence/sc4-av-downscale-survival-20260723T203018Z.txt
  modified:
    - scripts/keda-av-downscale-survival.sh

key-decisions:
  - "Confirmed root-cause hypothesis before running anything: internal/queue/queue.go's AllConvertQueues() already includes QueueAV (current HEAD) -- the previously-BLOCKED run's deployed octoconv-api:dev image (sha d4167d7c1bfa) predated this and never emitted the av queue-depth series. Rebuilt octoconv-api:dev from current HEAD (new sha 7df54cbed57f) BEFORE running either script, per the plan's explicit fix instruction."
  - "Task 1 (keda-av-loadproof.sh) passed clean on the FIRST attempt after the api rebuild -- av-worker settled to genuine 0, scaled 0->1 on the trigger job, drained back to 0. This directly confirms the stale-api-image hypothesis was the sole root cause of the prior BLOCKED run; no other fix was needed for SC3."
  - "Task 2 (keda-av-downscale-survival.sh) failed on its first attempt at STEP 9 (BUSY_POD identification returned empty) -- a SEPARATE, independent bug from the Task 1 root cause: the script's jsonpath filter `{.items[?(@.metadata.deletionTimestamp==\"\")]...}` is the exact WR-05 defect documented as an accepted residual for the FROZEN scripts/keda-load-proof.sh (29-REVIEW.md/33-03-SUMMARY.md) -- kubectl client v1.36.2 never matches an absent key against `==\"\"`. Since keda-av-downscale-survival.sh is explicitly NOT one of the three frozen scripts (per its own header comment), this is a live bug in in-scope code, not an accepted residual to carry forward -- fixed directly (Rule 1) via `-o json | jq 'select(.metadata.deletionTimestamp == null)'`, which correctly treats an absent key the same as null."
  - "Re-ran Task 2 after the jsonpath fix; passed clean on the retry -- all 22 assertions passed, including the SC4 triple-check (SIGTERM before job completion, exactly one queued->active transition, graceful exit 0/Completed, terminationGracePeriodSeconds=783 confirmed on the pod spec)."
  - "Did NOT touch any of the three frozen scripts (keda-load-proof.sh, keda-gate.sh, keda-audio-loadproof.sh) or the production grace-period/stabilization values -- values-loadproof.yaml's 15s override is the only overlay difference from production, exactly as designed."
  - "Task 3 (human-verify checkpoint) intentionally NOT attempted -- operator-owned per this run's explicit scope. AVE-05 is NOT marked complete; phase.complete / roadmap.update-plan-progress are NOT run; those are reserved for after operator approval."

requirements-completed: []

# Metrics
duration: 40min
completed: 2026-07-23
---

# Phase 37 Plan 03: av Live Load-Proof (SC3 + SC4) — RESOLVED after api image rebuild + a live jsonpath bug fix

**Rebuilding octoconv-api:dev from current HEAD (which already has QueueAV wired into AllConvertQueues()) resolved the prior BLOCKED run's STEP 6 failure outright, and Task 1 (SC3 scale-from-zero) passed clean on the first attempt; Task 2 (SC4 downscale-survival) then surfaced and fixed a second, independent live bug (an inherited WR-05-class jsonpath defect in the non-frozen keda-av-downscale-survival.sh) before passing all 22 assertions on retry, live-proving that a long hevc@1080 av transcode survives a genuine KEDA/HPA 2->1 downscale under production terminationGracePeriodSeconds=783 with a graceful exit 0.**

## Performance

- **Duration:** ~40 min (image rebuilds + k8s bring-up + Task 1 clean pass + Task 2 fail/diagnose/fix/retry-pass + teardown)
- **Started:** 2026-07-23T20:19:00Z (approx, `orb start k8s`)
- **Completed:** 2026-07-23T20:38:47Z (`orb stop k8s` confirmed cluster unreachable)
- **Tasks:** 2/2 completed (Task 3 out of scope for this execution, operator-owned)
- **Files modified:** 1 script file (bug fix); 5 evidence files created

## Accomplishments

- Confirmed the root-cause hypothesis at the code level BEFORE running anything live: `internal/queue/queue.go`'s `AllConvertQueues()` (current HEAD) already includes `QueueAV`, confirming the prior BLOCKED run's deployed api image was stale.
- Rebuilt `octoconv-api:dev` from current HEAD sequentially (new image sha `7df54cbed57f...`, replacing the stale `d4167d7c1bfa`), then rebuilt `octoconv-av-worker:dev` (cache-hit, confirmed current, sha `772ab12cf0b1...`) — both confirmed present via `docker image inspect` before any script ran.
- Brought up OrbStack k8s (`orb start k8s`), verified `kubectl cluster-info` reachable and node `orbstack` Ready, confirmed compose stack down (0 `octoconv-*` containers) before any build/install — per the hard environment-discipline rule.
- **Task 1 (SC3):** Ran `scripts/keda-av-loadproof.sh` — passed clean on the FIRST attempt (12/12 assertions). `av-worker` settled to a genuine 0 replicas, scaled 0->1 on the trigger job (an mkv->mp4 stream-copy-eligible lavfi fixture), the job reached `done`, and `av-worker` drained back to 0 — the full scale-from-zero cycle. Event-timeline evidence (`Scheduled/Pulled/Created/Started`, `Pulling`/`Pulled` empty since the image was already locally present — the expected/recorded OrbStack caveat, AVX-02) captured with real timestamps in `sc3-av-scale-from-zero-20260723T202158Z.txt`.
- **Task 2 (SC4), first attempt:** `scripts/keda-av-downscale-survival.sh` passed STEP 1-8 cleanly (KEDA install, octoconv install with both overlays, queue seeding, long hevc@1080 job submitted and confirmed active, av-worker scaled 0->1) but FAILED at STEP 9: `BUSY_POD` identification returned empty after the short job scaled av-worker 1->2.
- **Root-caused the STEP 9 failure live:** the script's victim-pod jsonpath filter `{.items[?(@.metadata.deletionTimestamp=="")]...}` is the exact WR-05 defect already documented as an accepted residual for the three FROZEN scripts (`keda-load-proof.sh`, `29-REVIEW.md`/`33-03-SUMMARY.md`) — kubectl client v1.36.2 never matches an absent `deletionTimestamp` key against `==""` (the key is entirely absent when unset, not present as an empty string). Since `keda-av-downscale-survival.sh` is explicitly NOT one of the three frozen scripts (confirmed via its own file-header comment — av logic "lives exclusively in this file and its sibling"), this defect is in-scope, live code, not an inherited accepted residual to carry forward silently.
- **Fixed the bug (Rule 1 — auto-fix, blocking):** replaced the jsonpath filter with `kubectl get pod ... -o json | jq -r '[.items[] | select(.metadata.deletionTimestamp == null)][0].metadata.name // empty'` — `jq` correctly treats an absent key the same as an explicit `null`, unlike kubectl's jsonpath filter comparison. Committed separately (`839d70b`) before re-running.
- **Task 2, retry after fix:** Ran to completion, all 22 assertions passed. `BUSY_POD` correctly identified (`av-worker-798bcfbbbd-5r4j4`), annotated with `pod-deletion-cost=-1000` before the downscale decision, `spec.terminationGracePeriodSeconds` confirmed `783` (the PRODUCTION value, even under the loadproof overlay). The genuine KEDA/HPA 2->1 downscale fired, delivering SIGTERM (kubelet `Killing` event at `20:32:30Z`) to the busy pod WHILE the long transcode was still in flight; the job reached `done` at `20:37:58Z` (~5m28s after SIGTERM, well inside the 783s grace window), the container terminated `reason=Completed exit=0` (NOT 137/SIGKILL/OOMKilled), and exactly one `queued->active` transition was recorded in `job_events` (no false retry from a premature kill). Evidence in `sc4-av-downscale-survival-20260723T203018Z.txt`.
- Re-confirmed IN-02 AV_* env-parity live via `helm template ... -f values-local.yaml`: 10 occurrences of `octoconv-config` ConfigMap references across all 6 queue-client Deployments (api, worker, document-worker, audio-worker, av-worker, webhook-worker — `chromium-worker` also present as a 7th consumer via `envFrom`), confirming `AV_ENGINE_TIMEOUT`/`AV_MAX_RETRY` propagate via the shared ConfigMap everywhere a `queue.NewClient()`-constructing process runs.
- Confirmed clean teardown after BOTH scripts' EXIT traps (including after the failed first Task 2 attempt): `helm list -A` empty, `kubectl get deployment -n octoconv`/`kubectl get all -n keda` empty, no orphaned `kubectl get pod`/`kubectl port-forward` processes (`pgrep` checks empty after every run).
- Stopped k8s (`orb stop k8s`) at the end; confirmed `kubectl cluster-info` unreachable (connection refused) and `docker compose ps` empty — both stacks DOWN, OrbStack at rest.

## Task Commits

1. **Task 1: Bring up k8s, sequential image builds, run keda-av-loadproof.sh, capture SC3 evidence** — no code changes required (the fix was rebuilding `octoconv-api:dev` from current HEAD, a Docker image rebuild, not a git-tracked change); evidence committed in `2ef9751`.
2. **Task 2: Run keda-av-downscale-survival.sh (SC4), confirm env-parity, leave both stacks down** — bug fix `839d70b` (`fix(37-03): fix WR-05 jsonpath BUSY_POD defect in keda-av-downscale-survival.sh`), evidence (both the failed first attempt and the passing retry) committed in `2ef9751`.

**Plan metadata:** (this commit, `docs(37-03): re-run Tasks 1-2 after api rebuild + jsonpath fix`)

_Note: No TDD tasks in this plan — plain `type="auto"` live-cluster execution tasks._

## Files Created/Modified

- `scripts/keda-av-downscale-survival.sh` - fixed the WR-05-class BUSY_POD jsonpath defect (switched to `-o json | jq` for correct absent-key handling)
- `.planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-20260723T202158Z.log` - Task 1 full run transcript (PASS, 12/12 assertions)
- `.planning/phases/37-keda-helm-chart-integration/evidence/sc3-av-scale-from-zero-20260723T202158Z.txt` - SC3 pod-event-timeline evidence (0->1->0 scale cycle, real timestamps)
- `.planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-downscale-20260723T202646Z.log` - Task 2 FIRST (failed) attempt transcript, kept as diagnostic history of the BUSY_POD bug discovery
- `.planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-downscale-20260723T203018Z.log` - Task 2 retry (PASS, 22/22 assertions) full run transcript
- `.planning/phases/37-keda-helm-chart-integration/evidence/sc4-av-downscale-survival-20260723T203018Z.txt` - SC4 SIGTERM-before-completion triple-check evidence (grace 783 honored, exit 0, no exit-137)

## Decisions Made

See `key-decisions` in frontmatter. Summary: (1) rebuilt `octoconv-api:dev` from current HEAD per the plan's explicit fix instruction, confirmed it resolved Task 1's prior STEP 6 blocker; (2) fixed a second, independent live bug in Task 2's non-frozen script rather than treating it as an inherited accepted residual, since the frozen-script exemption does not apply to `keda-av-downscale-survival.sh`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed WR-05-class BUSY_POD jsonpath defect in keda-av-downscale-survival.sh**
- **Found during:** Task 2, first live attempt (STEP 9)
- **Issue:** `kubectl get pod ... -o jsonpath='{.items[?(@.metadata.deletionTimestamp=="")].metadata.name}'` never matched any pod on kubectl client v1.36.2, because `deletionTimestamp` is entirely absent from the JSON when unset (not present as an empty string), and kubectl's jsonpath filter comparison against an absent key never evaluates true. `BUSY_POD` was always empty, failing `assert_nonempty`.
- **Fix:** Replaced with `kubectl get pod ... -o json | jq -r '[.items[] | select(.metadata.deletionTimestamp == null)][0].metadata.name // empty'` — `jq` correctly treats an absent key the same as an explicit `null`.
- **Files modified:** `scripts/keda-av-downscale-survival.sh`
- **Verification:** Re-ran the full gate after the fix; `BUSY_POD` correctly identified and all 22 assertions passed, including the SC4 triple-check.
- **Committed in:** `839d70b`

---

**Total deviations:** 1 auto-fixed (1 blocking bug fix, Rule 1)
**Impact on plan:** Necessary to complete Task 2 at all — no scope creep. The fix is scoped to a single jsonpath-selector line and does not touch the frozen scripts, the production grace-period values, or any chart template.

## Issues Encountered

**RESOLVED: Prior BLOCKED-run root cause (stale api image missing QueueAV in its metrics collector).** Confirmed at the code level before this run (current HEAD's `internal/queue/queue.go` `AllConvertQueues()` already includes `QueueAV`) and empirically confirmed by Task 1 passing clean on the first attempt immediately after rebuilding `octoconv-api:dev` from current HEAD. No further action needed — this was purely an environment/image-staleness issue, not a code defect.

**RESOLVED: keda-av-downscale-survival.sh BUSY_POD jsonpath defect (see Deviations above).** Fixed directly since this script is not one of the three frozen scripts.

## User Setup Required

None. `octoconv-api:dev` and `octoconv-av-worker:dev` are now current and confirmed present locally for any future retry (no rebuild needed unless source changes again).

## Next Phase Readiness — Task 3 (human-verify) PENDING, do not proceed to phase completion yet

- **Task 3 (human-verify checkpoint): PENDING (operator).** Both live proofs (SC3, SC4) now have passing, committed evidence for the operator to review per the plan's `<how-to-verify>` steps (`sc3-av-scale-from-zero-20260723T202158Z.txt`, `sc4-av-downscale-survival-20260723T203018Z.txt`, both gate transcripts, and the `helm template` IN-02 parity check above).
- **Do NOT mark AVE-05 complete.** `.planning/REQUIREMENTS.md`'s AVE-05 checkbox must remain unchecked until the operator approves Task 3.
- **Do NOT run `phase.complete` or `roadmap.update-plan-progress` for Phase 37.** Reserved for the orchestrator after operator approval of Task 3.
- **Environment state at hand-back:** both stacks confirmed DOWN — `docker compose ps` empty, `kubectl cluster-info` unreachable (`orb stop k8s` completed), `helm list -A`/`kubectl get all -n keda`/`kubectl get all -n octoconv` all empty before k8s was stopped, no orphaned `kubectl get pod`/`kubectl port-forward` processes.
- **Recommended next action:** operator reviews the two evidence files + IN-02 parity output and approves Task 3, at which point AVE-05, Phase 37, and milestone v1.8 can all be closed.

---
*Phase: 37-keda-helm-chart-integration*
*Completed: 2026-07-23 (Tasks 1-2 PASS; Task 3 human-verify PENDING operator approval)*

## Self-Check: PASSED

Verified below.
