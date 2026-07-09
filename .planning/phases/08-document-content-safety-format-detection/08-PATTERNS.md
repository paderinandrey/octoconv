# Phase 8: Document Content Safety & Format Detection - Pattern Map

**Mapped:** 2026-07-09
**Files analyzed:** 8 (2 new, 6 modified)
**Analogs found:** 8 / 8

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/convert/docsniff.go` (NEW) | utility (content-format detector) | transform (binary/container parsing) | `internal/convert/sniff.go` | role-match (closed dispatch table + peek idiom identical in spirit; container vs. prefix parsing differs per D-02) |
| `internal/convert/docsniff_test.go` (NEW) | test | transform | `internal/convert/sniff_test.go`, `internal/convert/dimensions_test.go` | exact (same package, same fixture-building test idiom) |
| `internal/convert/dimensions.go` (MODIFY — add `HasDimensionLimit`) | utility (predicate over existing dispatch table) | transform | `internal/convert/dimensions.go` (self, `dimensionParsers` map + `Dimensions()`) | exact |
| `internal/api/handlers.go` (MODIFY — `handleCreateJob`) | controller | request-response | `internal/api/handlers.go` (self, existing Sniff→pair-check→Dimensions sequence, lines 116-170) | exact |
| `internal/api/api.go` (MODIFY — `Config`/`Server`) | config/service-wiring | request-response | `internal/api/api.go` (self, existing `MaxImagePixels`/`maxImagePixels` field pair) | exact |
| `cmd/api/main.go` (MODIFY — env wiring) | config | request-response | `cmd/api/main.go` (self, existing `MaxImagePixels: uint64(envInt64(...))` line) | exact |
| `.env.example` (MODIFY) | config | — | `.env.example` (self, existing `MAX_IMAGE_PIXELS` line + comment style) | exact |
| `internal/api/handlers_test.go` (MODIFY — new test cases) | test | request-response | `internal/api/handlers_test.go` (self, `TestCreateJob_DimensionLimitExceeded`, `TestCreateJob_UnrecognizedContent`) | exact |

## Pattern Assignments

### `internal/convert/docsniff.go` (utility, transform) — NEW FILE

**Analogs:** `internal/convert/sniff.go` (peek/dispatch-table shape) + `internal/convert/dimensions.go` (fail-closed bounded-read + closed dispatch-table shape, citation-comment style)

**Package/imports pattern** (`internal/convert/sniff.go:1-6`, `internal/convert/dimensions.go:1-10`):
```go
package convert

import (
	"bytes"
	"io"
)
```
Docsniff will additionally need `"archive/zip"` and `"errors"` (for a new sentinel, mirroring `dimensions.go`'s `ErrDimensionsUnknown`). No project-internal imports needed — `internal/convert` has no dependents inside itself; keep this file dependency-free like its siblings.

**Closed dispatch table pattern** (`internal/convert/sniff.go:31-40`, `internal/convert/dimensions.go:34-43`):
```go
// signatures is the hardcoded, closed detection table (D-03) scoped to
// exactly the formats registered in convert.Default (imageFormats in
// libvips.go): png, jpg, webp, heic, tiff.
var signatures = []signature{
	{"png", matchPNG},
	{"jpg", matchJPEG},
	{"webp", matchWebP},
	{"heic", matchHEIC},
	{"tiff", matchTIFF},
}
```
Follow this exact shape for `ooxmlRootParts`/`odfMimetypes`/`ooxmlMacroParts` maps — package-level `var`, comment naming the exact closed set, one line per entry.

**Citation-comment style for spec-grounded parsing** (`internal/convert/dimensions.go:70-71`, `:119-123`, `:173-180`):
```go
// pngDimensions reads the IHDR chunk's width/height fields.
// Source: https://www.w3.org/TR/png-3/#11IHDR (IHDR chunk layout)
func pngDimensions(b []byte) (uint32, uint32, bool) {
```
Every new format-detection function in `docsniff.go` (ODF mimetype check, OOXML root-part check, macro-part check) must open with a doc comment starting with the function name plus a `// Source: <spec/RFC>` line, exactly this style — e.g. `// Source: OASIS OpenDocument v1.2 Part 3 §17.4` for the ODF check.

**Fail-closed sentinel pattern** (`internal/convert/dimensions.go:23-27`):
```go
// ErrDimensionsUnknown is returned when a registered format's declared
// pixel dimensions could not be located within the bounded peek window —
// treated as a rejection (D-07), not a fallback accept, since this is a
// resource-exhaustion security control.
var ErrDimensionsUnknown = errors.New("cannot determine declared image dimensions")
```
Follow this for a new `ErrNotAZip`-style sentinel (RESEARCH.md's Code Examples section already names it `ErrNotAZip`) if `SniffContainer` needs to distinguish "not a zip at all" from "zip but unrecognized office format" — decide based on whether `handleCreateJob` needs to branch on that distinction (per CONTEXT.md it likely does not; a plain `detected==""` fallthrough to the existing D-02 path may suffice, matching Pattern 2/5 of RESEARCH.md's fall-through behavior).

**Peek-and-restitch idiom — explicitly NOT reusable here (read carefully)** (`internal/convert/sniff.go:78-93`, `internal/convert/dimensions.go:50-57`):
```go
func Sniff(r io.Reader) (detected string, rest io.Reader, err error) {
	buf := make([]byte, sniffLen)
	n, readErr := io.ReadFull(r, buf)
	...
	rest = io.MultiReader(bytes.NewReader(buf), r)
	...
}
```
`Sniff`/`Dimensions` both take `io.Reader` and return a re-stitched `rest`. **`SniffContainer` must NOT follow this exact signature** — RESEARCH.md Pattern 5 confirms `archive/zip.NewReader` requires `io.ReaderAt` + `size int64`, and `Sniff`'s `rest` (an `io.MultiReader`) does not implement `io.ReaderAt`. `SniffContainer` must accept `(r io.ReaderAt, size int64)` and read from the **original** `multipart.File`, not from `rest`. No re-stitching is needed on the `SniffContainer` side at all — `ReadAt` calls never disturb `file`'s sequential read cursor (verified in RESEARCH.md), so the existing `Sniff`-produced `rest` stream remains valid and unchanged for the final `s.storage.Upload` call.

**Recommended function signature** (RESEARCH.md's validated design, `internal/convert/docsniff.go` new):
```go
type ContainerResult struct {
	Format            string // "docx"|"xlsx"|"pptx"|"odt"|"ods"|"odp"|"" (unrecognized)
	TotalUncompressed uint64
	HasMacro          bool
	DuplicateRootPart bool // fail-closed signal (Pitfall 3) — reject if >1 root/macro part share a name
}

func SniffContainer(r io.ReaderAt, size int64) (ContainerResult, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return ContainerResult{}, ErrNotAZip
	}
	// single for _, f := range zr.File loop computing all four signals together
	// (Pitfall 4 — never call zip.NewReader more than once per upload)
	...
}
```
One `zip.NewReader` call, one loop, all four concerns computed together — this is a hard constraint from RESEARCH.md Pitfall 4, not a style preference.

---

### `internal/convert/docsniff_test.go` (test) — NEW FILE

**Analog:** `internal/convert/sniff_test.go`, `internal/convert/dimensions_test.go`

**Fixture-builder + table-style assertion pattern** (`internal/convert/sniff_test.go:9-18`, `dimensions_test.go:11-36`):
```go
func TestSniffPNG(t *testing.T) {
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
	detected, _, err := Sniff(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Sniff error: %v", err)
	}
	if detected != "png" {
		t.Fatalf("detected = %q, want png", detected)
	}
}
```
```go
// pngFixture builds a minimal PNG signature + IHDR chunk declaring width x
// height.
func pngFixture(width, height uint32) []byte {
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // signature
	...
	return data
}
```
For `docsniff_test.go`, build small hand-crafted in-memory ZIPs with Go's own `archive/zip.Writer` (not fixture byte literals — a real ZIP has a variable-length central directory, so building via `zip.Writer` into a `bytes.Buffer` is both correct and idiomatic; RESEARCH.md's own verification work built fixtures exactly this way). Needed fixture-builder helpers, one per scenario (mirrors `pngFixture`/`oversizedPNGFixture`/`truncatedIHDRPNGFixture`'s one-helper-per-scenario style):
- `ooxmlZipFixture(rootPart string)` → builds a minimal zip with e.g. `word/document.xml` present
- `odfZipFixture(mimetype string)` → builds a zip whose first entry is `mimetype`, `Method: zip.Store`, containing the given payload
- `zipBombFixture(declaredSize uint64)` → an entry whose `UncompressedSize64`-reporting content can be built via a real (small) deflate-compressed all-zeros payload, OR by directly constructing a `zip.FileHeader` with a spoofed `UncompressedSize64` if a cheaper unit-test fixture is preferred (decide based on whether `archive/zip.Writer` trusts caller-set sizes — verify before relying on this)
- `macroZipFixture(macroPartName string)` → a zip containing e.g. `word/vbaProject.bin` alongside `word/document.xml`
- `duplicateRootPartZipFixture()` → a zip with two entries named `word/document.xml`

Test names should follow the existing `Test<Function><Scenario>` convention: `TestSniffContainer_DOCX`, `TestSniffContainer_ODT`, `TestSniffContainer_ZipBombRejected` (or however the size-check is exposed), `TestSniffContainer_MacroDetected`, `TestSniffContainer_DuplicateRootPartRejected`, `TestSniffContainer_BareZipUnrecognized`.

---

### `internal/convert/dimensions.go` (MODIFY — add `HasDimensionLimit`)

**Analog:** self — `internal/convert/dimensions.go:34-43`

**Existing dispatch table to key off of:**
```go
var dimensionParsers = map[string]dimensionParser{
	"png":  pngDimensions,
	"jpg":  jpegDimensions,
	"webp": webpDimensions,
	"heic": heicDimensions,
	"tiff": tiffDimensions,
}
```

**New predicate to add** (per RESEARCH.md Pitfall 5 / CONTEXT.md's locked scope — documents skip the check entirely, they do NOT get a document-specific dimension check):
```go
// HasDimensionLimit reports whether format has a registered dimension
// parser (i.e. is an image format subject to the declared-pixel-dimension
// ceiling). Document formats (docx/xlsx/pptx/odt/ods/odp) have no pixel-
// dimension concept and must skip the check entirely — this predicate is
// the scope guard, not a new document-specific check.
func HasDimensionLimit(format string) bool {
	_, ok := dimensionParsers[NormalizeFormat(format)]
	return ok
}
```
Place this immediately after `Dimensions()` (after line 68), following the file's existing top-down order: constant → sentinel error → type → dispatch table → primary function → predicate/helper → per-format parsers.

---

### `internal/api/handlers.go` (MODIFY — `handleCreateJob`)

**Analog:** self, lines 116-170 (existing Sniff → mismatch-rejection → pair-check → Dimensions sequence)

**Exact insertion point and existing surrounding code** (`internal/api/handlers.go:116-170`):
```go
detected, rest, err := convert.Sniff(file)
if err != nil {
	writeError(w, http.StatusBadRequest, "invalid multipart form")
	return
}
if detected == "" {
	log.Printf("content validation rejected: client_id=%s filename=%q reason=unrecognized_content", client.ID, filename)
	writeError(w, http.StatusUnprocessableEntity,
		"unrecognized file content for "+filename)
	return
}
if detected != source {
	log.Printf("content validation rejected: client_id=%s filename=%q reason=mismatch declared=%s detected=%s", client.ID, filename, source, detected)
	writeError(w, http.StatusUnprocessableEntity,
		"declared format "+source+" does not match detected content "+detected)
	return
}

if !convert.Default.Supports(detected, target) {
	writeError(w, http.StatusUnprocessableEntity,
		"unsupported conversion: "+detected+" -> "+target)
	return
}

width, height, dimRest, err := convert.Dimensions(detected, rest)
if err != nil {
	log.Printf("content validation rejected: client_id=%s filename=%q reason=dimensions_unknown", client.ID, filename)
	writeError(w, http.StatusUnprocessableEntity,
		"cannot determine declared image dimensions for "+filename)
	return
}
rest = dimRest
totalPixels := uint64(width) * uint64(height)
if totalPixels > s.maxImagePixels {
	log.Printf("content validation rejected: client_id=%s filename=%q reason=dimension_limit width=%d height=%d limit=%d", client.ID, filename, width, height, s.maxImagePixels)
	writeError(w, http.StatusUnprocessableEntity,
		"declared image dimensions exceed configured limit")
	return
}
```

**Required changes, following this exact idiom:**

1. **New container-inspection branch** slotted right where `detected == ""` is currently handled (before the "unrecognized_content" rejection fires) — reads from the original `file`/`header.Size` (NOT `rest`, per docsniff.go's pattern above):
```go
if detected == "" {
	var prefix [4]byte
	if n, _ := file.ReadAt(prefix[:], 0); n == 4 && bytes.Equal(prefix[:], []byte{'P', 'K', 3, 4}) {
		cr, cerr := convert.SniffContainer(file, header.Size)
		if cerr == nil && cr.Format != "" && !cr.DuplicateRootPart {
			detected = cr.Format
			if cr.TotalUncompressed > s.maxDocumentUncompressedBytes {
				log.Printf("content validation rejected: client_id=%s filename=%q reason=zip_bomb declared_uncompressed=%d limit=%d", client.ID, filename, cr.TotalUncompressed, s.maxDocumentUncompressedBytes)
				writeError(w, http.StatusUnprocessableEntity, "declared uncompressed size exceeds configured limit")
				return
			}
			if cr.HasMacro {
				log.Printf("content validation rejected: client_id=%s filename=%q reason=macro_detected", client.ID, filename)
				writeError(w, http.StatusUnprocessableEntity, "macro-carrying documents are not accepted")
				return
			}
		}
	}
}
if detected == "" {
	// existing unrecognized_content rejection, unchanged
	...
}
```
Match the existing `log.Printf("content validation rejected: client_id=%s filename=%q reason=...")` structured-log convention exactly (`reason=<snake_case_tag>`) for every new rejection branch — this is the D-08 logging convention already used four times in this function.

2. **Guard the existing dimension-check block** with the new `HasDimensionLimit` predicate — wrap lines 156-170 (quoted above) in:
```go
if convert.HasDimensionLimit(detected) {
	width, height, dimRest, err := convert.Dimensions(detected, rest)
	if err != nil {
		...
	}
	rest = dimRest
	totalPixels := uint64(width) * uint64(height)
	if totalPixels > s.maxImagePixels {
		...
	}
}
```
This is the exact fix for the confirmed regression (RESEARCH.md Pitfall 5) — documents skip the block entirely, `rest` stays as `Sniff`'s (or `SniffContainer`-validated) stream unmodified.

**Error-message discipline** (established throughout this handler): never leak internal error text; always a short fixed string via `writeError(w, status, "message")`, matching every existing call in this function.

---

### `internal/api/api.go` (MODIFY — `Config`/`Server`)

**Analog:** self — the existing `MaxImagePixels`/`maxImagePixels` field pair is the direct precedent for the new field.

**Struct field pattern** (`internal/api/api.go:56-61`, `68-74`, `87-89`, `102-104`):
```go
// Server
maxImagePixels uint64

// Config
MaxImagePixels     uint64

// NewServer defaulting
if cfg.MaxImagePixels == 0 {
	cfg.MaxImagePixels = 100_000_000 // D-05: 100 megapixels default
}

// struct literal wiring
maxImagePixels: cfg.MaxImagePixels,
```
Add `maxDocumentUncompressedBytes uint64` to `Server`, `MaxDocumentUncompressedBytes uint64` to `Config`, a default block (`500 << 20` per D-04's 500 MiB default, with a comment citing D-04), and the corresponding struct-literal wiring line — same four touch-points, same order, same style as `MaxImagePixels`.

---

### `cmd/api/main.go` (MODIFY — env wiring)

**Analog:** self, line 101

**Existing pattern:**
```go
MaxImagePixels:     uint64(envInt64("MAX_IMAGE_PIXELS", 100_000_000)), // D-05: 100 megapixels default
```
Add, in the same `api.Config{...}` literal:
```go
MaxDocumentUncompressedBytes: uint64(envInt64("MAX_DOCUMENT_UNCOMPRESSED_BYTES", 500<<20)), // D-04: 500 MiB default
```
Uses the existing `envInt64` helper (`cmd/api/main.go:157-165`) unchanged — no new parsing helper needed, exactly mirrors `MAX_IMAGE_PIXELS`'s wiring.

---

### `.env.example` (MODIFY)

**Analog:** self, existing `MAX_IMAGE_PIXELS` line under the `# API` section

**Existing pattern:**
```
MAX_IMAGE_PIXELS=100000000   # decompression-bomb guard: max declared width*height (default 100 megapixels, e.g. 10000x10000)
```
Add immediately below it:
```
MAX_DOCUMENT_UNCOMPRESSED_BYTES=524288000   # zip-bomb guard: max total declared uncompressed size summed across all ZIP entries in an office document (default 500 MiB)
```
Same inline-comment style: short label, colon-free description, default called out in parens.

---

### `internal/api/handlers_test.go` (MODIFY — new test cases)

**Analog:** self — `TestCreateJob_DimensionLimitExceeded` (lines 320-345), `TestCreateJob_UnrecognizedContent` (lines 252-275), `TestCreateJob_ContentMismatch` (lines 220-250), plus fixture helpers `oversizedPNGFixture`/`truncatedIHDRPNGFixture` (lines 122-145) and `multipartBody` (lines 153-169).

**Fixture-then-assert-4-invariants pattern** (`internal/api/handlers_test.go:323-345`):
```go
func TestCreateJob_DimensionLimitExceeded(t *testing.T) {
	repo := &fakeRepo{}
	store := &fakeStorage{}
	resolver := newFakeResolver()
	srv := NewServer(repo, store, &fakeQueue{}, resolver, healthyDeps(), Config{MaxUploadBytes: 1 << 20, MaxImagePixels: 1_000_000})

	body, ct := multipartBody(t, "in.png", "webp", oversizedPNGFixture())
	req := authed(httptest.NewRequest(http.MethodPost, "/v1/jobs", body))
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()

	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if store.uploaded {
		t.Error("must not upload a decompression-bomb-shaped upload before the dimension check")
	}
	if repo.created != nil {
		t.Error("must not create job for an oversized declared-dimension upload")
	}
}
```
Every new test (zip-bomb-over-limit, macro-detected, duplicate-root-part, OOXML/ODF disambiguation happy-path, bare-.zip-unrecognized, dimension-check-skipped-for-documents) should follow this exact shape: construct via `NewServer(..., Config{...})` (overriding only the relevant limit field, e.g. `MaxDocumentUncompressedBytes: <small value>` to make a bomb-fixture trivially over-limit without needing a real 500 MiB fixture), build the multipart body via the existing `multipartBody(t, filename, target, data)` helper, `POST` through `srv.Routes().ServeHTTP`, then assert status code + `store.uploaded == false` + `repo.created == nil` for every rejection case (never upload/never create before validation completes — the established "reject before any storage write" invariant this whole test file already enforces).

**New fixture-builder helpers needed** (same one-per-scenario style as `oversizedPNGFixture`/`truncatedIHDRPNGFixture`):
```go
// docxFixture returns a minimal, valid-shaped docx built via archive/zip.Writer
// (word/document.xml root part present) so convert.SniffContainer detects it.
func docxFixture(t *testing.T) []byte { ... }

// odtFixture returns a minimal odt: first entry "mimetype", Method=Store,
// payload "application/vnd.oasis.opendocument.text".
func odtFixture(t *testing.T) []byte { ... }

// zipBombFixture returns a docx-shaped zip whose declared UncompressedSize64
// exceeds the given limit (build via a real small deflate payload OR a
// hand-set zip.FileHeader.UncompressedSize64 — verify archive/zip.Writer's
// exact trust behavior before choosing).
func zipBombFixture(t *testing.T, declaredSize uint64) []byte { ... }

// macroDocxFixture is a valid docx PLUS a word/vbaProject.bin entry.
func macroDocxFixture(t *testing.T) []byte { ... }
```
For a happy-path test asserting the pair-check still rejects (since no `Converter` is registered for document formats until Phase 9), expect the EXISTING `TestCreateJob_UnsupportedPair` shape (lines 297-318) to apply directly: a well-formed docx upload should currently 422 with "unsupported conversion" (not a format-detection failure) — this is the correct, expected outcome per RESEARCH.md's System Architecture Diagram (`convert.Default.Supports` still returns false for every document pair this phase). Add a test asserting exactly this to lock in that this phase's detection code integrates correctly without prematurely enabling conversion.

## Shared Patterns

### Structured rejection logging (D-08)
**Source:** `internal/api/handlers.go:129`, `:137`, `:158`, `:166`
**Apply to:** Every new rejection branch in `handleCreateJob` (zip-bomb, macro, duplicate-root-part)
```go
log.Printf("content validation rejected: client_id=%s filename=%q reason=<tag> <extra_fields>", client.ID, filename, ...)
```
Always prefixed `"content validation rejected: client_id=%s filename=%q reason=..."`, always includes the resolved `client.ID` and `filename`, extra fields appended as `key=value` pairs relevant to the specific rejection (mirrors `reason=dimension_limit width=%d height=%d limit=%d`).

### Fail-closed, never-leak-internals error responses
**Source:** `internal/api/handlers.go` (every `writeError` call), `internal/convert/dimensions.go:23-27` (`ErrDimensionsUnknown`)
**Apply to:** `docsniff.go`'s new sentinel errors and every new `handleCreateJob` rejection
```go
writeError(w, http.StatusUnprocessableEntity, "declared uncompressed size exceeds configured limit")
```
Short, fixed, non-echoing message strings only — the underlying `err`/parse-failure detail is discarded from the HTTP response (logged instead, per the structured-logging pattern above).

### Closed dispatch table + citation comments for spec-grounded binary parsing
**Source:** `internal/convert/sniff.go:31-40`, `internal/convert/dimensions.go:34-43`, `:70-71`
**Apply to:** All new maps/functions in `docsniff.go` (`ooxmlRootParts`, `odfMimetypes`, `ooxmlMacroParts`, and each detection function)
```go
// Source: OASIS OpenDocument v1.2 Part 3 §17.4 (mimetype file requirement)
var odfMimetypes = map[string]string{ ... }
```

### Env-var-configurable limit with a sensible compiled-in default
**Source:** `internal/api/api.go:87-89` (`MaxImagePixels` default), `cmd/api/main.go:101` (`envInt64` wiring), `.env.example` (`MAX_IMAGE_PIXELS` line)
**Apply to:** `MaxDocumentUncompressedBytes`/`MAX_DOCUMENT_UNCOMPRESSED_BYTES` — three-touch-point pattern: `Config` field + zero-value default in `NewServer`, `envInt64(...)` wiring in `cmd/api/main.go`, and a documented line in `.env.example`.

### "Reject before any storage write" test invariant
**Source:** `internal/api/handlers_test.go` (every `TestCreateJob_*` rejection test asserts `store.uploaded == false` and `repo.created == nil`)
**Apply to:** Every new test in `handlers_test.go` for the zip-bomb/macro/duplicate-root-part rejections.

## No Analog Found

None — every new/modified file has a direct, exact-or-role-match analog already in the codebase (this phase is explicitly framed by CONTEXT.md/RESEARCH.md as extending the Phase 4/7 validation-gate pattern to a second container family, not introducing a new architectural shape).

## Metadata

**Analog search scope:** `internal/convert/`, `internal/api/`, `cmd/api/`, `.env.example` (entire relevant surface per RESEARCH.md's Recommended Project Structure)
**Files scanned:** `sniff.go`, `sniff_test.go`, `dimensions.go`, `dimensions_test.go`, `convert.go`, `converters.go`, `handlers.go`, `handlers_test.go`, `api.go`, `main.go` (`cmd/api`), `.env.example`
**Pattern extraction date:** 2026-07-09
