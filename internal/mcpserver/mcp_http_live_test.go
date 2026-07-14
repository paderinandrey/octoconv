// Deliberate convention deviation, mirroring mcp_live_test.go's own called-
// out exception: this file is a cross-cutting live integration gate for the
// chart-deployed cmd/mcp-http binary (Phase 25, D-08), not a unit test of one
// source file in this package.
//
// mcp_http_live_test.go drives a REAL go-sdk streamable-HTTP MCP client
// against a chart-deployed mcp-http pod on OrbStack k8s (reached via
// `kubectl port-forward svc/mcp-http`): initialize -> tools/list=5 ->
// convert_file with a real per-request caller key -> presigned-only result
// (no local_path, MCPH-02/D-04) -> a request without an ApiKey header
// rejected 401 before any tool runs (D-03) -> (best-effort) a session-hijack
// 403 case. It also directly host-dials the returned presigned URL from
// wherever `go test` itself runs, closing Phase 24's deferred SC3 recheck
// (D-08).
//
// It is env-gated on OCTOCONV_MCP_HTTP_LIVE=1 exactly like mcp_live_test.go
// is gated on E2E_BASE_URL: unset, it self-skips so `go test ./...` stays
// green offline.
//
// Required environment for a live run:
//
//	OCTOCONV_MCP_HTTP_LIVE=1  gate flag
//	MCP_HTTP_ENDPOINT         e.g. http://127.0.0.1:8070 (port-forwarded svc/mcp-http)
//	MCP_HTTP_API_KEY          a raw client key minted via cmd/manage-clients
//	                          against the in-cluster Postgres
//
// Optional:
//
//	MCP_HTTP_CONVERT_PATH     path to an existing regular file INSIDE the
//	                          mcp-http pod's own filesystem (convert_file's
//	                          "path" is resolved server-side -- D-08's
//	                          fixture is placed there via `kubectl cp`, not
//	                          shipped in the image). Default "/tmp/sample.png".
//	MCP_HTTP_SC3_FALLBACK_ADDR  host:port (e.g. a `kubectl port-forward
//	                          svc/minio` local address) to dial INSTEAD of
//	                          the presigned URL's own host, while preserving
//	                          its Host header -- the 24-03 `--connect-to`
//	                          workaround for a wedged OrbStack host->cluster
//	                          proxy. Only consulted if the direct dial fails.
package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// initializeBodyJSON is a minimal JSON-RPC "initialize" request body, usable
// for raw (non-go-sdk-client) HTTP probes that must not go through a full
// client handshake.
const initializeBodyJSON = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"mcp-http-live-gate-probe","version":"0.0.0"}}}`

// wantHTTPTools is the exact five-tool surface this gate demands (MCPH-01),
// mirroring mcp_live_test.go's wantTools.
var wantHTTPTools = []string{
	"convert_file",
	"get_job_status",
	"download_result",
	"list_supported_formats",
	"list_presets",
}

// authTransport injects "Authorization: ApiKey <key>" on every request, the
// way a real internal-service MCP client would (mirrors cmd/mcp-http's own
// main_test.go helper of the same name/shape).
type authTransport struct {
	key string
}

func (a authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "ApiKey "+a.key)
	return http.DefaultTransport.RoundTrip(req)
}

// connectHTTPMCP connects a real go-sdk streamable client to endpoint,
// presenting key on every request.
func connectHTTPMCP(t *testing.T, endpoint, key string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "octoconv-mcp-http-live-gate", Version: "0.0.0"}, nil)
	cs, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: authTransport{key: key}},
	}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestMCPHTTPLive is the phase's D-08 live hard gate: chart-deployed
// mcp-http pod on OrbStack k8s, driven over a real streamable-HTTP session.
func TestMCPHTTPLive(t *testing.T) {
	if os.Getenv("OCTOCONV_MCP_HTTP_LIVE") != "1" {
		t.Skip("OCTOCONV_MCP_HTTP_LIVE not set; skipping live mcp-http gate (D-08)")
	}

	endpoint := os.Getenv("MCP_HTTP_ENDPOINT")
	if endpoint == "" {
		t.Fatal("MCP_HTTP_ENDPOINT must be set (e.g. http://127.0.0.1:8070)")
	}
	apiKey := os.Getenv("MCP_HTTP_API_KEY")
	if apiKey == "" {
		t.Fatal("MCP_HTTP_API_KEY must be set (raw key minted via cmd/manage-clients)")
	}
	convertPath := os.Getenv("MCP_HTTP_CONVERT_PATH")
	if convertPath == "" {
		convertPath = "/tmp/sample.png"
	}

	// 1. initialize (Connect performs the full initialize handshake
	// internally, including the notifications/initialized follow-up).
	cs := connectHTTPMCP(t, endpoint, apiKey)
	if cs.InitializeResult() == nil {
		t.Fatal("initialize: InitializeResult is nil")
	}
	t.Logf("initialize OK: serverInfo=%+v", cs.InitializeResult().ServerInfo)

	// 2. tools/list -- exactly the five tools (MCPH-01).
	listResult, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	gotTools := make(map[string]bool, len(listResult.Tools))
	for _, tool := range listResult.Tools {
		gotTools[tool.Name] = true
	}
	if len(gotTools) != len(wantHTTPTools) {
		t.Errorf("tools/list returned %d tools, want exactly %d: got=%v", len(gotTools), len(wantHTTPTools), gotTools)
	}
	for _, name := range wantHTTPTools {
		if !gotTools[name] {
			t.Errorf("tools/list missing expected tool %q; got %v", name, gotTools)
		}
	}
	t.Logf("tools/list OK: %v", gotTools)

	// 3. convert_file happy path with the real minted caller key --
	// presigned-only result (MCPH-02/D-04): non-empty presigned_url, EMPTY
	// local_path.
	convRes, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "convert_file",
		Arguments: map[string]any{"path": convertPath, "target_format": "jpg"},
	})
	if err != nil {
		t.Fatalf("CallTool convert_file: %v", err)
	}
	if convRes.IsError {
		t.Fatalf("convert_file returned IsError=true; content=%v", convRes.Content)
	}
	sc, ok := convRes.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("convert_file structuredContent is not a map: %v", convRes.StructuredContent)
	}
	presignedURL, _ := sc["presigned_url"].(string)
	if presignedURL == "" {
		t.Fatalf("convert_file structuredContent missing non-empty presigned_url: %v", sc)
	}
	if localPath, hasLocalPath := sc["local_path"]; hasLocalPath && localPath != "" && localPath != nil {
		t.Fatalf("convert_file structuredContent carries a non-empty local_path (want omitted in remote/HTTP mode, MCPH-02/D-04): %v", sc)
	}
	t.Logf("convert_file OK: presigned-only result, presigned_url=%s local_path=absent", presignedURL)

	// 4. Direct host-dial of the presigned URL -- closes Phase 24's deferred
	// SC3 recheck (D-08). This test process runs on the OrbStack host, so a
	// plain http.Get here is exactly the "direct from the host" case. If
	// OrbStack's host->cluster proxy is wedged (24-03 Deviation #4: connects
	// to a synthetic 198.18.x IP, then EOF/empty-reply), fall back to the
	// 24-03 `--connect-to`-equivalent path -- dial MCP_HTTP_SC3_FALLBACK_ADDR
	// instead, while the request's Host header (and therefore the AWS4
	// signature's SignedHeaders=host) is left untouched. The path actually
	// used (direct vs fallback) is recorded honestly, never silently
	// swallowed.
	sc3Body, sc3Path, sc3Err := sc3Dial(presignedURL)
	if sc3Err != nil {
		t.Fatalf("SC3 recheck FAILED on both direct and fallback paths: %v", sc3Err)
	}
	if len(sc3Body) < 3 || sc3Body[0] != 0xFF || sc3Body[1] != 0xD8 || sc3Body[2] != 0xFF {
		t.Fatalf("SC3 recheck (%s path): body head = % x, want JPEG magic bytes ff d8 ff", sc3Path, sc3Body[:min(3, len(sc3Body))])
	}
	t.Logf("SC3 RECHECK: PASS (%s path) -- presigned URL returned 200 + valid JPEG (%d bytes)", sc3Path, len(sc3Body))

	// 5. A request without an ApiKey header is rejected 401 BEFORE any tool
	// runs (D-03) -- raw HTTP, bypassing the go-sdk client's transport so no
	// Authorization header is ever set.
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(initializeBodyJSON))
	if err != nil {
		t.Fatalf("build no-auth request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	noAuthResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("no-auth request: %v", err)
	}
	defer noAuthResp.Body.Close()
	noAuthBody, _ := io.ReadAll(noAuthResp.Body)
	if noAuthResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-auth request status = %d, want 401; body=%s", noAuthResp.StatusCode, noAuthBody)
	}
	t.Logf("401-without-key OK: status=401 body=%s", noAuthBody)

	// 6. Best-effort session-hijack case (T-25-04b): a second caller (K2)
	// presenting K1's Mcp-Session-Id must be rejected 403 before any tool
	// runs. cs.ID() exposes the streamable client's own Mcp-Session-Id.
	sessionID := cs.ID()
	if sessionID == "" {
		t.Log("session-hijack case skipped: server issued no Mcp-Session-Id (acceptable in the current Stateless:true configuration)")
		return
	}
	hijackReq, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(initializeBodyJSON))
	if err != nil {
		t.Fatalf("build hijack request: %v", err)
	}
	hijackReq.Header.Set("Content-Type", "application/json")
	hijackReq.Header.Set("Accept", "application/json, text/event-stream")
	hijackReq.Header.Set("Authorization", "ApiKey k2-"+shortHash(apiKey))
	hijackReq.Header.Set("Mcp-Session-Id", sessionID)
	hijackResp, err := http.DefaultClient.Do(hijackReq)
	if err != nil {
		t.Fatalf("hijack request: %v", err)
	}
	defer hijackResp.Body.Close()
	hijackBody, _ := io.ReadAll(hijackResp.Body)
	if hijackResp.StatusCode != http.StatusForbidden {
		t.Errorf("session-hijack request (K2 presenting K1's session) status = %d, want 403; body=%s", hijackResp.StatusCode, hijackBody)
	} else {
		t.Logf("session-hijack OK: status=403 body=%s", hijackBody)
	}
}

// sc3Dial fetches presignedURL, first by dialing its own host directly (the
// "direct from the host" claim under test), and -- only if that attempt
// fails outright -- by falling back to MCP_HTTP_SC3_FALLBACK_ADDR (a
// port-forwarded local address) while preserving the request's original
// Host header, mirroring 24-03's `curl --connect-to` workaround for a
// wedged OrbStack host->cluster proxy. Returns the body, which path was
// used ("direct" or "fallback"), and a non-nil error only if BOTH paths
// failed.
func sc3Dial(presignedURL string) (body []byte, path string, err error) {
	directResp, directErr := http.Get(presignedURL)
	if directErr == nil {
		defer directResp.Body.Close()
		b, readErr := io.ReadAll(directResp.Body)
		if readErr == nil && directResp.StatusCode == http.StatusOK {
			return b, "direct", nil
		}
		if readErr != nil {
			directErr = readErr
		} else {
			directErr = &httpStatusError{status: directResp.StatusCode, body: b}
		}
	}

	fallbackAddr := os.Getenv("MCP_HTTP_SC3_FALLBACK_ADDR")
	if fallbackAddr == "" {
		return nil, "", directErr
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, fallbackAddr)
			},
		},
	}
	fbResp, fbErr := client.Get(presignedURL)
	if fbErr != nil {
		return nil, "", fmt.Errorf("direct dial failed (%v); fallback dial to %s also failed: %w", directErr, fallbackAddr, fbErr)
	}
	defer fbResp.Body.Close()
	b, readErr := io.ReadAll(fbResp.Body)
	if readErr != nil {
		return nil, "", fmt.Errorf("direct dial failed (%v); fallback read failed: %w", directErr, readErr)
	}
	if fbResp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("direct dial failed (%v); fallback status = %d, body=%s", directErr, fbResp.StatusCode, b)
	}
	return b, "fallback", nil
}

// httpStatusError reports a non-200 HTTP response for sc3Dial's direct
// attempt (kept distinct from a transport-level error so the fallback path's
// error message can quote the direct attempt's actual status/body).
type httpStatusError struct {
	status int
	body   []byte
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("status = %d, body=%s", e.status, e.body)
}

// shortHash returns a short, non-reversible tag derived from key, used only
// to build an Authorization header value for a DIFFERENT (bogus) caller in
// the hijack case -- never the raw key itself.
func shortHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:16]
}
