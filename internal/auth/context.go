package auth

import (
	"context"

	"github.com/apaderin/octoconv/internal/clients"
)

// ctxKey is the unexported context key type for the resolved client. This is
// the only use of context.WithValue in the codebase; it is deliberately kept
// to exactly this one key type plus the two accessors below.
type ctxKey struct{}

// WithClient returns a copy of ctx carrying the resolved client. Only
// Middleware should call this, and only after a successful ResolveClient.
func WithClient(ctx context.Context, c *clients.Client) context.Context {
	return context.WithValue(ctx, ctxKey{}, c)
}

// ClientFromContext returns the client injected by Middleware, if any.
// Handlers downstream of Middleware can rely on ok being true.
func ClientFromContext(ctx context.Context) (*clients.Client, bool) {
	c, ok := ctx.Value(ctxKey{}).(*clients.Client)
	return c, ok
}
