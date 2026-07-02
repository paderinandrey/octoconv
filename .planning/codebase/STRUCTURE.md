# Codebase Structure

**Analysis Date:** 2026-07-02

## Directory Layout

```
octoconv/
├── cmd/                        # Executable entry points (one main package per binary)
│   ├── api/
│   │   └── main.go             # HTTP API process
│   ├── worker/
│   │   └── main.go             # asynq image-conversion worker process
│   └── migrate/
│       └── main.go             # One-shot DB migration runner
├── internal/                   # Application code, not importable outside this module
│   ├── api/                    # HTTP layer: router, handlers, dependency interfaces
│   │   ├── api.go              # Server struct, Config, Repo/Storage/Enqueuer interfaces
│   │   ├── routes.go           # chi router + middleware wiring
│   │   ├── handlers.go         # handleCreateJob, handleGetJob, handleHealth
│   │   └── handlers_test.go
│   ├── convert/                # Converter interface, registry, engine implementations
│   │   ├── convert.go          # Pair, Converter interface, Registry, NormalizeFormat
│   │   ├── converters.go       # init() wiring concrete converters into Default registry
│   │   ├── exec.go             # runCommand: hardened os/exec with process-group kill
│   │   ├── libvips.go          # LibvipsConverter (image engine)
│   │   └── convert_test.go
│   ├── jobs/                   # Postgres-backed job repository (system of record)
│   │   ├── jobs.go             # Domain types (Job, Input, Output) + status constants
│   │   ├── repo.go             # Repo: Create, MarkActive/Done/Failed, Get, Inputs, Outputs
│   │   └── repo_test.go
│   ├── queue/                  # asynq task types, payload, producer client
│   │   ├── queue.go            # Task/queue name constants, ConvertPayload, RedisOpt
│   │   ├── client.go           # Client (asynq.Client wrapper), EnqueueImageConvert
│   │   └── queue_test.go
│   ├── storage/                # S3/MinIO client wrapper
│   │   ├── storage.go          # Client: New, Upload, Download, PresignGet
│   │   ├── keys.go             # InputKey/OutputKey object-key builders
│   │   └── storage_test.go
│   ├── worker/                 # asynq task handler: orchestrates the conversion pipeline
│   │   └── worker.go           # Handler.HandleImageConvert + process/download/upload helpers
│   └── db/                     # Postgres pool + embedded migration runner
│       ├── db.go                # Connect, Migrate
│       └── migrations/
│           └── 0001_init.sql   # Full schema DDL (clients, presets, jobs, job_inputs,
│                                #   job_outputs, job_events, webhook_deliveries)
├── bin/                        # Local build output (gitignored; contains compiled `api`)
├── .claude/                    # GSD tooling (agents, commands, skills config) — not app code
├── .planning/                  # GSD planning artifacts (this document lives here)
├── docker-compose.yml          # postgres, redis, minio, createbucket, api, worker services
├── Dockerfile.api               # Multi-stage build: Go binary → debian-slim (no engine deps)
├── Dockerfile.worker             # Multi-stage build: Go binary → debian-slim + libvips-tools
├── go.mod / go.sum              # Module: github.com/apaderin/octoconv, Go 1.26.4
├── .env.example                  # Documented env vars (copy to .env for local dev)
└── README.md                     # Setup, stack, config reference (Russian-language)
```

## Directory Purposes

**`cmd/`:**
- Purpose: One subdirectory per compiled binary; each `main.go` only wires dependencies (constructors from `internal/*`) and starts/stops the process
- Contains: `main` packages, no business logic
- Key files: `cmd/api/main.go`, `cmd/worker/main.go`, `cmd/migrate/main.go`

**`internal/api/`:**
- Purpose: HTTP transport layer — turns HTTP requests into calls against `jobs.Repo`, `storage.Client`, and `queue.Client` via narrow local interfaces
- Contains: chi routing, multipart handling, JSON response writing
- Key files: `internal/api/handlers.go` (business-facing logic), `internal/api/api.go` (interfaces + `Server`)

**`internal/convert/`:**
- Purpose: Abstracts "convert file format A to format B" behind the `Converter` interface; owns the registry of supported format pairs and all external-process execution
- Contains: interface + registry (`convert.go`), engine registration (`converters.go`), process hardening helper (`exec.go`), one implementation per engine (`libvips.go`)
- Key files: `internal/convert/convert.go` (core abstraction), `internal/convert/exec.go` (reusable process runner for future engines)

**`internal/jobs/`:**
- Purpose: Sole owner of job persistence and the job status state machine; Postgres is the system of record
- Contains: domain types, guarded transition logic, SQL queries (raw SQL via pgx, no ORM)
- Key files: `internal/jobs/repo.go`

**`internal/queue/`:**
- Purpose: Defines the asynq task contract (type name, queue name, payload shape) shared by producer (API) and consumer (worker)
- Contains: constants, `ConvertPayload`, `Client` (producer wrapper), `RedisOpt()` env parsing
- Key files: `internal/queue/queue.go` (contract), `internal/queue/client.go` (producer)

**`internal/storage/`:**
- Purpose: Wraps the MinIO/S3 SDK behind a small `Client` type; centralizes object-key naming conventions
- Contains: upload/download/presign methods, key builders
- Key files: `internal/storage/storage.go`, `internal/storage/keys.go`

**`internal/worker/`:**
- Purpose: The asynq consumer — orchestrates the full per-job pipeline by composing `jobs.Repo`, `storage.Client`, and `convert.Registry`
- Contains: a single `Handler` type bound as an asynq handler function
- Key files: `internal/worker/worker.go`

**`internal/db/`:**
- Purpose: Owns the Postgres connection pool lifecycle and applies embedded SQL migrations idempotently
- Contains: `Connect`/`Migrate` functions, `migrations/*.sql` embedded via `go:embed`
- Key files: `internal/db/db.go`, `internal/db/migrations/0001_init.sql`

**`bin/`:**
- Purpose: Local compiled binary output (e.g., from `go build -o bin/api ./cmd/api`)
- Generated: Yes
- Committed: No (gitignored via `/bin/`)

## Key File Locations

**Entry Points:**
- `cmd/api/main.go`: HTTP API process bootstrap and graceful shutdown
- `cmd/worker/main.go`: asynq worker process bootstrap
- `cmd/migrate/main.go`: standalone migration CLI

**Configuration:**
- `.env.example`: documented environment variables (copy to `.env`, never commit `.env`)
- `docker-compose.yml`: per-service environment variables for containerized runs
- Environment variables are read directly via `os.Getenv` in `cmd/*/main.go`, `internal/db/db.go`, `internal/queue/queue.go`, `internal/storage/storage.go` — there is no central config struct/package

**Core Logic:**
- `internal/api/handlers.go`: job creation and status endpoints
- `internal/worker/worker.go`: end-to-end conversion pipeline
- `internal/jobs/repo.go`: job state machine
- `internal/convert/convert.go` + `internal/convert/libvips.go`: conversion engine abstraction and implementation

**Testing:**
- `internal/api/handlers_test.go`, `internal/convert/convert_test.go`, `internal/jobs/repo_test.go`, `internal/queue/queue_test.go`, `internal/storage/storage_test.go` — one `_test.go` file per package, co-located with the code under test

## Naming Conventions

**Files:**
- One file per cohesive responsibility within a package, lowercase snake-free names (`api.go`, `routes.go`, `handlers.go`, `keys.go`) — Go convention, no dashes/underscores except in test suffix
- Test files mirror the file/package they cover with a `_test.go` suffix, e.g. `handlers.go` → `handlers_test.go`; package-level test files use `{package}_test.go` when testing the package as a whole (`convert_test.go`, `queue_test.go`, `storage_test.go`)
- SQL migrations: `NNNN_description.sql` (4-digit zero-padded sequence), e.g. `0001_init.sql`, applied in lexical order and tracked in a `schema_migrations` table

**Directories:**
- `internal/{noun}/` — one directory per bounded responsibility/package, named after the domain noun it owns (`jobs`, `queue`, `storage`, `convert`, `worker`, `api`, `db`), not by technical layer (no `services/`, `controllers/`, `models/` split)
- `cmd/{binary-name}/` — one directory per compiled binary, directory name matches the resulting binary name

**Go identifiers:**
- Exported types/functions use PascalCase (`Repo`, `NewServer`, `EnqueueImageConvert`); unexported helpers use camelCase (`writeJSON`, `contentTypeFor`, `firstField`)
- Interfaces are named for the role they play from the consumer's perspective (`Repo`, `Storage`, `Enqueuer` in `internal/api/api.go`), not suffixed with `Interface`
- Status/constant groups use a common prefix (`StatusQueued`, `StatusActive`, `StatusDone` in `internal/jobs/jobs.go`; `TypeImageConvert`, `QueueImage` in `internal/queue/queue.go`)

## Where to Add New Code

**New conversion engine (e.g., LibreOffice, ffmpeg):**
- Implementation: new file in `internal/convert/` (e.g., `internal/convert/libreoffice.go`) implementing the `Converter` interface (`Pairs()`, `Convert()`), using `runCommand` from `internal/convert/exec.go` for process execution
- Registration: add one line to `internal/convert/converters.go` `init()` — `Default.Register(LibreOfficeConverter{})`
- Tests: add to `internal/convert/convert_test.go` or a new `internal/convert/{engine}_test.go`
- No changes needed in `internal/worker` or `internal/api` — both already look up converters generically via `convert.Default.Supports`/`Lookup`

**New engine class / queue (e.g., a `document` engine class alongside `image`):**
- Queue contract: add task type + queue name constants and a payload/enqueue helper in `internal/queue/queue.go` and `internal/queue/client.go`, following `TypeImageConvert`/`QueueImage`/`EnqueueImageConvert`
- Worker: new `cmd/{class}worker/main.go` (or extend `cmd/worker` if sharing a process) registering a new handler on the `ServeMux`; handler logic goes in a new or extended `internal/worker/` file
- API: extend `internal/api/handlers.go` to route the right engine class based on the detected/requested operation, and enqueue via the new queue helper

**New HTTP endpoint:**
- Route registration: `internal/api/routes.go`
- Handler: `internal/api/handlers.go` (or split into a new file if the package grows large)
- New dependencies the handler needs: extend the `Repo`/`Storage`/`Enqueuer` interfaces in `internal/api/api.go` (interface segregation — add only the methods actually called)

**New database table/column:**
- Add a new migration file `internal/db/migrations/000N_description.sql` (never edit `0001_init.sql` in place); the embedded `migrationsFS` and `Migrate()` in `internal/db/db.go` pick it up automatically by lexical filename order
- Add corresponding query methods to `internal/jobs/repo.go` (or a new `internal/{domain}/repo.go` package if the table is not job-related, e.g., `clients`, `presets`)

**Utilities:**
- Shared helpers currently live inline within the package that needs them (e.g., `firstField`/`envInt`/`envDuration` are duplicated in `cmd/api/main.go` and `cmd/worker/main.go` rather than factored into a shared package) — there is no `internal/pkg/` or `internal/util/` package yet; if a third `cmd/` binary needs the same env-parsing helpers, extract them into a new `internal/envconfig/` (or similar) package rather than copying again

## Special Directories

**`internal/db/migrations/`:**
- Purpose: SQL schema migrations, embedded into the binary via `//go:embed migrations/*.sql` in `internal/db/db.go`
- Generated: No (hand-written SQL)
- Committed: Yes

**`bin/`:**
- Purpose: Local `go build` output directory
- Generated: Yes
- Committed: No (`.gitignore` excludes `/bin/`)

**`.claude/`:**
- Purpose: GSD (get-shit-done) tooling — agents, slash commands, workflows used to plan/execute development, not part of the application
- Generated: No (installed tooling)
- Committed: Yes (tooling config, not app code)

**`.planning/`:**
- Purpose: GSD planning artifacts, including this codebase map (`.planning/codebase/`)
- Generated: Partially (docs like this one are generated by mapping agents)
- Committed: Yes

---

*Structure analysis: 2026-07-02*
