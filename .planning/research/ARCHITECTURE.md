# Architecture Research

**Domain:** Kubernetes + KEDA deployment of an existing Go/asynq/Postgres/Redis/MinIO conversion service (OctoConv v1.6)
**Researched:** 2026-07-14
**Confidence:** HIGH (all findings grounded in direct reads of `cmd/*/main.go`, `docker-compose*.yml`, `internal/e2e/e2e_test.go`, `internal/mcpserver/*.go`, `internal/api/presets_handlers.go`, migrations, `.github/workflows/ci.yml`); MEDIUM for OrbStack-specific networking claims (verified via one WebSearch against OrbStack's own docs, not independently tested against this repo's actual chart)

This is **integration-design** research, not generic "how do Kubernetes apps look" research: every finding below is anchored to a real file in this repository. Where the four SEED-004 "landmines" have a code-verified zero-code-change fix, that is stated as fact, not inferred.

## Standard Architecture

### System Overview

```
┌──────────────────────────────────────────────────────────────────────────┐
│ Namespace: octoconv (Helm release: octoconv, chart: deploy/chart/octoconv)│
├──────────────────────────────────────────────────────────────────────────┤
│  Deployments (existing binaries, one container image each)               │
│  ┌────────┐ ┌────────┐ ┌──────────────┐ ┌────────────────┐ ┌───────────┐ │
│  │  api   │ │ worker │ │document-worker│ │chromium-worker │ │webhook-wkr│ │
│  │ (1..N) │ │ (KEDA) │ │    (KEDA)     │ │    (KEDA)      │ │ (fixed=2) │ │
│  └───┬────┘ └───┬────┘ └──────┬───────┘ └───────┬────────┘ └─────┬─────┘ │
│      │          │             │                 │                │       │
│  ┌───┴──────────┴─────────────┴─────────────────┴────────────────┴────┐ │
│  │        Service: octoconv-api (ClusterIP, LB via OrbStack)          │ │
│  └─────────────────────────────────────────────────────────────────────┘ │
│  ┌────────────┐  ┌──────────────┐                                        │
│  │ mcp-http   │  │ asynqmon      │  NEW (Phase D) / existing-but-relocated│
│  │ (1 replica)│  │ (dashboard)   │                                        │
│  └────────────┘  └──────────────┘                                        │
├──────────────────────────────────────────────────────────────────────────┤
│  Jobs / Hooks                                                            │
│  ┌──────────────────┐   ┌───────────────────┐   ┌──────────────────────┐ │
│  │ migrate (Helm     │   │ createbucket (Helm │   │ e2e-test (manual Job,│ │
│  │ pre-install/      │   │ pre-install/       │   │ NOT a hook — run     │ │
│  │ pre-upgrade hook) │   │ pre-upgrade hook)  │   │ on demand)            │ │
│  └──────────────────┘   └───────────────────┘   └──────────────────────┘ │
├──────────────────────────────────────────────────────────────────────────┤
│  KEDA (Active KEDA namespace, chart-conditional keda.enabled)             │
│  ScaledObject×3 (image/document/html queues) → Prometheus scaler         │
│  querying octoconv_queue_depth{queue=...,state="pending"}                │
│  webhook-worker: fixed 2 replicas, NOT KEDA-managed (redundancy, not      │
│  elasticity — matches the existing "≥2-consumer" design intent)          │
├──────────────────────────────────────────────────────────────────────────┤
│  Stateful dependencies (own templates, not subcharts — see rationale)     │
│  ┌──────────┐  ┌───────┐  ┌───────┐                                     │
│  │ postgres │  │ redis │  │ minio │  each: Deployment + PVC + ClusterIP │
│  └──────────┘  └───────┘  └───────┘  Service with an FQDN-stable name   │
└──────────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | File/Chart Location |
|-----------|-----------------|----------------------|
| `deploy/chart/octoconv/templates/deployment-{api,worker,document-worker,chromium-worker,webhook-worker}.yaml` | One Deployment template per existing binary, `{{ .Values.<svc>.replicas }}` (worker/document-worker/chromium-worker default low, overridden by KEDA once `keda.enabled`); env sourced from a shared `configmap.yaml` + `secret.yaml` | NEW |
| `deploy/chart/octoconv/templates/job-migrate.yaml`, `job-createbucket.yaml` | Helm hooks (`pre-install,pre-upgrade`) running `cmd/migrate` and an `mc mb --ignore-existing` one-shot against the chart's own MinIO Service | NEW |
| `deploy/chart/octoconv/templates/scaledobject-*.yaml` | KEDA `ScaledObject` per engine-class queue, `keda.enabled` gate | NEW |
| `deploy/chart/octoconv/templates/deployment-mcp-http.yaml` | Runs `cmd/mcp-http` (new binary) behind a ClusterIP Service | NEW |
| `deploy/chart/octoconv/templates/{postgres,redis,minio}.yaml` | Deployment + PVC + Service for each stateful dependency | NEW |
| `cmd/mcp-http/main.go` | New thin entrypoint: identical wiring to `cmd/mcp-server/main.go` (`mcpserver.Load()` → `NewClient` → `NewServer`) but runs `mcp.NewStreamableHTTPHandler` over `net/http` instead of `&mcp.StdioTransport{}` | NEW |
| `Dockerfile.mcp-http` | New image; `cmd/mcp-server` has never been containerized (no existing `Dockerfile.mcp-server`) | NEW |
| `internal/api/system_presets_handlers.go` | New handlers reusing the **already scope-agnostic** `PresetAdmin` interface with `Scope: presets.ScopeSystem, ClientID: nil` | NEW |
| `internal/api/middleware` (or inline in `routes.go`) | New `RequireOperator` gate checking `client.ID` against an env allowlist | NEW |
| `cmd/worker/main.go`, `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go`, `cmd/webhook-worker/main.go` | Add a real `/healthz` (Postgres/Redis/S3 ping) alongside the existing `promhttp.Handler()` on the metrics listener | MODIFIED (small) |
| `internal/e2e/e2e_test.go` | **Zero changes** — `E2E_WEBHOOK_HOST` and `E2E_S3_DIAL_ADDR` are already env-driven escape hatches; only the Job manifest wiring them changes | UNCHANGED |

## Recommended Chart Structure

```
deploy/chart/octoconv/
├── Chart.yaml                     # single chart, apiVersion v2, no chart deps (see rationale below)
├── values.yaml                    # see shape below
├── values-orbstack.yaml           # local-dev overrides (LoadBalancer type, image.pullPolicy=Never)
├── templates/
│   ├── _helpers.tpl               # fullname/labels helpers + shared env-block templates
│   ├── configmap.yaml             # non-secret env: S3_BUCKET, METRICS_ADDR, *_MAX_RETRY, *_ENGINE_TIMEOUT...
│   ├── secret.yaml                # DATABASE_URL, API_KEY_SALT, WEBHOOK_SIGNING_SECRET, S3 creds, OCTOCONV_API_KEY (mcp)
│   ├── deployment-api.yaml
│   ├── deployment-worker.yaml
│   ├── deployment-document-worker.yaml
│   ├── deployment-chromium-worker.yaml
│   ├── deployment-webhook-worker.yaml   # replicas: 2 fixed, NOT templated by KEDA
│   ├── deployment-mcp-http.yaml         # conditional: .Values.mcpHttp.enabled
│   ├── deployment-asynqmon.yaml
│   ├── service-*.yaml                   # one per component that needs one (api, mcp-http, asynqmon, postgres, redis, minio)
│   ├── postgres.yaml                    # Deployment + PVC + Service (own template, not a subchart)
│   ├── redis.yaml
│   ├── minio.yaml
│   ├── job-migrate.yaml                 # helm.sh/hook: pre-install,pre-upgrade
│   ├── job-createbucket.yaml            # helm.sh/hook: pre-install,pre-upgrade, hook-weight after migrate
│   ├── scaledobject-image.yaml          # {{ if .Values.keda.enabled }}
│   ├── scaledobject-document.yaml
│   ├── scaledobject-html.yaml
│   ├── networkpolicy-metrics.yaml       # restrict :9090 ingress to the scraper's namespace/pod selector
│   └── networkpolicy-mcp.yaml           # restrict mcp-http ingress to trusted in-cluster callers
└── crds/                                # NOT needed — KEDA CRDs are installed by the KEDA operator itself,
                                          # this chart only creates ScaledObject *instances*
```

### values.yaml shape

```yaml
global:
  imageTag: "dev"          # single tag for all first-party images (built with the same commit)
  namespace: octoconv

image:
  registry: ""              # empty for OrbStack local images (no push needed, see Pitfalls)
  pullPolicy: Never          # OrbStack builds land directly in its containerd/moby store

metrics:
  addr: "0.0.0.0:9090"       # overrides compose's 127.0.0.1:9090 default — see Landmine 1

s3:
  # FQDN, not short Service name — resolvable from BOTH in-cluster pods AND the
  # OrbStack host (see Landmine 4). Namespace must match .Release.Namespace.
  endpoint: "octoconv-minio.octoconv.svc.cluster.local:9000"

api:
  replicas: 1
  resources: {...}

worker:
  replicas: 1                # baseline before KEDA takes over; ignored once ScaledObject is active
document-worker:
  replicas: 1
chromium-worker:
  replicas: 1
webhook-worker:
  replicas: 2                 # fixed redundancy — NEVER templated by KEDA (see Component Responsibilities)

keda:
  enabled: false               # off by default; chart installs cleanly even without the KEDA operator present
  image:
    pollingInterval: 15
    minReplicaCount: 0
    maxReplicaCount: 8

mcpHttp:
  enabled: false               # Phase D ships this off-by-default until validated

operator:
  clientIds: []                # OPERATOR_CLIENT_IDS allowlist for system-presets REST (Q5)
```

### Structure Rationale

- **Single chart, not per-service charts**: every component already deploys from one repo/one commit (`global.imageTag`), and the compose file's own structure (`docker-compose.yml`) is a single-file, single-stack description that this chart should mirror 1:1 for maintainability — a multi-chart umbrella would just re-add complexity the compose file never had.
- **Postgres/Redis/MinIO as own templates, not Bitnami/official subcharts**: `docker-compose.yml` pins `postgres:18`, `redis:8`, `minio/minio:latest` directly with hand-rolled healthchecks and a single PVC each — no StatefulSet features (ordinal identity, headless Service) are actually used anywhere in the current architecture (single-instance stateful deps, not a Postgres/Redis cluster). Pulling in Bitnami subcharts would import a large surface of unused configurability (replication, TLS, metrics exporters) for zero benefit at this milestone's scope, and would fight the project's own "minimize new dependency surface" bias. Recommendation: three flat Deployment+PVC+Service templates, revisit subcharts only if genuine HA/replication requirements emerge (explicitly out of scope per `PROJECT.md`'s "Kubernetes + KEDA" framing as an infra-validation milestone, not a production-HA milestone).
- **KEDA ScaledObjects live in the SAME chart**, gated by `keda.enabled`, not a separate chart: they are `ScaledObject` custom resources, not the KEDA operator itself (the operator is a cluster-wide prerequisite, installed once via its own Helm chart, exactly like `cert-manager` or an ingress controller — outside this chart's concern). Keeping the `ScaledObject` *instances* in this chart means `helm install octoconv` and `helm install octoconv --set keda.enabled=true` are the only two states an operator needs to reason about, matching SEED-004's framing ("KEDA: ScaledObjects per engine-class... на УЖЕ СУЩЕСТВУЮЩУЮ Prometheus-метрику").
- **`webhook-worker` is a fixed-replica Deployment, deliberately NOT KEDA-managed**: its ×2 replication exists for delivery redundancy and singleton-sweeper failover (Postgres advisory lock — `PROJECT.md` Key Decision, Phase 16), not for elastic throughput scaling. A KEDA `ScaledObject` targeting it would risk scaling to 1 or 0 replicas under low webhook-queue depth, silently breaking the "≥2 consumers" redundancy guarantee the whole webhook architecture depends on. This is a genuine engine-class-vs-consumer-topology distinction worth flagging explicitly for the roadmap.

## Architectural Patterns

### Pattern 1: Zero-code-change env override for METRICS_ADDR (Landmine 1)

**What:** `os.Getenv("METRICS_ADDR")` is read identically in all five binaries — `cmd/api/main.go:129`, `cmd/worker/main.go:99`, `cmd/document-worker/main.go:104`, `cmd/chromium-worker/main.go:96`, `cmd/webhook-worker/main.go:125` — falling back to `"127.0.0.1:9090"` only when unset, then used directly as `http.Server{Addr: metricsAddr}`. There is no code path anywhere that hardcodes `127.0.0.1` beyond this single default string.

**When to use:** Every k8s Deployment template sets `METRICS_ADDR=0.0.0.0:9090` via the shared ConfigMap. This is a **values.yaml-only change — verified zero Go code change required.**

**Trade-off:** Binding `0.0.0.0` inside the pod removes the "unreachable outside the host" security boundary the compose deployment relies on for `/metrics` and `asynqmon` (`docker-compose.yml`'s own comment: "Bound to 127.0.0.1 only... no auth layer needed since it's unreachable outside the host"). In-cluster, ANY pod that can route to the metrics port can scrape it — there is no equivalent implicit isolation. **A `NetworkPolicy` restricting ingress on `:9090` to the Prometheus scraper's pod/namespace selector is not optional polish — it is the direct in-cluster replacement for the security property `127.0.0.1` used to provide.** Same reasoning applies to `asynqmon` (Deployment + ClusterIP Service + its own `NetworkPolicy`, since compose's `ports: - "127.0.0.1:8980:8080"` host-loopback restriction has no k8s equivalent — a ClusterIP Service alone is already reachable from every pod in the cluster by default).

```yaml
# networkpolicy-metrics.yaml (all Deployments carrying metrics:9090)
podSelector:
  matchExpressions:
    - {key: app.kubernetes.io/part-of, operator: In, values: [octoconv]}
ingress:
  - from:
      - namespaceSelector: {matchLabels: {name: monitoring}}   # or podSelector for a Prometheus Operator scrape target
    ports: [{port: 9090, protocol: TCP}]
```

### Pattern 2: In-cluster E2E as a self-addressing Job (Landmine 2)

**What:** `internal/e2e/e2e_test.go` does **not** use `httptest.NewServer`'s default loopback listener. It explicitly does:

```go
ln, err := net.Listen("tcp", "0.0.0.0:0")   // e2e_test.go:348, :1010
...
srv.Listener.Close()
srv.Listener = ln
```

— i.e., the webhook/canary receiver is deliberately bound to all interfaces on an OS-assigned ephemeral port, precisely so it is reachable from outside the test process's own network namespace. The receiver's address is then read back from `ln.Addr()` and combined with the already-overridable `E2E_WEBHOOK_HOST` env var (`e2eSetup`, `e2e_test.go:78-85`, default `"host.docker.internal"`).

**Two in-cluster options compared:**

| | Run e2e suite AS a k8s Job (test binary in-cluster) | Receiver-pod + host-run tests via port-forward |
|---|---|---|
| Webhook reachability | **Trivial.** Pod IP is directly routable from every other pod by default (flat k8s pod network, no NAT) — inject the Job's own pod IP via the Downward API (`fieldRef: status.podIP`) into `E2E_WEBHOOK_HOST`. **No code change** — `E2E_WEBHOOK_HOST` already exists as an override knob. | **Structurally hard.** In-cluster `webhook-worker` pods would need to dial back OUT of the cluster to a process on the developer's host. There is no k8s equivalent of Docker's `host-gateway` — this is exactly the landmine SEED-004 names ("host.docker.internal:host-gateway трюк... в k8s не существует"). Only works at all if OrbStack's specific host-bridging behavior happens to route it, which is undocumented and non-portable to kind/k3d (the stated fallback). |
| S3 presigned-download reachability | **Trivial**, if `S3_ENDPOINT` is set to MinIO's FQDN (`octoconv-minio.<ns>.svc.cluster.local:9000`, see Landmine 4) — the in-cluster Job resolves it exactly like any other pod. `E2E_S3_DIAL_ADDR` becomes **unnecessary** entirely (it exists only to bridge the host-vs-container DNS gap; running the suite in-cluster eliminates that gap). | Host-run tests need `E2E_S3_DIAL_ADDR` (or OrbStack's `cluster.local` host resolution, see Landmine 4) regardless — no advantage over the Job approach here. |
| Portability beyond OrbStack | Fully portable — Downward API pod IP + Service DNS work identically on kind/k3d/any real cluster (the stated SEED-004 fallback). | Depends on host-to-pod ingress specifics of the chosen local cluster; not guaranteed on kind/k3d. |
| CI reuse | The Job manifest can be driven by the exact same `go test ./internal/e2e/...` invocation `ci.yml`'s `e2e` tier already runs — only the environment differs (Downward API env vars vs `localhost`/`E2E_S3_DIAL_ADDR`). | Requires a materially different harness (port-forward setup, receiver-pod lifecycle) with no CI-tier reuse. |

**Recommendation: run the e2e suite as an in-cluster Kubernetes `Job`.** It is the only option that (a) requires zero changes to `internal/e2e/e2e_test.go`, (b) removes two landmines simultaneously (webhook host-gateway AND S3 dial-redirect, since both existing escape hatches become no-ops in-cluster), and (c) is portable to the kind/k3d fallback SEED-004 explicitly names, unlike the port-forward option which leans on undocumented OrbStack-specific host networking.

```yaml
# job-e2e.yaml (manual, NOT a Helm hook — run on demand: `helm template ... | kubectl apply -f -` or `helm test`)
spec:
  template:
    spec:
      containers:
        - name: e2e
          image: "{{ .Values.image.registry }}/octoconv-e2e:{{ .Values.global.imageTag }}"
          env:
            - {name: E2E_BASE_URL, value: "http://octoconv-api:8090"}
            - {name: DATABASE_URL, valueFrom: {secretKeyRef: {name: octoconv-secret, key: database-url}}}
            - {name: API_KEY_SALT, valueFrom: {secretKeyRef: {name: octoconv-secret, key: api-key-salt}}}
            - {name: WEBHOOK_SIGNING_SECRET, valueFrom: {secretKeyRef: {name: octoconv-secret, key: webhook-signing-secret}}}
            - name: E2E_WEBHOOK_HOST
              valueFrom: {fieldRef: {fieldPath: status.podIP}}   # replaces host.docker.internal entirely
            # E2E_S3_DIAL_ADDR: intentionally OMITTED — S3_ENDPOINT is already the
            # in-cluster-resolvable MinIO FQDN, no redirect needed.
      restartPolicy: Never
```

This needs a **new** `Dockerfile.e2e` (multi-stage: build the test binary with `go test -c ./internal/e2e/...`, copy fixtures under `internal/e2e/testdata/`) — a new image, not a new chart concept.

### Pattern 3: migrate/createbucket as Helm hooks, not initContainers

**What:** `docker-compose.yml`'s ordering today is `depends_on: {condition: service_healthy}` (postgres/redis/minio) plus a dedicated one-shot `createbucket` service. Two idiomatic k8s translations exist: (a) Helm `pre-install,pre-upgrade` hook Jobs, or (b) `initContainers` on every consuming Deployment.

**Recommendation: Helm hooks, not initContainers**, for both `migrate` and `createbucket`:
- **Migrate** is a single logical operation that must run exactly once per deploy, before ANY consumer starts — an `initContainer` model would re-run `cmd/migrate` on every single Deployment's every single pod restart (api, worker, document-worker, chromium-worker, webhook-worker ×2 = 6+ redundant migration attempts per rollout). `cmd/migrate`'s embedded-SQL runner (`internal/db/db.go`) is presumably idempotent (`CREATE TABLE IF NOT EXISTS`-style), but repeating it N times per deploy for no benefit is pure waste and a needless race-surface (N pods racing to apply the same migration concurrently) versus a single Job with `hook-weight` ordering guaranteeing exactly one execution.
- **createbucket** has the same "logically once" shape (`mc mb --ignore-existing`) — a `pre-install,pre-upgrade` hook with a higher (later) `hook-weight` than migrate's Job, so ordering is: `migrate` → `createbucket` → main release resources. Helm guarantees hook Jobs complete (or fail, blocking the release) before the main manifests are applied, replicating compose's `depends_on: condition: service_healthy` guarantee declaratively.
- **initContainers remain the right tool** only for genuinely PER-POD readiness gating (e.g., "wait for Postgres/Redis/MinIO to be reachable before this pod's main container starts" — but even that is largely redundant here since `cmd/*/main.go` already calls `log.Fatalf` on connect failure, causing k8s's own container restart/backoff to retry, which is arguably sufficient without an initContainer at all, given the existing fail-fast startup discipline in this codebase).

```yaml
# job-migrate.yaml
metadata:
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "0"
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
---
# job-createbucket.yaml
metadata:
  annotations:
    "helm.sh/hook": pre-install,pre-upgrade
    "helm.sh/hook-weight": "1"       # runs strictly after migrate
    "helm.sh/hook-delete-policy": before-hook-creation,hook-succeeded
```

### Pattern 4: FQDN S3_ENDPOINT closes the presigned-URL landmine for BOTH consumer classes (Landmine 4)

**What:** Presigned URLs bake `S3_ENDPOINT`'s host directly into the signed URL (`internal/storage/storage.go`, mirroring compose's `minio:9000`). Two consumer classes exist in the k8s scenario:

1. **In-cluster consumers** (worker pods redownloading, the in-cluster e2e Job, `mcp-http`'s server-side `Download`) — resolve any Service DNS name (short or FQDN) fine, since kube-dns configures a search path (`<ns>.svc.cluster.local`) for every pod.
2. **Host consumers** (a developer's `curl`/browser hitting a returned `download_url`, or a locally-run `cmd/mcp-server` in stdio mode pointed at the in-cluster API) — do **not** have that search path; a short Service name like `octoconv-minio:9000` will NOT resolve from macOS.

**Verified (WebSearch against OrbStack's own docs, MEDIUM-HIGH confidence):** OrbStack resolves standard `<service>.<namespace>.svc.cluster.local` domains directly from the macOS host — no port-forward, no `/etc/hosts` edits required — and additionally exposes `LoadBalancer`-type Services at `*.k8s.orb.local`. This means setting `S3_ENDPOINT` to the **fully-qualified** MinIO Service name (`octoconv-minio.octoconv.svc.cluster.local:9000`), not the short name, makes presigned URLs dialable identically from in-cluster pods AND the OrbStack host — closing the landmine for both consumer classes **without** needing `E2E_S3_DIAL_ADDR`, an Ingress, or a `LoadBalancer` for MinIO specifically, on OrbStack. This is an OrbStack-specific convenience, not a general k8s guarantee — flag explicitly that a real multi-node cluster (beyond this milestone's stated OrbStack/local scope) would need an Ingress or a MinIO `LoadBalancer`/`NodePort` for host-external presigned-URL access instead.

**Consumer map for this milestone's scenario:**

| Consumer | Reaches API via | Reaches presigned MinIO URL via |
|---|---|---|
| In-cluster workers (image/document/chromium/webhook) | ClusterIP Service DNS | FQDN Service DNS (same cluster) |
| In-cluster e2e Job (Pattern 2) | ClusterIP Service DNS | FQDN Service DNS (same cluster) |
| `mcp-http` pod (server-side `Download` in `convert_file`/`download_result`) | ClusterIP Service DNS (calls its own cluster's API) | FQDN Service DNS (same cluster) |
| Developer's Mac (`curl`, browser, a locally-run stdio `cmd/mcp-server` pointed at the cluster) | `*.k8s.orb.local` (LoadBalancer Service for `octoconv-api`) | FQDN `octoconv-minio.octoconv.svc.cluster.local:9000` (OrbStack host-side resolution) |

## Worker Probes (Q3)

**Current state, verified:** `worker`, `document-worker`, `chromium-worker`, `webhook-worker` expose exactly one HTTP listener — `promhttp.Handler()` on `METRICS_ADDR` — and nothing else. asynq itself exposes no health endpoint (confirmed: no health-check method exists on `asynq.Server`/`asynq.Client` in this codebase's usage, matching the prompt's own framing). The Postgres pool, Redis connection, and S3 client are all established once at startup and never actively re-checked.

**Assessment: a bare TCP probe against the metrics port is a weak but non-zero liveness signal** — it proves the Go process is alive and its own goroutines are scheduling (the metrics `http.Server` is started in a `go func()` well after `asynq.Server.Start(mux)`, which is itself non-blocking — see `cmd/worker/main.go:81-92` — so a listening metrics port implies the asynq processing loop was successfully constructed). It proves **nothing** about ongoing Redis/Postgres connectivity, since a dropped Redis connection mid-run does not close the independent metrics listener goroutine.

**Two options:**

1. **Zero-code-change**: `livenessProbe`/`readinessProbe` both as `tcpSocket` on port 9090. Cheapest path; catches only "process crashed/deadlocked," not "lost its dependencies."
2. **Recommended small addition**: give each worker binary a real `/healthz` on the metrics listener (switch `metricsSrv.Handler` from a bare `promhttp.Handler()` to an `http.ServeMux` with `/metrics` and `/healthz` registered), pinging `pool.Ping(ctx)` (pgxpool's native method), `store.Ping(ctx)` (already exists on `storage.Client`, reused verbatim from the API's `HealthDeps.S3`), and a small dedicated Redis ping client (mirrors `cmd/api/main.go`'s existing `redisPinger` — ~10 lines, duplicated per the codebase's established per-binary-duplication convention for small helpers). Use this for **readiness only** (`livenessProbe` stays `tcpSocket` — a transient Redis blip should not trigger a restart-loop policy); readiness failing here has limited practical effect since nothing routes Service traffic TO worker pods (they pull from Redis, they don't serve requests), but it gives `kubectl get pods` and any future HPA/monitoring genuine signal instead of a process-liveness-only proxy, and is directly consistent with the project's own established principle that `/healthz` must ping real dependencies, not just report "process up" (`internal/api/handlers.go`'s `handleHealth` doc comment: "It is read-only: it only pings, never writes" — extend the same discipline here rather than introduce an inconsistent, weaker health model for workers).

**Recommendation: do the small addition (option 2).** Given "Deployments/probes" is explicitly named as in-scope chart work in `SEED-004`, and the marginal code cost is genuinely small (three already-available ping primitives, no new packages), settling for a TCP-only proxy here would be an inconsistency the project's own conventions argue against.

## MCP HTTP Architecture (Q4)

**Verified:** `internal/mcpserver/mcpserver.go`'s `NewServer(cfg Config, c *Client) *mcp.Server` is genuinely transport-agnostic — it only builds an `*mcp.Server` and registers five tools against a `*Client`; the ONLY transport-specific line in the entire MCP stack is `cmd/mcp-server/main.go:46`: `srv.Run(ctx, &mcp.StdioTransport{})`. The SDK (`github.com/modelcontextprotocol/go-sdk` v1.6.1, confirmed installed) ships `mcp.NewStreamableHTTPHandler(getServer func(*http.Request) *mcp.Server, opts *mcp.StreamableHTTPOptions) *mcp.StreamableHTTPHandler` (`mcp/streamable.go:194`), which implements `http.Handler` — a drop-in target for a `net/http.Server`, exactly mirroring the existing `promhttp.Handler()` pattern already used for `/metrics` in every `cmd/*/main.go`.

**cmd/mcp-server gains `--http` vs new `cmd/mcp-http`:** the project's own established convention is **one binary per deployment unit**, explicitly justified for the analogous document/image engine split (`PROJECT.md` Key Decision: "Отдельный `cmd/document-worker` бинарник/контейнер вместо второго `asynq.Server` внутри image-воркера — ... ресурсная изоляция по классам движков"). A stdio MCP server is exec'd on-demand by a local MCP host (Claude Code, etc.) with a completely different lifecycle than a long-running, resource-limited, probed Kubernetes Deployment. **Recommendation: new `cmd/mcp-http/main.go`** (near-identical wiring to `cmd/mcp-server/main.go`: `mcpserver.Load()` → `mcpserver.NewClient(cfg)` → `mcpserver.NewServer(cfg, client)`, but the tail swaps `srv.Run(ctx, &mcp.StdioTransport{})` for an `http.Server` wrapping `mcp.NewStreamableHTTPHandler(...)`, bound to an env-configurable `MCP_HTTP_ADDR` mirroring the `METRICS_ADDR` pattern, with the same graceful-shutdown shape already used in `cmd/api/main.go`). **`internal/mcpserver` needs zero changes** for this. A new `Dockerfile.mcp-http` is required — no `Dockerfile.mcp-server` exists today (stdio has never been containerized).

**Auth pass-through — two options:**

1. **Pod holds ONE key (recommended)**: the `getServer` closure ignores the incoming `*http.Request` and always returns the single `*mcp.Server` built once at process start from the pod's `OCTOCONV_API_KEY` env var — an exact transport-swap of today's stdio model, with **zero changes** to `internal/mcpserver/client.go`'s `NewClient(cfg Config) (*Client, error)` (confirmed: it already bakes `apiKey` into the `Client` struct at construction, one key per process). Every caller that reaches the `mcp-http` Service acts as this one designated OctoConv client. This is architecturally consistent with the ALREADY-SHIPPED v1.5 design principle ("zero-privilege HTTP-клиент с редакцией ключа" — Phase 21): the MCP layer has never claimed per-end-user identity, only per-service identity, matching OctoConv's actual trust model where `clients` rows represent internal SERVICES, not individual humans. Gate WHO can reach this identity via a `NetworkPolicy` restricting ingress to specific trusted namespaces/pods — this is the in-cluster analog of "one developer, one locally-exec'd stdio process, one key" scaled to "one shared internal automation surface, one key, network-scoped."
2. **Per-caller pass-through** (caller supplies its own API key in a header, MCP pod holds none): requires a new `getServer(r *http.Request)` closure that extracts a caller-supplied key, a new `mcpserver.NewClientWithKey`-style constructor, and per-key `*mcp.Server`/`*Client` caching to avoid rebuilding on every request. This is the "more correct" multi-tenant answer (every caller's actions map to its own `clients` row for rate-limiting/audit) but is a **materially bigger change** not requested by this milestone's stated goal ("MCP получает in-cluster HTTP-эндпоинт" — a transport change, not a multi-tenancy redesign).

**Recommendation: Option 1 for v1.6.** Flag Option 2 explicitly as a documented future Key Decision if/when multiple distinct internal services need to share one `mcp-http` endpoint under their own separate identities — do not silently default into it.

**Integration gap worth flagging (not a blocker, but a real finding):** every MCP tool response includes a `local_path` field (`internal/mcpserver/tools.go:39,153` — JSON schema description: *"local filesystem path (inside the server's OUTPUT_DIR) where the converted file was already downloaded"*), and `convertFileHandler`/`downloadResultHandler` both actually write the result to `cfg.OutputDir` on the server's own filesystem (`internal/mcpserver/client.go:284` `Download`) before returning that path. In the stdio model this is correct (the calling MCP host and the server share a filesystem — same machine). **Once `mcp-http` runs as a remote pod, `local_path` refers to the pod's own ephemeral filesystem and is meaningless/unusable to any remote HTTP caller** — the `PresignedURL`/`DownloadURL` field remains the only usable part of the response for that transport. This is not a code-breaking bug (nothing crashes; the field is simply inert for remote callers), but it is a real, user-facing contract mismatch worth a documented note (or a follow-on `Config.SkipLocalDownload` knob) rather than silently shipping a misleading tool description under the HTTP transport.

## system-presets REST Operator Gate (Q5)

**Verified — the domain layer needs ZERO changes.** `internal/api/api.go`'s `PresetAdmin` interface (already used by the existing client-scope `/v1/presets` handlers) is already scope-agnostic: `Create(ctx, presets.CreateParams{Scope: ..., ClientID: ...})`, `Update/Deactivate/Get/List(ctx, scope string, clientID *uuid.UUID, ...)`. Today's handlers (`internal/api/presets_handlers.go`) simply always hardcode `Scope: presets.ScopeUser, ClientID: &client.ID`. **System-presets REST is therefore purely a new set of REST handlers + a new route group + a new authorization gate — not a domain/repo change.**

**Operator identity source — verified via schema:** `clients` table (`internal/db/migrations/0001_init.sql` + `0002_client_api_keys.sql`) has exactly `id, name, created_at, api_key_hash, api_key_hash_secondary, primary_revoked_at, secondary_revoked_at, updated_at` — **no spare role/flag column**, and `internal/clients/clients.go`'s `Client` struct mirrors this minimally (`ID uuid.UUID`, `Name string` only).

**Two options:**

1. **`OPERATOR_CLIENT_IDS` env allowlist (recommended)**: comma-separated UUID list, read once at API startup (mirrors the existing `envInt64`/`firstField` env-parsing idiom already in `cmd/api/main.go`), stored on `api.Config`, checked by a new gate — e.g. `middleware.RequireOperator(allowlist map[uuid.UUID]bool)` mounted only on a new `/v1/system/presets` route group, running AFTER `auth.Middleware` (client identity already resolved) and BEFORE the new handlers. **Zero schema migration.** Matches this project's own strict "environment-variable configuration only" architectural constraint (*"No config file support; every runtime setting... read from `os.Getenv`"*) and its precedent for exactly this shape of allowlist gate (`WEBHOOK_ALLOW_PRIVATE_IPS`, rate-limit env vars). Trade-off: changing the operator set requires a redeploy/restart (acceptable — internal-only, small, slow-changing operator population); no built-in per-operator audit trail beyond whatever `job_events`-style logging is added alongside.
2. **`is_operator boolean` column on `clients`** (new migration `0006_client_operator_flag.sql`): more scalable (change without redeploy, via `manage-clients`), supports a future per-operator audit trail naturally. Genuinely the better long-term answer, but is unjustified schema churn for this milestone's stated scope (a handful of internal operators, not a growing/self-service operator population).

**Recommendation: `OPERATOR_CLIENT_IDS` env allowlist for v1.6.** Document the DB-column alternative as an explicit future Key Decision, to be revisited if/when the operator set needs to change without a redeploy or per-operator audit becomes a real requirement.

**No-leak consistency finding**: this codebase's presets handlers (`internal/api/presets_handlers.go`) are unusually disciplined about **never returning 403** — cross-client and system-scope-write attempts all collapse into the identical `noSuchPreset` 404 (explicit `D-03` comment in the file). The new operator gate should follow the SAME discipline: a non-operator client hitting `/v1/system/presets/*` should receive the same uniform 404, not a 403 that would confirm "this endpoint exists and requires elevated privilege you don't have" — consistent with the Phase 1 Key Decision ("cross-client доступ → 404, никогда 403") extended one level further to operator-vs-non-operator.

## Build Order (Q6)

Five clusters of work, sequenced by real dependency, not just milestone-listing order:

1. **Phase A — Chart Core** (foundational, must go first): Helm chart skeleton for all five existing binaries + postgres/redis/minio + migrate/createbucket Helm hooks + the four landmine closures (METRICS_ADDR override + NetworkPolicy, in-cluster e2e-as-Job, hook ordering, FQDN S3_ENDPOINT) + worker `/healthz` probes. Every other phase either deploys through this chart or benefits from its conventions (Secrets/ConfigMap shape, namespace, probe pattern).
2. **Phase B — KEDA Autoscaling**: `ScaledObject`×3 (image/document/html) against `octoconv_queue_depth`, `keda.enabled` gate. **Depends on Phase A** (needs the Deployments to scale and a reachable, NetworkPolicy-scoped `/metrics`).
3. **Phase C — Load Test / Autoscale Validation**: synthetic load (or a higher-volume e2e run) proving 0→N→0 with observable criteria (replica count over time, KEDA/HPA events, queue-depth graph). **Depends on Phase B.** This closes the milestone's actual stated Core Value ("воркеры автоскейлятся KEDA... доказано") — Phases A→B→C form the primary, sequential arc.
4. **Phase D — MCP HTTP**: `cmd/mcp-http` + `Dockerfile.mcp-http` + chart template + NetworkPolicy. **No code/architecture dependency on B or C** — only a soft dependency on Phase A for a clean deployment target (Secret/ConfigMap conventions, probe pattern). Can run in parallel with B/C if capacity allows, or immediately after A.
5. **Phase E — system-presets REST**: new handlers + operator-gate middleware + `OPERATOR_CLIENT_IDS` env var. **Fully independent of all k8s work** — pure `internal/api` change, no chart dependency at all (the only chart touch is eventually adding `OPERATOR_CLIENT_IDS` to the ConfigMap, trivial whenever it lands).

**Recommended sequencing: A → D → E → B → C.** Rationale: A is the highest-risk, most novel, SEED-004-flagged work and must be first. D and E have zero dependency on the harder KEDA arc and are small/independent — sequencing them right after A gives two quick, low-risk wins and validates the chart's Secret/ConfigMap/probe conventions on smaller surfaces before the higher-stakes KEDA work. B→C then close the milestone's primary, hardest, most novel goal as a contiguous arc, minimizing context-switching mid-validation. **D and E have no ordering constraint relative to each other or to B/C** — a roadmap author may freely interleave or reorder them (e.g., run E first since it needs zero k8s context at all, or defer D/E to the very end as a closing cluster) without breaking any real dependency; the A→B→C spine is the only hard-ordered sequence.

## New vs Modified Files Summary

| File | Status | Purpose |
|------|--------|---------|
| `deploy/chart/octoconv/**` (full chart) | NEW | Helm chart — Deployments, Services, Jobs, ScaledObjects, NetworkPolicies |
| `Dockerfile.e2e` | NEW | In-cluster e2e test-binary image (Pattern 2) |
| `Dockerfile.mcp-http` | NEW | `cmd/mcp-http` container image (Q4) |
| `cmd/mcp-http/main.go` | NEW | HTTP-transport MCP entrypoint (thin, mirrors `cmd/mcp-server`) |
| `internal/api/system_presets_handlers.go` | NEW | System-scope preset REST handlers, reusing existing `PresetAdmin` |
| `internal/api/routes.go` | MODIFIED | Mount `/v1/system/presets` group + operator-gate middleware |
| `cmd/api/main.go` | MODIFIED | Parse `OPERATOR_CLIENT_IDS`, thread into `api.Config` |
| `cmd/worker/main.go`, `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go`, `cmd/webhook-worker/main.go` | MODIFIED | Add `/healthz` alongside `/metrics` (Q3) |
| `internal/mcpserver/**` | UNCHANGED | Already transport-agnostic |
| `internal/e2e/e2e_test.go` | UNCHANGED | `E2E_WEBHOOK_HOST`/`E2E_S3_DIAL_ADDR` already env-driven |
| `.github/workflows/ci.yml` | UNCHANGED (this milestone) | CI stays entirely compose-based; see note below |

**CI note (explicitly checked):** `.github/workflows/ci.yml`'s 4 tiers (`gate` → `race` → `docker-build` → `e2e`) are 100% docker-compose-based — no `kind`/`k3d`/`helm lint`/`helm template` step exists anywhere, and nothing in this milestone's scope (per `PROJECT.md`'s Active requirements) calls for adding one. **A k8s validation CI tier is out of scope for v1.6** and should be treated as a candidate seed for a future milestone, not silently folded into this one.

## Anti-Patterns

### Anti-Pattern 1: Binding `0.0.0.0:9090` without a NetworkPolicy

**What people do:** Fix the METRICS_ADDR landmine by only changing the env var, treating it as "done" because the pod now starts and Prometheus can scrape it.
**Why it's wrong:** This silently removes the security boundary `docker-compose.yml` relied on (`127.0.0.1`-only binding, explicitly documented there as "no auth layer needed since it's unreachable outside the host") — in k8s, `0.0.0.0` on any port is reachable from every pod in the cluster by default, with zero implicit isolation.
**Do this instead:** Ship the `METRICS_ADDR` change and a `NetworkPolicy` restricting `:9090` ingress in the SAME phase/plan — never as a follow-up.

### Anti-Pattern 2: KEDA-scaling the webhook-worker Deployment

**What people do:** Apply the same "ScaledObject per queue" pattern uniformly to all five queue-backed binaries, including `webhook-worker`.
**Why it's wrong:** `webhook-worker`'s ×2 replication exists specifically for delivery redundancy and Postgres-advisory-lock sweeper failover (a documented Key Decision), not throughput elasticity. Scaling it to 0 or 1 under low webhook-queue depth would silently break the "no single point of failure" guarantee the whole reconciler/webhook architecture depends on.
**Do this instead:** KEDA only for `worker`/`document-worker`/`chromium-worker` (genuine engine-class elastic scaling); `webhook-worker` stays a fixed-replica (2) Deployment, explicitly excluded from `keda.enabled`'s scope.

### Anti-Pattern 3: Short MinIO Service name in `S3_ENDPOINT`

**What people do:** Set `S3_ENDPOINT=octoconv-minio:9000` (mirroring compose's `minio:9000`), reasoning "it's just the compose hostname → k8s Service name translation."
**Why it's wrong:** A short Service name only resolves for consumers inside the same namespace with kube-dns's search-path configured (in-cluster pods) — it does NOT resolve from the OrbStack host, re-creating the exact "compose-DNS-in-presigned-URL" landmine SEED-004 names, just with a different literal hostname.
**Do this instead:** Use the fully-qualified `<service>.<namespace>.svc.cluster.local` form, verified resolvable from both in-cluster pods and the OrbStack host (Pattern 4).

## Sources

- `cmd/api/main.go`, `cmd/worker/main.go`, `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go`, `cmd/webhook-worker/main.go` — direct read, confirms `METRICS_ADDR`/`os.Getenv` pattern is identical and code-change-free to override — HIGH confidence
- `docker-compose.yml`, `docker-compose.e2e.yml` — direct read, confirms current port bindings, `extra_hosts` host-gateway usage, `createbucket`/`asynqmon` trust-model comments — HIGH confidence
- `internal/e2e/e2e_test.go` (lines 1-140, 340-370, 1000-1021) — direct read, confirms `0.0.0.0:0` explicit listener bind and the already-env-driven `E2E_WEBHOOK_HOST`/`E2E_S3_DIAL_ADDR` knobs — HIGH confidence
- `.github/workflows/ci.yml` — direct read, confirms 4-tier compose-only pipeline, no k8s step — HIGH confidence
- `internal/mcpserver/mcpserver.go`, `config.go`, `client.go`, `tools.go`, `cmd/mcp-server/main.go` — direct read, confirms transport-agnostic `NewServer`, one-key-per-process `Client`, and the `local_path`/`OUTPUT_DIR` contract — HIGH confidence
- `/Users/apaderin/go/pkg/mod/github.com/modelcontextprotocol/go-sdk@v1.6.1/mcp/streamable.go` — direct read of the installed module, confirms `NewStreamableHTTPHandler(getServer func(*http.Request) *mcp.Server, opts *StreamableHTTPOptions) *StreamableHTTPHandler` exists and implements `http.Handler` — HIGH confidence
- `internal/api/presets_handlers.go`, `internal/api/api.go`, `internal/api/routes.go` — direct read, confirms `PresetAdmin` is already scope-agnostic and the existing no-leak-404 discipline — HIGH confidence
- `internal/db/migrations/0001_init.sql`, `0002_client_api_keys.sql` — direct read, confirms `clients` table has no spare role/flag column — HIGH confidence
- `internal/metrics/queue_collector.go` — direct read, confirms the `octoconv_queue_depth{queue,state}` Prometheus gauge KEDA's Prometheus scaler would query — HIGH confidence
- `internal/storage/storage.go` (`Client.Ping`) — direct read, confirms a reusable ping primitive for worker `/healthz` — HIGH confidence
- `.planning/PROJECT.md`, `.planning/seeds/SEED-004.md` — direct read, milestone goal framing and the four named landmines — HIGH confidence
- WebSearch: "OrbStack Kubernetes service domain resolvable from macOS host LoadBalancer" (docs.orbstack.dev/kubernetes) — confirms `cluster.local` domains and `*.k8s.orb.local` LoadBalancer access resolve directly from the Mac host — MEDIUM-HIGH confidence (official docs summary, not independently tested against this repo's actual chart)

---
*Architecture research for: OctoConv v1.6 (Kubernetes & KEDA)*
*Researched: 2026-07-14*
