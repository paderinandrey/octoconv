# Phase 13: Cross-Format Conversion & Input Safety - Pattern Map

**Mapped:** 2026-07-10
**Files analyzed:** 5 (4 modified, 1 test file extended; no wholly-new production files — this phase is entirely a generalization of existing files, per D-01/D-05's explicit choice to keep the CFB check out of `sniff.go`'s registry table)
**Analogs found:** 5 / 5 (every file to modify is its own best analog — this phase generalizes existing functions in place rather than introducing new files/roles)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `internal/convert/libreoffice.go` (`Pairs`, `filterFor`, `Convert`, `validatePDF`→`validateDocumentOutput`) | service (converter engine) | file-I/O + transform | itself (current `Pairs`/`filterFor`/`validatePDF`) | exact — same file, same functions, generalized |
| `internal/api/handlers.go` (new CFB branch in `handleCreateJob`) | controller (request-response, fail-closed validation) | request-response | itself — existing ZIP-branch (lines 125-158) and macro/zip-bomb 422 pattern (lines 137-152) | exact — same function, same branch style |
| `internal/worker/worker.go` (`terminalLibreOfficeSignatures`) | config/constant (terminal-error classification table) | transform (error string → bool) | itself — existing `terminalLibreOfficeSignatures` slice (lines 45-49) | exact — literally append to the same var |
| new `internal/convert/olecfb.go` (or equivalent CFB magic-check helper) | utility (single-purpose detector) | transform | `internal/convert/docsniff.go` (`SniffContainer`/`ErrNotAZip`) for file/package shape; `internal/api/callbackurl.go` for "single-purpose validation file returning a plain error" shape | role-match — new file, but the project's own single-purpose-file convention is well established |
| `internal/e2e/e2e_test.go` (extend `TestDocumentConversionE2E`-style table + new CFB-rejection test) | test (E2E, live-stack) | request-response | itself — existing `fixtures` table loop (lines 275-315) and `postJob`/`pollUntilDone` helpers | exact — same file, same harness, same table-driven pattern |

## Pattern Assignments

### `internal/convert/libreoffice.go` — `Pairs()` generalization (D-01)

**Analog:** itself, current `Pairs()` (lines 20-27)

**Current pattern to extend** (`internal/convert/libreoffice.go:20-27`):
```go
// documentFormats are the office document formats LibreOffice converts to pdf.
var documentFormats = []string{"docx", "odt", "xlsx", "ods", "pptx", "odp"}

// Pairs returns one {format, "pdf"} pair per supported source document format.
func (LibreOfficeConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(documentFormats))
	for _, f := range documentFormats {
		pairs = append(pairs, Pair{From: f, To: "pdf"})
	}
	return pairs
}
```

**What D-01 requires:** append exactly 6 new `Pair{From, To}` entries — `{docx,odt}`, `{odt,docx}`, `{xlsx,ods}`, `{ods,xlsx}`, `{pptx,odp}`, `{odp,pptx}` — to the same slice literal/append pattern, keeping the existing 6 `→pdf` pairs unchanged. No cross-family pairs (docx→ods etc.) per D-01 — keep the new table a flat, explicit list rather than a generated cross-product, mirroring the existing `documentFormats` slice's explicitness. `Pair{}` itself needs no change (`internal/convert/convert.go:24-27` — already a generic `{From, To}` struct); `Registry.Register` (`convert.go:69-73`) already iterates `Pairs()` generically, so no registry change needed.

---

### `internal/convert/libreoffice.go` — `filterFor` generalization (D-02, Pitfall 1/3)

**Analog:** itself, current `filterFor` (lines 73-87)

**Current pattern** (`internal/convert/libreoffice.go:73-87`):
```go
// filterFor maps a source document extension to the LibreOffice PDF export
// filter that produces the correct output for that document's application
// (Writer/Calc/Impress).
func filterFor(sourceExt string) (string, error) {
	switch NormalizeFormat(sourceExt) {
	case "docx", "odt":
		return "writer_pdf_Export", nil
	case "xlsx", "ods":
		return "calc_pdf_Export", nil
	case "pptx", "odp":
		return "impress_pdf_Export", nil
	default:
		return "", fmt.Errorf("no pdf export filter for %q", sourceExt)
	}
}
```

**D-02 requires:** signature becomes `filterFor(sourceExt, targetFormat string) (string, error)` — an explicit `(source, target) → filter name` table, NOT an auto-derived one. Recommended shape mirrors the existing `switch`-per-app-family structure but keyed on both axes:
```go
func filterFor(sourceExt, targetFormat string) (string, error) {
	target := NormalizeFormat(targetFormat)
	switch NormalizeFormat(sourceExt) {
	case "docx", "odt":
		switch target {
		case "pdf":
			return "writer_pdf_Export", nil
		case "odt":
			return "writer8", nil
		case "docx":
			return "MS Word 2007 XML", nil
		}
	case "xlsx", "ods":
		switch target {
		case "pdf":
			return "calc_pdf_Export", nil
		case "ods":
			return "calc8", nil
		case "xlsx":
			return "Calc MS Excel 2007 XML", nil
		}
	case "pptx", "odp":
		switch target {
		case "pdf":
			return "impress_pdf_Export", nil
		case "odp":
			return "impress8", nil
		case "pptx":
			return "Impress MS PowerPoint 2007 XML", nil
		}
	}
	return "", fmt.Errorf("no export filter for %s -> %s", sourceExt, targetFormat)
}
```
Filter name strings above (`writer8`/`calc8`/`impress8`, `"MS Word 2007 XML"`/`"Calc MS Excel 2007 XML"`/`"Impress MS PowerPoint 2007 XML"`) are ARCHITECTURE.md's researched values (Pitfall 3) — CONTEXT.md D-01/Claude's Discretion flags these as needing live verification against LibreOffice 7.4 (bookworm) during implementation; do not treat the literal strings above as final without that verification.

**Test analog:** `internal/convert/libreoffice_test.go:14-41` (`TestFilterFor`) — existing table-driven test over a `map[string]string` of `input → want`; extend to a `map[[2]string]string` (or two-field struct) keyed by `(sourceExt, targetFormat)` once the signature changes. The existing `.txt`-returns-error case (lines 36-40) should stay as the terminal-error regression check.

---

### `internal/convert/libreoffice.go` — `Convert` generalization (Pitfall 1)

**Analog:** itself, current `Convert` (lines 34-68)

**Current pattern** (`internal/convert/libreoffice.go:34-68`, hardcoded pdf-only parts marked):
```go
func (LibreOfficeConverter) Convert(ctx context.Context, inPath, outPath string, _ map[string]any) error {
	workDir := filepath.Dir(outPath)
	profileDir := filepath.Join(workDir, "lo-profile")
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return fmt.Errorf("libreoffice: mkdir profile: %w", err)
	}

	filter, err := filterFor(filepath.Ext(inPath))          // ← must also take target
	if err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}

	args := []string{
		"--headless", "--invisible", "--nocrashreport", "--nodefault",
		"--nologo", "--nofirststartwizard", "--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", "pdf:" + filter,                     // ← hardcoded "pdf"
		"--outdir", workDir,
		inPath,
	}
	if err := runCommand(ctx, "soffice", args...); err != nil {
		return fmt.Errorf("libreoffice: %w", err)
	}

	producedPath := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(inPath), filepath.Ext(inPath))+".pdf") // ← hardcoded ".pdf"
	if err := os.Rename(producedPath, outPath); err != nil {
		return fmt.Errorf("libreoffice: rename output: %w", err)
	}

	return validatePDF(outPath)                              // ← hardcoded pdf-only validator
}
```

**Required generalization:** derive `targetFormat` from `filepath.Ext(outPath)` (the worker already builds `outPath` as `"out."+job.TargetFormat`, `internal/worker/worker.go:397-398` — this is the existing convention the converter should lean on, exactly as it already does for `inPath`'s `"in."+job.SourceFormat` convention at line 62's `strings.TrimSuffix`/`filepath.Ext(inPath)` idiom). All three marked lines become target-aware:
- `filterFor(filepath.Ext(inPath), filepath.Ext(outPath))`
- `"--convert-to", NormalizeFormat(filepath.Ext(outPath)) + ":" + filter`
- `producedPath := ... + "." + NormalizeFormat(filepath.Ext(outPath))`
- final line: `return validateDocumentOutput(outPath, filepath.Ext(outPath))` (new dispatcher, see next section) instead of the bare `validatePDF(outPath)` call.

**Grep verification per Pitfall 1's own warning sign:** after this change, `grep -n '"\.pdf"\|"pdf:"' internal/convert/libreoffice.go` should show zero hardcoded literals outside of the `filterFor` table's own `case "pdf":` branches (which are legitimate — pdf remains a valid target, just no longer the ONLY one).

---

### `internal/convert/libreoffice.go` — output validation dispatcher (D-03, D-04, Pitfall 4)

**Analog:** current `validatePDF` (lines 92-117) — reuse verbatim for the pdf-target branch; new sibling function reuses `SniffContainer` from `internal/convert/docsniff.go`

**Current `validatePDF`** (keep unchanged, called only for `target == "pdf"`):
```go
var pdfMagic = []byte("%PDF-")

func validatePDF(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("libreoffice: stat output: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("libreoffice: output is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("libreoffice: open output: %w", err)
	}
	defer f.Close()
	buf := make([]byte, len(pdfMagic))
	if _, err := io.ReadFull(f, buf); err != nil {
		return fmt.Errorf("libreoffice: read output header: %w", err)
	}
	if !bytes.Equal(buf, pdfMagic) {
		return fmt.Errorf("libreoffice: output missing %%PDF- magic bytes")
	}
	return nil
}
```

**New non-PDF branch reuses `SniffContainer`** (`internal/convert/docsniff.go:62-99`, signature `func SniffContainer(r io.ReaderAt, size int64) (ContainerResult, error)`, returning `ContainerResult{Format string, ...}`). D-03's exact framing to preserve in the doc comment: "output validated by the same sniff that validates input." Sketch dispatcher (ARCHITECTURE.md's own sketch, reproduced here as the concrete pattern to copy):
```go
func validateDocumentOutput(path, targetFormat string) error {
	if NormalizeFormat(targetFormat) == "pdf" {
		return validatePDF(path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("libreoffice: stat output: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("libreoffice: output is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("libreoffice: open output: %w", err)
	}
	defer f.Close()
	cr, err := SniffContainer(f, fi.Size())
	if err != nil || cr.Format != NormalizeFormat(targetFormat) {
		return fmt.Errorf("libreoffice: output does not match expected container format %s", targetFormat)
	}
	return nil
}
```
Note: reuse the exact error-message vocabulary already used elsewhere in this file (`"libreoffice: <action>: %w"` wrap style, `internal/convert/exec.go`/`libreoffice.go` convention) — the new terminal error string here is a NEW string that must be added to `terminalLibreOfficeSignatures` in the SAME commit (see below, D-04 critical dependency).

**Test analog:** `internal/convert/libreoffice_test.go:43-69` (`TestValidatePDF`) — same three-case shape (valid / empty / wrong-magic) should be replicated for `validateDocumentOutput`'s non-pdf branch, using a small in-memory ZIP fixture with a `mimetype` entry or `ooxmlRootParts` entry (see `internal/convert/docsniff_test.go` for how existing SniffContainer tests construct ZIP fixtures in-process — read that file if constructing a synthetic fixture is needed, not required to duplicate here since D-07 already provides real fixtures via `internal/e2e/testdata/sample.*`).

---

### `internal/worker/worker.go` — `terminalLibreOfficeSignatures` update (D-04, CRITICAL same-commit coupling)

**Analog:** itself, current slice (lines 38-49)

**Current pattern** (`internal/worker/worker.go:38-49`):
```go
// terminalLibreOfficeSignatures are lowercased error-message substrings that
// indicate a deterministically-unrecoverable document conversion: LibreOffice's
// validatePDF guard against its documented "exit 0 but empty/corrupt output"
// failure mode ("output is empty", "output missing %pdf- magic bytes",
// internal/convert/libreoffice.go's validatePDF) and filterFor's unsupported-
// source-extension error ("no pdf export filter for"). No retry can fix any
// of these — a corrupt document always fails validatePDF again.
var terminalLibreOfficeSignatures = []string{
	"output missing %pdf- magic bytes",
	"output is empty",
	"no pdf export filter for",
}
```

**Required change:** append the new lowercased substring from `validateDocumentOutput`'s non-pdf error path (e.g. `"output does not match expected container format"`, matching whatever exact literal is chosen above — MUST match casing-insensitively since `isTerminal`, line 74, does `strings.ToLower(err.Error())` before substring matching) and update the `filterFor`-related string if the new two-arg `filterFor`'s error message text changes (e.g. `"no export filter for"` replacing `"no pdf export filter for"` if that literal is edited — verify the substring still matches after the signature/message change, since `isTerminal` (line 75-78) also does a substring check for `"no converter for"` separately, from `process()`'s own error, not `filterFor`'s). Update the doc comment above the slice to describe the new dispatcher name (`validateDocumentOutput`) alongside `validatePDF`, mirroring the existing comment's explicit "why no retry can help" framing.

**This is the single highest-risk coupling in the phase per Pitfall/D-04** — must land in the exact same commit/PR as the new validator, never split across two commits, or a corrupt/mismatched cross-format output becomes a silently-retried transient error (bounded only by `DOCUMENT_MAX_RETRY`) instead of an immediate terminal failure.

---

### `internal/api/handlers.go` — OLE-CFB rejection branch (D-05, D-06)

**Analog:** itself — existing ZIP-branch structure (lines 125-158) and the macro/zip-bomb inline-422 pattern within it (lines 137-152)

**Current pattern to mirror** (`internal/api/handlers.go:125-167`, the existing `if detected == ""` cascade):
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
	log.Printf("content validation rejected: client_id=%s filename=%q reason=unrecognized_content", client.ID, filename)
	writeError(w, http.StatusUnprocessableEntity, "unrecognized file content for "+filename)
	return
}
```

**D-05 requires:** a THIRD branch, inserted between the ZIP branch and the final generic-422 fallback (both still inside the outer `if detected == ""` gate, since the ZIP branch already only sets `detected` on success and otherwise falls through). It must:
1. Reuse the SAME `file.ReadAt` receiver already in scope (no new file handle) — mirroring exactly how the ZIP branch reads its 4-byte prefix via `file.ReadAt(prefix[:], 0)`.
2. Call a new detector (see `internal/convert/olecfb.go` below) checking the 8-byte magic `D0 CF 11 E0 A1 B1 1A E1`.
3. On match, log with a DISTINCT `reason=` tag (following the exact `log.Printf("content validation rejected: client_id=%s filename=%q reason=...", ...)` template used for `zip_bomb`/`macro_detected`/`unrecognized_content`/`mismatch` above) — e.g. `reason=legacy_or_encrypted_document`.
4. `writeError(w, http.StatusUnprocessableEntity, "...")` with D-06's message, styled like the existing English 422 strings (`"macro-carrying documents are not accepted"`, `"unsupported conversion: X -> Y"`) — e.g. `"legacy binary or password-protected Office format is not supported; convert to docx/xlsx/pptx or remove the password"` — and `return` immediately, same as every other 422 branch in this function.
5. Must NOT set `detected` to any value and must NOT go through `convert.Default.EngineFor` (ARCHITECTURE.md's explicit design point) — this is an unconditional reject, structurally identical to the macro/zip-bomb early-returns, not a registry lookup.
6. Runs BEFORE `s.storage.Upload` (line 231) and `s.repo.Create` (line 238), same as every other content-validation branch — D-06 requires this ordering explicitly.

**Constants block analog** (`internal/api/handlers.go:23-28`) — no new form-field constant needed (CFB detection reads the same `file`/`header` already parsed for `formFieldFile`), but if a `reason=` string constant style is wanted, mirror `operationConv = "convert"`'s pattern of a named const rather than a repeated literal.

---

### New `internal/convert/olecfb.go` — CFB magic detector (D-05)

**Analog:** `internal/convert/docsniff.go` for single-purpose-detector-file shape (package `convert`, exported detector function + package-level var for the signature, doc comment on the exported function per project convention) and `internal/api/callbackurl.go` for "returns a plain answer, no registry involvement" shape.

**Pattern to follow** (package/import/doc-comment style, from `docsniff.go:1-22`):
```go
package convert

import (
	"bytes"
	"io"
)

// oleCFBMagic is the fixed 8-byte Compound File Binary (OLE2/MS-CFB)
// signature shared by legacy binary .doc/.xls/.ppt and password-protected
// "Agile Encryption" modern OOXML (.docx/.xlsx/.pptx wrapped in a CFB
// container) — both begin with this identical header (SAFE-01, D-05).
var oleCFBMagic = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

// IsOLECFB reports whether r begins with the OLE-CFB signature. This is a
// fail-closed pre-flight rejection, not a Sniff/SniffContainer registry entry
// (D-05): no Converter is ever registered for this "format" — every match is
// unconditionally rejected in internal/api/handlers.go, mirroring the
// existing macro/zip-bomb inline-reject pattern rather than the
// Sniff-then-EngineFor lookup path.
func IsOLECFB(r io.ReaderAt) bool {
	var buf [8]byte
	n, _ := r.ReadAt(buf[:], 0)
	return n == 8 && bytes.Equal(buf[:], oleCFBMagic)
}
```
Exported (`IsOLECFB`, `PascalCase`) since it's called from `internal/api`, matching `Sniff`/`SniffContainer`/`Dimensions`/`NormalizeFormat`'s existing exported-function convention across package `convert`. Per project convention (every package has exactly one package-doc-comment file — `docsniff.go`/`sniff.go`/`libreoffice.go` do NOT repeat the package doc, only `convert.go` line 1-2 carries it) — `olecfb.go` should NOT add another package comment.

**Explicit non-pattern (per D-05/ARCHITECTURE.md):** do NOT add this to `signatures []signature` in `sniff.go` (lines 34-40) or to `ooxmlRootParts`/`odfMimetypes` in `docsniff.go` — both of those tables' contracts are "detected AND supported," and CFB is "detected AND always rejected." Keeping it a separate function preserves `EngineFor`'s fail-closed assumption that every `detected` value it receives came from a real registered `Converter.Engine()`.

**No test-file analog needed as a new file** — extend `internal/convert/docsniff_test.go`-style unit tests (magic-byte true/false cases) inline near wherever `olecfb.go` lands, or add `olecfb_test.go` following the exact `sniff_test.go`/`docsniff_test.go` structure (table of byte-prefix fixtures → expected bool).

---

### `internal/e2e/e2e_test.go` — cross-pair + CFB-rejection live tests (D-07)

**Analog:** itself — `TestDocumentConversionE2E`'s `fixtures` table loop (lines 271-315) and `postJob`/`pollUntilDone` helpers (lines 116-214)

**Current table-loop pattern to mirror for the 6 new cross-pairs:**
```go
fixtures := []string{
	"sample.docx", // the one webhook-asserting pair (D-05)
	"sample.xlsx",
	"sample.pptx",
	"sample.odt",
	"sample.ods",
	"sample.odp",
}

for _, filename := range fixtures {
	t.Run(filename, func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("testdata", filename))
		...
		jobID := postJob(t, cfg.baseURL, apiKey, filename, data, "pdf", callbackURL)
		body := pollUntilDone(t, cfg.baseURL, apiKey, jobID, 5*time.Minute)
		downloadURL, _ := body["download_url"].(string)
		...
		assertDownloadIsPDF(t, downloadURL)
	})
}
```

**D-07 requires a new test** (e.g. `TestCrossFormatConversionE2E`) with a `{filename, target}` pair table reusing the SAME existing fixtures as inputs (`sample.docx→odt`, `sample.odt→docx`, `sample.xlsx→ods`, `sample.ods→xlsx`, `sample.pptx→odp`, `sample.odp→pptx`) — same `postJob`/`pollUntilDone` calls, but `postJob`'s `target` argument becomes the cross-format target instead of the hardcoded `"pdf"` literal (`postJob`'s signature already takes `target` as a parameter, `internal/e2e/e2e_test.go:119`, so no helper signature change needed). The download assertion CANNOT reuse `assertDownloadIsPDF` (lines 320-338, hardcoded `%PDF-` check) — needs a new `assertDownloadIsFormat(t, downloadURL, expectedFormat)` sibling that reuses `convert.SniffContainer` (import `internal/convert`) against the downloaded bytes instead of a bare magic-byte check, mirroring D-03's "output validated the same way as input" symmetry claim at the E2E layer too.

**D-07 also requires a CFB-rejection live test** (e.g. `TestOLECFBRejectionE2E`): structurally NOT a `postJob`/`pollUntilDone` pair (no job is ever created) — instead a direct `POST /v1/jobs` call asserting `422` status directly, similar in shape to `postJob`'s own request-building code (lines 119-153) but asserting `http.StatusUnprocessableEntity` instead of `http.StatusAccepted`, using a real legacy `.doc` or password-protected `.docx` fixture (new file under `internal/e2e/testdata/`, per D-07/Claude's Discretion — exact fixture name/creation method left to implementer). Consider factoring a small `postJobExpectStatus(t, ..., wantStatus int) (body []byte)` helper alongside `postJob` rather than duplicating the multipart-building code, since `postJob` today hard-asserts `202` (line 156-159) and has no path for asserting a different expected status.

---

## Shared Patterns

### Fail-closed 422 before storage write
**Source:** `internal/api/handlers.go` (macro check lines 147-152, zip-bomb check lines 139-144, dimension check lines 196-212, generic-422 lines 159-167)
**Apply to:** the new OLE-CFB branch — every content-rejection in this handler runs BEFORE `s.storage.Upload` (line 231) and BEFORE `s.repo.Create` (line 238); log with `client_id`/`filename`/`reason=` via `log.Printf`, then `writeError(w, http.StatusUnprocessableEntity, "<message>")`, then `return`.
```go
log.Printf("content validation rejected: client_id=%s filename=%q reason=%s ...", client.ID, filename, "<new_reason>")
writeError(w, http.StatusUnprocessableEntity, "<message>")
return
```

### Terminal vs transient worker-error classification
**Source:** `internal/worker/worker.go:38-93` (`terminalLibreOfficeSignatures`, `isTerminal`)
**Apply to:** any new error string introduced by `validateDocumentOutput`'s non-pdf branch — MUST be added to `terminalLibreOfficeSignatures` in the same commit as the validator (D-04), lowercase-substring-matched by `isTerminal` (line 74, 85-91).

### Structural container validation (input AND output)
**Source:** `internal/convert/docsniff.go` (`SniffContainer`, `ContainerResult`)
**Apply to:** both `internal/api/handlers.go`'s existing input-side ZIP branch (unchanged) and the new `validateDocumentOutput` non-pdf branch in `internal/convert/libreoffice.go` — same function, same `ContainerResult.Format` comparison, applied symmetrically to input (must equal declared `target`... wait, applies to detected==declared at input; applies to `cr.Format == expected target format` at output).

### Single-purpose detector file convention
**Source:** `internal/convert/docsniff.go`, `internal/convert/sniff.go`, `internal/api/callbackurl.go`
**Apply to:** new `internal/convert/olecfb.go` — one file, one exported detector function, package-level var for the magic signature, doc comment explaining "why" (CFB legacy/encrypted ambiguity) not just "what."

### Error wrapping convention
**Source:** `internal/convert/libreoffice.go` throughout (`fmt.Errorf("libreoffice: <action>: %w", err)`)
**Apply to:** any new error path inside `libreoffice.go` (filter lookup failure, output-validation failure) — keep the `"libreoffice: "` prefix so `terminalLibreOfficeSignatures`' substring matching and existing log/diagnostic conventions stay consistent.

## No Analog Found

None — every file in this phase's scope is a generalization of an existing, already-analyzed file (per CONTEXT.md's `canonical_refs`, this phase's own files ARE the source of truth for its patterns). The one net-new file (`internal/convert/olecfb.go`) has strong role-match analogs (`docsniff.go`, `callbackurl.go`) covering both its package conventions and its "detect-and-reject, not detect-and-register" semantics.

## Metadata

**Analog search scope:** `internal/convert/`, `internal/api/`, `internal/worker/`, `internal/e2e/` (directed by CONTEXT.md's `canonical_refs` — no broader Glob/Grep sweep was needed since the phase's own integration points are explicitly enumerated file:line in ARCHITECTURE.md and CONTEXT.md)
**Files scanned:** `internal/convert/libreoffice.go`, `internal/convert/libreoffice_test.go`, `internal/convert/sniff.go`, `internal/convert/docsniff.go`, `internal/convert/convert.go`, `internal/convert/converters.go`, `internal/api/handlers.go`, `internal/api/callbackurl.go`, `internal/worker/worker.go`, `internal/e2e/e2e_test.go`, `internal/e2e/testdata/` (directory listing only)
**Pattern extraction date:** 2026-07-10
</content>
