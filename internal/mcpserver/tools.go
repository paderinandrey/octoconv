package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file implements the five agent-facing MCP tools on top of Client
// (client.go, Plan 21-01). Each handler only maps tool input/output shapes
// and (for convert_file) owns progress notification -- the blocking/poll/
// redaction logic itself lives in Client. Handlers never inline file bytes
// (D-07): results always carry a presigned URL and/or a local path.
//
// Errors returned by these handlers are plain (non-jsonrpc) Go errors. The
// go-sdk's generic AddTool wrapper converts any such error into a
// CallToolResult{IsError: true} whose Content carries err.Error() verbatim,
// WITHOUT ever surfacing it as a protocol-level error (verified by reading
// mcp.toolForErr in the pinned v1.6.1 source) -- this is exactly the
// mechanism D-11 requires, so handlers simply return the underlying error.

// ConvertFileInput is the input to the convert_file tool.
type ConvertFileInput struct {
	Path         string         `json:"path" jsonschema:"absolute or relative path to an existing local regular file to convert"`
	TargetFormat string         `json:"target_format,omitempty" jsonschema:"desired output format, e.g. jpg/png/webp -- provide exactly one of target_format or preset, never both, never neither"`
	Preset       string         `json:"preset,omitempty" jsonschema:"name of a saved conversion preset to apply -- provide exactly one of target_format or preset, never both, never neither"`
	Opts         map[string]any `json:"opts,omitempty" jsonschema:"engine-specific conversion options; only meaningful alongside target_format, not preset"`
}

// ConvertFileOutput is the result of a successful convert_file call. It
// never carries file bytes (D-07): only a presigned URL and a local path.
type ConvertFileOutput struct {
	JobID        string `json:"job_id" jsonschema:"the id of the created conversion job"`
	PresignedURL string `json:"presigned_url" jsonschema:"short-lived presigned URL for downloading the converted file directly"`
	LocalPath    string `json:"local_path" jsonschema:"local filesystem path (inside the server's OUTPUT_DIR) where the converted file was already downloaded"`
	TargetFormat string `json:"target_format" jsonschema:"the target_format that was requested; empty when a preset determined the output format instead"`
}

// convertFileHandler builds the convert_file tool handler bound to c. It
// blocks for the full conversion (D-04): upload, poll GetJob on every tick,
// download the result. On every poll tick, if (and only if) the calling
// request supplied a progress token, it emits a best-effort NotifyProgress
// so long-lived stdio clients see continuous forward progress instead of
// hitting an idle-window timeout (D-04, PITFALLS P1).
func convertFileHandler(c *Client) mcp.ToolHandlerFor[ConvertFileInput, ConvertFileOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ConvertFileInput) (*mcp.CallToolResult, ConvertFileOutput, error) {
		path, err := sanitizeInputPath(in.Path)
		if err != nil {
			return nil, ConvertFileOutput{}, err
		}

		var rawOpts string
		if len(in.Opts) > 0 {
			b, err := json.Marshal(in.Opts)
			if err != nil {
				return nil, ConvertFileOutput{}, fmt.Errorf("encode opts: %w", err)
			}
			rawOpts = string(b)
		}

		onTick := progressTicker(ctx, req)

		result, err := c.ConvertBlocking(ctx, path, in.TargetFormat, in.Preset, rawOpts, onTick)
		if err != nil {
			return nil, ConvertFileOutput{}, err
		}

		return nil, ConvertFileOutput{
			JobID:        result.JobID,
			PresignedURL: result.DownloadURL,
			LocalPath:    result.LocalPath,
			TargetFormat: in.TargetFormat,
		}, nil
	}
}

// progressTicker returns an onTick callback for Client.ConvertBlocking that
// emits a NotifyProgress notification per invocation, or nil when the
// calling request supplied no progress token (best-effort, silent no-op --
// D-04). Returning nil (rather than a callback that checks the token on
// every call) means req.Session is never dereferenced unless a token is
// actually present.
func progressTicker(ctx context.Context, req *mcp.CallToolRequest) func(status string) {
	token := req.Params.GetProgressToken()
	if token == nil {
		return nil
	}
	tick := 0
	return func(status string) {
		tick++
		// Best-effort: a failed progress notification must never fail the
		// conversion itself.
		_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
			ProgressToken: token,
			Message:       fmt.Sprintf("job status: %s", status),
			Progress:      float64(tick),
		})
	}
}

// sanitizeInputPath resolves raw to an absolute, cleaned path and confirms
// it names an existing regular file, before any network call is made
// (D-09: never touch the filesystem or upstream API with an unvalidated
// agent-supplied path).
func sanitizeInputPath(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("path is required")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", raw, err)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", raw, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path %q is not a regular file", raw)
	}
	return abs, nil
}

// GetJobStatusInput is the input to the get_job_status tool.
type GetJobStatusInput struct {
	JobID string `json:"job_id" jsonschema:"the job id returned by convert_file, or a previous get_job_status/download_result call"`
}

// getJobStatusHandler builds the get_job_status tool handler bound to c
// (D-05): a non-blocking escape hatch for checking on a long-running job.
// JobStatus (client.go) already carries the exact shape we want to expose,
// so it doubles as this tool's typed Out.
func getJobStatusHandler(c *Client) mcp.ToolHandlerFor[GetJobStatusInput, JobStatus] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in GetJobStatusInput) (*mcp.CallToolResult, JobStatus, error) {
		js, err := c.GetJob(ctx, in.JobID)
		return nil, js, err
	}
}

// DownloadResultInput is the input to the download_result tool.
type DownloadResultInput struct {
	JobID    string `json:"job_id" jsonschema:"the job id of a job whose status is done"`
	Filename string `json:"filename,omitempty" jsonschema:"optional local filename to save the result as (sanitized; always written inside OUTPUT_DIR, never at an agent-controlled path)"`
}

// DownloadResultOutput is the result of a successful download_result call.
// It never carries file bytes (D-07).
type DownloadResultOutput struct {
	LocalPath    string `json:"local_path" jsonschema:"local filesystem path (inside OUTPUT_DIR) where the result was downloaded"`
	PresignedURL string `json:"presigned_url" jsonschema:"the presigned URL the result was downloaded from"`
}

// downloadResultHandler builds the download_result tool handler bound to c
// (D-05): fetches the job, requires it to be done, and downloads the
// presigned result into OUTPUT_DIR. When the agent supplies a filename
// hint, it is sanitized via sanitizeBasename and used to rename the
// downloaded file -- still strictly inside OUTPUT_DIR (D-09); an
// unsanitizable hint is silently ignored in favor of the server-derived
// name Client.Download already chose.
func downloadResultHandler(c *Client) mcp.ToolHandlerFor[DownloadResultInput, DownloadResultOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in DownloadResultInput) (*mcp.CallToolResult, DownloadResultOutput, error) {
		js, err := c.GetJob(ctx, in.JobID)
		if err != nil {
			return nil, DownloadResultOutput{}, err
		}
		if js.Status != statusDone {
			return nil, DownloadResultOutput{}, fmt.Errorf("job %s is %s, not done -- use get_job_status to check on it", in.JobID, js.Status)
		}
		if js.DownloadURL == "" {
			return nil, DownloadResultOutput{}, fmt.Errorf("job %s is done but has no download URL", in.JobID)
		}

		localPath, err := c.Download(ctx, in.JobID, js.DownloadURL)
		if err != nil {
			return nil, DownloadResultOutput{}, err
		}

		if in.Filename != "" {
			if renamed := sanitizeBasename(in.Filename); renamed != "" {
				target := filepath.Join(c.outputDir, renamed)
				if target != localPath {
					if err := os.Rename(localPath, target); err == nil {
						localPath = target
					}
				}
			}
		}

		return nil, DownloadResultOutput{LocalPath: localPath, PresignedURL: js.DownloadURL}, nil
	}
}

// ListSupportedFormatsInput is the (empty) input to the
// list_supported_formats tool.
type ListSupportedFormatsInput struct{}

// listSupportedFormatsHandler builds the list_supported_formats tool
// handler bound to c: a thin wrapper over GET /v1/formats (D-06).
// FormatsResponse (client.go) already carries the exact shape we want to
// expose, so it doubles as this tool's typed Out.
func listSupportedFormatsHandler(c *Client) mcp.ToolHandlerFor[ListSupportedFormatsInput, FormatsResponse] {
	return func(ctx context.Context, req *mcp.CallToolRequest, _ ListSupportedFormatsInput) (*mcp.CallToolResult, FormatsResponse, error) {
		fr, err := c.ListFormats(ctx)
		return nil, fr, err
	}
}

// ListPresetsInput is the input to the list_presets tool.
type ListPresetsInput struct {
	IncludeInactive bool `json:"include_inactive,omitempty" jsonschema:"when true, also include inactive/superseded preset versions; default false returns only active presets"`
}

// ListPresetsOutput is the result of a successful list_presets call.
type ListPresetsOutput struct {
	Presets []Preset `json:"presets" jsonschema:"the resolved list of conversion presets (client-scoped overrides merged over system defaults)"`
}

// listPresetsHandler builds the list_presets tool handler bound to c: a
// thin wrapper over GET /v1/presets (D-06), which already returns the
// merged client+system view.
func listPresetsHandler(c *Client) mcp.ToolHandlerFor[ListPresetsInput, ListPresetsOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ListPresetsInput) (*mcp.CallToolResult, ListPresetsOutput, error) {
		presets, err := c.ListPresets(ctx, in.IncludeInactive)
		if err != nil {
			return nil, ListPresetsOutput{}, err
		}
		return nil, ListPresetsOutput{Presets: presets}, nil
	}
}
