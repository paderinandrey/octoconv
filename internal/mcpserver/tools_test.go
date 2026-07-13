package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// testHarness bundles a connected MCP client session driving a NewServer
// instance whose Client points at an httptest fake of the OctoConv public
// API (D-12: full tool coverage without docker). Using a genuine in-memory
// MCP session (rather than calling handler functions directly) is what lets
// these tests observe the go-sdk's own error->isError conversion (D-11) and
// exercise real progress notifications (D-04) over req.Session.
type testHarness struct {
	cs  *mcp.ClientSession
	dir string
}

func newHarness(t *testing.T, cfg Config, copts *mcp.ClientOptions) *testHarness {
	t.Helper()
	if cfg.OutputDir == "" {
		cfg.OutputDir = t.TempDir()
	}
	if cfg.ConvertTimeout == 0 {
		cfg.ConvertTimeout = 5 * time.Second
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 10 * time.Millisecond
	}
	if cfg.APIKey == "" {
		cfg.APIKey = testAPIKey
	}

	c, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	s := NewServer(cfg, c)

	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server Connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, copts)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	return &testHarness{cs: cs, dir: cfg.OutputDir}
}

// decodeResult unmarshals a successful CallToolResult's text content (the
// JSON serialization of the tool's structured Out value) into out.
func decodeResult(t *testing.T, res *mcp.CallToolResult, out any) {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	if err := json.Unmarshal([]byte(tc.Text), out); err != nil {
		t.Fatalf("decode result content: %v (text=%q)", err, tc.Text)
	}
}

// resultText returns the text of a result's first content block, for
// isError assertions.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

// TestConvertFile_HappyPath drives the full blocking convert_file tool call
// against an httptest fake (queued -> active -> done, then a JPEG-magic
// download) and asserts the result never inlines the file body (D-07).
func TestConvertFile_HappyPath(t *testing.T) {
	var getCalls int32
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"job_id": "job-1"})
	})
	mux.HandleFunc("/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&getCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			status := "queued"
			if n == 2 {
				status = "active"
			}
			_ = json.NewEncoder(w).Encode(JobStatus{JobID: "job-1", Status: status})
			return
		}
		_ = json.NewEncoder(w).Encode(JobStatus{
			JobID:       "job-1",
			Status:      "done",
			DownloadURL: srv.URL + "/results/job-1/result.jpg",
		})
	})
	mux.HandleFunc("/results/job-1/result.jpg", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "convert_file",
		Arguments: map[string]any{"path": sampleFile, "target_format": "jpg"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}

	var out ConvertFileOutput
	decodeResult(t, res, &out)

	if out.JobID != "job-1" {
		t.Fatalf("JobID = %q, want job-1", out.JobID)
	}
	if out.PresignedURL == "" {
		t.Fatal("PresignedURL is empty")
	}
	if out.TargetFormat != "jpg" {
		t.Fatalf("TargetFormat = %q, want jpg", out.TargetFormat)
	}
	if filepath.Dir(out.LocalPath) != h.dir {
		t.Fatalf("LocalPath dir = %q, want %q (OUTPUT_DIR only)", filepath.Dir(out.LocalPath), h.dir)
	}

	data, err := os.ReadFile(out.LocalPath)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
		t.Fatalf("downloaded file head = % x, want JPEG magic bytes ff d8 ff", data[:min(3, len(data))])
	}

	// D-07: the tool result must never inline the file body -- only a path
	// and a URL, both far smaller than the file itself.
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(raw), string(data)) {
		t.Fatal("tool result content contains raw file bytes")
	}
	if len(raw) > 2048 {
		t.Fatalf("tool result content is %d bytes, want a small path/URL payload, not inlined bytes", len(raw))
	}
}

// TestConvertFile_ProgressNotifiedOnEveryTick asserts NotifyProgress fires
// once per poll tick when the calling request supplies a progress token
// (D-04).
func TestConvertFile_ProgressNotifiedOnEveryTick(t *testing.T) {
	var getCalls int32
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"job_id": "job-p"})
	})
	mux.HandleFunc("/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&getCalls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			status := "queued"
			if n == 2 {
				status = "active"
			}
			_ = json.NewEncoder(w).Encode(JobStatus{JobID: "job-p", Status: status})
			return
		}
		_ = json.NewEncoder(w).Encode(JobStatus{
			JobID:       "job-p",
			Status:      "done",
			DownloadURL: srv.URL + "/results/job-p/out.jpg",
		})
	})
	mux.HandleFunc("/results/job-p/out.jpg", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	var mu sync.Mutex
	var progress []*mcp.ProgressNotificationParams
	copts := &mcp.ClientOptions{
		ProgressNotificationHandler: func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			mu.Lock()
			progress = append(progress, req.Params)
			mu.Unlock()
		},
	}

	h := newHarness(t, Config{BaseURL: srv.URL}, copts)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "convert_file",
		Arguments: map[string]any{"path": sampleFile, "target_format": "jpg"},
		Meta:      mcp.Meta{"progressToken": "tok-1"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}

	// The client's notification dispatch may run a beat after CallTool's
	// final response is processed; poll briefly rather than assuming strict
	// same-instant delivery.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(progress)
		mu.Unlock()
		if n >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d progress notifications, want at least 3 (one per poll tick)", n)
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	for i, p := range progress {
		if p.ProgressToken != "tok-1" {
			t.Fatalf("progress[%d].ProgressToken = %v, want tok-1", i, p.ProgressToken)
		}
		if p.Message == "" {
			t.Fatalf("progress[%d].Message is empty", i)
		}
	}
}

// TestConvertFile_NoProgressToken_NoPanic asserts that omitting a progress
// token is a silent no-op (D-04: best-effort, never required) and never
// dereferences a nil session.
func TestConvertFile_NoProgressToken_NoPanic(t *testing.T) {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"job_id": "job-np"})
	})
	mux.HandleFunc("/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JobStatus{
			JobID:       "job-np",
			Status:      "done",
			DownloadURL: srv.URL + "/results/job-np/out.jpg",
		})
	})
	mux.HandleFunc("/results/job-np/out.jpg", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "convert_file",
		Arguments: map[string]any{"path": sampleFile, "target_format": "jpg"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}
}

// TestConvertFile_BothTargetAndPreset_IsError (D-04/D-11): the API's 400/422
// text for supplying both target_format and preset is forwarded verbatim as
// an isError tool result, never a protocol error.
func TestConvertFile_BothTargetAndPreset_IsError(t *testing.T) {
	const errText = "specify either preset or target/opts, not both"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": errText})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "convert_file",
		Arguments: map[string]any{"path": sampleFile, "target_format": "jpg", "preset": "thumb"},
	})
	if err != nil {
		t.Fatalf("CallTool returned a transport/protocol error %v, want a tool-level isError result", err)
	}
	if !res.IsError {
		t.Fatalf("IsError = false, want true; content = %v", res.Content)
	}
	if got := resultText(t, res); !strings.Contains(got, errText) {
		t.Fatalf("result text = %q, want it to contain %q", got, errText)
	}
}

// TestConvertFile_UnsupportedPair_IsError (D-11): a 422 for an unsupported
// conversion pair is forwarded verbatim as an isError tool result.
func TestConvertFile_UnsupportedPair_IsError(t *testing.T) {
	const errText = "unsupported conversion: png -> weird"

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": errText})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "convert_file",
		Arguments: map[string]any{"path": sampleFile, "target_format": "weird"},
	})
	if err != nil {
		t.Fatalf("CallTool returned a transport/protocol error %v, want a tool-level isError result", err)
	}
	if !res.IsError {
		t.Fatalf("IsError = false, want true; content = %v", res.Content)
	}
	if got := resultText(t, res); !strings.Contains(got, errText) {
		t.Fatalf("result text = %q, want it to contain %q", got, errText)
	}
}

// TestConvertFile_BadPath_IsError (D-09): a non-existent path is rejected
// before any HTTP call is made.
func TestConvertFile_BadPath_IsError(t *testing.T) {
	called := false
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "convert_file",
		Arguments: map[string]any{
			"path":          filepath.Join(t.TempDir(), "does-not-exist.png"),
			"target_format": "jpg",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError = false, want true; content = %v", res.Content)
	}
	if called {
		t.Fatal("convert_file made an HTTP call before validating the input path")
	}
}

// TestGetJobStatus (D-05): a non-blocking status check decodes cleanly.
func TestGetJobStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs/job-42", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JobStatus{JobID: "job-42", Status: "active"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_job_status",
		Arguments: map[string]any{"job_id": "job-42"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}

	var out JobStatus
	decodeResult(t, res, &out)
	if out.JobID != "job-42" || out.Status != "active" {
		t.Fatalf("out = %+v, want job-42/active", out)
	}
}

// TestGetJobStatus_NotFound_IsError (D-11): an upstream 404 surfaces as
// isError.
func TestGetJobStatus_NotFound_IsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs/missing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_job_status",
		Arguments: map[string]any{"job_id": "missing"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError = false, want true; content = %v", res.Content)
	}
	if got := resultText(t, res); !strings.Contains(got, "job not found") {
		t.Fatalf("result text = %q, want it to contain %q", got, "job not found")
	}
}

// TestDownloadResult (D-05/D-07): downloads a done job's result strictly
// into OUTPUT_DIR.
func TestDownloadResult(t *testing.T) {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/jobs/job-9", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JobStatus{
			JobID:       "job-9",
			Status:      "done",
			DownloadURL: srv.URL + "/results/job-9/out.jpg",
		})
	})
	mux.HandleFunc("/results/job-9/out.jpg", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "download_result",
		Arguments: map[string]any{"job_id": "job-9"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}

	var out DownloadResultOutput
	decodeResult(t, res, &out)
	if filepath.Dir(out.LocalPath) != h.dir {
		t.Fatalf("LocalPath dir = %q, want %q (OUTPUT_DIR only)", filepath.Dir(out.LocalPath), h.dir)
	}
	if _, err := os.Stat(out.LocalPath); err != nil {
		t.Fatalf("downloaded file missing: %v", err)
	}
}

// TestDownloadResult_FilenameHintSanitized (D-09): an agent-supplied
// filename hint is honored only after sanitization, and the file still
// lands strictly inside OUTPUT_DIR.
func TestDownloadResult_FilenameHintSanitized(t *testing.T) {
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/jobs/job-10", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JobStatus{
			JobID:       "job-10",
			Status:      "done",
			DownloadURL: srv.URL + "/results/job-10/out.jpg",
		})
	})
	mux.HandleFunc("/results/job-10/out.jpg", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "download_result",
		Arguments: map[string]any{"job_id": "job-10", "filename": "../../evil.jpg"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}

	var out DownloadResultOutput
	decodeResult(t, res, &out)
	if filepath.Dir(out.LocalPath) != h.dir {
		t.Fatalf("LocalPath dir = %q, want %q (OUTPUT_DIR only)", filepath.Dir(out.LocalPath), h.dir)
	}
	base := filepath.Base(out.LocalPath)
	if strings.Contains(base, "..") || strings.ContainsAny(base, "/\\") {
		t.Fatalf("LocalPath base = %q, contains path-traversal remnants", base)
	}
}

// TestDownloadResult_NotDone_IsError (D-05): download_result refuses to
// fetch a job that has not reached the done status.
func TestDownloadResult_NotDone_IsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs/job-11", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JobStatus{JobID: "job-11", Status: "active"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "download_result",
		Arguments: map[string]any{"job_id": "job-11"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError = false, want true; content = %v", res.Content)
	}
	if got := resultText(t, res); !strings.Contains(got, "active") {
		t.Fatalf("result text = %q, want it to mention the current status", got)
	}
}

// TestListSupportedFormats (D-06): a thin passthrough of GET /v1/formats.
func TestListSupportedFormats(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/formats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"engines": map[string]any{
				"image": map[string]any{"pairs": [][2]string{{"png", "jpg"}}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_supported_formats",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}

	var out FormatsResponse
	decodeResult(t, res, &out)
	eng, ok := out.Engines["image"]
	if !ok || len(eng.Pairs) != 1 || eng.Pairs[0] != [2]string{"png", "jpg"} {
		t.Fatalf("out = %+v, want image engine with pairs [[png jpg]]", out)
	}
}

// TestToolListPresets (D-06): a thin passthrough of GET /v1/presets via the
// list_presets tool, including the include_inactive -> ?all=true mapping.
func TestToolListPresets(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/presets", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]Preset{
			// Options is always at least "{}" in the real system (repo.Create
			// defaults nil Options to "{}" before persisting), never a JSON
			// null -- mirror that invariant here rather than leaving the Go
			// zero value (a nil map, which marshals as null and fails output
			// schema validation for the non-omitempty Options field).
			{Name: "p1", Version: 1, Scope: "user", Operation: "convert", TargetFormat: "jpg", Options: map[string]any{}, IsActive: true},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_presets",
		Arguments: map[string]any{"include_inactive": true},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}

	var out ListPresetsOutput
	decodeResult(t, res, &out)
	if len(out.Presets) != 1 || out.Presets[0].Name != "p1" {
		t.Fatalf("out = %+v, want a single preset named p1", out)
	}
	if gotQuery != "all=true" {
		t.Fatalf("query = %q, want all=true (include_inactive=true)", gotQuery)
	}
}

// TestServer_RegistersFiveTools (D-01): NewServer exposes exactly the five
// expected tool names.
func TestServer_RegistersFiveTools(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	h := newHarness(t, Config{BaseURL: srv.URL}, nil)

	names := map[string]bool{}
	for tool, err := range h.cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		names[tool.Name] = true
	}

	want := []string{"convert_file", "get_job_status", "download_result", "list_supported_formats", "list_presets"}
	if len(names) != len(want) {
		t.Fatalf("got %d tools (%v), want exactly %d: %v", len(names), names, len(want), want)
	}
	for _, w := range want {
		if !names[w] {
			t.Fatalf("missing expected tool %q; got %v", w, names)
		}
	}
}
