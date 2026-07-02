# Coding Conventions

**Analysis Date:** 2026-07-02

## Naming Patterns

**Files:**
- Lowercase, no underscores/hyphens: `handlers.go`, `routes.go`, `keys.go`, `libvips.go`, `converters.go`
- Test files use the standard Go suffix: `handlers_test.go`, `repo_test.go`, `queue_test.go`, `convert_test.go`, `storage_test.go`
- One file per responsibility within a package, e.g. `internal/convert/`: `convert.go` (abstraction + registry), `exec.go` (hardened process exec), `libvips.go` (concrete converter), `converters.go` (registration wiring)
- Package name matches directory name exactly (`package api` in `internal/api/`, `package jobs` in `internal/jobs/`, etc.)

**Functions:**
- Exported functions/methods: `PascalCase` — `NewServer`, `NewRepo`, `HandleImageConvert`, `PresignGet`, `NormalizeFormat`
- Unexported helpers: `camelCase` — `writeJSON`, `writeError`, `runCommand`, `contentTypeFor`, `envInt64`, `firstField`, `deref`, `contains`
- Handler methods on `*Server` follow `handle<Noun>` — `handleHealth`, `handleCreateJob`, `handleGetJob` (`internal/api/handlers.go`)
- Constructors are `New<Type>` — `NewServer`, `NewClient`, `NewRepo`, `NewHandler`, `NewRegistry` (never `New()` alone except within an already-scoped package like `storage.New`)

**Variables:**
- Short receiver names, one or two letters, consistent per type: `s *Server`, `r *Repo`, `c *Client`, `h *Handler` (`internal/api/handlers.go`, `internal/jobs/repo.go`, `internal/storage/storage.go`, `internal/worker/worker.go`)
- `ctx` always first parameter, always named `ctx` (never `c` — that's reserved for receivers/clients)
- Loop/error idioms: `err` reused per-scope, not renamed (`err := ...; if err != nil`)
- Local pointer-to-string scratch vars for nullable DB columns are abbreviated: `src, tgt, code, msg *string` (`internal/jobs/repo.go:123`)

**Types:**
- Structs: `PascalCase` nouns — `Server`, `Config`, `Client`, `Job`, `Input`, `Output`, `CreateParams`, `ConvertPayload`, `Pair`, `Registry`, `Handler`
- Interfaces: `PascalCase`, named for role not "I" prefix — `Converter`, `Repo`, `Storage`, `Enqueuer` (`internal/api/api.go`, `internal/convert/convert.go`)
- Errors: package-level `var Err<Reason>` — `ErrNotFound` (`internal/jobs/repo.go:14`)
- String constants for enum-like values instead of typed enums — `StatusQueued`, `StatusActive`, etc. as untyped `string` consts (`internal/jobs/jobs.go:13-20`); comment ties them back to the DB CHECK constraint

## Code Style

**Formatting:**
- Standard `gofmt` formatting throughout; verified clean (`gofmt -l .` reports no files)
- Tabs for indentation (Go default)
- No custom formatter config (no `.editorconfig`, no non-default `gofmt` flags)

**Linting:**
- No `.golangci.yml` or other linter config present in the repo
- No Makefile or CI workflow (`.github/workflows`) wiring lint/test/build — rely on `go build`, `go vet`, `go test` run manually
- Code passes `go vet ./...` cleanly; treat this as the enforced minimum bar for new code

## Import Organization

**Order:**
Every file with mixed stdlib/third-party/internal imports groups them in three blocks, separated by a blank line, in this order:
1. Standard library (`context`, `encoding/json`, `net/http`, ...)
2. Third-party packages (`github.com/go-chi/chi/v5`, `github.com/google/uuid`, `github.com/jackc/pgx/v5`, ...)
3. Internal packages (`github.com/apaderin/octoconv/internal/...`)

Example (`internal/api/handlers.go:3-16`):
```go
import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/storage"
)
```

**Path Aliases:**
- None. Full module path `github.com/apaderin/octoconv/internal/<pkg>` is always used; no import aliasing observed.

## Error Handling

**Patterns:**
- Wrap errors with `fmt.Errorf("<action>: %w", err)` to preserve context and chain — used consistently in `internal/jobs/repo.go`, `internal/storage/storage.go`, `internal/db/db.go`, `internal/worker/worker.go`
- Sentinel errors declared at package level and checked with `errors.Is`: `jobs.ErrNotFound` checked via `errors.Is(err, jobs.ErrNotFound)` (`internal/api/handlers.go:129`) and `errors.Is(err, pgx.ErrNoRows)` (`internal/jobs/repo.go:130`)
- Typed error unwrap with `errors.As` for framework-specific errors, e.g. `http.MaxBytesError` (`internal/api/handlers.go:38-42`)
- HTTP layer never leaks internal error text to clients — handlers always map errors to a short fixed `writeError(w, status, "message")` string; the underlying `err` is discarded rather than echoed (`internal/api/handlers.go` throughout)
- Worker layer distinguishes retryable vs terminal failures using `asynq.SkipRetry`: wrap with `fmt.Errorf("%w: %v", asynq.SkipRetry, err)` when a retry cannot help (unparseable payload, illegal state transition) (`internal/worker/worker.go:44,55`)
- Repository "guarded transitions" return a plain (non-wrapped, non-sentinel) `fmt.Errorf("illegal transition %s -> %s for job %s", ...)` for invalid state changes — callers treat any transition error as terminal/non-retryable (`internal/jobs/repo.go:219`)
- DB writes that must be atomic use `pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {...})` — a single closure returning error triggers automatic rollback; success returns `nil` (`internal/jobs/repo.go:47-72`, `:207-233`)
- Never use `panic` for control flow anywhere in `internal/`; the only recovery mechanism is chi's `middleware.Recoverer` at the HTTP boundary (`internal/api/routes.go:14`)

## Logging

**Framework:** Standard library `log` package only — no structured/leveled logging framework.

**Patterns:**
- Only `cmd/*/main.go` log directly, via `log.Printf` / `log.Fatalf` / `log.Println` — library code (`internal/*`) never logs, it only returns errors
- Startup/shutdown messages use an emoji prefix for scannability: `"🚀 API listening on %s"`, `"🛑 shutting down API..."`, `"bye 👋"`, `"🐙 image worker starting (queue=%s)"` (`cmd/api/main.go`, `cmd/worker/main.go`)
- `log.Fatalf` only at startup for unrecoverable init errors (DB connect, migrate, storage init, queue init) — never inside request/task handling paths
- chi's `middleware.Logger` provides per-request access logging in the API (`internal/api/routes.go:13`); there is no equivalent structured request logging in the worker

## Comments

**When to Comment:**
- Every package has a package-level doc comment on exactly one file explaining its role in the system, e.g. `// Package jobs is the Postgres-backed repository for conversion jobs...` (`internal/jobs/jobs.go:1-3`), `// Package worker contains the asynq task handlers...` (`internal/worker/worker.go:1`)
- Exported types/functions get a one-to-few-sentence doc comment starting with the identifier name, per Go convention: `// Repo is the jobs repository backed by a pgx pool.` (`internal/jobs/repo.go:16`)
- Non-obvious "why" decisions get inline comments near the code, not just doc comments — e.g. why the process group is killed (`internal/convert/exec.go:11-18`), why validation happens before storage write (`internal/api/handlers.go:67`), why JSON escaping is disabled (`internal/api/handlers.go:174`)
- Comments explicitly call out architectural intent that isn't visible from code alone, e.g. "Postgres-first double write" (`internal/api/handlers.go:83`, `internal/jobs/repo.go:40`)

**JSDoc/TSDoc:** N/A (Go project) — standard Go doc comments only, no tooling beyond `go doc`/`godoc` conventions.

## Function Design

**Size:** Small and single-purpose. HTTP handlers stay under ~40 lines by delegating validation, storage, and persistence to injected dependencies. The largest function in the codebase is `worker.Handler.process` (~50 lines) which is a linear pipeline (download → convert → upload → record → mark done) kept flat rather than split further since each step is a single guarded call.

**Parameters:** `ctx context.Context` always first. Beyond 3-4 parameters, prefer a params struct (`jobs.CreateParams`, `api.Config`) over long positional argument lists. Optional/tunable values are passed via a `Config` struct with zero-value defaults applied in the constructor (`internal/api/api.go:49-55`, `internal/worker/worker.go:30-34`).

**Return Values:** Idiomatic Go `(value, error)` pairs throughout; no exceptions/panics for expected failure modes. Functions that can return "not found" use a sentinel error rather than `(value, bool)`.

## Module Design

**Exports:** Each `internal/<pkg>` exposes a small, intentional interface surface: a primary struct (`Client`, `Repo`, `Server`, `Handler`) with exported methods, plus role-scoped interfaces defined at the *consumer* (not producer) side — e.g. `api.Repo`, `api.Storage`, `api.Enqueuer` in `internal/api/api.go` are minimal interfaces that `jobs.Repo`, `storage.Client`, and `queue.Client` happen to satisfy. This enables the fake-based unit testing pattern (see TESTING.md) without a mocking framework.

**Barrel Files:** Not used (no re-export/index files) — Go doesn't support this pattern; each package is imported by its full path.

**Dependency Injection:** Constructor injection throughout — `NewServer(repo, storage, queue, cfg)`, `NewHandler(repo, store, registry, timeout)`. Dependencies are interfaces defined by the consumer package, concrete types are wired only in `cmd/*/main.go`.

**Registry Pattern:** `internal/convert` uses a runtime registry (`Registry.Register`, `Registry.Lookup`) populated via `init()` in `internal/convert/converters.go`, so new engines/format pairs are added with a single `Default.Register(...)` line without touching calling code.

---

*Convention analysis: 2026-07-02*
