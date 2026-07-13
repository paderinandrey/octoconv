---
phase: 21-mcp-server
verified: 2026-07-13T04:17:59Z
status: passed
score: 13/13 must-haves verified
overrides_applied: 0
---

# Phase 21: MCP Server Verification Report

**Phase Goal:** Agents (e.g. a Claude Code session) can convert files and discover capabilities through a stdio MCP server that holds zero privileged access and is a pure HTTP client of the existing public API.
**Verified:** 2026-07-13T04:17:59Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria + PLAN must_haves, merged)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `convert_file` from a real MCP client session converts a real file (blocking, XOR target_format/preset) and returns presigned URL + local path; bytes never inlined | VERIFIED | `internal/mcpserver/tools.go:26-79` (`ConvertFileOutput` has no bytes field); live gate `TestLiveStdioJSONRPCGate` executed by orchestrator on main: `--- PASS (2.80s)`, asserting a real png→jpg conversion returns `presigned_url` + an existing `local_path` with JPEG magic bytes (`mcp_live_test.go:429-449`). Offline: `TestConvertFile_HappyPath` passes with a no-inline-bytes assertion. |
| 2 | `get_job_status` and `download_result` support the non-blocking flow; `list_supported_formats`/`list_presets` return live data via Phase 20 REST endpoints | VERIFIED | `tools.go:128-233` implements all four; `client.go` `GetJob`/`ListFormats`/`ListPresets` hit `/v1/jobs/{id}`, `/v1/formats`, `/v1/presets`. Live gate confirms non-isError round-trips for list_supported_formats/list_presets (`mcp_live_test.go:455-477`). |
| 3 | Poll loop emits progress each tick and enforces its own max-duration guard (30-min idle-window survival) | VERIFIED | `client.go:347-391` `ConvertBlocking` ticks every `PollInterval`, calls `onTick` every iteration, bounded by `ConvertTimeout`, returns `*TimeoutError` carrying job id on deadline. `tools.go:87-103` `progressTicker` calls `Session.NotifyProgress` on every tick when a token is present. Unit test `TestConvertFile_ProgressNotifiedOnEveryTick` passes; `NewServer` sets `KeepAlive: 30s` (`mcpserver.go:17,31`). |
| 4 | API key never appears in tool result/error text; paths canonicalized/contained (no traversal); stdout carries only JSON-RPC (logs to stderr); API errors map to isError, not protocol errors | VERIFIED | `client.go:411-453` `redact()`/`apiError`/`wrapTransportErr`; unit test `TestClient_KeyNeverLeaks` passes. `tools.go:109-126` `sanitizeInputPath` (Abs+Clean+regular-file check before any I/O); `client.go:393-409` `sanitizeBasename` strips separators/`..`; `TestClient_PathSanitization` passes. `cmd/mcp-server/main.go:27` `log.SetOutput(os.Stderr)` is the first statement; `grep 'fmt.Print' cmd/mcp-server/` finds nothing; live gate asserts every stdout line is valid `jsonrpc:"2.0"` JSON (`mcp_live_test.go:250-272`). isError mapping verified by reading pinned SDK source (`toolForErr`) per 21-02 SUMMARY, and proven by `TestConvertFile_BothTargetAndPreset_IsError`/`TestConvertFile_UnsupportedPair_IsError`/`TestConvertFile_BadPath_IsError`, plus live bad-input case (`mcp_live_test.go:485-499`). |
| 5 (D-01) | MCP SDK linked only into internal/mcpserver + cmd/mcp-server; zero transitive bleed into api/worker/document-worker/chromium-worker/webhook-worker | VERIFIED | `go list -deps` over the five production binaries → 0 `modelcontextprotocol` hits (reproduced live, see Evidence Trail). |
| 6 (D-02) | internal/mcpserver imports NO other internal/* package | VERIFIED | `go list -deps github.com/apaderin/octoconv/internal/mcpserver \| grep -E 'octoconv/internal/(jobs\|presets\|convert\|storage\|api\|queue\|worker)'` → empty. Only production-code import of `internal/*` from this surface is `cmd/mcp-server/main.go` importing `internal/mcpserver` itself (expected; test-only files import `clients`/`auth`/`db` for the live-gate provisioning, explicitly scoped out of D-02 by both PLAN and CONTEXT). |
| 7 (D-03) | Config fails fast at startup on missing OCTOCONV_BASE_URL/OCTOCONV_API_KEY; documented defaults for the rest | VERIFIED | `config.go:43-68`; reproduced live: built binary with empty env → exit 1, stderr `"config: missing required environment variable OCTOCONV_BASE_URL"`, stdout empty (0 bytes). |
| 8 (D-07) | Client/tool results never inline file bytes — presigned URL + local path only | VERIFIED | `ConvertResult`/`ConvertFileOutput`/`DownloadResultOutput` structs carry only string paths/URLs (`client.go:111-117`, `tools.go:34-41,150-155`); `Download` streams directly to disk via `io.Copy`, never buffers into a returned value (`client.go:284-320`). |
| 9 (D-08) | API key never appears in any returned error string | VERIFIED | `TestClient_KeyNeverLeaks` (client_test.go) passes offline. |
| 10 (D-09) | Server-supplied output filenames sanitized to a basename; writes only inside OUTPUT_DIR | VERIFIED | `TestClient_PathSanitization` passes; `sanitizeBasename` (client.go:393-409) strips separators/leading dots, rejects `.`/`..`/empty. |
| 11 (D-12) | httptest fake + in-memory MCP session drive full unit coverage offline, no docker | VERIFIED | `go test ./internal/mcpserver/... -count=1 -v` → 22 tests PASS, 1 (`TestLiveStdioJSONRPCGate`) SKIP (self-skip, no E2E_BASE_URL) — reproduced directly in this verification session. |
| 12 (D-13) | Live hard gate: real binary over stdio JSON-RPC against the live compose stack — 5 tools, real png→jpg, list round-trips, isError, stdout purity | VERIFIED | Orchestrator-run `TestLiveStdioJSONRPCGate --- PASS (2.80s)` on main after OrbStack restart (recorded in 21-03-SUMMARY.md "RESOLVED" appendix and supplied as a verification fact). Test code inspected directly (`mcp_live_test.go`) and confirms it asserts exactly the claimed behaviors (five tools, JPEG magic bytes, isError on bad input, stdout purity) — this is not merely a SUMMARY claim; the test source was read line-by-line and its assertions map 1:1 to the SUMMARY's narrative. Not independently re-run in this verification session (stack state not re-confirmed); accepted per the supplied verification facts as primary evidence, since re-running was explicitly marked optional by the orchestrator and the test source review corroborates the claim was not fabricated. |
| 13 (D-14) | README gains an "MCP server" section with client config JSON example | VERIFIED | `README.md` "## MCP server" section present: tool table, build command, env var table, `claude_desktop`/`claude-code` JSON config block naming the binary (read directly). |

**Score:** 13/13 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/mcpserver/config.go` | env Config, fail-fast | VERIFIED | 94 lines; `Load()` validates required vars, documented defaults, `S3DialAddr` optional |
| `internal/mcpserver/client.go` | HTTP API client (CreateJob/GetJob/Download/ListFormats/ListPresets/ConvertBlocking) | VERIFIED | 454 lines; all six methods present, stdlib-only imports, redaction/sanitization/dial-redirect implemented |
| `internal/mcpserver/client_test.go` | httptest-fake unit tests incl. key-redaction, path-sanitization, dial-redirect | VERIFIED | 9 tests, all pass (`TestClient_KeyNeverLeaks`, `TestClient_PathSanitization`, `TestClient_DialRedirect` with control case) |
| `internal/mcpserver/tools.go` | five typed MCP tool handlers + In/Out structs | VERIFIED | 234 lines; `convert_file`, `get_job_status`, `download_result`, `list_supported_formats`, `list_presets` handlers present |
| `internal/mcpserver/mcpserver.go` | `NewServer(cfg, client)` registers all five tools | VERIFIED | 66 lines; 5x `mcp.AddTool` calls, `KeepAlive: 30s` |
| `internal/mcpserver/tools_test.go` | handler-level unit tests incl. isError mapping and no-inline-bytes | VERIFIED | 13 tests, all pass, incl. `TestServer_RegistersFiveTools` |
| `cmd/mcp-server/main.go` | thin stdio entrypoint, stderr logging, fail-fast | VERIFIED | 50 lines; `log.SetOutput(os.Stderr)` first statement, `Config.Load` fail-fast, `srv.Run(ctx, &mcp.StdioTransport{})` |
| `internal/mcpserver/mcp_live_test.go` | E2E_BASE_URL-gated live stdio JSON-RPC hard gate | VERIFIED | 502 lines; self-skips offline (confirmed), asserts 5 tools/JPEG magic/isError/stdout purity/child-env dial-redirect |
| `README.md` | MCP server section with config JSON | VERIFIED | "## MCP server" section present with tool table, env vars, JSON config example |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/mcpserver/client.go` | `POST /v1/jobs` | multipart + `Authorization: ApiKey` | WIRED | `client.go:171-176`; `TestClientConvertBlocking_HappyPath` exercises it |
| `internal/mcpserver/client.go` | `GET /v1/jobs/{id}` | poll loop + onTick | WIRED | `client.go:358-364` inside `ConvertBlocking` |
| `internal/mcpserver/mcpserver.go` | `internal/mcpserver/client.go` | `NewServer(cfg, c)` wires tools to Client | WIRED | `mcpserver.go:26-64`, each handler closes over `c *Client` |
| `internal/mcpserver/tools.go` | `mcp.CallToolResult` | plain-error → `IsError` via SDK's `AddTool`/`toolForErr` | WIRED | Confirmed by reading pinned v1.6.1 SDK source (per 21-02 SUMMARY) and proven behaviorally by `TestConvertFile_BothTargetAndPreset_IsError` et al. |
| `cmd/mcp-server/main.go` | `internal/mcpserver.NewServer` | wires Config+Client, runs StdioTransport | WIRED | `main.go:35-48` |
| `internal/mcpserver/mcp_live_test.go` | `cmd/mcp-server` (built binary) | `exec.Command` over stdio, `OCTOCONV_S3_DIAL_ADDR` on child env | WIRED | `mcp_live_test.go:353-366` sets `cmd.Env` including the dial-redirect var; live run confirmed the download reached MinIO via this path (JPEG magic bytes asserted) |

### Data-Flow Trace (Level 4)

Not applicable in the UI-rendering sense (no frontend). The equivalent trace for this phase — tool handler → Client → live API/S3 — was exercised end-to-end by the live gate (D-13), which is the strongest available data-flow proof: a real file went in, a real converted file came back with correct magic bytes.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Binary fails fast on missing required env | `OCTOCONV_BASE_URL= OCTOCONV_API_KEY= /tmp/octoconv-mcp-verify` | exit 1; stderr: `config: missing required environment variable OCTOCONV_BASE_URL`; stdout: 0 bytes | PASS |
| No `fmt.Print*` in cmd/mcp-server | `grep -rn 'fmt\.Print' cmd/mcp-server/` | no matches (exit 1 from grep = no hits) | PASS |
| `log.SetOutput(os.Stderr)` precedes SDK init | inspected `main.go:20-33` | confirmed first statement in `main()` | PASS |
| Zero internal/* imports from internal/mcpserver production code | `go list -deps .../internal/mcpserver \| grep -E 'internal/(jobs\|presets\|convert\|storage\|api\|queue\|worker)'` | no matches | PASS |
| Zero MCP SDK transitive bleed into 5 production binaries | `go list -deps` over api/worker/document-worker/chromium-worker/webhook-worker, grep `modelcontextprotocol` | count = 0 | PASS |
| Offline test suite green | `go test ./internal/mcpserver/... -count=1 -v` | 22 PASS, 1 SKIP (live gate self-skip, no E2E_BASE_URL) | PASS |
| Full repo build/vet/format clean | `go build ./...`, `go vet ./...`, `gofmt -l .` | all clean (no output) | PASS |
| go.sum integrity | `go mod verify` | "all modules verified" | PASS |
| SDK pin | `grep modelcontextprotocol/go-sdk go.mod` | `v1.6.1` (no `-pre` suffix) | PASS |

### Probe Execution

Not applicable — this is not a migration/tooling-probe phase; there are no `scripts/*/tests/probe-*.sh` files declared or discovered for this phase. The equivalent hard gate is D-13's `TestLiveStdioJSONRPCGate`, covered above under Behavioral Spot-Checks / Observable Truths (item 12).

| Probe | Command | Result | Status |
|-------|---------|--------|--------|
| `TestLiveStdioJSONRPCGate` (live gate, not a shell probe) | `go test ./internal/mcpserver/... -run Live -count=1 -v` (against live compose stack) | `--- PASS (2.80s)`, run by orchestrator after OrbStack restart (per supplied verification facts); not independently re-executed in this verification session (marked optional) | PASS (accepted, not independently re-run) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| MCP-01 | 21-01, 21-02, 21-03 | Blocking `convert_file` with progress notification on every poll tick | SATISFIED | `client.go` ConvertBlocking + `tools.go` progressTicker + live gate |
| MCP-02 | 21-01, 21-03 | Result = presigned URL + local path, no inline bytes | SATISFIED | `ConvertResult`/`ConvertFileOutput`, live gate JPEG-magic assertion |
| MCP-03 | 21-02 | `get_job_status`/`download_result` non-blocking tools | SATISFIED | `tools.go:128-195` |
| MCP-04 | 21-02 | `list_supported_formats`/`list_presets` (merged view) as tools | SATISFIED | `tools.go:197-233` |
| MCP-05 | 21-01, 21-02, 21-03 | Key redaction, path canonicalization, stdout purity, isError mapping | SATISFIED | key-redaction/path-sanitization unit tests, stdout-purity live assertion, isError unit+live tests |

**Note (tracking-doc lag, not a code gap):** `.planning/REQUIREMENTS.md` still shows MCP-01..05 as unchecked `[ ]` / "Pending" in its coverage table, and `.planning/STATE.md` still reads "Phase 21 execution started... Plan 1 of 3" — both stale relative to the actual completed work (ROADMAP.md itself already marks Phase 21 `[x]` complete, and all three plans/summaries exist and are commit-verified). This is an administrative bookkeeping gap, not a phase-goal deficiency, and does not block phase closure — but the next `/gsd` step that touches these files should reconcile them.

### Anti-Patterns Found

None. Grep for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER|not yet implemented|coming soon` across all phase-modified files (`config.go`, `client.go`, `tools.go`, `mcpserver.go`, `main.go`) returned zero matches. No empty handler stubs (`return null`, `=> {}`), no hardcoded-empty data flowing to results. `gofmt -l .` and `go vet ./...` both clean across the whole repo.

### Human Verification Required

None. The phase's designated acceptance gate (D-13, a scripted live JSON-RPC session against the real binary and the real compose stack) is a fully automated hard gate, not a manual/visual check, and it has already passed (orchestrator-run, `TestLiveStdioJSONRPCGate --- PASS (2.80s)`). A real Claude Code client session was explicitly scoped in 21-CONTEXT.md (D-13) as OPTIONAL operator corroboration, not a required gate — its absence does not create a human-verification requirement.

### Gaps Summary

No gaps. All 13 must-have truths (roadmap Success Criteria + PLAN frontmatter decisions D-01 through D-14, excluding Claude's-discretion items) verified against actual code and test execution, not SUMMARY narrative alone:

- Source code for all listed artifacts was read directly and checked against each must-have's specific claim (redaction, sanitization, dial-redirect, isError mapping, stdout purity, fail-fast).
- The full offline test suite (22 tests) was re-executed in this verification session and passed; the live test correctly self-skips offline.
- `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go mod verify` were all re-run clean.
- Transitive SDK isolation and zero-internal-import invariants were independently re-verified via `go list -deps`, not merely trusted from the SUMMARY.
- The one item not independently re-executed in this session — the live D-13 gate against the running compose stack — was accepted on the strength of (a) the orchestrator's own fresh run recorded with a concrete PASS timing (2.80s) after infra recovery, and (b) direct line-by-line reading of `mcp_live_test.go`'s assertions confirming they match the claimed behaviors (not a rubber-stamp SUMMARY claim). This is flagged transparently rather than silently accepted; re-running was explicitly optional per the supplied verification facts.

Only administrative/tracking-doc lag was found (REQUIREMENTS.md checkboxes, STATE.md position marker) — noted above for cleanup, not treated as a phase-goal gap since ROADMAP.md (the authoritative phase contract) already reflects completion and all code/tests independently confirm the goal is achieved.

---

*Verified: 2026-07-13T04:17:59Z*
*Verifier: Claude (gsd-verifier)*
