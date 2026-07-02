# Feature Research

**Domain:** Internal async job-processing API (file/image conversion) — "submit → poll or webhook → download" pattern
**Researched:** 2026-07-02
**Confidence:** MEDIUM-HIGH (webhook/rate-limit/outbox patterns are well-documented industry practice; asynq-specific reconciliation is synthesized from asynq's public API docs + general dual-write literature, not a single canonical "asynq reconciler" reference)

## Feature Landscape

### Table Stakes (Users Expect These)

These are the items already listed in `PROJECT.md` Active scope. Research confirms all six are genuinely "table stakes" for a production async job API, even for internal-only clients — none of them are optional once real traffic and real client teams depend on the service. "Users" here = the internal engineering teams integrating with OctoConv.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| API-key auth scoped to `client_id` | Any service reachable on the network can currently create jobs and read other clients' presigned download URLs (CONCERNS.md: "No authentication ... fully public"). Internal-only does not mean trust-everyone — a compromised or buggy internal service is the realistic threat model. | LOW-MEDIUM | Static bearer/API-key header validated against `clients` table, hashed at rest (never store plaintext keys). Must enforce ownership on `GET /v1/jobs/{id}` (403/404 for jobs not owned by caller), not just on creation. |
| Per-client rate limiting | Without limits, one misbehaving/looping internal caller can starve the shared queue and worker pool for everyone else (see "Scaling Limits" in CONCERNS.md — API always accepts regardless of queue depth). This is the standard failure mode for shared internal infra. | LOW-MEDIUM | Token bucket per `client_id` is the industry-default algorithm for this shape of API — allows legitimate bursts (batch submission) while bounding sustained rate. Return `429` + `Retry-After`. |
| Webhook delivery (at-least-once, signed, retried) | `callback_url` and `webhook_deliveries` already exist in the schema but are unused (CONCERNS.md: "No webhook delivery despite schema support"). Every mature job-processing API (Stripe, GitHub, S3 event notifications, video/image conversion SaaS like Cloudinary/Coconut) ships push notifications with signature verification and automatic retry — polling-only is the "incomplete" version of this pattern. | MEDIUM-HIGH | See dedicated breakdown below — this is the single largest feature in scope. |
| Reconciler/sweeper for stranded jobs | Explicitly a known gap with a code comment admitting it ("a reconciler (next steps) will recover it") — jobs can get stuck in `queued` forever if enqueue fails after the DB write commits (CONCERNS.md). This is a correctness bug, not a nice-to-have. | MEDIUM-HIGH | See dedicated breakdown below. Tightly coupled to fixing the existing single-attempt-processing bug. |
| Magic-bytes content validation | Trusting `path.Ext` + client-supplied `Content-Type` (current behavior) means a client can upload arbitrary bytes with a `.png` extension and have it handed directly to `vips` (CONCERNS.md). Sniffing real file type before trusting client metadata is baseline hygiene for any file-ingestion API, internal or not. | LOW | Use content-sniffing (stdlib `http.DetectContentType` or `github.com/gabriel-vasile/mimetype` for broader/more accurate format coverage) against the first N bytes; reject on extension/content mismatch *before* upload to S3, not after. |
| S3/MinIO lifecycle TTL on `uploads/` and `results/` | `job_outputs.expires_at` exists in schema but nothing sets/enforces it — storage currently grows unbounded forever (CONCERNS.md: "Impact: Storage grows unbounded... no cost/capacity ceiling"). This is a standard, near-zero-maintenance feature once retention policy is decided. | LOW | Prefer a native S3/MinIO bucket lifecycle rule (prefix + age-based expiration) over an application-level sweeper — it's built into the object store, requires no extra process, and can't drift from what's actually stored. Reserve `expires_at` in Postgres for reporting/API-visible "this result disappears at X" rather than as the enforcement mechanism. |
| Baseline observability (metrics + real health checks) | `/healthz` currently returns `{"status":"ok"}` unconditionally without checking Postgres/Redis/S3 (CONCERNS.md) — an orchestrator restarting on this signal is worse than no health check. Prometheus metrics (queue depth, job latency, success/failure rate, webhook delivery success rate) are the minimum needed to detect the exact failure modes this milestone is designed to close (stuck jobs, webhook delivery failures, rate-limit thrash). | LOW-MEDIUM | asynqmon (or asynq's built-in web UI) for queue/task introspection; Prometheus client for `/metrics`; split `/healthz` (liveness, cheap) from `/readyz` (checks DB/Redis/S3 with short timeouts). |
| Transient-vs-terminal error distinction in the worker | Not separately listed in PROJECT.md but is a *hard dependency* of both the reconciler and of webhook delivery being meaningful: currently every job gets exactly one real attempt regardless of asynq's retry config, because `MarkFailed` (terminal) is called even for transient infra blips, which then poisons any retry (CONCERNS.md). A reconciler built on top of this bug will just keep re-stranding jobs. | MEDIUM | Classify errors (S3/Postgres transient network errors, context deadline exceeded → retry; malformed input, unsupported format, decompression-bomb rejection → terminal). Only call `MarkFailed` for terminal errors; let asynq's native retry mechanism handle transient ones. |
| Idempotent job creation (dedupe on retried submissions) | Internal callers doing HTTP retries (timeouts, load-balancer retries) will otherwise create duplicate conversion jobs and duplicate storage costs — a documented pitfall in every job-submission API. | LOW-MEDIUM | Accept an optional client-supplied idempotency key (or dedupe on `client_id` + content hash) at the API layer; asynq separately supports `TaskID`/`Unique` options for queue-level dedup, but that only protects against duplicate *enqueue*, not duplicate *job rows* — dedup needs to happen at the Postgres write. |

### Differentiators (Competitive Advantage)

Not required for this milestone's "production ready" bar, but worth flagging now because some interact with the reconciler/webhook/rate-limit work and are natural v1.x follow-ons.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Per-client webhook secrets + rotation | Stripe/GitHub-style HMAC secret rotation without downtime lets a client rotate compromised secrets without coordinating a deploy window. Meaningful once >1 external-facing team depends on webhooks. | LOW-MEDIUM | Straightforward extension of the `clients` table (store current + previous secret, accept either during a grace window). |
| Manual replay UI/CLI for failed webhook deliveries | `webhook_deliveries` already gives you the audit log; exposing "retry this delivery" as an ops action (rather than fully automatic forever-retry) turns an audit table into an operational tool. | LOW | Could be a CLI command against the DB initially rather than a UI — internal-only clients don't need self-service. |
| Per-client rate-limit tiers / burst allowances | Different internal teams have different legitimate load profiles (batch nightly job vs interactive request). A flat global-per-client limit is fine for v1; tiering is a natural evolution once real usage data exists. | LOW | Store limit config per row in `clients` rather than a global constant — cheap to add later if the column exists from day one. |
| Priority queues / per-client fairness | Prevents one client's large batch from starving another client's interactive jobs sharing the same asynq queue (flagged as a scaling limit in CONCERNS.md, but not urgent at current single-worker scale). | MEDIUM | asynq natively supports multiple named queues with weighted priority — defer until there's evidence of noisy-neighbor problems. |
| OpenTelemetry distributed tracing across API → queue → worker → S3 | Makes debugging a specific job's full lifecycle (submission → enqueue → pickup → engine → upload → webhook) far faster than log-correlation alone. | MEDIUM-HIGH | High value once there are multiple engine classes (doc/av/CAD) and cross-service calls; lower urgency for a single image/libvips slice. |
| Automatic dead-letter reprocessing with backoff ceiling | Beyond manual replay: auto-retry archived asynq tasks under specific safe conditions (e.g., known-transient error class) instead of requiring a human to trigger `RetryArchivedTask`. | MEDIUM | Risk of retry storms if the underlying cause wasn't transient — treat as a v2 refinement once error classification (see table stakes) has matured and proven reliable. |

### Anti-Features (Commonly Requested, Often Problematic)

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|------------------|-------------|
| Public developer portal / self-service API docs & key management UI | Feels like "what a real API product does" | Clients are internal company services only (PROJECT.md constraint) — building a portal is pure scope creep with zero users who need it; every hour spent here is an hour not spent on the reconciler/webhook correctness work that's actually blocking production readiness | Provision API keys via an internal runbook / ops script against the `clients` table; document the API in a README or internal wiki page |
| Usage-based billing / metering | Natural companion to rate limiting and "looks professional" | No monetization need for internal clients; adds an entire subsystem (usage aggregation, invoicing, dashboards) with no stakeholder asking for it | Prometheus metrics (already table-stakes) give enough visibility into per-client volume for capacity planning without building billing |
| Real-time job status via WebSockets/SSE | Feels more "modern" than poll-or-webhook | Webhooks already solve the push-notification need; adding a second real-time transport doubles the delivery-guarantee surface (now you need retry/reconnect logic for two channels) for no client-visible benefit over an at-least-once webhook + idempotent polling fallback | Webhook delivery (table stakes) + existing `GET /v1/jobs/{id}` polling covers 100% of the "how do I know when it's done" need |
| Fully automatic, unbounded dead-letter auto-retry | "Just keep retrying until it works" feels resilient | Silently masks systemic failures (e.g., a bad engine deploy) behind infinite retries, burning compute and delaying detection — this is exactly the inverse of the bug already in CONCERNS.md ("transient infra hiccups permanently fail jobs") just in the other direction | Bounded retries with asynq's native backoff + archive-on-exhaustion (table stakes), surfaced via metrics/alerts for a human to triage; manual replay (differentiator) once triaged |
| mTLS or OAuth2 client-credentials flow for auth | "More secure than API keys" | Significant added complexity (cert issuance/rotation or an OAuth token service) for a closed internal-network deployment where a hashed API key already raises the bar far above the current fully-public state; the risk being mitigated (external attacker) isn't the actual threat model (internal misbehaving service) | Hashed API key per client (table stakes) is proportionate to the actual risk; revisit mTLS only if the network perimeter assumption changes (e.g., multi-cluster, zero-trust mesh) |
| Building a custom webhook-management dashboard | Natural companion once `webhook_deliveries` exists | Duplicates what asynqmon + a SQL query against `webhook_deliveries` already gives an operator; a bespoke UI is a maintenance burden with no external customers to justify it | Use asynqmon for queue-level visibility, direct SQL/psql access or a simple internal CLI for `webhook_deliveries` inspection |

## Feature Dependencies

```
API-key auth (client_id resolution)
    ├──requires──> per-client rate limiting (needs client identity to key the bucket)
    └──requires──> webhook delivery attribution (delivery must know which client/callback_url)

Transient-vs-terminal error classification
    ├──requires──> reconciler/sweeper is meaningful (else sweeper just re-strands jobs the same way)
    └──requires──> asynq's native retry actually functions as configured

Reconciler/sweeper
    └──requires──> transient-vs-terminal error classification (see above)
    └──enhances──> webhook delivery reliability (fewer jobs silently stuck = fewer missed webhooks)

Webhook delivery
    ├──requires──> API-key auth / client_id (callback_url + secret are per-client concerns)
    ├──requires──> idempotent job creation (avoid double-delivery from duplicate job rows)
    └──enhances──> observability (delivery success/failure rate is a core metric)

Magic-bytes validation
    (no hard dependency — independent, should land before/alongside auth since it's pure request-validation)

S3 lifecycle TTL
    (no hard dependency — independent; only needs a decided retention policy, not other features)

Observability (metrics + readiness checks)
    ├──enhances──> reconciler (need metrics to know sweep is firing / jobs are stuck)
    ├──enhances──> webhook delivery (need delivery success-rate visibility)
    └──enhances──> rate limiting (need to see 429 rates per client to tune limits)

Per-client webhook secret rotation (differentiator)
    └──requires──> webhook delivery (table stakes) must exist first

Priority queues / per-client fairness (differentiator)
    └──requires──> per-client rate limiting (related but distinct concept — rate limiting bounds request rate; fairness bounds queue scheduling)
```

### Dependency Notes

- **Reconciler requires transient-vs-terminal error classification:** Building the sweeper before fixing the "every job gets exactly one real attempt" bug (CONCERNS.md) means the sweeper will re-enqueue jobs that then fail again on the very first `MarkActive` guard-clause conflict, or will indefinitely resurrect jobs that should have been terminal. These two items should land in the same phase, error classification first.
- **Webhook delivery requires client_id/auth:** `callback_url` is currently a bare column on `jobs` with no ownership concept; delivery attempts, signing secrets, and retry attribution all need to know *which client* to look up. Auth must land before or alongside webhook delivery, not after.
- **Rate limiting requires client_id/auth:** a per-client token bucket is meaningless without a stable client identity to key it on — this is a hard precondition, not just a nice ordering.
- **Magic-bytes validation and S3 lifecycle TTL are independent:** neither blocks nor is blocked by the auth/webhook/reconciler cluster. They can be built in parallel or slotted into any phase based on risk-ordering preference (both are called out as security/cost concerns in CONCERNS.md and are comparatively low effort).
- **Observability enhances everything else:** it's not a hard dependency for the other features to function, but building the reconciler or webhook delivery *without* metrics to observe them defeats the purpose — you'd have no way to tell if the sweeper is actually recovering stuck jobs or if webhook delivery success rate is acceptable. Strongly recommend landing basic metrics early, not last.

## Webhook Delivery — Deep Dive

Industry-standard webhook systems (Stripe, GitHub, and general engineering references) converge on the same guarantees. For OctoConv's `jobs.callback_url` + `webhook_deliveries` schema, table stakes are:

1. **At-least-once delivery, not exactly-once.** Never promise exactly-once — promise idempotent consumption instead (see #4). This is the universal pattern; exactly-once webhook delivery isn't achievable over HTTP.
2. **Retry with exponential backoff + jitter, bounded duration.** Stripe's live-mode policy is a useful reference point: retries for up to 3 days with exponential backoff, then the endpoint is marked failed/disabled. A smaller internal service can use a shorter total window (e.g., minutes-to-hours, not days) given internal callers are expected to have much higher uptime than arbitrary third-party endpoints — but the shape (exponential backoff + full jitter to avoid retry-storm thundering herd, capped max delay) is the right pattern regardless of window length.
3. **HMAC-SHA256 signature over the raw payload, in a header** (e.g., `X-OctoConv-Signature`), verified with constant-time comparison on the receiving end. Include a timestamp in the signed payload/header and have receivers reject deliveries outside a tolerance window (Stripe uses 5 minutes) to mitigate replay attacks.
4. **Idempotency via a stable delivery/event ID.** Every delivery attempt for the same underlying event should carry the same `event_id` (distinct from a fresh HTTP-level attempt) so receivers can dedupe — "persist processed event IDs, short-circuit on repeat" is the standard consumer-side pattern to document for internal client teams.
5. **Timeout on the delivery HTTP call itself**, short enough that a slow/hanging client endpoint doesn't block the delivery worker pool (mirrors the existing "no timeout on storage/DB calls" gap already flagged in CONCERNS.md — don't repeat that mistake for outbound HTTP).
6. **Dead-letter on exhaustion, not silent drop.** `webhook_deliveries` should retain the row with a terminal `failed`/`exhausted` status and full attempt history rather than deleting it — this is what makes manual replay (a documented differentiator) possible later.
7. **Fire delivery asynchronously from job completion**, not inline in the worker's completion transaction — use a separate asynq task (or a small poller reading `webhook_deliveries` rows in `pending` state) so a slow/down client endpoint can never block job processing throughput.

## Reconciler / Sweeper — Deep Dive

There is no single "canonical asynq reconciler" reference pattern in public docs — this is genuinely a build-it-yourself component, synthesized from asynq's public `Inspector` API plus general transactional-outbox / dual-write literature:

- **Two distinct failure modes to cover**, matching the two states currently unguarded:
  1. **Orphaned in Postgres, missing from Redis**: DB row is `queued` but the corresponding asynq task was never enqueued (enqueue call failed after the DB commit) — the exact bug already documented in CONCERNS.md.
  2. **Orphaned in Redis, missing/stale in Postgres** (less likely given current code order, but worth guarding): a task exists in the queue/active set but the DB row disagrees, e.g., after a worker crash mid-processing leaves a job `active` in Postgres with no live asynq task holding it.
- **Detection approach:** a periodic asynq task itself (asynq supports scheduled/periodic tasks natively) that:
  - Queries Postgres for jobs in `queued` older than a grace threshold (e.g., 30-60s — long enough to not race a normal enqueue-then-pickup, short enough to recover quickly) and cross-checks against asynq's `Inspector.ListPendingTasks`/`ListActiveTasks` for that job ID; if absent from Redis, re-enqueue.
  - Queries Postgres for jobs `active` past a "worker heartbeat" threshold (there is no such heartbeat in the current schema — this would be a new addition, e.g., `jobs.lease_expires_at` set/refreshed by the worker) and treats expired leases as candidates for either re-enqueue or terminal failure depending on attempt count.
- **Two architectural alternatives, pick one deliberately:**
  - **Reactive sweeper (recommended for this milestone):** keep the current write-then-enqueue order, add the periodic reconciler as a safety net for the (hopefully rare) failure window. Lower implementation cost, fits the existing code shape, and CONCERNS.md's own suggested fix approach already points this direction.
  - **Transactional outbox:** write job + an outbox row in the same Postgres transaction, have a separate relay process move outbox rows into asynq using `FOR UPDATE SKIP LOCKED` to allow multiple relay instances safely. Stronger correctness guarantee (eliminates the failure window entirely rather than detecting-and-repairing it) but is a more invasive rewrite of the job-creation path. Worth flagging as the "if reconciler false-negatives become a real problem" escalation path, not required for v1 of this milestone.
- **Bound re-enqueue attempts.** The sweeper must track an attempt/reconcile counter (or reuse asynq's own retry count once re-enqueued) and stop retrying after N sweeps, moving the job to a terminal `failed` state with a distinguishable reason (e.g., `stranded_max_attempts`) — otherwise a permanently-broken downstream (e.g., Redis genuinely down) turns the sweeper itself into an infinite-retry anti-pattern.
- **Emit metrics on every sweep action** (jobs found stuck, jobs recovered, jobs terminally failed) — this is the concrete tie-in to the observability table-stakes item; without it, nobody will know the reconciler is doing its job or is itself broken.

## Rate Limiting — Deep Dive

For an internal-only, API-key-authenticated service, the expected shape differs from public API rate limiting mainly in what's *not* needed:

- **Per-client quota is table stakes; a global-only limit is not sufficient**, because the failure mode being prevented is one noisy/buggy internal caller starving the shared queue for everyone else (already flagged as a scaling limit in CONCERNS.md) — a single global limit doesn't stop a single client from consuming all of it.
- **A coarse global ceiling is a reasonable secondary safety net** on top of per-client limits (protects total worker/storage capacity regardless of how well-behaved individual clients are), but should not be the primary mechanism.
- **Token bucket is the standard algorithm choice** here: it naturally accommodates legitimate bursty usage (e.g., an internal batch job submitting 200 conversions at once) while still bounding sustained throughput — better fit than a strict fixed-window counter for this workload shape.
- **What's genuinely NOT needed for internal-only clients** (differs from public-API expectations):
  - No need for tiered/paid rate-limit plans — a flat per-client default is sufficient for v1 (see differentiators table for future tiering).
  - No need for public-facing rate-limit documentation/dashboards — internal teams can be told their limit via the same runbook used to provision their API key.
  - No need for IP-based limiting layered on top of client-key-based limiting — the API key is already the trust boundary in a closed internal network.
- **Implementation note:** if the API is ever scaled to multiple replicas, an in-process (single-instance) token bucket is insufficient — the limiter state needs to live in Redis (already a dependency via asynq) for cross-instance consistency, using standard atomic Lua-script-based Redis token-bucket implementations to avoid the race conditions that plague naive multi-step Redis rate limiters.

## MVP Definition

### Launch With (v1) — this milestone

This *is* the MVP for this hardening milestone — all items are already scoped in PROJECT.md's Active section and confirmed as genuine table stakes by this research, not aspirational additions.

- [ ] API-key auth scoped to `client_id`, enforced on both job creation and job lookup — closes the current fully-public API gap
- [ ] Transient-vs-terminal error classification in the worker — prerequisite for the reconciler and for asynq's retry to function at all
- [ ] Reconciler/sweeper for jobs stranded in `queued` (and `active` past a lease threshold) — closes the documented "stuck forever" bug
- [ ] Webhook delivery: signed (HMAC-SHA256 + timestamp), retried with exponential backoff + jitter, delivery attempts logged in `webhook_deliveries`, dead-lettered (not silently dropped) on exhaustion
- [ ] Per-client rate limiting (token bucket), 429 + Retry-After on exceed
- [ ] Magic-bytes content validation before storage upload / engine invocation
- [ ] S3/MinIO bucket lifecycle TTL on `uploads/` and `results/` prefixes
- [ ] Prometheus metrics + real `/readyz` dependency checks + asynqmon for queue introspection

### Add After Validation (v1.x)

- [ ] Per-client webhook secret rotation — once webhook delivery has been running long enough that a rotation need has actually come up
- [ ] Manual replay tooling for failed webhook deliveries — once `webhook_deliveries` has accumulated real failure data worth acting on
- [ ] Per-client rate-limit tiers — once real usage patterns across internal clients are observed
- [ ] Idempotency key support on job creation, if duplicate-submission problems are observed in practice (flagged as table stakes above but can slip to v1.x if initial client integrations are well-behaved and this isn't yet causing pain)

### Future Consideration (v2+)

- [ ] Priority queues / per-client fairness — defer until there's evidence of noisy-neighbor contention (single worker replica today per CONCERNS.md)
- [ ] OpenTelemetry distributed tracing — higher value once multiple engine classes (doc/av/CAD, per PROJECT.md Out of Scope) exist and cross-service debugging gets harder
- [ ] Transactional outbox rewrite of job creation — only if the reactive-sweeper reconciler proves to have unacceptable false-negative/recovery-latency characteristics in practice
- [ ] Automatic (non-manual) dead-letter reprocessing — only after error classification has proven reliable enough to trust unattended retries

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|----------------------|----------|
| API-key auth + client scoping | HIGH | LOW-MEDIUM | P1 |
| Transient-vs-terminal error classification | HIGH | MEDIUM | P1 |
| Reconciler/sweeper | HIGH | MEDIUM-HIGH | P1 |
| Webhook delivery (signed, retried, logged) | HIGH | MEDIUM-HIGH | P1 |
| Per-client rate limiting | MEDIUM-HIGH | LOW-MEDIUM | P1 |
| Magic-bytes validation | MEDIUM | LOW | P1 |
| S3 lifecycle TTL | MEDIUM | LOW | P1 |
| Metrics + readiness checks | HIGH (enabling) | LOW-MEDIUM | P1 |
| Webhook secret rotation | LOW-MEDIUM | LOW-MEDIUM | P2 |
| Manual webhook replay tooling | LOW-MEDIUM | LOW | P2 |
| Idempotency key on job creation | MEDIUM | LOW-MEDIUM | P2 |
| Per-client rate-limit tiers | LOW | LOW | P3 |
| Priority queues / fairness | LOW (today) | MEDIUM | P3 |
| OpenTelemetry tracing | LOW (today) | MEDIUM-HIGH | P3 |
| Transactional outbox rewrite | LOW (unless sweeper fails in practice) | HIGH | P3 |

**Priority key:**
- P1: Must have for this milestone's "production ready" bar
- P2: Should have, natural v1.x follow-on once v1 is running
- P3: Nice to have, revisit only if a specific pain point emerges

## Competitor / Reference System Analysis

Not a market-competitor comparison (OctoConv is internal, non-commercial) — instead, reference implementations that define the domain's "table stakes" bar for job-processing + webhook APIs.

| Feature | Stripe (webhooks) | GitHub (webhooks) | AWS S3 (lifecycle) | Our Approach |
|---------|--------------------|--------------------|----------------------|--------------|
| Signature verification | HMAC-SHA256 over raw body, `Stripe-Signature` header with timestamp | HMAC-SHA256, `X-Hub-Signature-256` header | N/A | HMAC-SHA256 signed payload + timestamp header, constant-time verify |
| Retry policy | Exponential backoff, up to 3 days (live mode) | Exponential backoff, limited attempts, then endpoint flagged | N/A | Exponential backoff + full jitter, shorter total window appropriate for internal callers (minutes-hours, not days) |
| Delivery failure handling | Endpoint auto-disabled after prolonged failure + notification | Delivery visible/re-deliverable in UI | N/A | Dead-letter row in `webhook_deliveries`, manual replay (v1.x), alert via metrics |
| Object expiration | N/A | N/A | Native bucket lifecycle rules (prefix + age) | Native MinIO/S3 lifecycle rule on `uploads/`/`results/` prefixes, not app-level sweep |
| Rate limiting | Per-API-key limits, documented per endpoint | Per-token, primary + secondary (abuse) limits | N/A | Per-client token bucket, single-tier flat limit for v1, coarse global ceiling as secondary net |

## Sources

- [Webhook Retry Best Practices for Sending Webhooks (Hookdeck)](https://hookdeck.com/outpost/guides/outbound-webhook-retry-best-practices)
- [Webhook Retry Policy: Backoff, Idempotency & Dead Letter Code (HookRay)](https://hookray.com/blog/webhook-retry-strategies-2026)
- [Webhook Reliability: Idempotency & Retry Reference (Digital Applied)](https://www.digitalapplied.com/blog/webhook-reliability-idempotency-retries-engineering-reference-2026)
- [Building Reliable Webhook Delivery: Retries, Signatures, and Failure Handling (DEV Community)](https://dev.to/young_gao/building-reliable-webhook-delivery-retries-signatures-and-failure-handling-40ff)
- [Webhook Delivery Guarantees — At-Least-Once, Retries, HMAC & Dead Letters](https://codelit.io/blog/api-webhooks-delivery-guarantee)
- [Webhook Best Practices: Retry Logic, Idempotency, and Error Handling (DEV Community)](https://dev.to/henry_hang/webhook-best-practices-retry-logic-idempotency-and-error-handling-27i3)
- [Receive Stripe events in your webhook endpoint (Stripe official docs)](https://docs.stripe.com/webhooks)
- [Stripe Webhook Best Practices: Raw Body, Signatures & Retries (HookRay)](https://hookray.com/blog/stripe-webhook-best-practices-2026)
- [hibiken/asynq — GitHub](https://github.com/hibiken/asynq)
- [asynq package docs — pkg.go.dev](https://pkg.go.dev/github.com/hibiken/asynq)
- [Unique Tasks — hibiken/asynq Wiki](https://github.com/hibiken/asynq/wiki/Unique-Tasks)
- [Periodic Tasks — hibiken/asynq Wiki](https://github.com/hibiken/asynq/wiki/Periodic-Tasks)
- [Transactional Outbox Pattern (gmhafiz)](https://www.gmhafiz.com/blog/transactional-outbox-pattern/)
- [Transactional Outbox: a Postgres Ledger, Not a Queue](https://tiarebalbi.com/en/blog/the-transactional-outbox-is-not-a-queue)
- [Transactional Outbox Pattern: From Theory to Production (NP Blog)](https://www.npiontko.pro/2025/05/19/outbox-pattern)
- [Design A Rate Limiter (ByteByteGo)](https://bytebytego.com/courses/system-design-interview/design-a-rate-limiter)
- [API Rate Limiting Strategies: 2026 Engineering Reference (Digital Applied)](https://www.digitalapplied.com/blog/api-rate-limiting-strategies-2026-engineering-reference)
- [Rate Limiting Best Practices in REST API Design (Speakeasy)](https://www.speakeasy.com/api-design/rate-limiting)
- [gabriel-vasile/mimetype — GitHub](https://github.com/gabriel-vasile/mimetype/)
- [Detecting MIME Types in Go (GeekyRyan)](https://rnemeth90.github.io/posts/2024-03-27-golang-detect-file-type/)
- `.planning/PROJECT.md` (project scope and constraints)
- `.planning/codebase/CONCERNS.md` (existing implementation gaps, verified against research)

---
*Feature research for: internal async job-processing API (file conversion) production-hardening milestone*
*Researched: 2026-07-02*
