package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/clients"
)

// fakeResolver implements ClientResolver for middleware tests: it returns a
// configured client or a configured error, regardless of the presented key.
type fakeResolver struct {
	client *clients.Client
	err    error
}

func (f *fakeResolver) ResolveClient(_ context.Context, _ string) (*clients.Client, error) {
	return f.client, f.err
}

// sentinelNext records whether it was invoked, and asserts the client
// resolved by the middleware is reachable via ClientFromContext.
type sentinelNext struct {
	called      bool
	gotClient   *clients.Client
	gotClientOK bool
}

func (s *sentinelNext) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.called = true
		s.gotClient, s.gotClientOK = ClientFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func newRequest(authHeader string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+uuid.New().String(), nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

func decodeErrorBody(t *testing.T, body []byte) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode error body: %v; body=%s", err, body)
	}
	return m
}

func TestMiddleware_ValidKey_PassesThrough(t *testing.T) {
	client := &clients.Client{ID: uuid.New(), Name: "acme"}
	resolver := &fakeResolver{client: client}
	next := &sentinelNext{}

	rec := httptest.NewRecorder()
	Middleware(resolver)(next.handler()).ServeHTTP(rec, newRequest("ApiKey good-key"))

	if !next.called {
		t.Fatal("expected next handler to be called on valid key")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !next.gotClientOK {
		t.Fatal("expected ClientFromContext to report ok=true inside next handler")
	}
	if next.gotClient == nil || next.gotClient.ID != client.ID {
		t.Fatalf("expected resolved client ID %s in context, got %+v", client.ID, next.gotClient)
	}
}

func TestMiddleware_MissingHeader_Unauthorized(t *testing.T) {
	resolver := &fakeResolver{client: &clients.Client{ID: uuid.New()}}
	next := &sentinelNext{}

	rec := httptest.NewRecorder()
	Middleware(resolver)(next.handler()).ServeHTTP(rec, newRequest(""))

	if next.called {
		t.Fatal("next handler must NOT be called when Authorization header is missing")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertJSONErrorBody(t, rec, "")
}

func TestMiddleware_WrongScheme_Unauthorized(t *testing.T) {
	cases := []string{
		"bare-token-no-scheme",
		"Bearer some-token",
		"ApiKey", // scheme with no key
	}
	for _, header := range cases {
		t.Run(header, func(t *testing.T) {
			resolver := &fakeResolver{client: &clients.Client{ID: uuid.New()}}
			next := &sentinelNext{}

			rec := httptest.NewRecorder()
			Middleware(resolver)(next.handler()).ServeHTTP(rec, newRequest(header))

			if next.called {
				t.Fatalf("next handler must NOT be called for malformed header %q", header)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 for header %q", rec.Code, header)
			}
		})
	}
}

func TestMiddleware_InvalidKey_Unauthorized(t *testing.T) {
	resolver := &fakeResolver{err: ErrInvalidKey}
	next := &sentinelNext{}

	rec := httptest.NewRecorder()
	presentedKey := "the-presented-key-value"
	Middleware(resolver)(next.handler()).ServeHTTP(rec, newRequest("ApiKey "+presentedKey))

	if next.called {
		t.Fatal("next handler must NOT be called when resolver returns ErrInvalidKey")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertJSONErrorBody(t, rec, presentedKey)
}

func TestMiddleware_ResolverError_InternalServerError(t *testing.T) {
	resolver := &fakeResolver{err: errors.New("db down")}
	next := &sentinelNext{}

	rec := httptest.NewRecorder()
	presentedKey := "another-presented-key"
	Middleware(resolver)(next.handler()).ServeHTTP(rec, newRequest("ApiKey "+presentedKey))

	if next.called {
		t.Fatal("next handler must NOT be called when resolver returns a non-ErrInvalidKey error")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	assertJSONErrorBody(t, rec, presentedKey)
}

// TestParseAPIKey covers the shared ApiKey scheme parser used by both the
// REST Middleware and cmd/mcp-http (Phase 25): exactly two fields, the first
// case-insensitively equal to "ApiKey".
func TestParseAPIKey(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		wantKey string
		wantOK  bool
	}{
		{"valid", "ApiKey secret-123", "secret-123", true},
		{"case-insensitive scheme", "apikey secret-123", "secret-123", true},
		{"upper scheme", "APIKEY secret-123", "secret-123", true},
		{"empty header", "", "", false},
		{"bare token", "secret-123", "", false},
		{"wrong scheme", "Bearer secret-123", "", false},
		{"scheme only", "ApiKey", "", false},
		{"three fields", "ApiKey secret extra", "", false},
		{"extra whitespace ok", "ApiKey   secret-123", "secret-123", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, ok := ParseAPIKey(tc.header)
			if ok != tc.wantOK {
				t.Fatalf("ParseAPIKey(%q) ok = %v, want %v", tc.header, ok, tc.wantOK)
			}
			if key != tc.wantKey {
				t.Fatalf("ParseAPIKey(%q) key = %q, want %q", tc.header, key, tc.wantKey)
			}
		})
	}
}

// assertJSONErrorBody asserts the response is JSON with Content-Type
// application/json, has a non-empty "error" field, and never echoes the
// presented raw key back to the caller.
func assertJSONErrorBody(t *testing.T, rec *httptest.ResponseRecorder, presentedKey string) {
	t.Helper()
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	body := decodeErrorBody(t, rec.Body.Bytes())
	if body["error"] == "" {
		t.Fatalf("expected non-empty \"error\" field, got body=%v", body)
	}
	if presentedKey != "" && strings.Contains(rec.Body.String(), presentedKey) {
		t.Fatalf("response body must never echo the presented key %q: %s", presentedKey, rec.Body.String())
	}
}
