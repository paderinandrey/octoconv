// Package worker contains the asynq task handlers that run conversion engines.
package worker

import (
	"context"
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
)

// Handler processes image conversion tasks end to end.
type Handler struct {
	repo         *jobs.Repo
	store        *storage.Client
	registry     *convert.Registry
	engineTimout time.Duration
}

// NewHandler builds a worker handler.
func NewHandler(repo *jobs.Repo, store *storage.Client, registry *convert.Registry, engineTimeout time.Duration) *Handler {
	if engineTimeout == 0 {
		engineTimeout = 120 * time.Second
	}
	return &Handler{repo: repo, store: store, registry: registry, engineTimout: engineTimeout}
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
		return err
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
