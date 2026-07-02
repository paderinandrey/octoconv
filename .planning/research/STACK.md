# Stack Research

**Domain:** Production-hardening an internal Go async job service (auth, rate limiting, webhooks, content validation, observability)
**Researched:** 2026-07-02
**Confidence:** HIGH

This research is scoped to the *hardening* additions only. Go 1.26, chi v5, asynq v0.26, pgx/v5, minio-go/v7, and Postgres 18 are locked (see `.planning/codebase/STACK.md`) and are not re-evaluated here — every recommendation below is chosen to compose with that existing stack, not replace parts of it.

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `crypto/sha256` + `crypto/subtle` (stdlib) | Go 1.26 stdlib | API-key hashing + constant-time comparison | API keys are high-entropy random tokens, not human passwords — the security comes from entropy, not hash slowness. Salted/fast SHA-256 + `subtle.ConstantTimeCompare` is the standard pattern used by Stripe/GitHub-style token auth; bcrypt/argon2 would add ~100-300ms CPU cost to *every* request for no security benefit and would bottleneck a high-throughput internal API. HIGH confidence (multiple independent sources agree; matches general industry consensus). |
| `github.com/go-chi/httprate` | v0.15.0 | Per-key in-process HTTP rate limiting middleware | Official `go-chi` org project (not third-party), actively maintained (pushed 2026-06-29), designed specifically as chi middleware with a pluggable `KeyFunc` (rate-limit by `client_id` from the auth middleware, not just IP). Zero new infrastructure — matches the current single API-instance docker-compose deployment. HIGH confidence (verified via Go module proxy + GitHub activity). |
| `github.com/gabriel-vasile/mimetype` | v1.4.13 | Magic-bytes/content-sniffing file type detection | Actively maintained (pushed 2026-07-01, ~2000 stars), reads only the file header (not the whole file) so it's cheap to run on every upload, and its hierarchical type tree gives you an actual MIME type + matching file extension in one call — a strict superset of stdlib `http.DetectContentType` (which only recognizes ~30 types and has no file-extension mapping). This is the de facto standard Go library for this exact "don't trust the client's declared Content-Type" problem. HIGH confidence. |
| `github.com/prometheus/client_golang` (`prometheus`, `promauto`, `promhttp`) | v1.23.2 | Expose `/metrics` endpoint from API and worker processes | The canonical, officially-maintained Prometheus Go client. `promhttp.Handler()` mounted as a chi route is all that's needed; `promauto` reduces registration boilerplate for custom counters/histograms (job counts by status, webhook delivery outcomes, HTTP latency). HIGH confidence (official Prometheus project, verified via module proxy). |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/hibiken/asynq/x/metrics` | ships with asynq v0.26.0 (subpackage in the already-vendored module, no new dependency) | `prometheus.Collector` for asynq queue metrics (queue size, latency, pending/active/retry counts) | Register `metrics.NewQueueMetricsCollector(inspector)` against your own `prometheus.Registry` in the API or a small metrics-only process, so queue depth/latency show up in the same Prometheus/Grafana stack as your app metrics — this is separate from asynqmon's own `--enable-metrics-exporter` flag (see below) and gives you first-party queue metrics without depending on the UI binary being up. |
| `hibiken/asynqmon` (Docker image `hibiken/asynqmon`) | v0.7.2 / `latest` image | Web UI for inspecting/retrying/deleting asynq tasks; can also expose its own `/metrics` via `--enable-metrics-exporter` and read back a Prometheus server via `--prometheus-addr` for in-UI graphs | Run as a sidecar container in docker-compose pointed at the same Redis instance. Last tagged release is from 2024, but it remains the standard/only actively-used web UI for asynq — there is no actively-maintained alternative. Use it for human operators (debugging stuck jobs, manual retry) and rely on `x/metrics` + Prometheus/Grafana for alerting, not the other way around. MEDIUM confidence on long-term maintenance (no recent releases) but no viable substitute exists. |
| `golang.org/x/time/rate` | v0.15.0 | In-process token-bucket limiter, alternative to `httprate` if you need custom limiter composition (e.g. wrapping the S3 upload path, not just HTTP request rate) | Only reach for this directly if `httprate`'s chi-native middleware doesn't fit a specific non-HTTP rate-limiting need (e.g. limiting concurrent engine invocations). For the "per-client HTTP rate limit" requirement, prefer `httprate` — it's already built on this package internally. |
| `github.com/go-redis/redis_rate/v10` | v10.0.1 (module tag from 2023; repo itself actively pushed as of 2026-06-22, not archived) | Redis-backed GCRA (leaky-bucket) rate limiter shared across replicas | Upgrade path, not needed now. Only adopt this when the API is horizontally scaled behind a load balancer (the KEDA/Kubernetes work explicitly called out as future/out-of-scope) — in-process `httprate` state doesn't survive across replicas or restarts. Depends on `github.com/redis/go-redis/v9`, which is already a transitive dependency via asynq, so adding it later is low-friction. |
| `crypto/hmac` + `crypto/sha256` (stdlib) | Go 1.26 stdlib | Sign outgoing webhook payloads (`X-OctoConv-Signature` header) | Use for every webhook delivery so receiving services can verify authenticity — standard practice (Stripe/GitHub webhook signature pattern), no third-party library needed. |
| Existing `asynq.Client`/`ServeMux` (no new dependency) | v0.26.0 | Reliable webhook delivery with retries, driven from `webhook_deliveries` table | See "Webhook delivery" pattern below — this is a technique, not a new library. |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| `docker-compose` service block for `asynqmon` | Run the monitoring UI alongside `api`/`worker`/`postgres`/`redis`/`minio` | Point `--redis-addr` at the existing `redis` service; add a healthcheck like the other services already have. |
| Prometheus (standalone container, not a Go library) | Scrape `/metrics` from `api`, `worker`, and `asynqmon` | Add a `prometheus.yml` scrape config with three jobs; this is infra config, not a Go dependency — mentioned here because the milestone explicitly asks for "asynqmon + Prometheus" as a pair. |

## Installation

```bash
# Core hardening additions
go get github.com/go-chi/httprate@v0.15.0
go get github.com/gabriel-vasile/mimetype@v1.4.13
go get github.com/prometheus/client_golang@v1.23.2

# x/metrics ships inside the already-vendored hibiken/asynq module — no separate `go get` needed,
# just import github.com/hibiken/asynq/x/metrics

# stdlib only, no install needed: crypto/sha256, crypto/subtle, crypto/hmac

# Deferred (only when horizontally scaling the API across replicas):
# go get github.com/go-redis/redis_rate/v10@v10.0.1
```

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|--------------------------|
| `go-chi/httprate` (in-process) | `go-redis/redis_rate` (Redis-backed GCRA) | When the API runs as multiple replicas behind a load balancer and rate-limit state must be shared/consistent across instances — not the case today (single API process per docker-compose). |
| `gabriel-vasile/mimetype` | stdlib `http.DetectContentType` | Only for a quick prototype or if you truly only need to distinguish a handful of common web types (images/text/pdf) — it recognizes far fewer formats than `mimetype` and has no extension-mapping API, which matters for validating HEIC/TIFF/WebP uploads. |
| `gabriel-vasile/mimetype` | `h2non/filetype` | Never for new code — `h2non/filetype`'s last tagged release was 2021 (v1.1.1); it is effectively unmaintained despite the repo not being formally archived. |
| SHA-256 + constant-time compare for API keys | bcrypt/argon2 for API keys | Only if API keys are ever *user-chosen, low-entropy* secrets (they won't be here — `clients.api_key` should be a server-generated random token). |
| asynq-driven webhook delivery (reuse existing queue) | Dedicated outbox library (e.g. `github.com/oagudo/outbox`) | If webhook delivery needs to be decoupled from asynq entirely (e.g. a separate relay process with its own polling loop independent of Redis). Given asynq/Redis is already the retry/backoff engine in this codebase, introducing a second retry mechanism is unnecessary complexity for this milestone — prefer reusing asynq's task retry (`asynq.MaxRetry`, `RetryDelayFunc`) driven off `webhook_deliveries` rows. |
| `hibiken/asynqmon` | Building a custom asynq inspector UI | Never justified — asynqmon already wraps `asynq.Inspector` with a full web UI; a custom UI would duplicate significant effort for no benefit at this project's scale. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|--------------|
| `h2non/filetype` | Last release 2021, effectively unmaintained (fewer supported formats, no active issue triage) | `gabriel-vasile/mimetype` |
| bcrypt/argon2 for API-key verification | Deliberately slow (100-300ms/op by design) — every authenticated request would pay this cost; this is a CPU DoS risk for a busy internal API, not a security improvement, since API keys are already high-entropy | Salted `sha256` hash stored in `clients` table + `subtle.ConstantTimeCompare` on lookup |
| Trusting client-supplied `Content-Type` header or file extension alone | The exact vulnerability class this milestone's "magic-bytes validation" work item is meant to close — a malicious or buggy client can claim `image/png` for anything | `mimetype.DetectReader` on the first N bytes of the uploaded stream, cross-checked against the declared/target format before it ever reaches libvips |
| A generic third-party "webhook delivery" SaaS-style Go library (e.g. Svix-style wrappers) | Adds an external dependency/opinionated retry model on top of a system that already has a durable queue (asynq) and a durable outbox table (`webhook_deliveries`) purpose-built for this | Hand-rolled delivery: enqueue an asynq task per pending `webhook_deliveries` row (or per job completion), let asynq's own retry/backoff drive re-delivery attempts, record each attempt (status code, timestamp, error) back into `webhook_deliveries` |
| `766b/chi-prometheus` (or similar old chi+Prometheus middleware shims) | Stale, unmaintained third-party glue code for something trivial to hand-write | A ~15-line custom chi middleware using 2-3 `promauto`-registered metrics (`http_requests_total`, `http_request_duration_seconds`) — full control over labels (route pattern via `chi.RouteContext`, status code, `client_id`) without an extra dependency |
| Redis-backed rate limiting from day one | Adds a Redis round-trip to every request and a new failure mode, for a benefit (cross-replica consistency) the project doesn't need yet — the API is currently single-instance | `go-chi/httprate` in-process; revisit when horizontal scaling actually happens |

## Stack Patterns by Variant

**If the reconciler needs to run on a schedule (sweeping stranded `queued` jobs):**
- Use `asynq`'s built-in periodic task scheduler (`asynq.PeriodicTaskManager` / `asynq.Scheduler`, part of the already-vendored `hibiken/asynq` module — no new dependency) to enqueue a reconciler task every N minutes, rather than adding `robfig/cron` directly (it's already a transitive dependency of asynq, but asynq's own scheduler is the idiomatic entry point) or a separate `os/exec` cron job.
- Because: keeps the reconciler in the same operational model (asynq queue, visible in asynqmon, same retry/observability) as the rest of the job pipeline, instead of introducing a second scheduling mechanism.

**If webhook delivery needs strict ordering per job or de-duplication:**
- Use a unique constraint on `(job_id, attempt)` or a dedicated `idempotency_key` sent in the webhook payload, and have the receiving side de-dupe — do not add a new library for this; it's a schema/payload design decision, not a Go dependency question.

**If S3/MinIO lifecycle TTL needs verifying against real MinIO behavior (not just AWS S3 semantics):**
- Configure via `mc ilm` (MinIO client CLI) or `minio-go/v7`'s `SetBucketLifecycle` API (already have `minio-go/v7` v7.2.1 in the stack) — no new library needed, but MEDIUM confidence that MinIO's lifecycle engine fully matches AWS S3 lifecycle semantics; verify against the specific MinIO server version in use during implementation rather than assuming AWS docs apply verbatim.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|------------------|-------|
| `go-redis/redis_rate/v10` | `github.com/redis/go-redis/v9` v9.0.2+ | Already have `redis/go-redis/v9` v9.14.1 transitively via asynq — no version conflict if `redis_rate` is added later. |
| `prometheus/client_golang` v1.23.2 | Go 1.21+ | Fine under Go 1.26. |
| `gabriel-vasile/mimetype` v1.4.13 | Go 1.20+ (per module's go.mod) | Fine under Go 1.26; no CGO requirement (stays consistent with `CGO_ENABLED=0` static builds used in `Dockerfile.api`/`Dockerfile.worker`). |
| `go-chi/httprate` v0.15.0 | `go-chi/chi/v5` (any v5.x) | Built specifically for chi v5's `http.Handler`-based middleware chain — direct fit with the existing `internal/api/routes.go` middleware stack. |
| `hibiken/asynq/x/metrics` | `hibiken/asynq` v0.26.0 (same module, same version, no separate resolution) | Ships inside the already-pinned asynq module; importing it does not change `go.sum` for asynq itself. |

## Sources

- Go module proxy (`proxy.golang.org`) — verified exact latest versions for `client_golang`, `mimetype`, `redis_rate/v10`, `x/time`, `asynq`, `asynqmon`, `httprate`, `ulule/limiter` (HIGH confidence, authoritative version source)
- GitHub API (`api.github.com/repos/...`) — verified `archived` status and `pushed_at` recency for `go-redis/redis_rate`, `hibiken/asynqmon`, `gabriel-vasile/mimetype`, `hibiken/asynq`, `h2non/filetype`, `oagudo/outbox` (HIGH confidence for maintenance-status claims)
- [go-chi/httprate](https://github.com/go-chi/httprate) — official chi-org rate-limiting middleware, confirmed via module proxy + GitHub activity
- [gabriel-vasile/mimetype GitHub](https://github.com/gabriel-vasile/mimetype/) — magic-number detection, header-only reads, hierarchical type tree (MEDIUM-HIGH, WebSearch + GitHub metadata cross-checked)
- [go-redis/redis_rate GitHub](https://github.com/go-redis/redis_rate) — GCRA/leaky-bucket algorithm description, Redis 3.2+ requirement (MEDIUM, WebSearch + module proxy cross-checked)
- [hibiken/asynqmon GitHub](https://github.com/hibiken/asynqmon) — `--enable-metrics-exporter` / `--prometheus-addr` flags for Prometheus integration (MEDIUM, WebSearch verified against pkg.go.dev description; no recent tagged release so flag names not independently re-verified against latest source)
- [prometheus/client_golang promhttp pkg.go.dev](https://pkg.go.dev/github.com/prometheus/client_golang/prometheus/promhttp) — official HTTP handler exposition pattern (HIGH, official Prometheus project docs)
- [Transactional Outbox Pattern in Go with PostgreSQL](https://www.glukhov.org/app-architecture/integration-patterns/transactional-outbox-pattern-go) — outbox/relay pattern with `retry_after` column and exponential backoff (MEDIUM, single-source pattern description, but consistent with the project's already-defined `webhook_deliveries` schema)
- WebSearch: "API key hashing SHA-256 vs bcrypt high entropy token storage best practice" — salted-SHA-256 + constant-time compare vs bcrypt/argon2 tradeoff for API keys specifically (MEDIUM, multiple independent blog sources agree, consistent with well-known industry pattern e.g. GitHub PAT / Stripe key storage)

---
*Stack research for: production-hardening an internal Go async file-conversion service*
*Researched: 2026-07-02*
