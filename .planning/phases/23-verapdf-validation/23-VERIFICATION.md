---
phase: 23-verapdf-validation
verified: 2026-07-13T00:00:00Z
status: passed
score: 12/12 must-haves verified
overrides_applied: 0
---

# Phase 23: veraPDF ISO 19005 Validation Verification Report

**Phase Goal:** PDF/A-2b outputs validated for real ISO 19005 conformance via veraPDF in document-worker; non-compliant export fails terminally; JVM cost measured BEFORE wiring (go/no-go passed).
**Verified:** 2026-07-13
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | veraPDF CLI bundled into Dockerfile.document-worker via pinned tag, glibc-compat verified live (D-03) | VERIFIED | `Dockerfile.document-worker:17` `FROM verapdf/cli:v1.30.2 AS verapdf`; runtime stage installs `openjdk-17-jre-headless` (path B, jlink path A live-failed per 23-01-SUMMARY) + `COPY --from=verapdf /opt/verapdf /opt/verapdf`; inline comment documents the musl->glibc failure discovered live |
| 2 | JVM cold-start p95 measured over ≥10 real runs BEFORE wiring, go/no-go gate honored (D-01/D-02) | VERIFIED | `scripts/verapdf-measure.sh` (7335 bytes, present); 23-01-SUMMARY.md records raw per-run numbers (4650,3771,3502,...) and nearest-rank p95=4650ms vs 10s budget; Plan 02 (wiring) commit `4e36a73` postdates Plan 01's measurement commits (`5ff59e0`, `c83248e`) — gate ordering respected |
| 3 | Real LibreOffice PDF/A-2b export validates isCompliant=true under real veraPDF (Pitfall 9 regression canary) | VERIFIED | 23-01-SUMMARY.md: "144 passed rules / 0 failed"; independently confirmed live in 23-03 (`TestPDFAExportE2E` PASS, PDFA subtest 8.03s) against the rebuilt image |
| 4 | For a wantPDFA job, after /GTS_PDFA pre-filter passes, veraPDF runs full ISO 19005-2b validation (D-05) | VERIFIED | `internal/convert/libreoffice.go:232-258` `validateDocumentOutput`: marker `bytes.Contains` check runs first (line 245), `ValidatePDFA(ctx, path)` called only after it passes and only when `wantPDFA` (line 255) |
| 5 | Non-compliant OR veraPDF error/timeout on PDF/A job returns terminal-classified error — fail-closed (D-06) | VERIFIED | `internal/convert/verapdf.go:137-146` `validateReport`: parse error -> `"pdf/a validation error"`; non-compliant -> `"pdf/a non-compliant"`; `ValidatePDFA` (line 166-180) also routes any runCommand error with empty stdout (incl. wrapped `context.DeadlineExceeded` on timeout) to `"pdf/a validation error"`. `internal/worker/worker.go:109-112` `terminalVeraPDFSignatures` consumed by `isTerminal`; `TestIsTerminalVeraPDFSignatures`/`TestIsDocumentTerminalVeraPDFSignatures` pass offline |
| 6 | veraPDF invoked through hardened runCommand (Setpgid) with its own VERAPDF_TIMEOUT bound (D-04) | VERIFIED | `internal/convert/verapdf.go:170` `runCommand(vctx, "verapdf", "-f", "2b", "--format", "xml", path)`; `vctx` derived via `context.WithTimeout(ctx, effectiveVeraPDFTimeout())` (line 167); `runCommand` itself unchanged Setpgid+SIGKILL hardening (`exec.go:30,47`) |
| 7 | VERAPDF_TIMEOUT read ONLY in cmd/document-worker/main.go, injected via SetVeraPDFTimeout — no os.Getenv in internal/convert | VERIFIED | `cmd/document-worker/main.go:81` `convert.SetVeraPDFTimeout(envDuration("VERAPDF_TIMEOUT", 60*time.Second))`, placed before `srv.Start(mux)` (line 97); `grep os.Getenv internal/convert/` returns no matches; `verapdf.go:18-36` package-level setter/getter pattern mirrors `engineTimeout` |
| 8 | terminalVeraPDFSignatures + verapdf.go error strings + ctx-threading land in ONE atomic same-commit (D-06/D-04 discipline) | VERIFIED | `git show 4e36a73 --stat` — single commit touches `internal/convert/verapdf.go` (new), `internal/worker/worker.go`, `internal/convert/libreoffice.go`, `cmd/document-worker/main.go`, plus test files, in one commit |
| 9 | veraPDF non-conformance reason reaches job_events via existing MarkFailed detail path — no new internal/ logging (D-07) | VERIFIED | Live-verified in 23-03: `TestPDFANonCompliantE2E` queries `job_events.detail->>'engine_stderr'` directly via Postgres and asserts it contains `"pdf/a non-compliant"` (`internal/e2e/e2e_test.go:711-721`); SUMMARY records the actual captured string including ISO clause 6.6.4 detail |
| 10 | Machine-report parser unit-tested against committed fixtures (D-08); libreoffice_test.go's 8 call sites compile against new ctx signature | VERIFIED | `go test ./internal/convert/... ./internal/worker/... -count=1 -v` (re-run by verifier) — all pass; `TestParseVeraPDFReportCompliant/NonCompliant/Unparseable/BatchSummaryFailure`, `TestValidateReportCompliant/NonCompliant/Unparseable`, `TestSetVeraPDFTimeout` all PASS; all 8 `validateDocumentOutput(...)` call sites in `libreoffice_test.go` pass a leading `ctx` (verified via grep) |
| 11 | LIVE UNCONDITIONAL: compliant PDF/A-2b export reaches status=done against rebuilt image, no regression (D-09, SC#2) | VERIFIED | 23-03-SUMMARY.md: `TestPDFAExportE2E` PASS (12.10s) against rebuilt compose stack; `NoOptsRegression` subtest also PASS |
| 12 | LIVE UNCONDITIONAL: marker-bearing non-compliant PDF fails job TERMINALLY (status=failed, no retry loop) with reason in job_events (D-09, SC#1) | VERIFIED | 23-03-SUMMARY.md: `TestPDFANonCompliantE2E` PASS (6.09s, well inside 5-min no-retry bound); fixture content independently re-verified by this verifier: `verapdf_noncompliant_marker.pdf` contains `GTS_PDFA` marker + `pdfXid` corruption + `%PDF-` magic; `trigger.docx` contains the `VERAPDF-E2E-NONCOMPLIANT-TRIGGER` canary; shim script (`internal/e2e/testdata/verapdf-shim/soffice`) correctly implements pass-through/injection logic |

**Score:** 12/12 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `Dockerfile.document-worker` | veraPDF bundled via pinned multi-stage COPY | VERIFIED | `verapdf/cli:v1.30.2` stage + path-B Debian JRE runtime install, `PATH` extended |
| `docker-compose.yml` | `platform: linux/amd64` pin + `VERAPDF_TIMEOUT` env | VERIFIED | Both present (lines 221, 253); no shim/fault-injection content (production-clean) |
| `.env.example` | `VERAPDF_TIMEOUT` documented | VERIFIED | Line 42, alongside `DOCUMENT_ENGINE_TIMEOUT` |
| `internal/convert/verapdf.go` | `ValidatePDFA`, `SetVeraPDFTimeout`, fail-closed parser | VERIFIED | All exports present, substantive (180 lines), wired into `libreoffice.go` |
| `internal/convert/verapdf_test.go` | Offline parser tests over fixtures | VERIFIED | 8 tests, all pass, zero binary dependency |
| `internal/convert/exec.go` | `runCommand` stdout capture | VERIFIED | Signature `([]byte, error)`; all 3 other callers (vips, soffice, chromium) updated mechanically, tests green |
| `internal/worker/worker.go` | `terminalVeraPDFSignatures` consumed by `isTerminal` | VERIFIED | Present, same-commit as verapdf.go (confirmed via `git show --stat`) |
| `cmd/document-worker/main.go` | `SetVeraPDFTimeout` call before `srv.Start` | VERIFIED | Line 81, before line 97 |
| `internal/e2e/e2e_test.go` | `TestPDFANonCompliantE2E` + `pollUntilFailed` | VERIFIED | Both present, functionally correct against job_events query |
| `internal/e2e/testdata/verapdf_noncompliant_marker.pdf` | Marker-bearing non-compliant fixture | VERIFIED | Content independently confirmed (GTS_PDFA + pdfXid corruption + %PDF-) |
| `internal/e2e/testdata/trigger.docx` | Canary-bearing docx | VERIFIED | Canary string confirmed present in raw bytes |
| `internal/e2e/testdata/verapdf-shim/soffice` | E2E-only interposer | VERIFIED | 0755, correct argv parsing, transparent pass-through logic reviewed |
| `docker-compose.e2e.yml` | Shadow-mount override, e2e-only | VERIFIED | Confined to this file; production `docker-compose.yml` has no shim reference |
| `scripts/verapdf-measure.sh` | Measurement harness | VERIFIED | 7335 bytes, present, committed |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `validateDocumentOutput` wantPDFA branch | `ValidatePDFA` | call after `/GTS_PDFA` pre-filter | WIRED | `libreoffice.go:245-255` — marker check first, then `ValidatePDFA` |
| `ValidatePDFA` | `runCommand` | hardened exec + VERAPDF_TIMEOUT sub-context | WIRED | `verapdf.go:167-170` |
| `isTerminal` | `terminalVeraPDFSignatures` | substring loop | WIRED | `worker.go:162` loop consumes the slice (confirmed via grep + passing tests) |
| `cmd/document-worker/main.go` | `convert.SetVeraPDFTimeout` | `envDuration` at startup, before `srv.Start` | WIRED | `main.go:81` precedes `main.go:97` |
| `docker-compose.e2e.yml` shim mount | document-worker soffice resolution | shadow mount over `/usr/local/bin/soffice` | WIRED | Confirmed in compose file; PATH not duplicated |
| `TestPDFANonCompliantE2E` | `job_events` | live Postgres query | WIRED | `e2e_test.go:711-721`, direct SQL query against `detail->>'engine_stderr'` |

### Data-Flow Trace (Level 4)

Not applicable in the UI-rendering sense (this phase has no frontend component). The equivalent trace — verdict flows from the veraPDF process's real stdout through `parseVeraPDFReport` into the terminal-error path into `job_events` — was traced end-to-end above (Truths 5, 9, 12) and independently confirmed by re-running the offline test suite plus grep-verifying the live-captured fixture content and shim logic.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Offline convert/worker test suite green | `go test ./internal/convert/... ./internal/worker/... -count=1 -v` | All PASS; `verapdf`/`soffice` LookPath-gated tests correctly SKIP | PASS |
| `go build ./...` | `go build ./...` | Clean, no errors | PASS |
| `go vet ./...` | `go vet ./...` | Clean | PASS |
| `gofmt -l .` | `gofmt -l .` | No output (clean) | PASS |
| Same-commit discipline | `git show 4e36a73 --stat` | verapdf.go + worker.go + libreoffice.go + main.go in ONE commit | PASS |
| No new Go deps | `git log c45bda0..HEAD -- go.mod go.sum` | Empty (no changes) | PASS |
| Production compose has no shim | inspection of `docker-compose.yml` | No shim/fault-injection references | PASS |
| Fixture content matches claims | `grep -a` for GTS_PDFA, pdfXid, %PDF-, trigger canary | All present as claimed | PASS |

**Live e2e execution (Docker-based, requires full stack up under amd64 emulation) was NOT independently re-run by this verifier** — this matches the verification-facts guidance ("only re-run live if you find code-level cause for doubt"). No such cause was found: the code paths, fixture contents, and test logic that the live SUMMARY claims to have exercised were all independently inspected and are internally consistent and correctly wired. The SUMMARY's claimed live results (exact error string with ISO clause 6.6.4 detail, timing numbers, PASS status for all 3 tests) are plausible, specific, and consistent with the code — not vague or templated claims.

### Probe Execution

No `scripts/*/tests/probe-*.sh` convention used in this phase; `scripts/verapdf-measure.sh` is a measurement harness (Plan 01), not a probe in the gates.md sense, and was not re-executed live per the same rationale as above (Docker/amd64-emulation live run, not required absent code-level doubt).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|--------------|------------|--------------|--------|----------|
| PDFA-01 | 23-02, 23-03 | Real ISO 19005 validation via veraPDF; non-compliant export fails terminally | SATISFIED | `ValidatePDFA` fail-closed wiring (Truths 4,5) + live terminal-fail proof (Truth 12) |
| PDFA-02 | 23-01, 23-02, 23-03 | veraPDF packaged via multi-stage COPY, hardened exec, own timeout, same-commit terminal signatures | SATISFIED | Truths 1,6,7,8 |

No orphaned requirements — REQUIREMENTS.md maps only PDFA-01/PDFA-02 to Phase 23, and both are claimed by the plans.

### Anti-Patterns Found

None. Scanned all key files modified in this phase (`verapdf.go`, `verapdf_test.go`, `exec.go`, `libreoffice.go`, `worker.go`, `main.go`, `e2e_test.go`, `docker-compose.e2e.yml`, `Dockerfile.document-worker`) for `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` and stub patterns. The single match ("placeholder" in a code comment in `verapdf_test.go:57`) is a plain-English test-comment describing what the test assertion avoids ("not just a generic placeholder") — not a stub marker.

### Notable Reviewed Deviations (all judged sound)

- `runCommand` signature extended from `error` to `([]byte, error)` — necessary because veraPDF's report is stdout-only; all 3 other callers (vips, libvips.go:31, chromium.go:187, libreoffice.go:109) mechanically updated to discard the new first return value with zero behavior change, confirmed by passing pre-existing tests.
- Docker tag correction `verapdf/cli:1.30.2` -> `v1.30.2` (leading "v") — verified against the live Docker Hub registry.
- glibc path A (jlink JRE) live-failed, fell back to documented path B (Debian JRE) exactly as the plan anticipated and instructed.
- `platform: linux/amd64` pin added to `docker-compose.yml` — necessary consequence of the pinned `verapdf/cli` image having no arm64 manifest; documented and scoped to the `document-worker` service only.
- D-06-supersedes-SC#2 severity reconciliation: ROADMAP SC#2's earlier "Warning vs Error" phrasing is explicitly superseded by 23-CONTEXT.md's locked D-06 decision (no warning tier — always terminal). This is a documented, intentional resolution of a phrasing conflict between an earlier ROADMAP draft and the later, more specific CONTEXT.md decision — not a scope reduction. No override needed; the locked decision (D-06) is authoritative per the phase's own context file.

### Human Verification Required

None. All must-haves are either verified directly against code/tests by this verifier, or verified via the phase's own live e2e hard gate (23-03), whose claimed evidence (exact error strings, fixture contents, timing) was independently cross-checked at the code/fixture level and found consistent, specific, and non-templated.

### Gaps Summary

No gaps found. All 12 derived truths (roadmap Success Criteria 1-4 plus PLAN-frontmatter must-haves from all 3 plans) are verified either directly by this verifier (code inspection, offline test re-run, git commit-boundary inspection, fixture byte-content inspection) or via internally-consistent, specific, non-vague live-run evidence in 23-03-SUMMARY.md that this verifier found no code-level reason to distrust.

---

*Verified: 2026-07-13*
*Verifier: Claude (gsd-verifier)*
