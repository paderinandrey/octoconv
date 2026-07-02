<!-- GSD:project-start source:PROJECT.md -->
## Project

**OctoConv**

OctoConv — внутренний асинхронный сервис конвертации файлов на Go для сервисов компании. Клиент отправляет файл через API, сервис кладёт его в S3-совместимое хранилище, ставит задачу в очередь (asynq/Redis), воркер запускает внешний движок конвертации и складывает результат обратно в S3. Сейчас реализован один сквозной вертикальный срез — конвертация изображений через libvips — рабочий end-to-end на живой инфраструктуре, но ещё не production-ready и не влит в `main`.

**Core Value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации изображения и получить результат — без риска для стабильности или безопасности продакшена.

### Constraints

- **Tech stack**: Go 1.26, chi (API), asynq + Redis (очередь), PostgreSQL 18 (система записи), S3/MinIO (хранилище) — зафиксировано в Notion-спеке, не пересматривается на этом этапе
- **Auth**: API-ключи через существующую таблицу `clients` — не вводить отдельный внешний auth-провайдер
- **Deployment**: Docker / docker-compose для локальной разработки; Kubernetes + KEDA — будущее, вне текущего фокуса
- **Сlients**: только внутренние сервисы компании — публичная многоарендность и биллинг не требуются на этом этапе
<!-- GSD:project-end -->

<!-- GSD:stack-start source:codebase/STACK.md -->
## Technology Stack

## Languages
- Go 1.26 (module directive `go 1.26.4` in `go.mod`, local toolchain `go1.26.4 darwin/arm64`) - entire codebase (`cmd/`, `internal/`)
- SQL (PostgreSQL DDL) - schema/migrations in `internal/db/migrations/0001_init.sql`
- Shell - `entrypoint`/setup scripts inline in `docker-compose.yml` (e.g. `createbucket` service)
## Runtime
- Go 1.26 toolchain (`go.mod:3`)
- Compiled to static binaries with `CGO_ENABLED=0` (`Dockerfile.api:7`, `Dockerfile.worker:7`)
- Runtime containers based on `debian:bookworm-slim`, running as `USER nobody` (`Dockerfile.api:15`, `Dockerfile.worker:16`)
- Go modules (`go.mod` / `go.sum`)
- Lockfile: present (`go.sum`)
## Frameworks
- `github.com/go-chi/chi/v5` v5.3.0 - HTTP router/middleware for the API (`internal/api/routes.go`)
- `github.com/hibiken/asynq` v0.26.0 - Redis-backed task queue for dispatching conversion jobs to workers (`internal/queue/`, `internal/worker/worker.go`, `cmd/worker/main.go`)
- `github.com/jackc/pgx/v5` v5.10.0 - PostgreSQL driver/connection pool (`internal/db/db.go`)
- `github.com/minio/minio-go/v7` v7.2.1 - S3-compatible object storage client (`internal/storage/storage.go`)
- `github.com/google/uuid` v1.6.0 - UUID generation for job IDs and task payloads (`internal/queue/queue.go`)
- Go standard library `testing` package - all `*_test.go` files (`internal/api/handlers_test.go`, `internal/convert/convert_test.go`, `internal/jobs/repo_test.go`, `internal/queue/queue_test.go`, `internal/storage/storage_test.go`)
- No third-party assertion/mocking library detected; tests use stdlib `testing` idioms only
- Docker multi-stage builds - `Dockerfile.api`, `Dockerfile.worker`
- Docker Compose - `docker-compose.yml` (orchestrates postgres, redis, minio, api, worker, and a one-shot bucket-creation job)
- `go build` invoked directly in Dockerfiles (no separate build tool/Makefile detected)
## Key Dependencies
- `github.com/hibiken/asynq` v0.26.0 - defines the job queue contract (task types, queue names) that couples the API (producer) and worker (consumer); depends transitively on `github.com/redis/go-redis/v9` v9.14.1 and `github.com/robfig/cron/v3` v3.0.1
- `github.com/jackc/pgx/v5` v5.10.0 - all persistence goes through this driver; Postgres is documented as "system of record" (`README.md:5`)
- `github.com/minio/minio-go/v7` v7.2.1 - S3-compatible storage client used for both direct upload/download and presigned URLs (`internal/storage/storage.go`)
- `os/exec` (stdlib) + `syscall` - hardened external process execution with process-group kill on timeout, used to shell out to conversion engines (`internal/convert/exec.go`)
- `github.com/go-chi/chi/v5` v5.3.0 with `chi/middleware` (RequestID, RealIP, Logger, Recoverer) - HTTP layer (`internal/api/routes.go`)
- `github.com/jackc/puddle/v2` v2.2.2 (indirect, via pgx) - connection pooling internals
- `github.com/klauspost/compress`, `github.com/klauspost/cpuid/v2` (indirect, via minio-go) - storage client performance internals
## Configuration
- Loaded via `os.Getenv` calls scattered through `cmd/api/main.go`, `cmd/worker/main.go`, `internal/db/db.go`, `internal/queue/queue.go`, `internal/storage/storage.go` — no `.env` parsing library; developers source `.env` manually (`set -a && . ./.env && set +a`, per `README.md:77`)
- `.env.example` documents all variables; a local `.env` exists but is git-ignored (`.gitignore:1`)
- Key configs required: `DATABASE_URL`, `REDIS_ADDR`, `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`, `S3_USE_SSL`, `API_ADDR`, `MAX_UPLOAD_BYTES`, `WORKER_CONCURRENCY`, `ENGINE_TIMEOUT` (`.env.example`)
- Numeric/duration env values tolerate trailing inline comments via a `firstField` helper duplicated in `cmd/api/main.go:89` and `cmd/worker/main.go:77`
- `Dockerfile.api` - two-stage build: `golang:1.26-bookworm` builder → `debian:bookworm-slim` runtime with only `ca-certificates`
- `Dockerfile.worker` - two-stage build: `golang:1.26-bookworm` builder → `debian:bookworm-slim` runtime with `ca-certificates` and `libvips-tools` (needed because the worker shells out to the `vips` CLI, `internal/convert/libvips.go:31`)
- `docker-compose.yml` - defines resource limits on the worker (`cpus: "2.0"`, `memory: 1g`) and healthchecks for postgres/redis/minio
## Platform Requirements
- Go 1.26+ (`README.md:44`)
- Docker + Docker Compose (`README.md:45`)
- `vips` CLI available locally if running the worker outside Docker (image conversions call `vips copy`, `internal/convert/libvips.go:31`)
- Docker Compose deployment target (services: `postgres:18`, `redis:8`, `minio/minio:latest`, plus built `api` and `worker` images) — see `docker-compose.yml`
- Non-standard host ports used to avoid local conflicts: Postgres on `5433`, MinIO API/console on `9100`/`9101` (`README.md:66`)
- Worker container resource-limited (2 CPU / 1 GiB RAM) and runs as unprivileged `nobody` since it shells out to untrusted-input engines (`Dockerfile.worker:16`)
<!-- GSD:stack-end -->

<!-- GSD:conventions-start source:CONVENTIONS.md -->
## Conventions

## Naming Patterns
- Lowercase, no underscores/hyphens: `handlers.go`, `routes.go`, `keys.go`, `libvips.go`, `converters.go`
- Test files use the standard Go suffix: `handlers_test.go`, `repo_test.go`, `queue_test.go`, `convert_test.go`, `storage_test.go`
- One file per responsibility within a package, e.g. `internal/convert/`: `convert.go` (abstraction + registry), `exec.go` (hardened process exec), `libvips.go` (concrete converter), `converters.go` (registration wiring)
- Package name matches directory name exactly (`package api` in `internal/api/`, `package jobs` in `internal/jobs/`, etc.)
- Exported functions/methods: `PascalCase` — `NewServer`, `NewRepo`, `HandleImageConvert`, `PresignGet`, `NormalizeFormat`
- Unexported helpers: `camelCase` — `writeJSON`, `writeError`, `runCommand`, `contentTypeFor`, `envInt64`, `firstField`, `deref`, `contains`
- Handler methods on `*Server` follow `handle<Noun>` — `handleHealth`, `handleCreateJob`, `handleGetJob` (`internal/api/handlers.go`)
- Constructors are `New<Type>` — `NewServer`, `NewClient`, `NewRepo`, `NewHandler`, `NewRegistry` (never `New()` alone except within an already-scoped package like `storage.New`)
- Short receiver names, one or two letters, consistent per type: `s *Server`, `r *Repo`, `c *Client`, `h *Handler` (`internal/api/handlers.go`, `internal/jobs/repo.go`, `internal/storage/storage.go`, `internal/worker/worker.go`)
- `ctx` always first parameter, always named `ctx` (never `c` — that's reserved for receivers/clients)
- Loop/error idioms: `err` reused per-scope, not renamed (`err := ...; if err != nil`)
- Local pointer-to-string scratch vars for nullable DB columns are abbreviated: `src, tgt, code, msg *string` (`internal/jobs/repo.go:123`)
- Structs: `PascalCase` nouns — `Server`, `Config`, `Client`, `Job`, `Input`, `Output`, `CreateParams`, `ConvertPayload`, `Pair`, `Registry`, `Handler`
- Interfaces: `PascalCase`, named for role not "I" prefix — `Converter`, `Repo`, `Storage`, `Enqueuer` (`internal/api/api.go`, `internal/convert/convert.go`)
- Errors: package-level `var Err<Reason>` — `ErrNotFound` (`internal/jobs/repo.go:14`)
- String constants for enum-like values instead of typed enums — `StatusQueued`, `StatusActive`, etc. as untyped `string` consts (`internal/jobs/jobs.go:13-20`); comment ties them back to the DB CHECK constraint
## Code Style
- Standard `gofmt` formatting throughout; verified clean (`gofmt -l .` reports no files)
- Tabs for indentation (Go default)
- No custom formatter config (no `.editorconfig`, no non-default `gofmt` flags)
- No `.golangci.yml` or other linter config present in the repo
- No Makefile or CI workflow (`.github/workflows`) wiring lint/test/build — rely on `go build`, `go vet`, `go test` run manually
- Code passes `go vet ./...` cleanly; treat this as the enforced minimum bar for new code
## Import Organization
- None. Full module path `github.com/apaderin/octoconv/internal/<pkg>` is always used; no import aliasing observed.
## Error Handling
- Wrap errors with `fmt.Errorf("<action>: %w", err)` to preserve context and chain — used consistently in `internal/jobs/repo.go`, `internal/storage/storage.go`, `internal/db/db.go`, `internal/worker/worker.go`
- Sentinel errors declared at package level and checked with `errors.Is`: `jobs.ErrNotFound` checked via `errors.Is(err, jobs.ErrNotFound)` (`internal/api/handlers.go:129`) and `errors.Is(err, pgx.ErrNoRows)` (`internal/jobs/repo.go:130`)
- Typed error unwrap with `errors.As` for framework-specific errors, e.g. `http.MaxBytesError` (`internal/api/handlers.go:38-42`)
- HTTP layer never leaks internal error text to clients — handlers always map errors to a short fixed `writeError(w, status, "message")` string; the underlying `err` is discarded rather than echoed (`internal/api/handlers.go` throughout)
- Worker layer distinguishes retryable vs terminal failures using `asynq.SkipRetry`: wrap with `fmt.Errorf("%w: %v", asynq.SkipRetry, err)` when a retry cannot help (unparseable payload, illegal state transition) (`internal/worker/worker.go:44,55`)
- Repository "guarded transitions" return a plain (non-wrapped, non-sentinel) `fmt.Errorf("illegal transition %s -> %s for job %s", ...)` for invalid state changes — callers treat any transition error as terminal/non-retryable (`internal/jobs/repo.go:219`)
- DB writes that must be atomic use `pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {...})` — a single closure returning error triggers automatic rollback; success returns `nil` (`internal/jobs/repo.go:47-72`, `:207-233`)
- Never use `panic` for control flow anywhere in `internal/`; the only recovery mechanism is chi's `middleware.Recoverer` at the HTTP boundary (`internal/api/routes.go:14`)
## Logging
- Only `cmd/*/main.go` log directly, via `log.Printf` / `log.Fatalf` / `log.Println` — library code (`internal/*`) never logs, it only returns errors
- Startup/shutdown messages use an emoji prefix for scannability: `"🚀 API listening on %s"`, `"🛑 shutting down API..."`, `"bye 👋"`, `"🐙 image worker starting (queue=%s)"` (`cmd/api/main.go`, `cmd/worker/main.go`)
- `log.Fatalf` only at startup for unrecoverable init errors (DB connect, migrate, storage init, queue init) — never inside request/task handling paths
- chi's `middleware.Logger` provides per-request access logging in the API (`internal/api/routes.go:13`); there is no equivalent structured request logging in the worker
## Comments
- Every package has a package-level doc comment on exactly one file explaining its role in the system, e.g. `// Package jobs is the Postgres-backed repository for conversion jobs...` (`internal/jobs/jobs.go:1-3`), `// Package worker contains the asynq task handlers...` (`internal/worker/worker.go:1`)
- Exported types/functions get a one-to-few-sentence doc comment starting with the identifier name, per Go convention: `// Repo is the jobs repository backed by a pgx pool.` (`internal/jobs/repo.go:16`)
- Non-obvious "why" decisions get inline comments near the code, not just doc comments — e.g. why the process group is killed (`internal/convert/exec.go:11-18`), why validation happens before storage write (`internal/api/handlers.go:67`), why JSON escaping is disabled (`internal/api/handlers.go:174`)
- Comments explicitly call out architectural intent that isn't visible from code alone, e.g. "Postgres-first double write" (`internal/api/handlers.go:83`, `internal/jobs/repo.go:40`)
## Function Design
## Module Design
<!-- GSD:conventions-end -->

<!-- GSD:architecture-start source:ARCHITECTURE.md -->
## Architecture

## System Overview
```text
```
## Component Responsibilities
| Component | Responsibility | File |
|-----------|----------------|------|
| API server | HTTP routing, multipart parsing, request validation | `internal/api/routes.go`, `internal/api/handlers.go` |
| API dependencies | Interfaces (Repo, Storage, Enqueuer) decoupling handlers from concrete implementations | `internal/api/api.go` |
| Jobs repository | Postgres CRUD + guarded status transitions + event log | `internal/jobs/repo.go` |
| Jobs domain types | Job/Input/Output structs, status constants | `internal/jobs/jobs.go` |
| Queue client (producer) | Wraps asynq.Client, builds/enqueues tasks | `internal/queue/client.go`, `internal/queue/queue.go` |
| Worker handler (consumer) | asynq task handler: orchestrates download → convert → upload → status update | `internal/worker/worker.go` |
| Converter registry | Maps (source, target) format pairs to a `Converter` implementation | `internal/convert/convert.go` |
| Converter implementations | Concrete engines (currently libvips) | `internal/convert/converters.go`, `internal/convert/libvips.go` |
| Hardened process exec | Runs external CLI engines with process-group kill on timeout | `internal/convert/exec.go` |
| Storage client | S3/MinIO wrapper: upload, download, presigned URLs | `internal/storage/storage.go` |
| Storage key builder | Deterministic object key layout for uploads/results | `internal/storage/keys.go` |
| DB pool + migrations | pgx pool connection, embedded SQL migration runner | `internal/db/db.go`, `internal/db/migrations/0001_init.sql` |
| API entry point | Wires dependencies, starts HTTP server with graceful shutdown | `cmd/api/main.go` |
| Worker entry point | Wires dependencies, starts asynq server | `cmd/worker/main.go` |
| Migrate entry point | One-shot migration runner CLI | `cmd/migrate/main.go` |
## Pattern Overview
- **Engine-class queue routing** — asynq queues are named per engine class (`image`, and future `document`/`av`/etc.), so worker pools can scale independently per engine; the task payload carries only a `job_id`, all details are re-read from Postgres (`internal/queue/queue.go:14-29`).
- **Postgres-first double write** — the API inserts the job row (status `queued`) inside a transaction *before* enqueuing the asynq task, so a crash before enqueue always leaves an inspectable/recoverable row rather than an orphaned queue message (`internal/api/handlers.go:83-109`).
- **Guarded status transitions** — every job status change goes through `Repo.transition`, which locks the row (`SELECT ... FOR UPDATE`), checks the current status is in an allow-list, applies the update, and appends a `job_events` row — all in one transaction (`internal/jobs/repo.go:197-234`).
- **Converter interface + registry** — engines implement a two-method `Converter` interface (`Pairs()`, `Convert()`); a single process-wide `Registry` (`convert.Default`) maps normalized `(from, to)` pairs to converters, populated via `init()` in `converters.go` so adding an engine is a one-line registration.
- **Hardened external process execution** — every engine invocation goes through `runCommand`, which sets `Setpgid` and kills the whole process group on context cancellation/timeout, preventing orphaned child processes (e.g., LibreOffice's `soffice.bin`) even though only libvips is wired up today (`internal/convert/exec.go`).
## Layers
- Purpose: Accept uploads, validate format-pair support, kick off jobs, report status/results
- Location: `internal/api/`
- Contains: chi router setup (`routes.go`), request handlers (`handlers.go`), dependency interfaces + `Server` struct (`api.go`)
- Depends on: `internal/jobs` (via `Repo` interface), `internal/storage` (via `Storage` interface), `internal/queue` (via `Enqueuer` interface), `internal/convert` (format validation only)
- Used by: `cmd/api/main.go`
- Purpose: Own job lifecycle state and persistence; the single source of truth for job status
- Location: `internal/jobs/`
- Contains: domain types (`Job`, `Input`, `Output`, status constants) and a pgx-backed `Repo` with guarded transitions
- Depends on: `github.com/jackc/pgx/v5`
- Used by: `internal/api`, `internal/worker`, `cmd/api/main.go`, `cmd/worker/main.go`
- Purpose: Define asynq task types/payloads and provide a typed producer client
- Location: `internal/queue/`
- Contains: task type/queue name constants, `ConvertPayload`, `Client` (producer wrapper), `RedisOpt()` helper
- Depends on: `github.com/hibiken/asynq`
- Used by: `internal/api` (producer, via `Enqueuer` interface), `internal/worker` (payload parsing), `cmd/api`, `cmd/worker`
- Purpose: Orchestrate the end-to-end conversion pipeline for one job: mark active → download input → run converter → upload output → record output → mark done/failed
- Location: `internal/worker/`
- Contains: `Handler` bound to an asynq `ServeMux` handler function (`HandleImageConvert`)
- Depends on: `internal/jobs`, `internal/storage`, `internal/convert`, `internal/queue` (payload parsing)
- Used by: `cmd/worker/main.go`
- Purpose: Abstract "convert file A to file B" behind a common interface; concrete engines shell out to CLI tools
- Location: `internal/convert/`
- Contains: `Converter` interface + `Registry` (`convert.go`), engine registration (`converters.go`), hardened process runner (`exec.go`), libvips implementation (`libvips.go`)
- Depends on: `os/exec`, `syscall` (process group control)
- Used by: `internal/worker`, `internal/api` (format-pair validation only)
- Purpose: Abstract the S3-compatible object store (MinIO in this deployment) for uploads/downloads/presigned URLs
- Location: `internal/storage/`
- Contains: `Client` wrapper (`storage.go`), deterministic object-key builders (`keys.go`)
- Depends on: `github.com/minio/minio-go/v7`
- Used by: `internal/api`, `internal/worker`, `cmd/api`, `cmd/worker`
- Purpose: Own the Postgres connection pool and embedded SQL migrations
- Location: `internal/db/`
- Contains: `Connect()`, `Migrate()`, embedded `migrations/*.sql` (currently `0001_init.sql`)
- Depends on: `github.com/jackc/pgx/v5/pgxpool`, `embed`
- Used by: `cmd/api`, `cmd/worker`, `cmd/migrate`
## Data Flow
### Primary Request Path (create job → convert → download result)
- All durable state lives in Postgres (`jobs`, `job_inputs`, `job_outputs`, `job_events` tables — `internal/db/migrations/0001_init.sql`); no in-memory job state is shared between API and worker processes.
- Job status transitions are guarded and idempotent via row-level locking (`SELECT ... FOR UPDATE`) plus an allow-list of valid source statuses (`internal/jobs/repo.go:200-234`); illegal transitions return an error rather than silently overwriting state, and the worker treats "already active/done" as `asynq.SkipRetry` rather than an infinite retry loop (`internal/worker/worker.go:53-56`).
- Redis/asynq holds only transient queue state (the task payload with a job id); it is not consulted for job status.
## Key Abstractions
- Purpose: Represents "convert a file from one format to another by invoking an external engine"
- Examples: `internal/convert/libvips.go` (`LibvipsConverter`)
- Pattern: Two methods — `Pairs() []Pair` (self-describes supported conversions) and `Convert(ctx, inPath, outPath, opts) error`. New engines are added by implementing this interface and registering an instance in `internal/convert/converters.go:init()`.
- Purpose: Runtime lookup table from normalized `(source, target)` format pair to the `Converter` that handles it
- Examples: `convert.Default` (process-wide singleton), `internal/convert/convert.go:42-72`
- Pattern: `Register(c Converter)` iterates `c.Pairs()` and indexes each pair; `Lookup`/`Supports` normalize inputs via `NormalizeFormat` before matching, so callers never need to worry about case or aliasing (`jpeg`→`jpg`, `tif`→`tiff`).
- Purpose: Decouple `internal/api` from concrete `internal/jobs`, `internal/storage`, `internal/queue` implementations for testability
- Examples: `Repo`, `Storage`, `Enqueuer` interfaces in `internal/api/api.go:16-31`
- Pattern: Each interface declares only the subset of methods the consuming package actually calls (interface segregation), not the full concrete type's method set. `internal/worker/worker.go` instead depends on concrete `*jobs.Repo` / `*storage.Client` / `*convert.Registry` types directly (no interface abstraction there).
- Purpose: Single choke point for every job status change, ensuring the state machine (`queued → active → done|failed`) is enforced consistently and every change is logged
- Examples: `Repo.transition` in `internal/jobs/repo.go:200-234`, called by `MarkActive`, `MarkDone`, `MarkFailed`
- Pattern: Takes the target status, an allow-list of valid source statuses, and a closure that performs the actual `UPDATE` inside the same transaction as the row lock and event-log insert.
- Purpose: Deterministic, collision-free S3 key layout tying storage objects to job id + ordinal
- Examples: `storage.InputKey`, `storage.OutputKey` in `internal/storage/keys.go`
- Pattern: `uploads/{job_id}/{ordinal}-{filename}` and `results/{job_id}/{ordinal}-{filename}`; ordinal supports future multi-input/multi-output jobs (batch operations) without a key format change.
## Entry Points
- Location: `cmd/api/main.go`
- Triggers: Process start; listens on `API_ADDR` (default `:8080`)
- Responsibilities: Connect to Postgres and run migrations, construct storage/queue clients, wire `api.NewServer`, start `net/http.Server`, handle `SIGINT`/`SIGTERM` for graceful shutdown (15s timeout)
- Location: `cmd/worker/main.go`
- Triggers: Process start; connects to Postgres, MinIO, Redis
- Responsibilities: Build `worker.Handler` with `convert.Default` registry and `ENGINE_TIMEOUT`, register it on an `asynq.ServeMux` for `queue.TypeImageConvert`, run an `asynq.Server` bound to the `image` queue with `WORKER_CONCURRENCY` concurrency (asynq handles its own graceful shutdown internally on SIGINT/SIGTERM)
- Location: `cmd/migrate/main.go`
- Triggers: Manual/CI invocation (`go run ./cmd/migrate`)
- Responsibilities: Connect to Postgres, apply all pending embedded SQL migrations, exit
## Architectural Constraints
- **Threading:** Each process is a standard Go program — the API uses `net/http`'s goroutine-per-request model; the worker uses asynq's internal goroutine pool sized by `WORKER_CONCURRENCY` (default 4, set in `cmd/worker/main.go:47`). No custom threading/worker-pool code exists outside asynq.
- **Global state:** `convert.Default` is a package-level singleton `*Registry` populated via `init()` in `internal/convert/converters.go` — the only global mutable-at-init state in the codebase. All other dependencies (DB pool, storage client, queue client) are constructed in `main()` and passed explicitly (no other singletons/globals).
- **Circular imports:** None observed. Dependency direction is strictly `cmd/* → internal/{api,worker} → internal/{jobs,storage,queue,convert} → internal/db` (db has no internal dependents besides being called from `cmd/`).
- **Single-input/single-output assumption:** The worker (`internal/worker/worker.go:117-126`) and API (`internal/api/handlers.go`) currently only read/write ordinal `0`; the schema (`job_inputs`, `job_outputs` with an `ordinal` column) supports multiple inputs/outputs per job but no code path populates or consumes more than one yet.
- **Synchronous engine execution:** Conversion runs synchronously inside the asynq handler goroutine (blocking on `os/exec`) bounded by `ENGINE_TIMEOUT` (default 120s) — there is no separate process pool or engine sandboxing beyond `Setpgid` + SIGKILL on timeout (`internal/convert/exec.go`).
- **Environment-variable configuration only:** No config file support; every runtime setting (`DATABASE_URL`, `REDIS_ADDR`, `S3_*`, `API_ADDR`, `MAX_UPLOAD_BYTES`, `WORKER_CONCURRENCY`, `ENGINE_TIMEOUT`) is read from `os.Getenv` in `cmd/*/main.go` and `internal/{db,queue,storage}`.
## Anti-Patterns
### Bypassing the guarded transition helper
### Adding an engine without going through the Converter interface
## Error Handling
- HTTP handlers translate errors to status codes explicitly via `writeError(w, status, msg)` — no generic error middleware or panic-to-500 mapping beyond chi's `middleware.Recoverer` (`internal/api/routes.go:14`, `internal/api/handlers.go:170-181`).
- Worker errors distinguish retryable vs. non-retryable failures via `asynq.SkipRetry` wrapping: unparseable payloads and illegal state transitions are wrapped with `%w: %v` against `asynq.SkipRetry` so asynq drops them instead of retrying forever (`internal/worker/worker.go:44,55`); genuine engine failures are returned unwrapped so asynq's default retry policy applies, and the job is marked `failed` in Postgres first (`internal/worker/worker.go:58-61`).
- Storage/DB errors are always wrapped with the operation and key/id for diagnosability (e.g., `fmt.Errorf("upload %q: %w", key, err)` in `internal/storage/storage.go:61`).
## Cross-Cutting Concerns
<!-- GSD:architecture-end -->

<!-- GSD:skills-start source:skills/ -->
## Project Skills

No project skills found. Add skills to any of: `.claude/skills/`, `.agents/skills/`, `.cursor/skills/`, `.github/skills/`, or `.codex/skills/` with a `SKILL.md` index file.
<!-- GSD:skills-end -->

<!-- GSD:workflow-start source:GSD defaults -->
## GSD Workflow Enforcement

Before using Edit, Write, or other file-changing tools, start work through a GSD command so planning artifacts and execution context stay in sync.

Use these entry points:
- `/gsd-quick` for small fixes, doc updates, and ad-hoc tasks
- `/gsd-debug` for investigation and bug fixing
- `/gsd-execute-phase` for planned phase work

Do not make direct repo edits outside a GSD workflow unless the user explicitly asks to bypass it.
<!-- GSD:workflow-end -->



<!-- GSD:profile-start -->
## Developer Profile

> Profile not yet configured. Run `/gsd-profile-user` to generate your developer profile.
> This section is managed by `generate-claude-profile` -- do not edit manually.
<!-- GSD:profile-end -->
