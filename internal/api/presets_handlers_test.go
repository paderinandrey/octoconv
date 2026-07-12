package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/presets"
)

// newPresetTestServer wires a Server with the given fakePresetAdmin and a
// fakeResolver that accepts testClientKey, returning the resolver too so
// tests can assert against its fixed client id (mass-assignment, D-08).
func newPresetTestServer(admin *fakePresetAdmin) (*Server, *fakeResolver) {
	resolver := newFakeResolver()
	srv := NewServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), admin, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})
	return srv, resolver
}

func jsonBody(t *testing.T, v map[string]any) *bytes.Buffer {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return bytes.NewBuffer(b)
}

func presetJSONReq(t *testing.T, method, path string, body map[string]any) *http.Request {
	t.Helper()
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, jsonBody(t, body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	return authed(req)
}

// TestCreatePreset_OK verifies POST /v1/presets returns 201 with the
// response DTO at version 1 and omits id/client_id (D-04).
func TestCreatePreset_OK(t *testing.T) {
	now := time.Now().UTC()
	admin := &fakePresetAdmin{
		getForClientResult: &presets.Preset{
			ID: uuid.New(), Name: "thumb", Version: 1, Scope: presets.ScopeUser,
			ClientID: func() *uuid.UUID { id := uuid.New(); return &id }(),
			Operation: presets.OperationConvert, TargetFormat: "webp",
			IsActive: true, CreatedAt: now, UpdatedAt: now,
		},
	}
	srv, _ := newPresetTestServer(admin)

	req := presetJSONReq(t, http.MethodPost, "/v1/presets", map[string]any{
		"name": "thumb", "target_format": "webp",
	})
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["version"] != float64(1) {
		t.Errorf("version = %v, want 1", resp["version"])
	}
	if _, present := resp["id"]; present {
		t.Errorf("response = %v, must never include id (D-04)", resp)
	}
	if _, present := resp["client_id"]; present {
		t.Errorf("response = %v, must never include client_id (D-04)", resp)
	}
}

// TestCreatePreset_Duplicate409 verifies POST on an existing active name
// returns 409 (D-03).
func TestCreatePreset_Duplicate409(t *testing.T) {
	admin := &fakePresetAdmin{createErr: presets.ErrAlreadyExists}
	srv, _ := newPresetTestServer(admin)

	req := presetJSONReq(t, http.MethodPost, "/v1/presets", map[string]any{
		"name": "thumb", "target_format": "webp",
	})
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreatePreset_MassAssignmentIgnored verifies T-20-01/D-02: a body
// carrying "scope":"system" and a foreign "client_id" has zero effect --
// the captured CreateParams always has Scope=ScopeUser and ClientID equal to
// the AUTHENTICATED client, never the body-supplied values.
func TestCreatePreset_MassAssignmentIgnored(t *testing.T) {
	now := time.Now().UTC()
	admin := &fakePresetAdmin{
		getForClientResult: &presets.Preset{Name: "thumb", Version: 1, Scope: presets.ScopeUser, CreatedAt: now, UpdatedAt: now},
	}
	srv, resolver := newPresetTestServer(admin)

	foreignClientID := uuid.New().String()
	req := presetJSONReq(t, http.MethodPost, "/v1/presets", map[string]any{
		"name": "thumb", "target_format": "webp",
		"scope": "system", "client_id": foreignClientID,
	})
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if admin.lastCreateParams == nil {
		t.Fatal("expected Create to be called")
	}
	if admin.lastCreateParams.Scope != presets.ScopeUser {
		t.Errorf("CreateParams.Scope = %q, want %q (body's scope=system must be ignored)", admin.lastCreateParams.Scope, presets.ScopeUser)
	}
	if admin.lastCreateParams.ClientID == nil || *admin.lastCreateParams.ClientID != resolver.client.ID {
		t.Errorf("CreateParams.ClientID = %v, want %s (body's foreign client_id must be ignored)", admin.lastCreateParams.ClientID, resolver.client.ID)
	}
}

// TestCreatePreset_BadOpts422 verifies opts failing presets.ValidateOptsJSON
// are rejected 422 (D-05).
func TestCreatePreset_BadOpts422(t *testing.T) {
	admin := &fakePresetAdmin{}
	srv, _ := newPresetTestServer(admin)

	req := presetJSONReq(t, http.MethodPost, "/v1/presets", map[string]any{
		"name": "thumb", "target_format": "webp",
		"options": map[string]any{"totally_unknown_field": true},
	})
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if admin.lastCreateParams != nil {
		t.Error("must not call Create for invalid opts")
	}
}

// TestListPresets_MergedView verifies GET /v1/presets returns both the
// caller's own preset and a read-only system preset, each carrying scope
// (D-10).
func TestListPresets_MergedView(t *testing.T) {
	now := time.Now().UTC()
	admin := &fakePresetAdmin{
		listForClientResult: []presets.Preset{
			{Name: "mine", Version: 1, Scope: presets.ScopeUser, TargetFormat: "webp", CreatedAt: now, UpdatedAt: now, IsActive: true},
			{Name: "shared", Version: 1, Scope: presets.ScopeSystem, TargetFormat: "avif", CreatedAt: now, UpdatedAt: now, IsActive: true},
		},
	}
	srv, _ := newPresetTestServer(admin)

	req := presetJSONReq(t, http.MethodGet, "/v1/presets", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp []map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp) != 2 {
		t.Fatalf("list = %+v, want 2 entries", resp)
	}
	scopes := map[string]bool{}
	for _, p := range resp {
		scopes[p["scope"].(string)] = true
	}
	if !scopes[presets.ScopeUser] || !scopes[presets.ScopeSystem] {
		t.Errorf("scopes seen = %+v, want both user and system", scopes)
	}
}

// TestShowPreset_SystemOnly verifies a system-only name shows read-only
// (scope=system) at 200 (D-10).
func TestShowPreset_SystemOnly(t *testing.T) {
	now := time.Now().UTC()
	admin := &fakePresetAdmin{
		getForClientResult: &presets.Preset{Name: "shared", Version: 1, Scope: presets.ScopeSystem, TargetFormat: "avif", CreatedAt: now, UpdatedAt: now, IsActive: true},
	}
	srv, _ := newPresetTestServer(admin)

	req := presetJSONReq(t, http.MethodGet, "/v1/presets/shared", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["scope"] != presets.ScopeSystem {
		t.Errorf("scope = %v, want system", resp["scope"])
	}
}

// TestShowPreset_NoLeak404 verifies D-03: a nonexistent name and a
// (simulated) cross-client/system-write-only name both return the IDENTICAL
// 404 body -- byte-for-byte, not just the same status code.
func TestShowPreset_NoLeak404(t *testing.T) {
	cases := []string{"nonexistent", "cross-client"}
	bodies := make([][]byte, 0, len(cases))
	for _, name := range cases {
		admin := &fakePresetAdmin{getForClientErr: presets.ErrNotFound}
		srv, _ := newPresetTestServer(admin)

		req := presetJSONReq(t, http.MethodGet, "/v1/presets/"+name, nil)
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404; body=%s", name, rec.Code, rec.Body.String())
		}
		bodies = append(bodies, rec.Body.Bytes())
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Errorf("nonexistent vs cross-client response bodies differ: %q vs %q (D-03 no-leak violation)", bodies[0], bodies[1])
	}
}

// TestUpdatePreset_BumpsVersion verifies PUT /v1/presets/{name} returns 200
// with the bumped version (D-03/PRAPI-02).
func TestUpdatePreset_BumpsVersion(t *testing.T) {
	now := time.Now().UTC()
	admin := &fakePresetAdmin{
		updateVersion: 2,
		getForClientResult: &presets.Preset{
			Name: "thumb", Version: 2, Scope: presets.ScopeUser, TargetFormat: "avif", CreatedAt: now, UpdatedAt: now, IsActive: true,
		},
	}
	srv, _ := newPresetTestServer(admin)

	req := presetJSONReq(t, http.MethodPut, "/v1/presets/thumb", map[string]any{"target_format": "avif"})
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["version"] != float64(2) {
		t.Errorf("version = %v, want 2", resp["version"])
	}
}

// TestUpdatePreset_NoLeak404 verifies updating a nonexistent, cross-client,
// or system-scope name returns 404 (D-03) -- the write is always scope=user,
// so a system-scope name for {name} fails the SAME guarded lookup as a
// truly-missing name inside Update.
func TestUpdatePreset_NoLeak404(t *testing.T) {
	admin := &fakePresetAdmin{updateErr: presets.ErrNotFound}
	srv, _ := newPresetTestServer(admin)

	req := presetJSONReq(t, http.MethodPut, "/v1/presets/shared", map[string]any{"target_format": "avif"})
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestDeactivatePreset_ThenShowNotFound verifies DELETE deactivates (soft,
// 200) and a subsequent show for the same server/admin returns 404 --
// no hard delete (PRAPI-02).
func TestDeactivatePreset_ThenShowNotFound(t *testing.T) {
	admin := &fakePresetAdmin{getForClientErr: presets.ErrNotFound}
	srv, _ := newPresetTestServer(admin)

	delReq := presetJSONReq(t, http.MethodDelete, "/v1/presets/thumb", nil)
	delRec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("deactivate status = %d, want 200; body=%s", delRec.Code, delRec.Body.String())
	}

	showReq := presetJSONReq(t, http.MethodGet, "/v1/presets/thumb", nil)
	showRec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(showRec, showReq)
	if showRec.Code != http.StatusNotFound {
		t.Fatalf("show-after-deactivate status = %d, want 404; body=%s", showRec.Code, showRec.Body.String())
	}
}

// TestDeactivatePreset_NoLeak404 verifies deactivating a nonexistent/
// cross-client/system name returns 404 (D-03).
func TestDeactivatePreset_NoLeak404(t *testing.T) {
	admin := &fakePresetAdmin{deactivateErr: presets.ErrNotFound}
	srv, _ := newPresetTestServer(admin)

	req := presetJSONReq(t, http.MethodDelete, "/v1/presets/ghost", nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPresets_Unauthenticated401 proves /v1/presets sits inside the /v1
// auth group (D-07/T-20-05): an unauthenticated request is rejected 401.
func TestPresets_Unauthenticated401(t *testing.T) {
	srv, _ := newPresetTestServer(&fakePresetAdmin{})

	req := httptest.NewRequest(http.MethodGet, "/v1/presets", nil) // no Authorization header
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
