# Stack Research

**Domain:** Kubernetes + KEDA local deployment for an existing Go microservice fleet (OctoConv v1.6)
**Researched:** 2026-07-14
**Confidence:** HIGH for versions/verified facts (Context7 has no entries for these infra tools — used live GitHub API/Helm repo index/official docs directly instead); MEDIUM for architecture/packaging recommendations (opinionated synthesis, not a single canonical source); explicitly flagged where LOW

This is a **replacement** stack note for OctoConv's first infrastructure milestone (v1.6, "Kubernetes & KEDA"). It supersedes the previous milestone's STACK.md content (v1.5 — MCP stdio SDK, veraPDF, mscfb — all already shipped and merged, not revisited here). It does not re-litigate the fixed core application stack (Go 1.26, chi, asynq/Redis, PostgreSQL 18, MinIO — Notion spec, out of scope) or any of the 6 existing service binaries/images. Everything below is net-new tooling for: (1) the Kubernetes distribution/host, (2) Helm packaging, (3) KEDA autoscaling, (4) a minimal in-cluster Prometheus, and (5) the MCP streamable-HTTP transport (already-pinned SDK, confirmed zero version bump needed).

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| KEDA | **v2.20.1** (app + Helm chart, chart repo `https://kedacore.github.io/charts`, chart name `keda`) | Per-engine-class queue-depth autoscaling (image/document/html/webhook) | Current stable per `kedacore/keda` GitHub Releases API (`tag_name: v2.20.1`, published 2026-06-08) and confirmed as the newest entry in the live Helm repo index (`keda-2.20.1.tgz`, `appVersion: 2.20.1`). `v2.21` exists only as "unreleased" in the docs version selector. HIGH confidence (GitHub API + Helm repo index, both live-fetched). |
| Helm | **v3.21.3** (July 9, 2026) or **v4.2.3** (July 9, 2026, GA) | Package/template the whole stack as one chart | Helm v3 and v4 both ship active patch releases on the same cadence; v3's own v3.21.0 release notes state "Helm v3 is approaching end-of-life. Please update to Helm v4." v4 is GA. **Recommendation: use Helm v4** for this new chart (greenfield, no v3-only plugin/CI dependency exists in this repo yet) — avoids starting a migration debt on day one. If any CI runner assumption turns out to require the v3 CLI/API shape, v3.21.3 is still fully supported (security fixes through Nov 2026) and is a safe fallback. HIGH confidence (GitHub releases, live-fetched). |
| OrbStack Kubernetes | Built-in, single-node, bundled Kubernetes has progressed **1.29.3 → 1.31.6 → 1.33.5** across recent OrbStack releases (exact current version not independently confirmed — verify with `kubectl version` at execution time) | Local cluster target (already decided in SEED-004 — not re-litigated here) | Verified: OrbStack's Kubernetes shares the **same container engine/image store as OrbStack's Docker daemon**, so locally-built images are visible to cluster pods with **no registry, no `kind load`-equivalent step**. This is the single biggest gate for the build/deploy loop and is confirmed directly by OrbStack's own Kubernetes docs. **Caveat (confirmed):** if an image tag is `:latest`, Kubernetes will still try to re-pull/update it by default — for local images either tag them non-`:latest` (e.g. `:dev`, `:local`) or set `imagePullPolicy: IfNotPresent`/`Never` on the Pod spec. This directly affects two images already in `docker-compose.yml` that use `:latest` (`minio/minio:latest`, `minio/mc:latest`) — repin or set the pull policy explicitly when porting. HIGH confidence (official OrbStack docs, live-fetched). |

### Supporting Infrastructure (stateful dependencies: Postgres, Redis, MinIO)

| Component | Recommendation | Version (match existing compose) | Why |
|-----------|-----------------|-----------------------------------|-----|
| PostgreSQL | Hand-rolled minimal `StatefulSet` + `PersistentVolumeClaim` + `Service`, own templates in the chart | `postgres:18` (same image already pinned in `docker-compose.yml`) | See "What NOT to use" below — do **not** pull in a Bitnami chart. A single-replica local-validation Postgres needs no operator (CloudNativePG/Zalando are overkill for infra-validation scope, not HA). A short StatefulSet template mirroring the existing compose healthcheck (`pg_isready -U octo -d octo_db`) as `readinessProbe`/`livenessProbe` is lower risk than adopting a third-party chart's opinions about ConfigMap/Secret shape or bundled extensions. |
| Redis | Hand-rolled minimal `StatefulSet` (or `Deployment` + PVC, since asynq treats Redis purely as transient broker state, not source of truth — Postgres is) + `Service` | `redis:8` (same as compose) | Same rationale as Postgres — avoid Bitnami. Official `redis:8` image + `redis-cli ping` readiness probe (mirrors existing compose healthcheck exactly). No Sentinel/Cluster needed for single-node local validation. |
| MinIO | Hand-rolled minimal `StatefulSet`/`Deployment` + `PVC` + `Service`, running the **same** `server /data --console-address ":9001"` command already in compose | `minio/minio:latest` → **repin to a dated release tag** before porting (both for supply-chain pinning hygiene already practiced elsewhere in this repo — `asynqmon:0.7.2` is explicitly pinned per a documented rationale — and to sidestep OrbStack's `:latest` re-pull caveat above) | Verified: MinIO's own **standalone Helm chart is community-support only** (not the officially recommended path); MinIO's own recommendation is the `minio/operator` + Tenant CRDs, built for multi-tenant/multi-drive production object storage — disproportionate for one local bucket. The `minio/charts` repo was briefly removed and later restored, another signal of churn/ambiguity in what "official" even means here. **Recommendation: skip both the community chart and the Operator; hand-roll the manifest**, matching the same packaging pattern used for Postgres/Redis — one consistent approach across all three stateful deps, zero new third-party chart maintenance-risk surface. MEDIUM confidence (GitHub discussions + artifacthub, live-searched; MinIO does not publish one single unambiguous "current state" page). |
| `createbucket` (mc-based one-shot) | Kubernetes `Job` with a `helm.sh/hook: post-install,post-upgrade` annotation (ordered via `helm.sh/hook-weight` after MinIO becomes ready), or a plain `Job` whose own container polls MinIO's health endpoint before running `mc mb` | n/a | Matches SEED-004 landmine #3. Helm hooks are the idiomatic way to express "run once, after X is ready" — compose's `depends_on: condition: service_healthy` has no direct one-line Kubernetes equivalent; probes give liveness/readiness on long-running objects, but cross-object ordering for one-shot Jobs still needs either hooks or a manual polling initContainer. Use the same hook pattern for `cmd/migrate`. |

### What NOT to use for Postgres/Redis/MinIO

| Avoid | Why | Confirmed state (2026-07) |
|-------|-----|----------------------------|
| Bitnami charts (`bitnami/postgresql`, `bitnami/redis`, any `bitnami/*`) | Broadcom's Aug 28, 2025 catalog change moved all **non-`:latest`** image tags — and, from Sep 29, 2025, most packaged chart OCI artifacts — behind a paid subscription (reported figures range roughly $6k/mo to $72k/yr). The "Bitnami Legacy" archive holding older/pinned tags is frozen (no further updates or security patches). Chart *source* on GitHub is still Apache-2.0, but the images the chart deploys by default are the part now paywalled or forced onto a floating `:latest` tag. This directly conflicts with this repo's existing "pin every image, no `:latest` in production paths" convention (see the `asynqmon:0.7.2` pin rationale in `docker-compose.yml`). **Do not adopt any Bitnami chart for this milestone.** HIGH confidence (`bitnami/charts` GitHub issue #35164 + Broadcom's own migration guidance, corroborated by three independent secondary write-ups — Chainguard, Minimus, Chkk). |
| MinIO Operator + Tenant CRDs | Correct long-term production path per MinIO's own docs, but adds an operator Deployment, several CRDs, and a Tenant resource for what is one local bucket in one infra-validation milestone — disproportionate. Revisit only if a real target platform later needs multi-drive/multi-tenant MinIO. | MEDIUM confidence |
| `kube-prometheus-stack` (full Prometheus Operator + Alertmanager + Grafana + node-exporter + kube-state-metrics) | KEDA's Prometheus scaler needs exactly one thing: an HTTP endpoint that answers PromQL-shaped queries at `serverAddress`. `kube-prometheus-stack` installs 15+ CRDs and several extra components purely to obtain that one Prometheus server — disproportionate for scraping 6 first-party pods' existing `/metrics` endpoints on a single-node local cluster. | Own architectural judgment (MEDIUM confidence) — see the minimal Prometheus recommendation below. |
| KEDA's Redis Lists scaler (`type: redis`, `listName`) as the *primary* per-queue-depth trigger | Asynq's pending-task Redis key **is** confirmed to be a plain Redis LIST (verified directly from `hibiken/asynq` source: `internal/base/base.go` defines `PendingKey(qname) = "asynq:{" + qname + "}:pending"`; `internal/rdb/rdb.go`'s enqueue Lua script does `LPUSH` into it, and dequeue uses `RPOPLPUSH` to move entries into the active list) — so a `redis-lists` KEDA trigger with `listName: "asynq:{image}:pending"` is *technically wireable* (note: the literal `{`/`}` braces are part of the real key, present for Redis-Cluster hash-tag compatibility, and easy to get wrong). But: (a) it reflects only the `pending` state, undercounting true backlog when a queue has in-flight retries (`asynq:{queue}:retry`, a ZSET) or scheduled/delayed tasks (also a ZSET); (b) it depends on an **undocumented internal implementation detail** of asynq with no semver-guaranteed contract across versions; (c) OctoConv **already exports** a purpose-built `queue_depth` Prometheus metric that presumably already accounts for the states that matter operationally. The Prometheus scaler against that existing metric is strictly better — semantically correct, versioned by first-party code, and matches this milestone's own stated design intent (SEED-004: "не redis-scaler по внутренностям asynq"). **Use the Redis Lists scaler only as a debugging/cross-check tool, never as the production trigger.** HIGH confidence on the Redis key structure (live-fetched from `hibiken/asynq` source); MEDIUM-HIGH confidence on the recommendation (matches the project's own prior decision rather than an independently re-derived conclusion). |

## KEDA: Scaler Configuration Shape (Prometheus, recommended trigger type)

Confirmed field list for the `prometheus` scaler trigger metadata (KEDA docs v2.20, live-fetched):

| Field | Required | Default | Meaning |
|-------|----------|---------|---------|
| `serverAddress` | Yes | — | URL of the Prometheus HTTP API, e.g. `http://prometheus.octoconv.svc.cluster.local:9090` (in-cluster DNS form `<svc>.<ns>.svc.cluster.local` — works when Prometheus and KEDA are co-located) |
| `query` | Yes | — | PromQL query that must resolve to a single scalar/vector value, e.g. `sum(octoconv_queue_depth{queue="image"})` (confirm the exact metric/label names against your live `/metrics` output — not independently verified here) |
| `threshold` | Yes | — | Value at which KEDA scales from 1→N (post-activation; the standard Kubernetes HPA takes over from here using this as the target metric value) |
| `activationThreshold` | No | `0` | Threshold that decides the 0→1 transition (the scale-*from*-zero "wake up" gate) — set above steady-state noise so a near-empty queue doesn't keep a deployment permanently alive |
| `namespace` | No (required for HA setups, e.g. Thanos) | — | Namespace label for HA-federated Prometheus |
| `customHeaders` | No | — | Comma-separated static headers (`X-Client-Id=cid,X-Tenant-Id=tid`) — use `TriggerAuthentication` instead for anything secret |
| `unsafeSsl` | No | `false` | Skip TLS cert verification — irrelevant for an in-cluster plain-HTTP Prometheus |
| `ignoreNullValues` | No | `true` | Whether a lost/empty Prometheus target is treated as an error vs. silently ignored |
| `timeout` | No | KEDA's global HTTP client default | Per-scaler query timeout |

**Two-phase scaling behavior (confirmed, KEDA docs v2.20):**
- **Activation phase (0↔1 replicas):** KEDA's own polling loop (`pollingInterval`, chart/spec default **30s**) evaluates the trigger while replicas = 0. Once `activationThreshold` is crossed, KEDA scales the target toward `minReplicaCount`.
- **Scaling phase (1↔N replicas):** control passes to the standard Kubernetes HPA object (created/owned by KEDA), which re-evaluates on its own sync loop (cluster default ~15s) and scales toward `maxReplicaCount` (spec default `100` — **override this per engine-class ScaledObject**, e.g. small numbers like 2–5 for local validation) using `threshold` as the target value.
- **Scale-to-zero cooldown:** `cooldownPeriod` (default **300s / 5 min**) applies **only** to the scale-*to-zero* transition once the trigger goes inactive — it does not gate 1→N or N→1 scaling, which is pure HPA behavior. For a demoable 0→N→0 load test, plan to tune `cooldownPeriod` down (e.g. 30–60s) so the "→0" leg doesn't take 5 minutes per queue during the demo.
- `minReplicaCount: 0` is what enables true scale-to-zero. A separate `idleReplicaCount` field exists for an intermediate non-zero "idle" floor but has documented HPA-controller limitations — not needed here, don't reach for it.

**Example shape (illustrative only — confirm the real `octoconv_queue_depth`-style metric/label names against the actual `/metrics` output before writing the real ScaledObject):**
```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: image-worker-scaledobject
spec:
  scaleTargetRef:
    name: image-worker
  minReplicaCount: 0
  maxReplicaCount: 5
  cooldownPeriod: 60
  pollingInterval: 15
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://prometheus.octoconv.svc.cluster.local:9090
        query: sum(octoconv_queue_depth{queue="image"})
        threshold: "5"
        activationThreshold: "1"
```

## Minimal Prometheus Footprint for KEDA

**Recommendation (own architectural judgment, MEDIUM confidence — no single canonical source names this exact minimal shape, but it's the consistent pattern across everything read):** do **not** install `kube-prometheus-stack`. Instead:
- A single `Deployment` running the official `prom/prometheus` image (pin an explicit release tag, not `:latest`, matching this repo's existing pinning convention).
- A `ConfigMap` holding a static (or `kubernetes_sd_configs`-based, for auto-discovery across Pods) `prometheus.yml` that scrapes the existing `/metrics` path on each of the 6 first-party Pods (api, worker, document-worker, chromium-worker, webhook-worker×2). This is exactly the fix for SEED-004 landmine #1 (`METRICS_ADDR` must bind `0.0.0.0` in-cluster, gated by `NetworkPolicy` rather than the current localhost-bind isolation the compose deployment relies on).
- No Alertmanager, no Grafana, no node-exporter, no kube-state-metrics, no Prometheus Operator CRDs (`ServiceMonitor`/`PodMonitor`) — all of those add real value at larger scale but are unnecessary machinery when the actual requirement is "KEDA needs one PromQL-answering URL."
- If `ServiceMonitor`-style discovery is later wanted, the **Prometheus Operator alone** (without the full `kube-prometheus-stack` bundle) is a smaller middle ground — not needed for this milestone's stated scope.

## MCP Streamable HTTP Transport (go-sdk, already pinned — confirm zero version bump)

Confirmed against `github.com/modelcontextprotocol/go-sdk@v1.6.1` — the exact version already in `go.mod` (HIGH confidence, pkg.go.dev fetched for this specific tag, not just "latest"):

- `func NewStreamableHTTPHandler(getServer func(*http.Request) *Server, opts *StreamableHTTPOptions) *StreamableHTTPHandler` — the entry point. `getServer` lets a `*mcp.Server` be constructed/selected per-request; a constant closure returning one shared `*Server` is sufficient for a single in-cluster deployment.
- `(*StreamableHTTPHandler) ServeHTTP(w http.ResponseWriter, req *http.Request)` — it **is** a plain `http.Handler`, so it composes with `net/http` exactly like the existing chi-based API server (mount it as a route, or run it standalone with `http.ListenAndServe`).
- `StreamableHTTPOptions` — options struct (includes things like an `OnConnectionClose` callback for session-end/timeout notification).
- Related lower-level types: `StreamableServerTransport` / `StreamableClientTransport` — not needed for a straightforward server mount.

**Auth — do NOT bump the SDK to get `RequireBearerToken`:** that middleware (OAuth-style bearer-token verification, `TokenVerifier`, scopes, `WWW-Authenticate` challenge) is confirmed **absent from v1.6.1** — it's a newer SDK addition. Bumping the SDK solely for it would (a) introduce a dependency change beyond this milestone's stated "k8s work is YAML-only except the already-pinned MCP HTTP transport" framing, and (b) is the wrong shape anyway: CLAUDE.md's constraint is explicit — **do not introduce a separate/external auth provider**; auth must go through the existing `clients` table / API-key model. **Recommendation:** wrap the plain `http.Handler` returned by `NewStreamableHTTPHandler` with a small stdlib `net/http` middleware — same shape as this codebase's existing API-key-check middleware in `internal/api` — validating a shared header before calling `ServeHTTP` (either the same client API key the MCP tool already forwards downstream today, or a separate cluster-internal shared secret). Combine this with the same "internal-only, never exposed outside the cluster" trust posture already used for `/metrics` and `asynqmon` (bind in-cluster, gate with `NetworkPolicy`), since the milestone's own framing describes this MCP endpoint as internal-only, not a new public surface. This path requires **zero new Go dependencies** — pure stdlib middleware, consistent with "no new deps beyond the already-pinned SDK" for this milestone.

## Helm Chart Layout

**Recommendation: a single chart, not an umbrella chart with subcharts.** (Own judgment, MEDIUM confidence — informed by general Helm guidance found via search, not a single authoritative source naming this exact project's shape.)

Rationale specific to this project:
- Umbrella charts/subcharts earn their overhead when there's genuine reuse (the same subchart deployed by multiple parent charts) or team-ownership boundaries needing independent release cadences/blast-radius isolation. OctoConv v1.6 is one operator, one cluster, one release unit, 6 tightly-coupled first-party services that already share one `docker-compose.yml` and one `.env` contract — there is no reuse case and no ownership boundary to protect.
- A single chart with **templates organized into per-service subdirectories** under `templates/` (e.g. `templates/api/deployment.yaml`, `templates/api/service.yaml`, `templates/worker/deployment.yaml`, `templates/keda/scaledobject-image.yaml`, `templates/postgres/statefulset.yaml`, etc.) gets the organizational clarity of subcharts without the overhead of N separate `Chart.yaml`/`values.yaml` pairs, N sets of chart versioning, and Helm's subchart value-override indirection (`<subchart>.path.to.value`).
- One flat `values.yaml` (with per-service top-level keys: `api:`, `worker:`, `documentWorker:`, `chromiumWorker:`, `webhookWorker:`, `postgres:`, `redis:`, `minio:`, `keda:`, `prometheus:`) maps directly onto the existing `.env.example` contract per service — easiest to keep in sync with the Go code's env-var reads.
- Revisit subcharts only if a second, independently-deployed environment/product ever needs to reuse just the Postgres/Redis/MinIO piece.

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|--------------------------|
| Helm v4 (or v3.21.3 fallback) | Kustomize (already floated as a fallback option in SEED-004) | If the team later wants pure-overlay patching without a templating language, or a Helm CLI dependency is undesirable in CI. Not the starting choice here since both SEED-004 and PROJECT.md already frame Helm as the primary path for this milestone. |
| KEDA Prometheus scaler on the existing `queue_depth` metric | KEDA Redis Lists scaler on asynq's internal `pending` list keys | Only as a secondary/debug signal, or if the Prometheus metric is ever found unreliable — but wiring it requires hardcoding the exact braced key format `asynq:{queue}:pending`, an internal implementation detail, not a documented asynq contract. |
| Hand-rolled minimal StatefulSets for Postgres/Redis/MinIO | Official/community Helm charts (`bitnami/*`, `minio/minio`, any community `postgresql`/`redis` chart) | If this ever needs to become a real multi-environment/HA deployment rather than a local validation milestone — at that point, properly evaluate CloudNativePG (Postgres operator), a maintained Redis chart/operator, and MinIO Operator+Tenant on their merits at that time. |
| Bare `prom/prometheus` Deployment + static scrape ConfigMap | `kube-prometheus-stack` / standalone Prometheus Operator | If dashboards (Grafana), alerting (Alertmanager), or `ServiceMonitor`-based auto-discovery across many future services become valuable — not needed for "KEDA needs one queryable URL" today. |

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|------------------|-------|
| KEDA v2.20.1 (Helm chart `kubeVersion: >=v1.23.0-0`) | OrbStack Kubernetes (observed progression 1.29→1.31→1.33 across recent releases) | Comfortably satisfied at any of these observed versions, all well above KEDA's floor. MEDIUM confidence on the *exact current* OrbStack k8s version (no single indexed canonical "current version" page found; inferred from release-notes/GitHub-issue history) — verify with `kubectl version` at execution time. |
| `modelcontextprotocol/go-sdk` v1.6.1 | Go 1.26 (project's pinned toolchain) | No changes needed — v1.6.1 already satisfies the streamable-HTTP requirement without a version bump. |
| `minio/minio:latest` (current compose pin) | OrbStack's "no registry needed" image visibility | Needs to be **repinned to a dated release tag** before the k8s port, both for supply-chain pinning hygiene (already the norm elsewhere in this repo) and to avoid OrbStack's documented default re-pull-on-`:latest` behavior undermining the "no registry" convenience during local iteration. |

## Sources

- https://github.com/kedacore/keda/releases (GitHub Releases API, live-fetched: `v2.20.1`, published 2026-06-08) — HIGH
- https://kedacore.github.io/charts/index.yaml (live-fetched Helm repo index: `keda` chart v2.20.1 / appVersion 2.20.1) — HIGH
- https://keda.sh/docs/2.20/scalers/prometheus/ (live-fetched: full field list for the Prometheus scaler trigger) — HIGH
- https://keda.sh/docs/2.20/reference/scaledobject-spec/ (live-fetched: `pollingInterval`/`cooldownPeriod`/`minReplicaCount`/`maxReplicaCount` defaults and activation vs. scaling phase behavior) — HIGH
- https://keda.sh/docs/2.20/scalers/redis-lists/ (live-searched: `listName`/`listLength`/`activationListLength` shape) — HIGH
- https://github.com/hibiken/asynq (source: `internal/base/base.go`, `internal/rdb/rdb.go`, live-fetched — confirmed `PendingKey` is a Redis LIST populated via `LPUSH` and drained via `RPOPLPUSH`, key format `asynq:{qname}:pending`) — HIGH
- https://github.com/helm/helm/releases (live-fetched: Helm v3.21.3 / v4.2.3, both released 2026-07-09; v4 GA, v3 explicitly noted as approaching EOL) — HIGH
- https://docs.orbstack.dev/kubernetes/ (live-fetched: shared image store / no-registry-needed behavior, `:latest` re-pull caveat, wildcard `*.k8s.orb.local` LoadBalancer routing, no built-in ingress controller by default) — HIGH
- OrbStack FAQ / release notes / GitHub issues (WebSearch, not one single fetched canonical page — kubectl context name `orbstack`, k8s version progression 1.29→1.33) — MEDIUM
- https://github.com/bitnami/charts/issues/35164 + Broadcom migration guidance + corroborating secondary sources (Chainguard, Minimus, Chkk) (WebSearch, multiple independent sources agreeing) — HIGH
- https://github.com/minio/operator (releases/discussions), https://artifacthub.io (minio/minio-operator listings) (WebSearch — MinIO has no single current-state doc; synthesized from several live-searched sources) — MEDIUM
- https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.6.1/mcp (live-fetched for the exact pinned version: `NewStreamableHTTPHandler`, `StreamableHTTPOptions`; confirms `RequireBearerToken` absent at this version) — HIGH
- General Helm umbrella-vs-single-chart guidance (WebSearch, multiple blog sources, no single canonical spec — treated as informed opinion, not settled fact) — MEDIUM

---
*Stack research for: Kubernetes + KEDA milestone (v1.6), OctoConv*
*Researched: 2026-07-14*
