---
phase: 21-mcp-server
plan: 01
subsystem: api
tags: [mcp, modelcontextprotocol, go-sdk, http-client, httptest, dial-redirect]

# Dependency graph
requires: []
provides:
  - "modelcontextprotocol/go-sdk v1.6.1 pinned as a direct dependency, isolated to the MCP surface (zero transitive bleed into the five production binaries)"
  - "internal/mcpserver.Config: env-driven, fail-fast on missing OCTOCONV_BASE_URL/OCTOCONV_API_KEY, with documented defaults and an optional OCTOCONV_S3_DIAL_ADDR escape hatch"
  - "internal/mcpserver.Client: CreateJob/GetJob/Download/ListFormats/ListPresets/ConvertBlocking against the public OctoConv API, stdlib-only, zero internal/* imports"
  - "httptest-fake unit coverage proving key-redaction (D-08), path-containment (D-09), and the dial-redirect knob (D-12) offline, no docker"
affects: [21-mcp-server-plan-02, cmd-mcp-server]

# Tech tracking
tech-stack:
  added: ["github.com/modelcontextprotocol/go-sdk v1.6.1 (pinned direct dep; not yet imported by any code -- reserved for Plan 21-02's cmd/mcp-server + tool layer)"]
  patterns:
    - "internal/mcpserver imports NO other internal/* package (D-02) -- a pure hand-rolled HTTP client of the public API, independently cross-checking the wire contract"
    - "Dial-redirect download client mirrors internal/e2e's downloadClient() exactly: empty knob = plain timeout-only *http.Client, set knob = custom Transport.DialContext that ignores the dialed addr and redials the fixed OCTOCONV_S3_DIAL_ADDR while leaving the request URL/Host untouched (preserves presigned V4 signature)"
    - "Every client error path passes through redact() before being returned, replacing any Authorization-header key substring with a fixed placeholder -- defense in depth even though the key never appears in a URL"

key-files:
  created:
    - internal/mcpserver/config.go
    - internal/mcpserver/client.go
    - internal/mcpserver/client_test.go
    - internal/mcpserver/testdata/sample.png
  modified:
    - go.mod
    - go.sum

key-decisions:
  - "Official modelcontextprotocol/go-sdk (v1.6.1, pinned) chosen over mark3labs/mcp-go per STACK.md research: stdio-only transport requirement removes mcp-go's main HTTP/SSE advantage, and the official SDK is the safer long-term bet for spec compliance (Google co-maintains it)."
  - "sanitizeBasename operates on a single rawPath argument and returns \"\" when nothing safe remains; Download supplies its own job-id-derived fallback name rather than sanitizeBasename baking in a fallback -- keeps the sanitizer a pure, independently-testable function."
  - "ConvertBlocking's timeout path returns a typed *TimeoutError carrying the job id (not a bare sentinel error) so a calling MCP tool (Plan 21-02) can hand off to get_job_status/download_result without losing the job reference."

requirements-completed: [MCP-01, MCP-02, MCP-05]

# Metrics
duration: 60min
completed: 2026-07-13
---

# Phase 21 Plan 01: MCP SDK Dependency + Config + HTTP API Client Summary

**Hand-rolled, zero-internal-import HTTP client of the OctoConv public API implementing the blocking convert workflow (multipart upload -> poll -> presigned download), with API-key redaction, output-path containment, and an optional Host-preserving dial-redirect knob for presigned downloads -- all proven by offline httptest unit tests.**

## Performance

- **Duration:** ~60 min
- **Started:** 2026-07-13T05:02:00+03:00 (approx.)
- **Completed:** 2026-07-13T06:04:00+03:00
- **Tasks:** 3/3 completed
- **Files modified:** 6 (2 modified: go.mod, go.sum; 4 created: config.go, client.go, client_test.go, testdata/sample.png)

## Accomplishments
- Pinned `github.com/modelcontextprotocol/go-sdk` at v1.6.1 (never a `v1.7.0-pre.*`), the project's first new direct dependency since v1.0, with a verified-zero transitive footprint across `cmd/api`, `cmd/worker`, `cmd/document-worker`, `cmd/chromium-worker`, and `cmd/webhook-worker`.
- Built `internal/mcpserver.Config` (fail-fast on missing `OCTOCONV_BASE_URL`/`OCTOCONV_API_KEY`, documented defaults for output dir/timeouts, optional `OCTOCONV_S3_DIAL_ADDR`).
- Built `internal/mcpserver.Client`, a stdlib-only HTTP client implementing `CreateJob`, `GetJob`, `Download`, `ListFormats`, `ListPresets`, and the composed blocking workflow `ConvertBlocking` (create -> poll every `PollInterval` with an `onTick` callback -> download on `done`, non-leaking error on `failed`, a job-id-carrying `*TimeoutError` on deadline).
- Proved, via `net/http/httptest` (no docker), that: the API key never appears in any returned error string (401 and transport-error cases); a `download_url` ending in `../../evil.jpg` still writes strictly inside `OUTPUT_DIR` with a stripped basename; and the `OCTOCONV_S3_DIAL_ADDR` knob is what makes an unresolvable-host download succeed (with an empty-knob control run failing the same request).

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the MCP Go SDK dependency (supply-chain gated)** - `60e0d0c` (chore)
2. **Task 2: Config + hand-rolled HTTP API client with redaction and path containment** - `1df1623` (feat)
3. **Task 3: httptest-fake unit tests (lifecycle, 422, presign, redaction, sanitization, dial-redirect)** - `de5fa07` (test)

**Plan metadata:** (this commit, see below)

## Files Created/Modified
- `go.mod` / `go.sum` - pinned `modelcontextprotocol/go-sdk v1.6.1`
- `internal/mcpserver/config.go` - `Config` struct + `Load()`, env-driven with fail-fast validation
- `internal/mcpserver/client.go` - `Client` (baseURL/apiKey/httpClient/outputDir/convertTimeout/pollInterval/s3DialAddr) implementing the full API surface plus `sanitizeBasename`, `redact`, `apiError`, `downloadClient`
- `internal/mcpserver/client_test.go` - httptest-fake coverage: config defaults/fail-fast, full convert lifecycle, 422, key-redaction (401 + transport error), path-sanitization, dial-redirect (+control), list-formats/list-presets
- `internal/mcpserver/testdata/sample.png` - tiny valid PNG fixture (copied from `internal/e2e/testdata/sample.png`)

## Decisions Made
- Official `modelcontextprotocol/go-sdk` v1.6.1 over `mark3labs/mcp-go`, per STACK.md: stdio-only requirement removes mcp-go's main advantage; official SDK is the safer long-term spec-compliance bet.
- `go get`'d the SDK without a subsequent `go mod tidy`-driven prune: since no code imports it yet (client.go is deliberately SDK-free per the plan; the SDK lands in Plan 21-02's tool/binary layer), `go.mod` marks it `// indirect` for now -- accurate today, will flip to direct once `cmd/mcp-server` imports it.
- `Download` takes an explicit `jobID` parameter (not just the presigned URL) so it can derive a safe fallback filename (`{jobID}-result`) when `sanitizeBasename` rejects the URL's path component entirely (empty, `.`, `..`, or a bare separator).
- Redaction (`redact`) is applied to every client error path, including transport-level errors, as defense-in-depth: the API key only ever appears in the `Authorization` header (never the URL), so a leak via `err.Error()` is not expected in practice, but the unit test enforces the invariant regardless of how future code changes might alter the error surface.

## Deviations from Plan

None - plan executed exactly as written. All three tasks matched their `<behavior>`/`<action>` specifications; no Rule 1-4 auto-fixes were needed.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. `go get` resolved `github.com/modelcontextprotocol/go-sdk@v1.6.1` directly from the module proxy with no additional credentials.

## Next Phase Readiness
- `internal/mcpserver.Client`/`Config` are ready for Plan 21-02 to build the 5 MCP tools (`convert_file`, `get_job_status`, `download_result`, `list_supported_formats`, `list_presets`) and `cmd/mcp-server`'s stdio entry point on top of.
- The MCP SDK dependency is pinned and isolated but not yet imported anywhere -- Plan 21-02 is the first consumer (`mcp.NewServer`/`mcp.AddTool`/`mcp.StdioTransport`), at which point `go.mod`'s `// indirect` marker on `go-sdk` will become a direct import.
- No blockers identified for Plan 21-02.

---
*Phase: 21-mcp-server*
*Completed: 2026-07-13*

## Self-Check: PASSED

All created files verified present on disk; all three task commits (`60e0d0c`, `1df1623`, `de5fa07`) verified present in git history.
