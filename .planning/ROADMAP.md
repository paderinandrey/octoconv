# Roadmap: OctoConv

## Milestones

- ✅ **v1.0 Hardening MVP** — Phases 1-4 (shipped 2026-07-08) — see `.planning/milestones/v1.0-ROADMAP.md`
- ✅ **v1.1 Tech Debt Cleanup** — Phases 5-7 (shipped 2026-07-08) — see `.planning/milestones/v1.1-ROADMAP.md`
- ✅ **v1.2 Document Engine Class** — Phases 8-11 (shipped 2026-07-10) — see `.planning/milestones/v1.2-ROADMAP.md`
- ✅ **v1.3 Document Class v2** — Phases 12-16 (shipped 2026-07-12) — see `.planning/milestones/v1.3-ROADMAP.md`
- ✅ **v1.4 CI, Presets & Debt Cleanup** — Phases 17-19 (shipped 2026-07-13) — see `.planning/milestones/v1.4-ROADMAP.md`
- ✅ **v1.5 MCP Access & Document Fidelity** — Phases 20-23 (shipped 2026-07-13) — see `.planning/milestones/v1.5-ROADMAP.md`
- 🚧 **v1.6 Kubernetes & KEDA** — Phases 24-28 (in progress) — details below

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

<details>
<summary>✅ v1.0 Hardening MVP (Phases 1-4) — SHIPPED 2026-07-08</summary>

- [x] Phase 1: Merge, Auth & Rate Limiting (4/4 plans) — completed 2026-07-04
- [x] Phase 2: Webhook Delivery (3/3 plans) — completed 2026-07-04
- [x] Phase 3: Retry-Safety & Reconciler (3/3 plans) — completed 2026-07-06
- [x] Phase 4: Content Validation, Storage Lifecycle & Observability (5/5 plans) — completed 2026-07-07

Full details: `.planning/milestones/v1.0-ROADMAP.md`

</details>

<details>
<summary>✅ v1.1 Tech Debt Cleanup (Phases 5-7) — SHIPPED 2026-07-08</summary>

- [x] Phase 5: Webhook SSRF Private-IP Opt-Out (1/1 plans) — completed 2026-07-08
- [x] Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test (4/4 plans) — completed 2026-07-08
- [x] Phase 7: Image Dimension Limit (Decompression-Bomb Protection) (2/2 plans) — completed 2026-07-08

Full details: `.planning/milestones/v1.1-ROADMAP.md`

</details>

<details>
<summary>✅ v1.2 Document Engine Class (Phases 8-11) — SHIPPED 2026-07-10</summary>

- [x] Phase 8: Document Content Safety & Format Detection (2/2 plans) — completed 2026-07-09
- [x] Phase 9: LibreOffice Converter Engine (2/2 plans) — completed 2026-07-09
- [x] Phase 10: Document Worker & Reconciler Integration (4/4 plans) — completed 2026-07-09
- [x] Phase 11: API Routing & End-to-End Document Conversion (4/4 plans, incl. gap closure 11-04) — completed 2026-07-10

Full details: `.planning/milestones/v1.2-ROADMAP.md`

</details>

<details>
<summary>✅ v1.3 Document Class v2 (Phases 12-16) — SHIPPED 2026-07-12</summary>

- [x] Phase 12: Tech Debt Cleanup (1/1 plans) — completed 2026-07-10
- [x] Phase 13: Cross-Format Conversion & Input Safety (3/3 plans) — completed 2026-07-10
- [x] Phase 14: Validated Conversion Options & PDF/A Export (3/3 plans) — completed 2026-07-10
- [x] Phase 15: HTML→PDF Chromium Engine (5/5 plans) — completed 2026-07-11
- [x] Phase 16: Webhook Delivery Decoupling (5/5 plans, incl. gap closure 16-05) — completed 2026-07-12

Full details: `.planning/milestones/v1.3-ROADMAP.md`

</details>

<details>
<summary>✅ v1.4 CI, Presets & Debt Cleanup (Phases 17-19) — SHIPPED 2026-07-13</summary>

- [x] Phase 17: Tech Debt Cleanup (2/2 plans) — completed 2026-07-12
- [x] Phase 18: Presets (4/4 plans) — completed 2026-07-12
- [x] Phase 19: CI Pipeline (2/2 plans) — completed 2026-07-13

Full details: `.planning/milestones/v1.4-ROADMAP.md`

</details>

<details>
<summary>✅ v1.5 MCP Access & Document Fidelity (Phases 20-23) — SHIPPED 2026-07-13</summary>

- [x] Phase 20: Presets REST CRUD & Format Discovery (2/2 plans) — completed 2026-07-13
- [x] Phase 21: MCP Server (3/3 plans) — completed 2026-07-13
- [x] Phase 22: CFB Encrypted-vs-Legacy Classification (2/2 plans) — completed 2026-07-13
- [x] Phase 23: veraPDF ISO 19005 Validation (3/3 plans) — completed 2026-07-13

Full details: `.planning/milestones/v1.5-ROADMAP.md`

</details>

### 🚧 v1.6 Kubernetes & KEDA (In Progress)

**Milestone Goal:** Весь стек OctoConv поднимается в Kubernetes из одного Helm-чарта на OrbStack k8s, воркеры автоскейлятся KEDA по глубине очередей (0→N→0 под нагрузкой — доказано с таймстампами), MCP получает in-cluster streamable-HTTP-эндпоинт, а system-пресеты управляются operator-scoped REST.

**Primary arc (hard-ordered):** 24 → 27 → 28. Phases 25 (MCP HTTP) and 26 (operator presets REST) are fully independent of the KEDA spine and of each other — freely reorderable/interleavable. The queue-depth exposition fix (KEDA-01) is folded into Phase 27 as its first plan (pure Go, hard prerequisite for the ScaledObjects that follow in the same phase).

- [x] **Phase 24: Helm Chart Core & Landmine Closure** - Full stack deploys via `helm install` on OrbStack k8s; four SEED-004 landmines closed; in-cluster E2E passes as a Job
- [x] **Phase 25: MCP Streamable HTTP** - `cmd/mcp-http` streamable-HTTP endpoint with per-request caller-key pass-through, in-cluster Service
- [x] **Phase 26: Operator System-Presets REST** - `/v1/system/presets` gated by `OPERATOR_CLIENT_IDS` env allowlist, 404-no-leak for non-operators (completed 2026-07-14)
- [x] **Phase 27: KEDA Autoscaling** - Queue-depth relocated to always-on api; per-engine-class ScaledObjects scale 0→N; webhook-worker fixed at 2 replicas (completed 2026-07-16)
- [ ] **Phase 28: Autoscale Load-Proof** - Timestamped 0→N→0 demonstration under burst load; long document job survives graceful scale-down

## Phase Details

### Phase 24: Helm Chart Core & Landmine Closure
**Goal**: The full OctoConv stack deploys to OrbStack Kubernetes from a single flat Helm chart and passes E2E inside the cluster.
**Depends on**: Nothing (first v1.6 phase; foundational — every other phase deploys through this chart or reuses its conventions)
**Requirements**: K8S-01, K8S-02, K8S-03
**Success Criteria** (what must be TRUE):
  1. `helm install octoconv ./deploy/chart` on OrbStack k8s reaches all-pods-Ready (api, worker, document-worker, chromium-worker, webhook-worker×2, mcp-http, plus Postgres/Redis/MinIO and the migrate/createbucket Jobs), and the in-cluster E2E Job completes with exit 0.
  2. migrate and createbucket run exactly once, before anything depends on them (Helm pre-install/pre-upgrade hook Jobs or init ordering) — verified on both a fresh install and a `helm upgrade`.
  3. A presigned result URL resolves both from inside a pod and from the OrbStack host, because `S3_ENDPOINT` uses the FQDN form `minio.<ns>.svc.cluster.local:9000`.
  4. `/metrics` binds `0.0.0.0` (values-only, zero Go-code change) yet is reachable only from the scoped Prometheus/kubelet source via NetworkPolicy — ingress from an unauthorized pod is denied (the in-cluster replacement for the old 127.0.0.1-bind security property, shipped atomically with the bind change).
  5. Each worker Deployment carries a class-appropriate `terminationGracePeriodSeconds` (document ≥ 300s, html ≥ 60s, image ≥ 120s) and dependency-aware liveness/readiness probes (api `/healthz`; workers on the metrics port after the 0.0.0.0 bind).
**Plans**: 3 plans
  - [x] 24-01-PLAN.md — Chart skeleton + full values contract + shared config/secret + stateful deps (Postgres/Redis/MinIO) + offline & server-dry-run gates
  - [x] 24-02-PLAN.md — 5 app Deployments (probes D-08, grace D-09, shm/limits D-10) + metrics NetworkPolicy (D-04) + migrate/createbucket hook Jobs (D-05) + Dockerfile.api migrate binary
  - [x] 24-03-PLAN.md — Dockerfile.e2e + in-cluster E2E Job (Downward-API podIP) + values-e2e overlay + THE LIVE HARD GATE (D-12): sequential build → helm install → E2E exit 0 → presigned-from-host → NetworkPolicy negative test → upgrade idempotence → teardown
**Notes**:
  - OrbStack operational discipline is non-negotiable: pre-build all 5 images once, sequentially, with fixed non-`latest` tags before iterating on the chart (OrbStack shares its image store with the Docker daemon, so locally-built images are pod-visible with no registry, but `:latest` still triggers re-pull attempts — repin/use `imagePullPolicy`). Never run the compose stack and the k8s stack hot simultaneously — three confirmed OrbStack daemon wedges under heavy parallel load are on record for this VM.
  - Research flags: OrbStack-specific k8s networking/FQDN host-resolution behavior and exact Helm hook re-run semantics on `helm upgrade` vs `install` are verified via docs only, not against a live chart — deeper research likely at phase planning. Verify OrbStack k8s version + PVC persistence across `helm uninstall`/reinstall live; do not assume compose-volume-like persistence.

### Phase 25: MCP Streamable HTTP
**Goal**: OctoConv's MCP tools are reachable over an in-cluster streamable-HTTP endpoint with per-caller identity preserved.
**Depends on**: Phase 24 (soft — deployment conventions only: Secret/ConfigMap shape, NetworkPolicy pattern, probe shape). No code/architecture dependency on the KEDA arc.
**Requirements**: MCPH-01, MCPH-02
**Success Criteria** (what must be TRUE):
  1. `cmd/mcp-http` serves the same 5 tools from the existing transport-agnostic `internal/mcpserver` via go-sdk `NewStreamableHTTPHandler` (v1.6.1, already pinned — zero version bump); a live MCP HTTP session against the in-cluster Service lists all 5 tools and runs a real conversion.
  2. Auth is per-request caller-key pass-through — two distinct client keys reaching the same Service each resolve to their own `clients` row (per-client rate limiting and preset scoping preserved; the pod stores no key — zero-privilege intact), reusing shared hash-compare auth logic, not a duplicated code path.
  3. The `convert_file` result is remote-usable in HTTP mode per the chosen `local_path` contract, decided and recorded at phase planning.
  4. `cmd/mcp-http` ships as a chart template gated by `mcpHttp.enabled`, with its Service and NetworkPolicy, `Stateless: true`, pinned to a single replica.
**Plans**: 3 plans
  - [x] 25-01-PLAN.md — internal/mcpserver refactor (per-request Client, ResultMode remote/local) + cmd/mcp-http binary + 401 middleware + D-02 stateless spike + unit tests
  - [x] 25-02-PLAN.md — Dockerfile.mcp-http + gated chart Deployment/Service + offline/dry-run gates + README HTTP section
  - [x] 25-03-PLAN.md — LIVE HARD GATE on OrbStack k8s (D-08): install, scripted MCP-over-HTTP session, presigned host-dial SC3 recheck, 401 case, teardown
**Notes**:
  - Key Decision to fix at phase planning — the `local_path` contract gap for remote callers (`local_path` refers to the pod's ephemeral filesystem, meaningless to a remote caller). Three options to weigh, no default recommended: (1) omit `local_path` entirely in HTTP mode (gate on a `SkipLocalDownload`-style flag; smallest diff, but response shape becomes transport-dependent); (2) return presigned-URL-only for the HTTP transport (specialization of #1 with clearer intent); (3) add a download-proxy tool that streams bytes back over the MCP transport (most capability, but binary streaming + size limits + a second code path).
  - Research flags: go-sdk v1.6.1's `Stateless: true` interaction with `convert_file`'s in-flight progress-notification streaming is flagged LOW confidence — live-verify before committing to the stateless design; also confirm server-side Streamable HTTP is actually present at the pinned version.

### Phase 26: Operator System-Presets REST
**Goal**: System-scope presets are manageable over REST by operator clients only, with no new auth model and no schema change.
**Depends on**: Nothing (fully independent of all Kubernetes work — a pure `internal/api` change; only eventual chart dependency is adding `OPERATOR_CLIENT_IDS` to the ConfigMap). Can run in parallel with or before/after Phase 25.
**Requirements**: OPER-01
**Success Criteria** (what must be TRUE):
  1. An operator client (ID present in `OPERATOR_CLIENT_IDS`) can create/list/show/update/deactivate system-scope presets via a `/v1/system/presets` route group, reusing the already scope-agnostic `PresetAdmin` interface unchanged.
  2. A non-operator client hitting the same routes receives the same uniform 404 the codebase already uses for cross-client access — never 403 — preserving the no-leak discipline held since Phase 1.
  3. The operator gate is env-only (`OPERATOR_CLIENT_IDS` allowlist parsed at API startup) with zero migrations and no second parallel auth system.
**Plans**: 2 plans
  - [x] 26-01-PLAN.md — Operator-only /v1/system/presets REST surface + OPERATOR_CLIENT_IDS gate
  - [x] 26-02-PLAN.md — Gap closure (CR-01): repo.Create version-bump + 23505→ErrAlreadyExists so deactivate→recreate works
**Notes**:
  - Standard pattern — skip research-phase. Additive REST handlers + one `RequireOperator` middleware, not a domain change.
  - Key Decision (already resolved by research, record at planning): `OPERATOR_CLIENT_IDS` env allowlist + 404-no-leak chosen over a new `is_operator` column + 403, matching the codebase's env-only-config constraint and its 404-never-403 convention. Trade-off accepted: changing the operator set requires an API restart, and there is no per-operator audit trail beyond added logging — the `is_operator` column is documented as a future option (K8SV2-03) to revisit if redeploy-free management or per-operator audit becomes a real requirement.

### Phase 27: KEDA Autoscaling
**Goal**: Each engine-class worker scales itself on its own queue depth via KEDA — including genuine scale-from-zero — while the sweeper-hosting webhook-worker stays fixed and never scales down.
**Depends on**: Phase 24 (deployable, NetworkPolicy-scoped `/metrics`)
**Requirements**: KEDA-01, KEDA-02
**Success Criteria** (what must be TRUE):
  1. `octoconv_queue_depth` is registered on (relocated/duplicated into) the always-on `api` process for all four queues (image/document/html/webhook), and resolves correctly via `kubectl get --raw` on the external metrics API even when a worker Deployment is at genuinely 0 replicas — not just at N.
  2. KEDA (v2.20.x, helm-installed) `ScaledObject`s for the image, document, and html workers scale each from `minReplicaCount 0` on its own queue-depth signal, driven by the Prometheus scaler (never asynq's internal Redis list keys).
  3. webhook-worker has no `ScaledObject` at all and runs a fixed `replicas: 2` Deployment — a fail-closed hard gate (never scaled to zero, since it is the sole host of the fleet-wide Postgres-advisory-lock sweeper), shipped in this same phase, not deferred.
  4. Autoscaling is gated behind a `keda.enabled` chart flag; `pollingInterval`/`cooldownPeriod` are tuned per engine class (image: fast/bursty, shorter cooldown; document/html: slow tasks, longer cooldown so one long task doesn't read as sustained load or premature idleness).
**Plans**: 3 plans
  - [x] 27-01-PLAN.md — KEDA-01 pure Go: relocate queue-depth collector to always-on api (all 4 queues) + per-class asynq ShutdownTimeout + compose-E2E validation (D-01/D-02/D-03)
  - [x] 27-02-PLAN.md — KEDA-02 chart: in-chart Prometheus + api :9090 Service port + NetworkPolicy Prometheus-admission fix + 3 gated ScaledObjects (image/document/html) + keda/prometheus values (D-04..D-10)
  - [x] 27-03-PLAN.md — KEDA-02 live gate on OrbStack: helm-install KEDA v2.20.1, scale-from-zero proof (SC1 metric at 0 replicas, SC2 all-three 0→1, webhook-worker fixed 2), teardown (D-11/D-12/D-13)
**Notes**:
  - KEDA-01 (queue-depth exposition relocation) is folded in as this phase's first plan: it is pure Go with no chart/manifest changes and can be validated against the existing compose E2E suite before any k8s work touches it. It is the single most load-bearing fix this milestone must not skip — it must be resolved before any `ScaledObject` manifest is written (otherwise a worker at 0 replicas has no pod exposing the metric KEDA needs to scale it back up).
  - Research flags: exact KEDA Prometheus-scaler tuning for this project's real task-duration distributions needs execution-time research; demo-tuned starting values (`pollingInterval` ~5-15s, `cooldownPeriod` ~60s) are a starting point, not production values. Confirm asynq v0.26.0's `Server.Shutdown()` deadline semantics so grace-period behavior holds through asynq's own shutdown path.

### Phase 28: Autoscale Load-Proof
**Goal**: The 0→N→0 autoscale behavior is proven under real load with timestamped evidence, including graceful scale-down while a long conversion is in flight. This is the milestone's flagship live acceptance.
**Depends on**: Phase 27
**Requirements**: KEDA-03
**Success Criteria** (what must be TRUE):
  1. With the image worker at genuinely 0 replicas and a freshly-filled queue, POSTing a burst of ~20 jobs scales it to ≥2 replicas within 60s — the 0→N leg verified separately (a running pod can always report its own draining metric, so this leg must be proven with the worker truly at 0).
  2. After the queue drains, the worker scales back to 0 within the cooldown window (the N→0 leg).
  3. A ~200s-long document conversion in flight survives a KEDA downscale event — the job completes with no SIGKILL mid-job and no spurious retry, validating Phase 24's `terminationGracePeriodSeconds` choice under real conditions (not just the fast image path).
  4. The full 0→N→0 timeline is captured as timestamped evidence — queue depth and pod count plotted on the same time axis (steady state → burst → time-to-first-replica → time-to-drain → time-to-scale-to-zero).
**Plans**: 3 plans
  - [x] 28-01-PLAN.md — Chart fixes: WR-02 field-level replicas omission on the 3 scaled Deployments (D-10) + document ScaledObject scaleDown-stabilization override + document-worker extraEnv + values-loadproof.yaml overlay
  - [ ] 28-02-PLAN.md — Tooling: gen_heavy_docx.py (D-07) + render_evidence.py (D-02) + keda-load-proof.sh gate (burst sampler D-01/D-04, N→0 D-06, SC3 downscale-soak D-08/D-09)
  - [ ] 28-03-PLAN.md — Live run: SC3 calibration (D-07) + full 0→N→0 gate run + committed timestamped evidence (D-03) + human-verify acceptance
**Notes**:
  - Timestamped-evidence requirement is a hard deliverable, not a nice-to-have: the acceptance is "доказано с таймстампами" — an explicit causal chain (queue-depth graph + pod count over time), not merely "it scaled." Both legs must be shown; the 0→N leg is the one that is easy to fake and must be proven with the worker at true 0 replicas.
  - Standard pattern — mostly execution/observation and load-generation scripting against already-decided infrastructure; low incremental research need beyond what Phases 24/27 established.

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|-----------------|--------|-----------|
| 1. Merge, Auth & Rate Limiting | v1.0 | 4/4 | Complete | 2026-07-04 |
| 2. Webhook Delivery | v1.0 | 3/3 | Complete | 2026-07-04 |
| 3. Retry-Safety & Reconciler | v1.0 | 3/3 | Complete | 2026-07-06 |
| 4. Content Validation, Storage Lifecycle & Observability | v1.0 | 5/5 | Complete | 2026-07-07 |
| 5. Webhook SSRF Private-IP Opt-Out | v1.1 | 1/1 | Complete | 2026-07-08 |
| 6. Reconciler Webhook-Gap Sweep & Staleness Soak Test | v1.1 | 4/4 | Complete | 2026-07-08 |
| 7. Image Dimension Limit (Decompression-Bomb Protection) | v1.1 | 2/2 | Complete | 2026-07-08 |
| 8. Document Content Safety & Format Detection | v1.2 | 2/2 | Complete | 2026-07-09 |
| 9. LibreOffice Converter Engine | v1.2 | 2/2 | Complete | 2026-07-09 |
| 10. Document Worker & Reconciler Integration | v1.2 | 4/4 | Complete | 2026-07-09 |
| 11. API Routing & End-to-End Document Conversion | v1.2 | 4/4 | Complete | 2026-07-10 |
| 12. Tech Debt Cleanup | v1.3 | 1/1 | Complete    | 2026-07-10 |
| 13. Cross-Format Conversion & Input Safety | v1.3 | 3/3 | Complete    | 2026-07-10 |
| 14. Validated Conversion Options & PDF/A Export | v1.3 | 3/3 | Complete    | 2026-07-10 |
| 15. HTML→PDF Chromium Engine | v1.3 | 5/5 | Complete    | 2026-07-11 |
| 16. Webhook Delivery Decoupling | v1.3 | 5/5 | Complete | 2026-07-12 |
| 17. Tech Debt Cleanup | v1.4 | 2/2 | Complete | 2026-07-12 |
| 18. Presets | v1.4 | 4/4 | Complete | 2026-07-12 |
| 19. CI Pipeline | v1.4 | 2/2 | Complete | 2026-07-13 |
| 20. Presets REST CRUD & Format Discovery | v1.5 | 2/2 | Complete | 2026-07-13 |
| 21. MCP Server | v1.5 | 3/3 | Complete | 2026-07-13 |
| 22. CFB Classification | v1.5 | 2/2 | Complete | 2026-07-13 |
| 23. veraPDF ISO 19005 Validation | v1.5 | 3/3 | Complete | 2026-07-13 |
| 24. Helm Chart Core & Landmine Closure | v1.6 | 0/? | Not started | - |
| 25. MCP Streamable HTTP | v1.6 | 0/? | Not started | - |
| 26. Operator System-Presets REST | v1.6 | 2/2 | Complete    | 2026-07-14 |
| 27. KEDA Autoscaling | v1.6 | 3/3 | Complete    | 2026-07-16 |
| 28. Autoscale Load-Proof | v1.6 | 1/3 | In Progress|  |

---

*Next: run `/gsd:plan-phase 24` to plan the first v1.6 phase.*
