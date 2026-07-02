# External Integrations

**Analysis Date:** 2026-07-02

## APIs & External Services

**Conversion engines (invoked via shelling out, not network APIs):**
- libvips (`vips` CLI) - raster image conversion between png/jpg/webp/heic/tiff
  - Invocation: `internal/convert/libvips.go:31` (`vips copy <in> <out>`)
  - Hardened execution wrapper: `internal/convert/exec.go` (process-group kill on context timeout, stderr capture)
  - Only engine class implemented in the current vertical slice (`README.md:8`); the DB schema already anticipates `document`, `av`, `cad`, `archive`, `probe` engines via a CHECK constraint (`internal/db/migrations/0001_init.sql`, `jobs.engine` column) but no code exists for them yet
  - Runtime dependency: `libvips-tools` apt package, installed only in the worker image (`Dockerfile.worker:12`)

No outbound calls to third-party SaaS APIs (payment, email, SMS, etc.) were found.

## Data Storage

**Databases:**
- PostgreSQL 18 - "system of record" / source of truth (`README.md:5`)
  - Connection: `DATABASE_URL` env var, DSN format `postgres://user:pass@host:port/db`
  - Client: `github.com/jackc/pgx/v5` `pgxpool` (`internal/db/db.go`)
  - Repository layer: `internal/jobs/repo.go` (job CRUD), `internal/jobs/jobs.go` (domain types)
  - Schema/migrations: `internal/db/migrations/0001_init.sql`, embedded into the binary via `go:embed` and applied idempotently at startup by both `cmd/api/main.go:32` and `cmd/migrate/main.go:19` (tracked in a `schema_migrations` table)
  - Tables: `clients`, `presets`, `jobs`, `job_inputs`, `job_outputs`, `job_events`, `webhook_deliveries`
  - Local dev/compose instance: `postgres:18` image, host port `5433` mapped to container `5432`, credentials `octo` / `octo-pass`, db `octo_db` (`docker-compose.yml:2-18`)

**File Storage:**
- S3-compatible object storage (MinIO in dev/compose, any S3-compatible endpoint in general)
  - Client: `github.com/minio/minio-go/v7` (`internal/storage/storage.go`)
  - Config: `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`, `S3_USE_SSL` env vars (`internal/storage/storage.go:25-29`)
  - Operations: `Upload` (PutObject), `Download` (GetObject + Stat probe), `PresignGet` (time-limited download URL) — `internal/storage/storage.go:56-88`
  - Object key layout: `internal/storage/keys.go`
  - Bucket existence is verified (not created) by the app at startup (`internal/storage/storage.go:43-49`); bucket creation is handled out-of-band by the compose one-shot `createbucket` service using `minio/mc` (`docker-compose.yml:52-62`)
  - Local dev/compose instance: `minio/minio:latest`, host ports `9100` (S3 API) / `9101` (console), credentials `minioadmin` / `minioadmin`, bucket `octoconv`
  - Known caveat: when both `api` and MinIO run inside Compose, presigned URLs point to the internal `minio:9000` endpoint, which is unreachable from the host; documented workaround is running `api` locally against `localhost:9100` (`README.md:70-72`)

**Caching:**
- None (Redis is used exclusively as the asynq task queue broker, not as a general cache)

## Authentication & Identity

**Auth Provider:**
- None implemented. No auth middleware, API keys, or session/token handling detected in `internal/api/`. The `clients` table (`internal/db/migrations/0001_init.sql`) models multi-tenant clients but there is no code wiring requests to a client/auth identity yet.

## Monitoring & Observability

**Error Tracking:**
- None (no Sentry/Bugsnag/etc. integration found)

**Logs:**
- Standard library `log` package to stdout, e.g. `log.Printf`/`log.Fatalf` in `cmd/api/main.go` and `cmd/worker/main.go`
- HTTP request logging via chi's `middleware.Logger` (`internal/api/routes.go:13`)
- Structured job lifecycle events persisted to Postgres in the `job_events` table (`internal/db/migrations/0001_init.sql`), not sent to an external log/metrics service

## CI/CD & Deployment

**Hosting:**
- Self-hosted via Docker Compose (`docker-compose.yml`) — no cloud provider config (Terraform, k8s manifests, etc.) detected

**CI Pipeline:**
- None detected (no `.github/workflows`, `.gitlab-ci.yml`, or similar found in the repo root)

## Environment Configuration

**Required env vars:**
- `DATABASE_URL` - Postgres DSN
- `REDIS_ADDR` - Redis address for asynq broker
- `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`, `S3_USE_SSL` - object storage credentials/config
- `API_ADDR` - HTTP API bind address
- `MAX_UPLOAD_BYTES` - upload size limit (default documented as 100 MiB)
- `WORKER_CONCURRENCY` - asynq worker concurrency
- `ENGINE_TIMEOUT` - per-conversion-engine-invocation timeout (parsed as a Go duration, e.g. `120s`)

**Secrets location:**
- Local `.env` file (git-ignored via `.gitignore:1`, present at repo root but never read by this tool per forbidden-file policy)
- `.env.example` documents variable names with non-secret placeholder/dev values only
- In `docker-compose.yml`, credentials for postgres/minio are hardcoded as dev-only defaults directly in the compose environment blocks (`octo`/`octo-pass`, `minioadmin`/`minioadmin`) — acceptable for local dev, not suitable for production as-is

## Webhooks & Callbacks

**Incoming:**
- None (no inbound webhook receiver endpoints found in `internal/api/routes.go`)

**Outgoing:**
- Schema-only, not yet implemented: `jobs.callback_url` column and a `webhook_deliveries` table (tracking `url`, `attempt`, `status_code`, `delivered`) exist in `internal/db/migrations/0001_init.sql`, but no Go code in `internal/` currently sends webhook callbacks or drains `webhook_deliveries` — this is a planned/future feature, not an active integration.

---

*Integration audit: 2026-07-02*
