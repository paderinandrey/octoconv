# Feature Research

**Domain:** Internal file-conversion service — MCP agent access, self-service presets REST, PDF/A ISO validation, OLE-CFB error taxonomy (milestone v1.5)
**Researched:** 2026-07-13
**Confidence:** MEDIUM overall (HIGH for MCP protocol mechanics and CFB stream names — grounded in official spec/blog sources; MEDIUM for veraPDF operational specifics — official docs found but no direct benchmark data; MEDIUM for presets REST — derived from existing verified CLI/repo code, not external research)

This file is organized by the four v1.5 clusters, each with its own Table Stakes / Differentiators / Anti-Features, followed by every open question with a recommended default, then cross-cluster dependencies and an MVP cut.

---

## Cluster 1: MCP Server

### Table Stakes

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| `convert_file` blocking tool (upload → poll → result) | This is the entire point of the seed (SEED-003) — agents want one call, not manual multipart+poll+presign | MEDIUM | Internally: multipart upload to `POST /v1/jobs`, then poll `GET /v1/jobs/{id}` until `done`/`failed`, bounded by a server-side timeout |
| `get_job_status` / `download_result` as separate tools | Escapes the blocking pattern for genuinely long conversions (large documents, HTML render) without inventing a new async protocol | LOW | Thin wrappers over existing `GET /v1/jobs/{id}` + presigned URL fetch — no new API surface needed |
| `list_supported_formats` | Agent must discover valid `(source, target)` pairs before calling `convert_file`, or it will blindly trigger 422s | LOW | Direct wrap of `convert.Default` registry (`Pairs()` per engine) — data already exists, no new backend work |
| `list_presets` | Mirrors CLI/API preset self-service; lets an agent discover `preset="archive-pdf"` instead of guessing opts | LOW-MEDIUM | See dependency note below — must merge system + client scope like `Resolve` does, not a single `repo.List` call |
| stdio transport, single API key via env | Matches the seed's decided design; simplest possible deployment for a developer's local MCP client config | LOW | `modelcontextprotocol/go-sdk` `StdioTransport`; no HTTP transport needed for v1.5 |
| Human-readable tool descriptions with explicit XOR/param guidance | Poorly described tools cause models to call them wrong (wrong param combo, wrong format casing) — directly costs correctness | LOW | Cheap to get right, expensive to retrofit — write descriptions test-first against a real agent session |
| Tool-execution errors surfaced as `isError: true` content, not protocol errors | Spec explicitly separates "business logic failure" (422 unsupported pair, 413 too large) from "protocol/malformed call" — conflating them breaks agent retry logic | LOW | Mechanical: forward the API's `{"error": "..."}` body text into the `isError` content block |

### Differentiators

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| `resource_link` result (presigned URL) instead of inline base64 | Avoids context-window poisoning; a 1 MB result would be ~1.5M tokens as base64 — no competitor-grade MCP server does this for files of any real size | LOW | OctoConv already has a presigned-URL mechanism (`PresignGet`) — this is pure reuse, not new plumbing |
| Progress notifications reusing the client's `progressToken` | Lets agent hosts render a progress bar during a multi-second/minute document or HTML conversion instead of an opaque hang | LOW-MEDIUM | Only if the incoming `tools/call` request supplied a `progressToken` (spec: optional, not all clients send one) — do not invent a bespoke notification channel |
| `convert_file` accepting `preset=` (mirrors API XOR) | Agents get the same "one named preset, zero opts fiddling" ergonomics as human API clients — directly extends v1.4 presets value into the agentic surface | LOW | The MCP layer performs no independent validation beyond a friendly early rejection; the API's existing D-01 XOR gate remains the single source of truth |

### Anti-Features

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|------------------|-------------|
| Direct Postgres/S3/Redis access from the MCP server | "Just skip the HTTP hop, it's faster" | Duplicates auth, rate limiting, content validation, and fail-closed logic in a second place — the seed explicitly rejects this (thin wrapper over HTTP API only) | Always go through the existing `/v1/*` API with the client's API key |
| Base64-embedding every result file by default | Simple, one content-type, "just works" for tiny test files | Breaks down catastrophically for real documents/images — 1 MB file ≈ 1.5M tokens; poisons agent context and burns budget | `resource_link` / presigned URL by default; base64 only as an explicit opt-in for genuinely tiny files (see Q1 below) |
| Reimplementing multipart/poll/presign logic per-tool inconsistently | Each tool "just does its own thing" quickly | Divergent error handling, inconsistent timeouts, duplicated poll loops — a maintenance trap as more tools are added | One shared internal client wrapping `POST /v1/jobs` / `GET /v1/jobs/{id}` / presigned download, all five tools call into it |
| Streamable-HTTP transport in this milestone | "Might as well support both transports now" | No current internal consumer needs shared/remote MCP access; adds transport-negotiation complexity and auth-over-HTTP questions (does the MCP HTTP endpoint need its own API-key middleware?) that are out of scope | stdio only for v1.5; revisit if an internal MCP-server catalog materializes (already flagged as a SEED-003 trigger condition) |
| Unbounded blocking wait inside `convert_file` | "Agents want one call" (true) taken to an extreme — wait forever until done | A stuck/slow document conversion (LibreOffice, Chromium engines have their own multi-second-to-minute budgets) can hang the MCP stdio pipe indefinitely, and MCP hosts often have their own client-side tool-call timeouts that will hard-kill the call anyway | Bound the internal poll loop with a config timeout (e.g. mirrors `ENGINE_TIMEOUT`/`DOCUMENT_ENGINE_TIMEOUT` order of magnitude); on timeout return `job_id` + a message telling the agent to use `get_job_status`/`download_result` — graceful degradation to the async pattern, never a silent infinite hang |

---

## Cluster 2: Presets REST CRUD

### Table Stakes

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| `POST /v1/presets` (create, client-scope only) | Self-service is the entire point of PRST-V2-01 | LOW | Thin handler over existing `presets.Repo.Create`; scope is ALWAYS derived from the authenticated client, never a request field |
| `GET /v1/presets` (list own presets) | A client needs to see what it already has before creating/updating | LOW | Wraps `Repo.List(ScopeUser, &client.ID, includeInactive=false)` |
| `GET /v1/presets/{name}` (show one) | Needed before update, to see current target/opts | LOW | Wraps `Repo.Get` |
| `PUT /v1/presets/{name}` (update = bump-on-update) | Mirrors CLI `update` verb exactly — same versioning model, same guarantees | LOW-MEDIUM | Wraps `Repo.Update`; full-replacement semantics (client resends target+opts+description each time), not a partial PATCH |
| `DELETE /v1/presets/{name}` (maps to deactivate, not hard delete) | Matches the existing "no hard delete" philosophy (client key rotation pattern, presets `is_active` flag) | LOW | Wraps `Repo.Deactivate`; DELETE verb is the REST-idiomatic name for the operation even though it's a soft-deactivate under the hood — document this explicitly so clients don't expect history loss |
| Existing per-client rate limiting applies automatically | `/v1/presets/*` sits inside the same chi `/v1` route group as `/v1/jobs` | LOW | No new middleware — `ratelimit.PerClient` already wraps the whole group in `routes.go` |
| Every response scoped/filtered so a client can never see another client's presets or any system preset via `GET /v1/presets` | Directly mirrors the AUTH-03 cross-client 404 discipline already enforced for jobs | LOW | `clientID` always comes from `auth.ClientFromContext`, never a request parameter — same pattern as `handleGetJob`'s ownership guard |

### Differentiators

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Response body echoes the new version number on create/update | Lets a self-service client immediately know which version a subsequent job will provenance-tag (`preset_name`/`preset_version` on the job row) | LOW | Already returned by `Repo.Create`/`Repo.Update` — just surface it in JSON |
| `409 Conflict` on create-when-active-exists | REST-idiomatic status distinct from generic 400/422, gives a self-service client (or the MCP-adjacent tooling) a programmatically distinguishable "use PUT instead" signal | LOW | The CLI just `log.Fatalf`s; the REST layer should map the same repo condition to `409` explicitly rather than a generic 500 |

### Anti-Features

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|------------------|-------------|
| Creating/updating **system**-scope presets via REST | "Why maintain two management surfaces?" | System presets are shared across every client — a self-service endpoint that could touch them turns any single compromised/misbehaving API key into a blast-radius-of-everyone incident; this is explicitly flagged in the milestone as an anti-feature | System presets stay exclusively on the operator `manage-presets` CLI, which requires DB access, not an API key |
| Exposing full version history over `GET /v1/presets` by default | "Might as well show everything, it's already in the table" | Adds response-shape complexity and a second semantic ("list active" vs "list all") for a self-service surface whose only real need is "what do I have right now" | Default to active-only, exactly like the CLI's default (`includeInactive=false`); do not add an `?all=true` history param in this milestone — defer until a real audit/debugging need appears |
| Idempotency-Key header / ledger for preset writes | "Best practice for REST APIs that mutate state" | Preset CRUD is low-frequency, human-paced self-service — not a hot retried path like webhook delivery; a full idempotency ledger is meaningfully more code (storage, TTL, key validation) for a feature with no evidenced need | `409` on duplicate create + normal client-side retry-on-network-error is sufficient; revisit only if evidence of double-submission problems emerges |
| A distinct/looser rate limit tier for `/v1/presets/*` | "Preset management feels different from job submission, maybe it needs its own quota" | Adds a second rate-limit configuration axis for a low-volume surface; existing per-client quota already exists and this traffic pattern (rare CRUD calls) won't meaningfully compete with job-submission quota | Reuse the existing single per-client `ratelimit.PerClient` quota unchanged |

---

## Cluster 3: veraPDF ISO 19005 Validation

### Table Stakes

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Always-on validation for every PDF/A export (no opt-in flag) | Mirrors the existing precedent: the current OutputIntent sanity check (v1.3 Phase 14) is already unconditional, not opt-in — DOCV3-01 is framed as *replacing* that check with a real one, not adding an optional extra | LOW (policy), MEDIUM (integration) | An opt-in flag would silently let non-compliant "PDF/A" ship for any client that forgets to set it — defeats the entire purpose of this milestone item |
| Validate against the **PDF/A-2b** profile specifically | Matches the existing export target (`pdf_profile` opt already produces PDF/A-2b, v1.3/Phase 14) | LOW | `verapdf -f 2b <file>` (or Docker image equivalent) — do not validate against a stricter/different profile (2u/3b) than what LibreOffice was configured to produce |
| Validation failure → terminal job failure (job `status=failed`, not a silent pass) | Mirrors current behavior (OutputIntent check already fails the job); a client who explicitly asked for PDF/A output has a right to know if what they got isn't actually compliant | LOW (policy), MEDIUM (plumbing) | `error_code="pdfa_validation_failed"`, `error_message` a bounded/truncated summary of the failing ISO rule(s) — veraPDF's full report can be large and must not be dumped verbatim into the `jobs` row |
| Bounded validation timeout, hardened process execution | veraPDF is a JVM CLI process — same untrusted-external-process risk class as libvips/LibreOffice/Chromium | MEDIUM | Reuse the existing hardened `runCommand` (Setpgid + process-group SIGKILL) pattern from `internal/convert/exec.go`; a veraPDF timeout should be classified terminal (a validation that can't complete can't be trusted), same as `DOCUMENT_ENGINE_TIMEOUT` |

### Differentiators

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Real ISO 19005 structural/semantic conformance (fonts embedded, color spaces, XMP metadata, etc.), not just an OutputIntent marker | This is the actual value-add of DOCV3-01 — internal clients relying on "PDF/A" for archival/compliance workflows get a genuine guarantee instead of a heuristic | HIGH | veraPDF is the industry-reference open-source validator (used by PDF Association test corpora) — no better-grounded alternative exists for self-hosted ISO conformance checking |

### Anti-Features

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|------------------|-------------|
| Opt-in `strict_validation` flag defaulting to off | "Don't slow down/break existing clients who don't care about strict compliance" | Silently reintroduces the exact gap this milestone exists to close; a client that doesn't set the flag gets the old (weaker) guarantee indefinitely | Always-on for `pdfa` target; if a genuine "I don't need ISO compliance" use case emerges later, that's a different target format/opt (e.g., "pdf" without the `pdf_profile` opt), not a bypass flag on the PDF/A path itself |
| Attaching the validation report as warning metadata but marking the job `done` anyway | "Let the client decide if the failure matters to them" | Directly contradicts the Core Value (safe, reliable conversion) — a client asked for PDF/A and would receive a file mislabeled as compliant; internal clients built around this milestone's guarantee would silently regress | Terminal `failed` status; if a future need for "best-effort with warnings" surfaces, it should be a distinct, explicitly-named target/opt, not a silent downgrade of the existing one |
| Running veraPDF as a new dedicated engine-class/queue | "It's a new external tool, treat it like a new engine" | This is a post-processing **gate** on an existing document conversion job, not a new conversion type — routing it through a whole new asynq queue/engine-class duplicates infrastructure for no benefit | Invoke veraPDF as an in-process step inside the existing document-worker's PDF/A conversion path, right after LibreOffice produces output, before `MarkDone` |

---

## Cluster 4: CFB Encrypted-vs-Legacy Distinction

### Table Stakes

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Distinct 422 for password-protected OOXML vs legacy binary Office | This is the literal deliverable (DOCV3-02) — clients currently get one undifferentiated message and can't tell "remove the password" from "convert the file format" | MEDIUM | Requires a minimal CFB **directory** parser (walk sector/FAT enough to enumerate root storage/stream entry names) — `IsOLECFB` today only checks the 8-byte magic, it does not open the directory |
| Encrypted branch triggers on presence of `EncryptedPackage` **and/or** `EncryptionInfo` root streams | These two streams are the primary, method-agnostic indicators of an encrypted Office document (both Standard and Agile encryption write them) — verified via CFB/OOXML encryption research (Didier Stevens' analysis, corroborated by Apache POI's own encryption-detection code) | MEDIUM | Confidence: MEDIUM — corroborated by two independent technical sources, not an official Microsoft spec citation, so validate against a few real encrypted `.docx`/`.xlsx` samples during implementation |
| Legacy branch triggers on presence of any of `WordDocument` / `Workbook` / `PowerPoint Document` root streams | These are the well-known root-level streams for binary `.doc`/`.xls`/`.ppt` (pre-2007 Office formats) | MEDIUM | Older Excel variants may use `Book` instead of `Workbook` — include both in the legacy allow-list to avoid a false miss |
| Fall-through: any other CFB content (neither list matches) keeps the CURRENT generic 422 | Fail-closed default — do not invent a third bespoke message for content this milestone wasn't asked to characterize (e.g., legacy Visio/Project files, corrupted directories) | LOW | Preserves the existing fail-closed discipline (D-05/D-06 in `olecfb.go`) for anything outside the two named cases |

### Differentiators

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Distinct logged `reason=` tags (`encrypted_document` / `legacy_document`) alongside the two 422s | Improves operator diagnosability without changing the client-facing contract — mirrors the existing scoped logging discipline (D-08) already used for every other content-validation rejection | LOW | Pure additive change to the existing `log.Printf` call sites |

### Anti-Features

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|------------------|-------------|
| Pulling in a full third-party OLE/CFB library (e.g. a Go port of `olefile`) | "Why write a parser when libraries exist" | Contradicts the project's established zero-dependency-parser convention (Phase 7's decompression-bomb protection deliberately avoided new deps for the same class of problem — bounded, scoped binary format parsing) | Implement the minimal directory-walk (enough to enumerate root entry names) from the public MS-CFB specification, in-house, matching existing code style |
| Adding a new machine-readable `error_code` field for these two 422s specifically | "Now that we distinguish them, give clients something to parse" | No other synchronous request-validation 422 in `handleCreateJob` carries a structured `error_code` today (that field only exists on job-level async `failed` status) — introducing it for just these two cases creates an inconsistent, half-covered API surface | Keep this a message-only distinction, consistent with every other content-validation 422 in the same handler; treat `error_code` taxonomy for synchronous validation as a separate, larger future decision if ever needed |
| Attempting to detect/report the *specific* encryption method (Standard vs Agile) or legacy sub-format details in the message | "More detail is more helpful" | Scope creep beyond what DOCV3-02 asks for; adds parsing surface (reading `EncryptionInfo`'s versioned binary header) with no client-actionable benefit — the remediation ("remove the password" / "convert the format") is identical regardless of which encryption method was used | Two flat messages, exactly as scoped; leave method-level detail out |

---

## Open Questions — Recommended Defaults

### Cluster 1: MCP

**Q: Progress notifications for long-running blocking tools?**
Recommended default: **Yes, but conditional and reused, never invented.** Send `notifications/progress` using the client-supplied `progressToken` only if the incoming `tools/call` request included one (per spec, this is optional — many hosts won't send it). Do not build a bespoke interim-message channel. If no `progressToken` is present, `convert_file` blocks silently until terminal status or timeout — this is spec-compliant and matches how mature MCP servers handle the optional case.

**Q: Tool result shape for a produced file — path, base64, or both?**
Recommended default: **`resource_link` (presigned URL) + structured JSON (`job_id`, `status`, `download_url`), never base64 by default.** Base64-inflating a real document/image result would poison the agent's context window (a 1 MB file ≈ 1.5M tokens per community/spec discussion). `download_result` performs the actual byte transfer only when explicitly invoked, and should write to a caller-specified local path, returning that path as text — not inline bytes. Reserve inline base64 for a possible future small-file fast-path (e.g., under a few KB), out of scope for v1.5.

**Q: Error mapping — API 4xx → MCP tool error vs `isError` result?**
Recommended default: **`isError: true` tool-execution result for all API business-logic 4xx (400/413/422/429).** These are legitimate outcomes the model should reason about (e.g., adjust `target_format`, remove password, retry after rate limit). Reserve JSON-RPC protocol-level errors for MCP-layer failures the model can't reason its way out of: malformed tool-call arguments (schema validation failure) or the MCP server itself being unable to reach the API at all (connection refused/5xx from the API's own infra, not a client-content problem).

**Q: Tool naming/description conventions?**
Recommended default: **snake_case verb_noun names exactly as sketched in SEED-003** (`convert_file`, `get_job_status`, `download_result`, `list_supported_formats`, `list_presets`) — consistent with widely-deployed reference MCP servers. Descriptions must explicitly state param mutual-exclusivity (`target_format` XOR `preset`) and point the model at the companion discovery tools (e.g., `convert_file`'s description should say "call `list_supported_formats` first if unsure").

**Q: Should `list_supported_formats`/`list_presets` be tools or MCP resources?**
Recommended default: **Ship as tools in v1.5.** By the pure MCP mental model (side-effect-free, application-controlled, cacheable data) these are textbook Resources. However: (a) the model actively needs this data as an input to a decision it's about to make (which `convert_file` call to construct) — a genuinely model-driven need, not just host-attached context; (b) resource subscription/attachment support varies more across MCP hosts than tool-calling does, and this milestone's target consumer (developer-facing coding-agent sessions) reliably supports tool calls; (c) low cardinality, single stdio client, no meaningful caching win to justify the extra resource-template/subscription plumbing right now. Flag this as a candidate to *additionally* expose as resources later, once resource support is verified against the actual target host(s).

**Q: `convert_file` param design — `target_format` XOR `preset`?**
Recommended default: **Mirror the API exactly** — two optional string params, description states XOR explicitly, and the MCP layer does not duplicate the API's validation beyond a cheap early client-side rejection for a friendlier message. The API's existing D-01 mutual-exclusivity gate remains the single source of truth; never let the MCP layer silently merge/prioritize if both happen to be set.

### Cluster 2: Presets REST

**Q: Endpoint shapes?**
Recommended default: `POST /v1/presets` (create), `GET /v1/presets` (list own, active-only), `GET /v1/presets/{name}` (show), `PUT /v1/presets/{name}` (update, bump-on-update), `DELETE /v1/presets/{name}` (maps to `Repo.Deactivate`, soft not hard). This exactly mirrors the five CLI verbs (`create/update/list/show/deactivate`) onto REST idioms.

**Q: Client-scope only — system presets via REST an anti-feature?**
Recommended default: **Confirmed anti-feature, hard rule.** No request field can ever select `scope=system`; the REST layer derives scope from `auth.ClientFromContext` exactly like `handleCreateJob`'s ownership checks. System presets remain exclusively the operator CLI's responsibility (requires direct DB access, not an API key).

**Q: Version semantics exposure — active only, or include history?**
Recommended default: **Active only by default**, matching the CLI's `includeInactive=false` default. Do not add a version-history query param in this milestone; it's low-value for a self-service surface and can be added later without breaking the default contract if a real need appears.

**Q: How does update work — PUT bump-on-update mirroring CLI?**
Recommended default: **Yes, exact mirror.** `PUT /v1/presets/{name}` calls `Repo.Update`, which deactivates the current active row and inserts version+1 — full-replacement semantics (client must resend `target_format`+`opts`+`description` together), not a partial PATCH.

**Q: Idempotency?**
Recommended default: **No idempotency-key mechanism.** `POST` create returns `409 Conflict` when an active preset of the same name already exists (mapped from the same repo condition the CLI already checks) — sufficient for a low-frequency, human/tool-paced self-service surface. Do not build an idempotency-key ledger.

**Q: Rate limit interplay?**
Recommended default: **No new limiter.** `/v1/presets/*` inherits the existing single per-client `ratelimit.PerClient` quota automatically by virtue of chi route grouping in `routes.go` — no separate preset-specific quota tier.

### Cluster 3: veraPDF

**Q: Validate ALWAYS for PDF/A exports, or opt-in flag?**
Recommended default: **Always, no opt-in.** Matches the existing precedent that the (weaker) OutputIntent check was already unconditional; an opt-in flag would silently preserve today's gap for any client that doesn't discover/set it, defeating DOCV3-01's purpose.

**Q: What to do on validation failure — terminal job failure vs warning metadata?**
Recommended default: **Terminal job failure** (`status=failed`, `error_code="pdfa_validation_failed"`, bounded/truncated `error_message`). Warning-only would mean a client explicitly requesting PDF/A output could receive a mislabeled non-compliant file — directly undermining the milestone's stated purpose and the project's Core Value (safe, reliable conversion).

**Q: Which profile?**
Recommended default: **2b**, matching the current PDF/A-2b export configuration (v1.3 Phase 14's `pdf_profile` opt). Do not validate against a stricter or different profile than what's actually produced.

**Q: Performance budget per job?**
Recommended default: **A dedicated, bounded timeout** (separate config, e.g. `VERAPDF_TIMEOUT`, order of magnitude similar to existing engine timeouts — tens of seconds), enforced via the existing hardened `runCommand` process-group-kill pattern. Classify a veraPDF timeout as terminal (an incomplete validation cannot be trusted to certify compliance), exactly like `DOCUMENT_ENGINE_TIMEOUT`'s existing terminal classification for the document engine. Note: veraPDF is JVM-based — expect nontrivial fixed startup cost (JVM boot) on top of per-page validation time; this pushes toward invoking it as a CLI subprocess bundled into the document-worker image (same isolation pattern as LibreOffice/Chromium) rather than a lighter embedded approach, since no pure-Go veraPDF equivalent exists.

### Cluster 4: CFB Distinction

**Q: Exact stream names distinguishing encrypted vs legacy?**
Recommended default: Encrypted → presence of `EncryptedPackage` and/or `EncryptionInfo` root-level streams (covers both Standard and Agile encryption methods). Legacy binary Office → presence of any of `WordDocument` (.doc), `Workbook` or `Book` (.xls), `PowerPoint Document` (.ppt) root-level streams. Any other CFB content (neither list matches) falls through to the current generic 422, fail-closed.

**Q: The two 422 messages' content?**
Recommended default:
- Encrypted: `"password-protected Office document is not supported; remove the password and resubmit"`
- Legacy: `"legacy binary Office format (.doc/.xls/.ppt) is not supported; convert to docx/xlsx/pptx"`

Both logged with a distinct `reason=` tag (`encrypted_document` / `legacy_document`), mirroring the existing D-08 logging discipline. No new structured `error_code` field — stay consistent with every other synchronous content-validation 422 in `handleCreateJob`, none of which carry one today.

**Q: Does the existing single-422 text need a compat note for existing clients?**
Recommended default: **Yes, note it, but treat as non-breaking.** The current text was never a documented stable contract (it's a free-text `message` field, not a parsed `error_code`), and the project has no precedent of clients pattern-matching on 422 message text. Still, call out the wording change explicitly in the v1.5 release notes/CHANGELOG, since `olecfb.go`'s own comments pre-announce DOCV3-02 as a planned split — an easy, low-cost courtesy to any client that may have started matching on the old string.

---

## Feature Dependencies

```
[MCP list_presets tool]
    └──requires──> [presets.Repo.List merged across BOTH scopes]
                       (existing Repo.List is scope-specific; a client-facing
                       list needs to reproduce Resolve's shadow-precedence —
                       system presets usable by any client, shadowed by a
                       same-named client preset — which today requires TWO
                       repo.List calls merged in Go, or a new repo method)

[MCP convert_file preset=] ──reuses──> [existing API D-01 XOR gate + Resolve]
    (no new server-side preset logic; MCP is a thin client of what already
    ships in v1.4)

[Presets REST CRUD] ──independent-of──> [MCP server]
    (self-service preset management via REST does not require the MCP
    server to exist, and vice versa — a human/tool can create a preset via
    REST that an agent later references via MCP's list_presets/convert_file,
    but neither cluster blocks the other)

[veraPDF validation] ──gates──> [existing PDF/A-2b export path]
    (post-processing step inside the document-worker's existing conversion
    flow, not a new engine-class/queue — depends on nothing from Clusters
    1/2/4)

[CFB distinction] ──replaces──> [existing single-422 IsOLECFB branch]
    (pure refinement of existing v1.3 code in internal/convert/olecfb.go +
    internal/api/handlers.go — depends on nothing from Clusters 1/2/3)
```

### Dependency Notes

- **MCP `list_presets` requires scope-merged listing:** `presets.Repo.List(scope, clientID, includeInactive)` is scope-specific (system XOR user). A client-facing "what presets can I use" view must reproduce `Resolve`'s shadow-precedence semantics (client preset of the same name hides the system one). Plan for either two `List` calls merged in the MCP-server (or API) layer, or a new repo method — do not ship a `list_presets` that silently omits system presets or double-lists shadowed names.
- **Presets REST and MCP are independent but complementary:** a human or automation could provision a client preset via `POST /v1/presets`, and an agent could later discover and use it via MCP's `list_presets`/`convert_file(preset=...)`. Neither cluster blocks the other's phase ordering.
- **veraPDF and CFB clusters are both pure document-class deepenings** with zero coupling to MCP or presets REST — they can be sequenced in either order relative to Clusters 1/2, and even relative to each other.

---

## MVP Definition

### Launch With (v1.5)

- [ ] `convert_file` (blocking, target_format XOR preset), `get_job_status`, `download_result`, `list_supported_formats`, `list_presets` — the five tools from SEED-003, stdio transport, go-sdk
- [ ] `resource_link`/presigned-URL result shape (no default base64 embedding)
- [ ] `POST/GET/PUT/DELETE /v1/presets` (+ `/{name}` variants), client-scope only, active-only listing, bump-on-update
- [ ] veraPDF PDF/A-2b validation, always-on, terminal failure on non-conformance
- [ ] CFB directory parse distinguishing encrypted vs legacy, two distinct 422s

### Add After Validation (v1.x)

- [ ] Progress-notification wiring for `convert_file` if agent hosts in practice send `progressToken` (validate need before building further polish)
- [ ] `?all=true` version history on `GET /v1/presets` if a real audit/debugging need appears
- [ ] Small-file inline-base64 fast path for MCP results, if agents in practice want inline content for tiny files

### Future Consideration (v2+)

- [ ] Exposing `list_supported_formats`/`list_presets` as MCP Resources in addition to tools, once resource support is verified against the actual target host(s)
- [ ] Streamable-HTTP MCP transport, if an internal shared/remote MCP-server catalog materializes
- [ ] Structured `error_code` taxonomy for synchronous 422 validation paths (a larger, cross-cutting API decision — not scoped to CFB alone)

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| `convert_file` + 4 companion MCP tools | HIGH | MEDIUM | P1 |
| Presets REST CRUD (client-scope) | HIGH | LOW-MEDIUM | P1 |
| veraPDF ISO 19005 validation | MEDIUM-HIGH | MEDIUM-HIGH | P1 |
| CFB encrypted-vs-legacy distinction | MEDIUM | MEDIUM | P1 |
| Progress notifications for MCP | LOW-MEDIUM | LOW-MEDIUM | P2 |
| Preset version history over REST | LOW | LOW | P3 |
| MCP resources (in addition to tools) | LOW | MEDIUM | P3 |

**Priority key:**
- P1: Must have for v1.5 milestone completion
- P2: Should have, add when possible within v1.5 if time allows
- P3: Nice to have, future consideration beyond v1.5

## Sources

- [Tools — Model Context Protocol specification (2025-06-18)](https://modelcontextprotocol.io/specification/2025-06-18/server/tools) — tool result content types, resource_link, isError vs protocol errors, output schema (HIGH confidence, official spec)
- [Architecture overview — Model Context Protocol](https://modelcontextprotocol.io/docs/learn/architecture) — progress notification / progressToken mechanics (MEDIUM confidence, official docs)
- [modelcontextprotocol/go-sdk (GitHub)](https://github.com/modelcontextprotocol/go-sdk) and [pkg.go.dev mcp package](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp) — official Go SDK API shape, `AddTool`/`StdioTransport` (HIGH confidence, official repo/docs)
- [Keep file bytes out of your AI agent's context window — Arcade.dev](https://www.arcade.dev/blog/model-never-needs-to-see-the-file/) and [MCP discussion #794/#793 on image content URL support](https://github.com/modelcontextprotocol/modelcontextprotocol/discussions/794) — base64 context-window cost, resource_link rationale (MEDIUM confidence, community consensus + spec discussion)
- [MCP Resources vs Tools — multiple sources cross-checked](https://zuplo.com/blog/mcp-resources), [dev.to pattern guide](https://dev.to/webbywisp/mcp-server-patterns-tools-vs-resources-vs-prompts-when-to-use-each-5bgp) — model-controlled vs application-controlled distinction (MEDIUM confidence, multiple independent sources agree)
- [veraPDF CLI docs — validation](https://docs.verapdf.org/cli/validation/) and [veraPDF CLI quick start](https://docs.verapdf.org/cli/) — `-f 2b` flavour flag, CLI invocation shape (HIGH confidence, official docs)
- [verapdf/cli Docker Hub image](https://hub.docker.com/r/verapdf/cli) — Docker/JVM packaging confirmation (MEDIUM confidence, official image, no direct performance benchmark found)
- [Encrypted OOXML Documents — Didier Stevens](https://blog.didierstevens.com/2018/06/07/encrypted-ooxml-documents/) — EncryptedPackage/EncryptionInfo stream analysis (MEDIUM confidence, well-known independent security researcher, corroborated by Apache POI's own encryption code and oletools' crypto.py)
- [Apache POI — Encryption support](https://poi.apache.org/encryption.html) — corroborates EncryptedPackage/EncryptionInfo as the standard detection streams (MEDIUM confidence, official project docs for a mature OOXML library)
- Internal code read directly: `internal/api/handlers.go`, `internal/convert/olecfb.go`, `internal/presets/repo.go`, `cmd/manage-presets/main.go`, `internal/api/routes.go`, `internal/convert/convert.go`, `internal/ratelimit/*_test.go`, `.planning/seeds/SEED-003.md`, `.planning/PROJECT.md` — grounds all "mirrors existing X" claims in actual shipped code, not assumption

---
*Feature research for: OctoConv v1.5 (MCP Access & Document Fidelity)*
*Researched: 2026-07-13*
