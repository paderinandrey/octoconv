package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/jobs"
)

// testClientKey is the raw key fakeResolver accepts; requests present it via
// "Authorization: ApiKey testkey".
const testClientKey = "testkey"

// --- fakes ---

type fakeRepo struct {
	created   *jobs.CreateParams
	getJob    *jobs.Job
	getErr    error
	outputs   []jobs.Output
	createdID uuid.UUID
	createErr error
}

func (f *fakeRepo) Create(_ context.Context, p jobs.CreateParams) (uuid.UUID, error) {
	f.created = &p
	if f.createErr != nil {
		return uuid.Nil, f.createErr
	}
	if f.createdID == uuid.Nil {
		f.createdID = uuid.New()
	}
	return f.createdID, nil
}
func (f *fakeRepo) Get(_ context.Context, _ uuid.UUID) (*jobs.Job, error) {
	return f.getJob, f.getErr
}
func (f *fakeRepo) Outputs(_ context.Context, _ uuid.UUID) ([]jobs.Output, error) {
	return f.outputs, nil
}

type fakeStorage struct {
	uploaded    bool
	contentType string
	presigned   string
}

func (f *fakeStorage) Upload(_ context.Context, _ string, r io.Reader, _ int64, contentType string) error {
	_, _ = io.Copy(io.Discard, r)
	f.uploaded = true
	f.contentType = contentType
	return nil
}
func (f *fakeStorage) PresignGet(_ context.Context, _ string, _ time.Duration) (string, error) {
	return f.presigned, nil
}

type fakeQueue struct{ enqueued uuid.UUID }

func (f *fakeQueue) EnqueueImageConvert(_ context.Context, id uuid.UUID) error {
	f.enqueued = id
	return nil
}

// fakePinger implements api.Pinger: returns err (nil = healthy) from Ping.
type fakePinger struct{ err error }

func (f fakePinger) Ping(_ context.Context) error { return f.err }

// healthyDeps is a HealthDeps with all three dependencies reachable.
func healthyDeps() HealthDeps {
	return HealthDeps{Postgres: fakePinger{}, Redis: fakePinger{}, S3: fakePinger{}}
}

// fakeResolver implements auth.ClientResolver: it resolves testClientKey to a
// fixed test client and rejects everything else with auth.ErrInvalidKey.
type fakeResolver struct {
	client *clients.Client
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{client: &clients.Client{ID: uuid.New(), Name: "test-client"}}
}

func (f *fakeResolver) ResolveClient(_ context.Context, rawKey string) (*clients.Client, error) {
	if rawKey != testClientKey {
		return nil, auth.ErrInvalidKey
	}
	return f.client, nil
}

// --- helpers ---

// pngBytesFixture returns the minimal magic-byte prefix that convert.Sniff
// detects as "png" (plus a few trailing bytes so it's a plausible file).
func pngBytesFixture() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
}

// jpegBytesFixture returns the minimal magic-byte prefix that convert.Sniff
// detects as "jpg".
func jpegBytesFixture() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01}
}

func multipartBody(t *testing.T, filename, target string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if filename != "" {
		fw, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(data)
	}
	if target != "" {
		_ = w.WriteField("target", target)
	}
	_ = w.Close()
	return &b, w.FormDataContentType()
}

// newTestServer wires a Server with a fakeResolver that accepts
// testClientKey; it returns the resolver too so tests can assert against its
// fixed client id.
func newTestServer(repo Repo, store Storage, q Enqueuer) (*Server, *fakeResolver) {
	resolver := newFakeResolver()
	return NewServer(repo, store, q, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20}), resolver
}

// authed sets the Authorization header requests need to pass auth.Middleware.
func authed(req *http.Request) *http.Request {
	req.Header.Set("Authorization", "ApiKey "+testClientKey)
	return req
}

// --- tests ---

func TestCreateJob_OK(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	q := &fakeQueue{}
	srv, resolver := newTestServer(repo, store, q)

	body, ct := multipartBody(t, "in.png", "webp", pngBytesFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if !store.uploaded {
		t.Error("expected upload to storage")
	}
	if store.contentType != "image/png" {
		t.Errorf("contentType = %q, want image/png (detected format, not client header, D-06)", store.contentType)
	}
	if repo.created == nil || repo.created.SourceFormat != "png" || repo.created.TargetFormat != "webp" {
		t.Errorf("unexpected create params: %+v", repo.created)
	}
	if repo.created == nil || repo.created.ClientID != resolver.client.ID {
		t.Errorf("expected CreateParams.ClientID = %s, got %+v", resolver.client.ID, repo.created)
	}
	if q.enqueued != repo.createdID {
		t.Errorf("enqueued %s, want %s", q.enqueued, repo.createdID)
	}
}

// TestCreateJob_ContentMismatch verifies D-01/D-04/D-05: a filename claiming
// one format but carrying magic bytes of a different (also-supported) format
// is rejected 422 with a detailed message naming both formats, before any
// storage write or job creation.
func TestCreateJob_ContentMismatch(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.jpg", "webp", pngBytesFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "jpg") || !strings.Contains(resp["error"], "png") {
		t.Errorf("error = %q, want a message naming both declared (jpg) and detected (png)", resp["error"])
	}
	if store.uploaded {
		t.Error("must not upload before content validation")
	}
	if repo.created != nil {
		t.Error("must not create job for mismatched content")
	}
}

// TestCreateJob_UnrecognizedContent verifies D-02: content matching no known
// signature is rejected 422 before storage/create, regardless of extension.
func TestCreateJob_UnrecognizedContent(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.png", "webp", []byte("notanimage"))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload unrecognized content")
	}
	if repo.created != nil {
		t.Error("must not create job for unrecognized content")
	}
}

func TestCreateJob_NoAuthHeader_Unauthorized(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.png", "webp", []byte("fakepng"))
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if repo.created != nil {
		t.Error("must not create job when unauthenticated")
	}
}

func TestCreateJob_UnsupportedPair(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.png", "mp3", pngBytesFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if store.uploaded {
		t.Error("must not upload before validating the pair")
	}
	if repo.created != nil {
		t.Error("must not create job for unsupported pair")
	}
}

func TestCreateJob_TooLarge(t *testing.T) {
	srv, _ := newTestServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{})

	big := make([]byte, (1<<20)+1024) // exceed 1 MiB test limit
	body, ct := multipartBody(t, "in.png", "webp", big)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestHealthz_NoAuthRequired(t *testing.T) {
	srv, _ := newTestServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil) // deliberately no Authorization header
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (D-09: /healthz must stay reachable without a key)", rec.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != "ok" || resp["postgres"] != "ok" || resp["redis"] != "ok" || resp["s3"] != "ok" {
		t.Fatalf("unexpected healthy body: %v", resp)
	}
}

// TestHealthz_Degraded verifies OBS-02/D-16/D-17: a failing dependency ping
// causes a 503 with per-dependency detail, while the other two dependencies
// still report "ok".
func TestHealthz_Degraded(t *testing.T) {
	cases := []struct {
		name   string
		health HealthDeps
		failed string
	}{
		{
			name:   "s3 unreachable",
			health: HealthDeps{Postgres: fakePinger{}, Redis: fakePinger{}, S3: fakePinger{err: errors.New("boom")}},
			failed: "s3",
		},
		{
			name:   "redis unreachable",
			health: HealthDeps{Postgres: fakePinger{}, Redis: fakePinger{err: errors.New("boom")}, S3: fakePinger{}},
			failed: "redis",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newFakeResolver()
			srv := NewServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{}, resolver, tc.health, Config{MaxUploadBytes: 1 << 20})

			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			rec := httptest.NewRecorder()
			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
			}
			var resp map[string]string
			_ = json.Unmarshal(rec.Body.Bytes(), &resp)
			if resp["status"] != "degraded" {
				t.Errorf("status field = %q, want degraded", resp["status"])
			}
			if resp[tc.failed] == "ok" {
				t.Errorf("%s = %q, want non-ok", tc.failed, resp[tc.failed])
			}
			for _, dep := range []string{"postgres", "redis", "s3"} {
				if dep == tc.failed {
					continue
				}
				if resp[dep] != "ok" {
					t.Errorf("%s = %q, want ok", dep, resp[dep])
				}
			}
		})
	}
}

func TestGetJob_DonePresigned(t *testing.T) {
	id := uuid.New()
	resolver := newFakeResolver()
	repo := &fakeRepo{
		getJob:  &jobs.Job{ID: id, ClientID: resolver.client.ID, Status: jobs.StatusDone},
		outputs: []jobs.Output{{Ordinal: 0, ObjectKey: "results/x/0-out.webp"}},
	}
	store := &fakeStorage{presigned: "https://example/download"}
	srv := NewServer(repo, store, &fakeQueue{}, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	req := authed(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id.String(), nil))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != jobs.StatusDone || resp["download_url"] != "https://example/download" {
		t.Fatalf("unexpected response: %v", resp)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	repo := &fakeRepo{getErr: jobs.ErrNotFound}
	srv, _ := newTestServer(repo, &fakeStorage{}, &fakeQueue{})

	req := authed(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+uuid.New().String(), nil))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetJob_CrossClient_NotFound(t *testing.T) {
	id := uuid.New()
	otherClientID := uuid.New()
	repo := &fakeRepo{getJob: &jobs.Job{ID: id, ClientID: otherClientID, Status: jobs.StatusQueued}}
	srv, _ := newTestServer(repo, &fakeStorage{}, &fakeQueue{})

	req := authed(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id.String(), nil))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != "job not found" {
		t.Fatalf(`error = %q, want "job not found" (identical to true-not-found)`, resp["error"])
	}
}

func TestGetJob_SameClient_OK(t *testing.T) {
	id := uuid.New()
	resolver := newFakeResolver()
	repo := &fakeRepo{getJob: &jobs.Job{ID: id, ClientID: resolver.client.ID, Status: jobs.StatusQueued}}
	srv := NewServer(repo, &fakeStorage{}, &fakeQueue{}, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	req := authed(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id.String(), nil))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
