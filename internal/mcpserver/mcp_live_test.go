// Deliberate convention deviation: the codebase convention is
// "<source>_test.go" naming one-file-per-responsibility (mirrored by
// client_test.go/tools_test.go in this package). This file has no single
// source counterpart -- it is a cross-cutting live integration gate spanning
// the whole package PLUS the built cmd/mcp-server binary, so it gets its own
// name instead of pretending to belong to one source file. This mirrors
// internal/e2e/e2e_test.go's own called-out naming exception for the
// project's first true end-to-end suite.
//
// mcp_live_test.go drives the REAL cmd/mcp-server binary over its real
// stdio JSON-RPC transport against a running docker-compose stack (D-13):
// initialize handshake, tools/list (five tools), a real png->jpg
// convert_file conversion, list_presets/list_supported_formats round-trip,
// a bad-input isError case, and a stdout-purity assertion that every
// non-empty line the binary writes to stdout parses as JSON-RPC (D-10).
//
// It is env-gated on E2E_BASE_URL exactly like internal/e2e: unset, it
// self-skips so `go test ./...` stays green offline.
//
// Required environment for a live run (mirrors internal/e2e/e2e_test.go):
//
//	E2E_BASE_URL      e.g. http://localhost:8090 -- the running API
//	DATABASE_URL      e.g. postgres://octo:octo-pass@localhost:5434/octo_db
//	API_KEY_SALT      MUST equal the running API's API_KEY_SALT value
//
// Optional:
//
//	E2E_S3_DIAL_ADDR  host:port the CHILD binary redials for the presigned
//	                  download when the URL's host (e.g. minio:9000) does
//	                  not resolve from the host running the test; default
//	                  127.0.0.1:9100. Set on the exec'd binary's env as
//	                  OCTOCONV_S3_DIAL_ADDR -- NOT on this test process's
//	                  env, since the binary itself performs the download.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/db"
)

// wantTools is the exact five-tool surface this gate demands (D-01/D-13).
var wantTools = []string{
	"convert_file",
	"get_job_status",
	"download_result",
	"list_supported_formats",
	"list_presets",
}

// provisionMCPClient creates a real client row in the stack's live Postgres
// and returns the raw API key, mirroring internal/e2e's provisionClient
// (test-only import of internal/clients+auth+db -- does NOT violate D-02,
// which constrains the PRODUCTION package internal/mcpserver, not its
// _test.go files). API_KEY_SALT MUST match the running API's value.
func provisionMCPClient(t *testing.T) string {
	t.Helper()
	salt := []byte(os.Getenv("API_KEY_SALT"))
	if len(salt) == 0 {
		t.Fatal("API_KEY_SALT must be set (and must equal the running API's value)")
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx) // reads DATABASE_URL
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := clients.NewRepo(pool)
	raw, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("auth.GenerateKey: %v", err)
	}
	hash := auth.HashKey(salt, raw)
	name := "mcp-live-test-client-" + uuid.NewString()[:8]
	if _, err := repo.Create(ctx, name, hash); err != nil {
		t.Fatalf("provision client %q: %v", name, err)
	}
	return raw
}

// buildMCPServerBinary builds the real cmd/mcp-server binary this gate
// drives -- the live gate must exercise the actual shipped artifact, never
// a mocked transport (D-13).
func buildMCPServerBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "octoconv-mcp")
	cmd := exec.Command("go", "build", "-o", binPath, "github.com/apaderin/octoconv/cmd/mcp-server")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build ./cmd/mcp-server: %v", err)
	}
	return binPath
}

// rpcHarness drives a minimal hand-rolled JSON-RPC-over-stdio session
// against the running child process: it owns the raw stdin/stdout pipes
// directly (rather than going through the go-sdk's own client), so every
// line the child writes to stdout is naturally observed and can be
// validated for stdout purity (D-10) as a side effect of normal protocol
// traffic, with no separate raw-pipe run required.
type rpcHarness struct {
	t     *testing.T
	cmd   *exec.Cmd
	stdin *os.File

	linesMu sync.Mutex
	lines   chan string

	idSeq int64
}

// startMCPServerProcess execs binPath with env (the CHILD's environment --
// see the package doc comment on why OCTOCONV_S3_DIAL_ADDR must live here,
// not on the test process), wires stdin/stdout pipes, and starts a
// background reader that pushes every non-empty stdout line onto a channel.
// The process runs in its own process group so a hung binary can be killed
// as a whole group on timeout, never leaving an orphaned child behind.
func startMCPServerProcess(t *testing.T, binPath string, env []string) *rpcHarness {
	t.Helper()

	cmd := exec.Command(binPath)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", binPath, err)
	}

	h := &rpcHarness{
		t:     t,
		cmd:   cmd,
		stdin: stdinPipe.(*os.File),
		lines: make(chan string, 256),
	}

	go func() {
		sc := bufio.NewScanner(stdoutPipe)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			if line := sc.Text(); strings.TrimSpace(line) != "" {
				h.lines <- line
			}
		}
		close(h.lines)
	}()

	t.Cleanup(func() {
		h.killAndWait()
		if stderrBuf.Len() == 0 {
			t.Error("expected at least one startup log line on the child's stderr; got none -- logging may not be reaching stderr")
		} else {
			t.Logf("child stderr (expected: startup log only, never JSON-RPC):\n%s", stderrBuf.String())
		}
	})

	return h
}

// killAndWait closes stdin (a graceful shutdown signal) and waits briefly
// for the process to exit; if it doesn't, it SIGKILLs the whole process
// group so a hung binary surfaces as a bounded test failure, never a
// wedged CI job.
func (h *rpcHarness) killAndWait() {
	_ = h.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = h.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		if h.cmd.Process != nil {
			_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGKILL)
		}
		<-done
	}
}

// rpcRequest is the newline-delimited JSON-RPC 2.0 request/notification
// frame this harness writes to the child's stdin (mirrors the wire shape
// mcp.StdioTransport reads/writes, per the pinned go-sdk v1.6.1 source).
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// send writes a JSON-RPC request (with a fresh id) and returns that id for
// a later call to await.
func (h *rpcHarness) send(method string, params any) int64 {
	h.t.Helper()
	h.idSeq++
	id := h.idSeq
	h.writeLine(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	return id
}

// notify writes a JSON-RPC notification (no id, no response expected).
func (h *rpcHarness) notify(method string, params any) {
	h.t.Helper()
	h.writeLine(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (h *rpcHarness) writeLine(req rpcRequest) {
	h.t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		h.t.Fatalf("marshal request %+v: %v", req, err)
	}
	b = append(b, '\n')
	if _, err := h.stdin.Write(b); err != nil {
		h.t.Fatalf("write to child stdin: %v", err)
	}
}

// await blocks until a JSON-RPC response with the given id is observed on
// the child's stdout, or timeout elapses. Every line read along the way is
// validated as valid JSON carrying "jsonrpc":"2.0" (D-10's stdout-purity
// assertion) -- a single malformed line fails the test immediately,
// regardless of which id it was waiting for. Unsolicited server->client
// "ping" keepalive requests (mcp.Server's KeepAlive option) are answered
// transparently so a slow live run never trips the server's own
// ping-timeout session teardown.
func (h *rpcHarness) await(id int64, timeout time.Duration) map[string]any {
	h.t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case line, ok := <-h.lines:
			if !ok {
				h.t.Fatalf("child stdout closed before response id=%d arrived", id)
			}
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				h.killAndWait()
				h.t.Fatalf("stdout purity violation (D-10): line is not valid JSON: %q: %v", line, err)
			}
			if msg["jsonrpc"] != "2.0" {
				h.killAndWait()
				h.t.Fatalf("stdout purity violation (D-10): line missing jsonrpc:2.0: %q", line)
			}

			if method, _ := msg["method"].(string); method != "" {
				// An incoming request/notification FROM the server. The only one
				// this gate expects is a keepalive "ping" request, which must be
				// answered so the server doesn't tear down the session mid-test.
				if method == "ping" {
					if rawID, hasID := msg["id"]; hasID {
						h.replyEmpty(rawID)
					}
				}
				continue
			}

			gotID, hasID := msg["id"]
			if !hasID {
				continue
			}
			idFloat, ok := gotID.(float64)
			if !ok || int64(idFloat) != id {
				continue
			}
			return msg
		case <-deadline:
			h.killAndWait()
			h.t.Fatalf("timed out after %v waiting for response id=%d", timeout, id)
			return nil
		}
	}
}

// replyEmpty answers an incoming server->client request (only "ping" is
// expected here) with an empty JSON-RPC result, preserving the peer's id.
func (h *rpcHarness) replyEmpty(rawID any) {
	h.t.Helper()
	b, err := json.Marshal(struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      any            `json:"id"`
		Result  map[string]any `json:"result"`
	}{JSONRPC: "2.0", ID: rawID, Result: map[string]any{}})
	if err != nil {
		h.t.Fatalf("marshal ping reply: %v", err)
	}
	b = append(b, '\n')
	if _, err := h.stdin.Write(b); err != nil {
		h.t.Fatalf("write ping reply to child stdin: %v", err)
	}
}

// mustCallResult extracts the CallToolResult-shaped "result" object from
// resp, failing the test if resp instead carries a protocol-level "error"
// (D-11: API/tool errors must surface as isError:true results, never
// protocol errors).
func mustCallResult(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	if errObj, ok := resp["error"]; ok {
		t.Fatalf("unexpected protocol-level JSON-RPC error (want isError result instead): %v", errObj)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("response missing a result object: %v", resp)
	}
	return result
}

// TestLiveStdioJSONRPCGate is the phase's D-13 acceptance gate: it builds
// the real cmd/mcp-server binary, drives a full JSON-RPC session over its
// real stdio transport against the live compose stack, and asserts the
// five-tool surface, a real png->jpg conversion, list round-trips, isError
// on bad input, and stdout purity (D-10) all hold for the actual shipped
// binary -- not a mocked transport.
func TestLiveStdioJSONRPCGate(t *testing.T) {
	baseURL := os.Getenv("E2E_BASE_URL")
	if baseURL == "" {
		t.Skip("E2E_BASE_URL not set; skipping live MCP stdio gate")
	}

	apiKey := provisionMCPClient(t)
	binPath := buildMCPServerBinary(t)

	dialAddr := os.Getenv("E2E_S3_DIAL_ADDR")
	if dialAddr == "" {
		dialAddr = "127.0.0.1:9100"
	}
	outDir := t.TempDir()

	// The CHILD binary's env (exec.Command.Env), not this test process's env
	// -- the binary itself performs the presigned download, so only its own
	// http.Client honors OCTOCONV_S3_DIAL_ADDR.
	childEnv := append(os.Environ(),
		"OCTOCONV_BASE_URL="+baseURL,
		"OCTOCONV_API_KEY="+apiKey,
		"OCTOCONV_OUTPUT_DIR="+outDir,
		"OCTOCONV_S3_DIAL_ADDR="+dialAddr,
	)

	h := startMCPServerProcess(t, binPath, childEnv)

	// 1. initialize handshake.
	initID := h.send("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "octoconv-mcp-live-gate", "version": "0.0.0"},
	})
	initResult := mustCallResult(t, h.await(initID, 15*time.Second))
	if _, ok := initResult["protocolVersion"]; !ok {
		t.Fatalf("initialize result missing protocolVersion: %v", initResult)
	}
	if _, ok := initResult["serverInfo"]; !ok {
		t.Fatalf("initialize result missing serverInfo: %v", initResult)
	}

	h.notify("notifications/initialized", map[string]any{})

	// 2. tools/list -- exactly the five tools (D-01).
	listID := h.send("tools/list", map[string]any{})
	listResult := mustCallResult(t, h.await(listID, 15*time.Second))
	toolsRaw, ok := listResult["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list result missing tools array: %v", listResult)
	}
	gotTools := make(map[string]bool, len(toolsRaw))
	for _, raw := range toolsRaw {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := tool["name"].(string); name != "" {
			gotTools[name] = true
		}
	}
	if len(gotTools) != len(wantTools) {
		t.Errorf("tools/list returned %d tools, want exactly %d: got=%v", len(gotTools), len(wantTools), gotTools)
	}
	for _, name := range wantTools {
		if !gotTools[name] {
			t.Errorf("tools/list missing expected tool %q; got %v", name, gotTools)
		}
	}

	samplePath, err := filepath.Abs(filepath.Join("testdata", "sample.png"))
	if err != nil {
		t.Fatalf("resolve testdata/sample.png: %v", err)
	}
	if _, err := os.Stat(samplePath); err != nil {
		t.Fatalf("testdata fixture missing: %v", err)
	}

	// 3. convert_file happy path: a real png->jpg conversion through the
	// live pipeline (D-13). libvips conversion is fast; the image queue
	// gives a generous but bounded window.
	convID := h.send("tools/call", map[string]any{
		"name":      "convert_file",
		"arguments": map[string]any{"path": samplePath, "target_format": "jpg"},
	})
	convResult := mustCallResult(t, h.await(convID, 90*time.Second))
	if isErr, _ := convResult["isError"].(bool); isErr {
		t.Fatalf("convert_file (png->jpg) returned isError: %v", convResult)
	}
	sc, ok := convResult["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("convert_file result missing structuredContent: %v", convResult)
	}
	presignedURL, _ := sc["presigned_url"].(string)
	if presignedURL == "" {
		t.Errorf("convert_file structuredContent missing non-empty presigned_url: %v", sc)
	}
	localPath, _ := sc["local_path"].(string)
	if localPath == "" {
		t.Fatalf("convert_file structuredContent missing non-empty local_path: %v", sc)
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("convert_file local_path %q does not exist / is not readable: %v", localPath, err)
	}
	if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
		t.Fatalf("convert_file local_path %q head = % x, want JPEG magic bytes ff d8 ff (proves the child binary's OCTOCONV_S3_DIAL_ADDR dial-redirect reached MinIO from the host)", localPath, data[:min(3, len(data))])
	}

	// 4a. list_supported_formats round-trip.
	formatsID := h.send("tools/call", map[string]any{
		"name":      "list_supported_formats",
		"arguments": map[string]any{},
	})
	formatsResult := mustCallResult(t, h.await(formatsID, 15*time.Second))
	if isErr, _ := formatsResult["isError"].(bool); isErr {
		t.Fatalf("list_supported_formats returned isError: %v", formatsResult)
	}
	formatsSC, ok := formatsResult["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("list_supported_formats result missing structuredContent: %v", formatsResult)
	}
	engines, _ := formatsSC["engines"].(map[string]any)
	if len(engines) == 0 {
		t.Errorf("list_supported_formats structuredContent has no engines: %v", formatsSC)
	}

	// 4b. list_presets round-trip.
	presetsID := h.send("tools/call", map[string]any{
		"name":      "list_presets",
		"arguments": map[string]any{},
	})
	presetsResult := mustCallResult(t, h.await(presetsID, 15*time.Second))
	if isErr, _ := presetsResult["isError"].(bool); isErr {
		t.Fatalf("list_presets returned isError: %v", presetsResult)
	}
	if _, ok := presetsResult["structuredContent"].(map[string]any); !ok {
		t.Fatalf("list_presets result missing structuredContent: %v", presetsResult)
	}

	// 5. bad input: target_format AND preset together violates the XOR
	// contract (D-04) -- the API itself 422s, and the tool must surface
	// that as isError:true, never a protocol-level error (D-11).
	badID := h.send("tools/call", map[string]any{
		"name":      "convert_file",
		"arguments": map[string]any{"path": samplePath, "target_format": "jpg", "preset": "definitely-not-a-real-preset"},
	})
	badResp := h.await(badID, 30*time.Second)
	if _, hasProtoErr := badResp["error"]; hasProtoErr {
		t.Fatalf("bad-input convert_file returned a protocol-level error (want isError:true tool result instead, D-11): %v", badResp)
	}
	badResult, ok := badResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("bad-input convert_file response missing result: %v", badResp)
	}
	if isErr, _ := badResult["isError"].(bool); !isErr {
		t.Fatalf("bad-input convert_file (target_format AND preset) expected isError:true, got: %v", badResult)
	}

	fmt.Fprintf(os.Stderr, "live gate ok: tools=%v conversion local_path=%s\n", wantTools, localPath)
}
