---
phase: 28-autoscale-load-proof
plan: 03
subsystem: infra
tags: [keda, k8s, load-testing, evidence, calibration, orbstack, hpa]

# Dependency graph
requires:
  - phase: 28-autoscale-load-proof
    plan: 01
    provides: "values-loadproof.yaml overlay (scaleDownStabilizationSeconds=15, DOCUMENT_WORKER_CONCURRENCY=1), field-level spec.replicas omission"
  - phase: 28-autoscale-load-proof
    plan: 02
    provides: "keda-load-proof.sh gate, gen_heavy_docx.py calibratable fixture generator, render_evidence.py CSV->PNG renderer"
provides:
  - "Committed live load-proof evidence (run 20260717T100342Z): sc1-sc2-burst CSV+PNG with all five timeline markers, ALL-27-ASSERTIONS-PASSED gate transcript, sc3-timestamps D-09 triple-check"
  - "Calibrated PAGE_UNITS=3900 (observed 178.5s in-cluster docx->pdf conversion on the post-cycle VM)"
  - "Observed 2->1 downscale settling time: 10.8s under the 15s stabilization override (28-RESEARCH.md Open Question 1 resolved live)"
  - "Hardened keda-load-proof.sh: targeted burst wait, pipefail-safe extractions, --connect-to presigned download, watch-based pod termination capture"
affects: [phase-28-verification, v1.6-milestone-audit]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "curl --connect-to <host>:<port>:127.0.0.1:<local> for presigned S3 URLs embedding in-cluster DNS: TCP redirected through a port-forward while URL/Host/signature stay byte-identical"
    - "kubectl get pod -w --output-watch-events for terminal container state: the DELETED watch event carries the final .state.terminated (reason/exitCode/finishedAt) that a sampling poll can miss entirely"
    - "Targeted `wait $pid` on collected subshell PIDs, never bare `wait`, in scripts with long-lived background jobs (port-forwards, samplers)"
    - "Test-only worker CPU throttle via values overlay (worker.resources.limits.cpu) to make burst backlog persist beyond the Prometheus scrape interval"

key-files:
  created:
    - .planning/phases/28-autoscale-load-proof/evidence/gate-transcript-20260717T100342Z.log
    - .planning/phases/28-autoscale-load-proof/evidence/sc1-sc2-burst-20260717T100342Z.csv
    - .planning/phases/28-autoscale-load-proof/evidence/sc1-sc2-burst-20260717T100342Z.png
    - .planning/phases/28-autoscale-load-proof/evidence/sc3-timestamps-20260717T100342Z.txt
  modified:
    - scripts/keda-load-proof.sh
    - deploy/chart/octoconv/values-loadproof.yaml

key-decisions:
  - "PAGE_UNITS=3900 chosen from POST-wedge-cycle calibration (178.5s observed) -- the VM's conversion rate shifted ~37% after the hard cycle, so pre-wedge numbers (5000 -> ~214s) were re-derived"
  - "SC1 backlog persistence achieved via a 200m CPU throttle on the image worker in values-loadproof.yaml (test-only overlay) plus a 90MP gradient fixture -- no fixture size within the 100MP MAX_IMAGE_PIXELS cap can hold a 2-CPU vips backlog past the 15s scrape (measured 0.3s/conversion)"
  - "The png->jpg pair, 20-job count, and every gate assertion stayed exactly as locked (D-04); only fixture content and worker throttle were calibrated (D-05 discretion)"

requirements-completed: [KEDA-03]

# Metrics
duration: ~4h (including one OrbStack control-plane wedge + recovery)
completed: 2026-07-17
---

# Phase 28 Plan 03: Live Load-Proof Run + Evidence Summary

**A single ALL-27-ASSERTIONS-PASSED live gate run against the real OrbStack cluster proving the full 0→4→0 image-class burst cycle (first replica +6s, peak 4 = maxReplicaCount at +11s, drain +76s, scale-to-zero +136s) and a 178s document conversion gracefully surviving a genuine KEDA/HPA 2→1 downscale (SIGTERM 142.8s before job completion, Completed/exit-0 with 188s of the 330s grace window unused) — all captured as committed, credential-free, timestamped evidence.**

## Performance

- **Duration:** ~4h active execution (12 calibration trials + 4 full-gate iterations + one OrbStack wedge recovery)
- **Completed:** 2026-07-17
- **Tasks:** 3 completed (2 auto + 1 human-verify checkpoint, ⚡ auto-approved under operator standing instruction)
- **Files modified:** 2 modified + 4 evidence files created

## Accomplishments

### Task 1 — Live calibration (D-07)

All calibration was live in-cluster (no local soffice exists — Pitfall 4); every trial installed KEDA+octoconv, submitted one heavy docx, read `jobs.started_at→finished_at` via psql, and tore down via the EXIT trap (D-12).

**Pre-wedge trials** (before the OrbStack control-plane wedge and hard cycle):

| PAGE_UNITS | Observed conversion |
|-----------:|--------------------:|
| 300 | 5.8s |
| 1200 | 21.1s |
| 2000 | 44.5s |
| 2500 | 63.4s |
| 2750 | 74.1s |
| 2900 | 84.2s |
| 2950 | 87.1s |
| 2980 | 86.5s |
| 2995 | 84.5s |
| 3000 | 1862.6s (anomaly — VM degrading toward the wedge; off any monotonic curve) |
| 5000 | 213.7s / 215.6s (two consistent runs) |

**Post-wedge-cycle re-calibration** (the hard cycle shifted the VM's conversion rate ~37% slower, then partially recovered — post-cycle numbers are the operative ones):

| PAGE_UNITS | Observed conversion |
|-----------:|--------------------:|
| 5000 | 294.1s (only 6s under DOCUMENT_ENGINE_TIMEOUT — rejected) |
| 3200 | 135.5s |
| **3900** | **178.5s — CHOSEN** (11.9x the 15s stabilization floor, 121s headroom under the 300s timeout) |

**Open Question 1 resolved live:** observed 2→1 downscale settling = **10.8s** after the short job completed (Killing event 10:08:56 − short_job_finished_at 10:08:45.18), consistent with the 15s `scaleDownStabilizationSeconds` override + HPA sync cadence. The 15s working assumption held; no re-derivation needed.

### Task 2 — Full gate run + committed evidence (SC1–SC4)

Run `20260717T100342Z`, ALL 27 ASSERTIONS PASSED, evidence committed under `.planning/phases/28-autoscale-load-proof/evidence/` (git add -f; `.planning/` is gitignored):

**SC1/SC2 timeline (from the committed CSV, one row per ~5s):**

| Marker | Timestamp (UTC) | Delta |
|--------|-----------------|-------|
| Steady state (qd=0, replicas=0) | 10:04:41 – 10:04:59 | — |
| Burst lands (qd=20, replicas **still 0** — true-zero proof) | 10:05:04 | t0 |
| Time-to-first-replica (0→1) | 10:05:10 | +6s |
| Peak replicas (→4 = maxReplicaCount, recorded not asserted) | 10:05:15 | +11s |
| Time-to-drain (qd 20→4→0) | 10:06:20 | +76s |
| Time-to-scale-to-zero (replicas 4→0, cooldown 60s) | 10:07:20 | +136s |

SC1 asserted literally: ≥2 replicas within 60s of the burst (observed 4 within 11s). SC2 asserted: back to 0 after drain.

**SC3/D-09 triple-check (from sc3-timestamps + transcript):**

1. Long job `37392708…` reached `done` and its result downloaded (4,629,779 bytes) through the presigned URL — fetched signature-intact via `curl --connect-to` over a minio port-forward.
2. `job_events` contains exactly **one** `queued→active` row (no false retry).
3. Killing-event SIGTERM at **10:08:56** preceded job completion at **10:11:18.85** by 142.8s; the pod exited **Completed / exit 0** at 10:11:18 — graceful, 188s before the 330s `terminationGracePeriodSeconds` deadline. The busy pod (annotated `pod-deletion-cost=-1000` before the downscale decision) was the genuine KEDA/HPA downscale victim; no imperative pod deletion anywhere.

**D-12 discipline held throughout:** compose stack verified down before every run, pre-built `dev`-tagged images reused, every install torn down via the EXIT trap (including all failed iterations), compose and k8s never hot simultaneously.

## Task Commits

1. **fix: restore executable bit on keda-load-proof.sh** — `5d1440f` (Rule 3)
2. **fix: wait only on burst-submission PIDs** — `01c8acb` (Rule 1)
3. **fix: calibrate SC1 burst backlog (gradient fixture + 200m throttle)** — `f43c3d7` (Rule 1)
4. **fix: pipefail-safe extractions + presigned download** — `9410ee6` (Rule 1)
5. **fix: curl --connect-to on a free local port** — `1177922` (Rule 1)
6. **fix: watch-based pod termination capture** — `10ef647` (Rule 1)
7. **fix: capture scaled-to-zero CSV leg + short-job ts** — `e1d0d80` (Rule 1)
8. **docs: commit live load-proof evidence** — `65fbc69` (Task 2 deliverable)

## Files Created/Modified

- `evidence/gate-transcript-20260717T100342Z.log` — full timestamped ALL-PASSED transcript (27 checks)
- `evidence/sc1-sc2-burst-20260717T100342Z.csv` — 5s-cadence queue-depth + replica sampler output
- `evidence/sc1-sc2-burst-20260717T100342Z.png` — dual-axis timeline with all five markers
- `evidence/sc3-timestamps-20260717T100342Z.txt` — watch-event pod lifecycle + D-09 raw evidence
- `scripts/keda-load-proof.sh` — six Rule-1/Rule-3 hardening fixes (see Deviations)
- `deploy/chart/octoconv/values-loadproof.yaml` — added test-only `worker.resources.limits.cpu: "200m"`

## Decisions Made

- **PAGE_UNITS=3900** from post-cycle observation (178.5s); pre-wedge numbers were invalidated by the VM's rate shift after the hard cycle — calibration must reflect the VM's current state, which is exactly why D-07 demands a live trial.
- **SC1 burst persistence via CPU throttle, not fixture size alone:** at the production 2-CPU limit a single worker converts even a 90MP png (the `MAX_IMAGE_PIXELS=100MP` legal maximum) in ~0.3s and drains all 20 jobs before Prometheus's 15s scrape can ever see the backlog — measured in `octoconv-worker:dev` at `--cpus=2/0.5/0.2`. The 200m overlay throttle (~10.5s/conversion) is the minimal honest lever; it lives only in `values-loadproof.yaml` and no assertion was weakened.
- **The 3000-unit 1862s outlier treated as environmental** (VM degradation preceding the wedge), not a real complexity cliff: 2995→84.5s and 5000→214s bracket it on a smooth near-linear curve.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Missing executable bit on keda-load-proof.sh**
- **Found during:** Task 1 first invocation ("permission denied")
- **Fix:** `chmod +x`, mode-only commit — **`5d1440f`**

**2. [Rule 1 - Bug] Bare `wait` deadlocked the gate after the burst**
- **Found during:** Task 2 first full run — the gate hung 20 minutes while KEDA scaled 0→4→0 unobserved (all 20 jobs done)
- **Issue:** bare `wait` blocks on ALL background children, including the two never-exiting `kubectl port-forward` processes and the 600s CSV sampler
- **Fix:** collect the 20 submission PIDs, wait only on those — **`01c8acb`**

**3. [Rule 1 - Bug] SC1 backlog invisible to Prometheus (queue drained in <2s)**
- **Found during:** Task 2 second run — SC1 failed with peak=1; the metric never rose above 1
- **Fix:** 90MP gradient fixture (<1MB wire, ~270MB decode) + `worker.resources.limits.cpu: "200m"` in the load-proof overlay (production values untouched; benchmarked live at 0.3s/1.6s/10.6s per conversion at 2/0.5/0.2 CPUs) — **`f43c3d7`**

**4. [Rule 1 - Bug] Silent death extracting the result URL + host-unreachable presigned URL**
- **Found during:** Task 2 third run — died right after "long job reaches done" with no FAIL line
- **Issue:** under `set -euo pipefail`, `grep -o '"result_url":…'` (zero matches — the API's field is `download_url`, `internal/api/handlers.go:570`) kills the script before the fallback; the presigned URL also embeds `minio.<ns>.svc.cluster.local:9000`, unresolvable from the host and unrewritable (host is inside the S3 v4 signature)
- **Fix:** `|| true` on every legitimately-empty extraction (incl. WR-01-style transient metric reads); download via minio port-forward — **`9410ee6`**, refined to `curl --connect-to` on local port 19000 because OrbStack itself occupies host port 9000 — **`1177922`**

**5. [Rule 1 - Bug] Pod termination state never captured (lastState + premature kill + sampling gap)**
- **Found during:** Task 2 fourth run — 23/24 checks passed; `terminated.finishedAt` empty
- **Issue:** the snapshot loop read `.lastState.terminated` (restart-only field), was killed 9s after the *downscale* though the SIGTERM'd pod keeps running ~3 minutes until the long job completes, and 3s sampling can miss the brief terminated-status window before object deletion anyway
- **Fix:** `kubectl get pod -w --output-watch-events` watcher (the DELETED event carries the final `.state.terminated`), kept alive until STEP 8 awaits the capture after the long job reaches done; live read prefers `.state.terminated` — **`10ef647`**

**6. [Rule 1 - Bug] CSV missing the scale-to-zero leg**
- **Found during:** review of the first ALL-PASSED run's evidence — last CSV row still showed 4 replicas
- **Issue:** SC2's poll returns the instant it sees 0 and killed the sampler between ticks; the PNG lacked the required fifth marker
- **Fix:** sampler runs ~3 more ticks after SC2 passes; also records `short_job_finished_at` so the observed 2→1 settling time is computable from committed evidence — **`e1d0d80`**

**Total deviations:** 7 auto-fixed (6 Rule 1, 1 Rule 3). All fixes are tooling/overlay-scoped; no application code touched, no assertion weakened, D-04's locked burst parameters (20 jobs, png→jpg, parallel curl) unchanged.

## Issues Encountered

- **Fourth documented OrbStack wedge on this VM:** mid-plan (during calibration downtime) the k8s control plane stopped responding (API server 127.0.0.1:26443) and was recovered by the operator via `orb stop k8s && orb start k8s`. The sustained calibration load likely contributed. Post-cycle the VM's LibreOffice conversion rate measurably shifted (~37% slower initially), which forced re-calibration — recorded above as the operative data set.
- **Workflow incident (disclosed for the orchestrator):** the first commit of this plan (`5d1440f`, exec-bit fix) was initially made in the primary checkout on `main` (as `f800d42`) due to a cwd mistake, then cherry-picked onto the correct worktree branch. The stray local `f800d42` on `main` could not be rolled back from this session (permission-denied); it is content-identical to `5d1440f` (mode-only change) so the eventual merge is a no-op, but the orchestrator should be aware `main` carries it.
- Ten calibration-only gate transcripts accumulated in `evidence/` during Task 1 and were removed before the proving run (calibration numbers live in this SUMMARY; evidence/ contains only the final ALL-PASSED run's four artifacts, per the orchestrator's instruction).

## Observations for Future Tuning (D-11-adjacent, next milestone)

- Image class: 0→4 in 11s from true zero (threshold 5, polling 5s, Prometheus scrape 15s). The scrape interval, not KEDA polling, is the reaction-time floor — sub-15s bursts are invisible without a faster scrape or native asynq scaler.
- Image cooldown 60s and document stabilization 15s both behaved exactly as configured (observed scale-to-zero at drain+60s; observed 2→1 settling at 10.8s).
- Document worker under `DOCUMENT_WORKER_CONCURRENCY=1` mapped jobs to pods perfectly deterministically across all runs; production concurrency 2 would need the pod-deletion-cost pattern re-validated.
- The heavy-docx conversion rate on this VM is environment-sensitive (~37% shift across a VM restart): any future timing-sensitive gate should re-calibrate in-session, never reuse recorded numbers.

## Human Verification Checkpoint (Task 3 — RESOLVED)

**⚡ Auto-approved** by the orchestrator under the operator's standing instruction to run all phases to completion (2026-07-17). The orchestrator reviewed the evidence before approving: 27/27 assertions, coherent SC1/SC2 timeline (true-zero burst → first replica +6s → peak 4 at +11s → drain +76s → zero +136s), SC3 triple-check ordering correct (Killing 10:08:56 → completion +142.8s → Completed/exit 0, 188s under grace), calibration story documented. The operator will review the evidence in the final phase summary. KEDA-03 closed.

## Next Phase Readiness

- All four SC1–SC4 must-haves have live, committed, credential-free evidence; KEDA-03 closed (⚡ auto-approved checkpoint, operator review deferred to the final phase summary).
- WR-01 (empty-PromQL semantics during api outage) remains deferred per D-11 — the `|| true` hardening in this gate handles the *observer* side only, not the trigger semantics.

---
*Phase: 28-autoscale-load-proof*
*Completed: 2026-07-17 (all 3 tasks; Task 3 checkpoint ⚡ auto-approved)*

## Self-Check: PASSED

All 4 evidence files + 2 modified files + this SUMMARY verified present; all 8 commit hashes (5d1440f, 01c8acb, f43c3d7, 9410ee6, 1177922, 10ef647, e1d0d80, 65fbc69) verified present in git log.
