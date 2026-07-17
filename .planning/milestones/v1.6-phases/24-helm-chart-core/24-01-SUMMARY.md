---
phase: 24-helm-chart-core
plan: 01
subsystem: infra
tags: [helm, kubernetes, orbstack, postgres, redis, minio, values-contract]

# Dependency graph
requires: []
provides:
  - "deploy/chart/octoconv single flat Helm chart (D-01) with Chart.yaml/.helmignore"
  - "Complete values.yaml contract (api/worker/documentWorker/chromiumWorker/webhookWorker/asynqmon + postgres/redis/minio/e2e) that plans 02/03 consume without editing"
  - "values-local.yaml committed dev-cred overlay mirroring docker-compose's trust model (D-03)"
  - "_helpers.tpl: octoconv.fullname/labels/selectorLabels/commonEnv, with the tier-label boundary (no octoconv.io/tier on shared helpers)"
  - "octoconv-config ConfigMap + octoconv-secret Secret with the full DEBT-05 env surface, FQDN S3_ENDPOINT (D-06), and 0.0.0.0 METRICS_ADDR (D-04)"
  - "Postgres/Redis/MinIO Deployment+Service(+PVC) templates, lint-clean and server-dry-run-clean against live OrbStack"
affects: ["24-02 (app Deployments/NetworkPolicy)", "24-03 (E2E overlay/live install)"]

# Tech tracking
tech-stack:
  added: ["Helm v4 chart (deploy/chart/octoconv)"]
  patterns:
    - "Interface-first values.yaml: full contract authored in plan 01, downstream plans only ADD templates"
    - "Tier-label boundary: octoconv.labels (part-of=octoconv) shared across ALL objects; octoconv.io/tier: app reserved exclusively for app Deployments (plan 02) to scope the metrics NetworkPolicy podSelector"
    - "Literal Service names (postgres/redis/minio) instead of fullname-prefixed, so FQDN env values resolve exactly"

key-files:
  created:
    - deploy/chart/octoconv/Chart.yaml
    - deploy/chart/octoconv/.helmignore
    - deploy/chart/octoconv/values.yaml
    - deploy/chart/octoconv/values-local.yaml
    - deploy/chart/octoconv/templates/_helpers.tpl
    - deploy/chart/octoconv/templates/NOTES.txt
    - deploy/chart/octoconv/templates/configmap.yaml
    - deploy/chart/octoconv/templates/secret.yaml
    - deploy/chart/octoconv/templates/postgres.yaml
    - deploy/chart/octoconv/templates/redis.yaml
    - deploy/chart/octoconv/templates/minio.yaml
  modified: []

key-decisions:
  - "S3_ENDPOINT in the ConfigMap is the FQDN minio.octoconv.svc.cluster.local:9000, not the compose-style bare minio:9000 (D-06)"
  - "METRICS_ADDR is 0.0.0.0:9090 (not 127.0.0.1) since each binary runs in its own pod with no shared loopback (D-04)"
  - "MinIO repinned from :latest to concrete RELEASE.2025-09-07T16-13-09Z / RELEASE.2025-08-13T08-35-41Z tags (D-02); grep-gate confirms zero :latest anywhere in the render"
  - "octoconv.io/tier: app deliberately omitted from postgres/redis/minio pod templates — reserved for plan 02's 5 app Deployments so the future metrics NetworkPolicy podSelector doesn't accidentally match the stateful backbone"
  - "DATABASE_URL constructed in secret.yaml from .Values.postgres.user/.Values.postgres.db + .Values.secrets.postgresPassword rather than a single opaque string, so it stays consistent with the postgres.yaml Service/PVC values"
  - "MinIO readiness/liveness use the HTTP /minio/health/ready endpoint instead of compose's `mc ready local` exec, because mc is not bundled in the minio/minio image"

requirements-completed: [K8S-01, K8S-02]

# Metrics
duration: 8min
completed: 2026-07-14
---

# Phase 24 Plan 01: Helm Chart Skeleton, Values Contract & Stateful Backbone Summary

**Single flat Helm chart (`deploy/chart/octoconv`) with a complete interface-first `values.yaml`, dev-cred `values-local.yaml`, shared config/secret templates carrying the FQDN `S3_ENDPOINT`/`0.0.0.0` `METRICS_ADDR` landmine fixes, and hand-rolled Postgres/Redis/MinIO Deployments — lint-clean and accepted by a live OrbStack `kubectl apply --dry-run=server`.**

## Performance

- **Duration:** ~8 min
- **Started:** 2026-07-14T00:52:03Z
- **Completed:** 2026-07-14T00:57:30Z
- **Tasks:** 3 (Task 3 was validation-only, no file changes needed)
- **Files modified:** 11 created

## Accomplishments
- Authored the complete `values.yaml` contract (global/image/metrics/s3 blocks + all 5 app-service blocks + postgres/redis/minio/e2e) so plans 02/03 never need to edit this file
- Ported the docker-compose env surface into `octoconv-config`/`octoconv-secret`, baking in the two SEED-004 landmine fixes: FQDN `S3_ENDPOINT` (D-06) and `0.0.0.0` `METRICS_ADDR` (D-04)
- Hand-rolled Postgres/Redis/MinIO Deployment+Service(+PVC) templates with compose-parity mount paths and probes, repinning MinIO off `:latest` (D-02)
- Established the tier-label boundary in `_helpers.tpl` and enforced it (grep-gate: 0 occurrences of `octoconv.io/tier: app`) so plan 02's metrics NetworkPolicy will scope correctly
- Validated the full render against the live OrbStack cluster with `kubectl apply --dry-run=server` — all 10 objects (Secret, ConfigMap, 2 PVCs, 3 Services, 3 Deployments) accepted with zero errors

## Task Commits

Each task was committed atomically:

1. **Task 1: Chart scaffold + full values contract + shared config/secret** - `8c7a97b` (feat)
2. **Task 2: Stateful dependency templates (postgres, redis, minio)** - `2c75221` (feat)
3. **Task 3: Offline + server-dry-run gate** - no commit (validation-only; NOTES.txt required no refinement, render was already clean)

## Files Created/Modified
- `deploy/chart/octoconv/Chart.yaml` - Chart metadata (apiVersion v2, name octoconv, version 0.1.0, appVersion 1.6)
- `deploy/chart/octoconv/.helmignore` - Standard ignores; values-local.yaml intentionally NOT ignored
- `deploy/chart/octoconv/values.yaml` - Complete per-service values contract for the whole phase
- `deploy/chart/octoconv/values-local.yaml` - Committed dev credentials (DEV-ONLY, mirrors compose trust model)
- `deploy/chart/octoconv/templates/_helpers.tpl` - fullname/labels/selectorLabels/commonEnv helpers
- `deploy/chart/octoconv/templates/NOTES.txt` - Post-install notes (in-cluster API URL, /metrics scoping caveat, pod-status command)
- `deploy/chart/octoconv/templates/configmap.yaml` - `octoconv-config`: full non-secret env contract (DEBT-05 surface)
- `deploy/chart/octoconv/templates/secret.yaml` - `octoconv-secret`: DATABASE_URL/REDIS_ADDR/API_KEY_SALT/WEBHOOK_SIGNING_SECRET/S3 creds/POSTGRES_PASSWORD, all from `.Values.secrets.*`
- `deploy/chart/octoconv/templates/postgres.yaml` - Postgres PVC+Deployment+Service (compose-parity mount, pg_isready probes)
- `deploy/chart/octoconv/templates/redis.yaml` - Redis Deployment+Service (redis-cli ping probes, no PVC)
- `deploy/chart/octoconv/templates/minio.yaml` - MinIO PVC+Deployment+Service (concrete RELEASE tag, HTTP health probe)

## Decisions Made
- Constructed `DATABASE_URL` in `secret.yaml` from `.Values.postgres.user`/`.Values.postgres.db` + `.Values.secrets.postgresPassword` (templated, not a single opaque string) to keep it in lockstep with `postgres.yaml`'s own values usage — reduces risk of drift between the Service/PVC config and the connection string.
- Used HTTP `/minio/health/ready` for MinIO probes instead of the compose `mc ready local` exec, since `mc` is not bundled in the `minio/minio` image (only in the separate `minio/mc` image referenced by `.Values.minio.mcImage` for the future createbucket hook in plan 02).
- Left the OrbStack `octoconv` namespace created (from the dry-run gate) rather than deleting it — cheap, and plan 02/03 will need it again; no live workloads were created (server dry-run only, nothing persisted).

## Deviations from Plan

None - plan executed exactly as written. All `must_haves.truths`, `artifacts`, and `key_links` from the plan frontmatter were satisfied without needing Rule 1-4 fixes.

## Issues Encountered

Initial `Write` calls targeted the shared main-repo path (`/Users/apaderin/dev/octoconv/deploy/...`) instead of this agent's worktree path and were correctly rejected by the tool's isolation guard. Recovered immediately by resolving `git rev-parse --show-toplevel` inside the worktree and using that as the base for every subsequent absolute path — no files were written to the wrong location.

## User Setup Required

None - no external service configuration required. This plan only touched chart YAML; no Go code, no CI, no runtime env changes to the existing compose stack.

## Next Phase Readiness

- `deploy/chart/octoconv/values.yaml` is the complete, stable contract plan 02 will consume for the 5 app Deployments (api/worker/documentWorker/chromiumWorker/webhookWorker) and asynqmon, plus plan 03's e2e overlay — no further edits to `values.yaml` should be needed.
- The tier-label boundary is in place and gate-verified (0 occurrences of `octoconv.io/tier: app`), so plan 02 can safely add that label to its 5 app Deployments and scope the metrics NetworkPolicy podSelector to it without touching this plan's templates.
- `octoconv-config`/`octoconv-secret` are ready to be referenced via `octoconv.commonEnv` (`_helpers.tpl`) by every app Deployment in plan 02.
- Live OrbStack cluster confirmed reachable and accepting server-side dry-run applies; no blockers for plan 02/03's further validation gates.

---
*Phase: 24-helm-chart-core*
*Completed: 2026-07-14*

## Self-Check: PASSED

All 11 created chart files and the SUMMARY.md itself verified present on disk; both task commits (`8c7a97b`, `2c75221`) verified present in `git log --oneline --all`. No missing items.
