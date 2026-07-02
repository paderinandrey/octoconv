package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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
	uploaded  bool
	presigned string
}

func (f *fakeStorage) Upload(_ context.Context, _ string, r io.Reader, _ int64, _ string) error {
	_, _ = io.Copy(io.Discard, r)
	f.uploaded = true
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
	return NewServer(repo, store, q, resolver, Config{MaxUploadBytes: 1 << 20}), resolver
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

	body, ct := multipartBody(t, "in.png", "webp", []byte("fakepng"))
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

	body, ct := multipartBody(t, "in.png", "mp3", []byte("fakepng"))
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

func TestGetJob_DonePresigned(t *testing.T) {
	id := uuid.New()
	resolver := newFakeResolver()
	repo := &fakeRepo{
		getJob:  &jobs.Job{ID: id, ClientID: resolver.client.ID, Status: jobs.StatusDone},
		outputs: []jobs.Output{{Ordinal: 0, ObjectKey: "results/x/0-out.webp"}},
	}
	store := &fakeStorage{presigned: "https://example/download"}
	srv := NewServer(repo, store, &fakeQueue{}, resolver, Config{MaxUploadBytes: 1 << 20})

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
	srv := NewServer(repo, &fakeStorage{}, &fakeQueue{}, resolver, Config{MaxUploadBytes: 1 << 20})

	req := authed(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id.String(), nil))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
