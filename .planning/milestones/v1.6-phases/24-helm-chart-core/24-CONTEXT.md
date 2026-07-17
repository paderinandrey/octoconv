# Phase 24: Helm Chart Core & Landmine Closure - Context

**Gathered:** 2026-07-14
**Status:** Ready for planning
**Source:** v1.6 research (STACK/FEATURES/ARCHITECTURE/PITFALLS + synthesis), user-approved roadmap

<domain>
## Phase Boundary

`deploy/chart/octoconv` Helm chart deploying the full existing stack (api, worker, document-worker, chromium-worker, webhook-worker×2 + Postgres/Redis/MinIO + migrate/createbucket Jobs) on OrbStack k8s, with all four SEED-004 landmines closed and the E2E suite passing as an in-cluster Job. NO KEDA (Phase 27), NO mcp-http (joins the chart in Phase 25 — K8S-01's mcp-http mention is satisfied incrementally). Expected Go-code diff: ZERO (values/env-only changes) except a possible tiny e2e helper if the Downward API wiring needs it.

</domain>

<decisions>
## Implementation Decisions

### Chart shape
- D-01: Single chart `deploy/chart/octoconv`, per-service template files, NO subcharts/umbrella (STACK rec); Helm version — verify live (v4 GA per research; use what's installed)
- D-02: Postgres/Redis/MinIO as our own minimal StatefulSets + Services using the SAME pinned images as compose; REPIN `minio/minio:latest` and `minio/mc:latest` to current concrete tags (OrbStack re-pulls :latest — landmine); PVCs for postgres/minio
- D-03: values.yaml: per-service blocks mirroring the compose env contract; dev credentials committed in values-local.yaml exactly like compose (same internal trust model — flagged, not a leak regression); global image tag + imagePullPolicy: IfNotPresent (OrbStack shares the docker image store — no registry)

### Landmines (K8S-02)
- D-04: METRICS_ADDR=0.0.0.0:9090 via env in values ONLY (all five binaries read it via os.Getenv — zero code change) + a NetworkPolicy restricting /metrics ingress to prometheus/keda namespaces (compensates the lost 127.0.0.1 boundary)
- D-05 (REFINED at planning, checker-verified): migrate via cmd/api self-migration (existing proven behavior — only api calls db.Migrate; single replica = race-free; a separate hook Job would RACE it against unlocked db.Migrate); createbucket as post-install/post-upgrade hook Job (mc mb --ignore-existing — idempotent); app Deployments get initContainers or readiness-dependent startup ordering only where strictly needed (postgres/redis reachability via probes+restarts is acceptable k8s-native)
- D-06: S3_ENDPOINT=minio.<namespace>.svc.cluster.local:9000 (FQDN — presigned URLs resolve from pods AND from the OrbStack host per research; no ingress needed)
- D-07: In-cluster E2E: the suite runs as a k8s Job from a test image; E2E_WEBHOOK_HOST = the Job pod's own IP via Downward API (status.podIP env) — receiver already binds 0.0.0.0; NO S3 dial redirect needed (FQDN resolves in-cluster). Test image: a new Dockerfile.e2e (golang base + repo source, runs go test ./internal/e2e/) built locally like other images

### Probes & lifecycle (K8S-03)
- D-08: api: liveness+readiness = GET /healthz; workers: liveness = HTTP GET /metrics on the 0.0.0.0 metrics port (process-alive signal; asynq has no health endpoint — metric-port probe is the accepted proxy); readiness same
- D-09: terminationGracePeriodSeconds per class: document-worker 330, chromium-worker 90, worker(image) 150, webhook-worker 60, api 30 (≥ engine timeout + margin)
- D-10: chromium-worker keeps shm requirements (emptyDir medium:Memory sizeLimit 256Mi volume at /dev/shm); document-worker/chromium keep resource limits mirroring compose; document-worker amd64 note: OrbStack runs amd64 images via Rosetta — keep the image as-is, no nodeSelector needed locally

### Operational discipline (PITFALLS — OrbStack history)
- D-11: Sequential image pre-build (docker build one at a time) BEFORE helm install; compose stack MUST be stopped before the k8s stack goes hot (never both); docker builder prune between if disk pressure
- D-12: LIVE HARD GATE (unconditional): helm install → kubectl wait all pods Ready (bounded) → in-cluster E2E Job exit 0 → helm upgrade idempotence check (hooks re-run safely) → teardown helm uninstall (PVCs may remain). If OrbStack k8s is not enabled: `orb start k8s`/enable via CLI first — verify availability as step zero, loud-fail if unavailable

### Claude's Discretion
- Template helpers layout (_helpers.tpl), label conventions
- Whether asynqmon joins the chart (nice-to-have; if trivial — include behind enabled:false default)
- Exact NetworkPolicy selectors (prometheus lands in Phase 27 — policy may allow a named-namespace placeholder now)

</decisions>

<canonical_refs>
## Canonical References

- `docker-compose.yml` — THE env/healthcheck/resource contract being ported (source of truth for every service block)
- `docker-compose.e2e.yml` — e2e relaxations to port into the E2E Job env
- `internal/e2e/e2e_test.go` (package doc + startWebhookReceiver — binds 0.0.0.0; E2E_WEBHOOK_HOST consumption)
- `cmd/*/main.go` — env reads (METRICS_ADDR et al.)
- `.env.example` — full env surface
- `.planning/research/STACK.md` (versions, OrbStack image-store, bitnami-dead, minimal deps), `ARCHITECTURE.md` (landmine fixes, FQDN, Downward API), `FEATURES.md` (probes, hooks), `PITFALLS.md` (grace periods, OrbStack discipline, chicken-egg — NOT this phase but don't preclude)
- `.planning/seeds/SEED-004.md`

</canonical_refs>

<specifics>
## Specific Ideas

- helm lint + helm template as offline gates; kubeconform/`kubectl apply --dry-run=server` if available
- The e2e Job needs DATABASE_URL/API_KEY_SALT etc. from the same values — one shared env ConfigMap/Secret helper template
- Existing scripts/presets-acceptance.sh etc. stay compose-based — untouched; CI stays compose-based (no chart impact on ci.yml)

</specifics>

<deferred>
## Deferred Ideas

- KEDA (Phase 27), mcp-http in chart (Phase 25), k8s in CI (K8SV2-01), ingress/TLS, multi-env values
</deferred>

---

*Phase: 24-helm-chart-core*
*Context gathered: 2026-07-14*
