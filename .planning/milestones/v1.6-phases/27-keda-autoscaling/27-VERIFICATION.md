---
phase: 27-keda-autoscaling
verified: 2026-07-16T22:27:34Z
status: passed
score: 8/8 must-haves verified
overrides_applied: 0
---

# Phase 27: KEDA Autoscaling Verification Report

**Phase Goal:** Each engine-class worker scales itself on its own queue depth via KEDA — including genuine scale-from-zero — while the sweeper-hosting webhook-worker stays fixed and never scales down.
**Verified:** 2026-07-16T22:27:34Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `octoconv_queue_depth` is registered on the always-on `api` process for all four queues (image/document/html/webhook) | ✓ VERIFIED | `cmd/api/main.go:91-92`: `prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueWebhook))`. `go build ./... && go vet ./...` clean. |
| 2 | No worker binary registers the collector any more (single source of truth) | ✓ VERIFIED | `grep -rl "NewQueueDepthCollector" cmd/` returns exactly `cmd/api/main.go`. |
| 3 | Metric resolves via `kubectl get --raw` on the external metrics API even at genuinely 0 worker replicas (SC1) | ✓ VERIFIED (script/evidence audit, not re-run) | `scripts/keda-gate.sh` STEP 6 polls `worker` to `status.replicas=0`, discovers the external metric name live (`status.externalMetricNames`, never hardcoded), then asserts `kubectl get --raw .../external.metrics.k8s.io/...` returns a `"value"`. 27-03-SUMMARY.md records the live transcript: `worker status.replicas settled at 0`, metric name `s0-prometheus`, raw response `{"value":"0"}`. Human-verify checkpoint (Task 3) was approved verbatim ("approved"). Script content is internally consistent with the transcript (18 distinct `assert_eq`/`assert_nonempty`/manual PASS_COUNT increments counted in the script match the claimed "18/18 assertions"). Per task instructions, the live cluster gate was not re-run; this is an evidence-consistency audit, not a fresh live confirmation. |
| 4 | KEDA v2.20.x `ScaledObject`s for image/document/html scale each from `minReplicaCount 0` via the Prometheus scaler | ✓ VERIFIED | `deploy/chart/octoconv/templates/scaledobject-{image,document,html}.yaml` each set `minReplicaCount: 0`, `type: prometheus` trigger with `serverAddress: http://prometheus.<ns>.svc.cluster.local:9090` and `query: sum(octoconv_queue_depth{queue="<class>", state=~"pending|active"})`. `helm template -f values-local.yaml` renders exactly 3 ScaledObjects named `worker-image-scaledobject`/`worker-document-scaledobject`/`worker-html-scaledobject`. |
| 5 | webhook-worker has no `ScaledObject` and runs fixed `replicas: 2` | ✓ VERIFIED | No `scaledobject-webhook.yaml` exists; chart search finds zero webhook-targeting ScaledObjects in any render (`grep -c "scaledobject-webhook\|worker-webhook-scaledobject"` = 0). Rendered `deployment-webhook-worker.yaml` → `replicas: 2` (confirmed via `helm template -f values-local.yaml`). Live gate script asserts this at START/MID/END (6 checks) per 27-03-SUMMARY.md. |
| 6 | Autoscaling gated behind `keda.enabled` (and co-gated on `prometheus.enabled` to prevent dangling scaler targets) | ✓ VERIFIED | `deploy/chart/octoconv/values.yaml`: `keda.enabled: false` / `prometheus.enabled: false` by default. All 3 ScaledObject templates and `prometheus.yaml` are wrapped in `{{- if and .Values.keda.enabled .Values.prometheus.enabled }}` / `{{- if .Values.prometheus.enabled }}`. Verified offline: defaults → 0 ScaledObjects; `keda.enabled=true` alone → 0 ScaledObjects; both enabled (values-local.yaml) → 3 ScaledObjects. |
| 7 | Per-class `pollingInterval`/`cooldownPeriod` tuned per engine class (image fast/bursty; document/html slower) | ✓ VERIFIED | `values.yaml` `keda:` block: image `pollingInterval:5, cooldownPeriod:60`; document `pollingInterval:15, cooldownPeriod:120`; html `pollingInterval:10, cooldownPeriod:90` — image is the shortest/fastest, document/html are longer, matching the roadmap SC4 intent. |
| 8 | Per-class asynq `ShutdownTimeout` set so pod `terminationGracePeriodSeconds` is no longer dead config (PLAN 27-01 must-have) | ✓ VERIFIED | All four workers set `ShutdownTimeout` in `asynq.Config`: image `ENGINE_TIMEOUT+10s`(130s<150s), document `DOCUMENT_ENGINE_TIMEOUT+10s`(310s<330s), html `HTML_ENGINE_TIMEOUT+10s`(70s<90s), webhook flat `30s`(<60s). `go build ./...` clean. |

**Score:** 8/8 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `cmd/api/main.go` | queue-depth collector registration for all 4 queues | ✓ VERIFIED | Contains `NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueWebhook)`; builds/vets clean. |
| `internal/e2e/e2e_test.go` | compose-E2E metrics reachability + relocation assertion | ✓ VERIFIED | `TestQueueDepthMetricRelocationE2E` (lines 1169-1241) asserts api :9190 serves `octoconv_queue_depth` for all four `queue="X"` labels and image worker :9191 does not; substantive implementation (not a stub), `go vet ./internal/e2e/...` clean. |
| `docker-compose.e2e.yml` | validation-only metrics-port publish for api and image worker | ✓ VERIFIED | `api` gets `9190:9090`, `worker` gets `9191:9090`, both `METRICS_ADDR: "0.0.0.0:9090"`; base `docker-compose.yml` untouched. |
| `internal/metrics/metrics_test.go` | unit half of D-03 | ✓ VERIFIED | `TestNewQueueDepthCollectorDescribeAllFourQueues` passes (`go test ./internal/metrics/... -run QueueDepth`). |
| `deploy/chart/octoconv/templates/prometheus.yaml` | in-chart Prometheus Deployment+ConfigMap+Service, gated `prometheus.enabled` | ✓ VERIFIED | Present, gated, scrapes `api.<ns>.svc.cluster.local:9090`; does not carry `octoconv.io/tier: app` (per RESEARCH.md Pitfall 1). |
| `deploy/chart/octoconv/templates/scaledobject-image.yaml` (+document, +html) | KEDA ScaledObject per class | ✓ VERIFIED | All three present, `kind: ScaledObject`, correct `scaleTargetRef`/thresholds/maxReplicaCount per D-05/D-06. |
| `deploy/chart/octoconv/values.yaml` | `keda.*` and `prometheus.*` blocks, default disabled | ✓ VERIFIED | Both blocks present, `enabled: false` by default. |
| `deploy/chart/octoconv/templates/service-api.yaml` | api Service exposes metrics port 9090 | ✓ VERIFIED | `metrics` port `9090`/targetPort `9090` added alongside existing `http` 8090. |
| `deploy/chart/octoconv/templates/networkpolicy-metrics.yaml` | admits in-chart Prometheus pod | ✓ VERIFIED | `:9090` rule now uses `namespaceSelector`(octoconv) + `podSelector`(`app.kubernetes.io/component: prometheus`); the string `monitoring` no longer appears in that rule. |
| `scripts/keda-gate.sh` | reproducible Phase-27 live hard gate on OrbStack k8s | ✓ VERIFIED | `bash -n` clean, executable, `set -euo pipefail`, discovers `externalMetricNames` live, asserts webhook-worker gate (21 references, START/MID/END), teardown via EXIT trap. 453 lines, no debt markers. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `cmd/api/main.go` | `metrics.NewQueueDepthCollector` | `prometheus.MustRegister` with all 4 queue constants | ✓ WIRED | Confirmed by direct grep of `cmd/api/main.go:91-92`. |
| `internal/e2e/e2e_test.go` | `api :9090/metrics` (published as :9190) | host-published metrics port | ✓ WIRED | Test performs real HTTP GET and asserts content; `docker-compose.e2e.yml` publishes the port. |
| `scaledobject-*.yaml` | `prometheus` Service :9090 | trigger `serverAddress` FQDN, co-gated on `prometheus.enabled` | ✓ WIRED | `serverAddress: http://prometheus.<ns>.svc.cluster.local:9090` in all 3 ScaledObjects; co-dependency `and` gate confirmed offline (keda-on/prometheus-off → 0 ScaledObjects). |
| `prometheus.yaml` scrape config | `api :9090/metrics` | static scrape target | ✓ WIRED | `prometheus-config` ConfigMap target: `api.<ns>.svc.cluster.local:9090`. |
| `networkpolicy-metrics.yaml` | prometheus pod | `namespaceSelector` + `podSelector` `component=prometheus` on :9090 | ✓ WIRED | Confirmed in rendered NetworkPolicy YAML. |
| `scripts/keda-gate.sh` | KEDA external metrics API | `kubectl get scaledobject -o jsonpath externalMetricNames` then `kubectl get --raw` | ✓ WIRED (per script inspection + recorded transcript) | STEP 6 of the script implements exactly this two-step discovery-then-query pattern; transcript in 27-03-SUMMARY.md shows the live values obtained. |
| `scripts/keda-gate.sh` | worker Deployments | job submit → replica-count poll | ✓ WIRED | `postJob()` + `waitForReplicasAtLeast`/`waitForReplicasAtMost` helpers implement the pattern for all three classes plus the image full-cycle. |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|---------------------|--------|
| `cmd/api/main.go` collector | `octoconv_queue_depth` gauge series | `asynq.NewInspector(redisOpt).GetQueueInfo` per queue (pull-based `Collect()`) | Yes — live compose E2E test confirms 20 real series (4 queues × 5 states) at value 0 against real Redis, not a static/empty return | ✓ FLOWING |
| `scaledobject-*.yaml` trigger | `sum(octoconv_queue_depth{queue="X", state=~"pending|active"})` | in-chart Prometheus scraping api :9090 | Yes — live gate transcript shows a real `{"value":"0"}` response from the external metrics API at genuinely 0 replicas, and real 0→1 transitions after real job submissions | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Repo builds/vets clean after all Phase-27 Go changes | `go build ./... && go vet ./...` | exit 0 | ✓ PASS |
| Collector relocated exactly once, on api | `grep -rl "NewQueueDepthCollector" cmd/` | `cmd/api/main.go` only | ✓ PASS |
| Unit test for 4-queue collector construction | `go test ./internal/metrics/... -run QueueDepth -count=1` | `PASS` (2 tests) | ✓ PASS |
| `go vet` on e2e package | `go vet ./internal/e2e/...` | exit 0 | ✓ PASS |
| Helm chart lints clean with local overlay | `helm lint deploy/chart/octoconv -f values-local.yaml` | `0 chart(s) failed` | ✓ PASS |
| Defaults render 0 ScaledObjects | `helm template ... (defaults, secrets stubbed)` \| `grep -c "kind: ScaledObject"` | `0` | ✓ PASS |
| keda-on/prometheus-off renders 0 ScaledObjects (co-gate) | `helm template ... --set keda.enabled=true` \| `grep -c "kind: ScaledObject"` | `0` | ✓ PASS |
| Both-on (values-local) renders exactly 3 ScaledObjects | `helm template ... -f values-local.yaml` \| `grep -c "kind: ScaledObject"` | `3` | ✓ PASS |
| webhook-worker never gets a ScaledObject | `helm template ... -f values-local.yaml` \| `grep -c "scaledobject-webhook\|worker-webhook-scaledobject"` | `0` | ✓ PASS |
| webhook-worker renders fixed `replicas: 2` | rendered `deployment-webhook-worker.yaml` `spec.replicas` | `2` | ✓ PASS |
| `keda-gate.sh` script parses cleanly and is executable | `bash -n scripts/keda-gate.sh && test -x scripts/keda-gate.sh` | clean, executable | ✓ PASS |

### Probe Execution

Per explicit task instruction: "the live cluster gate was already run and human-approved (18/18 assertions) — verify code/manifest/script truths against the repo state and the recorded evidence, not by reinstalling to the cluster." The verifier did NOT re-run `scripts/keda-gate.sh` against a live cluster (that would require `helm install`/live k8s mutation, explicitly out of scope for this verification pass). Instead:

| Check | Method | Result | Status |
|-------|--------|--------|--------|
| `scripts/keda-gate.sh` syntax/structure | `bash -n`, executable bit, grep for `externalMetricNames`/`set -euo pipefail`/`webhook-worker` | All present as claimed | ✓ VERIFIED (static) |
| Script assertion count matches SUMMARY's "18/18" claim | manual count of `assert_eq`/`assert_nonempty`/manual `PASS_COUNT` increments in the script body | 18 distinct assertion points counted, matching the claim exactly | ✓ CONSISTENT |
| Current cluster state (read-only check, no install/uninstall performed) | `kubectl get ns`, `helm list -A`, `kubectl get all -n octoconv`, `kubectl get all -n keda`, `kubectl get crd \| grep keda` | `octoconv`/`keda` namespace objects exist (stale, empty shells) but contain zero Deployments, zero Helm releases, zero KEDA CRDs | ✓ CONSISTENT with "clean teardown" claim (SUMMARY claimed 0 deployments/0 CRDs, not 0 namespace objects — namespace deletion was explicitly NOT part of the teardown script, confirmed in script source and in the code-review's IN-05 note) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| KEDA-01 | 27-01-PLAN.md | `octoconv_queue_depth` экспонируется always-on api-процессом | ✓ SATISFIED | Collector relocated to `cmd/api/main.go`, removed from all 4 workers, live compose-E2E proof exists and is substantive. |
| KEDA-02 | 27-02-PLAN.md, 27-03-PLAN.md | KEDA v2.20.x + ScaledObjects (image/document/html, minReplicaCount 0); webhook-worker excluded, fixed 2 replicas | ✓ SATISFIED | Chart manifests present/gated/offline-verified (27-02); live gate script authored, executed, and human-approved with 18/18 assertions recorded (27-03). |

No orphaned requirements: REQUIREMENTS.md maps only KEDA-01 and KEDA-02 to Phase 27, and both appear in the `requirements:` frontmatter of the plans (27-01 declares KEDA-01; 27-02 and 27-03 both declare KEDA-02). KEDA-03 is explicitly out of scope for this phase (mapped to Phase 28).

**Note (non-blocking):** `.planning/REQUIREMENTS.md` still shows `KEDA-01`/`KEDA-02` as `[ ]`/"Pending" in the traceability table — this is the standard pre-completion state; it is expected to be flipped to `[x]`/"Complete" during the phase-completion "evolve project docs" step that follows a passing verification (matching the pattern seen for Phase 26 in git history), not a gap in this phase's implementation.

### Anti-Patterns Found

None. Scanned all 17 files modified across the three plans (`cmd/api/main.go`, all four worker mains, `internal/metrics/metrics_test.go`, `internal/e2e/e2e_test.go`, `docker-compose.e2e.yml`, `prometheus.yaml`, `service-api.yaml`, `networkpolicy-metrics.yaml`, three `scaledobject-*.yaml`, `values.yaml`, `values-local.yaml`, `scripts/keda-gate.sh`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` — zero matches.

**Informational (from the phase's own code-review, `27-REVIEW.md`, status: `issues_found`, 0 critical / 6 warning / 9 info):** None of the 6 warnings block the roadmap success criteria — all four SCs are demonstrated true by the live gate transcript and offline chart renders. They are legitimate robustness/production-hardening follow-ups, most relevant to Phase 28 (load-proof) tuning:
- WR-01: `ignoreNullValues: "true"` means a *prolonged* api outage (not just a brief restart) could scale busy classes to 0 without tripping `fallback`, since KEDA's fallback only fires on scaler *errors*, not "genuinely 0" results after the source is gone.
- WR-02: Helm always renders `spec.replicas: 1` on the three scaled Deployments; a `helm upgrade` after KEDA has scaled to 0 will cold-start one pod per class until KEDA re-settles — replica flapping on every deploy (the gate script already has to poll around this, per its own STEP 6 comment).
- WR-03: ScaledObjects hardcode `metadata.namespace: {{ .Values.global.namespace }}` instead of `{{ .Release.Namespace }}` — works today only because the gate always installs into `-n octoconv` matching that value.
- WR-04: The `ShutdownTimeout` ↔ `terminationGracePeriodSeconds` 5s-margin invariant is maintained by hand across two disconnected files with no cross-reference comment on the values.yaml side.
- WR-05: No `checksum/config` annotation on the Prometheus pod template — a ConfigMap-only `helm upgrade` won't restart Prometheus to pick up scrape-config changes.
- WR-06: `cooldownPeriod > max retry backoff` is an unstated invariant; tuning `cooldownPeriod` below ~30s in Phase 28 could strand retry-state tasks at 0 replicas.

These are recorded here for visibility per the adversarial-verification mandate, but do not change the phase's pass/fail determination — the roadmap's four Success Criteria are about ScaledObjects existing/scaling/gating/tuning correctly, all of which are proven true; none of the SCs assert resilience to a prolonged api outage or `helm upgrade`-time replica-flapping, which are the (valid) concerns raised.

### Human Verification Required

None outstanding. The phase's one human-verify checkpoint (27-03 Task 3: "Human verification of the live gate evidence") was already executed during phase execution and approved verbatim ("approved"), as recorded in `27-03-SUMMARY.md` under "Checkpoint Resolution (Task 3)". No new human-verification items were identified by this audit.

### Gaps Summary

No gaps. All 8 derived must-have truths (roadmap SC1-4 plus PLAN-level KEDA-01 shutdown-timeout truth) verified against actual code, rendered Helm manifests, and the recorded live-gate transcript/script consistency check. `go build`/`go vet`/`go test` all pass. `helm lint`/`helm template` confirm exact ScaledObject counts (0/0/3) across all three flag states. The live cluster gate was not re-run per explicit instruction, but the script's structure, assertion count (18, matching the claimed 18/18), and recorded transcript are internally consistent and the human-verify checkpoint was already approved.

---

_Verified: 2026-07-16T22:27:34Z_
_Verifier: Claude (gsd-verifier)_
