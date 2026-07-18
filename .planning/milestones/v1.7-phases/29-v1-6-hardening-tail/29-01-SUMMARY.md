---
phase: 29-v1-6-hardening-tail
plan: 01
subsystem: infra
tags: [helm, keda, prometheus, kubernetes, autoscaling, chart]

# Dependency graph
requires:
  - phase: 27-keda-autoscaling
    provides: the ScaledObject trio, Prometheus in-chart deployment, and scaleDownStabilizationSeconds knob this plan hardens
  - phase: 28-autoscale-load-proof
    provides: values-loadproof.yaml overlay and the stabilization-window template consumed here
provides:
  - Fail-safe ignoreNullValues:false on all three ScaledObject Prometheus triggers
  - Retry-inclusive PromQL (pending|active|retry) on all three ScaledObject triggers
  - Recursion-safe checksum/config annotation on the Prometheus pod-template, backed by a shared _helpers.tpl named-template
  - hasKey-AND-not-nil-guarded stabilization block on scaledobject-document.yaml (falsy-0 fix)
  - values.yaml cooldownPeriod > max-retry-backoff invariant documentation
affects: [phase-33-audio-scaledobject, keda-gate-scripts]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Shared named-template as checksum source (avoid whole-file self-hash recursion in multi-resource template files)"
    - "hasKey + ne...nil pairing to distinguish unset/null from explicit falsy values in Helm templates"

key-files:
  created: []
  modified:
    - deploy/chart/octoconv/templates/scaledobject-image.yaml
    - deploy/chart/octoconv/templates/scaledobject-document.yaml
    - deploy/chart/octoconv/templates/scaledobject-html.yaml
    - deploy/chart/octoconv/templates/_helpers.tpl
    - deploy/chart/octoconv/templates/prometheus.yaml
    - deploy/chart/octoconv/values.yaml

key-decisions:
  - "D-01: ignoreNullValues flipped true->false on all three ScaledObjects; sustained metric absence now trips fallback.replicas:1 instead of reading as an empty queue"
  - "D-03: PromQL triggers now count pending|active|retry so a retry backlog scales a zeroed class back up before the reconciler sweep"
  - "D-02/WR-05: checksum/config hashes a new _helpers.tpl named-template (octoconv.prometheusScrapeConfig), not the whole prometheus.yaml file, avoiding infinite template recursion since that file also contains the pod-template carrying the annotation"
  - "D-06 fix #1: scaledobject-document.yaml's stabilization guard requires BOTH hasKey and ne...nil so the null production default renders no stabilizationWindowSeconds while an explicit 0 still renders instant downscale"

patterns-established:
  - "Multi-resource single-file chart templates (Deployment+ConfigMap+Service) must externalize any content they want to self-checksum into a named-template in _helpers.tpl, never hash `$.Template.BasePath` of their own file"

requirements-completed: [HARD-01, HARD-03]

# Metrics
duration: ~35min
completed: 2026-07-17
---

# Phase 29 Plan 01: Chart Robustness (Offline) Summary

**Flipped all three KEDA ScaledObjects to fail-safe null handling and retry-inclusive PromQL, replaced a would-be recursive Prometheus checksum with a shared named-template, and closed the falsy-0 stabilization bug with a paired hasKey/not-nil guard — all verified offline via helm lint/template, no cluster involved.**

## Performance

- **Duration:** ~35 min
- **Completed:** 2026-07-17
- **Tasks:** 3/3 completed
- **Files modified:** 6

## Accomplishments
- Closed HARD-01 (WR-01/WR-05/WR-06): `ignoreNullValues: "false"` + retry-inclusive PromQL on all three ScaledObject templates, plus a recursion-safe Prometheus config-drift checksum
- Closed HARD-03 gate-tooling fix #1 (falsy-0 stabilization): `scaledobject-document.yaml`'s stabilization block now distinguishes explicit `0` from the unset/null production default
- Proved the whole substrate renders clean under `helm lint`/`helm template` with both `values-local.yaml` and `values-loadproof.yaml`, with enforcing assertions on every D-01/D-02/D-03/D-06 behavior

## Task Commits

1. **Task 1: Flip ScaledObject trio to fail-safe null handling + retry-inclusive PromQL + hasKey-AND-not-nil-guarded stabilization** - `a07d71e` (feat)
2. **Task 2: Recursion-safe Prometheus checksum/config annotation via shared _helpers.tpl named-template + values.yaml cooldownPeriod invariant comment** - `da861fd` (feat)
3. **Task 3: Offline chart proof — helm lint + render assertions** - no commit (verification-only task, no files modified)

**Plan metadata:** committed alongside this SUMMARY.

## Files Created/Modified
- `deploy/chart/octoconv/templates/scaledobject-image.yaml` - ignoreNullValues:false, retry-inclusive query, rewritten fail-safe comment + header
- `deploy/chart/octoconv/templates/scaledobject-document.yaml` - same as image, plus the hasKey+ne-nil stabilization guard
- `deploy/chart/octoconv/templates/scaledobject-html.yaml` - same as image
- `deploy/chart/octoconv/templates/_helpers.tpl` - new `octoconv.prometheusScrapeConfig` named-template, shared by ConfigMap data and checksum annotation
- `deploy/chart/octoconv/templates/prometheus.yaml` - pod-template `checksum/config` annotation; ConfigMap `data.prometheus.yml` now `include`s the shared named-template instead of inlining it
- `deploy/chart/octoconv/values.yaml` - cooldownPeriod > max-retry-backoff invariant comment referencing `internal/queue/queue.go`'s per-class retry schedules

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue in verify script] `values-loadproof.yaml` cannot render standalone**
- **Found during:** Task 3 verification
- **Issue:** The plan's Task 3 verify script renders `helm template oc deploy/chart/octoconv -f deploy/chart/octoconv/values-loadproof.yaml` alone. `values-loadproof.yaml`'s own header comment states it is "layered ON TOP of values-local.yaml, NEVER standalone" — rendering it alone fails in `secret.yaml` on a nil `.Values.secrets.postgresPassword`, which only `values-local.yaml` sets. This is pre-existing chart behavior, unrelated to any edit in this plan (confirmed: the same nil-pointer failure has nothing to do with ScaledObject/Prometheus/_helpers.tpl changes).
- **Fix:** Verified the intent — "no template break under values-loadproof" — using the documented layering (`-f values-local.yaml -f values-loadproof.yaml`), which renders cleanly and also exercises the falsy-0 fix live via the overlay's own `scaleDownStabilizationSeconds: 15` (confirmed `stabilizationWindowSeconds: 15` in output). No source files were changed for this fix; it only affects how the offline proof was run.
- **Files modified:** None (verification-only).
- **Commit:** N/A (no file changes).

## Threat Flags

None — this plan's threat model (T-29-01 through T-29-04) is fully addressed by the committed changes; no new surface introduced beyond what was already declared.

## Self-Check: PASSED

Verified below.
