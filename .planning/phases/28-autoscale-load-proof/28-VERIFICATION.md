---
phase: 28-autoscale-load-proof
verified: 2026-07-17T13:53:54Z
status: passed
score: 10/10 must-haves verified
overrides_applied: 0
---

# Phase 28: Autoscale Load-Proof Verification Report

**Phase Goal:** The 0→N→0 autoscale behavior is proven under real load with timestamped evidence, including graceful scale-down while a long conversion is in flight. This is the milestone's flagship live acceptance.
**Verified:** 2026-07-17T13:53:54Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | SC1 — image worker at genuine 0 replicas, burst of 20 jobs → ≥2 replicas within 60s | ✓ VERIFIED | `sc1-sc2-burst-20260717T100342Z.csv` rows 5-7: burst lands 10:05:04 with `queue_depth=20, worker_replicas=0` (true zero at burst time), first replica at 10:05:10 (+6s), 4 replicas (maxReplicaCount) by 10:05:15 (+11s). Transcript: `PASS: SC1 -- worker (image) scaled 0->4 within 60s of the 20-job burst`. |
| 2 | SC2 — after drain, worker returns to 0 within cooldown | ✓ VERIFIED | CSV: queue drains to 0 at 10:06:20 (replicas still 4), replicas drop to 0 at 10:07:20 (+60s cooldown from drain, matches `cooldownPeriod=60` for image class). Transcript: `PASS: SC2 -- worker (image) full-cycled back to 0 replicas`. |
| 3 | SC3 — ~200s document conversion survives a KEDA downscale (no SIGKILL, no spurious retry) | ✓ VERIFIED | Calibrated PAGE_UNITS=3900 → observed 178.5s (28-03-SUMMARY table). `sc3-timestamps-20260717T100342Z.txt`: Killing-event SIGTERM at 10:08:56, pod terminated `reason=Completed exit=0` at 10:11:18, `job_finished_at` 10:11:18.85 — ordering SIGTERM (10:08:56) < completion (10:11:18) confirmed both by the gate's own epoch-comparison assertion (`PASS: D-09(3) -- SIGTERM ... occurred BEFORE job completion ...`) and by independent manual comparison of the raw timestamps. `queued_to_active_count=1` (no spurious retry). Grace deadline 330s; pod exited 188s early. |
| 4 | SC4 — full 0→N→0 timeline captured as timestamped evidence (queue depth + pod count, one time axis) | ✓ VERIFIED | `sc1-sc2-burst-20260717T100342Z.png` (1440×600, valid PNG, non-trivial 57.6KB) rendered from the 32-row CSV via `render_evidence.py` (dual-axis `twinx()`); transcript confirms `rows: 32, pod-count column: worker_replicas`. All five markers present in CSV: steady state (10:04:43-10:04:59, qd=0/replicas=0), burst (10:05:04), first replica (10:05:10), drain (10:06:20), scale-to-zero (10:07:20). |
| 5 | Live gate run ends with all assertions passing | ✓ VERIFIED | `gate-transcript-20260717T100342Z.log` ends `=== ALL 27 ASSERTIONS PASSED ===` followed by a per-SC summary and clean teardown (`octoconv namespace deployments remaining: 0`). |
| 6 | D-10/WR-02 — the three KEDA-scaled Deployments (worker, document-worker, chromium-worker) omit `spec.replicas` under `keda.enabled && prometheus.enabled`; render it when KEDA is off; `webhook-worker` untouched in every mode | ✓ VERIFIED | `helm template` (live-run, this session): keda-on → 0 `replicas:` lines on all three scaled Deployments; keda-off (`--set keda.enabled=false`) → exactly 1 `replicas:` line on each; `deployment-webhook-worker.yaml` → exactly 1 `replicas:` line unconditionally. `webhook-worker.yaml` last touched in Phase 24 (`589566b`), never modified by Phase 28. `helm lint` clean. |
| 7 | Document ScaledObject gains a values-gated `scaleDownStabilizationSeconds` override (off by default, 15s via overlay); production HPA default untouched | ✓ VERIFIED | `scaledobject-document.yaml` — `{{- if .Values.keda.document.scaleDownStabilizationSeconds }}` block; `helm template` with only `values-local.yaml` → 0 `stabilizationWindowSeconds` occurrences; with `+values-loadproof.yaml` → exactly 1, value 15. `values.yaml:157` sets `scaleDownStabilizationSeconds: null` by default. `triggers:`/`ignoreNullValues` block byte-identical since before Phase 28 (D-11 untouched, confirmed via git diff on the Task 2 commit). |
| 8 | `values-loadproof.yaml` is the sole enabler; sets deterministic overlay knobs, never touches production values | ✓ VERIFIED | File header explicitly documents "layered ON TOP of values-local.yaml, NEVER standalone"; sets `keda.document.scaleDownStabilizationSeconds: 15`, `documentWorker.extraEnv.DOCUMENT_WORKER_CONCURRENCY: "1"`, and (added live during 28-03) `worker.resources.limits.cpu: "200m"` (test-only throttle, documented rationale for why the burst backlog needed it to persist past the Prometheus scrape interval). `documentWorker.extraEnv` rendered `env: DOCUMENT_WORKER_CONCURRENCY "1"` confirmed present with overlay, absent without. |
| 9 | `scripts/keda-gate.sh` (Phase 27 gate) remains working; only comment changed | ✓ VERIFIED | `bash -n scripts/keda-gate.sh` exits 0. `git diff 6971aba 4b9bc23 -- scripts/keda-gate.sh` shows only `#`-comment line changes in STEP 6 (rationale rewrite); no assertion/loop/echo line touched. No further diff since (`git diff 4b9bc23 HEAD -- scripts/keda-gate.sh` empty). |
| 10 | `scripts/keda-load-proof.sh` is syntactically valid and self-contained, never modifies `keda-gate.sh` | ✓ VERIFIED | `bash -n scripts/keda-load-proof.sh` exits 0 (972 lines). References `values-loadproof.yaml`, `trap teardown EXIT`, `render_evidence.py`, `gen_heavy_docx.py`. `git diff --quiet scripts/keda-gate.sh` true throughout all three plans (per 28-02/28-03 SUMMARY self-checks and independently re-confirmed here). |

**Score:** 10/10 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `evidence/gate-transcript-20260717T100342Z.log` | Full timestamped ALL-PASSED transcript | ✓ VERIFIED | 172 lines, ends `ALL 27 ASSERTIONS PASSED`, git-tracked (`git ls-files --error-unmatch` succeeds). |
| `evidence/sc1-sc2-burst-20260717T100342Z.csv` | 5s-cadence queue-depth+replica sampler | ✓ VERIFIED | 32 data rows, supports all 5 SC4 markers, git-tracked. |
| `evidence/sc1-sc2-burst-20260717T100342Z.png` | Dual-axis timeline PNG | ✓ VERIFIED | Valid PNG (magic bytes confirmed), 1440×600, 57.6KB, git-tracked. |
| `evidence/sc3-timestamps-20260717T100342Z.txt` | D-09 triple-check raw evidence | ✓ VERIFIED | Watch-event pod lifecycle + all 3 D-09 checks recorded, git-tracked. |
| `deploy/chart/octoconv/values-loadproof.yaml` | Load-proof-only overlay | ✓ VERIFIED | Exists, contains `scaleDownStabilizationSeconds`, `DOCUMENT_WORKER_CONCURRENCY`, header documents overlay-only usage. |
| `deploy/chart/octoconv/templates/scaledobject-document.yaml` | Values-gated HPA scaleDown override | ✓ VERIFIED | Contains `stabilizationWindowSeconds` gated block; `triggers:` unchanged. |
| `deploy/chart/octoconv/templates/deployment-document-worker.yaml` | Field-level replicas conditional + extraEnv | ✓ VERIFIED | Both present and render correctly with/without overlay. |
| `scripts/keda-load-proof.sh` | Phase 28 live load-proof gate | ✓ VERIFIED | 972 lines (min_lines: 250 satisfied), `bash -n` clean. |
| `scripts/fixtures/gen_heavy_docx.py` | Calibrated heavy-docx generator | ✓ VERIFIED | 109 lines, `--page-units` present, `py_compile` clean. |
| `scripts/fixtures/render_evidence.py` | CSV→PNG dual-axis renderer | ✓ VERIFIED | 101 lines, `twinx()` present, `py_compile` clean. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `values-loadproof.yaml` | `scaledobject-document.yaml` | `keda.document.scaleDownStabilizationSeconds` | ✓ WIRED | `helm template` with overlay renders exactly 1 `stabilizationWindowSeconds: 15`; without overlay renders 0. |
| `values-loadproof.yaml` | `deployment-document-worker.yaml` | `documentWorker.extraEnv` | ✓ WIRED | `helm template` with overlay renders `DOCUMENT_WORKER_CONCURRENCY "1"` in `env:`. |
| `scripts/keda-load-proof.sh` | `deploy/chart/octoconv/values-loadproof.yaml` | `helm install -f values-local.yaml -f values-loadproof.yaml` | ✓ WIRED | grep confirms the exact invocation in the script (line 275); the live transcript shows `STEP 4: helm install octoconv -f values-local.yaml -f values-loadproof.yaml` executed. |
| `scripts/keda-load-proof.sh` | `scripts/fixtures/render_evidence.py` | `uv run --with matplotlib` after sampler | ✓ WIRED | grep + live transcript: `Rendered evidence PNG: .../sc1-sc2-burst-20260717T100342Z.png`. |
| `scripts/keda-load-proof.sh` | `scripts/fixtures/gen_heavy_docx.py` | `uv run --with python-docx` | ✓ WIRED | grep + live transcript: `Generated heavy docx: /tmp/heavy-sc3-20260717T100342Z.docx`, `page-units: 3900`. |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|---------------------|--------|
| `sc1-sc2-burst-*.png` | `queue_depth`, `worker_replicas` | `sc1-sc2-burst-*.csv`, sampled live from `kubectl get --raw` external-metric endpoint + `status.replicas` | Yes — 32 rows with a real 0→20→0 queue-depth curve and 0→1→4→0 replica curve, not static/empty | ✓ FLOWING |
| `sc3-timestamps-*.txt` | SIGTERM ts, pod-terminated state, job_events/job.finished_at | live `kubectl get events`, `kubectl get pod -w` watcher, `psql` against the real `octo_db` | Yes — concrete job IDs, pod name, and a coherent causal timestamp chain (annotate → Killing → job done → pod Completed) | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Chart renders replicas correctly (keda on/off, webhook contrast) | `helm template ... --show-only templates/deployment-{worker,document-worker,chromium-worker,webhook-worker}.yaml` | keda-on: 0/0/0 replicas lines; keda-off: 1/1/1; webhook: 1 (always) | ✓ PASS |
| Document ScaledObject stabilization override gating | `helm template ... --show-only templates/scaledobject-document.yaml` (with/without overlay) | 0 without overlay, 1 (=15) with overlay | ✓ PASS |
| `documentWorker.extraEnv` override | `helm template ... --show-only templates/deployment-document-worker.yaml` (with overlay) | `DOCUMENT_WORKER_CONCURRENCY "1"` present | ✓ PASS |
| `helm lint` | `helm lint deploy/chart/octoconv -f values-local.yaml` | `0 chart(s) failed` | ✓ PASS |
| `scripts/keda-gate.sh` syntax + comment-only diff | `bash -n`; `git diff 6971aba 4b9bc23 -- scripts/keda-gate.sh` | exits 0; diff only touches `#` comment lines | ✓ PASS |
| `scripts/keda-load-proof.sh` syntax | `bash -n scripts/keda-load-proof.sh` | exits 0 | ✓ PASS |
| Python fixtures syntax | `python3 -m py_compile gen_heavy_docx.py render_evidence.py` | no errors | ✓ PASS |
| PNG evidence validity | PNG signature + IHDR read | valid, 1440×600 | ✓ PASS |
| Evidence files git-tracked | `git ls-files --error-unmatch` on all 4 evidence files | all 4 tracked | ✓ PASS |
| No secrets in committed evidence | `grep -rIl -e 'dev-only-change-me' -e 'DATABASE_URL=postgres' -e 'ApiKey ' -e 'octo-pass' evidence/` | no matches (exit 1) | ✓ PASS |

Note: the live cluster gate itself was NOT re-run in this verification session per instructions — the above are offline/static re-derivations of the already-committed live-run evidence (run `20260717T100342Z`), not a fresh cluster execution.

### Probe Execution

No `scripts/*/tests/probe-*.sh`-style probes declared for this phase; the phase's own "probe" is the live gate `scripts/keda-load-proof.sh`, which per the task instructions was NOT re-run (offline verification only, against already-committed run `20260717T100342Z`). `bash -n` syntax validation substituted, as directed.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| KEDA-03 | 28-01, 28-02, 28-03 | Live load-proof: burst-fill via API → observed 0→N→0 scale with timestamps (0→N leg verified separately); scale-down soak — long document conversion in flight survives graceful downscale (not SIGKILL) | ✓ SATISFIED | All 4 SC1-SC4 truths verified above with committed, timestamped, credential-free evidence from a real live gate run (27/27 assertions). |

**Orphaned requirements:** None — REQUIREMENTS.md maps only KEDA-03 to Phase 28, and it is claimed by all three plans' frontmatter.

**Note on REQUIREMENTS.md tracking state:** REQUIREMENTS.md currently shows `KEDA-03` as `[ ]`/"Pending" (line 20, line 60). This is expected at this point in the workflow — per repository convention (confirmed via `git log -- .planning/REQUIREMENTS.md`), the checkbox/status flip to `[x]`/"Complete" happens in the `docs(phase-N): complete phase execution — verification passed` commit that follows a passing verification (see `0f4e1c1` for Phase 27, `01af7e1` for Phase 26). This is not a gap; it is the next expected step after this verification passes.

### Anti-Patterns Found

Scanned all files modified in Phase 28 (`deployment-worker.yaml`, `deployment-document-worker.yaml`, `deployment-chromium-worker.yaml`, `scaledobject-document.yaml`, `values.yaml`, `values-loadproof.yaml`, `scripts/keda-gate.sh`, `scripts/keda-load-proof.sh`, `scripts/fixtures/gen_heavy_docx.py`, `scripts/fixtures/render_evidence.py`) for TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER markers and stub patterns.

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | none found | — | No debt markers, no stub returns, no hardcoded-empty data flows in any Phase 28 file. |

**28-REVIEW.md cross-check (0 critical / 6 warnings / 9 info):** reviewed each warning against the four Success Criteria — none contradicts an already-achieved SC:
- WR-01 (`scaleDownStabilizationSeconds: 0` treated as unset) — the committed run used `15`, not `0`; not exercised.
- WR-02 (stale-Terminating-pod race in busy-pod selection) — did not occur in the committed run; `sc3-timestamps` confirms the correctly-identified busy pod (`document-worker-d5db9bb7f-8dhw8`) was the genuine long-job pod (SIGTERM→completion ordering and psql triple-check both corroborate correct victim selection).
- WR-03 (false-PASS on an S3 error-body download) — the committed run downloaded 4,629,779 bytes, far larger than any plausible MinIO XML error body; the download was real.
- WR-04 (orphaned `kubectl -w` watcher on teardown) — a process-hygiene issue for repeat runs, does not affect the correctness of already-committed evidence.
- WR-05 (Python < 3.11 `fromisoformat` risk) — did not manifest; the PNG was successfully rendered in the committed run.
- WR-06 (CWD-relative image path) — did not manifest; the gate `cd`s to repo root before invoking the fixture generator.
- IN-07 (timestamp-mangling via `tr -d`) is real but functionally benign for 2-digit zero-padded psql timestamps (verified algebraically: `%d`/`%H` fixed-width parsing recovers the correct split regardless of the missing space) — confirmed correct in the committed evidence by independent manual comparison of the raw SIGTERM/finished_at values.

These are legitimate hardening findings for **future** runs of the gate (flake-reduction, teardown hygiene, stricter failure detection) but do not invalidate the SC1-SC4 truths already proven and committed in run `20260717T100342Z`.

### Human Verification Required

None outstanding. The phase's own `checkpoint:human-verify` (28-03-PLAN Task 3) was already resolved: ⚡ auto-approved by the orchestrator under the operator's standing "run all phases to completion" instruction, with the evidence (27/27 assertions, coherent SC1/SC2 timeline, correct SC3 ordering, calibration story) explicitly reviewed before approval, as recorded in `28-03-SUMMARY.md`. Per this verification's instructions, that resolution is accepted as-is; no cluster re-run or fresh human review is requested.

### Gaps Summary

No gaps. All four ROADMAP.md Success Criteria (SC1-SC4) are backed by committed, timestamped, credential-free live-cluster evidence with internally consistent causal ordering, cross-checked against the raw CSV/log/timestamp files (not just SUMMARY narrative). The chart substrate (D-10/WR-02, HPA stabilization override, extraEnv hook) is machine-verified via `helm template`/`helm lint` re-run in this session. `scripts/keda-gate.sh` (Phase 27) is confirmed untouched beyond a comment-only diff. `scripts/keda-load-proof.sh` and both Python fixtures pass offline syntax/compile checks. No secrets found in committed evidence. 28-REVIEW.md's 6 warnings are real but advisory — none contradicts an achieved Success Criterion; they are appropriately scoped as hardening work for future gate runs, not blockers to this phase's goal.

---

_Verified: 2026-07-17T13:53:54Z_
_Verifier: Claude (gsd-verifier)_
