---
phase: 25-mcp-streamable-http
plan: 03
subsystem: testing
tags: [mcp, streamable-http, kubernetes, orbstack, live-gate, helm]

# Dependency graph
requires:
  - phase: 25-mcp-streamable-http
    provides: "cmd/mcp-http binary + per-request caller-key pass-through + presigned-only remote results (25-01)"
  - phase: 25-mcp-streamable-http
    provides: "Dockerfile.mcp-http + chart Deployment/Service/NetworkPolicy (25-02)"
provides:
  - "Live D-08 hard-gate evidence: chart-deployed mcp-http pod on OrbStack k8s proven end-to-end over real streamable HTTP"
  - "internal/mcpserver/mcp_http_live_test.go — offline-skip-guarded live driver, reusable for future live re-checks"
  - "Phase 24 SC3 recheck closed (direct dial re-attempted, fallback path used and recorded honestly)"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "sc3Dial: direct-then-fallback presigned-URL host-dial helper (attempts direct FQDN dial first, falls back to a DialContext override pointed at a port-forwarded local address while preserving the Host header, mirroring curl --connect-to) — reusable for any future OrbStack live gate needing an honest direct-vs-fallback verdict"
    - "kubectl cp of a tiny fixture into a running pod's /tmp for a live-gate input file, when the tool-under-test resolves 'path' server-side and the image ships no testdata"

key-files:
  created:
    - internal/mcpserver/mcp_http_live_test.go
  modified: []

key-decisions:
  - "convert_file's 'path' argument is resolved on the SERVER (the mcp-http pod), not the client — this is unchanged HTTP-mode behavior from 25-01/D-04, which only resolved the OUTPUT (presigned-only) side. The live gate satisfies the pod-local-file requirement via `kubectl cp` of the existing internal/mcpserver/testdata/sample.png into /tmp inside the running pod (no image/chart change; ephemeral, gone at pod restart)."
  - "SC3 recheck used the FALLBACK path again: OrbStack's host->cluster proxy was wedged identically to 24-03 (synthetic 198.18.x IP, connects then EOF/empty-reply on every attempt). The direct dial was attempted first and genuinely failed before falling back to a port-forwarded svc/minio + Host-header-preserving DialContext override (the Go equivalent of 24-03's curl --connect-to). Recorded honestly as FALLBACK PASS, not silently upgraded to PASS."
  - "Session-hijack 403 case (T-25-04b) was scripted cheaply using cs.ID() (the go-sdk client's own Mcp-Session-Id) and ran successfully as a bonus assertion beyond the plan's minimum ask."

requirements-completed: [MCPH-01, MCPH-02]

# Metrics
duration: ~45min
completed: 2026-07-14
---

# Phase 25 Plan 03: Live Hard Gate — chart-deployed mcp-http on OrbStack k8s Summary

**D-08 live hard gate PASSED: a real streamable-HTTP MCP session against a chart-deployed mcp-http pod completed initialize, tools/list=5, a real png→jpg conversion with a per-request minted caller key returning a presigned-only result (no local_path), a 401-without-key rejection, and a bonus session-hijack 403 — plus Phase 24's deferred SC3 presigned-from-host recheck, which again required the `--connect-to`-equivalent fallback path (OrbStack proxy wedge, same as 24-03).**

## Gate verdict (loud)

| Check | Result |
|-------|--------|
| mcp-http Deployment Available (single replica) | **PASS** |
| All app Deployments Available (api, worker, document-worker, chromium-worker, webhook-worker×2) | **PASS** |
| initialize handshake | **PASS** — `serverInfo.Name=octoconv, Version=0.1.0` |
| tools/list = exactly 5 tools (MCPH-01) | **PASS** — convert_file, get_job_status, download_result, list_supported_formats, list_presets |
| convert_file real conversion, per-request minted caller key | **PASS** — png→jpg via live pipeline, `isError=false` |
| convert_file result presigned-only, no local_path (MCPH-02/D-04) | **PASS** — `presigned_url` non-empty, `local_path` absent from structuredContent |
| SC3 recheck — presigned URL host-dial (Phase 24 deferred) | **PASS (fallback path)** — direct FQDN dial genuinely attempted first and failed (OrbStack proxy wedge, identical symptom to 24-03); fell back to port-forwarded `svc/minio` + Host-header-preserving dial; 200 + valid JPEG (804 bytes) |
| Request without ApiKey header rejected 401 before any tool runs (D-03) | **PASS** — `{"error":"missing or malformed Authorization header"}` |
| Session-hijack: K2 presenting K1's Mcp-Session-Id (T-25-04b, bonus) | **PASS** — 403 `{"error":"session does not belong to this api key"}` |
| Offline `go test ./...` stays green (live test self-skips) | **PASS** |
| Teardown (helm uninstall, namespace kept) | **PASS** — all pods and PVCs gone |
| OrbStack discipline (compose down throughout, sequential builds, no unresolved >120s hang) | **PASS** |

## Live-gate transcript (UTC, 2026-07-14)

```
04:10   Pre-flight: docker compose ps empty; kubectl get nodes Ready (orbstack);
        images verified — api/worker/document-worker/chromium-worker/
        webhook-worker:dev already present from Phase 24/25-02 sessions
04:10   docker build -f Dockerfile.mcp-http -t octoconv-mcp-http:dev .
        (7.9s go build step; same source sha as 25-02's :planlint smoke build)
04:11:43 helm install octoconv deploy/chart/octoconv -f values-local.yaml
        --create-namespace  (NO --wait, 24-03 discipline) -> "Install complete"
        in ~14s; mcpHttp.enabled=true (chart default)
04:11:43-04:12:12  app pods crash-restarted 2x while the post-install
        createbucket hook created the S3 bucket (compose-equivalent, 24-03
        precedent); mcp-http pod (key-free, no S3/DB dependency) was
        1/1 Running from the very first list — never crash-looped
04:12   kubectl wait --for=condition=Available on all 6 Deployments incl.
        mcp-http -> all "condition met"; MCPHTTP_READY verify passed
04:13   port-forward svc/postgres 5434:5432; go run ./cmd/manage-clients
        create "mcp-http-live-gate-25-03" against in-cluster DB (sanctioned
        mechanism, 24-03 precedent) -> client id + raw key minted
04:14   port-forward svc/mcp-http 8070:8070
04:14   kubectl cp internal/mcpserver/testdata/sample.png <mcp-http-pod>:/tmp/sample.png
        (convert_file's "path" resolves server-side; the pod ships no
        testdata of its own — see Decisions Made)
04:17   go test -run TestMCPHTTPLive: initialize OK, tools/list=5 OK,
        convert_file OK (presigned-only) -- SC3 direct dial FAILED first
        (curl to minio.octoconv.svc.cluster.local:9000 confirmed: resolves
        to synthetic 198.18.0.120, connects, then "Empty reply from server"
        -- reproduced twice, identical to 24-03 Deviation #4)
04:17   port-forward svc/minio 9110:9000; re-ran with
        MCP_HTTP_SC3_FALLBACK_ADDR=127.0.0.1:9110
04:19   go test -run TestMCPHTTPLive: ALL PASS incl. SC3 (fallback path,
        200 + 804-byte valid JPEG), 401-without-key, session-hijack 403
04:19   helm uninstall octoconv -> all pods/PVCs gone within 5s;
        namespace octoconv kept (per instruction); port-forwards killed
```

## Test output (final passing run)

```
=== RUN   TestMCPHTTPLive
    initialize OK: serverInfo=&{Name:octoconv Title: Version:0.1.0 WebsiteURL: Icons:[]}
    tools/list OK: map[convert_file:true download_result:true get_job_status:true list_presets:true list_supported_formats:true]
    convert_file OK: presigned-only result, presigned_url=http://minio.octoconv.svc.cluster.local:9000/octoconv/results/82c28ed0-.../0-out.jpg?X-Amz-... local_path=absent
    SC3 RECHECK: PASS (fallback path) -- presigned URL returned 200 + valid JPEG (804 bytes)
    401-without-key OK: status=401 body={"error":"missing or malformed Authorization header"}
    session-hijack OK: status=403 body={"error":"session does not belong to this api key"}
--- PASS: TestMCPHTTPLive (1.17s)
PASS
```

(Raw minted client key and full presigned signature omitted from this SUMMARY per T-25-09; the transcript above uses the actual test log lines with the URL query string intact — it is a short-lived (900s), already-expired-by-teardown presigned S3 URL for a namespace-scoped MinIO instance that has since been deleted, not a durable secret.)

## What was built (Task 2, commit c844d45)

- **`internal/mcpserver/mcp_http_live_test.go`** — offline-skip-guarded (`OCTOCONV_MCP_HTTP_LIVE=1`) live driver:
  - `connectHTTPMCP`/`authTransport` — real `mcp.NewClient` + `StreamableClientTransport`, presenting `Authorization: ApiKey <key>` on every request via a custom `http.RoundTripper` (mirrors `cmd/mcp-http/main_test.go`'s own helper of the same shape).
  - `TestMCPHTTPLive` — initialize (via `Connect`), `tools/list` (asserts exactly the 5-tool surface), `convert_file` (asserts `presigned_url` non-empty AND `local_path` absent — MCPH-02), a direct-then-fallback SC3 host-dial of the returned presigned URL, a raw-HTTP 401-without-key case (bypasses the go-sdk client entirely so no Authorization header is ever set), and a bonus session-hijack 403 case using the client's own `cs.ID()`.
  - `sc3Dial` — attempts a direct `http.Get` first; only on failure builds a second `http.Client` with a `DialContext` override pointed at `MCP_HTTP_SC3_FALLBACK_ADDR` (leaving the Host header untouched, so the AWS4 signature's `SignedHeaders=host` still validates) — the Go equivalent of 24-03's `curl --connect-to` workaround. Returns which path succeeded so the caller can log/report it honestly.

Task 1 (OrbStack pre-flight, sequential build, `helm install`) touched no source files — pure live-cluster operations, no commit.

## Task Commits

1. **Task 1: OrbStack pre-flight + sequential build + helm install** — no commit (live-cluster operations only, per plan's own file-list note)
2. **Task 2: Scripted MCP-over-HTTP session, SC3 recheck, 401 case, teardown, verdict** — `c844d45` (test)

## Files Created/Modified

- `internal/mcpserver/mcp_http_live_test.go` — the offline-skip-guarded live driver (see above)

## Decisions Made

- **convert_file's server-side path resolution required a pod-local fixture, not a chart/image change.** `sanitizeInputPath` in `internal/mcpserver/tools.go` resolves `path` relative to the process that receives the tool call — in HTTP mode that's the mcp-http pod itself, not the machine running `go test`. Rather than bake a testdata file into `Dockerfile.mcp-http` (a persistent artifact change out of this plan's scope), the existing `internal/mcpserver/testdata/sample.png` was `kubectl cp`'d into `/tmp/sample.png` inside the running pod for the duration of the gate — ephemeral, gone with the pod, zero image/chart changes.
- **SC3 fallback path used again, recorded honestly.** The direct dial to `minio.octoconv.svc.cluster.local:9000` was attempted first and failed with the exact same symptom 24-03 documented (resolves to a synthetic `198.18.x` IP, TCP connects, then "Empty reply from server" — reproduced twice to rule out a one-off blip). `sc3Dial` then fell back to a `kubectl port-forward svc/minio` + Host-header-preserving `DialContext` override and got a clean 200 + valid JPEG. The verdict table above says "PASS (fallback path)", never a bare "PASS" — per the plan's explicit "never silently downgrade a failure to a warning" instruction (inverted here: never silently upgrade a degraded pass to a clean one).
- **Session-hijack case included as a bonus.** The plan listed it as optional ("if scripted cheaply"). `cs.ID()` exposes the streamable client's own `Mcp-Session-Id` with zero extra wiring, so a second raw request presenting a different (bogus) key alongside that session id was added; it correctly got 403 from the session-key-binding middleware shipped in 25-01.

## Deviations from Plan

None — plan executed exactly as written. The SC3 fallback-path usage and the `kubectl cp` fixture placement are both explicitly anticipated by the plan text itself ("if OrbStack's host→cluster proxy is wedged again, fall back...") and by 25-01/25-02's own documented constraints (server-side path resolution, key-free pod), not unplanned deviations.

## Issues Encountered

- **OrbStack host→cluster proxy wedge (environmental, same as 24-03 Deviation #4).** Direct dial of `minio.octoconv.svc.cluster.local:9000` from the host resolved to a synthetic `198.18.0.120` address, connected, then returned an empty reply — on two separate attempts. No local remediation was available within this gate's scope (the 24-03-documented OrbStack restart runbook was not re-attempted here since the port-forward fallback already fully closes the check; a full restart risks disrupting other unrelated workloads on the shared daemon, same caution 24-03 recorded). The presigned URL itself, its signature, and its cluster-internal resolvability are all proven correct — only the OrbStack host-routing layer is unreliable in this dev environment. Re-attempting the un-degraded direct-dial claim after an OrbStack restart remains a standing recommendation carried over from 24-03.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- MCPH-01 and MCPH-02 are both live-gate-proven end-to-end on the actual Phase 24 chart deployment target.
- `internal/mcpserver/mcp_http_live_test.go` is a reusable offline-skip-guarded driver for any future mcp-http live re-check (e.g. a production-representative cluster where the direct-dial SC3 claim could be re-verified without the OrbStack-specific proxy wedge).
- The OrbStack host→cluster proxy wedge is now confirmed to recur across two independent gates (24-03, 25-03) on this machine/OrbStack version — any future live gate on this environment should plan for the port-forward + Host-preserving fallback as the default expectation, not an exceptional path.
- No blockers for Phase 25 completion. Compose path and all other Go packages remain untouched and green offline.

---
*Phase: 25-mcp-streamable-http*
*Completed: 2026-07-14*

## Self-Check: PASSED

- `internal/mcpserver/mcp_http_live_test.go`: FOUND
- Commit `c844d45`: FOUND
- Offline `go test ./...`: all packages green, `TestMCPHTTPLive` skips without live envs
