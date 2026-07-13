# Feature Research

**Domain:** Internal Go microservice moving from Docker Compose to Kubernetes + KEDA; adding an in-cluster MCP HTTP endpoint and an operator-scoped presets REST API
**Researched:** 2026-07-14
**Confidence:** MEDIUM (Helm/KEDA patterns are well-documented and stable; go-sdk streamable-HTTP specifics are current-training-data + one doc fetch, not independently verified against a running cluster — flagged LOW where noted)

## Recommended Defaults — Answer to Every Open Question

This section directly answers the milestone's five open questions before the standard feature-landscape tables below (per the quality gate: every question needs an explicit recommended default).

### 1. Helm chart UX

- **`values.yaml` surface (table stakes, not more):** per-service `image.repository`/`image.tag` (default `tag: latest` overridden by `--set` in local dev — OrbStack's k8s pulls from its own local image cache when `imagePullPolicy: IfNotPresent` and the tag matches an image already `docker build`-ed on the host; no registry push needed), `replicaCount` (fixed int, not autoscaling — KEDA owns the workers), `resources.limits/requests` (port directly from existing `docker-compose.yml` `cpus:`/`memory:` values: image/document workers `2 CPU / 1Gi`, chromium-worker `2 CPU / 2Gi`), and `env` overrides for anything not templated as a Secret. Secrets (`API_KEY_SALT`, `WEBHOOK_SIGNING_SECRET`, Postgres/MinIO credentials) go into a single templated `Secret` object populated from `values.secrets.*`, **never committed with real values** — local dev supplies them via `--set` or a git-ignored `values.local.yaml`, mirroring the existing `.env`-is-gitignored convention.
- **`NOTES.txt`:** yes, include one — it's near-zero cost and is the idiomatic place to print the in-cluster API URL, the `kubectl port-forward` command for local access, and a reminder that `/metrics` is intentionally not exposed outside the cluster. Table stakes for "helm install works" as a self-documenting artifact.
- **`helm test` hooks:** recommend **skip for this milestone**. A `helm test` Job duplicates work the E2E adaptation (SEED-004 item 4) already does more thoroughly (full upload→convert→webhook round trip). Adding a second, thinner test surface is scope creep for a first infra milestone — defer until there's a second consumer of the chart (e.g., a staging environment) that actually needs a fast smoke gate independent of the full E2E suite.
- **Readiness/liveness probes:**
  - **api**: `httpGet /healthz` for both liveness and readiness — it already pings Postgres/Redis/S3 and returns 503 on degradation (`internal/api/handlers_test.go:1015-1041`), which is exactly what a readiness probe wants. Do not also use it as a naive liveness probe without a longer `failureThreshold`/`periodSeconds` — a transient S3 blip should pull the pod out of Service rotation (readiness) but should **not** restart the process (liveness), since restarting doesn't fix an external dependency outage and causes unnecessary churn. Recommended split: liveness = same `/healthz` path but tolerant (`failureThreshold: 6`, `periodSeconds: 10`, i.e. ~60s of sustained failure before restart); readiness = tighter (`failureThreshold: 2`, `periodSeconds: 5`).
  - **workers (image/document/html/webhook)**: none of them expose an HTTP surface except the metrics port. Two real options: (a) `exec` probe shelling a trivial process check (e.g. `pgrep` the worker binary, or a lock-file heartbeat) — brittle, adds shell dependency to already-minimal `debian:bookworm-slim` images; (b) point liveness/readiness at the already-existing `/metrics` endpoint once `METRICS_ADDR` is changed from `127.0.0.1` to `0.0.0.0` (SEED-004 landmine 1) — a 200 on `/metrics` proves the process's HTTP listener goroutine is alive and the binary hasn't deadlocked, which is a legitimate (if indirect) liveness signal for a worker whose real job (draining a queue) isn't otherwise probeable over HTTP. **Recommended default: (b), httpGet on `/metrics`.** It reuses infrastructure that already exists, costs zero new code, and is consistent across all four worker binaries. Do not conflate this with removing localhost-only metrics security — pair it with a `NetworkPolicy` restricting `:9090` ingress to only the Prometheus/kubelet source, so widening the bind address doesn't silently regress the "operational data isn't public" decision already recorded in Key Decisions.
- **Migrate as Helm hook vs initContainer — recommend the hook.** A `pre-install,pre-upgrade` hook Job (mirroring `cmd/migrate`'s existing one-shot CLI semantics) runs exactly once per install/upgrade, before any Deployment rolls out — this is a direct, low-risk port of the current `createbucket`-style one-shot pattern (SEED-004 landmine 3) and matches documented Helm best practice (Jobs, not per-pod initContainers, avoid N-replica races where multiple pods each try to run the same migration concurrently). An initContainer on the `api` Deployment would re-run the migration on every pod restart/scale event, which is harmless here only because `cmd/migrate` is already idempotent (`internal/db/db.go` embedded-migration runner tracks applied migrations) — but it's still the wrong tool: it couples migration timing to whichever pod happens to (re)start first, offers no single clear failure point to `helm install --wait`, and would run redundantly on every KEDA scale-up of a worker if ever mis-attached there. Use `helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded` so the Job is retained on failure for `kubectl logs` debugging but cleaned up on the next successful run, and `backoffLimit: 0` so a broken migration fails loudly instead of silently retrying.

### 2. KEDA UX

- **What a convincing 0→N→0 proof looks like:** capture, with timestamps, all of: (a) queue depth at 0, worker replicas at 0 (steady state before the burst); (b) a burst of N jobs submitted via the API in a tight loop; (c) time-to-first-replica (KEDA's `pollingInterval` tick that first observes `octoconv_queue_depth{queue="image",state=~"pending|active"} > 0` and asks the HPA to scale up from 0→1, then the HPA's normal reactive scaling 1→N as depth climbs); (d) time-to-drain (last job's `job_events` row hits `done`/`failed`); (e) time-to-scale-to-zero (`cooldownPeriod` after depth returns to 0, KEDA scales the Deployment back to 0). Plot pod count and `octoconv_queue_depth` on the same time axis (Prometheus/Grafana or even a `kubectl get pods -w` timestamped log correlated against a `promtool query` loop) — a reviewer needs to see the causal chain, not just "it scaled."
- **Metrics to observe:** `octoconv_queue_depth{queue="image",state="pending"}` (the KEDA trigger signal itself), pod count over time (`kubectl get deployment octoconv-worker -w` or `kube_deployment_status_replicas` if kube-state-metrics is available), and job-outcome metrics already emitted (`internal/metrics` — conversion duration/outcome) to prove the scaled-up replicas actually did useful work, not just existed.
- **Timing windows — recommended defaults for a snappy, legible demo (not production tuning):** `pollingInterval: 5` (seconds; KEDA default is 30 — too slow to eyeball in a demo), `cooldownPeriod: 60` (KEDA default is 300; 60s is long enough to avoid obvious flapping but short enough that a demo doesn't require minutes of dead air after drain). Document explicitly that these are demo-tuned, not production-tuned — production would likely widen `cooldownPeriod` since cold-starting a container (pulling the image, `libvips`/LibreOffice/Chromium startup cost) is more expensive than a few extra idle minutes of one replica.
- **Flapping protection:** two independent mechanisms, both needed. (1) KEDA's own `cooldownPeriod` gates the 1→0 transition specifically (scale-up is not similarly debounced by KEDA itself — that's delegated to the HPA it manages). (2) For the HPA-managed 1→N range, set `advanced.horizontalPodAutoscalerConfig.behavior.scaleDown.stabilizationWindowSeconds` (e.g. 60s) so a queue depth that briefly dips to 0 mid-burst (a natural artifact of jobs finishing faster than new ones are being submitted) doesn't cause a premature scale-down mid-burst, only to immediately scale back up — the classic flapping failure mode this milestone must visibly avoid.
- **`minReplicaCount` per worker class — 0 for image/document/html, but the webhook-worker is a hard exception:**
  - image-worker, document-worker, chromium-worker (html): `minReplicaCount: 0`. Each is a pure queue consumer with no other duty; scaling to zero when idle is exactly the target behavior this milestone proves.
  - **webhook-worker: `minReplicaCount` must NOT be 0 — flag this explicitly, it is the single most important interaction in this research.** The webhook-worker container is not just a webhook-queue consumer: it is also the exclusive host of the reconciler sweeper (`internal/reconciler.RunWithLock`, gated by a Postgres session-level advisory lock so exactly one replica across the fleet acts as sweeper at any time — see `internal/reconciler/reconciler.go:95-226`). If KEDA ever scaled the webhook-worker Deployment to zero replicas, **the sweeper stops entirely**: no replica exists to hold the advisory lock, so no process ever recovers jobs stranded in `queued`/`active` past their staleness threshold, and no process ever detects/repairs `webhook_deliveries` gaps (RECON-04). This is a silent, delayed failure mode — nothing errors immediately, jobs just accumulate as permanently stuck rows the next time a worker crashes mid-task, with detection only surfacing much later (or never) because the very component that would notice is asleep. **Recommended default: do not put a KEDA `ScaledObject` on the webhook-worker at all this milestone.** Keep it a plain Kubernetes `Deployment` with a fixed `replicas: 2` (porting the existing docker-compose two-replica-always-on redundancy design verbatim — that redundancy was deliberately built in v1.3 Phase 16 precisely so sweeper failover survives a single replica's crash/deploy, ~11s observed failover). If a future milestone wants KEDA on the webhook queue too (e.g. to add extra webhook-delivery throughput under a bursty callback storm), the correct shape is `minReplicaCount: 2` (never 0, never even 1 — 1 replica has no failover partner) with the KEDA trigger only adding replicas beyond the floor, never removing the sweeper-hosting floor. That is more complexity than this milestone's KEDA proof needs; the simple, safe default is "webhook-worker opts out of KEDA entirely."
- **Load-generation approach:** a small script/CLI (reuse an existing internal test client or a plain `curl` loop against `POST /v1/jobs`) that authenticates with a real client API key and bursts N image-conversion jobs (recommend N≈30-50 — enough to visibly exceed single-replica throughput and force multi-replica scale-up under the existing per-job `ENGINE_TIMEOUT`/concurrency, but small enough to drain in well under a minute so the demo stays legible) against a small fixture image. Submit the burst in a tight loop (not one every few seconds) so the queue-depth spike is unambiguous in the metrics graph, then let the system settle to prove the full 0→N→0 arc unattended.

### 3. MCP HTTP endpoint

- **Auth model: pass-through of the existing per-client API key via header, checked by HTTP middleware wrapping the SDK's `StreamableHTTPHandler` — not mTLS.** The `getServer` callback the SDK's `mcp.NewStreamableHTTPHandler(getServer func(*http.Request) *Server, opts *StreamableHTTPOptions)` takes receives the full `*http.Request`, so auth can either happen in that callback or (cleaner, and consistent with the existing chi-middleware style in `internal/api/routes.go`) in a standard `http.Handler` middleware wrapping the SDK handler before it's mounted. Recommend reusing the same `X-API-Key`-style header and hashed-lookup-against-`clients` mechanism the REST API already uses (do not invent a second credential type for the same trust domain) — factor the existing auth-check logic out of `internal/api` into something both packages can call, rather than duplicating the hash-compare code. mTLS is unwarranted complexity for this milestone: the constraint is "внутренние клиенты, безопасно через API-ключ" — mTLS would require a cert-issuance/rotation story the project doesn't have and doesn't need yet; a `NetworkPolicy` restricting which in-cluster pods can reach the MCP Service, combined with the existing API-key check, matches the trust model already used everywhere else in the system.
- **Session semantics of streamable HTTP in go-sdk v1.6.1:** the SDK exposes `Mcp-Session-Id` header-based session tracking by default (stateful mode), with a `StreamableHTTPOptions.Stateless` flag to opt out, and a `GetSessionID` override for custom extraction. **Recommend `Stateless: true` for the in-cluster endpoint.** Rationale: (a) the existing tool set (`convert_file`, `get_job_status`, `download_result`, `list_supported_formats`, `list_presets`) is already designed so each call is self-contained — `convert_file` blocks and streams progress within a single request/response, and follow-up polling explicitly uses `get_job_status` with a job id rather than relying on server-held session state; (b) stateless mode means a plain Kubernetes `Service` can round-robin across MCP replicas without needing session affinity (`sessionAffinity: ClientIP`) — sticky sessions are one more moving part this first infra milestone doesn't need to introduce. Flag as **LOW confidence** pending a live smoke test: verify specifically that `convert_file`'s in-flight progress-notification stream still works correctly end-to-end in stateless mode before committing to it (this is exactly the kind of claim that must be verified against the real SDK behavior, not asserted from documentation alone).
- **Confirm: the stdio binary and the HTTP endpoint MUST share tool code — this is already true today, not a redesign.** `internal/mcpserver.NewServer(cfg, client) *mcp.Server` builds a transport-agnostic `*mcp.Server` with all five tools registered; the *only* transport-specific code is one line in `cmd/mcp-server/main.go` (`srv.Run(ctx, &mcp.StdioTransport{})`). Adding the HTTP endpoint is additive: a new `cmd/mcp-http-server` (or a flag on the existing binary) that instead does `http.ListenAndServe(addr, authMiddleware(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, opts)))`. Do **not** fork or duplicate `internal/mcpserver/tools.go` — that would be the anti-pattern this question is explicitly guarding against.

### 4. system-presets REST (PRAPIV2-01) — operator authorization

- **Recommended minimal-safe option: add an `is_operator boolean not null default false` column to the `clients` table (new migration), checked by the existing API-key auth middleware, gating only the system-scope preset write routes.** This is the option most consistent with the project's own stated constraint ("Auth: API-ключи через существующую таблицу `clients` — не вводить отдельный внешний auth-провайдер") and its established convention of extending `clients` for new authorization concerns (the two-slot rotation columns in `0002_client_api_keys.sql` are the direct precedent: add columns to `clients`, don't introduce a parallel credential system). Concretely:
  - New migration adds `is_operator` (boolean, default false) to `clients`.
  - `cmd/manage-clients` gets a new flag (e.g. `--operator`) to set/unset it on `create`/an existing client — reusing the CLI that already owns client lifecycle, not a new tool.
  - The existing per-request auth middleware, which already resolves the request's `client_id` from the API key, additionally loads `is_operator` and stores it on request context (mirroring how `client_id` is already threaded through).
  - System-scope preset write handlers (`POST/PUT/DELETE` equivalents for `scope=system` in `internal/presets`) check `is_operator` and reject with **403** (not 404) when false — this is a deliberate departure from the existing "cross-client → 404, never 403" convention, and the departure is correct: that convention exists to avoid leaking *whether a specific resource exists* to a client who shouldn't see it (an ownership/enumeration concern). Insufficient privilege to perform an operator-only *action* is a different kind of failure — there is nothing to hide about the existence of the system-presets endpoint itself, and a plain 403 is the standard, unambiguous signal for "this exists, you're just not allowed."
  - Reads of system presets (`GET`) can remain open to any authenticated client exactly as today — `Resolve`/`GetForClient`/`ListForClient` in `internal/presets/repo.go` already let any client read system-scope presets; only the *write* path is operator-gated.
- **Why not a separate `ADMIN_API_KEY` env var:** it introduces a second, out-of-band secret with no rotation story, no audit trail tying an action back to a specific `client_id` (breaking the existing `job_events`/audit-style traceability pattern used everywhere else), and duplicates auth infrastructure instead of reusing what already exists — inconsistent with the "не вводить отдельный... провайдер" constraint's spirit even though it's technically the same *mechanism* (a bearer credential). The `is_operator` column keeps exactly one authentication system and one authorization model in the codebase.

### 5. Anti-features — explicitly out of scope this milestone

- **No public ingress / TLS story.** Local validation on OrbStack k8s only needs `kubectl port-forward` or a `ClusterIP`/`NodePort` Service for manual poking — do not build an `Ingress` resource, cert-manager wiring, or any TLS termination. The constraint stack already defers "Kubernetes + KEDA" as the current infra frontier and public multi-tenancy is explicitly out of scope; adding ingress/TLS now is solving a problem (external exposure) this milestone doesn't have.
- **No multi-environment `values.yaml` sprawl.** Resist the urge to create `values-dev.yaml`/`values-staging.yaml`/`values-prod.yaml` from day one. This milestone has exactly one target (local OrbStack k8s) — a single `values.yaml` with sane local defaults, overridable via `--set`/`-f values.local.yaml` (git-ignored), is table stakes; a multi-environment values hierarchy is premature structure for a chart that has never been used against a second environment.
- **No CD pipeline.** `helm install`/`helm upgrade` run manually against the local cluster for this milestone's proof. Wiring the existing GitHub Actions CI (which already bakes 5 images) into an automatic `helm upgrade` step is a reasonable *future* milestone, not this one — there is no remote cluster to deploy to yet, and building deployment automation before there's a real deployment target is speculative.
- **No KEDA on the webhook-worker (repeated here as an anti-feature, not just a caveat).** As detailed in the KEDA section above: the webhook-worker hosts the sweeper and must never scale to zero. The *simplest* correct move is not "KEDA with `minReplicaCount: 2`" but "no `ScaledObject` at all for this Deployment" — keep it a plain fixed-`replicas: 2` Deployment. Treat any temptation to autoscale it as scope creep this milestone doesn't need.
- **No KEDA/HPA on the MCP HTTP endpoint or the API Deployment this milestone.** The milestone's KEDA proof is specifically about the engine-class queue-depth architecture (image/document/html workers) — that is the thing SEED-004 says needs proving. The API and MCP server are request/response services with no queue-depth signal to scale on; leave them as plain fixed-replica Deployments. Introducing a second autoscaling *kind* (e.g. CPU-based HPA on the API) this milestone dilutes the actual target of the proof.
- **No CAD/av/archive engine classes, no full Handler/Capability contract rework** — already correctly out of scope per PROJECT.md; unaffected by this infra milestone and not to be pulled in "while we're touching the worker Deployments anyway."

## Feature Landscape

### Table Stakes (Users Expect These)

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| `helm install` brings up the entire stack (Postgres, Redis, MinIO, api, 4 worker classes, asynqmon) in one command | This is the literal definition of "we have a Helm chart" — a chart that requires manual post-install steps isn't a chart, it's a partial template set | MEDIUM | Postgres/Redis/MinIO can be plain Deployments+PVCs for local dev (not full HA StatefulSets/operators) — matches compose's own non-HA local setup; no need to over-engineer datastore HA for a local validation milestone |
| Readiness probes gate Service traffic correctly per component | Kubernetes' own contract — a pod that isn't ready must not receive traffic, or requests silently fail during rollout/scale events | LOW | api: `/healthz` (already exists, already dependency-aware). Workers: `/metrics` once `METRICS_ADDR` is `0.0.0.0` — see recommended defaults above |
| Migration and bucket-creation run exactly once, before anything depends on them, with clear ordering | Directly ports two `docker-compose.yml` one-shot patterns (`createbucket`, embedded migration runner) that already work — must not regress on the k8s port | MEDIUM | Helm `pre-install,pre-upgrade` hook Jobs (see recommended default above); this is SEED-004 landmine 3 |
| Secrets/config are never baked into the chart's committed defaults | Matches existing `.env`-is-gitignored / `.env.example`-documents-shape convention; a chart that ships real secrets in `values.yaml` is a supply-chain hazard even for "internal only" | LOW | Templated `Secret` object populated from `values.secrets.*`, left empty/placeholder in the committed chart, supplied locally via `--set` or a git-ignored overlay |
| Per-engine-class KEDA `ScaledObject`s that visibly scale 0→N→0 under a real load burst | This is the milestone's stated headline goal (SEED-004) — the entire point of the engine-class queue architecture built since v1.2 is to prove independent per-class autoscaling actually works, not just compiles | HIGH | Prometheus scaler against `octoconv_queue_depth{queue=...,state=~"pending|active"}` — the metric already exists (`internal/metrics/queue_collector.go`), zero new instrumentation needed |
| MCP tool code is identical between stdio and HTTP transports | Already true today (`internal/mcpserver.NewServer` is transport-agnostic) — users/reviewers will immediately notice and flag a fork | LOW | New thin entrypoint only; see recommended default #3 above |
| System-preset writes require an explicit operator credential, not just "any valid client key" | System presets affect every client (`ListForClient`'s shadow-merge means a system preset is visible to all callers) — letting any authenticated client edit them would let one internal service silently change conversion behavior for every other internal service | MEDIUM | `is_operator` column + 403 gate; see recommended default #4 above |

### Differentiators (Competitive Advantage)

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| A load-proven 0→N→0 autoscale demonstration with an explicit timeline (queue depth graph + pod count over time) | Converts the "KEDA — будущее" line in PROJECT.md's own Constraints from an unverified hypothesis into a demonstrated fact, closing SEED-004's stated purpose outright | MEDIUM | The differentiator isn't KEDA itself (table stakes for any k8s-native queue consumer) — it's the *rigor* of the proof: explicit timestamps, explicit flapping-protection reasoning, explicit acknowledgment of the sweeper exception |
| A single shared `internal/mcpserver` tool implementation exposed over two transports (stdio for local dev tooling, streamable HTTP for in-cluster automated consumers) | Lets internal services and human developer tooling use the exact same conversion capabilities without maintaining two integrations | LOW (already structurally free — see Table Stakes) | The differentiator is architectural discipline already paid for in Phase 21; this milestone just adds the second transport |
| Operator-role authorization layered onto the existing single-credential-type API-key model, without introducing a second auth system | Keeps the "internal clients, minimal auth surface" trust model intact while still supporting a real privilege distinction (any client vs. operator) that the system genuinely needs now that system-scope presets are writable via REST | LOW | The differentiator is restraint — solving the authorization problem with the smallest addition to an already-trusted model, not a full RBAC system |

### Anti-Features (Commonly Requested, Often Problematic)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|------------------|-------------|
| KEDA `ScaledObject` (including scale-to-zero) on the webhook-worker | "Consistency" — every other engine-class worker gets one, why not this one too? | Scales away the only host of the reconciler sweeper (Postgres advisory-lock-elected, singleton-by-design) — silent, delayed failure: stranded jobs and webhook-delivery gaps stop being detected/repaired with no immediate error | Plain fixed `replicas: 2` Deployment, no `ScaledObject` at all (see recommended default above) |
| mTLS between in-cluster MCP consumers and the MCP HTTP endpoint | "It's a network boundary now, not a local stdio pipe — shouldn't it be more secure?" | Requires a cert issuance/rotation/CA story the project has never needed and doesn't need yet; massively over-engineered relative to the "internal clients, API-key auth" trust model already used for the REST API and every other service boundary | Existing per-client API-key header check (reused, not duplicated) + `NetworkPolicy` restricting which pods can reach the Service |
| A separate `ADMIN_API_KEY` environment variable for operator-only presets actions | Feels like the fastest way to add "an admin path" without touching the `clients` schema | Out-of-band secret with no rotation mechanism, no per-action audit trail back to a `client_id`, a second parallel auth model living beside the existing one | `is_operator` boolean column on `clients`, checked through the existing auth middleware (see recommended default above) |
| Public `Ingress` + cert-manager/TLS for the new HTTP surfaces (MCP HTTP, presets REST) | "We're exposing new HTTP endpoints, shouldn't they be reachable and secured like a real service?" | Solves external exposure for a service whose constraints explicitly state internal-only clients and no public release; adds a TLS/cert lifecycle this milestone has no consumer for | `ClusterIP` Service + `kubectl port-forward` for local validation; defer ingress to when there's an actual external/cross-cluster consumer |
| `helm test` hooks as an additional CI-style smoke gate | Feels like standard Helm chart hygiene | Duplicates the in-cluster E2E adaptation (SEED-004 item 4) with a thinner, second test surface for zero additional coverage this milestone | Rely on the adapted E2E suite as the chart's correctness gate; revisit `helm test` only if a second chart consumer needs a faster, narrower smoke check |
| Per-environment `values-*.yaml` file sprawl from the first commit | "Best practice" cargo-culted from multi-environment production charts | Structure for environments that don't exist yet (only OrbStack-local is a target this milestone); premature abstraction that adds maintenance surface with zero current payoff | One `values.yaml` with local defaults; add environment-specific overlays only when a second real environment shows up |

## Feature Dependencies

```
Helm chart (base manifests + probes + Secrets/ConfigMaps)
    └──requires──> Migrate-as-hook Job (jobs table must exist before any worker/API pod starts)
    └──requires──> METRICS_ADDR = 0.0.0.0 port (SEED-004 landmine 1) — needed for both
                    worker liveness probes AND the KEDA Prometheus scaler's scrape target
                       └──enables──> KEDA ScaledObjects per engine class (need a scrapeable
                                     octoconv_queue_depth metric)

KEDA ScaledObjects (image/document/html workers)
    └──excludes──> webhook-worker (must stay a plain fixed-replica Deployment — see
                   the sweeper-singleton constraint; this is a hard exclusion, not
                   an oversight to fix later)

MCP streamable-HTTP endpoint
    └──requires──> internal/mcpserver.NewServer's existing transport-agnostic design
                    (already satisfied — no new dependency, just a new entrypoint)
    └──requires──> Shared auth-check logic factored so both internal/api and the new
                    MCP HTTP entrypoint can validate the same API-key header without
                    duplicating the hash-compare code

system-presets REST (PRAPIV2-01)
    └──requires──> clients.is_operator column (new migration) + manage-clients CLI flag
                    └──requires──> existing auth middleware extended to load/thread
                                   is_operator alongside client_id (small, additive change)

Load-proven 0→N→0 KEDA demonstration
    └──requires──> KEDA ScaledObjects already deployed and correctly scoped (excludes
                    webhook-worker, per above)
    └──requires──> A load-generation script/client bursting real jobs through the
                    already-authenticated API (no new feature — reuses existing
                    POST /v1/jobs)
```

### Dependency Notes

- **Helm chart requires migrate-as-hook:** the `jobs`/`clients`/`presets`/etc. tables must exist before the api Deployment's pods can serve traffic or before any worker can query `jobs` — a hook Job ordered before all Deployments is the correct place to enforce this, mirroring the existing compose `depends_on: condition: service_healthy` + embedded-migration-on-`cmd/api`-startup pattern but making the ordering explicit and singular instead of implicit-per-process.
- **KEDA requires `METRICS_ADDR=0.0.0.0`:** this is a hard prerequisite, not a nice-to-have — KEDA's Prometheus scaler and any worker liveness probe both need the metrics endpoint reachable from outside the pod's own network namespace (a Prometheus scrape and a kubelet probe both originate outside the container). This is exactly SEED-004's landmine 1, and it must be paired with a `NetworkPolicy` to avoid silently regressing the "operational data isn't public" decision.
- **KEDA excludes webhook-worker:** this is the single most load-bearing exclusion in this document. Every other dependency chain in this milestone assumes "per-engine-class worker gets a `ScaledObject`" — webhook-worker is the one deliberate, documented exception, and any roadmap phase that touches KEDA ScaledObjects must explicitly carve it out rather than apply a template uniformly across all worker Deployments.
- **MCP HTTP requires shared auth-check logic, not a second copy:** factoring out the API-key validation (hash lookup against `clients.api_key_hash`/`api_key_hash_secondary`, revocation check) into something both `internal/api` and the new MCP HTTP entrypoint call is a small refactor, but skipping it (i.e., copy-pasting the check) would silently diverge the two credential-validation paths over time — flag this dependency explicitly in phase planning so it isn't done as an afterthought inside the MCP phase alone.
- **system-presets REST requires the `is_operator` migration before the REST routes:** the REST handlers must be able to read `is_operator` from request context from day one — there is no safe intermediate state where the routes exist but the authorization check is a no-op, since that would ship a de-facto "any client can edit system presets" window.

## MVP Definition

### Launch With (v1 of this milestone)

- [ ] Helm chart deploying the full stack (Postgres, Redis, MinIO, api, image/document/chromium/webhook workers, asynqmon) to OrbStack k8s via `helm install`, with migrate/createbucket as `pre-install,pre-upgrade` hook Jobs — this is the literal milestone headline and everything else builds on it being solid
- [ ] `METRICS_ADDR` ported to `0.0.0.0` with a `NetworkPolicy` restricting `:9090` ingress — prerequisite for both worker liveness probes and KEDA's Prometheus scaler; blocks two other MVP items if skipped
- [ ] KEDA `ScaledObject`s for image/document/html workers only (explicitly excluding webhook-worker), with a documented, timestamped 0→N→0 load-proof run — the other stated headline goal
- [ ] webhook-worker ported as a plain fixed `replicas: 2` Deployment (no `ScaledObject`) — must ship alongside the KEDA work, not as an afterthought, since it's the one component that must NOT get the default treatment
- [ ] MCP streamable-HTTP endpoint reusing `internal/mcpserver`'s existing transport-agnostic server, gated by the same API-key header check as the REST API
- [ ] system-presets REST routes gated by a new `clients.is_operator` column + `manage-clients` CLI flag, returning 403 (not 404) for non-operator callers

### Add After Validation (v1.x)

- [ ] Widen `pollingInterval`/`cooldownPeriod` from demo-tuned values to production-appropriate ones once real traffic patterns (not a synthetic burst) are observed — trigger: this chart gets used against something other than a one-off local proof
- [ ] Session-affinity or multi-replica tuning for the MCP HTTP endpoint if `Stateless: true` turns out to be wrong for some tool interaction not yet exercised live — trigger: the live smoke test flagged as LOW confidence above surfaces a real gap
- [ ] `helm test` smoke hook — trigger: a second consumer of the chart needs a faster gate than the full E2E suite

### Future Consideration (v2+)

- [ ] Ingress/TLS for the HTTP surfaces — defer until there's an actual external or cross-cluster consumer, not "because it's now HTTP instead of stdio"
- [ ] CD pipeline wiring CI's existing image bake into automatic `helm upgrade` — defer until there's a real remote cluster target
- [ ] Multi-environment `values-*.yaml` — defer until a second real environment exists
- [ ] KEDA on the API/MCP Deployments (CPU-based or request-rate-based) — defer until there's evidence the request/response tier, not just the queue-consumer tier, needs elastic scaling

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|----------------------|----------|
| Helm chart bringing up the full stack | HIGH | MEDIUM | P1 |
| Migrate/createbucket as hook Jobs | HIGH | LOW | P1 |
| `METRICS_ADDR=0.0.0.0` + NetworkPolicy | HIGH (blocks two other P1 items) | LOW | P1 |
| KEDA ScaledObjects (image/document/html) with 0→N→0 proof | HIGH | HIGH | P1 |
| webhook-worker opt-out of KEDA (fixed replicas: 2) | HIGH (correctness-critical, not optional) | LOW | P1 |
| MCP streamable-HTTP endpoint sharing tool code | HIGH | MEDIUM | P1 |
| system-presets REST with `is_operator` gate | HIGH | MEDIUM | P1 |
| `NOTES.txt` on the chart | MEDIUM | LOW | P2 |
| Liveness probe tuning (tolerant vs. tight thresholds) | MEDIUM | LOW | P2 |
| Demo-vs-production KEDA timing-window documentation | MEDIUM | LOW | P2 |
| `helm test` hooks | LOW | MEDIUM | P3 |
| Ingress/TLS | LOW (no current consumer) | HIGH | P3 |
| Multi-environment values | LOW (no current consumer) | MEDIUM | P3 |

**Priority key:**
- P1: Must have for this milestone to be considered shipped
- P2: Should have, meaningfully improves the milestone's quality/legibility but not launch-blocking
- P3: Explicitly deferred — see Anti-Features and Future Consideration above

## Competitor / Reference Pattern Analysis

Not a market-facing product, so this section maps to reference infrastructure patterns rather than competitors:

| Concern | Standard k8s/KEDA/Helm pattern | Our approach |
|---------|-------------------------------|--------------|
| Scale-to-zero queue consumers | KEDA `ScaledObject` with `minReplicaCount: 0`, a scaler (Prometheus/Redis/etc.) driving the HPA it manages | Prometheus scaler against the already-existing `octoconv_queue_depth` metric — no new scaler type, no redis-internals scraper, exactly matching SEED-004's own decision to avoid an asynq-internals redis scaler |
| Pre-deploy one-shot setup tasks (migrations, bucket creation) | Helm `pre-install,pre-upgrade` hook Jobs with `hook-delete-policy` and `backoffLimit: 0` | Direct port, same shape, reusing the existing idempotent `cmd/migrate` binary unchanged |
| MCP server transport flexibility (stdio for local tools, HTTP for networked consumers) | go-sdk's transport-agnostic `*mcp.Server` + `mcp.NewStreamableHTTPHandler` wrapping the same server instance | Already structurally in place from Phase 21 (`internal/mcpserver.NewServer`); this milestone adds the second transport's thin entrypoint only |
| Operator/admin authorization layered onto an existing single-tenant-style API-key model | Either a role/claim column on the existing principal table, or a fully separate RBAC/IdP integration | Minimal column addition (`is_operator`) on `clients`, consistent with the project's own established pattern of extending `clients` for new auth concerns (key rotation slots) rather than reaching for a new provider |

## Sources

- KEDA documentation: Prometheus scaler (`keda.sh/docs/2.19/scalers/prometheus/`), ScaledObject spec (`keda.sh/docs/2.20/reference/scaledobject-spec/`) — MEDIUM confidence (WebSearch-aggregated against official KEDA docs, not independently verified against a running cluster this session)
- Helm hooks / migration-Job vs. initContainer best-practice discussion (multiple 2026 blog sources aggregated via WebSearch, consistent with each other and with general Helm documentation on hook annotations) — MEDIUM confidence
- `github.com/modelcontextprotocol/go-sdk` `mcp` package (`pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp`), `StreamableHTTPHandler`/`StreamableHTTPOptions`/session semantics — MEDIUM confidence for the API shape (fetched from pkg.go.dev), **LOW confidence** specifically for the `Stateless: true` recommendation's interaction with `convert_file`'s progress-notification streaming — flagged for live validation during phase execution
- go-sdk DNS-rebinding-protection commit (`github.com/modelcontextprotocol/go-sdk@67bd3f2`) — confirms automatic localhost-origin protection exists and is bypassable via `DisableLocalhostProtection`/correct `Host` header handling behind a proxy; relevant if the in-cluster Service is ever fronted by something that rewrites `Host` — MEDIUM confidence
- Direct codebase inspection (HIGH confidence, primary source): `docker-compose.yml` (existing resource limits, healthchecks, per-worker env), `internal/reconciler/reconciler.go` (`RunWithLock`/`PGAdvisoryLock` — sweeper singleton mechanics), `internal/presets/repo.go` (scope/shadow model — no existing operator concept), `internal/db/migrations/0001_init.sql` + `0002_client_api_keys.sql` (`clients` schema — confirms no role/scope column exists today), `internal/metrics/queue_collector.go` (`octoconv_queue_depth{queue,state}` — confirms the exact metric KEDA would scrape already exists), `internal/mcpserver/mcpserver.go` + `cmd/mcp-server/main.go` (confirms `NewServer` is transport-agnostic today), `.planning/seeds/SEED-004.md` (the planted seed enumerating known porting landmines), `.planning/PROJECT.md` (milestone scope, constraints, Out of Scope, Key Decisions history)

---
*Feature research for: Kubernetes + KEDA milestone (v1.6), OctoConv*
*Researched: 2026-07-14*
