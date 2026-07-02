# Pitfalls Research

**Domain:** Production-hardening an internal async job-processing service (Go/chi/asynq/Postgres/MinIO) — adding auth, rate limiting, webhook delivery, a reconciler, magic-bytes validation, S3 lifecycle TTL, and observability to an already-running vertical slice.
**Researched:** 2026-07-02
**Confidence:** HIGH for patterns grounded in the existing codebase (`.planning/codebase/CONCERNS.md`), MEDIUM for general webhook/queue ecosystem practices (verified against multiple independent sources).

This research assumes the reader has read `.planning/codebase/CONCERNS.md`. Several pitfalls below are not hypothetical — they are the exact bugs already present in OctoConv's codebase, described here in generalized form so the roadmap phases that touch them don't reintroduce the same mistake elsewhere.

## Critical Pitfalls

### Pitfall 1: Retrofitting retry-safety by adding retries without first classifying errors

**What goes wrong:**
Teams bump asynq's `MaxRetry` or add backoff config and assume "retries are now fixed," without changing the handler's error-handling logic. If the handler still treats every non-nil error as terminal (calls a `MarkFailed`-equivalent state transition before returning), the retry mechanism never gets a chance to run — the *second* delivery of the same task fails immediately because the state machine now rejects the `queued`/`active` → terminal transition a second time, and that guard error gets wrapped in `SkipRetry` (or equivalent), silently converting "retry configured" into "one attempt, then archived." This is the exact bug already present in OctoConv's `HandleImageConvert` (`internal/worker/worker.go:40-63`) — asynq is configured for retries but every job effectively gets one real attempt.

**Why it happens:**
Retry-safety looks like a queue-configuration problem ("just set MaxRetry higher") when it's actually an error-taxonomy problem: the handler must know the difference between "this specific input can never succeed" (terminal — mark failed, don't retry) and "the world was briefly unavailable" (transient — return the error and let asynq redeliver, leaving the job's domain state unchanged or in a re-entrant-safe state). Retrofitting onto a handler that currently makes this same terminal-only assumption compounds the mistake because the state machine itself (not just the handler logic) actively forbids re-entry.

**How to avoid:**
1. Define an explicit error taxonomy in code (sentinel errors or an error-classification function) — e.g. `ErrTerminal` (bad input, unsupported format, malformed file) vs. everything else defaults to transient.
2. Only transition the job to a terminal DB state (`failed`) for classified-terminal errors. For transient errors, return the raw error from the handler *without* moving the job out of `active` — let asynq's redelivery re-invoke the handler.
3. Make the state machine re-entrant: allow `active -> active` (no-op) or design the handler to be idempotent on retry (i.e., check-then-act on whether the output already exists in storage before re-running the conversion), not just permissive of the transition.
4. Use asynq's `IsFailure` predicate (verified via Context7/official docs: `Config.IsFailure(error) bool`) to prevent transient errors from consuming the task's retry-count budget at all, so a flaky dependency doesn't exhaust retries before it recovers.
5. Cap retryable transient errors with `asynq.MaxRetry` and route exhausted-retry tasks to asynq's archive/dead-letter queue rather than silently dropping them — someone needs to see permanently-stuck jobs.

**Warning signs:**
- The handler calls the "mark failed" repository method before returning an error, in the same code path used for storage/network errors.
- No test exercises "handler fails once due to a transient error, then succeeds on retry" — only "handler fails and job ends up `failed`."
- `asynq.SkipRetry` appears wrapping errors that originate from infrastructure (DB, S3, Redis) rather than from input validation.
- Job state transition guards (`queued -> active`, `active -> done/failed`) have no path back to `active` from `active` or `failed` for legitimate retries.

**Phase to address:**
Should be its own phase (or the first sub-task of the reliability phase), completed *before* the reconciler phase — the reconciler and retry-safety fix interact (see Pitfall 3) and should not be built independently of each other.

---

### Pitfall 2: Webhook delivery with no idempotency key, letting retries create duplicate side effects downstream

**What goes wrong:**
The delivery system retries a webhook because it didn't receive a 2xx within its timeout — but the original POST *did* arrive and was processed by the receiver, and the receiver's slow response (not a delivery failure) triggered the retry. The receiving internal service then processes "job completed" twice: double-charges, double-triggers downstream automation, or writes duplicate records. This is a "your problem becomes their problem" pitfall — it doesn't break OctoConv itself, but it breaks every internal consumer of `callback_url`, and consumers will not consistently build dedup logic unless the payload gives them a stable key to dedup on.

**Why it happens:**
At-least-once delivery is the only economically sane guarantee for webhooks (exactly-once delivery requires distributed transactions across service boundaries that don't exist here). Teams build the retry/backoff logic first (because it's visible in demos) and treat idempotency as a receiver-side concern, forgetting that the receiver can only dedup if the sender provides something stable to dedup on.

**How to avoid:**
- Include a stable, unique delivery identifier in every webhook payload/header (e.g., `X-Octoconv-Delivery-Id` sourced from the `webhook_deliveries.id` primary key, *not* regenerated per retry attempt — same delivery ID on every retry of the same logical event).
- Document in the webhook contract that receivers MUST treat delivery as at-least-once and dedup on this ID; this is a contract concern even for internal consumers.
- Distinguish "delivery attempt" (retryable, has its own row/counter in `webhook_deliveries`) from "event" (the job completion, one per job) — the delivery ID should be tied to the job/event, not to the attempt, so retries of the same event carry the same ID.

**Warning signs:**
- `webhook_deliveries` schema has no natural idempotency key exposed to the payload, or the ID changes on each retry.
- No documentation tells internal consumers what "duplicate delivery" looks like or how to detect it.

**Phase to address:**
Webhook delivery phase — must be part of the initial webhook payload/header design, not bolted on after the first internal consumer complains about duplicates.

---

### Pitfall 3: No signature verification on outbound webhooks, or verification the receiver can't practically implement

**What goes wrong:**
Either (a) OctoConv sends webhooks with no signature at all, so any internal service that can guess or intercept a `callback_url` can forge a "job completed" notification with an attacker-controlled result URL, or (b) a signature scheme is added but only documented informally, so most internal consumers skip verification because it's friction, defeating the purpose. A related, more subtle version: verifying against the parsed/re-serialized body instead of the exact raw bytes sent — JSON re-serialization can reorder keys or change whitespace, making the signature check fail (or "succeed" against the wrong body) depending on which side is affected.

**Why it happens:**
"Internal service, low priority" reasoning creeps in ("it's not public-facing, why bother") — but internal-only is a network-position assumption, not an authentication guarantee (see Pitfall 5). Signature schemes are also frequently implemented once and never load-tested against real receiver code, so verification bugs (raw body vs. re-parsed body, timestamp tolerance, encoding) aren't caught until an internal consumer's verification silently always fails or always passes.

**How to avoid:**
- Sign every outbound webhook with HMAC-SHA256 over the raw request body plus a timestamp, using a per-client secret (from the `clients` table, likely a separate `webhook_secret` distinct from the API key used for inbound auth — do not reuse the API key as the webhook signing secret).
- Include the timestamp in the signed payload and give receivers a tolerance window (e.g., 5 minutes) to reject replayed old webhooks, but don't make the tolerance so tight that clock skew between internal services causes false rejections.
- Publish a minimal verification snippet (even just a comment in the webhook payload docs) so internal teams copy-paste correct verification instead of reimplementing HMAC comparison with non-constant-time `==` checks.
- Use constant-time comparison (`hmac.Equal` in Go) when *OctoConv itself* validates anything symmetric, and recommend the same to consumers.

**Warning signs:**
- `callback_url` is stored and POSTed to with no additional secret/signature field in the `clients` or `webhook_deliveries` schema.
- No dedicated webhook-secret column/rotation mechanism separate from the client's API key.

**Phase to address:**
Webhook delivery phase, designed alongside idempotency (Pitfall 2) — these two are typically shipped together as "webhook payload contract" and should not be split across phases.

---

### Pitfall 4: Unbounded or unbackoff'd webhook retries causing a retry storm against a struggling receiver

**What goes wrong:**
A receiving internal service degrades (slow, briefly down, redeploying). OctoConv's webhook sender retries aggressively (fixed short interval, no cap, no backoff) — a burst of completed jobs during the outage now floods the same endpoint with tightly-packed retries the moment it degrades, keeping it down longer or causing it to reject even healthy traffic once it recovers (thundering herd on recovery). Without a maximum retry count or a dead-letter/give-up path, `webhook_deliveries` rows accumulate indefinitely in a "still retrying" state and nobody is ever alerted that a receiver has been unreachable for days.

**Why it happens:**
The naive version of a delivery system ("send the POST, if it fails try again in N seconds") looks correct in isolation and only breaks under load or partial-outage conditions that don't show up in a demo.

**How to avoid:**
- Use exponential backoff with jitter (e.g., 1s, 5s, 30s, 5m, 30m, up to hours) and a hard cap on both retry count and total time window (industry-common: 24-48h before giving up).
- Cap the number of *concurrent* in-flight delivery attempts to a single `callback_url`/client so a burst of completed jobs doesn't multiply into a burst of simultaneous POSTs to the same struggling endpoint.
- After exhausting retries, mark the delivery permanently failed in `webhook_deliveries` and surface it (metrics/log) rather than deleting or silently stopping — this ties directly into the observability phase.
- If asynq is reused to schedule webhook delivery attempts (natural fit given it already provides delayed/retry task scheduling), apply the same `IsFailure`/backoff configuration used for the fix in Pitfall 1, rather than hand-rolling a second retry mechanism.

**Warning signs:**
- Retry interval is a fixed constant with no backoff multiplier.
- No maximum attempt count or expiry in the webhook delivery loop/schema.
- No metric for "deliveries currently in the retrying state" or "deliveries that exhausted retries."

**Phase to address:**
Webhook delivery phase for the retry/backoff logic; observability phase should add the metric/alert for exhausted deliveries so this doesn't silently rot.

---

### Pitfall 5: API-key auth that actually trusts network position (Docker network / internal VPC) instead of the key

**What goes wrong:**
Because clients are "only internal services," it's tempting to treat network reachability as sufficient trust and implement auth as an afterthought — e.g., a middleware that's easy to bypass by hitting an internal port directly, or logic that only checks the API key on some routes but not others, or a health/debug/admin endpoint left unauthenticated "because it's internal." The result: anyone who can reach the API on the internal network (which in practice is a larger trust boundary than intended — other teams' services, CI runners, debug tooling, a misconfigured ingress) can submit jobs as any client or read any client's job status, because the code path that's supposed to enforce `client_id` scoping is either missing or only partially wired (this is explicitly already the case in OctoConv today — `internal/api/routes.go:16-20` has zero auth middleware, and `jobs.client_id` exists in the schema but nothing enforces it).

**Why it happens:**
"Internal service" is treated as a synonym for "trusted," but internal network boundaries erode constantly (shared clusters, service meshes, third-party CI, contractors, misconfigured network policies) and are not something the application itself controls or can verify. The org constraint here (see PROJECT.md: "auth requirements not reduced despite internal-only clients") exists precisely because this mistake is common enough to call out explicitly.

**How to avoid:**
- Enforce API-key auth as middleware applied globally to the router (or an explicit allowlist of the 1-2 truly public routes like `/healthz`), not opt-in per-handler — opt-in auth is the pattern that leaks unauthenticated routes over time as new endpoints get added.
- Resolve the API key to a `client_id` in the middleware and thread it through `context.Context`; every handler that reads/writes a `jobs` row must filter/verify against that `client_id`, not just trust the caller-supplied job ID.
- Return `404` (not `403`) for jobs that exist but belong to a different client, to avoid confirming job-ID existence to an unauthorized caller.
- Do not treat "runs inside the Docker/K8s internal network" as an auth bypass condition anywhere in the code, even temporarily for local dev — use a distinct, clearly-fake dev API key seeded in `docker-compose.yml` instead of an auth-skip flag, so the auth code path is always exercised.

**Warning signs:**
- Any route registered outside the auth middleware group without a documented reason.
- A `client_id` on the `jobs` row that's set once at creation but never checked again on read (`GET /v1/jobs/{id}` returning any job regardless of the caller's key).
- Environment-variable or build-flag "skip auth" toggles that could accidentally ship enabled.

**Phase to address:**
Auth phase — explicitly the first hardening priority per PROJECT.md Key Decisions ("Auth + rate limiting — первый приоритет hardening").

---

### Pitfall 6: API keys stored/logged in plaintext, or rotation designed as an afterthought

**What goes wrong:**
Keys generated for the `clients` table get stored as plaintext (or reversibly encrypted) rather than hashed, meaning a database read (backup leak, replica misconfiguration, insider access) exposes every client's live credential. Separately, keys get logged in cleartext via standard HTTP access logs or error logs (`Authorization` header dumped on a 401/500), and because rotation was never designed in from the start, the schema has only one active key per client — rotating a leaked key requires either an outage window (old key stops working before the new one is distributed) or an emergency schema migration under pressure.

**Why it happens:**
Hashing feels like unnecessary friction for an "internal, low-stakes" system, and rotation is invisible until the first time a key needs to be rotated urgently (compromise, employee offboarding) — at which point the lack of a grace-period mechanism turns a 5-minute credential swap into a coordinated multi-team incident.

**How to avoid:**
- Store only a salted hash (SHA-256 is sufficient for high-entropy random API keys, unlike password hashing) of the API key in `clients`; show the raw key to the operator exactly once at creation time.
- Support two simultaneously-valid keys per client from day one (e.g., `clients.api_key_hash` + `clients.api_key_hash_secondary` with an expiry timestamp on the secondary), even if the rotation *tooling* (CLI/admin endpoint) ships later — retrofitting the schema for multi-key support after the fact is far more disruptive than including the column now.
- Scrub `Authorization`/API-key headers from all structured logs and error responses at the middleware/logging-config level, not per-handler.
- Treat key values as secrets in any seed data / fixtures / docker-compose env — don't reuse the same "dev" key across environments long enough for it to end up in a shared `.env.example` that gets copied into a real deployment.

**Warning signs:**
- `clients` table has a single plaintext `api_key` column with no hash and no secondary-key column.
- Access logs or error handlers include the raw `Authorization` header value.
- No documented/scripted process exists for "rotate this client's key without downtime."

**Phase to address:**
Auth phase.

---

### Pitfall 7: Reconciler double-processing jobs that are merely slow, not actually stranded

**What goes wrong:**
The reconciler/sweeper scans for jobs stuck in `queued` (or `active`) past a threshold and re-enqueues them — but "stuck" is inferred purely from elapsed wall-clock time on the DB row, without checking whether a worker is, in fact, still actively processing that job (just slowly, e.g. a large image or a briefly overloaded libvips process). The reconciler re-enqueues a duplicate task; now two workers race to process the same job concurrently, both call `MarkActive`/`MarkDone` against the same row, produce two competing writes to the same `results/{job_id}/...` storage key, and the client's presigned download URL may point at whichever write "won," non-deterministically. This is the single most likely new bug this milestone introduces, because it directly modifies the exact state machine and enqueue path already flagged as fragile in CONCERNS.md.

**Why it happens:**
A time-based sweep is the simplest reconciler to write, but "job has been in `queued`/`active` longer than N minutes" conflates two very different situations: (1) the enqueue call genuinely failed after the DB write committed (the documented gap in `internal/api/handlers.go:105-109`) — safe to re-enqueue, nothing is running; and (2) the job is legitimately mid-flight in a worker that's just slow or under load — unsafe to re-enqueue, a duplicate will race the original.

**How to avoid:**
- Give the reconciler a way to distinguish "never made it into the queue" from "in the queue/being processed but slow." Options, in order of robustness:
  - Best: use asynq's own inspector API (verified via Context7/official docs) to check whether a task with this job's ID actually exists in the queue (scheduled/pending/active/retry sets) before re-enqueueing — if asynq already knows about it, do not duplicate.
  - Minimum: use a heartbeat/lease pattern — the worker periodically updates an `updated_at`/`lease_expires_at` timestamp on the `active` job row while processing; the reconciler only re-enqueues `active` jobs whose lease has actually expired (not just "old"), and only re-enqueues `queued` jobs whose age exceeds a bound tied to a max realistic enqueue-lag, not conversion time.
- Make re-enqueue itself idempotent at the queue layer: derive the asynq task ID deterministically from the job ID (asynq supports unique task options — verified via official docs `asynq.TaskID`/`asynq.Unique`) so a duplicate enqueue for the same job ID is rejected by asynq itself rather than relying purely on reconciler logic to never race.
- Make the worker handler itself idempotent regardless of reconciler correctness (defense in depth): before running the conversion, check if `results/{job_id}/...` already exists and the job is already `done`; if so, no-op rather than re-converting and re-uploading.
- Reconcile in both directions deliberately and separately: "stranded queued row with no matching queue task" (recoverable) vs. "orphaned queue task with no matching DB row" (shouldn't happen but log/alert if it does) — don't collapse these into one sweep function.

**Warning signs:**
- Reconciler query is only `WHERE status = 'queued' AND created_at < now() - interval`, with no check against asynq's actual queue/task state.
- No idempotency key or unique task ID used when the reconciler calls the same `Enqueue*` function the API handler calls.
- No test exercises "reconciler runs concurrently with a legitimately slow in-flight job" — only "reconciler runs against a job that was never enqueued."
- Worker handler has no short-circuit for "output already exists in storage for this job ID."

**Phase to address:**
Reconciler phase — should be sequenced *after* the retry-safety fix (Pitfall 1), because a correct reconciler needs the state machine to already distinguish transient-retry-in-progress states from truly-abandoned ones; building the reconciler against the current single-attempt state machine will encode assumptions that break once retry-safety changes the state machine underneath it.

---

### Pitfall 8: Reconciler and asynq's own retry mechanism fighting over the same job

**What goes wrong:**
Once Pitfall 1 is fixed (asynq retries transient failures), the system now has *two* independent recovery mechanisms operating on the same job: asynq's built-in redelivery (for tasks that got into the queue but whose handler failed transiently) and the new reconciler (for jobs whose DB row exists but which may never have made it into the queue, or whose queue task is presumed lost). If these two mechanisms aren't designed with a shared understanding of "who owns recovery for which failure mode," they can both attempt to recover the same job simultaneously — e.g., the reconciler re-enqueues a job that asynq was about to retry naturally, doubling concurrency on that job right when Pitfall 7's race condition is most likely to bite.

**Why it happens:**
Retry-safety and reconciliation are usually planned as two separate, sequential backlog items ("fix retries," then later "add a sweeper for stuck jobs") without an explicit design pass on how their responsibilities don't overlap — each looks complete in isolation.

**How to avoid:**
- Write down (in the design, not just in code comments) a clear ownership split: asynq's retry/backoff owns recovery for tasks *known to be in the queue* that failed; the reconciler owns recovery only for the enqueue-gap case (DB row exists, no corresponding queue task ever got created, or is provably gone e.g. after asynq's own retry budget is exhausted and the task was archived/dead-lettered).
- Have the reconciler check asynq's queue/archive state (via the inspector API) before acting, so it never re-enqueues a job that asynq is still actively retrying.
- Consider: for jobs whose asynq task was archived (retries exhausted), decide explicitly whether that's a terminal failure (surface to client, no reconciler action) or a candidate for reconciler-driven re-enqueue with a distinct, lower threshold count — don't let the reconciler retry indefinitely, or you've just built an infinite-retry loop with extra steps.

**Warning signs:**
- Reconciler and asynq retry configuration were designed/implemented in separate PRs without either referencing the other's failure-handling assumptions.
- No single source of truth for "how many total attempts has this job had" across both mechanisms combined (asynq's per-task retry count and any reconciler-driven re-enqueues need to sum toward one job-level attempt cap).

**Phase to address:**
Reconciler phase, but requires explicit acknowledgment of the retry-safety phase's design as an input — flag this as a phase *dependency*, not just an ordering preference.

---

### Pitfall 9: Rate limiting implemented at a layer that doesn't match the actual bottleneck, or that trusts the same weak identity as auth

**What goes wrong:**
Rate limiting gets added per-IP (meaningless for internal services often calling through a shared gateway/NAT, where many clients share one source IP, or one client's traffic looks like it comes from many pods) instead of per-`client_id` derived from the verified API key. Or, rate limiting is enforced only at the HTTP layer (requests/sec to `POST /v1/jobs`) while the actual scarce resource is worker/conversion capacity — a client under the request-rate limit can still flood the queue with a burst of large, slow-converting jobs and starve every other client's jobs, because nothing limits *concurrent in-flight jobs per client*, only request rate.

**Why it happens:**
Per-IP rate limiting is the default example in every rate-limiting library/tutorial and is trivial to bolt onto chi middleware without touching the auth-derived identity; it's easy to ship without realizing it doesn't map to the actual multi-tenancy model (multiple clients behind shared infra, or one client's burst monopolizing shared worker concurrency regardless of request rate).

**How to avoid:**
- Key all rate limiting off the authenticated `client_id`, never off IP — this requires rate limiting middleware to run *after* auth middleware in the chain, not before.
- Add a second limiter dimension beyond requests/sec: cap concurrent in-flight (`queued`+`active`) jobs per client, since that's what actually protects shared worker concurrency (`WORKER_CONCURRENCY`) from one noisy client — a burst of accepted-but-queued jobs is just as damaging as a burst of raw requests.
- Return `429` with a `Retry-After` header so well-behaved internal clients back off correctly instead of hammering.

**Warning signs:**
- Rate limit middleware reads `r.RemoteAddr`/`X-Forwarded-For` instead of the resolved client identity from auth context.
- Only one rate-limiting dimension exists (request rate), with no per-client cap on jobs currently `queued`/`active`.

**Phase to address:**
Rate limiting phase, sequenced immediately after auth (rate limiting has a hard dependency on auth resolving a `client_id` first).

---

### Pitfall 10: Magic-bytes validation that trusts the sniffed type without re-checking against the requested conversion pair, or that itself becomes a decompression-bomb vector

**What goes wrong:**
Magic-bytes sniffing gets added to replace trusting the file extension/Content-Type (closing the gap flagged in CONCERNS.md), but two follow-on mistakes are common: (1) the code sniffs the type and stores/logs it but doesn't actually *reject* mismatches against the client-declared source format before enqueueing — so a mismatched file still reaches the worker and either fails late (wasting a worker slot/timeout) or, worse, is silently "coerced" into being processed as the client-declared type; (2) the sniffing step itself reads/buffers more of the file than necessary to make a decision, or triggers early decode, reintroducing a resource-exhaustion vector at the API layer instead of the worker layer — moving the decompression-bomb risk (already flagged in CONCERNS.md for the worker) earlier in the pipeline rather than eliminating it.

**Why it happens:**
Magic-bytes checks are often treated as a pure "detect and log" addition bolted onto the existing upload handler without revisiting the accept/reject decision point, and sniffing libraries that read a small fixed-size header are assumed to be safe by default without confirming they don't attempt a full decode.

**How to avoid:**
- Reject (422, same status as the existing unsupported-pair check) at upload time when sniffed magic bytes don't match the client-declared source format — don't just log and proceed.
- Ensure sniffing reads only a small, bounded header (a few hundred bytes to a few KB), never a full decode, at the API layer — this is a distinct, smaller check than the full decompression-bomb guard the worker still needs (per CONCERNS.md, that guard doesn't exist yet either and should be tracked alongside, even if not literally the same phase).
- Reuse the same format/pair validation logic path that already exists for extension-based checks (`internal/convert` registry) so magic-bytes becomes an additional gate in front of it, not a parallel, divergent code path.

**Warning signs:**
- Magic-bytes result is logged/stored but the existing 422-on-unsupported-pair logic is untouched (still only keyed off extension).
- The sniffing library/call reads the entire file into memory or invokes a decoder rather than checking a fixed header prefix.

**Phase to address:**
Magic-bytes validation phase; flag the worker-side decompression-bomb guard (pixel-count/dimension limits) as a related-but-separate CONCERNS.md item that this phase does not by itself resolve.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|-----------------|------------------|
| Single API key per client, no rotation support in schema | Faster auth phase delivery | Emergency key rotation requires a schema migration under incident pressure | Never for this milestone — add the secondary-key column now even if rotation tooling ships later |
| Time-based-only reconciler sweep (no queue-state check) | Simple to implement and reason about | Double-processes legitimately slow jobs (Pitfall 7) once volume/latency variance increases | Only acceptable as a stopgap if reconciler action is "alert a human," never "auto re-enqueue," until queue-state checking is added |
| Fixed-interval webhook retry with no backoff | Simple, ships fast | Retry storms against a degraded receiver (Pitfall 4) | Acceptable only for a true MVP with a single, well-known internal consumer and manual monitoring — not for general `callback_url` support |
| Treating "runs on internal network" as implicit auth | Skips auth middleware plumbing | Silent full-access bypass the moment network boundaries shift (Pitfall 5) | Never |
| Logging full request/response bodies for webhook delivery debugging | Easier troubleshooting | Leaks signing secrets or client payload data into log aggregation | Only with signature/secret fields explicitly redacted before logging |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|-----------------|-------------------|
| asynq (Redis) | Treating `MaxRetry` config as sufficient without handler-level error classification | Combine `IsFailure` predicate + explicit terminal/transient error taxonomy in the handler (Pitfall 1) |
| asynq (Redis) | Reconciler re-enqueues without checking asynq's own queue/archive state first | Use the asynq inspector API to check task existence/state before re-enqueueing; use `asynq.TaskID`/unique options to make re-enqueue idempotent at the broker level |
| Postgres (job state machine) | State transition guards only permit forward-only transitions (`queued->active->done/failed`), blocking legitimate retry re-entry | Add an explicit re-entrant path (`active->active` no-op, or a retry-count-aware transition) rather than loosening the guard entirely |
| MinIO/S3 (webhook payload) | Presigned download URL embedded in the webhook payload generated against an internal-only hostname unreachable by the receiving service (the exact `minio:9000` issue already flagged in CONCERNS.md for the polling API) | Generate the presigned URL used in webhook payloads against the same externally-reachable endpoint config as `GET /v1/jobs/{id}`, and add a smoke test that fetches the URL from outside the internal network |
| Internal HTTP clients receiving webhooks | Assumed to always implement retry/backoff/idempotency correctly with no contract given | Publish an explicit webhook contract doc (headers, signature scheme, delivery-ID semantics, expected response codes) so internal teams don't each reinvent handling |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|-----------------|
| Reconciler sweep runs a full-table scan over `jobs` on every tick | Sweep latency grows with total historical job count, not just the stuck subset | Index/partial-index on `status` (or a partial index `WHERE status IN ('queued','active')`), and bound the sweep to a reasonable lookback window | Once job history grows past tens of thousands of rows without an index |
| Webhook delivery sent synchronously from the worker's job-completion path | Worker slot blocked on a slow/unresponsive receiver, reducing effective conversion concurrency (echoes the existing "no timeout on storage/DB calls" issue in CONCERNS.md) | Enqueue webhook delivery as its own asynq task (or outbox row processed by a separate dispatcher), decoupled from the conversion worker's critical path | As soon as any single internal consumer has non-trivial latency or intermittent slowness |
| Rate limiter backed by an in-memory counter per API instance | Effective limit is `configured_limit * replica_count`, silently far looser than intended once the API scales horizontally | Use a shared store (Redis, already in the stack via asynq) for rate-limit counters, not per-process memory | The moment the API runs more than one replica |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Reusing the client's inbound API key as the webhook HMAC signing secret | A leaked API key compromises both inbound auth and the receiver's ability to trust webhooks; also complicates independent rotation | Separate `webhook_secret` per client, rotatable independently of the API key |
| No constant-time comparison when validating any HMAC/signature server-side | Timing side-channel could theoretically assist forgery over many attempts | Use `hmac.Equal` (Go stdlib) or equivalent constant-time compare, never `==`/`bytes.Equal` on secret-derived values |
| Returning `403` instead of `404` for jobs belonging to another client | Confirms job-ID existence/enumeration to an unauthorized caller | Return `404` uniformly for "doesn't exist" and "exists but not yours" |
| Presigned S3 URLs with long/default expiry embedded in webhook payloads that may sit in a receiver's logs indefinitely | A logged webhook payload becomes a long-lived, unauthenticated download link to the client's converted file | Use short presigned URL expiry (minutes, not the S3 default which can be much longer) and consider requiring the receiver to call back to `GET /v1/jobs/{id}` for a fresh URL rather than embedding a long-lived one |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|--------------|-------------------|
| Client has no way to know whether webhook delivery ever succeeded, only whether polling shows job `done` | Internal teams silently fall back to polling anyway, defeating the point of adding webhooks | Expose delivery status/history via `GET /v1/jobs/{id}` (e.g. embed the latest `webhook_deliveries` status) so clients can debug their own integration without DB access |
| Rate-limit rejection returns a bare `429` with no guidance | Internal teams hardcode arbitrary retry delays or hammer the endpoint | Always include `Retry-After` and a machine-readable reason in the 429 body |
| Reconciler-recovered jobs are indistinguishable from normally-processed jobs in the API response | Hard to debug "why did my job take 10x longer than usual" | Record reconciler intervention in `job_events` (an append-only log already exists per PROJECT.md) so it's visible via the same audit trail as other transitions |

## "Looks Done But Isn't" Checklist

- [ ] **Retry-safety fix:** Often missing a test that actually forces a transient failure and asserts the job succeeds on a subsequent asynq redelivery — verify by writing a test that injects one failing then one succeeding storage/DB call and confirms the job reaches `done`, not just that the code compiles against the new error taxonomy.
- [ ] **Webhook delivery:** Often missing signature verification documentation/reference implementation for consumers — verify a sample verifier script/snippet exists and was tested against a real delivered payload, not just that signing code exists on the sender side.
- [ ] **Reconciler:** Often missing a test for the "job is legitimately still processing, slowly" case — verify by writing a test where the reconciler runs mid-flight against an `active` job with a fresh heartbeat and asserts it does NOT re-enqueue.
- [ ] **API-key auth:** Often missing coverage on *every* route, not just the primary ones — verify by asserting `/healthz` (or documented public routes) plus every `/v1/*` route's auth-enforcement in a single test that iterates the router's registered routes, so a newly added route can't silently ship unauthenticated.
- [ ] **Rate limiting:** Often missing the per-client concurrent-jobs dimension, only implementing per-request rate — verify by asserting a burst of large jobs from one client is throttled even while under the requests/sec limit.
- [ ] **Magic-bytes validation:** Often missing an actual reject path — verify with a test that uploads a `.png`-named file containing non-image bytes and asserts a 422, not just that sniffing code runs.
- [ ] **S3 lifecycle TTL:** Often set only on `results/` and forgotten on `uploads/` (or vice versa) — verify both prefixes have lifecycle rules, and that `job_outputs.expires_at` (already in the schema per CONCERNS.md) is either actually written/enforced or explicitly deprecated in favor of the bucket-level rule.
- [ ] **Observability:** Often missing metrics for the *failure* paths specifically (webhook exhausted-retry count, reconciler recovery count, auth-rejection count) — verify dashboards/alerts exist for these, not just for happy-path throughput.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|-----------------|-------------------|
| Retrofit retry-safety shipped without full error classification, transient errors still marked terminal | LOW | Add missing error-taxonomy cases incrementally; no schema change needed if the state-machine re-entry path was built correctly the first time |
| Reconciler double-processes a job (Pitfall 7) in production | MEDIUM | Make the worker handler idempotent (check output-exists-in-storage short-circuit) as a fast mitigation; add asynq-unique-task-ID enforcement as the durable fix; audit `job_events` to find affected jobs |
| Webhook signing secret leaked | MEDIUM | Rotate `webhook_secret` per affected client (requires the dual-secret schema from Pitfall 6/2 analog); notify affected internal consumers to re-verify against the new secret |
| API key stored in plaintext discovered post-launch | HIGH | Requires a coordinated rotation of every client's key (forces the "grace period, dual key" mechanism to exist retroactively if it wasn't built in) plus a migration to hash the column and purge plaintext from backups/WAL |
| Retry storm from an unbounded webhook retry loop degrades a receiver | LOW | Circuit-break: pause deliveries to the affected `callback_url` (flag on the client or endpoint), drain `webhook_deliveries` backlog with backoff once the receiver recovers |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|-------------------|----------------|
| 1. Retry-safety retrofit without error classification | Retry-safety / reliability phase (before reconciler) | Test: inject one transient failure + one success, assert job reaches `done` via asynq redelivery, not a second manual attempt |
| 2. Webhook idempotency key missing | Webhook delivery phase | Payload/header includes a stable delivery ID unchanged across retries; documented in webhook contract |
| 3. Webhook signature verification missing/broken | Webhook delivery phase | HMAC over raw body + timestamp, per-client secret distinct from API key; sample verifier tested against a real delivery |
| 4. Webhook retry storm | Webhook delivery phase (logic) + Observability phase (alerting) | Backoff+cap implemented; metric for exhausted/in-flight deliveries exists |
| 5. Auth trusting network position | Auth phase (first hardening priority) | Route-iteration test asserts every non-public route requires a valid key; `client_id` scoping enforced on read, not just write |
| 6. API key storage/rotation | Auth phase | Hash-only storage verified; dual-key column present even if rotation tooling is deferred |
| 7. Reconciler double-processing | Reconciler phase (after retry-safety phase) | Test: reconciler run against a legitimately slow `active` job with fresh heartbeat does not re-enqueue; asynq-unique-task-ID enforced |
| 8. Reconciler vs. asynq retry ownership conflict | Reconciler phase (explicit dependency on retry-safety phase's design) | Documented ownership split; reconciler checks asynq queue/archive state before acting |
| 9. Rate limiting on wrong identity/dimension | Rate limiting phase (after auth phase) | Limiter keyed on `client_id`; concurrent-jobs-per-client cap tested independent of request-rate cap |
| 10. Magic-bytes validation without reject path | Magic-bytes validation phase | Test: mismatched magic bytes vs. declared format returns 422; sniff reads bounded header only |

## Sources

- [Task Retry — hibiken/asynq Wiki](https://github.com/hibiken/asynq/wiki/Task-Retry) — `SkipRetry` and `IsFailure` semantics (MEDIUM-HIGH confidence, official project wiki)
- [asynq package docs — pkg.go.dev](https://pkg.go.dev/github.com/hibiken/asynq) — API reference for retry/unique-task configuration
- [Webhook Architecture: Retries, Idempotency, and the ... — birjob.com](https://www.birjob.com/blog/webhook-architecture)
- [Common Mistakes with Outbound Webhooks (And How to Avoid Them) — Hookdeck](https://hookdeck.com/outpost/guides/common-outbound-webhook-mistakes)
- [Building Reliable Webhook Delivery: Retries, Signatures, and Failure Handling — DEV Community](https://dev.to/young_gao/building-reliable-webhook-delivery-retries-signatures-and-failure-handling-40ff)
- [Handling Payment Webhooks Reliably (Idempotency, Retries, Validation) — Medium](https://medium.com/@sohail_saifii/handling-payment-webhooks-reliably-idempotency-retries-validation-69b762720bf5)
- [Best practice for securely validating GitHub webhook payloads — GitHub Community Discussion #182735](https://github.com/orgs/community/discussions/182735)
- [Webhook HMAC Verification Checklist Guide — ClientOps Devtools](https://clientops.dev/guides/webhook-hmac-verification-checklist/)
- [API Key Management Best Practices for Secure Services — OneUptime](https://oneuptime.com/blog/post/2026-02-20-api-key-management-best-practices/view)
- [Credential Rotation Guide — OLOID](https://www.oloid.com/blog/credential-rotation)
- [Transactional Outbox Pattern — Microservices.io](https://microservices.io/patterns/data/transactional-outbox.html)
- [Transactional outbox pattern — AWS Prescriptive Guidance](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html)
- `.planning/codebase/CONCERNS.md` — first-party source for existing, verified bugs (single-attempt processing, no reconciler, no auth, no webhook delivery, presign hostname mismatch) — HIGH confidence, direct code review
- `.planning/PROJECT.md` — milestone scope and constraints (auth priority, internal-clients-still-need-auth constraint)

---
*Pitfalls research for: internal async job-processing service hardening (auth, webhooks, reconciliation, retry-safety)*
*Researched: 2026-07-02*
