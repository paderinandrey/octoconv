# Architecture Research

**Domain:** Internal async file-conversion service — milestone v1.5 integration design (MCP server, presets REST CRUD, veraPDF validation, CFB directory classification)
**Researched:** 2026-07-13
**Confidence:** HIGH for patterns directly extending existing, live-verified code (presets REST, CFB classification); MEDIUM for MCP SDK specifics and veraPDF CLI packaging (verified via WebSearch against official sources, not yet live-tested in this repo); LOW/flagged where a genuinely new integration surface is proposed (capabilities endpoint, veraPDF error-signature set)

## Standard Architecture

### System Overview

```
┌──────────────────────────────────────────────────────────────────────┐
│  NEW: Agent / developer machine (outside docker-compose)             │
│  ┌──────────────────┐                                                │
│  │  cmd/mcp-server   │  stdio transport (modelcontextprotocol/go-sdk)│
│  │  (thin binary)    │  reads OCTOCONV_BASE_URL / OCTOCONV_API_KEY   │
│  └────────┬──────────┘                                                │
│           │ internal/mcpserver: convert_file/get_job_status/          │
│           │ download_result/list_supported_formats/list_presets       │
│           │ (pure HTTP client, ApiKey header — no internal/* imports  │
│           │  besides mcpserver's own client)                          │
└───────────┼────────────────────────────────────────────────────────────┘
            │ HTTPS/HTTP — same wire contract as any client
            ▼
┌──────────────────────────────────────────────────────────────────────┐
│                          internal/api (existing)                      │
│  routes.go: /healthz (public) · /v1 group (ByIP → auth → PerClient)    │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────────────────────┐  │
│  │ /v1/jobs     │ │ NEW          │ │ NEW                          │  │
│  │ POST,GET/{id}│ │ /v1/presets  │ │ /v1/formats (or /capabilities)│  │
│  │ (existing)   │ │ CRUD group   │ │ GET, read-only, registry dump │  │
│  └──────┬───────┘ └──────┬───────┘ └──────────────┬────────────────┘  │
└─────────┼────────────────┼────────────────────────┼───────────────────┘
          │                │                        │
          ▼                ▼                        ▼
   internal/jobs    internal/presets.Repo    convert.Default (registry)
   (Postgres)        (List/Get/Create/         .Pairs()/.Engine() walk
                      Update/Deactivate,        — read-only introspection
                      client-scope only          only, no storage/DB
                      via auth.ClientFromContext)

┌──────────────────────────────────────────────────────────────────────┐
│           document-worker container (existing, extended)             │
│  Dockerfile.document-worker: debian:bookworm-slim + LibreOffice        │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │ LibreOfficeConverter.Convert → validateDocumentOutput            │  │
│  │   target==pdf && wantPDFA:                                       │  │
│  │     [existing] /GTS_PDFA substring pre-filter (cheap, kept)      │  │
│  │     [NEW] internal/convert/verapdf.go: runVeraPDFCheck(ctx,path) │  │
│  │           os/exec via SAME hardened runCommand (exec.go)         │  │
│  │           requires JRE + verapdf CLI ADDED to this image          │  │
│  └────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────────┐
│                   internal/convert/olecfb.go (extended)               │
│  IsOLECFB(r) [unchanged, cheap 8-byte magic gate]                      │
│  NEW: ClassifyCFB(r) → {encrypted|legacy|unknown}                      │
│    hand-rolled CFB directory-sector reader (no new dependency),        │
│    looks for top-level stream names:                                  │
│      EncryptionInfo + EncryptedPackage  → encrypted                   │
│      WordDocument / Workbook|Book / PowerPoint Document → legacy       │
│      neither recognized                → unknown (fail-closed 422)    │
│  Consumed by handleCreateJob's existing IsOLECFB branch (3-way split)  │
└──────────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | New or Modified |
|-----------|----------------|-----------------|
| `cmd/mcp-server` | Thin entrypoint: env config, wire `internal/mcpserver`, run stdio transport | NEW cmd |
| `internal/mcpserver` | MCP tool handlers + minimal HTTP API client (multipart upload, poll, presigned download) | NEW package |
| `internal/api` routes.go / new `presets_handlers.go` | `/v1/presets` CRUD group, mounted inside existing authenticated `/v1` group | MODIFIED (routes.go) + NEW file |
| `internal/api` new `formats_handlers.go` | `/v1/formats` (or `/v1/capabilities`) read-only registry dump | NEW file (small) |
| `internal/presets.Repo` | Already has List/Get/Create/Update/Deactivate — reused as-is | UNCHANGED (already shaped for this, per repo.go comment referencing SEED-003) |
| `internal/convert/verapdf.go` | Hardened `os/exec` wrapper invoking the `verapdf` CLI, parses its report for ISO 19005 compliance | NEW file |
| `Dockerfile.document-worker` | Adds JRE + veraPDF CLI alongside existing LibreOffice | MODIFIED |
| `internal/worker/worker.go` | New `terminalVeraPDFSignatures` slice wired into `isTerminal`, same-commit discipline as D-04 | MODIFIED |
| `internal/convert/olecfb.go` | `ClassifyCFB` added alongside existing `IsOLECFB` | MODIFIED |
| `internal/api/handlers.go` | `handleCreateJob`'s single OLE-CFB 422 branch splits into 3 (encrypted/legacy/unknown), 3 distinct log reasons | MODIFIED |
| `internal/e2e` | `TestOLECFBRejectionE2E` per-fixture message assertions; new MCP live-gate test file | MODIFIED + possible NEW file |

## Recommended Project Structure

```
cmd/
├── mcp-server/
│   └── main.go              # env parsing only: OCTOCONV_BASE_URL, OCTOCONV_API_KEY,
│                             # optional OCTOCONV_CONVERT_TIMEOUT; wires internal/mcpserver
│                             # and calls server.Run(ctx, &mcp.StdioTransport{})
internal/
├── mcpserver/
│   ├── mcpserver.go          # package doc + NewServer(client *Client) *mcp.Server wiring
│   ├── client.go             # minimal hand-rolled HTTP client: CreateJob, GetJob,
│   │                         # DownloadResult, ListFormats, ListPresets — NOT shared
│   │                         # with internal/e2e (its helpers live in a _test.go file
│   │                         # and are not importable by production code anyway)
│   ├── tools.go              # MCP tool definitions: convert_file (blocking poll loop),
│   │                         # get_job_status, download_result, list_supported_formats,
│   │                         # list_presets
│   ├── client_test.go        # httptest-based fake-API unit tests (offline, always runs)
│   └── tools_test.go
├── api/
│   ├── routes.go             # MODIFIED: add r.Route("/v1/presets", ...) + GET /v1/formats
│   │                         # inside the existing authenticated /v1 group
│   ├── api.go                # MODIFIED: add PresetAdmin interface + Server field
│   ├── handlers.go           # MODIFIED: split OLE-CFB 422 branch into 3
│   ├── presets_handlers.go   # NEW: handlePresetList/Get/Create/Update/Deactivate
│   └── formats_handlers.go   # NEW: handleListFormats (registry walk, no auth-sensitive data)
├── convert/
│   ├── olecfb.go             # MODIFIED: add ClassifyCFB
│   ├── olecfb_test.go        # MODIFIED: 3-way classification cases
│   ├── verapdf.go            # NEW: runVeraPDFCheck(ctx, path) via hardened exec.go's runCommand
│   ├── verapdf_test.go       # NEW
│   └── libreoffice.go        # MODIFIED: validateDocumentOutput calls runVeraPDFCheck
│                             # instead of (or in addition to, as a fast pre-filter) the
│                             # bare /GTS_PDFA substring check
├── worker/
│   └── worker.go             # MODIFIED: terminalVeraPDFSignatures slice, wired into isTerminal
├── presets/
│   └── repo.go               # UNCHANGED — List/Get/Create/Update/Deactivate already exist
└── e2e/
    ├── e2e_test.go            # MODIFIED: per-fixture OLE-CFB message assertions
    └── mcp_e2e_test.go        # NEW (optional): E2E_BASE_URL-gated live round-trip through
                                # internal/mcpserver's tool handlers directly (not through
                                # stdio framing — that layer is the SDK's own tested concern)
Dockerfile.document-worker      # MODIFIED: + JRE + verapdf CLI
docker-compose.yml               # UNCHANGED for MCP (deliberately not added — see below);
                                  # document-worker environment gets no new required var if
                                  # veraPDF timeout is folded into DOCUMENT_ENGINE_TIMEOUT
```

### Structure Rationale

- **`internal/mcpserver/` (not a self-contained `cmd/`-only package):** matches the existing project convention "`cmd/*` triggers, `internal/*` holds logic" (see `cmd/api/main.go`, `cmd/worker/main.go`, `cmd/document-worker/main.go` — all thin wiring over `internal/worker`, `internal/api`). Even though `internal/mcpserver` imports **no other** `internal/*` package (it is a pure HTTP client of the public API, matching the milestone context's "no internal package imports beyond maybe a shared api-client helper"), keeping it under `internal/` preserves the project's established unit-testing shape (package-scoped `_test.go` files, `httptest` fakes) rather than bloating `cmd/mcp-server/main.go` into an untested monolith.
- **Hand-rolled client, not shared with `internal/e2e`:** `internal/e2e`'s `postJob`/`pollUntilDone`/`buildJobRequest` helpers live in `e2e_test.go` — a `_test.go` file. Go tooling makes these **structurally unimportable** by any non-test, non-`e2e`-package code; "share vs duplicate" is not actually a live choice without first extracting them into a new non-test package (e.g. `internal/httpclient`) used by both `internal/e2e` and `internal/mcpserver`. That refactor is a valid future consolidation but is deliberately **out of scope for v1.5**: the two call sites have different failure-handling shapes (e2e's helpers call `t.Fatalf`; MCP's client must return `error` for tool-call error mapping), the duplicated surface is small (~50-80 lines: build multipart request, poll loop, presigned download), and independent implementations act as a natural cross-check on wire-contract assumptions (mirroring the project's existing preference for independent input/output validation rather than trusting one code path — cf. `validateDocumentOutput` re-sniffing outputs with the same `SniffContainer` used for inputs, rather than trusting the conversion blindly).
- **`internal/api/presets_handlers.go` and `formats_handlers.go` as separate files, not appended to `handlers.go`:** matches the established "one file per responsibility" convention (cf. `internal/convert/`'s `convert.go`/`exec.go`/`libvips.go` split). `handlers.go` is already the API's largest file; presets CRUD and format-discovery are each a distinct, independently-reviewable surface.
- **`internal/convert/verapdf.go` as its own file:** mirrors `libvips.go`/`libreoffice.go`/`chromium.go`'s one-engine-integration-per-file pattern, even though veraPDF is a *validator*, not a `Converter` — it still shells out to an external CLI via the same hardened `exec.go` machinery, so it deserves the same isolation.

## Architectural Patterns

### Pattern 1: Thin external-client `cmd/` binary wrapping the public HTTP API

**What:** A new binary that behaves exactly like any other authenticated internal client of OctoConv (multipart upload, poll `GET /v1/jobs/{id}`, fetch a presigned URL) — it holds **zero** privileged access (no DB, no S3 credentials, no Redis), identical to what `internal/e2e`'s helpers already prove is sufficient to drive the full pipeline.
**When to use:** Any future access surface (CLI tool, Slack bot, MCP server) that only needs "submit + poll + download" — never grant it internal package access; auth/rate-limit/content-validation/fail-closed logic must stay in exactly one place (`internal/api`).
**Trade-offs:** Duplicates a small amount of HTTP-plumbing code per new client; in exchange, every new access surface inherits every existing security property (API-key auth, rate limiting, magic-byte content validation, SSRF-guarded webhooks) for free, with zero risk of a second, subtly-different enforcement path.

**Example (convert_file's blocking poll shape, matching `internal/e2e.pollUntilDone`'s interval/pattern but budget-bounded rather than test-fatal):**
```go
func (c *Client) ConvertFileBlocking(ctx context.Context, path, target string, opts map[string]any) (*JobResult, error) {
    jobID, err := c.CreateJob(ctx, path, target, opts) // multipart POST /v1/jobs
    if err != nil {
        return nil, err
    }
    deadline := time.Now().Add(c.convertTimeout) // OCTOCONV_CONVERT_TIMEOUT, default e.g. 300s
    for time.Now().Before(deadline) {
        job, err := c.GetJob(ctx, jobID) // GET /v1/jobs/{id}
        if err != nil {
            return nil, err
        }
        switch job.Status {
        case "done":
            return c.downloadToTemp(ctx, jobID, job.DownloadURL)
        case "failed":
            return nil, fmt.Errorf("job %s failed: %s", jobID, job.ErrorMessage)
        }
        time.Sleep(2 * time.Second) // same interval convention as e2e's pollUntilDone
    }
    // Budget exceeded: hand control back with the job id rather than hanging the
    // calling agent turn — the agent can retry via get_job_status separately.
    return nil, &JobStillRunningError{JobID: jobID}
}
```

### Pattern 2: Interface-segregated REST-admin surface reusing an already-complete repository

**What:** `internal/presets.Repo` already implements `List`/`Get`/`Create`/`Update`/`Deactivate` (built in Phase 18 explicitly "shaped for reuse by a future MCP list_presets tool"). The API layer needs a **second**, narrow interface alongside the existing `PresetRepo` (`Resolve`-only, used by `handleCreateJob`) — not a widened `PresetRepo` — so `handleCreateJob`'s dependency stays exactly what it uses (existing interface-segregation discipline: "Each interface declares only the subset of methods the consuming package actually calls").
**When to use:** Any time an existing concrete repository already has more capability than one existing consumer needs, and a *new* consumer needs the rest.
**Trade-offs:** Two interface-typed `Server` fields backed by the same concrete `*presets.Repo` value at wiring time (`NewServer(..., presetsRepo, presetsRepo, ...)`) — mildly redundant construction, but keeps both interfaces independently mockable in handler tests and preserves the documented "resolution only" contract on the hot job-creation path.

**Example:**
```go
// internal/api/api.go — ADD alongside the existing PresetRepo (unchanged):
type PresetAdmin interface {
    List(ctx context.Context, scope string, clientID *uuid.UUID, includeInactive bool) ([]presets.Preset, error)
    Get(ctx context.Context, scope string, clientID *uuid.UUID, name string) (*presets.Preset, error)
    Create(ctx context.Context, p presets.CreateParams) (uuid.UUID, int, error)
    Update(ctx context.Context, scope string, clientID *uuid.UUID, name, targetFormat string, options map[string]any, description string) (int, error)
    Deactivate(ctx context.Context, scope string, clientID *uuid.UUID, name string) error
}
```

Ownership is enforced identically to every other client-scoped resource in the codebase: `scope` is **hardcoded** to `presets.ScopeUser` and `clientID` is **always** `&client.ID` from `auth.ClientFromContext(ctx)` inside every REST preset handler — the request body/path never supplies scope or client_id, mirroring the job-ownership 404-not-403 no-leak convention already established in `handleGetJob`. A recommended query-param extension worth flagging explicitly: `GET /v1/presets?include_system=true` merges in read-only system-scope presets (name/target/options only, `client_id` always null, never marked editable) — this is what actually makes MCP's `list_presets` useful (the tool needs the *effective usable* preset set, i.e. what `handleCreateJob`'s shadow-resolution would honor, not just the client's own writable rows). Without this, `list_presets` would only ever show client-owned presets and silently omit every system preset a client is otherwise allowed to use via `preset=<name>`.

### Pattern 3: Hardened external-process validation reusing the existing exec harness

**What:** veraPDF ships as a Java CLI (`verapdf`, requires a JRE — confirmed via the official `veraPDF-apps` repo and the `ghcr.io/verapdf/cli` Docker image, which itself bundles a trimmed JRE specifically to control image size). Rather than introducing a new integration mechanism (HTTP sidecar), invoke it exactly like `soffice` is invoked today: through `internal/convert/exec.go`'s hardened `runCommand` (process-group `Setpgid` + SIGKILL-on-timeout), inside the **same** `attemptCtx` deadline `worker.go`'s `process()` already threads through the whole attempt.
**When to use:** Any additional CLI-based engine or validator that needs the identical hardening (timeout, orphan-process cleanup) the project already built once.
**Trade-offs:** JVM cold-start latency is a real, unverified-in-this-repo cost (LOW confidence — flag for live benchmarking during execution) — mitigate by only invoking veraPDF when `wantPDFA` is true (i.e., only jobs that actually requested `pdf_profile`), and by keeping the cheap `/GTS_PDFA` substring check as a fast pre-filter *before* paying for JVM startup on outputs that trivially lack the marker at all.

**Example (mirrors `validateDocumentOutput`'s existing PDF/A branch):**
```go
// internal/convert/libreoffice.go — validateDocumentOutput, PDF/A branch:
if !bytes.Contains(data, gtsPDFAMarker) {
    return fmt.Errorf("libreoffice: output missing PDF/A OutputIntent marker")
}
// NEW: the marker alone is a non-authoritative sanity check (documented residual
// risk, DOCV3-01) — invoke the real ISO 19005 validator for the authoritative result.
if err := runVeraPDFCheck(ctx, path); err != nil {
    return fmt.Errorf("verapdf: %w", err) // error text feeds terminalVeraPDFSignatures
}
return nil
```

```go
// internal/worker/worker.go — same D-04 same-commit coupling discipline already
// documented for terminalLibreOfficeSignatures:
var terminalVeraPDFSignatures = []string{
    "not compliant",       // veraPDF's own non-conformance report language (verify
                            // exact wording live during execution — LOW confidence,
                            // training-data-only until confirmed against a real run)
    "verapdf: ",           // any wrapped error from runVeraPDFCheck itself (missing
                            // binary, malformed invocation) — deterministic, not
                            // retry-fixable, since the SAME document produces the
                            // SAME failure on the SAME immutable output file.
}
```

### Pattern 4: Fail-closed, magic-byte-then-structural two-stage content classification

**What:** `IsOLECFB` (cheap 8-byte magic check) stays exactly as-is as the fast pre-flight gate; `ClassifyCFB` is a **second**, deeper parse invoked only when `IsOLECFB` already matched — mirroring the existing `Sniff` → `SniffContainer` two-stage pattern for ZIP-based office formats (bare magic bytes can't disambiguate `docx`/`odt`/plain `.zip`; a structural directory walk is needed either way).
**When to use:** Any format family sharing a magic-byte prefix across multiple sub-cases that need different handling.
**Trade-offs:** CFB directory-sector parsing (header + FAT + directory-sector chain to enumerate top-level stream names) is real, bounded complexity — comparable in scope to `SniffContainer`'s ZIP central-directory walk, but for a format with no Go standard-library support. Recommend a **hand-rolled minimal reader** (only enough of MS-CFB to enumerate root-storage entry names — no full stream-content reading, no mini-FAT needed for this classification), consistent with the project's stated zero-new-dependency philosophy (explicitly cited for the decompression-bomb guards) rather than adopting a third-party CFB library.

**Example:**
```go
// internal/convert/olecfb.go — NEW
type CFBClass string

const (
    CFBEncrypted CFBClass = "encrypted" // EncryptionInfo + EncryptedPackage streams present
    CFBLegacy    CFBClass = "legacy"    // WordDocument | Workbook/Book | PowerPoint Document
    CFBUnknown   CFBClass = "unknown"   // magic matched, directory unrecognized — fail closed
)

// ClassifyCFB parses r's CFB directory-sector chain and returns the first
// recognized bucket; IsOLECFB(r) MUST already be true before calling this
// (this function does not itself re-check the magic bytes).
func ClassifyCFB(r io.ReaderAt) (CFBClass, error) { /* ... */ }
```

`handleCreateJob`'s existing single OLE-CFB branch splits into three, each with its own log `reason=` tag and remedy-specific 422 message (`reason=encrypted_document`, `reason=legacy_document`, `reason=cfb_unknown` — replacing the current single `reason=legacy_or_encrypted_document`), and `internal/e2e`'s existing loose `"password"`-substring assertion (deliberately shared across both `legacy.doc` and `encrypted.docx` today, per its own comment) becomes two distinct, fixture-specific assertions using the **already-existing** `testdata/legacy.doc` and `testdata/encrypted.docx` fixtures from Phase 13 — no new fixture work required.

## Data Flow

### Key Data Flows

1. **MCP `convert_file`:** agent → `mcp-server` (stdio) → `internal/mcpserver.Client.CreateJob` (multipart `POST /v1/jobs`, `Authorization: ApiKey ...`) → poll `GET /v1/jobs/{id}` on a 2s interval, bounded by `OCTOCONV_CONVERT_TIMEOUT` → on `done`, GET the presigned `download_url` (self-authorizing, no API key needed — identical to `internal/e2e.assertDownloadIsPDF`'s pattern) → write to a per-call `os.MkdirTemp` directory, preserving the output filename → return the **file path** (not embedded bytes) as the tool result. On budget exhaustion, return the `job_id` so the agent can retry via `get_job_status` later — the job itself keeps running server-side regardless of whether the MCP client is still polling.
2. **MCP `list_presets` / `list_supported_formats`:** both are **read-only GETs against the new REST surfaces** (`GET /v1/presets[?include_system=true]`, `GET /v1/formats`) — this is the hard dependency the milestone context flags: since `internal/mcpserver` has no `internal/convert`/`internal/presets` import, these tools cannot exist before the corresponding REST endpoints ship.
3. **Presets self-service CRUD:** client → `POST/GET/PUT/DELETE /v1/presets[/{name}]` (same `/v1` auth+rate-limit middleware chain as `/v1/jobs`) → handler hardcodes `scope=presets.ScopeUser`, `clientID=&client.ID` from `auth.ClientFromContext` → delegates to `internal/presets.Repo`'s existing methods, unmodified. Write-time opts feedback reuses `presets.ValidateOptsJSON` (the **same** function `cmd/manage-presets`'s `runCreate`/`runUpdate` already call) — this is a UX nicety only; the real safety boundary remains `handleCreateJob`'s existing TOCTOU re-check + full `ValidateApplicability` re-validation at use time (D-06/Pitfall 8), unchanged and untouched by this milestone.
4. **veraPDF validation:** `LibreOfficeConverter.Convert` (document-worker) → `validateDocumentOutput` (only when `wantPDFA`) → cheap `/GTS_PDFA` substring pre-filter → `runVeraPDFCheck(ctx, path)` via `exec.go`'s hardened `runCommand` → non-zero/non-compliant result returns an error whose text is coupled, same-commit, into a new `terminalVeraPDFSignatures` slice consumed by `isTerminal`/`isDocumentTerminal` — a non-compliant PDF/A output is a deterministic, non-retryable failure exactly like every other `terminalLibreOfficeSignatures` entry.
5. **CFB classification:** `handleCreateJob`'s existing pre-storage content-validation chain → `IsOLECFB(file)` (unchanged fast gate) → **new** `ClassifyCFB(file)` → one of 3 distinct 422s, still entirely before any S3/Postgres write (preserves the existing fail-closed-before-any-write discipline).

## Anti-Patterns to Avoid

### Anti-Pattern 1: Granting the MCP server internal package access "for convenience"

**What people might do:** Import `internal/jobs`/`internal/storage`/`internal/presets` directly into `cmd/mcp-server` to skip HTTP round-trips (faster, "it's all one Go module anyway").
**Why it's wrong:** Every safety property this project has spent 19 phases building (auth, rate limiting, magic-byte content validation, SSRF-guarded callbacks, guarded status transitions) lives in `internal/api`'s HTTP handlers, not in the underlying repositories. A direct-DB MCP server would be a second, unauthenticated, unvalidated write path into the exact same tables — precisely the kind of dual-enforcement-path risk the project has consistently avoided (cf. the single-validation-authority discipline already documented for opts).
**Instead:** `internal/mcpserver` talks **only** HTTP to the same public API surface every other internal client uses, holding a real client API key exactly like `internal/e2e`'s `provisionClient` helper proves is sufficient.

### Anti-Pattern 2: Running the MCP server as a docker-compose service

**What people might do:** Add an `mcp-server` service to `docker-compose.yml` for consistency with `api`/`worker`/`document-worker`.
**Why it's wrong:** stdio transport has no network-addressable listening port, no health-check surface, and no "always running" daemon state — it is a per-invocation subprocess spawned directly by the calling agent's own MCP client configuration (e.g. `claude mcp add`), typically on a developer's or agent runtime's local machine, not inside the shared compose network. `docker compose up -d` daemonizes services; a stdio binary inside that model would need an unusual interactive-attach invocation that defeats compose's entire usage pattern, and nothing else in the compose network is an MCP client that would consume it anyway.
**Instead:** Ship `cmd/mcp-server` as a plain Go binary, documented for local/agent-runtime invocation. If a shared, network-facing internal MCP access point is ever needed (SEED-003 explicitly floats this as a *future* streamable-HTTP variant), **that** variant would legitimately belong in `docker-compose.yml` as an ordinary service — but it is out of scope for this milestone's stdio-only build.

### Anti-Pattern 3: Widening `PresetRepo` instead of adding `PresetAdmin`

**What people might do:** Add `List`/`Get`/`Create`/`Update`/`Deactivate` directly onto the existing `PresetRepo` interface since the concrete `*presets.Repo` already implements all of them, avoiding a second interface/field.
**Why it's wrong:** `handleCreateJob` genuinely only needs `Resolve` — the existing doc comment is explicit about this ("the narrow, interface-segregated subset ... resolution only"). Widening the interface it depends on for the sake of a *different* consumer (the new REST handlers) breaks that documented invariant and makes `handleCreateJob`'s test doubles need to implement methods they never call.
**Instead:** A second, separately-named interface (`PresetAdmin`), a second `Server` field, both backed by the same concrete `*presets.Repo` value at wiring time.

### Anti-Pattern 4: Introducing an HTTP sidecar for veraPDF "for isolation"

**What people might do:** Package veraPDF as its own container with a thin HTTP wrapper, called by document-worker over the network, reasoning that keeping the JVM out of the LibreOffice container is cleaner.
**Why it's a genuine trade-off (not a clear anti-pattern, but flagged):** This *would* isolate the JVM's resource/failure domain from `soffice`'s, matching the project's existing philosophy of per-engine-class container isolation (`Dockerfile.worker` vs `Dockerfile.document-worker` vs `Dockerfile.chromium-worker`). However, engine-class separation in this codebase has always been about **horizontal worker types** (separate asynq consumers, separate scaling knobs), never about decomposing a *single job's* pipeline into cross-container HTTP calls — every existing engine invocation (`vips`, `soffice`, `chromium-headless-shell`) is a same-container `os/exec` call through the shared hardened `runCommand`. A sidecar introduces a new integration pattern (HTTP call + its own timeout/retry/transient-vs-terminal classification) for a single validation step, which is disproportionate complexity for this milestone. **Recommendation:** bundle veraPDF into `Dockerfile.document-worker` via `os/exec`, matching every other engine integration; revisit a sidecar only if live JVM startup cost or image bloat prove prohibitive during execution.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| MCP client (agent/IDE) | stdio subprocess, JSON-RPC per `modelcontextprotocol/go-sdk` | Confirmed HIGH confidence: official Go SDK exists, maintained with Google, supports `mcp.StdioTransport` (`github.com/modelcontextprotocol/go-sdk`, https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp). Exact tool-registration API surface should be re-verified against the SDK version pinned in `go.mod` at execution time (SDK is actively evolving). |
| veraPDF CLI | `os/exec` via existing `runCommand` (`internal/convert/exec.go`) | MEDIUM confidence: official CLI + Docker image confirmed (`veraPDF/veraPDF-apps`, `ghcr.io/verapdf/cli`), requires a JRE. Exact non-conformance report format/exit codes to parse into `terminalVeraPDFSignatures` need live verification during execution — do not hardcode signature strings from training data alone without confirming against a real invocation. |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| `cmd/mcp-server` ↔ `internal/api` | Plain HTTPS, `Authorization: ApiKey`, identical wire contract to any other client | No shared code with `internal/e2e` (test-only file, not importable) — deliberate, low-risk duplication |
| `internal/api` (new preset/format handlers) ↔ `internal/presets.Repo` | Direct method calls through new `PresetAdmin` interface | Repo already complete; zero repo changes needed |
| `internal/convert/libreoffice.go` ↔ `internal/convert/verapdf.go` | In-process function call, same `attemptCtx` deadline | No new timeout knob; reuses `DOCUMENT_ENGINE_TIMEOUT` budget |
| `internal/api/handlers.go` (CFB branch) ↔ `internal/convert/olecfb.go` | Direct function call, `ClassifyCFB` gated behind existing `IsOLECFB` | Still entirely pre-storage-write, no new integration surface |

## Build Order (dependency-justified)

1. **Presets REST CRUD (`/v1/presets` + minimal `/v1/formats`)** — build first. Fully independent of the other 3 clusters (touches only `internal/api` + already-complete `internal/presets.Repo`). **Hard prerequisite for MCP's `list_presets` and `list_supported_formats` tools** — the milestone context is explicit that MCP wraps the HTTP API only, with no `internal/convert`/`internal/presets` imports, so those two tools **cannot** be implemented before these endpoints exist. Note: `/v1/formats` is a **newly-identified small gap** — no format-discovery endpoint exists on `main` today; it was not separately called out in the milestone's 4 named clusters but is a hard dependency for `list_supported_formats` and should be bundled into this phase (same theme: expose read-only self-service capability over REST, trivial to implement as a `convert.Default` registry walk).
2. **MCP server (`cmd/mcp-server` + `internal/mcpserver`)** — build second, after step 1's endpoints exist. `convert_file`/`get_job_status`/`download_result` have no dependency on step 1 and could theoretically ship earlier, but shipping the full tool set (including `list_presets`/`list_supported_formats`) in one phase avoids a half-shipped MCP surface and matches the milestone's stated framing of MCP as "new territory — research first," where the REST endpoints from step 1 are a natural, already-scheduled warm-up.
3. **CFB classification (`ClassifyCFB` + 3-way 422 split)** — independent of steps 1-2 and of step 4; touches only `internal/convert/olecfb.go` + `internal/api/handlers.go`'s existing branch + `internal/e2e`'s existing test. Recommended **before** veraPDF: smaller, self-contained, well-precedented (mirrors `SniffContainer`'s existing structural-parse pattern), fixtures already exist from Phase 13 (`legacy.doc`, `encrypted.docx`) — lower execution risk, good to de-risk first.
4. **veraPDF validation** — independent of steps 1-3; touches `internal/convert/{libreoffice,verapdf}.go`, `internal/worker/worker.go`'s terminal-signature slice, and `Dockerfile.document-worker`. Recommended **last**: it is the highest-uncertainty item in the milestone (new JVM runtime dependency, unverified image-size/build-time impact, unverified exact CLI report format needed for terminal-error classification) — sequencing it last means any schedule slip from live-testing surprises doesn't block the other 3, independent clusters.

**Parallelization option:** clusters 1+2 (self-service/agent-access, files under `internal/api`, `internal/presets`, `cmd/mcp-server`) and clusters 3+4 (document-fidelity, files under `internal/convert`, `internal/worker`, `Dockerfile.document-worker`) touch **entirely disjoint files** and could run as two concurrent phases if resourcing allows — the only cross-cluster coupling is conceptual (both improve "document class" quality), not a code dependency.

## Sources

- [modelcontextprotocol/go-sdk (GitHub)](https://github.com/modelcontextprotocol/go-sdk) — official Go SDK, maintained with Google, stdio + streamable-HTTP transports, `mcp.StdioTransport` confirmed (MEDIUM-HIGH confidence, re-verify exact tool-registration API against the pinned SDK version at execution time)
- [mcp package docs (pkg.go.dev)](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp)
- [MCP Go SDK overview](https://go.sdk.modelcontextprotocol.io/)
- [veraPDF/veraPDF-apps (GitHub)](https://github.com/veraPDF/veraPDF-apps) — CLI/GUI/installer source, confirms Java/JRE requirement
- [verapdf/cli (Docker Hub)](https://hub.docker.com/r/verapdf/cli) — official image, ~JRE-trimmed Alpine base, confirms CLI-invocation shape (`docker run ... verapdf/cli a.pdf`)
- [veraPDF CLI Quick Start (docs.verapdf.org)](https://docs.verapdf.org/cli/)
- Direct repository reads: `internal/api/{routes.go,api.go,handlers.go}`, `internal/presets/repo.go`, `internal/convert/{olecfb.go,libreoffice.go,convert.go,docsniff.go}`, `internal/worker/worker.go`, `internal/e2e/e2e_test.go`, `Dockerfile.document-worker`, `docker-compose.yml`, `cmd/{manage-presets,document-worker}/main.go`, `.planning/PROJECT.md`, `.planning/seeds/SEED-003.md`

---
*Architecture research for: OctoConv v1.5 (MCP Access & Document Fidelity)*
*Researched: 2026-07-13*
