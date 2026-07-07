package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

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

// handleHealth probes Postgres, Redis, and S3/MinIO reachability under a
// shared short timeout and reports per-dependency status (OBS-02, D-16/D-17).
// It is read-only: it only pings, never writes.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	result := map[string]string{}
	healthy := true

	if err := s.health.Postgres.Ping(ctx); err != nil {
		result["postgres"] = "unreachable"
		healthy = false
	} else {
		result["postgres"] = "ok"
	}

	if err := s.health.Redis.Ping(ctx); err != nil {
		result["redis"] = "unreachable"
		healthy = false
	} else {
		result["redis"] = "ok"
	}

	if err := s.health.S3.Ping(ctx); err != nil {
		result["s3"] = "unreachable"
		healthy = false
	} else {
		result["s3"] = "ok"
	}

	status := http.StatusOK
	result["status"] = "ok"
	if !healthy {
		status = http.StatusServiceUnavailable
		result["status"] = "degraded"
	}
	writeJSON(w, status, result)
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
	// Declared source, from the (attacker-controllable) filename extension.
	// Still needed below for the D-01 honesty comparison against the
	// magic-byte-detected format — it is no longer trusted on its own.
	source := convert.NormalizeFormat(strings.TrimPrefix(path.Ext(filename), "."))
	if source == "" {
		writeError(w, http.StatusBadRequest, "cannot determine source format from filename")
		return
	}

	// Middleware guarantees a resolved client is present before this handler
	// runs. Resolved BEFORE content detection because a mismatch/unrecognized
	// rejection below must log the client id (D-08).
	client, _ := auth.ClientFromContext(ctx)

	// Detect the actual content format by magic bytes BEFORE anything else
	// touches storage or Postgres (D-01/D-02/D-05). rest re-stitches the
	// peeked prefix onto the remaining stream so the full file still reaches
	// s3.Upload below.
	detected, rest, err := convert.Sniff(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}
	if detected == "" {
		// D-02: no known signature matches — reject rather than let the
		// (untrustworthy) extension win. D-08: scoped internal/* logging
		// exception for this rejection, tagged with the resolved client.
		log.Printf("content validation rejected: client_id=%s filename=%q reason=unrecognized_content", client.ID, filename)
		writeError(w, http.StatusUnprocessableEntity,
			"unrecognized file content for "+filename)
		return
	}
	if detected != source {
		// D-01/D-04: declared extension must be honest about the actual
		// content; no auto-correction to the detected format.
		log.Printf("content validation rejected: client_id=%s filename=%q reason=mismatch declared=%s detected=%s", client.ID, filename, source, detected)
		writeError(w, http.StatusUnprocessableEntity,
			"declared format "+source+" does not match detected content "+detected)
		return
	}

	// Validate the conversion pair BEFORE writing anything to storage. The
	// DETECTED format (not the extension-derived one) is the source of truth
	// fed into the pair-check (D-05).
	if !convert.Default.Supports(detected, target) {
		writeError(w, http.StatusUnprocessableEntity,
			"unsupported conversion: "+detected+" -> "+target)
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
	// Stored Content-Type is the canonical MIME of the detected format, never
	// the client-supplied multipart header (D-06).
	contentType := convert.MIMEType(detected)

	if err := s.storage.Upload(ctx, key, rest, header.Size, contentType); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to store upload")
		return
	}

	// Postgres-first double write: record the job, then enqueue. The job id is
	// the one already embedded in the storage key above so they stay aligned.
	createdID, err := s.repo.Create(ctx, jobs.CreateParams{
		ID:           jobID,
		ClientID:     client.ID,
		Operation:    operationConv,
		Engine:       engineImage,
		SourceFormat: detected,
		TargetFormat: target,
		CallbackURL:  callbackURL,
		Input: jobs.Input{
			Ordinal:     0,
			ObjectKey:   key,
			Filename:    filename,
			Format:      detected,
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
