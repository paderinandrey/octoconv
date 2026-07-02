# Project Research Summary

**Project:** OctoConv
**Domain:** Production-hardening an internal async file-conversion service (Go / chi / asynq / Postgres / MinIO) — adding auth, rate limiting, webhook delivery, a reconciler, content validation, storage lifecycle, and observability to an already-working vertical slice (image conversion via libvips)
**Researched:** 2026-07-02
**Confidence:** HIGH

## Executive Summary

OctoConv is an internal "submit → convert → download" async job API, architecturally similar to Stripe/GitHub-style webhook-driven job processors. The existing vertical slice (image conversion, `queued → active → done/failed` state machine, Postgres system-of-record, asynq/Redis queue, MinIO storage) is functionally complete but has seven well-documented production gaps: no auth, no rate limiting, no webhook delivery despite schema support, no reconciler for stranded jobs, trust-based content-type validation, unbounded storage growth, and no real health/observability. All four research streams converge on the same conclusion: none of these gaps require new infrastructure or a stack rewrite — every fix composes additively onto the existing Postgres-first-then-enqueue pattern, the existing chi middleware chain, and the existing single asynq worker process (new task types on new queues, not new binaries).

The recommended approach is incremental hardening in dependency order, not a redesign. Two findings are load-bearing for sequencing: (1) rate limiting and webhook delivery both hard-depend on `client_id` from auth, so auth must land first; (2) the reconciler and webhook retry-storm safety both depend on fixing the current bug where every job effectively gets one real attempt (transient errors are treated as terminal, poisoning asynq's own retry mechanism) — building the reconciler on top of the current state machine will actively make things worse by re-enqueuing jobs that are just slow, causing duplicate processing and racing writes to the same storage key. This retry-safety fix is not explicitly listed in PROJECT.md's Active scope but is a hard technical prerequisite discovered by research, not an optional nice-to-have — it needs to be a phase (or an early sub-phase) in its own right.

Key risks, in priority order: (1) auth that trusts network position instead of the key itself (the current de facto state — zero auth middleware exists) — mitigated by making auth mandatory-by-default on the router rather than opt-in per-route; (2) reconciler double-processing legitimately-slow jobs by using time-only heuristics instead of checking asynq's actual queue state — mitigated by querying asynq's Inspector API and using idempotent `TaskID`-based re-enqueue; (3) webhook retry storms and duplicate side-effects at receivers — mitigated by exponential backoff+jitter, a stable per-event delivery ID, and HMAC signing with a dedicated (non-API-key) secret. Confidence across all four research areas is HIGH-to-MEDIUM-HIGH: findings are grounded both in official library/API documentation and in the project's own `.planning/codebase/CONCERNS.md`, which independently documents most of these exact gaps as known bugs, not hypothetical risks.

## Key Findings

### Recommended Stack

No changes to the locked core stack (Go 1.26, chi v5, asynq v0.26, pgx/v5, minio-go/v7, Postgres 18). Hardening additions are narrowly scoped, actively-maintained libraries that compose with what's already vendored — several new capabilities (queue metrics, periodic tasks, unique-task idempotency) ship inside the already-vendored `asynq` module with no new dependency at all.

**Core technologies:**
- `crypto/sha256` + `crypto/subtle` (stdlib) — API-key hashing + constant-time comparison — API keys are high-entropy tokens, not passwords; bcrypt/argon2 would add unnecessary per-request CPU cost (industry-standard Stripe/GitHub PAT pattern)
- `github.com/go-chi/httprate` v0.15.0 — per-client in-process rate limiting — official go-chi org project, pluggable `KeyFunc` to key off `client_id` (not just IP), zero new infra for the current single-instance deployment
- `github.com/gabriel-vasile/mimetype` v1.4.13 — magic-bytes content-type detection — actively maintained, header-only reads (cheap), covers HEIC/HEIF which stdlib `http.DetectContentType` does not (a hard requirement given the current format set)
- `github.com/prometheus/client_golang` v1.23.2 — `/metrics` endpoint — canonical, officially-maintained Prometheus Go client
- `hibiken/asynq/x/metrics` + `hibiken/asynqmon` (Docker sidecar) — queue-depth/latency metrics and a web UI for asynq task inspection — no new Go dependency, reuses the already-pinned asynq module and existing Redis

Deferred by design: Redis-backed rate limiting (`go-redis/redis_rate`) and OpenTelemetry tracing — both are correct future upgrades once the API runs multiple replicas or spans multiple engine classes, but add complexity/infra cost the current single-instance deployment doesn't need yet.

### Expected Features

All items already in PROJECT.md's Active scope are confirmed by research as genuine table stakes (not aspirational) for a production async job API, even with internal-only clients. Research also surfaces one hard prerequisite not explicitly listed in PROJECT.md: transient-vs-terminal error classification in the worker, which the reconciler and webhook retry-safety both depend on.

**Must have (table stakes) — this milestone:**
- API-key auth scoped to `client_id`, enforced on both creation and lookup (403/404 semantics)
- Transient-vs-terminal error classification in the worker (prerequisite for retries and reconciler to function at all)
- Reconciler/sweeper for jobs stranded in `queued` (and `active` past a lease threshold)
- Webhook delivery: HMAC-signed, retried with exponential backoff+jitter, logged in `webhook_deliveries`, dead-lettered on exhaustion
- Per-client rate limiting (token bucket), 429 + Retry-After
- Magic-bytes content validation before storage upload
- S3/MinIO bucket lifecycle TTL on `uploads/` and `results/`
- Prometheus metrics + real `/readyz` dependency checks + asynqmon

**Should have (v1.x follow-on):**
- Per-client webhook secret rotation (once webhook delivery has run long enough to need it)
- Manual replay tooling for failed webhook deliveries
- Idempotency key on job creation (dedupe retried submissions)
- Per-client rate-limit tiers

**Defer (v2+):**
- Priority queues / per-client fairness (no evidence of noisy-neighbor contention yet, single worker replica)
- OpenTelemetry distributed tracing (higher value once multiple engine classes exist)
- Transactional outbox rewrite (only if the reactive-sweeper reconciler proves insufficient in practice)
- Public developer portal, usage-based billing, WebSocket/SSE status, mTLS/OAuth2 — explicit anti-features for an internal-only, closed-network service; scope creep relative to actual threat model and stakeholders

### Architecture Approach

Every new component attaches additively to the existing API/worker/Postgres/Redis/MinIO topology: no new processes, no new message bus, no rewrite of the core job-creation write path. Auth, rate limiting, and magic-byte sniffing are new chi middleware/handler-layer additions; the reconciler and webhook delivery are new asynq task types consumed by the same worker process on new named queues (`system`, `webhooks`), following the exact same "Postgres-first, then idempotent enqueue" convention already established for job creation.

**Major components:**
1. `internal/auth` — resolves API key → `clients` row (hashed lookup), injects `client.ID` into request context; must be mandatory-by-default middleware, not opt-in per route
2. `internal/ratelimit` — per-client (post-auth) + coarse per-IP (pre-auth) token-bucket limiting via `go-chi/httprate`
3. `internal/contenttype` — magic-byte sniff-then-reject (422) before any S3/Postgres write, using a small peekable reader (no full buffering)
4. `internal/reconcile` — periodic asynq task (`reconcile:sweep`) that cross-checks Postgres against asynq's own Inspector state before re-enqueuing (never time-only heuristics)
5. `internal/webhook` — dedicated `webhook:deliver` asynq task on its own queue; HMAC-SHA256 signed, backoff+jitter retry, full attempt history in `webhook_deliveries`
6. Storage lifecycle TTL — MinIO/S3 bucket lifecycle rule (infrastructure config, zero application code)
7. Observability — `prometheus/client_golang` `/metrics` + `asynqmon` sidecar, wrapping existing components without owning new state

Layered chi middleware ordering is a specific, load-bearing pattern from research: coarse pre-auth IP rate limit → auth (resolve client, 401) → per-client rate limit → magic-byte sniff → handler. This prevents unauthenticated floods from paying a DB-lookup cost, and ensures rate limiting is keyed on verified client identity, not spoofable IP/headers.

### Critical Pitfalls

1. **Retrofitting retries without error classification** — bumping `MaxRetry` without distinguishing transient vs. terminal errors means the second delivery attempt hits the same terminal-transition guard and dies immediately; this is the current state of `HandleImageConvert` today. Fix: explicit error taxonomy, only call `MarkFailed` for terminal errors, make the state machine re-entrant for transient retries. Must land before the reconciler phase.
2. **Reconciler double-processing legitimately-slow jobs** — a time-only sweep ("stuck in `queued`/`active` longer than N minutes") can't distinguish "enqueue genuinely failed" from "job is mid-flight but slow," causing duplicate concurrent processing and racing writes to the same storage key. Fix: check asynq's Inspector API before re-enqueuing, use `asynq.TaskID`-based idempotent enqueue, and make the worker handler itself idempotent (short-circuit if output already exists).
3. **Reconciler and asynq's own retry fighting over the same job** — once retry-safety is fixed, two independent recovery mechanisms exist; without an explicit ownership split (asynq owns tasks known to be in-queue, reconciler owns the enqueue-gap case only) they can both act on the same job simultaneously.
4. **Auth trusting network position instead of the key** — "internal-only" gets silently treated as "trusted," leaving routes unauthenticated or `client_id` scoping unenforced on reads. Fix: auth as mandatory global middleware (allowlist only `/healthz`), `404` (not `403`) for cross-client job access, no network-based auth-skip even in dev.
5. **Webhook retry storms and missing idempotency/signature** — fixed-interval unbounded retries flood a struggling receiver; missing delivery ID lets receivers double-process; missing/broken HMAC lets `callback_url` be spoofed. Fix: exponential backoff+jitter with a hard cap, stable per-event delivery ID unchanged across retries, HMAC-SHA256 over raw body with a per-client secret distinct from the API key.

## Implications for Roadmap

Based on combined research (feature dependencies, architecture composition, and pitfall sequencing all agree on this order), suggested phase structure:

### Phase 1: Merge and baseline
**Rationale:** PROJECT.md's first Active item ("merge `feat/scaffold-and-infra` into `main`") is an explicit precondition — no hardening work should happen on a branch that isn't the integration target.
**Delivers:** Existing vertical slice on `main`, CI/deployment baseline intact.
**Addresses:** N/A (housekeeping, not a feature)
**Avoids:** N/A

### Phase 2: Auth (API keys + client scoping)
**Rationale:** Hard prerequisite for rate limiting and webhook delivery attribution (both need a stable `client_id`); also the explicit first hardening priority per PROJECT.md.
**Delivers:** `clients` table-backed API-key auth, mandatory middleware on all non-public routes, `client_id` ownership enforced on read (404 for cross-client access), hashed key storage with dual-key column for future rotation.
**Addresses:** "API-ключи для клиентов через таблицу `clients`" (PROJECT.md Active)
**Avoids:** Pitfall 5 (network-position-as-auth), Pitfall 6 (plaintext/unrotatable keys)

### Phase 3: Retry-safety (error classification)
**Rationale:** Not explicitly in PROJECT.md's Active list but discovered as a hard technical prerequisite by research — the reconciler (Phase 5) and webhook retry logic (Phase 4) both depend on the worker correctly distinguishing transient vs. terminal errors. Building either on top of the current single-attempt state machine would compound the existing bug.
**Delivers:** Explicit error taxonomy in the worker, re-entrant state machine transitions, `IsFailure`/`SkipRetry` wired correctly so asynq's existing retry config actually functions.
**Uses:** asynq's `IsFailure` predicate, `MaxRetry`, `RetryDelayFunc` (already-vendored, no new dependency)
**Implements:** N/A (fix to existing `internal/worker` handler)
**Avoids:** Pitfall 1 (retrofitting retries without classification) — root cause of Pitfalls 7 and 8

### Phase 4: Rate limiting
**Rationale:** Hard-depends on Phase 2 (client identity to key the bucket); independent of Phases 3/5, could theoretically run in parallel with the reconciler track but is lower-risk to land right after auth while that middleware chain is fresh.
**Delivers:** Per-client token-bucket limiting (`go-chi/httprate`) layered after auth in the middleware chain, coarse pre-auth IP limit as a flood guard, 429 + Retry-After.
**Addresses:** "Rate limiting на клиента" (PROJECT.md Active)
**Avoids:** Pitfall 9 (rate limiting keyed on IP/weak identity instead of verified `client_id`)

### Phase 5: Webhook delivery
**Rationale:** Requires auth (Phase 2, for `callback_url`/secret attribution) and benefits from retry-safety (Phase 3, for consistent backoff patterns) already in place; this is the single largest feature in scope per FEATURES.md.
**Delivers:** Dedicated `webhook:deliver` asynq task/queue, HMAC-SHA256 signed payloads with per-client secret (distinct from API key), stable per-event delivery ID, exponential backoff+jitter with hard cap, full attempt history in `webhook_deliveries`, dead-letter on exhaustion.
**Addresses:** "Webhook-доставка результата" (PROJECT.md Active)
**Avoids:** Pitfalls 2, 3, 4 (missing idempotency key, missing/broken signature, retry storms)

### Phase 6: Reconciler / sweeper
**Rationale:** Explicitly sequenced after Phase 3 (retry-safety) per pitfalls research — a reconciler built against the current single-attempt state machine will encode assumptions that break once retry-safety changes the state machine underneath it. Also benefits from Phase 5 existing (the sweep query extends naturally to cover stuck `webhook_deliveries`, not just stuck jobs).
**Delivers:** Periodic asynq task (`reconcile:sweep`) that queries Postgres for stranded `queued`/`active` rows, cross-checks against asynq's Inspector state before re-enqueuing, bounded re-enqueue attempts, metrics on every sweep action.
**Addresses:** "Reconciler/свипер задач, зависших в `queued`" (PROJECT.md Active)
**Avoids:** Pitfall 7 (double-processing slow-but-healthy jobs), Pitfall 8 (reconciler vs. asynq retry ownership conflict)

### Phase 7: Content validation + storage lifecycle
**Rationale:** Both independent of the auth/webhook/reconciler cluster per FEATURES.md dependency analysis — low effort, no hard dependencies, can be slotted anywhere but grouped here as a natural "close the remaining CONCERNS.md gaps" phase.
**Delivers:** Magic-bytes sniff-then-reject (422) at upload time using `gabriel-vasile/mimetype`; MinIO/S3 bucket lifecycle TTL rules on `uploads/` and `results/` prefixes.
**Addresses:** "Валидация содержимого файла по magic bytes", "Lifecycle TTL на бакете S3/MinIO" (PROJECT.md Active)
**Avoids:** Pitfall 10 (sniff-but-don't-reject, or sniffing that itself becomes a resource-exhaustion vector)

### Phase 8: Observability
**Rationale:** Research explicitly recommends landing basic metrics early rather than last (observability enhances every other phase's ability to be verified), but a full Prometheus+asynqmon rollout is listed last here because it's most valuable once the other failure modes it needs to observe (stuck jobs, webhook failures, rate-limit rejections) actually exist to be measured. Teams should consider pulling a minimal `/metrics` skeleton earlier if phases 3-7 span a long timeline.
**Delivers:** `/metrics` endpoint (Prometheus client), real `/readyz` checking Postgres/Redis/S3, `asynqmon` sidecar in docker-compose, dashboards/alerts specifically for failure paths (webhook exhausted-retry count, reconciler recovery count, auth-rejection count).
**Addresses:** "Метрики и наблюдаемость (asynqmon + Prometheus)" (PROJECT.md Active)
**Avoids:** The "looks done but isn't" gap of only alerting on happy-path throughput, missing failure-path metrics

### Phase Ordering Rationale

- **Auth first:** hard dependency for rate limiting (needs `client_id` to key on) and webhook delivery (needs `client_id`/secret attribution) — not just a priority preference but a structural precondition confirmed independently by FEATURES.md's dependency graph and PITFALLS.md's phase mapping.
- **Retry-safety before reconciler:** this is the single most emphasized sequencing constraint across both FEATURES.md and PITFALLS.md — skipping it produces a reconciler that actively causes duplicate-processing bugs (Pitfall 7) rather than fixing the stuck-job problem it's meant to solve.
- **Webhook delivery and reconciler share failure-recovery design space:** both use the same "Postgres-first, then idempotent `TaskID`-based enqueue" pattern; landing webhook delivery before the reconciler lets the reconciler's sweep query be extended to also cover stuck `webhook_deliveries` rows without redesign.
- **Content validation and storage lifecycle are true parallel tracks:** no dependency on auth/webhook/reconciler; a team could pull these forward if there's spare capacity without disrupting the critical path.
- **This grouping avoids the two biggest pitfalls the research flags as most likely to introduce new bugs** (Pitfall 1's retry-safety retrofit, and Pitfall 7's reconciler double-processing) by making their correct ordering structural rather than a note to remember.

### Research Flags

Phases likely needing deeper research during planning (`/gsd:plan-phase --research-phase <N>`):
- **Phase 5 (Webhook delivery):** MEDIUM confidence — no single canonical reference for this exact combination (asynq-driven delivery + `webhook_deliveries` schema); backoff window, retry cap, and SSRF-guarding of client-supplied `callback_url` need concrete decisions during planning, not just pattern-level guidance.
- **Phase 6 (Reconciler):** MEDIUM confidence — explicitly "build-it-yourself," synthesized from asynq's public Inspector API plus general dual-write literature, not a single canonical "asynq reconciler" pattern. Lease/heartbeat threshold values and the exact ownership split with asynq's native retry need to be worked out concretely.
- **Phase 7 (Storage lifecycle, MinIO-specific):** MEDIUM confidence on whether MinIO's lifecycle engine fully matches AWS S3 lifecycle semantics — flagged in STACK.md as needing verification against the specific MinIO server version in use.

Phases with standard, well-documented patterns (skip research-phase):
- **Phase 2 (Auth):** HIGH confidence — SHA-256 + constant-time compare for high-entropy API keys is industry-standard (Stripe/GitHub PAT pattern), chi middleware ordering is well-documented.
- **Phase 3 (Retry-safety):** HIGH confidence — asynq's `IsFailure`/`SkipRetry`/retry-config semantics are directly documented in official asynq docs.
- **Phase 4 (Rate limiting):** HIGH confidence — `go-chi/httprate` is an official go-chi-org project with direct chi integration; token-bucket-per-client is the standard algorithm choice for this workload shape.
- **Phase 8 (Observability):** HIGH confidence — `prometheus/client_golang` and `asynqmon` integration patterns are official-docs-verified.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | Verified via Go module proxy (exact versions) and GitHub API (maintenance/archived status) for every recommended library; no speculative recommendations |
| Features | MEDIUM-HIGH | Webhook/rate-limit/content-validation patterns are well-documented industry practice (Stripe, GitHub, general engineering references); asynq-specific reconciliation is synthesized from asynq's public API docs + general dual-write literature, not a single canonical source |
| Architecture | HIGH for component placement / chi middleware ordering / magic-byte libraries (verified against official docs); MEDIUM for reconciler and webhook-delivery patterns (multiple independent sources, no single canonical spec for this exact stack combination) |
| Pitfalls | HIGH for patterns grounded directly in the existing codebase (`.planning/codebase/CONCERNS.md` independently documents most of these as known bugs); MEDIUM for general webhook/queue ecosystem practices (cross-checked against multiple independent sources) |

**Overall confidence:** HIGH

### Gaps to Address

- **Reconciler lease/heartbeat thresholds:** research recommends the pattern (asynq Inspector check + heartbeat/lease staleness) but concrete threshold values (grace period for `queued`, staleness window for `active`) need to be decided during Phase 6 planning based on actual job-duration data, not assumed from research alone.
- **Webhook retry window length:** research suggests "minutes-to-hours" (shorter than Stripe's 3-day window, appropriate for internal callers) but the exact backoff schedule and total attempt cap should be a deliberate Phase 5 planning decision, not inherited verbatim from external references.
- **MinIO lifecycle-rule semantics vs. AWS S3 docs:** flagged as MEDIUM confidence in STACK.md — verify against the specific MinIO server version in docker-compose during Phase 7 implementation rather than assuming AWS lifecycle documentation applies verbatim.
- **`hibiken/asynqmon` long-term maintenance:** last tagged release is from 2024 (no recent releases), though it remains the only actively-used web UI for asynq with no viable substitute — acceptable risk, but worth a brief check-in during Phase 8 planning that the pinned image/flags still work as documented.
- **SSRF guarding of client-supplied `callback_url`:** architecture research flags this as a needed safeguard (reject callback URLs resolving to internal/link-local ranges) but doesn't fully specify the validation approach — needs concrete design during Phase 5 planning.

## Sources

### Primary (HIGH confidence)
- Go module proxy (`proxy.golang.org`) — exact latest versions for all recommended libraries
- GitHub API (`api.github.com/repos/...`) — maintenance/archived status verification
- [go-chi/httprate](https://github.com/go-chi/httprate), [go-chi/chi middleware docs](https://pkg.go.dev/github.com/go-chi/chi/v5/middleware) — official chi-org middleware patterns
- [gabriel-vasile/mimetype](https://github.com/gabriel-vasile/mimetype/) and magic package docs — HEIC/HEIF signature coverage confirmed
- [prometheus/client_golang promhttp docs](https://pkg.go.dev/github.com/prometheus/client_golang/prometheus/promhttp) — official Prometheus project docs
- [hibiken/asynq Wiki: Task Retry](https://github.com/hibiken/asynq/wiki/Task-Retry), [Unique Tasks](https://github.com/hibiken/asynq/wiki/Unique-Tasks), [Periodic Tasks](https://github.com/hibiken/asynq/wiki/Periodic-Tasks) — `IsFailure`/`SkipRetry`/`TaskID` semantics
- [Transactional outbox pattern — AWS Prescriptive Guidance](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html)
- [Stripe webhook docs](https://docs.stripe.com/webhooks) — retry policy, signature verification reference implementation
- `.planning/codebase/CONCERNS.md`, `.planning/codebase/ARCHITECTURE.md`, `.planning/PROJECT.md` — first-party ground truth for existing system and scope

### Secondary (MEDIUM confidence)
- [go-redis/redis_rate](https://github.com/go-redis/redis_rate) — GCRA/leaky-bucket algorithm (deferred upgrade path)
- [hibiken/asynqmon](https://github.com/hibiken/asynqmon) — `--enable-metrics-exporter`/`--prometheus-addr` flags, no recent tagged release so flag names not independently re-verified against latest source
- Webhook reliability/retry best-practice articles (Hookdeck, Svix, HookRay, Digital Applied) — cross-checked across multiple independent sources, consistent conclusions
- [Using FOR UPDATE SKIP LOCKED For Queue Workflows](https://www.netdata.cloud/academy/update-skip-locked/) — reconciler sweep query pattern

### Tertiary (LOW confidence)
- MinIO-specific lifecycle-rule behavior vs. documented AWS S3 semantics — not independently verified against the project's actual MinIO server version; flag for validation during Phase 7 implementation

---
*Research completed: 2026-07-02*
*Ready for roadmap: yes*
