// Package ratelimit provides in-process HTTP rate-limiting middleware for the
// /v1 API surface: a coarse pre-auth IP limiter (ByIP) that throttles flood
// traffic before it reaches the auth resolver / Postgres lookup, and a
// per-client limiter (PerClient) that keys on the authenticated client_id so
// one client's burst never affects another's quota.
//
// Both are in-process (github.com/go-chi/httprate's default in-memory
// counter), which matches the current single-API-instance docker-compose
// deployment. If the API is later horizontally scaled behind a load
// balancer, this package's state stops being shared across replicas; the
// upgrade path at that point is httprate's WithLimitCounter option backed by
// a Redis-based counter (e.g. github.com/go-redis/redis_rate), not a
// rewrite of the ByIP/PerClient call sites.
//
// This package takes already-parsed thresholds (ints), never reads env vars
// itself — env parsing stays in cmd/api/main.go, mirroring how internal/worker
// receives ENGINE_TIMEOUT already parsed.
package ratelimit

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/httprate"

	"github.com/apaderin/octoconv/internal/auth"
)

// window is the fixed limiting window for both limiters: requests-per-minute.
const window = time.Minute

// PerClient returns chi middleware enforcing rpm requests per minute per
// authenticated client. It MUST run after auth.Middleware: its key func reads
// the resolved client from context (auth.ClientFromContext), never the
// request's IP, so the limit tracks verified identity and can't be evaded or
// falsely shared by spoofing/rotating source addresses.
func PerClient(rpm int) func(http.Handler) http.Handler {
	return httprate.Limit(
		rpm,
		window,
		httprate.WithKeyFuncs(clientKey),
		httprate.WithLimitHandler(limitHandler(window)),
	)
}

// ByIP returns chi middleware enforcing a coarse rpm requests per minute per
// source IP. It is meant to run BEFORE auth: it needs no request context
// beyond the IP, so it throttles unauthenticated flood traffic before it can
// reach the auth resolver's Postgres lookup.
func ByIP(rpm int) func(http.Handler) http.Handler {
	return httprate.Limit(
		rpm,
		window,
		httprate.WithKeyFuncs(httprate.KeyByIP),
		httprate.WithLimitHandler(limitHandler(window)),
	)
}

// clientKey is an httprate.KeyFunc that keys on the authenticated client's
// id. It is only ever invoked downstream of auth.Middleware, so the client
// should always be present; the error return exists only to satisfy
// httprate.KeyFunc's signature and should not occur in practice.
func clientKey(r *http.Request) (string, error) {
	client, ok := auth.ClientFromContext(r.Context())
	if !ok {
		return "", errors.New("ratelimit: no client in context; PerClient must run after auth.Middleware")
	}
	return client.ID.String(), nil
}

// limitHandler builds the 429 response httprate calls when a key exceeds its
// limit: a Retry-After header set to the window length in whole seconds, and
// a small JSON error body matching the shape used elsewhere in the API
// (internal/api/handlers.go's writeError), duplicated locally rather than
// imported to keep internal/ratelimit free of an internal/api dependency.
func limitHandler(window time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", strconv.Itoa(int(window/time.Second)))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(map[string]string{"error": "rate limit exceeded"})
	}
}
