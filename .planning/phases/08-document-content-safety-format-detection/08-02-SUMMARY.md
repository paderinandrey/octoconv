---
phase: 08-document-content-safety-format-detection
plan: 02
subsystem: api
tags: [go, content-validation, zip-bomb-guard, macro-detection, handler-wiring]

# Dependency graph
requires:
  - phase: 08-document-content-safety-format-detection
    plan: 01
    provides: "SniffContainer/ContainerResult/HasDimensionLimit detection layer this plan wires into the HTTP request pipeline"
provides:
  - "handleCreateJob container-inspection branch: structural docx/xlsx/pptx/odt/ods/odp disambiguation, zip-bomb rejection, macro rejection -- all before any storage write"
  - "MAX_DOCUMENT_UNCOMPRESSED_BYTES configurable zip-bomb ceiling (Config/env/docs, default 500 MiB)"
  - "Fixed dimension-check regression: documents skip the image-only Dimensions() block entirely"
affects: [09-libreoffice-converter-engine, 10-document-worker-queue-reconciler, 11-document-engine-routing]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Container-inspection branch slotted inside the existing detected==\"\" rejection point, reading from the original multipart.File (ReadAt), never from Sniff's re-stitched rest stream"
    - "Fail-closed fallthrough: DuplicateRootPart/ErrNotAZip/Format==\"\" all leave detected empty so the existing unrecognized-content 422 handles them uniformly"
    - "HasDimensionLimit-guarded dimension-check block (image-only), replacing the previously-unconditional Dimensions() call"

key-files:
  created: []
  modified:
    - internal/api/api.go
    - cmd/api/main.go
    - .env.example
    - internal/api/handlers.go
    - internal/api/handlers_test.go

key-decisions:
  - "zip-bomb/macro/duplicate-root-part/bare-zip tests intentionally assert only the observable HTTP contract (422 + store.uploaded==false + repo.created==nil), which the pre-existing fail-closed unrecognized-content path already satisfied before this plan's implementation -- only the two happy-path tests (docx/odt detected-but-unsupported) are true RED/GREEN differentiators for the new detection wiring; documented under TDD Gate Compliance below rather than silently treated as a stuck signal"
  - "Zip-bomb test fixture reuses the docsniff_test.go real-deflate-content technique (zipBombDocxFixture writes actual compressible zero bytes) so the physical multipart body stays small enough to pass MaxUploadBytes even while the declared uncompressed total exceeds the test's small MaxDocumentUncompressedBytes"

requirements-completed: [DOC-01, DOC-02, DOC-03]

# Metrics
duration: 25min
completed: 2026-07-09
---

# Phase 8 Plan 2: Document Content Safety Handler Wiring Summary

**`handleCreateJob` now structurally detects the six ZIP-based office formats via `convert.SniffContainer`, rejects zip-bomb-shaped and macro-carrying uploads with 422 before any storage write, and the confirmed image-only dimension-check regression is fixed with a `HasDimensionLimit` guard — all threaded through a new configurable `MAX_DOCUMENT_UNCOMPRESSED_BYTES` limit (500 MiB default).**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-09T04:41:00+03:00
- **Completed:** 2026-07-09T04:44:38+03:00
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments
- `MaxDocumentUncompressedBytes` wired through `Config` → `Server` → `NewServer` default (500 MiB, D-04) → `cmd/api/main.go`'s `envInt64("MAX_DOCUMENT_UNCOMPRESSED_BYTES", 500<<20)` → documented in `.env.example`, mirroring the existing `MaxImagePixels` four-touch-point pattern exactly
- `handleCreateJob` gains a container-inspection branch inside the existing `detected == ""` rejection point: reads the first 4 bytes via `file.ReadAt` (never `rest`), and when PK-prefixed calls `convert.SniffContainer(file, header.Size)` to disambiguate docx/xlsx/pptx/odt/ods/odp
- Zip-bomb rejection (`cr.TotalUncompressed > s.maxDocumentUncompressedBytes`) and unconditional macro rejection (`cr.HasMacro`) both fire before any storage write, with structured `reason=zip_bomb`/`reason=macro_detected` rejection logs matching the existing D-08 convention
- `DuplicateRootPart`, `ErrNotAZip`, and `Format==""` all fail closed by leaving `detected` empty, falling through to the pre-existing unrecognized-content 422 — no separate rejection path needed
- The dimension-check block (previously unconditional `convert.Dimensions(...)`) is now wrapped in `if convert.HasDimensionLimit(detected)`, so document uploads skip it entirely while image behavior is byte-for-byte unchanged
- 6 new `TestCreateJob_*` document/container test cases plus all 8 pre-existing image-path tests pass; full repo `go test ./... -count=1` is green with zero regressions

## Task Commits

Task 2 followed the RED → GREEN TDD cycle (tdd="true"), committed atomically:

1. **Task 1: Wire MaxDocumentUncompressedBytes config (three touch-points)**
   - `a69047e` feat(08-02): wire MaxDocumentUncompressedBytes config
2. **Task 2: Integrate SniffContainer + zip-bomb/macro rejections + dimension-check guard into handleCreateJob**
   - `26001ed` test(08-02): add failing tests for document container-inspection wiring (RED)
   - `54026ac` feat(08-02): wire SniffContainer detection and dimension-check guard into handleCreateJob (GREEN)

_No REFACTOR commit was needed — the GREEN implementation passed cleanly without follow-up cleanup._

## Files Created/Modified
- `internal/api/api.go` - `Config.MaxDocumentUncompressedBytes` / `Server.maxDocumentUncompressedBytes` fields + 500 MiB (`500 << 20`) default block + struct-literal wiring
- `cmd/api/main.go` - `MAX_DOCUMENT_UNCOMPRESSED_BYTES` env wiring via the existing `envInt64` helper
- `.env.example` - documented `MAX_DOCUMENT_UNCOMPRESSED_BYTES=524288000` line under `# API`
- `internal/api/handlers.go` - container-inspection branch (docx/xlsx/pptx/odt/ods/odp detection, zip-bomb rejection, macro rejection) plus `HasDimensionLimit`-guarded dimension-check block; added `bytes` import
- `internal/api/handlers_test.go` - 6 new fixture-builder helpers (`docxFixture`, `odtFixture`, `zipBombDocxFixture`, `macroDocxFixture`, `duplicateRootPartDocxFixture`, `bareZipFixture`) + `mustWriteZipEntry` + 6 new `TestCreateJob_*` cases

## TDD Gate Compliance

Task 2 (`tdd="true"`) followed the mandatory RED → GREEN gate sequence:
- RED commit `26001ed` (`test(08-02): ...`) precedes GREEN commit `54026ac` (`feat(08-02): ...`) in git log — gate order verified.
- At RED time, 2 of the 6 new tests genuinely failed (`TestCreateJob_DocumentDetectedButUnsupported`, `TestCreateJob_ODFDetectedButUnsupported` — both asserted "unsupported conversion" but got "unrecognized file content" since the detection branch wasn't wired yet). The other 4 (`TestCreateJob_ZipBombRejected`, `TestCreateJob_MacroRejected`, `TestCreateJob_DuplicateRootPartRejected`, `TestCreateJob_BareZipUnrecognized`) passed trivially at RED time, because they only assert the observable HTTP contract (422 + `store.uploaded==false` + `repo.created==nil`), which the pre-existing fail-closed `unrecognized_content` path already satisfied for any undetected content — coincidentally correct for the wrong underlying reason. This was investigated per the TDD fail-fast rule and judged not a stuck signal: the two differentiator tests are the genuine RED/GREEN proof that `SniffContainer` wiring works; the other four function as regression locks on the pre-existing fail-closed invariant, not as feature-existence proofs. All 6 pass at GREEN.

## Decisions Made
- Kept the container-inspection branch strictly inside the `detected == ""` block (per PATTERNS.md's exact insertion sketch) rather than restructuring the surrounding Sniff/mismatch/pair-check sequence — minimizes surface area touched and keeps the existing image path provably unchanged.
- Used `s.maxDocumentUncompressedBytes` (Server field, not Config) for the runtime comparison, matching the existing `s.maxImagePixels` idiom exactly.

## Deviations from Plan

None - plan executed exactly as written; only the TDD-gate RED-signal nuance above (documented, not a deviation from plan instructions).

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. Zero new runtime dependencies (uses only `convert.SniffContainer`/`convert.HasDimensionLimit` from Plan 08-01 and stdlib `bytes`).

## Next Phase Readiness

- Document uploads (docx/xlsx/pptx/odt/ods/odp) now survive the full `handleCreateJob` pipeline up through the pair-check, correctly 422ing with "unsupported conversion" (not a detection or dimension failure) since no document `Converter` is registered yet — Phase 9 (LibreOffice converter) can register one and this plan's detection/safety gate will pass qualifying uploads straight through to upload/create/enqueue with no further handler changes needed.
- `MAX_DOCUMENT_UNCOMPRESSED_BYTES` is live and documented; operators can tune the zip-bomb ceiling independently of `MAX_IMAGE_PIXELS`.
- No blockers.

---
*Phase: 08-document-content-safety-format-detection*
*Completed: 2026-07-09*

## Self-Check: PASSED

All modified/created files verified present on disk; all 3 referenced commit hashes (`a69047e`, `26001ed`, `54026ac`) verified present in `git log --oneline --all`.
