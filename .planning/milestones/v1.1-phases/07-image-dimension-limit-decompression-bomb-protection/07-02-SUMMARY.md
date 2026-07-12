---
phase: 07-image-dimension-limit-decompression-bomb-protection
plan: 02
subsystem: api
tags: [image-validation, decompression-bomb, handler-wiring, config]

# Dependency graph
requires:
  - phase: 07-image-dimension-limit-decompression-bomb-protection
    plan: 01
    provides: convert.Dimensions(format, r) + convert.ErrDimensionsUnknown
provides:
  - "MAX_IMAGE_PIXELS configurable pixel-count ceiling (default 100 megapixels), threaded env -> cmd/api/main.go -> api.Config -> Server.maxImagePixels"
  - "handleCreateJob dimension gate: rejects 422 before storage.Upload/enqueue on undeterminable or over-limit declared dimensions (VALID-03 closed)"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Config/env wiring cross-cutting change (Config field + Server field + zero-value default + cmd/api/main.go envInt64 call + .env.example doc line), mirroring MAX_UPLOAD_BYTES's existing four-touch-point pattern"
    - "Reject-before-storage-write gate ordering: dimension check inserted between the Supports pair-check and callback_url validation, same client_id-tagged log.Printf + writeError(422) idiom as the existing Sniff-rejection blocks"

key-files:
  created: []
  modified:
    - internal/api/api.go
    - cmd/api/main.go
    - .env.example
    - internal/api/handlers.go
    - internal/api/handlers_test.go

key-decisions:
  - "maxImagePixels declared as uint64 (wider than maxUploadByte's int64) with an explicit comment explaining why — the comparison operand is a product of two uint32 declared dimensions and must not wrap under narrower/signed arithmetic (Pitfall 1)"
  - "Widened pngBytesFixture from a 16-byte Sniff-only prefix to a full 24-byte IHDR declaring 100x100 — required because Dimensions() needs the complete chunk, and every existing test consuming pngBytesFixture (TestCreateJob_OK, TestCreateJob_ContentMismatch, TestCreateJob_UnsupportedPair) still passes since those tests reject before reaching the Dimensions check (mismatch/pair-check both precede it) or, for the happy path, declare an in-limit size"

requirements-completed: [VALID-03]

# Metrics
duration: ~6min
completed: 2026-07-09
---

# Phase 7 Plan 2: Wire Dimension Check into handleCreateJob Summary

**MAX_IMAGE_PIXELS config/env wiring plus a handleCreateJob gate that calls convert.Dimensions between the format pair-check and callback_url validation, rejecting 422 before any storage write when declared pixel dimensions are unknown or exceed the configurable 100-megapixel default.**

## Performance

- **Duration:** ~6 min
- **Completed:** 2026-07-09
- **Tasks:** 2 completed
- **Files modified:** 5 (0 created)

## Accomplishments
- `api.Config` gained `MaxImagePixels uint64`; `api.Server` gained `maxImagePixels uint64` with a documented-wider-type rationale; `NewServer` applies a 100,000,000 (100 megapixel) zero-value default (D-05).
- `cmd/api/main.go` parses `MAX_IMAGE_PIXELS` via the existing `envInt64` helper (cast to `uint64`), reusing the helper's trailing-inline-comment tolerance — no new helper added.
- `.env.example` documents `MAX_IMAGE_PIXELS=100000000` immediately after `MAX_UPLOAD_BYTES`, matching the file's existing inline-comment doc style.
- `handleCreateJob` now calls `convert.Dimensions(detected, rest)` immediately after the `Supports` pair-check and before `callback_url` validation (D-06 ordering): on `ErrDimensionsUnknown` it logs `reason=dimensions_unknown` and returns 422 (fail-closed, D-07); otherwise it computes `uint64(width) * uint64(height)` (Pitfall 1 overflow-safe) and rejects 422 with `reason=dimension_limit` when it exceeds `s.maxImagePixels`. `rest` is reassigned to `Dimensions`' returned reader so the full original stream still reaches `s.storage.Upload` unmodified.
- `pngBytesFixture` widened to a full 24-byte IHDR (100x100, in-limit) so the existing happy-path test still exercises the new Dimensions gate successfully; two new fixtures (`oversizedPNGFixture` at 20000x20000, `truncatedIHDRPNGFixture` with a non-"IHDR" chunk type) drive the two new tests.
- `TestCreateJob_DimensionLimitExceeded` (constructs a `Server` directly with `Config{MaxImagePixels: 1_000_000}` so the oversized fixture triggers rejection) and `TestCreateJob_DimensionsUnknown` both assert 422 + `store.uploaded == false` + `repo.created == nil`.
- Full module test suite (`go test ./...`), `go vet ./...`, and `gofmt -l` all pass clean; no `go.mod`/`go.sum` changes.

## Task Commits

Each task was committed atomically:

1. **Task 1: Config + env wiring for MAX_IMAGE_PIXELS** - `2f861e5` (feat)
2. **Task 2: handleCreateJob dimension check + handler rejection tests** - `b844eb8` (feat)

## Files Created/Modified
- `internal/api/api.go` - `Config.MaxImagePixels`, `Server.maxImagePixels` (uint64, with wider-type rationale comment), 100-megapixel zero-value default in `NewServer`
- `cmd/api/main.go` - `MaxImagePixels: uint64(envInt64("MAX_IMAGE_PIXELS", 100_000_000))` added to the `api.Config` literal
- `.env.example` - documented `MAX_IMAGE_PIXELS=100000000` line
- `internal/api/handlers.go` - `handleCreateJob` dimension-check block (convert.Dimensions call, dimensions_unknown/dimension_limit rejections, `rest` reassignment)
- `internal/api/handlers_test.go` - widened `pngBytesFixture`, new `oversizedPNGFixture`/`truncatedIHDRPNGFixture` builders, `TestCreateJob_DimensionLimitExceeded`, `TestCreateJob_DimensionsUnknown`

## Decisions Made
- Declared `maxImagePixels` as `uint64` even though the sibling `maxUploadByte` is `int64`, with an explicit comment — deliberate width mismatch driven by Pitfall 1 (product of two `uint32`s), not an inconsistency.
- Widened the shared `pngBytesFixture` rather than introducing a separate "Dimensions-capable" fixture, since the plan's acceptance criteria required the existing happy-path test to keep passing against the new gate — this keeps `TestCreateJob_OK` exercising the full real pipeline (Sniff -> mismatch -> pair-check -> Dimensions -> upload -> create -> enqueue) rather than special-casing it.
- `TestCreateJob_DimensionLimitExceeded` constructs its `Server` directly via `NewServer(..., Config{MaxImagePixels: 1_000_000})` (following the file's existing direct-`NewServer`-call precedent, e.g. `TestHealthz_Degraded`) rather than extending the shared `newTestServer` helper's `Config{}` literal, to keep the low pixel limit scoped to only the tests that need it.

## Deviations from Plan

### Auto-fixed Issues

None — no bugs, missing functionality, or blocking issues were encountered; the interfaces provided by Plan 07-01 (`convert.Dimensions`, `convert.ErrDimensionsUnknown`) matched RESEARCH.md/PATTERNS.md exactly.

### Minor discrepancy (informational, not a defect)

- **Acceptance-criteria grep count mismatch:** the plan's acceptance criteria state `grep -c 'convert.Dimensions' internal/api/handlers.go` should return 1; the actual result is 2. This is because the inserted code block's doc comment (copied verbatim from RESEARCH.md's own "Handler integration point" code example) itself contains the substring `convert.Dimensions` in prose ("convert.Dimensions re-stitches its own bounded peek onto rest...") in addition to the actual call site (`convert.Dimensions(detected, rest)`). RESEARCH.md's own code example has this same two-occurrence shape, so this is a copy-paste-consistent minor plan-authoring inconsistency, not an implementation defect — the functional requirement (dimension check present, correctly placed) is fully satisfied and verified by the passing test suite.

Or, functionally: plan executed exactly as written — the one grep-count discrepancy above does not affect correctness, security, or any success criterion.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. Operators who want a non-default pixel-count ceiling can set `MAX_IMAGE_PIXELS` in their `.env`; the documented default (100,000,000 / 100 megapixels) applies otherwise.

## Next Phase Readiness

- VALID-03 is fully closed: `handleCreateJob` rejects decompression-bomb-shaped and undeterminable-dimension uploads with 422 before any storage write or job enqueue, and accepts within-limit uploads unchanged (202, full pipeline).
- This was the second and final plan of Phase 7. No blockers for milestone v1.1 progression.

---
*Phase: 07-image-dimension-limit-decompression-bomb-protection*
*Completed: 2026-07-09*

## Self-Check: PASSED

All created/modified files verified present; all task commit hashes (2f861e5, b844eb8, b204d0b) verified present in git log.
