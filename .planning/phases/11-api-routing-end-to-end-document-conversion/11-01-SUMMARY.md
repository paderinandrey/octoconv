---
phase: 11-api-routing-end-to-end-document-conversion
plan: 01
subsystem: api
tags: [go, chi, engine-routing, converter-registry, document-conversion]

# Dependency graph
requires:
  - phase: 09-libreoffice-converter-engine
    provides: LibreOfficeConverter registered in convert.Default
  - phase: 10-document-worker-reconciler-integration
    provides: EnqueueDocumentConvert on queue.Client, document queue/worker, reconciler engine-switch pattern
provides:
  - "Converter.Engine() self-description on every converter (image/document)"
  - "Registry.EngineFor(from, to) (string, bool) — single source of truth for engine-class routing"
  - "handleCreateJob routes accepted uploads to the correct engine-class queue based on detected content, not the attacker-supplied extension"
affects: [reconciler, worker, document-worker, api]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Converter self-describes its engine class via Engine() string; Registry.EngineFor wraps Lookup exactly like Supports — no parallel engine-class map"
    - "handleCreateJob's engine switch mirrors reconciler.go's fail-closed engine switch (unknown engine => HTTP 500, never silently dropped)"

key-files:
  created: []
  modified:
    - internal/convert/convert.go
    - internal/convert/libvips.go
    - internal/convert/libreoffice.go
    - internal/convert/convert_test.go
    - internal/api/api.go
    - internal/api/handlers.go
    - internal/api/handlers_test.go

key-decisions:
  - "Engine class stays an untyped, self-described string per converter (no typed enum, no parallel lookup map) — consistent with the existing StatusQueued-style string-constant convention"
  - "handleCreateJob's engine switch fails closed (HTTP 500) on an unrecognized engine value, mirroring reconciler.go's reasoning, rather than silently defaulting to the image queue"

patterns-established:
  - "Engine-class routing is derived exclusively from the magic-byte-detected format via EngineFor(detected, target), never from the client-supplied filename extension (reuses the Phase 8 content-honesty gate)"

requirements-completed: [DOC-10]

# Metrics
duration: 3min
completed: 2026-07-10
---

# Phase 11 Plan 01: API Engine-Aware Routing Summary

**handleCreateJob now derives the job's engine class from content-detected format via a new Converter.Engine()/Registry.EngineFor contract, routing document uploads to the document queue (Engine="document") and image uploads to the image queue, replacing the last hardcoded image-only assumption in the request path.**

## Performance

- **Duration:** ~3 min (461b6eb to 3c1cb30)
- **Started:** 2026-07-09T23:58:08+03:00
- **Completed:** 2026-07-10T00:00:27+03:00
- **Tasks:** 3 completed
- **Files modified:** 7

## Accomplishments
- `Converter` interface gained a third method `Engine() string`; `LibvipsConverter` returns `"image"`, `LibreOfficeConverter` returns `"document"` — each converter is now the single source of truth for its own engine class (D-01)
- `Registry.EngineFor(from, to) (string, bool)` mirrors `Supports`, giving `handleCreateJob` one call that both validates the pair and yields the routing decision (D-02)
- `handleCreateJob` sets `Engine: engine` (derived, not hardcoded) on the created job and switches between `EnqueueImageConvert`/`EnqueueDocumentConvert`, with a fail-closed `default` case (HTTP 500) mirroring `reconciler.go`'s existing engine switch (T-11-02)
- Locked in via test (D-06): a document upload is accepted (202) even when `MaxImagePixels=1`, proving `HasDimensionLimit` correctly scopes the pixel-dimension check to image formats only

## Task Commits

Each task was committed atomically:

1. **Task 1: Add Engine() to Converter + EngineFor to Registry** - `461b6eb` (feat)
2. **Task 2: Engine-aware routing in handleCreateJob** - `a79a83e` (feat)
3. **Task 3: Update handler tests for engine-aware routing + dimension-skip** - `3c1cb30` (test)

_Note: Task 2's commit intentionally left `go vet ./internal/api/` red for one commit (fakeQueue not yet implementing the widened Enqueuer) — this is standard for this repo's non-TDD-tagged multi-file plans where the test double lives in a later task; Task 3 restores full green immediately after._

## Files Created/Modified
- `internal/convert/convert.go` - `Engine() string` added to `Converter` interface; `Registry.EngineFor(from, to) (string, bool)` added
- `internal/convert/libvips.go` - `LibvipsConverter.Engine()` returns `"image"`
- `internal/convert/libreoffice.go` - `LibreOfficeConverter.Engine()` returns `"document"`
- `internal/convert/convert_test.go` - `TestConverterEngine`, `TestRegistryEngineFor` (covers all 6 document source formats + alias normalization + unsupported-pair zero value)
- `internal/api/api.go` - `Enqueuer` interface widened with `EnqueueDocumentConvert`; doc comment updated
- `internal/api/handlers.go` - `engineDocument` const added; pair-check replaced with `EngineFor`; `Engine: engine` (not hardcoded `engineImage`); enqueue call replaced with a fail-closed `switch engine`
- `internal/api/handlers_test.go` - `fakeQueue` split into `enqueuedImage`/`enqueuedDocument`; `TestCreateJob_OK` asserts image-only routing; document tests assert document-queue routing + `Engine == "document"`; new `TestCreateJob_DocumentSkipsDimensionCheck`; stale "Phase 10/11 transitional" doc comments removed

## Decisions Made
- Kept engine class as an untyped string self-described per converter rather than introducing a typed enum or a separate engine-class lookup map, per the plan's explicit instruction and the codebase's existing string-constant-for-enum-like-values convention (`StatusQueued` etc.)
- Fail-closed `default` case in `handleCreateJob`'s switch returns HTTP 500 rather than silently routing to the image queue — mirrors `reconciler.go`'s established reasoning for unroutable engine values (T-11-02); in practice this branch is unreachable given `EngineFor` only ever returns a value produced by a registered `Converter.Engine()`, but it protects against a future registry/routing bug rather than assuming it away

## Deviations from Plan

None - plan executed exactly as written. All three tasks' acceptance criteria were verified via automated checks (`go build`, `go vet`, `go test`, targeted `grep`) before each commit.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. No new packages installed (T-11-SC: no supply-chain surface introduced).

## Next Phase Readiness

- The engine-routing decision is now real, testable code end-to-end: an accepted docx/xlsx/pptx/odt/ods/odp upload reaches the document queue/worker/LibreOffice engine that Phases 8-10 already built, with zero further plumbing required.
- `go build ./...`, `go vet ./...`, `go test ./... -count=1` all green; `gofmt -l internal/convert internal/api` reports no files.
- No blockers for Plan 02/03 of Phase 11.

---
*Phase: 11-api-routing-end-to-end-document-conversion*
*Completed: 2026-07-10*
