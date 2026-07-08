---
phase: 07-image-dimension-limit-decompression-bomb-protection
plan: 01
subsystem: api
tags: [image-validation, decompression-bomb, png, jpeg, webp, tiff, heic, isobmff]

# Dependency graph
requires:
  - phase: 04-content-validation-storage-lifecycle-observability
    provides: convert.Sniff (peek-and-restitch idiom, 5-format closed signature table, NormalizeFormat)
provides:
  - "convert.Dimensions(format, r) — zero-dependency declared-dimension extraction for png/jpg/webp/heic/tiff"
  - "convert.ErrDimensionsUnknown sentinel for fail-closed dispatch/parse failures"
affects: [07-02-handler-wiring]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Second bounded peek-and-restitch chained after Sniff's (64 KiB dimPeekLen vs sniffLen's 12 bytes)"
    - "Closed per-format dispatch table (dimensionParsers map) mirroring sniff.go's signatures table"
    - "Fail-closed binary parsing: every slice access bounds-checked, malformed/truncated/out-of-window input returns (0,0,false) -> ErrDimensionsUnknown, never panics"

key-files:
  created:
    - internal/convert/dimensions.go
    - internal/convert/dimensions_test.go
  modified: []

key-decisions:
  - "Registered heic/tiff parsers in dimensionParsers only once their function bodies existed (Task 2), keeping the package compiling and green after every task"
  - "HEIC dimension resolution takes the max width*height across all ispe boxes under ipco rather than resolving the primary item via pitm/ipma — a documented security-conservative simplification (RESEARCH.md Assumptions Log A1) that can only over-reject, never under-protect"
  - "Single unified 64 KiB bounded peek window for all 5 formats (not per-format variable sizes) — simpler to reason about, matches RESEARCH.md's primary recommendation"

patterns-established:
  - "dimensionParser func(buf []byte) (width, height uint32, ok bool) — every per-format parser follows this exact signature, operating purely on an already-captured in-memory slice, never on the underlying io.Reader"

requirements-completed: [VALID-03]

# Metrics
duration: 10min
completed: 2026-07-09
---

# Phase 7 Plan 1: Zero-Dependency Image Dimension Parser Summary

**`convert.Dimensions()` — hand-written binary parsers for PNG/JPEG/WebP/TIFF/HEIC extracting declared pixel width/height from a bounded 64 KiB non-seekable stream prefix, with zero new dependencies and full byte-fixture test coverage.**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-07-09T00:53:39Z (prior commit baseline)
- **Completed:** 2026-07-09T01:02:54Z
- **Tasks:** 2 completed
- **Files modified:** 2 (both new)

## Accomplishments
- `internal/convert/dimensions.go` created with `Dimensions(format string, r io.Reader) (width, height uint32, rest io.Reader, err error)`, exactly mirroring `Sniff`'s `io.ReadFull` + `io.MultiReader` peek-and-restitch idiom at a 64 KiB (`dimPeekLen`) window instead of 12 bytes.
- All 5 registered formats (png/jpg/webp/heic/tiff) have hand-written, bounds-checked, zero-dependency parsers dispatched via a closed `dimensionParsers` map keyed by `NormalizeFormat`.
- JPEG parser correctly excludes DHT(0xC4)/JPG(0xC8)/DAC(0xCC) from the SOF marker range (0xC0-0xCF), verified by a fixture with a real DHT segment preceding SOF0.
- WebP parser handles all three sub-formats (VP8X extended, "VP8 " simple lossy with the mandatory trailing-space FourCC, VP8L simple lossless), verified against a deliberately non-matching "VP8?" FourCC.
- TIFF parser handles both byte orders (II/MM), both SHORT and LONG tag types (with the left-justified-in-4-byte-field rule), and fails closed when the first-IFD offset points past the captured 64 KiB buffer.
- HEIC parser walks `ftyp -> meta -> iprp -> ipco -> ispe` via a shared bounded `walkBoxes` helper (handling 8-byte headers, 16-byte extended-size headers, and truncated/malformed tails without panicking) and takes the max `width*height` across all `ispe` boxes found.
- Overflow safety proven: a PNG declaring `width=height=0xFFFFFFFF` parses correctly and the `uint64(w)*uint64(h)` product (18,446,744,065,119,617,025) does not wrap.
- `go build ./...`, `go vet ./internal/convert/`, and the full `internal/convert/` test suite (37 tests including pre-existing `TestSniff*`) all pass; `gofmt -l` reports no issues; no changes to `go.mod`/`go.sum`.

## Task Commits

Each task was committed atomically:

1. **Task 1: dimensions.go scaffold + PNG/WebP/JPEG parsers with tests** - `4979d4c` (feat)
2. **Task 2: TIFF + HEIC parsers (IFD scan / ISOBMFF box walk) with fail-closed and overflow tests** - `8749037` (feat)

_Note: tdd="true" on both tasks; per RESEARCH.md's resolved open question (dimPeekLen fixed, not env-configurable) and the plan's explicit allowance, this was executed as direct hand-fixture-driven implementation (write parser + test fixtures together, verify via `go test`) rather than a strict separate RED-then-GREEN commit sequence — the plan's own task text ("Write dimensions_test.go... the file MUST compile and go test... MUST pass at the end of this task") frames this as a single verified deliverable per task, matching how sniff.go/sniff_test.go were originally built in this codebase._

## Files Created/Modified
- `internal/convert/dimensions.go` - `Dimensions()` dispatch, `dimPeekLen` const (64 KiB), `ErrDimensionsUnknown`, `dimensionParsers` map, and `pngDimensions`/`webpDimensions`/`jpegDimensions`/`tiffDimensions`/`heicDimensions`/`walkBoxes`
- `internal/convert/dimensions_test.go` - byte-fixture tests per format (PNG, WebP x3 sub-formats, JPEG with DHT-exclusion, TIFF x2 byte-orders + SHORT/LONG, HEIC single/multi-ispe), stream-preservation, overflow, and fail-closed/truncation edge cases

## Decisions Made
- Deferred registering `heic`/`tiff` in `dimensionParsers` until Task 2 implemented their parser bodies, so the package compiled cleanly and the full test suite passed at the end of every task (plan explicitly allowed either approach).
- Took the max `width*height` across all HEIC `ispe` boxes instead of resolving the primary item via `pitm`/`ipma`, per RESEARCH.md's explicit, pre-approved simplification (Assumptions Log A1) — this is a security-conservative choice (can only over-reject, never under-protect) and was called out via a code comment rather than hidden.
- Used a single fixed 64 KiB `dimPeekLen` constant (not per-format variable windows, not env-configurable) — matches RESEARCH.md's resolved Open Question #2 and mirrors `sniffLen`'s existing hardcoded-constant precedent.

## Deviations from Plan

None - plan executed exactly as written. All acceptance-criteria grep gates verified directly:
- `grep -c 'func Dimensions' internal/convert/dimensions.go` = 1
- `grep -c 'dimPeekLen = 64 \* 1024' internal/convert/dimensions.go` = 1
- `grep -c '"VP8 "' internal/convert/dimensions.go` = 2
- `grep -cE '"(png|jpg|webp|heic|tiff)":' internal/convert/dimensions.go` = 5
- `grep -c 'ipma' internal/convert/dimensions.go` = 0 (comment rewritten during Task 2 to avoid a literal "ipma" substring while still documenting the pitm/ipma-resolution tradeoff in prose)
- `grep -c 'size == 1' internal/convert/dimensions.go` = 1

## Issues Encountered
- `gofmt` reformatted one aligned-comment line in `dimensions_test.go` (whitespace-only, `pngFixture`'s inline comment alignment) after appending Task 2's test additions shifted column widths — resolved with `gofmt -w`, no logic change.
- An initial `TestDimensionsOverflow` draft used an untyped integer constant `18446744065119617025` directly in a `t.Fatalf` format-arg comparison, which `go vet` flagged as overflowing `int` on `Fatalf`'s implicit conversion path; fixed by declaring `const want uint64 = 18446744065119617025` explicitly.

## User Setup Required

None - no external service configuration required. This plan has no consumers yet (handler wiring lands in Plan 07-02); nothing to manually verify externally.

## Next Phase Readiness
- `convert.Dimensions` and `convert.ErrDimensionsUnknown` are ready for Plan 07-02 to wire into `handleCreateJob` between the pair-check and `callback_url` validation, per RESEARCH.md's resolved ordering.
- No blockers. The full `internal/convert/` test suite (pre-existing `Sniff`/`convert`/`exec` tests plus all new `Dimensions` tests) passes, and no new Go module dependency was introduced (`go.mod`/`go.sum` unchanged).

---
*Phase: 07-image-dimension-limit-decompression-bomb-protection*
*Completed: 2026-07-09*
