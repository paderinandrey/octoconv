// Package queue defines the asynq task types and helpers used to dispatch
// conversion work to the engine-class workers.
package queue

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/apaderin/octoconv/internal/convert"
)

// Task type names. One asynq task type per engine class operation.
const (
	TypeImageConvert    = "image:convert"
	TypeWebhookDeliver  = "webhook:deliver"
	TypeDocumentConvert = "document:convert"
	TypeHTMLConvert     = "html:convert"
	TypeAudioConvert    = "audio:convert"
)

// Queue names. asynq routes tasks to a queue per engine class so workers and
// autoscaling can be scoped to a single class. QueueImage/QueueDocument/
// QueueHTML/QueueAudio are tied to convert.EngineImage/EngineDocument/
// EngineHTML/EngineAudio (the single source of truth for engine-class
// literals, DEBT-02) so queue names cannot drift from the engine-class
// identifiers.
const (
	QueueImage    = convert.EngineImage
	QueueWebhook  = "webhook"
	QueueDocument = convert.EngineDocument
	QueueHTML     = convert.EngineHTML
	QueueAudio    = convert.EngineAudio
)

// ConvertPayload is the task payload. It carries only the job id — all task
// details live in Postgres, the system of record.
type ConvertPayload struct {
	JobID uuid.UUID `json:"job_id"`
}

// WebhookPayload is the task payload for a webhook delivery attempt. It
// carries only the job id — the handler re-reads callback_url/status/error
// details from Postgres per attempt, same minimal-payload discipline as
// ConvertPayload (D-04).
type WebhookPayload struct {
	JobID uuid.UUID `json:"job_id"`
}

// NewImageConvertTask builds an asynq task for an image conversion job,
// routed to the image queue, bounded to maxRetry (D-05, IMAGE_MAX_RETRY —
// small compared to webhook's 6, since image conversion failures should
// retry a few times fast, not linger), and carrying a per-job asynq.Unique
// lock (uniqueTTL, see ImageUniqueTTL) so a second enqueue for the same
// jobID while the first task/lock is still live collides on the same
// uniqueness key and returns asynq.ErrDuplicateTask instead of creating a
// second concurrent task — the mechanism the Plan 03 reconciler relies on.
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

// ParseConvertPayload decodes a ConvertPayload from a task body.
func ParseConvertPayload(b []byte) (ConvertPayload, error) {
	var p ConvertPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("unmarshal convert payload: %w", err)
	}
	return p, nil
}

// NewDocumentConvertTask builds an asynq task for a document conversion job,
// routed to the document queue, bounded to maxRetry (DOCUMENT_MAX_RETRY —
// a bounded budget lower than image's, since each document attempt is
// expensive at up to DOCUMENT_ENGINE_TIMEOUT seconds), and carrying a
// per-job asynq.Unique lock (uniqueTTL, see DocumentUniqueTTL) so a second
// enqueue for the same jobID while the first task/lock is still live
// collides on the same uniqueness key and returns asynq.ErrDuplicateTask
// instead of creating a second concurrent task — mirrors
// NewImageConvertTask exactly; reuses ConvertPayload/ParseConvertPayload
// (no new payload type needed, all job detail is re-read from Postgres).
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

// NewHTMLConvertTask builds an asynq task for an html-to-pdf conversion job,
// routed to the html queue, bounded to maxRetry (HTML_MAX_RETRY), and
// carrying a per-job asynq.Unique lock (uniqueTTL, see HTMLUniqueTTL) so a
// second enqueue for the same jobID while the first task/lock is still live
// collides on the same uniqueness key and returns asynq.ErrDuplicateTask
// instead of creating a second concurrent task — mirrors
// NewDocumentConvertTask exactly; reuses ConvertPayload/ParseConvertPayload
// (no new payload type needed, all job detail is re-read from Postgres).
func NewHTMLConvertTask(jobID uuid.UUID, maxRetry int, uniqueTTL time.Duration) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeHTMLConvert, b,
		asynq.Queue(QueueHTML),
		asynq.MaxRetry(maxRetry),
		asynq.Unique(uniqueTTL),
	), nil
}

// NewAudioConvertTask builds an asynq task for an audio transcription job,
// routed to the audio queue, bounded to maxRetry (AUDIO_MAX_RETRY), and
// carrying a per-job asynq.Unique lock (uniqueTTL, see AudioUniqueTTL) so a
// second enqueue for the same jobID while the first task/lock is still live
// collides on the same uniqueness key and returns asynq.ErrDuplicateTask
// instead of creating a second concurrent task — mirrors
// NewDocumentConvertTask exactly; reuses ConvertPayload/ParseConvertPayload
// (no new payload type needed, all job detail is re-read from Postgres).
func NewAudioConvertTask(jobID uuid.UUID, maxRetry int, uniqueTTL time.Duration) (*asynq.Task, error) {
	b, err := json.Marshal(ConvertPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal convert payload: %w", err)
	}
	return asynq.NewTask(TypeAudioConvert, b,
		asynq.Queue(QueueAudio),
		asynq.MaxRetry(maxRetry),
		asynq.Unique(uniqueTTL),
	), nil
}

// NewWebhookDeliverTask builds an asynq task for a single webhook delivery
// job, routed to the webhook queue and bounded to MaxRetry=6 (D-05) — retry
// count is a property of the task, set once at creation. Carries a per-job
// asynq.Unique lock (uniqueTTL, see WebhookUniqueTTL) so a second enqueue for
// the same jobID while the first task/lock is still live collides on the
// same uniqueness key and returns asynq.ErrDuplicateTask instead of creating
// a second concurrent task — the mechanism RECON-04's gap sweep relies on,
// mirroring NewImageConvertTask's asynq.Unique shape exactly.
func NewWebhookDeliverTask(jobID uuid.UUID, uniqueTTL time.Duration) (*asynq.Task, error) {
	b, err := json.Marshal(WebhookPayload{JobID: jobID})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook payload: %w", err)
	}
	return asynq.NewTask(TypeWebhookDeliver, b,
		asynq.Queue(QueueWebhook),
		asynq.MaxRetry(webhookMaxRetry),
		asynq.Unique(uniqueTTL),
	), nil
}

// ParseWebhookPayload decodes a WebhookPayload from a task body.
func ParseWebhookPayload(b []byte) (WebhookPayload, error) {
	var p WebhookPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("unmarshal webhook payload: %w", err)
	}
	return p, nil
}

// webhookRetrySchedule is the base backoff schedule for webhook delivery
// retries (D-05): 30s, 1m, 2m, 4m, 8m, 15m — a ~30 minute total retry window
// across MaxRetry=6 attempts.
var webhookRetrySchedule = []time.Duration{
	30 * time.Second,
	1 * time.Minute,
	2 * time.Minute,
	4 * time.Minute,
	8 * time.Minute,
	15 * time.Minute,
}

// WebhookRetryDelay is an asynq RetryDelayFunc for the webhook queue. asynq
// calls this with n = the 0-based count of retries so far (0 on the first
// retry, after the first delivery attempt failed), so n indexes directly
// into webhookRetrySchedule with no off-by-one adjustment. The index is
// clamped to the last entry once n exceeds the schedule length, with up to
// ±25% jitter so simultaneously-failing deliveries don't thundering-herd a
// recovering endpoint (D-05).
func WebhookRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	idx := n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(webhookRetrySchedule) {
		idx = len(webhookRetrySchedule) - 1
	}
	base := webhookRetrySchedule[idx]

	// Jitter of up to ±25%.
	jitterRange := float64(base) * 0.25
	jitter := (rand.Float64()*2 - 1) * jitterRange
	delay := time.Duration(float64(base) + jitter)
	if delay < 0 {
		delay = 0
	}
	return delay
}

// imageRetrySchedule is the fast backoff schedule for image conversion
// retries (D-06): 2s, 5s, 15s — deliberately a seconds-scale schedule,
// distinct from webhookRetrySchedule's minutes-scale 30s->15m window, since
// a transient image conversion failure (engine timeout, storage hiccup)
// should be retried quickly a few times, not ground through slowly.
var imageRetrySchedule = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	15 * time.Second,
}

// ImageRetryDelay is an asynq RetryDelayFunc for the image queue. Mirrors
// WebhookRetryDelay's clamp-index-to-schedule-length shape but WITHOUT
// jitter — D-06 does not require jitter for image retries, since the
// schedule is already short and image tasks are not competing to avoid a
// thundering herd on a shared external endpoint the way webhook deliveries
// are.
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

// documentRetrySchedule is the backoff schedule for document conversion
// retries: 5s, 15s, 30s — no jitter, mirroring imageRetrySchedule's shape
// exactly but scaled up proportionally to DOCUMENT_ENGINE_TIMEOUT being
// ~2.5x ENGINE_TIMEOUT (300s vs 120s), since a transient document conversion
// failure (LibreOffice engine hiccup, storage blip) should still be retried
// a few times quickly, just on a slightly longer cadence than image's
// 2s/5s/15s given the heavier per-attempt cost.
var documentRetrySchedule = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

// DocumentRetryDelay is an asynq RetryDelayFunc for the document queue.
// Mirrors ImageRetryDelay's clamp-index-to-schedule-length shape exactly —
// NOT WebhookRetryDelay's jittered shape — since document retries don't need
// jitter to avoid thundering-herding a shared external endpoint the way
// webhook deliveries do.
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

// htmlRetrySchedule is the backoff schedule for html-to-pdf conversion
// retries: 5s, 15s, 30s — no jitter, mirroring documentRetrySchedule's shape
// exactly (same rationale: a transient chromium render failure should be
// retried a few times quickly, not ground through slowly, and there is no
// shared external endpoint here that needs jitter to avoid a thundering
// herd).
var htmlRetrySchedule = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

// HTMLRetryDelay is an asynq RetryDelayFunc for the html queue. Mirrors
// DocumentRetryDelay's clamp-index-to-schedule-length shape exactly — NOT
// WebhookRetryDelay's jittered shape.
func HTMLRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	idx := n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(htmlRetrySchedule) {
		idx = len(htmlRetrySchedule) - 1
	}
	return htmlRetrySchedule[idx]
}

// audioRetrySchedule is the backoff schedule for audio transcription
// retries: 5s, 15s, 30s — no jitter, mirroring documentRetrySchedule's/
// htmlRetrySchedule's shape exactly (a defensible default: a transient
// audio-pipeline failure — ffmpeg normalize or whisper-cli transcribe — should
// still be retried a few times quickly, on the same cadence as the other
// non-webhook engine classes).
var audioRetrySchedule = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

// AudioRetryDelay is an asynq RetryDelayFunc for the audio queue. Mirrors
// DocumentRetryDelay's/HTMLRetryDelay's clamp-index-to-schedule-length shape
// exactly — NOT WebhookRetryDelay's jittered shape.
func AudioRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	idx := n
	if idx < 0 {
		idx = 0
	}
	if idx >= len(audioRetrySchedule) {
		idx = len(audioRetrySchedule) - 1
	}
	return audioRetrySchedule[idx]
}

// RetryDelayFunc dispatches to a per-queue retry delay function based on the
// task type. It fixes the confirmed defect where every queue silently
// inherited WebhookRetryDelay via a single server-wide
// asynq.Config.RetryDelayFunc: image tasks now retry on their own fast
// schedule, webhook tasks keep their existing schedule, and any future task
// type falls back to asynq's own default rather than accidentally reusing
// the webhook schedule.
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
	default:
		return asynq.DefaultRetryDelayFunc(n, e, t)
	}
}

// uniqueTTLSafetyMargin is fixed headroom added on top of the computed
// worst-case image-retry lifetime, covering the case where every attempt
// hangs for the full ENGINE_TIMEOUT.
const uniqueTTLSafetyMargin = 2 * time.Minute

// imageBackoffSum sums ImageRetryDelay(i) for i in [0, maxRetry) — the total
// backoff time asynq spends waiting before each of maxRetry retries,
// clamped to the last schedule entry once i reaches len(imageRetrySchedule).
func imageBackoffSum(maxRetry int) time.Duration {
	var sum time.Duration
	for i := 0; i < maxRetry; i++ {
		sum += ImageRetryDelay(i, nil, nil)
	}
	return sum
}

// ImageUniqueTTL derives the per-job asynq.Unique lock TTL for image
// conversion tasks from the actual retry budget (maxRetry, normally
// IMAGE_MAX_RETRY) and the per-attempt bound (engineTimeout, normally
// ENGINE_TIMEOUT), so the lock can never silently drift under asynq's true
// worst-case retry lifetime if either env var changes later.
//
// Worst-case formula: (maxRetry+1) * engineTimeout + imageBackoffSum(maxRetry) + margin.
// asynq's archive condition is `msg.Retried >= msg.Retry`, checked AFTER
// each failed attempt, so a task with MaxRetry=maxRetry gets maxRetry+1
// total engine executions (1 initial attempt + maxRetry retries) before
// archival — NOT maxRetry. For the defaults (IMAGE_MAX_RETRY=4,
// ENGINE_TIMEOUT=120s) this is 5*120s + (2+5+15+15)s + 120s = 757s,
// comfortably above asynq's 637s worst-case retry lifetime
// (5*120s + 37s).
//
// The lock must outlive that lifetime for two reasons: (a) while asynq is
// still legitimately retrying, a reconciler re-enqueue for the same job
// collides on the still-live lock and is a safe no-op; (b) asynq does NOT
// release the unique lock on archive (only on success or TTL expiry), so
// once the retry budget is exhausted the lock lapses within a bounded
// window and the reconciler can genuinely re-enqueue.
//
// The TTL is DERIVED rather than a hardcoded constant deliberately: if
// IMAGE_MAX_RETRY or ENGINE_TIMEOUT are later changed via env, the margin
// scales with them automatically and cannot fall behind the real retry
// lifetime.
//
// SOUNDNESS DEPENDENCY: this "each attempt <= engineTimeout" worst-case
// bound is sound only because Plan 02 (03-02 T-03-11) bounds the ENTIRE
// conversion attempt — download + convert + upload + record, not just
// conv.Convert() — with a single context.WithTimeout(ctx, ENGINE_TIMEOUT)
// in process(); a stalled S3 transfer on an unbounded ctx would otherwise
// let one attempt exceed engineTimeout and silently outlive this lock.
func ImageUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + imageBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}

// documentBackoffSum sums DocumentRetryDelay(i) for i in [0, maxRetry) — the
// total backoff time asynq spends waiting before each of maxRetry retries,
// clamped to the last schedule entry once i reaches
// len(documentRetrySchedule). This mirrors imageBackoffSum and deliberately
// does NOT follow webhookBackoffSum's jitter-ceiling shape: calling
// DocumentRetryDelay directly here is safe because DocumentRetryDelay — like
// ImageRetryDelay — has no jitter, so there is no random sample to
// accidentally bake into the derivation.
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
// DOCUMENT_ENGINE_TIMEOUT), mirroring ImageUniqueTTL's derivation exactly so
// the lock can never silently drift under asynq's true worst-case retry
// lifetime if either env var changes later.
//
// Worst-case formula: (maxRetry+1) * engineTimeout + documentBackoffSum(maxRetry) + margin.
// Same "(maxRetry+1) executions, not maxRetry" correction as ImageUniqueTTL
// applies here (asynq's archive condition is checked AFTER each failed
// attempt). REUSES the shared uniqueTTLSafetyMargin const verbatim — no
// document-specific margin constant.
//
// The TTL is DERIVED rather than a hardcoded constant deliberately: if
// DOCUMENT_MAX_RETRY or DOCUMENT_ENGINE_TIMEOUT are later changed via env,
// the margin scales with them automatically and cannot fall behind the real
// retry lifetime.
func DocumentUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + documentBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}

// htmlBackoffSum sums HTMLRetryDelay(i) for i in [0, maxRetry) — the total
// backoff time asynq spends waiting before each of maxRetry retries,
// clamped to the last schedule entry once i reaches len(htmlRetrySchedule).
// Mirrors documentBackoffSum; safe to call HTMLRetryDelay directly since it
// has no jitter (same reasoning as documentBackoffSum's doc comment).
func htmlBackoffSum(maxRetry int) time.Duration {
	var sum time.Duration
	for i := 0; i < maxRetry; i++ {
		sum += HTMLRetryDelay(i, nil, nil)
	}
	return sum
}

// HTMLUniqueTTL derives the per-job asynq.Unique lock TTL for html-to-pdf
// conversion tasks from the actual retry budget (maxRetry, normally
// HTML_MAX_RETRY) and the per-attempt bound (engineTimeout, normally
// HTML_ENGINE_TIMEOUT), mirroring DocumentUniqueTTL's derivation exactly so
// the lock can never silently drift under asynq's true worst-case retry
// lifetime if either env var changes later.
//
// Worst-case formula: (maxRetry+1) * engineTimeout + htmlBackoffSum(maxRetry) + margin.
// Same "(maxRetry+1) executions, not maxRetry" correction as
// ImageUniqueTTL/DocumentUniqueTTL applies here. REUSES the shared
// uniqueTTLSafetyMargin const verbatim — no html-specific margin constant.
func HTMLUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + htmlBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}

// audioBackoffSum sums AudioRetryDelay(i) for i in [0, maxRetry) — the total
// backoff time asynq spends waiting before each of maxRetry retries, clamped
// to the last schedule entry once i reaches len(audioRetrySchedule). Mirrors
// documentBackoffSum/htmlBackoffSum; safe to call AudioRetryDelay directly
// since it has no jitter (same reasoning as documentBackoffSum's doc
// comment).
func audioBackoffSum(maxRetry int) time.Duration {
	var sum time.Duration
	for i := 0; i < maxRetry; i++ {
		sum += AudioRetryDelay(i, nil, nil)
	}
	return sum
}

// AudioUniqueTTL derives the per-job asynq.Unique lock TTL for audio
// transcription tasks from the actual retry budget (maxRetry, normally
// AUDIO_MAX_RETRY) and the per-attempt bound (engineTimeout, normally
// AUDIO_ENGINE_TIMEOUT), mirroring DocumentUniqueTTL's/HTMLUniqueTTL's
// derivation exactly so the lock can never silently drift under asynq's true
// worst-case retry lifetime if either env var changes later.
//
// Per the AudioUniqueTTL binding decision (STATE.md): this is DERIVED FRESH
// from AUDIO_MAX_RETRY/AUDIO_ENGINE_TIMEOUT — it deliberately never reuses
// ImageUniqueTTL/DocumentUniqueTTL/HTMLUniqueTTL's values, since audio's
// per-attempt cost (ffmpeg normalize + whisper-cli transcribe, bounded by a
// distinct AUDIO_ENGINE_TIMEOUT) is not comparable to any other engine
// class's.
//
// Worst-case formula: (maxRetry+1) * engineTimeout + audioBackoffSum(maxRetry) + margin.
// Same "(maxRetry+1) executions, not maxRetry" correction as
// ImageUniqueTTL/DocumentUniqueTTL/HTMLUniqueTTL applies here. REUSES the
// shared uniqueTTLSafetyMargin const verbatim — no audio-specific margin
// constant.
//
// The TTL is DERIVED rather than a hardcoded constant deliberately: if
// AUDIO_MAX_RETRY or AUDIO_ENGINE_TIMEOUT are later changed via env, the
// margin scales with them automatically and cannot fall behind the real
// retry lifetime.
func AudioUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + audioBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}

// webhookJitterCeiling is WebhookRetryDelay's documented maximum +25% jitter
// bound. webhookBackoffSum uses this to compute a genuine worst-case backoff
// sum rather than a random sample.
const webhookJitterCeiling = 1.25

// webhookMaxRetry mirrors the literal 6 passed to asynq.MaxRetry in
// NewWebhookDeliverTask. Unlike imageMaxRetry (IMAGE_MAX_RETRY), this is NOT
// env-configurable — Phase 2's D-05 fixed MaxRetry=6 as a constant; kept here
// as a named const (not a magic number) so NewWebhookDeliverTask and
// WebhookUniqueTTL can never silently drift apart. Revisit only if a future
// phase (D-05 in that phase's own numbering) makes it configurable.
const webhookMaxRetry = 6

// webhookPerAttemptTimeout MUST match internal/webhook/deliver.go's
// NewDeliverer HTTP client Timeout field. There is no shared exported
// constant between the two packages (internal/webhook does not export one,
// and internal/queue does not currently import internal/webhook) — this is a
// manually-maintained invariant, same discipline as detailActionRecovery's
// single-source-of-truth comment in internal/jobs/repo.go. If deliver.go's
// timeout ever changes, this constant must change with it.
const webhookPerAttemptTimeout = 10 * time.Second

// webhookBackoffSum sums the WORST-CASE (jitter-inflated) backoff for i in
// [0, maxRetry), using the raw webhookRetrySchedule directly rather than
// calling the jittered WebhookRetryDelay. Calling WebhookRetryDelay here
// would bake in a random sample each time, silently violating the "always
// exceeds worst case" contract this TTL must satisfy (see ImageUniqueTTL's
// doc comment for the same contract on the image side, where it holds
// trivially because ImageRetryDelay has no jitter). This is the single most
// important deviation from imageBackoffSum's shape — do NOT port it
// verbatim.
func webhookBackoffSum(maxRetry int) time.Duration {
	var sum time.Duration
	for i := 0; i < maxRetry; i++ {
		idx := i
		if idx >= len(webhookRetrySchedule) {
			idx = len(webhookRetrySchedule) - 1
		}
		sum += time.Duration(float64(webhookRetrySchedule[idx]) * webhookJitterCeiling)
	}
	return sum
}

// WebhookUniqueTTL derives the per-job asynq.Unique lock TTL for webhook
// delivery tasks, mirroring ImageUniqueTTL's derivation exactly: (maxRetry+1)
// total attempts (asynq's archive check runs AFTER each failed attempt, same
// as the image queue) times the per-attempt bound, plus the worst-case
// (jitter-inflated) backoff sum, plus the shared safety margin. REUSES
// uniqueTTLSafetyMargin verbatim (RESEARCH.md A1) rather than a
// webhook-specific margin constant.
//
// Worst-case formula: (maxRetry+1) * perAttemptTimeout + webhookBackoffSum(maxRetry) + margin.
// For the fixed webhook constants (maxRetry=6, perAttemptTimeout=10s):
//
//	7*10s + (37.5+75+150+300+600+1125)s + 120s = 70s + 2287.5s + 120s = 2477.5s (~41m17.5s)
//
// — meaningfully longer than the "~30 minute" backoff-window figure quoted in
// Phase 2's context, because that figure did not account for jitter, the
// "+1 attempt" correction, or the safety margin.
//
// The TTL is DERIVED rather than a hardcoded constant deliberately: if
// webhookMaxRetry or webhookPerAttemptTimeout are later changed, the margin
// scales with them automatically and cannot fall behind the real retry
// lifetime.
//
// SOUNDNESS CAVEAT (weaker than ImageUniqueTTL's): ImageUniqueTTL's
// correctness depends on Plan 02 (03-02) wrapping the ENTIRE image-conversion
// attempt in a single context.WithTimeout(ctx, ENGINE_TIMEOUT). No equivalent
// wrapping exists for HandleWebhookDeliver (internal/worker/worker.go) — only
// the outbound HTTP POST itself is bounded, at 10s, via webhook.Deliverer's
// http.Client.Timeout. The Postgres reads (Get, Outputs) and presign-URL
// generation that happen before the POST are NOT wrapped in any per-attempt
// deadline. This is an accepted, pre-existing residual risk (out of this
// phase's scope), documented here rather than silently assumed.
func WebhookUniqueTTL(maxRetry int, perAttemptTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*perAttemptTimeout + webhookBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}

// RedisOpt builds the asynq Redis connection options from REDIS_ADDR.
func RedisOpt() (asynq.RedisClientOpt, error) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		return asynq.RedisClientOpt{}, fmt.Errorf("REDIS_ADDR must be set")
	}
	return asynq.RedisClientOpt{Addr: addr}, nil
}
