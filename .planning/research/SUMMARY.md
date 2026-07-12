# Project Research Summary

**Project:** OctoConv — v1.5 "MCP Access & Document Fidelity"
**Domain:** Internal async file-conversion service — adding an MCP agent-access surface, self-service preset management REST, ISO 19005 (PDF/A) conformance validation, and OLE-CFB structural error taxonomy
**Researched:** 2026-07-13
**Confidence:** MEDIUM-HIGH

## Executive Summary

v1.5 is four largely-independent capability additions layered onto a mature, hardened v1.0–v1.4 core: (1) a stdio MCP server (`cmd/mcp-server`) that lets agents call `convert_file`/`get_job_status`/`download_result`/`list_supported_formats`/`list_presets` as a thin, zero-privilege HTTP client of the existing public API; (2) self-service `POST/GET/PUT/DELETE /v1/presets` REST CRUD, client-scope only, reusing the already-complete `internal/presets.Repo` built in Phase 18; (3) replacing the current PDF/A `OutputIntent` heuristic with real ISO 19005 conformance validation via the veraPDF CLI (the industry-reference validator — no credible pure-Go or dependency-free alternative exists); and (4) parsing the OLE-CFB directory to split today's single generic 422 into distinct "password-protected" vs "legacy binary format" errors. All four research streams agree the codebase's established conventions (fail-closed-by-default, hardened `os/exec`, interface-segregated repos, ownership-derived-from-auth-context, zero-new-deps bias, one-file-per-engine) extend cleanly to every one of these additions — this is evolution, not architectural rework.

The recommended build order is dependency-justified, not arbitrary: Presets REST CRUD (plus a newly-identified, previously-uncalled-out `GET /v1/formats` endpoint) is a **hard prerequisite** for two of MCP's five tools (`list_presets`, `list_supported_formats`), because the MCP server is designed to hold zero `internal/*` package imports beyond its own HTTP client — it can only discover presets/formats over REST, not by importing `internal/presets`/`internal/convert` directly. veraPDF validation and CFB classification are both pure document-class deepenings, coupled to neither MCP/presets nor each other, and can be sequenced independently (research recommends CFB before veraPDF: smaller, better-precedented, existing fixtures, versus veraPDF's higher uncertainty — new JVM runtime, unverified image/latency impact, unverified CLI report format).

The dominant risk cluster is **security/DoS discipline on two genuinely new trust boundaries**: (a) the MCP server is a new agent-facing surface holding a real API key and accepting agent-supplied filesystem paths — it must not leak the key into tool error text, must not blindly trust agent-supplied paths (traversal risk), must never dump raw file bytes into tool results (context-window poisoning), and must keep `stdout` reserved exclusively for JSON-RPC framing; and (b) CFB directory parsing reopens a well-documented DoS class (crafted directory-chain cycles causing infinite loops — see `openmcdf` GHSA-jxpf-xq2m-q525, multiple historical Apache POI CVEs) that the project's existing 8-byte-magic-only check was deliberately designed to avoid — any new parser (see Key Decision below) must be bounded, cycle-detected, and fuzz-tested before shipping. A secondary but real risk is veraPDF's JVM-per-invocation cost silently regressing job latency and the CI e2e time budget, and its stricter validation potentially turning previously-"good enough" LibreOffice PDF/A output into new terminal failures without a documented severity policy.

## Key Findings

### Recommended Stack

Core net-new stack for v1.5: the official `modelcontextprotocol/go-sdk` v1.6.1 for the MCP server (stdio transport only; pin to v1.6.1, not v1.5.x, for a fixed keepalive race condition, and not v1.7.0-pre.x, which targets an unfinalized protocol revision), the `verapdf/cli:1.30.2` Docker image bundled into `Dockerfile.document-worker` via multi-stage `COPY` (no Go module — invoked as an external CLI through the existing hardened `exec.go` runner, exactly like `soffice`/`vips`), and **zero new dependencies** for presets REST (pure application-layer addition on top of already-shipped `internal/presets`, chi, pgx).

**Core technologies:**
- `github.com/modelcontextprotocol/go-sdk` v1.6.1 — official Go MCP SDK, stdio transport, reflection-based typed-tool schemas matching this codebase's existing struct-tag conventions; requires Go 1.25+ (compatible with 1.26.4)
- `verapdf/cli:1.30.2` (Docker, not a Go module) — the industry-reference open-source ISO 19005 validator; no credible pure-Go alternative found after a targeted search; integrates via the existing `os/exec` hardened-runner pattern
- Existing chi + pgx (no version change) — presets REST CRUD needs no new library surface; `internal/presets.Repo` (Phase 18) already owns the SQL layer

**See Key Decision (CFB parsing) below** — this is the one point where STACK.md and ARCHITECTURE.md diverged and required an explicit resolution.

### Expected Features

**Must have (table stakes):**
- MCP: `convert_file` (blocking, target_format XOR preset), `get_job_status`, `download_result`, `list_supported_formats`, `list_presets`, stdio transport, single API key via env, `resource_link`/presigned-URL results (never base64-embedded bytes), tool errors surfaced as `isError: true` content (not protocol errors)
- Presets REST: full CRUD mirroring the 5 CLI verbs (`POST/GET/GET-by-name/PUT/DELETE`), client-scope only, active-only listing, bump-on-update, inherits existing per-client rate limiting automatically via chi route grouping
- veraPDF: always-on validation for PDF/A-2b exports (no opt-in flag — an opt-in would silently preserve today's gap), validation failure = terminal job failure, bounded timeout via existing hardened exec pattern
- CFB: distinct 422 for encrypted (`EncryptionInfo`/`EncryptedPackage` streams) vs legacy binary (`WordDocument`/`Workbook`|`Book`/`PowerPoint Document` streams), fail-closed fallthrough to today's generic 422 for anything unrecognized — never a path that proceeds to conversion

**Should have (differentiators):**
- MCP progress notifications reusing the client's `progressToken` (best-effort, spec-optional)
- Presets REST: `409 Conflict` on create-when-active-exists (vs generic 400/500), response echoes new version number
- CFB: distinct logged `reason=` tags per classification for operator diagnosability

**Defer (v2+):**
- MCP resources (in addition to tools) for `list_presets`/`list_supported_formats`
- Streamable-HTTP MCP transport
- `?all=true` preset version history over REST
- Small-file inline-base64 fast path for MCP results
- Structured `error_code` taxonomy for synchronous 422s generally (not just CFB)

### Architecture Approach

Every new component is additive and follows an already-established shape: `cmd/mcp-server` is a thin `cmd/`-triggers-`internal/`-holds-logic binary exactly like `cmd/api`/`cmd/worker`, and `internal/mcpserver` holds **zero** privileged access (no DB/S3/Redis) — it is a pure HTTP client of the public API, identical in trust level to `internal/e2e`'s test helpers. Presets REST adds a second, narrower `PresetAdmin` interface alongside the existing `PresetRepo` (interface segregation, not widening) backed by the same concrete `*presets.Repo`. veraPDF is invoked in-process inside the existing document-worker's PDF/A path via the same hardened `runCommand` used for `soffice`, with its own file (`internal/convert/verapdf.go`) mirroring the one-engine-per-file convention. CFB gets a second-stage structural parse (`ClassifyCFB`) gated behind the existing cheap 8-byte `IsOLECFB` magic check, mirroring the project's existing `Sniff` → `SniffContainer` two-stage pattern.

**Major components:**
1. `cmd/mcp-server` + `internal/mcpserver` — stdio MCP tool handlers + minimal hand-rolled HTTP client (multipart upload, poll, presigned download); no shared code with `internal/e2e` (test-only, structurally unimportable)
2. `internal/api/presets_handlers.go` + `formats_handlers.go` — new REST surfaces mounted inside the existing authenticated `/v1` group, delegating to unmodified `internal/presets.Repo` and a read-only `convert.Default` registry walk
3. `internal/convert/verapdf.go` — hardened `os/exec` wrapper around the veraPDF CLI, called from `validateDocumentOutput`'s existing PDF/A branch, its error text feeding a new `terminalVeraPDFSignatures` slice in `worker.go` (same D-04 same-commit discipline as the existing LibreOffice signature list)
4. `internal/convert/olecfb.go`'s new `ClassifyCFB` — bounded, directory-only CFB structural parse producing `{encrypted|legacy|unknown}`, consumed by a 3-way split of `handleCreateJob`'s existing single OLE-CFB 422 branch

**Build order / dependency graph (from ARCHITECTURE.md, confirmed against FEATURES.md's independent cross-check):**
- Presets REST + `GET /v1/formats` → **hard prerequisite** for MCP's `list_presets`/`list_supported_formats` tools (MCP has no `internal/convert`/`internal/presets` imports)
- MCP server → depends on the above; `convert_file`/`get_job_status`/`download_result` alone have no such dependency, but shipping the full 5-tool set in one phase avoids a half-shipped surface
- CFB classification → fully independent; touches only `internal/convert/olecfb.go` + `internal/api/handlers.go`'s existing branch; well-precedented, existing fixtures from Phase 13
- veraPDF validation → fully independent; highest uncertainty (new JVM runtime, unverified image/latency impact, unverified CLI report format) — sequence last so any schedule slip doesn't block the other three
- Presets/MCP (files under `internal/api`, `internal/presets`, `cmd/mcp-server`) and CFB/veraPDF (files under `internal/convert`, `internal/worker`, `Dockerfile.document-worker`) touch **entirely disjoint files** and can run as two parallel phase tracks if resourcing allows

### Critical Pitfalls

1. **`convert_file` blocks past the MCP client's idle-notification window** — document/chromium-class jobs can run minutes; MCP clients enforce an idle window (not a fixed wall-clock timeout — ~30min stdio vs ~5min HTTP). Every poll iteration must call `NotifyProgress` if a `progressToken` was supplied, and the poll loop needs its own independent max-duration guard (never block forever on a stuck job) — this is the single most likely "looks done but isn't" trap (demo image jobs finish before the failure path is ever exercised).
2. **API key leaks into MCP tool results or error text** — the MCP server holds a real client API key; naive error wrapping (`fmt.Errorf("request failed: %w", err)` on an `*url.Error`) can leak the `Authorization` header into LLM-visible tool output/logs/transcripts. Construct the outgoing request once inside a client wrapper that never logs the raw header; map every upstream failure to a fixed message, exactly like `internal/api/handlers.go`'s existing no-leak discipline.
3. **Agent-supplied paths enable traversal / arbitrary file read-write** — `convert_file(path)`/`download_result` accept paths from LLM reasoning, not a trusted human CLI arg (and can be attacker-influenced via prompt injection in processed file content). Require `filepath.Abs` + strict-descendant-of-configured-root containment, reject symlinks, generate `download_result`'s destination path server-side rather than trusting an agent-supplied one.
4. **Mass-assignment on presets REST lets a client set `scope=system` or spoof `client_id`** — reusing `presets.CreateParams` (which has `Scope`/`ClientID` fields, correct for the trusted operator CLI) directly as the REST JSON DTO lets a client body-inject elevated scope or cross-client writes. Use a narrower REST-only request DTO with no scope/ownership fields at all; derive both exclusively from `auth.ClientFromContext`. Flagged as the single highest-severity item in the presets phase.
5. **Full/naive CFB parsing reopens a documented directory-cycle DoS class** — `openmcdf` GHSA-jxpf-xq2m-q525 (crafted CFB directory cycle → infinite loop) plus historical Apache POI CVEs are direct precedent. Any new parser must cap sectors/entries walked, bounds-check every length/offset read against actual buffer size, track visited-sector state to reject cycles immediately, and ship with fuzz coverage (`go test -fuzz`) as a phase-exit gate, not optional polish.
6. **veraPDF's JVM-per-invocation cost silently regresses job latency and the CI e2e budget** — the existing v1.3 decision explicitly chose the cheap OutputIntent check specifically to avoid this cost; veraPDF's own official `verapdf/rest` daemon-mode image exists precisely to amortize JVM startup across requests, which is external validation this is a known real problem, not a hypothetical one.

## Key Decision (flagged for phase planning): CFB directory parsing — hand-rolled `ClassifyCFB`, not `richardlehane/mscfb`

STACK.md recommended the maintained `richardlehane/mscfb` library (credible single-maintainer author, Apache-2.0, small transitive footprint) specifically because this is a fail-closed security classification and FAT/DIFAT sector-chain walking is a known-tricky binary format. ARCHITECTURE.md recommended a hand-rolled `ClassifyCFB` on zero-new-deps grounds, since only stream **names** from the directory are needed (no mini-FAT stream reassembly). PITFALLS.md tips the balance decisively: it documents the `openmcdf` directory-cycle DoS advisory (GHSA-jxpf-xq2m-q525) as live proof that even mainstream, actively-maintained CFB libraries carry this exact bug class, and its own Technical Debt Patterns table states explicitly — "Adopting a general third-party CFB library instead of hand-rolling the bounded directory reader ... Never, given the project's explicit precedent (Phase 4/7) of hand-rolling parsers for this exact reason."

**Recommendation: hand-rolled `ClassifyCFB`, zero new dependency.** Rationale:
- The scope needed is genuinely narrow — header + FAT/DIFAT sector-chain walk to enumerate root-level directory-entry names only, never touching mini-FAT or stream content — a well-bounded, independently-fuzzable target, unlike full CFB parsing.
- This matches the project's own repeatedly-honored precedent (Phase 4 magic-bytes-not-shell-out, Phase 7 own-parser-not-`golang.org/x/image` for decompression-bomb protection) — a maintained library does not automatically inherit "safer" status for this bug class; mscfb has not been independently fuzzed against OctoConv's specific cycle/bounds requirements, and adopting it would still require the same verification work (fuzzing, bounds-checking assumptions) that hand-rolling requires anyway, while additionally taking on a larger, more general-purpose parsing surface than needed.
- The correctness risk STACK.md raises (subtly-wrong sector-chain walk) is real but is exactly what a mandatory fuzz-testing phase-exit gate (Pitfall 10) is designed to catch — treat fuzzing as non-negotiable, not optional polish, and this closes most of the residual risk gap between "hand-rolled" and "vetted library."

**Action for phase planning:** record this explicitly as a `PROJECT.md` Key Decision (per Pitfall 12's "new-dependency review discipline" — the CFB decision is really a "why we did NOT add a dependency" entry) with the hand-rolled implementation requiring: (1) a maximum sector/entry walk bound derived from and cross-checked against the actual reader length, (2) a visited-sector set for immediate cycle rejection, (3) a fuzz target seeded with the existing Phase 13 fixtures (`legacy.doc`, `encrypted.docx`) plus deliberately corrupted variants (truncated header, self-referential sector index, oversized declared count), run as a phase-exit gate before merge.

## Implications for Roadmap

Based on research, suggested phase structure:

### Phase 1: Presets REST CRUD + `/v1/formats`
**Rationale:** Fully independent of the other three clusters; touches only `internal/api` + the already-complete `internal/presets.Repo`. Hard prerequisite for MCP's `list_presets`/`list_supported_formats` tools — MCP is designed with zero `internal/convert`/`internal/presets` imports, so it cannot discover presets/formats before these REST endpoints exist. `/v1/formats` is a newly-identified small gap (no format-discovery endpoint exists on `main` today) that should be bundled here rather than treated as a separate cluster.
**Delivers:** `POST/GET/PUT/DELETE /v1/presets[/{name}]` (client-scope, active-only, bump-on-update), `GET /v1/formats` (read-only registry walk)
**Addresses:** PRST-V2-01 table stakes; sets up MCP's discovery tools
**Avoids:** Mass-assignment (Pitfall 4/6) via a narrow REST DTO deriving scope/client_id only from `auth.ClientFromContext`; IDOR (Pitfall 7) via consistent ownership-scoped repo calls

### Phase 2: MCP Server (`cmd/mcp-server` + `internal/mcpserver`)
**Rationale:** Depends on Phase 1's endpoints for 2 of 5 tools; shipping the full tool set in one phase avoids a half-built MCP surface. This is explicitly "new territory" per PROJECT.md — treat as the phase most likely to need mid-planning research.
**Delivers:** `convert_file` (blocking, preset-aware), `get_job_status`, `download_result`, `list_supported_formats`, `list_presets`, stdio transport via official `go-sdk` v1.6.1
**Uses:** `modelcontextprotocol/go-sdk` v1.6.1, existing `PresignGet`/presigned-URL mechanism
**Implements:** Thin external-client `cmd/` pattern (Architecture Pattern 1) — zero privileged internal access, pure HTTP client of the public API
**Must also address:** idle-window/progress-notification design (Pitfall 1), API-key leak prevention (Pitfall 2), path-traversal containment (Pitfall 3), no-inline-bytes result shape (Pitfall 4), stdout-purity discipline (Pitfall 5) — all flagged as MCP-phase design deliverables, not afterthoughts

### Phase 3: CFB Encrypted-vs-Legacy Classification
**Rationale:** Independent of Phases 1–2 and of Phase 4; smaller and better-precedented than veraPDF (mirrors the existing `SniffContainer` structural-parse pattern, fixtures already exist from Phase 13). Recommended before veraPDF to de-risk the lower-uncertainty item first.
**Delivers:** `ClassifyCFB` (hand-rolled, bounded, cycle-detected, fuzz-tested — see Key Decision above), 3-way split of `handleCreateJob`'s existing OLE-CFB 422 branch
**Addresses:** DOCV3-02 table stakes
**Avoids:** Directory-cycle DoS (Pitfall 5/10) via mandatory fuzz gate; fail-closed weakening (Pitfall 11) via an explicit 3-outcome model where "unrecognized" always rejects, never proceeds to conversion

### Phase 4: veraPDF ISO 19005 Validation
**Rationale:** Independent of Phases 1–3; highest uncertainty in the milestone (new JVM runtime dependency, unverified image-size/build-time/latency impact, unverified exact CLI report format for terminal-error classification). Sequencing it last means schedule slip here doesn't block the other three independent clusters.
**Delivers:** Always-on PDF/A-2b validation replacing the OutputIntent heuristic, terminal failure on non-conformance, bounded timeout via existing hardened exec
**Uses:** `verapdf/cli:1.30.2` bundled into `Dockerfile.document-worker`
**Implements:** Hardened external-process validation pattern (Architecture Pattern 3), reusing `exec.go`'s `runCommand`
**Must also address:** JVM-per-invocation latency budget vs CI e2e cap — Architecture recommends starting with a bundled CLI (simpler, matches every other engine integration) while Pitfalls leans more cautiously toward measuring first and considering a daemon/server-mode container if cold-start proves prohibitive (Pitfall 8); this tradeoff should be an explicit, measured decision made during this phase, not assumed either way. Also: severity policy for veraPDF verdicts (Warning vs Error rule violations, Pitfall 9) must be decided and re-validated against existing v1.3 PDF/A fixtures before merge.

### Phase Ordering Rationale

- Presets REST is sequenced first purely because of the hard MCP dependency identified in ARCHITECTURE.md (not previously called out as a separate milestone cluster: the `/v1/formats` gap)
- CFB before veraPDF: both are independent document-track deepenings with zero coupling to each other or to MCP/presets, but CFB is smaller, better-precedented, and lower-risk — sequencing it first de-risks the document track before absorbing veraPDF's JVM-integration uncertainty
- Presets+MCP and CFB+veraPDF touch entirely disjoint files (`internal/api`/`internal/presets`/`cmd/mcp-server` vs `internal/convert`/`internal/worker`/`Dockerfile.document-worker`) and could run as two parallel phase tracks if resourcing allows, with only conceptual (not code-level) coupling between them

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 2 (MCP server):** New territory per PROJECT.md itself. Re-verify the exact `go-sdk` tool-registration API surface against the pinned version at execution time (SDK is actively evolving); verify progress-notification/keepalive mechanics live rather than trusting docs alone; verify idle-window behavior empirically against the actual target MCP host(s).
- **Phase 4 (veraPDF):** Highest-uncertainty item in the milestone. Needs live measurement of JVM cold-start cost against `DOCUMENT_ENGINE_TIMEOUT` and the CI e2e 25-minute cap; needs live verification of veraPDF's exact non-conformance report format/exit codes before hardcoding `terminalVeraPDFSignatures`; needs the CLI-vs-daemon architecture decision resolved with data, not assumption; needs a documented severity-policy decision (Pitfall 9) verified against existing v1.3 fixtures.

Phases with standard patterns (well-documented, can likely skip a dedicated research-phase step):
- **Phase 1 (Presets REST):** Directly extends already-complete, already-verified `internal/presets.Repo` from Phase 18; interface-segregation and ownership-derivation patterns are already established and proven elsewhere in the codebase (jobs 404-not-403 convention).
- **Phase 3 (CFB classification):** Mirrors the existing, already-shipped `Sniff`/`SniffContainer` two-stage pattern; the main non-standard element (fuzz-testing gate) is well-specified in PITFALLS.md and doesn't require external research so much as disciplined execution.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH for MCP SDK and mscfb (verified via GitHub/pkg.go.dev API metadata); MEDIUM for veraPDF operational specifics (official docs/Docker Hub confirmed, but no first-party JVM cold-start benchmark found — only an unverified vendor blog claim) |
| Features | MEDIUM overall — HIGH for MCP protocol mechanics and CFB stream names (official spec/well-corroborated security research); MEDIUM for veraPDF operational behavior and presets REST (the latter derived from existing verified internal code, not external research, which is actually a strength here) |
| Architecture | HIGH for patterns directly extending existing, live-verified code (presets REST, CFB two-stage classification); MEDIUM for MCP SDK specifics and veraPDF CLI packaging (verified via WebSearch against official sources, not yet live-tested in this repo) |
| Pitfalls | MEDIUM-HIGH — MCP timeout/stdio behavior cross-referenced against multiple independent GitHub issues plus official docs; veraPDF server-mode rationale and CFB directory-cycle DoS class verified via official security advisories/project architecture; presets/auth pitfalls derived directly from this codebase's own established conventions (HIGH there) |

**Overall confidence:** MEDIUM-HIGH

### Gaps to Address

- **veraPDF CLI-per-job vs daemon/server-mode:** Architecture and Pitfalls research land on different defaults (bundle-and-revisit-if-needed vs measure-first-and-lean-toward-daemon). Resolve with a live JVM cold-start measurement during Phase 4 planning/execution before committing to either shape.
- **veraPDF non-conformance report format/exit codes:** Not verified against a real invocation — `terminalVeraPDFSignatures` must not be hardcoded from training-data assumptions alone; confirm live during Phase 4.
- **veraPDF severity policy (Pitfall 9):** Whether all non-compliance is terminal-fail, or only Error-severity rule violations, is undecided — must be resolved and validated against existing v1.3 PDF/A-2b fixtures before Phase 4 ships, to avoid silently regressing previously-good conversions.
- **MCP `list_presets` scope-merging:** Needs to reproduce `Resolve`'s shadow-precedence (client preset hides same-named system preset) across two scope-specific `Repo.List` calls or a new repo method — not fully designed yet in any research file; a concrete decision (merge in API layer via a `?include_system=true` param, per ARCHITECTURE.md's suggestion) should be finalized during Phase 1 planning since it also determines what Phase 1's REST surface needs to expose.
- **MCP SDK tool-registration API surface:** Confirmed HIGH-confidence at the metadata level (official repo, active maintenance) but the exact `mcp.AddTool`/schema-generation call shape should be re-verified against whatever `go-sdk` version is actually pinned in `go.mod` at Phase 2 execution time, since the SDK is actively evolving (a `v1.7.0-pre.x` exists already).

## Sources

### Primary (HIGH confidence)
- https://github.com/modelcontextprotocol/go-sdk — official Go MCP SDK repo metadata (GitHub API)
- https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/go.mod — Go 1.25 floor, dependency list
- https://hub.docker.com/v2/repositories/verapdf/cli/tags — Docker Hub API, confirms `latest`=v1.30.2, image size
- https://github.com/veraPDF/veraPDF-apps/blob/integration/Dockerfile — official multi-stage build confirmation
- https://github.com/richardlehane/mscfb + pkg.go.dev — API shape, license, transitive deps, maintenance status
- [Tools — Model Context Protocol specification (2025-06-18)](https://modelcontextprotocol.io/specification/2025-06-18/server/tools) — tool result content types, `resource_link`, `isError` semantics
- [openmcdf GHSA-jxpf-xq2m-q525](https://github.com/openmcdf/openmcdf/security/advisories/GHSA-jxpf-xq2m-q525) — official security advisory, CFB directory-cycle DoS precedent
- [MCP official debugging docs](https://modelcontextprotocol.io/docs/tools/debugging) — stdout-corruption failure mode
- Direct repository reads: `internal/api/{routes.go,api.go,handlers.go}`, `internal/presets/repo.go`, `internal/convert/{olecfb.go,libreoffice.go,convert.go}`, `internal/worker/worker.go`, `internal/e2e/e2e_test.go`, `Dockerfile.document-worker`, `.planning/PROJECT.md`, `.planning/seeds/SEED-003.md`

### Secondary (MEDIUM confidence)
- [Encrypted OOXML Documents — Didier Stevens](https://blog.didierstevens.com/2018/06/07/encrypted-ooxml-documents/) + [Apache POI encryption docs](https://poi.apache.org/encryption.html) — corroborated CFB encrypted-stream detection
- [veraPDF/veraPDF-rest](https://github.com/veraPDF/veraPDF-rest) — official project's own daemon-mode architecture, external validation of JVM cold-start concern
- [veraPDF-library issue #1253](https://github.com/veraPDF/veraPDF-library/issues/1253) — parser-backend verdict divergence on edge cases
- Live GitHub issues on `anthropics/claude-code` (#17662, #22542, #47076, #65643, #44006) — MCP idle-window behavior cross-referenced

### Tertiary (LOW confidence)
- Vendor blog (ConvertAPI) mentioning veraPDF JVM cold-start friction — commercial interest in a paid alternative, not a measured number, flagged as unverified
- Single WebSearch result on go-sdk keepalive race-condition fix — not cross-checked against raw changelog diff

---
*Research completed: 2026-07-13*
*Ready for roadmap: yes*
