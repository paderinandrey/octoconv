package mcpserver

// http_test.go covers the HTTP-mode (remote) behavior added in Phase 25:
// ResultMode-aware tool results (D-04: presigned-only, no server-side files)
// and per-request Client construction (D-03: a Client built for caller key K
// only ever sends K -- no shared mutable key state, no cross-bleed).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// dirEntries returns the names of entries in dir, tolerating a dir that was
// never created (remote mode must not create OutputDir at all).
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestRemoteMode_ConvertFile_PresignedOnly (D-04/MCPH-02): in remote mode
// convert_file returns a presigned URL plus an expiry note, omits local_path,
// and never performs the server-side download -- the presigned endpoint is
// never fetched and OUTPUT_DIR stays untouched.
func TestRemoteMode_ConvertFile_PresignedOnly(t *testing.T) {
	var downloadHits int
	var mu sync.Mutex

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"job_id": "job-r1"})
	})
	mux.HandleFunc("/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JobStatus{
			JobID:       "job-r1",
			Status:      "done",
			DownloadURL: srv.URL + "/results/job-r1/out.jpg",
		})
	})
	mux.HandleFunc("/results/job-r1/out.jpg", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		downloadHits++
		mu.Unlock()
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	outDir := t.TempDir()
	h := newHarness(t, Config{BaseURL: srv.URL, OutputDir: outDir, ResultMode: ResultRemote}, nil)

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

	if out.LocalPath != "" {
		t.Fatalf("LocalPath = %q, want empty in remote mode (D-04)", out.LocalPath)
	}
	if out.PresignedURL == "" {
		t.Fatal("PresignedURL is empty, want the presigned result URL")
	}
	if out.ExpiryNote == "" {
		t.Fatal("ExpiryNote is empty, want an expiry note in remote mode (D-04)")
	}

	// The raw serialized result must genuinely omit local_path, not carry "".
	if got := resultText(t, res); strings.Contains(got, "local_path") {
		t.Fatalf("remote-mode result JSON still carries local_path: %s", got)
	}

	mu.Lock()
	hits := downloadHits
	mu.Unlock()
	if hits != 0 {
		t.Fatalf("presigned endpoint was fetched %d time(s), want 0 (remote mode must not download server-side)", hits)
	}
	if names := dirEntries(t, outDir); len(names) != 0 {
		t.Fatalf("OUTPUT_DIR gained entries %v, want none in remote mode", names)
	}
}

// TestRemoteMode_DownloadResult_ReturnsURLWithoutFile (D-04): in remote mode
// download_result returns the presigned URL without fetching it or writing
// any file.
func TestRemoteMode_DownloadResult_ReturnsURLWithoutFile(t *testing.T) {
	var downloadHits int
	var mu sync.Mutex

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/v1/jobs/job-r2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JobStatus{
			JobID:       "job-r2",
			Status:      "done",
			DownloadURL: srv.URL + "/results/job-r2/out.jpg",
		})
	})
	mux.HandleFunc("/results/job-r2/out.jpg", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		downloadHits++
		mu.Unlock()
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	outDir := t.TempDir()
	h := newHarness(t, Config{BaseURL: srv.URL, OutputDir: outDir, ResultMode: ResultRemote}, nil)

	res, err := h.cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "download_result",
		Arguments: map[string]any{"job_id": "job-r2"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}

	var out DownloadResultOutput
	decodeResult(t, res, &out)
	if out.LocalPath != "" {
		t.Fatalf("LocalPath = %q, want empty in remote mode (D-04)", out.LocalPath)
	}
	if out.PresignedURL == "" {
		t.Fatal("PresignedURL is empty, want the presigned result URL")
	}

	mu.Lock()
	hits := downloadHits
	mu.Unlock()
	if hits != 0 {
		t.Fatalf("presigned endpoint was fetched %d time(s), want 0 (remote download_result returns the URL, never proxies bytes)", hits)
	}
	if names := dirEntries(t, outDir); len(names) != 0 {
		t.Fatalf("OUTPUT_DIR gained entries %v, want none in remote mode", names)
	}
}

// TestRemoteMode_ToolDescriptions_DropLocalPathLanguage (D-04): remote-mode
// tool descriptions must not reference OUTPUT_DIR or promise a local path,
// so an agent never expects a filesystem artifact from a remote endpoint.
func TestRemoteMode_ToolDescriptions_DropLocalPathLanguage(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()

	// A deliberately distinctive dir name so a leaked reference is unambiguous.
	outDir := t.TempDir() + "/distinctive-output-dir-marker"
	h := newHarness(t, Config{BaseURL: srv.URL, OutputDir: outDir, ResultMode: ResultRemote}, nil)

	for tool, err := range h.cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		if tool.Name != "convert_file" && tool.Name != "download_result" {
			continue
		}
		if strings.Contains(tool.Description, outDir) {
			t.Fatalf("%s description references OUTPUT_DIR %q in remote mode: %q", tool.Name, outDir, tool.Description)
		}
		if strings.Contains(strings.ToLower(tool.Description), "local path") {
			t.Fatalf("%s description promises a local path in remote mode: %q", tool.Name, tool.Description)
		}
	}
}

// TestNewClientForKey_PerRequestIsolation (D-03/D-07): two Clients built via
// NewClientForKey from the same base Config but different caller keys each
// send exactly their own Authorization header -- no shared mutable key state,
// no cross-bleed.
func TestNewClientForKey_PerRequestIsolation(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JobStatus{JobID: "job-x", Status: "active"})
	}))
	defer srv.Close()

	base := Config{
		BaseURL:        srv.URL,
		ResultMode:     ResultRemote,
		ConvertTimeout: time.Second,
		PollInterval:   time.Millisecond,
	}

	c1, err := NewClientForKey(base, "caller-key-one")
	if err != nil {
		t.Fatalf("NewClientForKey(K1): %v", err)
	}
	c2, err := NewClientForKey(base, "caller-key-two")
	if err != nil {
		t.Fatalf("NewClientForKey(K2): %v", err)
	}

	if _, err := c1.GetJob(context.Background(), "job-x"); err != nil {
		t.Fatalf("c1.GetJob: %v", err)
	}
	if _, err := c2.GetJob(context.Background(), "job-x"); err != nil {
		t.Fatalf("c2.GetJob: %v", err)
	}
	if _, err := c1.GetJob(context.Background(), "job-x"); err != nil {
		t.Fatalf("c1.GetJob (second): %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"ApiKey caller-key-one", "ApiKey caller-key-two", "ApiKey caller-key-one"}
	if len(seen) != len(want) {
		t.Fatalf("saw %d requests (%v), want %d", len(seen), seen, len(want))
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("request %d Authorization = %q, want %q (per-request key isolation, D-03)", i, seen[i], want[i])
		}
	}
}

// TestNewClientForKey_RemoteMode_NoFilesystemTouch (D-04/D-03): building a
// per-request remote Client must not create OutputDir -- the HTTP pod needs
// no writable filesystem.
func TestNewClientForKey_RemoteMode_NoFilesystemTouch(t *testing.T) {
	dir := t.TempDir() + "/never-created"
	base := Config{
		BaseURL:    "http://unused.invalid",
		OutputDir:  dir,
		ResultMode: ResultRemote,
	}
	if _, err := NewClientForKey(base, "some-key"); err != nil {
		t.Fatalf("NewClientForKey: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("OutputDir %q was created (stat err = %v), want untouched in remote mode", dir, err)
	}
}
