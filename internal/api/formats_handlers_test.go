package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestListFormats_RegistryDerived verifies GET /v1/formats returns a 200
// with a registry-derived engine->pairs map (D-06) that contains the
// "image" class with a known libvips pair.
func TestListFormats_RegistryDerived(t *testing.T) {
	srv, _ := newTestServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{})

	req := authed(httptest.NewRequest(http.MethodGet, "/v1/formats", nil))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Engines map[string]struct {
			Pairs [][]string `json:"pairs"`
		} `json:"engines"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v; body=%s", err, rec.Body.String())
	}

	image, ok := resp.Engines["image"]
	if !ok {
		t.Fatalf(`response engines = %+v, want an "image" class`, resp.Engines)
	}
	found := false
	for _, pair := range image.Pairs {
		if len(pair) == 2 && pair[0] == "png" && pair[1] == "webp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf(`image.pairs = %+v, want it to contain ["png","webp"]`, image.Pairs)
	}
}

// TestListFormats_Unauthenticated401 proves GET /v1/formats sits inside the
// /v1 auth group (D-07): an unauthenticated request is rejected 401.
func TestListFormats_Unauthenticated401(t *testing.T) {
	srv, _ := newTestServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{})

	req := httptest.NewRequest(http.MethodGet, "/v1/formats", nil) // no Authorization header
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
