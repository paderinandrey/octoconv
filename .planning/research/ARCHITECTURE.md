# Architecture Research

**Domain:** Production-hardening an internal async file-conversion service (Go / chi / asynq / Postgres / MinIO)
**Researched:** 2026-07-02
**Confidence:** HIGH (component placement, chi middleware ordering, magic-byte libraries — verified against official docs/READMEs) / MEDIUM (reconciler and webhook-delivery patterns — verified against multiple independent sources, no single canonical spec for this exact stack)

This research answers four architecture questions for the current hardening milestone, on top of the *existing* system documented in `.planning/codebase/ARCHITECTURE.md`. It does not propose a new stack — only where new components attach to the existing API/worker/Postgres/Redis/MinIO topology.

## Standard Architecture

### System Overview

```text
┌───────────────────────────────────────────────────────────────────────────┐
│                         HTTP API (cmd/api)                                 │
│  chi router                                                                │
│                                                                             │
│  Global:      RequestID → RealIP → Logger → Recoverer → Timeout            │
│  Per-route:   [coarse IP rate-limit] → Auth (API key) → [per-client        │
│               rate-limit] → magic-byte sniff → handler                     │
│                                                                             │
│  internal/api/*.go (existing) + internal/auth/*.go (new) +                 │
│  internal/ratelimit/*.go (new)                                             │
└───────┬───────────────────┬───────────────────┬───────────────────────────┘
        │                   │                   │
        ▼                   ▼                   ▼
┌───────────────┐  ┌────────────────┐  ┌────────────────────┐
│ storage.Client │  │   jobs.Repo     │  │   queue.Client      │
│ (S3/MinIO)     │  │  (Postgres)     │  │  (asynq/Redis)      │
│ existing       │  │  existing +     │  │  existing +          │
│                │  │  client_id,     │  │  new task types:     │
│                │  │  webhook_       │  │  reconcile:sweep,    │
│                │  │  deliveries     │  │  webhook:deliver      │
└───────┬────────┘  └────────┬────────┘  └──────────┬──────────┘
        │                    │                       │
        │                    │                       ▼
        │                    │           ┌──────────────────────────────┐
        │                    │           │  Worker (cmd/worker)           │
        │                    │           │  asynq.ServeMux, multi-queue   │
        │                    │           │  - image (existing conversion) │
        │                    │           │  - system (reconcile:sweep)    │
        │                    │           │  - webhooks (webhook:deliver)  │
        │                    │           └──────┬──────────┬─────────────┘
        │                    │                  │          │
        │                    │                  ▼          ▼
        │                    │      ┌────────────────┐  ┌──────────────────┐
        │                    │      │ convert.Registry│  │ webhook.Delivery  │
        │                    │      │ (existing)       │  │Client (new: HTTP  │
        │                    │      │                  │  │POST + backoff)    │
        │                    │      └────────────────┘  └──────────┬─────────┘
        ▼                    ▼                                     ▼
┌───────────────────────────────────────────────────────────────────────────┐
│  Postgres (system of record: jobs, clients, webhook_deliveries)            │
│  Redis (asynq broker: image / system / webhooks queues)                    │
│  S3/MinIO (uploads/, results/ — with bucket lifecycle TTL rules)           │
└───────────────────────────────────────────────────────────────────────────┘
```

Nothing here replaces an existing component. Auth, rate limiting, and magic-byte sniffing are new *middleware/handler-layer* additions to `internal/api`. The reconciler and webhook delivery are new *asynq task types* consumed by the existing worker process (or an additional worker process later, if queue contention demands it) — no new infrastructure (no separate cron daemon, no message broker beyond the Redis already in use).

### Component Responsibilities

| Component | Responsibility | Typical Implementation |
|-----------|----------------|-------------------------|
| Auth middleware | Resolve `Authorization`/API-key header → `clients` row, reject unknown/revoked keys, attach `client.ID` to request context | chi middleware, hits `internal/jobs`-adjacent `clients` repo, hashed key lookup (never store/compare plaintext keys) |
| Rate limit middleware | Reject requests over a per-client (or coarse per-IP) threshold before they reach handlers | `go-chi/httprate` (in-process) now; `go-chi/httprate-redis` when the API runs >1 replica |
| Magic-byte validator | Sniff true file type from upload bytes, reject mismatches with the declared/extension-derived format | `gabriel-vasile/mimetype`, invoked in the handler before any S3 or Postgres write |
| Reconciler / sweeper | Periodically scan for jobs stuck in `queued` (enqueue failed) or `active` (worker died) past a threshold and recover them | An asynq **periodic task** (`asynq.Scheduler` registering a cron-scheduled `reconcile:sweep` task), processed by a normal handler in the existing worker process — not a separate cron binary |
| Webhook delivery worker | Deliver `POST {callback_url}` with job result payload, retry with backoff, record every attempt | New asynq task type `webhook:deliver` on its own queue, own handler package `internal/webhook/` |
| Storage lifecycle TTL | Auto-expire `uploads/` and `results/` objects | MinIO/S3 bucket lifecycle rule (infrastructure config, not application code) — zero new Go code |
| Observability | Expose queue depth, job outcomes, HTTP metrics | `asynqmon` (existing asynq queue introspection UI) + `prometheus/client_golang` metrics endpoint, wraps existing components, doesn't own state |

## Recommended Project Structure

```
internal/
├── api/                  # existing — routes, handlers, Server struct
│   ├── routes.go         # add middleware chain here (auth, ratelimit groups)
│   └── handlers.go       # add magic-byte sniff call inside handleCreateJob
├── auth/                 # new — API key resolution + chi middleware
│   ├── auth.go           # ResolveClient(ctx, apiKey) (*Client, error)
│   └── middleware.go     # chi.Middleware wrapping ResolveClient, context injection
├── ratelimit/            # new — thin wrapper choosing in-memory vs Redis-backed limiter
│   └── ratelimit.go
├── clients/               # new (or extend internal/jobs) — Postgres CRUD for `clients` table
│   └── repo.go
├── jobs/                  # existing — extend Create() to accept client_id, callback_url
│   └── repo.go
├── contenttype/           # new — magic-byte detection + declared-format comparison
│   └── sniff.go
├── reconcile/             # new — sweep queries + asynq periodic task registration
│   ├── sweep.go           # SQL: find stuck queued/active rows
│   └── task.go            # asynq handler for `reconcile:sweep`
├── webhook/                # new — delivery client, retry policy, webhook_deliveries repo
│   ├── deliver.go          # HTTP POST + signature header, timeout
│   ├── repo.go              # webhook_deliveries CRUD (status, attempt, next_attempt_at)
│   └── task.go               # asynq handler for `webhook:deliver`
├── queue/                  # existing — add task type constants + queue names for the two new task types
├── worker/                  # existing — register reconcile + webhook handlers on the ServeMux alongside HandleImageConvert
├── storage/                  # existing — unchanged (lifecycle TTL is bucket config, not app code)
├── convert/                   # existing — unchanged
└── db/                          # existing — new migration(s) for webhook_deliveries columns if the current schema needs adjustment (attempt count, next_attempt_at)
```

### Structure Rationale

- **`internal/auth/` and `internal/ratelimit/` as separate packages:** keeps `internal/api` focused on HTTP wiring; both are reusable middleware that could plausibly be tested in isolation without spinning up the full API server.
- **`internal/reconcile/` and `internal/webhook/` live next to `internal/worker/`, not inside it:** they are new *asynq consumers*, structurally identical in shape to the existing `worker.Handler` — same pattern (asynq `ServeMux` registration in `cmd/worker/main.go`), just a different task type and queue. This mirrors the codebase's existing "engine-class queue routing" convention (`internal/convert` note in `.planning/codebase/ARCHITECTURE.md`) rather than inventing a new composition style.
- **No new process/binary:** both new background components register on the *same* `asynq.Server`/`ServeMux` used by `cmd/worker/main.go`, just on additional queues (`system`, `webhooks`) with their own weight/concurrency. This is the lowest-risk way to add background work without provisioning new deployment units, consistent with the milestone constraint to not rewrite the core stack.

## Architectural Patterns

### Pattern 1: Reconciler/sweeper as an asynq periodic task (not a transactional outbox)

**What:** A scheduled task (`reconcile:sweep`, e.g. every 30–60s) that queries Postgres for jobs stuck in `queued` beyond a threshold with no corresponding active/pending asynq task, and re-enqueues them; and separately for jobs stuck in `active` beyond `ENGINE_TIMEOUT` × N (worker crashed mid-processing) and either retries or marks them `failed`.

**When to use:** This is the right fit here because the current write path (`jobs.Repo.Create` commits the row, *then* `EnqueueImageConvert` is called — see `.planning/codebase/ARCHITECTURE.md` "Postgres-first double write") already gives you the crash-safety half of an outbox pattern for free: a crash before enqueue always leaves an inspectable row, never an orphaned queue message. The only gap is *recovery* — nothing currently re-enqueues that stranded row. A sweeper closes that gap additively, without touching the write path at all.

**Trade-offs:**
- A *full* transactional outbox (write an `outbox` event row in the same transaction as the job insert; a separate relay process polls the outbox and publishes to Redis/asynq) is the textbook answer to the dual-write problem, but it is strictly more machinery here: a new table, a new relay loop, and — critically — it duplicates asynq's own queue/broker role, since asynq *is* the message bus already. Adopting it now would mean rewriting the enqueue path (`internal/api/handlers.go:105-109`) to write-then-relay instead of write-then-enqueue, which is exactly the kind of core-flow change the milestone wants to avoid.
- The sweeper pattern is *idempotent by construction* if you enqueue with `asynq.TaskID(jobID)`: re-running the sweep against an already-enqueued job returns `asynq.ErrTaskIDConflict` (a safe no-op) rather than creating a duplicate task. This gives outbox-like exactly-once-enqueue semantics without an outbox table. (Verified against asynq's own unique-task/TaskID documentation.)
- Latency: a sweeper has a detection lag equal to its poll interval (worst case ~60s before a stranded job is recovered), whereas a true outbox relay can be near-real-time. For an internal service where jobs already take seconds-to-minutes to convert, this lag is immaterial.
- The sweeper must also cover the *second* failure mode not fixed by outbox patterns at all: a worker that crashes after `MarkActive` but before `MarkDone`/`MarkFailed`. Outbox patterns only address the producer-side dual write; recovering stuck `active` rows requires the same sweep query extended to check `active` jobs against a staleness threshold (e.g., `updated_at < now() - ENGINE_TIMEOUT * 3`) and either flip them back to `queued` for retry or terminally fail them. This directly closes the "single-attempt processing" gap flagged in `.planning/codebase/CONCERNS.md`.

**Example (sweep query shape):**
```sql
-- Case A: enqueue never happened (row inspectable, no queue message)
SELECT id FROM jobs
WHERE status = 'queued' AND created_at < now() - interval '2 minutes'
FOR UPDATE SKIP LOCKED;

-- Case B: worker died mid-processing (row locked past a staleness window)
SELECT id FROM jobs
WHERE status = 'active' AND updated_at < now() - interval '10 minutes'
FOR UPDATE SKIP LOCKED;
```
```go
// Re-enqueue is safe to call repeatedly: TaskID(jobID) makes it a no-op
// if a task for this job is already pending/active in Redis.
_, err := client.EnqueueContext(ctx, task, asynq.TaskID(jobID.String()), asynq.Queue("image"))
if errors.Is(err, asynq.ErrTaskIDConflict) {
    // already in flight — nothing to do
}
```

### Pattern 2: Webhook delivery as a dedicated asynq task type/queue, not inline

**What:** When a job transitions to `done`/`failed` and has a non-null `callback_url`, the worker writes a `webhook_deliveries` row (status `pending`) in the *same transaction* as the `MarkDone`/`MarkFailed` call, then — after commit — enqueues a `webhook:deliver` task (payload: `delivery_id`) onto a separate `webhooks` queue. A dedicated handler in `internal/webhook/` performs the actual `POST`, updates the delivery row (status, attempt count, last HTTP status/error, `next_attempt_at`), and lets asynq's retry mechanism (or the sweeper, for deliveries stuck `pending` past a threshold) drive re-attempts.

**When to use:** Always, for this milestone. Inline delivery (calling the webhook synchronously inside `HandleImageConvert` right after `MarkDone`) is the anti-pattern to avoid: a slow or unreachable client endpoint would block the conversion worker goroutine, directly reducing `WORKER_CONCURRENCY` for actual conversion jobs — the exact "noisy neighbor" risk already flagged in `.planning/codebase/CONCERNS.md` for storage/DB calls without timeouts.

**Trade-offs:**
- Separate queue (`webhooks`) means a flaky third-party endpoint's retries never starve image-conversion throughput. asynq supports multiple named queues on one `asynq.Server` with independent weights/concurrency, so this can run in the *same worker process* initially — no new deployment unit required — and be split into a dedicated worker binary later purely by moving the queue registration, with no handler code changes.
- This mirrors the *same* Postgres-first-then-enqueue pattern already established for job creation (write durable state, then hand off to the queue) — consistent with the codebase's existing convention rather than introducing a second pattern.
- Retry/backoff: use asynq's built-in retry with a custom `RetryDelayFunc` (exponential backoff, e.g. `base * 2^n` capped at ~1h, with jitter) rather than hand-rolling delay scheduling — asynq already persists retry state in Redis and asynqmon (already planned for this milestone) will show pending/retry counts for the `webhooks` queue for free. Cap total attempts (e.g., 8–10 over ~24h, matching common webhook-provider conventions like Stripe/Svix) and terminal-fail into the existing `webhook_deliveries` row rather than a separate dead-letter table, since the schema is already the audit log.
- Record every attempt (not just the final outcome) in `webhook_deliveries` — this is exactly what the pre-existing but unused table is for; it turns "did the webhook fire?" from a log-grep question into a queryable one, which matters for an internal-service SLA conversation.
- Idempotency for the receiving side: include a delivery/event ID header so downstream consumers can dedupe, since asynq retries plus the sweep's stale-`pending` recovery both mean at-least-once delivery, never exactly-once.

**Example (task registration, mirrors existing `worker.Handler` pattern):**
```go
mux := asynq.NewServeMux()
mux.HandleFunc(queue.TypeImageConvert, workerHandler.HandleImageConvert)
mux.HandleFunc(queue.TypeReconcileSweep, reconcileHandler.HandleSweep)
mux.HandleFunc(queue.TypeWebhookDeliver, webhookHandler.HandleDeliver)

srv := asynq.NewServer(redisOpt, asynq.Config{
    Queues: map[string]int{
        "image":    6, // conversion — highest weight
        "webhooks": 3,
        "system":   1, // reconcile sweeps — low volume, low priority
    },
    RetryDelayFunc: exponentialBackoffWithJitter,
})
```

### Pattern 3: Layered chi middleware — coarse limits and identity resolution before business logic

**What:** Global middleware (`RequestID`, `RealIP`, `Logger`, `Recoverer`, request `Timeout`) applies to every route via `r.Use(...)`, exactly as today. New concerns are added as an ordered chain applied per protected route group: **(1)** a coarse, cheap, unauthenticated IP-based rate limit as a first line of defense against floods before any DB lookup happens, **(2)** auth middleware that resolves the API key against `clients`, rejects on failure (401), and injects the resolved `client.ID`/`client.Name` into request context, **(3)** a second, per-client rate limit keyed off the now-known `client.ID` (tighter, business-meaningful limits — e.g., "100 jobs/min for this client" — rather than IP, since internal services may share egress IPs/NAT).

**When to use:** Any endpoint that creates cost (job creation) or exposes another client's data (job status/download) needs both auth and per-client rate limiting; `/healthz` and `/metrics` stay outside this chain entirely (unauthenticated, unlimited, used by orchestrators/scrapers).

**Trade-offs:**
- Rate limiting *before* auth (IP-based) protects against pure floods cheaply (no DB hit per request) but can't be fair across clients sharing an IP; rate limiting *after* auth (client-based) is fair and meaningful but costs one DB lookup per request regardless of whether the request is later rejected for being over-limit — acceptable for an internal, moderate-traffic service, and cacheable (short-TTL in-memory cache of `apiKey → client` keyed by the key hash) if it ever becomes measurable overhead.
- If the API ever runs more than one replica, an in-memory (`golang.org/x/time/rate` or default `go-chi/httprate`) limiter under-counts because each replica has its own state — switch the per-client limiter's `LimitCounter` implementation to `go-chi/httprate-redis` (Redis is already a hard dependency via asynq, so this adds no new infrastructure) rather than accepting per-replica-only limits silently.
- Auth failure and rate-limit rejection should return *before* any multipart parsing/storage/DB write begins — this preserves the existing "validate early, write late" principle already used for format-pair validation (`.planning/codebase/ARCHITECTURE.md`: "Format-pair validation happens once, early ... before any storage write").

**Example:**
```go
r := chi.NewRouter()
r.Use(middleware.RequestID, middleware.RealIP, middleware.Logger, middleware.Recoverer, middleware.Timeout(30*time.Second))

r.Get("/healthz", h.handleHealth) // no auth, no rate limit

r.Group(func(r chi.Router) {
    r.Use(httprate.LimitByIP(60, time.Minute))      // coarse, pre-auth flood guard
    r.Use(auth.Middleware(clientRepo))               // resolves client, 401 on failure
    r.Use(ratelimit.PerClient(clientLimiter))         // fair, business-meaningful limit
    r.Post("/v1/jobs", h.handleCreateJob)
    r.Get("/v1/jobs/{id}", h.handleGetJob)
})
```

### Pattern 4: Sniff-then-stream — magic-byte validation before any S3 or Postgres write

**What:** Inside `handleCreateJob`, after the existing size cap (`http.MaxBytesReader`) and multipart parse but *before* `storage.Client.Upload` or `jobs.Repo.Create` are called, read a small prefix of the uploaded file (a few KB is enough for `gabriel-vasile/mimetype`'s deepest signatures), run detection, and compare the sniffed MIME/format against the format implied by the filename extension / declared `Content-Type` that the existing code already normalizes. On mismatch, reject with `422` — the same status code and validation choke-point already used for unsupported format pairs — without ever touching storage.

**When to use:** Always, for every upload. This closes the exact gap flagged in `.planning/codebase/CONCERNS.md` ("Uploaded content-type and format are trusted from client-supplied data").

**Trade-offs:**
- **Before S3 write is strictly correct here, not just a preference.** Validating after upload (sniff, then delete-if-invalid) means malformed/spoofed content briefly lands in `uploads/`, costs a wasted PUT + DELETE round trip to MinIO, and complicates the error path (now every failure mode needs a cleanup step) — none of which buys anything, since the whole point is to *avoid* processing an invalid file. This also matches the codebase's established principle of validating before any storage write.
- Go's stdlib `http.DetectContentType` (mimesniff-spec based) is *not sufficient alone* here: it recognizes `image/webp` but has no signature for HEIC/HEIF (ISO-BMFF `ftyp` box), which this service explicitly supports as a source/target format. Use `gabriel-vasile/mimetype` instead — its signature table includes `image/heic`, `image/heif`, and their sequence variants, plus PNG/JPEG/TIFF/WebP, covering every format currently in `imageFormats`. (Verified: gabriel-vasile/mimetype's magic-signature package explicitly implements Heic/HeicSequence/Heif/HeifSequence matchers.)
- Streaming cost: don't buffer the entire file into memory to sniff it. Wrap the multipart file in a small peekable reader (read first N bytes into a buffer, sniff, then reconstruct the full stream via `io.MultiReader(bytes.NewReader(buf), file)`) so the sniff step adds a fixed, small, constant-time cost regardless of file size, and the subsequent upload to storage still streams rather than fully buffering — consistent with how `MAX_UPLOAD_BYTES`/`http.MaxBytesReader` already bound worst-case memory without requiring full buffering.
- This is a *content* check, not a *safety* check — it stops "renamed/spoofed extension" mistakes and casual abuse, not a determined attacker crafting a byte-valid-but-malicious file of the declared type. It's complementary to, not a replacement for, the existing hardened process execution (`Setpgid` + timeout + kill) around the actual `vips` invocation, and does not by itself address the separately-flagged decompression-bomb risk.

**Example:**
```go
func sniffAndValidate(file multipart.File, declaredFormat string) (multipart.File, error) {
    buf := make([]byte, 3072) // mimetype's deepest signatures need up to a few KB
    n, _ := io.ReadFull(file, buf)
    buf = buf[:n]

    mt := mimetype.Detect(buf)
    if !formatMatches(mt, declaredFormat) { // e.g. mt.Is("image/heic") for declared "heic"
        return nil, fmt.Errorf("%w: declared %q, detected %q", ErrContentMismatch, declaredFormat, mt.String())
    }
    // Reassemble the stream for the subsequent storage upload — no full buffering.
    return struct {
        io.Reader
        multipart.File
    }{io.MultiReader(bytes.NewReader(buf), file), file}, nil
}
```

## Data Flow

### Request Flow (extended create-job path)

```
Client POST /v1/jobs (multipart, Authorization: ApiKey ...)
    ↓
[coarse IP rate limit] → [auth: resolve API key → client] → [per-client rate limit]
    ↓
handleCreateJob: MaxBytesReader → parse multipart → normalize source/target
    ↓
convert.Default.Supports(pair)?  ── no ──▶ 422 (existing check, unchanged)
    ↓ yes
sniffAndValidate(file, declaredFormat) ── mismatch ──▶ 422 (new check)
    ↓ match
storage.Upload(uploads/{job_id}/...)          ← still after all validation, unchanged position
    ↓
jobs.Repo.Create (job + input + queued event, txn) — now also writes client_id, callback_url
    ↓
queue.EnqueueImageConvert(job_id, TaskID=job_id)  ← idempotent enqueue key
    ↓
202 Accepted {job_id, status: queued}
```

### Job Completion → Webhook Flow (new)

```
Worker: HandleImageConvert → convert → upload result → jobs.Repo.MarkDone (txn)
    ↓ (same txn, if job.callback_url != nil)
webhook.Repo.CreateDelivery(job_id, status=pending)  ← durable record before enqueue
    ↓ (after commit)
queue.EnqueueWebhookDeliver(delivery_id, TaskID=delivery_id, queue="webhooks")
    ↓
Worker (webhooks queue): HandleDeliver → HTTP POST callback_url →
    success: mark delivery "delivered"
    failure: mark "failed", return error → asynq retry with backoff
    exhausted retries: mark "dead", visible in webhook_deliveries for manual triage
```

### Reconciler Flow (new, periodic)

```
asynq.Scheduler: every N seconds → enqueue reconcile:sweep (unique per tick)
    ↓
Worker (system queue): HandleSweep
    ↓
SELECT jobs WHERE status='queued' AND created_at < now()-threshold FOR UPDATE SKIP LOCKED
    → re-enqueue each (TaskID=job_id, safe no-op if already in flight)
SELECT jobs WHERE status='active' AND updated_at < now()-threshold FOR UPDATE SKIP LOCKED
    → revert to queued (bounded retry count) or MarkFailed if retry budget exhausted
SELECT webhook_deliveries WHERE status='pending' AND updated_at < now()-threshold
    → re-enqueue delivery task (same idempotent-enqueue pattern)
```

**State Management:**
- All new durable state continues to live in Postgres (`webhook_deliveries` attempt tracking, `clients` for auth) — no new source of truth is introduced. Redis/asynq remains transient queue state only, exactly as today.
- The reconciler and webhook-delivery flows both re-use the *same* "Postgres-first, then idempotent enqueue" shape already established for job creation — this is the one data-flow convention worth keeping consistent across the whole system rather than inventing a second pattern per new feature.

## Scaling Considerations

| Scale | Architecture Adjustments |
|-------|---------------------------|
| Current (single API replica, single worker replica) | In-memory `httprate` limiter is fine; reconciler/webhook handlers share the existing worker process on separate asynq queues; no changes needed |
| Multiple API replicas (horizontal scale for availability) | Switch rate limiter to `go-chi/httprate-redis` (shared counter state) — otherwise each replica enforces its own independent limit, silently multiplying the effective ceiling |
| Webhook endpoints become slow/unreliable at volume | Split `webhooks` queue onto a dedicated worker process/replica so a flood of slow client endpoints can't reduce `image` queue concurrency; asynq's per-queue weighting already supports this without a code change, only a deployment change |
| Reconciler sweep query becomes expensive at high job volume | Ensure a composite index on `(status, updated_at)` (or `(status, created_at)` for the queued case) on `jobs`, matching the existing guarded-transition row-locking pattern; `SKIP LOCKED` keeps concurrent sweep runs (if ever parallelized) from blocking each other |

### Scaling Priorities

1. **First bottleneck:** Per-client rate limiting becomes inconsistent the moment there's more than one API replica — this is the first thing that will silently misbehave, not "break loudly," so plan the Redis-backed limiter switch as soon as multi-replica deployment is on the table, not after observing the bug.
2. **Second bottleneck:** A misbehaving webhook endpoint (slow/hanging) competing for worker concurrency with the `image` queue — mitigated from day one by using a separate `webhooks` queue with its own weight, so this is a non-issue if Pattern 2 is followed, but worth calling out as the reason that queue separation exists.

## Anti-Patterns

### Anti-Pattern 1: Building a full transactional outbox table for this milestone

**What people do:** See "dual write problem" articles and reach for the textbook outbox pattern (new `outbox` table + relay process) as the default fix.
**Why it's wrong here:** The system already gets crash-safety from its existing Postgres-first-then-enqueue ordering; the only missing piece is *recovery*, not *atomicity*. Building a full outbox duplicates asynq's role as the message bus and requires rewriting the job-creation write path — the opposite of the milestone's "no rewrite" constraint.
**Do this instead:** A periodic reconciler/sweeper task (Pattern 1), using asynq's own `TaskID` uniqueness for idempotent re-enqueue.

### Anti-Pattern 2: Delivering webhooks synchronously inside the conversion handler

**What people do:** Call the client's callback URL directly from `HandleImageConvert` right after `MarkDone`, since "it's just one HTTP call."
**Why it's wrong:** A slow or hanging third-party endpoint blocks a conversion-worker goroutine for the duration of the HTTP call (or its timeout), directly reducing effective `WORKER_CONCURRENCY` for unrelated jobs — the same class of problem already flagged for unbounded storage/DB calls in `CONCERNS.md`.
**Do this instead:** Enqueue a dedicated `webhook:deliver` task on a separate queue (Pattern 2), decoupling delivery latency/failure from conversion throughput entirely.

### Anti-Pattern 3: Rate limiting solely by client-resolved identity with no pre-auth guard

**What people do:** Put rate limiting entirely *after* auth, reasoning "we need to know who they are to limit them fairly."
**Why it's wrong:** Every request — including floods of requests with invalid/garbage API keys — still pays the cost of a DB lookup in the auth middleware before being rejected, giving an attacker a way to load-test your database for free.
**Do this instead:** A cheap, coarse, pre-auth IP-based limit (Pattern 3) as the very first gate, with the fairer per-client limit layered on afterward.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|----------------------|-------|
| Client webhook endpoints (internal services' callback URLs) | Outbound `HTTP POST` from `internal/webhook/`, asynq-scheduled retries | Treat as untrusted network boundary even though clients are internal — apply a request timeout, cap redirects, and consider basic SSRF guards (reject callback URLs resolving to internal/link-local ranges) since `callback_url` is client-supplied data |
| MinIO/S3 lifecycle engine | Bucket lifecycle rule (server-side, no app polling) | Configure via `mc ilm` / Terraform / bucket policy, not application code — keep the TTL value consistent with the webhook retry window and the client's polling/download expectations so results don't expire before they're fetched |
| asynqmon | Reads asynq's Redis-stored queue state directly | No application code changes needed beyond running the existing asynqmon binary/container against the same `REDIS_ADDR`; new queues (`system`, `webhooks`) appear automatically |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|----------------|-------|
| `internal/api` ↔ `internal/auth` | Direct call (middleware wraps handlers, injects `client.ID` into `context.Context`) | Follow the existing narrow-interface convention (`api.Repo`, `api.Storage`, `api.Enqueuer`) — define a small `auth.ClientResolver` interface consumed by `internal/api`, not a concrete struct dependency |
| `internal/worker` ↔ `internal/webhook` | Same-process, asynq `ServeMux` registration (like `HandleImageConvert`) | No direct Go-level dependency between `internal/convert` and `internal/webhook` — they're independent task handlers sharing only the worker process and `jobs`/`webhook_deliveries` repos |
| `internal/worker` ↔ `internal/reconcile` | Same-process, asynq `ServeMux` + `asynq.Scheduler` registration | Scheduler registration lives in `cmd/worker/main.go` alongside existing `asynq.Server` setup, consistent with the existing single-entry-point wiring convention |
| API and worker processes | Postgres (`clients`, `jobs`, `webhook_deliveries`) + Redis (asynq) only, never direct RPC | Unchanged from existing architecture — new components must not introduce a new communication channel between the two processes |

## Sources

- [Unique recurring task · hibiken/asynq Discussion #376](https://github.com/hibiken/asynq/discussions/376) — TaskID + Retention for idempotent periodic enqueue (HIGH — official repo discussion)
- [Unique Tasks · hibiken/asynq Wiki](https://github.com/hibiken/asynq/wiki/Unique-Tasks) — `ErrTaskIDConflict` semantics (HIGH — official docs)
- [hibiken/asynq server.go](https://github.com/hibiken/asynq/blob/master/server.go) — multi-queue weighting, `RetryDelayFunc` (HIGH — source of truth)
- [Solving the Dual Write Problem with the Transactional Outbox Pattern](https://yashodharanawaka.medium.com/solving-the-dual-write-problem-with-the-transactional-outbox-pattern-e74a79fed0ef) (MEDIUM — cross-checked against AWS Prescriptive Guidance)
- [Transactional outbox pattern - AWS Prescriptive Guidance](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html) (HIGH — official AWS docs)
- [Using FOR UPDATE SKIP LOCKED For Queue Workflows - Netdata](https://www.netdata.cloud/academy/update-skip-locked/) (MEDIUM)
- [The Queue Was a Table: Claim/Unclaim Workers with SKIP LOCKED, Stale Recovery, Retry Caps - DEV](https://dev.to/daniel_romitelli_44e77dc6/the-queue-was-a-table-how-i-built-claimunclaim-workers-with-skip-locked-stale-recovery-and-1ojm) (MEDIUM — matches heartbeat/staleness recovery pattern recommended above)
- [Webhook Retry Best Practices for Sending Webhooks - Hookdeck](https://hookdeck.com/outpost/guides/outbound-webhook-retry-best-practices) (MEDIUM)
- [Webhook Retry Best Practices - Svix](https://www.svix.com/resources/webhook-best-practices/retries/) (MEDIUM — industry-standard webhook provider's own guidance, jitter + capped exponential backoff)
- [go-chi/httprate](https://github.com/go-chi/httprate) and [go-chi/httprate-redis](https://github.com/go-chi/httprate-redis) (HIGH — official repos, confirmed `LimitCounter` pluggability for distributed rate limiting)
- [go-chi/chi middleware package docs](https://pkg.go.dev/github.com/go-chi/chi/v5/middleware) (HIGH — official docs)
- [gabriel-vasile/mimetype](https://github.com/gabriel-vasile/mimetype/) and its [magic package docs](https://pkg.go.dev/github.com/gabriel-vasile/mimetype/internal/magic) (HIGH — official repo, confirmed HEIC/HEIF/HeicSequence/HeifSequence signature support, which Go's stdlib `http.DetectContentType`/mimesniff spec does not cover)
- [On-the-Fly Content Type Sniffing and Validation in Go](https://destel.dev/blog/on-the-fly-content-type-detection-in-go) (MEDIUM — streaming-sniff-then-reassemble pattern)
- Existing codebase analysis: `.planning/codebase/ARCHITECTURE.md`, `.planning/codebase/CONCERNS.md` (HIGH — ground truth for current system)

---
*Architecture research for: production-hardening an internal async file-conversion service (Go)*
*Researched: 2026-07-02*
