# Milestones

## v1.0 Hardening MVP (Shipped: 2026-07-08)

**Phases completed:** 4 phases, 15 plans, 36 tasks

**Key accomplishments:**

- Salted-SHA-256 client API key issuance: `0002` migration with dual-slot key columns, `internal/auth` hash helpers, `internal/clients` repository, and a `manage-clients` operator CLI supporting create/add-key/revoke.
- chi middleware turning issued API keys into hard-cutover 401 enforcement on `/v1/*`, with `client_id` threaded through job creation and a 404-only (never 403) cross-client ownership guard on job reads.
- In-process `go-chi/httprate` middleware (`internal/ratelimit`) with a coarse pre-auth IP flood guard and a per-client fair-use limiter keyed on the authenticated `client_id`, wired into `/v1` as `ByIP -> auth -> PerClient` with env-configurable 60/120 rpm defaults.
- Fixed two verified-but-unfixed gaps from `01-VERIFICATION.md`: jobs integration tests violating the new `jobs_client_id_fkey` when run against a live Postgres, and the pre-auth `ratelimit.ByIP` guard being fully bypassable via spoofed `X-Forwarded-For` because of chi's deprecated `middleware.RealIP`.
- POST /v1/jobs now accepts a per-job `callback_url`, rejecting SSRF targets (loopback/RFC1918/link-local/metadata) and non-https schemes with a fixed 400 before any storage write, and persists/reads it through Postgres via the existing nullable-column idiom.
- HMAC-SHA256 payload signing, a Postgres delivery-attempt repository with dead-lettering, and a single-attempt HTTPS deliverer (2xx-only, 10s timeout), each independently unit/integration tested.
- Completing jobs with a callback_url now trigger a signed, retried, tracked webhook end-to-end: `webhook:deliver` enqueued after MarkDone/MarkFailed, delivered with a freshly-presigned URL per attempt, retried by asynq with bounded exponential backoff + jitter, and dead-lettered after 6 exhausted retries.
- Image conversion tasks now retry on their own fast 2s/5s/15s schedule with a bounded MaxRetry (default 4) via a queue-aware RetryDelayFunc dispatcher, and carry a per-job asynq.Unique lock whose TTL is derived from IMAGE_MAX_RETRY + ENGINE_TIMEOUT so duplicate enqueues collide safely instead of double-processing.
- Worker now distinguishes transient from terminal image-conversion failures via a pure `isTerminal(err)` classifier, `MarkActive` is idempotent for asynq's same-task retries, raw vips stderr no longer reaches `error_message`, and a single whole-attempt timeout bounds download+convert+upload+record so no attempt can outlive the asynq unique-lock TTL.
- A ticker-driven reconciler now sweeps Postgres every minute for jobs stranded in `queued`/`active` past a staleness threshold, requeues genuinely-stranded ones through an enqueue-first, `asynq.ErrDuplicateTask`-guarded recovery path (never duplicating a still-live task or falsely inflating a backlogged job's recovery count), and terminally fails jobs that exceed a bounded recovery cap with a webhook fired on exhaustion.
- Magic-byte content sniffing (hardcoded 5-format signature table) gates `handleCreateJob` before any pair-check or S3 write, rejecting declared/detected mismatches and unrecognized content with a detailed 422 and a client-scoped log line.
- MinIO ILM lifecycle rule (7-day default TTL on uploads/ and results/) applied declaratively via minio-go's SetBucketLifecycle at API startup, plus a read-only storage.Ping probe for the future health endpoint.
- Defined four Prometheus metric families (job outcomes, job duration, webhook deliveries, reconciler actions) plus a pull-based queue-depth collector in a new `internal/metrics` package, and instrumented the existing worker/reconciler terminal exit points to call them — closing the instrumentation half of OBS-01.
- GET /healthz now pings Postgres, Redis, and S3/MinIO under a shared 3s timeout, returning 200/ok when all reachable and 503/degraded with per-dependency detail otherwise.
- Second localhost-only `/metrics` HTTP listener mounted in both `cmd/api/main.go` and `cmd/worker/main.go` (promhttp.Handler(), METRICS_ADDR default 127.0.0.1:9090), queue-depth collector registered in the worker, and a pinned `hibiken/asynqmon:0.7.2` dashboard service bound to 127.0.0.1:8980 — all three verified live end-to-end (real conversion job, real metrics scrape, real dashboard query).

---
