---
phase: 25-mcp-streamable-http
plan: 01
subsystem: mcp
tags: [mcp, streamable-http, go-sdk, api-key, session-binding, presigned]

# Dependency graph
requires:
  - phase: 21-mcp-server
    provides: transport-agnostic internal/mcpserver (Client, NewServer, five tools)
  - phase: 04-security (auth middleware lineage)
    provides: internal/auth ApiKey scheme + 401 semantics
provides:
  - "cmd/mcp-http: streamable-HTTP MCP binary (MCP_HTTP_ADDR default :8070, /healthz probe)"
  - "internal/mcpserver.ResultMode (local zero-value / remote presigned-only) + NewClientForKey per-request Client"
  - "auth.ParseAPIKey: single shared ApiKey scheme parser (REST middleware + mcp-http)"
  - "Session-key binding middleware (sessionID -> sha256(key), 403 on mismatch) applied unconditionally"
  - "D-02 verdict test: stateless_spike_test.go proves in-request NotifyProgress delivery"
affects: [25-02 (chart deployment of mcp-http), 25-03 (live HTTP gate)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-request server construction: getServer closure reads context key -> NewClientForKey -> NewServer"
    - "ResultMode zero-value backward compatibility (existing callers untouched)"
    - "Session-key binding map with response-writer capture (Unwrap-preserving) + DELETE prune + idle-TTL sweep"

key-files:
  created:
    - cmd/mcp-http/main.go
    - cmd/mcp-http/main_test.go
    - internal/mcpserver/http_test.go
    - internal/mcpserver/stateless_spike_test.go
  modified:
    - internal/mcpserver/config.go
    - internal/mcpserver/client.go
    - internal/mcpserver/tools.go
    - internal/mcpserver/mcpserver.go
    - internal/auth/auth.go
    - internal/auth/middleware.go
    - internal/auth/middleware_test.go

key-decisions:
  - "D-02 VERDICT: PASS — go-sdk v1.6.1 Stateless:true DOES deliver in-flight NotifyProgress to the HTTP client; cmd/mcp-http ships with StreamableHTTPOptions{Stateless: true}"
  - "Session-key binding middleware ships UNCONDITIONALLY (inert belt-and-suspenders in stateless; the active isolation control if options ever flip to stateful)"
  - "Lifecycle: DELETE prunes bindings AND a periodic idle-TTL sweep (30m TTL / 5m tick) runs anyway — in stateless mode the SDK still issues session ids on initialize, so the map would otherwise leak one entry per non-DELETEing client"
  - "ResultLocal is the ResultMode zero value ('') so NewServer(cfg, client) and every existing caller stay behaviorally identical"
  - "local_path gains omitempty (both outputs) so remote results genuinely omit it; expiry_note (omitempty) added, set only in remote mode"

patterns-established:
  - "Per-request credential pass-through: one parse site (middleware) -> context value -> getServer -> NewClientForKey"
  - "sha256(key) session binding: raw keys never stored in the binding map"

requirements-completed: [MCPH-01, MCPH-02]

# Metrics
duration: ~35min
completed: 2026-07-14
---

# Phase 25 Plan 01: Stateless spike + mcpserver refactor + cmd/mcp-http Summary

**Streamable-HTTP MCP endpoint with per-request caller-key pass-through, presigned-only remote results, and an unconditional session-key-binding hijack guard — after a live spike proved go-sdk v1.6.1 stateless mode delivers in-flight progress notifications.**

## D-02 Stateless Verdict (input for Plan 03's live gate)

**PASS — stateless delivers progress.** `stateless_spike_test.go` stands up
`mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true})`
behind `httptest`, connects a real `mcp.StreamableClientTransport` client, and a stub tool
emitting 3 `req.Session.NotifyProgress` calls from inside its handler (the exact
`progressTicker` pattern convert_file uses). **All 3 notifications reached the HTTP client.**

**Exact options shipped in cmd/mcp-http:** `mcp.StreamableHTTPOptions{Stateless: true}`
(everything else zero: no JSONResponse, no EventStore, no SessionTimeout, default
localhost/cross-origin protection). Plan 03's live gate should expect:
- initialize response still carries an `Mcp-Session-Id` header (SDK default `GetSessionID` fires in stateless mode)
- GET (standalone SSE) returns 405 with `Allow: POST` — the SDK's documented stateless behavior; the go-sdk client tolerates this
- progress notifications arrive on the POST response stream of the tool call itself

## Performance

- **Duration:** ~35 min (including one API-error interruption/resume)
- **Completed:** 2026-07-14
- **Tasks:** 3/3
- **Files modified:** 11 (4 created, 7 modified)

## Accomplishments

- **D-02 spike (Task 1):** definitive PASS verdict, recorded above.
- **internal/mcpserver refactor (Task 2, TDD):** `ResultMode` (zero value = local, so stdio
  path is byte-behaviorally unchanged), `NewClientForKey(base, apiKey)` for isolated
  per-request Clients, remote mode = presigned-only (`ConvertBlocking` skips `Download`;
  `download_result` returns the URL without fetching or writing; tool descriptions drop
  OUTPUT_DIR/local-path language; `NewClient` never calls `MkdirAll` in remote mode).
- **cmd/mcp-http (Task 3, TDD):** binary with `/healthz`, middleware chain
  (1) `auth.ParseAPIKey` → 401 JSON before any JSON-RPC, (2) session-key binding
  `map[sessionID]sha256(key)` → 403 on mismatch before ServeHTTP, then the streamable
  handler whose getServer builds a per-request remote-mode Client. `auth.ParseAPIKey`
  extracted and the REST `Middleware` refactored to call it (middleware_test.go green,
  behavior preserved). No `OCTOCONV_API_KEY` read anywhere in the binary (D-03/D-06).

## Task Commits

1. **Task 1: D-02 stateless spike** — `20172ca` (test)
2. **Task 2 RED: remote-mode + per-request-key tests** — `a336700` (test)
3. **Task 2 GREEN: ResultMode + NewClientForKey** — `fb9a4b7` (feat)
4. **Task 3 RED: mcp-http + ParseAPIKey tests** — `6a55f01` (test)
5. **Task 3 GREEN: cmd/mcp-http + auth.ParseAPIKey + session binding** — `2e6d3cb` (feat)

## TDD Gate Compliance

Both tdd tasks followed RED → GREEN with separate commits (`a336700`→`fb9a4b7`,
`6a55f01`→`2e6d3cb`). No REFACTOR commits — implementations landed clean. RED commits
fail via undefined new API symbols (compile failure), the standard failure mode for
new-API TDD.

## Files Created/Modified

- `cmd/mcp-http/main.go` — the binary: config load (OCTOCONV_BASE_URL only), middleware chain, getServer, sweeper, graceful shutdown
- `cmd/mcp-http/main_test.go` — httptest stack tests: healthz, 401s, five tools, two-key isolation, exact hijack (K2 + K1's session → 403 + upstream untouched), DELETE cleanup, sweep
- `internal/mcpserver/config.go` — ResultMode type/constants + Config.ResultMode
- `internal/mcpserver/client.go` — Client.resultMode, remote MkdirAll skip, NewClientForKey, remote ConvertBlocking short-circuit
- `internal/mcpserver/tools.go` — local_path omitempty + expiry_note fields; remote branches in convert_file/download_result handlers
- `internal/mcpserver/mcpserver.go` — ResultMode-branched descriptions for convert_file/download_result
- `internal/mcpserver/http_test.go` — remote-mode result-shape + NewClientForKey isolation tests
- `internal/mcpserver/stateless_spike_test.go` — D-02 verdict test
- `internal/auth/auth.go` — exported ParseAPIKey
- `internal/auth/middleware.go` — Middleware refactored onto ParseAPIKey
- `internal/auth/middleware_test.go` — TestParseAPIKey table

## Decisions Made

- **Sweep runs even in the stateless branch** (plan allowed skipping it): in stateless
  mode the SDK's default `GetSessionID` still issues session ids on initialize, clients
  echo them back, and the binding map binds them — so entries WOULD accumulate for
  clients that never DELETE. A 30-minute idle-TTL sweep (5-minute tick) bounds the map
  in both branches; the constant is documented to stay above any future SDK
  SessionTimeout if the stateful branch is ever activated.
- **Incoming first-sight session ids are also bound** (not only response-captured ones):
  after a process restart, a surviving client's session id is re-bound to its first
  presenter, restoring the guarantee without a 404 round-trip. In stateless mode the SDK
  doesn't validate ids anyway; in a stateful future the SDK would 404 unknown ids before
  any tool runs.
- **`local_path` became `omitempty`** so the remote result genuinely omits the field
  (the must_have says "omits local_path", not "empty string"); local mode always sets it
  non-empty, so serialization there is unchanged.
- **Middleware tests live in `cmd/mcp-http/main_test.go`** (package main), not
  `internal/mcpserver/http_test.go` as the task's file list nominally grouped them —
  the middleware and handler stack are package-main symbols; `go test ./cmd/mcp-http/...`
  (the plan's own verify command) covers them.

## Deviations from Plan

**1. [Rule 2 - Missing critical functionality] Sweep added despite the stateless-PASS branch permitting skip**
- **Found during:** Task 3 (session-key binding lifecycle analysis)
- **Issue:** The plan's "map is inert in stateless mode" assumption is only half-true: the SDK still issues/echoes session ids in stateless mode, so the binding map accumulates one entry per non-DELETEing client — an unbounded-growth (memory) defect over a long-lived pod.
- **Fix:** Timestamped bindings + `sweep(now)` on a 5m ticker dropping entries idle > 30m; unit-tested.
- **Files modified:** cmd/mcp-http/main.go, cmd/mcp-http/main_test.go
- **Verification:** TestSessionBinding_SweepDropsIdleEntries
- **Committed in:** 2e6d3cb (part of task commit)

**Total deviations:** 1 auto-fixed (Rule 2)
**Impact on plan:** Strictly additive hardening within the plan's own LIFECYCLE note; no scope creep.

## Issues Encountered

- Executor was interrupted by an API error immediately before Task 1's commit; resumed
  cleanly (spike file was intact and untracked; committed on resume, no duplicate work).

## Verification Gates

- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean
- `go test ./...` — green (live gates `TestLiveStdioJSONRPCGate` / e2e self-skip offline)
- stdio regression: `NewServer(cfg, client)` signature unchanged; tools_test.go,
  client_test.go, mcp_live_test.go compile and pass untouched; `go build ./cmd/mcp-server` clean

## Known Stubs

None — no placeholder values or unwired data paths were introduced.

## Next Phase Readiness

- Plan 02 (chart) can add `Dockerfile.mcp-http` + deployment/service: binary needs only
  `OCTOCONV_BASE_URL` (+ optional `MCP_HTTP_ADDR`, timeouts); `/healthz` for probes; no
  volume/filesystem requirements (remote mode never writes).
- Plan 03 (live gate) expectations documented in the D-02 verdict section above.

---
*Phase: 25-mcp-streamable-http*
*Completed: 2026-07-14*

## Self-Check: PASSED

All created files exist; all five task commits (20172ca, a336700, fb9a4b7, 6a55f01, 2e6d3cb) verified in git log.
