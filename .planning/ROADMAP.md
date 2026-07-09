# Roadmap: OctoConv

## Milestones

- ✅ **v1.0 Hardening MVP** — Phases 1-4 (shipped 2026-07-08) — see `.planning/milestones/v1.0-ROADMAP.md`
- ✅ **v1.1 Tech Debt Cleanup** — Phases 5-7 (shipped 2026-07-08) — see `.planning/milestones/v1.1-ROADMAP.md`
- 🚧 **v1.2 Document Engine Class** — Phases 8-11 (in progress)

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

### 🚧 v1.2 Document Engine Class (In Progress)

**Milestone Goal:** Внутренние сервисы могут конвертировать офисные документы (docx/xlsx/pptx/odt/ods/odp) в PDF через новый класс движков на LibreOffice, поверх уже готовой production-инфраструктуры (auth, rate limiting, webhook-доставка, reconciler, observability).

- [x] **Phase 8: Document Content Safety & Format Detection** - Stdlib-only ZIP/ODF/OOXML disambiguation, zip-bomb guard, and macro rejection gate office uploads before they reach storage or the engine (completed 2026-07-09)
- [ ] **Phase 9: LibreOffice Converter Engine** - `LibreOfficeConverter` with per-job profile isolation, output validation, and a verified process-kill guarantee
- [ ] **Phase 10: Document Worker & Reconciler Integration** - Separate `cmd/document-worker` binary, `DOCUMENT_ENGINE_TIMEOUT`, and engine-aware reconciler recovery
- [ ] **Phase 11: API Routing & End-to-End Document Conversion** - `handleCreateJob` routes documents to the document engine and the full pipeline is live-verified across all 6 format pairs

## Phase Details

### Phase 8: Document Content Safety & Format Detection
**Goal**: The API can tell a genuine office document from a spoofed or hostile one, and rejects anything unsafe before it touches storage or the conversion engine.
**Depends on**: Phase 7 (existing magic-byte validation pattern this phase extends)
**Requirements**: DOC-01, DOC-02, DOC-03
**Success Criteria** (what must be TRUE):
  1. API accepts docx/xlsx/pptx/odt/ods/odp uploads and structurally disambiguates them (ZIP/OOXML central-directory check, ODF fixed-offset `mimetype` check) instead of trusting the file extension or a generic ZIP signature.
  2. API rejects with 422, before any S3 write, an upload whose structural content doesn't match its claimed office format.
  3. API rejects with 422 an office document whose declared uncompressed ZIP size exceeds a configurable limit (zip-bomb guard), before conversion.
  4. API rejects with 422 an office document containing macro parts (`vbaProject.bin` / Basic-script manifest).
**Plans**: 2 plans
  - [x] 08-01-PLAN.md — internal/convert detection layer: SniffContainer (OOXML/ODF disambiguation, zip-bomb size sum, macro + duplicate-root-part scan) + HasDimensionLimit regression-fix predicate
  - [x] 08-02-PLAN.md — handleCreateJob integration: SniffContainer branch, zip-bomb/macro 422 rejections, dimension-check guard, MAX_DOCUMENT_UNCOMPRESSED_BYTES config wiring

### Phase 9: LibreOffice Converter Engine
**Goal**: The worker can turn an accepted office document into a trustworthy PDF via LibreOffice headless, and never leaves an orphaned `soffice` process behind.
**Depends on**: Phase 8 (only validated documents reach the engine)
**Requirements**: DOC-04, DOC-05, DOC-06
**Success Criteria** (what must be TRUE):
  1. Worker converts docx/xlsx/pptx/odt/ods/odp to PDF through LibreOffice headless, with each job running against its own isolated `-env:UserInstallation` profile so concurrent jobs never collide on a shared lock file.
  2. Worker validates the conversion output (non-zero size, valid `%PDF-` magic bytes) before marking a job `done`; invalid output is a terminal failure, not a false success.
  3. An integration test proves that when a conversion is killed on timeout, the real `soffice`/`soffice.bin` process and any children are actually terminated — zero surviving processes, not an assumption inherited from the image engine's exec wrapper.
**Plans**: 2 plans
  - [x] 09-01-PLAN.md — LibreOfficeConverter (Pairs/Convert, per-job -env:UserInstallation isolation, %PDF- output validation), registry wiring, and unit + soffice-gated live tests
  - [ ] 09-02-PLAN.md — Dockerfile.worker LibreOffice provisioning + Dockerfile.worker-test harness; live DOC-06 process-kill proof (zero survivors) run inside the LibreOffice image

### Phase 10: Document Worker & Reconciler Integration
**Goal**: Document conversions run in their own resource-isolated process, respect their own timeout budget, and are recovered correctly if they get stranded.
**Depends on**: Phase 9 (converter must exist to wire into a worker)
**Requirements**: DOC-07, DOC-08, DOC-09
**Success Criteria** (what must be TRUE):
  1. Document conversion jobs are processed by a separate `cmd/document-worker` binary/container, resource-isolated (own CPU/RAM limits, own Docker image) from the image worker.
  2. A document conversion exceeding `DOCUMENT_ENGINE_TIMEOUT` (distinct from and typically larger than `ENGINE_TIMEOUT`) is classified as a terminal failure rather than retried forever.
  3. A document job stranded in `queued`/`active` is recovered by the reconciler through the document queue specifically — never misrouted onto the image queue.
**Plans**: TBD

### Phase 11: API Routing & End-to-End Document Conversion
**Goal**: A client can submit an office document and get a converted PDF back through the exact same API, webhook, and download flow already used for images — no separate integration path.
**Depends on**: Phase 8, Phase 10 (needs both the safety gate and the document queue/engine to route to)
**Requirements**: DOC-10
**Success Criteria** (what must be TRUE):
  1. `POST /v1/jobs` routes an accepted office document to the document engine/queue (not the image queue) and skips the image-only dimension check for document uploads.
  2. `GET /v1/jobs/{id}` returns status and a working presigned download URL for a completed document job, identically to image jobs.
  3. Webhook delivery fires for completed/failed document jobs using the existing signed-delivery pipeline, with no document-specific changes required.
  4. A live end-to-end test converts all 6 supported format pairs (docx, xlsx, pptx, odt, ods, odp → pdf) successfully through the full upload → convert → download pipeline.
**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 8 → 9 → 10 → 11

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|-----------------|--------|-----------|
| 1. Merge, Auth & Rate Limiting | v1.0 | 4/4 | Complete | 2026-07-04 |
| 2. Webhook Delivery | v1.0 | 3/3 | Complete | 2026-07-04 |
| 3. Retry-Safety & Reconciler | v1.0 | 3/3 | Complete | 2026-07-06 |
| 4. Content Validation, Storage Lifecycle & Observability | v1.0 | 5/5 | Complete | 2026-07-07 |
| 5. Webhook SSRF Private-IP Opt-Out | v1.1 | 1/1 | Complete | 2026-07-08 |
| 6. Reconciler Webhook-Gap Sweep & Staleness Soak Test | v1.1 | 4/4 | Complete | 2026-07-08 |
| 7. Image Dimension Limit (Decompression-Bomb Protection) | v1.1 | 2/2 | Complete | 2026-07-08 |
| 8. Document Content Safety & Format Detection | v1.2 | 2/2 | Complete   | 2026-07-09 |
| 9. LibreOffice Converter Engine | v1.2 | 1/2 | In Progress|  |
| 10. Document Worker & Reconciler Integration | v1.2 | 0/TBD | Not started | - |
| 11. API Routing & End-to-End Document Conversion | v1.2 | 0/TBD | Not started | - |
