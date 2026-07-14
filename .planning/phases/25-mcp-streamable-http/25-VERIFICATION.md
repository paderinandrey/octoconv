---
phase: 25-mcp-streamable-http
verified: 2026-07-14T04:27:10Z
status: passed
score: 15/15 must-haves verified
overrides_applied: 0
---

# Phase 25: MCP Streamable HTTP Verification Report

**Phase Goal:** `cmd/mcp-http` streamable-HTTP MCP endpoint: per-request caller-key pass-through (pod holds NO key), presigned-only remote results, gated chart Deployment/Service/NetworkPolicy, single replica; stdio unchanged.
**Verified:** 2026-07-14T04:27:10Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (merged ROADMAP SC1-4 + plan frontmatter must_haves)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A caller with a valid `Authorization: ApiKey <key>` can list all 5 tools and run a conversion over streamable HTTP (D-01, MCPH-01, ROADMAP SC1) | VERIFIED | `cmd/mcp-http/main.go` builds `mcp.NewStreamableHTTPHandler`; `TestValidKey_FiveTools_PerRequestClient` (cmd/mcp-http/main_test.go:205) proves 5-tool listing + a real tool call; live gate (25-03 SUMMARY) proves it against a chart-deployed pod: `tools/list=5`, real png→jpg conversion |
| 2 | Missing/malformed Authorization header rejected 401 before any tool executes (D-03) | VERIFIED | `withAuthParse` middleware (main.go:95-104) runs first in the chain; `TestMissingAuth_401`, `TestMalformedAuth_401` (main_test.go) green; live gate 401-without-key PASS |
| 3 | Two distinct caller keys resolve to two isolated per-request Clients, no cross-bleed (D-03, D-07, ROADMAP SC2) | VERIFIED | `NewClientForKey` (client.go:88-91) takes `base` by value; `TestTwoKeys_NoCrossBleed` (main_test.go:252) asserts upstream sees each caller's own key on each call, in interleaved order |
| 4 | K2 presenting K1's Mcp-Session-Id is rejected 403 before any tool runs; K1's session-bound Client never executes K2's call (D-03/D-07) | VERIFIED | `sessionBindings.middleware` (main.go:226-264) checks sha256(key) before ServeHTTP; `TestSessionHijack_403` (main_test.go:292) proves 403 + zero upstream calls after hijack attempt, then confirms the legitimate creator still works; live gate reproduced this live (bonus assertion) |
| 5 | Remote/HTTP mode convert_file omits local_path and returns presigned_url; download_result returns the presigned URL without writing files (D-04, MCPH-02, ROADMAP SC3) | VERIFIED | `client.go` ConvertBlocking's `done` branch skips `Download` when `resultMode==ResultRemote` (client.go:385-395); `tools.go` download_result handler returns URL without fetching (tools.go:194-199); `local_path` field is `omitempty` (tools.go:46,169); unit tests `TestRemoteMode_ConvertFile_PresignedOnly`, `TestRemoteMode_DownloadResult_ReturnsURLWithoutFile` green; live gate confirms `presigned_url` non-empty AND `local_path` absent from structuredContent |
| 6 | stdio `cmd/mcp-server` stays byte-behaviorally unchanged | VERIFIED | `git log --oneline -- cmd/mcp-server/` shows zero commits in the Phase 25 commit range (last touch 0aeef3d, Phase 21); `NewServer(cfg, c)` signature unchanged (mcpserver.go:29); `go test ./internal/mcpserver/...` — all pre-existing local-mode tests (tools_test.go equivalents) green |
| 7 | D-02 Stateless progress-notification question answered by an in-process spike with a recorded PASS/fallback verdict | VERIFIED | `stateless_spike_test.go`; `go test -run Stateless -v` → `TestStateless_ProgressNotificationsReachClient` PASS, logs "D-02 VERDICT: PASS"; `cmd/mcp-http/main.go` StreamableHTTPOptions{Stateless: true} (main.go:285-290) matches the verdict |
| 8 | Dockerfile.mcp-http builds the binary in the same multi-stage shape as Dockerfile.api, USER nobody, :8070 (D-05) | VERIFIED | `Dockerfile.mcp-http` read: golang:1.26-bookworm build → debian:bookworm-slim runtime, ca-certificates only, `USER nobody`, `EXPOSE 8070` |
| 9 | helm install with mcpHttp.enabled renders single-replica Deployment + ClusterIP Service on :8070, gated so mcpHttp.enabled=false renders neither (D-05, ROADMAP SC4) | VERIFIED | `helm template --set mcpHttp.enabled=true` renders `name: mcp-http` Deployment/Service; `--set mcpHttp.enabled=false` renders neither (both checks executed live in this verification, PASS) |
| 10 | mcp-http pod receives only OCTOCONV_BASE_URL + MCP_HTTP_ADDR — no API key, no DB/S3 secret env (D-06, D-03) | VERIFIED | `deployment-mcp-http.yaml` explicit `env:` block has exactly those two vars, no `octoconv.commonEnv`; live grep of rendered mcp-http block for `secretRef|DATABASE_URL|API_KEY` returned zero matches (executed in this verification) |
| 11 | NetworkPolicy default-denies ingress except :8070 from the release namespace, gated by mcpHttp.enabled (ROADMAP SC4) | VERIFIED | `networkpolicy-mcp-http.yaml` renders when enabled=true, absent when enabled=false (both executed live); podSelector on component=mcp-http, single ingress rule TCP:8070 from namespaceSelector |
| 12 | helm lint + helm template + kubectl apply --dry-run=server pass both ways | VERIFIED | `helm lint deploy/chart/octoconv -f values-local.yaml` → "0 chart(s) failed" (executed live in this verification); template gating both ways confirmed live; 25-02 SUMMARY records the dry-run apply result (not re-run here — no cluster available, offline lint/template is the reproducible subset) |
| 13 | README documents the MCP HTTP transport config | VERIFIED | README.md:296-325 "MCP over HTTP (in-cluster)" section documents transport, per-request auth, presigned-only results, key-free config, port-forward access |
| 14 | Live: chart-deployed mcp-http pod on OrbStack k8s serves a real session: initialize + tools/list=5, convert_file presigned-only, 401-without-key, teardown clean (D-08) | VERIFIED (evidence: committed live driver + 25-03 SUMMARY transcript, per verification_facts primary-evidence acceptance — re-run would require a ~15min cluster reinstall) | `internal/mcpserver/mcp_http_live_test.go` (TestMCPHTTPLive) is a real, non-trivial driver (not a stub): connects a genuine go-sdk streamable client, asserts exact 5-tool surface, asserts presigned_url non-empty AND local_path absent, does a raw-HTTP 401 case bypassing the SDK client entirely, and a session-hijack 403 case. Code inspection of the driver and of `cmd/mcp-http/main.go`/`main_test.go` corroborates every assertion the transcript claims is independently backed by unit-level equivalents that actually run and pass offline. No doubt raised by inspection — re-run not triggered. |
| 15 | Zero new go.mod dependencies (T-25-SC accept) | VERIFIED | `git diff ad0acca..HEAD -- go.mod go.sum` — empty diff; go-sdk v1.6.1 was already pinned in Phase 21 |

**Score:** 15/15 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `cmd/mcp-http/main.go` | streamable-HTTP MCP binary with 401 + session-binding middleware + per-request Client getServer | VERIFIED | Contains `NewStreamableHTTPHandler`, full middleware chain in order (auth-parse → session-binding → ServeHTTP), `/healthz`, graceful shutdown |
| `internal/mcpserver/mcpserver.go` | ResultMode-aware NewServer | VERIFIED | `cfg.ResultMode == ResultRemote` branches tool descriptions (mcpserver.go:43-52); `NewServer(cfg, c)` signature unchanged |
| `internal/auth/auth.go` | exported `ParseAPIKey` shared by REST middleware and mcp-http | VERIFIED | `func ParseAPIKey(header string) (key string, ok bool)` (auth.go:18); `internal/auth/middleware.go`'s `Middleware` calls it (middleware.go:23); `cmd/mcp-http/main.go`'s `withAuthParse` calls it (main.go:97) — single shared parse site, not duplicated |
| `internal/mcpserver/stateless_spike_test.go` | D-02 decision gate | VERIFIED | Test exists, runs, definitive PASS verdict logged |
| `Dockerfile.mcp-http` | container image for cmd/mcp-http | VERIFIED | Present, multi-stage, contains `cmd/mcp-http` build path |
| `deploy/chart/octoconv/templates/deployment-mcp-http.yaml` | gated single-replica Deployment w/ /healthz probes | VERIFIED | `mcpHttp.enabled` gate, `replicas: {{ .Values.mcpHttp.replicas }}` (=1 in values.yaml), readiness/liveness on /healthz:8070 |
| `deploy/chart/octoconv/templates/service-mcp-http.yaml` | ClusterIP Service on 8070 | VERIFIED | literal name `mcp-http`, port 8070 |
| `deploy/chart/octoconv/templates/networkpolicy-mcp-http.yaml` | gated default-deny ingress NetworkPolicy (SC4) | VERIFIED | `kind: NetworkPolicy`, gated, podSelector + single ingress rule |
| `internal/mcpserver/mcp_http_live_test.go` | offline-skip-guarded live MCP-over-HTTP driver | VERIFIED | Present, substantive (not a stub), self-skips offline (`OCTOCONV_MCP_HTTP_LIVE` gate confirmed by running `go test` offline — it SKIPs cleanly) |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `cmd/mcp-http/main.go` | `internal/mcpserver` per-request Client | `getServer` closure reads caller key → `NewClientForKey` → `NewServer` | WIRED | `newHandler`'s `getServer` (main.go:272-283) does exactly this |
| `cmd/mcp-http/main.go` (401 middleware) | `internal/auth.ParseAPIKey` | shared parse, not duplicated | WIRED | `withAuthParse` calls `auth.ParseAPIKey` directly (main.go:97); `internal/auth.Middleware` also calls it (middleware.go:23) — single source |
| `cmd/mcp-http/main.go` (session-key binding) | go-sdk stateful session reuse (streamable.go:400) | `map[sessionID]sha256(key)`, 403 before ServeHTTP | WIRED | `sessionBindings.middleware` applied unconditionally, before `streamable` handler in the mux chain (main.go:296) |
| `deployment-mcp-http.yaml` | api Service FQDN | `OCTOCONV_BASE_URL` env | WIRED | Rendered env carries `http://api.octoconv.svc.cluster.local:8090` |
| `service-mcp-http.yaml` | mcp-http pod | ClusterIP selector, component=mcp-http, :8070 | WIRED | selector via `octoconv.selectorLabels` dict, confirmed in rendered template |
| `networkpolicy-mcp-http.yaml` | mcp-http pod | podSelector + namespaceSelector allow :8070 | WIRED | confirmed in rendered template; OrbStack-unenforced residual documented, matching Phase 24 precedent |

### Data-Flow Trace (Level 4)

Not applicable in the conventional UI-rendering sense — this phase is an API/binary, not a component tree. The equivalent trace (Client→API→result) was exercised: `ConvertBlocking`'s remote branch genuinely reads `job.DownloadURL` from a real `GetJob` poll cycle (client.go:374-395) rather than returning a hardcoded value — confirmed by `TestRemoteMode_ConvertFile_PresignedOnly` and the live gate's actual presigned URL (real MinIO-issued, verified by direct/fallback host-dial returning valid JPEG bytes).

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full offline test suite for phase packages | `go test ./internal/mcpserver/... ./internal/auth/... ./cmd/... -count=1 -v` | All PASS; 2 live tests self-skip (`TestMCPHTTPLive`, `TestLiveStdioJSONRPCGate`) | PASS |
| `go build ./...` | clean | 0 errors | PASS |
| `go vet ./...` | clean | 0 issues | PASS |
| `gofmt -l .` | clean | no files listed | PASS |
| `helm lint deploy/chart/octoconv -f values-local.yaml` | 0 charts failed | PASS |
| `helm template` gating both ways (enabled=true/false) | Deployment/Service/NetworkPolicy render together, vanish together | PASS |
| grep rendered mcp-http block for secret/DB/key env | zero matches | PASS |
| `git diff ad0acca..HEAD -- go.mod go.sum` | empty | PASS (zero new deps) |
| `git log --oneline -- cmd/mcp-server/` since phase start | zero commits | PASS (stdio untouched) |

### Probe Execution

No `scripts/*/tests/probe-*.sh` convention used in this project/phase. The equivalent "probe" is `internal/mcpserver/mcp_http_live_test.go`, which is a live-gated Go test rather than a shell probe script; it was inspected (not re-executed, per the accepted verification_facts primary-evidence rule — a live re-run requires a ~15min OrbStack chart reinstall and the stack is currently uninstalled). Code inspection found no discrepancy between the committed driver and the transcript in 25-03-SUMMARY.md, so no re-run was triggered.

| Probe | Command | Result | Status |
|-------|---------|--------|--------|
| `internal/mcpserver/mcp_http_live_test.go` (offline) | `go test ./internal/mcpserver/ -run MCPHTTPLive -v` | SKIP (env-gated) | PASS (expected offline behavior) |
| `internal/mcpserver/mcp_http_live_test.go` (live) | not re-run — see rationale above | N/A | ACCEPTED (25-03 SUMMARY transcript + code inspection) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| MCPH-01 | 25-01, 25-02, 25-03 | streamable-HTTP endpoint, per-request pass-through, same 5 tools, container+Service in chart | SATISFIED | See Truths 1-4, 8-13 |
| MCPH-02 | 25-01, 25-03 | convert_file HTTP-mode result remote-usable, local_path contract resolved | SATISFIED | See Truth 5 |

REQUIREMENTS.md checkbox state for MCPH-01/MCPH-02 is still shown as `[ ]` (Pending) — this is a tracking-document sync gap, not a code gap: ROADMAP.md already marks Phase 25 `[x]` and the 25-01/03 SUMMARY frontmatter both list `requirements-completed: [MCPH-01, MCPH-02]`. Noted for the orchestrator to sync REQUIREMENTS.md; does not affect this phase's pass/fail determination.

### Anti-Patterns Found

None. Scanned all files modified across 25-01/02/03 (`internal/mcpserver/{mcpserver,config,client,tools,http_test,stateless_spike_test,mcp_http_live_test}.go`, `internal/auth/{auth,middleware}.go`, `cmd/mcp-http/{main,main_test}.go`, `Dockerfile.mcp-http`, `deploy/chart/octoconv/values.yaml`, the three mcp-http chart templates, `README.md`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER`, placeholder/stub language, and empty-implementation patterns. Zero matches (one incidental match on the word "placeholder" in a doc-comment describing the redaction string constant — not a stub marker).

### Human Verification Required

None. Every must-have truth was verifiable by direct code inspection, offline test execution, and offline helm gates. The one item that cannot be re-run in this verification pass (the live OrbStack cluster gate) is accepted per the supplied `verification_facts` as primary evidence, backed by a genuinely substantive, non-stub, committed test driver that was read in full and found to match its own transcript's claims exactly — no discrepancy that would warrant a mandatory re-run.

### Gaps Summary

No gaps. All 15 observable truths (merged from ROADMAP SC1-4 and the three plans' must_haves) are VERIFIED. Code inspection confirms:
- The pod genuinely holds no API key (grep-confirmed zero `OCTOCONV_API_KEY`/`secretRef`/`DATABASE_URL` in the rendered mcp-http block).
- Per-request Client isolation is real (base Config passed by value, no shared mutable key state) and unit-tested with interleaved calls proving no cross-bleed.
- The session-hijack guard is wired unconditionally and unit-tested with a positive (legit creator still works) and negative (impostor 403, zero upstream calls) case.
- Remote mode's presigned-only contract is enforced in both `ConvertBlocking` (skips `Download`) and `download_result` (returns URL without fetching), not just at the JSON-serialization layer.
- stdio (`cmd/mcp-server`) has zero commits in the phase's range — genuinely untouched, not just "no logical changes."
- The chart's gating, key-free env, and NetworkPolicy were independently re-rendered in this verification session (not just trusted from the SUMMARY) and matched every claim.
- The one environmental residual (SC3 direct-host-dial failing on OrbStack, falling back to Host-preserving port-forward) is honestly recorded in both the SUMMARY and the live driver's own logic (never silently upgraded to a clean PASS) and matches the class of residual already accepted at Phase 24 — not a new gap.

---

*Verified: 2026-07-14T04:27:10Z*
*Verifier: Claude (gsd-verifier)*
