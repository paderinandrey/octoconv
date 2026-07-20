// Package worker contains the asynq task handlers that run conversion engines.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/minio/minio-go/v7"

	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/metrics"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/storage"
	"github.com/apaderin/octoconv/internal/webhook"
)

// terminalVipsSignatures are stderr substrings (lowercased) that vips emits
// for genuinely corrupted/unknown input formats. Verified live-tested
// (debian:bookworm-slim + libvips-tools, vips-8.14.1): exit code is 1 for
// EVERY failure mode (transient or terminal), so classification must be on
// stderr content, not exit code.
var terminalVipsSignatures = []string{
	"is not a known file format",
	"premature end of jpeg file",
	"jpeg datastream contains no image",
}

// terminalLibreOfficeSignatures are lowercased error-message substrings that
// indicate a deterministically-unrecoverable document conversion:
// validateDocumentOutput's guard against LibreOffice's documented "exit 0 but
// empty/corrupt/wrong-container output" failure mode ("output is empty",
// "output missing %pdf- magic bytes" from validatePDF, and "output does not
// match expected container format" from validateDocumentOutput's non-pdf
// SniffContainer check, internal/convert/libreoffice.go), plus filterFor's
// unsupported-(source,target)-pair error ("no export filter for"). No retry
// can fix any of these — a corrupt or filter-confused document always fails
// validateDocumentOutput/filterFor again (D-04): coupling the validator's
// error string into this slice in the same commit that introduces it is what
// keeps a mismatched cross-format output from being silently retried up to
// DOCUMENT_MAX_RETRY times before finally failing (T-13-02).
var terminalLibreOfficeSignatures = []string{
	"output missing %pdf- magic bytes",
	"output is empty",
	"no export filter for",
	"output does not match expected container format",
	"produced no output file",
	// PDF/A OutputIntent marker missing on a requested pdf_profile export
	// (D-05/D-06, phase 14): a mis-tagged output always fails the same
	// check again, so it must never retry into a false "done".
	"output missing pdf/a outputintent marker",
	// pdf_profile persisted on a non-pdf target (Convert's argv-side
	// invariant guard, internal/convert/libreoffice.go, WR-03): a corrupt
	// or hand-inserted jobs.options row deterministically fails the same
	// check on every delivery.
	"pdf_profile requested for non-pdf target",
}

// terminalChromiumSignatures are lowercased error-message substrings that
// indicate a deterministically-unrecoverable html->pdf conversion. Verified
// live-tested (Plan 04 smoke checklist, debian:bookworm-slim +
// chromium-headless-shell 150.0.7871.100):
//   - "output is empty" / "output missing %pdf- magic bytes" -- carried
//     over unchanged; internal/convert/chromium.go reuses validatePDF
//     verbatim from libreoffice.go, so these two error strings apply
//     identically to a corrupt/empty chromium-produced PDF.
//   - "stat output" -- validatePDF's os.Stat failure branch
//     (fmt.Errorf("libreoffice: stat output: %w", err)) fires when
//     --print-to-pdf writes NO file at all. Live-observed directly during
//     the smoke checklist's item 2 investigation: chromium-headless-shell
//     can exit 0 while silently producing zero output for a render that
//     never completes the print-to-pdf command handler (originally
//     triggered by the since-removed --blink-settings=scriptEnabled=false
//     flag, but the underlying "exit 0, no output file" failure mode is a
//     property of the one-shot command handler itself, not exclusive to
//     that one flag) -- classifying it terminal prevents burning the full
//     HTML_MAX_RETRY budget on a render that will deterministically produce
//     no output again on retry.
var terminalChromiumSignatures = []string{
	"output is empty",
	"output missing %pdf- magic bytes",
	"stat output",
}

// terminalAudioSignatures are lowercased error-message substrings that
// classify validateAudioOutput's "exit 0 but empty/missing output" failure
// mode (internal/convert/whisper.go) as deterministically unrecoverable:
// whisper-cli can exit 0 while writing an empty file ("audio: output is
// empty") or no file at all ("audio: stat output", validateAudioOutput's
// os.Stat failure branch) — a deterministic no-output render produces no
// output again on retry, so retrying only burns AUDIO_MAX_RETRY budget.
// Before WR-03 these two errors classified terminal only by ACCIDENT —
// substring-matching the foreign "output is empty"/"stat output" entries in
// terminalLibreOfficeSignatures/terminalChromiumSignatures inside the shared
// isTerminal loop — so rewording another engine's signature (or
// validateAudioOutput's message) would have silently flipped audio retry
// behavior with no failing test. This dedicated list, checked explicitly in
// isAudioTerminal BEFORE the shared-isTerminal fallthrough, makes the audio
// outcome self-contained; coupling validateAudioOutput's exact error strings
// into this slice follows the same commit-coupling discipline the
// LibreOffice/veraPDF lists document (D-04/D-06 precedent). The "audio: "
// prefix keeps these entries from ever matching another engine's stderr.
var terminalAudioSignatures = []string{
	"audio: output is empty",
	"audio: stat output",
}

// terminalVeraPDFSignatures are lowercased error-message substrings that
// classify a real ISO 19005-2b PDF/A validation failure as deterministically
// unrecoverable (D-06, phase 23): "pdf/a non-compliant" is emitted by
// ValidatePDFA (internal/convert/verapdf.go) when veraPDF's machine-readable
// report says isCompliant=false; "pdf/a validation error" is emitted for ANY
// veraPDF invocation/parse failure (unparseable report, batchSummary failure
// counters, process start failure) -- fail-closed per D-06: an unverifiable
// archival claim is a failed archival claim, never silently retried into a
// false "done". A VERAPDF_TIMEOUT expiry needs no signature here --
// isDocumentTerminal already classifies a wrapped context.DeadlineExceeded as
// terminal before this substring loop is ever reached. Coupling both
// substrings into this slice in the SAME commit that introduces them
// (verapdf.go) is what keeps a non-compliant/unverifiable PDF/A output from
// being silently retried up to DOCUMENT_MAX_RETRY times before finally
// failing.
var terminalVeraPDFSignatures = []string{
	"pdf/a non-compliant",
	"pdf/a validation error",
}

// isTerminal classifies a process() error as terminal (no retry can help:
// bad input, unsupported pair, missing storage object) vs. transient
// (network/S3/engine-timeout/Postgres blip — broad-retry default, D-01).
// This is the SHARED classifier used by both HandleImageConvert and (via
// isDocumentTerminal) HandleDocumentConvert. It deliberately has NO
// context.DeadlineExceeded arm — a timeout must keep classifying as
// transient here so the image path (HandleImageConvert) continues to retry
// engine timeouts; the document engine's divergent timeout-is-terminal
// behavior (DOC-08) lives in the engine-scoped isDocumentTerminal below, not
// here.
func isTerminal(err error) bool {
	if err == nil {
		return false
	}
	// internal/storage wraps every minio error via fmt.Errorf("...: %w", err),
	// and minio.ToErrorResponse itself is a bare type switch (no unwrapping) —
	// so errors.As must walk the %w chain first to surface the underlying
	// minio.ErrorResponse before classifying its Code.
	var mErr minio.ErrorResponse
	if errors.As(err, &mErr) && minio.ToErrorResponse(mErr).Code == minio.NoSuchKey {
		// D-02: storage input genuinely missing.
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no converter for") {
		// D-01: registry.Lookup miss — no engine supports this format pair.
		return true
	}
	for _, sig := range terminalVipsSignatures {
		if strings.Contains(msg, sig) {
			// D-01: engine reports a corrupted/unknown input format.
			return true
		}
	}
	for _, sig := range terminalLibreOfficeSignatures {
		if strings.Contains(msg, sig) {
			// D-01 (document analog): LibreOffice reports a deterministically
			// bad/unsupported document.
			return true
		}
	}
	for _, sig := range terminalChromiumSignatures {
		if strings.Contains(msg, sig) {
			// D-01 (html analog): chromium-headless-shell reports a
			// deterministically bad/unrenderable input.
			return true
		}
	}
	for _, sig := range terminalVeraPDFSignatures {
		if strings.Contains(msg, sig) {
			// D-06 (phase 23): veraPDF reports a non-compliant or
			// unverifiable PDF/A archival claim -- fail-closed, no retry.
			return true
		}
	}
	return false
}

// timeoutIsTerminal is the shared body behind the timeout-aware engine
// classifiers (isDocumentTerminal, isHTMLTerminal). It encodes the single
// divergence those engines share with the image path: nil is never terminal;
// a wrapped context.DeadlineExceeded (an engine-timeout expiry) IS terminal —
// a stuck soffice/chromium render is no more likely to succeed on retry, so
// the only reliable backstop is an immediate terminal failure (MarkFailed +
// SkipRetry, no asynq retry) rather than burning the whole retry budget;
// every non-timeout error falls through to the shared isTerminal classifier.
// Extracting this keeps the two engine-scoped entrypoints from drifting apart
// on a future edit (IN-01) while preserving their distinct doc comments.
func timeoutIsTerminal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return isTerminal(err)
}

// isDocumentTerminal is the document engine's engine-scoped terminal
// classifier — DOC-08's deliberate divergence from the image engine. Unlike
// isTerminal (which keeps treating a timeout as transient so the image path
// retries it), a DOCUMENT_ENGINE_TIMEOUT expiry IS classified terminal here:
// LibreOffice's documented hang-on-bad-input behavior means a retry cannot
// help, so an immediate terminal failure (MarkFailed + SkipRetry, no asynq
// retry) is the only reliable backstop. Without this, a stuck soffice would
// be retried up to DOCUMENT_MAX_RETRY times, each burning up to
// DOCUMENT_ENGINE_TIMEOUT (300s default) before finally being dropped.
//
// A DOCUMENT_ENGINE_TIMEOUT expiry surfaces as a wrapped context.
// DeadlineExceeded: exec.go's process-group kill produces
// fmt.Errorf("%s killed: %w", name, ctx.Err()), preserved through
// libreoffice.go's fmt.Errorf("libreoffice: %w", err) and process()'s
// fmt.Errorf("convert: %w", err) — so errors.Is(err, context.DeadlineExceeded)
// stays true all the way up to this handler.
//
// Every non-timeout error is classified identically to the image path by
// delegating (via timeoutIsTerminal) to isTerminal — no document-specific
// duplication of the no-converter/minio.NoSuchKey/LibreOffice-signature checks.
func isDocumentTerminal(err error) bool {
	return timeoutIsTerminal(err)
}

// isHTMLTerminal is the html engine's engine-scoped terminal classifier —
// HTML-01's deliberate divergence from the image engine, mirroring
// isDocumentTerminal exactly (DOC-08's timeout-is-terminal pattern applied
// to the chromium engine). An HTML_ENGINE_TIMEOUT expiry surfaces as a
// wrapped context.DeadlineExceeded (same exec.go process-group-kill shape,
// preserved through chromium.go's fmt.Errorf("chromium: %w", err) and
// process()'s fmt.Errorf("convert: %w", err)) and is classified terminal
// here rather than retried: a stuck chromium render is no more likely to
// succeed on retry than a stuck soffice render. Every non-timeout error is
// classified identically to the image/document paths by delegating (via
// timeoutIsTerminal) to the shared isTerminal (which now also checks
// terminalChromiumSignatures).
func isHTMLTerminal(err error) bool {
	return timeoutIsTerminal(err)
}

// isAudioTerminal is the audio engine's engine-scoped terminal classifier —
// Key Decision 1 (STATE.md, BINDING): a stage-aware split, deliberately NOT
// a copy of timeoutIsTerminal's blanket "any wrapped context.DeadlineExceeded
// is terminal" shape (the superseded interpretation FEATURES.md/
// ARCHITECTURE.md recommended). A ffmpeg-stage failure OR timeout (the
// "audio: ffmpeg:" prefix emitted by whisper.go's stage 1, normalize) is
// classified terminal — a malformed/adversarial input signal, mirroring the
// image engine's dimension-bomb terminal philosophy (T-31-08); a
// whisper-stage timeout on audio that already passed the ffprobe
// duration/format check (the "audio: whisper-cli:" prefix, stage 2) stays
// transient, bounded by AUDIO_MAX_RETRY (T-31-07) — mirroring the image
// engine's own timeout-stays-transient behavior, NOT
// isDocumentTerminal's/isHTMLTerminal's timeoutIsTerminal delegation.
// ErrAudioDurationExceeded (the EnforceMaxDuration guard spliced into
// process(), T-30-08/IN-02) always classifies terminal — a declared-duration
// rejection can never succeed on retry. Deterministic ffprobe-stage failures
// (the duration guard's own probe: a non-zero ffprobe exit on broken
// container metadata, or an unparseable/implausible reported duration) are
// likewise terminal (WR-01) — the probe runs BEFORE ffmpeg on the same
// untrusted input, so a parse-level probe failure is the same
// malformed/adversarial-input signal Key Decision 1 already classifies
// terminal at the ffmpeg stage; ffprobe environment/timeout shapes ("start
// ffprobe:", "ffprobe killed:") deliberately stay transient. Every other
// error (S3/Postgres blips, no-converter, non-timeout whisper-cli failures,
// ffprobe environment/timeout shapes) falls through to
// the shared isTerminal classifier — per 31-RESEARCH.md A2 (Claude's
// Discretion, adopted): the input to whisper-cli is a server-produced
// normalized WAV, so a non-timeout whisper-cli failure is most plausibly
// environment/config, not malformed client input. One deliberate exception
// to that transient default (WR-03): validateAudioOutput's exit-0-but-no-
// output shapes ("audio: output is empty", "audio: stat output") ARE
// terminal, via the dedicated terminalAudioSignatures list checked here —
// previously they classified terminal only by accidentally matching the
// LibreOffice/Chromium signature lists inside isTerminal; the explicit list
// makes that outcome self-contained and pinned.
//
// Deliberately does NOT call timeoutIsTerminal.
func isAudioTerminal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, convert.ErrAudioDurationExceeded) {
		// T-30-08/IN-02: a rejected declared duration can never succeed on
		// retry.
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "audio: ffmpeg:") {
		// Key Decision 1: ffmpeg-stage failure OR timeout is a
		// malformed/adversarial input signal — terminal, no retry.
		return true
	}
	if strings.Contains(msg, "ffprobe: unparseable duration") ||
		strings.Contains(msg, "ffprobe: implausible duration") ||
		strings.Contains(msg, "ffprobe failed:") {
		// WR-01: duration-guard (ffprobe) stage. The probe runs BEFORE ffmpeg
		// on the same untrusted input, so a deterministic probe-level failure
		// (non-zero exit on malformed container metadata — exec.go's
		// "ffprobe failed:" shape — or audioduration.go's
		// unparseable/implausible duration rejections) is the same
		// malformed/adversarial-input signal Key Decision 1 already
		// classifies terminal at the ffmpeg stage: a corrupt-but-sniffable
		// file must fail terminal at the very first probe instead of burning
		// the AUDIO_MAX_RETRY + reconciler budget. Environment/timeout shapes
		// are deliberately NOT matched here and stay transient: "start
		// ffprobe:" (binary missing/unrunnable — a deployment problem, not an
		// input problem) and "ffprobe killed:" (probe-ctx expiry under
		// enforceAudioGuardBeforeConvert's short probe bound — a hung probe
		// on a loaded host may succeed on retry).
		return true
	}
	for _, sig := range terminalAudioSignatures {
		if strings.Contains(msg, sig) {
			// WR-03: whisper-cli's exit-0-but-empty/missing-output failure
			// mode (validateAudioOutput) is deterministic — terminal via
			// audio's OWN signature list, independent of the
			// LibreOffice/Chromium lists these strings previously (and
			// accidentally) matched inside the shared isTerminal loop.
			return true
		}
	}
	return isTerminal(err)
}

// isAVTerminal is the av engine's engine-scoped terminal classifier — D-02
// (35-CONTEXT.md, BINDING): a stage-aware split derived FRESH for video, not
// a copy of isAudioTerminal's blanket "any 'audio: ffmpeg:' failure or
// timeout is terminal" rule. That rule is correct for audio because ffmpeg
// is audio's CHEAP normalize stage — a timeout there is a strong
// malformed-input signal. It would be WRONG for av: av's transcode stage
// (ErrAVTranscodeFailed) is the EXPENSIVE operation this whole class
// exists to run, so a timeout there may simply mean the retry budget ran
// out under load, not that the input is malformed — porting audio's rule
// verbatim would make every transcode timeout terminal and silently
// destroy the transient-retry behavior this classifier exists to provide.
// ErrAudioDurationExceeded is reused here on purpose (not a bug): AV's
// duration guard calls the same enforceMaxDurationOf helper audio uses
// (av.go:395), so the same sentinel — and the same always-terminal
// treatment — keeps the two engines' client-facing contracts symmetric.
func isAVTerminal(err error) bool {
	if err == nil {
		return false
	}
	// Deterministic guard/output-validation rejections: always terminal,
	// regardless of which stage produced them — a rejected declared
	// resolution, an out-of-range timecode, a missing/empty output, or a
	// missing video stream can never succeed on retry.
	if errors.Is(err, convert.ErrAVOutputMissingOrEmpty) ||
		errors.Is(err, convert.ErrAVTimecodeOutOfRange) ||
		errors.Is(err, convert.ErrAVResolutionExceeded) ||
		errors.Is(err, convert.ErrAudioDurationExceeded) ||
		errors.Is(err, convert.ErrAVNoVideoStream) {
		return true
	}
	isTimeout := errors.Is(err, context.DeadlineExceeded)
	switch {
	case errors.Is(err, convert.ErrAVTranscodeFailed):
		// D-02: transcode is the expensive stage — a timeout stays
		// TRANSIENT (this is the single assertion that distinguishes this
		// classifier from isAudioTerminal); a non-timeout ffmpeg failure
		// (malformed/adversarial input) stays terminal.
		return !isTimeout
	case errors.Is(err, convert.ErrAVAudioExtractFailed), errors.Is(err, convert.ErrAVThumbnailFailed):
		// D-02: audio-extract and thumbnail are the cheap stages — ANY
		// failure (timeout or not) is TERMINAL, mirroring audio's own
		// cheap-stage rule.
		return true
	}
	// No substring-based fallback here: after Plan 01's D-01 sentinel
	// refactor, every av ffmpeg stage has a typed sentinel, so matching on
	// error text would only reintroduce the exact fragility D-01 removed.
	return isTerminal(err) // no-converter / minio.NoSuchKey / shared fallthrough
}

// Handler processes image conversion tasks end to end.
type Handler struct {
	repo          *jobs.Repo
	store         *storage.Client
	registry      *convert.Registry
	engineTimout  time.Duration
	webhookRepo   *webhook.Repo
	deliverer     *webhook.Deliverer
	enqueuer      *queue.Client
	signingSecret []byte
	presignTTL    time.Duration
	// audioMaxDuration is the AUDIO_MAX_DURATION_SECONDS ceiling passed to
	// convert.EnforceMaxDuration for EngineAudio jobs inside process()
	// (T-30-08/IN-02). Zero (the value every non-audio worker cmd passes) is
	// inert — process() only invokes the guard when job.Engine ==
	// convert.EngineAudio, so image/document/html/webhook workers never read
	// this field. The real value is wired by the audio worker cmd (Plan 04).
	audioMaxDuration time.Duration
}

// NewHandler builds a worker handler.
func NewHandler(repo *jobs.Repo, store *storage.Client, registry *convert.Registry, engineTimeout time.Duration, webhookRepo *webhook.Repo, deliverer *webhook.Deliverer, enqueuer *queue.Client, signingSecret []byte, presignTTL time.Duration, audioMaxDuration time.Duration) *Handler {
	if engineTimeout == 0 {
		engineTimeout = 120 * time.Second
	}
	if presignTTL == 0 {
		// D-09: comfortably exceed the ~30 min retry window so a recovering
		// or dead-lettered client can still fetch the result.
		presignTTL = 6 * time.Hour
	}
	return &Handler{
		repo:             repo,
		store:            store,
		registry:         registry,
		engineTimout:     engineTimeout,
		webhookRepo:      webhookRepo,
		deliverer:        deliverer,
		enqueuer:         enqueuer,
		signingSecret:    signingSecret,
		presignTTL:       presignTTL,
		audioMaxDuration: audioMaxDuration,
	}
}

// HandleImageConvert runs one image conversion: load job -> mark active ->
// download input -> run engine -> upload output -> record output -> mark done.
// A terminal error (bad input, unsupported pair, missing storage object) marks
// the job failed immediately and skips asynq's retry (D-01/D-02); a transient
// error (network, S3/engine timeout, Postgres write blip) is returned
// unwrapped so the job stays active and asynq retries the same task
// (D-01/D-03/D-04) — mirroring HandleWebhookDeliver's classification pattern.
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

	start := time.Now()
	if err := h.process(ctx, job); err != nil {
		if isTerminal(err) {
			// Sanitized message only in error_message (exposed via GET
			// /jobs/{id} and webhook payloads, T-03-03); the raw stderr
			// (which contains local temp paths) is kept in job_events.detail
			// for internal diagnostics only.
			ferr := h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", map[string]any{"engine_stderr": err.Error()})
			metrics.RecordJobOutcome(queue.QueueImage, jobs.StatusFailed, time.Since(start))
			// Postgres-first: enqueue the webhook ONLY once the failed status
			// is actually committed. If MarkFailed itself failed (Postgres
			// blip), the job is still active — a webhook now would re-read the
			// row and deliver the non-terminal status "active", which the
			// contract never defines (WR-04); the reconciler will requeue the
			// still-active job instead. A failed enqueue after a successful
			// MarkFailed must not fail the conversion — best-effort only.
			if ferr == nil && job.CallbackURL != "" {
				_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
			}
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		// Transient: do NOT mark failed — the job stays active so asynq's own
		// retry/backoff (ImageRetryDelay/IMAGE_MAX_RETRY, Plan 01) applies.
		// Not a terminal outcome, so it is NOT recorded in the job-outcome
		// metric (Pitfall 6 — one asynq retry must not double-count).
		return err
	}
	metrics.RecordJobOutcome(queue.QueueImage, jobs.StatusDone, time.Since(start))
	// Postgres-first: MarkDone already committed inside process(), so a
	// failed enqueue must not fail the conversion — best-effort only.
	if job.CallbackURL != "" {
		_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
	}
	return nil
}

// HandleDocumentConvert runs one document conversion: load job -> mark
// active -> download input -> run LibreOffice (via the engine-agnostic
// process(), which reuses registry.Lookup — the LibreOfficeConverter
// registered in convert.Default handles the actual conversion) -> upload
// output -> record output -> mark done. Structurally identical to
// HandleImageConvert with exactly two differences (DOC-07/DOC-08): (1) the
// terminal-classification branch calls isDocumentTerminal instead of
// isTerminal, so a DOCUMENT_ENGINE_TIMEOUT expiry takes the terminal path
// (MarkFailed + SkipRetry, no asynq retry) — a deliberate divergence from
// the image engine, which treats the same timeout as transient; (2) both
// metrics.RecordJobOutcome calls are tagged queue.QueueDocument. Non-timeout
// transient failures (S3/Postgres blips) still fall through
// isDocumentTerminal -> isTerminal -> false and are returned unwrapped so
// asynq retries them, bounded by DOCUMENT_MAX_RETRY.
func (h *Handler) HandleDocumentConvert(ctx context.Context, t *asynq.Task) error {
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

	// Strict re-parse of the persisted opts (D-10): garbage in jobs.options
	// is a terminal, not transient, failure -- a corrupt column value can
	// never fix itself on retry. This is a strictness check only; the
	// applicability/enum business rules already ran once, at the API layer
	// (single source of validation truth), and are deliberately not
	// duplicated here.
	if _, err := convert.DocOptsFromMap(job.Opts); err != nil {
		// Mark failed BEFORE SkipRetry: without this the job would stay
		// queued forever (no webhook, client polls a dead job) while the
		// reconciler requeues it into the same failure until MaxRecoveries
		// is exhausted (T-14-02b). Sanitized message only; the parse error
		// is kept in job_events.detail for internal diagnostics.
		ferr := h.repo.MarkFailed(ctx, jobID, "invalid_options", "stored conversion options are invalid", map[string]any{"opts_error": err.Error()})
		// Postgres-first: enqueue the webhook ONLY once the failed status is
		// actually committed — if MarkFailed itself failed, a webhook now
		// would report the non-terminal status "queued" (WR-04). A failed
		// enqueue after a successful MarkFailed must not fail the job —
		// best-effort only.
		if ferr == nil && job.CallbackURL != "" {
			_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
		}
		return fmt.Errorf("%w: opts: %v", asynq.SkipRetry, err)
	}

	if err := h.repo.MarkActive(ctx, jobID); err != nil {
		// Already active/done/canceled — let asynq drop it rather than loop.
		return fmt.Errorf("%w: mark active: %v", asynq.SkipRetry, err)
	}

	start := time.Now()
	if err := h.process(ctx, job); err != nil {
		if isDocumentTerminal(err) {
			// Sanitized message only in error_message (exposed via GET
			// /jobs/{id} and webhook payloads, T-03-03); the raw stderr
			// (which contains local temp paths) is kept in job_events.detail
			// for internal diagnostics only.
			ferr := h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", map[string]any{"engine_stderr": err.Error()})
			metrics.RecordJobOutcome(queue.QueueDocument, jobs.StatusFailed, time.Since(start))
			// Postgres-first: enqueue the webhook ONLY once the failed status
			// is actually committed. If MarkFailed itself failed (Postgres
			// blip), the job is still active — a webhook now would re-read the
			// row and deliver the non-terminal status "active", which the
			// contract never defines (WR-04); the reconciler will requeue the
			// still-active job instead. A failed enqueue after a successful
			// MarkFailed must not fail the conversion — best-effort only.
			if ferr == nil && job.CallbackURL != "" {
				_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
			}
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		// Transient: do NOT mark failed — the job stays active so asynq's own
		// retry/backoff (DocumentRetryDelay/DOCUMENT_MAX_RETRY, Plan 01) applies.
		// Not a terminal outcome, so it is NOT recorded in the job-outcome
		// metric (Pitfall 6 — one asynq retry must not double-count).
		return err
	}
	metrics.RecordJobOutcome(queue.QueueDocument, jobs.StatusDone, time.Since(start))
	// Postgres-first: MarkDone already committed inside process(), so a
	// failed enqueue must not fail the conversion — best-effort only.
	if job.CallbackURL != "" {
		_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
	}
	return nil
}

// HandleHTMLConvert runs one html->pdf conversion: load job -> mark active ->
// download input -> run chromium-headless-shell (via the engine-agnostic
// process(), which reuses registry.Lookup — the ChromiumConverter registered
// in convert.Default handles the actual render) -> upload output -> record
// output -> mark done. Structurally identical to HandleDocumentConvert, with
// the same two divergences from the image path (HTML-01): (1) the
// terminal-classification branch calls isHTMLTerminal instead of isTerminal,
// so an HTML_ENGINE_TIMEOUT expiry takes the terminal path (MarkFailed +
// SkipRetry, no asynq retry); (2) both metrics.RecordJobOutcome calls are
// tagged queue.QueueHTML. Non-timeout transient failures (S3/Postgres blips)
// still fall through isHTMLTerminal -> isTerminal -> false and are returned
// unwrapped so asynq retries them, bounded by HTML_MAX_RETRY.
func (h *Handler) HandleHTMLConvert(ctx context.Context, t *asynq.Task) error {
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

	// Strict re-parse of the persisted opts (D-10): garbage in jobs.options
	// is a terminal, not transient, failure -- a corrupt column value can
	// never fix itself on retry. This is a strictness check only; the
	// applicability/enum business rules already ran once, at the API layer
	// (single source of validation truth), and are deliberately not
	// duplicated here.
	if _, err := convert.HTMLOptsFromMap(job.Opts); err != nil {
		// Mark failed BEFORE SkipRetry: without this the job would stay
		// queued forever (no webhook, client polls a dead job) while the
		// reconciler requeues it into the same failure until MaxRecoveries
		// is exhausted (T-14-02b). Sanitized message only; the parse error
		// is kept in job_events.detail for internal diagnostics.
		ferr := h.repo.MarkFailed(ctx, jobID, "invalid_options", "stored conversion options are invalid", map[string]any{"opts_error": err.Error()})
		// Postgres-first: enqueue the webhook ONLY once the failed status is
		// actually committed — if MarkFailed itself failed, a webhook now
		// would report the non-terminal status "queued" (WR-04). A failed
		// enqueue after a successful MarkFailed must not fail the job —
		// best-effort only.
		if ferr == nil && job.CallbackURL != "" {
			_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
		}
		return fmt.Errorf("%w: opts: %v", asynq.SkipRetry, err)
	}

	if err := h.repo.MarkActive(ctx, jobID); err != nil {
		// Already active/done/canceled — let asynq drop it rather than loop.
		return fmt.Errorf("%w: mark active: %v", asynq.SkipRetry, err)
	}

	start := time.Now()
	if err := h.process(ctx, job); err != nil {
		if isHTMLTerminal(err) {
			// Sanitized message only in error_message (exposed via GET
			// /jobs/{id} and webhook payloads, T-03-03); the raw stderr
			// (which contains local temp paths) is kept in job_events.detail
			// for internal diagnostics only.
			ferr := h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", map[string]any{"engine_stderr": err.Error()})
			metrics.RecordJobOutcome(queue.QueueHTML, jobs.StatusFailed, time.Since(start))
			// Postgres-first: enqueue the webhook ONLY once the failed status
			// is actually committed. If MarkFailed itself failed (Postgres
			// blip), the job is still active — a webhook now would re-read the
			// row and deliver the non-terminal status "active", which the
			// contract never defines (WR-04); the reconciler will requeue the
			// still-active job instead. A failed enqueue after a successful
			// MarkFailed must not fail the conversion — best-effort only.
			if ferr == nil && job.CallbackURL != "" {
				_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
			}
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		// Transient: do NOT mark failed — the job stays active so asynq's own
		// retry/backoff (HTMLRetryDelay/HTML_MAX_RETRY, Plan 01) applies.
		// Not a terminal outcome, so it is NOT recorded in the job-outcome
		// metric (Pitfall 6 — one asynq retry must not double-count).
		return err
	}
	metrics.RecordJobOutcome(queue.QueueHTML, jobs.StatusDone, time.Since(start))
	// Postgres-first: MarkDone already committed inside process(), so a
	// failed enqueue must not fail the conversion — best-effort only.
	if job.CallbackURL != "" {
		_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
	}
	return nil
}

// HandleAudioConvert runs one audio transcription: load job -> mark active ->
// download input -> run the ffmpeg-normalize/whisper-cli-transcribe pipeline
// (via the engine-agnostic process(), which reuses registry.Lookup — the
// AudioConverter registered in convert.Default handles the actual
// transcription; process() also enforces the EnforceMaxDuration guard for
// EngineAudio jobs, T-30-08/IN-02) -> upload output -> record output -> mark
// done. Structurally mirrors HandleDocumentConvert/HandleHTMLConvert with
// three differences (AUD-05): (1) the terminal-classification branch calls
// isAudioTerminal instead of isDocumentTerminal/isHTMLTerminal — Key
// Decision 1's stage-aware classifier, NOT a blanket
// timeout-is-terminal copy; (2) a duration-guard rejection
// (errors.Is(err, convert.ErrAudioDurationExceeded)) gets its own distinct
// client-facing error_code "duration_exceeded" rather than the generic
// "engine_error" (31-RESEARCH.md A3) — a declared-duration rejection is a
// distinguishable, actionable failure mode for the client, not a generic
// engine failure; (3) both metrics.RecordJobOutcome calls are tagged
// queue.QueueAudio.
func (h *Handler) HandleAudioConvert(ctx context.Context, t *asynq.Task) error {
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

	// Strict re-parse of the persisted opts (D-10): garbage in jobs.options
	// is a terminal, not transient, failure -- a corrupt column value can
	// never fix itself on retry. This is a strictness check only; the
	// applicability/enum business rules already ran once, at the API layer
	// (single source of validation truth), and are deliberately not
	// duplicated here.
	if _, err := convert.AudioOptsFromMap(job.Opts); err != nil {
		// Mark failed BEFORE SkipRetry: without this the job would stay
		// queued forever (no webhook, client polls a dead job) while the
		// reconciler requeues it into the same failure until MaxRecoveries
		// is exhausted (T-14-02b). Sanitized message only; the parse error
		// is kept in job_events.detail for internal diagnostics.
		ferr := h.repo.MarkFailed(ctx, jobID, "invalid_options", "stored conversion options are invalid", map[string]any{"opts_error": err.Error()})
		// Postgres-first: enqueue the webhook ONLY once the failed status is
		// actually committed — if MarkFailed itself failed, a webhook now
		// would report the non-terminal status "queued" (WR-04). A failed
		// enqueue after a successful MarkFailed must not fail the job —
		// best-effort only.
		if ferr == nil && job.CallbackURL != "" {
			_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
		}
		return fmt.Errorf("%w: opts: %v", asynq.SkipRetry, err)
	}

	if err := h.repo.MarkActive(ctx, jobID); err != nil {
		// Already active/done/canceled — let asynq drop it rather than loop.
		return fmt.Errorf("%w: mark active: %v", asynq.SkipRetry, err)
	}

	start := time.Now()
	if err := h.process(ctx, job); err != nil {
		if isAudioTerminal(err) {
			// Sanitized message only in error_message (exposed via GET
			// /jobs/{id} and webhook payloads, T-03-03); the raw stderr
			// (which contains local temp paths) is kept in job_events.detail
			// for internal diagnostics only.
			//
			// A duration-guard rejection gets a distinct client-facing code
			// (31-RESEARCH.md A3) — it must NOT reuse the generic
			// "engine_error" path: "declared duration exceeds the configured
			// maximum" is a distinguishable, actionable failure the client
			// can react to differently than a generic corrupted-input
			// failure.
			var ferr error
			if errors.Is(err, convert.ErrAudioDurationExceeded) {
				ferr = h.repo.MarkFailed(ctx, jobID, "duration_exceeded", "declared audio duration exceeds the configured maximum", map[string]any{"engine_stderr": err.Error()})
			} else {
				ferr = h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", map[string]any{"engine_stderr": err.Error()})
			}
			metrics.RecordJobOutcome(queue.QueueAudio, jobs.StatusFailed, time.Since(start))
			// Postgres-first: enqueue the webhook ONLY once the failed status
			// is actually committed. If MarkFailed itself failed (Postgres
			// blip), the job is still active — a webhook now would re-read the
			// row and deliver the non-terminal status "active", which the
			// contract never defines (WR-04); the reconciler will requeue the
			// still-active job instead. A failed enqueue after a successful
			// MarkFailed must not fail the conversion — best-effort only.
			if ferr == nil && job.CallbackURL != "" {
				_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
			}
			return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
		}
		// Transient: do NOT mark failed — the job stays active so asynq's own
		// retry/backoff (AudioRetryDelay/AUDIO_MAX_RETRY, Plan 01) applies.
		// Not a terminal outcome, so it is NOT recorded in the job-outcome
		// metric (Pitfall 6 — one asynq retry must not double-count).
		return err
	}
	metrics.RecordJobOutcome(queue.QueueAudio, jobs.StatusDone, time.Since(start))
	// Postgres-first: MarkDone already committed inside process(), so a
	// failed enqueue must not fail the conversion — best-effort only.
	if job.CallbackURL != "" {
		_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
	}
	return nil
}

// HandleWebhookDeliver delivers one webhook attempt for a completed job:
// re-read the job from Postgres, regenerate a fresh presigned download_url
// per attempt (done only, D-09), sign, deliver, record the attempt, and
// dead-letter on final-attempt exhaustion (D-10). Delivery failures are
// returned unwrapped so asynq applies its own retry/backoff (D-05); only
// unparseable payloads and jobs with no callback_url are terminal.
func (h *Handler) HandleWebhookDeliver(ctx context.Context, t *asynq.Task) error {
	payload, err := queue.ParseWebhookPayload(t.Payload())
	if err != nil {
		// Unparseable payload: nothing we can retry into success.
		return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
	}
	jobID := payload.JobID

	job, err := h.repo.Get(ctx, jobID)
	if err != nil {
		return fmt.Errorf("load job %s: %w", jobID, err)
	}

	if job.CallbackURL == "" {
		// Nothing to deliver to — terminal, not a transient failure.
		return fmt.Errorf("%w: no callback_url", asynq.SkipRetry)
	}

	body := map[string]any{
		"job_id": job.ID,
		"status": job.Status,
	}
	if job.Status == jobs.StatusDone {
		outs, err := h.repo.Outputs(ctx, job.ID)
		if err != nil {
			return fmt.Errorf("load outputs for job %s: %w", jobID, err)
		}
		if len(outs) == 0 {
			return fmt.Errorf("job %s has no outputs", jobID)
		}
		// D-09: regenerate a fresh presigned URL on every attempt, never
		// reuse a stale one across retries.
		url, err := h.store.PresignGet(ctx, outs[0].ObjectKey, h.presignTTL)
		if err != nil {
			return fmt.Errorf("presign output for job %s: %w", jobID, err)
		}
		body["download_url"] = url
	}
	if job.Status == jobs.StatusFailed {
		if job.ErrorCode != "" {
			body["error_code"] = job.ErrorCode
		}
		if job.ErrorMessage != "" {
			body["error_message"] = job.ErrorMessage
		}
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal webhook body for job %s: %w", jobID, err)
	}

	ts := time.Now().Unix()
	sig := webhook.SignPayload(h.signingSecret, ts, bodyBytes)

	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	attempt := retryCount + 1

	code, derr := h.deliverer.Deliver(ctx, job.CallbackURL, bodyBytes, ts, sig)
	metrics.RecordWebhookDelivery(derr == nil)

	var statusCodePtr *int
	if code > 0 {
		statusCodePtr = &code
	}
	deliveryID, recErr := h.webhookRepo.RecordAttempt(ctx, jobID, job.CallbackURL, attempt, statusCodePtr, derr == nil)

	if derr != nil {
		if recErr == nil && retryCount >= maxRetry {
			// Final attempt exhausted: flag for investigation (D-10).
			_ = h.webhookRepo.MarkDeadLetter(ctx, deliveryID)
		}
		// Unwrapped: let asynq's own retry policy + backoff (D-05) apply.
		return derr
	}
	return nil
}

// audioProbeTimeout is the SHORT bound the duration guard's ffprobe
// invocation runs under (WR-02/T-30-03): ProbeDuration's contract is explicit
// that its ctx must never carry the full AUDIO_ENGINE_TIMEOUT budget —
// ffprobe reads container metadata and is near-instant even for large files,
// while a hung/adversarial probe (the guard's own threat model: it runs on
// untrusted input BEFORE any decode) would otherwise consume the entire
// whole-attempt deadline on every retry. A probe-ctx expiry surfaces as
// "ffprobe: ffprobe killed: context deadline exceeded", which isAudioTerminal
// deliberately classifies TRANSIENT (WR-01's documented split — a hung probe
// on a loaded host may succeed on retry), so this bound converts a
// full-budget hang into a fast, retryable failure.
const audioProbeTimeout = 15 * time.Second

// enforceAudioGuardBeforeConvert splices the T-30-08/IN-02 declared-duration
// guard in front of the engine's Convert call, gated on engine ==
// convert.EngineAudio (image/document/html jobs never pay the ffprobe cost
// and audioMaxDuration, 0 for every non-audio worker cmd, is never
// consulted for them). Factored out of process() as its own package-level
// function so the guard-runs-strictly-before-Convert ordering is
// unit-testable (the IN-02 pinning test) without a live-Postgres/S3
// Handler — process()'s h.repo/h.store are concrete types, not interfaces
// (ARCHITECTURE.md's "Key Abstractions" note), so a full Handler cannot be
// constructed in a pure unit test. A rejection here surfaces as
// ErrAudioDurationExceeded, unwrapped, which isAudioTerminal classifies
// terminal.
func enforceAudioGuardBeforeConvert(ctx context.Context, engine, inPath string, audioMaxDuration time.Duration, convertFn func() error) error {
	if engine == convert.EngineAudio {
		// WR-02/T-30-03: derive a short probe-only deadline from the
		// whole-attempt ctx rather than passing it through — ProbeDuration
		// must never run under the full AUDIO_ENGINE_TIMEOUT budget.
		probeCtx, cancel := context.WithTimeout(ctx, audioProbeTimeout)
		err := convert.EnforceMaxDuration(probeCtx, inPath, audioMaxDuration)
		cancel()
		if err != nil {
			return err
		}
	}
	return convertFn()
}

func (h *Handler) process(ctx context.Context, job *jobs.Job) error {
	// The ENTIRE attempt (input lookup, download, convert, upload, record) is
	// bounded by a single whole-attempt deadline, not just conv.Convert(): the
	// minio client sets no transport read-deadline, so a stalled (hung, not
	// refused) S3 transfer would otherwise have NO upper bound. Plan 01's
	// derived ImageUniqueTTL assumes every attempt is <= ENGINE_TIMEOUT and
	// asynq never refreshes the per-job unique lock on internal same-task
	// retries — a single attempt that outlives that TTL would let the lock
	// lapse in Redis while this handler is still running, letting the
	// reconciler's next EnqueueImageConvert create a second concurrent task
	// for the same job (the T-03-10 double-processing race). Threading one
	// attemptCtx through every step closes that gap (T-03-11).
	attemptCtx, cancel := context.WithTimeout(ctx, h.engineTimout)
	defer cancel()

	inputs, err := h.inputKey(attemptCtx, job.ID)
	if err != nil {
		return err
	}

	conv, ok := h.registry.Lookup(job.SourceFormat, job.TargetFormat)
	if !ok {
		return fmt.Errorf("no converter for %s -> %s", job.SourceFormat, job.TargetFormat)
	}

	workDir, err := os.MkdirTemp("", "octoconv-"+job.ID.String()+"-")
	if err != nil {
		return fmt.Errorf("mkdtemp: %w", err)
	}
	defer os.RemoveAll(workDir)

	inPath := filepath.Join(workDir, "in."+job.SourceFormat)
	outName := "out." + job.TargetFormat
	outPath := filepath.Join(workDir, outName)

	if err := h.downloadTo(attemptCtx, inputs.ObjectKey, inPath); err != nil {
		return err
	}

	// T-30-08/IN-02: the declared-duration guard MUST actually run, not just
	// be documented — closes the gap where EnforceMaxDuration existed
	// (Phase 30) but no caller ever invoked it. Runs AFTER download (there
	// is nothing to probe before the file exists locally) and strictly
	// BEFORE conv.Convert (the most expensive step) via
	// enforceAudioGuardBeforeConvert, factored out as its own package-level
	// function so the ordering is unit-testable without a live Postgres/S3
	// Handler.
	if err := enforceAudioGuardBeforeConvert(attemptCtx, job.Engine, inPath, h.audioMaxDuration, func() error {
		if err := conv.Convert(attemptCtx, inPath, outPath, job.Opts); err != nil {
			return fmt.Errorf("convert: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	outKey := storage.OutputKey(job.ID, 0, outName)
	size, err := h.uploadFrom(attemptCtx, outKey, outPath, convert.MIMEType(job.TargetFormat))
	if err != nil {
		return err
	}

	if err := h.repo.AddOutput(attemptCtx, job.ID, jobs.Output{
		Ordinal:     0,
		ObjectKey:   outKey,
		Filename:    outName,
		Format:      job.TargetFormat,
		SizeBytes:   size,
		ContentType: convert.MIMEType(job.TargetFormat),
	}); err != nil {
		return err
	}

	return h.repo.MarkDone(attemptCtx, job.ID)
}

func (h *Handler) inputKey(ctx context.Context, jobID uuid.UUID) (jobs.Input, error) {
	ins, err := h.repo.Inputs(ctx, jobID)
	if err != nil {
		return jobs.Input{}, err
	}
	if len(ins) == 0 {
		return jobs.Input{}, fmt.Errorf("job %s has no inputs", jobID)
	}
	return ins[0], nil
}

func (h *Handler) downloadTo(ctx context.Context, key, path string) error {
	rc, err := h.store.Download(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("write input: %w", err)
	}
	return nil
}

func (h *Handler) uploadFrom(ctx context.Context, key, path, contentType string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open output: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat output: %w", err)
	}
	if err := h.store.Upload(ctx, key, f, fi.Size(), contentType); err != nil {
		return 0, err
	}
	return fi.Size(), nil
}
