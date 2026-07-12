package api

import (
	"bytes"
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
	"github.com/apaderin/octoconv/internal/presets"
	"github.com/apaderin/octoconv/internal/storage"
)

const (
	formFieldFile        = "file"
	formFieldTarget      = "target"
	formFieldCallbackURL = "callback_url"
	formFieldOpts        = "opts"
	formFieldPreset      = "preset"
	operationConv        = "convert"
	// maxOptsBytes bounds the opts JSON field before it is even parsed
	// (T-14-02): a conservative 4 KiB comfortably fits the closed DocOpts
	// schema while bounding the DoS surface of an oversized field.
	maxOptsBytes = 4096
	// maxPresetNameBytes bounds the client-supplied preset name before any DB
	// lookup (T-18-09) -- an opaque-string DoS guard, request-independent of
	// preset existence, so a 400 here leaks nothing about the presets table.
	maxPresetNameBytes = 128
	// errUnknownPreset is the SINGLE non-leaking 422 text for every preset
	// resolution miss -- nonexistent, inactive, or cross-client (D-03). No
	// other branch in handleCreateJob may return a distinguishable message
	// for a preset lookup failure.
	errUnknownPreset = "unknown or inactive preset"
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

	// preset, target, and opts are read up front so the D-01 mutual-
	// exclusivity gate can run before any other field validation -- this is a
	// pure request-shape check, independent of client/content/DB state.
	presetName := r.FormValue(formFieldPreset)
	rawTarget := r.FormValue(formFieldTarget)
	rawOpts := r.FormValue(formFieldOpts)
	usingPreset := presetName != ""

	if usingPreset && (rawTarget != "" || (rawOpts != "" && rawOpts != "{}")) {
		// D-01: preset and an explicit target/opts are mutually exclusive --
		// no merge, no precedence guessing.
		writeError(w, http.StatusUnprocessableEntity, "specify either preset or target/opts, not both")
		return
	}
	if usingPreset && len(presetName) > maxPresetNameBytes {
		// Length is request-independent of DB state, so a 400 here leaks
		// nothing about preset existence (T-18-09).
		writeError(w, http.StatusBadRequest, "invalid preset name")
		return
	}

	target := convert.NormalizeFormat(rawTarget)
	if !usingPreset && target == "" {
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

	// Preset resolution (D-07): resolved AFTER client auth but BEFORE content
	// detection / EngineFor, so a preset supplies target_format (and opts,
	// below) exactly as if the client had sent them directly. presetID/
	// presetVer are stashed for the pre-Create active re-check (Pitfall 8);
	// presetOptsMap feeds the SAME opts validation pipeline as ad-hoc opts
	// (D-06, below) -- no bypass branch.
	var presetOptsMap map[string]any
	var presetNameProv string
	var presetVerProv int
	var presetID uuid.UUID
	var presetVer int
	if usingPreset {
		p, err := s.presets.Resolve(ctx, client.ID, presetName)
		if err != nil {
			if errors.Is(err, presets.ErrNotFound) {
				// D-03: nonexistent, inactive, and cross-client all collapse
				// to ErrNotFound inside Resolve -- no ownership branch here.
				writeError(w, http.StatusUnprocessableEntity, errUnknownPreset)
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to resolve preset")
			return
		}
		target = convert.NormalizeFormat(p.TargetFormat)
		presetOptsMap = p.Options
		presetNameProv = p.Name
		presetVerProv = p.Version
		presetID = p.ID
		presetVer = p.Version
	}

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
		// Sniff's prefix table doesn't disambiguate ZIP-based office formats
		// (docx/xlsx/pptx/odt/ods/odp share PK\x03\x04 with each other and a
		// bare .zip) — structurally inspect the ZIP central directory instead
		// (D-01). Read from the original file (io.ReaderAt), NOT rest (an
		// io.MultiReader that does not implement ReaderAt); ReadAt never
		// disturbs Sniff's sequential cursor, so rest remains valid below.
		var prefix [4]byte
		if n, _ := file.ReadAt(prefix[:], 0); n == 4 && bytes.Equal(prefix[:], []byte{'P', 'K', 3, 4}) {
			cr, cerr := convert.SniffContainer(file, header.Size)
			if cerr == nil && cr.Format != "" && !cr.DuplicateRootPart {
				detected = cr.Format
				// D-02/D-04: reject a zip-bomb-shaped declared uncompressed
				// total before any storage write or decompression.
				if cr.TotalUncompressed > s.maxDocumentUncompressedBytes {
					log.Printf("content validation rejected: client_id=%s filename=%q reason=zip_bomb declared_uncompressed=%d limit=%d", client.ID, filename, cr.TotalUncompressed, s.maxDocumentUncompressedBytes)
					writeError(w, http.StatusUnprocessableEntity,
						"declared uncompressed size exceeds configured limit")
					return
				}
				// D-05: unconditional macro rejection, no operator opt-out —
				// macro code never executes as part of producing a PDF output.
				if cr.HasMacro {
					log.Printf("content validation rejected: client_id=%s filename=%q reason=macro_detected", client.ID, filename)
					writeError(w, http.StatusUnprocessableEntity,
						"macro-carrying documents are not accepted")
					return
				}
			}
			// A DuplicateRootPart result, ErrNotAZip, or Format=="" leaves
			// detected empty intentionally — fail closed to the unrecognized-
			// content rejection below rather than accept an ambiguous archive.
		}
	}
	if detected == "" && source == "html" && convert.LooksLikeHTML(file, header.Size) {
		// D-07/HTML-02: HTML has no magic bytes for Sniff's table to match,
		// so content detection here is gated on the (still-untrusted)
		// declared source already claiming "html" (post NormalizeFormat's
		// htm->html alias) PLUS a fail-closed structural content check
		// (UTF-8, no NUL, doctype/html marker). A .html-named file whose
		// content fails LooksLikeHTML leaves detected=="" and falls through
		// to the generic unrecognized-content 422 below (fail-closed,
		// before any storage write) -- never silently accepted just
		// because the extension claims html.
		detected = "html"
	}
	if detected == "" && convert.IsOLECFB(file) {
		// D-05/D-06: legacy binary Office (.doc/.xls/.ppt) and password-
		// protected OOXML ("Agile Encryption") share the identical 8-byte
		// OLE-CFB header — this milestone rejects both as one fail-closed
		// branch rather than attempting to distinguish them (v2 DOCV3-02).
		// A distinct log reason keeps this diagnosable from the generic
		// unrecognized-content 422 below.
		log.Printf("content validation rejected: client_id=%s filename=%q reason=legacy_or_encrypted_document", client.ID, filename)
		writeError(w, http.StatusUnprocessableEntity,
			"legacy binary or password-protected Office format is not supported; convert to docx/xlsx/pptx or remove the password")
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

	// Validate the conversion pair BEFORE writing anything to storage, and
	// derive the engine class in the same step (D-01/D-02). The DETECTED
	// format (not the extension-derived one) is the source of truth fed into
	// the pair-check (D-05).
	engine, ok := convert.Default.EngineFor(detected, target)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity,
			"unsupported conversion: "+detected+" -> "+target)
		return
	}

	// VALID-03: reject a decompression-bomb-shaped upload (declared pixel
	// dimensions exceeding the configured limit) before any storage write.
	// convert.Dimensions re-stitches its own bounded peek onto rest, so the
	// full original stream still reaches s.storage.Upload below unmodified.
	// HasDimensionLimit scopes this to image formats only — pixel dimensions
	// are not a document concept, so documents skip the block entirely
	// (fixes the confirmed regression where Dimensions() unconditionally
	// 422'd every document upload, RESEARCH.md Pitfall 5).
	if convert.HasDimensionLimit(detected) {
		width, height, dimRest, err := convert.Dimensions(detected, rest)
		if err != nil {
			log.Printf("content validation rejected: client_id=%s filename=%q reason=dimensions_unknown", client.ID, filename)
			writeError(w, http.StatusUnprocessableEntity,
				"cannot determine declared image dimensions for "+filename)
			return
		}
		rest = dimRest
		totalPixels := uint64(width) * uint64(height)
		if totalPixels > s.maxImagePixels {
			log.Printf("content validation rejected: client_id=%s filename=%q reason=dimension_limit width=%d height=%d limit=%d", client.ID, filename, width, height, s.maxImagePixels)
			writeError(w, http.StatusUnprocessableEntity,
				"declared image dimensions exceed configured limit")
			return
		}
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

	// opts is optional (D-02); an empty value or the literal "{}" means "no
	// opts" (D-09) and normalizedOpts stays nil, skipping validation
	// entirely. Otherwise: size cap, then engine-keyed syntax parse
	// (ParseDocOpts/ParseHTMLOpts), then engine-keyed applicability
	// (ValidateApplicability/ValidateHTMLApplicability, now that
	// engine/detected/target are known) -- all BEFORE s.storage.Upload below
	// (D-03/D-04). HTMLOpts is a structurally different closed type from
	// DocOpts (page_size/margin_mm/landscape/print_background, not
	// pdf_profile), so the dispatch is engine-keyed rather than a single
	// unconditional call (HTML-03). The API never duplicates
	// internal/convert's validation logic; it only calls it (single
	// validation authority, D-04/D-10).
	//
	// D-06: when a preset was used, rawOpts is re-sourced from the preset's
	// resolved options map (re-marshaled to JSON) INSTEAD of the client's
	// opts form field (which the D-01 mutual-exclusivity gate above already
	// guaranteed is empty). The validators below are completely unaware of
	// this substitution -- stored preset opts flow through the identical
	// fail-closed ParseDocOpts/ParseHTMLOpts + ValidateApplicability path as
	// ad-hoc opts, with no bypass branch. A stored preset whose opts fail
	// current validation fails job creation here, exactly like bad ad-hoc opts.
	var normalizedOpts map[string]any
	if usingPreset {
		if len(presetOptsMap) > 0 {
			presetOptsJSON, err := json.Marshal(presetOptsMap)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to normalize opts")
				return
			}
			rawOpts = string(presetOptsJSON)
		} else {
			rawOpts = ""
		}
	}
	if rawOpts != "" && rawOpts != "{}" {
		if len(rawOpts) > maxOptsBytes {
			log.Printf("content validation rejected: client_id=%s filename=%q reason=opts_too_large size=%d limit=%d", client.ID, filename, len(rawOpts), maxOptsBytes)
			writeError(w, http.StatusUnprocessableEntity, "opts field too large")
			return
		}
		var normalizedRaw []byte
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
			// D-08: persist the normalized struct, never the raw client
			// bytes -- round-trip through json.Marshal so only the
			// validated enum value ever reaches storage.
			normalizedRaw, err = json.Marshal(htmlOpts)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to normalize opts")
				return
			}
		default:
			docOpts, err := convert.ParseDocOpts([]byte(rawOpts))
			if err != nil {
				log.Printf("content validation rejected: client_id=%s filename=%q reason=invalid_opts", client.ID, filename)
				writeError(w, http.StatusUnprocessableEntity, "invalid opts")
				return
			}
			if err := convert.ValidateApplicability(engine, detected, target, docOpts); err != nil {
				log.Printf("content validation rejected: client_id=%s filename=%q reason=opts_not_applicable", client.ID, filename)
				writeError(w, http.StatusUnprocessableEntity, "opts not applicable to this conversion")
				return
			}
			// D-08: persist the normalized struct, never the raw client
			// bytes -- round-trip through json.Marshal so only the
			// validated enum value ever reaches storage.
			normalizedRaw, err = json.Marshal(docOpts)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to normalize opts")
				return
			}
		}
		if err := json.Unmarshal(normalizedRaw, &normalizedOpts); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to normalize opts")
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

	// Pitfall 8 (TOCTOU): a preset can be deactivated or bumped to a new
	// version in the window between the resolution above and this job-row
	// insert. Immediately before repo.Create, re-run the SAME cheap
	// non-locking Resolve; ErrNotFound or a changed id/version means the
	// preset is no longer the one that was resolved, so the job must NOT be
	// created. Mirrors the existing repo.Create-failure path exactly: the
	// just-uploaded object is left in place for TTL cleanup, never deleted
	// here. No preset row is ever locked.
	if usingPreset {
		p2, err := s.presets.Resolve(ctx, client.ID, presetName)
		if err != nil {
			if errors.Is(err, presets.ErrNotFound) {
				writeError(w, http.StatusUnprocessableEntity, errUnknownPreset)
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to resolve preset")
			return
		}
		if p2.ID != presetID || p2.Version != presetVer {
			writeError(w, http.StatusUnprocessableEntity, errUnknownPreset)
			return
		}
	}

	// Postgres-first double write: record the job, then enqueue. The job id is
	// the one already embedded in the storage key above so they stay aligned.
	createdID, err := s.repo.Create(ctx, jobs.CreateParams{
		ID:            jobID,
		ClientID:      client.ID,
		Operation:     operationConv,
		Engine:        engine,
		SourceFormat:  detected,
		TargetFormat:  target,
		CallbackURL:   callbackURL,
		Opts:          normalizedOpts,
		PresetName:    presetNameProv,
		PresetVersion: presetVerProv,
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

	// Route to the matching engine-class queue (mirrors reconciler.go's
	// engine switch). The row stays in 'queued' on any enqueue failure; a
	// reconciler will recover it.
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
		// silently dropped (T-11-02). EngineFor above only ever returns a
		// value produced by a registered Converter.Engine(), so this branch
		// signals a registry/routing bug, not a client input.
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}
	if enqueueErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to enqueue job")
		return
	}

	resp := map[string]any{
		"job_id": createdID,
		"status": jobs.StatusQueued,
	}
	if len(normalizedOpts) > 0 {
		resp["opts"] = normalizedOpts
	}
	writeJSON(w, http.StatusAccepted, resp)
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
	if len(job.Opts) > 0 {
		resp["opts"] = job.Opts
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
