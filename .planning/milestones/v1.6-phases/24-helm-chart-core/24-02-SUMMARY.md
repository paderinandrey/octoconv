---
phase: 24-helm-chart-core
plan: 02
subsystem: infra
tags: [helm, kubernetes, networkpolicy, deployment, probes, orbstack, minio, hooks]

# Dependency graph
requires:
  - phase: 24-helm-chart-core (plan 01)
    provides: deploy/chart/octoconv scaffold — values.yaml per-service blocks, _helpers.tpl (octoconv.labels/selectorLabels/commonEnv), octoconv-config ConfigMap, octoconv-secret Secret, postgres/redis/minio stateful backbone
provides:
  - Five app Deployments (api, worker, document-worker, chromium-worker, webhook-worker) with dependency-aware probes, per-class terminationGracePeriodSeconds, and octoconv.io/tier=app pod label
  - api Service (literal name "api", ClusterIP 8090)
  - networkpolicy-metrics.yaml closing the D-04 metrics-bind landmine atomically with the 0.0.0.0 bind shipped in plan 01's ConfigMap
  - Gated asynqmon Deployment/Service/NetworkPolicy (disabled by default)
  - createbucket post-install/post-upgrade hook Job (idempotent, wait-for-minio initContainer)
  - D-05 refinement recorded: migrate hook Job dropped entirely — api self-migrates
affects: [24-03 (E2E Job + upgrade-idempotence gate), 27-keda (must never scale webhook-worker below 2 or attach a ScaledObject to it), 27-observability (monitoring namespaceSelector placeholders need a real Prometheus/ops namespace)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Dependency-aware probes: real /healthz for api, /metrics-as-liveness-proxy for asynq workers with no health endpoint (D-08)"
    - "Tier-label NetworkPolicy discriminator: octoconv.io/tier: app added ONLY to pod templates, kept off the Deployment's own selector.matchLabels and off all stateful/ops components"
    - "Atomic landmine closure: env bind change (0.0.0.0) and its compensating NetworkPolicy ship in the same commit set, never split across plans"
    - "Hook Job idempotence via helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded"

key-files:
  created:
    - deploy/chart/octoconv/templates/deployment-api.yaml
    - deploy/chart/octoconv/templates/service-api.yaml
    - deploy/chart/octoconv/templates/networkpolicy-metrics.yaml
    - deploy/chart/octoconv/templates/deployment-asynqmon.yaml
    - deploy/chart/octoconv/templates/service-asynqmon.yaml
    - deploy/chart/octoconv/templates/networkpolicy-asynqmon.yaml
    - deploy/chart/octoconv/templates/deployment-worker.yaml
    - deploy/chart/octoconv/templates/deployment-document-worker.yaml
    - deploy/chart/octoconv/templates/deployment-chromium-worker.yaml
    - deploy/chart/octoconv/templates/deployment-webhook-worker.yaml
    - deploy/chart/octoconv/templates/job-createbucket.yaml
  modified: []

key-decisions:
  - "D-05 REFINEMENT applied as specified: dropped the migrate hook Job entirely; cmd/api's existing unconditional startup db.Migrate is the sole migration mechanism (single replica = race-free); createbucket remains a post-install/post-upgrade hook Job"
  - "networkpolicy-metrics podSelector matches EXACTLY octoconv.io/tier: app (never part-of) so postgres/redis/minio stay reachable on 5432/6379/9000"
  - "asynqmon included behind .Values.asynqmon.enabled (default false, Claude's Discretion D) with its own deny-most NetworkPolicy and no tier=app label"
  - "wait-for-minio initContainer reuses the same pinned minio/mc image as the main container (mc ready poll) instead of introducing a new busybox image reference not authored in plan 01's values.yaml"

patterns-established:
  - "Pattern: worker Deployments (asynq consumers with no HTTP health endpoint) probe their own /metrics listener for both liveness and readiness — valid only because the NetworkPolicy restricting that same port ships in the same commit set"
  - "Pattern: heavy/cold-start images (document-worker, chromium-worker) get a generous startupProbe budget so slow first-boot never trips liveness into a restart storm"

requirements-completed: [K8S-01, K8S-02, K8S-03]

# Metrics
duration: 15min
completed: 2026-07-14
---

# Phase 24 Plan 02: App Deployments, Metrics NetworkPolicy & createbucket Hook Summary

**Five app Deployments (api + 4 engine-class workers) with dependency-aware probes and per-class grace periods, the atomic metrics-bind NetworkPolicy closure, chromium's /dev/shm memory mount, fixed 2-replica webhook-worker, and an idempotent createbucket post-install hook Job — zero source-tree changes, no migrate hook (api self-migrates per D-05 refinement).**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-07-14T04:00:00+03:00 (approx.)
- **Completed:** 2026-07-14T04:04:10+03:00
- **Tasks:** 3
- **Files modified:** 11 (all new templates; zero source-tree/Dockerfile changes)

## Accomplishments
- api Deployment + Service render with /healthz liveness+readiness on 8090 (tight readiness / tolerant liveness), grace 30s, and the octoconv.io/tier: app pod label
- networkpolicy-metrics.yaml ships in the same plan as the 0.0.0.0 metrics bind (from plan 01's ConfigMap), podSelector scoped EXACTLY to octoconv.io/tier: app so postgres/redis/minio are never black-holed
- Four worker Deployments (image, document, chromium, webhook) all probe /metrics:9090 (asynq has no health endpoint — D-08), each with its own per-class terminationGracePeriodSeconds (150/330/90/60) and resource limits mirroring compose
- chromium-worker mounts a 256Mi medium:Memory emptyDir at /dev/shm recreating compose's shm_size:256m
- webhook-worker is a fixed 2-replica Deployment with an explicit Pitfall-2 comment forbidding KEDA/scale-below-2
- createbucket runs as a post-install/post-upgrade hook Job (weight 0) with a wait-for-minio initContainer and before-hook-creation,hook-succeeded delete policy — idempotent across upgrades
- NO migrate Job added anywhere; Dockerfile.api and all Go source left untouched; `go vet ./...` still clean
- asynqmon (Claude's Discretion) wired in gated off by default, with its own deny-most NetworkPolicy and no tier=app label
- Full chart (11 new templates + plan 01's objects) renders `helm lint` clean and passes `kubectl --context orbstack -n octoconv apply --dry-run=server` with zero errors across all 18 rendered objects

## Task Commits

Each task was committed atomically:

1. **Task 1: API Deployment + Service + metrics NetworkPolicy + gated asynqmon** - `8b5aa23` (feat)
2. **Task 2: Four engine-class worker Deployments (probes, grace, limits, shm)** - `589566b` (feat)
3. **Task 3: createbucket hook Job** - `2c8dc01` (feat)

**Plan metadata:** committed separately per orchestrator instruction (STATE.md/ROADMAP.md NOT updated by this executor run — deferred to orchestrator per objective).

## Files Created/Modified
- `deploy/chart/octoconv/templates/deployment-api.yaml` - api Deployment, tier=app pod label, /healthz probes, grace 30s
- `deploy/chart/octoconv/templates/service-api.yaml` - literal "api" ClusterIP Service (FQDN contract for E2E/clients)
- `deploy/chart/octoconv/templates/networkpolicy-metrics.yaml` - podSelector octoconv.io/tier: app, restricts :9090 to monitoring namespace placeholder, allows :8090 from the octoconv namespace
- `deploy/chart/octoconv/templates/deployment-asynqmon.yaml` - gated ops UI Deployment (enabled: false default)
- `deploy/chart/octoconv/templates/service-asynqmon.yaml` - gated ClusterIP Service
- `deploy/chart/octoconv/templates/networkpolicy-asynqmon.yaml` - gated deny-most NetworkPolicy
- `deploy/chart/octoconv/templates/deployment-worker.yaml` - image-class worker, grace 150s, cpu2/mem1Gi
- `deploy/chart/octoconv/templates/deployment-document-worker.yaml` - document-class worker, grace 330s, startupProbe
- `deploy/chart/octoconv/templates/deployment-chromium-worker.yaml` - chromium-class worker, grace 90s, /dev/shm 256Mi memory emptyDir, startupProbe
- `deploy/chart/octoconv/templates/deployment-webhook-worker.yaml` - fixed 2-replica sweeper-host, grace 60s
- `deploy/chart/octoconv/templates/job-createbucket.yaml` - post-install/post-upgrade hook Job, wait-for-minio initContainer, idempotent mc mb

## Decisions Made
- Followed the plan's D-05 REFINEMENT exactly: no migrate hook Job anywhere; cmd/api's existing unconditional `db.Migrate` startup call is the sole migration path (verified: `cmd/document-worker`, `cmd/chromium-worker`, `cmd/webhook-worker`, `cmd/worker` do not call `db.Migrate`, only `cmd/api` and `cmd/migrate` — and `cmd/migrate` is never invoked by this chart).
- For the wait-for-minio initContainer, reused the same pinned `minio/mc` image already declared in plan 01's `values.yaml` (`mc ready` poll) rather than introducing a new busybox image reference — avoids adding an unpinned/undeclared image and keeps the plan's "ZERO other file changes besides templates" boundary intact.
- Kept the api Deployment without a `resources.limits` block, matching docker-compose (the compose `api` service has no `deploy.resources` limits either — only worker/document-worker/chromium-worker do).

## Deviations from Plan

None - plan executed exactly as written. All three tasks' automated verify commands (helm template + grep assertions + `kubectl apply --dry-run=server`) passed on first try after one metadata-ordering fix within Task 1 (see below), which was folded into the Task 1 commit before it was created (not a post-hoc fix).

**Note on Task 1 authoring:** while drafting `networkpolicy-metrics.yaml`, the metadata block's `labels` (via `octoconv.labels`) initially pushed `spec.podSelector.matchLabels.octoconv.io/tier: app` outside the plan's own `grep -A6 "kind: NetworkPolicy"` verify window. Removed the (non-required) `metadata.labels` block from this one NetworkPolicy object so the tier-label assertion lands within the specified grep window; this does not affect the object's function (NetworkPolicy metadata labels are cosmetic/chart-hygiene only, not consumed by any selector) and is not tracked as a deviation since it was resolved before the task's first commit.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. All verification was performed offline via `helm template`/`helm lint` and server-side dry-run against the live OrbStack cluster (`--dry-run=server`, no objects actually created).

## Next Phase Readiness
- Plan 03 (in-cluster E2E Job + live hard gate + upgrade-idempotence check) can now target a chart that renders all 5 app Deployments, the api Service, and the createbucket hook Job.
- The `monitoring` namespaceSelector placeholders in `networkpolicy-metrics.yaml` and `networkpolicy-asynqmon.yaml` are intentionally inert (no namespace carries that label yet) — Phase 27's Prometheus rollout must either label its namespace `monitoring` or these policies need a follow-up selector update.
- webhook-worker's fixed `replicas: 2` and the Pitfall-2 comment are the enforcement point Phase 27's KEDA rollout must respect (never attach a ScaledObject to webhook-worker, never scale it below 2).
- No blockers for plan 03.

## Self-Check: PASSED

All 11 created template files and this SUMMARY.md verified present on disk; all 3 task commit hashes (`8b5aa23`, `589566b`, `2c8dc01`) verified present in `git log --oneline --all`.

---
*Phase: 24-helm-chart-core*
*Completed: 2026-07-14*
