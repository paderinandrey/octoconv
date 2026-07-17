---
phase: 27-keda-autoscaling
plan: 03
subsystem: infra
tags: [keda, kubernetes, orbstack, helm, prometheus, live-gate, scale-from-zero]

# Dependency graph
requires:
  - phase: 27-keda-autoscaling
    provides: "plan 01's api-side octoconv_queue_depth registration (the metric KEDA reads at 0 worker replicas)"
  - phase: 27-keda-autoscaling
    provides: "plan 02's chart manifests: in-chart Prometheus, three per-class ScaledObjects, api :9090 Service port, fixed NetworkPolicy"
provides:
  - "scripts/keda-gate.sh — reproducible, self-tearing-down Phase-27 live hard gate on OrbStack k8s (D-12 proof encoded as loud assertions; exit code IS the gate)"
  - "Live D-12 evidence: SC1 metric-at-genuine-zero via kubectl get --raw, SC2 all three classes 0→1 from one real job each, image full-cycle back to 0, webhook-worker fixed at 2 with no ScaledObject START/MID/END"
  - "Human-verify checkpoint approval of the gate evidence (operator typed 'approved')"
affects: [28-load-proof]

# Tech tracking
tech-stack:
  added: ["KEDA v2.20.1 (helm-installed live by the gate script into its own keda namespace, torn down after)"]
  patterns:
    - "Gate script installs its own infra (KEDA) idempotently and tears EVERYTHING down via an EXIT trap that runs on success AND failure — OrbStack is never left hot (D-13)"
    - "External metric name discovered live per-ScaledObject via status.externalMetricNames, never hardcoded (Pitfall 5) — observed name was s0-prometheus but the script never assumes it"
    - "Post-install 0-replica settling: chart-owned spec.replicas=1 until KEDA's HPA takes ownership and cools down to minReplicaCount=0 — gate polls (cooldownPeriod + margin) instead of asserting instantly"

key-files:
  created:
    - scripts/keda-gate.sh
  modified: []

key-decisions:
  - "Gate waits for Available only on the always-on/min-1 Deployments (postgres/redis/minio/api/prometheus/webhook-worker) — the three scaled workers are expected at 0 and never waited on (Phase 24 no---wait install decision extended)"
  - "SC1's genuinely-at-zero precondition is reached by polling the worker Deployment down to 0 after install (KEDA ownership + cooldown), not by asserting instantly — the initial replicas:1 window is expected chart behavior, not a defect"
  - "Job submission uses port-forwarded svc/api + svc/postgres with a freshly minted client key via cmd/manage-clients (sanctioned 24-03/25-03 mechanism; users never depend on OrbStack's flaky host→cluster proxy)"

patterns-established:
  - "k8s live-gate script shape for this repo: presets-acceptance.sh bash conventions (set -euo pipefail, assert_* helpers, loud PASS/FAIL, exit-code-is-gate) + Phase 24 install flow + unconditional EXIT-trap teardown"

requirements-completed: [KEDA-02]

# Metrics
duration: ~30min
completed: 2026-07-17
---

# Phase 27 Plan 03: KEDA Live Hard Gate Summary

**Authored and executed `scripts/keda-gate.sh` on OrbStack k8s: KEDA v2.20.1 installed live, the full D-12 proof passed 18/18 assertions (metric resolves at genuinely 0 replicas, all three classes scale 0→1 from one real job each, image cycles back to 0, webhook-worker fixed at 2 throughout), and teardown left the cluster completely clean — human-verify checkpoint approved.**

## Performance

- **Duration:** ~30 min (first task commit 2026-07-16T21:07:53Z to fix commit 21:35:12Z; checkpoint approval followed)
- **Started:** 2026-07-16T21:00:00Z (approx)
- **Completed:** 2026-07-17 (checkpoint approved)
- **Tasks:** 3 (Task 1 auto, Task 2 auto/live, Task 3 human-verify checkpoint — approved)
- **Files modified:** 1 (scripts/keda-gate.sh, created)

## Gate verdict (loud)

| Check | Result |
|-------|--------|
| Preflight: kubectl reaches OrbStack; compose stack down; KEDA v2.20.1 re-verified live | **PASS** |
| KEDA v2.20.1 helm-installed into `keda` namespace (idempotent); `v1beta1.external.metrics.k8s.io` Available:True | **PASS** |
| octoconv installed WITHOUT `--wait`; postgres/redis/minio/api/prometheus/webhook-worker all Available | **PASS** |
| SC1 (D-12a): `worker` at genuine `status.replicas=0`; metric name discovered live (`s0-prometheus`); `kubectl get --raw` returned `"value":"0"` | **PASS** |
| SC2 (D-12b) image: sample.png→jpg job → `worker` scaled 0→1 | **PASS** |
| SC2 (D-12b) document: sample.docx→pdf job → `document-worker` scaled 0→1 | **PASS** |
| SC2 (D-12b) html: sample.html→pdf job → `chromium-worker` scaled 0→1 | **PASS** |
| SC2 cont. (D-12c): image job `done`; `worker` cycled back to 0 replicas post-cooldown | **PASS** |
| SC3 (D-12d/D-09): webhook-worker `spec.replicas=2` + zero ScaledObjects targeting it, at START/MID/END | **PASS** (×6 checks) |
| Teardown: helm uninstall octoconv + keda via EXIT trap; 0 deployments, 0 keda CRDs remain; compose still down | **PASS** |

**Total: 18/18 assertions passed; script exited 0 with the `✅ PASS` banner.**

## Live-gate transcript (key evidence, UTC 2026-07-16, final passing run)

```
21:32:09  helm install keda kedacore/keda -n keda --create-namespace --version 2.20.1 --wait
          -> deployed; v1beta1.external.metrics.k8s.io Available:True
21:32:36  helm install octoconv deploy/chart/octoconv -f values-local.yaml -n octoconv
          (NO --wait, 24-03 decision) -> deployed
          postgres/redis/minio/api/prometheus/webhook-worker all Available
          webhook-worker replicas (START) == 2; ScaledObjects targeting it == 0
21:33:03  SC1: worker status.replicas settled at 0 (KEDA ownership + cooldown);
          external metric name discovered live: s0-prometheus
          kubectl get --raw /apis/external.metrics.k8s.io/v1beta1/namespaces/
            octoconv/s0-prometheus?labelSelector=scaledobject.keda.sh%2Fname%3D
            worker-image-scaledobject
          -> {"kind":"ExternalMetricValueList",...,"items":[{"metricName":
             "s0-prometheus","timestamp":"2026-07-16T21:33:03Z","value":"0"}]}
          /healthz 200 via port-forward: {"postgres":"ok","redis":"ok","s3":"ok","status":"ok"}
          gate client minted via cmd/manage-clients over port-forwarded postgres
~21:33+   SC2 image:    job 9967ac3a-0242-4d7e-937f-da11667a09a0 (png→jpg)  -> worker 0→1
          SC2 document: job 2a5fb75d-41a2-4de4-abae-11cbcfb2ba95 (docx→pdf) -> document-worker 0→1
          SC2 html:     job 3c9d52c2-3c99-49ac-9235-22b633db46a3 (html→pdf) -> chromium-worker 0→1
          webhook-worker replicas (MID) == 2; ScaledObjects targeting it == 0
~21:36+   image job reached done; worker cycled back to 0 replicas (bounded
          post-cooldown poll, cooldownPeriod=60s + margin)
          webhook-worker replicas (END) == 2; ScaledObjects targeting it == 0
          === ALL 18 ASSERTIONS PASSED === -> EXIT-trap teardown:
          helm uninstall octoconv + keda; octoconv deployments remaining: 0
post-run  verified: kubectl get all -n octoconv -> none; -n keda -> none;
          kubectl get crd | grep keda -> none; docker compose ps -> empty
```

## What was built (Task 1, commit d49959d; fixes in 6971aba)

- **`scripts/keda-gate.sh`** (executable, `bash -n` clean) — follows `presets-acceptance.sh` conventions (`#!/usr/bin/env bash`, `set -euo pipefail`, `cd "$(dirname "$0")/.."`, named `assert_eq`/`assert_nonempty` helpers with loud FAIL, final `✅ PASS` banner; exit code IS the gate). Implements the 9-step 27-PATTERNS sequence:
  1. Preflight: cluster reachability, compose-stack-down check (fails loud if any `octoconv-*` container is running), live KEDA-version re-verification with a repin message if 2.20.1 drifts.
  2. Idempotent KEDA install (`helm upgrade` if the release already exists).
  3. Bounded poll (not a fixed sleep) on `v1beta1.external.metrics.k8s.io` for `Available:True`, with an explicit NOTE that the metric list is empty until a ScaledObject exists.
  4. `helm install octoconv` WITHOUT `--wait`; `kubectl wait` per always-on Deployment only.
  5. SC1: poll `worker` to genuine 0 replicas, discover the metric name live via `status.externalMetricNames`, `kubectl get --raw` with the ScaledObject labelSelector, assert a `"value"` comes back.
  6. SC2 per class: port-forward api+postgres, mint a client key, POST one real e2e fixture per class (`sample.png→jpg`, `sample.docx→pdf`, `sample.html→pdf`), poll each target Deployment to `status.replicas >= 1` with bounded per-class timeouts that print the last observed value on failure.
  7. Full-cycle (image only): poll the job to `done`, then poll `worker` back to 0 within cooldown + margin.
  8. webhook-worker gate at START/MID/END: `spec.replicas == 2` AND zero ScaledObjects whose `scaleTargetRef.name` is `webhook-worker`.
  9. Teardown via an EXIT trap that runs on success AND failure: kill port-forwards, `helm uninstall octoconv` + `keda`, wait for deployments to drain, print the final PASS/FAIL banner.

## Task Commits

Each task was committed atomically:

1. **Task 1: Author scripts/keda-gate.sh (the reproducible live gate)** - `d49959d` (feat)
2. **Task 2: Pre-build images sequentially and execute the live gate** - `6971aba` (fix — two Rule 1 auto-fixes found during live execution; the live run itself modifies no files beyond these fixes)
3. **Task 3: Human verification of the live gate evidence** - no commit (checkpoint; operator approved verbatim: "approved")

## Files Created/Modified

- `scripts/keda-gate.sh` - The Phase-27 live hard gate (created in Task 1, two runtime fixes in Task 2).

## Decisions Made

- **Always-on wait list includes `prometheus`:** the in-chart Prometheus (plan 02) is a min-1 Deployment the ScaledObjects depend on, so the gate waits for it alongside api/webhook-worker; the three scaled workers are never waited on for Available (they are expected at 0).
- **SC1 precondition reached by polling, not asserting:** helm renders `worker` with the chart-owned `spec.replicas: 1`; KEDA's HPA takes ownership and scales to `minReplicaCount: 0` only after observing the empty queue across the class's `cooldownPeriod` (60s for image). The gate polls up to 150s for genuine 0 rather than asserting instantly — this is expected KEDA-adoption behavior, documented in-script.
- **Job submission mechanism:** port-forwarded `svc/api` (:18090) and `svc/postgres` (:15434) + `cmd/manage-clients` for key minting — the sanctioned 24-03/25-03 mechanism, deliberately avoiding OrbStack's twice-confirmed-flaky host→cluster proxy for anything load-bearing.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] SC1's zero-replica assertion fired before KEDA had taken ownership of the worker Deployment**
- **Found during:** Task 2, first live gate run
- **Issue:** The script asserted `worker` `status.replicas == 0` immediately after the always-on waits. But helm renders the worker Deployment with its chart-owned `spec.replicas: 1` default; KEDA's HPA only assumes ownership and scales to `minReplicaCount: 0` after observing the queue empty across the image class's `cooldownPeriod` (60s). First run failed with `expected [0], got [1]` — an assertion-timing bug, not a chart or KEDA defect.
- **Fix:** Replaced the instant assertion with a bounded poll (up to 150s = cooldownPeriod + generous margin) that settles at 0 before the SC1 raw-metric check, with an in-script comment explaining why the initial `1` is expected.
- **Files modified:** `scripts/keda-gate.sh`
- **Verification:** Final run: `worker` settled at 0 well within the window, SC1 metric check then ran at genuine 0 replicas.
- **Commit:** `6971aba`

**2. [Rule 1 - Bug] `postJob()`'s single-statement `local` chain hit bash's word-expansion ordering under `set -u`**
- **Found during:** Task 2, second live gate run
- **Issue:** `local filename="$1" target="$2" content_type="$3" out_file="/tmp/keda-gate-post-${filename}.json"` — in bash, all words of one `local` statement are expanded BEFORE any of the assignments take effect, so `${filename}` in the last word resolved against the (unset) outer scope; with `set -u` the script aborted with `filename: unbound variable` on the first job submission.
- **Fix:** Split into two `local` statements (`local filename=... target=... content_type=...` then `local out_file=...`).
- **Files modified:** `scripts/keda-gate.sh`
- **Verification:** Final run: all three per-class job submissions succeeded (202 + job_id each).
- **Commit:** `6971aba`

---

**Total deviations:** 2 auto-fixed (both Rule 1 bugs in the gate script itself, found by actually running it live — exactly what the live gate is for)
**Impact on plan:** None on scope — zero chart or app-code changes were needed; the chart from plan 02 and the relocation from plan 01 worked live on the first fully-executed attempt.

## Issues Encountered

- **Three gate runs total to reach the 18/18 pass:** run 1 failed on deviation 1 (SC1 timing), run 2 on deviation 2 (bash `local` expansion) — each run's EXIT-trap teardown cleaned the cluster fully before the next attempt, so no manual cleanup was ever needed between runs (validating the trap design).
- **Images pre-built sequentially per D-13:** all 6 app images (`api`, `worker`, `document-worker` with `--platform linux/amd64`, `chromium-worker`, `webhook-worker`, `mcp-http` — the last because `mcpHttp.enabled: true` is the chart default) built one at a time with the `dev` tag before any helm install; Prometheus and KEDA images were chart-pulled, not built. No disk-pressure/kubelet-GC events this session (unlike 24-03).

## Checkpoint Resolution (Task 3)

Human-verify checkpoint (gate=blocking) presented the full 18/18 transcript with per-assertion observed values. The operator reviewed SC1 (metric-at-zero via `kubectl get --raw`), SC2 (all three classes 0→1), the image full cycle back to 0, the webhook-worker fixed-2 gate, and the clean teardown, and **approved verbatim ("approved")** — recorded here as the plan's checkpoint resolution.

## User Setup Required

None — no external service configuration required. KEDA is installed and removed by the gate script itself.

## Next Phase Readiness

- Phase 27's milestone-critical scale-from-zero proof is complete and human-approved: the relocated metric (plan 01) + chart manifests (plan 02) + live KEDA behavior (this plan) are proven end-to-end on OrbStack.
- Phase 28 (load-proof) can reuse `scripts/keda-gate.sh` as its starting scaffold: the install/teardown flow, live metric-name discovery, port-forward job-submission helpers, and replica-poll helpers (`waitForReplicasAtLeast`/`waitForReplicasAtMost`) all transfer directly; Phase 28 adds burst load, timestamped 0→N→0 capture, and the long-document graceful-scale-down soak (which will exercise plan 01's ShutdownTimeout fix).
- Known behavioral note for Phase 28: after a fresh install there is a ~60-150s window where the scaled workers sit at the chart's `replicas: 1` before KEDA cools them to 0 — any timestamped burst measurement must start after that settling window (the gate script's poll pattern shows how).

---
*Phase: 27-keda-autoscaling*
*Completed: 2026-07-17*

## Self-Check: PASSED

- `scripts/keda-gate.sh`: FOUND (executable)
- `.planning/phases/27-keda-autoscaling/27-03-SUMMARY.md`: FOUND
- Commit `d49959d` (feat, Task 1): FOUND
- Commit `6971aba` (fix, Task 2): FOUND
- Commit `98f87cd` (docs, SUMMARY): FOUND
- Worktree clean, no untracked files; cluster verified clean post-teardown (0 releases, 0 keda CRDs, compose down)
