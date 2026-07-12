---
phase: 07-image-dimension-limit-decompression-bomb-protection
verified: 2026-07-09T01:20:00Z
status: passed
score: 4/4 success criteria verified (11/11 combined PLAN must_haves truths verified)
overrides_applied: 0
---

# Phase 7: Image Dimension Limit (Decompression-Bomb Protection) Verification Report

**Phase Goal:** Operators are protected from decompression-bomb uploads — the API rejects images whose declared pixel dimensions exceed a configured limit before any conversion work begins.
**Verified:** 2026-07-09
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Uploading an image whose declared width × height exceeds the configured limit is rejected with 422 before the file is written to S3 and before a conversion job is enqueued | ✓ VERIFIED | `internal/api/handlers.go:156-170` — `convert.Dimensions` call + `uint64(width) * uint64(height) > s.maxImagePixels` check runs *before* `s.storage.Upload` (line 189) and `s.queue.EnqueueImageConvert` (line 218). `TestCreateJob_DimensionLimitExceeded` (handlers_test.go:323-345) independently re-run: PASS, asserts `rec.Code==422`, `store.uploaded==false`, `repo.created==nil`. |
| 2 | Uploading an image within the configured dimension limit succeeds and proceeds through the existing pipeline unaffected | ✓ VERIFIED | `TestCreateJob_OK` (handlers_test.go:187-218) uses the widened 24-byte `pngBytesFixture()` (100×100, well under the 100MP default) and independently re-run: PASS — 202 Accepted, `store.uploaded==true`, job created, enqueued. |
| 3 | The dimension limit is configurable via environment variable, with a documented sane default | ✓ VERIFIED | `cmd/api/main.go:101` — `MaxImagePixels: uint64(envInt64("MAX_IMAGE_PIXELS", 100_000_000))`; `.env.example:17` — `MAX_IMAGE_PIXELS=100000000   # decompression-bomb guard: max declared width*height (default 100 megapixels, e.g. 10000x10000)`; `internal/api/api.go:87-89` — zero-value default of 100,000,000 in `NewServer` as a defense-in-depth fallback. |
| 4 | The check parses actual pixel dimensions from file headers across all currently-supported formats (png/jpg/webp/heic/tiff), rather than trusting magic-byte format detection alone | ✓ VERIFIED | `internal/convert/dimensions.go:37-43` — `dimensionParsers` map registers exactly the 5 formats matching `sniff.go`'s closed signature table (`png`, `jpg`, `webp`, `heic`, `tiff` — cross-checked against `sniff.go:35-39`). Each parser reads real header fields (PNG IHDR, JPEG SOF0, WebP VP8X/VP8 /VP8L, TIFF IFD tags 256/257, HEIC `ispe` box) — not the Sniff magic-byte signature. Full byte-fixture test suite independently re-run: 26 `TestDimensions*` cases PASS. |

**Score:** 4/4 truths verified

### PLAN Frontmatter Must-Haves (Detail Level)

07-01 (parser) truths — all verified by direct code read + independent test re-run:
- `convert.Dimensions` parses all 5 formats reading only header fields, zero new dependency — VERIFIED (`git diff --stat` on go.mod/go.sum across the full phase 7 commit range `6fbdaa5..25d4738` shows no changes).
- Fail-closed on undeterminable dimensions (`ErrDimensionsUnknown`, no buffer growth/seek/panic) — VERIFIED (`TestDimensionsTIFF_IFDBeyondWindowFailsClosed`, `TestDimensionsHEICTruncatedFailsClosed`, `TestDimensionsHEICMalformedNoPanic`, `TestDimensionsShortInputNoPanic` all pass).
- `rest` reader reproduces the full original stream — VERIFIED (`TestDimensionsPreservesFullStream` passes, asserts byte-for-byte equality).
- uint32 return types, bounds-checked, no panics — VERIFIED by code read (every slice access in `dimensions.go` is preceded by an explicit `len(b) < N` / `uint64(off)+N > uint64(len(b))` check).
- JPEG SOF scan excludes DHT/JPG/DAC — VERIFIED, see Pitfall (a) below.

07-02 (handler wiring) truths — all verified:
- Rejects 422 before `s.storage.Upload`/`queue.EnqueueImageConvert` — VERIFIED (code order + test).
- Within-limit upload proceeds unchanged (202) — VERIFIED (`TestCreateJob_OK`).
- Configurable via `MAX_IMAGE_PIXELS`, documented default 100 megapixels — VERIFIED.
- Undeterminable dimensions rejected 422, fail-closed, client_id-tagged log — VERIFIED (`TestCreateJob_DimensionsUnknown`; `handlers.go:158` logs `client_id=%s filename=%q reason=dimensions_unknown`).
- uint64(width)*uint64(height) product computation — VERIFIED, see overflow check below.
- Full original file reaches `s.storage.Upload` (rest reassigned) — VERIFIED (`handlers.go:163` `rest = dimRest`).

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/dimensions.go` | `Dimensions()`, `dimPeekLen`, `ErrDimensionsUnknown`, 5 parsers, `walkBoxes` | ✓ VERIFIED | All present, exported correctly, `go build`/`go vet` clean. |
| `internal/convert/dimensions_test.go` | Byte-fixture tests per format + edge cases | ✓ VERIFIED | 26 test functions covering PNG/WebP(x3)/JPEG(DHT)/TIFF(LE/BE/LONG/fail-closed)/HEIC(single/multi-max/truncated/malformed)/overflow/walkBoxes-extended-size/stream-preservation. |
| `internal/api/api.go` | `Config.MaxImagePixels`, `Server.maxImagePixels`, 100M default | ✓ VERIFIED | Lines 61, 70, 87-89, 103. |
| `cmd/api/main.go` | `MAX_IMAGE_PIXELS` env wiring | ✓ VERIFIED | Line 101, reuses `envInt64` helper, cast to uint64. |
| `.env.example` | Documented `MAX_IMAGE_PIXELS` line | ✓ VERIFIED | Line 17. |
| `internal/api/handlers.go` | `convert.Dimensions` call + 422 rejections between pair-check and callback_url validation | ✓ VERIFIED | Lines 152-170, correctly ordered. |
| `internal/api/handlers_test.go` | New rejection tests + widened happy-path fixture | ✓ VERIFIED | `pngBytesFixture` widened to full 24-byte IHDR (100×100); `oversizedPNGFixture`, `truncatedIHDRPNGFixture` added; `TestCreateJob_DimensionLimitExceeded`, `TestCreateJob_DimensionsUnknown` added. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| `handleCreateJob` | `convert.Dimensions(detected, rest)` | Call after Supports pair-check, before callback_url validation | ✓ WIRED | `handlers.go:146-156` — pair-check at 146, Dimensions call at 156, callback_url validation at 175. Order matches D-06. |
| `handleCreateJob` | `s.maxImagePixels` comparison | `uint64(width)*uint64(height) > s.maxImagePixels` → 422 | ✓ WIRED | `handlers.go:164-170`. |
| `cmd/api/main.go` | `api.Config.MaxImagePixels` | `uint64(envInt64("MAX_IMAGE_PIXELS", 100_000_000))` | ✓ WIRED | `cmd/api/main.go:101`. |
| `internal/convert/dimensions.go Dimensions()` | `dimensionParsers` map | Map lookup + parser(buf) call | ✓ WIRED | `dimensions.go:59-67`. |

### Data-Flow Trace (Level 4)

Not applicable in the UI-rendering sense — this is a security gate, not a rendering pipeline. The relevant flow (declared width/height → uint64 product → comparison against configured limit → HTTP 422/pipeline continuation) was traced end-to-end above and confirmed via passing integration-style handler tests (`TestCreateJob_DimensionLimitExceeded`, `TestCreateJob_DimensionsUnknown`, `TestCreateJob_OK`) that exercise the full `handleCreateJob` code path through `srv.Routes().ServeHTTP`, not mocked at the dimension-check boundary.

### Behavioral Spot-Checks (Independent Re-Run, not SUMMARY-trusted)

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `go build ./...` | `go build ./...` | no output, exit 0 | ✓ PASS |
| `go vet ./...` | `go vet ./...` | no output, exit 0 | ✓ PASS |
| `internal/convert` full test suite | `go test ./internal/convert/... -v` | 37 tests, all PASS | ✓ PASS |
| `internal/api` TestCreateJob suite | `go test ./internal/api/... -v -run TestCreateJob` | 8 tests, all PASS | ✓ PASS |
| Full module test suite | `go test ./...` | all packages ok | ✓ PASS |
| `gofmt -l` on phase-touched files | `gofmt -l dimensions.go dimensions_test.go api.go handlers.go handlers_test.go main.go` | no output (clean) | ✓ PASS |
| No new go.mod/go.sum entries across phase 7 | `git diff --stat 6fbdaa5 25d4738 -- go.mod go.sum` | no output (no changes) | ✓ PASS |

### Research Pitfall Verification (explicit, code-level)

**(a) JPEG SOF marker range must exclude DHT(0xFFC4)/JPG(0xFFC8)/DAC(0xFFCC):**
`internal/convert/dimensions.go:155-156`:
```go
isSOF := marker >= 0xC0 && marker <= 0xCF &&
    marker != 0xC4 && marker != 0xC8 && marker != 0xCC
```
Confirmed correct. `TestDimensionsJPEG` uses a fixture with SOI → DHT(0xFFC4) → APP0 → SOF0, independently re-run: PASS, asserts width=400/height=300 (not DHT garbage bytes). A second dedicated test, `TestDimensionsJPEG_DHTMarkersExcludedFromSOFRange`, constructs a fixture with DHT/JPG/DAC markers and NO true SOF, asserting `jpegDimensions` returns `ok=false` rather than false-matching one of the excluded markers — independently re-run: PASS. **VERIFIED, not just plan-mentioned.**

**(b) WebP simple-lossy FourCC must compare against 4-byte "VP8 " (trailing space), not 3-byte "VP8":**
`internal/convert/dimensions.go:90-99` — `fourCC := string(b[12:16])` (4-byte slice) and `case "VP8 "` (4-char literal with trailing space) in the switch. `TestDimensionsWebP_VP8_NoTrailingSpaceFourCCNotMatched` constructs a non-matching `"VP8?"` FourCC and asserts `ErrDimensionsUnknown` — independently re-run: PASS. **VERIFIED.**

**(c) TIFF must fail closed (422, not seek/grow buffer) when the IFD offset falls outside the 64KiB bounded peek:**
`internal/convert/dimensions.go:194-197`:
```go
ifdOffset := bo.Uint32(b[4:8])
if uint64(ifdOffset)+2 > uint64(len(b)) {
    return 0, 0, false // IFD beyond bounded window: fail closed (D-07)
}
```
No `Seek` call anywhere in the file (confirmed by reading the full file — only `bytes`, `encoding/binary`, `errors`, `io` are imported, and `io.ReadFull`/`io.MultiReader` are the only I/O operations). `TestDimensionsTIFF_IFDBeyondWindowFailsClosed` sets IFD offset to `0xFFFFFFFF` and asserts `ErrDimensionsUnknown` — independently re-run: PASS. This propagates to a 422 in the handler layer since `Dimensions()` returns `ErrDimensionsUnknown` on any parser failure, which `handleCreateJob` maps to `http.StatusUnprocessableEntity`. **VERIFIED.**

**Overflow protection is uint64, not uint32 (both files):**
- `internal/convert/dimensions.go` — parsers return `uint32` width/height (never compute a product internally); `TestDimensionsOverflow` computes `uint64(w) * uint64(h)` in the test itself and asserts `18446744065119617025` (no wraparound) for `w=h=0xFFFFFFFF` — independently re-run: PASS.
- `internal/api/handlers.go:164` — `totalPixels := uint64(width) * uint64(height)` — confirmed by direct code read, both operands explicitly cast to `uint64` before multiplication (not `width*height` computed in a narrower type first). **VERIFIED in both locations.**

**HEIC ispe box-walk takes the MAX of all ispe boxes (documented conservative simplification, not full pitm/ipma resolution):**
`internal/convert/dimensions.go:244-277` (`heicDimensions`) walks `ftyp → meta → iprp → ipco → ispe`, and for every `ispe` box found compares `uint64(w)*uint64(h) > uint64(maxW)*uint64(maxH)` to keep the max. The doc comment explicitly states the pitm/ipma primary-item resolution is deliberately NOT implemented, and explains why this is security-conservative (over-rejects, never under-protects). `grep -c 'ipma' internal/convert/dimensions.go` returns 0 (comment avoids the literal substring while documenting the tradeoff in prose, per the SUMMARY's noted rewrite) — confirmed this is not evidence of a missing implementation, since the surrounding prose ("primary-item and item-property-association boxes") clearly documents the same tradeoff without the literal substring. `TestDimensionsHEIC_MultipleTakesMax` constructs 3 ispe boxes (320×240, 4032×3024, 160×120) and asserts the max is returned — independently re-run: PASS. **VERIFIED.**

### Executor-Flagged Deviation Check

**07-02's "grep count mismatch" (expected 1, found 2 for `convert.Dimensions` in handlers.go):**
Confirmed genuinely harmless. `grep -n "convert.Dimensions" internal/api/handlers.go` returns two lines:
- Line 154: a doc comment — `// convert.Dimensions re-stitches its own bounded peek onto rest, so the`
- Line 156: the actual call site — `width, height, dimRest, err := convert.Dimensions(detected, rest)`

There is exactly one call site; the second match is prose in an adjacent comment, not a duplicate or misplaced call. **Confirmed harmless, not a defect.**

### pngBytesFixture Regression Check

The planner flagged a self-identified regression risk: the pre-existing `pngBytesFixture` test helper was only 16 bytes (a Sniff-only prefix stopping mid-IHDR), which would now fail `convert.Dimensions` parsing (which needs the full 24-byte IHDR chunk) and turn `TestCreateJob_OK` (and other tests reusing the fixture) into false-422s.

Confirmed fixed: `internal/api/handlers_test.go:112-120` — `pngBytesFixture()` now returns the full 24 bytes (8-byte signature + 4-byte chunk length + 4-byte "IHDR" + 4-byte width + 4-byte height = 24 bytes, declaring 100×100), and the doc comment explicitly explains why (`"Dimensions needs the full IHDR ... unlike a bare 16-byte Sniff-only prefix which stops mid-chunk"`). `TestCreateJob_OK`, `TestCreateJob_ContentMismatch`, and `TestCreateJob_UnsupportedPair` all still pass (the latter two reject before reaching the Dimensions check, at the mismatch/pair-check stages respectively — confirmed by re-reading their assertions, which check `store.uploaded==false`/`repo.created==nil` for reasons unrelated to dimensions). **VERIFIED, not left at the stale 16-byte version.**

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| VALID-03 | 07-01, 07-02 | API отклоняет загрузку, если заявленные размеры изображения превышают настраиваемый лимит, до запуска конвертации | ✓ SATISFIED | All 4 ROADMAP success criteria verified above; handler test suite independently re-run and passing. |

**Note on REQUIREMENTS.md bookkeeping:** `.planning/REQUIREMENTS.md` currently shows `VALID-03` as `[ ]` (Pending) and the traceability table marks it "Pending" — this is a documentation-sync lag, not a functional gap. Cross-referencing git history, Phases 5 and 6 each received their REQUIREMENTS.md checkbox update in a dedicated `docs(0X): add phase verification report; mark <REQ> done` commit created as part of/after their own verification pass; no equivalent commit yet exists for Phase 7 (this is that verification pass). This is expected finalization bookkeeping, not evidence the requirement is functionally incomplete — the code-level evidence above fully satisfies VALID-03's stated behavior. Recommend a follow-up commit checking off VALID-03 and updating the traceability table to "Done" as part of closing out this verification, consistent with the Phase 5/6 pattern.

### Anti-Patterns Found

None. Scanned all 6 phase-touched files (`internal/convert/dimensions.go`, `internal/convert/dimensions_test.go`, `internal/api/api.go`, `internal/api/handlers.go`, `internal/api/handlers_test.go`, `cmd/api/main.go`, `.env.example`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` and placeholder-language patterns — zero matches. `gofmt -l` clean on all files.

### Human Verification Required

None. All 4 success criteria and all pitfall/edge-case claims are independently verifiable via code reading and automated test execution; no visual, real-time, or external-service behavior is involved in this phase's deliverable.

### Milestone v1.1 Close-Out Note

This is the last phase of milestone v1.1 ("Tech Debt Cleanup"). Status of all 4 v1.1 requirements as of this verification:

| Requirement | Phase | Verification Status | Score |
|-------------|-------|---------------------|-------|
| WEBHOOK-06 | Phase 5 | passed (`05-VERIFICATION.md`) | 4/4 |
| RECON-04 | Phase 6 | passed (`06-VERIFICATION.md`) | 4/4 |
| RECON-05 | Phase 6 | passed (`06-VERIFICATION.md`) | 4/4 |
| VALID-03 | Phase 7 | passed (this report) | 4/4 |

All 4 of v1.1's requirements are now fully implemented and independently verified against the codebase (not just SUMMARY.md claims). `.planning/REQUIREMENTS.md`'s VALID-03 checkbox and traceability status are stale (see note above) and should be updated to `[x]` / "Done" as a follow-up bookkeeping step — this does not block milestone close-out, since the underlying functionality is confirmed working. Milestone v1.1 is ready for `/gsd-audit-milestone` / close-out.

### Gaps Summary

No gaps found. All 4 ROADMAP success criteria are independently verified against the actual codebase (not SUMMARY.md claims), all 3 named research pitfalls (JPEG DHT/JPG/DAC exclusion, WebP "VP8 " trailing-space FourCC, TIFF fail-closed IFD-offset bounds) are confirmed correctly implemented and test-covered, the uint64 overflow protection is confirmed in both `dimensions.go` and `handlers.go`, the HEIC max-ispe simplification is confirmed correctly implemented and documented, the executor-flagged grep-count deviation is confirmed harmless, and the self-flagged `pngBytesFixture` regression risk is confirmed resolved. All tests were independently re-run by this verifier (not trusted from SUMMARY.md) and pass. The only finding is a stale REQUIREMENTS.md checkbox/traceability entry, which is expected pre-verification bookkeeping lag consistent with how Phases 5 and 6 were closed out, not a functional gap.

---

*Verified: 2026-07-09*
*Verifier: Claude (gsd-verifier)*
