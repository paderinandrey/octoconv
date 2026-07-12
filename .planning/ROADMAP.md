# Roadmap: OctoConv

## Milestones

- ✅ **v1.0 Hardening MVP** — Phases 1-4 (shipped 2026-07-08) — see `.planning/milestones/v1.0-ROADMAP.md`
- ✅ **v1.1 Tech Debt Cleanup** — Phases 5-7 (shipped 2026-07-08) — see `.planning/milestones/v1.1-ROADMAP.md`
- ✅ **v1.2 Document Engine Class** — Phases 8-11 (shipped 2026-07-10) — see `.planning/milestones/v1.2-ROADMAP.md`
- ✅ **v1.3 Document Class v2** — Phases 12-16 (shipped 2026-07-12) — see `.planning/milestones/v1.3-ROADMAP.md`
- ✅ **v1.4 CI, Presets & Debt Cleanup** — Phases 17-19 (shipped 2026-07-13) — see `.planning/milestones/v1.4-ROADMAP.md`
- 🚧 **v1.5 MCP Access & Document Fidelity** — Phases 20-23 (in progress) — details below

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

<details>
<summary>✅ v1.0 Hardening MVP (Phases 1-4) — SHIPPED 2026-07-08</summary>

- [x] Phase 1: Merge, Auth & Rate Limiting (4/4 plans) — completed 2026-07-04
- [x] Phase 2: Webhook Delivery (3/3 plans) — completed 2026-07-04
- [x] Phase 3: Retry-Safety & Reconciler (3/3 plans) — completed 2026-07-06
- [x] Phase 4: Content Validation, Storage Lifecycle & Observability (5/5 plans) — completed 2026-07-07

Full details: `.planning/milestones/v1.0-ROADMAP.md`

</details>

<details>
<summary>✅ v1.1 Tech Debt Cleanup (Phases 5-7) — SHIPPED 2026-07-08</summary>

- [x] Phase 5: Webhook SSRF Private-IP Opt-Out (1/1 plans) — completed 2026-07-08
- [x] Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test (4/4 plans) — completed 2026-07-08
- [x] Phase 7: Image Dimension Limit (Decompression-Bomb Protection) (2/2 plans) — completed 2026-07-08

Full details: `.planning/milestones/v1.1-ROADMAP.md`

</details>

<details>
<summary>✅ v1.2 Document Engine Class (Phases 8-11) — SHIPPED 2026-07-10</summary>

- [x] Phase 8: Document Content Safety & Format Detection (2/2 plans) — completed 2026-07-09
- [x] Phase 9: LibreOffice Converter Engine (2/2 plans) — completed 2026-07-09
- [x] Phase 10: Document Worker & Reconciler Integration (4/4 plans) — completed 2026-07-09
- [x] Phase 11: API Routing & End-to-End Document Conversion (4/4 plans, incl. gap closure 11-04) — completed 2026-07-10

Full details: `.planning/milestones/v1.2-ROADMAP.md`

</details>

<details>
<summary>✅ v1.3 Document Class v2 (Phases 12-16) — SHIPPED 2026-07-12</summary>

- [x] Phase 12: Tech Debt Cleanup (1/1 plans) — completed 2026-07-10
- [x] Phase 13: Cross-Format Conversion & Input Safety (3/3 plans) — completed 2026-07-10
- [x] Phase 14: Validated Conversion Options & PDF/A Export (3/3 plans) — completed 2026-07-10
- [x] Phase 15: HTML→PDF Chromium Engine (5/5 plans) — completed 2026-07-11
- [x] Phase 16: Webhook Delivery Decoupling (5/5 plans, incl. gap closure 16-05) — completed 2026-07-12

Full details: `.planning/milestones/v1.3-ROADMAP.md`

</details>

<details>
<summary>✅ v1.4 CI, Presets & Debt Cleanup (Phases 17-19) — SHIPPED 2026-07-13</summary>

- [x] Phase 17: Tech Debt Cleanup (2/2 plans) — completed 2026-07-12
- [x] Phase 18: Presets (4/4 plans) — completed 2026-07-12
- [x] Phase 19: CI Pipeline (2/2 plans) — completed 2026-07-13

Full details: `.planning/milestones/v1.4-ROADMAP.md`

</details>

### 🚧 v1.5 MCP Access & Document Fidelity (Phases 20-23) — IN PROGRESS

Four largely-independent capability additions layered onto the hardened v1.0–v1.4 core: an
agent-facing stdio MCP server, self-service preset REST CRUD, real ISO 19005 PDF/A validation,
and an encrypted-vs-legacy CFB error taxonomy. Build order is dependency-justified — Presets REST
(with the new `GET /v1/formats`) is a hard prerequisite for two of MCP's five tools; CFB and
veraPDF are independent document-track deepenings, with veraPDF (highest uncertainty) sequenced last.

- [ ] **Phase 20: Presets REST CRUD & Format Discovery** — Clients self-service manage client-scope presets and discover supported formats over authenticated REST
- [ ] **Phase 21: MCP Server** — Agents convert files and discover capabilities through a stdio MCP server that is a zero-privilege HTTP client of the API
- [ ] **Phase 22: CFB Encrypted-vs-Legacy Classification** — OLE-CFB uploads get distinct, bounded 422s (password-protected vs legacy binary)
- [ ] **Phase 23: veraPDF ISO 19005 Validation** — PDF/A-2b outputs validated for real conformance; non-compliant exports fail terminally

## Phase Details

### Phase 20: Presets REST CRUD & Format Discovery
**Goal**: Clients can self-service manage their own presets and discover supported formats over authenticated REST — the discovery substrate MCP's list tools depend on.
**Depends on**: Nothing new (extends already-shipped `internal/presets.Repo` from Phase 18; first v1.5 phase)
**Requirements**: PRAPI-01, PRAPI-02, PRAPI-03
**Success Criteria** (what must be TRUE):
  1. A client can create / list / show / update / deactivate its own client-scope presets via `/v1/presets`, with scope and client_id derived solely from the auth context — a request body attempting `scope=system` or a foreign `client_id` is ignored (narrow DTO, no mass-assignment).
  2. REST behavior mirrors the CLI through the shared `internal/presets.Repo`: update bumps the version and echoes the new number, a duplicate active-create returns 409, and there is no hard delete (deactivate only).
  3. Requesting another client's or a non-existent preset returns the no-leak response (404-style, never 403, never revealing existence).
  4. `GET /v1/formats` returns the supported (source, target) format pairs and their engine classes from a read-only registry walk.
**Plans**: TBD

### Phase 21: MCP Server
**Goal**: Agents (e.g. a Claude Code session) can convert files and discover capabilities through a stdio MCP server that holds zero privileged access and is a pure HTTP client of the existing public API.
**Depends on**: Phase 20 (`list_presets` and `list_supported_formats` tools consume the `/v1/presets` and `/v1/formats` REST endpoints — MCP holds no `internal/presets`/`internal/convert` imports)
**Requirements**: MCP-01, MCP-02, MCP-03, MCP-04, MCP-05
**Success Criteria** (what must be TRUE):
  1. `convert_file` from a real MCP client session converts a real file (blocking, `target_format` XOR `preset`) and returns a presigned URL plus the local path of the downloaded result — file bytes are never inlined into the tool result.
  2. `get_job_status` and `download_result` support the non-blocking flow; `list_supported_formats` and `list_presets` (merged client+system view) return live data through the Phase 20 REST endpoints.
  3. During a long-running conversion the internal poll loop emits a progress notification each tick and enforces its own max-duration guard, so a stuck job never blocks past the ~30-min stdio idle window.
  4. The API key never appears in any tool result or error text; agent-supplied paths are canonicalized and contained (no traversal); stdout carries only JSON-RPC framing (logs go to stderr); upstream API errors map to `isError` content results, not protocol errors.
**Plans**: TBD
**Live acceptance**: real MCP client session (Claude Code) driving the tools against the running docker-compose stack — not a mocked transport. This is "new territory" per PROJECT.md; re-verify the pinned `go-sdk` (≥v1.6.1) tool-registration API surface and progress/keepalive mechanics live at planning time.

### Phase 22: CFB Encrypted-vs-Legacy Classification
**Goal**: OLE-CFB uploads receive a distinct, bounded 422 telling the client whether the file is password-protected or a legacy binary format — never a hang, never a path to conversion.
**Depends on**: Nothing (independent document-track deepening; mirrors the existing `SniffContainer` two-stage pattern, reuses Phase 13 fixtures)
**Requirements**: CFB-01, CFB-02
**Success Criteria** (what must be TRUE):
  1. A password-protected OOXML upload (`EncryptionInfo`/`EncryptedPackage` streams) gets an "encrypted" 422 while a legacy binary `.doc`/`.xls`/`.ppt` (`WordDocument`/`Workbook`|`Book`/`PowerPoint Document` streams) gets a distinct "legacy" 422 — both before any S3/Postgres write.
  2. An unrecognized or corrupt CFB structure falls through fail-closed to today's generic 422 and never proceeds to conversion.
  3. A crafted CFB with a directory-chain cycle (or truncated header / self-referential sector index / oversized declared count) gets a bounded 422 and never hangs — the parser caps sectors/entries walked and rejects cycles via a visited-set.
  4. The hand-rolled CFB directory parser survives Go native fuzzing (crash-free, bounded) as the phase exit-gate, seeded with the Phase 13 fixtures plus deliberately corrupted variants.
**Plans**: TBD

### Phase 23: veraPDF ISO 19005 Validation
**Goal**: PDF/A-2b outputs are validated for real ISO 19005 conformance, replacing the v1.3 OutputIntent heuristic, so a non-compliant export fails the job terminally with the veraPDF reason recorded.
**Depends on**: Nothing (independent; highest-uncertainty item, sequenced last so any slip doesn't block Phases 20–22)
**Requirements**: PDFA-01, PDFA-02
**Success Criteria** (what must be TRUE):
  1. A deliberately non-compliant PDF/A export fails the job terminally with the veraPDF non-conformance reason recorded in `job_events`.
  2. A genuinely compliant PDF/A-2b export still passes validation against the existing v1.3 PDF/A fixtures — no regression from the stricter check, per the decided severity policy (Warning vs Error rule violations).
  3. veraPDF is bundled into `Dockerfile.document-worker` (multi-stage `COPY` from `verapdf/cli`, glibc-compat verified live) and invoked through the hardened `runCommand` with its own bounded timeout; `terminalVeraPDFSignatures` are added same-commit (D-04 discipline).
  4. The measured JVM cold-start cost is captured against `DOCUMENT_ENGINE_TIMEOUT` and the CI e2e budget before wiring veraPDF into the job path; if the cost proves prohibitive, the daemon/server-mode (`verapdf/rest`) fallback is documented as the decided alternative.
**Plans**: TBD
**Cost gate**: JVM-per-invocation cost must be measured live before veraPDF is placed on the job path; the CLI-vs-daemon shape is an explicit, measured decision (not assumed), and the daemon fallback is documented either way. Verify veraPDF's exact non-conformance report format/exit codes live before hardcoding terminal signatures.

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|-----------------|--------|-----------|
| 1. Merge, Auth & Rate Limiting | v1.0 | 4/4 | Complete | 2026-07-04 |
| 2. Webhook Delivery | v1.0 | 3/3 | Complete | 2026-07-04 |
| 3. Retry-Safety & Reconciler | v1.0 | 3/3 | Complete | 2026-07-06 |
| 4. Content Validation, Storage Lifecycle & Observability | v1.0 | 5/5 | Complete | 2026-07-07 |
| 5. Webhook SSRF Private-IP Opt-Out | v1.1 | 1/1 | Complete | 2026-07-08 |
| 6. Reconciler Webhook-Gap Sweep & Staleness Soak Test | v1.1 | 4/4 | Complete | 2026-07-08 |
| 7. Image Dimension Limit (Decompression-Bomb Protection) | v1.1 | 2/2 | Complete | 2026-07-08 |
| 8. Document Content Safety & Format Detection | v1.2 | 2/2 | Complete | 2026-07-09 |
| 9. LibreOffice Converter Engine | v1.2 | 2/2 | Complete | 2026-07-09 |
| 10. Document Worker & Reconciler Integration | v1.2 | 4/4 | Complete | 2026-07-09 |
| 11. API Routing & End-to-End Document Conversion | v1.2 | 4/4 | Complete | 2026-07-10 |
| 12. Tech Debt Cleanup | v1.3 | 1/1 | Complete    | 2026-07-10 |
| 13. Cross-Format Conversion & Input Safety | v1.3 | 3/3 | Complete    | 2026-07-10 |
| 14. Validated Conversion Options & PDF/A Export | v1.3 | 3/3 | Complete    | 2026-07-10 |
| 15. HTML→PDF Chromium Engine | v1.3 | 5/5 | Complete    | 2026-07-11 |
| 16. Webhook Delivery Decoupling | v1.3 | 5/5 | Complete | 2026-07-12 |
| 17. Tech Debt Cleanup | v1.4 | 2/2 | Complete | 2026-07-12 |
| 18. Presets | v1.4 | 4/4 | Complete | 2026-07-12 |
| 19. CI Pipeline | v1.4 | 2/2 | Complete | 2026-07-13 |
| 20. Presets REST CRUD & Format Discovery | v1.5 | 0/? | Not started | - |
| 21. MCP Server | v1.5 | 0/? | Not started | - |
| 22. CFB Encrypted-vs-Legacy Classification | v1.5 | 0/? | Not started | - |
| 23. veraPDF ISO 19005 Validation | v1.5 | 0/? | Not started | - |

---

*Next: run `/gsd:plan-phase 20` to plan the first v1.5 phase.*
