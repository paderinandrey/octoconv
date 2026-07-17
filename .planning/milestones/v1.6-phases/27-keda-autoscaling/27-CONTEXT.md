# Phase 27: KEDA Autoscaling - Context

**Gathered:** 2026-07-16
**Status:** Ready for planning
**Source:** v1.6 research (STACK/PITFALLS/ARCHITECTURE/FEATURES/SUMMARY), user-approved roadmap, user discussion

<domain>
## Phase Boundary

Queue-depth exposition relocated to the always-on `api` process (KEDA-01), then KEDA v2.20.x installed and per-engine-class `ScaledObject`s (image/document/html) scaling from `minReplicaCount: 0` on the Prometheus scaler, all gated behind `keda.enabled`. webhook-worker hard-excluded from KEDA — fixed `replicas: 2` (sole host of the advisory-lock sweeper). Burst load-proof (0→N→0 with timestamps, graceful scale-down of a long document job) is Phase 28 — NOT this phase.

</domain>

<decisions>
## Implementation Decisions

### Queue-depth relocation (KEDA-01 — plan 1, pure Go, before any k8s work)
- **D-01:** MOVE, not duplicate: remove `prometheus.MustRegister(metrics.NewQueueDepthCollector(...))` from all four worker mains (`cmd/worker/main.go:89`, `cmd/document-worker/main.go:94`, `cmd/chromium-worker/main.go:86`, `cmd/webhook-worker/main.go:114`); register in `cmd/api/main.go` instead. Single source of truth, no duplicate series. Worker liveness probes unaffected — their `/metrics` endpoints stay (other metrics live there).
- **D-02:** api registers ALL FOUR queues (image/document/html/webhook) — one collector call with all queue names. webhook isn't KEDA-scaled but its depth stays observable; matches roadmap SC1 ("all four queues").
- **D-03:** Validation before any k8s work: unit test that api's registry exposes `octoconv_queue_depth` for all 4 queues + compose-E2E assertion that api `/metrics` serves the metric per queue and workers no longer serve it. Live proof on the cheap stack first.

### ScaledObject trigger design (KEDA-02)
- **D-04:** PromQL signal = `pending + active` for the worker's own queue (sum over those two states of `octoconv_queue_depth{queue="X"}`). A long in-flight task keeps the signal non-zero — KEDA doesn't begin scale-down mid-job. Do NOT include retry/scheduled/archived.
- **D-05:** Per-class `threshold` (tasks-per-replica): image 5 / document 1 / html 2. Demo starting values; real tuning is Phase 28's job.
- **D-06:** Per-class `maxReplicaCount`: image 4 / document 2 / html 2 — ceilings sized for the local OrbStack VM (three documented daemon wedges under load). Values live in `values.yaml` so production can raise them.
- **D-07:** `fallback.replicas: 1` on every ScaledObject — if Prometheus is unreachable after the failure threshold, each scaled class holds 1 worker and keeps processing (fail-safe toward availability).
- **D-08:** `pollingInterval`/`cooldownPeriod` per class per roadmap SC4: image fast/bursty (short cooldown), document/html long-task classes (longer cooldown so one long task doesn't read as sustained load or premature idleness). Demo-tuned starting values (~5-15s polling, ~60s+ cooldown) — planner/research picks exact numbers.
- **D-09:** webhook-worker: NO ScaledObject at all; fixed `replicas: 2` Deployment — fail-closed hard gate shipped in this phase.

### Chart gating & install
- **D-10:** `keda.enabled: false` default in `values.yaml`, `true` in the local overlay (values-local/values-e2e pattern from Phase 24/25). With `enabled=false` the chart renders zero ScaledObjects — base chart installs cleanly on a cluster without KEDA CRDs; offline check: `helm template` with flag off renders none.
- **D-11:** KEDA installed BY the gate script: idempotent `helm install` of pinned KEDA v2.20.x into its own `keda` namespace — reproducible from scratch, consistent with Phase 24 install-flow discipline. Prometheus ships inside our chart behind a flag (minimal `prom/prometheus` Deployment + static scrape ConfigMap per research — NOT kube-prometheus-stack).

### Live gate (this phase's proof; burst proof is Phase 28)
- **D-12:** Gate proves: (a) SC1 — `octoconv_queue_depth` resolves via `kubectl get --raw` on the external metrics API while a worker Deployment is at genuinely 0 replicas; (b) ALL THREE classes scale 0→1 from a single job of their own type (SC2 literally — catches doc/html-specific cold-start issues now, not in Phase 28); (c) full cycle back →0 by cooldown awaited only on image (fastest class); (d) webhook-worker holds 2 replicas with no ScaledObject throughout.
- **D-13:** OrbStack discipline unchanged (Phase 24 D-11/D-12): sequential image pre-builds, compose and k8s stacks never hot simultaneously, teardown after gate.

### Claude's Discretion
- Prometheus specifics (area not selected for discussion — research defaults apply): lives in our chart behind `prometheus.enabled`-style flag; scrape target = api's `/metrics` (that's where the KEDA-relevant metric lives — scraping all pods optional); update Phase 24's `networkpolicy-metrics.yaml` placeholder to admit the in-chart Prometheus; exact scrape interval.
- Registration point details in `cmd/api/main.go` (reuse `queue.RedisOpt()`, one `asynq.Inspector`); whether the Inspector is closed on shutdown.
- ScaledObject template layout (one template file with per-class values loop vs three files), label conventions, exact fallback `failureThreshold`.
- Exact demo pollingInterval/cooldownPeriod numbers per class (within D-08 constraints).
- asynq v0.26.0 `Server.Shutdown()` deadline semantics — confirm during research (roadmap note) so grace-period behavior holds; matters for Phase 28 but verify assumptions now if cheap.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Queue-depth relocation (KEDA-01)
- `internal/metrics/queue_collector.go` — the collector to relocate; reads Redis via asynq Inspector, reusable as-is
- `cmd/worker/main.go`, `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go`, `cmd/webhook-worker/main.go` — current registration sites (lines ~86-114) to remove
- `cmd/api/main.go` — new registration home (has `queue.RedisOpt()` access already)
- `internal/queue/queue.go` — queue name constants (QueueImage/QueueDocument/QueueHTML/QueueWebhook)

### Chart & KEDA
- `deploy/chart/octoconv/` — Phase 24/25 chart: values contract, per-service deployment templates, `networkpolicy-metrics.yaml` (prometheus placeholder to resolve), values-local/values-e2e overlay pattern
- `deploy/chart/octoconv/templates/deployment-webhook-worker.yaml` — must stay fixed replicas: 2, no ScaledObject

### Research (v1.6)
- `.planning/research/PITFALLS.md` — Pitfall 1 (chicken-and-egg at zero, verified registration sites), Pitfall 2 (webhook sweeper singleton), OrbStack wedge history
- `.planning/research/SUMMARY.md` — Key Decisions: Prometheus scaler only (never asynq Redis internals), relocate-to-api as option 1, minimal prom/prometheus
- `.planning/research/STACK.md` — KEDA v2.20.1 helm chart, Helm v4, prom/prometheus minimal footprint
- `.planning/research/FEATURES.md` — KEDA UX defaults (webhook hard-excluded, demo-tuned polling/cooldown, 0→N→0 timeline capture)

### Prior phase conventions
- `.planning/phases/24-helm-chart-core/24-CONTEXT.md` — D-04 (metrics NetworkPolicy), D-08 (worker probes on /metrics), D-11/D-12 (OrbStack discipline, live-gate shape)
- `.planning/phases/24-helm-chart-core/24-03-SUMMARY.md` — proven install flow the gate script extends

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `metrics.NewQueueDepthCollector(inspector, queues...)` — already variadic over queue names; api can register all four with one call, zero collector-code changes
- Compose E2E suite (`internal/e2e/`) — the pre-k8s validation bed for plan 1
- Phase 24 gate script flow (build → install → wait Ready → assert → teardown) — extend, don't reinvent

### Established Patterns
- Env-only config read in `cmd/*/main.go` (no config files) — any new knob (if needed) follows `os.Getenv`
- Per-service template files in flat chart, feature gates as `<component>.enabled` values flags (mcpHttp.enabled precedent)
- Worker liveness/readiness = HTTP GET /metrics on METRICS_ADDR (Phase 24 D-08) — unaffected by collector removal, endpoint remains

### Integration Points
- `cmd/api/main.go` — collector registration next to existing metrics wiring
- `deploy/chart/octoconv/templates/` — new: scaledobject-*.yaml (×3, gated), prometheus.yaml (deployment+configmap+service, gated); modified: networkpolicy-metrics.yaml (admit prometheus)
- Gate script — new KEDA helm-install step before `helm install octoconv`

</code_context>

<specifics>
## Specific Ideas

- 0→1 proof per class uses a single real conversion job of that class's type via the API (same fixtures as E2E)
- The `kubectl get --raw /apis/external.metrics.k8s.io/...` check runs while the target Deployment is verifiably at 0 replicas — that's the literal SC1 wording
- Verify `helm upgrade` idempotence still holds with keda.enabled both on and off (Phase 24 convention)

</specifics>

<deferred>
## Deferred Ideas

- Burst 0→N→0 with timestamps + graceful scale-down soak of a long document job — Phase 28 (KEDA-03)
- Threshold/cooldown production tuning against real task-duration distributions — Phase 28 findings feed values
- KEDA/HPA for api or mcp-http (request/response tiers — no queue signal; research explicitly out of scope)
- kube-prometheus-stack / full observability stack — disproportionate for one PromQL endpoint

</deferred>

---

*Phase: 27-keda-autoscaling*
*Context gathered: 2026-07-16*
