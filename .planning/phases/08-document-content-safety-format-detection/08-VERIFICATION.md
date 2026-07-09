---
phase: 08-document-content-safety-format-detection
verified: 2026-07-09T01:49:16Z
status: passed
score: 4/4 must-haves verified
overrides_applied: 0
---

# Phase 8: Document Content Safety & Format Detection Verification Report

**Phase Goal:** The API can tell a genuine office document from a spoofed or hostile one, and rejects anything unsafe before it touches storage or the conversion engine.
**Verified:** 2026-07-09T01:49:16Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP.md Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | API accepts docx/xlsx/pptx/odt/ods/odp uploads and structurally disambiguates them (ZIP/OOXML central-directory check, ODF fixed-offset `mimetype` check) instead of trusting extension or generic ZIP signature | VERIFIED | `internal/convert/docsniff.go:29-42` defines closed `ooxmlRootParts` (root-part-presence-by-name: `word/document.xml`→docx, `xl/workbook.xml`→xlsx, `ppt/presentation.xml`→pptx) and `odfMimetypes` (index-0 `mimetype`, `Method==zip.Store`, exact OASIS media-type payload). `SniffContainer` (`docsniff.go:62-99`) implements this in a single `zip.NewReader` pass. Verified NOT the disproven first-entry-inflation approach — `TestSniffContainer_PPTX` uses `ooxmlZipFixture` which inserts 7 filler entries before the target root part (root part lands at central-directory index 8), directly proving position-independence (`internal/convert/docsniff_test.go:29-42,170-179`). `internal/api/handlers.go:126-159` wires this into `handleCreateJob` before the unrecognized-content 422. All 6 formats independently unit-tested (`TestSniffContainer_DOCX/XLSX/PPTX/ODT`) and independently re-run by this verifier: PASS. |
| 2 | API rejects with 422, before any S3 write, an upload whose structural content doesn't match its claimed office format | VERIFIED | `handlers.go:169-176`: existing `detected != source` honesty check applies to the now-correctly-`detected` format (docx/xlsx/etc. instead of ""). Duplicate-root-part / non-zip / unrecognized zips leave `detected` empty and fail closed to the pre-existing `unrecognized_content` 422 at `handlers.go:160-168`, which occurs before `s.storage.Upload` (`handlers.go:230`) and before `s.repo.Create` (`handlers.go:237`). `TestCreateJob_DuplicateRootPartRejected` and `TestCreateJob_BareZipUnrecognized` assert `store.uploaded==false` and `repo.created==nil` and both independently re-run: PASS. |
| 3 | API rejects with 422 an office document whose declared uncompressed ZIP size exceeds a configurable limit (zip-bomb guard), before conversion | VERIFIED | `docsniff.go:71-72` sums `f.UncompressedSize64` across **every** central-directory entry (not per-entry) into `res.TotalUncompressed`, zero decompression. `handlers.go:140-145` rejects 422 with `reason=zip_bomb` when `cr.TotalUncompressed > s.maxDocumentUncompressedBytes`, before `Upload`/`Create`. Limit is `MAX_DOCUMENT_UNCOMPRESSED_BYTES`, default `500 << 20` (524288000 = 500 MiB, matches D-04), wired `Config.MaxDocumentUncompressedBytes` (`api.go:75,96-97`) → `Server.maxDocumentUncompressedBytes` (`api.go:61,66,113`) → `cmd/api/main.go:102` via `envInt64("MAX_DOCUMENT_UNCOMPRESSED_BYTES", 500<<20)` → `.env.example:18` (`MAX_DOCUMENT_UNCOMPRESSED_BYTES=524288000`). `TestSniffContainer_ZipBombDeclaredSize` (sum-across-entries proof, real deflate-compressible content, not a spoofed field) and `TestCreateJob_ZipBombRejected` (`store.uploaded==false`, `repo.created==nil`) both independently re-run: PASS. |
| 4 | API rejects with 422 an office document containing macro parts (`vbaProject.bin` / Basic-script manifest) | VERIFIED | `docsniff.go:48-52` closed `ooxmlMacroParts` set (`word\|xl\|ppt/vbaProject.bin`) plus `hasBasicPrefix` (`docsniff.go:106-108`, `Basic/` prefix) both checked in the same single pass (`docsniff.go:74-77`), setting `res.HasMacro`. `handlers.go:146-153` rejects 422 with `reason=macro_detected`, **unconditional** — grepped `.env.example`, `internal/api/`, `cmd/api/` for any `MACRO`-named env var/opt-out: zero hits, confirming D-05 (always-on, no opt-out). `TestSniffContainer_MacroDetectedOOXML`, `TestSniffContainer_MacroDetectedODF`, `TestCreateJob_MacroRejected` (asserts `store.uploaded==false`/`repo.created==nil`) all independently re-run: PASS. |

**Score:** 4/4 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/docsniff.go` | `SniffContainer(io.ReaderAt,int64)` + `ContainerResult` + `ErrNotAZip` | VERIFIED | Exists, exact signature `func SniffContainer(r io.ReaderAt, size int64) (ContainerResult, error)` at line 62. Single `zip.NewReader` call (grep-confirmed count=1). Imports only `archive/zip`, `errors`, `io` — no internal deps, zero new dependencies. |
| `internal/convert/docsniff_test.go` | Unit tests for all detection/rejection branches | VERIFIED | 15 `TestSniffContainer_*` cases covering DOCX/XLSX/PPTX/ODT(×3 mimetypes)/bare-zip/zip-bomb-sum/macro-OOXML/macro-ODF/duplicate-root-part/duplicate-macro-part/not-a-zip. All independently re-run by this verifier: PASS. |
| `internal/convert/dimensions.go` | `HasDimensionLimit` predicate | VERIFIED | `func HasDimensionLimit(format string) bool` at line 76, body `_, ok := dimensionParsers[NormalizeFormat(format)]; return ok`. `dimensionParsers` map confirmed unchanged (png/jpg/webp/heic/tiff only, `dimensions.go:37-43`). |
| `internal/api/handlers.go` | SniffContainer branch + zip-bomb/macro 422 rejections + HasDimensionLimit guard | VERIFIED | Container-inspection branch at lines 126-159 (reads `file.ReadAt` for the 4-byte PK-prefix check, then `convert.SniffContainer(file, header.Size)` — never `rest`). Dimension block wrapped in `if convert.HasDimensionLimit(detected)` at line 195. |
| `internal/api/api.go`, `cmd/api/main.go`, `.env.example` | `MaxDocumentUncompressedBytes` config wiring | VERIFIED | All four touch-points present and independently grep-confirmed (see Truth 3 evidence). |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/convert/docsniff.go` | `archive/zip.NewReader` | single central-directory pass | WIRED | `grep -c ':= zip\.NewReader(' docsniff.go` == 1 |
| `internal/convert/dimensions.go` | `dimensionParsers` | map membership check | WIRED | `HasDimensionLimit` reads the same package-level map `Dimensions()` uses |
| `internal/api/handlers.go` | `convert.SniffContainer` | call on original `multipart.File` + `header.Size` | WIRED | `handlers.go:135`: `convert.SniffContainer(file, header.Size)`. Confirmed `grep -c 'SniffContainer(rest' internal/api/handlers.go` == 0 — never passes the `io.MultiReader` (`rest`), always the original `io.ReaderAt`-implementing `file`. |
| `internal/api/handlers.go` | `convert.HasDimensionLimit` | guard wrapping existing `Dimensions()` block | WIRED | `handlers.go:195`: `if convert.HasDimensionLimit(detected) { ... }` wraps the entire prior-unconditional block (lines 196-210). |
| `cmd/api/main.go` | `api.Config.MaxDocumentUncompressedBytes` | `envInt64` wiring | WIRED | `cmd/api/main.go:102` |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|---------------------|--------|
| `SniffContainer` result (`cr`) | `cr.Format`/`cr.TotalUncompressed`/`cr.HasMacro`/`cr.DuplicateRootPart` | `zip.NewReader(file, header.Size)` central-directory entries — real ZIP structure parsed from the actual uploaded bytes, zero hardcoded/static values | Yes | FLOWING |
| `detected` (used for `SourceFormat` in `jobs.CreateParams`, `contentType`, `Supports()` check) | Set from `cr.Format` when safety checks pass | Traced end-to-end: `cr.Format` → `detected` (handlers.go:137) → `convert.MIMEType(detected)` (handlers.go:228) → `jobs.CreateParams.SourceFormat` (handlers.go:242) | Yes | FLOWING |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|--------------|--------|----------|
| DOC-01 | 08-01, 08-02 | Structural docx/xlsx/pptx/odt/ods/odp acceptance + content-mismatch 422 before S3 write | SATISFIED | Truths 1-2 above |
| DOC-02 | 08-01, 08-02 | Zip-bomb declared-size rejection before conversion | SATISFIED | Truth 3 above |
| DOC-03 | 08-01, 08-02 | Macro-part rejection with 422 | SATISFIED | Truth 4 above |

**Note (informational, not a gap):** `.planning/REQUIREMENTS.md` lines 12-14 and 63-65 still show DOC-01/02/03 as `[ ]` unchecked / status "Pending" in the tracking table. This is a documentation-tracking artifact, not a code deficiency — no requirement in REQUIREMENTS.md across the entire v1.2 milestone is checked `[x]` yet (this appears to be updated at milestone-completion time in this project's convention, not per-phase). Does not affect the phase verdict; flagged for the developer's awareness in case the project intends per-phase updates.

### Anti-Patterns Found

None. Scanned all phase-modified files (`internal/convert/docsniff.go`, `internal/convert/docsniff_test.go`, `internal/convert/dimensions.go`, `internal/convert/dimensions_test.go`, `internal/api/handlers.go`, `internal/api/handlers_test.go`, `internal/api/api.go`, `cmd/api/main.go`, `.env.example`) for `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER`/"not yet implemented"/"coming soon" — zero matches.

### Behavioral Spot-Checks / Independent Test Re-Run

Per the task instructions, this verifier independently re-ran the test suites rather than trusting SUMMARY.md claims (this phase is a security control):

| Check | Command | Result | Status |
|-------|---------|--------|--------|
| `go build ./...` | full repo build | clean, no errors | PASS |
| `go vet ./...` | full repo vet | clean, no findings | PASS |
| `internal/convert` package tests | `go test ./internal/convert/... -count=1 -v` | 15/15 `TestSniffContainer_*` + 4/4 `TestHasDimensionLimit_*` pass; full package (including pre-existing tests) green | PASS |
| `internal/api` package tests | `go test ./internal/api/... -count=1 -v` | All 24 tests pass including 6 new `TestCreateJob_*` document cases and all pre-existing image-path tests (no regression) | PASS |
| Full repo test suite | `go test ./... -count=1` | All 11 tested packages green, zero failures | PASS |
| `SniffContainer(rest` occurrence check | `grep -rn "SniffContainer(rest" internal/` | 0 occurrences | PASS |
| Macro opt-out env var check | `grep -rn "MACRO" .env.example internal/api/ cmd/api/` | 0 occurrences (confirms D-05: unconditional, no opt-out) | PASS |

### Deferred / Documented Non-Gap

Per the task's explicit guidance: the "documents skip the dimension check" behavior (must-have from 08-02-PLAN.md) is **not** independently exercisable end-to-end via `handleCreateJob` this phase, because `convert.Default.Supports()` already rejects every document format pair (confirmed: `internal/convert/converters.go` registers only `LibvipsConverter{}`; `LibvipsConverter.Pairs()` in `internal/convert/libvips.go:16-26` enumerates only `imageFormats` combinations — no document Converter exists until Phase 9) before the dimension-check block is ever reached. This is verified instead via:
1. Code inspection: `handlers.go:195` — `if convert.HasDimensionLimit(detected) { ... }` correctly wraps the entire former-unconditional block.
2. Isolated unit test: `TestHasDimensionLimit_Documents` (independently re-run, PASS) confirms `HasDimensionLimit` returns `false` for all 6 document formats.

This is a documented, intentional deferral (per 08-02-PLAN.md must_haves and plan-checker's accepted warning #3) — not treated as a gap, per task instructions.

### Human Verification Required

None. All success criteria are structurally verifiable via code inspection and automated tests; no visual/UX/external-service behavior is in scope for this phase.

### Gaps Summary

No gaps found. All 4 ROADMAP.md success criteria are VERIFIED against the actual codebase (not just SUMMARY.md claims), all key links are wired correctly (confirmed the critical `SniffContainer(rest` anti-pattern is absent), the duplicate-entry guard correctly covers both root-part and macro-part names (the specific fix noted from plan-checker warning #1), OOXML detection uses root-part-presence-by-name (not the disproven first-entry-inflation approach, with an explicit position-independence test), and the dimension-check regression is fixed and unit-tested. All tests were independently re-run by this verifier and pass; full repo build/vet/test suite is clean with zero regressions.

---

_Verified: 2026-07-09T01:49:16Z_
_Verifier: Claude (gsd-verifier)_
