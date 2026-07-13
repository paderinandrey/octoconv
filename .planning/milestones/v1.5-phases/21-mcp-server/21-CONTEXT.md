# Phase 21: MCP Server - Context

**Gathered:** 2026-07-13
**Status:** Ready for planning
**Source:** SEED-003 design sketch + v1.5 research (STACK/FEATURES/ARCHITECTURE/PITFALLS), user-confirmed at requirements and kickoff

<domain>
## Phase Boundary

`cmd/mcp-server` — stdio MCP server exposing OctoConv to agents as tools. Pure HTTP client of the public API (zero internal/* imports). Consumes Phase 20's `/v1/presets` and `/v1/formats`. No streamable-HTTP transport, no compose entry, no write-tools (MCPV2-*).

</domain>

<decisions>
## Implementation Decisions

### Structure & SDK
- D-01: `cmd/mcp-server` (thin main) + `internal/mcpserver` (tool implementations, API client); SDK: `github.com/modelcontextprotocol/go-sdk` pinned ≥v1.6.1 (NOT v1.7.0-pre.*); stdio transport only (`mcp.StdioTransport`)
- D-02: The server is a pure HTTP client of the PUBLIC API — imports NO other internal/* packages (no jobs/presets/convert); hand-rolled minimal API client inside internal/mcpserver (research: e2e helpers are test-only and structurally unimportable; independent client doubles as wire-contract cross-check)
- D-03: Config via env, fail-fast at startup if missing: `OCTOCONV_BASE_URL`, `OCTOCONV_API_KEY`; optional `OCTOCONV_OUTPUT_DIR` (default `os.TempDir()/octoconv-mcp`), `OCTOCONV_CONVERT_TIMEOUT` (default 10m), `OCTOCONV_POLL_INTERVAL` (default 1s)

### Tools (5)
- D-04: `convert_file(path, target_format?, preset?, opts?)` — blocking: multipart upload → poll GET /v1/jobs/{id} every POLL_INTERVAL with `NotifyProgress` on EVERY tick (30-min stdio idle-window discipline, PITFALLS P1) → on done: download presigned result into OUTPUT_DIR → result = {job_id, presigned_url, local_path, target_format}; target_format XOR preset mirrors the API contract (both/neither → isError with the API's 422 text)
- D-05: `get_job_status(job_id)` and `download_result(job_id, filename?)` — non-blocking escape hatch for long jobs; download writes into OUTPUT_DIR only
- D-06: `list_supported_formats` → GET /v1/formats passthrough (shaped for agent readability); `list_presets` → GET /v1/presets (merged view from Phase 20, D-10) — both TOOLS, not resources (host-support variance, FEATURES rec)
- D-07: Tool results NEVER inline file bytes (presigned URL + local path only, FEATURES: 1MB ≈ 1.5M tokens as base64)

### Security (MCP-05 — each is a must_have)
- D-08: API key never appears in any tool result, error text, or progress message — client wraps errors and redacts the Authorization header; a unit test greps serialized results/errors for the key
- D-09: Path handling: input path → filepath.Abs+Clean, must be an existing regular file (no dirs, no symlink escape needed beyond Clean for an internal tool — but reject inputs resolving outside the user's reachable FS is N/A; primary risk is OUTPUT: files are written ONLY into OUTPUT_DIR with a sanitized basename (strip separators/.. from server-supplied filenames), never agent-controlled paths
- D-10: stdout carries ONLY JSON-RPC: all logging via log.SetOutput(os.Stderr) BEFORE any SDK init; a live-gate assertion greps captured stdout for non-JSON lines
- D-11: API 4xx/5xx → MCP `isError: true` tool results (never protocol errors); the API's own no-leak texts pass through verbatim

### Verification
- D-12: Unit layer: httptest fake of the API (job lifecycle, 422s, presign) — full tool coverage without docker
- D-13: LIVE HARD GATE: scripted JSON-RPC session over the real binary's stdio against the real compose stack — initialize handshake, tools/list (5 tools), convert_file on a real PNG (png→jpg), assert result has presigned_url + existing local_path with correct magic bytes, list_presets/list_supported_formats round-trip, isError on bad input; stdout-purity assertion. Real Claude Code client session = OPTIONAL operator corroboration, not the gate (checkpoint only if the scripted gate can't run)
- D-14: README gains an "MCP server" section with a claude_desktop/claude-code config JSON example

### Claude's Discretion
- Exact tool descriptions/annotations wording (optimize for agent tool-choice)
- Poll backoff shape (fixed vs slight jitter)
- Progress message content per tick

</decisions>

<canonical_refs>
## Canonical References

### Consumed API surface (Phase 20 + earlier)
- `internal/api/routes.go` + `presets_handlers.go` + `formats_handlers.go` — endpoint contracts
- `internal/api/handlers.go` — POST /v1/jobs multipart contract (file, target_format XOR preset, opts), GET /v1/jobs/{id} response shape (status, download_url)
- `internal/e2e/e2e_test.go` — reference for the wire contract (READ ONLY — do not import)

### Research
- `.planning/research/STACK.md` — go-sdk v1.6.1 API shape (mcp.NewServer/AddTool/StdioTransport, NotifyProgress, KeepAlive), pin rationale
- `.planning/research/FEATURES.md` — tool UX, isError mapping, no-base64
- `.planning/research/ARCHITECTURE.md` — package layout, zero-internal-imports policy
- `.planning/research/PITFALLS.md` — P1 idle-window/progress, P2 stdout purity, P3 context bloat, key-leak, path traversal
- `.planning/seeds/SEED-003.md` — original design intent

</canonical_refs>

<specifics>
## Specific Ideas

- The live-gate script can drive stdio JSON-RPC with a small Go test helper or `jq -c` + coprocess in bash; a Go-based `internal/mcpserver` integration test with exec.Command on the built binary is likely cleanest (env-gated on E2E_BASE_URL like internal/e2e)
- go.mod gains ONE new dependency (the MCP SDK) — package legitimacy: official modelcontextprotocol org, verified in research
- After phase close, SEED-003 gets status: implemented

</specifics>

<deferred>
## Deferred Ideas

- Streamable HTTP transport + compose entry (MCPV2-01)
- MCP resources (MCPV2-02)
- Write-tools (preset management via MCP)
</deferred>

---

*Phase: 21-mcp-server*
*Context gathered: 2026-07-13 (research-derived, user-confirmed)*
