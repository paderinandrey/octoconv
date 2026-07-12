# Phase 1: Merge, Auth & Rate Limiting - Pattern Map

**Mapped:** 2026-07-03
**Files analyzed:** 14 (9 new, 5 modified)
**Analogs found:** 12 / 14

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/clients/clients.go` | model | CRUD | `internal/jobs/jobs.go` | exact |
| `internal/clients/repo.go` | model (repo) | CRUD | `internal/jobs/repo.go` | exact |
| `internal/auth/auth.go` | service | request-response | `internal/api/api.go` (interface) + `internal/clients/repo.go` (lookup) | role-match |
| `internal/auth/hash.go` | utility | transform | `internal/storage/keys.go` (pure deterministic helper) | partial |
| `internal/auth/middleware.go` | middleware | request-response | `internal/api/routes.go` (chi chain) | partial (no prior custom middleware) |
| `internal/ratelimit/ratelimit.go` | middleware | request-response | `internal/api/routes.go` + `internal/storage/storage.go` (env-driven constructor) | partial (new dependency, no prior analog) |
| `internal/db/migrations/0002_client_api_keys.sql` | migration | batch/DDL | `internal/db/migrations/0001_init.sql` | exact |
| `cmd/manage-clients/main.go` | config (CLI) | request-response (one-shot) | `cmd/migrate/main.go` | exact |
| `internal/api/routes.go` (modify) | route | request-response | itself (extend per research Pattern 3) | exact |
| `internal/api/api.go` (modify) | service (wiring) | request-response | itself | exact |
| `internal/api/handlers.go` (modify) | controller | request-response | itself | exact |
| `internal/jobs/repo.go` (modify) | model (repo) | CRUD | itself (`Repo.transition`, `Create`, `Get`) | exact |
| `internal/jobs/jobs.go` (modify) | model | CRUD | itself (`CreateParams`, `Job`) | exact |
| `cmd/api/main.go` (modify) | config (entrypoint) | request-response | `cmd/worker/main.go` (env helpers) | exact |

## Pattern Assignments

### `internal/clients/clients.go` (model, CRUD)

**Analog:** `internal/jobs/jobs.go`

**Package doc + domain types pattern** (`internal/jobs/jobs.go:1-20,37-45`):
```go
// Package jobs is the Postgres-backed repository for conversion jobs and their
// inputs, outputs and event log. Postgres is the system of record: status truth
// always lives here.
package jobs

import (
	"time"

	"github.com/google/uuid"
)

// Job statuses (mirror the CHECK constraint in the jobs table).
const (
	StatusAwaitingUpload = "awaiting_upload"
	StatusQueued         = "queued"
	...
)

// Input is a row of the job_inputs table.
type Input struct {
	Ordinal     int
	ObjectKey   string
	...
}
```

**Apply to `internal/clients/clients.go`:**
```go
// Package clients is the Postgres-backed repository for API clients and their
// hashed API keys. Postgres is the system of record: key validity truth always
// lives here.
package clients

import "time"

import "github.com/google/uuid"

// Client is a row of the clients table (subset used by auth + provisioning).
type Client struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}
```
Follow the same "string const instead of typed enum, comment ties back to DB CHECK constraint" convention (`internal/jobs/jobs.go:12-13`) if a key/client status column is added (e.g. `is_active boolean` maps to a plain `bool` field, not an enum — match the schema shape from D-04/D-06).

---

### `internal/clients/repo.go` (model/repo, CRUD)

**Analog:** `internal/jobs/repo.go`

**Imports + repo struct + constructor** (`internal/jobs/repo.go:1-24`):
```go
package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a job does not exist.
var ErrNotFound = errors.New("job not found")

// Repo is the jobs repository backed by a pgx pool.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo wraps a pgx pool.
func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}
```
Apply verbatim to `clients.Repo` — same `ErrNotFound` sentinel-per-package convention, same constructor shape (`NewRepo(pool *pgxpool.Pool) *Repo`).

**Create pattern (single-statement insert, no transaction needed since no
multi-table write)** (`internal/jobs/repo.go:108-118`, `AddOutput` — the closest
single-INSERT example in the codebase):
```go
func (r *Repo) AddOutput(ctx context.Context, jobID uuid.UUID, o Output) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO job_outputs (job_id, ordinal, object_key, filename, format, size_bytes, content_type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		jobID, o.Ordinal, o.ObjectKey, o.Filename, o.Format, o.SizeBytes, o.ContentType)
	if err != nil {
		return fmt.Errorf("insert job_output: %w", err)
	}
	return nil
}
```
For `clients.Repo.Create(ctx, name string) (uuid.UUID, error)`, mirror this shape
(single `pool.Exec`/`QueryRow` with `RETURNING id`, wrapped error `fmt.Errorf("insert client: %w", err)`).

**Guarded key-hash lookup pattern (row lock + allow-list guard)** — reuse the
shape of `transition` for `Revoke`/`RotateKey` since those are also guarded
state changes (`internal/jobs/repo.go:197-234`):
```go
func (r *Repo) transition(
	ctx context.Context,
	id uuid.UUID,
	to string,
	allowedFrom []string,
	apply func(ctx context.Context, tx pgx.Tx) error,
) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		var from string
		if err := tx.QueryRow(ctx,
			`SELECT status FROM jobs WHERE id = $1 FOR UPDATE`, id,
		).Scan(&from); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("lock job: %w", err)
		}
		if !contains(allowedFrom, from) {
			return fmt.Errorf("illegal transition %s -> %s for job %s", from, to, id)
		}
		if err := apply(ctx, tx); err != nil {
			return fmt.Errorf("apply transition: %w", err)
		}
		return nil
	})
}
```
For `clients.Repo.Revoke(ctx, keyID uuid.UUID) error` (D-05: revoke marks the
hash inactive, never deletes the row), use `pgx.BeginFunc` + `SELECT ... FOR
UPDATE` + `UPDATE ... SET revoked_at = now()` in one transaction, same wrapped-
error style. This directly reuses the "Postgres-first, guarded-transition"
code-style precedent flagged in CONTEXT.md's Established Patterns section.

**Get-by-hash lookup pattern (`ErrNotFound` mapping)** (`internal/jobs/repo.go:121-141`):
```go
func (r *Repo) Get(ctx context.Context, id uuid.UUID) (*Job, error) {
	var j Job
	...
	err := r.pool.QueryRow(ctx, `SELECT ... FROM jobs WHERE id = $1`, id,
	).Scan(...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	return &j, nil
}
```
Apply to `clients.Repo.GetByKeyHash(ctx, hash []byte) (*Client, error)` — the
core lookup the auth middleware calls on every request. Query both
`api_key_hash` and `api_key_hash_secondary` columns (D-06: two simultaneously
active hashes support zero-downtime rotation) and filter out revoked rows,
e.g. `WHERE (api_key_hash = $1 OR api_key_hash_secondary = $1) AND revoked_at IS NULL`.

---

### `internal/auth/auth.go` (service, request-response)

**Analog (interface-segregation convention):** `internal/api/api.go:15-31`
```go
// Repo is the subset of the jobs repository the API depends on.
type Repo interface {
	Create(ctx context.Context, p jobs.CreateParams) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (*jobs.Job, error)
	Outputs(ctx context.Context, id uuid.UUID) ([]jobs.Output, error)
}
```
Per CONTEXT.md's Integration Points and ARCHITECTURE.md's "Internal Boundaries"
table: `internal/api` must depend on a narrow `auth.ClientResolver` interface,
not a concrete `*clients.Repo`. Define the interface *in* `internal/auth`
(consumer-owns pattern used by `api.Repo`):
```go
// ClientResolver looks up a client by its API key. Implemented by
// internal/clients.Repo (production) and fakes in tests.
type ClientResolver interface {
	ResolveClient(ctx context.Context, rawKey string) (*clients.Client, error)
}
```
`ResolveClient` hashes `rawKey` (via `auth.HashKey`, see `hash.go` below), then
delegates to `clients.Repo.GetByKeyHash`, converting `clients.ErrNotFound` into
a package-level `auth.ErrInvalidKey` sentinel — same "wrap the repo's not-found
sentinel into a layer-appropriate sentinel" idiom used at
`internal/api/handlers.go:129` (`errors.Is(err, jobs.ErrNotFound)` →
`writeError(w, http.StatusNotFound, ...)`).

---

### `internal/auth/hash.go` (utility, transform)

**No direct analog in the codebase** (no existing crypto/hashing code) — this
is genuinely new. Follow package conventions from `internal/storage/keys.go`
(pure, dependency-free helper functions, no state) for *style* only:
```go
// storage/keys.go convention: small, pure, deterministic helper functions
// grouped in their own file within the owning package.
```
Per STACK.md (D-07, locked): use `crypto/sha256` + `crypto/subtle.ConstantTimeCompare`,
not bcrypt/argon2:
```go
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
)

// GenerateKey returns a new high-entropy raw API key (base64url, 32 bytes of
// entropy) and never stores/logs it beyond the single caller that prints it.
func GenerateKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate key: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashKey returns the hex-encoded SHA-256 digest of a raw key, for storage/
// lookup. Never store or log the raw key itself.
func HashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
```
Note: `HashKey` is deterministic (no per-key salt) because it is only ever
used as a DB lookup key, not compared byte-by-byte in application code —
Postgres equality on the hash column is the comparison; `subtle.ConstantTimeCompare`
is only needed if the middleware ever compares two hash values directly in Go
(e.g. defense-in-depth double-check) rather than relying solely on the SQL `WHERE`.

---

### `internal/auth/middleware.go` (middleware, request-response)

**No prior custom-middleware analog exists in this codebase** — `internal/api/routes.go`
only chains stdlib chi middleware (`RequestID`, `RealIP`, `Logger`, `Recoverer`).
Follow chi's standard middleware function signature (the shape every chi
middleware, including the ones already wired, conforms to):
```go
// internal/api/routes.go:9-22 — existing chain shows the *wiring* convention,
// not a custom middleware implementation:
func (s *Server) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", s.handleHealth)
	r.Route("/v1", func(r chi.Router) {
		r.Post("/jobs", s.handleCreateJob)
		r.Get("/jobs/{id}", s.handleGetJob)
	})
	return r
}
```
New `auth.Middleware(resolver ClientResolver) func(http.Handler) http.Handler`
must:
1. Extract the API key from the `Authorization` header (e.g. `ApiKey <key>` scheme).
2. Call `resolver.ResolveClient(ctx, key)`.
3. On `ErrInvalidKey`/missing header: `http.Error` / project's `writeError`-equivalent
   with `401` and return — **before** calling `next.ServeHTTP` (hard cutover, D-08:
   no warn-only path).
4. On success: inject the resolved `*clients.Client` into `r.Context()` via an
   unexported context-key type (standard Go idiom; no existing precedent in
   this codebase since no other package currently uses `context.WithValue` —
   this is the first one, keep it minimal: one unexported key type, one
   `ClientFromContext(ctx) (*clients.Client, bool)` accessor, mirroring how
   `jobs.ErrNotFound` is a small single-purpose exported symbol next to its
   producer).

Error-response convention to reuse (`internal/api/handlers.go:179-181`,
`internal/api` is where `writeError`/`writeJSON` live — the auth middleware
lives in a different package, so either duplicate this ~5-line helper locally
in `internal/auth` or write directly via `http.Error`/`json.NewEncoder` — do
**not** import `internal/api` from `internal/auth` to reuse it, since that
would invert the dependency direction ARCHITECTURE.md specifies
(`internal/api` depends on `internal/auth`, never the reverse):
```go
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
```

---

### `internal/ratelimit/ratelimit.go` (middleware, request-response)

**No direct analog** — `go-chi/httprate` is a new dependency (STACK.md,
not yet in `go.mod`). Follow two existing conventions for the *wrapper* shape:

**Env-var-driven constructor convention** (`internal/storage/storage.go:24-33`):
```go
func New(ctx context.Context) (*Client, error) {
	endpoint := os.Getenv("S3_ENDPOINT")
	...
	if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
		return nil, fmt.Errorf("S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY and S3_BUCKET must be set")
	}
	...
}
```
Apply the same shape to reading `RATE_LIMIT_*` env vars, but per CONTEXT.md
D-Rate-Limiting these are **optional with conservative defaults** (not
required-or-fail like storage), so follow the `envInt`/`envDuration`-with-default
convention from `cmd/api/main.go:79-87` / `cmd/worker/main.go:59-75` instead
(those are the closest "optional numeric env var with fallback" analogs):
```go
func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(firstField(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}
```
Note: this helper is currently duplicated per-`cmd/*/main.go` (flagged in
CLAUDE.md conventions as a known duplication) — do not further duplicate it a
third time into `internal/ratelimit`; instead have `cmd/api/main.go` read the
env vars and pass parsed `int`/`time.Duration` values into
`ratelimit.Config`, keeping `internal/ratelimit` itself free of `os.Getenv`
calls (matching how `internal/worker` receives `ENGINE_TIMEOUT` as an
already-parsed `time.Duration` parameter rather than reading the env itself —
`cmd/worker/main.go:41`).

**Chi middleware group wiring convention** (research `ARCHITECTURE.md` Pattern 3,
directly reusable as the target shape for `routes.go`):
```go
r.Group(func(r chi.Router) {
	r.Use(httprate.LimitByIP(60, time.Minute))      // coarse, pre-auth flood guard
	r.Use(auth.Middleware(clientRepo))               // resolves client, 401 on failure
	r.Use(ratelimit.PerClient(clientLimiter))         // fair, business-meaningful limit
	r.Post("/v1/jobs", h.handleCreateJob)
	r.Get("/v1/jobs/{id}", h.handleGetJob)
})
```
`ratelimit.PerClient` should key `httprate.KeyFunc` off `auth.ClientFromContext(r.Context())`
(the client resolved by the *previous* middleware in the chain), not IP —
this is the explicit Pitfall-9 requirement from RESEARCH (rate-limit on
`client_id`, never on network identity).

---

### `cmd/manage-clients/main.go` (config/CLI, one-shot binary)

**Analog:** `cmd/migrate/main.go` (entire file, 24 lines — exact structural fit)
```go
// Command migrate applies all pending database migrations and exits.
package main

import (
	"context"
	"log"

	"github.com/apaderin/octoconv/internal/db"
)

func main() {
	ctx := context.Background()
	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("migrations applied")
}
```
`cmd/manage-clients/main.go` follows the identical shape: `db.Connect(ctx)` →
`defer pool.Close()` → do the one-shot operation → `log.Println`/`log.Fatalf`
→ exit. The difference is subcommand dispatch (`create <name>` / `revoke
<key-id>`); use plain `os.Args[1]` switch (stdlib only, no CLI framework
dependency — matches the codebase's zero-framework, stdlib-first convention
already evident in `cmd/api/main.go` and `cmd/worker/main.go` having no flag
package usage either):
```go
func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: manage-clients <create|revoke> ...")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	repo := clients.NewRepo(pool)
	switch os.Args[1] {
	case "create":
		runCreate(ctx, repo, os.Args[2:])
	case "revoke":
		runRevoke(ctx, repo, os.Args[2:])
	default:
		log.Fatalf("unknown subcommand %q", os.Args[1])
	}
}
```
**Critical constraint from D-03/D-07:** the raw key is generated (`auth.GenerateKey()`),
hashed (`auth.HashKey()`) before `repo.Create`, and printed via `fmt.Println`
(**not** `log.Println` — avoid it ever hitting a log aggregator/log line
prefix) exactly once. Never pass the raw key to any `log.*` call anywhere in
this binary.

---

### `internal/db/migrations/0002_client_api_keys.sql` (migration, batch/DDL)

**Analog:** `internal/db/migrations/0001_init.sql` (SQL DDL conventions)

**Trigger + index conventions to reuse** (`0001_init.sql:1-14, 37-39, 67-74`):
```sql
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TABLE clients (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
```
The existing `clients` table (line 10-14) has **no** key-hash columns yet
(confirmed by CONTEXT.md's Notion cross-ref). New migration must `ALTER TABLE
clients ADD COLUMN` rather than redefine the table (migrations are additive,
append-only per the `schema_migrations`-tracked, lexically-ordered runner in
`internal/db/db.go:34-92`):
```sql
ALTER TABLE clients
    ADD COLUMN api_key_hash            text UNIQUE,
    ADD COLUMN api_key_hash_secondary  text UNIQUE,
    ADD COLUMN revoked_at              timestamptz,
    ADD COLUMN updated_at              timestamptz NOT NULL DEFAULT now();

CREATE INDEX clients_api_key_hash_idx           ON clients (api_key_hash)           WHERE revoked_at IS NULL;
CREATE INDEX clients_api_key_hash_secondary_idx ON clients (api_key_hash_secondary) WHERE revoked_at IS NULL;

CREATE TRIGGER clients_set_updated
    BEFORE UPDATE ON clients
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```
Reuses the exact `set_updated_at()` trigger function already defined in
`0001_init.sql:3-8` (no need to redefine it — `CREATE OR REPLACE FUNCTION` in
0001 already makes it available; only `CREATE TRIGGER clients_set_updated` is
new). Partial unique indexes filtered on `revoked_at IS NULL` mirror the
existing `jobs_inflight_idx` partial-index convention (`0001_init.sql:71`).

---

### `internal/api/routes.go` (modify — route, request-response)

**Analog:** itself, extended per research Pattern 3.

**Current state** (`internal/api/routes.go:9-22`, full file):
```go
func (s *Server) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", s.handleHealth)
	r.Route("/v1", func(r chi.Router) {
		r.Post("/jobs", s.handleCreateJob)
		r.Get("/jobs/{id}", s.handleGetJob)
	})
	return r
}
```
Target shape (D-09: `/healthz` stays outside the auth/rate-limit group;
coarse IP limit → auth → per-client limit ordering from Pattern 3):
```go
func (s *Server) Routes() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", s.handleHealth) // no auth, no rate limit

	r.Route("/v1", func(r chi.Router) {
		r.Use(s.ipRateLimit)   // coarse, pre-auth flood guard
		r.Use(s.authMiddleware) // resolves client, 401 on failure
		r.Use(s.clientRateLimit) // fair, per-client limit
		r.Post("/jobs", s.handleCreateJob)
		r.Get("/jobs/{id}", s.handleGetJob)
	})
	return r
}
```
Keep the field-per-dependency style already on `*Server` (`internal/api/api.go:34-40`)
for the new middleware closures rather than free functions, so they can close
over `s.resolver`/`s.clientLimiter` the same way `s.handleCreateJob` closes
over `s.repo`/`s.storage`/`s.queue`.

---

### `internal/api/api.go` (modify — service wiring)

**Analog:** itself.

**Current struct + Config + constructor** (`internal/api/api.go:33-63`, full):
```go
type Server struct {
	repo          Repo
	storage       Storage
	queue         Enqueuer
	maxUploadByte int64
	presignTTL    time.Duration
}

type Config struct {
	MaxUploadBytes int64
	PresignTTL     time.Duration
}

func NewServer(repo Repo, storage Storage, queue Enqueuer, cfg Config) *Server {
	if cfg.PresignTTL == 0 {
		cfg.PresignTTL = 15 * time.Minute
	}
	if cfg.MaxUploadBytes == 0 {
		cfg.MaxUploadBytes = 100 << 20 // 100 MiB
	}
	return &Server{
		repo:          repo,
		storage:       storage,
		queue:         queue,
		maxUploadByte: cfg.MaxUploadBytes,
		presignTTL:    cfg.PresignTTL,
	}
}
```
Add `resolver auth.ClientResolver` and rate-limit config fields following the
identical "zero-value gets a conservative default" idiom already used for
`PresignTTL`/`MaxUploadBytes`:
```go
type Server struct {
	repo          Repo
	storage       Storage
	queue         Enqueuer
	resolver      auth.ClientResolver
	maxUploadByte int64
	presignTTL    time.Duration
	ipRateLimit   RateLimitConfig
	clientRateLimit RateLimitConfig
}

type Config struct {
	MaxUploadBytes int64
	PresignTTL     time.Duration
	IPRateLimit     RateLimitConfig
	ClientRateLimit RateLimitConfig
}

func NewServer(repo Repo, storage Storage, queue Enqueuer, resolver auth.ClientResolver, cfg Config) *Server {
	if cfg.IPRateLimit.RequestsPerMinute == 0 {
		cfg.IPRateLimit.RequestsPerMinute = 60 // conservative default, per D-Rate-Limiting discretion
	}
	...
}
```
`NewServer`'s signature grows by one positional param (`resolver`), matching
how the existing signature already takes three narrow interfaces positionally
before `Config` — do not fold `resolver` into `Config` (interfaces are
dependencies, `Config` is tunables only — the existing file already enforces
this separation).

---

### `internal/api/handlers.go` (modify — controller, request-response)

**Analog:** itself, `handleCreateJob` (`internal/api/handlers.go:32-115`) and
`handleGetJob` (`internal/api/handlers.go:119-168`).

**Client-scoping insertion point in `handleCreateJob`** — thread
`client_id` through `jobs.CreateParams` (currently missing that field):
```go
// internal/api/handlers.go:85-99 (current)
createdID, err := s.repo.Create(ctx, jobs.CreateParams{
	ID:           jobID,
	Operation:    operationConv,
	Engine:       engineImage,
	SourceFormat: source,
	TargetFormat: target,
	Input: jobs.Input{ ... },
})
```
becomes (pulling the resolved client off context, set by the new auth
middleware):
```go
client, _ := auth.ClientFromContext(ctx) // guaranteed present: auth middleware runs first
createdID, err := s.repo.Create(ctx, jobs.CreateParams{
	ID:           jobID,
	ClientID:     client.ID,
	Operation:    operationConv,
	...
})
```

**Ownership-check insertion point in `handleGetJob`** — CONTEXT.md's
Integration Points explicitly requires **404, not 403**, for cross-client
access (matches the existing convention of never leaking internal state
distinctions to callers — same principle as
"HTTP layer never leaks internal error text to clients", CLAUDE.md Error
Handling section):
```go
// internal/api/handlers.go:128-136 (current)
job, err := s.repo.Get(ctx, id)
if errors.Is(err, jobs.ErrNotFound) {
	writeError(w, http.StatusNotFound, "job not found")
	return
}
```
extend with an ownership check immediately after the existing not-found
branch, reusing the exact same `writeError(w, http.StatusNotFound, "job not
found")` call (not a distinct message — cross-client access and true
not-found must be indistinguishable to the caller):
```go
client, _ := auth.ClientFromContext(ctx)
if job.ClientID != client.ID {
	writeError(w, http.StatusNotFound, "job not found")
	return
}
```

---

### `internal/jobs/repo.go` (modify — model/repo, CRUD)

**Analog:** itself, `Create` (`internal/jobs/repo.go:41-77`) and `Get`
(`internal/jobs/repo.go:121-141`).

**Current insert (no client_id column)** (`internal/jobs/repo.go:47-54`):
```go
if _, err := tx.Exec(ctx, `
	INSERT INTO jobs (id, operation, engine, status, source_format, target_format)
	VALUES ($1, $2, $3, 'queued', $4, $5)`,
	jobID, p.Operation, p.Engine, p.SourceFormat, p.TargetFormat,
); err != nil {
	return fmt.Errorf("insert job: %w", err)
}
```
Add `client_id` as a bound parameter (column already exists in the `jobs`
table per `0001_init.sql:43`, just unused by `Create` today):
```go
if _, err := tx.Exec(ctx, `
	INSERT INTO jobs (id, client_id, operation, engine, status, source_format, target_format)
	VALUES ($1, $2, $3, $4, 'queued', $5, $6)`,
	jobID, p.ClientID, p.Operation, p.Engine, p.SourceFormat, p.TargetFormat,
); err != nil {
	return fmt.Errorf("insert job: %w", err)
}
```
`CreateParams` (`internal/jobs/jobs.go`, alongside existing `ID`,
`Operation`, `Engine` fields) gets a new `ClientID uuid.UUID` field, and
`Job` (`internal/jobs/jobs.go:22-35`) gets a matching `ClientID uuid.UUID`
field, both selected/scanned in `Get` (`internal/jobs/repo.go:121-141`)
alongside the existing `id, operation, engine, status, ...` column list —
same `deref`-style scan pattern already used for nullable columns.

---

### `cmd/api/main.go` (modify — config/entrypoint)

**Analog:** itself + `cmd/worker/main.go`'s env-parsing helpers
(`cmd/worker/main.go:59-84`, `envInt`/`envDuration`/`firstField` — identical
duplicated helper already exists per-binary, confirmed in CLAUDE.md
Configuration conventions).

**Current wiring** (`cmd/api/main.go:41-49`):
```go
qc, err := queue.NewClient()
if err != nil {
	log.Fatalf("queue: %v", err)
}
defer qc.Close()

srv := api.NewServer(jobs.NewRepo(pool), store, qc, api.Config{
	MaxUploadBytes: envInt64("MAX_UPLOAD_BYTES", 100<<20),
})
```
Extend with a `clients.NewRepo(pool)` + `auth` resolver construction, and read
the new rate-limit env vars using the same `envInt64`/duplicate-`envInt`
convention (`cmd/api/main.go:79-96`, full existing helper block):
```go
func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(firstField(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}
```
```go
clientRepo := clients.NewRepo(pool)
resolver := auth.NewResolver(clientRepo)

srv := api.NewServer(jobs.NewRepo(pool), store, qc, resolver, api.Config{
	MaxUploadBytes: envInt64("MAX_UPLOAD_BYTES", 100<<20),
	IPRateLimit: api.RateLimitConfig{
		RequestsPerMinute: envInt64("RATE_LIMIT_IP_RPM", 60),
	},
	ClientRateLimit: api.RateLimitConfig{
		RequestsPerMinute: envInt64("RATE_LIMIT_CLIENT_RPM", 120),
	},
})
```
Add the two new env vars (`RATE_LIMIT_IP_RPM`, `RATE_LIMIT_CLIENT_RPM`) to
`.env.example` next to the existing `MAX_UPLOAD_BYTES` line, matching its
inline-comment-for-units convention (`.env.example:12`: `MAX_UPLOAD_BYTES=104857600   # 100 MiB`).

---

## Shared Patterns

### Environment-variable-only configuration
**Source:** `cmd/api/main.go:79-96`, `cmd/worker/main.go:59-84`, `internal/storage/storage.go:24-33`
**Apply to:** `internal/ratelimit` config (thresholds), `internal/db/migrations/0002_*.sql` is schema not config (N/A)
```go
func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(firstField(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}
```
No config-file support anywhere in this codebase — new auth/rate-limit
tunables must follow this exact pattern, parsed in `cmd/*/main.go` and passed
in as already-typed values (never `os.Getenv` inside `internal/*` packages
except `internal/db`, `internal/queue`, `internal/storage`, which are the
three existing exceptions that read env vars directly in their own `New`/`Connect`
constructors — follow that same exception shape if `internal/ratelimit` or
`internal/auth` genuinely needs its own required env var, but prefer passing
parsed config from `cmd/api/main.go` for anything with a sane default).

### Error handling: sentinel errors + errors.Is + wrapped context
**Source:** `internal/jobs/repo.go:14,130-135`, `internal/api/handlers.go:129`
```go
var ErrNotFound = errors.New("job not found")
...
if errors.Is(err, pgx.ErrNoRows) {
	return nil, ErrNotFound
}
if err != nil {
	return nil, fmt.Errorf("get job: %w", err)
}
```
**Apply to:** `internal/clients` (`clients.ErrNotFound`), `internal/auth`
(`auth.ErrInvalidKey`, wrapping/mapping `clients.ErrNotFound` at the auth
layer boundary) — one sentinel per package, checked via `errors.Is` by
callers, never string-matched.

### HTTP error responses: fixed short messages, no internal leakage
**Source:** `internal/api/handlers.go:179-181`
```go
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
```
**Apply to:** `internal/auth/middleware.go` (401 on invalid/missing key),
`internal/ratelimit` (429 + `Retry-After` header on exceed, per CONTEXT.md
Rate Limiting decision) — both live outside `internal/api` so cannot import
`writeError` directly (would invert the dependency direction); duplicate the
~5-line JSON-error-body helper locally in each package, same as `firstField`/
`envInt` are already duplicated per-binary in this codebase (an accepted,
existing convention, not an anti-pattern to fix here).

### Guarded transitions via row lock + transaction
**Source:** `internal/jobs/repo.go:197-234` (`Repo.transition`)
**Apply to:** `internal/clients/repo.go` `Revoke`/rotation methods — lock the
row (`SELECT ... FOR UPDATE`), assert current state allows the change, apply
the update, all inside one `pgx.BeginFunc` closure.

### Postgres-first, then act
**Source:** `internal/api/handlers.go:83-109` (comment: "Postgres-first double write")
**Apply to:** `cmd/manage-clients` key creation — insert the `clients` row +
hash first (durable), print the raw key to stdout only after the DB write
succeeds, never before.

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `internal/auth/hash.go` | utility | transform | No prior crypto/hashing code exists in the codebase; genuinely new (STACK.md D-07 is the sole source of truth for the approach) |
| `internal/auth/middleware.go` | middleware | request-response | No custom chi middleware precedent exists — only stdlib `chi/middleware` is chained today; follow chi's standard `func(http.Handler) http.Handler` signature and research ARCHITECTURE.md Pattern 3's example as the primary reference instead of a codebase analog |
| `internal/ratelimit/ratelimit.go` | middleware | request-response | `go-chi/httprate` is a new dependency not yet in `go.mod`; no in-process rate limiting exists anywhere in the current codebase |

## Metadata

**Analog search scope:** `internal/api/`, `internal/jobs/`, `internal/storage/`, `internal/db/`, `internal/queue/`, `internal/worker/`, `cmd/*/main.go`, `internal/db/migrations/`
**Files scanned:** 17 non-test `.go` files, 1 SQL migration, `go.mod`, `.env.example`, `CLAUDE.md`
**Pattern extraction date:** 2026-07-03
**Note on repo state:** `git status`/`git branch` show `feat/scaffold-and-infra` is no longer a separate branch — the vertical slice is already present on `main` at the file-content level (D-01/D-02's merge-commit mechanics are a historical/process concern for the planner to verify against actual branch state at execution time, not a code-pattern concern for this document).

---
*Pattern mapping for: Phase 1 - Merge, Auth & Rate Limiting*
*Mapped: 2026-07-03*
