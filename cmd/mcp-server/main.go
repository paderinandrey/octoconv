// Command mcp-server runs the OctoConv MCP server over stdio: a thin
// entrypoint that wires internal/mcpserver's Config + Client + Server and
// runs the stdio JSON-RPC transport to completion (D-01). All conversion
// logic lives in internal/mcpserver; this binary only loads config, builds
// the server, and runs it.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/apaderin/octoconv/internal/mcpserver"
)

func main() {
	// D-10/Pitfall 5: stdout is reserved EXCLUSIVELY for the MCP stdio
	// transport's newline-delimited JSON-RPC stream. Redirect all logging to
	// stderr as the very first statement, before any SDK init or config
	// load, so no code path in this binary can ever write a stray line to
	// stdout ahead of it. (log already defaults to stderr, but this makes the
	// invariant explicit and future-proof against a default change.)
	log.SetOutput(os.Stderr)

	cfg, err := mcpserver.Load()
	if err != nil {
		// D-03: fail fast, non-zero exit, message on stderr only.
		log.Fatalf("config: %v", err)
	}

	client, err := mcpserver.NewClient(cfg)
	if err != nil {
		log.Fatalf("client: %v", err)
	}

	srv := mcpserver.NewServer(cfg, client)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("🐙 octoconv-mcp starting (base_url=%s, output_dir=%s)", cfg.BaseURL, cfg.OutputDir)
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
		log.Fatalf("run: %v", err)
	}
}
