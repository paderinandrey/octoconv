package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
)

// TestParseOperatorClientIDs covers the fail-closed empty-input case, the
// happy CSV path (including whitespace and a trailing comma), and the
// fail-loud malformed-UUID case (D-01/T-26-03).
func TestParseOperatorClientIDs(t *testing.T) {
	validA := uuid.New().String()
	validB := uuid.New().String()

	tests := []struct {
		name    string
		raw     string
		wantLen int
		wantErr bool
	}{
		{name: "empty string", raw: "", wantLen: 0},
		{name: "whitespace only", raw: "   ", wantLen: 0},
		{name: "single valid uuid", raw: validA, wantLen: 1},
		{name: "csv with surrounding whitespace", raw: "  " + validA + " , " + validB + "  ", wantLen: 2},
		{name: "trailing comma ignored", raw: validA + ",", wantLen: 1},
		{name: "leading comma ignored", raw: "," + validA, wantLen: 1},
		{name: "malformed uuid errors", raw: "not-a-uuid", wantErr: true},
		{name: "one bad token among good ones errors", raw: validA + ",not-a-uuid", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			set, err := ParseOperatorClientIDs(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseOperatorClientIDs(%q) = %v, nil; want a non-nil error", tc.raw, set)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOperatorClientIDs(%q) unexpected error: %v", tc.raw, err)
			}
			if set == nil {
				t.Fatalf("ParseOperatorClientIDs(%q) returned a nil set; want non-nil even when empty (fail-closed contract)", tc.raw)
			}
			if len(set) != tc.wantLen {
				t.Errorf("ParseOperatorClientIDs(%q): len(set) = %d, want %d", tc.raw, len(set), tc.wantLen)
			}
		})
	}
}

// newOperatorGateTestServer builds a Server with the given operator
// allowlist for exercising requireOperator directly (routing is wired in
// Task 2; this task tests the middleware in isolation).
func newOperatorGateTestServer(operatorIDs map[uuid.UUID]struct{}) *Server {
	return NewServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), newFakeResolver(), healthyDeps(), Config{
		MaxUploadBytes:    1 << 20,
		OperatorClientIDs: operatorIDs,
	})
}

// TestRequireOperator_OperatorAllowed verifies the resolved caller in the
// allowlist reaches the wrapped handler.
func TestRequireOperator_OperatorAllowed(t *testing.T) {
	client := &clients.Client{ID: uuid.New(), Name: "operator"}
	srv := newOperatorGateTestServer(map[uuid.UUID]struct{}{client.ID: {}})

	called := false
	h := srv.requireOperator(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(auth.WithClient(req.Context(), client))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected the wrapped handler to run for an operator")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestRequireOperator_NonOperatorNoLeak404 verifies a resolved caller NOT in
// the allowlist gets a byte-identical no-leak 404 (never 403) and the
// wrapped handler never runs (D-02/T-26-02).
func TestRequireOperator_NonOperatorNoLeak404(t *testing.T) {
	operator := &clients.Client{ID: uuid.New(), Name: "operator"}
	other := &clients.Client{ID: uuid.New(), Name: "other"}
	srv := newOperatorGateTestServer(map[uuid.UUID]struct{}{operator.ID: {}})

	called := false
	h := srv.requireOperator(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(auth.WithClient(req.Context(), other))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("wrapped handler must not run for a non-operator")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}

	wantRec := httptest.NewRecorder()
	writeError(wantRec, http.StatusNotFound, noSuchPreset)
	if !bytes.Equal(rec.Body.Bytes(), wantRec.Body.Bytes()) {
		t.Errorf("body = %q, want byte-identical to noSuchPreset 404 %q", rec.Body.String(), wantRec.Body.String())
	}
}

// TestRequireOperator_EmptyAllowlistDeniesEveryone verifies an
// empty/unset OPERATOR_CLIENT_IDS denies even a resolved, otherwise-valid
// caller (fail-closed, T-26-03).
func TestRequireOperator_EmptyAllowlistDeniesEveryone(t *testing.T) {
	client := &clients.Client{ID: uuid.New(), Name: "would-be-operator"}
	srv := newOperatorGateTestServer(nil)

	called := false
	h := srv.requireOperator(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req = req.WithContext(auth.WithClient(req.Context(), client))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if called {
		t.Fatal("wrapped handler must not run when the allowlist is empty")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
