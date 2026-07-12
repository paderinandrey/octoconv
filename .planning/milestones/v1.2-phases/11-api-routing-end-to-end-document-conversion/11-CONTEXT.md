# Phase 11: API Routing & End-to-End Document Conversion - Context

**Gathered:** 2026-07-09
**Status:** Ready for planning

<domain>
## Phase Boundary

A client can submit an office document and get a converted PDF back through the exact same API, webhook, and download flow already used for images — no separate integration path. This phase covers: `handleCreateJob` routing accepted office documents to the document engine/queue instead of the image queue (the last hardcoded `EnqueueImageConvert`/`Engine: engineImage` call in `internal/api/handlers.go`), and a live end-to-end verification that all 6 document format pairs (docx/xlsx/pptx/odt/ods/odp → pdf) work through the full upload → convert → download (+ webhook) pipeline. It does NOT cover: any new document validation (Phase 8), the LibreOffice converter itself (Phase 9), or queue/worker/reconciler plumbing (Phase 10) — all of that infrastructure already exists; this phase only wires the API's routing decision and proves the whole thing end-to-end.

</domain>

<decisions>
## Implementation Decisions

### Engine-routing mechanism
- **D-01:** The `Converter` interface (`internal/convert/convert.go`) gains a new `Engine() string` method returning the engine class (`"image"` or `"document"`) a converter belongs to. Both `LibvipsConverter` and `LibreOfficeConverter` implement it. This is the single source of truth for engine classification — mirrors how `Pairs()` already lets each converter self-describe its supported format pairs.
- **D-02:** `Registry` gains a new `EngineFor(from, to string) (string, bool)` convenience method that wraps `Lookup` + `c.Engine()` internally (mirroring how `Supports` wraps `Lookup`). `handleCreateJob` calls `convert.Default.EngineFor(detected, target)` and uses the result to decide `Engine: "image"` vs `Engine: "document"` on the job row, and `EnqueueImageConvert` vs `EnqueueDocumentConvert` on the queue client. `internal/api/api.go`'s `Enqueuer` interface must be extended with `EnqueueDocumentConvert` (mirrors the reconciler's `enqueuer` interface extension in Phase 10) since `handleCreateJob` needs to call it through the interface, not the concrete `*queue.Client`.

### Live end-to-end test
- **D-03:** A new committed Go test (env-gated, following the project's established `if os.Getenv("VAR") == "" { t.Skip(...) }` convention) drives the full pipeline against a **real running docker-compose stack** (API + document-worker + Postgres/Redis/MinIO, with document-worker actually invoking `soffice`) — not an in-process handler call and not a manual runbook. This is the project's first true E2E test spanning the HTTP layer (TESTING.md currently says "E2E Tests: Not used"); planner/researcher should decide the exact package location (e.g. a new `internal/e2e` package or a top-level `e2e/` directory) and the specific gating env var name (e.g. `E2E_BASE_URL` or similar, pointing at a running API instance).
- **D-04:** The test covers all 6 format pairs (docx/xlsx/pptx/odt/ods/odp → pdf) via real HTTP calls: `POST /v1/jobs` → poll `GET /v1/jobs/{id}` until `done` → verify the presigned download URL actually returns a valid PDF (`%PDF-` magic bytes).
- **D-05:** At least one of the 6 format-pair runs must also set a `callback_url` pointing at a local `httptest.Server` acting as the webhook receiver, and assert the signed webhook payload actually arrives — folding SC#3 (webhook delivery) into the same test rather than a separate one. The other 5 pairs only need to poll status (no need to duplicate webhook verification 6×).

### Image-only validation scoping (confirmed, not re-implemented)
- **D-06:** `HasDimensionLimit`'s existing scoping (`internal/api/handlers.go:195`) already satisfies SC#1 — document formats already skip the pixel-dimension check today. No handler code change needed for this specific point; the planner should add a focused unit/handler test asserting a document upload does NOT trigger the dimension-check code path, to lock in this behavior as an explicit phase requirement rather than an accidental side effect of existing code.

### Claude's Discretion
- Exact naming/location of the new E2E test package and its gating env var(s).
- Whether `EngineFor` returns `(string, bool)` or a different zero-value convention for an unsupported pair — planner's call, should mirror `Lookup`'s `(Converter, bool)` shape.
- Test fixture strategy for the 6 sample documents (checked-in minimal fixtures vs. generated at test time) — technical detail.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Current Milestone v1.2 section
- `.planning/REQUIREMENTS.md` — `DOC-10` (locked v1.2 scope for this phase, the last requirement in the milestone)
- `.planning/ROADMAP.md` — Phase 11 goal, success criteria (4 criteria, all quoted in this file's domain/decisions above)

### Prior Phase Context (infrastructure this phase wires into, not rebuilds)
- `.planning/phases/10-document-worker-reconciler-integration/10-CONTEXT.md` / `10-SUMMARY.md` (all 4 plans) — the document queue/worker/reconciler infrastructure this phase's API routing targets
- `.planning/phases/09-libreoffice-converter-engine/09-SUMMARY.md` — `LibreOfficeConverter.Pairs()` (the exact 6 format pairs this phase's E2E test must cover)
- `.planning/phases/08-document-content-safety-format-detection/08-SUMMARY.md` — `SniffContainer`/`HasDimensionLimit` validation already in `handleCreateJob`, must not be disturbed by the routing change

### Existing Codebase (reference patterns to follow)
- `internal/api/handlers.go` — `handleCreateJob` (lines 74-269): the hardcoded `Engine: engineImage` (line 241) and `s.queue.EnqueueImageConvert` (line 259) call sites this phase replaces with engine-aware routing; `HasDimensionLimit` scoping (line 195) to confirm via test
- `internal/api/api.go` — `Enqueuer` interface (line 31: currently only `EnqueueImageConvert`) — needs `EnqueueDocumentConvert` added
- `internal/convert/convert.go` — `Converter` interface, `Registry.Lookup`/`Supports` — the exact shape `Engine()`/`EngineFor` must mirror
- `internal/convert/libvips.go`, `internal/convert/libreoffice.go` — the two concrete converters that must each implement the new `Engine()` method
- `internal/queue/client.go` — `EnqueueImageConvert`/`EnqueueDocumentConvert` (both already exist, from Phase 10)
- `.planning/codebase/TESTING.md` — current testing conventions (env-gated integration test skip pattern); explicitly notes "E2E Tests: Not used" today — this phase changes that
- `docker-compose.yml` — the stack the new E2E test runs against (api, worker, document-worker, postgres, redis, minio services)

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `Registry.Lookup`/`Supports` pattern (`internal/convert/convert.go`) — `EngineFor` is a thin wrapper following the exact same shape
- Existing env-gated integration test skip convention (`if os.Getenv("VAR") == "" { t.Skip(...) }`) used throughout `internal/jobs`, `internal/storage`, `internal/queue`, `internal/reconciler` — the new E2E test follows this same convention, just at a higher (HTTP) layer
- `httptest.Server` webhook-receiver pattern already exists in the webhook delivery test suite (Phase 2) — reusable for D-05's webhook assertion

### Established Patterns
- Converter self-description via interface methods (`Pairs()`) — `Engine()` extends this same idiom rather than introducing a parallel lookup table
- Postgres-first double write (job row created before enqueue) — unaffected by this phase, but the E2E test's polling loop must account for the brief `queued` window

### Integration Points
- `internal/convert/convert.go` (MODIFY — `Converter` interface gains `Engine()`, `Registry` gains `EngineFor`)
- `internal/convert/libvips.go`, `internal/convert/libreoffice.go` (MODIFY — implement `Engine()`)
- `internal/api/api.go` (MODIFY — `Enqueuer` interface gains `EnqueueDocumentConvert`)
- `internal/api/handlers.go` (MODIFY — `handleCreateJob` engine-aware routing)
- New E2E test package/file (CREATE — location left to planner, per D-03)

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — backend-only API routing phase. Concrete asks: make the engine decision a real, testable piece of code (not a hardcoded string), and prove the whole document pipeline works end-to-end against real infrastructure, not mocks — matching the project's established "live e2e verified" bar from every prior phase.

</specifics>

<deferred>
## Deferred Ideas

None raised this phase — this is the last phase in the v1.2 milestone (DOC-10 is the final requirement); no new capabilities were suggested during discussion.

</deferred>

---

*Phase: 11-API Routing & End-to-End Document Conversion*
*Context gathered: 2026-07-09*
