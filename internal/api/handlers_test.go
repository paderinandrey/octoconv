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

	"github.com/apaderin/octoconv/internal/jobs"
)

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

func newTestServer(repo Repo, store Storage, q Enqueuer) *Server {
	return NewServer(repo, store, q, Config{MaxUploadBytes: 1 << 20})
}

// --- tests ---

func TestCreateJob_OK(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	q := &fakeQueue{}
	srv := newTestServer(repo, store, q)

	body, ct := multipartBody(t, "in.png", "webp", []byte("fakepng"))
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
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
	if q.enqueued != repo.createdID {
		t.Errorf("enqueued %s, want %s", q.enqueued, repo.createdID)
	}
}

func TestCreateJob_UnsupportedPair(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.png", "mp3", []byte("fakepng"))
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
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
	srv := newTestServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{})

	big := make([]byte, (1<<20)+1024) // exceed 1 MiB test limit
	body, ct := multipartBody(t, "in.png", "webp", big)
	req := httptest.NewRequest(http.MethodPost, "/v1/jobs", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestGetJob_DonePresigned(t *testing.T) {
	id := uuid.New()
	repo := &fakeRepo{
		getJob:  &jobs.Job{ID: id, Status: jobs.StatusDone},
		outputs: []jobs.Output{{Ordinal: 0, ObjectKey: "results/x/0-out.webp"}},
	}
	store := &fakeStorage{presigned: "https://example/download"}
	srv := newTestServer(repo, store, &fakeQueue{})

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id.String(), nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["status"] != jobs.StatusDone || resp["download_url"] != "https://example/download" {
		t.Fatalf("unexpected response: %v", resp)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	repo := &fakeRepo{getErr: jobs.ErrNotFound}
	srv := newTestServer(repo, &fakeStorage{}, &fakeQueue{})

	req := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+uuid.New().String(), nil)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
