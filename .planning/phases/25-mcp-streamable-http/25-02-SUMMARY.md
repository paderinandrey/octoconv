---
phase: 25-mcp-streamable-http
plan: 02
subsystem: infra
tags: [docker, helm, kubernetes, networkpolicy, mcp, chart]

# Dependency graph
requires:
  - phase: 25-mcp-streamable-http
    provides: "cmd/mcp-http binary (25-01) — the container this plan packages and deploys"
provides:
  - "Dockerfile.mcp-http — two-stage build mirroring Dockerfile.api, USER nobody, EXPOSE 8070"
  - "mcpHttp values.yaml block (enabled, image.repository, replicas, addr, baseURL)"
  - "deployment-mcp-http.yaml — gated single-replica Deployment, key-free explicit env, /healthz probes"
  - "service-mcp-http.yaml — ClusterIP Service, literal name mcp-http, port 8070"
  - "networkpolicy-mcp-http.yaml — gated default-deny ingress NetworkPolicy (ROADMAP SC4)"
  - "README MCP-over-HTTP section"
affects: [25-03-mcp-streamable-http-live-gate]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Explicit env: block (not octoconv.commonEnv) for pods that must stay key-free (D-06 zero-privilege boundary)"
    - "Dedicated per-service NetworkPolicy (asynqmon-pattern) for a Service with no built-in auth-at-the-edge, gated by the same enabled flag as its Deployment/Service"

key-files:
  created:
    - Dockerfile.mcp-http
    - deploy/chart/octoconv/templates/deployment-mcp-http.yaml
    - deploy/chart/octoconv/templates/service-mcp-http.yaml
    - deploy/chart/octoconv/templates/networkpolicy-mcp-http.yaml
  modified:
    - deploy/chart/octoconv/values.yaml
    - README.md

key-decisions:
  - "mcp-http Deployment carries NO octoconv.io/tier: app label — it exposes no /metrics this plan, so it must stay outside the metrics NetworkPolicy's podSelector; it gets its own dedicated networkpolicy-mcp-http.yaml instead"
  - "Service targetPort references the named container port (http) rather than the numeric 8070, consistent with the plan's explicit instruction"
  - "mcpHttp.enabled defaults to true in the committed values.yaml so a plain helm install brings the pod up (D-05)"

patterns-established:
  - "Per-service NetworkPolicy pattern (podSelector on component label, namespaceSelector allow from release namespace, single port) reusable for any future Service without in-app auth-at-the-edge"

requirements-completed: [MCPH-01]

# Metrics
duration: 16min
completed: 2026-07-14
---

# Phase 25 Plan 02: mcp-http Container + Chart Deployment Summary

**Dockerfile.mcp-http + gated single-replica Deployment/ClusterIP Service/NetworkPolicy for cmd/mcp-http, wired with key-free env (OCTOCONV_BASE_URL + MCP_HTTP_ADDR only) and verified entirely offline (helm lint, template gating, server dry-run).**

## Performance

- **Duration:** ~16 min
- **Started:** 2026-07-14T06:52:00Z (base commit)
- **Completed:** 2026-07-14T07:07:28+03:00
- **Tasks:** 2 completed
- **Files modified:** 6 (2 created in Task 1, 4 created/modified in Task 2)

## Accomplishments
- `Dockerfile.mcp-http` builds a working image (`octoconv-mcp-http:planlint`, verified with a real `docker build`) mirroring `Dockerfile.api`'s multi-stage shape exactly: `golang:1.26-bookworm` build → `debian:bookworm-slim` runtime, `ca-certificates` only, `USER nobody`, `EXPOSE 8070`.
- `mcpHttp` values block added to `values.yaml` (the sanctioned single edit for this plan, per 25-CONTEXT existing-behavior note): `enabled: true`, `image.repository`, `replicas: 1`, `addr`, `baseURL` (FQDN to the `api` Service).
- Gated chart templates (`{{- if .Values.mcpHttp.enabled }}`) for Deployment, Service, and NetworkPolicy — all three render together and vanish together.
- Deployment uses an explicit `env:` block (`OCTOCONV_BASE_URL`, `MCP_HTTP_ADDR`) instead of `octoconv.commonEnv`, so the pod never receives `DATABASE_URL`/`secretRef`/any API key (D-06). Verified by grep against the rendered template.
- Service pinned to the literal name `mcp-http` (not fullname-prefixed) so Plan 03 can `kubectl port-forward svc/mcp-http` and in-cluster callers can resolve `mcp-http.octoconv.svc.cluster.local:8070`.
- `networkpolicy-mcp-http.yaml` (ROADMAP SC4) mirrors `networkpolicy-asynqmon.yaml`'s per-service shape: default-deny all ingress to the mcp-http pod except TCP `:8070` from a `namespaceSelector` matching the release namespace. Carries the same OrbStack-unenforced-locally note as `networkpolicy-metrics.yaml` (24-03 precedent) — per-request `Authorization: ApiKey` (Plan 01, D-03) is the always-on runtime control regardless of NetworkPolicy enforcement.
- README gained a "MCP over HTTP (in-cluster)" section documenting transport, per-request auth model, presigned-only result shape (D-04), key-free config, and the `kubectl port-forward` access path.
- Offline gates all green: `helm lint` (with `values-local.yaml` for the required secret values — clean), `helm template --set mcpHttp.enabled=true` (Deployment+Service+NetworkPolicy render, env carries `OCTOCONV_BASE_URL`, no secretRef/DATABASE_URL/API_KEY in the mcp-http block), `helm template --set mcpHttp.enabled=false` (all three absent), and `kubectl --context orbstack -n octoconv apply --dry-run=server` on the full render (every resource including `service/mcp-http`, `deployment.apps/mcp-http`, and `networkpolicy.networking.k8s.io/octoconv-mcp-http` accepted).

## Task Commits

Each task was committed atomically:

1. **Task 1: Dockerfile.mcp-http + mcpHttp values block** - `ee3533a` (feat)
2. **Task 2: mcp-http Deployment + Service templates, README, offline gates** - `79e93e5` (feat)

**Plan metadata:** SUMMARY committed separately per this plan's directive (STATE.md/ROADMAP.md updates deliberately skipped — orchestrator's responsibility).

_Note: no TDD tasks in this plan (infra/deployment plan, not tdd="true")._

## Files Created/Modified
- `Dockerfile.mcp-http` - two-stage build for `cmd/mcp-http`; verified with a real `docker build` (image `octoconv-mcp-http:planlint`, sha `23ef6f5bf7fb`)
- `deploy/chart/octoconv/values.yaml` - added `mcpHttp` block (enabled, image.repository, replicas, addr, baseURL)
- `deploy/chart/octoconv/templates/deployment-mcp-http.yaml` - gated single-replica Deployment, explicit key-free env, `/healthz` probes on 8070
- `deploy/chart/octoconv/templates/service-mcp-http.yaml` - gated ClusterIP Service, literal name `mcp-http`, port 8070 → targetPort `http`
- `deploy/chart/octoconv/templates/networkpolicy-mcp-http.yaml` - gated default-deny ingress NetworkPolicy, allow `:8070` from release namespace (SC4)
- `README.md` - new "MCP over HTTP (in-cluster)" section

## Decisions Made
- Kept the Service `targetPort` as the named port `http` (matching the container port name) rather than the numeric `8070`, per the plan's explicit wording — functionally equivalent to `service-api.yaml`'s numeric targetPort, just named for readability since the container port itself is also named `http`.
- No `octoconv.io/tier: app` label on the mcp-http pod template — mcp-http exposes no `/metrics` this plan (D-05 marks metrics optional/nice-to-have), and adding the tier label would fold it into `networkpolicy-metrics.yaml`'s podSelector, which is scoped to services carrying that label deliberately (see `_helpers.tpl` boundary note). A standalone NetworkPolicy was the correct, plan-directed choice instead.
- `helm lint`/`helm template` gates were run with `-f deploy/chart/octoconv/values-local.yaml` layered on top of `values.yaml` — the committed `values.yaml` alone has no `secrets.*` block (by design; those live in `values-local.yaml`/CI-injected values only), so bare `helm lint` fails on an unrelated pre-existing template (`secret.yaml`) needing `.Values.secrets.postgresPassword`. This is not a regression introduced by this plan; `values-local.yaml` is the chart's documented mechanism for supplying those values, and doing so let the gate actually exercise the mcp-http templates end-to-end.

## Deviations from Plan

None - plan executed exactly as written. The `values-local.yaml` layering for `helm lint`/`helm template` (documented above under Decisions Made) is a verification-tooling detail, not a change to any deliverable, so it is not logged as a Rule 1-4 deviation.

## Issues Encountered
- Bare `helm lint deploy/chart/octoconv` (no extra `-f`) fails on `templates/secret.yaml`'s reference to `.Values.secrets.postgresPassword`, which only exists in `values-local.yaml`. Resolved by running the gate with `-f deploy/chart/octoconv/values-local.yaml`, the chart's existing convention for supplying dev-only secret values (see that file's own header comment). This is pre-existing chart behavior from Phase 24, unrelated to this plan's templates.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- Plan 03 can `docker build -f Dockerfile.mcp-http` (already proven to build), `helm install`/`upgrade` the chart with `mcpHttp.enabled=true` (default), and `kubectl -n octoconv port-forward svc/mcp-http 8070:8070` to drive the live streamable-HTTP MCP session (D-08 live hard gate).
- Image tag/name for the live gate: build and tag as `octoconv-mcp-http:<imageTag>` matching `global.imageTag` in the values used for `helm install` (the plan's own smoke build used tag `planlint`; Plan 03 should build/tag consistently with whatever `global.imageTag` its install command uses, same as the existing api/worker images).
- Port-forward command for Plan 03: `kubectl --context orbstack -n octoconv port-forward svc/mcp-http 8070:8070`.
- No blockers. Compose path (`docker-compose.yml`, Go source) untouched — CI regression gate unaffected, per this plan's `<verification>` clause.

---
*Phase: 25-mcp-streamable-http*
*Completed: 2026-07-14*

## Self-Check: PASSED

All created files verified present on disk; all task commit hashes (`ee3533a`, `79e93e5`) verified present in git history.
