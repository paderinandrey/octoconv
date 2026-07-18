# Phase 31: Queue, Worker & Routing Integration - Pattern Map

**Mapped:** 2026-07-18
**Files analyzed:** 12 (2 new, 10 modified)
**Analogs found:** 12 / 12 (this is a "4th engine class" integration phase — every splice point has a proven 3x-repeated in-repo analog; the document/html engine-class addition is the closest match for nearly every file)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/db/migrations/0006_audio_engine.sql` | migration | batch (DDL) | `internal/db/migrations/0005_html_engine.sql` | exact |
| `internal/convert/converters.go` | config (registry wiring) | event-driven (init-time registration) | itself, prior `Default.Register(ChromiumConverter{})` line | exact |
| `internal/convert/whisper.go` (add `SetAudioModelPath`/`audioModelPath`/`effectiveAudioModelPath`, or a new small file alongside it) | config (setter) | request-response (single write, many reads) | `internal/convert/verapdf.go` (`SetVeraPDFTimeout`/`verapdfTimeout`/`effectiveVeraPDFTimeout`) | exact |
| `internal/queue/queue.go` (additions) | service (task/queue definitions) | event-driven (task construction) + batch (TTL derivation) | itself, document class block (`TypeDocumentConvert`, `QueueDocument`, `NewDocumentConvertTask`, `documentRetrySchedule`, `DocumentRetryDelay`, `documentBackoffSum`, `DocumentUniqueTTL`) | exact |
| `internal/queue/client.go` (additions) | service (producer client) | event-driven (enqueue) | itself, document fields/method (`documentMaxRetry`/`documentUniqueTTL`, `EnqueueDocumentConvert`) | exact |
| `internal/worker/worker.go` (additions: `HandleAudioConvert`, `isAudioTerminal`, duration-guard splice in `process()`) | controller (asynq task handler) | event-driven (consume → orchestrate pipeline) | `HandleDocumentConvert` for the handler shape; `isDocumentTerminal`/`isHTMLTerminal` for classifier shape (but see Pattern 4 — do NOT copy `timeoutIsTerminal` body) | exact (handler shape) / partial (classifier — genuinely new shape) |
| `internal/reconciler/reconciler.go` (additions: `enqueuer` interface method + `sweep()` switch case) | controller (fleet-wide sweeper routing) | event-driven | itself, `case convert.EngineHTML: enqueueErr = s.enq.EnqueueHTMLConvert(ctx, j.ID)` | exact |
| `internal/api/api.go` (add `EnqueueAudioConvert` to `Enqueuer` interface) | model/interface (dependency contract) | request-response | itself, `Enqueuer` interface's `EnqueueHTMLConvert` line | exact |
| `internal/api/handlers.go` (additions: SniffAudio splice, opts-switch `EngineAudio` case, enqueue-switch `EngineAudio` case) | controller (HTTP handler) | request-response | itself — `EngineHTML` branches in the content-detection chain (`LooksLikeHTML`), opts switch, and enqueue switch | exact |
| `cmd/audio-worker/main.go` | controller (process entry point) | event-driven (asynq server bootstrap) | `cmd/document-worker/main.go` | exact |
| `.env.example` (additions) | config | — | itself, `# Document worker` / `# Chromium (html) worker` blocks | exact |
| `internal/queue/queue_test.go` (add `TestAudioUniqueTTL`) | test | batch (pure-function assertions) | `TestDocumentUniqueTTL` (`internal/queue/queue_test.go:262-283`) | exact |
| `internal/reconciler/reconciler_test.go` (add `EnqueueAudioConvert` to `fakeEnqueuer`, add `TestSweepRoutesAudioJobsToAudioQueue`) | test | event-driven | `fakeEnqueuer.EnqueueHTMLConvert`; `TestSweepRoutesDocumentJobsToDocumentQueue` (`reconciler_test.go:202-225`) | exact |

## Pattern Assignments

### `internal/db/migrations/0006_audio_engine.sql` (migration)

**Analog:** `internal/db/migrations/0005_html_engine.sql` (full file, 12 lines)

**Full template to copy verbatim, swapping only the added value:**
```sql
-- Source: internal/db/migrations/0005_html_engine.sql (verified read, this repo)
-- Add 'html' to the jobs.engine allow-list (HTML-01/D-08).
--
-- Hard prerequisite for the third (chromium) engine class: no engine="html"
-- job row can be created until this constraint accepts the value. The
-- constraint name jobs_engine_check is Postgres's auto-generated name for
-- the inline, unnamed column CHECK declared in 0001_init.sql (the standard
-- <table>_<column>_check convention for an unnamed CHECK) -- a live \d jobs
-- confirmation happens during Plan 05 acceptance; if the live name differs,
-- this migration's DROP is corrected then.
ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check
    CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe', 'html'));
```
**Audio version (0006), same DROP/ADD shape, current full allow-list plus `'audio'`:**
```sql
-- Add 'audio' to the jobs.engine allow-list (AUD-05).
ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check
    CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe', 'html', 'audio'));
```
Original column definition, for reference (`internal/db/migrations/0001_init.sql:47-48`):
```sql
engine         text NOT NULL
               CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe')),
```
`db.Migrate()` (embedded-migration runner) runs automatically at `cmd/api/main.go:52` on every API startup — file must be numbered `0006_` to be picked up by the embed glob, next after `0005_`.

---

### `internal/convert/converters.go` (config, registry wiring)

**Analog:** itself (current 3-line body)

**Current file, verbatim (11 lines):**
```go
// Source: internal/convert/converters.go (verified read, this repo, full file)
package convert

// init wires concrete converters into the Default registry. To add support for
// a new engine or format pair, register it here with a single line.
func init() {
	Default.Register(LibvipsConverter{})
	Default.Register(LibreOfficeConverter{})
	Default.Register(ChromiumConverter{})
	// Future engines (one line each):
	// Default.Register(FFmpegConverter{})
}
```
**Fix — add one line** (`AudioConverter{}` zero-value; its `modelPath` field resolves via the new `SetAudioModelPath` setter, not here):
```go
func init() {
	Default.Register(LibvipsConverter{})
	Default.Register(LibreOfficeConverter{})
	Default.Register(ChromiumConverter{})
	Default.Register(AudioConverter{})
}
```
`Classes()` (`internal/convert/convert.go:108-125`) and `EngineFor`/`Registry.Lookup` become audio-aware automatically — no handler-side code needs to change for `/v1/formats` or `EngineFor` (verified: `Classes()` has no engine-specific code, only a generic `for pair, c := range r.m` walk).

---

### `internal/convert/whisper.go` (or new small file) — `SetAudioModelPath` setter

**Analog:** `internal/convert/verapdf.go:10-36` (full setter block, verified read)

```go
// Source: internal/convert/verapdf.go:10-36 (verified read, this repo) -- the shape to replicate
var verapdfTimeout time.Duration

// SetVeraPDFTimeout stores the VERAPDF_TIMEOUT budget for every subsequent
// ValidatePDFA call. Call exactly once at process startup, BEFORE the asynq
// server starts consuming tasks (single write before any concurrent reader
// -- no mutex needed, mirroring how engineTimeout is threaded into
// NewHandler).
func SetVeraPDFTimeout(d time.Duration) {
	verapdfTimeout = d
}

// effectiveVeraPDFTimeout returns the configured timeout, defaulting to 60s
// when SetVeraPDFTimeout was never called (verapdfTimeout == 0).
func effectiveVeraPDFTimeout() time.Duration {
	if verapdfTimeout > 0 {
		return verapdfTimeout
	}
	return 60 * time.Second
}
```
**Fix — add the same three-part shape for the model path** (a `string`, not a `time.Duration`; the fallback tier is `defaultAudioModelPath`, not a numeric default):
```go
var audioModelPath string

// SetAudioModelPath stores the AUDIO_MODEL_PATH override for every
// subsequent AudioConverter.model() call whose own modelPath field is
// unset. Call exactly once at process startup (cmd/audio-worker/main.go),
// BEFORE the asynq server starts consuming tasks -- mirrors
// SetVeraPDFTimeout's single-write-before-any-concurrent-reader contract.
func SetAudioModelPath(path string) {
	audioModelPath = path
}
```
And change `AudioConverter.model()` (`internal/convert/whisper.go:47-52`, current 2-tier fallback) to a 3rd tier:
```go
// Current (2-tier), internal/convert/whisper.go:47-52:
func (c AudioConverter) model() string {
	if c.modelPath != "" {
		return c.modelPath
	}
	return defaultAudioModelPath
}
// Add a middle tier reading audioModelPath before falling back to defaultAudioModelPath.
```
Convention this mirrors: `internal/convert` never calls `os.Getenv` directly (`verapdf.go:10-14`'s own doc comment states this explicitly) — the setter is called from `main()`, not from `converters.go`'s `init()`.

---

### `internal/queue/queue.go` (additions — 4th task-type/queue/TTL block)

**Analog:** the document block, `internal/queue/queue.go:81-101` (task const), `:219-246` (retry schedule/delay), `:349-384` (backoff sum/UniqueTTL) — all verified read in full above.

**Task-type/queue-name consts** (`internal/queue/queue.go:18-36`):
```go
const (
	TypeImageConvert    = "image:convert"
	TypeWebhookDeliver  = "webhook:deliver"
	TypeDocumentConvert = "document:convert"
	TypeHTMLConvert     = "html:convert"
)
const (
	QueueImage    = convert.EngineImage
	QueueWebhook  = "webhook"
	QueueDocument = convert.EngineDocument
	QueueHTML     = convert.EngineHTML
)
```
Add `TypeAudioConvert = "audio:convert"` and `QueueAudio = convert.EngineAudio` to these same two const blocks. **Naming: `TypeAudioConvert`/`EnqueueAudioConvert`/`NewAudioConvertTask`/`QueueAudio`** — NOT `*Transcribe*` (research explicitly flags `.planning/research/ARCHITECTURE.md`'s `TypeAudioTranscribe` naming as superseded; every existing task type follows the uniform `{class}:convert` shape regardless of what the engine semantically does).

**`New{Class}ConvertTask` template** (`internal/queue/queue.go:91-101`, document version, exact shape to copy):
```go
func NewDocumentConvertTask(jobID uuid.UUID, maxRetry int, uniqueTTL time.Duration) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeDocumentConvert, b,
		asynq.Queue(QueueDocument),
		asynq.MaxRetry(maxRetry),
		asynq.Unique(uniqueTTL),
	), nil
}
```
Reuses the existing `ConvertPayload`/`ParseConvertPayload` — no new payload type needed for audio.

**Retry schedule + delay func template** (`internal/queue/queue.go:219-246`, document version):
```go
var documentRetrySchedule = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

func DocumentRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	idx := n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(documentRetrySchedule) {
		idx = len(documentRetrySchedule) - 1
	}
	return documentRetrySchedule[idx]
}
```
No jitter (document/html shape, not webhook's jittered shape). Add `audioRetrySchedule`/`AudioRetryDelay` in this exact shape — schedule values are the planner's/CONTEXT.md's discretion (research flags this openly; no binding value dictated).

**`RetryDelayFunc` dispatch switch** (`internal/queue/queue.go:281-294`) — add a `TypeAudioConvert` case or it silently falls through to `asynq.DefaultRetryDelayFunc` (not a compile error, easy to miss):
```go
func RetryDelayFunc(n int, e error, t *asynq.Task) time.Duration {
	switch t.Type() {
	case TypeImageConvert:
		return ImageRetryDelay(n, e, t)
	case TypeWebhookDeliver:
		return WebhookRetryDelay(n, e, t)
	case TypeDocumentConvert:
		return DocumentRetryDelay(n, e, t)
	case TypeHTMLConvert:
		return HTMLRetryDelay(n, e, t)
	default:
		return asynq.DefaultRetryDelayFunc(n, e, t)
	}
}
```

**BackoffSum + UniqueTTL template** (`internal/queue/queue.go:349-384`, document version, full doc comments included — copy the doc-comment DISCIPLINE, not just the code):
```go
func documentBackoffSum(maxRetry int) time.Duration {
	var sum time.Duration
	for i := 0; i < maxRetry; i++ {
		sum += DocumentRetryDelay(i, nil, nil)
	}
	return sum
}

// DocumentUniqueTTL derives the per-job asynq.Unique lock TTL for document
// conversion tasks from the actual retry budget (maxRetry, normally
// DOCUMENT_MAX_RETRY) and the per-attempt bound (engineTimeout, normally
// DOCUMENT_ENGINE_TIMEOUT) ...
// Worst-case formula: (maxRetry+1) * engineTimeout + documentBackoffSum(maxRetry) + margin.
// REUSES the shared uniqueTTLSafetyMargin const verbatim -- no
// document-specific margin constant.
func DocumentUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + documentBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}
```
`AudioUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration` follows this exact formula, reusing `uniqueTTLSafetyMargin` (`queue.go:299`) verbatim.

---

### `internal/queue/client.go` (additions)

**Analog:** `documentMaxRetry`/`documentUniqueTTL` fields + `EnqueueDocumentConvert` method (`internal/queue/client.go:34-45`, `:112-123`, full file verified read).

**`Client` struct fields** (`client.go:34-45`):
```go
// documentMaxRetry is the per-task MaxRetry budget for document
// conversion tasks (DOCUMENT_MAX_RETRY, default 3 -- bounded lower than
// image's 4, since each document attempt is expensive at up to
// DOCUMENT_ENGINE_TIMEOUT seconds and DOC-08 requires documents not be
// retried forever).
documentMaxRetry int
// documentUniqueTTL is the per-job asynq.Unique lock TTL for document
// conversion tasks, derived once at construction from documentMaxRetry
// and DOCUMENT_ENGINE_TIMEOUT via DocumentUniqueTTL ...
documentUniqueTTL time.Duration
```
Add `audioMaxRetry int` / `audioUniqueTTL time.Duration` in this shape.

**`NewClient()` construction block** (`client.go:69-82`):
```go
documentMaxRetry := envInt("DOCUMENT_MAX_RETRY", 3)
documentEngineTimeout := envDuration("DOCUMENT_ENGINE_TIMEOUT", 300*time.Second)
...
return &Client{
	...
	documentMaxRetry:  documentMaxRetry,
	documentUniqueTTL: DocumentUniqueTTL(documentMaxRetry, documentEngineTimeout),
	...
}, nil
```
Add `audioMaxRetry := envInt("AUDIO_MAX_RETRY", ...)` / `audioEngineTimeout := envDuration("AUDIO_ENGINE_TIMEOUT", ...)` and the two corresponding struct-literal fields.

**`Enqueue{Class}Convert` method template** (`client.go:112-123`):
```go
func (c *Client) EnqueueDocumentConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewDocumentConvertTask(jobID, c.documentMaxRetry, c.documentUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue document convert %s: %w", jobID, err)
	}
	return nil
}
```
`EnqueueAudioConvert` copies this exact shape.

---

### `internal/worker/worker.go` (additions)

**Analog for `HandleAudioConvert`:** `HandleDocumentConvert` (`internal/worker/worker.go:329-419`, full method verified read above) — structurally identical: parse payload → load job → strict opts re-parse (terminal on garbage) → MarkActive → `process()` → terminal/transient branch → metrics/webhook. Copy this shape verbatim, substituting:
- `convert.DocOptsFromMap(job.Opts)` → `convert.AudioOptsFromMap(job.Opts)`
- `isDocumentTerminal(err)` → `isAudioTerminal(err)` (see below — genuinely different body, do not reuse `timeoutIsTerminal`)
- `queue.QueueDocument` → `queue.QueueAudio` in both `metrics.RecordJobOutcome` calls

Exact strict-reparse block to mirror (`worker.go:356-378`):
```go
if _, err := convert.DocOptsFromMap(job.Opts); err != nil {
	ferr := h.repo.MarkFailed(ctx, jobID, "invalid_options", "stored conversion options are invalid", map[string]any{"opts_error": err.Error()})
	if ferr == nil && job.CallbackURL != "" {
		_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
	}
	return fmt.Errorf("%w: opts: %v", asynq.SkipRetry, err)
}
```

**Analog for `isAudioTerminal` — genuinely NEW shape, do NOT reuse `timeoutIsTerminal`.** `timeoutIsTerminal` (`worker.go:172-190`) and its two callers `isDocumentTerminal`/`isHTMLTerminal` (`worker.go:192-230`) are the pattern EVERY prior engine class used — but Key Decision 1 (binding, per RESEARCH.md) explicitly supersedes copying this shape for audio. The shared `isTerminal` base classifier (`worker.go:114-170`) is still the correct fallthrough target:
```go
// Source: internal/worker/worker.go:114-170 (verified read) -- the shared base classifier, called as isAudioTerminal's own fallthrough
func isTerminal(err error) bool {
	if err == nil {
		return false
	}
	var mErr minio.ErrorResponse
	if errors.As(err, &mErr) && minio.ToErrorResponse(mErr).Code == minio.NoSuchKey {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no converter for") {
		return true
	}
	// ... terminalVipsSignatures / terminalLibreOfficeSignatures / terminalChromiumSignatures / terminalVeraPDFSignatures loops ...
	return false
}
```
**Recommended `isAudioTerminal` body** (RESEARCH.md Pattern 4, already fully drafted and binding):
```go
func isAudioTerminal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, convert.ErrAudioDurationExceeded) {
		return true // pre-decode guard rejection -- no retry can fix an oversized declared duration
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "audio: ffmpeg:") {
		return true // ffmpeg-stage failure OR timeout: malformed/adversarial-input signal (Key Decision 1)
	}
	// whisper-cli-stage failure/timeout on already-duration-validated audio: transient by default.
	return isTerminal(err)
}
```
The `"audio: ffmpeg:"` / `"audio: whisper-cli:"` prefixes are already emitted by `internal/convert/whisper.go:171,181` (verified: `fmt.Errorf("audio: ffmpeg: %w", err)` / `fmt.Errorf("audio: whisper-cli: %w", err)`) specifically for this future classifier to key on.

**Duration-guard splice in `process()`** (`internal/worker/worker.go:596-657`, full method verified read above) — insert immediately after `downloadTo` (line 631) and before `conv.Convert` (line 635):
```go
// Current shape, worker.go:631-637:
if err := h.downloadTo(attemptCtx, inputs.ObjectKey, inPath); err != nil {
	return err
}

if err := conv.Convert(attemptCtx, inPath, outPath, job.Opts); err != nil {
	return fmt.Errorf("convert: %w", err)
}
```
```go
// Add, gated on job.Engine == convert.EngineAudio (job already in scope, no signature change needed):
if job.Engine == convert.EngineAudio {
	if err := convert.EnforceMaxDuration(attemptCtx, inPath, h.audioMaxDuration); err != nil {
		return err
	}
}
```
`Handler` struct (`worker.go:233-243`) needs a new `audioMaxDuration time.Duration` field, threaded through `NewHandler`'s existing 9-positional-param constructor (`worker.go:246`) as a 10th param — non-audio callers (`cmd/document-worker/main.go`, `cmd/api`, etc.) already pass `nil`/`0` for engine-scoped fields they don't use (see `cmd/document-worker/main.go:60-64` passing `nil, nil, ..., nil, 0` for webhook-only fields), so a 10th audio-only field follows the established pattern exactly.

---

### `internal/reconciler/reconciler.go` (additions)

**Analog:** `enqueuer` interface (`reconciler.go:58-64`) + `sweep()`'s engine-routing switch (`reconciler.go:282-302`), both verified read in full above.

**Interface extension:**
```go
// Source: internal/reconciler/reconciler.go:58-64 (verified read, this repo)
type enqueuer interface {
	EnqueueImageConvert(ctx context.Context, id uuid.UUID) error
	EnqueueWebhookDeliver(ctx context.Context, id uuid.UUID) error
	EnqueueDocumentConvert(ctx context.Context, id uuid.UUID) error
	EnqueueHTMLConvert(ctx context.Context, id uuid.UUID) error
}
```
Add `EnqueueAudioConvert(ctx context.Context, id uuid.UUID) error`.

**Switch case** (`reconciler.go:282-302`):
```go
var enqueueErr error
switch j.Engine {
case convert.EngineImage:
	enqueueErr = s.enq.EnqueueImageConvert(ctx, j.ID)
case convert.EngineDocument:
	enqueueErr = s.enq.EnqueueDocumentConvert(ctx, j.ID)
case convert.EngineHTML:
	enqueueErr = s.enq.EnqueueHTMLConvert(ctx, j.ID)
default:
	// Fail closed (T-10-03): ...
	metrics.RecordReconcilerAction("unroutable_engine")
	continue
}
```
Add:
```go
case convert.EngineAudio:
	enqueueErr = s.enq.EnqueueAudioConvert(ctx, j.ID)
```
alongside the existing three, before `default:`. `*queue.Client` satisfies the widened interface structurally the moment `EnqueueAudioConvert` is added to it (Go structural interfaces — no explicit `implements` declaration needed).

---

### `internal/api/api.go` (Enqueuer interface extension)

**Analog:** itself, `Enqueuer` interface (`internal/api/api.go:53-58`, verified read in full above):
```go
type Enqueuer interface {
	EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueDocumentConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueHTMLConvert(ctx context.Context, jobID uuid.UUID) error
}
```
Add `EnqueueAudioConvert(ctx context.Context, jobID uuid.UUID) error`. This is a SECOND, independently-declared interface from `internal/reconciler/reconciler.go`'s `enqueuer` — both need the method added (they do not share a type).

---

### `internal/api/handlers.go` (three additions)

**Analog for content-detection splice:** the existing OLE-CFB / HTML detection branches in the same chain (`handlers.go:184-283`, verified read in full above). Chain off `rest` (already correctly represents the full file from byte 0, `Sniff`'s own re-stitch), NOT `file` — `file`'s sequential-Read cursor has already advanced past `sniffLen` (12) bytes by the time this branch is reached, and `SniffAudio` re-stitches its own `rest`, not a `ReadAt`-based check like the ZIP/HTML/OLE-CFB branches:
```go
// Placement: alongside the existing OLE-CFB check (handlers.go:239-266),
// AFTER it (mp3PeekLen = 512 KiB is materially more expensive to buffer
// than the ReadAt-based checks, so ordering it last is correct and cheap
// for non-audio uploads -- never reached).
if detected == "" {
	if audioDetected, audioRest, aerr := convert.SniffAudio(rest); aerr == nil && audioDetected != "" {
		detected = audioDetected
		rest = audioRest
	}
}
```
No `source` gate needed (unlike the HTML branch at `handlers.go:227-238`, which is gated on `source=="html"`) — `SniffAudio` self-identifies from magic bytes, matching the ZIP/OLE-CFB branches' unconditional shape. `SniffAudio`'s signature (`internal/convert/audiosniff.go:99`): `func SniffAudio(r io.Reader) (detected string, rest io.Reader, err error)`.

**Analog for opts-parsing switch case:** the existing `EngineHTML` branch (`handlers.go:374-395`, verified read in full above):
```go
switch engine {
case convert.EngineHTML:
	htmlOpts, err := convert.ParseHTMLOpts([]byte(rawOpts))
	if err != nil {
		log.Printf("content validation rejected: client_id=%s filename=%q reason=invalid_opts", client.ID, filename)
		writeError(w, http.StatusUnprocessableEntity, "invalid opts")
		return
	}
	if err := convert.ValidateHTMLApplicability(engine, detected, target, htmlOpts); err != nil {
		log.Printf("content validation rejected: client_id=%s filename=%q reason=opts_not_applicable", client.ID, filename)
		writeError(w, http.StatusUnprocessableEntity, "opts not applicable to this conversion")
		return
	}
	normalizedRaw, err = json.Marshal(htmlOpts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to normalize opts")
		return
	}
default:
	docOpts, err := convert.ParseDocOpts([]byte(rawOpts))
	...
}
```
**Fix — add a third case BEFORE `default:`** (currently `EngineAudio` would silently fall into `default:` and be strictly parsed as `DocOpts`, rejecting every audio opts request with a spurious 422 — RESEARCH.md Pattern 3, a confirmed integration bug if skipped):
```go
case convert.EngineAudio:
	audioOpts, err := convert.ParseAudioOpts([]byte(rawOpts))
	if err != nil {
		log.Printf("content validation rejected: client_id=%s filename=%q reason=invalid_opts", client.ID, filename)
		writeError(w, http.StatusUnprocessableEntity, "invalid opts")
		return
	}
	if err := convert.ValidateAudioApplicability(engine, detected, target, audioOpts); err != nil {
		log.Printf("content validation rejected: client_id=%s filename=%q reason=opts_not_applicable", client.ID, filename)
		writeError(w, http.StatusUnprocessableEntity, "opts not applicable to this conversion")
		return
	}
	normalizedRaw, err = json.Marshal(audioOpts)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to normalize opts")
		return
	}
```
`ParseAudioOpts`/`ValidateAudioApplicability` signatures (`internal/convert/audioopts.go:58,97`, verified read): `func ParseAudioOpts(raw []byte) (AudioOpts, error)`, `func ValidateAudioApplicability(engine, source, target string, o AudioOpts) error`.

**Analog for enqueue switch case:** existing switch (`handlers.go:487-499`, verified read in full above):
```go
var enqueueErr error
switch engine {
case convert.EngineImage:
	enqueueErr = s.queue.EnqueueImageConvert(ctx, createdID)
case convert.EngineDocument:
	enqueueErr = s.queue.EnqueueDocumentConvert(ctx, createdID)
case convert.EngineHTML:
	enqueueErr = s.queue.EnqueueHTMLConvert(ctx, createdID)
default:
	// Fail closed: an engine class with no known queue must never be
	// silently dropped (T-11-02). ...
```
Add:
```go
case convert.EngineAudio:
	enqueueErr = s.queue.EnqueueAudioConvert(ctx, createdID)
```
alongside the existing three, before `default:`.

---

### `cmd/audio-worker/main.go` (new)

**Analog:** `cmd/document-worker/main.go` (166 lines, full file verified read above) — copy verbatim structure, swapping every `Document`/`document` identifier for `Audio`/`audio`:

```go
// Source: cmd/document-worker/main.go (verified read, this repo, full file) -- the exact template
package main

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
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/storage"
	"github.com/apaderin/octoconv/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()

	store, err := storage.New(ctx)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	redisOpt, err := queue.RedisOpt()
	if err != nil {
		log.Fatalf("redis: %v", err)
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
		convert.Default,
		envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second), // [ASSUMED] placeholder, Phase 32 re-derives from RTF measurement
		nil, // webhookRepo -- webhook-only; HandleAudioConvert never reads it
		nil, // deliverer -- webhook-only; HandleAudioConvert never reads it
		qc,
		nil, // signingSecret -- webhook-only; HandleAudioConvert never reads it
		0,   // presignTTL -- webhook-only; HandleAudioConvert never reads it
		// + audioMaxDuration (new 10th param), from envDuration("AUDIO_MAX_DURATION_SECONDS", ...)
	)

	// D-04/D-05 precedent: the stale-job sweep loop runs solely in
	// cmd/webhook-worker (under a Postgres advisory lock) -- no sweeper of
	// any kind is constructed or run here.

	convert.SetAudioModelPath(os.Getenv("AUDIO_MODEL_PATH")) // env-only-in-main, mirrors SetVeraPDFTimeout; MUST run before srv.Start(mux)

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeAudioConvert, h.HandleAudioConvert)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("AUDIO_WORKER_CONCURRENCY", 2),
		Queues:         map[string]int{queue.QueueAudio: 1},
		RetryDelayFunc: queue.RetryDelayFunc,
		ShutdownTimeout: envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second) + 10*time.Second,
	})

	log.Printf("🐙 audio-worker starting (queue=%s)", queue.QueueAudio)
	if err := srv.Start(mux); err != nil {
		log.Fatalf("audio-worker: %v", err)
	}

	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = "127.0.0.1:9090"
	}
	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           promhttp.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("📊 metrics listening on %s", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("metrics listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("🛑 shutting down audio-worker...")
	srv.Shutdown()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics graceful shutdown failed: %v", err)
	}
	log.Println("bye 👋")
}

// envInt/envDuration/firstField: duplicated verbatim from cmd/document-worker/main.go's own private helpers (per-package convention, no shared internal/config package exists).
```
**Note (research-flagged, NOT this phase's job):** `NewQueueDepthCollector` in `cmd/api/main.go` is explicitly `AUD-08`'s job (Phase 33) — do not add `QueueAudio` to it this phase (scope creep against the phase boundary).

---

### `.env.example` (additions)

**Analog:** the `# Document worker` and `# Chromium (html) worker` blocks (verified read in full above):
```
# Document worker
DOCUMENT_WORKER_CONCURRENCY=2   # lower than WORKER_CONCURRENCY's 4 -- soffice.bin's heavier per-conversion memory footprint within the same cpus:2.0/memory:1g ceiling (D-02/D-03)
DOCUMENT_ENGINE_TIMEOUT=300s   # Phase 9 D-01
DOCUMENT_MAX_RETRY=3   # bounded retry budget for document conversion, mirrors IMAGE_MAX_RETRY; also feeds the derived per-job uniqueness-lock TTL together with DOCUMENT_ENGINE_TIMEOUT
VERAPDF_TIMEOUT=60s   # own timeout for the bundled veraPDF CLI validation call, separate from DOCUMENT_ENGINE_TIMEOUT (Phase 23 D-04); well inside the 300s document engine budget
```
Add an analogous `# Audio worker` block with `AUDIO_ENGINE_TIMEOUT` (placeholder, e.g. `600s`, [ASSUMED] per RESEARCH.md A1 — explicitly commented as a Phase-32-RTF-measurement placeholder, do NOT copy `DOCUMENT_ENGINE_TIMEOUT`'s value verbatim), `AUDIO_MAX_RETRY`, `AUDIO_WORKER_CONCURRENCY`, `AUDIO_MAX_DURATION_SECONDS`, `AUDIO_MODEL_PATH`. Also note (comment only, per Pitfall 2) near `RECONCILER_ACTIVE_STALE_AFTER=5m` that raising it for audio affects image/document/html staleness detection globally too.

---

## Shared Patterns

### Postgres-first double write + engine-routing switch (repeated 3x already, apply identically for audio)
**Source:** `internal/api/handlers.go:457-499` (job insert, then enqueue switch)
**Apply to:** `internal/api/handlers.go`'s enqueue switch (new `EngineAudio` case)
```go
createdID, err := s.repo.Create(ctx, jobs.CreateParams{ ID: jobID, ... Engine: engine, ... })
if err != nil {
	writeError(w, http.StatusInternalServerError, "failed to create job")
	return
}
var enqueueErr error
switch engine {
case convert.EngineImage:
	enqueueErr = s.queue.EnqueueImageConvert(ctx, createdID)
// ... add case convert.EngineAudio: enqueueErr = s.queue.EnqueueAudioConvert(ctx, createdID)
}
```

### Guarded MarkActive + strict opts re-parse before processing
**Source:** `internal/worker/worker.go:356-383` (`HandleDocumentConvert`'s opts-reparse + MarkActive block)
**Apply to:** `HandleAudioConvert` (new)
```go
if _, err := convert.DocOptsFromMap(job.Opts); err != nil { /* MarkFailed("invalid_options", ...) + SkipRetry */ }
if err := h.repo.MarkActive(ctx, jobID); err != nil { /* SkipRetry, let asynq drop it */ }
```
Swap `convert.DocOptsFromMap` → `convert.AudioOptsFromMap`.

### asynq.Unique-derived TTL (worst-case-retry-lifetime formula, repeated 3x)
**Source:** `internal/queue/queue.go:345-347` (`ImageUniqueTTL`), `:382-384` (`DocumentUniqueTTL`), `:410-412` (`HTMLUniqueTTL`) — identical formula each time
**Apply to:** new `AudioUniqueTTL`
```go
func XUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + xBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}
```
`uniqueTTLSafetyMargin` (`queue.go:299`, `= 2 * time.Minute`) is REUSED verbatim across every class — never a per-class margin constant.

### Fail-closed `default:` branch on engine-routing switches
**Source:** `internal/reconciler/reconciler.go:290-301` (`unroutable_engine` metric + `continue`, no enqueue/no RequeueStale)
**Apply to:** every engine-routing switch (reconciler sweep, API enqueue switch) — a new engine class must add its own `case`, never fall through to a guessed default.

## No Analog Found

None. Every file this phase touches has a direct 3x-repeated in-repo analog (image → document → html engine-class additions each touched the identical file set in the identical shape) — the audio engine class is the 4th repetition of an already-proven pattern, not a novel architecture.

**One deliberate NON-analog, flagged explicitly (not a gap, a documented divergence):** `isAudioTerminal` must NOT copy `isDocumentTerminal`/`isHTMLTerminal`'s `timeoutIsTerminal(err)` one-liner shape (Pattern 4 above) — this is the single place in this phase where "copy the 3x-proven pattern" is the WRONG move, per the binding Key Decision 1 in `.planning/STATE.md`. Flag to the planner: this classifier needs its own hand-written body keyed on the `"audio: ffmpeg:"`/`"audio: whisper-cli:"` stage-prefix convention, not a copy-paste of the existing two-line wrapper.

## Metadata

**Analog search scope:** `internal/convert/`, `internal/queue/`, `internal/worker/`, `internal/reconciler/`, `internal/api/`, `internal/db/migrations/`, `cmd/document-worker/`, `cmd/api/`, `internal/queue/queue_test.go`, `internal/reconciler/reconciler_test.go` — all directories the document/html engine-class additions previously touched (verified exhaustively by RESEARCH.md's own direct file:line reads, cross-checked here by re-reading each file in full or via targeted grep+offset reads).
**Files scanned:** 15 (12 target files' analogs + 3 supporting reads: `internal/convert/convert.go` for `EngineAudio`/`Classes()`, `internal/convert/audiosniff.go` for `SniffAudio`'s signature, `internal/convert/audioduration.go`+`audioopts.go` for `EnforceMaxDuration`/`ParseAudioOpts`/`ValidateAudioApplicability` signatures)
**Pattern extraction date:** 2026-07-18
