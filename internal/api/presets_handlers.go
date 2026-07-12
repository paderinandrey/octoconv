package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/presets"
)

const (
	// maxPresetBodyBytes bounds the JSON request body for preset writes
	// before it is even parsed (mirrors maxOptsBytes' T-14-02 discipline):
	// generous slack over the 4 KiB opts cap for name/target_format/
	// description overhead, independent of DB state.
	maxPresetBodyBytes = 8 << 10
	// noSuchPreset is the SINGLE no-leak 404 body for every show/update/
	// deactivate miss -- nonexistent, cross-client, AND system-scope-write
	// attempts all collapse to this exact message (D-03/T-20-03); no branch
	// in these handlers may return a distinguishable message for a lookup
	// miss.
	noSuchPreset = "preset not found"
	// invalidPresetName is the fixed 400 body for a missing/oversized preset
	// name, whether it comes from the request body (create) or the URL path
	// (show/update/deactivate). Length is request-independent of DB state,
	// so this leaks nothing about preset existence (mirrors T-18-09).
	invalidPresetName = "invalid preset name"
)

// presetRequest is the narrow write DTO for POST/PUT /v1/presets (D-02,
// T-20-01): it deliberately has NO scope or client_id field. A request body
// carrying "scope":"system" or a foreign "client_id" is simply ignored by
// json.Unmarshal (unknown fields), so mass-assignment to a different scope
// or owner is structurally impossible -- scope is always hardcoded
// presets.ScopeUser and clientID always comes from auth.ClientFromContext.
type presetRequest struct {
	Name         string         `json:"name"`
	TargetFormat string         `json:"target_format"`
	Options      map[string]any `json:"options"`
	Description  string         `json:"description"`
}

// presetResponse is the narrow read DTO (D-04): it NEVER carries id or
// client_id. Scope is informational only -- "user" for the caller's own
// preset, "system" for a merged-in system preset that is visible but
// read-only via this API (D-10).
type presetResponse struct {
	Name         string         `json:"name"`
	Version      int            `json:"version"`
	Scope        string         `json:"scope"`
	Operation    string         `json:"operation"`
	TargetFormat string         `json:"target_format"`
	Options      map[string]any `json:"options"`
	Description  string         `json:"description"`
	IsActive     bool           `json:"is_active"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// newPresetResponse maps a full presets.Preset row to the narrow response
// DTO, deliberately dropping ID and ClientID (D-04).
func newPresetResponse(p *presets.Preset) presetResponse {
	return presetResponse{
		Name:         p.Name,
		Version:      p.Version,
		Scope:        p.Scope,
		Operation:    p.Operation,
		TargetFormat: p.TargetFormat,
		Options:      p.Options,
		Description:  p.Description,
		IsActive:     p.IsActive,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

// decodePresetRequest reads and JSON-decodes a size-capped request body into
// a presetRequest. On any read/parse error it writes a 400 and returns
// ok=false; callers must return immediately in that case.
func decodePresetRequest(w http.ResponseWriter, r *http.Request) (presetRequest, bool) {
	var req presetRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxPresetBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return presetRequest{}, false
	}
	return req, true
}

// validPresetName reports whether name is non-empty and within the
// request-independent length guard (T-18-09 style, reused here for D-01..
// D-03's write/read handlers).
func validPresetName(name string) bool {
	return name != "" && len(name) <= maxPresetNameBytes
}

// handleCreatePreset creates a client-scope (scope=user) preset owned by the
// authenticated caller (D-01/D-02). Every field the caller does not control
// (scope, client_id) is derived solely from auth.ClientFromContext -- the
// request DTO has no such fields, so mass-assignment is structurally
// impossible (T-20-01).
func (s *Server) handleCreatePreset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	client, _ := auth.ClientFromContext(ctx)

	req, ok := decodePresetRequest(w, r)
	if !ok {
		return
	}
	if !validPresetName(req.Name) {
		writeError(w, http.StatusBadRequest, invalidPresetName)
		return
	}
	if err := presets.ValidateOptsJSON(req.Options); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid opts")
		return
	}

	if _, _, err := s.presetAdmin.Create(ctx, presets.CreateParams{
		Name:         req.Name,
		Scope:        presets.ScopeUser,
		ClientID:     &client.ID,
		TargetFormat: req.TargetFormat,
		Options:      req.Options,
		Description:  req.Description,
	}); err != nil {
		if errors.Is(err, presets.ErrAlreadyExists) {
			writeError(w, http.StatusConflict, "preset already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create preset")
		return
	}

	created, err := s.presetAdmin.GetForClient(ctx, &client.ID, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load created preset")
		return
	}
	writeJSON(w, http.StatusCreated, newPresetResponse(created))
}

// handleListPresets returns the merged effective view (D-10): the caller's
// own presets plus system presets, each carrying scope so system entries are
// recognizable as read-only. Active-only by default; ?all=true includes
// inactive versions (D-01).
func (s *Server) handleListPresets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	client, _ := auth.ClientFromContext(ctx)

	includeInactive := r.URL.Query().Get("all") == "true"
	list, err := s.presetAdmin.ListForClient(ctx, &client.ID, includeInactive)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list presets")
		return
	}

	resp := make([]presetResponse, 0, len(list))
	for i := range list {
		resp = append(resp, newPresetResponse(&list[i]))
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleShowPreset returns the effective preset for {name} (D-10): a user
// override wins over a same-name system preset; a system-only name returns
// the system preset, read-only. A nonexistent or cross-client name returns
// the single no-leak 404 (D-03).
func (s *Server) handleShowPreset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	client, _ := auth.ClientFromContext(ctx)

	name := chi.URLParam(r, "name")
	if !validPresetName(name) {
		writeError(w, http.StatusBadRequest, invalidPresetName)
		return
	}

	p, err := s.presetAdmin.GetForClient(ctx, &client.ID, name)
	if errors.Is(err, presets.ErrNotFound) {
		writeError(w, http.StatusNotFound, noSuchPreset)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load preset")
		return
	}
	writeJSON(w, http.StatusOK, newPresetResponse(p))
}

// handleUpdatePreset bumps the version of the caller's OWN (scope=user)
// preset named {name} (D-01/D-03/PRAPI-02, bump-on-update). Because the
// write is always scope=user + the ctx-derived client id, a nonexistent,
// cross-client, or system-scope name for {name} all fail the SAME guarded
// lookup inside Update and return the identical no-leak 404 -- there is no
// separate branch that could distinguish "exists but is system-scope" from
// "doesn't exist at all".
func (s *Server) handleUpdatePreset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	client, _ := auth.ClientFromContext(ctx)

	name := chi.URLParam(r, "name")
	if !validPresetName(name) {
		writeError(w, http.StatusBadRequest, invalidPresetName)
		return
	}

	req, ok := decodePresetRequest(w, r)
	if !ok {
		return
	}
	if err := presets.ValidateOptsJSON(req.Options); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid opts")
		return
	}

	if _, err := s.presetAdmin.Update(ctx, presets.ScopeUser, &client.ID, name, req.TargetFormat, req.Options, req.Description); err != nil {
		if errors.Is(err, presets.ErrNotFound) {
			writeError(w, http.StatusNotFound, noSuchPreset)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update preset")
		return
	}

	updated, err := s.presetAdmin.GetForClient(ctx, &client.ID, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load updated preset")
		return
	}
	writeJSON(w, http.StatusOK, newPresetResponse(updated))
}

// handleDeactivatePreset soft-deactivates the caller's OWN (scope=user)
// preset named {name} -- no hard delete (D-01/PRAPI-02). Mirrors
// handleUpdatePreset's no-leak discipline: nonexistent, cross-client, and
// system-scope names all return the identical 404 (D-03).
func (s *Server) handleDeactivatePreset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	client, _ := auth.ClientFromContext(ctx)

	name := chi.URLParam(r, "name")
	if !validPresetName(name) {
		writeError(w, http.StatusBadRequest, invalidPresetName)
		return
	}

	if err := s.presetAdmin.Deactivate(ctx, presets.ScopeUser, &client.ID, name); err != nil {
		if errors.Is(err, presets.ErrNotFound) {
			writeError(w, http.StatusNotFound, noSuchPreset)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to deactivate preset")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"name": name, "is_active": false})
}
