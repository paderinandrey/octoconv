# Pitfalls Research

**Domain:** Porting OctoConv (Go/asynq/Redis/Postgres/MinIO, 4 engine-class workers + webhook-worker) to Kubernetes + KEDA, on local OrbStack, for milestone v1.6.
**Researched:** 2026-07-14
**Confidence:** HIGH for code-grounded findings (queue-depth exposition, sweeper singleton, MCP key model, OrbStack history — all verified directly against this repo's source and `.planning` history); MEDIUM for general KEDA/Helm/OrbStack-k8s mechanics (verified against current docs/GitHub issues, not against this project's own cluster yet, since no chart exists today).

## Critical Pitfalls

### Pitfall 1: KEDA Prometheus scaler can't see queue depth once workers are at zero replicas (verified in code)

**What goes wrong:**
`octoconv_queue_depth{queue=...}` — the exact metric SEED-004 names as the intended KEDA trigger source — is registered by `prometheus.MustRegister(metrics.NewQueueDepthCollector(...))` **inside each engine-worker's own `main()`**, once per binary, scoped to only that worker's own queue:

- `cmd/worker/main.go:89` → registers `queue.QueueImage` only
- `cmd/document-worker/main.go:94` → registers `queue.QueueDocument` only
- `cmd/chromium-worker/main.go:86` → registers `queue.QueueHTML` only
- `cmd/webhook-worker/main.go:114` → registers `queue.QueueWebhook` only

`cmd/api/main.go` registers **no** queue-depth collector — it only serves the globally-registered promauto counters/histograms (job outcomes, webhook deliveries, reconciler actions), none of which is queue depth.

So the *exposition endpoint* for a queue's depth lives exclusively inside the pod that consumes that queue. If KEDA scales the image-worker Deployment to 0 replicas, there is no pod left serving `/metrics` for `octoconv_queue_depth{queue="image"}` — the Prometheus scrape target disappears (Service has zero endpoints), the series goes stale/absent, and KEDA's `prometheus` trigger has nothing to query. **A worker at zero can never see that new work has arrived for it to scale back up on** — this is the literal chicken-and-egg problem, confirmed directly from the collector registration sites, not assumed.

Underlying data note: `queueDepthCollector.Collect()` (`internal/metrics/queue_collector.go:32`) calls `asynq.Inspector.GetQueueInfo(q)`, which reads **Redis** directly — the *data* is not worker-local, only the *exposition* is. This is the fix surface: the metric's source of truth (Redis via asynq's Inspector) is completely decoupled from where it happens to be exposed today.

**Why it happens:**
The collector was added per-worker (OBS-01, v1.0 Phase 4) when scale-to-zero wasn't a concern — every worker was always running, so "expose my own queue's depth from my own process" was the simplest correct design. Porting to KEDA silently invalidates that assumption with no code change flagging it.

**How to avoid:**
Do **not** point KEDA's `prometheus` scaler at the worker Deployments' own `/metrics`. Choose one:
1. **Move (or duplicate) `NewQueueDepthCollector` registration into the API process** (`cmd/api/main.go`), which never scales to zero (it's the ingress path) and already has `REDIS_ADDR`/`queue.RedisOpt()` available. Register collectors for image/document/html/webhook queues there; scrape the API's `/metrics` for the KEDA trigger. Smallest-diff fix; the collector code is reusable as-is.
2. **OR use KEDA's native `redis`/`redis-lists` scaler directly against asynq's Redis keys** (bypassing Prometheus entirely) — works regardless of worker replica count. However, SEED-004 explicitly says "не redis-scaler по внутренностям asynq" (don't couple to asynq's Redis internals), so option 1 is the one consistent with the stated design intent; keep option 2 as documented fallback if the Prometheus round-trip proves unreliable at zero.
3. **Do not use asynqmon as the metric source** — it is `127.0.0.1`-bound with no auth by design (`docker-compose.yml:305-321`) and is a UI, not a scrape target; making it scrape-safe is a separate porting concern (see Pitfall 4) and doesn't remove the need to decide who owns the metric.

**Warning signs:**
- ScaledObject sits at `minReplicaCount` even after jobs are enqueued; `kubectl describe hpa` shows `unable to get metric` or a stale `0`.
- Prometheus shows the worker scrape target `DOWN` while the queue clearly has pending items (visible via asynq Inspector or Redis directly).
- The "0→N→0" proof only ever demonstrates N→0 (scale down works — the still-running pod exposes its own draining metric) but the 0→N leg never fires from a cold queue.

**Phase to address:**
KEDA ScaledObjects phase — resolve **before** writing any ScaledObject manifest, as an early gating sub-task ("expose queue depth from an always-on component"). Do not let this surface only in the load-proof phase; by then the wrong architecture is baked into the chart.

---

### Pitfall 2: Scaling webhook-worker to zero silently kills the singleton reconciler-sweeper (verified in code)

**What goes wrong:**
`internal/reconciler/reconciler.go` gates the sweeper behind a Postgres session-level advisory lock (`PGAdvisoryLock`, `RunWithLock`) so exactly one replica across the webhook-worker fleet sweeps at any time (v1.3 Phase 16). `cmd/webhook-worker` is the **only** binary that constructs and runs the sweeper, and the only consumer of the webhook queue. If KEDA scales `webhook-worker` to 0 (its own queue being momentarily empty makes it look like a good candidate), two duties die at once:
1. No pod holds the advisory lock → the fleet-wide stale-job sweep stops **for every engine class** (image/document/html), not just webhooks — stranded `queued`/`active` jobs from any engine accumulate unrecovered until a webhook-worker pod exists again. The webhook-gap sweep (RECON-04) stops too.
2. No pod consumes the webhook queue → deliveries enqueued while at 0 sit pending until scale-up (delayed, not lost — unlike #1, which needs a live sweeper to notice anything at all).

Because the sweeper fixes *other* engines' stuck jobs, scaling this class to zero has fleet-wide blast radius disproportionate to its own queue traffic.

Audit result (the "verify nothing else assumes always-on" question): grep confirms the sweeper is the **only** always-on duty riding in a scalable worker today — single `RunWithLock` call site, only in `cmd/webhook-worker`; `cmd/worker/main.go:74-76` explicitly documents "no sweeper of any kind is constructed or run here" (same for document/chromium workers). So image/document/html classes are genuinely safe to scale to zero from an always-on-duty standpoint.

**Why it happens:**
The engine-class-per-queue KEDA design treats every queue as an independent, symmetric autoscaling unit. webhook-worker is architecturally *not* symmetric — it carries hidden always-on responsibility unrelated to its own queue depth.

**How to avoid:**
- Set `minReplicaCount: 1` (or 2, matching compose's deliberate ≥2-consumer redundancy, `docker-compose.yml:149-153`) — or, simpler and truer to SEED-004's intent (the architecture being validated is the *conversion* engine classes): **don't put webhook-worker under KEDA at all** this milestone; run it as a fixed 2-replica Deployment and reserve ScaledObjects for image/document/html.
- Re-verify the "sweeper is the only always-on duty" audit at execution time, in case new always-on logic lands first.
- If webhook-worker is KEDA-scaled anyway, treat `minReplicaCount >= 1` as a fail-closed hard gate, not a tunable.

**Warning signs:**
- Engine-class scale-to-zero load-proof passes, but a soak test shows a job stuck in `active` past `RECONCILER_ACTIVE_STALE_AFTER` (5m) with zero recovery attempts in `job_events`.
- `octoconv_reconciler_actions_total` flatlines across **all** actions during a window correlating with webhook-worker at 0 replicas.

**Phase to address:**
KEDA ScaledObjects phase, as an explicit exclusion/guard. Verification: ≥1 webhook-worker pod Running throughout a full image-worker 0→N→0 cycle, with continuous sweep activity.

---

### Pitfall 3: asynq graceful-shutdown window vs `terminationGracePeriodSeconds` mismatch — SIGKILL mid-conversion

**What goes wrong:**
Kubernetes sends SIGTERM, waits `terminationGracePeriodSeconds` (default **30s**), then SIGKILLs. Every worker's `main()` calls `srv.Shutdown()` on SIGTERM, and asynq waits for in-progress handlers — but per-attempt engine budgets are `ENGINE_TIMEOUT=120s` (image), `DOCUMENT_ENGINE_TIMEOUT=300s` (document), `HTML_ENGINE_TIMEOUT=60s` (html). A pod mid-way through a legitimate 300s LibreOffice conversion gets SIGKILLed at 30s under the default. Consequences:
- The Postgres row stays `active` with no clean `MarkFailed`/`MarkDone`; recovery falls to the reconciler via staleness — up to `RECONCILER_ACTIVE_STALE_AFTER` (5m) of extra latency plus a wasted full attempt, on every scale-down that catches a long job.
- The `asynq.Unique` TTL derivations in `internal/queue/queue.go` (`ImageUniqueTTL`/`DocumentUniqueTTL`/`HTMLUniqueTTL`) assumed asynq's own retry/archive semantics, not "pod force-killed mid-attempt, maybe rescheduled minutes later" — compose's `restart: always` (in-place restart) never exercised this mode. The math still holds (TTL is a worst-case ceiling), but re-check the interaction consciously rather than assume.
- KEDA's `cooldownPeriod` (default 300s) only delays the scale-down *decision*; once replicas are reduced, vanilla Deployment pod selection applies — **no** guarantee the terminated pod is an idle one rather than one mid-task.

**Why it happens:**
Kubernetes' 30s default is tuned for stateless HTTP services, not long-running task handlers; nothing forces anyone to notice the mismatch until a long job dies mid-flight.

**How to avoid:**
- Set `terminationGracePeriodSeconds` **per engine-class Deployment**, derived from the real worst case (mirroring the project's own UniqueTTL derivation discipline): document `>= 300s + download/upload overhead + margin`; image `>= 120s + margin`; html `>= 60s + margin`. Never leave the default.
- Verify asynq v0.26.0's `Server.Shutdown()` semantics specifically (does it block until handlers return, or does it have an internal deadline?) — if asynq has its own shutdown timeout, it must be `<=` the pod grace period or the mismatch reproduces inside asynq itself.
- A `preStop` hook is likely unnecessary (asynq workers are pull-based, not behind a Service receiving traffic) — but make that a deliberate yes/no in the chart, not a silent default.
- Load-test scale-DOWN with a long-running **document** job in flight (not just image, which is fast).

**Warning signs:**
- `job_events` shows jobs stuck in `active` time-correlated with KEDA scale-down events (`kubectl get events` termination timestamps vs `active`-since).
- `octoconv_reconciler_actions_total{action="recovered"}` spikes right after scale-down cycles during the load-proof.

**Phase to address:**
Helm chart phase sets per-class grace periods; KEDA load-proof phase verifies by injecting a slow document conversion during a scale-down window and confirming clean completion (not reconciler-recovered).

---

### Pitfall 4: `METRICS_ADDR=127.0.0.1` fix is a fail-silent trap if only half-done (SEED-004 landmine #1, sharpened)

**What goes wrong:**
`METRICS_ADDR: 127.0.0.1:9090` binds the metrics listener to pod loopback — unreachable by any in-cluster scraper. But the localhost-bind was itself the **security control** (D-19/T-04-13: operational data never exposed, internal-only trust model), not an accident. Flipping it to `0.0.0.0:9090` without a compensating NetworkPolicy silently breaks the trust-model constraint: anything in the cluster can then read queue depths and job outcomes — and for asynqmon (same `127.0.0.1` pattern), an entire **unauthenticated queue-inspection UI**, because auth was deliberately omitted on the grounds of loopback-only reachability.

**Why it happens:**
"Fix unreachable metrics" and "leak metrics" are the same one-line diff; the compensating control lives in a different file (NetworkPolicy) that nothing forces you to write.

**How to avoid:**
Ship the bind change and the NetworkPolicy (scoped to the Prometheus scrape identity) as one atomic unit. Apply identically to asynqmon's in-cluster exposure. Fail-closed: if the policy can't be verified, the port stays unexposed. Also keep `127.0.0.1:9090` as the **code default** and override to `0.0.0.0` only via chart env — so compose/CI behavior is untouched (see Pitfall 8's compose-compat rule).

**Warning signs:**
- Any manifest exposing 9090 (or asynqmon's 8080) without a NetworkPolicy in the same PR/plan.
- A successful `curl` of the metrics endpoint from `kubectl exec` in an unrelated pod — hard-gate failure, not a warning.

**Phase to address:**
Helm chart phase, bundled — NetworkPolicy is the direct replacement for the security property being removed, not "later hardening."

---

### Pitfall 5: MCP-HTTP breaks the stdio mode's per-client "zero-privilege" key model (verified in code)

**What goes wrong:**
`internal/mcpserver/config.go` reads exactly one `OCTOCONV_API_KEY` at startup; `internal/mcpserver/client.go` embeds that single key in every outbound request (`req.Header.Set("Authorization", "ApiKey "+c.apiKey)` at lines 176/203/228/259). Under **stdio** this is safe: process boundary == trust boundary (one process per user/session, spawned by the calling agent) — that's what makes the client "zero-privilege." An **in-cluster HTTP** endpoint (MCPV2-01) that keeps the load-once pattern serves **one shared key to every caller**: either a single high-privilege shared secret for the whole MCP-HTTP surface (worse blast radius than any one client's key, defeating the `clients`-table per-client rotation/revocation model), or silent wrongness the first time two internal callers need different permissions/rate limits.

**Why it happens:**
`Load()`'s "read once, fail fast" pattern is exactly right for process-per-session and exactly wrong for a shared server; the code has no per-request-identity notion because it never needed one.

**How to avoid:**
For the HTTP transport, pass the caller's own API key through per-request: forward the incoming MCP-HTTP request's `Authorization` header to the outbound OctoConv API client per call, so each request uses the calling internal service's own already-provisioned key — key-per-request keeps zero-privilege, zero new secrets, and full reuse of existing auth/rate-limit/rotation. Fallback (conscious tradeoff only): one dedicated `clients` row + Secret per consuming team, pushing "one key per trust boundary" to the deployment layer at the cost of endpoint-per-consumer. Also verify at execution time whether go-sdk v1.6.1 (current, in `go.mod`) ships a server-side Streamable HTTP transport or a bump is needed — check Context7/changelog; MCP transport APIs are moving fast (see Pitfall 6).

**Warning signs:**
- A diff that keeps `Config.APIKey` as a single startup-read field now serving a multi-caller `net/http.Server`.
- No way to answer "which `clients` row authorized this MCP tool call" for an arbitrary in-cluster request.

**Phase to address:**
MCP streamable HTTP phase (MCPV2-01) — an auth-model decision to resolve **before** wiring the transport, not to discover in a security pass afterward.

---

### Pitfall 6: MCP streamable HTTP session state across pod restarts / multi-replica (spec-timing dependent)

**What goes wrong:**
Under the pre-2026-07-28 Streamable HTTP spec (the era go-sdk v1.6.1 implements — the stateless spec RC found in research postdates it), the transport is session-based: the server issues `Mcp-Session-Id` on `initialize`, and every later request must present it. With >1 replica behind a plain round-robin Service, a session initialized on pod A 400s when a later request lands on pod B. Even at 1 replica, a rolling restart drops every live session with no failover. stdio never had this problem (one process = one implicit session), so this is a genuinely new state-management problem, not "same logic, different transport."

**Why it happens:**
SDK server implementations hold session state in-process by default; nothing in a naive port externalizes it.

**How to avoid:**
- Simplest for this milestone (internal-only, modest concurrency): pin the MCP-HTTP Deployment to **1 replica**, explicitly documented as a known limit — MCPV2-01 doesn't ask for MCP HA/autoscaling.
- If multi-replica is ever wanted: header-based sticky routing on `Mcp-Session-Id` at the ingress (ClientIP affinity is a weak proxy). Don't build a shared session store speculatively.

**Warning signs:**
- MCP-HTTP E2E passes reliably at 1 replica but flakes at >1 or during rolling updates — that "flakiness" is this bug, not test noise.

**Phase to address:**
MCP streamable HTTP phase — decide and document the replica-count constraint in that phase's plan, not during the (engine-queue-scoped) load-proof.

---

### Pitfall 7: OrbStack daemon has wedged 3× under heavy parallel build/compose load — this milestone is the highest-risk session yet (project history, verified)

**What goes wrong:**
`.planning/RETROSPECTIVE.md` documents this project's own incidents, not generic OrbStack rumor:
- Line 142: OrbStack wedged during the first 18-04 attempt — 10-minute executor stall, then a misdiagnosed restart (Docker Desktop isn't installed; OrbStack is).
- Line 183: "OrbStack wedged twice more during heavy builds; the restart runbook (quit → pkill helper → open -a OrbStack → poll docker version) is now proven but the root cause (daemon under parallel build+compose load) remains."

Three confirmed wedges, root cause unresolved, trigger = parallel build + compose load. This milestone stacks onto the same VM: a full k8s control plane; 7+ simultaneous pods on `helm install` (chromium-worker at 2GB, document-worker under amd64 emulation, LibreOffice image heavy); iterative image rebuilds during chart development; rapid pod churn during the 0→N→0 load-proof. The `platform: linux/amd64` pin on document-worker means OrbStack's Rosetta-class translation (confirmed Rosetta-class, not qemu, in `v1.5-phases/23-verapdf-validation/23-01-SUMMARY.md`) now runs inside a k8s pod with kubelet/scheduler overhead on top. Also documented: a `/tmp` build-context xattr failure (`23-03-SUMMARY.md`: `failed to xattr /private/tmp/devio_semaphore_...`) — stage build contexts inside the worktree, never `/tmp`.

**Why it happens:**
Root cause explicitly unresolved — a standing operational risk to route around, not a solved problem.

**How to avoid:**
- **Pre-build all 5 images once, sequentially, with fixed non-`latest` tags, before iterating on the chart.** OrbStack k8s shares the Docker engine — built images are immediately pod-usable without a registry (official OrbStack docs) — use `imagePullPolicy: IfNotPresent`/`Never` or non-`latest` tags so Kubernetes never attempts a registry pull.
- Never run the compose stack and the k8s stack hot simultaneously; serialize compose-based and k8s-based validation. The VM memory ceiling matters: LibreOffice + Chromium + emulated JVM + Postgres/Redis/MinIO ×2 stacks would be the heaviest resident set this project has ever asked of the VM.
- Raise OrbStack's VM memory/CPU allocation before starting, if not already generous.
- Apply the existing kill-after-120s rule for hanging docker/kubectl commands; run the proven restart runbook at the first stall rather than waiting it out.
- Schedule the load-proof phase (highest pod churn) with headroom, not back-to-back with a chart-iteration cycle.
- PVC note (LOW confidence — verify live, not assume): OrbStack k8s PVCs use a local provisioner inside the VM; fine for dev Postgres/MinIO data, but confirm data survives `helm uninstall`/reinstall the way compose named volumes do before any test flow relies on it.

**Warning signs:**
- Any docker/kubectl command silent >30-60s during a build or apply; past 120s, treat as a wedge (existing rule), not a slow command.
- `docker version`/`kubectl get nodes` unresponsive after a normal-looking `helm upgrade` — same signature as prior incidents.

**Phase to address:**
Cross-cutting — flag at roadmap risk level; apply concretely in the first full chart bring-up and the load-proof phases (the two highest-load moments). Add an explicit pre-flight task: build+cache all images, confirm `docker images`, confirm node Ready, then begin.

---

### Pitfall 8: host-gateway E2E trick and compose-DNS presigned URLs need re-derived (not renamed) solutions in k8s; nothing may break compose/CI (SEED-004 landmines #2/#4)

**What goes wrong:**
- The E2E webhook receiver reaches a host process via `host.docker.internal:host-gateway` in compose; k8s has no equivalent. The receiver must become an in-cluster pod/Service (SEED-004 already says so), which changes the *harness*, not just a hostname: receiver lifecycle, how the (possibly host-run) test driver asserts against its received payloads, and the SSRF-guard env (`WEBHOOK_ALLOW_PRIVATE_IPS` relaxation lives only in the e2e overlay today) all need re-solving.
- MinIO presigned URLs embed compose-DNS (`minio:9000`); the dial-redirect workarounds built for e2e and the MCP client (`OCTOCONV_S3_DIAL_ADDR`, preserving the Host header the V4 signature covers) are compose-topology-specific. In k8s the hostname regime changes (Service DNS), and a host-run test client typically can't resolve/reach cluster DNS at all without port-forward/Ingress — pushing toward running E2E as in-cluster Jobs, matching PROJECT.md's own goal ("проходит E2E внутри кластера"). Decide the driver topology (in-cluster Job vs host-run + port-forward) explicitly and early; it determines which existing dial-redirect implementation is the closer analog, and whether one is needed at all.
- **Test-image drift / CI protection:** k8s validation is explicitly NOT in CI this milestone (local-only). The standing risk is chart work leaking into shared code and breaking the compose path CI *does* protect — Dockerfiles, `.env` contracts, e2e code, env-default changes (like the METRICS_ADDR bind or collector relocation). Every k8s-motivated code change must keep compose defaults intact (override via chart env, not by changing code defaults) and be re-validated against the local compose E2E, since that's the only regression gate for it.

**Why it happens:**
Both compose workarounds patch the same category of problem ("embedded hostname vs consumer's network position") in ways that were only simple under compose's flat bridge network; and a not-in-CI target invites silent drift into the target that *is* in CI.

**How to avoid:**
- Treat the in-cluster webhook receiver as a design task with its own plan item (reuse the receiver *code*, expect the *harness* to change).
- Re-derive the presigned-URL handling against the actual chosen topology, as one unit of work spanning API/workers/e2e/MCP — not four independent fixes.
- Compose-compat rule for every code-touching phase: code defaults unchanged, k8s differences expressed only in chart values/env; run compose E2E locally after each shared-code change.

**Warning signs:**
- Copy-pasting the compose `extra_hosts` block into a manifest (meaningless in k8s) — a sign of renaming instead of re-deriving.
- E2E failing at the presigned-download step specifically (not the conversion step) — the classic URL/DNS mismatch signature.
- A red compose-E2E CI run on a "k8s-only" PR — the chart leaked into shared code.

**Phase to address:**
In-cluster E2E adaptation phase for the receiver/presigned work; the compose/CI-compatibility rule applies to every phase that touches Go code or env defaults.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|--------------------|-----------------|------------------|
| Dev creds (`minioadmin`/`octo-pass`/`dev-only-change-me-in-real-deploys`) copied from `docker-compose.yml` into committed `values.yaml` | Fast bring-up, parity with the already-accepted compose trust model | Same actual risk compose accepts, slightly higher exposure surface (Helm defaults are more likely to be reused as-is than `.env.example`, which forces a copy step) | Acceptable this milestone (same internal-only precedent) **only if** loudly flagged as dev-only in values comments/README, exactly like compose does today |
| Fixed 2-replica webhook-worker Deployment instead of KEDA (Pitfall 2) | Sidesteps the sweeper-singleton risk entirely | Slight inconsistency with the "every engine-class autoscales" narrative | Acceptable and arguably correct; document as an intentional exclusion |
| Single-replica MCP-HTTP Deployment (Pitfall 6) | Avoids sticky-routing/session-store work | No MCP HA; restart drops live sessions | Acceptable — MCPV2-01 doesn't ask for MCP HA; document as a known limit |
| Keeping the `linux/amd64` pin on document-worker in k8s | Zero new investigation; veraPDF keeps working as measured | Emulation cost persists inside a pod; irrelevant on OrbStack's single node, matters only on future heterogeneous clusters | Acceptable — pre-existing accepted risk (v1.5 PDFA decision); note that multi-node clusters need nodeSelector/affinity where compose's `platform:` sufficed |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|------------------|-------------------|
| KEDA `prometheus` scaler | Pointing the trigger at the worker's own metrics endpoint (the natural default) | Point at the always-on component hosting the collector post-fix (Pitfall 1); verify via `kubectl get --raw` on the external metrics API that the value resolves correctly at **0** replicas, not just at N |
| KEDA `pollingInterval`/`cooldownPeriod` | Leaving defaults (30s poll / 300s cooldown) without considering per-class task durations — one long document task, or a queue that empties/refills within the cooldown window, causes flapping | Tune per engine class: image (fast, bursty) shorter cooldown; document/html (slow tasks) longer, so one 300s job doesn't read as sustained load or premature idleness. Total metric lag = Prometheus scrape interval + KEDA poll + HPA stabilization — budget it explicitly in the load-proof's pass criteria |
| Helm hook Jobs (`migrate`, `createbucket`) | Relying on migrate's idempotence alone; without `helm.sh/hook-delete-policy` the previous release's completed Job collides by name and the **upgrade fails outright** | `helm.sh/hook: pre-install,pre-upgrade` + `hook-delete-policy: before-hook-creation,hook-succeeded` + `restartPolicy: Never` + `activeDeadlineSeconds` on both Jobs. Migrate IS idempotent (per PROJECT.md) and createbucket uses `mc mb --ignore-existing` — the wiring, not the logic, is the risk. Ordering: compose's `depends_on: service_healthy` disappears; hook weights + probes that genuinely gate on DB/bucket readiness replace it |
| Probes on LibreOffice/chromium workers | Copying compose healthchecks as aggressive liveness probes; heavy images + amd64 emulation make cold start slow, and tight `initialDelaySeconds`/`failureThreshold` produces CrashLoopBackOff on a healthy slow start | Use a `startupProbe` with generous failureThreshold × period covering worst-case cold start under emulation, gating liveness; keep liveness cheap (process-level). Workers are pull-based — readiness gates traffic they don't receive anyway, so probe design is about restart avoidance, not routing |
| MinIO/S3 in k8s | Assuming `S3_ENDPOINT=minio:9000` translates unchanged | Use the k8s Service DNS name consistently in env **and** accept it lands inside presigned URLs — treat the presigned-hostname problem as one unit across API/workers/e2e/MCP (SEED-004 #4, Pitfall 8) |
| OrbStack local images | `:latest` + default `imagePullPolicy: Always` → registry pull attempts for images that only exist locally → ImagePullBackOff | Fixed non-`latest` tags or explicit `imagePullPolicy: IfNotPresent`/`Never`; OrbStack shares the engine, so local images are pod-visible without a registry |
| chromium-worker `/dev/shm` | Forgetting compose's `shm_size: "256m"` has a k8s equivalent that must be recreated | Mount an `emptyDir` with `medium: Memory` and `sizeLimit: 256Mi` at `/dev/shm` (k8s default shm is 64Mi); the `--disable-dev-shm-usage` argv flag alone was deliberately judged insufficient (compose comment: "some Chromium internal code paths still touch /dev/shm even with that flag") |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|-----------------|
| Cold-start 0→1 stacking: heavy image + amd64-emulated JVM (document-worker) + metric lag + HPA cycle | Time-to-first-conversion after scale-from-zero far worse for document/html than image | Per-class pass/fail budgets in the load-proof, not one uniform threshold; pre-pulled images (already required by Pitfall 7) remove pull time from the equation | Only a production concern if `minReplicaCount: 0` survives into a real deployment for latency-sensitive classes — for this proof, measure honestly rather than hiding it with minReplicaCount=1 |
| Queue-depth metric lag → replica flapping | Replicas oscillate under steady load; HPA events alternate scale up/down | Scale-down stabilization/cooldown longer than the longest per-class task duration; see the KEDA tuning row above | Visible immediately in the load-proof if untuned — a tuning task, not an architecture flaw |
| VM memory ceiling with both stacks resident | OrbStack VM swapping; pods OOMKilled; daemon stall (Pitfall 7) | Never run compose + k8s stacks hot simultaneously; carry compose's per-service memory limits (worker 1g / document 1g / chromium 2g) into pod `resources` so the scheduler can actually protect the node | At full-chart bring-up, immediately, if the compose stack is still up |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| `values.yaml` Secret defaults that look production-usable | Dev creds copied into a real deployment (worse than `.env.example`, which forces an explicit copy step) | Loudly-fake defaults (the existing `dev-only-change-me...` strings qualify) + chart README stating they MUST be overridden; same trust-model precedent as compose, explicitly flagged |
| Metrics/asynqmon rebind without NetworkPolicy | In-cluster leak of operational data and an unauthenticated queue-inspection UI | Pitfall 4: bind change + NetworkPolicy as one atomic unit; negative-test the policy |
| MCP-HTTP shared static key | Single high-value credential reachable by anything that can route to the Service | Pitfall 5: per-request auth-header passthrough; plus a NetworkPolicy on who may reach the MCP Service at all |

## "Looks Done But Isn't" Checklist

- [ ] **KEDA 0→N→0 proof:** often only proves N→0 (a running pod can always report its own draining metric) — verify the 0→N leg separately with the worker genuinely at 0 replicas and a freshly-filled queue (Pitfall 1).
- [ ] **`helm install` "just works":** often missing the migrate/createbucket **ordering** guarantee compose's `depends_on: service_healthy` gave for free — and verify `helm upgrade` (not just install) re-runs hooks without Job-name collisions.
- [ ] **In-cluster E2E "passes":** often means happy-path conversion only — verify webhook delivery + signature, reconciler recovery, and MCP are all exercised in-cluster, not just convert-and-download.
- [ ] **NetworkPolicies "added":** verify with a negative test from an unrelated pod, not by the resource merely existing.
- [ ] **Compose/CI still green:** every k8s-motivated code change (bind addresses, collector relocation, env defaults) re-validated against the local compose E2E — k8s isn't in CI this milestone, and CI only protects the compose path.
- [ ] **Graceful shutdown "handled":** `srv.Shutdown()` exists in every worker, but the pod grace period is what makes it real in k8s — verify per-class `terminationGracePeriodSeconds` is set and a live long job survives a scale-down.
- [ ] **chromium `/dev/shm`:** compose's `shm_size` silently vanishes in a naive port — verify the memory-backed emptyDir mount exists and Chromium renders a heavy page without shm errors.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|----------------|-----------------|
| Scale-up-from-zero never fires (Pitfall 1 shipped) | LOW | Register `NewQueueDepthCollector` for all queues in the API process (it already has `queue.RedisOpt()`); repoint the ScaledObject query — the collector code is reusable as-is from any process |
| Sweeper stopped after webhook-worker hit 0 (Pitfall 2 shipped) | LOW | Chart-config-only: `minReplicaCount: 1` or remove the ScaledObject; sweeping resumes on the next tick once a pod holds the lock |
| SIGKILL mid-conversion (Pitfall 3) | MEDIUM | Raise per-class grace periods; the reconciler already self-heals stranded jobs within `RECONCILER_ACTIVE_STALE_AFTER` — unfixed instances are slow and noisy, not data-lossy |
| MCP-HTTP shared-key design shipped (Pitfall 5) | HIGH | Retrofit per-request identity into a client built once at startup (`internal/mcpserver/client.go` construction-lifecycle change) — much cheaper caught at design time |
| OrbStack wedge mid-milestone (Pitfall 7) | LOW per incident | Proven runbook: quit OrbStack → pkill helper → `open -a OrbStack` → poll `docker version`; expect recurrence (root cause unresolved) |
| Probe-induced CrashLoopBackOff on slow LibreOffice start | LOW | Add/loosen `startupProbe`; no code change |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|-------------------|---------------|
| 1. Queue-depth chicken-egg at 0 replicas | KEDA phase, gating early sub-task (metric relocation before ScaledObjects) | External-metrics API returns correct depth with the worker Deployment at 0 and a nonzero queue; 0→N scale-up observed live |
| 2. webhook-worker scale-to-zero kills sweeper | KEDA phase (explicit exclusion / minReplicaCount guard) | ≥1 webhook-worker pod Running throughout a full engine-class 0→N→0 cycle; continuous sweep activity in `job_events` |
| 3. SIGKILL mid-conversion | Helm chart phase (per-class `terminationGracePeriodSeconds`) | Long document job in flight during scale-down completes cleanly, not reconciler-recovered |
| 4. METRICS_ADDR without NetworkPolicy | Helm chart phase (atomic bind + policy change) | Negative curl from an unrelated pod fails; scraper succeeds |
| 5. MCP-HTTP shared key | MCP HTTP phase, design step before transport wiring | Two callers with two distinct API keys are separately attributed/rate-limited |
| 6. MCP session state across replicas | MCP HTTP phase | Single-replica constraint documented + enforced, or a session survives routing across replicas |
| 7. OrbStack wedging | Cross-cutting; pre-flight task in chart bring-up + load-proof phases | Sequential pre-build completes; no >120s stalls; runbook on hand |
| 8. E2E receiver + presigned DNS + compose/CI protection | In-cluster E2E phase; compose-compat rule in every code-touching phase | Full webhook round-trip against an in-cluster receiver; presigned download works; compose E2E stays green after each shared-code change |

## Sources

- This project's own source, read directly (HIGH confidence, primary evidence): `internal/queue/queue.go`, `cmd/worker/main.go:89`, `cmd/document-worker/main.go:94`, `cmd/chromium-worker/main.go:86`, `cmd/webhook-worker/main.go:114`, `cmd/api/main.go`, `internal/metrics/queue_collector.go`, `internal/metrics/metrics.go`, `internal/reconciler/reconciler.go`, `internal/mcpserver/config.go`, `internal/mcpserver/client.go`, `docker-compose.yml`, `go.mod`
- This project's planning history (HIGH confidence): `.planning/PROJECT.md`, `.planning/seeds/SEED-004.md`, `.planning/RETROSPECTIVE.md` (OrbStack wedges, lines 138/142/183), `.planning/milestones/v1.5-phases/23-verapdf-validation/23-01-SUMMARY.md` (Rosetta-class amd64 emulation), `23-03-SUMMARY.md` (/tmp build-context xattr failure)
- [Prometheus: Scaling to zero not working in KEDA 1.4.0 · Issue #770](https://github.com/kedacore/keda/issues/770) — MEDIUM, chicken-egg precedent
- [KEDA vs native HPA scale-to-zero](https://blog.devops.dev/keda-vs-kubernetes-1-36-native-hpa-the-ultimate-scale-to-zero-showdown-0a85f79cd7ed) — MEDIUM, external-metric-source framing of the zero-pods problem
- [Redis Lists | KEDA](https://keda.sh/docs/2.8/scalers/redis-lists/), [KEDA discussion #6443](https://github.com/kedacore/keda/discussions/6443) — MEDIUM, redis-scaler fallback mechanics
- [ScaledObject specification | KEDA](https://keda.sh/docs/2.20/reference/scaledobject-spec/), [Flapping when idleReplicaCount != 0 · Issue #2314](https://github.com/kedacore/keda/issues/2314) — MEDIUM/HIGH (official docs), defaults 30s poll / 300s cooldown, flapping precedent
- [Kubernetes best practices: terminating with grace (Google Cloud)](https://cloud.google.com/blog/products/containers-kubernetes/kubernetes-best-practices-terminating-with-grace), [CNCF: pod termination lifecycle](https://www.cncf.io/blog/2024/12/19/decoding-the-pod-termination-lifecycle-in-kubernetes-a-comprehensive-guide/) — MEDIUM, SIGTERM/grace/SIGKILL mechanics
- [Chart Hooks | Helm](https://helm.sh/docs/topics/charts_hooks/) — HIGH (official), hook-delete-policy and idempotency guidance
- [Kubernetes · OrbStack Docs](https://docs.orbstack.dev/kubernetes/) — HIGH (official), local images pod-usable without a registry; pull-policy guidance
- [Scaling HTTP Streamable MCP Servers on Kubernetes (sticky sessions)](https://zhimin-wen.medium.com/scaling-http-streamable-mcp-servers-on-kubernetes-handling-sticky-sessions-24212857c8ca), [MCP 2026-07-28 Release Candidate](https://blog.modelcontextprotocol.io/posts/2026-07-28-release-candidate/) — MEDIUM; confirms the stateful `Mcp-Session-Id` era applies to go-sdk v1.6.1's timeframe. LOW-MEDIUM on whether v1.6.1 ships server-side Streamable HTTP at all — verify via Context7/changelog at execution time

---
*Pitfalls research for: OctoConv v1.6 (Kubernetes & KEDA milestone)*
*Researched: 2026-07-14*
