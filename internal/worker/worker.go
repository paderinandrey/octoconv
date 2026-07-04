// Package worker contains the asynq task handlers that run conversion engines.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/storage"
	"github.com/apaderin/octoconv/internal/webhook"
)

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
// Errors mark the job failed and are returned so asynq applies its retry policy.
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
		// Postgres-first: the failed status is already committed above, so a
		// failed enqueue must not fail the conversion — best-effort only.
		if job.CallbackURL != "" {
			_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)
		}
		return err
	}
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
	inputs, err := h.inputKey(ctx, job.ID)
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

	if err := h.downloadTo(ctx, inputs.ObjectKey, inPath); err != nil {
		return err
	}

	// Bound the engine run with the configured timeout.
	engineCtx, cancel := context.WithTimeout(ctx, h.engineTimout)
	defer cancel()
	if err := conv.Convert(engineCtx, inPath, outPath, nil); err != nil {
		return fmt.Errorf("convert: %w", err)
	}

	outKey := storage.OutputKey(job.ID, 0, outName)
	size, err := h.uploadFrom(ctx, outKey, outPath, contentTypeFor(job.TargetFormat))
	if err != nil {
		return err
	}

	if err := h.repo.AddOutput(ctx, job.ID, jobs.Output{
		Ordinal:     0,
		ObjectKey:   outKey,
		Filename:    outName,
		Format:      job.TargetFormat,
		SizeBytes:   size,
		ContentType: contentTypeFor(job.TargetFormat),
	}); err != nil {
		return err
	}

	return h.repo.MarkDone(ctx, job.ID)
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
