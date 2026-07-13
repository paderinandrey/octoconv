---
phase: 21-mcp-server
plan: 03
subsystem: mcp
tags: [mcp, stdio, go-sdk, modelcontextprotocol, jsonrpc, cmd-binary, e2e]

# Dependency graph
requires:
  - phase: 21-mcp-server (21-01, 21-02)
    provides: internal/mcpserver (Config, Client, NewServer + five tool handlers)
provides:
  - cmd/mcp-server thin stdio entrypoint (fail-fast config, stderr-only logging)
  - README "MCP server" section with claude-code/claude_desktop config JSON example
  - internal/mcpserver/mcp_live_test.go: env-gated live stdio JSON-RPC hard gate (D-13)
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Thin cmd/* main: log.SetOutput(os.Stderr) before any SDK init, Config.Load fail-fast, mcp.StdioTransport to completion"
    - "Hand-rolled raw-pipe JSON-RPC test harness for live stdio gates (no mocked transport, no SDK client dependency in the test itself)"

key-files:
  created:
    - cmd/mcp-server/main.go
    - internal/mcpserver/mcp_live_test.go
  modified:
    - README.md

key-decisions:
  - "Hand-rolled a minimal JSON-RPC-over-stdio test harness (rpcHarness) instead of driving functional assertions through the go-sdk's own mcp.Client, so stdout-purity validation (D-10) is a side effect of normal protocol traffic on the SAME raw pipe used for functional assertions -- one self-contained gate, no separate raw-pipe run needed"
  - "The harness transparently answers the server's KeepAlive 'ping' keepalive requests so a slow live run can't trip the SDK's own ping-timeout session teardown"
  - "Bad-input case uses target_format+preset together (violates the API's XOR contract) rather than an unsupported format pair, exercising the D-04 XOR-validation isError path specifically"

requirements-completed: [MCP-01, MCP-02, MCP-03, MCP-04, MCP-05]

# Metrics
duration: ~55min
completed: 2026-07-13
---

# Phase 21 Plan 03: MCP Server Binary + Live Stdio Gate Summary

**cmd/mcp-server ships as a thin, fail-fast, stderr-only-logging stdio binary; README documents client wiring; a hand-rolled JSON-RPC-over-stdio test harness drives the real binary for D-13 -- but the live run itself could not be executed in this session because the local Docker/OrbStack daemon was unresponsive.**

## Performance

- **Duration:** ~55 min
- **Completed:** 2026-07-13
- **Tasks:** 2/2 code-complete; live human-check portion of Task 2 blocked by environment (see below)
- **Files modified:** 3 (1 created binary entrypoint, 1 created test file, 1 modified README)

## Accomplishments

- `cmd/mcp-server/main.go`: a thin main that sets `log.SetOutput(os.Stderr)` as its first statement (before `Config.Load` or any SDK init), fails fast via `log.Fatalf` (non-zero exit, stderr-only, empty stdout) when required env is missing, and runs `mcp.StdioTransport{}` to completion with SIGINT/SIGTERM-driven shutdown.
- README gained an "MCP server" section: a tool table, the build command, all env vars (including the operator-only `OCTOCONV_S3_DIAL_ADDR`), and a `claude-code`/`claude_desktop` MCP config JSON example naming the binary.
- `internal/mcpserver/mcp_live_test.go`: an env-gated (`E2E_BASE_URL`) live stdio JSON-RPC hard gate that builds the real `cmd/mcp-server` binary, execs it with the child's own env carrying `OCTOCONV_S3_DIAL_ADDR`, and drives a hand-rolled raw JSON-RPC session (`rpcHarness`) over its stdin/stdout: `initialize` handshake, `notifications/initialized`, `tools/list` (asserts the exact five-tool set), a real `png`→`jpg` `convert_file` call (asserts a non-empty `presigned_url` and a `local_path` that exists on disk with JPEG magic bytes), `list_supported_formats`/`list_presets` round-trips, and a bad-input (`target_format`+`preset` together) `isError:true` case. Every stdout line the harness reads is validated as JSON carrying `"jsonrpc":"2.0"` as a side effect of normal traffic, satisfying D-10's stdout-purity requirement without a separate raw-pipe run.
- Offline verification is green: `gofmt -l`, `go vet ./...`, `go build ./...`, and `go test ./internal/mcpserver/... -count=1` all pass, with `TestLiveStdioJSONRPCGate` self-skipping (`E2E_BASE_URL not set; skipping live MCP stdio gate`) exactly as required.

## Task Commits

Each task was committed atomically:

1. **Task 1: cmd/mcp-server thin main + README section** - `0aeef3d` (feat)
2. **Task 2: LIVE stdio JSON-RPC hard gate (test code)** - `b9cdda3` (test)

**Plan metadata:** (this commit, SUMMARY.md via `git add -f`)

## Files Created/Modified

- `cmd/mcp-server/main.go` - thin stdio MCP entrypoint: stderr-only logging before SDK init, fail-fast Config.Load, runs mcp.StdioTransport
- `internal/mcpserver/mcp_live_test.go` - env-gated live JSON-RPC-over-stdio hard gate against the real binary + compose stack
- `README.md` - new "MCP server" section (tool table, env vars, build command, client config JSON example)

## Decisions Made

- Chose the "single raw pipe" test-driving strategy the plan called out as preferred: a hand-rolled `rpcHarness` (not the go-sdk's own `mcp.Client`/`CommandTransport`) owns the child's stdin/stdout directly, so the SAME session that drives functional assertions also naturally observes and validates every stdout line for D-10 purity -- no separate raw-pipe run required.
- The harness auto-replies to unsolicited server `"ping"` keepalive requests (the SDK's 30s `KeepAlive` option) so a slower live run environment can't trip the server's own ping-failure session teardown mid-gate.
- The bad-input assertion exercises `target_format`+`preset` supplied together (violating the API's documented XOR contract, D-04) rather than an unsupported format pair, so it specifically proves the XOR-rejection path surfaces as `isError:true`, not a protocol error (D-11).

## Deviations from Plan

None - plan executed exactly as written for the code deliverables (Task 1 and Task 2's test file). The live *execution* of the gate (the plan's `<human-check>` verification step) could not be completed in this session; see "Issues Encountered" below. This is an environment-availability blocker, not a code defect, and no deviation-rule fix applies (it isn't a bug, missing functionality, or an architectural question -- it's infrastructure being unreachable).

## Issues Encountered

**Docker/OrbStack daemon unreachable in this execution environment; the live human-check run could not be performed.**

- `docker info`, `docker compose -p octoconv ps`, and a raw `curl --unix-socket .../docker.sock http://localhost/version` all hung indefinitely (tested at 10s, 60s, 90s, and 100s bounds) with no response from the daemon, across multiple independent attempts and after re-launching the OrbStack app.
- A full `docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml up -d --build` was attempted under a 280s bound (well above the plan's 120s hang-rule threshold) and was killed by `timeout` having produced zero output the entire run (exit 124) -- confirming the daemon never became reachable, not merely a slow build.
- Per the plan's own "hang rule: >120s -> kill, retry once, loud stop" discipline, this was retried (multiple `docker info` probes plus one full `compose up` attempt) and then stopped rather than retried indefinitely.
- No containers were created and no teardown was necessary (the connection to the daemon was never established at any point, so nothing was left running as a result of this session's attempts).
- **What IS verified:** the binary builds/vets/runs correctly, fails fast on missing env, and the live-gate test code compiles, vets cleanly, and correctly self-skips offline. **What is NOT verified in this session:** the actual live pass against a running compose stack (five tools over the wire, a real png->jpg conversion with a working `OCTOCONV_S3_DIAL_ADDR` dial-redirect, list round-trips, bad-input isError, and stdout purity under real traffic) -- this remains D-13's stated acceptance gate and should be re-run by an operator (or a follow-up session) once a working Docker daemon is available:

  ```bash
  docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml up -d --build
  E2E_BASE_URL=http://localhost:8090 \
  DATABASE_URL=postgres://octo:octo-pass@localhost:5434/octo_db \
  API_KEY_SALT=dev-only-change-me-in-real-deploys \
  E2E_S3_DIAL_ADDR=127.0.0.1:9100 \
  go test ./internal/mcpserver/... -run Live -count=1 -v -timeout 15m
  docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml stop
  ```

## Known Stubs

None.

## Threat Flags

None -- the binary and test file implement exactly the trust boundaries (stdout JSON-RPC purity, fail-fast config) called out in the plan's threat model; no new network endpoints, auth paths, or schema changes were introduced.

## User Setup Required

None for the code shipped in this plan. **Follow-up required:** re-run the live gate (see command block above) once a working Docker daemon is available, to close out D-13's acceptance criterion for this plan.

## Next Phase Readiness

- `cmd/mcp-server` is a complete, buildable, fail-fast stdio MCP binary; README documents it for agent/client wiring.
- The live JSON-RPC hard gate exists, compiles, vets cleanly, and self-skips offline -- it is ready to run the moment a Docker daemon is reachable; no further code changes are anticipated to make it pass.
- **Blocker for full phase closure:** the live pass itself (D-13's actual acceptance evidence) is outstanding pending a working Docker environment -- this should be the first thing re-attempted before considering Phase 21 fully verified end-to-end.

---
*Phase: 21-mcp-server*
*Completed: 2026-07-13*

## Self-Check: PASSED

- FOUND: cmd/mcp-server/main.go
- FOUND: internal/mcpserver/mcp_live_test.go
- FOUND: README "MCP server" section
- FOUND: commit 0aeef3d (Task 1)
- FOUND: commit b9cdda3 (Task 2)

---

**RESOLVED (orchestrator follow-up, 2026-07-13):** the Docker daemon (OrbStack) was restarted
and the outstanding D-13 live gate executed from main (HEAD after merge):
`TestLiveStdioJSONRPCGate` → `--- PASS (2.80s)` against the freshly rebuilt compose stack
(api image rebuilt; fresh client key minted via manage-clients). Full chain proven live:
initialize handshake → tools/list (5) → convert_file png→jpg (presigned_url + local file with
JPEG magic, via OCTOCONV_S3_DIAL_ADDR=127.0.0.1:9100 on the child env) → list tools round-trip
→ isError on bad input → every stdout line valid JSON-RPC. No code changes were needed.
