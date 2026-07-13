---
phase: 23-verapdf-validation
plan: 02
subsystem: document-conversion
tags: [verapdf, pdf-a, iso19005, libreoffice, worker, terminal-classification, xml-parsing]

# Dependency graph
requires:
  - phase: 23-verapdf-validation
    plan: "01"
    provides: veraPDF CLI bundled into Dockerfile.document-worker, verified CLI contract (-f 2b, --format xml, exit codes), committed compliant/non-compliant .mrr.xml report fixtures
provides:
  - internal/convert.ValidatePDFA(ctx, path) -- real ISO 19005-2b validation wired into the wantPDFA export path, AFTER the cheap /GTS_PDFA marker pre-filter
  - internal/convert.SetVeraPDFTimeout(d) -- env-only-in-main timeout injection seam for VERAPDF_TIMEOUT
  - internal/worker.terminalVeraPDFSignatures -- fail-closed terminal classification for non-compliant/unverifiable PDF/A jobs
  - runCommand's stdout-capture extension (internal/convert/exec.go) -- reusable by any future engine whose CLI reports to stdout
affects: [23-03-verapdf-e2e]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Fail-closed archival validation: any non-compliant OR unverifiable (unparseable/validator-failure) veraPDF verdict is a terminal job failure, never a retry candidate (D-06)"
    - "Authoritative-verdict-over-exit-code: parse the isCompliant attribute + batchSummary failure counters, never trust the process exit code alone (D-09)"
    - "env-only-in-main timeout injection via package-level setter (SetVeraPDFTimeout), mirroring NewHandler's engineTimeout threading -- internal/convert never calls os.Getenv"
    - "runCommand now returns captured stdout alongside the error, so hardened-exec engines whose CLI reports to stdout (not a file) can be added without a parallel exec abstraction"

key-files:
  created:
    - internal/convert/verapdf.go
    - internal/convert/verapdf_test.go
  modified:
    - internal/convert/exec.go
    - internal/convert/libvips.go
    - internal/convert/chromium.go
    - internal/convert/convert_test.go
    - internal/convert/libreoffice.go
    - internal/convert/libreoffice_test.go
    - internal/worker/worker.go
    - internal/worker/worker_test.go
    - cmd/document-worker/main.go

key-decisions:
  - "runCommand (internal/convert/exec.go) extended to capture and return stdout (even on non-zero exit), rather than adding a parallel exec abstraction or shelling through sh -c for redirection -- veraPDF's --format xml report rides on stdout (confirmed by scripts/verapdf-measure.sh's own `verapdf ... > report.xml` redirection), and the existing runCommand had no way to expose it. All other callers (vips, soffice, chromium-headless-shell) were updated to discard the new first return value; behavior for them is unchanged."
  - "terminalVeraPDFSignatures is a SEPARATE slice (not appended to terminalLibreOfficeSignatures), matching worker.go's existing per-engine-family convention (terminalVipsSignatures / terminalLibreOfficeSignatures / terminalChromiumSignatures)."
  - "VERAPDF_TIMEOUT injection: verapdfTimeout is an unexported package-level time.Duration in internal/convert/verapdf.go, written exactly once via SetVeraPDFTimeout(d) from cmd/document-worker/main.go BEFORE srv.Start(mux) (the point asynq worker goroutines begin concurrently reading it -- single write happens-before every concurrent read, no mutex needed). effectiveVeraPDFTimeout() defaults to 60s when never set, so hermetic tests and any caller that skips SetVeraPDFTimeout still get a sane bound. internal/convert never calls os.Getenv directly (env-only-in-main preserved)."
  - "Parser authoritative signal: parseVeraPDFReport reads the isCompliant attribute on the FIRST <job>'s <validationReport> (single-job invocation per veraPDF call in this codebase) as the primary verdict, but treats non-zero batchSummary failedToParse/outOfMemory/veraExceptions/validationReports.failedJobs counters as an OVERRIDING validator-failure signal (per 23-01-SUMMARY's guidance) -- a report can be well-formed XML with isCompliant=false yet still represent a genuine non-compliant verdict (not an error), while a report with any of those counters set represents the validator itself failing to produce a trustworthy verdict at all, which is fail-closed to 'pdf/a validation error' rather than 'pdf/a non-compliant'."
  - "D-06-supersedes-SC#2 severity reconciliation (WARNING-5, explicitly recorded per plan's <output> instruction): ROADMAP SC#2's earlier phrasing implied a possible 'Warning vs Error' tiering for PDF/A conformance failures. D-06's fail-closed policy is a strict blanket rule with NO warning tier -- ValidatePDFA returns exactly two outcomes for a wantPDFA job: nil (genuinely ISO 19005-2b compliant) or a terminal error (non-compliant OR the validator itself could not produce a trustworthy verdict). There is no code path in this plan that logs a PDF/A conformance problem as a warning and still marks the job done. This plan's implementation supersedes SC#2's earlier phrasing: a failed or unverifiable archival claim is ALWAYS a terminal job failure, never a soft warning."
  - "TestValidatePDFAOutputIntent restructure: the marker-absent legs (wantPDFA=true -> OutputIntent pre-filter error, wantPDFA=false -> nil) stay fully offline since they never reach ValidatePDFA. The marker-PRESENT+wantPDFA=true leg now flows past the pre-filter into a real ValidatePDFA call (the synthetic '/GTS_PDFA1' fixture bytes are not a genuine PDF/A document), so it can no longer assert err==nil; it is gated behind exec.LookPath(\"verapdf\") (t.Skip when absent, mirroring the existing soffice-skip precedent) and, when veraPDF IS present, asserts rejection with a terminal veraPDF signature (NOT the OutputIntent substring), proving control passed the marker gate into real validation."

requirements-completed: [PDFA-01, PDFA-02]

# Metrics
duration: 55min
completed: 2026-07-13
---

# Phase 23 Plan 02: veraPDF Go Integration Summary

**ValidatePDFA wired into validateDocumentOutput's wantPDFA branch (after the /GTS_PDFA pre-filter) via the existing hardened runCommand, extended to capture stdout since veraPDF's `--format xml` report is stdout-only; fail-closed terminal classification (terminalVeraPDFSignatures) lands in the same commit, and VERAPDF_TIMEOUT is injected from cmd/document-worker/main.go via SetVeraPDFTimeout.**

## Performance

- **Duration:** ~55 min
- **Started:** 2026-07-13T14:18:00+03:00 (approx.)
- **Completed:** 2026-07-13T14:29:37+03:00
- **Tasks:** 1 auto task (the single atomic implementation task, per D-06/D-04 discipline)
- **Files modified/created:** 11 (9 modified, 2 created)

## Accomplishments
- `internal/convert/verapdf.go`: `ValidatePDFA(ctx, path)` invokes the pinned veraPDF CLI (`-f 2b --format xml`) through the existing hardened `runCommand`, bounded by its own `VERAPDF_TIMEOUT` sub-context derived from the caller's ctx
- `parseVeraPDFReport` reads `isCompliant` off `<validationReport>` as the authoritative verdict (never trusting exit code alone, D-09), cross-checked against `<batchSummary>`'s `failedToParse`/`outOfMemory`/`veraExceptions`/`failedJobs` counters for the validator-failure case
- Fail-closed terminal error strings: `"pdf/a non-compliant"` (with a bounded, D-07-compliant rule-violation summary) and `"pdf/a validation error"` (any invocation/parse/validator failure)
- `validateDocumentOutput` now takes a leading `ctx` and calls `ValidatePDFA` immediately after the existing `/GTS_PDFA` marker pre-filter passes (D-05) -- the marker remains the fast, JVM-cost-avoiding pre-filter it always was
- `terminalVeraPDFSignatures` added to `internal/worker/worker.go` and consumed by `isTerminal` in the SAME commit as the error strings it classifies (D-06/D-04 discipline) -- a non-compliant/unverifiable PDF/A job now fails terminally instead of burning `DOCUMENT_MAX_RETRY` retries
- `VERAPDF_TIMEOUT` read exactly once, in `cmd/document-worker/main.go`, and injected via `convert.SetVeraPDFTimeout(...)` before `srv.Start(mux)` -- `internal/convert` never calls `os.Getenv`
- `runCommand` (`internal/convert/exec.go`) extended to capture and return stdout (even on non-zero exit) -- a small, well-contained Rule-3 fix required because veraPDF's report rides on stdout, not a file; all other callers updated mechanically with zero behavior change
- All 8 `validateDocumentOutput` call sites in `libreoffice_test.go` updated for the new `ctx` param; `TestValidatePDFAOutputIntent` restructured so its live-veraPDF leg is gated behind `exec.LookPath("verapdf")`
- 8 new offline unit tests (`verapdf_test.go`) against Plan 01's committed real `.mrr.xml` fixtures, zero veraPDF binary dependency (D-08); 2 new worker tests proving both terminal signatures classify via `isTerminal`/`isDocumentTerminal`

## Task Commits

1. **Task 1: verapdf.go (parser + hardened invocation) + wire into validateDocumentOutput + terminalVeraPDFSignatures + ctx-thread the 8 test call sites + timeout injection — ONE atomic commit (D-06)** - `4e36a73` (feat)

**Plan metadata:** (this commit, docs: complete plan)

## Files Created/Modified
- `internal/convert/verapdf.go` - `ValidatePDFA`, `parseVeraPDFReport`, `validateReport`, `SetVeraPDFTimeout`, `effectiveVeraPDFTimeout`; the veraPDF XML report schema struct (`veraPDFReport`)
- `internal/convert/verapdf_test.go` - Parser/validateReport/timeout-injection tests against the committed compliant/non-compliant fixtures plus a synthetic batchSummary-failure case, entirely offline
- `internal/convert/exec.go` - `runCommand` now captures stdout into a buffer and returns `([]byte, error)` instead of just `error`; stdout is returned even on non-zero exit (D-09: some engines, like veraPDF, use exit codes to report a valid-but-negative result)
- `internal/convert/libvips.go`, `internal/convert/chromium.go`, `internal/convert/convert_test.go` - Mechanical updates to discard `runCommand`'s new first return value; no behavior change
- `internal/convert/libreoffice.go` - `validateDocumentOutput` takes a leading `ctx`; its Convert call site threads ctx through; the wantPDFA pdf branch calls `ValidatePDFA(ctx, path)` after the marker pre-filter
- `internal/convert/libreoffice_test.go` - All 8 `validateDocumentOutput` call sites updated; `TestValidatePDFAOutputIntent` restructured (see key-decisions); its 2 direct `runCommand` calls updated for the new signature
- `internal/worker/worker.go` - `terminalVeraPDFSignatures` slice + consumption in `isTerminal`
- `internal/worker/worker_test.go` - `TestIsTerminalVeraPDFSignatures`, `TestIsDocumentTerminalVeraPDFSignatures`
- `cmd/document-worker/main.go` - `convert.SetVeraPDFTimeout(envDuration("VERAPDF_TIMEOUT", 60*time.Second))` called before `srv.Start(mux)`

## Decisions Made
See `key-decisions` in the frontmatter for the full rationale on each of the five decisions below (condensed here):
1. Extended `runCommand` to capture/return stdout rather than adding a parallel exec path or shell redirection -- veraPDF's report is stdout-only, confirmed by the Plan 01 measurement script's own `> report.xml` redirection.
2. `terminalVeraPDFSignatures` is a separate slice, matching the existing per-engine convention.
3. `VERAPDF_TIMEOUT` injected via `SetVeraPDFTimeout` called once at startup before `srv.Start(mux)`, mirroring `NewHandler`'s `engineTimeout` threading.
4. `parseVeraPDFReport` treats `batchSummary`'s failure counters as an overriding validator-failure signal, distinct from a genuine `isCompliant=false` verdict.
5. **D-06-supersedes-SC#2 (WARNING-5, explicitly recorded per the plan's `<output>` instruction):** ROADMAP SC#2's earlier "Warning vs Error" phrasing is superseded by D-06's strict fail-closed policy -- there is no warning tier; a non-compliant or unverifiable PDF/A archival claim is ALWAYS a terminal job failure.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Extended `runCommand`'s signature to capture stdout**
- **Found during:** Task 1 (designing `ValidatePDFA`'s invocation of the `verapdf` CLI)
- **Issue:** The plan's `<read_first>` instructs reusing `runCommand` "verbatim" (D-04), but `runCommand` only ever returned `error` -- it discarded stdout entirely (`cmd.Stdout` was left nil). veraPDF's `--format xml` machine-readable report is written to STDOUT, not a file (confirmed by Plan 01's own `scripts/verapdf-measure.sh`, which redirects `verapdf ... > report.xml` via shell). Without stdout capture, `ValidatePDFA` would have no way to read the report at all.
- **Fix:** Extended `runCommand`'s signature from `func(ctx, name, args...) error` to `func(ctx, name, args...) ([]byte, error)`, capturing stdout into a `bytes.Buffer` identically to how stderr was already captured, and returning it even on non-zero exit (veraPDF exits 1 for a non-compliant-but-valid report -- D-09 explicitly warns not to trust exit codes alone). This is an extension of the existing hardened exec function, not a new/parallel exec abstraction -- the Setpgid/process-group-kill hardening is completely unchanged. The three existing callers (`vips`, `soffice`, `chromium-headless-shell`) and their direct test call sites were updated to discard the new first return value; their behavior is unchanged (verified: full test suite green).
- **Files modified:** `internal/convert/exec.go`, `internal/convert/libvips.go`, `internal/convert/chromium.go`, `internal/convert/convert_test.go`, `internal/convert/libreoffice.go` (soffice call site), `internal/convert/libreoffice_test.go` (2 direct `runCommand` calls)
- **Verification:** `go build ./...`, `go vet ./...`, `gofmt -l` clean; full `go test ./...` green (all pre-existing tests for vips/soffice/chromium pass unchanged).
- **Committed in:** `4e36a73` (same commit as the rest of Task 1, per D-06/D-04 atomic-commit discipline -- this was a necessary precondition for `ValidatePDFA` to be implementable at all, not a separable change)

---

**Total deviations:** 1 auto-fixed (Rule 3 -- blocking issue required to make `ValidatePDFA` capable of reading veraPDF's stdout-only report at all, per the plan's own instruction to reuse `runCommand`). No scope creep: the fix only adds a return value; it does not change hardening behavior, timeout semantics, or any existing caller's observable behavior.

## Issues Encountered

None beyond the `runCommand` stdout-capture gap documented above, which was discovered and resolved during Task 1's design (not a runtime failure).

## User Setup Required

None - no external service configuration required. `VERAPDF_TIMEOUT=60s` was already documented in `.env.example` by Plan 01.

## Next Phase Readiness

- **Plan 23-03 (live e2e gate, D-09) is cleared to proceed.** This plan's implementation is offline-verified only (parser fixtures + fixture-driven `validateReport` tests + terminal-classification tests); the actual live invocation of the real `verapdf` binary against a real PDF/A export inside the built document-worker image has NOT been exercised in this environment (no verapdf/soffice binaries available locally -- all `exec.LookPath`-gated tests correctly skipped).
- Plan 23-03 must verify LIVE: (1) a genuine compliant PDF/A-2b export validates `nil` end-to-end through the full `HandleDocumentConvert` path; (2) a deliberately non-compliant PDF/A export (the `verapdf_noncompliant.pdf` fixture Plan 01 committed) fails terminally with the veraPDF reason visible in `job_events.detail`; (3) the exact CLI invocation (`verapdf -f 2b --format xml <path>`) and the stdout-capture assumption both hold against the real pinned `verapdf/cli:v1.30.2` binary -- this plan's design is correct per the measurement script's demonstrated behavior, but has not been exercised via Go code calling the real binary.
- `TestValidatePDFAOutputIntent`'s live leg (marker-present+wantPDFA=true, gated behind `exec.LookPath("verapdf")`) will start exercising the real binary automatically once run inside the document-worker test image -- no further code change needed for that assertion to activate.

---
*Phase: 23-verapdf-validation*
*Completed: 2026-07-13*

## Self-Check: PASSED

All created/modified files verified present on disk (internal/convert/verapdf.go, internal/convert/verapdf_test.go, internal/convert/exec.go, internal/convert/libreoffice.go, internal/convert/libreoffice_test.go, internal/worker/worker.go, internal/worker/worker_test.go, cmd/document-worker/main.go); the task commit (`4e36a73`) verified present in git log.
