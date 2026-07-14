package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/presets"
)

// ParseOperatorClientIDs parses the OPERATOR_CLIENT_IDS env value (D-01): a
// comma-separated list of client UUIDs authorized to manage system-scope
// presets over /v1/system/presets. An empty or all-whitespace raw value is
// NOT an error -- it returns an empty, non-nil set, meaning zero operators
// (fail-closed: T-26-03, D-01). Entries are trimmed of surrounding
// whitespace and empty entries between/around commas (e.g. a trailing
// comma) are silently skipped. Any entry that is not a valid UUID makes the
// WHOLE parse fail-loud: the caller (cmd/api/main.go) must log.Fatalf and
// abort startup rather than silently admitting a smaller-than-intended
// operator set (T-26-03).
func ParseOperatorClientIDs(raw string) (map[uuid.UUID]struct{}, error) {
	set := make(map[uuid.UUID]struct{})
	if strings.TrimSpace(raw) == "" {
		return set, nil
	}
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		id, err := uuid.Parse(tok)
		if err != nil {
			return nil, fmt.Errorf("invalid OPERATOR_CLIENT_IDS entry %q: %w", tok, err)
		}
		set[id] = struct{}{}
	}
	return set, nil
}

// requireOperator gates the /v1/system/presets subtree to callers whose
// resolved client.ID is a member of s.operators (D-01/T-26-01). It runs
// AFTER auth.Middleware in the chi middleware chain (routes.go), so the
// caller is already authenticated -- this is a second, narrower
// authorization check inside that boundary, not a new identity source
// (T-26-04). A non-operator -- including every caller when s.operators is
// empty (fail-closed) -- receives the exact SAME no-leak 404 body as a
// foreign/nonexistent preset lookup (T-26-02): never 403, and never a
// response distinguishable from an ordinary missing-preset 404.
func (s *Server) requireOperator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, ok := auth.ClientFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusNotFound, noSuchPreset)
			return
		}
		if _, isOperator := s.operators[client.ID]; !isOperator {
			writeError(w, http.StatusNotFound, noSuchPreset)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// The five handlers below mirror presets_handlers.go's user-scope handlers
// exactly in error-mapping discipline, but hardcode presets.ScopeSystem and
// a nil clientID -- they manage system rows DIRECTLY via Get/List/Create/
// Update/Deactivate, never the merged-view GetForClient/ListForClient (D-02/
// D-03). They reuse presetRequest/newPresetResponse/decodePresetRequest/
// validPresetName/noSuchPreset/invalidPresetName from presets_handlers.go
// without redeclaring them. Every route these serve sits behind
// requireOperator (routes.go), so reaching this code already proves the
// caller is an authorized operator.

// handleCreateSystemPreset creates a system-scope preset (D-02/D-04):
// duplicate active name -> 409, invalid opts -> 422, invalid name -> 400.
func (s *Server) handleCreateSystemPreset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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
		Scope:        presets.ScopeSystem,
		ClientID:     nil,
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

	created, err := s.presetAdmin.Get(ctx, presets.ScopeSystem, nil, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load created preset")
		return
	}
	writeJSON(w, http.StatusCreated, newPresetResponse(created))
}

// handleListSystemPresets returns every system-scope preset (D-02); active
// only by default, ?all=true includes inactive versions.
func (s *Server) handleListSystemPresets(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	includeInactive := r.URL.Query().Get("all") == "true"
	list, err := s.presetAdmin.List(ctx, presets.ScopeSystem, nil, includeInactive)
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

// handleShowSystemPreset returns the active system-scope preset named
// {name}; ErrNotFound -> the uniform no-leak 404 (D-02/D-03).
func (s *Server) handleShowSystemPreset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	name := chi.URLParam(r, "name")
	if !validPresetName(name) {
		writeError(w, http.StatusBadRequest, invalidPresetName)
		return
	}

	p, err := s.presetAdmin.Get(ctx, presets.ScopeSystem, nil, name)
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

// handleUpdateSystemPreset bumps the version of the system-scope preset
// named {name} (D-02/D-03, bump-on-update); ErrNotFound -> the uniform
// no-leak 404.
func (s *Server) handleUpdateSystemPreset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

	if _, err := s.presetAdmin.Update(ctx, presets.ScopeSystem, nil, name, req.TargetFormat, req.Options, req.Description); err != nil {
		if errors.Is(err, presets.ErrNotFound) {
			writeError(w, http.StatusNotFound, noSuchPreset)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update preset")
		return
	}

	updated, err := s.presetAdmin.Get(ctx, presets.ScopeSystem, nil, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load updated preset")
		return
	}
	writeJSON(w, http.StatusOK, newPresetResponse(updated))
}

// handleDeactivateSystemPreset soft-deactivates the system-scope preset
// named {name} -- no hard delete (D-02/D-03); ErrNotFound -> the uniform
// no-leak 404.
func (s *Server) handleDeactivateSystemPreset(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	name := chi.URLParam(r, "name")
	if !validPresetName(name) {
		writeError(w, http.StatusBadRequest, invalidPresetName)
		return
	}

	if err := s.presetAdmin.Deactivate(ctx, presets.ScopeSystem, nil, name); err != nil {
		if errors.Is(err, presets.ErrNotFound) {
			writeError(w, http.StatusNotFound, noSuchPreset)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to deactivate preset")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"name": name, "is_active": false})
}
