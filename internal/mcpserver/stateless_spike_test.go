// D-02 decision gate: this file is a self-contained, one-shot live spike
// answering a single question before cmd/mcp-http (Task 3) commits to a
// StreamableHTTPOptions shape: does convert_file-style in-flight progress
// notification (req.Session.NotifyProgress, called from inside a tool
// handler while the calling request is still open -- exactly what
// progressTicker in tools.go does) actually reach an HTTP client when the
// go-sdk's streamable handler runs with Stateless: true?
//
// The go-sdk's own doc comment on StreamableHTTPOptions.Stateless says:
// "Server->Client notifications may reach the client if they are made in
// the context of an incoming request" -- convert_file's NotifyProgress call
// is made from exactly that context (inside the CallTool handler, using the
// ctx/req of the still-open tool call), so this is the precise case the
// spike must confirm rather than assume.
//
// It is deliberately NOT coupled to the OctoConv API or to internal/mcpserver's
// own Client/Config/NewServer: the tool under test is a minimal stub that only
// emits progress and returns, mirroring the pattern in tools.go's
// progressTicker and the client-side progress-handler wiring already used by
// tools_test.go's newHarness/copts.
package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// spikeToolInput is the (empty) input to the stub progress-emitting tool.
type spikeToolInput struct{}

// spikeToolOutput is the (empty) output of the stub progress-emitting tool --
// only the progress notifications sent during the call matter to this spike.
type spikeToolOutput struct{}

// TestStateless_ProgressNotificationsReachClient is the D-02 verdict test.
// It stands up a real streamable-HTTP handler with Stateless: true behind
// httptest.NewServer, connects a real go-sdk streamable client, calls a tool
// that emits several NotifyProgress calls from within its own handler (the
// same "in the context of an incoming request" case the SDK doc calls out),
// and asserts the client observed at least one of them.
func TestStateless_ProgressNotificationsReachClient(t *testing.T) {
	getServer := func(*http.Request) *mcp.Server {
		s := mcp.NewServer(&mcp.Implementation{
			Name:    "stateless-spike",
			Version: "0.0.1",
		}, nil)

		mcp.AddTool(s, &mcp.Tool{
			Name:        "progress_stub",
			Description: "emits a few progress notifications, then returns -- D-02 spike only, not a real OctoConv tool",
		}, func(ctx context.Context, req *mcp.CallToolRequest, _ spikeToolInput) (*mcp.CallToolResult, spikeToolOutput, error) {
			token := req.Params.GetProgressToken()
			if token != nil {
				for i := 1; i <= 3; i++ {
					// Best-effort, mirroring progressTicker in tools.go: a failed
					// notification must never fail the tool call itself.
					_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
						ProgressToken: token,
						Message:       fmt.Sprintf("spike tick %d", i),
						Progress:      float64(i),
					})
				}
			}
			return nil, spikeToolOutput{}, nil
		})

		return s
	}

	handler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
	srv := httptest.NewServer(handler)
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

	client := mcp.NewClient(&mcp.Implementation{Name: "spike-client", Version: "0.0.1"}, copts)
	ctx := context.Background()
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
	if err != nil {
		t.Fatalf("client Connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "progress_stub",
		Arguments: map[string]any{},
		Meta:      mcp.Meta{"progressToken": "spike-tok"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError = true, want false; content = %v", res.Content)
	}

	// Notification dispatch may run a beat after CallTool's own response is
	// processed (mirrors tools_test.go's TestConvertFile_ProgressNotifiedOnEveryTick
	// polling pattern) -- poll briefly rather than assuming same-instant delivery.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(progress)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			// D-02 VERDICT: stateless mode did NOT deliver progress notifications
			// to the HTTP client within the polling window. Task 3's
			// cmd/mcp-http MUST NOT use StreamableHTTPOptions{Stateless: true};
			// it must fall back to stateful sessions (Stateless omitted/false)
			// and the deployment must be pinned to a single replica (PITFALLS
			// P6, already the plan). Record this verdict in the plan's SUMMARY.
			t.Fatalf("D-02 VERDICT: stateless progress notifications did NOT reach the HTTP client (got 0 within %v) -- cmd/mcp-http must use the stateful fallback, not Stateless: true", 2*time.Second)
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	got := len(progress)
	first := progress[0]
	mu.Unlock()

	if first.ProgressToken != "spike-tok" {
		t.Fatalf("progress[0].ProgressToken = %v, want spike-tok", first.ProgressToken)
	}

	// D-02 VERDICT: stateless progress notifications DID reach the HTTP
	// client (observed at least one in-flight NotifyProgress delivered over
	// the streamable transport with Stateless: true). cmd/mcp-http (Task 3)
	// uses StreamableHTTPOptions{Stateless: true}. The session-key binding
	// middleware still ships unconditionally (D-03/D-07) -- it is inert
	// belt-and-suspenders in this stateless mode, since getServer already
	// runs per request.
	t.Logf("D-02 VERDICT: PASS -- stateless mode delivered %d progress notification(s) to the HTTP client; cmd/mcp-http will use StreamableHTTPOptions{Stateless: true}", got)
}
