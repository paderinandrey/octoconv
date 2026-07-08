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

---

# Architecture Research — Addendum: v1.2 Document Engine Class (LibreOffice)

**Domain:** Adding a `document` engine class (LibreOffice, `soffice --headless`) to OctoConv's existing `Converter`/`Registry` conversion pipeline
**Researched:** 2026-07-09
**Confidence:** HIGH (integration points — grounded directly in current repo code) / MEDIUM (LibreOffice-specific operational behavior — WebSearch-verified against multiple independent community/bug-tracker sources, no official LibreOffice docs consulted directly) / LOW (exact Docker image size delta — directionally consistent across sources but no hard number verified)

This addendum answers the document-engine-class integration questions for the v1.2 milestone, on top of the system documented above (v1.0/v1.1 hardening) and the ground-truth code in `internal/convert/`, `internal/queue/`, `internal/worker/`, `cmd/worker/main.go`, and `internal/api/handlers.go`. The locked decision (`.planning/PROJECT.md`) is to extend the existing `Converter`/`Registry` abstraction — **not** introduce a Handler/Capability/Input/Output contract, and **not** a new binary.

## Standard Architecture

### System Overview (delta)

```
┌──────────────────────────────────────────────────────────────────────┐
│ API (cmd/api) — internal/api/handlers.go: handleCreateJob            │
│  Sniff → [detected==source?] → Supports(pair)? → dimension-check     │
│  (image-only, MUST become conditional) → callback_url → S3 upload →  │
│  jobs.Create (Postgres, status=queued) → queue.EnqueueImageConvert   │
│  or a NEW queue.EnqueueDocumentConvert (routed by resolved engine)    │
└───────────────────────────────┬────────────────────────────────────┘
                                 │ asynq (Redis) — engine-class queues
                 ┌───────────────┴────────────────┐
                 │                                 │
        queue "image"  (existing)         queue "document"  (NEW)
        TypeImageConvert                   TypeDocumentConvert (NEW)
                 │                                 │
                 └───────────────┬────────────────┘
                                 │ SAME asynq.Server, SAME cmd/worker binary
┌──────────────────────────────────────────────────────────────────────┐
│ Worker (cmd/worker) — internal/worker/worker.go: Handler               │
│  HandleImageConvert(ctx,t)    ── timeout=ENGINE_TIMEOUT                │
│  HandleDocumentConvert(ctx,t) (NEW) ── timeout=DOCUMENT_ENGINE_TIMEOUT │
│  both call a shared process(ctx, job, timeout) [signature widened]    │
└───────────────────────────────┬────────────────────────────────────┘
                                 │
┌──────────────────────────────────────────────────────────────────────┐
│ internal/convert — Converter interface + Registry (convert.go)        │
│  convert.Default.Register(LibvipsConverter{})        [existing]       │
│  convert.Default.Register(LibreOfficeConverter{})    [NEW, converters.go] │
│  LibreOfficeConverter.Convert() shells out via runCommand (exec.go,   │
│  UNCHANGED) to `soffice --headless --convert-to pdf`                  │
└──────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | File (existing / NEW) |
|-----------|----------------|------------------------|
| `LibreOfficeConverter` | Implements `Converter` for `{docx,xlsx,pptx,odt,ods,odp} → pdf`; shells out to `soffice --headless`; owns per-job profile isolation and soffice's "exit 0 but no output" quirk | **NEW** `internal/convert/libreoffice.go` |
| Registry wiring | One-line registration of the new converter | **MODIFY** `internal/convert/converters.go` (`init()`) |
| ZIP-container format sniffing | Disambiguate OOXML (docx/xlsx/pptx) vs ODF (odt/ods/odp) vs a bare `.zip` — all share `PK\x03\x04` | **NEW** logic inside/alongside `internal/convert/sniff.go` |
| Dimension-check scoping | Skip decompression-bomb pixel-dimension check for non-image formats | **MODIFY** `internal/convert/dimensions.go` (add a predicate) + `internal/api/handlers.go` (guard the call) |
| Document queue routing | `TypeDocumentConvert`/`QueueDocument` constants, `NewDocumentConvertTask`, retry schedule | **MODIFY** `internal/queue/queue.go` |
| Document producer | `EnqueueDocumentConvert` on the queue client | **MODIFY** `internal/queue/client.go` |
| Document task handler | `HandleDocumentConvert` bound to `TypeDocumentConvert`, using `DOCUMENT_ENGINE_TIMEOUT` | **MODIFY** `internal/worker/worker.go` |
| Worker wiring | Register second queue + second timeout, add `document` to `asynq.Config.Queues` | **MODIFY** `cmd/worker/main.go` |
| API request path | Route enqueue call by resolved engine class; conditionally skip dimension check | **MODIFY** `internal/api/handlers.go` |
| Docker image | Add LibreOffice headless components alongside `libvips-tools` | **MODIFY** `Dockerfile.worker` |

## Recommended Project Structure (delta only — no new top-level packages)

```
internal/convert/
├── convert.go          # UNCHANGED — Converter interface, Registry, NormalizeFormat
├── converters.go        # MODIFY — register LibreOfficeConverter{} in init()
├── libvips.go           # UNCHANGED
├── libreoffice.go        # NEW — LibreOfficeConverter (Pairs, Convert)
├── exec.go              # UNCHANGED — runCommand already generic (Setpgid+SIGKILL);
│                        #   its own doc comment already anticipates soffice.bin by name
├── sniff.go             # MODIFY — extend signature table / add zip-container disambiguation
├── dimensions.go        # MODIFY — add HasDimensionLimit(format) predicate; dimensionParsers stays image-only
internal/queue/
├── queue.go             # MODIFY — TypeDocumentConvert, QueueDocument, NewDocumentConvertTask,
│                        #          documentRetrySchedule, DocumentRetryDelay, DocumentUniqueTTL
├── client.go            # MODIFY — EnqueueDocumentConvert, documentMaxRetry, documentUniqueTTL fields
internal/worker/
├── worker.go            # MODIFY — Handler gains documentEngineTimeout field + HandleDocumentConvert;
│                        #          process() signature widened to accept an explicit timeout;
│                        #          isTerminal() gains LibreOffice-specific signature matching
cmd/worker/main.go        # MODIFY — wire DOCUMENT_ENGINE_TIMEOUT, register HandleDocumentConvert,
│                        #          add QueueDocument to asynq.Config.Queues
internal/api/handlers.go  # MODIFY — route enqueue by engine class; guard dimension-check by format
Dockerfile.worker         # MODIFY — add libreoffice-{writer,calc,impress} + fonts
```

### Structure Rationale

No new packages. Every addition is either a new file inside an existing package (`libreoffice.go` next to `libvips.go`, mirroring the file-per-engine convention already documented for `internal/convert`) or a targeted extension of an existing file that already has a per-format dispatch table (`sniff.go`'s `signatures` slice, `dimensions.go`'s `dimensionParsers` map, `queue.go`'s per-queue retry-schedule constants). This matches the locked decision: extend `Converter`/`Registry`, do not introduce a new abstraction layer.

## Architectural Patterns

### Pattern 1: Two-stage ZIP-container disambiguation (NEW — required for docx/xlsx/pptx/odt/ods/odp)

**What:** `docx`, `xlsx`, `pptx`, `odt`, `ods`, `odp`, and a bare `.zip` all share the identical 4-byte ZIP local-file-header magic (`PK\x03\x04`). The existing `sniff.go` signature table (`matchPNG`, `matchJPEG`, etc.) matches on a fixed 12-byte prefix and cannot disambiguate these — a naive "add PK\x03\x04 as a signature" would misclassify every OOXML/ODF/zip file as the same format.

Real-world tools (`file`/libmagic; confirmed via research) resolve this by reading past the ZIP magic into the **first local-file-entry's filename and, for OOXML, its (typically deflate-compressed) content**:
- **ODF (odt/ods/odp):** the ODF spec *mandates* the first ZIP entry be named `mimetype`, **stored uncompressed** (compression method `0`), whose raw payload is the literal string `application/vnd.oasis.opendocument.text` (odt) / `...spreadsheet` (ods) / `...presentation` (odp). This is a cheap, bounded-prefix check with no decompression needed — parse the local-file-header fields (compression method at header offset 8-9, filename length at offset 26-27), confirm filename == `"mimetype"` and method == `0`, then string-compare the immediately-following uncompressed bytes.
- **OOXML (docx/xlsx/pptx):** producers (Microsoft Office, LibreOffice, OpenXML SDK) conventionally emit `[Content_Types].xml` as the **first** ZIP entry (not spec-mandated, but a de facto universal convention libmagic itself relies on). Its content is usually deflate-compressed (method `8`); disambiguating docx/xlsx/pptx requires **inflating that first entry** (Go stdlib `compress/flate`, zero new dependency — consistent with the codebase's existing zero-new-deps discipline already established in `dimensions.go`) and substring-matching on `wordprocessingml.document` (docx) / `spreadsheetml.sheet` (xlsx) / `presentationml.presentation` (pptx).
- **Generic `.zip`:** first entry is neither `mimetype` (stored) nor `[Content_Types].xml` with a matching content-type string → falls through to "unrecognized" (existing D-02 422 path), exactly like today's unmatched-signature case.

**When to use:** Any container format whose outer magic bytes are ambiguous (this is the *only* ambiguous case in the current/planned format set — none of the 5 existing image signatures overlap).

**Trade-offs:** This is meaningfully more code than any existing entry in `sniff.go` (which are all pure byte-compares) — it is closer in shape to `dimensions.go`'s hand-rolled parsers (bounded window, fail-closed, own const for peek size) than to `sniff.go`'s current one-liners. Recommend a **new bounded peek constant** (e.g., `zipEntryPeekLen`, sized generously — several KB, since `[Content_Types].xml` uncompressed is typically 1-5 KB but can grow with many part overrides in large workbooks) separate from `sniffLen=12`, with the existing fail-closed philosophy: unrecognized content within the bounded window → reject, never fall back to trusting the extension.

### Pattern 2: Per-job LibreOffice profile isolation reuses the existing per-job workDir (NEW, but zero interface change)

**What:** LibreOffice headless (`soffice --headless`) is **not safe for concurrent invocations sharing the same user profile** — a second `soffice` process finds the first's `.lock` file and either attaches to the running instance or fails outright (confirmed via multiple independent sources: LibreOffice bug tracker, Gotenberg's own architecture notes, community writeups). The fix used everywhere in practice is `-env:UserInstallation=file:///<unique-dir>` per invocation, giving each conversion its own isolated profile.

Conveniently, `internal/worker/worker.go`'s `process()` **already** creates a fresh per-job temp directory (`workDir, err := os.MkdirTemp("", "octoconv-"+job.ID.String()+"-")`, `worker.go:270`) that both `inPath` and `outPath` live inside and that is `os.RemoveAll`'d on completion. `LibreOfficeConverter.Convert(ctx, inPath, outPath, opts)` can derive its `-env:UserInstallation` target from `filepath.Dir(outPath)` (i.e., the same per-job workDir) with **zero change to the `Converter` interface signature** — the isolation falls out of infrastructure that already exists for an unrelated reason (temp-file cleanup).

**When to use:** Any converter engine that maintains process-local mutable state across invocations (LibreOffice's UNO profile). Not needed for libvips, which is a pure single-shot CLI invocation with no shared state.

**Trade-offs:** This means the `document` asynq queue *can* safely run with concurrency > 1 in `cmd/worker/main.go`'s `asynq.Config.Queues` map (unlike the naive "must serialize to concurrency=1" answer commonly suggested for LibreOffice) — **because** profile isolation is per-job, not global. This is a meaningfully better outcome than the common "single LibreOffice instance behind a queue" pattern used by tools like Gotenberg (which serialize because they share one long-lived soffice instance across requests) — OctoConv's per-job short-lived `soffice --headless --convert-to` invocation model sidesteps that entirely, at the cost of LibreOffice's slower per-invocation startup (see Pattern 3/Scaling section below). As a secondary, free benefit: the worker container runs `USER nobody` (`Dockerfile.worker:16`), which typically has no writable `$HOME`; LibreOffice's default profile location is `$HOME/.config/libreoffice`, so explicitly passing `-env:UserInstallation` avoids relying on a writable home directory at all.

### Pattern 3: soffice's unreliable exit code — the Converter, not worker.go, must validate success

**What:** Unlike `vips` (whose behavior the codebase's own comment in `worker.go` confirms as "exit code is 1 for EVERY failure mode"), LibreOffice's `--convert-to` is documented (LibreOffice bug tracker, multiple confirmed reports across versions) to **return exit code 0 even when conversion fails** (e.g., "Error: source file could not be loaded" on stderr, no PDF produced) — so `runCommand`'s existing "non-zero exit or ctx timeout = error" contract (`exec.go`, unchanged) is **not sufficient** on its own for this engine.

**When to use:** `LibreOfficeConverter.Convert()` must, after `runCommand` returns nil, explicitly verify the expected output file exists in the outdir with non-zero size (recalling: `soffice --outdir <dir> --convert-to pdf <inPath>` writes `<inPath-basename>.pdf`, **not** the caller's arbitrary `outPath` — see Pattern 4) and return its own synthetic error (e.g., `fmt.Errorf("libreoffice: no output produced (source file could not be loaded)")`) when it does not. This keeps the "translate engine quirks into a Go error" responsibility inside the concrete `Converter` implementation, matching the existing pattern where `LibvipsConverter.Convert` wraps vips-specific behavior (`fmt.Errorf("libvips: %w", err)`) rather than leaking engine internals into `worker.go`.

**Trade-offs:** `internal/worker/worker.go`'s `isTerminal(err)` function (`worker.go:41`) currently has a hardcoded `terminalVipsSignatures` slice scanned against `err.Error()`. This needs an equivalent `terminalLibreOfficeSignatures` slice (e.g., `"source file could not be loaded"`, `"no export filter"`) — either as a second slice checked alongside the first, or (simpler, since these substrings are engine-specific and won't collide) appended into the same scan. Recommend keeping `isTerminal` engine-agnostic (scan both slices unconditionally) rather than branching on `job.Engine`, to avoid adding a new parameter to a function three call sites already share.

### Pattern 4: soffice's `--outdir` + basename-derived filename requires a rename step

**What:** `soffice --headless --convert-to pdf --outdir <dir> <inPath>` names its output `<basename(inPath) without ext>.pdf` inside `<dir>` — it has **no flag to specify an arbitrary output filename**. But `worker.go:process()` already constructs a specific `outPath := filepath.Join(workDir, "out."+job.TargetFormat)` (`worker.go:278`) and passes that exact path to `Convert(ctx, inPath, outPath, opts)`.

**When to use:** `LibreOfficeConverter.Convert` must run `soffice --headless --convert-to pdf --outdir <dir> <inPath>` with `dir := filepath.Dir(outPath)` (already the per-job `workDir`), then `os.Rename(filepath.Join(dir, stripExt(filepath.Base(inPath))+".pdf"), outPath)` before returning success. Purely internal to `libreoffice.go` — no change needed to `worker.go`'s call site or the `Converter` interface.

## Data Flow

### Request Flow (delta from existing `handleCreateJob`)

```
multipart upload
    ↓
convert.Sniff(file)                         [UNCHANGED signature, EXTENDED signature table/logic
    ↓                                         for zip-container disambiguation, Pattern 1]
detected == declared extension?  (422 if not)
    ↓
convert.Default.Supports(detected, target)   [UNCHANGED — Registry.Lookup already engine-agnostic;
    ↓                                          LibreOfficeConverter.Pairs() registers docx→pdf etc.]
convert.HasDimensionLimit(detected)?          [NEW predicate]
    ├─ true  (image format) → convert.Dimensions(...) as today
    └─ false (document format, or any future non-raster format) → SKIP, rest unchanged
    ↓
callback_url validation (UNCHANGED)
    ↓
S3 upload, jobs.Create (UNCHANGED — job.Engine already a column; set to "document" for these pairs)
    ↓
route enqueue by engine class:
    job.Engine == "image"    → queue.EnqueueImageConvert    [existing]
    job.Engine == "document" → queue.EnqueueDocumentConvert [NEW]
```

### Worker Flow (delta from existing `HandleImageConvert`)

```
asynq dispatches by task Type (ServeMux routing, UNCHANGED mechanism):
    TypeImageConvert    → Handler.HandleImageConvert    (existing, timeout=ENGINE_TIMEOUT)
    TypeDocumentConvert → Handler.HandleDocumentConvert  (NEW,      timeout=DOCUMENT_ENGINE_TIMEOUT)
                              ↓ both call:
                          Handler.process(ctx, job, timeout)   [signature WIDENED — was
                              ↓                                  single engineTimout field before]
                          context.WithTimeout(ctx, timeout)     [same whole-attempt-timeout pattern,
                              ↓                                  parameterized instead of hardcoded]
                          registry.Lookup(job.SourceFormat, job.TargetFormat)
                              ↓ resolves to LibreOfficeConverter for docx/xlsx/pptx/odt/ods/odp → pdf
                          download → conv.Convert(ctx, inPath, outPath, nil) → upload → MarkDone
```

### Key Data Flows

1. **Engine-class routing is entirely queue/task-type driven, not registry-driven.** The `Converter` `Registry` only ever answers "can this (from,to) pair convert, and with what converter" — it has no concept of queues, timeouts, or task types. Those live in `internal/queue` (task type/queue name constants) and `internal/worker` (which `Handle*` method runs, with what timeout). This separation is why adding a converter to the registry (`converters.go`) and adding a new queue/task type/handler (`queue.go`/`worker.go`/`cmd/worker/main.go`) are independent changes that can be built and tested in either order — the registry doesn't need to know queues exist, and the queue/handler layer only needs the registry to already contain a matching `Pair`.
2. **`job.Engine` is already a persisted column** (used today as the literal `"image"` constant, `internal/api/handlers.go:26` `engineImage`), so `handleCreateJob` already has the data it needs to decide `image` vs `document` enqueue routing — no schema change required. The natural implementation is a small lookup (e.g., a `convert` package helper mapping normalized target format → engine-class string, or a type switch on the `Converter` value returned by `Registry.Lookup`) rather than hardcoding a format list inside `internal/api`.

## Anti-Patterns

### Anti-Pattern 1: Adding `PK\x03\x04` as a plain `sniff.go` signature entry

**What people do:** Naively extend the `signatures` slice with `{"docx", matchZIP}` using the same 12-byte-prefix-match shape as `matchPNG`/`matchWebP`.
**Why it's wrong:** `PK\x03\x04` is shared by docx/xlsx/pptx/odt/ods/odp/plain-zip/epub/jar/apk — a plain-magic match cannot distinguish them, and `Sniff`'s existing "first match wins" iteration order would silently misclassify five of the six new formats as whichever is listed first.
**Do this instead:** Implement the two-stage container-inspection logic from Pattern 1 (read past the ZIP local-file-header into the first entry's name/content) as its own function, called only when the outer bytes match `PK\x03\x04`.

### Anti-Pattern 2: Sharing one long-lived `soffice` instance across worker goroutines (the "Gotenberg model")

**What people do:** Start one `soffice --headless` process in "listening/server" mode at worker startup and pipe conversion requests to it, to amortize the multi-second LibreOffice startup cost.
**Why it's wrong:** Requires serializing all document conversions to concurrency 1 (LibreOffice's own lock mechanism forbids concurrent operations against one profile/instance), adds a long-lived stateful process to manage (restart-on-crash, health-check, zombie `soffice.bin` cleanup) that doesn't fit the codebase's existing "hardened one-shot `os/exec` invocation" model (`exec.go`), and is explicitly a maintenance burden even in tools built around exactly that model (Gotenberg's own issue tracker documents queue-limiting problems from this design).
**Do this instead:** Pattern 2 — one-shot `soffice --headless --convert-to` per job with a job-scoped `-env:UserInstallation`, reusing `runCommand`'s existing hardened process-group-kill-on-timeout behavior (`exec.go`, unchanged) exactly as `LibvipsConverter` does today. This trades a few hundred ms–seconds of LibreOffice startup overhead per job for zero new process-lifecycle-management code — an acceptable trade given `DOCUMENT_ENGINE_TIMEOUT` is already being set longer than `ENGINE_TIMEOUT` for this exact reason.

### Anti-Pattern 3: Trusting `soffice`'s exit code the same way `vips`'s is trusted

**What people do:** Reuse `isTerminal`'s "vips always exits 1 on failure" assumption for LibreOffice, or simply treat `runCommand`'s `nil` return as conversion success.
**Why it's wrong:** Confirmed via LibreOffice's own bug tracker (multiple independent reports across versions): `soffice --convert-to` can exit `0` while having produced no output file and logged "source file could not be loaded" only to stderr.
**Do this instead:** Pattern 3 — `LibreOfficeConverter.Convert` must positively verify the renamed output file exists with non-zero size before returning `nil`, converting the "silent failure" case into an explicit Go error the same way any other engine failure is surfaced.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| `soffice` (LibreOffice headless CLI) | Same `os/exec` + `Setpgid` + SIGKILL-on-timeout pattern as `vips` (`internal/convert/exec.go`, unchanged, already explicitly documented as anticipating LibreOffice — see `exec.go`'s doc comment referencing `soffice.bin` by name) | Needs `-env:UserInstallation=file://<per-job-workDir>/loprofile` per invocation (Pattern 2); needs post-hoc output-file existence check (Pattern 3); needs a rename step from LibreOffice's basename-derived output filename to the caller's `outPath` (Pattern 4) |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| `internal/convert` (Registry) ↔ `internal/worker` (Handler) | `registry.Lookup(from, to)` returns a `Converter`; `Handler.process` calls `.Convert(ctx, inPath, outPath, nil)` | Unchanged interface — `LibreOfficeConverter{}` slots in exactly like `LibvipsConverter{}`; `process()`'s signature gains an explicit `timeout time.Duration` parameter (previously read from the single `h.engineTimout` field) |
| `internal/api` (handleCreateJob) ↔ `internal/queue` (Client) | Currently a single hardcoded `s.queue.EnqueueImageConvert(ctx, createdID)` call | **Must become a branch**: route to `EnqueueImageConvert` or the new `EnqueueDocumentConvert` based on which engine class the resolved `Converter` belongs to (derivable via the registry, or via the persisted `job.Engine`/detected-format engine-class lookup) |
| `internal/api` (handleCreateJob) ↔ `internal/convert` (Dimensions) | Currently an unconditional call to `convert.Dimensions(detected, rest)` for every format | **Must become conditional** on a new `convert.HasDimensionLimit(detected)`-style predicate — see "Confirmed Gap" below |
| `cmd/worker/main.go` ↔ `internal/worker` (Handler) | `worker.NewHandler(..., envDuration("ENGINE_TIMEOUT", 120*time.Second), ...)` — single timeout threaded through one constructor param | Constructor gains a second `documentEngineTimeout time.Duration` parameter (read from `DOCUMENT_ENGINE_TIMEOUT`, e.g. default 600s vs `ENGINE_TIMEOUT`'s 120s, given LibreOffice's slower cold-start + larger-document conversion times); `asynq.Config.Queues` map gains a `queue.QueueDocument: N` entry alongside `QueueImage`/`QueueWebhook` |

### Confirmed Gap: `internal/api/handlers.go`'s dimension-check is NOT already scoped to images

Reading `internal/convert/dimensions.go` directly: `dimensionParsers` is a closed `map[string]dimensionParser` keyed by exactly `{png, jpg, webp, heic, tiff}`. `Dimensions(format, r)` for any format **not** in that map returns `ErrDimensionsUnknown` (fail-closed) — it does **not** treat an unrecognized format as "no check applicable." `internal/api/handlers.go:handleCreateJob` (lines 152-170) calls `convert.Dimensions(detected, rest)` **unconditionally** for every accepted format, immediately after the pair-check, with no format-based branch. **As written today, this would 422-reject every docx/xlsx/pptx/odt/ods/odp upload** with "cannot determine declared image dimensions" — this is a real, must-fix integration point, not a non-issue. The minimal fix is to add a small exported predicate to `internal/convert` (e.g. `HasDimensionLimit(format string) bool` backed by the same `dimensionParsers` map) and wrap the existing dimension-check block in `internal/api/handlers.go` with `if convert.HasDimensionLimit(detected) { ... }`.

## Build Order (dependency-ordered)

1. **`internal/convert/sniff.go`** — implement ZIP-container disambiguation (Pattern 1). No dependency on anything else; testable standalone with sample docx/xlsx/pptx/odt/ods/odp fixtures.
2. **`internal/convert/dimensions.go`** — add `HasDimensionLimit` (or equivalent) predicate. Small, independent, unblocks the API-side fix.
3. **`internal/convert/libreoffice.go`** — implement `LibreOfficeConverter` (Pairs + Convert, including Patterns 2/3/4). Depends on (1) only insofar as tests want realistic fixture files; otherwise independent of the queue/worker layer. Register in `internal/convert/converters.go`'s `init()`.
4. **`internal/queue/queue.go` + `internal/queue/client.go`** — add `TypeDocumentConvert`/`QueueDocument`, `NewDocumentConvertTask`, a document retry schedule/delay func (mirroring `ImageRetryDelay`'s shape but calibrated to LibreOffice's slower attempts), `DocumentUniqueTTL`, `EnqueueDocumentConvert`. Independent of (1)-(3); can be built in parallel.
5. **`internal/worker/worker.go`** — widen `process()`'s signature to take an explicit timeout, add `Handler.documentEngineTimeout` field, add `HandleDocumentConvert`, extend `isTerminal` with LibreOffice signatures. Depends on (3) (needs `LibreOfficeConverter` registered to be meaningfully testable end-to-end) and (4) (needs `TypeDocumentConvert`/payload parsing reused, `queue.QueueDocument` for metrics labeling).
6. **`cmd/worker/main.go`** — wire `DOCUMENT_ENGINE_TIMEOUT`, register `HandleDocumentConvert` on the mux, add `QueueDocument` to `asynq.Config.Queues`. Depends on (5).
7. **`internal/api/handlers.go`** — branch the enqueue call by engine class; guard the dimension-check with `HasDimensionLimit`. Depends on (1)+(2)+(4) (needs the new Sniff logic, the predicate, and `EnqueueDocumentConvert` to exist) but is otherwise independent of the worker-side changes (5)/(6) — the API can be built/tested against a stubbed `Enqueuer` per the existing interface-segregation pattern in `internal/api/api.go`.
8. **`Dockerfile.worker`** — add LibreOffice headless packages. Purely additive; can happen any time after (3), needed before live end-to-end testing of the whole path.

This order lets format-detection (1)(2), the engine implementation (3), and queue plumbing (4) proceed in parallel (no shared dependencies), converging in the worker (5)(6), with the API branch (7) and Docker image (8) as the final integration steps before an end-to-end test.

## Scaling Considerations

Not a public-facing/high-QPS service (internal clients only, per CLAUDE.md constraints) — the relevant "scale" axis here is per-job cost and worker resource contention, not request volume tiers.

| Concern | Current (image only) | With `document` added |
|---------|----------------------|------------------------|
| Per-job engine cost | `vips copy` — sub-second for typical images | `soffice --headless` — multi-second cold start per invocation (no shared long-lived instance, Pattern 2) *plus* actual conversion time; large multi-sheet xlsx/many-slide pptx can run tens of seconds. This is the stated rationale for a longer `DOCUMENT_ENGINE_TIMEOUT`. |
| Worker container resource limits | `docker-compose.yml` sets `cpus: "2.0"`, `memory: 1g` for the (single, shared) worker service | Each concurrent `soffice` invocation is itself a multi-process tree (soffice.bin + helpers) with non-trivial memory footprint (150-300MB+ per instance is a commonly cited community rule of thumb, not an official hard number) — the existing 1 GiB limit was sized for libvips-only concurrency and **should be revisited** once `WORKER_CONCURRENCY`/`QueueDocument`'s asynq weight are set, to avoid OOM-killing the worker under concurrent document jobs. Flag as a phase-planning follow-up rather than guessing a fixed number here. |
| Queue-level concurrency | `asynq.Config.Queues: {image: 2, webhook: 1}` | Add `document: N` — because Pattern 2's per-job profile isolation removes the "must be 1" constraint some naive integrations impose, `N` can be tuned like any other queue weight, bounded in practice by the container memory ceiling above rather than by LibreOffice's own locking. |

## Docker Impact

`Dockerfile.worker`'s runtime stage currently adds a single package (`libvips-tools`) to `debian:bookworm-slim`. LibreOffice headless conversion needs a meaningfully larger package set:

- **Minimum viable set** for docx/xlsx/pptx/odt/ods/odp → pdf: `libreoffice-writer libreoffice-calc libreoffice-impress` (covers Writer/Calc/Impress document families, which map onto both the OOXML and ODF variants of each) — **not** the full `libreoffice` metapackage, which additionally pulls in Base (database), Math, Draw, and various GUI/help components unnecessary for headless PDF export. Use `apt-get install -y --no-install-recommends` (same flag already used for `libvips-tools`) to avoid recommended-but-unneeded extras (e.g., `libreoffice-gnome`, spell-check dictionaries for unneeded languages).
- **Fonts:** headless LibreOffice document→PDF fidelity is font-dependent — a documented, commonly-hit pitfall is substituted fonts silently reflowing/repaginating documents when expected fonts (e.g., core Microsoft-compatible fonts referenced by imported docx/pptx files) are missing from the container. Worth budgeting a core font package (e.g., `fonts-liberation` or an equivalent Debian package providing metric-compatible substitutes for common MS fonts) alongside the LibreOffice components — flagged as a pitfall to validate against real client documents during implementation, not a hard requirement confirmed by official docs in this research pass.
- **Size/build-time impact — genuinely worth flagging, not a minor footnote:** LibreOffice is a categorically larger dependency than libvips. Where `libvips-tools` is a single CLI package with a small, focused dependency tree, `libreoffice-writer`/`-calc`/`-impress` collectively pull in LibreOffice's shared core (`libreoffice-core`) plus dozens of supporting libraries — commonly reported as several hundred MB installed, versus low tens of MB for libvips-tools. This will measurably increase both the built worker image size and `docker-compose build`/CI build time (larger `apt-get install` package count and download size). Because the locked decision keeps image and document engines in the **same** `cmd/worker` binary and the **same** `Dockerfile.worker` (mirroring the precedent set when the `webhook` queue was added to the same worker in Phase 2 — no new binary), **every worker replica will carry this weight even in a deployment that only wants to scale image conversions.** This is an accepted trade-off given the locked "no new binary" decision, but should be called out explicitly in the phase plan as a known cost, with "split into per-engine-class worker images" noted as a candidate future optimization if image size/build time or independent scaling of image vs. document workers becomes an actual operational pain point.

## Sources

- [How Accurate Is Magic Number Detection for Identifying File Types? (inventivehq.com)](https://inventivehq.com/blog/how-accurate-is-magic-number-detection-for-identifying-file-types) — MEDIUM confidence, corroborated by the libmagic-focused search below
- [libmagic / Office Open XML detection via `[Content_Types].xml` as first ZIP member (perlmonks.org)](https://www.perlmonks.org/?node_id=958016) — MEDIUM confidence, cross-referenced against the inventivehq result
- [Identify .xlsx and .docx or .pptx from their header signature — Microsoft Learn forum](https://learn.microsoft.com/en-us/archive/msdn-technet-forums/190e803a-5306-48f4-a901-5d4f5b2e1fa2) — MEDIUM confidence
- LibreOffice concurrency / `-env:UserInstallation` isolation: [Serving Concurrent Requests for LibreOffice Service (jdhao.github.io)](https://jdhao.github.io/2021/06/11/libreoffice_concurrent_requests/), [Gotenberg issue #94 "Libreoffice Concurrence"](https://github.com/thecodingmachine/gotenberg/issues/94), [Gotenberg conversion engines docs (deepwiki mirror)](https://deepwiki.com/gotenberg/gotenberg/4-conversion-engines) — MEDIUM confidence, multiple independent sources agree
- LibreOffice exit-code-0-on-failure: [Debian bug #1058653 "libreoffice-writer-nogui: fails to convert ODT to PDF 'Error: source file could not be loaded'"](https://groups.google.com/g/linux.debian.bugs.dist/c/VOK10GzVqs0), [shelfio/libreoffice-lambda-layer issue #36](https://github.com/shelfio/libreoffice-lambda-layer/issues/36) — MEDIUM confidence, multiple independent bug reports agree
- `soffice --convert-to` output-filename-from-basename behavior: [Baeldung — Converting doc/docx to PDF with LibreOffice](https://www.baeldung.com/linux/latex-doc-docx-pdf-conversion) — MEDIUM confidence
- LibreOffice Debian package naming (`libreoffice-writer`/`-calc`/`-impress`, `libreoffice-nogui`): [LinuxCapable — How to Install LibreOffice on Debian](https://linuxcapable.com/how-to-install-libreoffice-on-debian-linux/), [Debian Wiki — LibreOffice](https://wiki.debian.org/LibreOffice) — MEDIUM confidence
- Repo files read directly (HIGH confidence, primary source): `internal/convert/convert.go`, `internal/convert/sniff.go`, `internal/convert/dimensions.go`, `internal/convert/converters.go`, `internal/convert/libvips.go`, `internal/convert/exec.go`, `internal/queue/queue.go`, `internal/queue/client.go`, `internal/worker/worker.go`, `cmd/worker/main.go`, `internal/api/handlers.go`, `Dockerfile.worker`, `.planning/PROJECT.md`

**No exact LibreOffice/apt package-size figure was independently verified against an official source** (Debian package metadata / `apt-cache show` output was not directly queried) — the "several hundred MB" figure is directionally consistent across multiple community sources, not a hard number. Flagged LOW confidence on the precise size; recommend running `docker build` locally during implementation and measuring the actual delta rather than trusting this estimate for capacity planning.

---
*Architecture research for: OctoConv v1.2 — document engine class (LibreOffice) integration*
*Researched: 2026-07-09*
