# Phase 27: KEDA Autoscaling - Pattern Map

**Mapped:** 2026-07-16
**Files analyzed:** 16
**Analogs found:** 14 / 16

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `cmd/api/main.go` (modify) | config/entry-point | event-driven (metrics registration) | `cmd/worker/main.go` (collector registration block, lines 87-89) | exact — same registration call, different host process |
| `cmd/worker/main.go` (modify: remove collector, add ShutdownTimeout) | config/entry-point | event-driven | itself (before/after diff) | exact |
| `cmd/document-worker/main.go` (modify: remove collector, add ShutdownTimeout) | config/entry-point | event-driven | `cmd/worker/main.go` | exact — identical shape, different queue const |
| `cmd/chromium-worker/main.go` (modify: remove collector, add ShutdownTimeout) | config/entry-point | event-driven | `cmd/worker/main.go` | exact — identical shape, different queue const |
| `cmd/webhook-worker/main.go` (modify: remove collector, add ShutdownTimeout) | config/entry-point | event-driven | `cmd/worker/main.go` | role-match — webhook-worker has extra sweeper/lock logic around the same registration block |
| `internal/metrics/queue_collector.go` | utility | transform (pull-based Collect) | itself — **reused as-is, zero changes** | exact (no-op) |
| `internal/metrics/metrics_test.go` (extend, optional) | test | unit | itself — `TestNewQueueDepthCollectorDescribe` | exact |
| `internal/e2e/e2e_test.go` (extend: metrics reachability assertion) | test | request-response / file-I/O (exec) | itself — `e2eSetup`/`postJob`/`pollUntilDone` helpers | role-match — new assertion shape (docker exec), no existing analog for the exec-based reachability check |
| `deploy/chart/octoconv/templates/scaledobject-image.yaml` (new) | config (k8s manifest) | event-driven (declarative scaler spec) | `deploy/chart/octoconv/templates/deployment-mcp-http.yaml` (gate pattern) + RESEARCH.md Pattern 3 (concrete YAML) | role-match — no existing ScaledObject in repo; gate-flag + label conventions borrowed from mcp-http |
| `deploy/chart/octoconv/templates/scaledobject-document.yaml` (new) | config (k8s manifest) | event-driven | same as image variant | role-match |
| `deploy/chart/octoconv/templates/scaledobject-html.yaml` (new) | config (k8s manifest) | event-driven | same as image variant | role-match |
| `deploy/chart/octoconv/templates/prometheus.yaml` (new: Deployment+ConfigMap+Service) | config (k8s manifest) | batch/pull (scrape) | `deploy/chart/octoconv/templates/redis.yaml` (Deployment+Service combo) + `configmap.yaml` (ConfigMap shape) | role-match — stateless single-replica server, same combined-file convention as redis.yaml |
| `deploy/chart/octoconv/templates/networkpolicy-metrics.yaml` (modify) | config (k8s manifest) | request-response (ingress rule) | itself — existing `:8090` rule in the same file is the template to mirror for the new `:9090` rule | exact |
| `deploy/chart/octoconv/templates/service-api.yaml` (modify: add metrics port) | config (k8s manifest) | request-response | itself — existing `http` port block | exact |
| `deploy/chart/octoconv/values.yaml` / `values-local.yaml` (modify: `keda.*`, `prometheus.*` blocks) | config | — | existing `mcpHttp:` / `e2e:` blocks (enabled-flag + per-service config shape) | exact |
| Gate script (new, e.g. `scripts/keda-gate.sh` or extends live-gate transcript flow) | test/script | request-response (helm/kubectl orchestration) | `scripts/presets-acceptance.sh` (bash gate-script conventions) + `.planning/phases/24-helm-chart-core/24-03-SUMMARY.md` (k8s live-gate command sequence, no script file exists yet) | role-match — no existing **k8s** gate script; compose gate script gives bash conventions, 24-03/25-03 summaries give the k8s command sequence |

## Pattern Assignments

### `cmd/api/main.go` (config/entry-point, event-driven)

**Analog:** `cmd/worker/main.go` (collector registration, lines 87-89) — this is a **relocation**, not a new pattern; the exact call moves from four files into this one.

**Imports to add** (mirrors `cmd/worker/main.go:16-24`):
```go
"github.com/hibiken/asynq"
"github.com/prometheus/client_golang/prometheus"

"github.com/apaderin/octoconv/internal/metrics"
```
`cmd/api/main.go` already imports `promhttp` and `queue` (lines 16, 25) — do not re-import.

**Core registration pattern** (source: `cmd/worker/main.go:87-89`, generalize to all 4 queues per D-02):
```go
// Register the queue-depth collector so /metrics reports per-queue task
// counts by state (OBS-01); read-only, pull-based on scrape.
prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueWebhook))
```

**Insertion point:** `cmd/api/main.go` already has `redisOpt` in scope at line 73-76 (built for the `/healthz` Redis pinger). Insert the registration anywhere in the synchronous startup section after that, before `<-ctx.Done()` (line 156) — e.g. directly after the `rdb := redis.NewClient(...)` block, since `Collect()` is pull-based/lazy.

**Logging convention** (if a startup log line is added, follow `cmd/api/main.go:130`/`150`'s emoji-prefixed style — `📊` is already used for the metrics listener log at line 150, do not duplicate it for the collector itself unless genuinely useful).

**Discretion (RESEARCH.md, "Registration Relocation Mechanics"):** none of the four workers call `Inspector.Close()` today (grep-confirmed) — match that precedent in `cmd/api/main.go` too (skip an explicit `Close()`), unless the planner prefers a 3-line `defer` for correctness; either is acceptable, but state the choice explicitly in the plan.

---

### `cmd/worker/main.go`, `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go`, `cmd/webhook-worker/main.go` (config/entry-point, event-driven)

**Analog:** each other — all four share the identical registration-removal shape; `cmd/worker/main.go` is the canonical reference, the other three are read-confirmed structurally identical (line numbers below from RESEARCH.md, re-verify at edit time).

**Removal pattern** (exact line, `cmd/worker/main.go:87-89`):
```go
// Register the queue-depth collector so /metrics reports per-queue task
// counts by state (OBS-01); read-only, pull-based on scrape.
prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage))
```
Remove this exact 3-line block (comment + call) from all four files. Corresponding lines (grep-confirmed today):
- `cmd/worker/main.go:87-89` — `queue.QueueImage`
- `cmd/document-worker/main.go:92-94` — `queue.QueueDocument`
- `cmd/chromium-worker/main.go:84-86` — `queue.QueueHTML`
- `cmd/webhook-worker/main.go:112-114` — `queue.QueueWebhook`

**Post-removal import cleanup:** `prometheus` and `internal/metrics` imports become unused in `cmd/worker/main.go`, `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go` (confirmed — neither package is referenced elsewhere in those files). `cmd/webhook-worker/main.go` needs a recheck at edit time (more imports overall) but the same pattern applies if nothing else in the file uses `metrics`/`prometheus`. `promhttp` and the `/metrics` HTTP listener stay untouched in every worker — only the collector registration moves.

**Recommended addition (Pattern 2, not a locked CONTEXT.md decision — flag explicitly to the planner): per-class `asynq.Config.ShutdownTimeout`**

Source: `cmd/worker/main.go:81-85` (current `asynq.NewServer` call, no `ShutdownTimeout` set):
```go
srv := asynq.NewServer(redisOpt, asynq.Config{
    Concurrency:    envInt("WORKER_CONCURRENCY", 4),
    Queues:         map[string]int{queue.QueueImage: 4},
    RetryDelayFunc: queue.RetryDelayFunc,
})
```
Recommended change (adds one field, reuses the existing `envDuration` helper already defined in the same file):
```go
srv := asynq.NewServer(redisOpt, asynq.Config{
    Concurrency:     envInt("WORKER_CONCURRENCY", 4),
    Queues:          map[string]int{queue.QueueImage: 4},
    RetryDelayFunc:  queue.RetryDelayFunc,
    ShutdownTimeout: envDuration("ENGINE_TIMEOUT", 120*time.Second) + 10*time.Second,
})
```
Mirror for `cmd/document-worker/main.go` (`DOCUMENT_ENGINE_TIMEOUT`), `cmd/chromium-worker/main.go` (`HTML_ENGINE_TIMEOUT`); `cmd/webhook-worker/main.go` uses a flat `30*time.Second` per RESEARCH.md (its per-attempt timeout is not one clean env var). Each worker's `asynq.NewServer(...)` call site (`cmd/worker/main.go:81`, `cmd/document-worker/main.go:86`, `cmd/chromium-worker/main.go:78`, `cmd/webhook-worker/main.go:106`) is the exact analog for all four — same struct-literal shape, different `Queues`/`Concurrency` values already.

---

### `internal/metrics/queue_collector.go`

**No changes.** Read the full file (46 lines) for reference — it is already variadic over queue names (`func NewQueueDepthCollector(inspector *asynq.Inspector, queues ...string) prometheus.Collector`), so registering it with all 4 queue names from `cmd/api/main.go` requires zero collector-code changes. This is the single reusable asset the whole KEDA-01 half depends on.

---

### `internal/metrics/metrics_test.go` (test, unit — D-03 unit-test half)

**Analog:** itself — `TestNewQueueDepthCollectorDescribe` (lines 48-63) is the exact existing pattern for testing this collector.

```go
// Source: internal/metrics/metrics_test.go:48-63 — existing pattern.
// A new test proving "api's registry exposes octoconv_queue_depth for all 4
// queues" (D-03) should follow this exact shape, but assert on Collect()
// output (queue label values), not just Describe()'s descriptor count.
func TestNewQueueDepthCollectorDescribe(t *testing.T) {
	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: "127.0.0.1:0"})
	c := NewQueueDepthCollector(inspector, "image", "webhook")

	ch := make(chan *prometheus.Desc, 10)
	c.Describe(ch)
	close(ch)
	// ...
}
```
Use `testutil.CollectAndCount`/`testutil.ToFloat64` (already imported in this file, `github.com/prometheus/client_golang/prometheus/testutil`) if the new test needs to assert `Collect()` output rather than just `Describe()`.

---

### `internal/e2e/e2e_test.go` (test, D-03 compose-E2E half — Pitfall 3, no direct analog)

**Analog:** itself — `e2eSetup(t)` (lines ~68-80) for the skip-gate convention, `e2eHTTP` client (line ~64) for HTTP calls that ARE host-reachable (api's `:8090` on `E2E_BASE_URL`).

**Gap (RESEARCH.md Pitfall 3, confirmed no existing pattern):** `METRICS_ADDR=127.0.0.1:9090` is loopback-bound inside every compose container (grep-confirmed, 6 occurrences in `docker-compose.yml`); a plain `e2eHTTP.Get("http://api:9090/metrics")` from the host-run test process will connection-refuse. `internal/e2e/e2e_test.go` has zero existing `:9090` references — this is genuinely new ground, not a re-use of an existing helper.

**Recommended mechanism (per RESEARCH.md, verify first):**
```go
// New helper, no existing analog — verify wget/curl presence in the built
// images FIRST (docker run --rm octoconv-api:dev which curl wget) before
// committing to this shape:
out, err := exec.CommandContext(ctx, "docker", "compose", "-f", "docker-compose.yml",
    "exec", "-T", "api", "wget", "-qO-", "http://localhost:9090/metrics").Output()
```
Fall back to log-line inspection (`docker compose logs api | grep '📊 metrics listening'`) combined with the `internal/metrics` unit test above, if neither `wget` nor `curl` is present in the `debian:bookworm-slim`-based runtime image. This is a **planner decision point**, not a locked pattern — flag it in the plan.

**Skip-gate convention to copy exactly** (source: `internal/e2e/e2e_test.go`, `e2eSetup`):
```go
func e2eSetup(t *testing.T) e2eConfig {
	t.Helper()
	baseURL := os.Getenv("E2E_BASE_URL")
	if baseURL == "" {
		t.Skip("E2E_BASE_URL not set; skipping E2E test")
	}
	// ...
}
```
Any new metrics-reachability test must call `e2eSetup(t)` first, same self-skip-offline convention as every other test in this file.

---

### `deploy/chart/octoconv/templates/scaledobject-{image,document,html}.yaml` (new, config/k8s manifest, event-driven)

**Analog:** No existing `ScaledObject` in this repo (KEDA is new this phase). Closest structural analogs for **conventions** (gate flag, label block, naming):

**Gate-flag pattern** (source: `deploy/chart/octoconv/templates/deployment-mcp-http.yaml:16`):
```yaml
{{- if .Values.mcpHttp.enabled }}
...
{{- end }}
```
Apply the same shape gated on `.Values.keda.enabled` (D-10 — `helm template` with the flag off must render zero ScaledObjects).

**Label/selector pattern** (source: `deploy/chart/octoconv/templates/deployment-worker.yaml:19-26`, `_helpers.tpl:25-41`):
```yaml
metadata:
  name: worker-image-scaledobject
  labels:
    {{- include "octoconv.labels" . | nindent 4 }}
    app.kubernetes.io/component: worker
```
`scaleTargetRef.name` must match the existing Deployment's literal `metadata.name` (`worker`, `document-worker`, `chromium-worker` — confirm exact names against `deployment-worker.yaml`/`deployment-document-worker.yaml`/`deployment-chromium-worker.yaml`).

**Concrete trigger YAML** (source: RESEARCH.md Pattern 3, KEDA docs-verified, illustrative for image class — mirror per class with D-05/D-06/D-08 values):
```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: worker-image-scaledobject
  namespace: {{ .Values.global.namespace }}
spec:
  scaleTargetRef:
    name: worker
  minReplicaCount: 0
  maxReplicaCount: {{ .Values.keda.image.maxReplicaCount }}   # D-06: 4
  pollingInterval: {{ .Values.keda.image.pollingInterval }}    # D-08 demo value
  cooldownPeriod: {{ .Values.keda.image.cooldownPeriod }}      # D-08 demo value
  fallback:
    failureThreshold: 3
    replicas: 1                                                 # D-07
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://prometheus.{{ .Values.global.namespace }}.svc.cluster.local:9090
        query: sum(octoconv_queue_depth{queue="image", state=~"pending|active"})
        threshold: {{ .Values.keda.image.threshold | quote }}   # D-05: "5"
```
Do NOT include `retry`/`scheduled`/`archived` in the `state=~` regex (D-04). `webhook` class gets NO ScaledObject file at all (D-09) — confirm `deployment-webhook-worker.yaml`'s existing header comment (lines 8-11) already documents this exclusion explicitly; do not create `scaledobject-webhook.yaml`.

---

### `deploy/chart/octoconv/templates/prometheus.yaml` (new, config/k8s manifest — Deployment+ConfigMap+Service combined file)

**Analog:** `deploy/chart/octoconv/templates/redis.yaml` (combined Deployment+Service in one file, stateless-adjacent single-replica pattern) + `deploy/chart/octoconv/templates/configmap.yaml` (ConfigMap shape).

**Combined-file convention** (source: `redis.yaml:1-62`):
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
  labels:
    {{- include "octoconv.labels" . | nindent 4 }}
    app.kubernetes.io/component: redis
spec:
  replicas: 1
  selector:
    matchLabels:
      {{- include "octoconv.selectorLabels" (dict "component" "redis" "root" $) | nindent 6 }}
  template:
    metadata:
      labels:
        {{- include "octoconv.labels" . | nindent 8 }}
        {{- include "octoconv.selectorLabels" (dict "component" "redis" "root" $) | nindent 8 }}
    spec:
      containers:
        - name: redis
          image: {{ .Values.redis.image | quote }}
          imagePullPolicy: {{ .Values.image.pullPolicy }}
          ports:
            - name: redis
              containerPort: 6379
---
apiVersion: v1
kind: Service
metadata:
  name: redis
  ...
```
Wrap the whole file (Deployment + ConfigMap + Service, all 3 objects) in `{{- if .Values.prometheus.enabled }} ... {{- end }}` (mcp-http gate pattern). Prometheus's pod does NOT get `octoconv.io/tier: app` (it is a scrape SOURCE, not a target the metrics NetworkPolicy should restrict as if it were an app pod — RESEARCH.md Pitfall 1 explicit warning) — give it its own distinct label, e.g. `app.kubernetes.io/component: prometheus`, matching mcp-http's precedent of NOT carrying the tier label (source: `deployment-mcp-http.yaml:7-10` comment).

**ConfigMap shape** (source: `configmap.yaml:1-43`) — mount a `prometheus.yml` scrape config as a ConfigMap, volume-mounted into the container (standard Prometheus deployment shape, the one exception to this project's env-only config convention, per RESEARCH.md).

**Service** — mirror `redis.yaml`'s Service block (ClusterIP, literal un-prefixed name e.g. `prometheus`, matching the FQDN convention already used by `postgres`/`redis`/`minio`/`api`, per D-06 precedent cited in RESEARCH.md's open question 3).

---

### `deploy/chart/octoconv/templates/networkpolicy-metrics.yaml` (modify — Pitfall 1 fix)

**Analog:** itself — the existing `:8090` ingress rule (source: `networkpolicy-metrics.yaml:51-57`) is the exact template to mirror for the corrected `:9090` rule.

**Current (broken for this phase's D-11 decision) rule to replace:**
```yaml
# networkpolicy-metrics.yaml:43-50 — selects a "monitoring" namespace that
# will never exist under D-11 (Prometheus ships IN this chart's own
# octoconv namespace, not a separate monitoring namespace).
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: monitoring
      ports:
        - protocol: TCP
          port: 9090
```

**Replacement (combine namespaceSelector + podSelector in one `from` entry, ANDed — mirrors the `:8090` rule's namespaceSelector shape at lines 51-57, plus a new podSelector matching Prometheus's own component label):**
```yaml
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: {{ .Values.global.namespace }}
        podSelector:
          matchLabels:
            app.kubernetes.io/component: prometheus
      ports:
        - protocol: TCP
          port: 9090
```
Do NOT give Prometheus's pod `octoconv.io/tier: app` — see Pitfall 1's full explanation in RESEARCH.md (it would make Prometheus itself subject to this same policy's `:9090` ingress restriction as a *target*, which is wrong; it's a scrape *source* here).

---

### `deploy/chart/octoconv/templates/service-api.yaml` (modify — add metrics port)

**Analog:** itself — existing `http` port block is the exact pattern to duplicate for the new `metrics` port.

**Current** (source: `service-api.yaml:19-22`):
```yaml
  ports:
    - name: http
      port: 8090
      targetPort: 8090
```
**Add** (mirrors `deployment-worker.yaml:40-42`'s `containerPort: 9090` naming convention, `name: metrics`):
```yaml
  ports:
    - name: http
      port: 8090
      targetPort: 8090
    - name: metrics
      port: 9090
      targetPort: 9090
```

---

### `deploy/chart/octoconv/values.yaml` / `values-local.yaml` (modify — `keda.*`, `prometheus.*` blocks)

**Analog:** existing `mcpHttp:` block (values.yaml:86-94, enabled-flag + per-service config shape) and `e2e:` block (values.yaml:119-121, minimal enabled-flag pattern).

**Pattern to copy** (source: `values.yaml:86-94`):
```yaml
mcpHttp:
  enabled: true
  image:
    repository: "octoconv-mcp-http"
  replicas: 1
  addr: ":8070"
  baseURL: "http://api.octoconv.svc.cluster.local:8090"
```
New blocks in `values.yaml` (default `enabled: false` per D-10):
```yaml
keda:
  enabled: false
  image:
    threshold: "5"          # example per-class key nesting
    maxReplicaCount: 4
    pollingInterval: 5
    cooldownPeriod: 60
  document:
    threshold: "1"
    maxReplicaCount: 2
    pollingInterval: 15
    cooldownPeriod: 120
  html:
    threshold: "2"
    maxReplicaCount: 2
    pollingInterval: 10
    cooldownPeriod: 90

prometheus:
  enabled: false
  image: "prom/prometheus:v3.13.1"
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"
    limits:
      cpu: "500m"
      memory: "256Mi"
```
`values-local.yaml` overlay (source: this file's existing shape is dev-credential-only today — new `keda.enabled: true` / `prometheus.enabled: true` lines follow the same top-level-key-override convention Helm applies when layering `-f values.yaml -f values-local.yaml`).

---

### Gate script (new — extends Phase 24/25 install flow)

**Analog:** `scripts/presets-acceptance.sh` for bash gate-script conventions (shebang, `set -euo pipefail`, `assert_eq` helpers, loud FAIL/PASS echo lines) — this is a **compose**-based gate script, so its HTTP/DB-assertion helpers do not transfer directly, but its structural conventions do.

**Bash conventions to copy** (source: `scripts/presets-acceptance.sh:1-30`):
```bash
#!/usr/bin/env bash
# <script-name>.sh -- Phase <N> live hard gate.
#
# Proves, against a REAL <compose|k8s> stack, that: ...
#
# This script's exit code IS the gate: any failed assertion aborts non-zero
# (set -e) with a loud FAIL message.
set -euo pipefail

cd "$(dirname "$0")/.."
```

**k8s command sequence to copy** (no existing script file — source: `.planning/phases/24-helm-chart-core/24-03-SUMMARY.md` live-gate transcript, and `.planning/phases/25-mcp-streamable-http/25-03-SUMMARY.md`):
```
helm install (WITHOUT --wait, per 24-03 decision — createbucket post-install
  hook chicken-egg with app readiness) → kubectl wait for each Deployment
  Available → assertion commands (kubectl get --raw, kubectl exec, curl/port-forward)
  → helm upgrade idempotence check → helm uninstall (teardown).
```
This phase's new steps to layer on top (D-11, D-12, D-13):
1. `helm repo add kedacore https://kedacore.github.io/charts && helm repo update`
2. `helm install keda kedacore/keda --namespace keda --create-namespace --version <pinned>` (idempotent — re-verify version live per RESEARCH.md "always re-check immediately before the live gate")
3. Poll `kubectl get apiservice v1beta1.external.metrics.k8s.io -o jsonpath='{.status.conditions}'` for `Available: True` (not a fixed sleep)
4. `helm install octoconv ...` with `keda.enabled=true prometheus.enabled=true` (values-local layered)
5. Per-class 0→1 proof: submit one real job of that class's type via the API (same fixtures as `internal/e2e`), then poll the target Deployment's replica count
6. `kubectl get scaledobject <name> -n octoconv -o jsonpath='{.status.externalMetricNames}'` — discover the metric name live, do NOT hardcode (Pitfall 5)
7. `kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/octoconv/<discovered-name>?labelSelector=..."` while the target Deployment is verifiably at 0 replicas (SC1)
8. webhook-worker replica assertion: `kubectl get deployment webhook-worker -n octoconv -o jsonpath='{.spec.replicas}'` must read `2` throughout, with no `scaledobject` resource referencing it
9. Teardown: `helm uninstall keda -n keda`, `helm uninstall octoconv -n octoconv` (OrbStack discipline, D-13 — sequential pre-builds, compose/k8s stacks never hot simultaneously)

## Shared Patterns

### Collector relocation (single source of truth)
**Source:** `internal/metrics/queue_collector.go` (unchanged) + `cmd/worker/main.go:87-89` (registration call shape)
**Apply to:** `cmd/api/main.go` (new registration site), all four worker mains (removal sites)

### Feature-gate via `.Values.<component>.enabled`
**Source:** `deploy/chart/octoconv/templates/deployment-mcp-http.yaml:16`, `job-e2e.yaml:1`, `networkpolicy-mcp-http.yaml:19`
**Apply to:** `scaledobject-*.yaml` (×3, gated `keda.enabled`), `prometheus.yaml` (gated `prometheus.enabled`)
```yaml
{{- if .Values.<key>.enabled }}
...
{{- end }}
```

### Chart label/selector helpers
**Source:** `deploy/chart/octoconv/templates/_helpers.tpl:25-55` (`octoconv.labels`, `octoconv.selectorLabels`, `octoconv.commonEnv`)
**Apply to:** all new templates (`prometheus.yaml`, `scaledobject-*.yaml`) — use `include "octoconv.labels" .` and `include "octoconv.selectorLabels" (dict "component" "<name>" "root" $)` exactly as every existing template does. Do NOT add `octoconv.io/tier: app` to Prometheus's pod (see Pitfall 1 warning above).

### Env-only config, no config files (Go layer)
**Source:** `cmd/worker/main.go:137-153` (`envInt`, `envDuration`, `firstField` helpers, duplicated per-binary per existing convention)
**Apply to:** any new env-driven knob in the four worker mains (e.g. `ShutdownTimeout` derivation reuses the existing `envDuration` helper already in each file — no new helper needed).

### values.yaml enabled-flag + per-service block convention
**Source:** `deploy/chart/octoconv/values.yaml:86-94` (`mcpHttp:`), `:119-121` (`e2e:`)
**Apply to:** new `keda:` and `prometheus:` blocks in `values.yaml` (default `enabled: false`) and `values-local.yaml` (`enabled: true` overlay).

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `scaledobject-{image,document,html}.yaml` | config (k8s manifest) | event-driven | No `ScaledObject` (or any KEDA CRD) exists anywhere in this repo yet — first use of the `keda.sh/v1alpha1` API. Chart conventions (gating, labels) borrowed from `deployment-mcp-http.yaml`; the trigger/spec body itself must come from RESEARCH.md's KEDA-docs-verified YAML (Pattern 3), not a repo analog. |
| Compose-E2E metrics reachability assertion (`internal/e2e/e2e_test.go` addition) | test | file-I/O (exec into container) | No existing e2e test shells out to `docker compose exec`/inspects container logs — grep-confirmed zero `:9090` references in the current suite (RESEARCH.md Pitfall 3). This is genuinely new ground; the planner must pick and verify a concrete mechanism (see Pattern Assignments above) before writing the test. |

## Metadata

**Analog search scope:** `cmd/{api,worker,document-worker,chromium-worker,webhook-worker}/main.go`, `internal/metrics/`, `internal/e2e/`, `deploy/chart/octoconv/{values.yaml,values-local.yaml,templates/*.yaml,_helpers.tpl}`, `scripts/*.sh`, `.planning/phases/{24-helm-chart-core,25-mcp-streamable-http}/*.md`
**Files scanned:** ~30 (full reads on 16 chart/Go files, targeted greps on the rest)
**Pattern extraction date:** 2026-07-16

## PATTERN MAPPING COMPLETE

**Phase:** 27 - KEDA Autoscaling
**Files classified:** 16
**Analogs found:** 14 / 16

### Coverage
- Files with exact analog: 8 (cmd/api/main.go relocation, 4 worker mains removal, queue_collector.go no-op, metrics_test.go, networkpolicy-metrics.yaml, service-api.yaml)
- Files with role-match analog: 6 (prometheus.yaml, scaledobject-*.yaml ×3, values.yaml/values-local.yaml, gate script)
- Files with no analog: 2 (scaledobject spec body itself — first KEDA CRD use in repo; compose-E2E exec-based metrics assertion — first use of this reachability mechanism)

### Key Patterns Identified
- Collector relocation is a pure move: `internal/metrics/queue_collector.go` needs zero code changes; only the `prometheus.MustRegister(metrics.NewQueueDepthCollector(...))` call site moves from 4 worker mains into `cmd/api/main.go`, expanded to register all 4 queue names in one call (D-02).
- Every new/modified chart template follows the same three conventions already established by Phase 24/25: `{{- if .Values.<x>.enabled }}` gating, `octoconv.labels`/`octoconv.selectorLabels` helpers, and literal un-prefixed Service names for in-cluster FQDN resolution.
- Prometheus's pod must NOT carry `octoconv.io/tier: app` (same precedent as mcp-http) — it is a scrape source, not a target the metrics NetworkPolicy should restrict.
- The `networkpolicy-metrics.yaml` `:9090` rule fix and the `service-api.yaml` metrics-port addition are both surgical, single-block edits to existing files — no new file needed for either.
- The KEDA ScaledObject YAML itself and the compose-E2E metrics-reachability test are the two genuinely novel pieces of this phase — both must be built from RESEARCH.md's verified specifics (not a repo analog), and both carry an explicit "verify before committing" step (KEDA external-metric-name discovery; wget/curl presence in runtime images).

### File Created
`/Users/apaderin/dev/octoconv/.planning/phases/27-keda-autoscaling/27-PATTERNS.md`

### Ready for Planning
Pattern mapping complete. Planner can now reference analog patterns in PLAN.md files.
