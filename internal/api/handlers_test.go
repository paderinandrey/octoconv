package api

import (
	"archive/zip"
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

type fakeQueue struct {
	enqueuedImage    uuid.UUID
	enqueuedDocument uuid.UUID
	enqueuedHTML     uuid.UUID
}

func (f *fakeQueue) EnqueueImageConvert(_ context.Context, id uuid.UUID) error {
	f.enqueuedImage = id
	return nil
}

func (f *fakeQueue) EnqueueDocumentConvert(_ context.Context, id uuid.UUID) error {
	f.enqueuedDocument = id
	return nil
}

func (f *fakeQueue) EnqueueHTMLConvert(_ context.Context, id uuid.UUID) error {
	f.enqueuedHTML = id
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

// pngBytesFixture returns a full 24-byte PNG signature+IHDR chunk (declaring
// a small, well-within-the-default-limit 100x100 image) so both convert.Sniff
// AND convert.Dimensions succeed on it — Dimensions needs the full IHDR
// (signature[8] + length[4] + type[4] + width[4] + height[4]), unlike a bare
// 16-byte Sniff-only prefix which stops mid-chunk.
func pngBytesFixture() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // signature
		0x00, 0x00, 0x00, 0x0D, // chunk length = 13
		0x49, 0x48, 0x44, 0x52, // "IHDR"
		0x00, 0x00, 0x00, 0x64, // width = 100
		0x00, 0x00, 0x00, 0x64, // height = 100
	}
}

// oversizedPNGFixture returns a full PNG signature+IHDR chunk declaring
// width=height=20000 (400 megapixels), over the 100-megapixel default limit.
func oversizedPNGFixture() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // signature
		0x00, 0x00, 0x00, 0x0D, // chunk length = 13
		0x49, 0x48, 0x44, 0x52, // "IHDR"
		0x00, 0x00, 0x4E, 0x20, // width = 20000
		0x00, 0x00, 0x4E, 0x20, // height = 20000
	}
}

// truncatedIHDRPNGFixture is a valid PNG signature (Sniff-passing) followed
// by a chunk whose type is NOT "IHDR" — convert.Dimensions cannot locate the
// declared dimensions and must fail closed (D-07).
func truncatedIHDRPNGFixture() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // signature
		0x00, 0x00, 0x00, 0x0D, // chunk length = 13
		0x00, 0x00, 0x00, 0x00, // NOT "IHDR"
		0x00, 0x00, 0x00, 0x64,
		0x00, 0x00, 0x00, 0x64,
	}
}

// jpegBytesFixture returns the minimal magic-byte prefix that convert.Sniff
// detects as "jpg".
func jpegBytesFixture() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01}
}

// --- document container fixture helpers (mirror internal/convert/docsniff_test.go) ---

// mustWriteZipEntry adds a single deflate-compressed entry to zw, failing
// the test on any write error.
func mustWriteZipEntry(t *testing.T, zw *zip.Writer, name, content string) {
	t.Helper()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		t.Fatalf("CreateHeader(%q): %v", name, err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("Write(%q): %v", name, err)
	}
}

// docxFixture returns a minimal, valid-shaped docx built via archive/zip.Writer
// (word/document.xml root part present) so convert.SniffContainer detects it.
func docxFixture(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	mustWriteZipEntry(t, zw, "[Content_Types].xml", "<Types/>")
	mustWriteZipEntry(t, zw, "word/document.xml", "root part content")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// odtFixture returns a minimal odt: first entry "mimetype", Method=Store,
// payload "application/vnd.oasis.opendocument.text".
func odtFixture(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	fw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatalf("CreateHeader(mimetype): %v", err)
	}
	if _, err := fw.Write([]byte("application/vnd.oasis.opendocument.text")); err != nil {
		t.Fatalf("Write(mimetype): %v", err)
	}
	mustWriteZipEntry(t, zw, "META-INF/manifest.xml", "<manifest/>")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// zipBombDocxFixture returns a docx-shaped zip whose declared
// UncompressedSize64 equals declaredSize, written from real (highly
// compressible, all-zero) content so the physical multipart body stays tiny
// while the declared total exceeds a small test MaxDocumentUncompressedBytes.
func zipBombDocxFixture(t *testing.T, declaredSize uint64) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "word/document.xml", Method: zip.Deflate})
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	chunk := make([]byte, 1<<20) // 1 MiB of zeros, reused per write
	var written uint64
	for written < declaredSize {
		n := declaredSize - written
		if n > uint64(len(chunk)) {
			n = uint64(len(chunk))
		}
		if _, err := w.Write(chunk[:n]); err != nil {
			t.Fatalf("Write: %v", err)
		}
		written += n
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// macroDocxFixture is a valid docx PLUS a word/vbaProject.bin entry.
func macroDocxFixture(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	mustWriteZipEntry(t, zw, "word/document.xml", "root part content")
	mustWriteZipEntry(t, zw, "word/vbaProject.bin", "macro content")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// duplicateRootPartDocxFixture is a zip with two entries named
// word/document.xml -- SniffContainer's fail-closed guard must leave this
// unrecognized rather than accept an ambiguous archive.
func duplicateRootPartDocxFixture(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	mustWriteZipEntry(t, zw, "word/document.xml", "first copy")
	mustWriteZipEntry(t, zw, "word/document.xml", "second copy, different content")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

// bareZipFixture is a plain .zip with no office root parts -- PK-prefixed
// but SniffContainer.Format stays "", so it must fall through to the
// existing unrecognized-content 422.
func bareZipFixture(t *testing.T) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	mustWriteZipEntry(t, zw, "readme.txt", "hello")
	if err := zw.Close(); err != nil {
		t.Fatalf("zw.Close: %v", err)
	}
	return buf.Bytes()
}

func multipartBody(t *testing.T, filename, target string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	return multipartBodyWithOpts(t, filename, target, data, "")
}

// multipartBodyWithOpts is multipartBody plus an optional "opts" form field,
// written only when non-empty -- mirrors the callback_url optional-field
// discipline used by handleCreateJob itself.
func multipartBodyWithOpts(t *testing.T, filename, target string, data []byte, opts string) (*bytes.Buffer, string) {
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
	if opts != "" {
		_ = w.WriteField("opts", opts)
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
	if q.enqueuedImage != repo.createdID {
		t.Errorf("enqueuedImage = %s, want %s", q.enqueuedImage, repo.createdID)
	}
	if q.enqueuedDocument != uuid.Nil {
		t.Errorf("enqueuedDocument = %s, want uuid.Nil (image upload must never touch the document queue)", q.enqueuedDocument)
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

// TestCreateJob_OLECFBRejected verifies SAFE-01/D-05/D-06: a file whose
// content begins with the OLE-CFB magic (legacy binary .doc/.xls/.ppt or a
// password-protected OOXML document — both share the identical header) is
// rejected 422 before any storage write, with a message naming both
// sub-cases and a remedy.
func TestCreateJob_OLECFBRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	cfb := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, []byte("legacy sector padding")...)
	body, ct := multipartBody(t, "in.doc", "pdf", cfb)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload an OLE-CFB document before rejection")
	}
	if repo.created != nil {
		t.Error("must not create job for an OLE-CFB document")
	}
	if !strings.Contains(rec.Body.String(), "password") {
		t.Errorf("body = %s, want a message mentioning password/remedy", rec.Body.String())
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

// TestCreateJob_DimensionLimitExceeded verifies VALID-03/D-04/D-06: an upload
// whose declared pixel dimensions exceed the configured MAX_IMAGE_PIXELS
// limit is rejected 422 before any storage write or job creation.
func TestCreateJob_DimensionLimitExceeded(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20, MaxImagePixels: 1_000_000})

	body, ct := multipartBody(t, "in.png", "webp", oversizedPNGFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload a decompression-bomb-shaped upload before the dimension check")
	}
	if repo.created != nil {
		t.Error("must not create job for an oversized declared-dimension upload")
	}
}

// TestCreateJob_DimensionsUnknown verifies D-07: a Sniff-passing upload whose
// declared dimensions cannot be located within the bounded window fails
// closed with 422, not a fallback accept.
func TestCreateJob_DimensionsUnknown(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.png", "webp", truncatedIHDRPNGFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "cannot determine declared image dimensions") {
		t.Errorf("error = %q, want a message about undeterminable declared dimensions", resp["error"])
	}
	if store.uploaded {
		t.Error("must not upload when declared dimensions cannot be determined")
	}
	if repo.created != nil {
		t.Error("must not create job when declared dimensions cannot be determined")
	}
}

// TestCreateJob_DocumentDetectedAndAccepted verifies DOC-01/D-01/D-02: a
// well-formed docx is structurally detected before any storage write, passes
// the pair-check, and is routed to the document queue with Engine="document"
// -- never the image queue.
func TestCreateJob_DocumentDetectedAndAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	body, ct := multipartBody(t, "in.docx", "pdf", docxFixture(t))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if !store.uploaded {
		t.Error("must upload a supported document pair")
	}
	if store.contentType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Errorf("contentType = %q, want the canonical docx MIME type (D-06 parity with image jobs)", store.contentType)
	}
	if repo.created == nil {
		t.Fatal("must create job for a supported document pair")
	}
	if repo.created.SourceFormat != "docx" || repo.created.TargetFormat != "pdf" {
		t.Errorf("job format = %s -> %s, want docx -> pdf", repo.created.SourceFormat, repo.created.TargetFormat)
	}
	if repo.created.Engine != "document" {
		t.Errorf("job Engine = %q, want document", repo.created.Engine)
	}
	if queue.enqueuedDocument != repo.createdID {
		t.Errorf("enqueuedDocument = %s, want %s", queue.enqueuedDocument, repo.createdID)
	}
	if queue.enqueuedImage != uuid.Nil {
		t.Errorf("enqueuedImage = %s, want uuid.Nil (document upload must never touch the image queue)", queue.enqueuedImage)
	}
}

// TestCreateJob_ODFDetectedAndAccepted proves the ODF disambiguation path
// (index-0 mimetype check) is reached and produces the same
// detected-and-accepted, document-queue-routed outcome as OOXML.
func TestCreateJob_ODFDetectedAndAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	body, ct := multipartBody(t, "in.odt", "pdf", odtFixture(t))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if !store.uploaded {
		t.Error("must upload a supported document pair")
	}
	if store.contentType != "application/vnd.oasis.opendocument.text" {
		t.Errorf("contentType = %q, want the canonical odt MIME type (D-06 parity with image jobs)", store.contentType)
	}
	if repo.created == nil {
		t.Fatal("must create job for a supported document pair")
	}
	if repo.created.SourceFormat != "odt" || repo.created.TargetFormat != "pdf" {
		t.Errorf("job format = %s -> %s, want odt -> pdf", repo.created.SourceFormat, repo.created.TargetFormat)
	}
	if repo.created.Engine != "document" {
		t.Errorf("job Engine = %q, want document", repo.created.Engine)
	}
	if queue.enqueuedDocument != repo.createdID {
		t.Errorf("enqueuedDocument = %s, want %s", queue.enqueuedDocument, repo.createdID)
	}
	if queue.enqueuedImage != uuid.Nil {
		t.Errorf("enqueuedImage = %s, want uuid.Nil (document upload must never touch the image queue)", queue.enqueuedImage)
	}
}

// TestCreateJob_DocumentSkipsDimensionCheck verifies D-06: HasDimensionLimit
// scopes the pixel-dimension check to image formats only, so a document
// upload is accepted even under a MaxImagePixels limit that would reject any
// real image (proving the image-only check is never reached for documents).
func TestCreateJob_DocumentSkipsDimensionCheck(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20, MaxImagePixels: 1})

	body, ct := multipartBody(t, "in.docx", "pdf", docxFixture(t))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (documents must skip the pixel-dimension check); body=%s", rec.Code, rec.Body.String())
	}
}

// TestCreateJob_ZipBombRejected verifies DOC-02/D-04: a docx-shaped upload
// whose declared uncompressed total exceeds the configured
// MaxDocumentUncompressedBytes limit is rejected 422 before any storage
// write, even though the actual compressed bytes transmitted are tiny.
func TestCreateJob_ZipBombRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, resolver, healthyDeps(), Config{
		MaxUploadBytes:               1 << 20,
		MaxDocumentUncompressedBytes: 1 << 20, // 1 MiB test limit
	})

	body, ct := multipartBody(t, "in.docx", "pdf", zipBombDocxFixture(t, 2<<20)) // declares 2 MiB uncompressed
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload a zip-bomb-shaped document before the size check")
	}
	if repo.created != nil {
		t.Error("must not create job for a zip-bomb-shaped document")
	}
}

// TestCreateJob_MacroRejected verifies DOC-03/D-05: a docx containing a
// macro part is rejected 422, unconditionally, before any storage write.
func TestCreateJob_MacroRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.docx", "pdf", macroDocxFixture(t))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload a macro-carrying document")
	}
	if repo.created != nil {
		t.Error("must not create job for a macro-carrying document")
	}
}

// TestCreateJob_DuplicateRootPartRejected verifies the fail-closed guard: a
// docx-shaped zip with two word/document.xml entries is NOT accepted as
// docx -- it falls through to the existing unrecognized-content 422.
func TestCreateJob_DuplicateRootPartRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.docx", "pdf", duplicateRootPartDocxFixture(t))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload a duplicate-root-part document")
	}
	if repo.created != nil {
		t.Error("must not create job for a duplicate-root-part document")
	}
}

// TestCreateJob_BareZipUnrecognized verifies T-08-07: a plain .zip with no
// office root parts is PK-prefixed but must still 422 as unrecognized
// content, never silently accepted.
func TestCreateJob_BareZipUnrecognized(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.zip", "pdf", bareZipFixture(t))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload a bare zip with no office root parts")
	}
	if repo.created != nil {
		t.Error("must not create job for a bare zip with no office root parts")
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

// --- opts tests (OPTS-01/OPTS-02, D-02/D-03/D-04/D-08/D-09) ---

// TestCreateJob_OptsAccepted verifies a valid opts field on a docx->pdf
// request is accepted, normalized, threaded into CreateParams.Opts, and
// echoed back in the 202 response.
func TestCreateJob_OptsAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.docx", "pdf", docxFixture(t), `{"pdf_profile":"pdf/a-2b"}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil {
		t.Fatal("expected job to be created")
	}
	if repo.created.Opts["pdf_profile"] != "pdf/a-2b" {
		t.Errorf("CreateParams.Opts = %+v, want pdf_profile=pdf/a-2b", repo.created.Opts)
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	optsResp, ok := resp["opts"].(map[string]any)
	if !ok || optsResp["pdf_profile"] != "pdf/a-2b" {
		t.Errorf("response opts = %v, want echoed pdf_profile=pdf/a-2b", resp["opts"])
	}
}

// TestCreateJob_OptsUnknownKeyRejected verifies an opts value with an
// unrecognized key is rejected 422 before any storage write (T-14-01).
func TestCreateJob_OptsUnknownKeyRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.docx", "pdf", docxFixture(t), `{"EncryptFile":true}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload before opts validation")
	}
	if repo.created != nil {
		t.Error("must not create job for an unknown-key opts value")
	}
}

// TestCreateJob_OptsInapplicableImage verifies pdf_profile requested on an
// image conversion (png->webp) is rejected 422 (inapplicable).
func TestCreateJob_OptsInapplicableImage(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.png", "webp", pngBytesFixture(), `{"pdf_profile":"pdf/a-2b"}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload before opts applicability validation")
	}
	if repo.created != nil {
		t.Error("must not create job for inapplicable opts on an image conversion")
	}
}

// TestCreateJob_OptsInapplicableTarget verifies pdf_profile requested on a
// docx->odt request (non-pdf target) is rejected 422 (inapplicable).
func TestCreateJob_OptsInapplicableTarget(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.docx", "odt", docxFixture(t), `{"pdf_profile":"pdf/a-2b"}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload before opts applicability validation")
	}
	if repo.created != nil {
		t.Error("must not create job for pdf_profile on a non-pdf target")
	}
}

// TestCreateJob_OptsOversizeRejected verifies an opts field larger than the
// size cap is rejected 422 before parsing (T-14-02). The payload is padded
// with JSON whitespace around a fully VALID pdf_profile, so ONLY the size
// cap can reject it — if the len(rawOpts) > maxOptsBytes guard were deleted,
// the opts would parse and validate cleanly and this test would fail
// (review WR-05: the previous payload also failed the enum allow-list,
// silently masking removal of the cap).
func TestCreateJob_OptsOversizeRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	oversized := `{` + strings.Repeat(" ", 5000) + `"pdf_profile":"pdf/a-2b"}`
	body, ct := multipartBodyWithOpts(t, "in.docx", "pdf", docxFixture(t), oversized)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload an oversized opts field")
	}
	if repo.created != nil {
		t.Error("must not create job for an oversized opts field")
	}
}

// TestCreateJob_OptsInjectionAttempt verifies an API-level attempt to smuggle
// a second filter property inside the pdf_profile value itself is rejected
// 422 -- the closed allow-list check in ParseDocOpts rejects anything other
// than the exact "pdf/a-2b" string, proving adversarial bytes never reach
// storage or the CreateParams.Opts persisted value (T-14-01).
func TestCreateJob_OptsInjectionAttempt(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	injection := `{"pdf_profile":"pdf/a-2b\",\"EncryptFile\":true"}`
	body, ct := multipartBodyWithOpts(t, "in.docx", "pdf", docxFixture(t), injection)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload before opts validation rejects the injection attempt")
	}
	if repo.created != nil {
		t.Error("must not create job for an opts injection attempt")
	}
}

// TestCreateJob_NoOptsResponseUnchanged is the D-09 regression: a no-opts
// POST response contains no "opts" key and CreateParams.Opts stays nil.
func TestCreateJob_NoOptsResponseUnchanged(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBody(t, "in.png", "webp", pngBytesFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil || repo.created.Opts != nil {
		t.Errorf("CreateParams.Opts = %+v, want nil (no opts field supplied)", repo.created)
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, present := resp["opts"]; present {
		t.Errorf("response = %v, want no \"opts\" key when no opts were supplied", resp)
	}
}

// TestCreateJob_EmptyOptsObjectTreatedAsNoOpts verifies the literal "{}"
// opts value is treated identically to an absent opts field (D-09).
func TestCreateJob_EmptyOptsObjectTreatedAsNoOpts(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.png", "webp", pngBytesFixture(), "{}")
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, present := resp["opts"]; present {
		t.Errorf("response = %v, want no \"opts\" key for opts={}", resp)
	}
}

// TestGetJob_OptsEcho verifies a job with stored opts echoes them in the GET
// response (D-09).
func TestGetJob_OptsEcho(t *testing.T) {
	id := uuid.New()
	resolver := newFakeResolver()
	repo := &fakeRepo{getJob: &jobs.Job{
		ID:       id,
		ClientID: resolver.client.ID,
		Status:   jobs.StatusQueued,
		Opts:     map[string]any{"pdf_profile": "pdf/a-2b"},
	}}
	srv := NewServer(repo, &fakeStorage{}, &fakeQueue{}, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	req := authed(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id.String(), nil))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	optsResp, ok := resp["opts"].(map[string]any)
	if !ok || optsResp["pdf_profile"] != "pdf/a-2b" {
		t.Errorf("response opts = %v, want echoed pdf_profile=pdf/a-2b", resp["opts"])
	}
}

// TestGetJob_NoOptsOmitted verifies a job without stored opts omits the
// "opts" key entirely (D-09 omitempty; existing no-opts responses unchanged).
func TestGetJob_NoOptsOmitted(t *testing.T) {
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
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if _, present := resp["opts"]; present {
		t.Errorf("response = %v, want no \"opts\" key for a job without stored opts", resp)
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
