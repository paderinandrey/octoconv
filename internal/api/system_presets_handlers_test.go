package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/presets"
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

// --- Task 2: five-verb operator vs non-operator vs unset-allowlist matrix (D-06) ---

// systemPresetCase describes one of the five /v1/system/presets verbs
// exercised by the matrix below.
type systemPresetCase struct {
	name   string
	method string
	path   string
	body   map[string]any
}

func systemPresetCases() []systemPresetCase {
	return []systemPresetCase{
		{name: "create", method: http.MethodPost, path: "/v1/system/presets", body: map[string]any{"name": "sys1", "target_format": "webp"}},
		{name: "list", method: http.MethodGet, path: "/v1/system/presets"},
		{name: "show", method: http.MethodGet, path: "/v1/system/presets/sys1"},
		{name: "update", method: http.MethodPut, path: "/v1/system/presets/sys1", body: map[string]any{"target_format": "avif"}},
		{name: "deactivate", method: http.MethodDelete, path: "/v1/system/presets/sys1"},
	}
}

// TestSystemPresets_OperatorSucceedsForAllVerbs verifies an operator (the
// resolved caller's id present in OperatorClientIDs) can create/list/show/
// update/deactivate system-scope presets via /v1/system/presets (OPER-01/
// SC1), and that no response body ever leaks operator-ness (Claude's
// Discretion, D-06).
func TestSystemPresets_OperatorSucceedsForAllVerbs(t *testing.T) {
	now := time.Now().UTC()
	admin := &fakePresetAdmin{
		getResult: &presets.Preset{
			Name: "sys1", Version: 1, Scope: presets.ScopeSystem, TargetFormat: "webp",
			CreatedAt: now, UpdatedAt: now, IsActive: true,
		},
	}
	// Operator set is built from the resolver's fixed client id AFTER the
	// resolver exists (D-06 action note), so the operator-success case
	// naturally includes the resolved caller.
	resolver := newFakeResolver()
	srv := NewServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), admin, resolver, healthyDeps(), Config{
		MaxUploadBytes:    1 << 20,
		OperatorClientIDs: map[uuid.UUID]struct{}{resolver.client.ID: {}},
	})

	wantStatus := map[string]int{
		"create":     http.StatusCreated,
		"list":       http.StatusOK,
		"show":       http.StatusOK,
		"update":     http.StatusOK,
		"deactivate": http.StatusOK,
	}

	for _, tc := range systemPresetCases() {
		t.Run(tc.name, func(t *testing.T) {
			req := presetJSONReq(t, tc.method, tc.path, tc.body)
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != wantStatus[tc.name] {
				t.Fatalf("%s: status = %d, want %d; body=%s", tc.name, rec.Code, wantStatus[tc.name], rec.Body.String())
			}

			var obj map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &obj); err == nil {
				for _, leak := range []string{"operator", "is_operator", "operator_id"} {
					if _, present := obj[leak]; present {
						t.Errorf("%s: response body leaks operator-ness via %q: %v", tc.name, leak, obj)
					}
				}
			}
		})
	}
}

// TestSystemPresets_NonOperatorNoLeak404 verifies a resolved caller NOT in
// the allowlist gets a byte-identical no-leak 404 on every verb (OPER-01/
// SC2) and that presetAdmin is never reached (requireOperator short-
// circuits before any handler runs).
func TestSystemPresets_NonOperatorNoLeak404(t *testing.T) {
	otherOperator := uuid.New()
	admin := &fakePresetAdmin{}
	resolver := newFakeResolver()
	srv := NewServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), admin, resolver, healthyDeps(), Config{
		MaxUploadBytes:    1 << 20,
		OperatorClientIDs: map[uuid.UUID]struct{}{otherOperator: {}},
	})

	wantRec := httptest.NewRecorder()
	writeError(wantRec, http.StatusNotFound, noSuchPreset)

	for _, tc := range systemPresetCases() {
		t.Run(tc.name, func(t *testing.T) {
			req := presetJSONReq(t, tc.method, tc.path, tc.body)
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s: status = %d, want 404; body=%s", tc.name, rec.Code, rec.Body.String())
			}
			if !bytes.Equal(rec.Body.Bytes(), wantRec.Body.Bytes()) {
				t.Errorf("%s: body = %q, want byte-identical to noSuchPreset 404 %q", tc.name, rec.Body.String(), wantRec.Body.String())
			}
			if admin.lastCreateParams != nil {
				t.Errorf("%s: presetAdmin.Create must never be reached for a non-operator", tc.name)
			}
		})
	}
}

// TestSystemPresets_EmptyAllowlistDeniesEveryone verifies an empty/unset
// OPERATOR_CLIENT_IDS denies every verb, including for the resolved test
// client itself (OPER-01/SC3, fail-closed).
func TestSystemPresets_EmptyAllowlistDeniesEveryone(t *testing.T) {
	admin := &fakePresetAdmin{}
	resolver := newFakeResolver()
	srv := NewServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), admin, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	for _, tc := range systemPresetCases() {
		t.Run(tc.name, func(t *testing.T) {
			req := presetJSONReq(t, tc.method, tc.path, tc.body)
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s: status = %d, want 404; body=%s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}
