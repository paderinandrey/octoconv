# Technology Stack

**Analysis Date:** 2026-07-02

## Languages

**Primary:**
- Go 1.26 (module directive `go 1.26.4` in `go.mod`, local toolchain `go1.26.4 darwin/arm64`) - entire codebase (`cmd/`, `internal/`)

**Secondary:**
- SQL (PostgreSQL DDL) - schema/migrations in `internal/db/migrations/0001_init.sql`
- Shell - `entrypoint`/setup scripts inline in `docker-compose.yml` (e.g. `createbucket` service)

## Runtime

**Environment:**
- Go 1.26 toolchain (`go.mod:3`)
- Compiled to static binaries with `CGO_ENABLED=0` (`Dockerfile.api:7`, `Dockerfile.worker:7`)
- Runtime containers based on `debian:bookworm-slim`, running as `USER nobody` (`Dockerfile.api:15`, `Dockerfile.worker:16`)

**Package Manager:**
- Go modules (`go.mod` / `go.sum`)
- Lockfile: present (`go.sum`)

## Frameworks

**Core:**
- `github.com/go-chi/chi/v5` v5.3.0 - HTTP router/middleware for the API (`internal/api/routes.go`)
- `github.com/hibiken/asynq` v0.26.0 - Redis-backed task queue for dispatching conversion jobs to workers (`internal/queue/`, `internal/worker/worker.go`, `cmd/worker/main.go`)
- `github.com/jackc/pgx/v5` v5.10.0 - PostgreSQL driver/connection pool (`internal/db/db.go`)
- `github.com/minio/minio-go/v7` v7.2.1 - S3-compatible object storage client (`internal/storage/storage.go`)
- `github.com/google/uuid` v1.6.0 - UUID generation for job IDs and task payloads (`internal/queue/queue.go`)

**Testing:**
- Go standard library `testing` package - all `*_test.go` files (`internal/api/handlers_test.go`, `internal/convert/convert_test.go`, `internal/jobs/repo_test.go`, `internal/queue/queue_test.go`, `internal/storage/storage_test.go`)
- No third-party assertion/mocking library detected; tests use stdlib `testing` idioms only

**Build/Dev:**
- Docker multi-stage builds - `Dockerfile.api`, `Dockerfile.worker`
- Docker Compose - `docker-compose.yml` (orchestrates postgres, redis, minio, api, worker, and a one-shot bucket-creation job)
- `go build` invoked directly in Dockerfiles (no separate build tool/Makefile detected)

## Key Dependencies

**Critical:**
- `github.com/hibiken/asynq` v0.26.0 - defines the job queue contract (task types, queue names) that couples the API (producer) and worker (consumer); depends transitively on `github.com/redis/go-redis/v9` v9.14.1 and `github.com/robfig/cron/v3` v3.0.1
- `github.com/jackc/pgx/v5` v5.10.0 - all persistence goes through this driver; Postgres is documented as "system of record" (`README.md:5`)
- `github.com/minio/minio-go/v7` v7.2.1 - S3-compatible storage client used for both direct upload/download and presigned URLs (`internal/storage/storage.go`)
- `os/exec` (stdlib) + `syscall` - hardened external process execution with process-group kill on timeout, used to shell out to conversion engines (`internal/convert/exec.go`)

**Infrastructure:**
- `github.com/go-chi/chi/v5` v5.3.0 with `chi/middleware` (RequestID, RealIP, Logger, Recoverer) - HTTP layer (`internal/api/routes.go`)
- `github.com/jackc/puddle/v2` v2.2.2 (indirect, via pgx) - connection pooling internals
- `github.com/klauspost/compress`, `github.com/klauspost/cpuid/v2` (indirect, via minio-go) - storage client performance internals

## Configuration

**Environment:**
- Loaded via `os.Getenv` calls scattered through `cmd/api/main.go`, `cmd/worker/main.go`, `internal/db/db.go`, `internal/queue/queue.go`, `internal/storage/storage.go` — no `.env` parsing library; developers source `.env` manually (`set -a && . ./.env && set +a`, per `README.md:77`)
- `.env.example` documents all variables; a local `.env` exists but is git-ignored (`.gitignore:1`)
- Key configs required: `DATABASE_URL`, `REDIS_ADDR`, `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`, `S3_USE_SSL`, `API_ADDR`, `MAX_UPLOAD_BYTES`, `WORKER_CONCURRENCY`, `ENGINE_TIMEOUT` (`.env.example`)
- Numeric/duration env values tolerate trailing inline comments via a `firstField` helper duplicated in `cmd/api/main.go:89` and `cmd/worker/main.go:77`

**Build:**
- `Dockerfile.api` - two-stage build: `golang:1.26-bookworm` builder → `debian:bookworm-slim` runtime with only `ca-certificates`
- `Dockerfile.worker` - two-stage build: `golang:1.26-bookworm` builder → `debian:bookworm-slim` runtime with `ca-certificates` and `libvips-tools` (needed because the worker shells out to the `vips` CLI, `internal/convert/libvips.go:31`)
- `docker-compose.yml` - defines resource limits on the worker (`cpus: "2.0"`, `memory: 1g`) and healthchecks for postgres/redis/minio

## Platform Requirements

**Development:**
- Go 1.26+ (`README.md:44`)
- Docker + Docker Compose (`README.md:45`)
- `vips` CLI available locally if running the worker outside Docker (image conversions call `vips copy`, `internal/convert/libvips.go:31`)

**Production:**
- Docker Compose deployment target (services: `postgres:18`, `redis:8`, `minio/minio:latest`, plus built `api` and `worker` images) — see `docker-compose.yml`
- Non-standard host ports used to avoid local conflicts: Postgres on `5433`, MinIO API/console on `9100`/`9101` (`README.md:66`)
- Worker container resource-limited (2 CPU / 1 GiB RAM) and runs as unprivileged `nobody` since it shells out to untrusted-input engines (`Dockerfile.worker:16`)

---

*Stack analysis: 2026-07-02*
