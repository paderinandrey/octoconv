package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/storage"
)

const (
	formFieldFile        = "file"
	formFieldTarget      = "target"
	formFieldCallbackURL = "callback_url"
	engineImage          = "image"
	operationConv        = "convert"
)

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleCreateJob accepts a multipart upload (fields: file, target), validates
// the conversion pair before touching storage, uploads the input to S3, records
// the job in Postgres (queued) and enqueues the conversion task.
func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Cap the request body to reject oversized uploads before buffering them.
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUploadByte)
	if err := r.ParseMultipartForm(s.maxUploadByte); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	target := convert.NormalizeFormat(r.FormValue(formFieldTarget))
	if target == "" {
		writeError(w, http.StatusBadRequest, "missing target format")
		return
	}

	file, header, err := r.FormFile(formFieldFile)
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()

	filename := path.Base(header.Filename)
	source := convert.NormalizeFormat(strings.TrimPrefix(path.Ext(filename), "."))
	if source == "" {
		writeError(w, http.StatusBadRequest, "cannot determine source format from filename")
		return
	}

	// Validate the conversion pair BEFORE writing anything to storage.
	if !convert.Default.Supports(source, target) {
		writeError(w, http.StatusUnprocessableEntity,
			"unsupported conversion: "+source+" -> "+target)
		return
	}

	// callback_url is optional (per-job, D-02); an empty value leaves the
	// existing polling path unchanged. When present it is SSRF-validated
	// BEFORE writing anything to storage, same discipline as the format pair.
	callbackURL := r.FormValue(formFieldCallbackURL)
	if callbackURL != "" {
		if err := validateCallbackURL(callbackURL); err != nil {
			writeError(w, http.StatusBadRequest, "invalid callback_url")
			return
		}
	}

	jobID := uuid.New()
	key := storage.InputKey(jobID, 0, filename)
	contentType := header.Header.Get("Content-Type")

	if err := s.storage.Upload(ctx, key, file, header.Size, contentType); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store upload")
		return
	}

	// Middleware guarantees a resolved client is present before this handler runs.
	client, _ := auth.ClientFromContext(ctx)

	// Postgres-first double write: record the job, then enqueue. The job id is
	// the one already embedded in the storage key above so they stay aligned.
	createdID, err := s.repo.Create(ctx, jobs.CreateParams{
		ID:           jobID,
		ClientID:     client.ID,
		Operation:    operationConv,
		Engine:       engineImage,
		SourceFormat: source,
		TargetFormat: target,
		CallbackURL:  callbackURL,
		Input: jobs.Input{
			Ordinal:     0,
			ObjectKey:   key,
			Filename:    filename,
			Format:      source,
			SizeBytes:   header.Size,
			ContentType: contentType,
		},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create job")
		return
	}

	if err := s.queue.EnqueueImageConvert(ctx, createdID); err != nil {
		// The row stays in 'queued'; a reconciler (next steps) will recover it.
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id": createdID,
		"status": jobs.StatusQueued,
	})
}

// handleGetJob returns the job status; when done, a presigned download URL for
// the first output.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}

	job, err := s.repo.Get(ctx, id)
	if errors.Is(err, jobs.ErrNotFound) {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load job")
		return
	}

	// Ownership guard: a job belonging to a different client is reported
	// with the EXACT SAME status and message as a truly-missing job, so
	// cross-client access is indistinguishable from not-found (AUTH-03) —
	// never a 403, never a distinct message.
	client, _ := auth.ClientFromContext(ctx)
	if job.ClientID != client.ID {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	resp := map[string]any{
		"job_id": job.ID,
		"status": job.Status,
	}

	switch job.Status {
	case jobs.StatusDone:
		outs, err := s.repo.Outputs(ctx, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load outputs")
			return
		}
		if len(outs) > 0 {
			url, err := s.storage.PresignGet(ctx, outs[0].ObjectKey, s.presignTTL)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to presign output")
				return
			}
			resp["download_url"] = url
		}
	case jobs.StatusFailed:
		if job.ErrorCode != "" {
			resp["error_code"] = job.ErrorCode
		}
		if job.ErrorMessage != "" {
			resp["error_message"] = job.ErrorMessage
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	// Don't HTML-escape: presigned URLs contain & that would become &.
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
