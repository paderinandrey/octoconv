# Phase 4: Content Validation, Storage Lifecycle & Observability - Pattern Map

**Mapped:** 2026-07-07
**Files analyzed:** 14 (3 new, 11 modified)
**Analogs found:** 14 / 14 (all files have an in-repo analog; this is an additive-only phase, no net-new architectural layer)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/convert/sniff.go` (NEW) | utility (format detection) | transform | `internal/convert/libvips.go` + `internal/convert/convert.go` | role-match (format-scoped pure functions, same package) |
| `internal/metrics/metrics.go` (NEW) | config/utility (metric registration) | event-driven | `internal/queue/queue.go` (package-level const/var + pure builder functions, no state mutation on the hot path) | role-match |
| `internal/metrics/queue_collector.go` (NEW) | service (pull-based collector) | request-response (scrape-triggered) | `internal/reconciler/reconciler.go` (`Sweeper` struct wrapping a dependency behind an interface, periodic/on-demand read) | role-match |
| `internal/api/handlers.go` `handleCreateJob` (MODIFIED) | controller | request-response, file-I/O | itself (existing handler, reordered) | exact |
| `internal/api/handlers.go` `handleHealth` (MODIFIED) | controller | request-response | itself (existing stub) + worker's dependency-construction pattern | exact |
| `internal/api/api.go` (MODIFIED — add `Pinger`/health deps) | config (interface + Server struct) | request-response | itself (`Repo`/`Storage`/`Enqueuer` interface-segregation pattern) | exact |
| `internal/storage/storage.go` `EnsureLifecycle` (NEW method) | service (S3 wrapper) | CRUD (idempotent PUT) | `storage.New` / `storage.Upload` (same file, same client, same error-wrap idiom) | exact |
| `internal/worker/worker.go` (MODIFIED — instrument job outcome/duration) | controller (asynq task handler) | event-driven | itself (`HandleImageConvert` terminal exit points) | exact |
| `internal/webhook/deliver.go` or `internal/worker/worker.go` `HandleWebhookDeliver` (MODIFIED — instrument delivery outcome) | controller (asynq task handler) | event-driven | `internal/worker/worker.go` `HandleWebhookDeliver` (same file, sibling handler) | exact |
| `internal/reconciler/reconciler.go` `sweep` (MODIFIED — instrument recovered/exhausted) | service (periodic sweep) | event-driven, batch | itself (`sweep` method, existing branches) | exact |
| `cmd/api/main.go` (MODIFIED — mount `/metrics` listener, call `EnsureLifecycle`) | entry point | request-response, startup | itself (existing wiring + graceful shutdown pattern) | exact |
| `cmd/worker/main.go` (MODIFIED — mount `/metrics` listener, wire queue collector) | entry point | request-response, startup | itself + `cmd/api/main.go` (mirrors the second-listener pattern once added there) | exact |
| `docker-compose.yml` (MODIFIED — add `asynqmon` service) | config | — | `createbucket` one-shot service block (same file, same "auxiliary ops container" shape) | exact |
| `.env.example` (MODIFIED — add `STORAGE_TTL`, `METRICS_ADDR`, health-check timeout) | config | — | itself (existing `# Reconciler` section block style) | exact |

## Pattern Assignments

### `internal/convert/sniff.go` (NEW — utility, transform)

**Analog:** `internal/convert/libvips.go` (file-scoped var + pure functions) and `internal/convert/convert.go` (`NormalizeFormat`)

**Package/imports pattern** (`internal/convert/libvips.go` lines 1-9):
```go
package convert

import (
	"context"
	"fmt"
)

// imageFormats are the raster formats libvips converts between in this slice.
var imageFormats = []string{"png", "jpg", "webp", "heic", "tiff"}
```
Mirror this: `sniff.go` stays in `package convert`, uses a package-level var for the signature table, and composes with `NormalizeFormat` (`internal/convert/convert.go:29-39`) so detected output feeds directly into `Registry.Supports`/`Lookup` without a second normalization pass:
```go
// internal/convert/convert.go:29-39 — reuse, do not duplicate
func NormalizeFormat(f string) string {
	f = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f), "."))
	switch f {
	case "jpeg":
		return "jpg"
	case "tif":
		return "tiff"
	default:
		return f
	}
}
```

**Doc-comment convention** (package-level doc comment lives on exactly one file — `convert.go:1-3` already carries it, so `sniff.go` gets only a function/type-level doc comment, not a second package doc comment):
```go
// internal/convert/convert.go:1-3 (existing package doc, do not duplicate)
// Package convert defines the Converter abstraction, a registry of supported
// format pairs, and the concrete engine implementations (libvips for images).
```

**Error handling / no-match convention:** Detection returns a plain `(format string, ok bool)` or `(format string, err error)` — follow the `Registry.Lookup` shape (`internal/convert/convert.go:59-63`) exactly, since callers already know how to branch on a boolean "not found" rather than a typed error:
```go
// internal/convert/convert.go:59-63 — shape to mirror
func (r *Registry) Lookup(from, to string) (Converter, bool) {
	c, ok := r.m[Pair{From: NormalizeFormat(from), To: NormalizeFormat(to)}]
	return c, ok
}
```

**MIME mapping to promote here (Open Question 1 in RESEARCH.md):** `internal/worker/worker.go:348-363` already has the exact function this phase needs shared between API and worker:
```go
// internal/worker/worker.go:348-363 — promote to convert.MIMEType, keep worker.go
// calling the promoted function instead of a private copy
func contentTypeFor(format string) string {
	switch convert.NormalizeFormat(format) {
	case "png":
		return "image/png"
	case "jpg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "heic":
		return "image/heic"
	case "tiff":
		return "image/tiff"
	default:
		return "application/octet-stream"
	}
}
```

---

### `internal/api/handlers.go` `handleCreateJob` (MODIFIED, controller, request-response/file-I/O)

**Analog:** itself — `internal/api/handlers.go:34-133`

**Current imports** (lines 1-17, extend with `convert`'s new `Sniff`/`MIMEType` — already imported):
```go
import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/storage"
)
```

**Current check order to reorder (D-05)** — lines 62-94, the exact block that must change:
```go
filename := path.Base(header.Filename)
source := convert.NormalizeFormat(strings.TrimPrefix(path.Ext(filename), "."))
if source == "" {
	writeError(w, http.StatusBadRequest, "cannot determine source format from filename")
	return
}

// Validate the conversion pair BEFORE writing anything to storage.
if !convert.Default.Supports(source, target) {
	writeError(w, http.StatusUnprocessableEntity,
		"unsupported conversion: "+source+" -> "+target)
	return
}
...
jobID := uuid.New()
key := storage.InputKey(jobID, 0, filename)
contentType := header.Header.Get("Content-Type")

if err := s.storage.Upload(ctx, key, file, header.Size, contentType); err != nil {
```
New order per D-05/D-06/D-08: (1) declared extension parsed as today (still needed for the D-01 honesty check), (2) `convert.Sniff(file)` peeks ≤12 bytes and re-stitches via `io.MultiReader` before the pair-check, (3) detected-vs-declared mismatch → 422 + `log.Printf` with `client_id` (D-08 exception, see Shared Patterns), (4) detected format (not extension) feeds `convert.Default.Supports`, (5) `contentType` for `s3.Upload` and `jobs.Input.ContentType` becomes `convert.MIMEType(detected)`, never `header.Header.Get("Content-Type")`.

**Existing error-response idiom to keep using** (lines 41-52, `writeError` for validation failures):
```go
var maxErr *http.MaxBytesError
if errors.As(err, &maxErr) {
	writeError(w, http.StatusRequestEntityTooLarge, "file exceeds size limit")
	return
}
writeError(w, http.StatusBadRequest, "invalid multipart form")
return
```
The new 422 mismatch/unrecognized-content errors use the same `writeError(w, http.StatusUnprocessableEntity, "<detailed msg>")` call shape already used at line 71-73 for the pair-check — D-04's detailed message is a drop-in replacement string, not a new response mechanism.

**Client identity already available for the D-08 log line** (line 97, resolved before storage write today — but note it currently runs AFTER upload; the mismatch-rejection log must read the client from context BEFORE the (now-reordered) upload call):
```go
// Middleware guarantees a resolved client is present before this handler runs.
client, _ := auth.ClientFromContext(ctx)
```

---

### `internal/api/handlers.go` `handleHealth` (MODIFIED, controller, request-response)

**Analog:** itself (current stub, line 27-29) + RESEARCH.md's verified code example built directly against this codebase's existing dependency-ping call sites.

**Current stub to replace:**
```go
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
```

**Existing `writeJSON`/`writeError` helpers to reuse unchanged** (`internal/api/handlers.go:198-209`):
```go
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	// Don't HTML-escape: presigned URLs contain & that would become &.
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}
```

**Dependency-ping idioms already in the codebase to call from the new handler** (not to reimplement):
```go
// internal/storage/storage.go:43-46 — MinIO existence probe pattern
exists, err := mc.BucketExists(ctx, bucket)
if err != nil {
	return nil, fmt.Errorf("check bucket %q: %w", bucket, err)
}
```
Postgres: `*pgxpool.Pool` already has `.Ping(ctx)` (stdlib pgx API) — `internal/db/db.go` constructs the pool but does not currently expose a health-specific wrapper; the D-16 interface (`Pinger` with a single `Ping(ctx) error` method) should mirror the existing interface-segregation style in `internal/api/api.go:16-32` (narrow interfaces per dependency, not the full concrete type).

**503-with-detail response shape (D-17)** — new pattern for this codebase, but built from existing primitives only (`writeJSON` + `http.StatusServiceUnavailable`), no new response-writing mechanism needed. Use `context.WithTimeout(r.Context(), <2-3s>)` per D-16, mirroring the existing engine-timeout-via-context idiom in `internal/worker/worker.go:250` (`context.WithTimeout(ctx, h.engineTimout)`).

---

### `internal/api/api.go` (MODIFIED — health-check dependencies)

**Analog:** itself — the existing interface-segregation convention at lines 16-32.

**Pattern to extend, not replace:**
```go
// internal/api/api.go:16-32
type Repo interface {
	Create(ctx context.Context, p jobs.CreateParams) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (*jobs.Job, error)
	Outputs(ctx context.Context, id uuid.UUID) ([]jobs.Output, error)
}

type Storage interface {
	Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

type Enqueuer interface {
	EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error
}
```
Add narrow health-only interfaces the same way (e.g. `type PgPinger interface { Ping(ctx context.Context) error }`, `type RedisPinger interface { Ping(ctx context.Context) error }`, and extend `Storage` with a `BucketExists`-shaped method, or add a small `S3Pinger` interface) — each interface declares only the method(s) the consuming package actually calls, exactly like the existing three. Extend `Server` struct (lines 35-44) and `NewServer` (lines 57-80) with the new fields/constructor params following the same positional-args-then-`Config`-for-tunables convention already established.

---

### `internal/storage/storage.go` `EnsureLifecycle` (NEW method — service, CRUD)

**Analog:** itself — `New`, `Upload` in the same file (lines 24-64), same client, same error-wrap idiom.

**Imports to add** (extends the existing block, lines 5-14):
```go
import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle" // NEW
)
```

**Core pattern — mirror `Upload`'s error-wrap shape** (lines 56-64):
```go
func (c *Client) Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := c.mc.PutObject(ctx, c.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("upload %q: %w", key, err)
	}
	return nil
}
```
`EnsureLifecycle(ctx context.Context, ttl time.Duration) error` follows the identical shape: build `*lifecycle.Configuration` with two `Rule`s (prefixes `uploads/`, `results/`, per `storage/keys.go:12-20`'s existing key layout), call `c.mc.SetBucketLifecycle(ctx, c.bucket, cfg)`, and wrap any error with `fmt.Errorf("set bucket lifecycle: %w", err)` — same pattern as `New`'s `fmt.Errorf("check bucket %q: %w", bucket, err)` (line 45).

**Key-prefix source of truth to reference, not duplicate** (`internal/storage/keys.go:10-20`):
```go
func InputKey(jobID uuid.UUID, ordinal int, filename string) string {
	return fmt.Sprintf("uploads/%s/%d-%s", jobID, ordinal, path.Base(filename))
}

func OutputKey(jobID uuid.UUID, ordinal int, filename string) string {
	return fmt.Sprintf("results/%s/%d-%s", jobID, ordinal, path.Base(filename))
}
```
The lifecycle rule's two `Filter.Prefix` values (`"uploads/"`, `"results/"`) must match these literal prefixes exactly.

---

### `internal/worker/worker.go` `HandleImageConvert`/`HandleWebhookDeliver` (MODIFIED — instrumentation)

**Analog:** itself — the two existing terminal-exit-point handlers (lines 110-236).

**Job-outcome terminal exit points to instrument (Pitfall 6 — only these two, never the transient-return path)** (lines 128-151):
```go
if err := h.process(ctx, job); err != nil {
	if isTerminal(err) {
		_ = h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", map[string]any{"engine_stderr": err.Error()})
		if job.CallbackURL != "" {
			_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
		}
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	// Transient: do NOT mark failed — the job stays active so asynq's own
	// retry/backoff (ImageRetryDelay/IMAGE_MAX_RETRY, Plan 01) applies.
	return err
}
if job.CallbackURL != "" {
	_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
}
return nil
```
Instrument `jobOutcomes.WithLabelValues(engineImage, "failed"/"done").Inc()` and the duration histogram immediately before each `return` in this block (matching RESEARCH.md's Code Examples section) — never inside `isTerminal`'s false branch (that path returns unwrapped for asynq's own retry, no terminal state reached yet).

**Webhook delivery outcome exit points to instrument** (lines 219-235, `HandleWebhookDeliver`):
```go
code, derr := h.deliverer.Deliver(ctx, job.CallbackURL, bodyBytes, ts, sig)
...
deliveryID, recErr := h.webhookRepo.RecordAttempt(ctx, jobID, job.CallbackURL, attempt, statusCodePtr, derr == nil)

if derr != nil {
	if recErr == nil && retryCount >= maxRetry {
		_ = h.webhookRepo.MarkDeadLetter(ctx, deliveryID)
	}
	return derr
}
return nil
```
Instrument a webhook success/fail counter right after `h.deliverer.Deliver` returns (label success on `derr == nil`), consistent with how `RecordAttempt`'s own `derr == nil` boolean is already computed at this exact line.

**Existing const naming to reuse for metric labels** (`internal/api/handlers.go:19-25`):
```go
const (
	engineImage = "image"
	...
)
```
Use `engineImage` (already defined) as the `engine` label value (D-13) rather than a new string literal — same identifier the codebase already uses for the `jobs.engine` column value.

---

### `internal/reconciler/reconciler.go` `sweep` (MODIFIED — instrumentation)

**Analog:** itself — the two existing branches (lines 94-105 exhausted, 107-175 recovered).

**Exhausted branch** (lines 94-105):
```go
if n >= s.cfg.MaxRecoveries {
	job, _ := s.store.Get(ctx, j.ID)
	_ = s.store.MarkFailed(ctx, j.ID, "reconciler_exhausted", "recovery attempts exhausted", map[string]any{"action": "reconciler_exhausted"})
	if job != nil && job.CallbackURL != "" {
		_ = s.enq.EnqueueWebhookDeliver(ctx, j.ID)
	}
	continue
}
```
Instrument a counter increment (`status="exhausted"`) right after the `MarkFailed` call, before `continue`.

**Recovered branch** (lines 112-131, success path only — the `errors.Is(err, asynq.ErrDuplicateTask)` case at lines 113-121 is NOT a recovery, per the existing comment, and must NOT increment the counter):
```go
if err := s.enq.EnqueueImageConvert(ctx, j.ID); err != nil {
	if errors.Is(err, asynq.ErrDuplicateTask) {
		// ... NOT a recovery, no metric here
		continue
	}
	continue
}
reason := "stale_" + j.Status
if err := s.store.RequeueStale(ctx, j.ID, reason); err != nil {
	// ... bounded single retry, see existing comment block
}
```
Instrument a counter increment (`status="recovered"`) only after a `RequeueStale` call actually succeeds — mirroring the existing comment's own distinction between "genuinely stranded and recovered" vs. "merely backlogged, no-op."

**Note on the package's explicit no-logging stance** (lines 76-79, doc comment on `sweep`):
```go
// A per-job error is swallowed (best
// effort — the next tick retries) so one bad job never stalls the sweep. No
// logging is added here — visibility is limited to job_events (D-15); Phase
// 4 owns OBS logging/metrics.
```
This comment is the explicit hook this phase fulfills — metrics are the sanctioned way to add visibility here, not new `log.Printf` calls (reconciler stays in `internal/*`, which never logs per CLAUDE.md).

---

### `internal/metrics/metrics.go` + `internal/metrics/queue_collector.go` (NEW package)

**Analog:** `internal/queue/queue.go` for the package-doc + const/var registration style; `internal/reconciler/reconciler.go`'s `Sweeper` struct for the collector's dependency-wrapping shape.

**Package doc comment convention to follow** (`internal/jobs/jobs.go:1-3`, `internal/worker/worker.go:1`, `internal/reconciler/reconciler.go:1-3` — one doc comment per package, on exactly one file):
```go
// Package reconciler periodically sweeps Postgres for jobs stranded in
// queued/active past a staleness threshold and requeues or terminally fails
// them, bounded by a recovery cap.
package reconciler
```
`metrics.go` gets the package doc: `// Package metrics defines the Prometheus counters, histograms, and collectors this service exposes.`

**Constructor naming convention** (`NewSweeper`, `NewRegistry`, `NewClient` — never bare `New` outside an already-scoped package):
```go
// internal/reconciler/reconciler.go:53-55
func NewSweeper(store jobStore, enq enqueuer, cfg Config) *Sweeper {
	return &Sweeper{store: store, enq: enq, cfg: cfg}
}
```
`queue_collector.go`'s constructor should be `NewQueueDepthCollector(inspector *asynq.Inspector, queues ...string) prometheus.Collector`, matching this `New<Type>` convention exactly (RESEARCH.md's Pattern 3 code example already follows it).

**No new dependency install pattern:** `github.com/prometheus/client_golang` is the one genuinely new `go.mod` entry this phase adds — gate the `go get` behind human verification per RESEARCH.md's Package Legitimacy Audit; `minio-go/v7/pkg/lifecycle` and `asynq.Inspector` need no `go.mod` changes (already-vendored transitive code).

---

### `cmd/api/main.go` / `cmd/worker/main.go` (MODIFIED — startup wiring)

**Analog:** itself — both files already share the `envInt`/`envInt64`/`envDuration`/`firstField` env-parsing convention and the goroutine + graceful-shutdown pattern.

**Existing env-parsing helpers to reuse for `STORAGE_TTL`/`METRICS_ADDR`** (`cmd/api/main.go:90-107`, duplicated verbatim in `cmd/worker/main.go:100-125` per the documented convention):
```go
func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(firstField(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}

func firstField(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}
```
`cmd/worker/main.go` already has an `envDuration` helper (lines 109-116) — reuse it for `STORAGE_TTL` if the API is the process that owns `EnsureLifecycle` (Pitfall 4 recommends API, following the `db.Migrate`-at-boot precedent at `cmd/api/main.go:34`), otherwise add the same `envDuration` helper to `cmd/api/main.go` (it currently lacks one).

**Second-listener pattern for `/metrics` (D-19 — separate from `API_ADDR`)** — model directly on the existing `httpSrv` construction + goroutine + shutdown pattern already in `cmd/api/main.go:66-86`:
```go
httpSrv := &http.Server{
	Addr:              addr,
	Handler:           srv.Routes(),
	ReadHeaderTimeout: 10 * time.Second,
}

go func() {
	log.Printf("🚀 API listening on %s", addr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
}()

<-ctx.Done()
log.Println("🛑 shutting down API...")
shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
defer cancel()
if err := httpSrv.Shutdown(shutdownCtx); err != nil {
	log.Printf("graceful shutdown failed: %v", err)
}
```
Add a second `*http.Server` bound to `METRICS_ADDR` (default `127.0.0.1:9090`) with `Handler: promhttp.Handler()`, started in its own goroutine with the same `log.Printf`/error-check idiom, and shut down alongside `httpSrv` in the same `<-ctx.Done()` block. `cmd/worker/main.go` needs the identical second-listener addition (it currently has no HTTP server at all — this is new for that file, modeled on `cmd/api/main.go`'s pattern once added there).

**`EnsureLifecycle` call-site** (one process only, per Pitfall 4) — call immediately after `storage.New(ctx)` succeeds, mirroring the existing `db.Migrate` startup-side-effect pattern:
```go
// cmd/api/main.go:34 — the existing "API owns one-time startup side effects" precedent
if err := db.Migrate(ctx, pool); err != nil {
	log.Fatalf("migrate: %v", err)
}
```

**Logging convention already established** (`cmd/api/main.go:73`, `cmd/worker/main.go:88` — emoji-prefixed startup lines, `log.Fatalf` only for unrecoverable init errors): apply the same style to any new startup log line (e.g. `log.Printf("📊 metrics listening on %s", metricsAddr)`).

---

### `docker-compose.yml` (MODIFIED — new `asynqmon` service)

**Analog:** the existing `createbucket` one-shot auxiliary service (lines 51-62) for shape, and `redis`/`minio` services (lines 20-49) for the `depends_on`/healthcheck/ports pattern.

**Model to follow** (lines 20-30, `redis` service — simplest existing service block):
```yaml
redis:
  image: redis:8
  container_name: octoconv-redis
  restart: always
  ports:
    - "6379:6379"
  healthcheck:
    test: ["CMD", "redis-cli", "ping"]
    interval: 5s
    timeout: 3s
    retries: 10
```
New `asynqmon` service: pin to a specific tag (not `:latest`, per RESEARCH.md's supply-chain note), `depends_on: redis: condition: service_healthy` (same shape as the `api`/`worker` services at lines 70-76/99-105), environment `REDIS_ADDR: redis:6379` (same value already used by `api`/`worker`, lines 79/108), and `ports: - "127.0.0.1:<port>:8080"` — the `127.0.0.1:` prefix is the only new port-binding shape in this file (existing services all bind `0.0.0.0` implicitly via bare `"host:container"` — this is a deliberate, documented deviation per D-18).

**Env var wiring convention to extend** (`api`/`worker` service `environment:` blocks, lines 77-89 / 106-115) — add `STORAGE_TTL` to whichever service owns `EnsureLifecycle`, and `METRICS_ADDR` to both `api` and `worker` service blocks, following the existing flat `KEY: "value"` style already used throughout.

---

### `.env.example` (MODIFIED — new config)

**Analog:** itself — the existing `# Reconciler` section (lines 28-32), the most recently added section, as the model for a new `# Storage lifecycle` / `# Observability` section:
```
# Reconciler
RECONCILER_QUEUED_STALE_AFTER=90s   # lost-enqueue queued threshold (D-08)
RECONCILER_ACTIVE_STALE_AFTER=5m   # crashed-worker active threshold, comfortably above ENGINE_TIMEOUT (D-09)
RECONCILER_SWEEP_INTERVAL=1m   # fixed sweep tick interval (D-10)
RECONCILER_MAX_RECOVERIES=3   # recovery cap before a job is marked reconciler_exhausted (D-12)
```
New entries follow the identical `KEY=value   # inline comment citing the decision ID` style: `STORAGE_TTL=168h   # single TTL for uploads/ and results/ prefixes, default 7 days (D-10/D-11)`, `METRICS_ADDR=127.0.0.1:9090   # localhost-only Prometheus /metrics listener, separate from API_ADDR (D-19)`, health-check-timeout var if made configurable (else hardcode 3s per D-16 and skip an env var).

---

## Shared Patterns

### Error wrapping (`fmt.Errorf("<action>: %w", err)`)
**Source:** `internal/storage/storage.go:40,45,61,70,76,85` — every storage operation wraps with action + key/id context.
**Apply to:** `EnsureLifecycle` (new storage method), health-check ping wrappers.
```go
if err := c.mc.SetBucketLifecycle(ctx, c.bucket, cfg); err != nil {
	return fmt.Errorf("set bucket lifecycle: %w", err)
}
```

### Handlers never leak internal error text — with two explicit, scoped exceptions this phase
**Source:** `internal/api/handlers.go` (`writeError` calls throughout use short fixed strings, discarding `err`).
**Apply to:** All new/modified `handleCreateJob` error paths EXCEPT the two D-04/D-08-sanctioned exceptions (422 mismatch detail message; `client_id`-tagged rejection log) — every other new error path (health-check failures, lifecycle-setup failures surfaced anywhere user-facing) must keep the existing generic-message discipline.
```go
writeError(w, http.StatusUnprocessableEntity, "unsupported conversion: "+source+" -> "+target)
```

### `internal/*` never logs — with one explicit, scoped exception (D-08)
**Source:** CLAUDE.md convention + `internal/reconciler/reconciler.go:76-79` doc comment explicitly deferring all logging/metrics to Phase 4.
**Apply to:** All new instrumentation in `internal/worker`, `internal/reconciler`, `internal/webhook` — metrics increments are fine (they are not logging), but do NOT add `log.Printf` calls in these packages. The ONE sanctioned exception is the D-08 magic-byte-mismatch rejection log in `internal/api/handlers.go` (still `internal/*`, but explicitly carved out by CONTEXT.md) — keep this the only such exception; do not generalize it to other validation failures.

### Postgres-first, guarded-transition discipline — metrics/health as passive observers
**Source:** `internal/jobs/repo.go` guarded `transition` pattern; RESEARCH.md's explicit framing.
**Apply to:** `handleHealth` (only calls `Ping`/`BucketExists`, never writes), `queue_collector.go` (only calls `Inspector.GetQueueInfo`, read-only), all `jobOutcomes`/`webhookOutcomes`/`reconcilerActions` counters (increment-only, never gate business logic on a metric value).

### `engine` label taxonomy mirrors existing queue-routing constants
**Source:** `internal/queue/queue.go:24-27` (`QueueImage = "image"`, `QueueWebhook = "webhook"`) and `internal/api/handlers.go:23` (`engineImage = "image"`).
**Apply to:** All OBS-01 metric label values — reuse these existing string constants rather than introducing new literals, so metric labels and queue-routing/DB `engine` column values never drift apart.

## No Analog Found

None — every file in this phase's scope has at least a role-match analog already in the repository; this is a purely additive hardening phase with no new architectural layer.

## Metadata

**Analog search scope:** `internal/api/`, `internal/convert/`, `internal/storage/`, `internal/worker/`, `internal/webhook/`, `internal/reconciler/`, `internal/queue/`, `internal/jobs/`, `cmd/api/`, `cmd/worker/`, `docker-compose.yml`, `.env.example`
**Files scanned:** 22 (full read, no re-reads; all files ≤ 400 lines, single-pass reads)
**Pattern extraction date:** 2026-07-07
