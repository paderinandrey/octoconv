---
phase: 28-autoscale-load-proof
plan: 01
subsystem: infra
tags: [helm, keda, k8s, autoscaling, hpa]

# Dependency graph
requires:
  - phase: 27-keda-autoscaling
    provides: "KEDA ScaledObjects per engine-class, in-chart Prometheus, keda.enabled/prometheus.enabled co-dependency guard idiom, keda-gate.sh live gate"
provides:
  - "Field-level spec.replicas omission on the three KEDA-scaled Deployments (worker, document-worker, chromium-worker) when keda.enabled && prometheus.enabled — helm upgrade no longer fights the HPA on a scaled-to-zero class (D-10/WR-02)"
  - "Values-gated fast HPA scaleDown.stabilizationWindowSeconds override on the document ScaledObject, off by default (null), 15s via values-loadproof.yaml"
  - "documentWorker.extraEnv hook mirroring the existing api.extraEnv idiom"
  - "values-loadproof.yaml overlay: scaleDownStabilizationSeconds=15 + DOCUMENT_WORKER_CONCURRENCY=1"
affects: [28-02, 28-03]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Field-level Helm conditional (not whole-resource gating): wrap only the spec.replicas line in {{- if and .Values.keda.enabled .Values.prometheus.enabled }}...{{- else }}...{{- end }} so the Deployment object always renders but the field is conditional"
    - "Values-gated nested HPA behavior block (advanced.horizontalPodAutoscalerConfig.behavior.scaleDown) mirroring the existing {{- if .Values.X }} optional-block idiom from deployment-asynqmon.yaml"

key-files:
  created:
    - deploy/chart/octoconv/values-loadproof.yaml
  modified:
    - deploy/chart/octoconv/templates/deployment-worker.yaml
    - deploy/chart/octoconv/templates/deployment-document-worker.yaml
    - deploy/chart/octoconv/templates/deployment-chromium-worker.yaml
    - deploy/chart/octoconv/templates/scaledobject-document.yaml
    - deploy/chart/octoconv/values.yaml
    - scripts/keda-gate.sh

key-decisions:
  - "spec.replicas omission is field-level, not whole-resource — the Deployment must always render so KEDA/HPA has an object to take ownership of"
  - "webhook-worker deliberately untouched — it is hard-excluded from KEDA (fixed 2 replicas, sweeper host)"
  - "keda-gate.sh STEP 6 poll-for-settling loop kept functionally unchanged (comment-only diff) — fresh-install settling is inherent to omitted-replicas semantics (k8s defaults unset replicas to 1 on CREATE), not the pre-WR-02 upgrade-reset bug the fix addresses"
  - "scaleDownStabilizationSeconds governs only the document class's N->N-1 (N>1) transition, distinct from cooldownPeriod which governs 1->0 — kept as two separate, non-conflated knobs per RESEARCH.md Pitfall 1"
  - "D-11/WR-01 (ignoreNullValues / trigger semantics) explicitly left untouched this plan"

patterns-established:
  - "Field-level Helm conditional for scale-subresource fields owned by an external controller (KEDA/HPA) — deployment object unconditional, single field conditional"

requirements-completed: [KEDA-03]

# Metrics
duration: ~20min
completed: 2026-07-17
---

# Phase 28 Plan 01: Chart Fixes for Load-Proof Substrate Summary

**Field-level `spec.replicas` omission on the three KEDA-scaled Deployments plus a values-gated document-class HPA scaleDown stabilization override, both production-inert by default and both machine-verified via `helm template` — the chart substrate the live load-proof gate (28-02/28-03) depends on.**

## Performance

- **Tasks:** 2 completed
- **Files modified:** 6 (5 modified + 1 new)

## Accomplishments

- The three KEDA-scaled Deployments (`worker`, `document-worker`, `chromium-worker`) now omit `spec.replicas` when `keda.enabled && prometheus.enabled` are both true, so a `helm upgrade` never resets a scaled-to-zero class back to 1 and fights the HPA (D-10/WR-02). `webhook-worker` — hard-excluded from KEDA — is untouched and still renders `replicas: {{ .Values.webhookWorker.replicas }}` unconditionally in every mode.
- `scripts/keda-gate.sh` STEP 6's stale explanatory comment was corrected to describe the post-WR-02 semantics (unset replicas defaults to 1 on fresh CREATE, KEDA then scales to 0 after cooldown) — machine-verified as a comment-only diff (no assertion/loop/echo line changed).
- The document `ScaledObject` gained a values-gated `advanced.horizontalPodAutoscalerConfig.behavior.scaleDown.stabilizationWindowSeconds` block, off by default (`null` in `values.yaml`, production Kubernetes 300s HPA default preserved), 15s via the new `values-loadproof.yaml` overlay — closing RESEARCH.md Pitfall 1 (the standard-HPA-governed 2→1 transition would otherwise default to 300s and outrun the document engine's own 300s timeout, invalidating SC3's timing assumptions).
- `deployment-document-worker.yaml` gained a `documentWorker.extraEnv` hook mirroring the existing `deployment-api.yaml` idiom, letting the load-proof overlay force `DOCUMENT_WORKER_CONCURRENCY=1` (deterministic pod↔job victim selection, D-08) without touching the ConfigMap's production default of `2`.
- WR-01/D-11 (`ignoreNullValues`, the prometheus trigger query) is byte-identical to before — confirmed via `git diff` showing no change to the `triggers:` block.

## Task Commits

Each task was committed atomically:

1. **Task 1: Field-level spec.replicas omission on the three KEDA-scaled Deployments (D-10/WR-02)** - `4b9bc23` (feat)
2. **Task 2: Document ScaledObject stabilization override + document-worker extraEnv + load-proof overlay (D-07/D-08 timing)** - `08c12dc` (feat)

## Files Created/Modified

- `deploy/chart/octoconv/templates/deployment-worker.yaml` - field-level conditional on `spec.replicas`
- `deploy/chart/octoconv/templates/deployment-document-worker.yaml` - field-level conditional on `spec.replicas` + new `documentWorker.extraEnv` block
- `deploy/chart/octoconv/templates/deployment-chromium-worker.yaml` - field-level conditional on `spec.replicas`
- `deploy/chart/octoconv/templates/scaledobject-document.yaml` - new values-gated `advanced.horizontalPodAutoscalerConfig.behavior.scaleDown` block
- `deploy/chart/octoconv/values.yaml` - new `keda.document.scaleDownStabilizationSeconds: null` key
- `deploy/chart/octoconv/values-loadproof.yaml` (new) - overlay setting `scaleDownStabilizationSeconds: 15` + `documentWorker.extraEnv.DOCUMENT_WORKER_CONCURRENCY: "1"`
- `scripts/keda-gate.sh` - comment-only update to STEP 6 explaining post-WR-02 settling semantics

## Decisions Made

- Field-level (not whole-resource) conditional for `spec.replicas` — the Deployment object must always render so KEDA can take ownership of an existing resource; only the replica-count field is conditional.
- Kept `keda-gate.sh`'s STEP 6 poll loop functionally unchanged, updating only its explanatory comment — the settling poll is required by omitted-replicas semantics in general (unset replicas defaults to 1 on Kubernetes CREATE), not specifically by the upgrade-reset bug this plan fixes, so removing it would be an unrelated functional change outside this plan's scope.
- `scaleDownStabilizationSeconds` added only under `keda.document` (not `keda.image`/`keda.html`) — RESEARCH.md's Pitfall 1 is specifically a document-class 2→1 timing problem; image/html don't exercise an N>1 downscale in this phase's planned scenarios.

## Deviations from Plan

None - plan executed exactly as written.

## Threat Flags

None - no new network endpoints, auth paths, file-access patterns, or schema changes introduced; this plan is chart-YAML + one bash comment only, matching the plan's own `threat_model` disposition (accept for webhook-worker/npm-install rows, mitigate rows all satisfied by the field-scoped conditional and null-default overlay).

## Self-Check: PASSED
