---
phase: 14-validated-conversion-options-pdf-a-export
verified: 2026-07-11T12:00:00Z
status: passed
score: 9/9 must-haves verified
overrides_applied: 0
---

# Phase 14: Validated Conversion Options & PDF/A Export Verification Report

**Phase Goal:** Клиенты могут безопасно передавать опции конвертации через `opts` (закрытый allowlist, без сырого попадания в CLI/filter-JSON движка), и первый реальный потребитель этого механизма — PDF/A-архивный экспорт документов.
**Verified:** 2026-07-11T12:00:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 (SC1) | `POST /v1/jobs` accepts `opts` validated against a closed allow-list (typed Go struct); unrecognized/invalid opts → 422; no client-supplied bytes reach engine CLI args/filter JSON verbatim, proven by a targeted injection test | ✓ VERIFIED | `internal/convert/opts.go` `DocOpts`/`ParseDocOpts` (DisallowUnknownFields + single-value allow-list); `internal/api/handlers.go:249-279` validates before `s.storage.Upload`; `TestCreateJob_OptsUnknownKeyRejected`, `TestCreateJob_OptsInjectionAttempt`, `TestDocOptsInjectionResistance` (5 adversarial cases) all PASS (`go test ./internal/api/... ./internal/convert/... -run Opts\|Injection -v` re-run by verifier, all green) |
| 2 (SC2) | A client can request PDF/A-2b export for document→pdf via `opts`; produced PDF is live-verified to carry a PDF/A OutputIntent marker | ✓ VERIFIED | Code: `internal/convert/opts.go` `PDFAFilterOptions` (server-constant suffix) + `internal/convert/libreoffice.go:213-229` (`/GTS_PDFA` check). Live evidence: 14-03-SUMMARY.md records a fresh docker-compose run where `TestPDFAExportE2E/PDFA` PASSED and the downloaded PDF's exact OutputIntent object was manually inspected: `<</Type/OutputIntent/S/GTS_PDFA1/.../>>`; checkpoint approved by user 2026-07-11 (per task instructions, this recorded live-run is treated as the live-verification source and was not re-run) |
| 3 (SC3) | Existing document→pdf jobs without `opts` continue converting successfully with no regression — live e2e verified | ✓ VERIFIED | Code: no-opts path leaves `normalizedOpts` nil, `isPDFA=false`, identical argv to pre-phase behavior (`internal/convert/libreoffice.go:80-86`). Live evidence: 14-03-SUMMARY.md matrix shows `TestPDFAExportE2E/NoOptsRegression` PASS plus the full pre-existing 12-pair document/cross-format regression suite PASS on the same live run |
| 4 | A job created with a non-empty Opts value persists it and reads back the identical normalized value (14-01) | ✓ VERIFIED | `internal/jobs/repo.go:93-95` marshals `p.Opts`; `:335` unmarshals into `j.Opts`; `TestOptsRoundTrip` compiles/self-skips offline (confirmed by verifier), SUMMARY records it PASSING live against local Postgres |
| 5 | A job created without opts stores the inert default `{}` and reads back empty/nil Opts | ✓ VERIFIED | `internal/jobs/repo.go:90-95` (nil → `{}` literal, NOT NULL column); `TestOptsRoundTripNilDefault` present and compiles; live-run per SUMMARY |
| 6 | A validated DocOpts with pdf_profile=pdf/a-2b produces a soffice `--convert-to` argument built ONLY from server constants | ✓ VERIFIED | `internal/convert/opts.go:92-97` `PDFAFilterOptions` returns a compile-time constant string keyed only on the validated enum; `internal/convert/libreoffice.go:72,80-86` appends it to a single argv element (no shell) |
| 7 | A document→pdf conversion that requested pdf_profile but whose output lacks `/GTS_PDFA` fails terminally, no retry | ✓ VERIFIED | `internal/convert/libreoffice.go:226-228` error string `"output missing PDF/A OutputIntent marker"`; `internal/worker/worker.go:60` (`terminalLibreOfficeSignatures`) contains the matching lowercased substring — coupled in the same plan/commit (D-06); `TestValidatePDFAOutputIntent` PASS |
| 8 | DocOpts is a closed struct whose only accepted pdf_profile value is `pdf/a-2b`, and the worker strict-re-parses persisted opts without duplicating business validation | ✓ VERIFIED (with caveat) | `internal/convert/opts.go` single allow-list constant `pdfProfileA2b`; `internal/worker/worker.go:256-264` calls `convert.DocOptsFromMap(job.Opts)` and SkipRetry's on error without re-running applicability rules. **Caveat:** code review (14-REVIEW.md WR-01) found `ParseDocOpts` is not fully strict — it accepts trailing JSON data after the first value, a bare `null`, and silently resolves duplicate keys to "last wins" (independently reproduced by this verifier). This does not violate the phase's security invariant (PDFAFilterOptions only ever switches on the parsed enum field, never raw bytes — confirmed by the injection test), but the "strict" claim in the plan's must-have wording is weaker than documented. See Anti-Patterns/Review Cross-Check below. |
| 9 | Accepted opts are normalized, persisted, and echoed in create/get responses with `omitempty` for empty opts | ✓ VERIFIED | `internal/api/handlers.go:271-280` (marshal/unmarshal round-trip of validated struct, never raw bytes — D-08); `:344-345` (201 echo, `len>0` gate); `handleGetJob` (`:385-386`, `len(job.Opts) > 0` gate) — D-09. `TestCreateJob_OptsAccepted`, `TestCreateJob_NoOptsResponseUnchanged`, `TestGetJob_OptsEcho`, `TestGetJob_NoOptsOmitted` all PASS |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/jobs/jobs.go` | `Job.Opts map[string]any` | ✓ VERIFIED | Field present, documented (line 32-35) |
| `internal/jobs/repo.go` | `CreateParams.Opts`, Create writes/Get reads `options` column | ✓ VERIFIED | Lines 76, 93-95 (write), 335 (read) |
| `internal/jobs/repo_test.go` | Opts round-trip test | ✓ VERIFIED | `TestOptsRoundTrip`, `TestOptsRoundTripNilDefault` present, compile, self-skip cleanly offline |
| `internal/convert/opts.go` (new) | `DocOpts`, `ParseDocOpts`, `DocOptsFromMap`, `ValidateApplicability`, `PDFAFilterOptions` | ✓ VERIFIED | All five identifiers exported and implemented as specified |
| `internal/convert/libreoffice.go` | Convert consumes opts; PDF/A filter suffix; OutputIntent check | ✓ VERIFIED | Lines 54-117 (Convert), 213-229 (validateDocumentOutput) |
| `internal/convert/libreoffice_test.go` | Builder + injection + OutputIntent tests | ✓ VERIFIED | `TestPDFAFilterOptions`, `TestDocOptsInjectionResistance`, `TestValidatePDFAOutputIntent` all present and pass |
| `internal/worker/worker.go` | job.Opts threaded, strict re-parse, OutputIntent terminal signature | ✓ VERIFIED | Lines 60 (signature), 262-264 (strict re-parse), 426 (`conv.Convert(..., job.Opts)`) |
| `internal/api/handlers.go` | opts parse/validate/persist/echo | ✓ VERIFIED | Lines 27-32 (consts), 249-280 (validation block), 303 (CreateParams), 344-345/385-386 (echo) |
| `internal/api/handlers_test.go` | accept/422/echo unit tests | ✓ VERIFIED | 10 opts-focused tests, all PASS on re-run |
| `internal/e2e/e2e_test.go` | PDF/A happy path + negative + no-opts regression | ✓ VERIFIED | `TestPDFAExportE2E`, `TestOptsRejectionE2E`, `assertDownloadIsPDFA` present; suite compiles and self-skips offline (re-run confirmed) |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| `internal/jobs/repo.go Create` | `jobs.options` jsonb column | `json.Marshal(p.Opts)` bound param | ✓ WIRED | Line 93-95, `$8` column confirmed in INSERT |
| `internal/jobs/repo.go Get` | `Job.Opts` | `json.Unmarshal` of scanned bytes | ✓ WIRED | Line 335 |
| `internal/worker/worker.go process()` | `conv.Convert(...) opts` | `job.Opts` replacing hardcoded nil | ✓ WIRED | Line 426: `conv.Convert(attemptCtx, inPath, outPath, job.Opts)` |
| `internal/convert/libreoffice.go Convert` | `soffice --convert-to` filter-options | server-constant PDF/A builder | ✓ WIRED | Lines 72, 80-86 |
| `internal/convert/libreoffice.go OutputIntent error` | `internal/worker/worker.go terminalLibreOfficeSignatures` | lowercased substring match | ✓ WIRED | `"output missing PDF/A OutputIntent marker"` (libreoffice.go:227) ↔ `"output missing pdf/a outputintent marker"` (worker.go:60) |
| `internal/api/handlers.go handleCreateJob` | `convert.ParseDocOpts` / `convert.ValidateApplicability` | two-step validation before storage.Upload | ✓ WIRED | Lines 257, 263, ordering confirmed before line ~283 `s.storage.Upload` |
| `internal/api/handlers.go handleCreateJob` | `jobs.CreateParams.Opts` | normalized DocOpts map | ✓ WIRED | Line 303: `Opts: normalizedOpts` |
| `internal/e2e/e2e_test.go` | `/GTS_PDFA` marker in downloaded PDF | `assertDownloadIsPDFA` | ✓ WIRED | Lines 415-434; live-confirmed per 14-03-SUMMARY.md |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full repo build | `go build ./...` | exit 0 | ✓ PASS |
| Full repo vet | `go vet ./...` | exit 0, no output | ✓ PASS |
| Full repo test suite | `go test ./... -count=1` | all packages `ok` | ✓ PASS |
| Injection resistance (convert pkg) | `go test ./internal/convert/... -run 'PDFA\|Injection\|OutputIntent' -v` | all subtests PASS | ✓ PASS |
| Opts API unit tests | `go test ./internal/api/... -run Opts -v` | 10/10 PASS | ✓ PASS |
| Opts jobs round-trip (offline) | `go test ./internal/jobs/... -run Opts -v` | compiles, self-skips cleanly (no DATABASE_URL) | ✓ PASS |
| E2E suite offline | `go test ./internal/e2e/... -count=1` | `ok`, self-skips cleanly (no E2E_BASE_URL) | ✓ PASS |
| `ParseDocOpts` strictness probe (independent reproduction of WR-01) | ad-hoc `_test.go` with trailing-data/`null`/duplicate-key inputs | all 4 adversarial inputs accepted without error (confirms WR-01) | ⚠️ CONFIRMED GAP (non-blocking, see caveat on truth #8) |

### Probe Execution

No `scripts/*/tests/probe-*.sh` style probes declared or found for this phase; live acceptance was executed by the wave-3 executor per the checkpoint protocol and is treated as the live-verification source per task instructions (docker-compose was not re-run by this verifier).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|--------------|--------|----------|
| OPTS-01 | 14-01, 14-02, 14-03 | Client can pass validated `opts` in `POST /v1/jobs`; closed allow-list (typed Go struct); invalid → 422; client bytes never reach CLI/filter-JSON raw | ✓ SATISFIED | Truths 1, 4, 5, 6, 8, 9 above |
| OPTS-02 | 14-02, 14-03 | Client can request PDF/A-2b export via opts; output carries PDF/A OutputIntent marker (sanity check; full veraPDF accepted residual risk) | ✓ SATISFIED | Truths 2, 7 above |

No orphaned requirements found — `.planning/REQUIREMENTS.md` maps only OPTS-01 and OPTS-02 to Phase 14, and both are declared across the phase's plans' `requirements:` frontmatter.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/convert/opts.go` | 39-49 | `ParseDocOpts` uses `json.Decoder.Decode` which only consumes the first JSON value — trailing data, a bare `null`, and duplicate keys ("last wins") are silently accepted despite the doc comment claiming "strict-decodes" (14-REVIEW.md WR-01; independently reproduced by this verifier) | ⚠️ Warning | Does not break the phase's core security invariant (no client bytes reach argv — `PDFAFilterOptions` only switches on the validated enum field, proven by the injection test), but the "strict" claim overpromises; a future caller trusting "ParseDocOpts rejected it or it's clean" inherits this hole |
| `internal/worker/worker.go` | 262-264 | Corrupt-opts terminal path (`SkipRetry`) skips `MarkFailed`, stranding the job in `queued` with no error record until the reconciler eventually exhausts recoveries (14-REVIEW.md WR-02) | ⚠️ Warning | Only reachable via DB corruption of `jobs.options` (not via the validated API write path); degrades operator visibility but does not cause a false "done"/security bypass |
| `internal/convert/libreoffice.go` | 80-86 | PDF/A suffix is appended whenever `pdf_profile` is set in opts, without an explicit `targetFormat == "pdf"` guard inside `Convert` itself (relies on API-layer `ValidateApplicability` having already enforced this) (14-REVIEW.md WR-03) | ⚠️ Warning | Defense-in-depth gap only reachable via DB corruption or a future write path bypassing the API; current API surface is unaffected |
| `internal/worker/worker.go` | 278-285 | `MarkFailed` error is discarded before unconditionally enqueuing a webhook (14-REVIEW.md WR-04) | ℹ️ Info | Pre-existing pattern, not new to this phase (also present on the image path); tangential to OPTS-01/02 |
| `internal/api/handlers_test.go` | 1023-1045 | Oversize-opts test payload also fails the enum allow-list, so it cannot detect regression of the size cap specifically (14-REVIEW.md WR-05) | ℹ️ Info | Test-quality gap only; the size cap itself is present and functions correctly (independently confirmed: `TestCreateJob_OptsOversizeRejected` PASS, code path at handlers.go:252 present) |

No `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` debt markers found in any of the 10 files this phase modified.

### Human Verification Required

None. The phase's one `checkpoint:human-verify` task (14-03 Task 3) was already executed and approved by the user on 2026-07-11 against a fresh docker-compose stack, per the recorded matrix in 14-03-SUMMARY.md. Per this verification task's explicit instructions, that recorded live-run evidence is the accepted source for success criteria 2 and 3 and was not re-executed by this verifier.

### Gaps Summary

No blocking gaps. All 9 merged must-have truths (3 ROADMAP success criteria + 6 plan-level truths) are verified against the actual codebase: the closed `DocOpts` allow-list, the fail-closed 422 validation ordering before any storage write, the server-constant-only PDF/A filter builder, the OutputIntent terminal-classification coupling, the options persistence round-trip, and the create/get response echo semantics are all implemented and covered by passing unit tests (independently re-run by this verifier, not just trusted from SUMMARY.md). Success criteria 2 and 3 rely on the wave-3 executor's live docker-compose run (recorded in 14-03-SUMMARY.md, user-approved), per this task's explicit instruction not to re-run docker-compose.

The code review (14-REVIEW.md, 0 Critical / 5 Warning / 3 Info) identified real, independently-reproduced weaknesses — most notably WR-01 (`ParseDocOpts` is not fully strict: accepts trailing JSON data, `null`, and duplicate keys) and WR-02 (a corrupted `jobs.options` value strands a job in `queued` instead of marking it failed). Neither breaks the three ROADMAP success criteria or the milestone's stated highest-severity invariant (client bytes never reaching the engine argv — proven by the injection test and confirmed by this verifier's own trace). These are legitimate follow-up items; they do not block Phase 14's goal achievement but are worth tracking (e.g., as a fast-follow fix or an explicit backlog item) before they compound in a future phase that reuses this opts mechanism (Phase 15's HTML-03 print options, per REQUIREMENTS.md, reuses "тот же validated-opts механизм").

---

_Verified: 2026-07-11T12:00:00Z_
_Verifier: Claude (gsd-verifier)_
