package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/apaderin/octoconv/internal/clients"
)

// ParseAPIKey parses an Authorization header value against the project's
// "ApiKey <key>" scheme: exactly two whitespace-delimited fields, the first
// case-insensitively equal to "ApiKey". It is the single shared parse used
// by both the REST Middleware and cmd/mcp-http (Phase 25, D-03) -- no
// divergent copies. ok is false for a missing, malformed, or wrong-scheme
// header; the raw key is returned only when ok is true.
func ParseAPIKey(header string) (key string, ok bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], authScheme) {
		return "", false
	}
	return fields[1], true
}

// ClientResolver resolves a raw API key presented by a caller to the client
// it belongs to. It is a narrow, consumer-owned interface: internal/api
// depends on this rather than the concrete Resolver type, so it never needs
// to import internal/clients directly.
type ClientResolver interface {
	ResolveClient(ctx context.Context, rawKey string) (*clients.Client, error)
}

// keyStore is the minimal lookup Resolver needs from internal/clients.Repo.
type keyStore interface {
	GetByKeyHash(ctx context.Context, keyHash string) (*clients.Client, error)
}

// ErrInvalidKey is returned by ResolveClient (and mapped to an HTTP 401 by
// Middleware) when the presented API key does not resolve to an active
// client — missing, malformed, unknown, or revoked all collapse to this one
// sentinel so the middleware cannot distinguish them for callers.
var ErrInvalidKey = errors.New("invalid api key")

// Resolver implements ClientResolver against a keyStore (internal/clients.Repo).
type Resolver struct {
	store keyStore
	salt  []byte
}

// NewResolver builds a Resolver. salt is the server-side pepper (wired from
// API_KEY_SALT by callers) — never read from the environment here, per
// package convention established in hash.go.
func NewResolver(store keyStore, salt []byte) *Resolver {
	return &Resolver{store: store, salt: salt}
}

// ResolveClient hashes rawKey with the configured salt and looks up the
// owning client, translating clients.ErrNotFound into ErrInvalidKey. The raw
// key is reduced to a salted SHA-256 digest before the only equality check
// (an indexed lookup in Postgres), so no variable-time comparison of raw
// secret material occurs in Go.
func (r *Resolver) ResolveClient(ctx context.Context, rawKey string) (*clients.Client, error) {
	h := HashKey(r.salt, rawKey)
	c, err := r.store.GetByKeyHash(ctx, h)
	if errors.Is(err, clients.ErrNotFound) {
		return nil, ErrInvalidKey
	}
	if err != nil {
		return nil, fmt.Errorf("resolve client: %w", err)
	}
	return c, nil
}
