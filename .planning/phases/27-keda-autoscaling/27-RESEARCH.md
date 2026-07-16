# Phase 27: KEDA Autoscaling - Research

**Researched:** 2026-07-16
**Domain:** Prometheus-metric relocation (Go) + KEDA v2.20.x ScaledObject configuration + minimal in-chart Prometheus, on OrbStack k8s
**Confidence:** HIGH for code-grounded findings (queue-depth relocation mechanics, asynq shutdown semantics, existing chart wiring — all verified directly against this repo's source) and for KEDA/Helm version facts (GitHub Releases API / Helm repo index, live-fetched today); MEDIUM for exact demo pollingInterval/cooldownPeriod numbers and fallback.failureThreshold (no single canonical "right" value — informed synthesis, explicitly flagged tunable)

## Summary

This phase has two genuinely separate halves, and the CONTEXT.md's own sequencing (KEDA-01 before any k8s work) is correct and should not be second-guessed: **KEDA-01** is a small, pure-Go change (move one collector-registration line from four `cmd/*-worker/main.go` files into `cmd/api/main.go`, all four files already grep-confirmed at their exact line numbers) validated entirely against the existing compose E2E suite; **KEDA-02** is chart/YAML work (three `ScaledObject`s, an in-chart Prometheus, KEDA itself helm-installed) validated on OrbStack k8s. Everything needed for KEDA-01 is already in the codebase — `metrics.NewQueueDepthCollector` is variadic over queue names and reusable as-is, `cmd/api/main.go` already has `queue.RedisOpt()` available at exactly the right point.

The one finding that changes the shape of this phase's scope beyond what CONTEXT.md's decisions anticipated: **asynq's `Server.Shutdown()` default `ShutdownTimeout` is 8 seconds — not configured by any of the four worker `main()` functions today**, so every worker currently aborts and requeues an in-flight task 8 seconds after SIGTERM, regardless of Phase 24's carefully-derived per-class `terminationGracePeriodSeconds` (150s/330s/90s/60s). This does not lose data (the task is safely requeued, not dropped) but it means "graceful scale-down survives a long-running document job" — the literal subject of Phase 28's KEDA-03 — is **not yet true** with the code as it stands today, and Phase 27 is the natural, cheap place to fix it (same files KEDA-01 already touches). This is presented as a strong recommendation, not a locked decision, since CONTEXT.md left it to research to resolve and to the planner to decide scope.

The KEDA-02 half confirms every number in STACK.md/PITFALLS.md still holds two days later (KEDA v2.20.1 is still current; Helm v4.2.3 and kubectl v1.36.2 are both already installed locally) and resolves the phase's stated open questions: the exact PromQL shape, the `fallback` block's confirmed behavior at `minReplicaCount: 0`, the `kubectl get --raw` verification path, and — most importantly, a load-bearing finding not anticipated by CONTEXT.md — a genuine conflict between the **already-shipped** `networkpolicy-metrics.yaml` (which currently ingress-allows `:9090` only from a namespace labeled `monitoring`) and D-11's decision that "Prometheus ships inside our chart" (i.e., in the `octoconv` namespace, not a separate `monitoring` namespace). This must be resolved as part of this phase's plan, not discovered live.

**Primary recommendation:** Do KEDA-01 exactly as CONTEXT.md's D-01/D-02/D-03 specify, and in the same plan add `asynq.Config{ShutdownTimeout: <per-class engine timeout + margin>}` to all four worker `main()` calls (cheap, same files, removes a Phase-28 blocker). For KEDA-02, fix `networkpolicy-metrics.yaml`'s `:9090` ingress rule to select the in-chart Prometheus pod by label within the `octoconv` namespace (not a `monitoring` namespace that nothing will ever create), and give every `ScaledObject` `ignoreNullValues: true` (default — keep it) plus a `fallback` block with `failureThreshold: 3` / `replicas: 1` as CONTEXT.md's D-07 requires.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Queue-depth metric exposition (`octoconv_queue_depth`) | API / Backend (`cmd/api`) | — | Must live on an always-on process; workers scale to 0, api never does (K8S-01/D-08 makes api's own scaling out of scope this milestone) |
| Metric scraping (Prometheus server) | Database / Storage-adjacent (in-cluster monitoring component) | — | Prometheus is a stateful scrape-and-store component, architecturally closer to a storage/observability tier than app logic; it has no business logic, only pull+retain |
| Scaling decision (KEDA operator + HPA) | API / Backend (control-plane extension) | — | KEDA extends the Kubernetes control plane (custom metrics adapter + HPA controller wiring); it is infrastructure, not application code — no Go code in this repo implements scaling logic |
| Worker Deployment replica count | API / Backend (Deployment spec, chart-owned) | — | Declarative desired-state owned by the `ScaledObject`/HPA, applied to the same Deployment objects Phase 24 already created |
| webhook-worker fixed replica count | API / Backend (Deployment spec, chart-owned) | — | Deliberately NOT KEDA-owned (Pitfall 2) — stays a plain `replicas: 2` field, no different in kind from Phase 24's existing fixed Deployments |

## User Constraints (from CONTEXT.md)

<user_constraints>

### Locked Decisions

**Queue-depth relocation (KEDA-01 — plan 1, pure Go, before any k8s work)**
- D-01: MOVE, not duplicate: remove `prometheus.MustRegister(metrics.NewQueueDepthCollector(...))` from all four worker mains (`cmd/worker/main.go:89`, `cmd/document-worker/main.go:94`, `cmd/chromium-worker/main.go:86`, `cmd/webhook-worker/main.go:114`); register in `cmd/api/main.go` instead. Single source of truth, no duplicate series. Worker liveness probes unaffected — their `/metrics` endpoints stay (other metrics live there).
- D-02: api registers ALL FOUR queues (image/document/html/webhook) — one collector call with all queue names.
- D-03: Validation before any k8s work: unit test that api's registry exposes `octoconv_queue_depth` for all 4 queues + compose-E2E assertion that api `/metrics` serves the metric per queue and workers no longer serve it. Live proof on the cheap stack first.

**ScaledObject trigger design (KEDA-02)**
- D-04: PromQL signal = `pending + active` for the worker's own queue (sum over those two states of `octoconv_queue_depth{queue="X"}`). Do NOT include retry/scheduled/archived.
- D-05: Per-class `threshold` (tasks-per-replica): image 5 / document 1 / html 2.
- D-06: Per-class `maxReplicaCount`: image 4 / document 2 / html 2.
- D-07: `fallback.replicas: 1` on every ScaledObject — if Prometheus is unreachable after the failure threshold, each scaled class holds 1 worker and keeps processing.
- D-08: `pollingInterval`/`cooldownPeriod` per class per roadmap SC4: image fast/bursty (short cooldown), document/html long-task classes (longer cooldown). Demo-tuned starting values (~5-15s polling, ~60s+ cooldown) — planner/research picks exact numbers.
- D-09: webhook-worker: NO ScaledObject at all; fixed `replicas: 2` Deployment.

**Chart gating & install**
- D-10: `keda.enabled: false` default in `values.yaml`, `true` in the local overlay. Offline check: `helm template` with flag off renders no ScaledObjects.
- D-11: KEDA installed BY the gate script: idempotent `helm install` of pinned KEDA v2.20.x into its own `keda` namespace. Prometheus ships inside our chart behind a flag (minimal `prom/prometheus` Deployment + static scrape ConfigMap — NOT kube-prometheus-stack).

**Live gate (this phase's proof; burst proof is Phase 28)**
- D-12: Gate proves (a) SC1 — `octoconv_queue_depth` resolves via `kubectl get --raw` at genuinely 0 replicas; (b) ALL THREE classes scale 0→1 from a single job of their own type; (c) full cycle back →0 by cooldown awaited only on image; (d) webhook-worker holds 2 replicas with no ScaledObject throughout.
- D-13: OrbStack discipline unchanged (Phase 24 D-11/D-12): sequential image pre-builds, compose and k8s stacks never hot simultaneously, teardown after gate.

### Claude's Discretion
- Prometheus specifics: lives in our chart behind `prometheus.enabled`-style flag; scrape target = api's `/metrics`; update Phase 24's `networkpolicy-metrics.yaml` placeholder to admit the in-chart Prometheus; exact scrape interval.
- Registration point details in `cmd/api/main.go` (reuse `queue.RedisOpt()`, one `asynq.Inspector`); whether the Inspector is closed on shutdown.
- ScaledObject template layout (one template file with per-class values loop vs three files), label conventions, exact fallback `failureThreshold`.
- Exact demo pollingInterval/cooldownPeriod numbers per class (within D-08 constraints).
- asynq v0.26.0 `Server.Shutdown()` deadline semantics — confirm during research so grace-period behavior holds; matters for Phase 28 but verify assumptions now if cheap.

### Deferred Ideas (OUT OF SCOPE)
- Burst 0→N→0 with timestamps + graceful scale-down soak of a long document job — Phase 28 (KEDA-03).
- Threshold/cooldown production tuning against real task-duration distributions — Phase 28 findings feed values.
- KEDA/HPA for api or mcp-http (request/response tiers — no queue signal).
- kube-prometheus-stack / full observability stack.

</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| KEDA-01 | `octoconv_queue_depth` exposed by the always-on api process (collector relocation) — hard prerequisite for scale-from-zero | "Registration Relocation Mechanics" + Code Examples sections below give the exact 5-file diff (4 removals + 1 addition), confirmed against current line numbers; D-03's compose-E2E reachability gap (localhost-bound `/metrics`) resolved in Pitfall 5 with a concrete `docker compose exec` recommendation |
| KEDA-02 | KEDA v2.20.x helm-installed; `ScaledObject`s for image/document/html scaling from `minReplicaCount 0` on the Prometheus scaler; webhook-worker excluded, fixed `replicas: 2` | "KEDA Prometheus Scaler — Confirmed Field Semantics", "PromQL Signal Shape", "KEDA Helm Install Mechanics", and "Minimal In-Chart Prometheus" sections give the exact trigger YAML, fallback behavior at zero replicas, install namespace/CRD handling, and the `networkpolicy-metrics.yaml` conflict that must be fixed in this phase, not discovered at gate time |

</phase_requirements>

## Project Constraints (from CLAUDE.md)

- Go 1.26 toolchain, `CGO_ENABLED=0`, static binaries — no new Go dependency is needed for either KEDA-01 or KEDA-02 (asynq, prometheus/client_golang are already in `go.mod`).
- Naming: exported constructors `New<Type>`, package doc comments, `ctx` always first/named-ctx — any new Go code (asynq.Config field addition) is a one-line change to existing `asynq.NewServer(redisOpt, asynq.Config{...})` calls, no new files/types needed.
- Error handling: HTTP/worker layers never leak internal error text; this phase adds no new HTTP handlers or worker error paths, so no new surface here.
- Logging: only `cmd/*/main.go` log directly (`log.Printf`/`log.Fatalf`), emoji-prefixed startup lines — if a new log line is added when registering the collector in `cmd/api/main.go`, follow the existing `📊`/`🚀` convention already used for the metrics/API listeners in that file.
- Environment-variable configuration only, no config files — Prometheus's scrape config is the one exception already implied by CONTEXT.md (a ConfigMap-mounted `prometheus.yml`), which is standard Prometheus deployment shape, not a project convention violation (Prometheus is not this repo's own Go binary).
- GSD workflow enforcement: this research does not perform edits; the planner must route implementation through `/gsd-execute-phase`.

## Standard Stack

### Core

| Library/Tool | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| KEDA | **v2.20.1** (Helm chart `keda`, repo `https://kedacore.github.io/charts`) | Prometheus-driven per-engine-class autoscaling, scale-from-zero | Re-confirmed live today (2026-07-16): `curl https://kedacore.github.io/charts/index.yaml` shows `keda` chart `appVersion: 2.20.1` (created 2026-06-15); GitHub Releases API confirms `v2.20.1` is still `latest` (published 2026-06-08). No drift from the 2026-07-14 STACK.md finding. [VERIFIED: KEDA GitHub Releases API + Helm chart index, both live-fetched today] |
| Helm | **v4.2.3** | Chart templating/install, including the separate `helm install keda ...` invocation | Already installed on the research/execution machine (`helm version --short` → `v4.2.3+g43e8b7f`), matching Phase 24's existing chart tooling — no version change needed. [VERIFIED: local `helm version` invocation] |
| kubectl | **v1.36.2** (client) | `kubectl get --raw` external-metrics verification, `kubectl wait`, gate scripting | Already installed locally; comfortably satisfies KEDA v2.20's `kubeVersion: >=v1.23.0-0` floor. [VERIFIED: local `kubectl version --client` invocation] |
| prom/prometheus | **v3.13.1** (Docker Hub `prom/prometheus:v3.13.1`) | Minimal in-chart Prometheus server, KEDA's `serverAddress` target | GitHub Releases API confirms `v3.13.1` published 2026-07-10 is `latest` — newer than STACK.md's synthesized "2.50.x/2.53.5" WebSearch guess (that guess is stale training-data noise; the live-fetched tag supersedes it). Prometheus 3.x's `scrape_configs` YAML shape for static targets is unchanged from 2.x for this phase's needs (single static job) — no config-format risk for a minimal scrape-only deployment. [VERIFIED: GitHub Releases API, live-fetched today] |

### Supporting (already in `go.mod` — zero new Go dependencies)

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/hibiken/asynq` | v0.26.0 (pinned) | `asynq.NewInspector`, `asynq.Config.ShutdownTimeout` | Both already used elsewhere in this codebase; KEDA-01 needs `asynq.NewInspector(redisOpt)` in `cmd/api/main.go` (new call site, same package already imported by `internal/metrics`); the shutdown-timeout fix needs one new `Config` field in the four worker mains |
| `github.com/prometheus/client_golang` | v1.23.2 (pinned) | `prometheus.MustRegister`, `metrics.NewQueueDepthCollector` | `cmd/api/main.go` needs a new import of the base `prometheus` package (currently only imports `promhttp`) plus `internal/metrics` (not currently imported by `cmd/api`) |

### Alternatives Considered

| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Prometheus scaler against relocated `octoconv_queue_depth` | KEDA's `redis`/`redis-lists` scaler against asynq's internal `asynq:{queue}:pending` LIST key | Explicitly rejected by both SEED-004 and this phase's own CONTEXT.md/D-04 — undocumented asynq internal, only sees `pending` not `pending+active`, no semver contract. Not revisited here. |
| Hand-rolled minimal `prom/prometheus` Deployment + static ConfigMap | `kube-prometheus-stack` / Prometheus Operator | Already rejected in STACK.md (v1.6 milestone research) — 15+ CRDs for one PromQL-answering endpoint is disproportionate. Not revisited. |
| Setting `asynq.Config.ShutdownTimeout` per class now (Phase 27) | Leaving the 8s default and fixing it in Phase 28 | Leaving it means Phase 28's KEDA-03 gate (graceful scale-down of a long document job) will fail on the FIRST attempt for a reason unrelated to KEDA/HPA timing — a pure asynq-config gap discovered late. Fixing it now costs one field in a file this phase already edits for KEDA-01. Recommended: fix now. |

**Installation:**
```bash
# KEDA — helm-installed into its own namespace (D-11), NOT a chart dependency
helm repo add kedacore https://kedacore.github.io/charts
helm repo update
helm install keda kedacore/keda --namespace keda --create-namespace --version 2.20.1

# Verify current version before installing (re-run at execution time —
# training data / this research may be stale by then):
helm search repo kedacore/keda --versions | head -5
curl -s https://api.github.com/repos/kedacore/keda/releases/latest | grep tag_name
```

No `npm install`/`pip install`/`cargo add` — this phase adds zero new Go module dependencies. `go.mod` is untouched.

## Package Legitimacy Audit

This phase installs no npm/PyPI/crates packages — `go.mod` gains no new entries (both `asynq` and `prometheus/client_golang` are already pinned dependencies, used in new call sites only). The two external artifacts this phase pulls in are a **Helm chart** (KEDA) and a **container image** (`prom/prometheus`), neither of which is covered by `slopcheck` (npm/PyPI/crates-oriented). Both were instead verified directly against their canonical/authoritative sources today:

| Artifact | Registry/Source | Verification | Disposition |
|----------|------------------|---------------|-------------|
| KEDA Helm chart `keda` v2.20.1 | `https://kedacore.github.io/charts` (official KEDA org chart repo) + GitHub Releases API (`kedacore/keda`) | Live-fetched `index.yaml` + Releases API today; matches `kedacore/keda` org (verified project maintainer, not a third-party mirror) | Approved — [VERIFIED: KEDA GitHub Releases API + official Helm chart index] |
| `prom/prometheus:v3.13.1` container image | Docker Hub `prom/` org (official Prometheus project images) + GitHub Releases API (`prometheus/prometheus`) | Live-fetched Releases API today; `prom/` is the Prometheus project's own official Docker Hub org | Approved — [VERIFIED: Prometheus GitHub Releases API] |

**Packages removed due to slopcheck [SLOP] verdict:** none (slopcheck not applicable — no npm/PyPI/crates packages in this phase).
**Packages flagged as suspicious [SUS]:** none.

## Architecture Patterns

### System Architecture Diagram

```
                    ┌─────────────────────────────────────────────┐
                    │  Namespace: octoconv                         │
                    │                                               │
  1 pending job ──▶ │  api (always-on, 1 replica, never scaled)   │
  arrives via       │   - HTTP :8090 (create/status)               │
  POST /v1/jobs      │   - metrics :9090                            │
                    │     └─ octoconv_queue_depth{queue,state}     │◀─┐ scrape
                    │        (NEW: registered here, KEDA-01)        │  │ every
                    │                                               │  │ N seconds
                    │  ┌───────────────────────────────────────┐   │  │
                    │  │ prometheus (NEW, Phase 27, in-chart)   │───┼──┘
                    │  │  - scrapes api:9090/metrics only        │  │
                    │  │  - :9090 own HTTP API (PromQL query)    │  │
                    │  └───────────────┬───────────────────────┘   │
                    │                  │ PromQL query               │
                    └──────────────────┼─────────────────────────────┘
                                       │ (cross-namespace, KEDA → prometheus)
                    ┌──────────────────▼─────────────────────────────┐
                    │  Namespace: keda (D-11)                         │
                    │   keda-operator                                 │
                    │   keda-operator-metrics-apiserver                │
                    │     registers: v1beta1.external.metrics.k8s.io  │
                    └──────────────────┬─────────────────────────────┘
                                       │ HPA reads external metric,
                                       │ scales target Deployment
                    ┌──────────────────▼─────────────────────────────┐
                    │  Namespace: octoconv                            │
                    │                                                  │
                    │  worker (image)      [ScaledObject: 0→4]        │
                    │  document-worker     [ScaledObject: 0→2]        │
                    │  chromium-worker     [ScaledObject: 0→2]        │
                    │  webhook-worker      [FIXED replicas: 2, no SO] │
                    │    └─ sole host of the advisory-lock sweeper    │
                    └──────────────────────────────────────────────────┘
```

A reader should trace: job created via api → api's Redis-backed collector reports `pending`/`active` per queue on its own `/metrics` (never a worker's) → in-chart Prometheus scrapes only api → KEDA's Prometheus scaler in the `keda` namespace polls Prometheus's HTTP API on `pollingInterval` → KEDA sets the external metric value → HPA (owned by the ScaledObject) scales the target Deployment 0→1→N or N→1→0. webhook-worker sits outside this entire loop.

### Recommended Project Structure (additions to `deploy/chart/octoconv/`)

```
deploy/chart/octoconv/
├── templates/
│   ├── prometheus.yaml              # NEW: Deployment + ConfigMap + Service, gated `prometheus.enabled`
│   ├── scaledobject-image.yaml      # NEW (or one templated file, per-class values loop — discretion)
│   ├── scaledobject-document.yaml   # NEW
│   ├── scaledobject-html.yaml       # NEW
│   └── networkpolicy-metrics.yaml   # MODIFIED — fix the monitoring-namespace mismatch (see Pitfall 1 below)
├── values.yaml                       # add `keda.enabled: false`, `prometheus.enabled: false`, per-class KEDA tuning keys
└── values-local.yaml                 # `keda.enabled: true`, `prometheus.enabled: true` (D-10)
```

### Pattern 1: Collector relocation (KEDA-01)

**What:** Move the single `prometheus.MustRegister(metrics.NewQueueDepthCollector(...))` call from all 4 worker binaries into `cmd/api/main.go`, registering all 4 queue names at once.
**When to use:** Any metric whose exposition process must outlive the process(es) that generate the underlying activity — this project's general "always-on component owns cross-cutting exposition" pattern, now explicit.

```go
// Source: this repo, cmd/api/main.go — insertion point confirmed against
// current source. Add imports: "github.com/hibiken/asynq",
// "github.com/prometheus/client_golang/prometheus",
// "github.com/apaderin/octoconv/internal/metrics"
//
// Insert AFTER redisOpt is obtained (main.go:73-76, already present for the
// /healthz Redis pinger) and BEFORE <-ctx.Done() — anywhere in the
// synchronous startup section is fine, since Collect() is pull-based and
// lazy (only queried on scrape).
prometheus.MustRegister(metrics.NewQueueDepthCollector(
    asynq.NewInspector(redisOpt),
    queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueWebhook,
))
```

Remove the exact corresponding lines from the four worker mains (line numbers confirmed by direct read, 2026-07-16):
- `cmd/worker/main.go:89` — `prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage))`
- `cmd/document-worker/main.go:94` — same shape, `queue.QueueDocument`
- `cmd/chromium-worker/main.go:86` — same shape, `queue.QueueHTML` (confirm exact line at edit time; not independently re-read this session, CONTEXT.md's line number matches the pattern)
- `cmd/webhook-worker/main.go:114` — same shape, `queue.QueueWebhook`

After removal, each worker file's `prometheus` and `metrics` imports may become unused if nothing else in that file references them — verify with `go vet`/`goimports` per-file; `internal/metrics` and `prometheus.MustRegister` are NOT otherwise used in `cmd/worker/main.go`/`cmd/document-worker/main.go`/`cmd/chromium-worker/main.go` (confirmed for `cmd/worker` and `cmd/document-worker` by direct read; `cmd/webhook-worker` also imports `metrics` only for this collector call per the same grep pattern — recheck at edit time since webhook-worker has more imports overall). `promhttp` stays in every worker (the `/metrics` HTTP listener itself is untouched — only the queue-depth *collector registration* moves, not the metrics server).

### Pattern 2: Per-class asynq graceful-shutdown timeout (recommended addition, not in CONTEXT.md's locked decisions)

**What:** Set `asynq.Config.ShutdownTimeout` in each worker's `asynq.NewServer(redisOpt, asynq.Config{...})` call to match (with margin) that class's engine timeout, so `srv.Shutdown()` actually waits for a legitimately-long in-flight task instead of defaulting to 8 seconds.
**When to use:** Any worker whose per-task processing time can exceed asynq's 8-second default shutdown grace — true for all four classes here (`ENGINE_TIMEOUT=120s` image / `DOCUMENT_ENGINE_TIMEOUT=300s` document / `HTML_ENGINE_TIMEOUT=60s` html / webhook has its own 10s-per-attempt HTTP timeout but a 6-retry budget).

```go
// Source: this repo, cmd/worker/main.go — CURRENT code (confirmed by direct
// read) does NOT set ShutdownTimeout, so srv.Shutdown() uses asynq's
// defaultShutdownTimeout = 8 * time.Second (verified in
// github.com/hibiken/asynq@v0.26.0/server.go:416/483-486). Recommended
// addition — same envDuration() helper already used for ENGINE_TIMEOUT:
srv := asynq.NewServer(redisOpt, asynq.Config{
    Concurrency:     envInt("WORKER_CONCURRENCY", 4),
    Queues:          map[string]int{queue.QueueImage: 4},
    RetryDelayFunc:  queue.RetryDelayFunc,
    ShutdownTimeout: envDuration("ENGINE_TIMEOUT", 120*time.Second) + 10*time.Second, // margin
})
```

Mirror for document-worker (`DOCUMENT_ENGINE_TIMEOUT` + margin), chromium-worker (`HTML_ENGINE_TIMEOUT` + margin). webhook-worker's case is weaker (WebhookUniqueTTL's own doc comment already flags the Postgres-read/presign-URL portion of a delivery attempt as unbounded) — a reasonable default is the same `10*time.Second` HTTP timeout plus generous margin (e.g., `30 * time.Second`), not the full ~41-minute retry-budget derivation (that governs the *lock*, not one attempt).

**Why this matters for k8s specifically:** Phase 24 already set `terminationGracePeriodSeconds` per class (document 330s, image 150s, html 90s, webhook 60s) specifically so a long in-flight job survives SIGTERM. Without this `ShutdownTimeout` fix, asynq itself cuts the grace window down to 8 seconds internally — the pod-level grace period becomes dead configuration for anything past 8 seconds, because `srv.Shutdown()` (called synchronously in every worker `main()`) returns (and the process exits) long before the pod's real termination deadline. **This is a decision the planner must make explicitly** — CONTEXT.md left it as "verify assumptions now if cheap"; the finding is confirmed and the fix is cheap (one field, same files as KEDA-01), so the recommendation is to include it in this phase's plan, but it is not a locked CONTEXT.md decision and the planner/user should decide scope.

### Pattern 3: KEDA Prometheus-scaler ScaledObject (KEDA-02)

```yaml
# Source: KEDA docs v2.20 (live-fetched 2026-07-14 + re-confirmed 2026-07-16),
# fields cross-checked against https://keda.sh/docs/2.20/reference/scaledobject-spec/
# and https://keda.sh/docs/2.20/scalers/prometheus/. Illustrative for the
# image class — mirror for document/html with D-05/D-06/D-08 values.
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: worker-image-scaledobject          # discretion: naming convention
  namespace: octoconv
spec:
  scaleTargetRef:
    name: worker                            # matches deployment-worker.yaml's Deployment name
  minReplicaCount: 0
  maxReplicaCount: 4                        # D-06
  pollingInterval: 5                        # D-08 demo value, image = fast/bursty
  cooldownPeriod: 60                        # D-08 demo value, image = short
  fallback:
    failureThreshold: 3                     # discretion — 3 consecutive failed polls (~15-45s at 5-15s polling)
    replicas: 1                             # D-07 — hold 1 worker if Prometheus is unreachable
  triggers:
    - type: prometheus
      metadata:
        serverAddress: http://octoconv-prometheus.octoconv.svc.cluster.local:9090
        query: sum(octoconv_queue_depth{queue="image", state=~"pending|active"})
        threshold: "5"                       # D-05
        # activationThreshold intentionally omitted (defaults to "0"): the
        # metric is an exact integer task count from Redis via asynq's
        # Inspector, not a sampled/noisy signal — any value > 0 should wake
        # the class up. Setting activationThreshold above 0 would delay
        # scale-from-zero until N tasks queue up, defeating "each job
        # triggers scale-up" (roadmap SC2's literal per-class 0→1 proof).
        # ignoreNullValues intentionally left at its default "true" — see
        # "Scrape-Gap / Absent-Series Behavior" below for the tradeoff.
```

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Scaling decision logic (poll metric, compare threshold, drive HPA) | A custom controller/cron that watches `octoconv_queue_depth` and calls the Deployments API | KEDA's `ScaledObject` + its Prometheus scaler | This is exactly KEDA's job; hand-rolling it duplicates a well-tested, HPA-integrated control loop for no benefit, and was already rejected implicitly by this milestone's own KEDA-02 requirement |
| Prometheus scrape/query engine | A custom `/metrics`-polling Go service that aggregates queue depth and exposes it in a shape KEDA can read | The official `prom/prometheus` image + KEDA's built-in `prometheus` scaler | KEDA's scaler already speaks PromQL over HTTP against any standard Prometheus server — there is no reason to build a bespoke aggregator; STACK.md already established this |
| External-metrics API registration (`v1beta1.external.metrics.k8s.io`) | A custom Kubernetes APIService/aggregated-apiserver | KEDA's bundled `keda-operator-metrics-apiserver` (installed automatically by the KEDA Helm chart) | This is precisely what the KEDA Helm chart's CRDs + metrics-apiserver component exist to do; confirmed via official docs (`keda-operator-metrics-apiserver` Deployment registers the APIService) |

**Key insight:** Every piece of this phase's "new" infrastructure (Prometheus, KEDA) is off-the-shelf; the only genuinely custom code is the ~15-line collector-registration move and the (recommended) 1-line-per-file `ShutdownTimeout` addition. Resist any temptation to write Go glue beyond that — the chart/YAML surface is where the real work is.

## Common Pitfalls

### Pitfall 1: `networkpolicy-metrics.yaml` already ingress-restricts `:9090` to a `monitoring` namespace that this phase's own decisions will never create

**What goes wrong:** Phase 24 shipped `deploy/chart/octoconv/templates/networkpolicy-metrics.yaml` with an ingress rule allowing `:9090` scrape traffic **only from a namespace labeled `kubernetes.io/metadata.name: monitoring`** — its own header comment explicitly says "Nothing in this cluster carries that namespace label yet (Prometheus lands in Phase 27)". But Phase 27's CONTEXT.md D-11 says "Prometheus ships **inside our chart**" — i.e., as part of the single `octoconv` Helm release, in the `octoconv` namespace (matching the project's own "single chart, no subcharts" philosophy carried since Phase 24's D-01). If Prometheus is deployed in the `octoconv` namespace, the existing `namespaceSelector: monitoring` rule will **never match it**, and Prometheus's scrape of `api:9090` will be silently denied by the very policy meant to protect it — SC1 (the metric must resolve correctly) will fail at the network layer, not the KEDA layer, and it will look like a KEDA/PromQL bug rather than a NetworkPolicy mismatch.

**Why it happens:** Phase 24 was written before this phase's namespace decision was made; the `monitoring` label was a reasonable placeholder guess that this phase's actual decision (D-11, single-chart Prometheus) doesn't match.

**How to avoid:** Change the `:9090` ingress rule to select the Prometheus pod **within the `octoconv` namespace** by combining a `namespaceSelector` (matching `.Values.global.namespace`, same pattern already used for the `:8090` rule) with a `podSelector` matching the new Prometheus pod's own label, in a single `from` list entry (namespaceSelector + podSelector inside one entry are ANDed in NetworkPolicy semantics — this is the standard pattern for "this specific pod, in this specific namespace"):

```yaml
# Source: this repo, deploy/chart/octoconv/templates/networkpolicy-metrics.yaml
# — REPLACE the existing rule (a)'s namespaceSelector-only `monitoring` block
# with this combined selector. Prometheus pod needs a label distinct from
# octoconv.io/tier: app (that label is reserved for the 5 scaled/fixed app
# Deployments per _helpers.tpl's own tier-label boundary comment — giving
# Prometheus that label would make IT subject to this same policy's :9090
# ingress restriction as a TARGET, which it should not be, since Prometheus
# itself is a scrape SOURCE here, not something KEDA/anything scrapes on
# :9090).
ingress:
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

Note: OrbStack's CNI has **no NetworkPolicy controller** (confirmed by Phase 24's own live-gate SC4 finding, `24-03-SUMMARY.md`) — this fix will not be locally enforceable/verifiable via a negative test on OrbStack (same environmental limitation Phase 24 already hit and recorded), but the manifest must still be *correct* since the same chart may later run on a policy-capable cluster. Do not skip the fix just because local verification is impossible — verify via `helm template` rendering the expected selector, not a live negative test.

**Warning signs:** `kubectl get --raw .../external.metrics.k8s.io/...` returns a stale/zero value even though the queue genuinely has pending tasks; Prometheus's own `/targets` page shows the api scrape target `DOWN` with a connection-refused-shaped error (NetworkPolicy denies look like connection timeouts/refusals from the client's perspective, not explicit 403s).

**Phase to address:** This phase (27), as part of the same plan that adds the Prometheus template — do not defer to a "later hardening" pass.

---

### Pitfall 2: asynq's 8-second default `ShutdownTimeout` silently caps every worker's graceful-shutdown window, independent of the pod's `terminationGracePeriodSeconds`

**What goes wrong:** Covered in depth in "Pattern 2" above. Restated as a pitfall because it is easy to assume Phase 24's per-class `terminationGracePeriodSeconds` (150s/330s/90s/60s) alone makes graceful shutdown correct — it doesn't. `srv.Shutdown()` (called synchronously by every worker's `main()` on `<-ctx.Done()`) internally waits at most `Config.ShutdownTimeout` (default 8s, confirmed in `asynq@v0.26.0/server.go:416`) for in-flight tasks, then force-aborts (cancels the task's context, pushes the message back to Redis via `Requeue`) and returns — the pod's own SIGKILL deadline is never reached because the process exits (via the worker main's own control flow) well before it.

**Why it happens:** asynq's own default is tuned for short-task workloads; nothing in this codebase overrides it, and the mismatch is invisible until a long-running task is actually mid-flight during a shutdown (either a pod eviction or, specifically, a KEDA cooldown-triggered scale-down).

**How to avoid:** Set `asynq.Config.ShutdownTimeout` per class (Pattern 2). This is a **recommendation, not a locked CONTEXT.md decision** — flag it explicitly to the planner as an in-scope-or-defer choice. Deferring it is not catastrophic (no data loss — `Requeue` is safe, guarded transitions and the reconciler already handle a requeued/restarted job) but it does mean Phase 28's KEDA-03 "graceful scale-down, not SIGKILL mid-job" proof will fail on its first real attempt against a document-class task, for a reason that traces back to this phase's code, not Phase 28's KEDA tuning.

**Warning signs:** A scale-down event correlates with a task being requeued (visible in asynq/Redis, or via a job transitioning `active`→ still `active` with a new task attempt) at almost exactly 8 seconds after the SIGTERM timestamp, regardless of how long `terminationGracePeriodSeconds` is set to.

**Phase to address:** Recommended for this phase (same files KEDA-01 already edits); acceptable to explicitly defer to Phase 28 if the planner judges scope should stay narrower — but must be a **conscious** decision, not a silent gap.

---

### Pitfall 3: The compose-E2E validation step (D-03) can't reach `/metrics` the naive way — `METRICS_ADDR` is `127.0.0.1:9090` in every compose service, including api

**What goes wrong:** `docker-compose.yml` sets `METRICS_ADDR: "127.0.0.1:9090"` identically on all 6 app services (grep-confirmed, 6 occurrences). This is a per-container loopback bind — the same trust-model reasoning that motivates Phase 24's k8s `NetworkPolicy` fix. In compose, unlike k8s, this phase's own values.yaml doesn't touch `METRICS_ADDR` at all (that's k8s-chart-only, via the ConfigMap) — so a compose-E2E test that tries `http.Get("http://api:9090/metrics")` or `http.Get("http://localhost:9090/metrics")` from the Go test process (which runs on the **host**, confirmed by `.github/workflows/ci.yml:120` invoking `go test ./internal/e2e/... -timeout 30m` directly, not inside a container) will get a connection refused — the port isn't published to the host, and even if it were, it's bound to the container's own loopback, not `0.0.0.0`.

**Why it happens:** D-03's compose-E2E assertion is new ground — no existing e2e test scrapes `/metrics` today (grep-confirmed: `internal/e2e/e2e_test.go` has zero `:9090` references), so there's no established pattern to copy, and the obvious first attempt (a plain HTTP GET) silently fails for a reason (container-loopback binding) that's easy to misdiagnose as "the collector isn't registered" rather than "the port isn't reachable."

**How to avoid:** Since the e2e test process already runs on the host with Docker available (same environment `docker compose -f ... up -d` ran in, per `ci.yml`), shell out via `os/exec` to `docker compose exec <service> wget -qO- http://localhost:9090/metrics` (or `curl`, whichever is present in the `debian:bookworm-slim` runtime images — verify `wget`/`curl` availability in the built images at execution time; neither is guaranteed present in a minimal `debian:bookworm-slim` + `ca-certificates` image per `Dockerfile.api`/`Dockerfile.worker`'s stated minimal package list). If neither tool is present in the runtime image, the simplest fix is a tiny inline Go HTTP check executed via `docker compose exec <service> /bin/sh -c '...'` is not possible either (no shell utilities to fetch), so the most robust option is likely a **unit test only** for D-03's "api's registry exposes the metric" half (test `metrics.NewQueueDepthCollector` + `prometheus.MustRegister` wiring directly in Go, no compose needed) plus a compose-level check that specifically inspects container **logs** for the `📊 metrics listening on 0.0.0.0:9090`-style startup line as a weaker but still meaningful "did the process start correctly" signal, OR temporarily add a debug-only compose override (mirroring `docker-compose.e2e.yml`'s existing pattern) that maps `METRICS_ADDR=0.0.0.0:9090` with a host port mapping for validation runs only. **This needs a concrete decision at planning time** — the CONTEXT.md's D-03 names the goal ("compose-E2E assertion") but not the mechanism, and the naive mechanism doesn't work as-is.

**Warning signs:** A new e2e test that expects `octoconv_queue_depth` from api's `/metrics` times out or connection-refuses in CI, with no clear signal whether the collector registration is wrong or the port is simply unreachable.

**Phase to address:** This phase (27), plan 1 (KEDA-01) — resolve the reachability mechanism before writing the assertion, not after discovering it fails.

---

### Pitfall 4: KEDA's `fallback` block DOES work at `minReplicaCount: 0` — but only for scaler *query failures*, not for a genuinely-empty queue, and the distinction is easy to get backwards

**What goes wrong (if misunderstood, not a code bug):** It would be easy to assume `fallback.replicas: 1` (D-07) means "if the queue is empty, hold 1 replica" — it does not. `ignoreNullValues` (default `true`, confirmed field, STACK.md) means an *empty PromQL result* (e.g., `sum()` over a query that matches zero series — which is exactly what happens if `octoconv_queue_depth{queue="image",...}` genuinely has no pending/active tasks, since Prometheus's `sum()` over an empty vector returns no result at all, not a literal `0`) is treated as a **successful** scrape returning value `0` — this does NOT count toward `fallback.failureThreshold` and does NOT trigger `fallback.replicas`. `fallback` only activates on genuine scaler *failures*: the HTTP call to `serverAddress` erroring, timing out, or the Prometheus server being unreachable — confirmed by the KEDA documentation and cross-referenced GitHub issue discussion (`kedacore/keda#4249`, `#6053`) describing exactly this "failed N times in a row → force `fallback.replicas`" mechanism, and explicitly stated to work "when behavior is not specified or given as `static`" (the default), which applies here since D-07 doesn't request `scalingModifiers`.

**Why it happens:** The word "fallback" invites reading it as a generic safety net for *any* uncertain state, but KEDA scopes it specifically to scaler unreachability/error, distinct from "scaler reachable, query answered, answer happens to be zero" (the normal, correct scale-to-zero path).

**How to avoid:** Keep `ignoreNullValues: true` (the default — do not set it to `false`). Setting it `false` would instead make *any* momentary empty-result scrape (e.g., during api's own pod restart/rolling update, which this phase doesn't autoscale but which still restarts on deploys) count as a scaler failure toward `fallback.failureThreshold` — turning a routine api restart into spurious `fallback.replicas` forcing across all three scaled classes. The tradeoff (documented, not load-tested this session — MEDIUM confidence): accepting `ignoreNullValues: true`'s small risk of a genuine scrape-gap being misread as "queue empty, scale down" is safer than `false`'s guaranteed false-positive on every api restart. This directly resolves the phase's stated open question ("behavior when the series is momentarily absent (scrape gap)").

**Warning signs:** If ever debugging why `fallback.replicas` didn't kick in during an intentional Prometheus outage test, check the query error rate (Prometheus/KEDA operator logs), not the queue's actual depth — an empty-but-reachable result will never trigger fallback by design.

**Phase to address:** This phase (27) — set `ignoreNullValues` deliberately (even though it's the default, the chart should set it explicitly with a comment, not rely on an unstated default, per this project's general "don't leave load-bearing behavior to an implicit default" discipline already visible in Phase 24's `terminationGracePeriodSeconds` handling).

---

### Pitfall 5: The external metric's exact name is auto-generated by KEDA, not chosen by the chart author — don't hardcode a guess into the gate script

**What goes wrong:** The `kubectl get --raw` verification command (SC1) needs the exact metric name KEDA registered for a given trigger, e.g. something shaped like `s0-prometheus` (index-`0` since each `ScaledObject` here has exactly one trigger, type `prometheus`) — but this pattern is inferred from KEDA's documented naming convention (`s{index}-{scaler-type}-{identifier}`, confirmed shape from official docs but the exact Prometheus-scaler identifier segment was not independently confirmed this session — LOW-MEDIUM confidence on the literal string). Hardcoding a guessed name into the gate script risks a silent typo that always fails the check for the wrong reason.

**How to avoid:** Discover the name live, per-`ScaledObject`, rather than hardcoding:
```bash
# Source: KEDA docs (live-searched 2026-07-16) — authoritative way to get
# the real metric name for a given ScaledObject, rather than guessing the
# s{index}-{type}-{identifier} pattern.
kubectl get scaledobject worker-image-scaledobject -n octoconv -o jsonpath='{.status.externalMetricNames}'
# then:
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/octoconv/<name-from-above>?labelSelector=scaledobject.keda.sh%2Fname%3Dworker-image-scaledobject"
```

**Phase to address:** This phase's gate script (D-12) — write it to look up the name dynamically, not as a literal string.

## Registration Relocation Mechanics (KEDA-01 detail, answers research question 6)

- `cmd/api/main.go` already constructs `redisOpt` via `queue.RedisOpt()` at line 73-76 (for the `/healthz` Redis pinger) — this is the exact value `asynq.NewInspector(redisOpt)` needs; no new env var, no new connection-option derivation.
- `asynq.NewInspector` opens its own Redis connection internally (confirmed: `Inspector` wraps an `rdb.RDB`/Redis client, separate from both the `qc` (`queue.NewClient()`) connection and the dedicated `redis.NewClient` used for `/healthz`) — this phase adds a **third** Redis connection to the api process. Given api is a single, always-on, low-replica-count process, this is a negligible resource cost, consistent with how each of the four worker binaries already independently opened their own `Inspector` today (one per worker pod) — api replacing four worker-side Inspectors with one api-side Inspector is a net reduction in total Redis connections cluster-wide, not an increase.
- **Whether to close the Inspector on shutdown (Claude's Discretion, per CONTEXT.md):** `Inspector.Close() error` exists (confirmed, `asynq@v0.26.0/inspector.go:49`). None of the four worker binaries currently call `.Close()` on their own `asynq.NewInspector(...)` result today (grep-confirmed absent in all four worker mains) — they simply let the process exit. Recommendation: match the existing (if imperfect) convention and skip an explicit `Close()` call in `cmd/api/main.go` too, for consistency — introducing it only in the relocated call site while every other Inspector-owning process in this codebase doesn't close it would be an inconsistent precedent with no clear benefit (the process is exiting anyway; OS reclaims the socket). If the planner prefers correctness over precedent-matching, closing it is also fine (it's a 3-line `defer` addition) — this is genuinely low-stakes either way.

## Requirement 7 — Worker Liveness Probes After Collector Removal (confirmed)

Verified by reading `internal/metrics/metrics.go`: the four `promauto.NewCounterVec`/`NewHistogramVec` metrics (`octoconv_job_outcomes_total`, `octoconv_job_duration_seconds`, `octoconv_webhook_deliveries_total`, `octoconv_reconciler_actions_total`) register onto `prometheus.DefaultRegisterer` at Go package-init time via `promauto`, **independent of** the `queueDepthCollector`'s explicit `prometheus.MustRegister` call. `promhttp.Handler()` (used by every worker's metrics `http.Server`, confirmed in all four worker mains) serves `prometheus.DefaultGatherer`, which always returns HTTP 200 for any registered state, empty or not — removing the queue-depth collector registration only removes that one metric family from a given worker's scrape output, it cannot cause the endpoint itself to error or stop responding. `deployment-worker.yaml`'s `livenessProbe`/`readinessProbe` (`httpGet: /metrics, port: 9090`) are unaffected by this phase's D-01/D-02 change. No verification action needed beyond a sanity-check after the code change (worker `/metrics` still returns 200, just without an `octoconv_queue_depth` line).

## KEDA Helm Install Mechanics (answers research question 3)

- Chart repo: `https://kedacore.github.io/charts`, chart name `keda`, pinned `--version 2.20.1` (re-verify at execution time via `helm search repo kedacore/keda --versions` — 12 days have already passed between the v1.6 milestone research and this phase's research with zero version drift, but always re-check immediately before the live gate).
- Namespace: dedicated `keda` namespace, created via `--create-namespace` (D-11 already specifies this) — idiomatic and matches the officially-documented install command exactly (`helm install keda kedacore/keda --namespace keda --create-namespace`).
- CRDs: bundled and installed automatically as part of the Helm chart install (confirmed — "Starting with v2.2.1, KEDA's Helm chart manages CRDs automatically"), no separate `kubectl apply -f keda-2.20.1-crds.yaml` step needed for the version in use here (well past v2.2.1).
- Time until the external metrics API is queryable: not independently benchmarked this session (LOW confidence on an exact number) — the practical gate pattern is to `helm install ... --wait` (waits for the `keda-operator`/`keda-operator-metrics-apiserver` Deployments to become Available) and then poll `kubectl get apiservice v1beta1.external.metrics.k8s.io -o jsonpath='{.status.conditions}'` for `"Available": "True"` before proceeding, rather than assuming a fixed sleep duration. Separately: the external metrics API returns an **empty list** until at least one `ScaledObject` exists in the cluster (confirmed via `kedacore/keda#1797`) — this is expected, not a failure signal, when checking readiness before any `ScaledObject` is applied.
- Exact `kubectl get --raw` path shape (confirmed, both from official docs and cross-referenced GitHub issues):
  ```
  kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace>/<metric-name>?labelSelector=scaledobject.keda.sh%2Fname%3D<ScaledObject-name>"
  ```
  `<metric-name>` must be discovered live per Pitfall 5 above, not hardcoded.

## Minimal In-Chart Prometheus (answers research question 4)

- Image: `prom/prometheus:v3.13.1` (verified today, see Standard Stack table).
- Scrape config: single static job targeting only api's Service — `api.octoconv.svc.cluster.local:9090` (or the bare in-namespace name `api:9090`, both resolve; the chart's existing convention elsewhere uses full FQDNs for cross-boundary clarity, e.g. `s3.endpoint`/`mcpHttp.baseURL` in `values.yaml`, so `api.octoconv.svc.cluster.local:9090` is the more consistent choice). Per CONTEXT.md's Claude's Discretion, scraping only api is sufficient (workers' `/metrics` no longer carries the KEDA-relevant metric after KEDA-01; scraping them too would add nothing for this phase's purpose, though it remains a legitimate future addition for job-outcome/duration dashboards, out of scope here).
- Retention/resources for a local single-node setup: given exactly one scrape target and a handful of time series (5 states × 4 queues = 20 series for `octoconv_queue_depth` alone, plus the smaller job-outcome/duration/webhook/reconciler cardinality from api's own promauto metrics), default retention (15 days) and minimal resource requests (e.g., `100m`/`128Mi` request, a modest limit like `500m`/`256Mi`) are comfortably sufficient — this is far below the "1 CPU / 1GB per 1M scraped metrics" guidance found for high-cardinality deployments; explicitly not a sizing concern at this scale. [ASSUMED — reasonable synthesis from general Prometheus sizing guidance, not independently load-tested against this exact workload]
- api-Service exposure gap: `deploy/chart/octoconv/templates/service-api.yaml` currently exposes **only port 8090** (`http`) — it does NOT expose `9090` (metrics). Prometheus's scrape target needs a reachable Service port for `:9090`. This phase must add a `metrics` port to the existing api Service (or a second headless Service) — confirmed by direct read, this is a genuine gap, not an assumption.
- How KEDA's prometheus trigger addresses it: in-cluster Service DNS, `http://<prometheus-service-name>.octoconv.svc.cluster.local:9090` (Prometheus's own default HTTP API port, 9090 — a separate Service/pod from api's own `:9090` metrics port; no actual port collision since they are different Services, but worth naming clearly in the chart to avoid confusion between "the app's metrics port" and "Prometheus server's own port," both conventionally `9090`).

## PromQL Signal Shape (answers research question 2)

Confirmed query shape for D-04's "pending + active" per queue:
```promql
sum(octoconv_queue_depth{queue="image", state=~"pending|active"})
```
- `sum()` collapses the (at most) 2 matching series (`state="pending"` and `state="active"` for the fixed `queue` label) into the single scalar KEDA's Prometheus scaler requires.
- Explicitly excludes `state="retry"`, `"scheduled"`, `"archived"` via the regex matcher only naming `pending|active` — matches D-04 exactly ("Do NOT include retry/scheduled/archived").
- Absent-series behavior: if the underlying time series for a queue temporarily has zero matching entries (both pending and active genuinely 0, OR a scrape gap), `sum()` over an empty vector returns **no result** (not a literal `0`) — this is where `ignoreNullValues: true` (default, kept — see Pitfall 4) converts "no result" into an effective `0` for KEDA's purposes, which is the desired behavior for the "genuinely empty queue" case and an accepted, documented tradeoff for the rarer "scrape gap" case.

## asynq v0.26.0 Server Shutdown Semantics (answers research question 5, code-verified against the pinned module)

Read directly from `/Users/apaderin/go/pkg/mod/github.com/hibiken/asynq@v0.26.0/server.go` and `processor.go` (the exact pinned version in `go.mod`, not a newer/older version):

- `Server.Shutdown()` (`server.go:724-754`) is a **blocking call**. It transitions server state to closed, then calls `srv.processor.shutdown()` (among other component shutdowns) and finally `srv.wg.Wait()` — it does not return until every internal goroutine (including the processor) has actually stopped.
- `processor.shutdown()` (`processor.go:139-150`): calls `p.stop()` (stop pulling new tasks from the queue immediately), starts a timer via `time.AfterFunc(p.shutdownTimeout, func() { close(p.abort) })`, then blocks until every worker goroutine has released its semaphore slot (i.e., finished or been aborted).
- `p.shutdownTimeout` comes from `asynq.Config.ShutdownTimeout` (`server.go:198-202`, doc comment: "specifies the duration to wait to let workers finish their tasks before forcing them to abort... If unset or zero, default timeout of 8 seconds is used"). Confirmed default constant: `defaultShutdownTimeout = 8 * time.Second` (`server.go:416`).
- **None of this project's four worker `main()` functions set `Config.ShutdownTimeout`** (confirmed by direct read of all four files) — every worker currently uses the 8-second default.
- What happens to an in-flight task at the 8-second mark (`processor.go:237-249`, the `exec()` goroutine's select statement): the `<-p.abort` case fires, logs `"Quitting worker. task id=%s"`, calls `p.requeue(lease, msg)` (pushes the task's message back onto the Redis queue via `p.broker.Requeue`, safe/idempotent as long as the lease is still valid), and returns. The **outer** goroutine's `defer cancel()` (set up earlier when the task's context was created) then fires as that goroutine unwinds, canceling the `context.Context` passed to the handler (`worker.HandleImageConvert` etc.) — this is the actual signal that causes any context-aware in-flight work (e.g., this project's `os/exec` process-group-kill-on-context-cancel pattern in `internal/convert/exec.go`) to actually stop. **The task is not lost** (it's back in Redis, available for another attempt) but it IS interrupted mid-work after 8 seconds by default, regardless of pod `terminationGracePeriodSeconds`.
- Deadline behavior: no separate internal deadline beyond `ShutdownTimeout` — it is the single governing duration for the entire graceful-shutdown window.
- Re-enqueue confirmed: yes, via `Requeue` (not `Archive`/`Retry` — those are for handler-returned errors, a different code path; `Requeue` on abort is unconditional and doesn't consume a retry-count increment, since the task never actually reported failure, it was interrupted).

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Demo `pollingInterval`/`cooldownPeriod` numbers (image 5s/60s, document 15s/120s, html 10s/90s) are reasonable starting values | Pattern 3, Code Examples | Low — CONTEXT.md D-08 already frames these as "demo starting values... research/planner picks exact numbers," and Phase 28 is explicitly the tuning phase. Wrong numbers cause flapping or slow reaction, not incorrect behavior, and are trivially re-tunable via `values.yaml`. |
| A2 | `fallback.failureThreshold: 3` is a reasonable default | Pattern 3, Pitfall 4 | Low — no official "recommended" value found (docs mark it mandatory with no default); 3 consecutive failed polls is a common community pattern but not independently verified against KEDA's own examples this session. Wrong value only affects how fast the fail-safe kicks in, not whether it eventually does. |
| A3 | Prometheus resource requests/limits (100m/128Mi request, 500m/256Mi limit) are sufficient for this workload's cardinality | Minimal In-Chart Prometheus | Low-Medium — under-provisioning would show as OOMKilled/CPU-throttled Prometheus pod during the live gate, easily caught and bumped; over-provisioning wastes a small amount of the already-tight OrbStack VM budget (Pitfall 7 in the v1.6 PITFALLS.md flags VM memory as a genuine constraint this milestone) |
| A4 | The external metric name for a single-trigger prometheus ScaledObject follows the `s0-prometheus`-shaped pattern | Pitfall 5 | Low — the research explicitly recommends discovering the name live via `kubectl get scaledobject ... -o jsonpath='{.status.externalMetricNames}'` rather than hardcoding, specifically because this pattern wasn't independently confirmed; the mitigation already accounts for the uncertainty |
| A5 | `docker exec`/`docker compose exec` shelling from the Go e2e test is the right mechanism for D-03's compose-E2E metrics assertion (vs. a compose override or log-inspection fallback) | Pitfall 3 | Medium — this is presented as a recommendation with fallbacks, not a locked decision; the planner must choose one concrete mechanism, and `wget`/`curl` presence in the minimal runtime images (`debian:bookworm-slim` + `ca-certificates`) was flagged as unverified this session — verify at execution time before committing to this approach |

## Open Questions

1. **Should Phase 27 fix asynq's `ShutdownTimeout` gap, or explicitly defer it to Phase 28?**
   - What we know: the gap is real and code-verified (see asynq Server Shutdown Semantics section); the fix is cheap and touches files KEDA-01 already edits.
   - What's unclear: whether the user/planner wants Phase 27's scope to stay strictly "queue-depth relocation + ScaledObjects" or absorb this adjacent fix now.
   - Recommendation: fix now (Pattern 2) — it's the same files, same plan, and removes a confirmed Phase 28 blocker. If deferred, it must be an explicit, documented decision (e.g., a new item in Phase 28's own CONTEXT.md), not a silent gap discovered mid-Phase-28.

2. **Exact mechanism for D-03's compose-E2E metrics reachability (Pitfall 3).**
   - What we know: the naive HTTP-GET approach fails (loopback-bound port); `docker compose exec` shelling is the leading candidate but depends on `wget`/`curl` being present in the minimal runtime images, which was not verified this session.
   - What's unclear: whether `wget`/`curl` exist in `debian:bookworm-slim` + `ca-certificates`-only images (`Dockerfile.api`, worker Dockerfiles) — if neither is present, a different mechanism (log-inspection, or a validation-only compose override) is needed.
   - Recommendation: verify `wget`/`curl` presence in the built images as the FIRST step of implementing D-03 (`docker run --rm octoconv-api:dev which curl wget`), before writing the assertion; fall back to log-line inspection (`docker compose logs api | grep '📊 metrics listening'` combined with a unit test for the collector wiring) if neither tool is present.

3. **Prometheus in-chart namespace: confirmed `octoconv` namespace (matching D-11), but the exact Service name for `serverAddress` is a naming choice, not yet fixed.**
   - What we know: D-11 says "inside our chart" (same Helm release, `octoconv` namespace); the chart's existing naming convention uses short literal Service names (`api`, presumably `postgres`/`redis`/`minio` — not independently re-verified this session but consistent with `service-api.yaml`'s comment about staying un-prefixed for FQDN simplicity).
   - What's unclear: exact chosen name (e.g., `prometheus` vs `octoconv-prometheus`) — cosmetic, but must be consistent between the Service template, the ScaledObjects' `serverAddress`, and the NetworkPolicy fix (Pitfall 1)'s `podSelector` label.
   - Recommendation: planner picks a short literal name (e.g., `prometheus`, matching the `api`/likely `postgres`/`redis`/`minio` convention) and uses it consistently across all three touch points in one plan, to avoid a three-way naming drift.

## Sources

### Primary (HIGH confidence)
- This repo's own source, read directly today (2026-07-16): `cmd/api/main.go`, `cmd/worker/main.go`, `cmd/document-worker/main.go`, `cmd/webhook-worker/main.go`, `internal/metrics/queue_collector.go`, `internal/metrics/metrics.go`, `internal/queue/queue.go`, `deploy/chart/octoconv/{values.yaml,values-local.yaml,Chart.yaml,templates/*.yaml}`, `docker-compose.yml`, `docker-compose.e2e.yml`, `.github/workflows/ci.yml`, `go.mod`
- `github.com/hibiken/asynq@v0.26.0` source, read directly from the local module cache (`/Users/apaderin/go/pkg/mod/github.com/hibiken/asynq@v0.26.0/{server.go,processor.go,inspector.go}`) — the exact pinned version, not a newer/older one
- `.planning/phases/24-helm-chart-core/24-CONTEXT.md`, `24-03-SUMMARY.md` — chart conventions, proven install flow, the `networkpolicy-metrics.yaml` "Prometheus lands in Phase 27" placeholder note, OrbStack CNI NetworkPolicy-unenforced finding
- `.planning/research/{PITFALLS,SUMMARY,STACK,FEATURES,ARCHITECTURE}.md` — v1.6 milestone research (2026-07-14), re-verified for drift today (none found for version numbers)
- KEDA GitHub Releases API (`https://api.github.com/repos/kedacore/keda/releases/latest`), live-fetched today — `v2.20.1`
- KEDA Helm chart index (`https://kedacore.github.io/charts/index.yaml`), live-fetched today — `keda` chart `appVersion: 2.20.1`
- Prometheus GitHub Releases API (`https://api.github.com/repos/prometheus/prometheus/releases/latest`), live-fetched today — `v3.13.1`
- Local `helm version --short` (`v4.2.3+g43e8b7f`) and `kubectl version --client` (`v1.36.2`) — both already installed
- https://keda.sh/docs/2.20/reference/scaledobject-spec/ — WebFetch'd today, confirmed pollingInterval/cooldownPeriod/minReplicaCount/maxReplicaCount defaults, fallback block field requirements, `advanced.horizontalPodAutoscalerConfig.behavior` (HPA stabilization window interaction)

### Secondary (MEDIUM confidence)
- WebSearch (today): `kedacore/keda` GitHub issues #4249, #6053, #6145, #5857 — cross-referenced consensus that `fallback` triggers on scaler query failure, not on a genuinely-empty/zero query result
- WebSearch (today): KEDA metrics-server docs + `kedacore/keda#5731`/`#1797` — `kubectl get --raw` path shape, external-metrics-API-empty-until-a-ScaledObject-exists behavior
- WebSearch (today): KEDA official install docs + community write-ups — `helm install keda kedacore/keda --namespace keda --create-namespace`, automatic CRD management since v2.2.1

### Tertiary (LOW confidence)
- External metric naming pattern (`s{index}-{scaler-type}-{identifier}`) — inferred from one documented example (`s1-rabbitmq-queueName2`), not independently confirmed for the `prometheus` scaler type specifically; mitigated by recommending live discovery instead of hardcoding (Pitfall 5)
- Prometheus resource sizing (100m/128Mi request) — general sizing-guidance synthesis, not load-tested against this exact 20-series workload

## Metadata

**Confidence breakdown:**
- Standard stack (versions): HIGH — every version number live-fetched from an authoritative API/index today, zero drift found from the 2-day-old v1.6 milestone research
- Architecture (relocation mechanics, NetworkPolicy conflict, Service exposure gap): HIGH — all grounded in direct reads of this repo's current source and chart templates
- Pitfalls (asynq shutdown timing, fallback semantics, compose reachability): HIGH for asynq (code-verified against the pinned module source) and the NetworkPolicy/Service gaps (direct chart read); MEDIUM for KEDA fallback/PromQL edge-case behavior (official docs + cross-referenced GitHub issues, not independently reproduced against a live cluster this session)
- KEDA install mechanics (CRDs, namespace, timing): HIGH for CRD/namespace facts (official docs), LOW for exact "how long until queryable" (no benchmark found — mitigated with a poll-not-sleep recommendation)

**Research date:** 2026-07-16
**Valid until:** ~14 days for the version pins (KEDA/Prometheus release cadence is roughly monthly-to-bimonthly per the observed release history; re-verify immediately before the live gate regardless) — ~30 days for the architectural/pitfall findings (code-grounded, won't drift unless this repo's own source changes first)
