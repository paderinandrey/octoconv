# Phase 35: Queue, Worker & Routing Integration - Pattern Map

**Mapped:** 2026-07-21
**Files analyzed:** 15 (11 modified existing files, 1 new binary, 3 test files with required additions)
**Analogs found:** 15 / 15 — this phase is the fifth engine class riding an established pattern; every seam has an exact, working analog in the AUDIO engine class (Phases 30-33). No "No Analog Found" section is needed.

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|---|---|---|---|---|
| `cmd/av-worker/main.go` (NEW) | worker entry point | event-driven (asynq consumer) | `cmd/audio-worker/main.go` | exact |
| `internal/queue/queue.go` (consts, `NewAVConvertTask`, `avRetrySchedule`, `AVRetryDelay`, `RetryDelayFunc` case, `avBackoffSum`, `AVUniqueTTL`) | queue/task definitions | pub-sub / event-driven | audio equivalents in same file (`TypeAudioConvert`, `NewAudioConvertTask`, `audioRetrySchedule`, `AudioRetryDelay`, `audioBackoffSum`, `AudioUniqueTTL`) | exact |
| `internal/queue/client.go` (fields, `EnqueueAVConvert`) | producer / service | request-response (enqueue) | audio fields + `EnqueueAudioConvert` in same file | exact |
| `internal/queue/queue_test.go` (`TestAVUniqueTTL`) | test | pure-function unit test | `TestAudioUniqueTTL` (`queue_test.go:406-436`) | exact |
| `internal/convert/av.go` (3 new sentinel errors, D-01 rewrap) | model / domain error types | transform (error classification) | `ErrAVOutputMissingOrEmpty`/`ErrAVTimecodeOutOfRange` already in same file (`av.go:260-276`); audio has no equivalent per-stage sentinel split — this is genuinely new shape within an existing file | partial (new within analog file) |
| `internal/convert/whisper.go` (`audioSourceFormats` grows, `minFfmpegBudget` becomes source-aware, `-map 0:a:0`) | converter / transform | file-I/O (ffmpeg subprocess) | same file, existing `audioSourceFormats`/`minFfmpegBudget`/`ffmpegNormalizeArgs` | exact (self-analog) |
| `internal/convert/converters.go` (register `AVConverter{}`) | config / wiring | — | `Default.Register(AudioConverter{})` (`converters.go:9`) | exact |
| `internal/worker/worker.go` (`isAVTerminal`, `HandleAVConvert`) | controller / task handler | event-driven (asynq handler) | `isAudioTerminal` (`worker.go:255-337`, CONTRAST not copy) + `HandleDocumentConvert`/`HandleHTMLConvert` (`worker.go:444-556`, shape to copy — no guard splice) | role-match (deliberately divergent classifier) / exact (handler shape) |
| `internal/api/api.go` (`Enqueuer` interface +`EnqueueAVConvert`) | interface / dependency seam | request-response | `Enqueuer.EnqueueAudioConvert` (`api.go:54-59`) | exact |
| `internal/api/handlers.go` (`SniffVideo` wiring, opts-dispatch case, enqueue switch case, per-engine upload ceiling) | controller | request-response | `convert.EngineAudio` cases + `SniffAudio` wiring in same file | exact |
| `internal/reconciler/reconciler.go` (`enqueuer` interface +method, routing case) | controller / sweep | batch (periodic sweep) | `convert.EngineAudio` case (`reconciler.go:284-303`) + interface method (`reconciler.go:64`) | exact |
| `internal/reconciler/reconciler_test.go` (`fakeEnqueuer.EnqueueAVConvert`, `TestSweepRoutesAVJobsToAVQueue`) | test fake + test | — | `fakeEnqueuer.EnqueueAudioConvert` + `TestSweepRoutesAudioJobsToAudioQueue` (`reconciler_test.go:132-136`, `:276-305`) | exact |
| `cmd/api/main.go` (queue-depth collector arg) | config / wiring | — | existing variadic call listing `queue.QueueAudio` (`main.go:91-92`) | exact |
| D-06 completeness test (new, location Claude's discretion — suggest `internal/api` or `internal/convert`) | test | — | `TestVideoBrandDisjointness` (`avsniff_test.go:118-140`) for table-driven-over-map shape | role-match |
| AV/Audio pair-disjointness test (Pitfall 7) | test | — | `TestVideoBrandDisjointness` (`avsniff_test.go:118-140`) | role-match |
| `internal/api/api.go` `Config`/`Server` (D-07 `MaxEngineBytes` field) | config | — | `maxImagePixels`/`maxDocumentUncompressedBytes` fields + defaulting in `NewServer` (`api.go:85-95`, `:136-141`) | exact |

## Pattern Assignments

### `cmd/av-worker/main.go` (worker entry point, event-driven)

**Analog:** `cmd/audio-worker/main.go` (full file, 254 lines — read in full, it is the template)

**Imports pattern** (lines 1-27):
```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
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
```
For av-worker, drop the audio-only threading/model-path setter block (`convert.SetAudioModelPath`/`convert.SetAudioThreads`/`resolveAudioThreads`) — AV has no equivalent env-only-in-main setter contract yet (avThreadCount() reads CgroupCPULimit()/NumCPU() directly, no setter). Keep everything else.

**Core wiring pattern** (lines 29-68, adapted):
```go
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
		envDuration("AV_ENGINE_TIMEOUT", 600*time.Second), // [ASSUMED] provisional, Phase 36 re-derives from RTF measurement
		nil, // webhookRepo — webhook-only; HandleAVConvert never reads it
		nil, // deliverer — webhook-only; HandleAVConvert never reads it
		qc,
		nil, // signingSecret — webhook-only; HandleAVConvert never reads it
		0,   // presignTTL — webhook-only; HandleAVConvert never reads it
		0,   // audioMaxDuration — 0 for every non-audio worker cmd (worker.go:350-355); AV's guard is self-contained inside Convert(), not spliced via this param
	)
```

**CRITICAL — the happens-before boundary and ShutdownTimeout override** (lines 100-114, copy verbatim with AV_* names):
```go
	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeAVConvert, h.HandleAVConvert)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("AV_WORKER_CONCURRENCY", 2),
		Queues:         map[string]int{queue.QueueAV: 1},
		RetryDelayFunc: queue.RetryDelayFunc,
		// asynq defaults ShutdownTimeout to 8s, silently capping the
		// graceful window regardless of the pod's
		// terminationGracePeriodSeconds. Aligning it to
		// AV_ENGINE_TIMEOUT+margin lets a genuinely long in-flight ffmpeg
		// transcode survive SIGTERM instead of being aborted+requeued.
		ShutdownTimeout: envDuration("AV_ENGINE_TIMEOUT", 600*time.Second) + 10*time.Second,
	})
```
Note the env read is duplicated (once for `NewHandler`'s `engineTimeout` param, once for `ShutdownTimeout`) — this exact duplication exists in `cmd/audio-worker/main.go:61` and `:113` and must be mirrored, not refactored into a shared variable, to keep the file a drop-in template match.

**Helper functions** (`envInt`, `envDuration`, `firstField` — lines 180-237): copy verbatim, unchanged. `envDurationSeconds`/`stripInlineComment`/`resolveAudioThreads` are audio-specific (model path, thread sizing) and have no AV equivalent yet — omit them unless a later plan introduces an AV thread/model tunable.

**Metrics + shutdown boilerplate** (lines 116-159): copy verbatim, replacing `audio-worker`/`audio` log strings with `av-worker`/`av`.

---

### `internal/queue/queue.go` (task type, queue name, task builder, retry schedule/delay, backoff sum, unique TTL)

**Analog:** same file's audio block, four non-contiguous excerpts.

**Task type + queue name constants** (lines 19-39):
```go
const (
	TypeImageConvert    = "image:convert"
	TypeWebhookDeliver  = "webhook:deliver"
	TypeDocumentConvert = "document:convert"
	TypeHTMLConvert     = "html:convert"
	TypeAudioConvert    = "audio:convert"
	TypeAVConvert       = "av:convert" // NEW
)

const (
	QueueImage    = convert.EngineImage
	QueueWebhook  = "webhook"
	QueueDocument = convert.EngineDocument
	QueueHTML     = convert.EngineHTML
	QueueAudio    = convert.EngineAudio
	QueueAV       = convert.EngineAV // NEW — ties queue name to convert.EngineAV, DEBT-02 discipline
)
```

**Task builder, mirroring `NewAudioConvertTask` exactly** (lines 126-144):
```go
// NewAVConvertTask builds an asynq task for a video conversion job, routed
// to the av queue, bounded to maxRetry (AV_MAX_RETRY), and carrying a
// per-job asynq.Unique lock (uniqueTTL, see AVUniqueTTL) so a second enqueue
// for the same jobID while the first task/lock is still live collides on
// the same uniqueness key and returns asynq.ErrDuplicateTask instead of
// creating a second concurrent task — mirrors NewAudioConvertTask exactly;
// reuses ConvertPayload/ParseConvertPayload (no new payload type needed).
func NewAVConvertTask(jobID uuid.UUID, maxRetry int, uniqueTTL time.Duration) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeAVConvert, b,
		asynq.Queue(QueueAV),
		asynq.MaxRetry(maxRetry),
		asynq.Unique(uniqueTTL),
	), nil
}
```

**Retry schedule + delay func, mirroring `audioRetrySchedule`/`AudioRetryDelay` — D-03 LOCKS the values at 30s/2m, not audio's 5s/15s/30s** (lines 297-321 shape):
```go
// avRetrySchedule is the backoff schedule for av conversion retries: 30s,
// 2m — LOCKED by D-03 (CONTEXT.md): fewer attempts, longer pauses than
// audio's 5s/15s/30s. The long first backoff lets load drain rather than
// hammering a busy worker 5 seconds later.
var avRetrySchedule = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
}

// AVRetryDelay is an asynq RetryDelayFunc for the av queue. Mirrors
// AudioRetryDelay's/DocumentRetryDelay's clamp-index-to-schedule-length
// shape exactly — NOT WebhookRetryDelay's jittered shape.
func AVRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	idx := n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(avRetrySchedule) {
		idx = len(avRetrySchedule) - 1
	}
	return avRetrySchedule[idx]
}
```

**`RetryDelayFunc` dispatch case — Pitfall 1: this case MUST land in the SAME commit as `TypeAVConvert`** (lines 330-345):
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
	case TypeAudioConvert:
		return AudioRetryDelay(n, e, t)
	case TypeAVConvert: // NEW
		return AVRetryDelay(n, e, t)
	default:
		return asynq.DefaultRetryDelayFunc(n, e, t)
	}
}
```

**Backoff sum + UniqueTTL, mirroring `audioBackoffSum`/`AudioUniqueTTL` exactly, reusing `uniqueTTLSafetyMargin`** (lines 465-505 shape):
```go
// avBackoffSum sums AVRetryDelay(i) for i in [0, maxRetry) — mirrors
// audioBackoffSum; safe to call AVRetryDelay directly (no jitter).
func avBackoffSum(maxRetry int) time.Duration {
	var sum time.Duration
	for i := 0; i < maxRetry; i++ {
		sum += AVRetryDelay(i, nil, nil)
	}
	return sum
}

// AVUniqueTTL derives the per-job asynq.Unique lock TTL for av conversion
// tasks from AV_MAX_RETRY and AV_ENGINE_TIMEOUT, mirroring
// AudioUniqueTTL's derivation exactly. DERIVED FRESH per the sequencing
// note (D-03/CONTEXT.md): AV_ENGINE_TIMEOUT is provisional in this phase
// and Phase 36 recomputes it, but the formula itself is unaffected — only
// the input changes. Worst-case formula: (maxRetry+1) * engineTimeout +
// avBackoffSum(maxRetry) + margin. REUSES the shared uniqueTTLSafetyMargin
// const verbatim (queue.go:350) — no AV-specific margin constant.
func AVUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + avBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}
```

---

### `internal/queue/client.go` (Client fields, env reads, `EnqueueAVConvert`)

**Analog:** same file's audio fields/method (full file read, 203 lines).

**Struct fields, mirroring `audioMaxRetry`/`audioUniqueTTL`** (lines 55-65):
```go
	// avMaxRetry is the per-task MaxRetry budget for av conversion tasks
	// (AV_MAX_RETRY, LOCKED default 2 per D-03 — fewer attempts than
	// audio's 3, since each attempt costs more).
	avMaxRetry int
	// avUniqueTTL is the per-job asynq.Unique lock TTL for av conversion
	// tasks, derived once at construction from avMaxRetry and
	// AV_ENGINE_TIMEOUT via AVUniqueTTL. Derived fresh, never reused from
	// another engine class's TTL.
	avUniqueTTL time.Duration
```

**`NewClient` construction, mirroring audio's env-read + struct-literal lines** (lines 76-101):
```go
	avMaxRetry := envInt("AV_MAX_RETRY", 2) // D-03 LOCKED default
	avEngineTimeout := envDuration("AV_ENGINE_TIMEOUT", 600*time.Second) // [ASSUMED] provisional
	return &Client{
		c:                 asynq.NewClient(opt),
		// ... existing fields unchanged ...
		avMaxRetry:        avMaxRetry,
		avUniqueTTL:       AVUniqueTTL(avMaxRetry, avEngineTimeout),
	}, nil
```

**`EnqueueAVConvert`, mirroring `EnqueueAudioConvert` exactly** (lines 156-166):
```go
// EnqueueAVConvert puts a video conversion job onto the av queue.
func (c *Client) EnqueueAVConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewAVConvertTask(jobID, c.avMaxRetry, c.avUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue av convert %s: %w", jobID, err)
	}
	return nil
}
```

---

### `internal/queue/queue_test.go` — `TestAVUniqueTTL`

**Analog:** `TestAudioUniqueTTL` (`queue_test.go:406-436`) — mirror the EXACT three-assertion shape.

```go
func TestAVUniqueTTL(t *testing.T) {
	maxRetry := 2                    // D-03 LOCKED
	engineTimeout := 600 * time.Second // matches the provisional AV_ENGINE_TIMEOUT default
	backoffSum := 30*time.Second + 2*time.Minute // i=0..1, per avRetrySchedule

	want := time.Duration(maxRetry+1)*engineTimeout + backoffSum + uniqueTTLSafetyMargin
	got := AVUniqueTTL(maxRetry, engineTimeout)
	if got != want {
		t.Errorf("AVUniqueTTL(%d, %v) = %v, want %v", maxRetry, engineTimeout, got, want)
	}
	// Assertion 1: exact worked-example value (compute by hand and pin it,
	// mirroring TestAudioUniqueTTL's "want != 2570*time.Second" self-check).

	// Assertion 2: monotonicity in BOTH arguments.
	if AVUniqueTTL(maxRetry+1, engineTimeout) <= AVUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("AVUniqueTTL must grow monotonically with maxRetry")
	}
	if AVUniqueTTL(maxRetry, engineTimeout+time.Second) <= AVUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("AVUniqueTTL must grow monotonically with engineTimeout")
	}

	// Assertion 3: strictly exceeds the zero-margin worst-case retry
	// lifetime — proves uniqueTTLSafetyMargin is load-bearing, not
	// accidentally zero.
	worstCaseNoMargin := time.Duration(maxRetry+1)*engineTimeout + avBackoffSum(maxRetry)
	if AVUniqueTTL(maxRetry, engineTimeout) <= worstCaseNoMargin {
		t.Errorf("AVUniqueTTL(%d, %v) = %v must strictly exceed the zero-margin worst-case lifetime %v",
			maxRetry, engineTimeout, AVUniqueTTL(maxRetry, engineTimeout), worstCaseNoMargin)
	}
}
```

---

### `internal/convert/av.go` — D-01 sentinel refactor (3 new sentinels, 3 call-site rewraps)

**Analog:** the file's own existing `ErrAVOutputMissingOrEmpty`/`ErrAVTimecodeOutOfRange` sentinel pattern (`av.go:260-276`) — same file, same declaration idiom (`var Err... = errors.New(...)`), applied to the three ffmpeg call sites that today all wrap identically.

**Current identical-wrap shape at all three call sites (MUST change per D-01):**
```go
// convertTranscode (av.go:480-482):
if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
	return fmt.Errorf("av: ffmpeg: %w", err)
}

// convertAudioExtract (av.go:527-529):
if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
	return fmt.Errorf("av: ffmpeg: %w", err)
}

// convertThumbnail (av.go:565-567):
if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
	return fmt.Errorf("av: ffmpeg: %w", err)
}
```

**Recommended new sentinels, following the existing `var Err<X> = errors.New("av: ...")` naming idiom in the same file** (exact names are Claude's Discretion per CONTEXT.md; this shape is consistent with `ErrAVOutputMissingOrEmpty`):
```go
var ErrAVTranscodeFailed    = errors.New("av: transcode failed")
var ErrAVAudioExtractFailed = errors.New("av: audio-extract failed")
var ErrAVThumbnailFailed    = errors.New("av: thumbnail failed")
```

**Rewrapped call sites (Go 1.20+ multi-%w, each stage gets its OWN sentinel so `errors.Is` can distinguish them):**
```go
// convertTranscode:
if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
	return fmt.Errorf("%w: %w", ErrAVTranscodeFailed, err)
}

// convertAudioExtract:
if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
	return fmt.Errorf("%w: %w", ErrAVAudioExtractFailed, err)
}

// convertThumbnail:
if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
	return fmt.Errorf("%w: %w", ErrAVThumbnailFailed, err)
}
```

**Verified (research):** grep across `av_test.go` found zero assertions pinning the literal `"av: ffmpeg:"` string — this refactor needs no existing-test updates, only new tests asserting the new sentinels (contrary to CONTEXT.md's original worst-case assumption).

---

### `internal/worker/worker.go` — `isAVTerminal` (CONTRAST case, not a copy target)

**Analog to learn structure from, NOT to copy the rule of:** `isAudioTerminal` (`worker.go:255-337`).

**What to keep from the analog's shape:**
- `if err == nil { return false }` guard first
- Deterministic-guard-rejection sentinels checked first via `errors.Is` (always terminal, regardless of stage)
- A `strings.ToLower(err.Error())`-based fallback ONLY where no typed sentinel exists yet — for AV this should be unnecessary once D-01 lands, since all three ffmpeg stages get typed sentinels
- Falls through to the shared `isTerminal(err)` at the end for S3/Postgres blips, no-converter, etc.

**What to explicitly NOT carry over — the one-line rule that makes `isAudioTerminal` WRONG for AV:**
```go
// isAudioTerminal (worker.go:302-306) — DO NOT PORT THIS RULE TO AV:
msg := strings.ToLower(err.Error())
if strings.Contains(msg, "audio: ffmpeg:") {
	// Key Decision 1: ffmpeg-stage failure OR timeout is a
	// malformed/adversarial input signal — terminal, no retry.
	return true
}
```
This treats ANY ffmpeg-stage failure (including a timeout) as terminal because ffmpeg is audio's CHEAP normalize stage. Applied to AV's transcode stage — the EXPENSIVE operation D-02 requires stays TRANSIENT on timeout — this rule would be actively wrong.

**Correct AV shape (from RESEARCH.md Pattern 3, verified against the D-01/D-02 decisions):**
```go
// isAVTerminal is the av engine's engine-scoped terminal classifier —
// D-02 (CONTEXT.md, BINDING): a stage-aware split. Deliberately NOT a copy
// of isAudioTerminal's blanket "any ffmpeg-stage failure/timeout is
// terminal" rule — that rule is correct for audio (ffmpeg is audio's cheap
// normalize stage) but would be WRONG for av, where transcode is the
// expensive stage and a timeout there may simply mean the retry budget ran
// out under load, not that the input is malformed.
func isAVTerminal(err error) bool {
	if err == nil {
		return false
	}
	// Deterministic guard/output-validation rejections: always terminal,
	// regardless of which stage produced them.
	if errors.Is(err, convert.ErrAVOutputMissingOrEmpty) ||
		errors.Is(err, convert.ErrAVTimecodeOutOfRange) ||
		errors.Is(err, convert.ErrAVResolutionExceeded) ||
		errors.Is(err, convert.ErrAudioDurationExceeded) { // REUSED sentinel — AV's duration guard calls the SAME enforceMaxDurationOf audio uses (av.go:395)
		return true
	}
	isTimeout := errors.Is(err, context.DeadlineExceeded)
	switch {
	case errors.Is(err, convert.ErrAVTranscodeFailed):
		// D-02: transcode is the expensive stage -- timeout stays TRANSIENT.
		return !isTimeout
	case errors.Is(err, convert.ErrAVAudioExtractFailed), errors.Is(err, convert.ErrAVThumbnailFailed):
		// D-02: cheap stages -- ANY failure (timeout or not) is TERMINAL.
		return true
	}
	return isTerminal(err) // no-converter / minio.NoSuchKey / shared fallthrough
}
```

---

### `internal/worker/worker.go` — `HandleAVConvert` (handler shape)

**Analog:** `HandleDocumentConvert`/`HandleHTMLConvert` (`worker.go:444-556`) — the SIMPLE shape (no guard splice), NOT `HandleAudioConvert`'s shape (which calls `enforceAudioGuardBeforeConvert` because audio's converter doesn't own its own guard). Per RESEARCH.md's verified correction: `AVConverter.Convert` already self-contains its duration/resolution guard (`av.go:388-400`), so `HandleAVConvert` calls the shared `process()` untouched, exactly like `HandleDocumentConvert` does.

**Shape to copy** (mirrors `HandleDocumentConvert`, `worker.go:458-534`, substituting `AV` for `Document` throughout):
```go
func (h *Handler) HandleAVConvert(ctx context.Context, t *asynq.Task) error {
	payload, err := queue.ParseConvertPayload(t.Payload())
	if err != nil {
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	jobID := payload.JobID

	job, err := h.repo.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job %s: %w", jobID, err)
	}

	// Strict re-parse of the persisted opts, mirrors DocOptsFromMap/
	// AudioOptsFromMap's terminal-on-garbage-opts discipline.
	if _, err := convert.AVOptsFromMap(job.Opts); err != nil {
		ferr := h.repo.MarkFailed(ctx, jobID, "invalid_options", "stored conversion options are invalid", map[string]any{"opts_error": err.Error()})
		if ferr == nil && job.CallbackURL != "" {
			_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
		}
		return fmt.Errorf("%w: opts: %v", asynq.SkipRetry, err)
	}

	if err := h.repo.MarkActive(ctx, jobID); err != nil {
		return fmt.Errorf("%w: mark active: %v", asynq.SkipRetry, err)
	}

	start := time.Now()
	if err := h.process(ctx, job); err != nil {
		if isAVTerminal(err) {
			// D-09: an ErrAVTimecodeOutOfRange rejection is a distinguishable
			// client-fault error_code (mirrors HandleAudioConvert's
			// "duration_exceeded" special-case, worker.go:699-704) — it must
			// NOT reuse the generic "engine_error" code, so the client can
			// tell "you asked for a timecode past the end of the video" apart
			// from a generic corrupted-input/engine failure.
			var ferr error
			switch {
			case errors.Is(err, convert.ErrAVTimecodeOutOfRange):
				ferr = h.repo.MarkFailed(ctx, jobID, "timecode_out_of_range", "requested thumbnail timecode exceeds source duration", map[string]any{"engine_stderr": err.Error()})
			case errors.Is(err, convert.ErrAudioDurationExceeded):
				ferr = h.repo.MarkFailed(ctx, jobID, "duration_exceeded", "declared video duration exceeds the configured maximum", map[string]any{"engine_stderr": err.Error()})
			case errors.Is(err, convert.ErrAVResolutionExceeded):
				ferr = h.repo.MarkFailed(ctx, jobID, "resolution_exceeded", "declared video resolution exceeds the configured maximum", map[string]any{"engine_stderr": err.Error()})
			default:
				ferr = h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", map[string]any{"engine_stderr": err.Error()})
			}
			metrics.RecordJobOutcome(queue.QueueAV, jobs.StatusFailed, time.Since(start))
			if ferr == nil && job.CallbackURL != "" {
				_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
			}
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		// Transient: do NOT mark failed — asynq's own retry/backoff
		// (AVRetryDelay/AV_MAX_RETRY) applies. Not recorded in the
		// job-outcome metric (one asynq retry must not double-count).
		return err
	}
	metrics.RecordJobOutcome(queue.QueueAV, jobs.StatusDone, time.Since(start))
	if job.CallbackURL != "" {
		_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
	}
	return nil
}
```
Note: `convert.AVOptsFromMap` must exist (it does — `avopts.go:126-134`, verified present from Phase 34).

---

### `internal/api/api.go` — `Enqueuer` interface

**Analog:** same file (`api.go:54-59`).

```go
// Enqueuer dispatches conversion work to the appropriate engine-class queue.
type Enqueuer interface {
	EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueDocumentConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueHTMLConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueAudioConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueAVConvert(ctx context.Context, jobID uuid.UUID) error // NEW
}
```
Pitfall 3 (research): this is a Go interface — a missing implementation on `*queue.Client` fails `go build ./...` immediately. No dedicated verification task needed here.

**D-07 two-tier ceiling — new `Config`/`Server` field, mirrors `maxImagePixels`/`maxDocumentUncompressedBytes`** (`api.go:85-95`, `:108-113`, `:136-141`):
```go
// Server field (mirrors maxImagePixels/maxDocumentUncompressedBytes shape):
	// maxEngineBytes is the D-07 per-engine post-detection upload ceiling
	// (engine -> byte limit). Checked AFTER format detection/EngineFor,
	// unlike maxUploadByte which is enforced pre-parse by
	// http.MaxBytesReader. Never nil after NewServer.
	maxEngineBytes map[string]int64

// Config field:
	MaxEngineBytes map[string]int64

// NewServer defaulting (mirrors the cfg.MaxImagePixels == 0 defaulting shape):
	engineBytes := cfg.MaxEngineBytes
	if engineBytes == nil {
		engineBytes = map[string]int64{
			convert.EngineImage:    100 << 20,
			convert.EngineDocument: 100 << 20,
			convert.EngineHTML:     100 << 20,
			convert.EngineAudio:    100 << 20,
			convert.EngineAV:       2 << 30, // D-07: 2 GiB, the class the raise exists for
		}
	}
```

---

### `internal/api/handlers.go` — detection chain (D-08 `SniffVideo`), opts dispatch, enqueue switch, per-engine ceiling (D-07)

**Analog:** the file's own `SniffAudio` wiring (detection chain, lines 267-284), `convert.EngineAudio` opts-dispatch case (lines 392-412), and `convert.EngineAudio` enqueue case (lines 526-543) — all in the same file.

**D-08: `SniffVideo` slot in the detection chain.** Per CONTEXT.md's discretion note, the order is `Sniff` (`:188`) → ZIP/`SniffContainer` (`:200-225`) → HTML (`:227-238`) → `IsOLECFB`/CFB (`:239-266`) → `SniffAudio` (`:267-284`, LAST because mp3PeekLen=512KiB is the most expensive buffer). `SniffVideo` should slot BEFORE `SniffAudio` (mkv/webm need EBML parsing, comparable or cheaper cost than the audio peek, and mp4/mov/avi are ALREADY caught by `Sniff`'s prefix table before this point per D-08's own framing — "today mp4/mov/avi are detectable via the signatures table but mkv/webm are not"). Mirror the exact `if detected == "" { ... }` gate shape:
```go
if detected == "" {
	// D-08: mkv/webm EBML-container detection, placed after the OLE-CFB
	// check and before SniffAudio (mirrors SniffAudio's own "detect from
	// magic bytes, no source gate" shape). mp4/mov/avi are already caught
	// by Sniff()'s prefix table earlier in the chain; this closes the
	// mkv/webm gap SniffVideo has had zero non-test callers for since
	// Phase 34 (WR-02).
	if videoDetected, videoRest, verr := convert.SniffVideo(rest); verr == nil && videoDetected != "" {
		detected = videoDetected
		rest = videoRest
	}
}
if detected == "" {
	// AUD-05: mp3/wav/m4a/ogg content detection ... (existing SniffAudio block, unchanged)
```

**D-07: per-engine ceiling check, inserted immediately after `EngineFor`** (new code, per RESEARCH.md's Code Examples section, mirrors the existing `maxDocumentUncompressedBytes`/`maxImagePixels` rejection-logging shape at `handlers.go:207-212`, `:332-337`):
```go
	engine, ok := convert.Default.EngineFor(detected, target)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "unsupported conversion: "+detected+" -> "+target)
		return
	}

	// D-07: per-engine post-detection ceiling — the global s.maxUploadByte
	// (enforced pre-parse by http.MaxBytesReader, line 93) is now a wide
	// 2 GiB video-appropriate cap; this second check restores each
	// non-video class's original tighter ceiling so raising the global cap
	// for video does not silently raise the DoS ceiling for image/document/
	// html/audio uploads.
	if limit, ok := s.maxEngineBytes[engine]; ok && header.Size > limit {
		log.Printf("content validation rejected: client_id=%s filename=%q reason=engine_size_limit engine=%s size=%d limit=%d", client.ID, filename, engine, header.Size, limit)
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds size limit for this conversion type")
		return
	}
```

**Opts-dispatch switch case, mirroring the `convert.EngineAudio` branch exactly** (lines 392-412):
```go
		switch engine {
		case convert.EngineAudio:
			// ... existing audio branch unchanged ...
		case convert.EngineAV: // NEW
			avOpts, err := convert.ParseAVOpts([]byte(rawOpts))
			if err != nil {
				log.Printf("content validation rejected: client_id=%s filename=%q reason=invalid_opts", client.ID, filename)
				writeError(w, http.StatusUnprocessableEntity, "invalid opts")
				return
			}
			if err := convert.ValidateAVApplicability(engine, detected, target, avOpts); err != nil {
				log.Printf("content validation rejected: client_id=%s filename=%q reason=opts_not_applicable", client.ID, filename)
				writeError(w, http.StatusUnprocessableEntity, "opts not applicable to this conversion")
				return
			}
			normalizedRaw, err = json.Marshal(avOpts)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to normalize opts")
				return
			}
		case convert.EngineHTML:
			// ... existing html branch unchanged ...
```
Note: `convert.ParseAVOpts` and `convert.ValidateAVApplicability` already exist from Phase 34 (`avopts.go:99`, `:145`) — this is registration, not new validation logic.

**Enqueue switch case, mirroring the `convert.EngineAudio` branch exactly** (lines 526-543):
```go
	switch engine {
	case convert.EngineImage:
		enqueueErr = s.queue.EnqueueImageConvert(ctx, createdID)
	case convert.EngineDocument:
		enqueueErr = s.queue.EnqueueDocumentConvert(ctx, createdID)
	case convert.EngineHTML:
		enqueueErr = s.queue.EnqueueHTMLConvert(ctx, createdID)
	case convert.EngineAudio:
		enqueueErr = s.queue.EnqueueAudioConvert(ctx, createdID)
	case convert.EngineAV: // NEW
		enqueueErr = s.queue.EnqueueAVConvert(ctx, createdID)
	default:
		// unchanged fail-closed default
	}
```

**`HasDimensionLimit` block (line 322):** confirmed non-issue per research — `dimensionParsers` is keyed only to the 5 image formats, video formats never enter this block. No change needed here, but do not re-litigate it.

---

### `internal/reconciler/reconciler.go` — `enqueuer` interface + routing switch

**Analog:** same file (`reconciler.go:58-65`, `:284-305`).

**Interface method** (lines 58-65):
```go
type enqueuer interface {
	EnqueueImageConvert(ctx context.Context, id uuid.UUID) error
	EnqueueWebhookDeliver(ctx context.Context, id uuid.UUID) error
	EnqueueDocumentConvert(ctx context.Context, id uuid.UUID) error
	EnqueueHTMLConvert(ctx context.Context, id uuid.UUID) error
	EnqueueAudioConvert(ctx context.Context, id uuid.UUID) error
	EnqueueAVConvert(ctx context.Context, id uuid.UUID) error // NEW
}
```

**Routing switch case, replacing the `default:` comment that explicitly names `av` as next** (lines 284-305):
```go
		switch j.Engine {
		case convert.EngineImage:
			enqueueErr = s.enq.EnqueueImageConvert(ctx, j.ID)
		case convert.EngineDocument:
			enqueueErr = s.enq.EnqueueDocumentConvert(ctx, j.ID)
		case convert.EngineHTML:
			enqueueErr = s.enq.EnqueueHTMLConvert(ctx, j.ID)
		case convert.EngineAudio:
			enqueueErr = s.enq.EnqueueAudioConvert(ctx, j.ID)
		case convert.EngineAV: // NEW
			enqueueErr = s.enq.EnqueueAVConvert(ctx, j.ID)
		default:
			// Fail closed: an unrecognized engine value must never be
			// guessed at. cad/archive/probe remain out of scope.
			metrics.RecordReconcilerAction("unroutable_engine")
			continue
		}
```

---

### `internal/reconciler/reconciler_test.go` — `fakeEnqueuer` + routing test

**Analog:** `fakeEnqueuer.EnqueueAudioConvert`/`audioCalls`/`audioCallIDs` + `TestSweepRoutesAudioJobsToAudioQueue` (`reconciler_test.go:89-184`, `:276-305`).

**Fake fields + method, mirroring the audio block exactly:**
```go
	enqueueAVErr error
	avCalls      []uuid.UUID

func (f *fakeEnqueuer) EnqueueAVConvert(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.avCalls = append(f.avCalls, id)
	return f.enqueueAVErr
}

func (f *fakeEnqueuer) avCallIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uuid.UUID, len(f.avCalls))
	copy(out, f.avCalls)
	return out
}
```

**Routing test, mirroring `TestSweepRoutesAudioJobsToAudioQueue`** (`reconciler_test.go:276-305`):
```go
func TestSweepRoutesAVJobsToAVQueue(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale: []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "av"}},
	}
	enq := &fakeEnqueuer{}
	sweeper := NewSweeper(store, enq, Config{...})
	sweeper.sweep(context.Background())

	if len(enq.avCalls) != 1 || enq.avCalls[0] != id {
		t.Fatalf("EnqueueAVConvert calls = %+v, want [%s]", enq.avCalls, id)
	}
	if len(enq.imageCalls) != 0 {
		t.Fatalf("EnqueueImageConvert should not be called for an av job, got %d calls", len(enq.imageCalls))
	}
	// ... same negative assertions for document/html/audio ...
}
```

---

### `cmd/api/main.go` — queue-depth collector arg (Pitfall 2, SILENT seam)

**Analog:** the existing variadic call (`main.go:91-92`).

```go
	prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt),
		queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueAudio, queue.QueueAV, queue.QueueWebhook)) // add queue.QueueAV
```
**This is the single most important line in the whole phase to double-check by hand** (per Pitfall 2/D-06): `NewQueueDepthCollector(inspector *asynq.Inspector, queues ...string)` (`internal/metrics/queue_collector.go:24`) is variadic — omitting `queue.QueueAV` compiles cleanly and simply drops the Prometheus series KEDA needs for Phase 37 autoscaling, with no test failure unless the D-06 completeness test explicitly covers this call site.

---

### D-06 Completeness test (new — shape borrowed from `TestVideoBrandDisjointness`)

**Analog:** `TestVideoBrandDisjointness` (`internal/convert/avsniff_test.go:118-140`) for the "iterate a closed set, assert membership in each of several parallel structures" shape. CONTEXT.md leaves the exact placement/shape to Claude's discretion (table-driven over `convert.go:19-25`'s engine constants vs. reflection over the switch). Given `cmd/api/main.go`'s collector list cannot be introspected at the `internal/api`/`internal/reconciler` package level (it lives in `cmd/api`), the most direct approach is three separate assertions per engine constant:
```go
// Iterate every convert.Engine* constant and assert:
//  1. a case exists in internal/api/handlers.go's enqueue switch (test lives
//     in internal/api, can call handleCreateJob's dispatch indirectly via a
//     table-driven fixture, OR structurally assert via a package-level map
//     that mirrors the switch — Claude's discretion on exact mechanism).
//  2. a case exists in internal/reconciler/reconciler.go's routing switch
//     (test lives in internal/reconciler, same shape as
//     TestSweepRoutesAudioJobsToAudioQueue but iterating all engines).
//  3. an entry exists in cmd/api/main.go's collector arg list — this one is
//     the hardest to test in-process (cmd/api is package main); consider a
//     package-level var in internal/queue exposing "all known queue names"
//     that cmd/api's call site is asserted (by a cmd/api-local test) to use
//     in full, OR a documented manual-checklist item if no clean unit-test
//     boundary exists — flag this explicitly in the plan rather than skip it
//     silently, since Pitfall 2 names this exact line as the highest-risk
//     silent omission in the whole phase.
```

---

### AV/Audio pair-disjointness test (Pitfall 7)

**Analog:** `TestVideoBrandDisjointness` (`avsniff_test.go:118-140`) for the nested-loop-over-two-collections shape.

```go
// TestAVAudioPairDisjointness proves AVConverter.Pairs() and
// AudioConverter.Pairs() share no (from, to) pair — a regression guard
// against Registry.Register's silent last-write-wins on collision
// (convert.go:76-80, Pitfall 7). Currently safe by construction: AV's
// target formats are {mp4,webm,mp3,wav,m4a,jpg,png,webp}, audio's are
// always {txt,srt,vtt,json} — disjoint target sets, so no (from,to) pair
// can collide even though both share SOURCE formats (mp4/mov/avi/mkv/webm
// after D-04).
func TestAVAudioPairDisjointness(t *testing.T) {
	for _, p := range (AVConverter{}).Pairs() {
		for _, q := range (AudioConverter{}).Pairs() {
			if p == q {
				t.Fatalf("pair %+v registered by both AVConverter and AudioConverter, want disjoint", p)
			}
		}
	}
}
```

---

### `internal/convert/whisper.go` — D-04/D-05 (video source formats, video-aware budget floor, `-map 0:a:0`)

**Analog:** same file's existing `audioSourceFormats`/`minFfmpegBudget`/`ffmpegNormalizeArgs` (lines 62-90, 159-166).

**D-04 — grow `audioSourceFormats`:**
```go
var (
	audioSourceFormats = []string{
		"mp3", "wav", "m4a", "ogg",
		"mp4", "mov", "avi", "mkv", "webm", // D-04: video containers
	}
	audioTargetFormats = []string{"txt", "srt", "vtt", "json"}
)
```

**D-05 — video-aware `minFfmpegBudget`, mirroring the existing const + inline check shape** (`whisper.go:90`, `:283-285`):
```go
const minFfmpegBudget = 30 * time.Second      // unchanged floor for audio-family sources
const minFfmpegBudgetVideo = 90 * time.Second // NEW floor for video-container sources (D-05)

var videoSourceFormats = map[string]bool{"mp4": true, "mov": true, "avi": true, "mkv": true, "webm": true}

// inside Convert, replacing the existing flat check at whisper.go:283-285:
floor := minFfmpegBudget
if videoSourceFormats[NormalizeFormat(filepath.Ext(inPath))] {
	floor = minFfmpegBudgetVideo
}
if dl, ok := ctx.Deadline(); ok && time.Until(dl) < floor {
	return fmt.Errorf("audio: insufficient attempt budget remaining: %w", context.DeadlineExceeded)
}
```

**`-map 0:a:0` fix in `ffmpegNormalizeArgs`** (`whisper.go:163-166`, exact insertion point):
```go
func ffmpegNormalizeArgs(inPath, normPath string) []string {
	return []string{"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:" + inPath,
		"-map", "0:a:0", // NEW: deterministic first-audio-stream selection,
		// mirrors AVConverter's own "0:a:0[?]" convention (av.go:122,143,163).
		// A no-op for existing single-audio-track sources.
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "file:" + normPath}
}
```

---

### `internal/convert/converters.go` — register `AVConverter{}`

**Analog:** same file, full 10-line file, `Default.Register(AudioConverter{})` (line 9).

```go
func init() {
	Default.Register(LibvipsConverter{})
	Default.Register(LibreOfficeConverter{})
	Default.Register(ChromiumConverter{})
	Default.Register(AudioConverter{})
	Default.Register(AVConverter{}) // NEW
}
```
**Registration-order hazard (Pitfall 7):** `Registry.Register` is a bare map assignment with silent last-write-wins on pair collision. Register AV AFTER Audio (as shown) is safe only because their target-format sets are disjoint (verified above) — this is not enforced by the registration order itself, only by the disjointness test.

## Shared Patterns

### Postgres-first double-write + engine-agnostic queue routing
**Source:** `internal/api/handlers.go:495-546` (job Create then enqueue-switch)
**Apply to:** No new code needed here beyond the AV case — the pattern itself (`repo.Create` succeeds before any enqueue attempt; enqueue failure leaves the row `queued` for reconciler recovery) is structural and already covers AV once the switch case exists.

### Enqueue-first reconciler recovery + `asynq.Unique` collision detection
**Source:** `internal/reconciler/reconciler.go:275-305`, `internal/queue/queue.go:396-398` (`ImageUniqueTTL` doc comment)
**Apply to:** `AVUniqueTTL` — the derived TTL must outlive asynq's true worst-case retry lifetime `(maxRetry+1)*engineTimeout + backoffSum` so a still-legitimately-running job's lock survives long enough for a duplicate reconciler enqueue to safely no-op via `asynq.ErrDuplicateTask`.

### `asynq.SkipRetry` terminal-failure wrapping discipline
**Source:** `internal/worker/worker.go:459-534` (`HandleDocumentConvert`, unparseable payload / invalid opts / terminal-classified process() error all wrap with `fmt.Errorf("%w: %v", asynq.SkipRetry, err)`)
**Apply to:** `HandleAVConvert` — every terminal branch must wrap with `asynq.SkipRetry`; every transient branch returns the bare error so asynq's own retry/backoff (via `AVRetryDelay`) applies.

### Postgres-first webhook enqueue ordering
**Source:** `internal/worker/worker.go:483-491`, `:513-518` (webhook enqueued ONLY after `MarkFailed`/`MarkDone` successfully commits; failed enqueue is best-effort, never fails the conversion)
**Apply to:** `HandleAVConvert`'s terminal and success paths — identical `if ferr == nil && job.CallbackURL != ""` / `if job.CallbackURL != ""` gating.

### Env-only-in-main / no `os.Getenv` inside `internal/convert`
**Source:** CLAUDE.md Architectural Constraints; `cmd/audio-worker/main.go:87,97` (`convert.SetAudioModelPath`, `convert.SetAudioThreads`)
**Apply to:** `AV_ENGINE_TIMEOUT`, `AV_MAX_RETRY`, `AV_WORKER_CONCURRENCY` are all read in `cmd/av-worker/main.go`/`internal/queue/client.go`, never inside `internal/convert/av.go`. No new setter is needed for AV in this phase since `avThreadCount()` already resolves CPU count without an env-driven override (unlike audio's `AUDIO_THREADS`).

### `fmt.Errorf("<action>: %w", err)` wrapping convention
**Source:** CLAUDE.md Error Handling; `internal/queue/client.go:113,125,138,151,163` (every `Enqueue*` method)
**Apply to:** `EnqueueAVConvert` — `fmt.Errorf("enqueue av convert %s: %w", jobID, err)`, exact same shape.

## No Analog Found

None — every file in this phase's scope has a direct, working analog in the audio engine class (Phases 30-33) or in the phase's own prior file (`av.go`'s sentinel-error idiom). This phase is pure wiring, not invention (per RESEARCH.md's own conclusion).

## Metadata

**Analog search scope:** `cmd/audio-worker/`, `internal/queue/`, `internal/worker/`, `internal/api/`, `internal/reconciler/`, `internal/convert/`, `cmd/api/`, `internal/metrics/` — all read directly at HEAD (no broader search needed; RESEARCH.md already verified every file:line seam)
**Files scanned:** 15 (11 targeted analog files + 4 files whose full content was read: `cmd/audio-worker/main.go`, `internal/convert/converters.go`, `internal/api/api.go`, `cmd/api/main.go`)
**Pattern extraction date:** 2026-07-21
