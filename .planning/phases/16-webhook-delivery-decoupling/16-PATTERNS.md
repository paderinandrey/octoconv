# Phase 16: Webhook Delivery Decoupling - Pattern Map

**Mapped:** 2026-07-11
**Files analyzed:** 8 (2 new, 6 modified)
**Analogs found:** 7 / 8

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `cmd/webhook-worker/main.go` | entry-point (asynq consumer) | event-driven | `cmd/worker/main.go` (full) + `cmd/document-worker/main.go` (trimming precedent) | exact (trim template) |
| `Dockerfile.webhook-worker` | config (container build) | batch (build) | `Dockerfile.worker` (runtime shape) | role-match, needs further trimming |
| `internal/reconciler/reconciler.go` (MODIFY: wrap `Run`) | service | event-driven / batch (ticker) | own `Sweeper.Run` (`reconciler.go:66-77`) | exact (in-place wrap) — no existing advisory-lock analog anywhere in repo |
| `cmd/worker/main.go` (MODIFY: remove lines 76, 83-85, 89, 95, 97) | entry-point | event-driven | itself (pre-image) | exact |
| `cmd/document-worker/main.go` (MODIFY: comment at `:50-53`) | entry-point | event-driven | itself | exact |
| `docker-compose.yml` (MODIFY: add 2 services, strip webhook env from `worker`) | config | batch (deploy) | `document-worker:` service block (`docker-compose.yml:155-199`) | exact |
| `docker-compose.e2e.yml` (MODIFY: add webhook-worker overrides, drop/adjust `worker:` override) | config | batch (deploy) | existing `worker:`/`document-worker:` override blocks (`docker-compose.e2e.yml:32-38`) | exact |
| `.env.example` (MODIFY: add `WEBHOOK_WORKER_CONCURRENCY`, verify existing vars) | config | n/a | `# Document worker` / `# Chromium (html) worker` sections (`.env.example:32-40`) | exact |

`internal/worker/worker.go` is **not modified** — `HandleWebhookDeliver` is reused verbatim (D-06) but re-bound in a new mux. See "Critical Finding" below — it is NOT storage-free as CONTEXT.md's D-07 assumes.

## Pattern Assignments

### `cmd/webhook-worker/main.go` (entry-point, event-driven) — NEW

**Primary analog:** `cmd/worker/main.go` (full file, 161 lines)
**Trimming precedent analog:** `cmd/document-worker/main.go` (shows how a prior phase already trimmed a shared-signature `worker.NewHandler` call down to only what one engine class needs)

**Imports pattern** (`cmd/worker/main.go:5-29`) — keep this shape, drop `internal/convert`:
```go
import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/metrics"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/reconciler"
	"github.com/apaderin/octoconv/internal/storage" // KEEP — see Critical Finding below, HandleWebhookDeliver calls store.PresignGet
	"github.com/apaderin/octoconv/internal/webhook"
	"github.com/apaderin/octoconv/internal/worker"
)
```
Note: `internal/convert` (the registry) is the one import that is genuinely droppable — `worker.NewHandler`'s `registry *convert.Registry` parameter can be passed as `nil` since `HandleWebhookDeliver` never touches `h.registry` (only `HandleImageConvert`/`HandleDocumentConvert`/`HandleHTMLConvert` do — confirm no shared helper reads it before wiring `nil`).

**Init/wiring pattern** (`cmd/worker/main.go:31-74`), same shape, same order (Postgres → storage → Redis opt → signing secret → queue client → repo → handler):
```go
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()

	store, err := storage.New(ctx) // KEEP — required by HandleWebhookDeliver's PresignGet call
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	redisOpt, err := queue.RedisOpt()
	if err != nil {
		log.Fatalf("redis: %v", err)
	}

	signingSecret := []byte(os.Getenv("WEBHOOK_SIGNING_SECRET"))
	if len(signingSecret) == 0 {
		log.Fatalf("WEBHOOK_SIGNING_SECRET must be set")
	}

	qc, err := queue.NewClient()
	if err != nil {
		log.Fatalf("queue client: %v", err)
	}
	defer qc.Close()

	repo := jobs.NewRepo(pool)

	h := worker.NewHandler(
		repo,
		store,
		nil, // convert.Registry — unused by HandleWebhookDeliver, only image/document/html handlers touch it
		0,   // engineTimeout — unused by HandleWebhookDeliver
		webhook.NewRepo(pool),
		webhook.NewDeliverer(),
		qc,
		signingSecret,
		envDuration("WEBHOOK_PRESIGN_TTL", 6*time.Hour),
	)
```

**Sweeper construction, now advisory-lock wrapped** (`cmd/worker/main.go:76-81`, unchanged construction, but `Run` call gated — see reconciler pattern below):
```go
	sweeper := reconciler.NewSweeper(repo, qc, reconciler.Config{
		QueuedStaleAfter: envDuration("RECONCILER_QUEUED_STALE_AFTER", 90*time.Second),
		ActiveStaleAfter: envDuration("RECONCILER_ACTIVE_STALE_AFTER", 5*time.Minute),
		SweepInterval:    envDuration("RECONCILER_SWEEP_INTERVAL", 1*time.Minute),
		MaxRecoveries:    envInt("RECONCILER_MAX_RECOVERIES", 3),
	})
```
New: pass `pool` (or a dedicated `pgx.Conn` acquired from it — Claude's Discretion per D-02) into whatever advisory-lock wrapper `internal/reconciler` exposes, then `go sweeper.Run(ctx)` unchanged in shape but now internally lock-gated.

**mux/server pattern** (`cmd/worker/main.go:83-97`), trim to webhook only, single-queue map, drop the image entry from `metrics.NewQueueDepthCollector`:
```go
	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("WEBHOOK_WORKER_CONCURRENCY", 4),
		Queues:         map[string]int{queue.QueueWebhook: 1},
		RetryDelayFunc: queue.RetryDelayFunc,
	})

	prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueWebhook))

	log.Printf("🐙 webhook-worker starting (queue=%s)", queue.QueueWebhook)
	if err := srv.Start(mux); err != nil {
		log.Fatalf("webhook-worker: %v", err)
	}
	go sweeper.Run(ctx)
```

**Metrics server, shutdown, `envInt`/`envDuration`/`firstField` helpers** (`cmd/worker/main.go:103-160`): copy verbatim, unchanged — this boilerplate is identical across every `cmd/*-worker/main.go` in the repo (confirmed same in `cmd/document-worker/main.go:98-155`). Update only the log strings (`"webhook-worker"` instead of `"worker"`).

---

### `Dockerfile.webhook-worker` (config) — NEW

**Analog:** `Dockerfile.worker` (`Dockerfile.worker:1-22`) — same two-stage shape, strip the engine package and the `USER nobody` engine-specific comment since there is no untrusted-input CLI invocation here (still keep `USER nobody` itself as least-privilege, just adjust the comment).

```dockerfile
# Build stage
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/webhook-worker ./cmd/webhook-worker

# Runtime stage: webhook delivery is a pure HTTP client + Postgres/Redis
# consumer — no conversion engine, no engine CLI tooling of any kind.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/webhook-worker /usr/local/bin/webhook-worker
USER nobody
ENTRYPOINT ["/usr/local/bin/webhook-worker"]
```
Contrast: do NOT add `tini` (that's `Dockerfile.document-worker:14,29` — only needed because LibreOffice forks `soffice.bin`); do NOT add `libvips-tools`/`libreoffice-*`/chromium packages (`Dockerfile.chromium-worker` adds a headless-Chromium apt source — irrelevant here).

---

### `internal/reconciler/reconciler.go` (service, event-driven/batch) — MODIFY

**Analog:** own existing `Sweeper.Run` (`internal/reconciler/reconciler.go:63-77`) — no existing advisory-lock precedent anywhere in the codebase (`grep` for `pgxpool.Acquire`/`pgx.Conn`/`Acquire(` across `internal/` and `cmd/` returned zero hits), so this is genuinely new code; the planner must design it fresh, guided by D-01/D-02's exact requirements.

**Current loop to wrap** (`internal/reconciler/reconciler.go:63-77`):
```go
// Run ticks every cfg.SweepInterval and calls sweep until ctx is cancelled,
// at which point it stops the ticker and returns promptly (no leaked
// goroutine).
func (s *Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.SweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}
```

**Required shape per D-01/D-02** — a dedicated, long-lived `pgx.Conn` (NOT drawn from `Repo`'s `pgxpool.Pool`, since `pg_try_advisory_lock` is session-scoped and pool connections are recycled/multiplexed across goroutines): try the lock every tick; sweep only if acquired; unlock (or just let the dedicated connection close on shutdown, since `pg_advisory_unlock` is unnecessary — session end auto-releases per D-02). Suggested new field on `Sweeper` (or a wrapping type) holding a `*pgx.Conn` acquired once at construction via `pool.Acquire(ctx)` (returns `*pgxpool.Conn`, whose underlying `.Conn()` gives the raw `*pgx.Conn` for `Exec`) — kept open for the process lifetime, never returned to the pool. Each tick: `conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&acquired)`; if `acquired`, call `s.sweep(ctx)`, else skip. Constant lock key: an untyped `const` int64, following the existing package-level-const convention seen in `internal/jobs/jobs.go:23,32` (`detailActionRecovery`, `detailActionWebhookGapRecovered`).

**Existing package-level const convention to mirror** (`internal/jobs/repo.go` imports pattern, `internal/jobs/jobs.go:18-32`):
```go
// detailActionRecovery is the job_events.detail->>'action' tag written by ...
const detailActionRecovery = "reconciler_recovery"
```
→ mirror with something like `const advisoryLockKey int64 = <fixed value>` at the top of `reconciler.go`, with a doc comment explaining the value is arbitrary but must never collide with another subsystem's advisory-lock usage (there are none today).

**pgx connection-pool import precedent** (`internal/db/db.go:11`, `internal/jobs/repo.go:12`):
```go
"github.com/jackc/pgx/v5/pgxpool"
```
`internal/reconciler` currently has zero pgx/db imports (it depends only on the `jobStore`/`enqueuer` interfaces — `internal/reconciler/reconciler.go:6-17,29-48`) — this is the one place a direct `pgxpool.Pool` (or `*pgxpool.Conn`) import must be added, breaking the package's current pure-interface-dependency style. Document why (D-01/D-02 require Postgres session semantics no interface abstraction can express).

**Error handling convention to preserve** — sweep already swallows per-tick errors as best-effort (`internal/reconciler/reconciler.go:97-102`, `:230-233`); the lock-acquisition failure path should follow the same best-effort discipline (a failed `pg_try_advisory_lock` call itself — e.g. connection dropped — should skip the tick, not crash the process; the dedicated connection should be reconnected/re-acquired lazily on next tick if lost, mirroring `sweep`'s "next tick retries" comments).

---

### `cmd/worker/main.go` (entry-point) — MODIFY (remove webhook role)

**What to delete**, referencing current line numbers (`cmd/worker/main.go`):
- `:76-81` — `sweeper := reconciler.NewSweeper(...)` block
- `:85` — `mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)`
- `:89` — `Queues: map[string]int{queue.QueueImage: 2, queue.QueueWebhook: 1}` → becomes `map[string]int{queue.QueueImage: 4}` (restore full concurrency to image now that webhook no longer shares it — Claude's Discretion on exact split, but dropping to a single-queue map is required)
- `:95` — `metrics.NewQueueDepthCollector(..., queue.QueueImage, queue.QueueWebhook)` → drop `queue.QueueWebhook`
- `:97` — log line `"queues=%s,%s"` → `"queue=%s"` (mirror `cmd/document-worker/main.go:93`'s single-queue log format)
- `:101` — `go sweeper.Run(ctx)` line
- Imports: drop `"github.com/apaderin/octoconv/internal/reconciler"` and `"github.com/apaderin/octoconv/internal/webhook"` (both become unused once the above is removed — `webhook.NewRepo`/`webhook.NewDeliverer` calls at `:69-70` also go, and `worker.NewHandler`'s webhook-related params become the same "inert but present" shape `cmd/document-worker/main.go:50-54,64-74` already uses, OR — better, since D-03 is a clean cut — check whether `worker.NewHandler`'s signature itself should be reconsidered so `cmd/worker` doesn't have to pass dead webhook params at all; that signature change is out of this phase's stated scope but flag it to the planner as a discretionary cleanup).

**Analog for the resulting shape:** `cmd/document-worker/main.go:80-96` (already the single-queue, no-sweeper mux/server pattern this phase converges `cmd/worker` toward for its webhook role).

---

### `cmd/document-worker/main.go` (entry-point) — MODIFY (comment only)

**Exact text to update** (`cmd/document-worker/main.go:50-53`):
```go
	// document-worker neither delivers nor signs webhooks (D-06 — cmd/worker
	// remains the sole webhook consumer), so a missing signing secret here is
	// non-fatal; it is passed through to worker.NewHandler only to satisfy its
	// shared signature and is inert for HandleDocumentConvert.
```
This must be rewritten to reflect Phase 16's D-07: webhook delivery now lives in `cmd/webhook-worker`, not `cmd/worker`. Also re-verify (Claude's Discretion, flagged in CONTEXT.md) whether `signingSecret`/`WEBHOOK_SIGNING_SECRET` is still read at all here — `document-worker` only *enqueues* `EnqueueWebhookDeliver` (via the sweeper's webhook-gap recovery path was moved out too under D-04, and `internal/worker/worker.go`'s document-convert handler enqueues on completion) and never signs, so this variable's read at `:54` was already inert before this phase and stays inert now — the comment fix is the deliverable, not a behavior change.

---

### `docker-compose.yml` (config) — MODIFY

**Analog:** `document-worker:` service block (`docker-compose.yml:155-199`), which is itself structured identically to `worker:` (`docker-compose.yml:111-153`) minus the webhook/reconciler env vars.

**New service pattern** (two named services per D-05, both from `Dockerfile.webhook-worker`, both needing Postgres+Redis+MinIO — MinIO required per the storage/PresignGet finding below):
```yaml
  webhook-worker-1:
    build:
      context: .
      dockerfile: Dockerfile.webhook-worker
    container_name: octoconv-webhook-worker-1
    restart: always
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
      minio:
        condition: service_healthy
    environment:
      DATABASE_URL: postgres://octo:octo-pass@postgres:5432/octo_db
      REDIS_ADDR: redis:6379
      S3_ENDPOINT: minio:9000
      S3_ACCESS_KEY: minioadmin
      S3_SECRET_KEY: minioadmin
      S3_BUCKET: octoconv
      S3_USE_SSL: "false"
      WEBHOOK_WORKER_CONCURRENCY: "4"
      WEBHOOK_SIGNING_SECRET: "dev-only-change-me-in-real-deploys"
      WEBHOOK_PRESIGN_TTL: "6h"
      RECONCILER_QUEUED_STALE_AFTER: "90s"
      RECONCILER_ACTIVE_STALE_AFTER: "5m"
      RECONCILER_SWEEP_INTERVAL: "1m"
      RECONCILER_MAX_RECOVERIES: "3"
      METRICS_ADDR: "127.0.0.1:9090"

  webhook-worker-2:
    # identical block, container_name: octoconv-webhook-worker-2
```
(copy for `-2`; both need distinct `container_name` only — everything else identical, since horizontal redundancy per D-05 means truly symmetric replicas, not primary/secondary config).

**`worker:` block edits** (`docker-compose.yml:111-153`): remove `WEBHOOK_SIGNING_SECRET`, `WEBHOOK_PRESIGN_TTL`, `RECONCILER_*` (4 vars) — image-worker no longer runs webhook or sweeper roles per D-03/D-04. `MinIO`/`S3_*` stay (still needed for image conversion itself).

**`document-worker:`/`chromium-worker:` blocks**: the existing `WEBHOOK_SIGNING_SECRET`/`WEBHOOK_PRESIGN_TTL` entries there are already annotated as "currently inert here — wired for parity/explicitness, revisited by the v1.3 webhook-decoupling phase (WEBH-01) (DEBT-05)" (`docker-compose.yml:187-193`, `:233-239`) — this phase is that revisit: remove both vars from both blocks now that the comment's own forward-reference resolves, and delete the DEBT-05 comment.

---

### `docker-compose.e2e.yml` (config) — MODIFY

**Analog:** existing `worker:`/`document-worker:`/`chromium-worker:` `extra_hosts` override blocks (`docker-compose.e2e.yml:32-46`).

**Change:** the header comment (`docker-compose.e2e.yml:30-31`) currently says *"worker delivers webhooks (HandleWebhookDeliver) — it is the process that actually dials the E2E test's host-bound receiver."* — this becomes false. Add `webhook-worker-1:`/`webhook-worker-2:` override blocks with the same `extra_hosts: ["host.docker.internal:host-gateway"]`, and either remove the `worker:` override block entirely (image-worker no longer dials webhooks) or leave a comment explaining why it's removed. `api:` keeps its override (`:17-28`) unchanged — `validateCallbackURL` still runs in the api process.

```yaml
  webhook-worker-1:
    extra_hosts:
      - "host.docker.internal:host-gateway"

  webhook-worker-2:
    extra_hosts:
      - "host.docker.internal:host-gateway"
```

---

### `.env.example` (config) — MODIFY

**Analog:** `# Document worker` / `# Chromium (html) worker` sections (`.env.example:32-40`), each a small labeled block with inline rationale comments.

**Verify already present** (per CONTEXT.md's own note — confirmed present at `.env.example:27,29-30`):
```
WEBHOOK_SIGNING_SECRET=change-me-to-a-long-random-secret   # HMAC-SHA256 secret for signed webhook callbacks (required)
WEBHOOK_ALLOW_INSECURE_HTTP=false   # opt-in: allow webhook callback_url to use http (non-https) scheme (default false)
WEBHOOK_ALLOW_PRIVATE_IPS=false   # opt-in: allow webhook callback_url to target RFC1918 private-IP addresses; loopback/link-local/unspecified remain hard-blocked regardless (default false, D-01/D-02)
```
These are currently under the `# Worker` section (`.env.example:23-30`) alongside `WORKER_CONCURRENCY`/`ENGINE_TIMEOUT`/`IMAGE_MAX_RETRY` — consider a new `# Webhook worker` section mirroring `# Document worker`'s structure, to physically separate webhook-worker-only vars from image-worker-only vars now that they run in different binaries:
```
# Webhook worker
WEBHOOK_WORKER_CONCURRENCY=4   # mirrors WORKER_CONCURRENCY's default
WEBHOOK_SIGNING_SECRET=change-me-to-a-long-random-secret   # HMAC-SHA256 secret for signed webhook callbacks (required)
WEBHOOK_PRESIGN_TTL=6h   # presigned download_url lifetime per webhook delivery attempt (optional, default 6h)
WEBHOOK_ALLOW_INSECURE_HTTP=false   # opt-in: allow webhook callback_url to use http (non-https) scheme (default false)
WEBHOOK_ALLOW_PRIVATE_IPS=false   # opt-in: allow webhook callback_url to target RFC1918 private-IP addresses; loopback/link-local/unspecified remain hard-blocked regardless (default false, D-01/D-02)
```
Move `RECONCILER_*` (`.env.example:43-46`) under this new section too (D-04: sweeper now runs only in webhook-worker) or add a note that it's webhook-worker-only going forward.

## Shared Patterns

### `envInt`/`envDuration`/`firstField` helper trio
**Source:** `cmd/worker/main.go:135-160` (byte-identical in `cmd/document-worker/main.go:130-155`)
**Apply to:** `cmd/webhook-worker/main.go` — copy verbatim, no modification needed. This is boilerplate duplicated per-binary throughout `cmd/*`, not a shared package — follow the existing convention (do not refactor into `internal/` as part of this phase; out of scope).

### Prometheus `/metrics` localhost listener + graceful shutdown
**Source:** `cmd/worker/main.go:103-131`
**Apply to:** `cmd/webhook-worker/main.go` — identical shape (own `METRICS_ADDR`, `127.0.0.1:9090` default, `promhttp.Handler()`, 15s shutdown timeout). Every `cmd/*-worker` binary runs its own local metrics listener; this is the established per-process pattern (D-19/T-04-13, cited in every existing `main.go`).

### `log.Fatalf` only at startup, emoji-prefixed lifecycle logs
**Source:** `cmd/worker/main.go:37,43,48,53,58,97,99,118,124,132`
**Apply to:** `cmd/webhook-worker/main.go` — `"🐙 webhook-worker starting (queue=%s)"`, `"🛑 shutting down webhook-worker..."`, `"bye 👋"`. Never `log.Fatalf` inside the asynq handler or sweep loop (that's `internal/worker`/`internal/reconciler`'s job — return errors instead).

### Guarded transaction via `pgx.BeginFunc`
**Source:** `internal/jobs/repo.go` (per CLAUDE.md: lines 47-72, 207-233 — guarded status transitions)
**Apply to:** NOT directly reused by the advisory-lock feature (`pg_try_advisory_lock` is intentionally session-scoped, not transaction-scoped — using `BeginFunc` would be a correctness bug per D-02, since the lock must outlive any single transaction and be tied to a dedicated connection's lifetime). Cited here only to warn the planner away from this otherwise-idiomatic pattern for this specific feature.

## No Analog Found

| File/Feature | Role | Data Flow | Reason |
|---------------|------|-----------|--------|
| Postgres session-level advisory-lock wrapper (D-01/D-02) | service (lock coordination) | event-driven | Zero existing usage of `pgxpool.Acquire`/dedicated `pgx.Conn`/any advisory-lock anywhere in `internal/` or `cmd/` (verified via repo-wide grep) — this is genuinely new infrastructure, not a refactor of an existing pattern. Planner should design fresh using pgx's documented `pool.Acquire(ctx)` → `.Conn()` → `SELECT pg_try_advisory_lock($1)` idiom (standard pgx usage, not project-specific), with the const-lock-key and error-handling conventions noted above applied for consistency with the rest of the codebase. |

## CRITICAL FINDING for planner (flagged per orchestrator request)

**`HandleWebhookDeliver` is NOT storage-free**, contradicting CONTEXT.md D-07's stated assumption ("**НЕ нужно**: S3/MinIO ... webhook-доставка читает job из Postgres, не трогает S3"):

`internal/worker/worker.go:512-527` — for jobs with `Status == jobs.StatusDone`, `HandleWebhookDeliver` calls:
```go
url, err := h.store.PresignGet(ctx, outs[0].ObjectKey, h.presignTTL)
```
`h.store` is a `*storage.Client` (`internal/worker/worker.go:208`), constructed via `storage.New(ctx)` (`internal/storage/storage.go:25`), which reads `S3_ENDPOINT`/`S3_ACCESS_KEY`/`S3_SECRET_KEY`/`S3_BUCKET`/`S3_USE_SSL` from the environment and dials MinIO at construction time (fails fast via `log.Fatalf` in every existing `cmd/*/main.go` if unreachable).

**Consequence:** `cmd/webhook-worker` DOES need:
- Full `storage.New(ctx)` wiring (all `S3_*` env vars)
- `docker-compose.yml`'s `webhook-worker-1`/`-2` services need `depends_on: minio: condition: service_healthy` and the `S3_*` environment block, exactly like `worker`/`document-worker`/`chromium-worker` already have
- `worker.NewHandler`'s `store *storage.Client` parameter cannot be `nil` or omitted

This does not block D-01–D-06 (sweeper/advisory-lock topology is unaffected) but directly contradicts the literal "НЕ нужно: S3/MinIO" line in D-07's binary-config decision. The planner must either (a) treat D-07 as inaccurate on this one point and wire storage into `cmd/webhook-worker` anyway (the code-correct path — omitting it would make every `done`-status webhook delivery fail at `PresignGet`), or (b) resurface this to the user as a scope question before planning proceeds, since it changes `cmd/webhook-worker`'s dependency footprint from "Postgres + Redis only" to "Postgres + Redis + S3/MinIO", which affects the "clean, minimal binary" narrative in D-07 and the container's `depends_on` graph.

**Sweeper has no hidden storage/convert-registry coupling** — confirmed clean: `internal/reconciler/reconciler.go` depends only on the `jobStore`/`enqueuer` interfaces (`:29-48`), which are satisfied by `*jobs.Repo`/`*queue.Client`; `convert.EngineImage`/`EngineDocument`/`EngineHTML` (`:135-139`) are just string constants from `internal/convert`, not a dependency on the `convert.Registry` singleton or any storage/engine-execution code — safe to run in a storage-less-except-for-webhook-delivery binary.

## Metadata

**Analog search scope:** `cmd/`, `internal/reconciler/`, `internal/worker/`, `internal/jobs/`, `internal/queue/`, `internal/storage/`, `internal/db/`, `Dockerfile.*`, `docker-compose*.yml`, `.env.example`
**Files scanned:** `cmd/worker/main.go`, `cmd/document-worker/main.go`, `internal/reconciler/reconciler.go`, `internal/worker/worker.go`, `internal/jobs/repo.go`, `internal/jobs/jobs.go`, `internal/db/db.go`, `internal/queue/queue.go`, `internal/queue/client.go`, `internal/storage/storage.go`, `internal/metrics/queue_collector.go`, `Dockerfile.worker`, `Dockerfile.document-worker`, `docker-compose.yml`, `docker-compose.e2e.yml`, `.env.example`
**Pattern extraction date:** 2026-07-11
