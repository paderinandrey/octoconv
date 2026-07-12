# Phase 10: Document Worker & Reconciler Integration - Pattern Map

**Mapped:** 2026-07-09
**Files analyzed:** 9 (2 new, 7 modified)
**Analogs found:** 9 / 9

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/queue/queue.go` (MODIFY — add `TypeDocumentConvert`/`QueueDocument`/`DocumentUniqueTTL`/`DocumentRetryDelay`) | utility (task/queue definitions) | event-driven | same file, `TypeImageConvert`/`QueueImage`/`ImageUniqueTTL`/`ImageRetryDelay` block | exact (sibling engine class in same file) |
| `internal/queue/client.go` (MODIFY — add `documentMaxRetry`/`documentUniqueTTL`/`EnqueueDocumentConvert`) | service (producer) | event-driven | same file, `imageMaxRetry`/`imageUniqueTTL`/`EnqueueImageConvert` | exact |
| `internal/jobs/repo.go` (MODIFY — `StaleJob` gains `Engine` field; `FindStale` SQL selects `engine`) | model/repository | CRUD | same file, `StaleJob`/`FindStale` (lines 34-213) | exact (must be extended, not just read) |
| `internal/reconciler/reconciler.go` (MODIFY — `enqueuer` interface gains `EnqueueDocumentConvert`; sweep routes by `j.Engine`) | service (background sweep) | event-driven | same file, current hardcoded `EnqueueImageConvert` call at line 127 | exact |
| `internal/worker/worker.go` (MODIFY — add `HandleDocumentConvert`, `isTerminalDocument` or shared classifier) | controller (asynq task handler) | event-driven | same file, `HandleImageConvert` (lines 104-158) + `isTerminal` (lines 41-66) | exact |
| `cmd/document-worker/main.go` (NEW) | config/entrypoint | event-driven | `cmd/worker/main.go` | exact (structural twin, image-only wiring pattern) |
| `Dockerfile.worker` (MODIFY — revert to libvips-only, drop LibreOffice/tini) | config | file-I/O (build) | `Dockerfile.api` (slim single-purpose runtime shape) + git history of `Dockerfile.worker` pre-Phase-9 | exact (revert target) |
| `Dockerfile.document-worker` (NEW) | config | file-I/O (build) | current `Dockerfile.worker` (has the LibreOffice/tini provisioning to relocate) | exact |
| `docker-compose.yml` (MODIFY — add `document-worker` service) | config | request-response (compose wiring) | same file, `worker` service block (lines 95-124) | exact |
| `.env.example` (MODIFY — add `DOCUMENT_WORKER_CONCURRENCY`/`DOCUMENT_ENGINE_TIMEOUT`/`DOCUMENT_MAX_RETRY`) | config | n/a | same file, `WORKER_CONCURRENCY`/`ENGINE_TIMEOUT`/`IMAGE_MAX_RETRY` lines (24-26) | exact |
| `internal/reconciler/reconciler_test.go` (MODIFY — `fakeEnqueuer` gains `EnqueueDocumentConvert`) | test | event-driven | same file, `fakeEnqueuer`/`fakeStore` (lines 17-95) | exact |
| `internal/queue/queue_test.go` (MODIFY — add `TestDocumentUniqueTTL` etc.) | test | event-driven | same file, `TestImageUniqueTTL` (lines 118-155) | exact |
| `internal/worker/worker_test.go` (MODIFY — add `TestHandleDocumentConvert*`) | test | event-driven | same file, `HandleImageConvert` tests | exact |

## Pattern Assignments

### `internal/queue/queue.go` (utility, event-driven)

**Analog:** same file — `TypeImageConvert`/`QueueImage` consts (lines 17-27), `imageRetrySchedule`/`ImageRetryDelay` (lines 140-166), `imageBackoffSum`/`ImageUniqueTTL` (lines 191-237).

**Task type / queue name consts to mirror** (lines 17-27):
```go
const (
	TypeImageConvert   = "image:convert"
	TypeWebhookDeliver = "webhook:deliver"
)

const (
	QueueImage   = "image"
	QueueWebhook = "webhook"
)
```
Add `TypeDocumentConvert = "document:convert"` and `QueueDocument = "document"` alongside these (do not create a new const block — extend the existing ones, matching the file's convention of one block per concern).

**Task constructor pattern to mirror** (lines 51-61, `NewImageConvertTask`):
```go
func NewImageConvertTask(jobID uuid.UUID, maxRetry int, uniqueTTL time.Duration) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeImageConvert, b,
		asynq.Queue(QueueImage),
		asynq.MaxRetry(maxRetry),
		asynq.Unique(uniqueTTL),
	), nil
}
```
`NewDocumentConvertTask` reuses the same `ConvertPayload`/`ParseConvertPayload` (job-id-only payload, no new payload type needed) — only the task type/queue constant differs.

**Retry schedule + delay func pattern to mirror** (lines 140-166, `imageRetrySchedule`/`ImageRetryDelay` — NOT `webhookRetrySchedule`/`WebhookRetryDelay`, since document retries should follow the no-jitter, seconds-or-longer clamped-schedule shape, same reasoning as image, not the jittered webhook shape):
```go
var imageRetrySchedule = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	15 * time.Second,
}

func ImageRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	idx := n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(imageRetrySchedule) {
		idx = len(imageRetrySchedule) - 1
	}
	return imageRetrySchedule[idx]
}
```
`documentRetrySchedule`/`DocumentRetryDelay` mirror this shape exactly. Values are Claude's Discretion (CONTEXT.md) but should scale with `DOCUMENT_ENGINE_TIMEOUT` being 2.5x `ENGINE_TIMEOUT` (300s vs 120s) — a proportionally longer schedule (e.g. seconds-to-tens-of-seconds) is defensible, document with the same "why" comment discipline as `imageRetrySchedule`'s doc comment (lines 140-144).

**`RetryDelayFunc` dispatch switch to extend** (lines 175-184):
```go
func RetryDelayFunc(n int, e error, t *asynq.Task) time.Duration {
	switch t.Type() {
	case TypeImageConvert:
		return ImageRetryDelay(n, e, t)
	case TypeWebhookDeliver:
		return WebhookRetryDelay(n, e, t)
	default:
		return asynq.DefaultRetryDelayFunc(n, e, t)
	}
}
```
Add a `case TypeDocumentConvert: return DocumentRetryDelay(n, e, t)` arm.

**Backoff-sum + UniqueTTL derivation pattern to mirror EXACTLY** (lines 191-237, `imageBackoffSum`/`ImageUniqueTTL` — this is the CONTEXT.md-flagged critical pattern):
```go
func imageBackoffSum(maxRetry int) time.Duration {
	var sum time.Duration
	for i := 0; i < maxRetry; i++ {
		sum += ImageRetryDelay(i, nil, nil)
	}
	return sum
}

// Worst-case formula: (maxRetry+1) * engineTimeout + imageBackoffSum(maxRetry) + margin.
func ImageUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + imageBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}
```
`documentBackoffSum`/`DocumentUniqueTTL` must mirror this shape verbatim (NOT `webhookBackoffSum`'s jitter-inflated shape, since `DocumentRetryDelay` — like `ImageRetryDelay` — has no jitter, so calling `DocumentRetryDelay` directly inside `documentBackoffSum` is correct and safe, unlike the webhook case documented at lines 261-269). Reuse the shared `uniqueTTLSafetyMargin` const (line 189) — do not add a document-specific margin, matching `WebhookUniqueTTL`'s explicit reuse (line "REUSES uniqueTTLSafetyMargin verbatim"). Formula: `(maxRetry+1)*DOCUMENT_ENGINE_TIMEOUT + documentBackoffSum(maxRetry) + uniqueTTLSafetyMargin`, using `DOCUMENT_MAX_RETRY` for `maxRetry`.

**Critical discipline note (from the analog's own doc comments, lines 224-227, 299-302):** the TTL must be DERIVED from `DOCUMENT_MAX_RETRY`/`DOCUMENT_ENGINE_TIMEOUT` at construction time in `client.go`, never hardcoded — so a later env change automatically keeps the lock TTL sound.

---

### `internal/queue/client.go` (service, event-driven)

**Analog:** same file — `imageMaxRetry`/`imageUniqueTTL` fields (lines 18-26), `NewClient` (lines 39-52), `EnqueueImageConvert` (lines 57-67).

**Client struct field pattern to mirror** (lines 14-34):
```go
type Client struct {
	c *asynq.Client

	imageMaxRetry    int
	imageUniqueTTL   time.Duration
	webhookUniqueTTL time.Duration
}
```
Add `documentMaxRetry int` and `documentUniqueTTL time.Duration` fields, each with a doc comment mirroring `imageMaxRetry`'s/`imageUniqueTTL`'s shape (lines 18-26).

**`NewClient` construction pattern to mirror** (lines 39-52):
```go
func NewClient() (*Client, error) {
	opt, err := RedisOpt()
	if err != nil {
		return nil, err
	}
	imageMaxRetry := envInt("IMAGE_MAX_RETRY", 4)
	engineTimeout := envDuration("ENGINE_TIMEOUT", 120*time.Second)
	return &Client{
		c:                asynq.NewClient(opt),
		imageMaxRetry:    imageMaxRetry,
		imageUniqueTTL:   ImageUniqueTTL(imageMaxRetry, engineTimeout),
		webhookUniqueTTL: WebhookUniqueTTL(webhookMaxRetry, webhookPerAttemptTimeout),
	}, nil
}
```
Add `documentMaxRetry := envInt("DOCUMENT_MAX_RETRY", <default>)` and `documentEngineTimeout := envDuration("DOCUMENT_ENGINE_TIMEOUT", 300*time.Second)` (300s per Phase 9 D-01, already established), then populate `documentMaxRetry`/`documentUniqueTTL: DocumentUniqueTTL(documentMaxRetry, documentEngineTimeout)` in the returned struct. Reuses the existing `envInt`/`envDuration`/`firstField` helpers (lines 81-116) unchanged — no new env-parsing helper needed.

**`EnqueueImageConvert` pattern to mirror exactly** (lines 57-67):
```go
func (c *Client) EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewImageConvertTask(jobID, c.imageMaxRetry, c.imageUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue image convert %s: %w", jobID, err)
	}
	return nil
}
```
`EnqueueDocumentConvert` is a direct copy with `Image`→`Document` substitutions.

---

### `internal/jobs/repo.go` (model/repository, CRUD) — REQUIRED but not explicitly named in CONTEXT.md's file list

**Analog:** same file — `StaleJob` struct (lines 34-40) and `FindStale` (lines 182-213).

**Critical gap discovered:** `StaleJob` currently has only `ID`/`Status` — no `Engine` field. The reconciler's sweep loop cannot route `EnqueueImageConvert` vs `EnqueueDocumentConvert` by `jobs.engine` (per D-04) without this. `FindStale`'s SQL also only selects `id, status` (line 194). **Both must change together:**

```go
// current (lines 34-40, 193-198, 204-211)
type StaleJob struct {
	ID     uuid.UUID
	Status string
}
...
rows, err := r.pool.Query(ctx, `
	SELECT id, status FROM jobs
	WHERE (status = 'queued' AND created_at < $1)
	   OR (status = 'active' AND started_at < $2)`,
	queuedCutoff, activeCutoff,
)
...
var j StaleJob
if err := rows.Scan(&j.ID, &j.Status); err != nil { ... }
```
Add an `Engine string` field to `StaleJob`, `SELECT id, status, engine FROM jobs ...`, and `rows.Scan(&j.ID, &j.Status, &j.Engine)`. No migration needed — `jobs.engine` already exists (CONTEXT.md D-04 confirms the CHECK constraint since `0001_init.sql`). `WebhookGapJob`/`FindWebhookGaps` do NOT need this change (webhook delivery is engine-agnostic — same `EnqueueWebhookDeliver` regardless of which engine produced the job).

---

### `internal/reconciler/reconciler.go` (service, event-driven)

**Analog:** same file — `enqueuer` interface (lines 42-45) and the sweep loop's `EnqueueImageConvert` call (line 127).

**`enqueuer` interface to extend** (lines 42-45):
```go
type enqueuer interface {
	EnqueueImageConvert(ctx context.Context, id uuid.UUID) error
	EnqueueWebhookDeliver(ctx context.Context, id uuid.UUID) error
}
```
Add `EnqueueDocumentConvert(ctx context.Context, id uuid.UUID) error` per D-04.

**Sweep-loop routing to replace** (lines 122-139, currently hardcoded):
```go
if err := s.enq.EnqueueImageConvert(ctx, j.ID); err != nil {
	if errors.Is(err, asynq.ErrDuplicateTask) {
		continue
	}
	continue
}
```
Must dispatch on `j.Engine` (now available once `StaleJob` is extended per above) — e.g.:
```go
var enqueueErr error
switch j.Engine {
case "document":
	enqueueErr = s.enq.EnqueueDocumentConvert(ctx, j.ID)
default:
	enqueueErr = s.enq.EnqueueImageConvert(ctx, j.ID)
}
if enqueueErr != nil {
	if errors.Is(enqueueErr, asynq.ErrDuplicateTask) {
		continue
	}
	continue
}
```
Keep the `default: image` fallback deliberate and documented — every other engine value in the CHECK constraint (`av`, `cad`, `archive`, `probe`) is out of scope for this milestone and currently unroutable; document that a future engine addition needs a new `case` here (mirroring the doc comments' existing "OBS-01/Phase 4 owns X" style of scoping notes elsewhere in this file, e.g. line 82). The exhaustion path (lines 108-119, `EnqueueWebhookDeliver` on cap exceeded) is engine-agnostic and needs NO change — webhook delivery does not depend on the source engine.

---

### `internal/worker/worker.go` (controller, event-driven)

**Analog:** same file — `Handler`/`HandleImageConvert` (lines 68-158), `isTerminal` (lines 41-66), `process` (lines 245-306).

**Note on scope:** CONTEXT.md's domain boundary places the actual `HandleDocumentConvert` handler logic in this phase (the worker binary must exist and function), but the LibreOffice-specific terminal-error classification signatures belong to Phase 9's `internal/convert/libreoffice.go` (already exists — see `docsniff.go`/`libreoffice.go` in `internal/convert/`). Do not re-derive classification signatures here; this phase wires the queue/worker/reconciler plumbing around the already-existing `LibreOfficeConverter`.

**`Handler` struct + `NewHandler` pattern to mirror** (lines 68-102): the existing `Handler` already carries a `*convert.Registry` (not an image-specific converter), so `HandleDocumentConvert` can likely reuse the SAME `Handler`/`NewHandler` (constructed once per binary, pointed at `convert.Default` — which already includes the LibreOffice converter registered via `init()` in `internal/convert/converters.go`) rather than requiring a parallel `DocumentHandler` type. Confirm during planning whether `cmd/document-worker/main.go` constructs its own `worker.Handler` (recommended — mirrors `cmd/worker/main.go`'s existing construction exactly, just with `DOCUMENT_ENGINE_TIMEOUT` instead of `ENGINE_TIMEOUT`) rather than sharing a `Handler` instance across binaries (it can't — separate processes).

**`HandleImageConvert` pattern to mirror exactly for `HandleDocumentConvert`** (lines 104-158, structural skeleton — only the metric queue-name constant and any document-specific terminal-error classification differ):
```go
func (h *Handler) HandleImageConvert(ctx context.Context, t *asynq.Task) error {
	payload, err := queue.ParseConvertPayload(t.Payload())
	if err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	jobID := payload.JobID

	job, err := h.repo.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job %s: %w", jobID, err)
	}

	if err := h.repo.MarkActive(ctx, jobID); err != nil {
		return fmt.Errorf("%w: mark active: %v", asynq.SkipRetry, err)
	}

	start := time.Now()
	if err := h.process(ctx, job); err != nil {
		if isTerminal(err) {
			_ = h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", map[string]any{"engine_stderr": err.Error()})
			metrics.RecordJobOutcome(queue.QueueImage, jobs.StatusFailed, time.Since(start))
			if job.CallbackURL != "" {
				_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
			}
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		return err
	}
	metrics.RecordJobOutcome(queue.QueueImage, jobs.StatusDone, time.Since(start))
	if job.CallbackURL != "" {
		_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
	}
	return nil
}
```
`HandleDocumentConvert` is this skeleton with `queue.QueueImage`→`queue.QueueDocument` in both `metrics.RecordJobOutcome` calls, and `payload`/`job`/`jobID` reused as-is (same `ConvertPayload` type — no new payload type). `h.process(ctx, job)` (lines 245-306) is ALREADY engine-agnostic — it calls `h.registry.Lookup(job.SourceFormat, job.TargetFormat)`, which works for any registered converter pair including LibreOffice's — so `process` needs NO changes; both `HandleImageConvert` and `HandleDocumentConvert` can call the same unmodified `process` method.

**`isTerminal` classification (lines 41-66)** currently only recognizes vips-specific stderr signatures (`terminalVipsSignatures`, lines 27-36) plus the engine-agnostic `"no converter for"` (registry miss) and `minio.NoSuchKey` (missing input) checks. If document conversions need their own terminal-signature classification (e.g. LibreOffice-specific corrupt-file stderr patterns), check whether Phase 9's `internal/convert/libreoffice.go`/`libreoffice_test.go` already defined such signatures — if so, extend `isTerminal` with a second signature list (`terminalLibreOfficeSignatures`) checked alongside `terminalVipsSignatures`, keeping the function a single shared classifier reused by both handlers (matching this file's existing engine-agnostic reuse of `isTerminal` in `HandleImageConvert` — no per-engine `isTerminal` duplication).

---

### `cmd/document-worker/main.go` (NEW — config/entrypoint, event-driven)

**Analog:** `cmd/worker/main.go` (entire file, 161 lines) — direct structural twin.

**Full wiring pattern to mirror**, with substitutions:
- Package doc comment: `"Command worker runs the OctoConv image-class worker..."` → `"Command document-worker runs the OctoConv document-class worker: it consumes the document queue and executes LibreOffice conversions."`
- `worker.NewHandler(..., envDuration("ENGINE_TIMEOUT", 120*time.Second), ...)` → `envDuration("DOCUMENT_ENGINE_TIMEOUT", 300*time.Second)` (300s per Phase 9 D-01)
- `mux.HandleFunc(queue.TypeImageConvert, h.HandleImageConvert)` → `mux.HandleFunc(queue.TypeDocumentConvert, h.HandleDocumentConvert)` (this binary registers ONLY the document handler — per the milestone-level "separate binary, separate process" decision cited in CONTEXT.md's Established Patterns, do NOT also register `HandleWebhookDeliver`/`TypeWebhookDeliver` here unless CONTEXT.md's D-04-adjacent decisions call for document-worker to also own webhook delivery for its own jobs — check whether webhook delivery stays solely on the image `cmd/worker` binary or must be duplicated; the safer literal read of D-04/D-01 is that `cmd/document-worker` owns ONLY `TypeDocumentConvert`, since webhook delivery is engine-agnostic and already lives on the existing `cmd/worker` process — flag this ambiguity for the planner to resolve explicitly against REQUIREMENTS.md DOC-07/08/09)
- `asynq.Config{Concurrency: envInt("WORKER_CONCURRENCY", 4), Queues: map[string]int{queue.QueueImage: 2, queue.QueueWebhook: 1}, ...}` → `Concurrency: envInt("DOCUMENT_WORKER_CONCURRENCY", 2)` (2 per CONTEXT.md's Claude's Discretion ballpark), `Queues: map[string]int{queue.QueueDocument: 1}` (single queue, no webhook queue if the above is resolved as "document-worker does not deliver webhooks")
- `sweeper := reconciler.NewSweeper(repo, qc, reconciler.Config{...})` — the reconciler sweep should run in ONLY ONE process to avoid double-sweeping the same stale jobs from two binaries; mirror `cmd/worker/main.go`'s existing sweeper wiring in `cmd/worker/main.go` unchanged, and do NOT duplicate `reconciler.NewSweeper`/`go sweeper.Run(ctx)` in `cmd/document-worker/main.go` (flag this explicitly for the planner — running two independent sweepers against the same `RECONCILER_*` thresholds is a correctness risk, not just redundant work, since both would race to recover the same stranded job)
- Startup log line: `log.Printf("🐙 worker starting (queues=%s,%s)", queue.QueueImage, queue.QueueWebhook)` → `log.Printf("🐙 document-worker starting (queue=%s)", queue.QueueDocument)` (matches `cmd/worker/main.go:97`'s emoji-prefixed style, `cmd/api/main.go`'s established convention per CLAUDE.md)
- `metricsAddr`/`METRICS_ADDR` handling, graceful shutdown block, `envInt`/`envDuration`/`firstField` helpers (lines 106-161): copy verbatim, unexported-per-package duplication is the established convention (CLAUDE.md: "Duplicated from cmd/worker/main.go's helper of the same name").
- `WEBHOOK_SIGNING_SECRET`/`webhook.NewRepo`/`webhook.NewDeliverer` wiring (lines 51-54, 69-70): only needed if `cmd/document-worker` ends up delivering webhooks (see ambiguity flagged above) — if not, this binary's `worker.NewHandler` call still requires non-nil `webhookRepo`/`deliverer` params per the current `NewHandler` signature (lines 82), so either the signature needs adjusting or these are still wired but simply never exercised (since `HandleDocumentConvert` need not call `h.enqueuer.EnqueueWebhookDeliver` at all if this binary opts out) — planner should verify `NewHandler`'s signature doesn't force document-worker to duplicate webhook config unnecessarily.

**Prometheus queue-depth collector line to mirror** (line 95):
```go
prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage, queue.QueueWebhook))
```
→ `metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueDocument)` (variadic-looking signature — confirm exact signature in `internal/metrics` before assuming it accepts a single queue name; check `internal/metrics/*.go` if not already read).

---

### `Dockerfile.worker` (MODIFY — revert) and `Dockerfile.document-worker` (NEW)

**Current `Dockerfile.worker` state (to be reverted, D-01)** — full file:
```dockerfile
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      tini \
      libvips-tools \
      libreoffice-writer-nogui \
      libreoffice-calc-nogui \
      libreoffice-impress-nogui \
      fonts-crosextra-carlito \
      fonts-crosextra-caladea \
      fonts-liberation2 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/worker /usr/local/bin/worker
USER nobody
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/worker"]
```

**Revert target for `Dockerfile.worker`** — drop `tini` and the 6 LibreOffice/font lines, drop the tini ENTRYPOINT wrapper (image engine has no orphaned-grandchild risk — libvips is a single synchronous CLI invocation, not a forking daemon like `soffice.bin`), keep `ca-certificates`/`libvips-tools`:
```dockerfile
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      libvips-tools \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/worker /usr/local/bin/worker
USER nobody
ENTRYPOINT ["/usr/local/bin/worker"]
```
(Update the stage-9 comment "Runtime stage: image engine needs the libvips CLI; document engine needs LibreOffice (soffice)." — the second clause becomes stale once split; move it to the new Dockerfile.)

**`Dockerfile.document-worker` (NEW)** — takes the tini + LibreOffice + font block being removed above, retargets the build to `cmd/document-worker`:
```dockerfile
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/document-worker ./cmd/document-worker

# Runtime stage: document engine needs LibreOffice (soffice) headless conversion.
FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      tini \
      libreoffice-writer-nogui \
      libreoffice-calc-nogui \
      libreoffice-impress-nogui \
      fonts-crosextra-carlito \
      fonts-crosextra-caladea \
      fonts-liberation2 \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/document-worker /usr/local/bin/document-worker
USER nobody
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/document-worker"]
```
Preserve the `tini`-as-PID-1 comment (lines 26-29 of current `Dockerfile.worker`) verbatim in the new file — it documents a live-verified (Phase 9's 09-02) correctness requirement specific to `soffice.bin`'s forking behavior, not a stylistic choice.

**`Dockerfile.worker-test` (Phase 9, existing) — Claude's Discretion, evaluate reuse:** already builds/tests against LibreOffice + tini + procps for the DOC-06 process-group-kill live test; CONTEXT.md D-01 says it "doesn't need to move" since it's independent of both runtime Dockerfiles. No changes required unless the planner decides to rename it for clarity now that `Dockerfile.document-worker` exists alongside it (e.g. avoid confusion between the two LibreOffice-containing Dockerfiles).

---

### `docker-compose.yml` (MODIFY — new `document-worker` service)

**Analog:** same file, `worker` service block (lines 95-124):
```yaml
worker:
  build:
    context: .
    dockerfile: Dockerfile.worker
  container_name: octoconv-worker
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
    WORKER_CONCURRENCY: "4"
    ENGINE_TIMEOUT: "120s"
    WEBHOOK_SIGNING_SECRET: "dev-only-change-me-in-real-deploys"
    METRICS_ADDR: "127.0.0.1:9090"
  deploy:
    resources:
      limits:
        cpus: "2.0"
        memory: 1g
```
`document-worker` mirrors this exactly with: `dockerfile: Dockerfile.document-worker`, `container_name: octoconv-document-worker`, `DOCUMENT_WORKER_CONCURRENCY: "2"` (or planner's finalized default) instead of `WORKER_CONCURRENCY`, `DOCUMENT_ENGINE_TIMEOUT: "300s"` instead of `ENGINE_TIMEOUT`, SAME `cpus: "2.0"`/`memory: 1g` limits (D-02 — explicitly not differentiated from the image worker's envelope). `METRICS_ADDR` must be a DIFFERENT localhost port than the `worker` service's `127.0.0.1:9090` if both run on the same compose network/host (check whether compose containers share a network namespace — they do NOT by default, so `127.0.0.1:9090` inside each container is fine and does not collide; no change needed there). Include `WEBHOOK_SIGNING_SECRET` only if the ambiguity flagged in the `cmd/document-worker/main.go` section above resolves to "document-worker also delivers webhooks."

---

### `.env.example` (MODIFY — new env vars)

**Analog:** same file, `# Worker` section (lines 23-30):
```
# Worker
WORKER_CONCURRENCY=4
ENGINE_TIMEOUT=120s
IMAGE_MAX_RETRY=4   # small bounded retry budget for image conversion (D-05, smaller than webhook's 6); also feeds the derived per-job uniqueness-lock TTL together with ENGINE_TIMEOUT
```
Add a parallel block (either extending `# Worker` or a new `# Document worker` section — the latter is cleaner given the separate-process architecture, and matches this file's existing pattern of one comment-headed section per concern, e.g. `# Reconciler`, `# Storage lifecycle`):
```
# Document worker
DOCUMENT_WORKER_CONCURRENCY=2   # lower than WORKER_CONCURRENCY's 4 — soffice.bin's heavier per-conversion memory footprint within the same cpus:2.0/memory:1g ceiling (D-03)
DOCUMENT_ENGINE_TIMEOUT=300s   # Phase 9 D-01
DOCUMENT_MAX_RETRY=<N>   # bounded retry budget for document conversion, mirrors IMAGE_MAX_RETRY's precedent; also feeds the derived per-job uniqueness-lock TTL together with DOCUMENT_ENGINE_TIMEOUT
```
Follow the exact inline-comment style (trailing `# comment`, tolerated by `firstField` per CLAUDE.md's Configuration conventions) already used throughout this file.

---

### `internal/reconciler/reconciler_test.go` (test)

**Analog:** same file, `fakeEnqueuer` (lines 81-95):
```go
type fakeEnqueuer struct {
	enqueueImageErr   error
	imageCalls        []uuid.UUID
	webhookCalls      []uuid.UUID
	enqueueWebhookErr error
}

func (f *fakeEnqueuer) EnqueueImageConvert(ctx context.Context, id uuid.UUID) error {
	f.imageCalls = append(f.imageCalls, id)
	return f.enqueueImageErr
}

func (f *fakeEnqueuer) EnqueueWebhookDeliver(ctx context.Context, id uuid.UUID) error {
	f.webhookCalls = append(f.webhookCalls, id)
	return f.enqueueWebhookErr
}
```
Add `enqueueDocumentErr error` and `documentCalls []uuid.UUID` fields plus an `EnqueueDocumentConvert` method mirroring `EnqueueImageConvert`'s shape, so `fakeEnqueuer` satisfies the extended `enqueuer` interface. `fakeStore.jobs`/`stale` fixtures (lines 20-21, 36-40) will also need `Engine` populated on `jobs.StaleJob{}` literals in existing tests once that field is added — a new test (e.g. `TestSweepRoutesDocumentJobsToDocumentQueue`) should assert `documentCalls` gets the right job ID when `StaleJob.Engine == "document"` and `imageCalls` does NOT, mirroring the existing `TestSweep...` test shapes (lines 108-150ish) for structure.

---

### `internal/queue/queue_test.go` (test)

**Analog:** same file, `TestImageUniqueTTL` (lines 118-155):
```go
func TestImageUniqueTTL(t *testing.T) {
	maxRetry := ...
	engineTimeout := ...
	got := ImageUniqueTTL(maxRetry, engineTimeout)
	want := ...
	if got != want { t.Errorf(...) }
	// monotonicity checks: growth with maxRetry, growth with engineTimeout
}
```
`TestDocumentUniqueTTL` mirrors this exactly with `DocumentUniqueTTL`/`DOCUMENT_ENGINE_TIMEOUT`'s 300s default. Also mirror `TestImageRetryDelaySchedule` (line 66) → `TestDocumentRetryDelaySchedule`, and `TestConvertPayloadRoundTrip` (line 14, uses `NewImageConvertTask`) → an equivalent using `NewDocumentConvertTask`.

---

## Shared Patterns

### Env var parsing (`envInt`/`envDuration`/`firstField`)
**Source:** `internal/queue/client.go` lines 81-116 (package-private copy), `cmd/worker/main.go` lines 135-160 (duplicate copy)
**Apply to:** `cmd/document-worker/main.go` (new duplicate, per-package convention — CLAUDE.md explicitly documents this as intentional duplication, not a shared utility to extract)
```go
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(firstField(v)); err == nil {
			return n
		}
	}
	return def
}
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(firstField(v)); err == nil {
			return d
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

### Genuinely-derived (not copy-pasted) TTL discipline
**Source:** `internal/queue/queue.go` `ImageUniqueTTL`'s doc comment (lines 224-227) and `WebhookUniqueTTL`'s (lines 299-302); established as a named pattern in Phase 6's `WebhookUniqueTTL` correction (CONTEXT.md's Established Patterns)
**Apply to:** `DocumentUniqueTTL` — the formula must reference `DOCUMENT_MAX_RETRY`/`DOCUMENT_ENGINE_TIMEOUT` inputs symbolically (via function parameters), never bake in the numeric defaults as hardcoded constants, so a later env change automatically keeps the derivation sound.

### Engine-class queue routing (asynq queue-per-engine, `job_id`-only payloads)
**Source:** `internal/queue/queue.go` (`ConvertPayload`, `TypeImageConvert`/`QueueImage`) and CLAUDE.md's documented "Engine-class queue routing" pattern
**Apply to:** `TypeDocumentConvert`/`QueueDocument` — reuse `ConvertPayload`/`ParseConvertPayload` unchanged (no new payload struct), since all job detail is re-read from Postgres by the handler, identical to the image path.

### Postgres-first double write + guarded status transitions
**Source:** `internal/jobs/repo.go` `Repo.transition` (lines ~200-234, per CLAUDE.md's Pattern Overview)
**Apply to:** No change needed — `HandleDocumentConvert` calls the SAME `h.repo.MarkActive`/`MarkFailed`/`MarkDone` methods as `HandleImageConvert`; the guarded-transition machinery is already engine-agnostic (status transitions don't branch on `engine`).

### Emoji-prefixed startup/shutdown logging
**Source:** `cmd/worker/main.go` lines 97, 124, 132; CLAUDE.md's Logging conventions
**Apply to:** `cmd/document-worker/main.go` — `"🐙 document-worker starting (queue=%s)"`, `"🛑 shutting down document-worker..."`, `"bye 👋"`.

## No Analog Found

None — every file in CONTEXT.md's Integration Points list has a direct, same-repo analog since this phase is explicitly "mechanical pattern-mirroring of existing engine-class-routing conventions" (per the orchestrator's framing, confirmed true on inspection).

## Open Questions For Planner (raised during pattern extraction, not resolved by CONTEXT.md)

1. **Does `cmd/document-worker` register `TypeWebhookDeliver`/`HandleWebhookDeliver`, or does webhook delivery for document jobs stay exclusively on the existing `cmd/worker` process?** `worker.Handler`'s constructor (`NewHandler`, `internal/worker/worker.go` lines 82-102) requires `webhookRepo`/`deliverer` params regardless; `HandleImageConvert`/`HandleDocumentConvert` both call `h.enqueuer.EnqueueWebhookDeliver` internally on completion (not a separate mux registration) — re-reading `cmd/worker/main.go` lines 84-85, `HandleWebhookDeliver` IS mux-registered separately from `HandleImageConvert`, meaning the CURRENT design already funnels ALL webhook deliveries (regardless of originating engine) through the single `cmd/worker` process's `webhook` queue. This strongly suggests `cmd/document-worker` should NOT register `TypeWebhookDeliver` at all (it only needs `TypeDocumentConvert`) but STILL needs a working `h.enqueuer.EnqueueWebhookDeliver` call inside `HandleDocumentConvert` to push onto the `webhook` queue that `cmd/worker` consumes — i.e. `document-worker` produces webhook tasks but never consumes them. Confirm this reading during planning; it affects both `cmd/document-worker/main.go`'s mux wiring and `docker-compose.yml`'s `WEBHOOK_SIGNING_SECRET` inclusion.
2. **Does the reconciler sweep run inside `cmd/document-worker` too, or only `cmd/worker`?** Running it in both risks double-recovery races (see reconciler.go section above). Recommend: sweep stays solely in `cmd/worker` (the existing single sweeper instance already becomes engine-aware per D-04, since one sweeper can enqueue to either queue) — `cmd/document-worker` need not wire `reconciler.NewSweeper` at all.
3. **`metrics.NewQueueDepthCollector`'s exact signature** — verify it accepts a single/variadic queue-name list before assuming `NewQueueDepthCollector(inspector, queue.QueueDocument)` compiles; not read during this pattern pass (out of the explicitly-flagged file set), check `internal/metrics/*.go` during planning.

## Metadata

**Analog search scope:** `internal/queue/`, `internal/reconciler/`, `internal/worker/`, `internal/jobs/`, `cmd/worker/`, `cmd/api/`, repo root (`Dockerfile*`, `docker-compose.yml`, `.env.example`)
**Files scanned:** `internal/queue/queue.go`, `internal/queue/client.go`, `internal/queue/queue_test.go`, `internal/reconciler/reconciler.go`, `internal/reconciler/reconciler_test.go`, `internal/worker/worker.go`, `internal/jobs/jobs.go`, `internal/jobs/repo.go`, `cmd/worker/main.go`, `Dockerfile.worker`, `Dockerfile.api`, `Dockerfile.worker-test`, `docker-compose.yml`, `.env.example`
**Pattern extraction date:** 2026-07-09
