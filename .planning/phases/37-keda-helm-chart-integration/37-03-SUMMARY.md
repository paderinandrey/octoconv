---
phase: 37-keda-helm-chart-integration
plan: 03
subsystem: infra
tags: [keda, helm, kubernetes, av, ffmpeg, load-proof, autoscaling, blocked]

# Dependency graph
requires:
  - phase: 37-keda-helm-chart-integration
    provides: "Plan 01's av-worker Deployment + av KEDA ScaledObject chart templates; Plan 02's scripts/keda-av-loadproof.sh + scripts/keda-av-downscale-survival.sh + values-loadproof.yaml keda.av.scaleDownStabilizationSeconds:15 override"
provides:
  - "Two failed keda-av-loadproof.sh run transcripts (STEP 6 av-worker-never-settles-at-0 failure), evidence for operator triage"
  - "octoconv-av-worker:dev image built and confirmed functional (ffmpeg n8.1.2, correct entrypoint) — reusable for the retry"
affects: [37-03-keda-helm-chart-integration retry, Phase 37 close, milestone v1.8 close]

# Tech tracking
tech-stack:
  added: []
  patterns: []

key-files:
  created:
    - .planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-20260723T194648Z.log
    - .planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-20260723T195300Z.log
  modified: []

key-decisions:
  - "ABORT-TO-OPERATOR invoked per plan's hard environment-discipline rule: keda-av-loadproof.sh (Task 1) errored twice (one initial run + one permitted retry), both failing identically at STEP 6 (av-worker Deployment never settled to 0 replicas within the 240s bound, observed replicas=1 both times) — third attempt was NOT taken, per the 'do not thrash / retry more than once' rule"
  - "Task 2 (keda-av-downscale-survival.sh) was NOT attempted — it depends on Task 1's scale-from-zero precondition passing first, and the plan's own task ordering blocks it behind Task 1"
  - "Task 3 (human-verify checkpoint) was NOT attempted, per this execution's explicit scope (operator-owned) — nothing to verify yet since Tasks 1-2 did not produce SC3/SC4 evidence"
  - "Environment safely returned to resting state despite the abort: both script runs' own EXIT traps tore down helm(octoconv+keda) cleanly (confirmed via 'helm list -A' empty, 'kubectl get deployment -n octoconv' empty) after each failed attempt; k8s was then explicitly stopped ('orb stop k8s') and compose was already confirmed down before any k8s work began"

requirements-completed: []

# Metrics
duration: 45min
completed: 2026-07-23
---

# Phase 37 Plan 03: av Live Load-Proof — BLOCKED at Task 1 (SC3 scale-from-zero)

**scripts/keda-av-loadproof.sh (SC3) failed twice at STEP 6 — the freshly-installed av-worker Deployment never settled to 0 replicas within the script's 240s bound (observed replicas=1 both times) — Task 2 (SC4) and Task 3 (human-verify) were not attempted; OrbStack was returned to a clean resting state (both stacks down) after each failed attempt, and the operator must triage the KEDA/HPA scale-to-zero behavior before this plan can be retried.**

## Performance

- **Duration:** ~45 min (mostly image build + two live-cluster attempts + teardown/diagnosis)
- **Started:** 2026-07-23T19:40:00Z (approx)
- **Completed:** 2026-07-23T19:59:41Z
- **Tasks:** 0/2 completed (Task 1 blocked after 1 retry; Task 2 not attempted; Task 3 out of scope for this execution)
- **Files modified:** 0 code files; 2 evidence log files committed

## Accomplishments
- Confirmed compose stack DOWN before any k8s work (`docker compose down`, `docker compose ps` empty).
- Brought up OrbStack k8s (`orb start k8s`), verified daemon health (`kubectl cluster-info` reachable, node `orbstack` Ready) before any build/install.
- Built the missing `octoconv-av-worker:dev` image (Dockerfile.av-worker, sequential single build, non-`:latest` tag matching `global.imageTag: "dev"`) — all other images the chart needs (`octoconv-api:dev`, `octoconv-worker:dev`, `octoconv-document-worker:dev`, `octoconv-audio-worker:dev`, `octoconv-chromium-worker:dev`, `octoconv-webhook-worker:dev`) were already present locally from prior phases, so no other build was required. Confirmed the built image runs ffmpeg n8.1.2 correctly (`docker run --entrypoint ffmpeg octoconv-av-worker:dev -version`).
- Ran `scripts/keda-av-loadproof.sh` twice (initial attempt + one permitted retry per the ABORT-TO-OPERATOR rule). Both runs passed STEP 1-5 (preflight, KEDA v2.20.1 install, octoconv helm install with `values-local.yaml`, always-on Deployments Available, asynq:queues seeding including `av`, api/postgres port-forward + client-key mint) and both failed identically at STEP 6: `av-worker Deployment status.replicas before any job (genuine zero) — expected [0], got [1]` after the full 240s wait (KEDA `cooldownPeriod=180s` + margin, per the script's own comment).
- Did NOT proceed to Task 2 (`keda-av-downscale-survival.sh`) since it depends on Task 1's live proof passing first and the plan's task ordering blocks it.
- Did NOT engineer an artificial fix or modify `scripts/keda-av-loadproof.sh` (a Plan-02-authored script, not one of the frozen scripts, but this run's job is to execute it, not patch it) — the repeated identical failure looked like it needs human/architectural triage, not an in-task auto-fix.
- Verified clean teardown after each failed run: the script's own `EXIT` trap ran `helm uninstall octoconv`/`helm uninstall keda` and both completed (`helm list -A` empty, `kubectl get deployment -n octoconv` empty) before this agent proceeded.
- Stopped k8s (`orb stop k8s`) after the second failed attempt and confirmed `kubectl cluster-info` is now unreachable (connection refused) — both stacks are DOWN, OrbStack is at rest.
- Confirmed no orphaned watchers (`pgrep -f 'kubectl get pod'` empty) or port-forwards (`pgrep -f 'kubectl port-forward'` empty) survived either run's teardown.

## Task Commits

No task commits — Task 1 did not reach a passing state, so no evidence artifacts matching the plan's success criteria (`sc3-av-scale-from-zero-*.txt`) were produced. The two gate-transcript logs from the failed attempts are committed as diagnostic evidence in the metadata commit that accompanies this SUMMARY (not as a "Task complete" commit, since no task's `<done>` criteria were met).

**Plan metadata:** (this commit, docs: record blocked plan state)

_Note: No TDD tasks in this plan — plain `type="auto"` live-cluster execution tasks, both blocked/not-attempted this run._

## Files Created/Modified
- `.planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-20260723T194648Z.log` - full transcript of the first failed `keda-av-loadproof.sh` run (fails at STEP 6)
- `.planning/phases/37-keda-helm-chart-integration/evidence/gate-transcript-20260723T195300Z.log` - full transcript of the second (retry) failed `keda-av-loadproof.sh` run (fails identically at STEP 6)

## Decisions Made
- Per the plan's hard `<critical_environment_discipline>` ABORT-TO-OPERATOR rule ("a load-proof script refuses to run or errors ... STOP immediately ... Do NOT thrash or retry more than once"), this execution allowed exactly ONE retry of `keda-av-loadproof.sh` after the first failure (treating it as possible first-time-KEDA/HPA-convergence timing flakiness, consistent with Phase 33's own precedent of multiple observed reruns during audio's live-proof development). The retry reproduced the identical failure at the identical step, which rules out simple flakiness and confirms this needs operator triage rather than a second retry.
- Did not attempt to extend the script's STEP 6 wait window or otherwise patch `scripts/keda-av-loadproof.sh` — this run's scope is execution, not script maintenance, and a script edit here would be an unreviewed change to a Plan-02 deliverable made under time pressure mid-failure, which is exactly the kind of decision the ABORT rule reserves for the operator.
- Task 2 was not attempted at all (never reached, since it depends on Task 1 first) and Task 3 was never in scope for this execution per this run's explicit objective.

## Deviations from Plan

None in the Rule 1-3 auto-fix sense — no code, chart, or script bugs were fixed, because the live failure occurred inside a Plan-02-authored script (not a bug I introduced in this plan, which has zero files-modified scope beyond evidence) and required an environment/architecture-level diagnosis rather than an in-task correction. This is documented as a blocker (see below), not a deviation.

## Issues Encountered

**BLOCKER: `av-worker` Deployment never settles to 0 replicas on a fresh KEDA+octoconv install (SC3 precondition), across 2 consecutive live attempts.**

- **Symptom:** Both runs of `scripts/keda-av-loadproof.sh` passed every preflight/install/seed step (KEDA v2.20.1 installed and healthy, `v1beta1.external.metrics.k8s.io` Available, octoconv installed via `values-local.yaml`, all always-on Deployments Available, `asynq:queues` seeded including `av`, api reachable via port-forward, client key minted) and then failed at STEP 6 with `av-worker Deployment status.replicas before any job (genuine zero) -- expected [0], got [1]` after waiting the full 240s bound (KEDA `cooldownPeriod=180s` + margin, matching the audio precedent's proven wait window from Phase 33).
- **What was ruled out by inspection (read-only, chart/values review — no additional live attempts made):**
  - `avWorker.replicas: 1` in `values.yaml` is the Deployment's *declared* initial replica count (same shape as `audioWorker.replicas: 1` and `documentWorker.replicas`, which worked in Phase 33) — KEDA/HPA (`minReplicaCount: 0`) is expected to override this down to 0 once the ScaledObject reports the trigger inactive, exactly as it did for audio in Phase 33's evidence (`sc3-audio-scale-from-zero-20260718T211401Z.txt` shows a clean 0 at STEP 6 on the first attempt).
  - The av `ScaledObject` (`scaledobject-av.yaml`) is structurally identical to audio's (same `cooldownPeriod: 180`, same `pollingInterval: 15`, same PromQL shape `sum(octoconv_queue_depth{queue="av", state=~"pending|active|retry"})`, same `ignoreNullValues: "false"` + `fallback.replicas: 1` triad) — no config divergence was found that would explain av behaving differently from audio.
  - Prometheus `scrape_interval: 15s` (chart-wide, `_helpers.tpl`) matches KEDA's `pollingInterval: 15s` — no scrape-cadence mismatch identified for av specifically.
  - `asynq:queues` seeding in STEP 4b explicitly includes `av` in the `SADD` call, so the WR-01 "absent metric read as scaler error, not queue-empty" fail-safe (which would otherwise hold `fallback.replicas: 1` indefinitely) should not be in play — but this could not be confirmed live (the environment was already torn down by the time this was checked), and is the leading hypothesis for a follow-up live diagnostic.
- **Leading hypotheses for the operator to check on the next live attempt (in order of likelihood), before assuming a code/chart bug exists:**
  1. **PromQL scrape-target timing on a brand-new class:** this is the *first-ever* time `av`'s `ScaledObject`/HPA pair has been reconciled by KEDA (unlike audio/document/image, which have prior live-proof history) — it is possible the Prometheus target for the `api` pod's `/metrics` (carrying `octoconv_queue_depth{queue="av",...}`) needs one or two more scrape cycles than the 240s bound accounts for on a completely cold KEDA operator + cold Prometheus install happening in the same run. Re-running with a longer STEP 6 wait (or a `kubectl get scaledobject worker-av-scaledobject -o yaml` / `kubectl get hpa` / `kubectl logs -n keda deploy/keda-operator` live check mid-run) would confirm or rule this out directly.
  2. **`fallback.replicas: 1` actively engaged:** if the Prometheus query for `queue="av"` is returning no series at all (as opposed to a `0`-valued series) — e.g., if the metric only gets a label value the first time a job is enqueued/dequeued for that queue, and the Redis `asynq:queues` SADD seeding does not by itself cause the api collector to emit a `queue="av"` labeled sample with value 0 — then `ignoreNullValues: "false"` would classify this as a scaler error and `fallback.replicas: 1` would hold indefinitely, exactly matching the observed symptom. This would be a genuine gap in the WR-01 mitigation for a queue that has *never* been seeded/labeled by the collector before (as opposed to audio/document/image, which by Phase 37 time already have historical queue-depth series in this environment's collector code paths). Worth a live `curl` of the api pod's `/metrics` endpoint (or a direct `promtool query instant` against the in-cluster Prometheus) for `octoconv_queue_depth{queue="av"}` on the next attempt, BEFORE STEP 6's wait begins, to see whether the series exists at all.
  3. **Chart-level issue in `deployment-av-worker.yaml`/`scaledobject-av.yaml`** (e.g. a `scaleTargetRef` name mismatch, a missing label selector) — considered less likely since `helm template ... -f values-local.yaml` (checked during Plan 01) already confirmed the av ScaledObject renders correctly with the full WR-01 triad and grace 783s, and Task 1's STEP 4 successfully brought `av-worker`'s sibling Deployments to `Available` with no anomaly — but not fully ruled out without a live `kubectl describe scaledobject worker-av-scaledobject` / `kubectl describe hpa` capture.
- **Resolution:** NOT resolved this run. Per the plan's ABORT-TO-OPERATOR rule, a second identical failure after the one permitted retry means this plan STOPS here rather than attempting a third live run. The environment was safely torn down (helm releases removed via each run's own EXIT trap, k8s stopped, compose already down) and this SUMMARY documents the exact failure point for the operator/next session to pick up with a live diagnostic (`kubectl describe scaledobject`/`hpa`, direct Prometheus query for the `av` queue series, `kubectl logs -n keda deploy/keda-operator`) BEFORE re-running `scripts/keda-av-loadproof.sh` a third time.

## User Setup Required

None new. `octoconv-av-worker:dev` is now built locally and available for the retry (no rebuild needed unless source changes).

## Next Phase Readiness — BLOCKED, do not proceed to Phase completion

- **Task 3 (human-verify checkpoint): PENDING (operator).** Nothing was produced for the operator to verify yet — Task 1 did not pass, so no `sc3-av-scale-from-zero-*.txt` or `sc4-av-downscale-survival-*.txt` evidence exists. Task 3 cannot be meaningfully attempted until Tasks 1-2 both pass on a future run.
- **Do NOT mark AVE-05 complete.** `.planning/REQUIREMENTS.md`'s AVE-05 checkbox must remain unchecked — this plan's live proof did not close it.
- **Do NOT run `phase.complete` for Phase 37.** The orchestrator should only do so after a future execution of this plan (or a fresh Task 1 + Task 2 attempt) passes both live proofs AND the operator approves the Task 3 checkpoint.
- **Recommended next action:** re-run `scripts/keda-av-loadproof.sh` with a live mid-run diagnostic capture at STEP 6 (`kubectl get hpa,scaledobject -n octoconv -o wide`, a direct Prometheus query for `octoconv_queue_depth{queue="av"}`, and `kubectl logs -n keda deploy/keda-operator --tail=200`) to distinguish hypothesis (1) cold-scrape-timing from hypothesis (2) fallback-stuck-on-absent-series before deciding whether a script/chart fix is needed. `octoconv-av-worker:dev` is already built and does not need to be rebuilt for that attempt (rebuild only if `Dockerfile.av-worker` or its build context changes).
- **Environment state at hand-back:** both stacks confirmed DOWN — `docker compose ps` empty, `kubectl cluster-info` unreachable (`orb stop k8s` completed), `helm list -A` was empty before k8s was stopped, no orphaned `kubectl get pod`/`kubectl port-forward` processes, no leftover `octoconv-*` containers.

---
*Phase: 37-keda-helm-chart-integration*
*Completed: 2026-07-23 (BLOCKED — Task 1 not passing; Task 2/3 not attempted)*

## Self-Check: PASSED

Verified below (both evidence log files exist on disk; no fabricated commit hashes are claimed since no task-passing commit was made).
