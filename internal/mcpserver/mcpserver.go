package mcpserver

import (
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	serverName    = "octoconv"
	serverVersion = "0.1.0"

	// serverKeepAlive is the ping interval used to detect a dead peer across
	// convert_file's potentially multi-minute blocking window. Combined with
	// per-tick NotifyProgress (D-04), this keeps a long-lived stdio session
	// alive instead of silently going idle (PITFALLS P1).
	serverKeepAlive = 30 * time.Second
)

// NewServer builds the OctoConv MCP server and registers exactly five tools
// against c: convert_file, get_job_status, download_result,
// list_supported_formats, list_presets (D-01). cfg is accepted for API
// symmetry with NewClient and so tool descriptions can reference
// config-derived values (e.g. OUTPUT_DIR) without reaching back into c's
// unexported fields.
func NewServer(cfg Config, c *Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    serverName,
		Version: serverVersion,
	}, &mcp.ServerOptions{
		KeepAlive: serverKeepAlive,
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "convert_file",
		Description: "Convert a local file to a target format or via a named preset. " +
			"Provide exactly one of target_format or preset -- never both, never neither. " +
			"Blocks until the conversion finishes (or times out); reports progress on every poll tick. " +
			"Returns a presigned URL and a local path under " + cfg.OutputDir + " -- never inlines file bytes.",
	}, convertFileHandler(c))

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_job_status",
		Description: "Check the status of a conversion job by id without blocking. " +
			"Use this to poll a long-running convert_file job, or after a convert_file timeout.",
	}, getJobStatusHandler(c))

	mcp.AddTool(s, &mcp.Tool{
		Name: "download_result",
		Description: "Download the result of a done conversion job into " + cfg.OutputDir + ". " +
			"Requires the job to already be in the done status (see get_job_status).",
	}, downloadResultHandler(c))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_supported_formats",
		Description: "List the (source, target) format pairs supported by each conversion engine.",
	}, listSupportedFormatsHandler(c))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_presets",
		Description: "List available named conversion presets (client-scoped overrides merged over system defaults).",
	}, listPresetsHandler(c))

	return s
}
