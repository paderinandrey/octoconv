# Roadmap: OctoConv

## Milestones

- ✅ **v1.0 Hardening MVP** — Phases 1-4 (shipped 2026-07-08) — see `.planning/milestones/v1.0-ROADMAP.md`
- ✅ **v1.1 Tech Debt Cleanup** — Phases 5-7 (shipped 2026-07-08) — see `.planning/milestones/v1.1-ROADMAP.md`
- ✅ **v1.2 Document Engine Class** — Phases 8-11 (shipped 2026-07-10) — see `.planning/milestones/v1.2-ROADMAP.md`
- 🚧 **v1.3 Document Class v2** — Phases 12-16 (in progress)

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

### 🚧 v1.3 Document Class v2

**Goal:** Документный класс перестаёт быть «только → PDF»: кросс-конвертация внутри класса, чёткие отказы для legacy/encrypted-форматов, архивный PDF/A через validated opts, новый chromium-based HTML→PDF движок (третий engine-class) — и webhook-доставка, переживающая деплой/падение любого подмножества engine-воркеров.

- [x] **Phase 12: Tech Debt Cleanup** - Закрыть унаследованный advisory tech debt v1.0–v1.2 перед новой движковой работой (completed 2026-07-10)
- [ ] **Phase 13: Cross-Format Conversion & Input Safety** - Кросс-конвертация внутри документного класса + структурная валидация выхода + отказ OLE-CFB входов
- [ ] **Phase 14: Validated Conversion Options & PDF/A Export** - Closed-allowlist `opts` механизм + PDF/A-архивный экспорт как первый реальный потребитель
- [ ] **Phase 15: HTML→PDF Chromium Engine** - Третий engine-class: chromium-based HTML→PDF с офлайн-рендерингом (SSRF-safe)
- [ ] **Phase 16: Webhook Delivery Decoupling** - Webhook-доставка переживает отсутствие/падение любого одного engine-воркера

## Phase Details

### Phase 12: Tech Debt Cleanup
**Goal**: Закрыть унаследованный advisory tech debt (v1.0 docker-compose audit + v1.2 11-REVIEW.md findings) перед новой движковой работой, чтобы новые фазы не наследуют известные слабые места E2E/наблюдаемости.
**Depends on**: Nothing (independent of feature work; sequenced first per milestone's stated tech-debt-first convention)
**Requirements**: DEBT-01, DEBT-02, DEBT-03, DEBT-04, DEBT-05
**Success Criteria** (what must be TRUE):
  1. E2E suite passes on plain-Linux docker (not just Docker Desktop): `docker-compose.e2e.yml` gives the `api` service `extra_hosts: host.docker.internal:host-gateway`, and the webhook-pair E2E test is verified to work without Docker Desktop's built-in DNS alias.
  2. Engine-class literals ("image"/"document"/…) exist as exported constants in `internal/convert`; API, reconciler, and worker code all reference the constants — a grep for the raw string literals outside `internal/convert` finds none.
  3. E2E HTTP clients carry explicit per-request timeouts; a deliberately hung API/download endpoint produces a diagnosable test failure (timeout error), not a `go test` binary-level panic.
  4. `gofmt -l ./...` returns zero files.
  5. Every variable documented in `.env.example` is either wired into the corresponding `docker-compose.yml` service or has an explicit, logged reason why not — no silent gaps remain.
**Plans**: 1 plan
- [x] 12-01-PLAN.md — Engine-class constants + E2E harness hardening (extra_hosts, HTTP timeouts) + docker-compose/.env audit + gofmt (DEBT-01..05)

### Phase 13: Cross-Format Conversion & Input Safety
**Goal**: Клиенты могут конвертировать между офисными форматами внутри документного класса (не только → PDF), полученный выход структурно проверяется на валидность, а legacy/encrypted документы отклоняются на входе, а не падают невнятным таймаутом внутри soffice.
**Depends on**: Phase 12 (sequenced after tech debt cleanup per milestone convention; no hard code dependency)
**Requirements**: CONV-01, CONV-02, SAFE-01
**Success Criteria** (what must be TRUE):
  1. Uploading a docx converts to a valid odt, and vice versa; same round-trip works for xlsx↔ods and pptx↔odp — through the existing `POST /v1/jobs` → poll/download → webhook flow, live e2e verified against a freshly built docker-compose stack.
  2. A corrupted/truncated non-PDF conversion output is detected structurally (container-level check against the expected target format) before the job is marked `done` — the job is instead marked `failed` (terminal), never a false success.
  3. Uploading a file with the OLE-CFB signature (`D0 CF 11 E0 A1 B1 1A E1` — legacy binary doc/xls/ppt, or password-protected OOXML) is rejected with 422 before any S3 write, verified live against real fixture files of both sub-cases.
**Plans**: TBD

### Phase 14: Validated Conversion Options & PDF/A Export
**Goal**: Клиенты могут безопасно передавать опции конвертации через `opts` (закрытый allowlist, без сырого попадания в CLI/filter-JSON движка), и первый реальный потребитель этого механизма — PDF/A-архивный экспорт документов.
**Depends on**: Phase 13 (reuses the target-format-aware output-validation path generalized there)
**Requirements**: OPTS-01, OPTS-02
**Success Criteria** (what must be TRUE):
  1. `POST /v1/jobs` accepts an `opts` field validated against a closed allow-list (typed Go struct); any unrecognized or invalid opts value returns 422, and no client-supplied bytes reach the engine's CLI arguments or filter JSON verbatim (proven by a targeted injection-attempt test).
  2. A client can request a PDF/A-2b export for a document→pdf job via `opts`; the resulting PDF is live-verified to carry a PDF/A OutputIntent marker.
  3. Existing document→pdf jobs submitted without `opts` continue converting successfully with no regression — live e2e verified.
**Plans**: TBD

### Phase 15: HTML→PDF Chromium Engine
**Goal**: HTML-файлы конвертируются в PDF через новый, полностью изолированный от сети (офлайн-рендеринг) третий engine-class, следующий паттерну engine-class из v1.2.
**Depends on**: Phase 14 (reuses the validated-opts mechanism for print options; also requires the `jobs.engine` CHECK-constraint migration as a hard prerequisite before any routing work)
**Requirements**: HTML-01, HTML-02, HTML-03
**Success Criteria** (what must be TRUE):
  1. The `jobs.engine` CHECK constraint accepts `html` as a valid value (migration applied), and uploading an HTML file to `POST /v1/jobs` routes it to its own queue/worker binary/container (distinct from `image` and `document`) — live e2e verified end-to-end (upload → convert → download).
  2. HTML referencing an external network resource (e.g. an `<img src>` pointing at an attacker-controlled or internal/metadata address) does not result in any network fetch during conversion — proven by a live test that demonstrates the render is network-blocked, not asserted by code review alone.
  3. A client can set page size, margins, and `printBackground` via the same validated-opts mechanism as Phase 14, and the resulting PDF reflects the requested options.
  4. HTML→PDF conversion is bounded by its own engine timeout, classified terminal on expiry (per the document-engine timeout pattern), and runs inside its own dedicated worker binary/container.
**Plans**: TBD

### Phase 16: Webhook Delivery Decoupling
**Goal**: Webhook-доставка результата переживает отсутствие или падение любого одного engine-воркер-процесса — деплой любого подмножества воркеров больше не может молча терять вебхуки.
**Depends on**: Nothing (orthogonal to all engine-feature phases; sequenced last so no future engine binary is tempted to also register as a webhook consumer)
**Requirements**: WEBH-01
**Success Criteria** (what must be TRUE):
  1. Stopping the `cmd/worker` (image) process entirely does not prevent a `document`- or `html`-engine job's completion webhook from being delivered — live verified.
  2. Killing one of ≥2 redundant webhook-consumer processes mid-delivery does not lose or duplicate the in-flight webhook; the remaining consumer(s) continue draining the queue.
  3. Exactly one reconciler-sweeper instance is active fleet-wide even with multiple webhook-consumer replicas running — no duplicate-sweep race — verified by topology design plus a live test.
**Plans**: TBD

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
| 13. Cross-Format Conversion & Input Safety | v1.3 | 0/TBD | Not started | - |
| 14. Validated Conversion Options & PDF/A Export | v1.3 | 0/TBD | Not started | - |
| 15. HTML→PDF Chromium Engine | v1.3 | 0/TBD | Not started | - |
| 16. Webhook Delivery Decoupling | v1.3 | 0/TBD | Not started | - |

---

*Next: run `/gsd:plan-phase 12` to plan the first v1.3 phase.*
