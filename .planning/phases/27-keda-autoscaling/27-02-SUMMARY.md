---
phase: 27-keda-autoscaling
plan: 02
subsystem: infra
tags: [helm, kubernetes, keda, prometheus, networkpolicy, autoscaling]

# Dependency graph
requires:
  - phase: 27-keda-autoscaling
    provides: "plan 01's api-side octoconv_queue_depth registration across all 4 queues (image/document/html/webhook)"
provides:
  - "In-chart minimal Prometheus (Deployment+ConfigMap+Service) gated prometheus.enabled, scraping api:9090"
  - "api Service :9090 metrics port for Prometheus to scrape"
  - "networkpolicy-metrics.yaml :9090 rule fixed to admit the in-chart Prometheus pod instead of a never-created monitoring namespace"
  - "Three per-class KEDA ScaledObjects (image/document/html) co-gated on keda.enabled AND prometheus.enabled, scaling worker/document-worker/chromium-worker from 0"
  - "keda.* and prometheus.* value blocks in values.yaml (default disabled) plus values-local.yaml enablement overlay"
affects: [27-03-live-gate]

# Tech tracking
tech-stack:
  added: ["prom/prometheus:v3.13.1 (in-chart, gated)", "keda.sh/v1alpha1 ScaledObject manifests"]
  patterns:
    - "Combined feature-gate `{{- if and .Values.keda.enabled .Values.prometheus.enabled }}` for ScaledObjects — prevents a ScaledObject from ever referencing a Prometheus Service that did not render"
    - "Combined-file Deployment+ConfigMap+Service template (prometheus.yaml) following redis.yaml's single-file convention"
    - "namespaceSelector + podSelector ANDed in one NetworkPolicy `from` entry to admit a specific pod (component=prometheus) rather than a whole namespace"

key-files:
  created:
    - deploy/chart/octoconv/templates/prometheus.yaml
    - deploy/chart/octoconv/templates/scaledobject-image.yaml
    - deploy/chart/octoconv/templates/scaledobject-document.yaml
    - deploy/chart/octoconv/templates/scaledobject-html.yaml
  modified:
    - deploy/chart/octoconv/templates/service-api.yaml
    - deploy/chart/octoconv/templates/networkpolicy-metrics.yaml
    - deploy/chart/octoconv/values.yaml
    - deploy/chart/octoconv/values-local.yaml

key-decisions:
  - "ScaledObjects gated on `and .Values.keda.enabled .Values.prometheus.enabled` (not keda.enabled alone) so keda-on/prometheus-off never renders a dangling scaler target"
  - "Prometheus pod does NOT carry octoconv.io/tier: app — it is a scrape source, not a metrics target subject to the app metrics NetworkPolicy"
  - "networkpolicy-metrics.yaml :9090 rule combines namespaceSelector(octoconv) + podSelector(component=prometheus) in one ANDed `from` entry, replacing the broken monitoring-namespace rule"
  - "ignoreNullValues: true on every ScaledObject trigger so a genuinely empty queue (empty PromQL result) is treated as 0, not a scaler failure that would spuriously trip fallback.replicas"

patterns-established:
  - "Co-dependency `and` gate for any future chart feature whose trigger/target references another gated object's Service"

requirements-completed: [KEDA-02]

duration: 15min
completed: 2026-07-16
---

# Phase 27 Plan 02: KEDA Autoscaling Chart Manifests Summary

**Three per-class KEDA ScaledObjects (image/document/html) scaling from zero on an in-chart Prometheus's `octoconv_queue_depth` signal, with both landmine gaps from RESEARCH.md (NetworkPolicy monitoring-namespace mismatch, missing api :9090 Service port) closed in the same plan.**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-07-16T20:53:00Z (approx, first commit at 20:57:55Z UTC)
- **Completed:** 2026-07-16T20:59:57Z
- **Tasks:** 3 (2 file-changing, 1 verification-only)
- **Files modified:** 8 (4 created, 4 modified)

## Accomplishments
- In-chart minimal Prometheus (Deployment+ConfigMap+Service combo, `templates/prometheus.yaml`) gated `prometheus.enabled`, scraping `api.octoconv.svc.cluster.local:9090/metrics`
- Closed the two RESEARCH.md landmines: `service-api.yaml` now exposes `:9090`; `networkpolicy-metrics.yaml`'s `:9090` rule now admits the in-chart Prometheus pod by `namespaceSelector`+`podSelector` instead of a `monitoring` namespace that never existed
- Three ScaledObjects (`worker-image-scaledobject`, `worker-document-scaledobject`, `worker-html-scaledobject`) targeting `worker`/`document-worker`/`chromium-worker`, each with `minReplicaCount: 0`, class-correct `maxReplicaCount`/`threshold`/`pollingInterval`/`cooldownPeriod` (D-05/D-06/D-08), `fallback.replicas: 1` (D-07), and `ignoreNullValues: "true"` (Pitfall 4)
- Co-dependency `and` gate (`keda.enabled AND prometheus.enabled`) proven offline: keda-on/prometheus-off renders 0 ScaledObjects, both-on renders exactly 3
- `webhook-worker` deliberately untouched — no `scaledobject-webhook.yaml`, still `replicas: 2` in every render
- Full offline verification matrix run for Task 3: `helm lint` clean, defaults render 0 ScaledObjects/0 Prometheus objects with the full existing 9-Deployment app stack intact, keda-only render 0 ScaledObjects, both-on render 3 ScaledObjects + Prometheus trio + fixed NetworkPolicy, zero `helm template` errors in any flag state

## Task Commits

Each task was committed atomically:

1. **Task 1: In-chart Prometheus + api metrics-port + NetworkPolicy fix** - `f72536e` (feat)
2. **Task 2: Three per-class ScaledObjects + keda values block (D-04..D-10)** - `bd6ec78` (feat)
3. **Task 3: Offline gate — dry-run render + idempotence sanity across flag states** - no commit (verification-only, no file edits — no render errors surfaced, so per plan instructions nothing needed fixing)

**Plan metadata:** committed alongside this SUMMARY (see final commit)

## Files Created/Modified
- `deploy/chart/octoconv/templates/prometheus.yaml` - New: Deployment+ConfigMap+Service, gated `prometheus.enabled`, scrapes api:9090
- `deploy/chart/octoconv/templates/scaledobject-image.yaml` - New: ScaledObject for `worker` (image class)
- `deploy/chart/octoconv/templates/scaledobject-document.yaml` - New: ScaledObject for `document-worker`
- `deploy/chart/octoconv/templates/scaledobject-html.yaml` - New: ScaledObject for `chromium-worker`
- `deploy/chart/octoconv/templates/service-api.yaml` - Added `metrics` port 9090
- `deploy/chart/octoconv/templates/networkpolicy-metrics.yaml` - Replaced broken `monitoring`-namespace `:9090` rule with `namespaceSelector`+`podSelector`(component=prometheus)
- `deploy/chart/octoconv/values.yaml` - Added `prometheus:` and `keda:` value blocks (both default `enabled: false`)
- `deploy/chart/octoconv/values-local.yaml` - Added `prometheus.enabled: true` and `keda.enabled: true` overlay

## Decisions Made
- ScaledObjects gated on the AND of both flags (not `keda.enabled` alone) — the trigger's `serverAddress` points at the in-chart `prometheus` Service, so a ScaledObject must never render unless that Service also renders. Verified offline: `keda.enabled=true, prometheus.enabled=false` renders 0 ScaledObjects.
- Prometheus's pod does NOT carry `octoconv.io/tier: app` — it is a scrape source, not a target the metrics NetworkPolicy should restrict as if it were an app pod (Pitfall 1). Confirmed 0 occurrences of `octoconv.io/tier: app` within the Prometheus pod's rendered block.
- `ignoreNullValues: "true"` set explicitly on every trigger (Pitfall 4) — an empty PromQL result (genuinely empty queue) is 0, not a scaler failure; without this a routine api restart could spuriously trip `fallback.replicas` across all three classes.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Verification script imprecision] `helm lint`/`helm template` bare invocations require secrets that only exist in values-local.yaml**
- **Found during:** Task 1 verification
- **Issue:** The plan's automated verify commands invoke `helm lint deploy/chart/octoconv` and `helm template octoconv deploy/chart/octoconv` with no values file. This chart has required `.Values.secrets.*` (postgresPassword, apiKeySalt, etc.) since Phase 24 — `secret.yaml` has no defaults, so a bare `helm lint`/`helm template` always errors with `nil pointer evaluating interface {}.postgresPassword`, unrelated to anything built in this plan. All prior phases' automated verify commands (24-01, 24-02, 24-03) correctly pass `--values deploy/chart/octoconv/values-local.yaml` or `--set secrets.*=...`; this plan's literal verify text omitted that, which is a plan-authoring gap, not an implementation defect.
- **Fix:** Ran the actual verification with `--values deploy/chart/octoconv/values-local.yaml` (for the "both enabled" case) and with explicit `--set secrets.*=x` placeholders (for the "defaults"/"keda-only" cases), matching the established convention from Phase 24. No template code was changed to work around this — it is a pre-existing, correct chart requirement (secrets have no committed defaults, by design, T-24-03).
- **Files modified:** None (verification-only)
- **Verification:** All acceptance criteria confirmed true under the corrected invocation — see Task Commits / Accomplishments above.

**2. [Rule 1 - Verification script imprecision] Plan's literal grep `"prom/prometheus\|component: prometheus"` also matches the NetworkPolicy's `podSelector` label reference**
- **Found during:** Task 1 verification
- **Issue:** The plan's acceptance criterion asserts the combined grep count is exactly 0 with defaults. But `networkpolicy-metrics.yaml`'s corrected `:9090` rule (this plan's own Task 1 output) always includes the literal string `app.kubernetes.io/component: prometheus` as a `podSelector` match value — regardless of whether `prometheus.enabled` is true. This is intentional: the NetworkPolicy admission rule exists independent of whether the Prometheus object itself currently renders. The literal grep therefore returns 1, not 0, even though zero actual Prometheus Deployment/ConfigMap/Service objects render.
- **Fix:** No implementation change needed — used a more precise check (`prom/prometheus` image string, `name: prometheus-config`, and `name: prometheus` as a standalone metadata name) which correctly returns 0 with defaults and >=1 with the local overlay. Documented here rather than altering the NetworkPolicy's intentional always-present admission rule.
- **Files modified:** None (verification-only)
- **Verification:** Precise grep confirmed 0 actual Prometheus objects with defaults, 5 with the local overlay (Deployment, ConfigMap, Service, plus image string and standalone name references).

---

**Total deviations:** 2 (both verification-script precision issues, zero implementation changes)
**Impact on plan:** No scope creep, no architectural changes. Both deviations are documentation of how the plan's literal verify commands needed minor adjustment to correctly test the intended behavior; the underlying chart implementation matches every acceptance criterion.

## Issues Encountered
- `kubectl apply --dry-run=client` against the both-flags-on render correctly accepted all 21 non-KEDA objects (Deployments, Services, ConfigMaps, Secret, PVCs, NetworkPolicies, Job) but rejected the 3 ScaledObjects with `no matches for kind "ScaledObject" in version "keda.sh/v1alpha1" — ensure CRDs are installed first`. This is expected: the KEDA CRDs are not installed in this offline-only plan (installing KEDA itself is plan 03's live-gate job, per this plan's explicit scope boundary). Not a defect; confirms the chart otherwise dry-run-clean.

## User Setup Required

None - no external service configuration required. This plan is offline-only (helm lint/template); no cluster changes were made.

## Next Phase Readiness
- Plan 03 (live gate) can now: install the pinned KEDA Helm chart, install `octoconv` with `keda.enabled=true prometheus.enabled=true` (values-local layered), and prove the 0→1→0 scaling behavior per class against the real Prometheus external-metrics adapter.
- All offline preconditions are in place: chart renders cleanly in all three flag states, the api metrics port and NetworkPolicy admission are wired, and the three ScaledObjects' trigger queries/thresholds match the locked D-04/D-05/D-06/D-08 values exactly.
- No blockers identified for plan 03.

---
*Phase: 27-keda-autoscaling*
*Completed: 2026-07-16*
