# Phase 23: veraPDF ISO 19005 Validation - Context

**Gathered:** 2026-07-13
**Status:** Ready for planning
**Source:** v1.5 research (STACK/FEATURES/ARCHITECTURE/PITFALLS), roadmap phase notes, user-confirmed

<domain>
## Phase Boundary

PDF/A-2b exports get REAL ISO 19005-2b validation via veraPDF inside the document-worker, replacing reliance on the OutputIntent substring sanity-check alone (the cheap `/GTS_PDFA` marker check stays as a pre-filter). Non-compliant export → terminal job failure. CLI-in-container packaging; the `verapdf/rest` daemon is the documented fallback ONLY if the measured JVM cost fails the budget.

</domain>

<decisions>
## Implementation Decisions

### Measurement gate FIRST (roadmap phase note)
- D-01: Before wiring into the job path, MEASURE veraPDF CLI cost inside the built document-worker image on a real PDF/A export: cold-start wall-clock over ≥5 runs. Budget: p95 ≤ 10s per validation (well inside DOCUMENT_ENGINE_TIMEOUT=300s). If the budget FAILS → STOP, record the measurement, present daemon-fallback decision to the operator (do NOT silently switch architectures)
- D-02: Measurement result recorded in the SUMMARY with raw numbers (это снимает research-флаг «no benchmark found»)

### Packaging
- D-03: Multi-stage `COPY --from=verapdf/cli:1.30.2` (pinned tag) into Dockerfile.document-worker; verify glibc/musl compatibility LIVE (STACK flagged: source image Alpine/musl, target Debian/glibc — if the jlink JRE fails to load, fall back to installing a minimal Debian JRE (openjdk-17-jre-headless) + copying only veraPDF's app jars; record which path was taken)
- D-04: Invocation via the existing hardened `runCommand` (internal/convert/exec.go — process-group kill), own timeout env `VERAPDF_TIMEOUT` (default 60s), inside the existing document-worker container only — no new service, no compose topology change beyond the image contents

### Validation semantics
- D-05: Runs ONLY for jobs that requested PDF/A (`wantPDFA` path); plain pdf/cross-format jobs untouched. The cheap `/GTS_PDFA` marker check stays as pre-filter (fast fail before JVM spin-up)
- D-06: veraPDF invoked with flavour 2b (`-f 2b`), machine-readable output; job fails terminally when the report says non-compliant (isCompliant=false) OR veraPDF itself errors/times out on a PDF/A job (fail-closed: an unverifiable archival claim is a failed archival claim). Error strings added to `terminalLibreOfficeSignatures` (or a parallel terminalVeraPDFSignatures consumed the same way) SAME-COMMIT per D-04 discipline (v1.2 convention)
- D-07: The veraPDF failure reason (first N chars of the rule violations summary) lands in job_events for diagnosability — no new logging in internal/

### Verification
- D-08: Unit layer: parser of veraPDF's machine output (fixture JSON/XML samples committed) + terminal-classification tests
- D-09: LIVE HARD GATE: rebuilt document-worker image → real PDF/A-2b export job → veraPDF validates compliant output → job done; plus a deliberately non-compliant PDF (fixture crafted/patched) fed through the validation path → terminal fail with veraPDF reason in job_events. e2e TestPDFAExportE2E extended or a new test added; runs against compose stack (UNCONDITIONAL)
- D-10: CI impact check: the document-worker image grows (~54MB JRE) — docker-build tier must stay within its 20-min bound; note actual build time delta in SUMMARY

### Claude's Discretion
- terminalVeraPDFSignatures as separate slice vs appending to the existing one (keep worker.go conventions)
- Exact veraPDF CLI flags for machine output (verify against the pinned image's actual CLI)
- Where the measurement script lives (scripts/ vs throwaway documented in SUMMARY)

</decisions>

<canonical_refs>
## Canonical References

- `internal/convert/libreoffice.go` — validatePDF, gtsPDFAMarker (line ~213), validateDocumentOutput dispatch (~216) — the seam veraPDF plugs into
- `internal/convert/exec.go` — runCommand hardened exec (process-group kill)
- `internal/worker/worker.go` — terminalLibreOfficeSignatures (~51) + D-04 same-commit discipline comment
- `Dockerfile.document-worker` — packaging target
- `internal/e2e/e2e_test.go` — TestPDFAExportE2E (extends)
- `internal/convert/opts.go` — PDFAFilterOptions / wantPDFA path
- `.planning/research/STACK.md` (verapdf/cli:1.30.2, glibc risk), `FEATURES.md` (always-validate for pdfa, terminal severity), `PITFALLS.md` (JVM cost, false-negative severity policy, version pinning), `SUMMARY.md` (CLI-first, daemon documented fallback)

</canonical_refs>

<specifics>
## Specific Ideas

- Non-compliant fixture: take a compliant PDF/A export and corrupt a required XMP metadata block, or use a plain (non-A) PDF renamed — verify veraPDF actually flags it (don't assume)
- veraPDF exit codes: verify against the real image (0 = ran, compliance in report — do not trust exit code alone)

</specifics>

<deferred>
## Deferred Ideas

- verapdf/rest daemon sidecar (documented fallback, only on measured budget failure)
- Other PDF/A flavours (1b/3b) — only 2b is exported today
</deferred>

---

*Phase: 23-verapdf-validation*
*Context gathered: 2026-07-13*
