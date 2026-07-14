package auth

import (
	"encoding/json"
	"errors"
	"net/http"
)

// authScheme is the required Authorization header scheme token.
const authScheme = "ApiKey"

// Middleware enforces API-key authentication on every request it wraps: it
// requires an "Authorization: ApiKey <key>" header, resolves the key via
// resolver, and injects the resolved client into the request context for
// downstream handlers (see WithClient/ClientFromContext). A missing,
// malformed, unknown, or revoked key is rejected with 401 before next runs —
// hard cutover per D-08, there is no warn-only path. A resolver error other
// than ErrInvalidKey (e.g. the database is unreachable) is a 500, also
// short-circuiting before next.
func Middleware(resolver ClientResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, ok := ParseAPIKey(r.Header.Get("Authorization"))
			if !ok {
				writeError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
				return
			}

			client, err := resolver.ResolveClient(r.Context(), key)
			if err != nil {
				if errors.Is(err, ErrInvalidKey) {
					writeError(w, http.StatusUnauthorized, "invalid api key")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to resolve api key")
				return
			}

			next.ServeHTTP(w, r.WithContext(WithClient(r.Context(), client)))
		})
	}
}

// writeError writes a JSON error body. Duplicated (not imported) from
// internal/api's helper of the same shape — internal/auth must not import
// internal/api, that would invert the dependency direction. This mirrors the
// existing firstField/envInt duplication-per-package convention.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
