package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
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
