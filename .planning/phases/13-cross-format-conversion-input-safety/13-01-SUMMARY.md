---
phase: 13-cross-format-conversion-input-safety
plan: 01
subsystem: api

tags: [libreoffice, convert, worker, cross-format, sniffcontainer, go]

# Dependency graph
requires:
  - phase: 11-engine-aware-api-routing
    provides: "convert.SniffContainer, docx/xlsx/pptx/odt/ods/odp content sniffing and Registry/EngineFor routing already in place"
provides:
  - "LibreOfficeConverter.Pairs() registering the 6 intra-family cross pairs (docx<->odt, xlsx<->ods, pptx<->odp) alongside the unchanged 6 ->pdf pairs"
  - "Target-aware filterFor(sourceExt, targetFormat) two-axis filter-name table"
  - "Target-aware Convert() deriving --convert-to target, produced-file extension, and validator selection from outPath's extension instead of hardcoded pdf"
  - "validateDocumentOutput dispatcher: validatePDF for pdf targets, convert.SniffContainer-based structural validation for every non-pdf target"
  - "worker.go terminalLibreOfficeSignatures extended + generalized so cross-format validation failures and filter-misses are classified terminal, never silently retried"
affects: [13-02-ole-cfb-rejection, 13-03-e2e-cross-format-and-cfb]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Target-derived-from-outPath-extension: Convert() reads filepath.Ext(outPath) once and threads the normalized target format through filterFor, --convert-to, produced-file rename, and validator selection -- zero hardcoded pdf literals remain outside filterFor's legitimate case \"pdf\" branches and validatePDF's %PDF- check"
    - "Output validated by the same sniff that validates input: validateDocumentOutput reuses convert.SniffContainer (the exact function that validates upload input) for non-pdf targets, zero new validation code"
    - "Same-commit terminal-classifier coupling (D-04): a new terminal error substring is never introduced without appending it to terminalLibreOfficeSignatures in the identical commit, so a validator can never regress into a silently-retried transient error"

key-files:
  created: []
  modified:
    - internal/convert/libreoffice.go
    - internal/convert/libreoffice_test.go
    - internal/worker/worker.go
    - internal/worker/worker_test.go

key-decisions:
  - "D-01/D-02/D-03/D-04 implemented exactly as locked in 13-CONTEXT.md: explicit crossPairs literal (no cross-product) to prevent forbidden cross-family pairs; explicit two-axis filterTable (no auto-derivation); validateDocumentOutput dispatches by target format; validator + terminalLibreOfficeSignatures update landed in the SAME commit (2ecaf86)"
  - "filterFor's unsupported-pair error message changed from \"no pdf export filter for %q\" to \"no export filter for %s -> %s\" (drops the word pdf per Pitfall 3) -- worker.go's terminalLibreOfficeSignatures entry updated in lockstep from \"no pdf export filter for\" to \"no export filter for\""

patterns-established:
  - "filterTable as an explicit map[[2]string]string keyed by normalized (source,target) -- the pattern any future engine's multi-target filter/profile selection should follow instead of ad hoc per-target switch statements"

requirements-completed: [CONV-01, CONV-02]

# Metrics
duration: 41min
completed: 2026-07-10
---

# Phase 13 Plan 01: Generalize LibreOfficeConverter for Cross-Format Conversion Summary

**LibreOfficeConverter now handles docx<->odt, xlsx<->ods, pptx<->odp via an explicit (source,target) filter table, with non-PDF output validated by the same convert.SniffContainer that guards upload input, coupled same-commit with the worker's terminal-error classifier.**

## Performance

- **Duration:** 41 min (13:41 planning-complete baseline to 14:22 last commit)
- **Started:** 2026-07-10T13:41:47+03:00
- **Completed:** 2026-07-10T14:22:16+03:00
- **Tasks:** 2 completed
- **Files modified:** 4

## Accomplishments
- `LibreOfficeConverter.Pairs()` registers exactly 6 new symmetric cross pairs (docx->odt, odt->docx, xlsx->ods, ods->xlsx, pptx->odp, odp->pptx) alongside the unchanged 6 `->pdf` pairs -- zero cross-family pairs, zero identity pairs
- `filterFor` generalized to an explicit two-axis `(source,target)` table; `Convert()` derives the export target, `--convert-to` flag, produced-file rename, and output validator selection from `filepath.Ext(outPath)` end to end -- no more hardcoded `.pdf`/`pdf:` assumptions
- `validateDocumentOutput` added: delegates to unchanged `validatePDF` for pdf targets, and to `convert.SniffContainer` (the exact same sniff that validates upload input) for every non-pdf target, rejecting empty/corrupt/wrong-container output
- `internal/worker/worker.go`'s `terminalLibreOfficeSignatures` updated in the SAME commit as the validator: gained `"output does not match expected container format"` and the generalized `"no export filter for"` (replacing `"no pdf export filter for"`) -- a mismatched cross-format output now fails terminally instead of being retried up to `DOCUMENT_MAX_RETRY` times

## Task Commits

Each task was committed atomically:

1. **Task 1: Generalize LibreOfficeConverter + couple the terminal-error classifier (D-01, D-02, D-03, D-04)** - `2ecaf86` (feat)
2. **Task 2: Unit tests for the two-key filter table, output validator, and terminal-classification coupling** - `b1b1143` (test)

**Plan metadata:** committed with this SUMMARY (see final commit in this plan's history).

## Files Created/Modified
- `internal/convert/libreoffice.go` - `crossPairs` literal, generalized `Pairs()`, two-axis `filterTable`/`filterFor`, target-aware `Convert()`, new `validateDocumentOutput` dispatcher
- `internal/convert/libreoffice_test.go` - `TestFilterFor` rekeyed to `(sourceExt, targetFormat)`, new `TestValidateDocumentOutput` (valid/empty/wrong-container/pdf-delegation cases reusing `docsniff_test.go`'s in-process zip fixture helpers), `TestRegistryLibreOfficePairs` extended with cross-pair/forbidden-pair/identity-pair assertions
- `internal/worker/worker.go` - `terminalLibreOfficeSignatures` slice + doc comment updated for the generalized filter-miss and new mismatch signatures
- `internal/worker/worker_test.go` - `TestIsTerminalLibreOfficeSignatures` and `TestIsDocumentTerminal` updated to the new wording and the new mismatch string

## Decisions Made
- Followed 13-CONTEXT.md D-01..D-04 exactly as locked; no new architectural decisions required during execution.
- `filterFor`'s error message wording changed to drop the word "pdf" (`"no export filter for %s -> %s"`), matching D-02's generalization -- coordinated with the worker.go substring update in the identical commit, per D-04's explicit instruction not to split this across two commits.

## Deviations from Plan

None - plan executed exactly as written. Task 1 and Task 2 map 1:1 onto the plan's task breakdown; no Rule 1-4 auto-fixes were needed.

## Filter-Name Verification Record (for Plan 13-03's live confirmation)

Exact filter-name strings implemented in `internal/convert/libreoffice.go`'s `filterTable` (researched LO 7.4/bookworm starting point, per D-02 -- NOT yet live-confirmed, offline table-lookup tests only):

| Source | Target | Filter |
|--------|--------|--------|
| docx | pdf | `writer_pdf_Export` |
| odt | pdf | `writer_pdf_Export` |
| xlsx | pdf | `calc_pdf_Export` |
| ods | pdf | `calc_pdf_Export` |
| pptx | pdf | `impress_pdf_Export` |
| odp | pdf | `impress_pdf_Export` |
| docx | odt | `writer8` |
| odt | docx | `MS Word 2007 XML` |
| xlsx | ods | `calc8` |
| ods | xlsx | `Calc MS Excel 2007 XML` |
| pptx | odp | `impress8` |
| odp | pptx | `Impress MS PowerPoint 2007 XML` |

## D-03/D-04 Verbatim String Confirmation

`validateDocumentOutput`'s mismatch error (`internal/convert/libreoffice.go`):
```go
fmt.Errorf("libreoffice: output does not match expected container format %s", targetFormat)
```
produces messages of the shape `libreoffice: output does not match expected container format <target>`.

`worker.go`'s `terminalLibreOfficeSignatures` entry:
```go
"output does not match expected container format"
```
is a substring of every message the validator produces -- confirmed verbatim (both by direct comparison here and by the passing `TestIsTerminalLibreOfficeSignatures`/`TestIsDocumentTerminal` regression tests added in Task 2).

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- CONV-01/CONV-02 offline surface (registration, filter table, output validation, terminal coupling) is complete and test-covered.
- Plan 13-03 must live-confirm the `filterTable` filter names against a real LibreOffice 7.4 (bookworm) container per D-02 -- this plan only proves the table returns the expected string, not that soffice accepts it.
- No blockers for Plan 13-02 (OLE-CFB rejection) -- disjoint files, no shared state.
