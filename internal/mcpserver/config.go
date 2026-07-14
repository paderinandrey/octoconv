// Package mcpserver is a pure HTTP client of the OctoConv public API: it
// implements the blocking convert workflow (multipart upload -> poll -> a
// presigned download) that every MCP tool built on top of it calls into. It
// imports NO other internal/* package (D-02) -- the wire contract it speaks
// is internal/api's public HTTP surface, never the API's internal Go types.
package mcpserver

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	envBaseURL       = "OCTOCONV_BASE_URL"
	envAPIKey        = "OCTOCONV_API_KEY"
	envOutputDir     = "OCTOCONV_OUTPUT_DIR"
	envConvertTO     = "OCTOCONV_CONVERT_TIMEOUT"
	envPollInterval  = "OCTOCONV_POLL_INTERVAL"
	envS3DialAddr    = "OCTOCONV_S3_DIAL_ADDR"
	defaultConvertTO = 10 * time.Minute
	defaultPollEvery = time.Second
)

// ResultMode selects how tool results are shaped: local (stdio) mode
// downloads results into OUTPUT_DIR and returns a local path alongside the
// presigned URL; remote (HTTP) mode is presigned-only -- no server-side
// download, no local path, no filesystem writes (D-04, Phase 25).
type ResultMode string

const (
	// ResultLocal is the zero value, so every existing caller (the stdio
	// binary, all pre-Phase-25 tests) keeps today's behavior without any
	// change: results are downloaded into OUTPUT_DIR and carry a local path.
	ResultLocal ResultMode = ""
	// ResultRemote is HTTP mode: results carry only the presigned URL (plus
	// an expiry note); the server never writes files (D-04, MCPH-02).
	ResultRemote ResultMode = "remote"
)

// Config is the env-driven configuration for the MCP client (D-03).
// OCTOCONV_BASE_URL and OCTOCONV_API_KEY are required; everything else has a
// documented default. S3DialAddr is an optional escape hatch (empty is a
// production no-op) that lets a host-run binary redial presigned-download
// requests to a compose-internal MinIO while preserving the Host header the
// V4 signature was computed against.
type Config struct {
	BaseURL        string
	APIKey         string
	OutputDir      string
	ConvertTimeout time.Duration
	PollInterval   time.Duration
	S3DialAddr     string
	// ResultMode selects local (zero value; stdio) vs remote (HTTP,
	// presigned-only) result shaping. Not read from the environment: the
	// binary chooses its own mode (stdio leaves it zero; mcp-http sets
	// ResultRemote explicitly).
	ResultMode ResultMode
}

// Load reads Config from the environment, failing fast (naming the missing
// variable) when a required value is absent.
func Load() (Config, error) {
	baseURL := os.Getenv(envBaseURL)
	if baseURL == "" {
		return Config{}, fmt.Errorf("missing required environment variable %s", envBaseURL)
	}
	apiKey := os.Getenv(envAPIKey)
	if apiKey == "" {
		return Config{}, fmt.Errorf("missing required environment variable %s", envAPIKey)
	}

	outputDir := os.Getenv(envOutputDir)
	if outputDir == "" {
		outputDir = filepath.Join(os.TempDir(), "octoconv-mcp")
	}

	return Config{
		BaseURL:        baseURL,
		APIKey:         apiKey,
		OutputDir:      outputDir,
		ConvertTimeout: envDuration(envConvertTO, defaultConvertTO),
		PollInterval:   envDuration(envPollInterval, defaultPollEvery),
		// S3DialAddr is validated only as a string (empty is valid and is the
		// production default) -- no fallback/default value beyond "".
		S3DialAddr: os.Getenv(envS3DialAddr),
	}, nil
}

// envDuration reads key as a time.Duration, falling back to def if unset or
// unparsable. Trailing inline comments/whitespace (as tolerated elsewhere in
// this codebase's .env-sourced values) are stripped via firstField.
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(firstField(v)); err == nil {
			return d
		}
	}
	return def
}

// firstField returns the leading whitespace-delimited token of s, mirroring
// cmd/api and cmd/worker's identical helper (duplicated here rather than
// imported, since internal/mcpserver may import no other internal/* package
// and cmd/* is not importable).
func firstField(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}
