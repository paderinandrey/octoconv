# Phase 25: MCP Streamable HTTP - Context

**Gathered:** 2026-07-14
**Status:** Ready for planning
**Source:** v1.6 research (STACK/FEATURES/ARCHITECTURE/PITFALLS + synthesis conflict resolution), user-approved roadmap

<domain>
## Phase Boundary

`cmd/mcp-http` — streamable-HTTP MCP endpoint serving the SAME five tools from transport-agnostic `internal/mcpserver`, deployed as a container+Service in the Phase 24 chart. Per-request caller-key pass-through (the pod stores NO API key). The `local_path` contract resolved for remote callers. stdio `cmd/mcp-server` stays untouched.

</domain>

<decisions>
## Implementation Decisions

### Binary & transport
- D-01: New `cmd/mcp-http` (one binary per deployment unit — project convention); uses go-sdk v1.6.1 `mcp.NewStreamableHTTPHandler` (symbol verified present in the pinned module); listens on MCP_HTTP_ADDR (default :8070); stdio binary unchanged
- D-02: `Stateless: true` recommended by research but flagged LOW confidence re: progress notifications — VERIFY LIVE early (smoke test: does convert_file's NotifyProgress reach an HTTP client in stateless mode? if not, use stateful sessions and document); this is a plan-1 spike gate, cheap
- D-03: Per-request caller-key pass-through (synthesis Key Decision): the HTTP handler extracts the caller's `Authorization: ApiKey <key>` header from the incoming MCP HTTP request and constructs a per-request/per-session Client with THAT key; the pod holds no key of its own (zero-privilege preserved; per-client rate limits and preset scoping intact). Missing/invalid header → 401 before any tool executes. internal/mcpserver may need Client construction refactored from process-startup to per-request — keep tools transport-agnostic

### local_path contract (MCPH-02 Key Decision — resolve at planning)
- D-04: RESOLVED: HTTP mode = presigned-only. convert_file/download_result results in HTTP mode omit local_path and return presigned_url (+ expiry note); the tool DESCRIPTIONS in HTTP mode reflect this so agents don't expect a path. Mechanism: a mode flag on the tool layer (e.g. ResultMode in the server construction — stdio passes "local", http passes "remote"). download_result in remote mode returns the presigned URL rather than writing files. Options omit/download-proxy considered: proxying bytes over MCP re-inflates context (violates v1.5 D-07 rationale); omit-only chosen as minimal and honest

### Chart & deployment
- D-05: Dockerfile.mcp-http (same multi-stage shape as api); chart gains deployment-mcp-http.yaml + service (ClusterIP :8070), gated `mcpHttp.enabled` (default true in values-local); probes: HTTP GET on a cheap endpoint (the MCP handler's own health semantics — or a /healthz sidecar-handler in the same binary); single replica; octoconv.io/tier: app label (metrics NP if it exposes /metrics — optional)
- D-06: The pod needs OCTOCONV_BASE_URL=http://api.octoconv.svc.cluster.local:8090 only (no key env — D-03)

### Verification
- D-07: Unit: httptest-level tests for header extraction/401/per-request client isolation (two different keys → two different clients, no cross-bleed); remote-mode result shape tests (no local_path field)
- D-08: LIVE HARD GATE: chart-deployed mcp-http pod on OrbStack k8s; a scripted MCP-over-HTTP session from the host (curl/Go test speaking streamable HTTP): initialize → tools/list=5 → convert_file with a real caller key (client minted via manage-clients against the in-cluster DB) → presigned-only result → 401-without-key case → list tools. Reuse the Phase 24 install flow (compose stopped, sequential builds, hang rules). Direct host-dial of the presigned URL doubles as the SC3 re-check deferred from Phase 24
- D-09: OrbStack discipline unchanged (D-11/D-12 from Phase 24); teardown uninstall after gate

### Claude's Discretion
- Session management details (per-request vs per-session client caching keyed by key hash)
- Exact health endpoint shape; whether metrics are exposed (nice-to-have)
- Go test vs bash for the live HTTP session driver

</decisions>

<canonical_refs>
## Canonical References

- `internal/mcpserver/mcpserver.go` + `tools.go` + `client.go` + `config.go` (transport-agnostic layer; Client construction seam for per-request refactor)
- `cmd/mcp-server/main.go` (stdio wiring — unchanged; pattern reference)
- `deploy/chart/octoconv/` (Phase 24 chart — the deployment target; values/env/helpers conventions)
- `internal/auth/middleware.go` (ApiKey header scheme to mirror for 401 semantics)
- `.planning/research/STACK.md` (NewStreamableHTTPHandler verified; RequireBearerToken ABSENT — stdlib middleware), `FEATURES.md` (Stateless LOW-confidence flag), `PITFALLS.md` (session state across restarts, key-per-request), `SUMMARY.md` (Key Decision rationale + local_path options)
- `.planning/phases/24-helm-chart-core/24-03-SUMMARY.md` (install flow evidence + SC3 recheck note)

</canonical_refs>

<specifics>
## Specific Ideas

- The 401 middleware wraps the MCP handler at net/http level (before JSON-RPC) — matches "no separate auth provider" constraint
- README MCP section gains the HTTP variant config example
- The live gate's manage-clients run needs DATABASE_URL to the in-cluster postgres — port-forward or kubectl exec psql; pick the simplest

</specifics>

<deferred>
## Deferred Ideas

- MCPV2-02 (resources), multi-replica mcp-http + session affinity, mTLS
</deferred>

---

*Phase: 25-mcp-streamable-http*
*Context gathered: 2026-07-14*
