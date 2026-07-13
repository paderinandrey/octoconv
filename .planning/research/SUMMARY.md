# Project Research Summary

**Project:** OctoConv — v1.6 milestone (Kubernetes & KEDA)
**Domain:** Porting an existing production-hardened Go/asynq/Postgres/Redis/MinIO async conversion service from Docker Compose to a local Kubernetes (OrbStack) deployment, adding KEDA per-engine-class autoscaling, an in-cluster MCP streamable-HTTP endpoint, and an operator-gated system-presets REST API.
**Researched:** 2026-07-14
**Confidence:** HIGH for code-grounded findings (queue-depth exposition, sweeper singleton, MCP key model, METRICS_ADDR override, `clients` schema); MEDIUM for general KEDA/Helm/OrbStack-k8s mechanics (verified against current docs, not against this project's own cluster, since no chart exists yet).

## Executive Summary

This milestone is not "learn Kubernetes" research — it is integration-design research against a codebase that is already production-hardened for a single-node Compose deployment. Every one of the four SEED-004-named landmines (localhost-bound `/metrics`, the `host.docker.internal` E2E trick, one-shot migration/bucket ordering, and compose-DNS baked into presigned S3 URLs) has a code-verified, mostly zero-Go-code-change fix, and the two new surfaces this milestone adds (MCP HTTP, system-presets REST) build on existing transport-agnostic (`internal/mcpserver`) and scope-agnostic (`PresetAdmin`) abstractions that need no domain-layer changes at all. The recommended approach is: a single flat Helm chart (no subcharts, no Bitnami/MinIO-Operator dependencies — hand-roll Postgres/Redis/MinIO as plain Deployment+PVC+Service, matching the compose file's own non-HA shape); KEDA `ScaledObject`s driven by the Prometheus scaler against the already-existing `octoconv_queue_depth` metric (never asynq's internal Redis list keys); and additive, thin new entrypoints (`cmd/mcp-http`, new REST handlers) rather than any refactor of shared domain code.

The single most important risk uncovered by this research is an architectural bug hiding in plain sight: `octoconv_queue_depth` is registered per-worker, inside each engine-worker's own `main()` — so once KEDA scales a worker Deployment to zero replicas, there is no pod left to expose the very metric KEDA needs to decide whether to scale back up. This must be fixed (relocate the collector registration to the always-on `api` process) as an explicit, isolated step before any `ScaledObject` manifest is written, not discovered during the load-proof. A second, closely-related risk is that `webhook-worker` is not a symmetric queue consumer like the other three engine classes — it is the sole host of the Postgres-advisory-lock singleton reconciler sweeper, so KEDA scaling it to zero would silently stop stale-job recovery for the entire fleet, not just webhooks. It must be excluded from KEDA entirely and kept as a fixed `replicas: 2` Deployment. A third, non-technical risk is operational: this project's own retrospective documents three confirmed OrbStack daemon wedges under heavy parallel build/compose load, and this milestone (full k8s control plane + iterative chart rebuilds + rapid pod churn during the load-proof) is the heaviest session this VM has ever been asked to run — sequential image pre-building and never running the compose and k8s stacks hot simultaneously are non-negotiable discipline, not nice-to-haves.

Two explicit conflicts between research files were resolved (see Key Decisions below): the system-presets operator gate uses an `OPERATOR_CLIENT_IDS` env allowlist + 404-no-leak (not a new `is_operator` column + 403), and the MCP HTTP endpoint uses per-request caller-key pass-through auth (not a single pod-held key). One integration gap is surfaced but deliberately left unresolved for roadmap-level decision: MCP tool responses include a `local_path` field that is meaningless once the MCP server runs as a remote in-cluster pod rather than a local stdio process sharing the caller's filesystem.

## Key Findings

### Recommended Stack

KEDA v2.20.1 (Helm chart `keda`), Helm v4 (GA, with v3.21.3 as a fully-supported fallback), and OrbStack's built-in single-node Kubernetes are the three new infra dependencies for this milestone; the core application stack (Go 1.26, chi, asynq/Redis, PostgreSQL 18, MinIO) is unchanged and out of scope. Postgres/Redis/MinIO should be hand-rolled minimal Deployment+PVC+Service templates in the chart, not Bitnami charts (paywalled/frozen since Broadcom's 2025 catalog change — do not adopt) and not the MinIO Operator+Tenant CRDs (correct for production multi-tenant object storage, disproportionate for one local bucket). KEDA's Prometheus scaler is the only scaler type to use — the tempting alternative (KEDA's Redis Lists scaler against asynq's internal `pending` list keys) works technically but couples to an undocumented internal implementation detail SEED-004 explicitly says to avoid. A minimal hand-rolled `prom/prometheus` Deployment + static scrape ConfigMap is sufficient; `kube-prometheus-stack` is disproportionate machinery for "KEDA needs one PromQL-answering URL."

**Core technologies:**
- KEDA v2.20.1 — per-engine-class queue-depth autoscaling — current stable, HIGH confidence (live GitHub Releases + Helm repo index)
- Helm v4 (GA) — chart packaging — v3 approaching EOL per its own release notes; greenfield chart has no reason to start on a deprecating major version
- OrbStack Kubernetes — local cluster target (already decided) — shares the same image store as OrbStack's Docker daemon, so locally-built images are pod-visible with no registry; `:latest` tags still trigger re-pull attempts, so repin/use `imagePullPolicy`
- Bare `prom/prometheus` Deployment + static scrape ConfigMap — minimal Prometheus footprint for the KEDA trigger — avoids 15+ CRDs of `kube-prometheus-stack` for a need that is exactly one queryable endpoint

### Expected Features

Five open questions this milestone must answer are directly resolved by FEATURES.md's recommended defaults: Helm chart UX (per-service `values.yaml`, migrate/createbucket as hook Jobs, split liveness/readiness probe thresholds), KEDA UX (webhook-worker hard-excluded, demo-tuned `pollingInterval`/`cooldownPeriod`, explicit 0→N→0 timeline capture), MCP HTTP transport (`Stateless: true`, shared tool code, per-client auth — see Key Decisions), and system-presets operator authorization (see Key Decisions).

**Must have (table stakes):**
- `helm install` brings up the entire stack (Postgres, Redis, MinIO, api, 4 worker classes, asynqmon) in one command
- Readiness/liveness probes gate Service traffic and restart correctly per component (api `/healthz`; workers get a real dependency-aware `/healthz` addition, not just a TCP probe)
- Migration and bucket-creation run exactly once, before anything depends on them (Helm `pre-install,pre-upgrade` hook Jobs)
- Per-engine-class KEDA `ScaledObject`s that visibly scale 0→N→0 under a real load burst
- MCP tool code identical between stdio and HTTP transports (already true — additive only)
- System-preset writes require an explicit operator credential, not just any valid client key

**Should have (competitive/differentiator):**
- A load-proven 0→N→0 autoscale demonstration with an explicit, timestamped causal chain (queue depth graph + pod count over time), not just "it scaled"
- Operator-role authorization layered onto the existing single-credential-type API-key model without introducing a second auth system

**Defer (v2+):**
- Ingress/TLS for any new HTTP surface — no external/cross-cluster consumer exists yet
- CD pipeline wiring CI's image bake into automatic `helm upgrade` — no remote cluster target yet
- Multi-environment `values-*.yaml` sprawl — only one target (OrbStack local) exists
- `helm test` hooks — the in-cluster E2E adaptation already covers this more thoroughly
- KEDA/HPA on the API or MCP-HTTP Deployments — no queue-depth-style signal to scale on for request/response tiers

### Architecture Approach

A single flat Helm chart (`deploy/chart/octoconv/`) with templates organized into per-service files (not subcharts), a shared `configmap.yaml`/`secret.yaml`, `keda.enabled` and `mcpHttp.enabled` feature gates, and `NetworkPolicy` templates as first-class chart artifacts (not follow-up hardening). Every major new component is additive: a new `cmd/mcp-http/main.go` (near-identical wiring to `cmd/mcp-server/main.go`, swapping `mcp.StdioTransport` for `mcp.NewStreamableHTTPHandler`), new `internal/api/system_presets_handlers.go` reusing the already scope-agnostic `PresetAdmin` interface, and small additions of `/healthz` to the four worker binaries. `internal/mcpserver`, `internal/e2e/e2e_test.go`, and the domain/repo layers need zero changes.

**Major components:**
1. Helm chart (Deployments, Services, Jobs, ScaledObjects, NetworkPolicies) — brings up the full stack and gates KEDA/MCP-HTTP behind values flags
2. `cmd/mcp-http` (new binary) — HTTP-transport MCP entrypoint sharing `internal/mcpserver`'s existing transport-agnostic server
3. `internal/api/system_presets_handlers.go` (new) — system-scope preset REST handlers, gated by a new operator middleware, reusing the existing `PresetAdmin` interface unchanged
4. KEDA `ScaledObject`×3 (image/document/html), explicitly excluding `webhook-worker`, driven by a relocated `octoconv_queue_depth` Prometheus metric served from the always-on `api` process

### Critical Pitfalls

1. Queue-depth chicken-and-egg at zero replicas — `octoconv_queue_depth` is registered inside each worker's own `main()`, so a worker scaled to 0 has no pod exposing the metric KEDA needs to scale it back up. Fix: relocate (or duplicate) `NewQueueDepthCollector` registration into the always-on `api` process before writing any `ScaledObject`.
2. webhook-worker scale-to-zero kills the fleet-wide reconciler sweeper — it is the sole host of the Postgres-advisory-lock singleton sweeper; scaling it to zero silently stops stale-job recovery for image/document/html too, not just webhooks. Fix: no `ScaledObject` on webhook-worker at all — fixed `replicas: 2`.
3. `terminationGracePeriodSeconds` default (30s) vs real engine timeouts (up to 300s for documents) — a scale-down mid-conversion gets SIGKILLed before the handler can finish or record a clean status. Fix: set per-engine-class grace periods derived from real worst-case timeouts + margin, never the default.
4. `METRICS_ADDR=0.0.0.0` without a NetworkPolicy — the localhost-bind removed by this change was the security control (no auth on `/metrics`/asynqmon by design). Fix: ship the bind change and a NetworkPolicy scoping ingress to the Prometheus/kubelet source as one atomic unit, always.
5. OrbStack daemon wedging under heavy parallel build/k8s load — three confirmed incidents in this project's own history, root cause unresolved. Fix: pre-build all 5 images sequentially with non-`latest` tags before iterating on the chart; never run compose and k8s stacks hot simultaneously.

## Key Decisions (Conflicts Resolved)

### Decision 1: Operator gate for system-presets REST — `OPERATOR_CLIENT_IDS` env allowlist + 404-no-leak

**Conflict:** FEATURES.md recommended a new `is_operator boolean` column on `clients` + `manage-clients --operator` CLI flag + explicit 403 for non-operators. ARCHITECTURE.md recommended an `OPERATOR_CLIENT_IDS` env allowlist (parsed once at API startup, no migration) + the existing 404-no-leak convention.

**Resolution:** `OPERATOR_CLIENT_IDS` env allowlist + 404.

**Rationale:**
- CLAUDE.md's own documented architectural constraint states plainly that this codebase has no config-file support; every runtime setting is read from `os.Getenv` — this is a fact about the existing system, not a stylistic preference, and there is direct precedent for exactly this shape of gate (`WEBHOOK_ALLOW_PRIVATE_IPS`, rate-limit env vars). A new migration is a heavier, less-precedented move for a milestone whose actual stated focus and risk budget is infrastructure (KEDA, OrbStack), not schema evolution.
- This codebase has been unusually disciplined about the 404-never-403 distinction since Phase 1 (`D-03`, explicit comment in `internal/api/presets_handlers.go`: cross-client access → 404, never 403) and has held that line through v1.2-v1.5 without exception. Introducing a 403 here would be the first deliberate break of an established, working security-through-non-enumeration pattern for a marginal semantic gain (privilege-failure vs resource-not-found) that the project has evidently not needed to make elsewhere.
- Zero migration = zero new schema-churn risk stacked onto an already infra-heavy milestone.
- Trade-off accepted: changing the operator set requires an API redeploy/restart (acceptable — internal-only, small, slow-changing operator population), and there's no built-in per-operator audit trail beyond whatever logging is added alongside. Document the `is_operator` column as an explicit future Key Decision, to revisit if/when the operator set needs to change without a redeploy or per-operator audit becomes a real requirement.

### Decision 2: MCP HTTP auth model — per-request caller-key pass-through

**Conflict:** FEATURES.md and PITFALLS.md recommended per-request pass-through of the caller's own API key (the `mcp-http` pod stores no key itself — true zero-privilege). ARCHITECTURE.md recommended the pod holding a single startup-loaded key, mirroring the stdio model exactly (`Config.APIKey`/`internal/mcpserver/client.go`'s existing one-key-per-process construction, zero code changes).

**Resolution:** per-request caller-key pass-through.

**Rationale:**
- The entire reason to add an HTTP transport (vs. the existing stdio, one-process-per-caller model) is to let multiple distinct internal automation clients reach the same in-cluster endpoint. A shared, pod-held single key collapses that multi-client reality into one identity: every caller reaching the Service impersonates the same `clients` row, which silently breaks per-client rate limiting, per-client preset scoping, and audit traceability (no way to answer "which `clients` row authorized this call") — a regression of the exact zero-privilege trust model v1.5 Phase 21 was built to establish ("zero-privilege HTTP client with key redaction").
- PITFALLS.md flags this explicitly (Pitfall 5) as a design decision that must be made before wiring the transport, with a HIGH recovery cost if shipped the other way (retrofitting per-request identity into a client built once at process start is a real refactor, not a config flip) — asymmetric risk strongly favors deciding correctly now.
- Implementation shape: the SDK's `getServer(r *http.Request) *mcp.Server` callback (or a wrapping `net/http` middleware, matching the existing chi-middleware style in `internal/api/routes.go`) extracts the caller's own `Authorization`/API-key header and threads it through to a per-request (or per-key-cached) `mcpserver.Client`, reusing the same hash-compare auth logic already used by the REST API — factored out into shared code, not duplicated. Combine with `NetworkPolicy` scoping which pods can reach the Service at all, and `Stateless: true` (no session-affinity requirement) so a plain `Service` can round-robin even with the added per-request auth wiring.
- Trade-off accepted: materially more implementation work than the "pod holds one key" mirror-of-stdio option, and requires a small but real design task (shared auth-check extraction, per-key client caching) rather than being a zero-diff port. This is the correct trade given the stated multi-client reality of an in-cluster HTTP surface.

### Surfaced but not resolved: `local_path` contract gap for remote MCP callers

Every MCP tool response (`convert_file`, `download_result`) includes a `local_path` field describing where the converted file was downloaded on the server's own filesystem (`internal/mcpserver/tools.go`, `client.go`'s `Download`). This is correct and meaningful under stdio (server and calling MCP host share a filesystem) but becomes inert/misleading once `mcp-http` runs as a remote in-cluster pod — the path refers to an ephemeral pod filesystem no remote caller can ever reach. This is not a crash or a code-breaking bug, but it is a real user-facing contract mismatch that this research does not resolve — it is a roadmap-level decision for the MCP HTTP phase. Options to weigh during phase planning:

1. Omit `local_path` entirely in HTTP mode — gate on a `Config.SkipLocalDownload`-style flag; simplest, smallest diff, but changes the tool's response shape depending on transport (callers must know which transport they're on).
2. Return presigned-URL-only for the HTTP transport — treat `PresignedURL`/`DownloadURL` as the sole authoritative result field when running as `mcp-http`; effectively a specialization of option 1 with clearer intent (the tool never claims to have staged a local file when there is no shared filesystem).
3. Add a download-proxy tool — a new MCP tool that streams the converted file's bytes back to the caller directly over the MCP transport (rather than relying on the caller reaching a presigned S3 URL independently). More capability (works even if the caller can't reach MinIO directly), but adds real complexity — binary streaming over MCP, size limits, and a second code path alongside the existing presigned-URL flow.

No default is recommended here; this should be an explicit decision captured when the MCP HTTP phase is planned.

## Implications for Roadmap

Based on research, suggested phase structure (six phases, with a hard-ordered Chart Core → Queue-Depth Fix → KEDA → Load-Proof spine and two independent, freely-orderable phases for MCP HTTP and system-presets REST):

### Phase 1: Helm Chart Core & Landmine Closure
**Rationale:** Foundational — every other phase either deploys through this chart or reuses its conventions (Secrets/ConfigMap shape, namespace, probe pattern, NetworkPolicy pattern). Highest-risk, most novel, SEED-004-flagged work; must go first.
**Delivers:** `helm install` brings up the full stack (Postgres, Redis, MinIO, api, 4 worker classes, asynqmon) on OrbStack k8s; migrate/createbucket run exactly once via `pre-install,pre-upgrade` hook Jobs; in-cluster E2E passes as a Kubernetes Job.
**Addresses:** Table-stakes Helm UX (probes, hook Jobs, Secret/ConfigMap shape) from FEATURES.md.
**Avoids:** Pitfall 4 (METRICS_ADDR bind without NetworkPolicy — ship as one atomic unit), Pitfall 7 (OrbStack wedging), Pitfall 8 (E2E host-gateway/presigned-DNS landmines).
**Critical sequencing facts baked into this phase:**
- `METRICS_ADDR=0.0.0.0` change and its compensating `NetworkPolicy` must ship together, never as a follow-up — this is the direct in-cluster replacement for the security property `127.0.0.1`-binding used to provide.
- `terminationGracePeriodSeconds` must be set per engine-class Deployment, derived from real worst-case engine timeouts + margin (image >=120s, document >=300s, html >=60s) — never left at Kubernetes' 30s default, or a legitimate long conversion gets SIGKILLed mid-flight.
- `S3_ENDPOINT` must use the fully-qualified `<service>.<namespace>.svc.cluster.local` form, not a short Service name — resolvable from both in-cluster pods and the OrbStack host, closing the presigned-URL landmine for both consumer classes.
- Sequential image pre-build discipline for OrbStack: build all 5 images once, sequentially, with fixed non-`latest` tags, before iterating on the chart — do this as an explicit pre-flight task, not incidentally. Never run the compose stack and the k8s stack hot simultaneously.
**Research flags:** Needs research — OrbStack-specific k8s networking/FQDN host resolution behavior (verified via docs only, not against a live chart yet), exact current Helm hook-ordering guarantees on `helm upgrade` (not just `install`).

### Phase 2: MCP Streamable HTTP Endpoint
**Rationale:** No code/architecture dependency on KEDA work — only a soft dependency on Phase 1 for deployment conventions (Secret/ConfigMap, NetworkPolicy pattern, probe shape). Small, independent, low-risk relative to the KEDA arc — sequencing it early validates the chart's conventions on a smaller surface before the higher-stakes KEDA phases.
**Delivers:** `cmd/mcp-http` + `Dockerfile.mcp-http` + chart template, gated by `mcpHttp.enabled`; per-request caller-key pass-through auth (Decision 2); `Stateless: true`; pinned to a single replica (session-state limitation, Pitfall 6).
**Uses:** `internal/mcpserver`'s existing transport-agnostic `NewServer`; go-sdk v1.6.1's `NewStreamableHTTPHandler` (already pinned, zero version bump).
**Implements:** Shared auth-check logic factored out so both `internal/api` and `cmd/mcp-http` validate the same API-key header without duplicating hash-compare code.
**Research flags:** Needs research — go-sdk v1.6.1's `Stateless: true` interaction with `convert_file`'s in-flight progress-notification streaming is explicitly flagged LOW confidence in FEATURES.md and must be live-verified before committing to it; also verify at execution time whether v1.6.1 ships server-side Streamable HTTP at all. Resolve the `local_path` contract gap (see above) as an explicit phase-planning decision.

### Phase 3: system-presets REST (Operator Gate)
**Rationale:** Fully independent of all Kubernetes work — a pure `internal/api` change with no chart dependency beyond eventually adding `OPERATOR_CLIENT_IDS` to the ConfigMap (trivial whenever it lands). Can run in parallel with or interleaved before/after Phase 2.
**Delivers:** `/v1/system/presets` route group + `RequireOperator` middleware checking `client.ID` against an `OPERATOR_CLIENT_IDS` env allowlist (Decision 1); non-operator access returns the same uniform 404 the codebase already uses for cross-client access, never 403.
**Addresses:** "System-preset writes require an explicit operator credential" from FEATURES.md table stakes.
**Avoids:** Introducing a second parallel auth model or breaking the established no-leak-404 discipline.
**Research flags:** Standard pattern — skip research-phase. `PresetAdmin` is already scope-agnostic (verified: `internal/api/presets_handlers.go`), so this is additive REST handlers + one middleware, not a domain change.

### Phase 4: Queue-Depth Exposition Fix
**Rationale:** Hard prerequisite for Phase 5 (KEDA). PITFALLS.md is explicit that this must be resolved "before writing any ScaledObject manifest," as an early, isolated gating step — not discovered during the load-proof, by which point the wrong architecture would already be baked into the chart. This is pure Go code (no chart/manifest changes), so it can and should be validated against the existing compose E2E suite before any k8s work touches it.
**Delivers:** `NewQueueDepthCollector` registration relocated into (or duplicated into) the always-on `api` process for all four queues (image/document/html/webhook), so the metric remains scrapeable even when a worker Deployment is at zero replicas. Confirm via `kubectl get --raw` on the external metrics API that the value resolves correctly at genuinely zero worker replicas, not just at N.
**Avoids:** Pitfall 1 (queue-depth chicken-and-egg at zero replicas) — the single most load-bearing fix this milestone must not skip or defer.
**Research flags:** Standard pattern — low research need; this is a targeted, code-verified relocation of existing, working collector code, not new design.

### Phase 5: KEDA Autoscaling (ScaledObjects)
**Rationale:** Depends on Phase 1 (deployable, NetworkPolicy-scoped `/metrics`) and Phase 4 (queue-depth exposed from an always-on component). Closes the milestone's other stated headline goal.
**Delivers:** `ScaledObject`x3 (image/document/html) via the Prometheus scaler against the relocated `octoconv_queue_depth` metric, `keda.enabled` chart gate; `webhook-worker` explicitly excluded and shipped as a fixed `replicas: 2` Deployment alongside this phase, not as an afterthought.
**Addresses:** "Per-engine-class KEDA ScaledObjects that visibly scale 0->N->0" from FEATURES.md.
**Avoids:** Pitfall 2 (webhook-worker scale-to-zero kills the fleet-wide sweeper) — treat `minReplicaCount >= 1`/no-ScaledObject-at-all as a fail-closed hard gate on this Deployment, not a tunable.
**Critical sequencing facts:** webhook-worker's exclusion from KEDA must ship in this same phase, not deferred — it is correctness-critical, not optional. Tune `pollingInterval`/`cooldownPeriod` per engine class (image: fast/bursty, shorter cooldown; document/html: slow tasks, longer cooldown so one long-running task doesn't read as sustained load or premature idleness).
**Research flags:** Needs research at execution time — exact KEDA Prometheus scaler tuning for this project's real task-duration distributions (demo-tuned defaults from research, e.g. `pollingInterval: 5-15s`, `cooldownPeriod: 60s`, are a starting point, not production values).

### Phase 6: Load-Proof / Autoscale Validation
**Rationale:** Depends on Phase 5. Closes the milestone's actual stated Core Value ("воркеры автоскейлятся KEDA... доказано"). Phases 1->4->5->6 form the primary, sequential, hard-ordered arc; Phases 2 and 3 have no ordering constraint relative to this spine or each other.
**Delivers:** A documented, timestamped 0->N->0 demonstration: queue depth at 0 with worker replicas at 0 (steady state) -> burst load -> time-to-first-replica -> time-to-drain -> time-to-scale-to-zero, with pod count and `octoconv_queue_depth` plotted on the same time axis.
**Avoids:** The "looks done but isn't" trap of only proving the N->0 leg (a running pod can always report its own draining metric) — the 0->N leg must be verified separately with the worker genuinely at 0 replicas and a freshly-filled queue. Also load-tests scale-down with a long-running document job in flight to verify Phase 1's `terminationGracePeriodSeconds` choice actually holds under real conditions (not just image, which is fast).
**Research flags:** Standard pattern — mostly execution/observation and load-generation scripting; low research need beyond what Phases 1/4/5 already established.

### Phase Ordering Rationale

- 1 -> 4 -> 5 -> 6 is the only hard-ordered sequence. The chart must exist and expose a NetworkPolicy-scoped `/metrics` (Phase 1) before the queue-depth metric can be relocated and validated at zero replicas (Phase 4), which must complete before any `ScaledObject` is written (Phase 5), which must complete before the load-proof can meaningfully run (Phase 6).
- Phases 2 (MCP HTTP) and 3 (system-presets REST) are fully independent of the KEDA spine and of each other. ARCHITECTURE.md's own recommended sequencing (A -> D -> E -> B -> C, i.e. chart core, then MCP HTTP and presets REST, then KEDA, then load-proof) front-loads these two small, low-risk, independently-shippable phases to validate chart conventions before committing to the higher-stakes, harder-to-unwind KEDA work — but a roadmap author may freely reorder or interleave them (e.g. run the presets REST phase first since it needs zero k8s context at all) without breaking any real dependency.
- This grouping avoids the two most severe pitfalls by construction: separating the queue-depth fix into its own phase (4) forces it to be resolved before ScaledObjects exist, rather than discovered mid-load-proof; and calling out webhook-worker's KEDA exclusion as a deliverable of Phase 5 itself (not a "phase 7 cleanup") prevents the natural "apply the same template to every worker" mistake PITFALLS.md flags as the most common failure mode here.

### Research Flags

Needs research (deeper investigation likely required during phase planning):
- Phase 1: OrbStack-specific k8s networking/FQDN host-resolution behavior, exact Helm hook re-run semantics on `helm upgrade` vs `install`.
- Phase 2: go-sdk v1.6.1's `Stateless: true` mode interaction with `convert_file`'s progress-notification streaming (explicitly flagged LOW confidence); confirm server-side Streamable HTTP is actually present at this pinned version.
- Phase 5: Real per-engine-class KEDA `pollingInterval`/`cooldownPeriod` tuning against this project's actual task-duration distributions (research supplies demo-tuned starting values only).

Phases with standard/well-documented patterns (skip `--research-phase`):
- Phase 3: Pure `internal/api` extension of an already scope-agnostic interface + one middleware — no novel pattern.
- Phase 4: Targeted relocation of existing, working collector code — no new design.
- Phase 6: Execution/observation and load-generation scripting against already-decided infrastructure.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH for versions (live-fetched GitHub Releases/Helm index/official docs); MEDIUM for packaging opinions (Bitnami avoidance, hand-rolled Postgres/Redis/MinIO, single-vs-umbrella chart) — informed synthesis, not one canonical source |
| Features | MEDIUM — KEDA/Helm feature patterns are well-documented and stable; go-sdk streamable-HTTP session-semantics specifics (`Stateless: true` + progress streaming) are explicitly flagged LOW and need live validation |
| Architecture | HIGH — every finding is anchored to a direct read of this repo's own source (`cmd/*/main.go`, `docker-compose*.yml`, `internal/e2e/e2e_test.go`, `internal/mcpserver/*.go`, `internal/api/presets_handlers.go`, migrations, CI workflow); MEDIUM specifically for OrbStack-networking claims (one WebSearch against OrbStack's own docs, not independently tested against this repo's actual chart yet) |
| Pitfalls | HIGH for code-grounded findings (queue-depth exposition, sweeper singleton, MCP key model, OrbStack wedge history — all verified directly against this repo's source and `.planning/RETROSPECTIVE.md`); MEDIUM for general KEDA/Helm/OrbStack-k8s mechanics (verified against current docs/GitHub issues, not against this project's own cluster) |

**Overall confidence:** HIGH on what must architecturally happen and in what order; MEDIUM on exact tuning values (probe thresholds, `cooldownPeriod`/`pollingInterval`) and on OrbStack-specific networking edge cases, both of which require live verification against an actual running chart rather than documentation alone.

### Gaps to Address

- `local_path` contract gap for remote MCP callers (see Key Decisions) — three options proposed, no default recommended; must be an explicit decision when Phase 2 is planned.
- go-sdk v1.6.1's exact Streamable HTTP session semantics — whether `Stateless: true` truly preserves `convert_file`'s progress-notification streaming is asserted from documentation/API-shape inspection only, not a live smoke test; verify before committing to the stateless design in Phase 2's plan.
- OrbStack Kubernetes' exact current version and PVC persistence behavior across `helm uninstall`/reinstall — inferred from release-notes history, not independently confirmed; verify with `kubectl version` and a live PVC-survival test at execution time, do not assume compose-volume-like persistence.
- asynq v0.26.0's `Server.Shutdown()` exact blocking/deadline semantics — needed to confirm the `terminationGracePeriodSeconds` fix (Phase 1) doesn't reproduce the same mismatch inside asynq's own shutdown path; flagged in PITFALLS.md as something to verify specifically, not assumed from the general asynq API surface.

## Sources

### Primary (HIGH confidence)
- Direct repository reads: `docker-compose.yml`, `cmd/api/main.go`, `cmd/worker/main.go`, `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go`, `cmd/webhook-worker/main.go`, `internal/metrics/queue_collector.go`, `internal/reconciler/reconciler.go`, `internal/mcpserver/{mcpserver,config,client,tools}.go`, `cmd/mcp-server/main.go`, `internal/api/{presets_handlers,api,routes}.go`, `internal/db/migrations/{0001_init,0002_client_api_keys}.sql`, `internal/e2e/e2e_test.go`, `.github/workflows/ci.yml`
- `.planning/PROJECT.md`, `.planning/seeds/SEED-004.md`, `.planning/RETROSPECTIVE.md` (OrbStack wedge history)
- https://github.com/kedacore/keda/releases, https://kedacore.github.io/charts/index.yaml, https://keda.sh/docs/2.20/ (scalers/prometheus, reference/scaledobject-spec) — live-fetched
- https://github.com/hibiken/asynq source (`internal/base/base.go`, `internal/rdb/rdb.go`) — confirms Redis pending-key structure
- https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk@v1.6.1/mcp — confirms `NewStreamableHTTPHandler` API shape at the exact pinned version
- https://github.com/helm/helm/releases, https://docs.orbstack.dev/kubernetes/ — live-fetched

### Secondary (MEDIUM confidence)
- https://github.com/bitnami/charts/issues/35164 + Broadcom migration guidance + corroborating secondary sources (Chainguard, Minimus, Chkk)
- MinIO Operator/community-chart state via GitHub discussions + artifacthub (no single canonical current-state doc)
- General Helm umbrella-vs-single-chart guidance (multiple blog sources, no single canonical spec)
- KEDA flapping/scale-to-zero GitHub issues (#770, #2314, discussion #6443)
- Kubernetes SIGTERM/grace-period best-practice write-ups (Google Cloud, CNCF)

### Tertiary (LOW confidence, flagged for live validation)
- go-sdk `Stateless: true` interaction with `convert_file`'s progress-notification streaming — needs a live smoke test before committing
- OrbStack's exact current Kubernetes version and PVC-persistence-across-reinstall behavior — inferred from release-notes/issue history, not one canonical page

---
*Research completed: 2026-07-14*
*Ready for roadmap: yes*
