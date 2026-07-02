<!-- refreshed: 2026-07-02 -->
# Architecture

**Analysis Date:** 2026-07-02

## System Overview

```text
┌─────────────────────────────────────────────────────────────┐
│                     HTTP API (cmd/api)                       │
│   chi router · multipart upload · status polling             │
│              `internal/api/*.go`                             │
└───────┬───────────────────┬───────────────────┬──────────────┘
        │                   │                   │
        ▼                   ▼                   ▼
┌───────────────┐  ┌────────────────┐  ┌────────────────────┐
│  storage.Client│  │   jobs.Repo     │  │   queue.Client      │
│  (S3/MinIO)    │  │  (Postgres)     │  │  (asynq/Redis)      │
│ `internal/     │  │ `internal/      │  │ `internal/queue/    │
│  storage/`     │  │  jobs/`         │  │  client.go`          │
└───────┬────────┘  └────────┬────────┘  └──────────┬──────────┘
        │                    │                       │
        │                    │                       ▼
        │                    │           ┌────────────────────────┐
        │                    │           │  Worker (cmd/worker)     │
        │                    │           │  asynq ServeMux          │
        │                    │           │ `internal/worker/        │
        │                    │           │  worker.go`               │
        │                    │           └──────────┬────────────────┘
        │                    │                       │
        │                    │                       ▼
        │                    │           ┌────────────────────────┐
        │                    │           │  convert.Registry        │
        │                    │           │  (Converter interface)   │
        │                    │           │ `internal/convert/`      │
        │                    │           │  → shells out to `vips`  │
        │                    │           └────────────────────────┘
        ▼                    ▼
┌─────────────────────────────────────────────────────────────┐
│      Postgres (system of record) · Redis (queue broker)      │
│           `internal/db/migrations/0001_init.sql`             │
└─────────────────────────────────────────────────────────────┘
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

**Overall:** Two-process, queue-decoupled vertical slice — an HTTP API process and an asynq worker process communicate only through Postgres (system of record) and Redis (job queue), never directly. Within each process, small `internal/` packages are composed via constructor injection (`NewServer`, `NewHandler`, `NewRepo`) and narrow local interfaces (`api.Repo`, `api.Storage`, `api.Enqueuer`) rather than a DI framework.

**Key Characteristics:**
- **Engine-class queue routing** — asynq queues are named per engine class (`image`, and future `document`/`av`/etc.), so worker pools can scale independently per engine; the task payload carries only a `job_id`, all details are re-read from Postgres (`internal/queue/queue.go:14-29`).
- **Postgres-first double write** — the API inserts the job row (status `queued`) inside a transaction *before* enqueuing the asynq task, so a crash before enqueue always leaves an inspectable/recoverable row rather than an orphaned queue message (`internal/api/handlers.go:83-109`).
- **Guarded status transitions** — every job status change goes through `Repo.transition`, which locks the row (`SELECT ... FOR UPDATE`), checks the current status is in an allow-list, applies the update, and appends a `job_events` row — all in one transaction (`internal/jobs/repo.go:197-234`).
- **Converter interface + registry** — engines implement a two-method `Converter` interface (`Pairs()`, `Convert()`); a single process-wide `Registry` (`convert.Default`) maps normalized `(from, to)` pairs to converters, populated via `init()` in `converters.go` so adding an engine is a one-line registration.
- **Hardened external process execution** — every engine invocation goes through `runCommand`, which sets `Setpgid` and kills the whole process group on context cancellation/timeout, preventing orphaned child processes (e.g., LibreOffice's `soffice.bin`) even though only libvips is wired up today (`internal/convert/exec.go`).

## Layers

**HTTP/API layer:**
- Purpose: Accept uploads, validate format-pair support, kick off jobs, report status/results
- Location: `internal/api/`
- Contains: chi router setup (`routes.go`), request handlers (`handlers.go`), dependency interfaces + `Server` struct (`api.go`)
- Depends on: `internal/jobs` (via `Repo` interface), `internal/storage` (via `Storage` interface), `internal/queue` (via `Enqueuer` interface), `internal/convert` (format validation only)
- Used by: `cmd/api/main.go`

**Domain/repository layer:**
- Purpose: Own job lifecycle state and persistence; the single source of truth for job status
- Location: `internal/jobs/`
- Contains: domain types (`Job`, `Input`, `Output`, status constants) and a pgx-backed `Repo` with guarded transitions
- Depends on: `github.com/jackc/pgx/v5`
- Used by: `internal/api`, `internal/worker`, `cmd/api/main.go`, `cmd/worker/main.go`

**Queue layer:**
- Purpose: Define asynq task types/payloads and provide a typed producer client
- Location: `internal/queue/`
- Contains: task type/queue name constants, `ConvertPayload`, `Client` (producer wrapper), `RedisOpt()` helper
- Depends on: `github.com/hibiken/asynq`
- Used by: `internal/api` (producer, via `Enqueuer` interface), `internal/worker` (payload parsing), `cmd/api`, `cmd/worker`

**Worker/consumer layer:**
- Purpose: Orchestrate the end-to-end conversion pipeline for one job: mark active → download input → run converter → upload output → record output → mark done/failed
- Location: `internal/worker/`
- Contains: `Handler` bound to an asynq `ServeMux` handler function (`HandleImageConvert`)
- Depends on: `internal/jobs`, `internal/storage`, `internal/convert`, `internal/queue` (payload parsing)
- Used by: `cmd/worker/main.go`

**Conversion engine layer:**
- Purpose: Abstract "convert file A to file B" behind a common interface; concrete engines shell out to CLI tools
- Location: `internal/convert/`
- Contains: `Converter` interface + `Registry` (`convert.go`), engine registration (`converters.go`), hardened process runner (`exec.go`), libvips implementation (`libvips.go`)
- Depends on: `os/exec`, `syscall` (process group control)
- Used by: `internal/worker`, `internal/api` (format-pair validation only)

**Storage layer:**
- Purpose: Abstract the S3-compatible object store (MinIO in this deployment) for uploads/downloads/presigned URLs
- Location: `internal/storage/`
- Contains: `Client` wrapper (`storage.go`), deterministic object-key builders (`keys.go`)
- Depends on: `github.com/minio/minio-go/v7`
- Used by: `internal/api`, `internal/worker`, `cmd/api`, `cmd/worker`

**Database/infrastructure layer:**
- Purpose: Own the Postgres connection pool and embedded SQL migrations
- Location: `internal/db/`
- Contains: `Connect()`, `Migrate()`, embedded `migrations/*.sql` (currently `0001_init.sql`)
- Depends on: `github.com/jackc/pgx/v5/pgxpool`, `embed`
- Used by: `cmd/api`, `cmd/worker`, `cmd/migrate`

## Data Flow

### Primary Request Path (create job → convert → download result)

1. Client `POST /v1/jobs` with multipart `file` + `target` fields — routed in `internal/api/routes.go:18`
2. `handleCreateJob` parses the multipart form (size-capped via `http.MaxBytesReader`), normalizes `source`/`target` formats, and validates the pair against `convert.Default.Supports` **before** touching storage (`internal/api/handlers.go:32-72`)
3. Generates a `jobID`, uploads the input file to MinIO under `uploads/{job_id}/{ordinal}-{filename}` (`internal/storage/keys.go:12-14`), then inserts the job + input + `queued` event in a single Postgres transaction via `jobs.Repo.Create` (`internal/jobs/repo.go:41-77`)
4. Enqueues an `image:convert` asynq task carrying only the `job_id`, routed to the `image` queue (`internal/queue/queue.go:33-39`, `internal/api/handlers.go:105`)
5. Responds `202 Accepted` with `{"job_id", "status":"queued"}` (`internal/api/handlers.go:111-114`)
6. Worker process picks up the task via `asynq.ServeMux` → `Handler.HandleImageConvert` (`internal/worker/worker.go:40-63`); parses payload, loads the job row, transitions it to `active` (guarded: must currently be `queued`)
7. `Handler.process` downloads the input to a temp dir, looks up the `Converter` for the (source, target) pair, runs it under an `ENGINE_TIMEOUT`-bounded context (`internal/worker/worker.go:65-115`), which shells out to `vips copy` with hardened process-group handling (`internal/convert/libvips.go:30-35`, `internal/convert/exec.go`)
8. Uploads the converted output to `results/{job_id}/{ordinal}-{filename}`, records a `job_outputs` row, and transitions the job to `done` (guarded: must currently be `active`)
9. Client `GET /v1/jobs/{id}` — loads the job, and if status is `done`, presigns a time-limited MinIO download URL for the first output (`internal/api/handlers.go:119-167`, `internal/storage/storage.go:82-88`)

**State Management:**
- All durable state lives in Postgres (`jobs`, `job_inputs`, `job_outputs`, `job_events` tables — `internal/db/migrations/0001_init.sql`); no in-memory job state is shared between API and worker processes.
- Job status transitions are guarded and idempotent via row-level locking (`SELECT ... FOR UPDATE`) plus an allow-list of valid source statuses (`internal/jobs/repo.go:200-234`); illegal transitions return an error rather than silently overwriting state, and the worker treats "already active/done" as `asynq.SkipRetry` rather than an infinite retry loop (`internal/worker/worker.go:53-56`).
- Redis/asynq holds only transient queue state (the task payload with a job id); it is not consulted for job status.

## Key Abstractions

**Converter (interface):**
- Purpose: Represents "convert a file from one format to another by invoking an external engine"
- Examples: `internal/convert/libvips.go` (`LibvipsConverter`)
- Pattern: Two methods — `Pairs() []Pair` (self-describes supported conversions) and `Convert(ctx, inPath, outPath, opts) error`. New engines are added by implementing this interface and registering an instance in `internal/convert/converters.go:init()`.

**Registry:**
- Purpose: Runtime lookup table from normalized `(source, target)` format pair to the `Converter` that handles it
- Examples: `convert.Default` (process-wide singleton), `internal/convert/convert.go:42-72`
- Pattern: `Register(c Converter)` iterates `c.Pairs()` and indexes each pair; `Lookup`/`Supports` normalize inputs via `NormalizeFormat` before matching, so callers never need to worry about case or aliasing (`jpeg`→`jpg`, `tif`→`tiff`).

**Repo interfaces at package boundaries (dependency inversion):**
- Purpose: Decouple `internal/api` from concrete `internal/jobs`, `internal/storage`, `internal/queue` implementations for testability
- Examples: `Repo`, `Storage`, `Enqueuer` interfaces in `internal/api/api.go:16-31`
- Pattern: Each interface declares only the subset of methods the consuming package actually calls (interface segregation), not the full concrete type's method set. `internal/worker/worker.go` instead depends on concrete `*jobs.Repo` / `*storage.Client` / `*convert.Registry` types directly (no interface abstraction there).

**Guarded transition helper:**
- Purpose: Single choke point for every job status change, ensuring the state machine (`queued → active → done|failed`) is enforced consistently and every change is logged
- Examples: `Repo.transition` in `internal/jobs/repo.go:200-234`, called by `MarkActive`, `MarkDone`, `MarkFailed`
- Pattern: Takes the target status, an allow-list of valid source statuses, and a closure that performs the actual `UPDATE` inside the same transaction as the row lock and event-log insert.

**Object key builders:**
- Purpose: Deterministic, collision-free S3 key layout tying storage objects to job id + ordinal
- Examples: `storage.InputKey`, `storage.OutputKey` in `internal/storage/keys.go`
- Pattern: `uploads/{job_id}/{ordinal}-{filename}` and `results/{job_id}/{ordinal}-{filename}`; ordinal supports future multi-input/multi-output jobs (batch operations) without a key format change.

## Entry Points

**HTTP API (`cmd/api/main.go`):**
- Location: `cmd/api/main.go`
- Triggers: Process start; listens on `API_ADDR` (default `:8080`)
- Responsibilities: Connect to Postgres and run migrations, construct storage/queue clients, wire `api.NewServer`, start `net/http.Server`, handle `SIGINT`/`SIGTERM` for graceful shutdown (15s timeout)

**Worker (`cmd/worker/main.go`):**
- Location: `cmd/worker/main.go`
- Triggers: Process start; connects to Postgres, MinIO, Redis
- Responsibilities: Build `worker.Handler` with `convert.Default` registry and `ENGINE_TIMEOUT`, register it on an `asynq.ServeMux` for `queue.TypeImageConvert`, run an `asynq.Server` bound to the `image` queue with `WORKER_CONCURRENCY` concurrency (asynq handles its own graceful shutdown internally on SIGINT/SIGTERM)

**Migrate (`cmd/migrate/main.go`):**
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

None observed as established patterns in this codebase — the code is small, consistently uses interface segregation at package boundaries, and enforces invariants (guarded transitions, Postgres-first writes, hardened process handling) explicitly. Watch points for future growth rather than existing anti-patterns:

### Bypassing the guarded transition helper

**What happens:** Nothing currently bypasses `Repo.transition` — all status changes (`MarkActive`, `MarkDone`, `MarkFailed`) route through it.
**Why it's wrong (if it were to happen):** Any direct `UPDATE jobs SET status = ...` outside `transition` would skip the row lock, allow-list check, and `job_events` append, breaking the auditability and idempotency guarantees the rest of the system relies on.
**Do this instead:** Add new status changes as new `Repo` methods that call `r.transition(...)`, following the pattern in `internal/jobs/repo.go:81-106`.

### Adding an engine without going through the Converter interface

**What happens:** Not present yet (only libvips exists), but the README explicitly earmarks `LibreOfficeConverter`/`FFmpegConverter` as future one-line registrations (`internal/convert/converters.go:7-9`).
**Why it's wrong (if bypassed):** A hand-rolled call to `os/exec` outside `runCommand` would lose the process-group timeout/kill hardening that prevents orphaned child processes.
**Do this instead:** Implement `Converter`, call `runCommand` (or an engine-specific wrapper on top of it) from `Convert()`, and register via `Default.Register(...)` in `converters.go`, exactly as `LibvipsConverter` does.

## Error Handling

**Strategy:** Errors are wrapped with `fmt.Errorf("...: %w", err)` at each layer boundary to preserve context and enable `errors.Is`/`errors.As` checks upstream; sentinel errors (`jobs.ErrNotFound`) are used for expected "not found" conditions.

**Patterns:**
- HTTP handlers translate errors to status codes explicitly via `writeError(w, status, msg)` — no generic error middleware or panic-to-500 mapping beyond chi's `middleware.Recoverer` (`internal/api/routes.go:14`, `internal/api/handlers.go:170-181`).
- Worker errors distinguish retryable vs. non-retryable failures via `asynq.SkipRetry` wrapping: unparseable payloads and illegal state transitions are wrapped with `%w: %v` against `asynq.SkipRetry` so asynq drops them instead of retrying forever (`internal/worker/worker.go:44,55`); genuine engine failures are returned unwrapped so asynq's default retry policy applies, and the job is marked `failed` in Postgres first (`internal/worker/worker.go:58-61`).
- Storage/DB errors are always wrapped with the operation and key/id for diagnosability (e.g., `fmt.Errorf("upload %q: %w", key, err)` in `internal/storage/storage.go:61`).

## Cross-Cutting Concerns

**Logging:** Standard library `log` package only, used at the `cmd/*/main.go` level for lifecycle events (startup, shutdown, fatal errors); no structured logging library. chi's `middleware.Logger` provides per-request HTTP access logs (`internal/api/routes.go:13`).

**Validation:** Format-pair validation happens once, early, in the API handler (`convert.Default.Supports`) before any storage write (`internal/api/handlers.go:68-72`); upload size is enforced via `http.MaxBytesReader` before parsing the multipart form (`internal/api/handlers.go:36`).

**Authentication:** None implemented. No auth middleware, API keys, or client identity checks exist despite the `clients` table already being present in the schema (`internal/db/migrations/0001_init.sql:10-14`) — jobs are not currently associated with a `client_id` by any code path.

---

*Architecture analysis: 2026-07-02*
