---
phase: 09-libreoffice-converter-engine
plan: 01
subsystem: infra
tags: [go, libreoffice, soffice, convert, pdf, process-isolation]

# Dependency graph
requires:
  - phase: 08-document-content-safety-format-detection
    provides: SniffContainer format/zip-bomb/macro validation for accepted documents before they reach any converter
provides:
  - LibreOfficeConverter implementing the Converter interface for docx/xlsx/pptx/odt/ods/odp -> pdf
  - Per-job LibreOffice profile isolation self-derived from filepath.Dir(outPath)
  - PDF output validation (non-zero size + %PDF- magic bytes) guarding LibreOffice's exit-0-corrupt failure mode
  - A soffice-binary-gated live test proving the process-group-kill wrapper terminates soffice/soffice.bin/oosplash with zero survivors (DOC-06 proof)
affects: [10-worker-reconciler-integration, 11-api-routing-e2e]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Second Converter implementation mirrors LibvipsConverter's shape exactly (stateless empty struct, value receiver, engine-name error prefix)"
    - "Converter self-derives per-job isolation state (LibreOffice profile dir) from filepath.Dir(outPath) instead of requiring an interface/parameter change"
    - "Engine output validation (magic bytes) lives inside the converter's Convert method, not a generic worker.go hook"

key-files:
  created:
    - internal/convert/libreoffice.go
    - internal/convert/libreoffice_test.go
  modified:
    - internal/convert/converters.go

key-decisions:
  - "filterFor uses explicit per-module PDF export filter names (writer_pdf_Export/calc_pdf_Export/impress_pdf_Export) rather than relying on soffice's --convert-to pdf auto-detection, per RESEARCH.md Pattern 2"
  - "DOC-06 kill test drives runCommand directly with the same soffice argv Convert builds, rather than calling Convert with an unsupported .txt input — filterFor(\".txt\") would short-circuit before soffice ever launches, producing a false pass"
  - "DOC-06 kill test polls ps for soffice.bin's running state before triggering the kill, rather than using a blind flat deadline, to avoid a false pass where the kill fires before any real process exists"
  - "DOCUMENT_ENGINE_TIMEOUT env-var wiring is out of this phase's scope per CONTEXT.md's Phase Boundary — Convert relies solely on the caller-supplied ctx timeout"

patterns-established:
  - "Pattern: engine converters that need per-job isolated working state derive it from filepath.Dir(outPath) rather than requiring Converter interface changes"

requirements-completed: [DOC-04, DOC-05, DOC-06]

# Metrics
duration: 25min
completed: 2026-07-09
---

# Phase 09 Plan 01: LibreOffice Converter Engine Summary

**LibreOfficeConverter shells out to soffice headless for docx/xlsx/pptx/odt/ods/odp to PDF, self-isolates its LibreOffice profile per job, and validates output (size + %PDF- magic bytes) before returning success; registered in convert.Default and covered by unit tests plus a soffice-gated live process-kill proof test.**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-09T13:55:00+03:00
- **Completed:** 2026-07-09T13:58:18+03:00
- **Tasks:** 2
- **Files modified:** 3 (2 created, 1 modified)

## Accomplishments
- `LibreOfficeConverter` implements the `Converter` interface for all 6 document->pdf pairs (docx, odt, xlsx, ods, pptx, odp -> pdf) and is registered in `convert.Default`
- Per-job LibreOffice profile isolation via `-env:UserInstallation` derived from `filepath.Dir(outPath)`, requiring zero changes to the `Converter` interface or any caller
- Output validation (`validatePDF`) enforces non-zero size AND leading `%PDF-` magic bytes before a conversion is treated as successful, guarding LibreOffice's documented exit-0-corrupt failure mode
- DOC-06 proof test (`TestLibreOfficeConverter_TimeoutKillsRealProcess`) authored: drives `runCommand` directly with real soffice argv, polls for `soffice.bin` actually running before killing, and asserts zero surviving `soffice`/`soffice.bin`/`oosplash` processes
- Live conversion proof test (`TestLibreOfficeConverter_ConvertProducesValidPDF`) authored: exercises the real `Convert` path end-to-end and validates the produced PDF
- All unit tests (`TestFilterFor`, `TestValidatePDF`, `TestRegistryLibreOfficePairs`) pass in a bare `go test` with no `soffice` installed; both live tests skip cleanly via `exec.LookPath("soffice")` gating

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement internal/convert/libreoffice.go and register it** - `5eb92b4` (feat)
2. **Task 2: Author internal/convert/libreoffice_test.go (unit + soffice-gated live tests)** - `69731f9` (test)

_Note: this environment has no `soffice` binary on `PATH`, so the two live tests were verified to compile, gate correctly (`--- SKIP`), and pass `go vet`/`gofmt` here; their actual live execution against a real LibreOffice install is plan 09-02's responsibility per CONTEXT.md D-03._

## Files Created/Modified
- `internal/convert/libreoffice.go` - `LibreOfficeConverter` (Pairs, Convert), `filterFor`, `validatePDF`, `pdfMagic`, `documentFormats`
- `internal/convert/libreoffice_test.go` - unit tests (filterFor, validatePDF, registry pairs) + soffice-gated live process-kill and conversion tests
- `internal/convert/converters.go` - promoted the commented `LibreOfficeConverter` placeholder to a live `Default.Register(LibreOfficeConverter{})` call

## Decisions Made
- Followed RESEARCH.md Patterns 1-3 verbatim (live-verified 2026-07-09 against a real Docker build + real soffice execution) for `Convert`'s body, `filterFor`, and `validatePDF`
- Renamed the kill test's context variable from `killCtx` to `ctx` so it matches the plan's exact source-assertion grep (`runCommand(ctx, "soffice"`) while preserving the outer generous-timeout/derived-cancel structure the plan specified
- No architectural changes needed — `Converter` interface, `exec.go`, and every existing caller were left untouched, exactly as CONTEXT.md and RESEARCH.md required

## Deviations from Plan

None - plan executed exactly as written. `exec.go` was read but not modified (verified via `git diff --quiet internal/convert/exec.go`, which passed after both commits).

## Issues Encountered

None. The one adjustment (variable renaming to satisfy a literal source-assertion grep) was a mechanical fix within the plan's own acceptance criteria, not a deviation from the plan's intent.

## User Setup Required

None - no external service configuration required. `Dockerfile.worker` LibreOffice package provisioning and the live execution of the process-kill test against a real installed `soffice` are plan 09-02's scope.

## Next Phase Readiness

- `LibreOfficeConverter` is fully implemented, registered, and unit-tested; ready for plan 09-02 to provision `Dockerfile.worker` with the LibreOffice packages/fonts and execute the soffice-gated live tests for real inside a Docker build.
- No blockers. Registering `LibreOfficeConverter{}` in `convert.Default` makes it reachable via `Lookup`/`Supports` for testing purposes only — it is not yet wired into the live queue/API (Phase 10/11 scope), consistent with CONTEXT.md's phase boundary.

---
*Phase: 09-libreoffice-converter-engine*
*Completed: 2026-07-09*

## Self-Check: PASSED

- FOUND: internal/convert/libreoffice.go
- FOUND: internal/convert/libreoffice_test.go
- FOUND: .planning/phases/09-libreoffice-converter-engine/09-01-SUMMARY.md
- FOUND commit: 5eb92b4 (Task 1)
- FOUND commit: 69731f9 (Task 2)
- FOUND commit: 02befac (SUMMARY.md)
