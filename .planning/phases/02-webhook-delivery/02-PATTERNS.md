# Phase 2: Webhook Delivery - Pattern Map

**Mapped:** 2026-07-04
**Files analyzed:** 14
**Analogs found:** 14 / 14 (12 exact/role-match, 2 partial — see "No Analog Found")

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/queue/queue.go` (MODIFY) | config/constants | event-driven | itself (existing `TypeImageConvert`/`QueueImage` block) | exact |
| `internal/queue/client.go` (MODIFY) | service (producer) | event-driven | itself (existing `EnqueueImageConvert`) | exact |
| `internal/webhook/webhook.go` (NEW) | model | CRUD | `internal/jobs/jobs.go` | exact |
| `internal/webhook/repo.go` (NEW) | service/repository | CRUD | `internal/jobs/repo.go` | exact |
| `internal/webhook/sign.go` (NEW) | utility | transform | `internal/auth/hash.go` | role-match |
| `internal/webhook/deliver.go` (NEW) | service (HTTP client) | request-response | `internal/convert/exec.go` (hardened external call) + `internal/storage/storage.go` (external SDK wrapper) | partial |
| `internal/worker/worker.go` (MODIFY) | controller (asynq task handler) | event-driven | itself (existing `Handler.HandleImageConvert`) | exact |
| `cmd/worker/main.go` (MODIFY) | config/entry point | event-driven | itself | exact |
| `internal/api/handlers.go` (MODIFY) | controller | request-response | itself (existing `handleCreateJob`) | exact |
| `internal/api/callback_url.go` (NEW) | utility (validation) | transform | `internal/convert/convert.go` `NormalizeFormat` (input-normalization-then-validate idiom) | partial |
| `internal/jobs/jobs.go` (MODIFY) | model | CRUD | itself | exact |
| `internal/jobs/repo.go` (MODIFY) | repository | CRUD | itself | exact |
| `internal/db/migrations/0003_webhook_dead_letter.sql` (NEW) | migration | batch | `internal/db/migrations/0002_client_api_keys.sql` | exact |
| `internal/webhook/*_test.go` (NEW) | test | — | `internal/jobs/repo_test.go`, `internal/auth/hash_test.go`, `internal/queue/queue_test.go` | role-match |

## Pattern Assignments

### `internal/queue/queue.go` (MODIFY: add webhook task type/queue + payload)

**Analog:** itself, lines 14-48 (`TypeImageConvert`/`QueueImage`/`ConvertPayload`/`NewImageConvertTask`/`ParseConvertPayload`)

**Const pattern to mirror** (lines 14-23):
```go
// Task type names. One asynq task type per engine class operation.
const (
	TypeImageConvert = "image:convert"
)

// Queue names. asynq routes tasks to a queue per engine class so workers and
// autoscaling can be scoped to a single class.
const (
	QueueImage = "image"
)
```
Add `TypeWebhookDeliver = "webhook:deliver"` to the first block and `QueueWebhook = "webhook"` to the second — do not create new blocks, extend the existing ones (matches D-04's "mirrors the existing engine-class queue routing pattern").

**Payload pattern to mirror** (lines 25-48):
```go
type ConvertPayload struct {
	JobID uuid.UUID `json:"job_id"`
}

func NewImageConvertTask(jobID uuid.UUID) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeImageConvert, b, asynq.Queue(QueueImage)), nil
}

func ParseConvertPayload(b []byte) (ConvertPayload, error) {
	var p ConvertPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("unmarshal convert payload: %w", err)
	}
	return p, nil
}
```
New `WebhookPayload{ JobID uuid.UUID }` (payload stays minimal — job_id only, per D-04/CONTEXT.md explicitly), `NewWebhookDeliverTask(jobID uuid.UUID) (*asynq.Task, error)`, `ParseWebhookPayload(b []byte) (WebhookPayload, error)` — same signatures, same error-wrap phrasing style (`"marshal webhook payload: %w"` / `"unmarshal webhook payload: %w"`).

**Retry/backoff (D-05) — new addition, no existing analog in this file.** asynq's `RetryDelayFunc` is set at `asynq.Server` construction (see `cmd/worker/main.go` below), not here, but if a named backoff helper is needed it belongs in this package (e.g. `WebhookRetryDelay(n int, e error, t *asynq.Task) time.Duration`) so `cmd/worker/main.go` stays a thin wiring file, consistent with how `RedisOpt()` lives in `queue.go` rather than in `cmd/*/main.go`.

Attach `asynq.MaxRetry(6)` as a task option in `NewWebhookDeliverTask`, mirroring how `asynq.Queue(QueueImage)` is attached as a task option in `NewImageConvertTask` — i.e. retry count is a property of the task, set once at creation, not scattered across call sites.

---

### `internal/queue/client.go` (MODIFY: add producer method)

**Analog:** itself, lines 27-35 (`EnqueueImageConvert`)

```go
// EnqueueImageConvert puts an image conversion job onto the image queue.
func (c *Client) EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewImageConvertTask(jobID)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue image convert %s: %w", jobID, err)
	}
	return nil
}
```
Add `EnqueueWebhookDeliver(ctx context.Context, jobID uuid.UUID) error` with identical shape and error message `"enqueue webhook deliver %s: %w"`. Note `internal/worker/worker.go`'s `Handler` will need its own way to enqueue this (worker enqueues right after `MarkDone`/`MarkFailed` — see below), so the `Enqueuer`-shaped dependency the worker needs may be narrower than the full `*queue.Client`; follow the `internal/api/api.go` `Enqueuer` interface-segregation pattern if the worker package should not depend on the concrete `*queue.Client` type.

---

### `internal/webhook/webhook.go` (NEW — domain types)

**Analog:** `internal/jobs/jobs.go` (full file, 57 lines)

**Package doc + status-consts + struct pattern to mirror** (lines 1-56):
```go
// Package jobs is the Postgres-backed repository for conversion jobs and their
// inputs, outputs and event log. Postgres is the system of record: status truth
// always lives here.
package jobs

const (
	StatusAwaitingUpload = "awaiting_upload"
	StatusQueued         = "queued"
	...
)

type Job struct {
	ID           uuid.UUID
	ClientID     uuid.UUID
	...
}
```
New `internal/webhook/webhook.go` should open with:
```go
// Package webhook delivers signed job-completion callbacks to client-supplied
// callback URLs and tracks delivery attempts in Postgres.
package webhook
```
and define a `Delivery` struct mirroring `jobs.Input`/`jobs.Output` shape (matches the existing `webhook_deliveries` columns: `id`, `job_id`, `url`, `attempt`, `status_code`, `delivered`, `created_at`, `updated_at`, plus the new `dead_letter bool` from D-10). No status-const block is needed here since `delivered`/`dead_letter` are plain booleans, not a CHECK-constrained status enum (unlike `jobs.Status*`) — do not invent a parallel status-string constant set where the schema already uses booleans.

---

### `internal/webhook/repo.go` (NEW — Postgres repo for delivery attempts)

**Analog:** `internal/jobs/repo.go`, full guarded-transition pattern (lines 1-24, 108-119, 205-242)

**Constructor + pool pattern** (lines 16-24):
```go
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}
```

**Simple insert pattern to mirror for recording an attempt** (`AddOutput`, lines 109-119):
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
Use this shape for `Repo.RecordAttempt(ctx, jobID uuid.UUID, url string, attempt int, statusCode *int, delivered bool) error` — a plain single-statement insert (no need for `pgx.BeginFunc`/row-locking here since `webhook_deliveries` has no state machine to guard, unlike `jobs.status`).

**Guarded-transition pattern (`transition`, lines 205-242) — apply for `MarkDeadLetter`:**
```go
func (r *Repo) transition(
	ctx context.Context, id uuid.UUID, to string, allowedFrom []string,
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
		...
	})
}
```
`webhook_deliveries` has no status enum, so a literal `transition` copy is not appropriate — but the underlying discipline (lock-then-check-then-update in one `pgx.BeginFunc` transaction) still applies to `MarkDeadLetter(ctx, deliveryID uuid.UUID) error`, which should `UPDATE webhook_deliveries SET dead_letter = true WHERE id = $1` on the final failed attempt (D-10), wrapped with `fmt.Errorf("mark dead letter: %w", err)` per the project's error-wrap convention.

---

### `internal/webhook/sign.go` (NEW — HMAC-SHA256 payload signing, D-01)

**Analog:** `internal/auth/hash.go`, full file (39 lines)

**Package-doc + pure-function idiom to mirror** (lines 1-14):
```go
// Package auth provides pure, dependency-free helpers for generating and
// hashing API keys. Keys are high-entropy random tokens, not user-chosen
// passwords, so a fast salted digest (crypto/sha256) is the correct primitive
// here — not a slow password hash like bcrypt/argon2...
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)
```

**Deterministic-digest function shape to mirror** (lines 34-39):
```go
func HashKey(salt []byte, raw string) string {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(raw))
	return hex.EncodeToString(h.Sum(nil))
}
```
New `internal/webhook/sign.go`: a pure, dependency-free function `SignPayload(secret []byte, timestamp int64, body []byte) string` using `crypto/hmac` + `crypto/sha256`, returning a hex digest (project convention: hex, not base64, for digests — see `HashKey`). The timestamp is included in the signed message (not just the body) so receivers can reject replayed requests (WEBHOOK-02). Doc comment explaining *why* HMAC-SHA256 (integrity/authenticity of a webhook payload sent to an external, potentially-adversarial endpoint) mirroring the "why this primitive" comment style in `hash.go` lines 1-5. `secret` is read once from `WEBHOOK_SIGNING_SECRET` by the caller (`cmd/worker/main.go`), never inside this package — same separation as `HashKey`'s `salt` parameter never being read from env inside `auth`.

**Test pattern to mirror:** `internal/auth/hash_test.go` (69 lines) — `TestHashKeyDeterministic`, `TestHashKeyDifferentSalt`, `TestHashKeyOutputFormat`. Write equivalent `TestSignPayloadDeterministic`, `TestSignPayloadDifferentSecret`, `TestSignPayloadOutputFormat` (hex regexp `^[0-9a-f]{64}$` reusable verbatim).

---

### `internal/webhook/deliver.go` (NEW — HTTP delivery of one attempt)

**No exact analog exists** (first outbound-HTTP-client code in the codebase — everything today is either inbound HTTP (`internal/api`) or shelling out to a local CLI (`internal/convert/exec.go`)). Closest partial analogs:

**Bounded-external-call-with-context pattern** from `internal/convert/exec.go` (hardened process exec) — the *timeout-via-context* idiom is the transferable part:
```go
engineCtx, cancel := context.WithTimeout(ctx, h.engineTimout)
defer cancel()
if err := conv.Convert(engineCtx, inPath, outPath, nil); err != nil {
	return fmt.Errorf("convert: %w", err)
}
```
(`internal/worker/worker.go:90-95`) — apply the same shape for the HTTP POST: build an `http.Client{Timeout: 10 * time.Second}` (D-08) or wrap the request context with `context.WithTimeout(ctx, 10*time.Second)`, whichever composes better with asynq's own task context.

**External-client wrapper pattern** from `internal/storage/storage.go` (`PresignGet`, lines 81-88) — thin method returning `(result, error)`, errors always wrapped with the operation + key:
```go
func (c *Client) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	u, err := c.mc.PresignedGetObject(ctx, c.bucket, key, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("presign %q: %w", key, err)
	}
	return u.String(), nil
}
```
New `Deliver(ctx context.Context, delivery Delivery, payload []byte, signature string) (statusCode int, err error)` should follow this shape: build the `http.Request` (POST, `Content-Type: application/json`, custom `X-OctoConv-Signature` header carrying the HMAC hex digest from `sign.go`), execute with the 10s timeout, and classify success as `statusCode >= 200 && statusCode < 300` per D-07 ("Success = HTTP 2xx... any other status code, timeout, or network error triggers a retry"). Do not swallow non-2xx as success; return a plain (unwrapped, non-`asynq.SkipRetry`) error so asynq's default retry policy applies (matches the `internal/worker/worker.go:58-61` genuine-failure pattern, which returns the raw error so asynq retries, versus the `asynq.SkipRetry`-wrapped errors for terminal/unparseable cases).

---

### `internal/worker/worker.go` (MODIFY — add webhook delivery handler + enqueue-on-completion)

**Analog:** itself, `Handler` struct + `HandleImageConvert` (lines 21-63)

**Struct pattern to mirror** (lines 21-35):
```go
type Handler struct {
	repo         *jobs.Repo
	store        *storage.Client
	registry     *convert.Registry
	engineTimout time.Duration
}

func NewHandler(repo *jobs.Repo, store *storage.Client, registry *convert.Registry, engineTimeout time.Duration) *Handler {
	if engineTimeout == 0 {
		engineTimeout = 120 * time.Second
	}
	return &Handler{repo: repo, store: store, registry: registry, engineTimout: engineTimeout}
}
```
Add new fields to the existing `Handler` (webhook repo, signing secret, HTTP delivery client) OR — if the executor/planner decides delivery deserves its own dependency-narrow handler type — a second `WebhookHandler` struct following the identical shape. CONTEXT.md's canonical ref explicitly says "`Handler` struct + `HandleImageConvert` method pattern to mirror for the webhook delivery handler", implying the same `Handler` type gains a second `Handle<Noun>` method (`HandleWebhookDeliver`) rather than a wholly separate type — this keeps one asynq `ServeMux` registration site and one `NewHandler` constructor call in `cmd/worker/main.go`.

**Handler method shape to mirror** (lines 40-63):
```go
func (h *Handler) HandleImageConvert(ctx context.Context, t *asynq.Task) error {
	payload, err := queue.ParseConvertPayload(t.Payload())
	if err != nil {
		// Unparseable payload: nothing we can retry into success.
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	jobID := payload.JobID

	job, err := h.repo.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job %s: %w", jobID, err)
	}

	if err := h.repo.MarkActive(ctx, jobID); err != nil {
		// Already active/done/canceled — let asynq drop it rather than loop.
		return fmt.Errorf("%w: mark active: %v", asynq.SkipRetry, err)
	}

	if err := h.process(ctx, job); err != nil {
		_ = h.repo.MarkFailed(ctx, jobID, "engine_error", err.Error())
		return err
	}
	return nil
}
```
`HandleWebhookDeliver(ctx context.Context, t *asynq.Task) error` mirrors this exactly: parse payload with `asynq.SkipRetry` on unparseable body, `h.repo.Get(ctx, jobID)` to re-read `callback_url`/`status`/error fields (payload carries only `job_id`, per D-04), skip via `asynq.SkipRetry` if `job.CallbackURL == ""` (nothing to deliver to — not a transient failure), regenerate the presigned URL fresh via `h.store.PresignGet` (D-09) only when `job.Status == jobs.StatusDone`, build+sign the payload, call `deliver.Deliver`, record the attempt via `webhookRepo.RecordAttempt`, and on the **final** attempt (asynq exposes this via `asynq.GetRetryCount`/`asynq.GetMaxRetry` from the task context, or via `t.ResultWriter()`/task metadata — check the asynq version's API) call `webhookRepo.MarkDeadLetter` (D-10). Return the raw (unwrapped) delivery error so asynq's own retry/backoff (D-05) applies — never wrap webhook-delivery failures in `asynq.SkipRetry`, since those are exactly the retryable case D-05 exists for.

**Enqueue-after-completion integration point** — inside `process()` (lines 65-115), right after `return h.repo.MarkDone(ctx, job.ID)` succeeds, and inside `HandleImageConvert` right after `_ = h.repo.MarkFailed(...)`:
```go
if err := h.process(ctx, job); err != nil {
	_ = h.repo.MarkFailed(ctx, jobID, "engine_error", err.Error())
	return err   // <- webhook enqueue for the failed case goes near here
}
return nil       // <- webhook enqueue for the done case goes near here
```
Per D-04, enqueue the new `webhook:deliver` task right after `MarkDone`/`MarkFailed`, carrying only `job_id` — same "Postgres-first" discipline as the API's job-creation double-write (`internal/api/handlers.go:87-114`): the status transition must commit before the webhook task is enqueued, never the reverse.

---

### `cmd/worker/main.go` (MODIFY — register handler + multi-queue weights + env wiring)

**Analog:** itself, full file (85 lines)

**Wiring pattern to mirror** (lines 22-56):
```go
h := worker.NewHandler(jobs.NewRepo(pool), store, convert.Default, envDuration("ENGINE_TIMEOUT", 120*time.Second))

mux := asynq.NewServeMux()
mux.HandleFunc(queue.TypeImageConvert, h.HandleImageConvert)

srv := asynq.NewServer(redisOpt, asynq.Config{
	Concurrency: envInt("WORKER_CONCURRENCY", 4),
	Queues:      map[string]int{queue.QueueImage: 1},
})
```
Extend to:
```go
mux.HandleFunc(queue.TypeImageConvert, h.HandleImageConvert)
mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)

srv := asynq.NewServer(redisOpt, asynq.Config{
	Concurrency:    envInt("WORKER_CONCURRENCY", 4),
	Queues:         map[string]int{queue.QueueImage: 2, queue.QueueWebhook: 1}, // weights: image prioritized (D-04)
	RetryDelayFunc: webhookRetryDelay, // exponential backoff + jitter, D-05
})
```
Read `WEBHOOK_SIGNING_SECRET` the same way `cmd/api/main.go` reads `API_KEY_SALT` (lines 49-52 there — `log.Fatalf` if unset, since it's required for every delivery):
```go
salt := []byte(os.Getenv("API_KEY_SALT"))
if len(salt) == 0 {
	log.Fatalf("API_KEY_SALT must be set")
}
```
→
```go
signingSecret := []byte(os.Getenv("WEBHOOK_SIGNING_SECRET"))
if len(signingSecret) == 0 {
	log.Fatalf("WEBHOOK_SIGNING_SECRET must be set")
}
```
Reuse the existing `envInt`/`envDuration`/`firstField` helpers verbatim (lines 59-84) for any new webhook tunables (per-attempt timeout override, presign TTL) — do not duplicate `firstField` a third time; it is already duplicated once between `cmd/api/main.go` and `cmd/worker/main.go`, so keep following that (pre-existing, if regrettable) duplication convention rather than introducing a shared `internal/envutil` package as an out-of-scope refactor.

---

### `internal/api/handlers.go` (MODIFY — accept + validate `callback_url`)

**Analog:** itself, `handleCreateJob` (lines 33-120)

**Form-field read + validate-before-side-effects pattern to mirror** (lines 48-73):
```go
target := convert.NormalizeFormat(r.FormValue(formFieldTarget))
if target == "" {
	writeError(w, http.StatusBadRequest, "missing target format")
	return
}
...
// Validate the conversion pair BEFORE writing anything to storage.
if !convert.Default.Supports(source, target) {
	writeError(w, http.StatusUnprocessableEntity,
		"unsupported conversion: "+source+" -> "+target)
	return
}
```
Add a new const `formFieldCallbackURL = "callback_url"` next to `formFieldFile`/`formFieldTarget` (lines 19-24), read it with `r.FormValue(formFieldCallbackURL)` (optional field — empty means "no webhook", polling still works), and validate it **before** the storage upload (same "validate before side effects" discipline already present for the format pair) using the new `validateCallbackURL` helper (see below). On validation failure: `writeError(w, http.StatusBadRequest, "invalid callback_url")` — same status/style as the other `handleCreateJob` validation failures (lines 50-51, 56-57, 64-65).

**Threading callback_url into the job row** — `s.repo.Create(ctx, jobs.CreateParams{...})` call (lines 89-104) needs a new `CallbackURL: callbackURL` field; this requires the companion `internal/jobs/jobs.go`/`internal/jobs/repo.go` changes below.

---

### `internal/api/callback_url.go` (NEW — SSRF guard, D-03)

**No close analog** — this is the first outbound-URL-validation code in the codebase. Closest structural precedent is the normalize-then-validate idiom in `internal/convert/convert.go`'s `NormalizeFormat`/`Supports` (format validation happens once, synchronously, before any side effect) — same "validate once, up front, at the boundary" discipline applies here per D-03 ("performed once at job-creation time... do NOT re-validate before each delivery attempt").

Suggested shape, matching project error-handling conventions (no swallowed errors, `fmt.Errorf` wrapping for internal detail, but the HTTP-facing message stays a short fixed string per the "never leak internal error text to clients" rule in `internal/api/handlers.go`):
```go
// validateCallbackURL enforces D-03's SSRF guard: a valid https (or http in
// dev) scheme, and a resolved hostname that is not loopback/RFC1918/
// link-local/the cloud metadata endpoint. Performed once at job creation;
// deliberately NOT re-checked before each delivery attempt (accepted residual
// risk for internal-only clients, see PROJECT.md).
func validateCallbackURL(raw string) error {
	...
}
```
Return a plain `error`; `handleCreateJob` maps any non-nil error to `writeError(w, http.StatusBadRequest, "invalid callback_url")` exactly like the existing validation blocks — do not leak the specific rejection reason (matching the "HTTP layer never leaks internal error text" convention).

---

### `internal/jobs/jobs.go` / `internal/jobs/repo.go` (MODIFY — surface `callback_url`)

**Analog:** itself — `Job` struct (jobs.go lines 22-36), `CreateParams` (repo.go lines 26-36), `Create` insert (repo.go lines 42-78), `Get` scan (repo.go lines 121-149)

Add `CallbackURL string` to both `jobs.Job` and `jobs.CreateParams`. In `Repo.Create`'s insert (repo.go lines 49-55), add `callback_url` to the column list and `$7` placeholder:
```go
INSERT INTO jobs (id, client_id, operation, engine, status, source_format, target_format, callback_url)
VALUES ($1, $2, $3, $4, 'queued', $5, $6, $7)
```
In `Repo.Get`'s scan (repo.go lines 122-149), `callback_url` is nullable in the schema (`internal/db/migrations/0001_init.sql:57`, no `NOT NULL`) — follow the exact `src, tgt, code, msg *string` + `deref()` pattern already used for the other nullable text columns:
```go
var j Job
var src, tgt, code, msg *string
...
).Scan(&j.ID, &clientID, &j.Operation, &j.Engine, &j.Status, &src, &tgt,
	&code, &msg, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
...
j.SourceFormat = deref(src)
j.TargetFormat = deref(tgt)
```
Add a `cb *string` local alongside `src, tgt, code, msg`, add `callback_url` to the `SELECT` column list and `Scan` call, and `j.CallbackURL = deref(cb)` alongside the other `deref()` assignments (repo.go lines 144-147) — do not introduce a different nullable-handling idiom for this one column.

---

### `internal/db/migrations/0003_webhook_dead_letter.sql` (NEW)

**Analog:** `internal/db/migrations/0002_client_api_keys.sql`, full file (32 lines)

**Convention to mirror** — top-of-file rationale comment, `ALTER TABLE ... ADD COLUMN`, reuse of `set_updated_at()` if a trigger is needed (it already exists on `webhook_deliveries` from `0001_init.sql:125-127`, so no new trigger needed here), partial-index convention for hot lookup paths (lines 16-27 of `0002`):
```sql
-- Add hashed API-key storage to clients.
--
-- Two independent key slots (primary/secondary)...
ALTER TABLE clients
    ADD COLUMN api_key_hash            text UNIQUE,
    ...
```
New migration:
```sql
-- Add dead-letter tracking to webhook_deliveries (D-10).
--
-- Set true on the row for the final delivery attempt once asynq exhausts
-- MaxRetry (~30 min backoff window, see internal/queue/queue.go). Operators
-- investigate dead-lettered rows via direct SQL in v1 — no CLI/API tooling
-- yet (see WEBHOOK-V2-02 in REQUIREMENTS.md for the planned v2 replay tool).
ALTER TABLE webhook_deliveries
    ADD COLUMN dead_letter boolean NOT NULL DEFAULT false;

CREATE INDEX webhook_deliveries_dead_letter_idx
    ON webhook_deliveries (job_id) WHERE dead_letter = true;
```
Naming follows the existing `NNNN_description.sql` convention (`0001_init.sql`, `0002_client_api_keys.sql` → `0003_webhook_dead_letter.sql`), per CONTEXT.md's "Claude's Discretion" note.

---

## Shared Patterns

### Error wrapping
**Source:** `internal/jobs/repo.go` throughout, `internal/storage/storage.go:61`
**Apply to:** `internal/webhook/repo.go`, `internal/webhook/deliver.go`, `internal/jobs/repo.go` changes
```go
return fmt.Errorf("insert job_output: %w", err)
return fmt.Errorf("upload %q: %w", key, err)
```
Always `fmt.Errorf("<action>[ %q]: %w", ..., err)` — action-first, wrapped, never bare.

### Retryable vs. terminal failure (asynq.SkipRetry)
**Source:** `internal/worker/worker.go:44,55`
**Apply to:** `internal/worker/worker.go` `HandleWebhookDeliver`
```go
return fmt.Errorf("%w: %v", asynq.SkipRetry, err)   // terminal: unparseable payload, no callback_url
return err                                           // retryable: genuine delivery failure (D-05 applies)
```

### Postgres-first double write
**Source:** `internal/api/handlers.go:87-114` (job create-then-enqueue), `internal/jobs/repo.go:42-78`
**Apply to:** `internal/worker/worker.go` webhook enqueue point (status transition commits before task enqueue) and `internal/webhook/repo.go` (record attempt before/alongside delivery, per code_context)

### Environment-variable-only config with required-var fail-fast
**Source:** `cmd/api/main.go:49-52` (`API_KEY_SALT`), `internal/storage/storage.go:31-33`, `internal/queue/queue.go:52-55` (`REDIS_ADDR`)
**Apply to:** `cmd/worker/main.go` (`WEBHOOK_SIGNING_SECRET`)
```go
if endpoint == "" || accessKey == "" || secretKey == "" || bucket == "" {
	return nil, fmt.Errorf("S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY and S3_BUCKET must be set")
}
```

### Never leak internal error detail to HTTP clients
**Source:** `internal/api/handlers.go` throughout (e.g. lines 79-82, 105-108)
**Apply to:** `internal/api/handlers.go`'s new `callback_url` validation branch
```go
if err := s.storage.Upload(ctx, key, file, header.Size, contentType); err != nil {
	writeError(w, http.StatusInternalServerError, "failed to store upload")
	return
}
```

### Package-level doc comment (one file per package)
**Source:** `internal/jobs/jobs.go:1-3`, `internal/worker/worker.go:1`, `internal/auth/hash.go:1-5`
**Apply to:** `internal/webhook/webhook.go` (the package's "primary" file) — every other new file in `internal/webhook/` (`repo.go`, `sign.go`, `deliver.go`) gets no package doc, only the one file that best represents the package's role does, per the one-doc-comment-per-package convention.

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `internal/webhook/deliver.go` | service (HTTP client) | request-response | First outbound-HTTP-call code in the codebase; `internal/api` only ever handles inbound HTTP, `internal/convert/exec.go` only shells out to local CLIs. Composited from the context-timeout idiom in `exec.go`/`worker.go:90-95` and the thin-wrapper-with-wrapped-errors idiom in `storage.go`'s `PresignGet` — see Pattern Assignments above. |
| `internal/api/callback_url.go` | utility (validation) | transform | First SSRF/URL-validation code in the codebase. Structurally follows the "validate once, up front, before side effects" discipline already used for format-pair validation in `handleCreateJob`, but the actual hostname-resolution/IP-range-check logic has no precedent to copy — planner should reference RESEARCH-equivalent guidance (none exists this phase) or standard Go `net.LookupHost` + `net/netip` range checks. |

## Metadata

**Analog search scope:** `internal/api/`, `internal/worker/`, `internal/jobs/`, `internal/queue/`, `internal/storage/`, `internal/auth/`, `internal/convert/`, `internal/ratelimit/`, `internal/db/migrations/`, `cmd/api/`, `cmd/worker/`
**Files scanned:** 20 (full reads, no re-reads; all files ≤ 260 lines so single-pass reads were sufficient)
**Pattern extraction date:** 2026-07-04
