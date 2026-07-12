# Stack Research

**Domain:** MCP stdio server, PDF/A ISO validation, OLE-CFB structural parsing (OctoConv v1.5)
**Researched:** 2026-07-13
**Confidence:** HIGH (MCP SDK, mscfb — verified via GitHub API + pkg.go.dev), MEDIUM (veraPDF packaging — verified via Docker Hub API + official Dockerfile, no first-party latency benchmark found)

This is an **additive** stack note for OctoConv v1.5 "MCP Access & Document Fidelity". It supersedes the previous milestone's STACK.md content (v1.4 "CI, Presets & Debt Cleanup" — GitHub Actions tooling, now shipped). It does not revisit the fixed core stack (Go 1.26, chi, asynq/Redis, PostgreSQL 18, MinIO — Notion spec, out of scope) or any existing engine/worker code. Everything below is net-new tooling for: (1) `cmd/mcp-server`, (2) PDF/A ISO 19005 validation, (3) OLE-CFB directory parsing, and an explicit confirmation that (4) REST `/v1/presets` needs **zero new Go dependencies**.

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| `github.com/modelcontextprotocol/go-sdk` | v1.6.1 (latest stable tag; `v1.7.0-pre.2` exists but targets an unfinalized protocol revision — do not use) | Official Go MCP server/client SDK, `cmd/mcp-server` stdio transport | Official SDK maintained by the MCP org in collaboration with Google (`modelcontextprotocol/go-sdk`, 4,788★, pushed 2026-07-10 — active). Requires Go 1.25+ (its own `go.mod` declares `go 1.25.0`) — compatible with OctoConv's 1.26.4. The `mcp.NewServer`/`mcp.AddTool`/`&mcp.StdioTransport{}` API is settled v1.x, not experimental. Reflection-based typed-struct tool schemas (via `jsonschema:"..."` struct tags) match this codebase's existing "typed Go structs over hand-authored JSON" conventions. Ships `ProgressNotificationParams` + `ServerSession.NotifyProgress` and `ServerOptions.KeepAlive` — directly relevant because `convert_file` is a **blocking** tool call that can run for tens of seconds to minutes on document/chromium jobs. |
| verapdf CLI (bundled into `Dockerfile.document-worker`, not a Go module) | `verapdf/cli:1.30.2` (Docker Hub `latest` tag as of 2026-06-03; veraPDF uses even-minor = stable-release convention, so 1.30.x is the current stable line) | ISO 19005 (PDF/A) conformance validation, replacing the existing OutputIntent sanity check | veraPDF is the open-source reference validator behind the PDF Association's own conformance test corpus — treated as *the* authoritative ISO 19005 checker industry-wide (used by national archives/libraries). No credible pure-Go or dependency-free alternative exists (confirmed by targeted search — see "What NOT to Use"). It is invoked as an external CLI process, which matches the codebase's existing hardened-exec pattern (`internal/convert/exec.go` — `Setpgid` + timeout + process-group kill), so integration needs zero new execution abstraction. |
| `github.com/richardlehane/mscfb` | v1.0.7 (released 2026-06-06 — actively maintained, not a stale/abandoned project) | Parse the OLE-CFB directory (header + FAT/DIFAT sector chain + mini-FAT + 128-byte directory entries) to enumerate stream names for legacy-vs-encrypted classification | Small (71★, effectively single-maintainer), but the author (richardlehane) is a recognized digital-preservation tooling author (same lineage as `siegfried`, a widely-used PRONOM format-identification tool) — a credibility signal beyond raw star count. Apache-2.0, one small transitive dependency (`richardlehane/msoleps`, same author). It is the only credible off-the-shelf Go CFB directory reader found; see "Alternatives Considered" for why a zero-dep hand-rolled parser is feasible but not recommended for this specific fail-closed security check. |
| chi + pgx (existing, no version change) | v5.3.0 / v5.10.0 (already in `go.mod`) | REST `/v1/presets` CRUD (PRST-V2-01) | **Confirmed: no new dependency needed.** `internal/presets` (shipped in Phase 18, v1.4) already owns the SQL layer (client/system-scope shadowing, bump-on-update, re-validation). This milestone only adds `internal/api` handlers + chi routes on top of the existing `Repo`/`Server`/`writeJSON`/`writeError` conventions and the already-mandatory API-key middleware on all `/v1/*` — a pure application-layer addition, not a new library surface. |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `github.com/richardlehane/msoleps` | v1.0.3 (transitive, pulled in automatically by mscfb's `go.mod`) | OLE property-set (MSOLEPS) parsing | Not called directly by OctoConv — mscfb's `go.mod` requires it, but only `Reader.Next()`/`File.Name` are needed for stream-name enumeration. Just be aware it lands in `go.sum` after adding mscfb; it is not itself a decision point. |
| `github.com/google/jsonschema-go` | v0.4.3 (transitive, pulled in by go-sdk) | Backs the reflection-based tool-schema generation inside `mcp.AddTool` | Not called directly; relevant only if debugging generated JSON-schema output for `convert_file`/`list_presets`/`list_supported_formats` tool definitions. |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| `go vet` / `gofmt` (existing) | Minimum enforced bar for new code, per project convention | No new lint tooling needed for this milestone; nothing about MCP/veraPDF/CFB changes the existing bar. |

## Installation

```bash
# Core — MCP SDK
go get github.com/modelcontextprotocol/go-sdk@v1.6.1

# Core — CFB directory parsing
go get github.com/richardlehane/mscfb@v1.0.7

# veraPDF is NOT a Go module — it is an external Java CLI, bundled into
# Dockerfile.document-worker via multi-stage COPY from the official image
# (see Dockerfile guidance below). No `go get` needed.

# REST presets CRUD: no new packages — reuse existing chi/pgx/internal/presets.
```

Multi-stage Dockerfile addition for `Dockerfile.document-worker` (recommended packaging — see rationale in "Alternatives Considered"):

```dockerfile
# --- veraPDF stage: reuse the official jlink-trimmed JRE + app, don't rebuild it ---
FROM verapdf/cli:1.30.2 AS verapdf

# In the runtime stage, alongside the existing LibreOffice apt-get install:
COPY --from=verapdf /opt/java/openjdk /opt/java/openjdk
COPY --from=verapdf /opt/verapdf /opt/verapdf
ENV PATH="/opt/java/openjdk/bin:/opt/verapdf:${PATH}"
```

Then invoke `verapdf` through the existing `internal/convert/exec.go` hardened runner (`Setpgid` + `ENGINE_TIMEOUT`-style bound + process-group kill), exactly like the libvips/LibreOffice/chromium engine calls today — no new execution abstraction needed.

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| `modelcontextprotocol/go-sdk` v1.6.1 | `mark3labs/mcp-go` (8,886★, pushed 2026-07-09 — more mature, far more GitHub dependents, predates the official SDK by months) | If OctoConv needed **HTTP/SSE transport** for the MCP server today — mcp-go has first-class HTTP/SSE support the official SDK does not yet prioritize (official SDK focuses on stdio/command transports). This milestone is explicitly stdio-only per PROJECT.md ("cmd/mcp-server, stdio-транспорт"), which removes mcp-go's main structural advantage. The official SDK is the safer long-term bet for spec compliance and future protocol revisions (Google co-maintains it under the `modelcontextprotocol` org), at the cost of a smaller community and less production mileage than mcp-go. Re-evaluate only if a remote/network MCP endpoint becomes a requirement later. |
| Bundle verapdf CLI into `Dockerfile.document-worker` (multi-stage `COPY` from `verapdf/cli`) | Separate `verapdf/rest` sidecar container (official image, ~75MB, Dropwizard-based REST wrapper, port 8080 + 8081 diagnostics) reached over HTTP from document-worker | If independent lifecycle/resource limits for the JVM heap (`JAVA_OPTS=-Xmx...`, `VERAPDF_MAX_FILE_SIZE`) separate from LibreOffice's own container limits matter, or if per-job JVM cold-start latency becomes a measured bottleneck (a long-lived REST service amortizes JVM startup across many requests instead of paying it per job). This is a real architectural tradeoff, flagged for the planner: bundling stays inside the existing "shell out to hardened exec" pattern with zero new inter-service communication; a sidecar introduces a genuinely new pattern — a worker calling another local service over HTTP, versus today's model where every worker only ever talks to Postgres/Redis/S3, never to another worker directly. Recommendation: start with the bundled CLI (simpler, no new communication pattern) and only move to a sidecar if measured cold-start latency proves unacceptable in practice. |
| `richardlehane/mscfb` for CFB directory parsing | Zero-dependency hand-rolled parser (read the 512-byte header, walk the FAT/DIFAT sector chain, walk the mini-FAT for small streams, parse 128-byte directory entries, decode UTF-16LE names) | Only if literally zero new deps is a hard requirement and there's confidence in correctly implementing DIFAT continuation (needed once a CFB file has >109 FAT sectors — rare for typical small Office docs, but not guaranteed absent) and mini-FAT chain walking. This is a **fail-closed security classification** (encrypted vs legacy 422), so a subtly-wrong hand-rolled walk is a real risk — a bug could misclassify a stream name or silently truncate the directory listing. mscfb's core file is only 416 lines (auditable/vendorable), so it avoids re-solving a well-known-tricky sector-chained binary format from scratch. Note: the project's own precedent of writing zero-dep parsers for decompression-bomb protection (PNG/JPEG/WEBP/HEIC/TIFF headers) is a weaker analogy here than it looks — those formats have simple linear headers, while CFB's FAT/mini-FAT sector-chaining is where the real complexity (and bug surface) lives. |

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| Any "pure-Go PDF/A ISO 19005 validator" | Searched specifically — none found with credible adoption or actual ISO 19005 conformance-test coverage. What exists are commercial cloud-API wrappers (ConvertAPI, Apryse) that call out to their own hosted servers or bundle non-Go engines under the hood; none is a standalone Go library doing real ISO 19005 validation locally. Building one from scratch would mean re-implementing years of the PDF Association's own conformance test corpus — well out of scope for this milestone. | veraPDF CLI, bundled as described above. |
| Apache PDFBox Preflight (standalone) | Also JVM-based (no packaging advantage over veraPDF) and has materially weaker/less-complete PDF/A rule coverage than veraPDF, which is the industry reference implementation. Swapping to it trades conformance rigor for nothing in return. | veraPDF CLI. |
| `modelcontextprotocol/go-sdk` v1.7.0-pre.x | Pre-release, targets a not-yet-final protocol revision (2026-07-28) — do not depend on a pre-release for a production internal service. | v1.6.1 (latest tagged stable). |
| Hand-authoring MCP tool JSON schemas as raw strings | The official SDK generates schemas via struct reflection + `jsonschema` tags on typed request/response structs — hand-authoring raw JSON schema duplicates work the SDK already does and risks drifting from the Go struct that actually decodes the request at runtime. | `mcp.AddTool(server, &mcp.Tool{...}, handlerFunc)` with typed structs + `jsonschema:"description"` tags. |
| Embedding full binary file bytes (base64 `BlobResourceContents`/`EmbeddedResource`) in MCP tool results for `download_result` | OctoConv already returns presigned S3 URLs (`GET /v1/jobs/{id}`); base64-embedding a multi-MB PDF/image inside an MCP `CallToolResult` bloats the context window the calling agent pays for, and duplicates data already reachable via URL — with no benefit since the agent still needs to fetch/display it externally. | Return the existing presigned URL as `TextContent` (or `ResourceLink`, if the protocol version in use supports it) from `get_job_status`/`download_result` — never `EmbeddedResource`. |

## Stack Patterns by Variant

**If the MCP server needs to expose progress during long conversions (multi-minute chromium/LibreOffice jobs):**
- Read `req.Params.GetProgressToken()` inside the `convert_file` handler; if present, poll the underlying `GET /v1/jobs/{id}` internally and call `req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{...})` per tick.
- Progress tokens are optional per the MCP spec (the client must opt in by sending one) — treat progress reporting as strictly best-effort. The tool must behave identically (just silently) when no token is present.

**If `convert_file` needs to survive a client-side idle timeout on very slow document/chromium jobs:**
- Set `mcp.ServerOptions.KeepAlive` so the stdio session isn't dropped by a missed ping during a long blocking tool call. Pin to **v1.6.1, not v1.5.x** — a race condition in `ServerSession.startKeepalive` (peers that don't implement ping being incorrectly killed) was fixed in the v1.6 line.

**If `Dockerfile.document-worker` image size becomes a real deployment concern after bundling veraPDF:**
- Reconsider the `verapdf/rest` sidecar (see Alternatives Considered). The official image is already a jlink-trimmed JRE (53.7MB for `cli`, 74.9MB for `rest`, both compressed), so the marginal cost of bundling is moderate, not catastrophic — but it does add a second runtime (JVM) alongside LibreOffice's native C++ stack inside one container, which is a real (if small) increase in attack surface and image complexity.

## Version Compatibility

| Package A | Compatible With | Notes |
|-----------|-----------------|-------|
| `modelcontextprotocol/go-sdk@v1.6.1` | Go 1.25+ (its own `go.mod` declares `go 1.25.0`) | OctoConv is on Go 1.26.4 — compatible, no toolchain conflict. |
| `richardlehane/mscfb@v1.0.7` | Go 1.18+ | Well below OctoConv's 1.26 floor — no compatibility concern. |
| `verapdf/cli:1.30.2` (Docker) | Copying its `/opt/java/openjdk` + `/opt/verapdf` into Debian bookworm-slim (OctoConv's existing worker base image) via multi-stage `COPY --from=` | **Flagged integration risk, not assumed-safe:** the official image's own final stage is `alpine:3` (musl libc), while `Dockerfile.document-worker`'s target is `debian:bookworm-slim` (glibc). Copying only the jlink-produced JRE tree and the `verapdf` app directory across that libc boundary is a common pattern but is not guaranteed to "just work" without verification — test that the copied JRE's native libraries (`libjvm.so` etc.) actually load correctly against bookworm's glibc/libstdc++ before relying on this in CI, rather than assuming it based on the Dockerfile structure alone. |

## Sources

- https://github.com/modelcontextprotocol/go-sdk — repo metadata via GitHub API (4,788★, pushed 2026-07-10, not archived) — HIGH confidence
- https://github.com/modelcontextprotocol/go-sdk/releases — release history (v1.5.0 → v1.6.1 stable, v1.7.0-pre.x pre-release) — HIGH confidence
- https://github.com/modelcontextprotocol/go-sdk/tags (via GitHub API) — confirms v1.6.1 is the latest non-prerelease tag — HIGH confidence
- https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/go.mod — confirms `go 1.25.0` floor and dependency list (jsonschema-go, oauth2, uritemplate, etc.) — HIGH confidence
- pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp (via WebFetch) — `TextContent`/`ImageContent`/`AudioContent`/`EmbeddedResource`/`ResourceLink` content types, `ProgressNotificationParams`, `ServerSession.NotifyProgress`, `ClientOptions.ProgressNotificationHandler`, `Ping`/`Wait` — MEDIUM-HIGH confidence (WebFetch summarization of a live pkg.go.dev page, not independently re-verified field-by-field)
- https://github.com/mark3labs/mcp-go — repo metadata via GitHub API (8,886★, pushed 2026-07-09, not archived) — HIGH confidence
- WebSearch: "modelcontextprotocol go-sdk ProgressNotification long running tool call keepalive" — keepalive race-condition fix mention — MEDIUM confidence (single search-result summary, not cross-checked against the raw changelog diff)
- https://hub.docker.com/v2/repositories/verapdf/cli/tags (Docker Hub API) — confirms `latest` = `v1.30.2`, image size 53.7MB, last pushed 2026-06-03 — HIGH confidence
- https://hub.docker.com/v2/repositories/verapdf/rest/tags (Docker Hub API) — confirms `latest` = `v1.30.2`, image size 74.9MB, last pushed 2026-06-07 — HIGH confidence
- https://github.com/veraPDF/veraPDF-apps/blob/integration/Dockerfile (via WebFetch) — multi-stage build: `eclipse-temurin:11-jdk-alpine` → jlink custom JRE → `alpine:3` final stage, `ENTRYPOINT ["/opt/verapdf/verapdf"]` — HIGH confidence
- WebSearch: "verapdf CLI startup time JVM invocation per file validation performance" — no first-party benchmark found; only a vendor blog (ConvertAPI, which has a commercial interest in promoting a paid alternative) mentioning "JVM dependency and cold-start cost" as a friction point — LOW confidence, flagged as an unverified vendor claim, not a measured number
- https://github.com/richardlehane/mscfb — repo metadata via GitHub API (71★, pushed 2026-06-06, not archived) — HIGH confidence
- pkg.go.dev/github.com/richardlehane/mscfb (via WebFetch) — API shape: `mscfb.New(io.ReaderAt)`, `Reader.Next() (*File, error)`, `File.Name`/`Path`/`Size`, Apache-2.0 license, v1.0.7 latest — HIGH confidence
- https://raw.githubusercontent.com/richardlehane/mscfb/master/go.mod — confirms `go 1.18` floor and single transitive dep `richardlehane/msoleps v1.0.3` — HIGH confidence
- https://api.github.com/repos/richardlehane/mscfb/tags — confirms v1.0.7 is the latest tag — HIGH confidence
- `go.mod` (this repo, read directly) — confirmed no existing MCP/PDF/CFB deps to build on; chi v5.3.0 + pgx v5.10.0 already present for REST presets — HIGH confidence
- `internal/convert/olecfb.go` (this repo, read directly) — confirmed the existing 8-byte magic-only detector this milestone's CFB parser extends, and its documented deferral of directory parsing to "v2 (DOCV3-02)" — HIGH confidence
- `Dockerfile.document-worker` (this repo, read directly) — confirmed current base (`debian:bookworm-slim`) and structure this milestone's veraPDF packaging decision lands on — HIGH confidence

---
*Stack research for: OctoConv v1.5 (MCP server, presets REST, veraPDF, CFB parsing)*
*Researched: 2026-07-13*
