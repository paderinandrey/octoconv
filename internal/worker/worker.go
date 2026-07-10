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
	return false
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
// delegating to isTerminal — no document-specific duplication of the
// no-converter/minio.NoSuchKey/LibreOffice-signature checks.
func isDocumentTerminal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	return isTerminal(err)
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
}

// NewHandler builds a worker handler.
func NewHandler(repo *jobs.Repo, store *storage.Client, registry *convert.Registry, engineTimeout time.Duration, webhookRepo *webhook.Repo, deliverer *webhook.Deliverer, enqueuer *queue.Client, signingSecret []byte, presignTTL time.Duration) *Handler {
	if engineTimeout == 0 {
		engineTimeout = 120 * time.Second
	}
	if presignTTL == 0 {
		// D-09: comfortably exceed the ~30 min retry window so a recovering
		// or dead-lettered client can still fetch the result.
		presignTTL = 6 * time.Hour
	}
	return &Handler{
		repo:          repo,
		store:         store,
		registry:      registry,
		engineTimout:  engineTimeout,
		webhookRepo:   webhookRepo,
		deliverer:     deliverer,
		enqueuer:      enqueuer,
		signingSecret: signingSecret,
		presignTTL:    presignTTL,
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
			_ = h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", map[string]any{"engine_stderr": err.Error()})
			metrics.RecordJobOutcome(queue.QueueImage, jobs.StatusFailed, time.Since(start))
			// Postgres-first: the failed status is already committed above, so a
			// failed enqueue must not fail the conversion — best-effort only.
			if job.CallbackURL != "" {
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
			_ = h.repo.MarkFailed(ctx, jobID, "engine_error", "unsupported or corrupted input format", map[string]any{"engine_stderr": err.Error()})
			metrics.RecordJobOutcome(queue.QueueDocument, jobs.StatusFailed, time.Since(start))
			// Postgres-first: the failed status is already committed above, so a
			// failed enqueue must not fail the conversion — best-effort only.
			if job.CallbackURL != "" {
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

	if err := conv.Convert(attemptCtx, inPath, outPath, nil); err != nil {
		return fmt.Errorf("convert: %w", err)
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
