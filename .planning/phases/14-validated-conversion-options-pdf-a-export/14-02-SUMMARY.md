---
phase: 14-validated-conversion-options-pdf-a-export
plan: 02
subsystem: convert
tags: [pdf-a, libreoffice, security, injection-resistance, terminal-classification]

# Dependency graph
requires:
  - "Job.Opts / CreateParams.Opts round-trip through jobs.options (14-01)"
provides:
  - "DocOpts closed struct + ParseDocOpts/DocOptsFromMap/ValidateApplicability/PDFAFilterOptions (internal/convert/opts.go)"
  - "LibreOfficeConverter.Convert consumes opts and builds PDF/A argv from server constants only"
  - "validateDocumentOutput OutputIntent (/GTS_PDFA) check, terminal-coupled in worker.go same commit"
  - "worker.go threads job.Opts into Convert and strict-reparses persisted opts (D-10 SkipRetry guard)"
affects: [14-03]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "opts.go follows the olecfb.go single-purpose-file convention (package-level doc comments, one exported contract, inline why-comments)"
    - "PDF/A filter suffix is a compile-time string constant, selected only by switching on a validated enum field — never built from client map bytes"
    - "terminalLibreOfficeSignatures gains its new substring in the SAME commit as the validator that emits it (D-06, inherited from Phase 13's D-03/D-04)"

key-files:
  created:
    - internal/convert/opts.go
  modified:
    - internal/convert/libreoffice.go
    - internal/convert/libreoffice_test.go
    - internal/worker/worker.go

key-decisions:
  - "ValidateApplicability's source parameter is accepted but intentionally unused — the plan's <behavior> spec only checks engine and NormalizeFormat(target)==\"pdf\"; source is part of the interface signature for future opts that may need it"
  - "gtsPDFAMarker uses the /GTS_PDFA family match (not the stricter /GTS_PDFA2 per-part variant), per CONTEXT.md's stated Claude's-Discretion default — a live LO 7.4 run in Plan 03 confirms this against real output"
  - "Fixed 5 pre-existing validateDocumentOutput call sites in libreoffice_test.go (added the new wantPDFA=false argument) as an in-scope Rule 3 blocking-issue fix, since Task 2's signature change would otherwise break compilation"

requirements-completed: [OPTS-01, OPTS-02]

# Metrics
duration: 25min
completed: 2026-07-11
---

# Phase 14 Plan 02: Validated Opts Core + PDF/A Filter Builder + OutputIntent Check Summary

**Closed DocOpts contract with strict allow-list parsing, a PDF/A-2b filter-options builder assembled entirely from server constants, and a worker-side OutputIntent check that fails a mis-tagged PDF/A export terminally — the injection unit test proves adversarial client bytes never reach the soffice argv.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-07-11 (first commit 65ba272)
- **Completed:** 2026-07-11 (last commit a3ca73c)
- **Tasks:** 3 completed
- **Files modified:** 4 (1 created, 3 modified)

## Accomplishments

- `internal/convert/opts.go` (new): `DocOpts` closed struct with a single `pdf_profile` field; `ParseDocOpts` strict-decodes with `DisallowUnknownFields` and validates against a single-value `pdf/a-2b` allow-list constant; `DocOptsFromMap` round-trips a persisted `map[string]any` through the same strictness (D-10); `ValidateApplicability` restricts `pdf_profile` to document-engine + pdf-target conversions; `PDFAFilterOptions` returns a hardcoded server-constant FilterOptions JSON suffix (`SelectPdfVersion=2` + `EmbedStandardFonts=true`, Pitfall 7) keyed only on the validated enum — never on raw client bytes (D-07, Pitfall 9).
- `internal/convert/libreoffice.go`: `Convert` derives `DocOpts` from the opts map, computes the PDF/A suffix, and appends it onto the same `--convert-to` argv element (no shell, no new escaping mechanism). `validateDocumentOutput` gained a `wantPDFA` parameter; when set and the target is pdf, the produced file must contain the `/GTS_PDFA` OutputIntent marker or the conversion fails with `"libreoffice: output missing PDF/A OutputIntent marker"`.
- `internal/worker/worker.go`: `terminalLibreOfficeSignatures` gained the lowercased OutputIntent-missing substring in this same commit (D-06 coupling); `HandleDocumentConvert` strict-reparses `job.Opts` via `convert.DocOptsFromMap` immediately after `h.repo.Get`, `SkipRetry`-wrapping any parse failure (D-10, garbage-column terminal guard); `process()` now passes `job.Opts` into `conv.Convert` instead of a hardcoded `nil`.
- `internal/convert/libreoffice_test.go`: three new tests — `TestPDFAFilterOptions` (builder forces both properties), `TestDocOptsInjectionResistance` (the mandatory success-criterion-1 artifact — 5 adversarial inputs, all rejected by `ParseDocOpts` before reaching the builder), `TestValidatePDFAOutputIntent` (marker present/absent/ignored three-case coverage). Also updated 5 pre-existing `validateDocumentOutput` call sites for the new `wantPDFA` parameter.

## Task Commits

Each task was committed atomically:

1. **Task 1: Create internal/convert/opts.go** — `65ba272` (feat)
2. **Task 2: Consume opts in Convert + OutputIntent check + worker.go coupling** — `8016179` (feat)
3. **Task 3: Unit tests (builder, injection, OutputIntent)** — `a3ca73c` (test)

_Note: like Plan 01, these three tasks are marked `tdd="true"` in the plan but sequence as implement(opts.go) → implement(libreoffice.go/worker.go) → test-the-implementation, rather than a single RED→GREEN cycle per task. All tests were written and run green in Task 3; Task 2 also required fixing 5 pre-existing test call sites broken by the `validateDocumentOutput` signature change (committed alongside Task 2's implementation since they were required for `go build`/`go vet` to pass, not new test coverage)._

## Files Created/Modified

- `internal/convert/opts.go` (new) — `DocOpts`, `ParseDocOpts`, `DocOptsFromMap`, `ValidateApplicability`, `PDFAFilterOptions`
- `internal/convert/libreoffice.go` — `Convert` consumes `opts`; PDF/A filter suffix appended to `--convert-to`; `validateDocumentOutput` gains `wantPDFA` + `/GTS_PDFA` marker check
- `internal/convert/libreoffice_test.go` — 3 new tests + 5 updated call sites (new `wantPDFA` param)
- `internal/worker/worker.go` — `terminalLibreOfficeSignatures` extended; `HandleDocumentConvert` strict-reparses `job.Opts`; `process()` threads `job.Opts` into `Convert`

## Decisions Made

- `ValidateApplicability`'s `source` parameter is accepted per the plan's exact interface signature but intentionally unused in the body — the plan's `<behavior>` spec only exercises `engine` and `target`; keeping the parameter matches the frozen interface contract Plan 03 will consume.
- `gtsPDFAMarker` uses the `/GTS_PDFA` family match rather than the stricter `/GTS_PDFA2` variant, per CONTEXT.md's explicit "Claude's Discretion, family match is the safe default" guidance — to be confirmed against real LO 7.4 output during Plan 03's live run.
- Fixed 5 pre-existing `validateDocumentOutput` call sites in `libreoffice_test.go` (added `false` as the new third argument) as part of Task 2's commit — these are non-PDF/A test cases the signature change would otherwise have broken; not new coverage, just compilation compatibility (Rule 3: auto-fix blocking issue).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking issue] Updated 5 existing `validateDocumentOutput` call sites for the new `wantPDFA` parameter**
- **Found during:** Task 2
- **Issue:** Changing `validateDocumentOutput`'s signature from `(path, targetFormat string)` to `(path, targetFormat string, wantPDFA bool)` broke 5 existing calls in `libreoffice_test.go` (`TestValidateDocumentOutput`'s a/b/c cases plus the pdf-delegation checks), which would have failed `go vet`/`go build` of the test package.
- **Fix:** Added `false` as the third argument to each of the 5 pre-existing calls (none of them test PDF/A behavior).
- **Files modified:** `internal/convert/libreoffice_test.go`
- **Commit:** `8016179`

No other deviations — plan executed as written otherwise.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required. No new Go dependency (go.mod/go.sum unchanged).

## Next Phase Readiness

- `internal/convert/opts.go`'s exported contract (`DocOpts`, `ParseDocOpts`, `DocOptsFromMap`, `ValidateApplicability`, `PDFAFilterOptions`) is frozen per the plan's `<interfaces>` block and ready for Plan 03 to consume from `internal/api/handlers.go` (syntax + applicability validation at `handleCreateJob`, D-04) and `internal/e2e/e2e_test.go` (live PDF/A round-trip + negative-opts 422 cases).
- The `/GTS_PDFA` family-match choice in `validateDocumentOutput` should be confirmed against real LO 7.4 output during Plan 03's live e2e run, per CONTEXT.md's discretion note.
- `go build ./...`, `go vet ./internal/convert/... ./internal/worker/...`, and the full `go test ./internal/...` suite are all clean. No blockers.

---
*Phase: 14-validated-conversion-options-pdf-a-export*
*Completed: 2026-07-11*

## Self-Check: PASSED

All created/modified files found on disk (internal/convert/opts.go, internal/convert/libreoffice.go, internal/convert/libreoffice_test.go, internal/worker/worker.go, this SUMMARY.md); all 4 task/summary commits (65ba272, 8016179, a3ca73c, bba8eac) verified present in git log.
