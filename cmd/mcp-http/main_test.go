package main

// main_test.go drives the full mcp-http handler stack (healthz + 401
// auth-parse middleware + session-key binding middleware + streamable MCP
// handler) at the httptest level (D-07): 401 before any MCP code, per-request
// key isolation, and the exact session-hijack rejection (K2 + K1's session id
// -> 403 before any tool runs).

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/apaderin/octoconv/internal/mcpserver"
)

const (
	initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"mcp-http-test","version":"0.0.1"}}}`
	getJobBody     = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_job_status","arguments":{"job_id":"job-h"}}}`
)

// fakeUpstream stands in for the OctoConv public API, recording every
// Authorization header it sees so tests can prove per-request key isolation
// (D-03/D-07) and that a rejected request never reached upstream.
func fakeUpstream(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var auths []string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mcpserver.JobStatus{JobID: "job-h", Status: "active"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), auths...)
	}
}

// newTestStack builds the production handler stack against a fake upstream
// and serves it over httptest, returning the MCP endpoint URL, the binding
// map (for lifecycle assertions), and the upstream's Authorization capture.
func newTestStack(t *testing.T) (string, *sessionBindings, func() []string) {
	t.Helper()
	upstream, seen := fakeUpstream(t)
	base := mcpserver.Config{
		BaseURL:        upstream.URL,
		ResultMode:     mcpserver.ResultRemote,
		ConvertTimeout: 5 * time.Second,
		PollInterval:   10 * time.Millisecond,
	}
	bindings := newSessionBindings()
	srv := httptest.NewServer(newHandler(base, bindings))
	t.Cleanup(srv.Close)
	return srv.URL, bindings, seen
}

// doMCP issues a raw streamable-HTTP request. key=="" omits Authorization;
// rawAuth (when non-empty) overrides the header verbatim for malformed-scheme
// cases; sid sets Mcp-Session-Id.
func doMCP(t *testing.T, method, url, key, rawAuth, sid, body string) *http.Response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if rawAuth != "" {
		req.Header.Set("Authorization", rawAuth)
	} else if key != "" {
		req.Header.Set("Authorization", "ApiKey "+key)
	}
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestHealthz(t *testing.T) {
	url, _, _ := newTestStack(t)
	resp, err := http.Get(url + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}
}

// TestMissingAuth_401 (D-03): no Authorization header is rejected with a 401
// JSON error before any MCP JSON-RPC code runs -- the upstream API never
// sees a request.
func TestMissingAuth_401(t *testing.T) {
	url, _, seen := newTestStack(t)

	resp := doMCP(t, http.MethodPost, url, "", "", "", initializeBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(readBody(t, resp)), &body); err != nil {
		t.Fatalf("401 body is not JSON: %v", err)
	}
	if body["error"] == "" {
		t.Fatalf("401 body has no error field: %v", body)
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("upstream saw %v, want no requests for an unauthenticated call", got)
	}
}

// TestMalformedAuth_401 (D-03): malformed Authorization variants are all 401.
func TestMalformedAuth_401(t *testing.T) {
	url, _, seen := newTestStack(t)

	const probeKey = "some-secret-probe-key"
	cases := []string{
		"Bearer " + probeKey,
		"ApiKey",
		probeKey,
		"ApiKey " + probeKey + " extra",
	}
	for _, rawAuth := range cases {
		t.Run(rawAuth, func(t *testing.T) {
			resp := doMCP(t, http.MethodPost, url, "", rawAuth, "", initializeBody)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 for header %q", resp.StatusCode, rawAuth)
			}
			if body := readBody(t, resp); strings.Contains(body, probeKey) {
				t.Fatalf("401 body echoes the presented key: %s", body)
			}
		})
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("upstream saw %v, want no requests for malformed auth", got)
	}
}

// authTransport injects "Authorization: ApiKey <key>" on every request, the
// way a real internal-service MCP client would.
type authTransport struct {
	key string
}

func (a authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "ApiKey "+a.key)
	return http.DefaultTransport.RoundTrip(req)
}

// connectMCP connects a real go-sdk streamable client to url, presenting key.
func connectMCP(t *testing.T, url, key string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-" + key, Version: "0.0.1"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &http.Client{Transport: authTransport{key: key}},
	}, nil)
	if err != nil {
		t.Fatalf("Connect with key %q: %v", key, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestValidKey_FiveTools_PerRequestClient (MCPH-01/D-03): a caller with a
// valid ApiKey header lists all five tools and its tool calls hit the
// upstream API with exactly that caller's key.
func TestValidKey_FiveTools_PerRequestClient(t *testing.T) {
	url, _, seen := newTestStack(t)

	cs := connectMCP(t, url, "caller-key-alpha")

	names := map[string]bool{}
	for tool, err := range cs.Tools(t.Context(), nil) {
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		names[tool.Name] = true
	}
	want := []string{"convert_file", "get_job_status", "download_result", "list_supported_formats", "list_presets"}
	if len(names) != len(want) {
		t.Fatalf("got %d tools (%v), want %d", len(names), names, len(want))
	}
	for _, w := range want {
		if !names[w] {
			t.Fatalf("missing tool %q; got %v", w, names)
		}
	}

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "get_job_status",
		Arguments: map[string]any{"job_id": "job-h"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true; content = %v", res.Content)
	}

	got := seen()
	if len(got) == 0 {
		t.Fatal("upstream saw no requests, want the get_job_status call")
	}
	for i, a := range got {
		if a != "ApiKey caller-key-alpha" {
			t.Fatalf("upstream request %d Authorization = %q, want the caller's own key", i, a)
		}
	}
}

// TestTwoKeys_NoCrossBleed (D-03/D-07): two concurrent caller keys resolve to
// two isolated per-request Clients -- upstream sees each caller's own key on
// each of its calls, never the other's.
func TestTwoKeys_NoCrossBleed(t *testing.T) {
	url, _, seen := newTestStack(t)

	cs1 := connectMCP(t, url, "caller-key-one")
	cs2 := connectMCP(t, url, "caller-key-two")

	call := func(cs *mcp.ClientSession) {
		t.Helper()
		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "get_job_status",
			Arguments: map[string]any{"job_id": "job-h"},
		})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("IsError = true; content = %v", res.Content)
		}
	}

	call(cs1)
	call(cs2)
	call(cs1)

	got := seen()
	want := []string{"ApiKey caller-key-one", "ApiKey caller-key-two", "ApiKey caller-key-one"}
	if len(got) != len(want) {
		t.Fatalf("upstream saw %d calls (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("upstream call %d Authorization = %q, want %q (cross-bleed!)", i, got[i], want[i])
		}
	}
}

// TestSessionHijack_403 (D-03/D-07, T-25-04b): K1 initializes and is assigned
// session id S; a request with valid key K2 but Mcp-Session-Id: S is rejected
// 403 BEFORE ServeHTTP -- K1's session-bound state never executes K2's call
// and the upstream API never sees the hijack attempt.
func TestSessionHijack_403(t *testing.T) {
	url, _, seen := newTestStack(t)

	// K1 initializes and receives a session id.
	initResp := doMCP(t, http.MethodPost, url, "hijack-victim-k1", "", "", initializeBody)
	if initResp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200 (body: %s)", initResp.StatusCode, readBody(t, initResp))
	}
	sid := initResp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("initialize response carries no Mcp-Session-Id; the hijack test needs one")
	}
	_ = readBody(t, initResp) // drain

	// K2 presents a valid key but K1's session id -> 403, no tool execution.
	hijack := doMCP(t, http.MethodPost, url, "hijacker-k2", "", sid, getJobBody)
	if hijack.StatusCode != http.StatusForbidden {
		t.Fatalf("hijack status = %d, want 403 (body: %s)", hijack.StatusCode, readBody(t, hijack))
	}
	if body := readBody(t, hijack); strings.Contains(body, "hijacker-k2") || strings.Contains(body, "hijack-victim-k1") {
		t.Fatalf("403 body echoes a key: %s", body)
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("upstream saw %v after the hijack attempt, want none -- K1's Client must never execute K2's call", got)
	}

	// Control: the session's creator K1 with the same session id is allowed.
	legit := doMCP(t, http.MethodPost, url, "hijack-victim-k1", "", sid, getJobBody)
	if legit.StatusCode != http.StatusOK {
		t.Fatalf("creator's own call status = %d, want 200 (body: %s)", legit.StatusCode, readBody(t, legit))
	}
	got := seen()
	if len(got) != 1 || got[0] != "ApiKey hijack-victim-k1" {
		t.Fatalf("upstream saw %v, want exactly one call carrying K1's key", got)
	}
}

// TestSessionBinding_DeleteCleansUp: a DELETE with the session id drops the
// binding (lifecycle: in stateless mode the map is belt-and-suspenders, but
// it must still not leak on graceful close).
func TestSessionBinding_DeleteCleansUp(t *testing.T) {
	url, bindings, _ := newTestStack(t)

	initResp := doMCP(t, http.MethodPost, url, "cleanup-key", "", "", initializeBody)
	if initResp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200", initResp.StatusCode)
	}
	sid := initResp.Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("initialize response carries no Mcp-Session-Id")
	}
	_ = readBody(t, initResp)

	if n := bindings.len(); n != 1 {
		t.Fatalf("bindings.len() = %d after initialize, want 1", n)
	}

	del := doMCP(t, http.MethodDelete, url, "cleanup-key", "", sid, "")
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", del.StatusCode)
	}
	if n := bindings.len(); n != 0 {
		t.Fatalf("bindings.len() = %d after DELETE, want 0 (binding must be dropped)", n)
	}
}

// TestSessionBinding_SweepDropsIdleEntries: the periodic sweep drops entries
// idle past the TTL, bounding the map even when clients never DELETE.
func TestSessionBinding_SweepDropsIdleEntries(t *testing.T) {
	b := newSessionBindings()
	b.bind("session-a", [32]byte{1})
	b.bind("session-b", [32]byte{2})
	if n := b.len(); n != 2 {
		t.Fatalf("len = %d, want 2", n)
	}

	// Sweep as-of a future instant beyond the idle TTL: everything is stale.
	b.sweep(time.Now().Add(bindingIdleTTL + time.Minute))
	if n := b.len(); n != 0 {
		t.Fatalf("len = %d after sweep past TTL, want 0", n)
	}
}
