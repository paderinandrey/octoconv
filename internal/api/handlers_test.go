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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/presets"
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
	// uploadedBytes captures the full stream s.storage.Upload received, so
	// tests can assert byte-for-byte integrity (T-31-02: the SniffAudio
	// truncation trap must never drop the leading bytes of an audio upload).
	uploadedBytes []byte
}

func (f *fakeStorage) Upload(_ context.Context, _ string, r io.Reader, _ int64, contentType string) error {
	b, _ := io.ReadAll(r)
	f.uploadedBytes = b
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
	enqueuedAudio    uuid.UUID
	enqueuedAV       uuid.UUID
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

func (f *fakeQueue) EnqueueAudioConvert(_ context.Context, id uuid.UUID) error {
	f.enqueuedAudio = id
	return nil
}

func (f *fakeQueue) EnqueueAVConvert(_ context.Context, id uuid.UUID) error {
	f.enqueuedAV = id
	return nil
}

// fakePresetRepo implements api.PresetRepo. It supports a DIFFERENT result on
// the SECOND (and later) Resolve call so the pre-Create active re-check
// (Pitfall 8) can be simulated: resolve/resolveErr are the default result
// returned on every call; when recheckSet is true, the second-and-later call
// returns recheckResolve/recheckErr instead.
type fakePresetRepo struct {
	resolve    *presets.Preset
	resolveErr error

	recheckSet     bool
	recheckResolve *presets.Preset
	recheckErr     error

	calls int
}

func (f *fakePresetRepo) Resolve(_ context.Context, _ uuid.UUID, _ string) (*presets.Preset, error) {
	f.calls++
	if f.calls >= 2 && f.recheckSet {
		return f.recheckResolve, f.recheckErr
	}
	return f.resolve, f.resolveErr
}

// defaultFakePresetRepo returns presets.ErrNotFound on every call -- the
// no-preset default used by every existing (pre-18-03) test so their
// behavior is unaffected by the new NewServer positional argument.
func defaultFakePresetRepo() *fakePresetRepo {
	return &fakePresetRepo{resolveErr: presets.ErrNotFound}
}

// fakePresetAdmin implements api.PresetAdmin (D-08/Task 2). It supports
// settable return values per method plus a captured lastCreateParams so
// tests can assert the mass-assignment guard (T-20-01): a request body
// attempting to smuggle scope/client_id never reaches the repo layer with
// anything other than the ctx-derived values.
type fakePresetAdmin struct {
	createErr error

	updateVersion int
	updateErr     error

	deactivateErr error

	getResult *presets.Preset
	getErr    error

	listResult []presets.Preset
	listErr    error

	getForClientResult *presets.Preset
	getForClientErr    error

	listForClientResult []presets.Preset
	listForClientErr    error

	lastCreateParams *presets.CreateParams
}

func (f *fakePresetAdmin) Create(_ context.Context, p presets.CreateParams) (uuid.UUID, int, error) {
	f.lastCreateParams = &p
	if f.createErr != nil {
		return uuid.Nil, 0, f.createErr
	}
	return uuid.New(), 1, nil
}

func (f *fakePresetAdmin) Update(_ context.Context, _ string, _ *uuid.UUID, _, _ string, _ map[string]any, _ string) (int, error) {
	if f.updateErr != nil {
		return 0, f.updateErr
	}
	if f.updateVersion == 0 {
		return 2, nil
	}
	return f.updateVersion, nil
}

func (f *fakePresetAdmin) Deactivate(_ context.Context, _ string, _ *uuid.UUID, _ string) error {
	return f.deactivateErr
}

func (f *fakePresetAdmin) Get(_ context.Context, _ string, _ *uuid.UUID, _ string) (*presets.Preset, error) {
	return f.getResult, f.getErr
}

func (f *fakePresetAdmin) List(_ context.Context, _ string, _ *uuid.UUID, _ bool) ([]presets.Preset, error) {
	return f.listResult, f.listErr
}

func (f *fakePresetAdmin) ListForClient(_ context.Context, _ *uuid.UUID, _ bool) ([]presets.Preset, error) {
	return f.listForClientResult, f.listForClientErr
}

func (f *fakePresetAdmin) GetForClient(_ context.Context, _ *uuid.UUID, _ string) (*presets.Preset, error) {
	return f.getForClientResult, f.getForClientErr
}

// defaultFakePresetAdmin returns presets.ErrNotFound on every read -- the
// no-op default used by every existing (pre-20-01) test so their behavior is
// unaffected by the new NewServer positional argument.
func defaultFakePresetAdmin() *fakePresetAdmin {
	return &fakePresetAdmin{getErr: presets.ErrNotFound, getForClientErr: presets.ErrNotFound}
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

// paddedPNGFixture returns pngBytesFixture() followed by size-24 zero bytes,
// giving D-07 ceiling tests (35-04) control over the PHYSICAL upload size
// (header.Size) independent of the small declared IHDR dimensions
// convert.Dimensions reads from the front of the stream -- padding never
// disturbs Sniff (fixed 12-byte window) or Dimensions (stops after the
// width/height fields). Returns pngBytesFixture() unchanged if size is not
// larger than it already is.
func paddedPNGFixture(size int) []byte {
	base := pngBytesFixture()
	if size <= len(base) {
		return base
	}
	out := make([]byte, size)
	copy(out, base)
	return out
}

// mp4BytesFixture returns the minimal ftyp+brand box matchMP4 (avsniff.go)
// detects as "mp4": a 4-byte box-size field (value unused by the matcher)
// followed by "ftyp" and the allowlisted major brand "isom".
func mp4BytesFixture() []byte {
	return []byte{
		0x00, 0x00, 0x00, 0x18, // box size (declared, ignored by matchMP4)
		0x66, 0x74, 0x79, 0x70, // "ftyp"
		0x69, 0x73, 0x6F, 0x6D, // "isom" -- mp4VideoBrands entry
	}
}

// paddedMP4Fixture mirrors paddedPNGFixture for the mp4 magic-byte prefix.
func paddedMP4Fixture(size int) []byte {
	base := mp4BytesFixture()
	if size <= len(base) {
		return base
	}
	out := make([]byte, size)
	copy(out, base)
	return out
}

// buildEBMLFixture constructs a byte-exact EBML header fixture for docType
// ("matroska" or "webm"), matching convert.SniffVideo's matchEBML parser
// exactly -- mirrors internal/convert/avsniff_test.go's own buildEBMLHeader
// helper (unexported to that package, so duplicated here rather than
// imported) so mkv/webm detection can be exercised end-to-end through
// handleCreateJob with a committed byte-slice literal, no binary fixture
// dependency (per the plan's stated preference). docType must be shorter
// than 16 bytes so its SIZE vint fits in a single byte (0x80|len).
func buildEBMLFixture(docType string) []byte {
	body := []byte{}
	body = append(body, 0x42, 0x86, 0x81, 0x01) // EBMLVersion = 1
	body = append(body, 0x42, 0xf7, 0x81, 0x01) // EBMLReadVersion = 1
	body = append(body, 0x42, 0xf2, 0x81, 0x04) // EBMLMaxIDLength = 4
	body = append(body, 0x42, 0xf3, 0x81, 0x08) // EBMLMaxSizeLength = 8
	body = append(body, 0x42, 0x82, byte(0x80|len(docType)))
	body = append(body, []byte(docType)...)
	body = append(body, 0x42, 0x87, 0x81, 0x04) // DocTypeVersion = 4
	body = append(body, 0x42, 0x85, 0x81, 0x02) // DocTypeReadVersion = 2

	header := []byte{0x1A, 0x45, 0xDF, 0xA3} // ebmlMagic (avsniff.go)
	header = append(header, byte(0x80|len(body)))
	header = append(header, body...)
	return header
}

// mkvBytesFixture returns a minimal, valid EBML header with DocType
// "matroska" -- convert.SniffVideo detects this as "mkv".
func mkvBytesFixture() []byte {
	return buildEBMLFixture("matroska")
}

// webmBytesFixture returns a minimal, valid EBML header with DocType
// "webm" -- convert.SniffVideo detects this as "webm".
func webmBytesFixture() []byte {
	return buildEBMLFixture("webm")
}

// avTestTarget is a synthetic, never-real target format string. It exists
// solely so fakeAVConverter's (mp4, avTestTarget) pair cannot collide with
// any real registration -- not AudioConverter's video-to-transcript pairs
// (D-04, mp4 -> txt/srt/vtt/json), and not the real AVConverter's own pairs
// (av.go), which is now registered into convert.Default (D-08, 35-06).
const avTestTarget = "avtestout"

// fakeAVConverter is a minimal convert.Converter registered ONLY so D-07
// ceiling/enqueue-switch tests (35-04) can exercise engine=="av" end-to-end
// through handleCreateJob via a collision-free synthetic pair, independent
// of whichever real (source, target) pairs the actual AVConverter happens to
// support.
type fakeAVConverter struct{}

func (fakeAVConverter) Pairs() []convert.Pair {
	return []convert.Pair{{From: "mp4", To: avTestTarget}}
}

func (fakeAVConverter) Convert(context.Context, string, string, map[string]any) error {
	return nil
}

func (fakeAVConverter) Engine() string { return convert.EngineAV }

func init() {
	convert.Default.Register(fakeAVConverter{})
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

// multipartBodyWithPreset writes a "preset" form field instead of "target",
// and an optional "opts" form field -- used by the D-01 mutual-exclusivity
// tests (preset+target, preset+opts) and the preset-resolution tests.
func multipartBodyWithPreset(t *testing.T, filename, preset, target string, data []byte, opts string) (*bytes.Buffer, string) {
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
	if preset != "" {
		_ = w.WriteField("preset", preset)
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
// fixed client id. The PresetRepo slot defaults to ErrNotFound on every call
// so existing non-preset tests are unaffected (D-09/Task 3).
func newTestServer(repo Repo, store Storage, q Enqueuer) (*Server, *fakeResolver) {
	resolver := newFakeResolver()
	return NewServer(repo, store, q, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20}), resolver
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

// TestCreateJob_OLECFBRejected verifies SAFE-01/D-05/Pitfall 11: a
// synthetic OLE-CFB-magic file whose directory does not decode to any known
// stream name classifies as convert.CFBUnknown and therefore still falls
// through to the ORIGINAL combined 422 message unchanged (fail-closed
// compat) — this is the CFBUnknown case of the 22-cfb-classification D-06
// three-way split; see TestCreateJob_CFB below for the CFBEncrypted/
// CFBLegacy cases over real fixtures.
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

// TestCreateJob_CFB verifies the 22-cfb-classification D-06 three-way split
// over the real fixtures (internal/api/testdata/, copied from
// internal/e2e/testdata/): a password-protected OOXML .docx classifies
// CFBEncrypted and gets its own distinct 422, and a legacy binary .doc
// classifies CFBLegacy and gets a DIFFERENT distinct 422 that must not
// mention "password" -- proving the two messages are actually
// distinguishable, not just both non-empty. Both must reject before any
// storage write or job creation (Pitfall 11 fail-closed).
func TestCreateJob_CFB(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		wantSubstr []string
		wantAbsent []string
	}{
		{
			name:       "CFBEncrypted",
			fixture:    "encrypted.docx",
			wantSubstr: []string{"remove the password", "password-protected"},
		},
		{
			name:       "CFBLegacy",
			fixture:    "legacy.doc",
			wantSubstr: []string{"legacy binary", ".doc/.xls/.ppt"},
			wantAbsent: []string{"password"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + tc.fixture)
			if err != nil {
				t.Fatalf("read fixture %s: %v", tc.fixture, err)
			}

			repo := &fakeRepo{}
			store := &fakeStorage{}
			srv, _ := newTestServer(repo, store, &fakeQueue{})

			ext := tc.fixture[strings.LastIndex(tc.fixture, "."):]
			body, ct := multipartBody(t, "in"+ext, "pdf", data)
			req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
			req.Header.Set("Content-Type", ct)
			rec := httptest.NewRecorder()

			srv.Routes().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
			}
			if store.uploaded {
				t.Errorf("must not upload %s before rejection", tc.fixture)
			}
			if repo.created != nil {
				t.Errorf("must not create job for %s", tc.fixture)
			}
			got := rec.Body.String()
			for _, want := range tc.wantSubstr {
				if !strings.Contains(got, want) {
					t.Errorf("body = %s, want substring %q", got, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("body = %s, must NOT contain %q (distinct-message proof)", got, absent)
				}
			}
		})
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
	srv := NewServer(repo, store, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20, MaxImagePixels: 1_000_000})

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
	srv := NewServer(repo, store, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20, MaxImagePixels: 1})

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
	srv := NewServer(repo, store, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{
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
			srv := NewServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, tc.health, Config{MaxUploadBytes: 1 << 20})

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
	srv := NewServer(repo, store, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

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
	srv := NewServer(repo, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

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
	srv := NewServer(repo, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

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
	srv := NewServer(repo, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	req := authed(httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id.String(), nil))
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// --- html engine tests (Plan 03: HTML-01/02/03) ---

// htmlFixture returns a small, genuinely-HTML document (valid UTF-8, no NUL,
// starts with <!doctype html> after trimming) that convert.LooksLikeHTML
// accepts.
func htmlFixture() []byte {
	return []byte("<!doctype html><html><head></head><body><p>hello</p></body></html>")
}

// TestCreateJob_HTMLDetectedAndAccepted proves a valid .html upload targeting
// pdf is detected (fail-closed content check, D-07), routed to the html
// engine, and enqueued via EnqueueHTMLConvert (not any other queue).
func TestCreateJob_HTMLDetectedAndAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	body, ct := multipartBody(t, "in.html", "pdf", htmlFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if !store.uploaded {
		t.Error("must upload a valid html upload")
	}
	if store.contentType != "text/html" {
		t.Errorf("contentType = %q, want text/html", store.contentType)
	}
	if repo.created == nil {
		t.Fatal("must create job for a valid html -> pdf conversion")
	}
	if repo.created.SourceFormat != "html" || repo.created.TargetFormat != "pdf" {
		t.Errorf("job format = %s -> %s, want html -> pdf", repo.created.SourceFormat, repo.created.TargetFormat)
	}
	if repo.created.Engine != "html" {
		t.Errorf("job Engine = %q, want html", repo.created.Engine)
	}
	if queue.enqueuedHTML != repo.createdID {
		t.Errorf("enqueuedHTML = %s, want %s", queue.enqueuedHTML, repo.createdID)
	}
	if queue.enqueuedDocument != uuid.Nil {
		t.Errorf("enqueuedDocument = %s, want uuid.Nil (html upload must never touch the document queue)", queue.enqueuedDocument)
	}
	if queue.enqueuedImage != uuid.Nil {
		t.Errorf("enqueuedImage = %s, want uuid.Nil (html upload must never touch the image queue)", queue.enqueuedImage)
	}
}

// TestCreateJob_HTMExtensionAliasAccepted proves the htm->html NormalizeFormat
// alias (Plan 01) lets a .htm-named upload with genuine HTML content route
// the same way a .html-named upload does.
func TestCreateJob_HTMExtensionAliasAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	body, ct := multipartBody(t, "in.htm", "pdf", htmlFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil || repo.created.SourceFormat != "html" {
		t.Fatalf("must create job with SourceFormat=html for a .htm upload, got %+v", repo.created)
	}
}

// TestCreateJob_HTMLContentRejected proves a .html-named upload whose content
// fails LooksLikeHTML (binary/non-HTML content masquerading as html, T-15-09)
// is rejected 422 BEFORE any storage write -- fail-closed, D-07.
func TestCreateJob_HTMLContentRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	// Binary garbage, NOT valid HTML text (contains NUL bytes).
	garbage := []byte{0x00, 0x01, 0x02, 0x03, 0xFF, 0xFE, 0x00, 0x00}
	body, ct := multipartBody(t, "in.html", "pdf", garbage)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload content that fails the HTML content check")
	}
	if repo.created != nil {
		t.Error("must not create job for content that fails the HTML content check")
	}
}

// TestCreateJob_HTMLOptsAccepted verifies a valid page_size/margin_mm/
// landscape/print_background opts payload is validated, persisted
// (normalized), and echoed in the response (HTML-03).
func TestCreateJob_HTMLOptsAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.html", "pdf", htmlFixture(), `{"page_size":"a4","margin_mm":10,"landscape":true,"print_background":true}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil {
		t.Fatal("must create job for a valid html opts payload")
	}
	if repo.created.Opts["page_size"] != "a4" {
		t.Errorf("CreateParams.Opts = %+v, want page_size=a4", repo.created.Opts)
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	optsResp, ok := resp["opts"].(map[string]any)
	if !ok || optsResp["page_size"] != "a4" {
		t.Errorf("response opts = %v, want echoed page_size=a4", resp["opts"])
	}
}

// TestCreateJob_HTMLOptsUnknownFieldRejected verifies an unknown opts field
// on an html job is rejected 422 before storage.
func TestCreateJob_HTMLOptsUnknownFieldRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.html", "pdf", htmlFixture(), `{"unknown_field":true}`)
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
		t.Error("must not create job for an unknown-key html opts value")
	}
}

// TestCreateJob_HTMLOptsMarginOutOfRangeRejected verifies an out-of-range
// margin_mm value on an html job is rejected 422 before storage.
func TestCreateJob_HTMLOptsMarginOutOfRangeRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.html", "pdf", htmlFixture(), `{"margin_mm":9999}`)
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
		t.Error("must not create job for an out-of-range margin_mm value")
	}
}

// TestCreateJob_DocumentOptsOnHTMLRejected verifies a document-only opts
// value (pdf_profile) requested on an html job is rejected 422 -- proves the
// engine-keyed opts dispatch does not accidentally accept DocOpts fields on
// an html conversion (HTML-03).
func TestCreateJob_DocumentOptsOnHTMLRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.html", "pdf", htmlFixture(), `{"pdf_profile":"pdf/a-2b"}`)
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
		t.Error("must not create job for a document opts value (pdf_profile) on an html job")
	}
}

// --- preset resolution tests (Plan 18-03: PRST-02/03/04, D-01/D-03/D-06/D-07/D-08, Pitfall 8) ---

// TestCreateJob_PresetResolvedImage verifies PRST-02/D-07/D-08: preset=<name>
// (no target) resolves to the preset's target_format, converts, and persists
// provenance into CreateParams.
func TestCreateJob_PresetResolvedImage(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	q := &fakeQueue{}
	resolver := newFakeResolver()
	presetRepo := &fakePresetRepo{resolve: &presets.Preset{
		ID: uuid.New(), Name: "thumb", Version: 2, TargetFormat: "webp",
	}}
	srv := NewServer(repo, store, q, presetRepo, defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	body, ct := multipartBodyWithPreset(t, "in.png", "thumb", "", pngBytesFixture(), "")
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
	if repo.created.TargetFormat != "webp" {
		t.Errorf("TargetFormat = %q, want webp (preset-resolved)", repo.created.TargetFormat)
	}
	if repo.created.PresetName != "thumb" || repo.created.PresetVersion != 2 {
		t.Errorf("provenance = %+v, want PresetName=thumb PresetVersion=2", repo.created)
	}
	if q.enqueuedImage != repo.createdID {
		t.Errorf("enqueuedImage = %s, want %s", q.enqueuedImage, repo.createdID)
	}
}

// TestCreateJob_PresetAndTargetMutuallyExclusive verifies D-01: supplying
// preset together with an explicit target is rejected 422, before any
// upload or preset lookup.
func TestCreateJob_PresetAndTargetMutuallyExclusive(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	presetRepo := &fakePresetRepo{resolve: &presets.Preset{ID: uuid.New(), Name: "thumb", Version: 1, TargetFormat: "webp"}}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, presetRepo, defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	body, ct := multipartBodyWithPreset(t, "in.png", "thumb", "webp", pngBytesFixture(), "")
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload for preset+target")
	}
	if repo.created != nil {
		t.Error("must not create job for preset+target")
	}
	if presetRepo.calls != 0 {
		t.Errorf("presetRepo.calls = %d, want 0 (mutual-exclusivity gate runs before any preset lookup)", presetRepo.calls)
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["error"], "either preset or target") {
		t.Errorf("error = %q, want a mutual-exclusivity message", resp["error"])
	}
}

// TestCreateJob_PresetAndOptsMutuallyExclusive verifies D-01: supplying
// preset together with a non-empty opts field is rejected 422.
func TestCreateJob_PresetAndOptsMutuallyExclusive(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	presetRepo := &fakePresetRepo{resolve: &presets.Preset{ID: uuid.New(), Name: "thumb", Version: 1, TargetFormat: "webp"}}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, presetRepo, defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	body, ct := multipartBodyWithPreset(t, "in.png", "thumb", "", pngBytesFixture(), `{"pdf_profile":"pdf/a-2b"}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload for preset+opts")
	}
	if repo.created != nil {
		t.Error("must not create job for preset+opts")
	}
}

// TestCreateJob_UnknownPreset422NoLeak verifies D-03/Pitfall 12: a
// nonexistent preset AND a (simulated) cross-client/inactive preset both
// collapse to ErrNotFound inside Resolve and return the IDENTICAL 422 body
// -- byte-for-byte, not just the same status code.
func TestCreateJob_UnknownPreset422NoLeak(t *testing.T) {
	cases := []struct {
		name       string
		presetRepo *fakePresetRepo
	}{
		{name: "nonexistent", presetRepo: &fakePresetRepo{resolveErr: presets.ErrNotFound}},
		{name: "cross-client-or-inactive", presetRepo: &fakePresetRepo{resolveErr: presets.ErrNotFound}},
	}

	bodies := make([][]byte, 0, len(cases))
	for _, tc := range cases {
		repo := &fakeRepo{}
		store := &fakeStorage{}
		resolver := newFakeResolver()
		srv := NewServer(repo, store, &fakeQueue{}, tc.presetRepo, defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

		body, ct := multipartBodyWithPreset(t, "in.png", "ghost", "", pngBytesFixture(), "")
		req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
		req.Header.Set("Content-Type", ct)
		rec := httptest.NewRecorder()

		srv.Routes().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d, want 422; body=%s", tc.name, rec.Code, rec.Body.String())
		}
		var resp map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp["error"] != errUnknownPreset {
			t.Errorf("%s: error = %q, want %q", tc.name, resp["error"], errUnknownPreset)
		}
		if repo.created != nil {
			t.Errorf("%s: must not create job for an unresolvable preset", tc.name)
		}
		bodies = append(bodies, rec.Body.Bytes())
	}
	if !bytes.Equal(bodies[0], bodies[1]) {
		t.Errorf("nonexistent vs cross-client/inactive response bodies differ: %q vs %q (D-03/Pitfall 12 no-leak violation)", bodies[0], bodies[1])
	}
}

// TestCreateJob_PresetOptsRevalidated verifies D-06: a preset's stored opts
// (containing a field the current allowlist rejects) are re-run through
// ParseDocOpts at USE time, not trusted -- a stale preset fails job creation.
func TestCreateJob_PresetOptsRevalidated(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	presetRepo := &fakePresetRepo{resolve: &presets.Preset{
		ID: uuid.New(), Name: "stale", Version: 1, TargetFormat: "pdf",
		Options: map[string]any{"EncryptFile": true}, // unknown key, since-invalidated
	}}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, presetRepo, defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	body, ct := multipartBodyWithPreset(t, "in.docx", "stale", "", docxFixture(t), "")
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload before opts re-validation rejects a stale preset")
	}
	if repo.created != nil {
		t.Error("must not create job for a preset with since-invalidated opts")
	}
}

// TestCreateJob_PresetDeactivatedDuringCreate verifies Pitfall 8: the preset
// resolves successfully on the FIRST Resolve (so resolution, opts
// validation, and upload all succeed), but the pre-Create re-check
// (SECOND Resolve) reports ErrNotFound -- simulating deactivation in the
// resolve-to-insert window. The job must be rejected 422 and repo.Create
// must NEVER be invoked; the already-uploaded object is left in place
// (mirrors the existing repo.Create-failure no-cleanup path).
func TestCreateJob_PresetDeactivatedDuringCreate(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	presetRepo := &fakePresetRepo{
		resolve:    &presets.Preset{ID: uuid.New(), Name: "thumb", Version: 1, TargetFormat: "webp"},
		recheckSet: true,
		recheckErr: presets.ErrNotFound,
	}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, presetRepo, defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	body, ct := multipartBodyWithPreset(t, "in.png", "thumb", "", pngBytesFixture(), "")
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["error"] != errUnknownPreset {
		t.Errorf("error = %q, want %q", resp["error"], errUnknownPreset)
	}
	if repo.created != nil {
		t.Error("repo.Create must NEVER be invoked when the preset is deactivated between resolve and insert")
	}
	if !store.uploaded {
		t.Error("upload happens before the pre-Create re-check; the object is left uploaded for TTL cleanup")
	}
	if presetRepo.calls < 2 {
		t.Errorf("presetRepo.calls = %d, want >= 2 (resolution + pre-Create re-check)", presetRepo.calls)
	}
}

// TestCreateJob_PresetHTMLOptsResolved proves the engine-keyed opts dispatch
// handles preset-sourced HTML opts identically to ad-hoc HTML opts.
func TestCreateJob_PresetHTMLOptsResolved(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	q := &fakeQueue{}
	presetRepo := &fakePresetRepo{resolve: &presets.Preset{
		ID: uuid.New(), Name: "a4pdf", Version: 1, TargetFormat: "pdf",
		Options: map[string]any{"page_size": "a4"},
	}}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, q, presetRepo, defaultFakePresetAdmin(), resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20})

	body, ct := multipartBodyWithPreset(t, "in.html", "a4pdf", "", htmlFixture(), "")
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil || repo.created.Opts["page_size"] != "a4" {
		t.Errorf("CreateParams.Opts = %+v, want page_size=a4", repo.created)
	}
	if q.enqueuedHTML != repo.createdID {
		t.Errorf("enqueuedHTML = %s, want %s", q.enqueuedHTML, repo.createdID)
	}
}

// --- audio engine tests (Plan 03: AUD-05) ---

// audioMP3Fixture reads the real fixture with a large ID3v2 tag, shared with
// internal/convert's own SniffAudio tests. Its whole size sits comfortably
// under mp3PeekLen (512 KiB) AND the 1 MiB newTestServer MaxUploadBytes
// default, so the entire file is read by SniffAudio's io.ReadFull peek in
// one shot.
func audioMP3Fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("../convert/testdata/audio/sample-id3.mp3")
	if err != nil {
		t.Fatalf("read audio fixture: %v", err)
	}
	return data
}

// TestCreateJob_AudioDetectedAndAccepted proves an mp3 upload with a large
// ID3v2 tag is detected by SniffAudio, routed to engine "audio", enqueued via
// EnqueueAudioConvert (and NONE of the other three queues), and -- the
// regression a naive "content validation passed" assertion would NOT catch
// (T-31-02) -- the object handed to storage.Upload is byte-identical to the
// uploaded file, proving SniffAudio was chained off `rest` and not `file`
// (which would have silently dropped the first sniffLen=12 bytes).
func TestCreateJob_AudioDetectedAndAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	data := audioMP3Fixture(t)
	body, ct := multipartBody(t, "in.mp3", "txt", data)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if !store.uploaded {
		t.Fatal("must upload a valid mp3 upload")
	}
	if store.contentType != "audio/mpeg" {
		t.Errorf("contentType = %q, want audio/mpeg", store.contentType)
	}
	if !bytes.Equal(store.uploadedBytes, data) {
		t.Fatalf("stored object (%d bytes) != uploaded bytes (%d bytes) -- SniffAudio must chain off `rest`, not `file` (T-31-02 truncation trap)", len(store.uploadedBytes), len(data))
	}
	if repo.created == nil {
		t.Fatal("must create job for a valid mp3 -> txt conversion")
	}
	if repo.created.SourceFormat != "mp3" || repo.created.TargetFormat != "txt" {
		t.Errorf("job format = %s -> %s, want mp3 -> txt", repo.created.SourceFormat, repo.created.TargetFormat)
	}
	if repo.created.Engine != "audio" {
		t.Errorf("job Engine = %q, want audio", repo.created.Engine)
	}
	if queue.enqueuedAudio != repo.createdID {
		t.Errorf("enqueuedAudio = %s, want %s", queue.enqueuedAudio, repo.createdID)
	}
	if queue.enqueuedHTML != uuid.Nil {
		t.Errorf("enqueuedHTML = %s, want uuid.Nil (mp3 upload must never touch the html queue)", queue.enqueuedHTML)
	}
	if queue.enqueuedDocument != uuid.Nil {
		t.Errorf("enqueuedDocument = %s, want uuid.Nil (mp3 upload must never touch the document queue)", queue.enqueuedDocument)
	}
	if queue.enqueuedImage != uuid.Nil {
		t.Errorf("enqueuedImage = %s, want uuid.Nil (mp3 upload must never touch the image queue)", queue.enqueuedImage)
	}
}

// TestCreateJob_AudioOptsAccepted proves an audio job carrying
// {"language":"ru"} opts is accepted via the dedicated EngineAudio opts case
// (ParseAudioOpts) rather than being rejected as an invalid DocOpts (T-31-04)
// -- the confirmed opts mis-routing bug this plan closes.
func TestCreateJob_AudioOptsAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	body, ct := multipartBodyWithOpts(t, "in.mp3", "txt", audioMP3Fixture(t), `{"language":"ru"}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (audio opts must be validated via ParseAudioOpts, not rejected as DocOpts); body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil || repo.created.Opts["language"] != "ru" {
		t.Errorf("CreateParams.Opts = %+v, want language=ru", repo.created)
	}
	if queue.enqueuedAudio != repo.createdID {
		t.Errorf("enqueuedAudio = %s, want %s", queue.enqueuedAudio, repo.createdID)
	}
}

// TestCreateJob_AudioOptsRejectedForWrongLanguage proves ParseAudioOpts's
// closed language allowlist is actually enforced through the API path (not
// silently bypassed), mirroring the HTML/Doc opts-applicability tests'
// discipline.
func TestCreateJob_AudioOptsRejectedForWrongLanguage(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.mp3", "txt", audioMP3Fixture(t), `{"language":"zz"}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if repo.created != nil {
		t.Error("repo.Create must never run for an unsupported language")
	}
}

// --- av engine detection/routing tests (Plan 06: D-08 SniffVideo wiring, AVE-03/AVT-01) ---

// TestCreateJob_MKVDetectedAndAccepted proves an mkv upload (undetectable
// before this plan, since SniffVideo had zero non-test callers) is now
// detected via the D-08 SniffVideo chain link, routed to the REAL
// convert.Default-registered AVConverter's "av" engine (transcode mkv->mp4
// is one of AVConverter's locked Pairs, av.go), enqueued via
// EnqueueAVConvert, and -- proving the rest-rebinding fix did not truncate
// the upload -- the object handed to storage.Upload is byte-identical to the
// uploaded file.
func TestCreateJob_MKVDetectedAndAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	data := mkvBytesFixture()
	body, ct := multipartBody(t, "in.mkv", "mp4", data)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (mkv must be detected via SniffVideo, not rejected as unrecognized content); body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(store.uploadedBytes, data) {
		t.Fatalf("stored object (%d bytes) != uploaded bytes (%d bytes) -- SniffVideo's rest must be rebound even on empty video-detection lookahead by SniffAudio, and preserved on a genuine mkv match", len(store.uploadedBytes), len(data))
	}
	if repo.created == nil {
		t.Fatal("must create job for a valid mkv -> mp4 conversion")
	}
	if repo.created.SourceFormat != "mkv" || repo.created.TargetFormat != "mp4" {
		t.Errorf("job format = %s -> %s, want mkv -> mp4", repo.created.SourceFormat, repo.created.TargetFormat)
	}
	if repo.created.Engine != convert.EngineAV {
		t.Errorf("job Engine = %q, want av", repo.created.Engine)
	}
	if queue.enqueuedAV != repo.createdID {
		t.Errorf("enqueuedAV = %s, want %s", queue.enqueuedAV, repo.createdID)
	}
	if queue.enqueuedAudio != uuid.Nil {
		t.Errorf("enqueuedAudio = %s, want uuid.Nil (mkv->mp4 transcode must never touch the audio queue)", queue.enqueuedAudio)
	}
}

// TestCreateJob_WebMDetectedAndAccepted mirrors
// TestCreateJob_MKVDetectedAndAccepted for the webm DocType -- both mkv and
// webm share the identical EBML magic and can only be disambiguated by the
// DocType element matchEBML walks to (avsniff.go).
func TestCreateJob_WebMDetectedAndAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	data := webmBytesFixture()
	body, ct := multipartBody(t, "in.webm", "mp4", data)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (webm must be detected via SniffVideo, not rejected as unrecognized content); body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(store.uploadedBytes, data) {
		t.Fatalf("stored object (%d bytes) != uploaded bytes (%d bytes)", len(store.uploadedBytes), len(data))
	}
	if repo.created == nil {
		t.Fatal("must create job for a valid webm -> mp4 conversion")
	}
	if repo.created.SourceFormat != "webm" || repo.created.TargetFormat != "mp4" {
		t.Errorf("job format = %s -> %s, want webm -> mp4", repo.created.SourceFormat, repo.created.TargetFormat)
	}
	if repo.created.Engine != convert.EngineAV {
		t.Errorf("job Engine = %q, want av", repo.created.Engine)
	}
	if queue.enqueuedAV != repo.createdID {
		t.Errorf("enqueuedAV = %s, want %s", queue.enqueuedAV, repo.createdID)
	}
}

// TestCreateJob_MP4StillDetectedBySniffPrefixTable proves the D-08 SniffVideo
// wiring does not disturb mp4 detection: mp4 is caught by Sniff()'s
// fixed-12-byte-window signatures table (matchMP4, avsniff.go) earlier in
// the chain, so the `detected == ""` gate guarding the new SniffVideo call
// is already false by the time execution reaches it -- SniffVideo never
// runs for mp4. Routed through the audio-extract pair (mp4 -> mp3, one of
// AVConverter's locked Pairs) so this exercises the REAL registered
// converter, not the test-only fakeAVConverter's synthetic pair.
func TestCreateJob_MP4StillDetectedBySniffPrefixTable(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	data := mp4BytesFixture()
	body, ct := multipartBody(t, "in.mp4", "mp3", data)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil || repo.created.SourceFormat != "mp4" {
		t.Fatalf("job SourceFormat = %+v, want mp4 (Sniff's prefix table must still win, not SniffVideo)", repo.created)
	}
	if repo.created.Engine != convert.EngineAV {
		t.Errorf("job Engine = %q, want av", repo.created.Engine)
	}
	if queue.enqueuedAV != repo.createdID {
		t.Errorf("enqueuedAV = %s, want %s", queue.enqueuedAV, repo.createdID)
	}
}

// --- av opts-dispatch tests (Plan 06: EngineAV case, AVE-03) ---

// TestCreateJob_AVOptsAccepted proves a valid AV opts payload (a thumbnail
// timecode on a thumbnail target) is accepted via the dedicated EngineAV
// opts case (ParseAVOpts/ValidateAVApplicability), normalized, and persisted
// -- mirrors TestCreateJob_AudioOptsAccepted's discipline for the av engine.
func TestCreateJob_AVOptsAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	body, ct := multipartBodyWithOpts(t, "in.mp4", "jpg", mp4BytesFixture(), `{"timecode":2.5}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (valid av opts must be accepted via ParseAVOpts, not rejected as DocOpts); body=%s", rec.Code, rec.Body.String())
	}
	if repo.created == nil || repo.created.Opts["timecode"] != 2.5 {
		t.Errorf("CreateParams.Opts = %+v, want timecode=2.5", repo.created)
	}
	if repo.created.Engine != convert.EngineAV {
		t.Errorf("job Engine = %q, want av", repo.created.Engine)
	}
	if queue.enqueuedAV != repo.createdID {
		t.Errorf("enqueuedAV = %s, want %s", queue.enqueuedAV, repo.createdID)
	}
}

// TestCreateJob_AVOptsAbsentAccepted proves an av-engine conversion with no
// opts field at all is accepted -- the fourth opts-dispatch behavior bullet
// (opts absent -> accepted), same discipline as every other engine's opts
// being entirely optional (D-02, opts.go doc comment mirrored across
// engines).
func TestCreateJob_AVOptsAbsentAccepted(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	srv, _ := newTestServer(repo, store, queue)

	body, ct := multipartBody(t, "in.mp4", "jpg", mp4BytesFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (absent opts must be accepted for the av engine); body=%s", rec.Code, rec.Body.String())
	}
	if queue.enqueuedAV != repo.createdID {
		t.Errorf("enqueuedAV = %s, want %s", queue.enqueuedAV, repo.createdID)
	}
}

// TestCreateJob_AVOptsRejectedForMalformedJSON proves ParseAVOpts's strict
// decode (checkStrictObject, opts.go) actually runs on the API path: a
// value of the wrong type for a known field is rejected with 422, not
// silently coerced or passed through to the converter.
func TestCreateJob_AVOptsRejectedForMalformedJSON(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.mp4", "jpg", mp4BytesFixture(), `{"timecode":"not-a-number"}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if repo.created != nil {
		t.Error("repo.Create must never run for malformed av opts JSON")
	}
}

// TestCreateJob_AVOptsRejectedForInapplicablePair proves
// ValidateAVApplicability's engine/target gating is actually enforced
// through the API path: a Timecode (thumbnail-only) requested against a
// transcode target is rejected with 422, mirroring the D-09/CR-01 "never
// silently retarget a client's request" discipline the plan calls out.
func TestCreateJob_AVOptsRejectedForInapplicablePair(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	srv, _ := newTestServer(repo, store, &fakeQueue{})

	body, ct := multipartBodyWithOpts(t, "in.mkv", "mp4", mkvBytesFixture(), `{"timecode":2.5}`)
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (timecode is thumbnail-only, mkv->mp4 is a transcode target); body=%s", rec.Code, rec.Body.String())
	}
	if repo.created != nil {
		t.Error("repo.Create must never run for opts inapplicable to the requested conversion")
	}
}

// --- D-07 two-tier upload ceiling tests (35-04) ---

// TestNewServer_MaxEngineBytesDefaults proves NewServer's zero-value
// Config.MaxEngineBytes defaulting yields a non-nil map covering all five
// engines: image/document/html/audio at their pre-Phase-35 effective 100 MiB
// ceiling, av at the raised 2 GiB allowance.
func TestNewServer_MaxEngineBytesDefaults(t *testing.T) {
	srv := NewServer(&fakeRepo{}, &fakeStorage{}, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), newFakeResolver(), healthyDeps(), Config{})

	if srv.maxEngineBytes == nil {
		t.Fatal("maxEngineBytes must be non-nil after NewServer")
	}
	for _, engine := range []string{convert.EngineImage, convert.EngineDocument, convert.EngineHTML, convert.EngineAudio} {
		if got := srv.maxEngineBytes[engine]; got != 100<<20 {
			t.Errorf("maxEngineBytes[%s] = %d, want 100 MiB (100<<20)", engine, got)
		}
	}
	if got := srv.maxEngineBytes[convert.EngineAV]; got != 2<<30 {
		t.Errorf("maxEngineBytes[av] = %d, want 2 GiB (2<<30)", got)
	}
}

// TestCreateJob_EngineSizeLimitRejectsOversizedImage verifies D-07's second
// tier: an image upload whose physical size exceeds ITS engine's ceiling is
// rejected 413 -- before any storage.Upload call and before repo.Create --
// even though it is comfortably under the (much larger) global
// MaxUploadBytes pre-parse bound. Uses a scaled-down MaxEngineBytes value
// (mirrors TestCreateJob_DimensionLimitExceeded's scaled-down MaxImagePixels)
// rather than an actual 200 MiB payload.
func TestCreateJob_EngineSizeLimitRejectsOversizedImage(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{
		MaxUploadBytes: 10 << 20,
		MaxEngineBytes: map[string]int64{convert.EngineImage: 100},
	})

	body, ct := multipartBody(t, "in.png", "webp", paddedPNGFixture(500))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not call storage.Upload for an upload exceeding its engine's size ceiling")
	}
	if repo.created != nil {
		t.Error("must not create a job for an upload exceeding its engine's size ceiling")
	}
}

// TestCreateJob_EngineSizeLimitAcceptsLargeAVUpload proves the OTHER half of
// D-07's two-tier design: an av-engine upload that would exceed the four
// existing engine classes' 100 MiB ceiling is accepted, routed with
// Engine="av", and enqueued via EnqueueAVConvert -- because av gets its own,
// much larger per-engine ceiling. Exercises engine=="av" through the fake
// (mp4, avTestTarget) collision-free synthetic pair registered above, kept
// independent of the real AVConverter's own pair set (now also registered,
// D-08, 35-06) so this test does not need to track it.
func TestCreateJob_EngineSizeLimitAcceptsLargeAVUpload(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	queue := &fakeQueue{}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, queue, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{
		MaxUploadBytes: 10 << 20,
		MaxEngineBytes: map[string]int64{convert.EngineImage: 1, convert.EngineAV: 5 << 20},
	})

	body, ct := multipartBody(t, "in.mp4", avTestTarget, paddedMP4Fixture(1<<20))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	if !store.uploaded {
		t.Error("must upload an av upload within the av engine's ceiling")
	}
	if repo.created == nil {
		t.Fatal("must create a job for an av upload within the av engine's ceiling")
	}
	if repo.created.Engine != convert.EngineAV {
		t.Errorf("job Engine = %q, want av", repo.created.Engine)
	}
	if queue.enqueuedAV != repo.createdID {
		t.Errorf("enqueuedAV = %s, want %s", queue.enqueuedAV, repo.createdID)
	}
}

// TestCreateJob_EngineAbsentFromSizeMapNotRejected verifies the fifth D-07
// behavior bullet: s.maxEngineBytes GATES known engines, it does not
// ALLOWLIST them. An engine deliberately omitted from a custom
// MaxEngineBytes map must never be rejected on size grounds, regardless of
// how large its upload is (bounded here only by MaxUploadBytes).
func TestCreateJob_EngineAbsentFromSizeMapNotRejected(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, defaultFakePresetRepo(), defaultFakePresetAdmin(), resolver, healthyDeps(), Config{
		MaxUploadBytes: 10 << 20,
		// EngineImage is deliberately absent -- it must not be gated.
		MaxEngineBytes: map[string]int64{convert.EngineDocument: 1},
	})

	body, ct := multipartBody(t, "in.png", "webp", paddedPNGFixture(500))
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (engine absent from MaxEngineBytes must not be rejected); body=%s", rec.Code, rec.Body.String())
	}
	if !store.uploaded {
		t.Error("must upload when the detected engine has no configured size ceiling")
	}
}
