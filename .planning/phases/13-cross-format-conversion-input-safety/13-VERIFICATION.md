---
phase: 13-cross-format-conversion-input-safety
verified: 2026-07-10T00:00:00Z
status: passed
score: 15/15 must-haves verified
overrides_applied: 0
---

# Phase 13: Cross-Format Conversion & Input Safety Verification Report

**Phase Goal:** Клиенты могут конвертировать между офисными форматами внутри документного класса (не только → PDF), выход структурно валидируется, legacy/encrypted документы отклоняются на входе.
**Verified:** 2026-07-10
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (Roadmap Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | docx↔odt, xlsx↔ods, pptx↔odp round-trips work through POST /v1/jobs → poll/download → webhook, live e2e verified against a freshly built stack | ✓ VERIFIED | `internal/e2e/e2e_test.go` `crossFormatPairs` table lists all 6 pairs exactly; `TestCrossFormatConversionE2E` drives each through `postJob`→`pollUntilDone`→`assertDownloadIsFormat`. 13-03-SUMMARY.md live-run matrix: all 6 cross pairs PASS (2.0-2.1s each) plus all 6 existing `->pdf` pairs PASS with a verified signed webhook, against a freshly built `docker compose ... --build` stack (25.6s total suite time). Human checkpoint (Task 3, 13-03-PLAN.md) approved by operator 2026-07-10. |
| 2 | Corrupted/mismatched non-PDF output detected structurally before `done` — terminal failure, never false success | ✓ VERIFIED | `validateDocumentOutput` (`internal/convert/libreoffice.go:181-205`) calls `SniffContainer` on non-pdf output and rejects on sniff error or format mismatch with `"output does not match expected container format %s"`. This string plus `"output is empty"`, `"no export filter for"`, `"output missing %pdf- magic bytes"`, and (post-review) `"produced no output file"` are all present in `terminalLibreOfficeSignatures` (`internal/worker/worker.go:51-57`), verified same-commit (2ecaf86) and post-review-commit (78d9535) coupling. Unit tests `TestValidateDocumentOutput`, `TestIsTerminalLibreOfficeSignatures`, `TestIsDocumentTerminal` all pass. Code review (13-REVIEW.md) traced (b) "False done on cross-format paths: none found" — `MarkDone` unreachable without `validateDocumentOutput` passing. |
| 3 | OLE-CFB uploads (legacy .doc AND password-protected .docx, real fixtures) rejected 422 before S3 write, live verified | ✓ VERIFIED | `convert.IsOLECFB` (`internal/convert/olecfb.go:24`) detects the 8-byte magic; wired as a third fail-closed branch in `handleCreateJob` (`internal/api/handlers.go:159-170`), positioned before `s.storage.Upload` (line 243) and `s.repo.Create` (line 250). Handler unit test `TestCreateJob_OLECFBRejected` asserts 422 + `store.uploaded==false` + `repo.created==nil`. Live: `internal/e2e/testdata/legacy.doc` and `encrypted.docx` are genuine CFB files (confirmed `d0cf11e0a1b11ae1` magic via `xxd`), exercised by `TestOLECFBRejectionE2E`; 13-03-SUMMARY.md matrix shows both PASS (422, body mentions "password") in the live run. |

**Score:** 3/3 roadmap success criteria verified

### Plan-Level Must-Haves (D-01..D-07)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| D-01 | Pairs() registers exactly 6 new symmetric cross pairs, no cross-family pairs | ✓ VERIFIED | `crossPairs` literal (`libreoffice.go:21-28`) lists exactly the 6 pairs; `TestRegistryLibreOfficePairs` asserts all 6 supported + forbidden cross-family pairs (e.g. docx->ods) and identity pairs are NOT supported. |
| D-02 | filterFor(sourceExt, targetFormat) explicit two-axis table, no auto-derivation | ✓ VERIFIED | `filterTable map[[2]string]string` (`libreoffice.go:108-122`), no derivation logic. `TestFilterFor` covers all 12 entries + case/alias robustness + unsupported-pair error. Live-confirmed in 13-03: zero filter-name corrections needed against real LO 7.4. |
| D-03 | Non-pdf output validated by convert.SniffContainer; validatePDF still guards ->pdf | ✓ VERIFIED | `validateDocumentOutput` dispatches on target format (`libreoffice.go:181-205`); delegates to `validatePDF` for pdf, `SniffContainer` otherwise. |
| D-04 | Terminal error substring appended in the SAME commit as the validator | ✓ VERIFIED | Commit `2ecaf86` touches both `internal/convert/libreoffice.go` and `internal/worker/worker.go` together (git show --stat confirms). Post-review fix commit `78d9535` similarly touches both files + both test files together for the WR-01/WR-02 additions. |
| Pitfall 1 gate | No hardcoded ".pdf"/"pdf:" literal outside legitimate case "pdf" branches | ✓ VERIFIED | `grep -nE '"\.pdf"\|"pdf:"' internal/convert/libreoffice.go` returns zero lines (ran directly). |
| D-05 | IsOLECFB is a fail-closed branch in handleCreateJob, NOT a sniff.go/docsniff.go registry entry | ✓ VERIFIED | `grep -rn 'IsOLECFB\|oleCFBMagic' internal/convert/sniff.go internal/convert/docsniff.go` returns zero matches (ran directly). `IsOLECFB` lives standalone in `internal/convert/olecfb.go`. |
| D-06 | 422 message names both sub-cases + remedy | ✓ VERIFIED | `handlers.go:167-168`: "legacy binary or password-protected Office format is not supported; convert to docx/xlsx/pptx or remove the password" — names both legacy and password-protected, suggests remedy. |
| CFB pre-storage ordering | CFB rejection runs before s.storage.Upload / s.repo.Create | ✓ VERIFIED | Line-number trace in handlers.go: CFB branch at line 159, `s.storage.Upload` at line 243, `s.repo.Create` at line 250. `TestCreateJob_OLECFBRejected` asserts `store.uploaded==false`. |
| CFB distinct log reason | reason=legacy_or_encrypted_document | ✓ VERIFIED | `grep -q 'reason=legacy_or_encrypted_document' internal/api/handlers.go` matches (handlers.go:166). |
| D-07 | E2E exercises all 6 cross pairs + both CFB fixtures live, SniffContainer-verified download, no regression on existing ->pdf/webhook | ✓ VERIFIED | See SC#1/SC#3 evidence above; 13-03-SUMMARY.md live matrix covers all items with PASS, webhook signature verified, zero regressions. |

**Score:** 12/12 plan-level must-haves verified

**Combined score: 15/15** (3 roadmap SCs + 12 plan-level D-truths, deduplicating overlaps).

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/libreoffice.go` | Target-aware Pairs/filterFor/Convert + validateDocumentOutput dispatcher | ✓ VERIFIED | Contains `validateDocumentOutput`, `crossPairs`, two-axis `filterTable`; WR-01/WR-02 fixes present (size<magic-length short-circuit, `os.Stat(producedPath)` pre-check before rename). |
| `internal/worker/worker.go` | terminalLibreOfficeSignatures extended | ✓ VERIFIED | Contains "output does not match expected container format", "no export filter for", "produced no output file"; stale "no pdf export filter for" absent. |
| `internal/convert/libreoffice_test.go` | Two-key filterFor table test + validateDocumentOutput tests | ✓ VERIFIED | `TestFilterFor` rekeyed to `[2]string`; `TestValidateDocumentOutput` present; `TestValidatePDF` includes the tiny-file (sub-magic) regression case. |
| `internal/convert/olecfb.go` | IsOLECFB detector + oleCFBMagic signature | ✓ VERIFIED | `func IsOLECFB(r io.ReaderAt) bool` present, exported, standalone file. |
| `internal/convert/olecfb_test.go` | Magic-byte true/false unit coverage | ✓ VERIFIED | `go test ./internal/convert/...` passes. |
| `internal/api/handlers.go` | Third fail-closed CFB branch | ✓ VERIFIED | `convert.IsOLECFB` call present, correctly ordered. |
| `internal/e2e/e2e_test.go` | Cross-format E2E table + CFB rejection live test | ✓ VERIFIED | `TestCrossFormatConversionE2E`, `TestOLECFBRejectionE2E`, `assertDownloadIsFormat` all present and wired to `SniffContainer`. |
| `internal/e2e/testdata/legacy.doc` | Genuine OLE-CFB legacy Word97 fixture | ✓ VERIFIED | File exists, magic bytes confirmed `d0cf11e0a1b11ae1`. |
| `internal/e2e/testdata/encrypted.docx` | Genuine password-protected OOXML fixture | ✓ VERIFIED | File exists, magic bytes confirmed `d0cf11e0a1b11ae1`. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/convert/libreoffice.go Convert` | `convert.SniffContainer` | validateDocumentOutput calls SniffContainer for non-pdf targets | ✓ WIRED | `libreoffice.go:200` calls `SniffContainer(f, fi.Size())` inside `validateDocumentOutput`. |
| `internal/worker/worker.go isTerminal` | validateDocumentOutput error string | terminalLibreOfficeSignatures substring match | ✓ WIRED | `worker.go:51-57` slice contains the exact substring `validateDocumentOutput` produces; regression tests confirm the match fires. |
| `internal/api/handlers.go handleCreateJob` | `convert.IsOLECFB` | third detection branch after ZIP branch, before generic-422 fallback | ✓ WIRED | Confirmed line-ordering: ZIP branch (125-158) → CFB branch (159-170) → generic 422 (171-179). |
| `internal/api/handlers.go CFB branch` | http 422 before storage | writeError(StatusUnprocessableEntity) + return, ahead of s.storage.Upload | ✓ WIRED | CFB branch at line 159-170 returns before `s.storage.Upload` at line 243. |
| `internal/e2e/e2e_test.go cross-format assertion` | `convert.SniffContainer` | assertDownloadIsFormat sniffs downloaded bytes | ✓ WIRED | `assertDownloadIsFormat` (e2e_test.go:428-450) calls `SniffContainer`. |
| `internal/e2e/e2e_test.go CFB test` | POST /v1/jobs -> 422 | postJobExpectStatus asserts 422 for both CFB fixtures | ✓ WIRED | `TestOLECFBRejectionE2E` iterates `oleCFBFixtures` calling `postJobExpectStatus(..., http.StatusUnprocessableEntity)`. |

### Data-Flow Trace (Level 4)

Not applicable in the strict sense (no UI/dashboard rendering dynamic data) — the equivalent trace here is the E2E live-run evidence: `internal/e2e/e2e_test.go` exercises the actual `POST /v1/jobs` → real LibreOffice engine → real S3 (MinIO) → download flow against a freshly-built docker-compose stack. 13-03-SUMMARY.md records this ran and passed (25.6s, all 14 sub-tests), with a human checkpoint approval recorded 2026-07-10. This satisfies the "prove it against real infrastructure, not mocks" requirement for this phase.

### Behavioral Spot-Checks (offline, run by this verifier)

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Build clean | `go build ./...` | success | ✓ PASS |
| Vet clean | `go vet ./...` | success | ✓ PASS |
| gofmt clean | `gofmt -l internal/convert internal/worker internal/api internal/e2e` | empty output | ✓ PASS |
| Pitfall 1 gate | `grep -nE '"\.pdf"|"pdf:"' internal/convert/libreoffice.go` | empty output | ✓ PASS |
| D-05 registry-isolation gate | `grep -rn 'IsOLECFB\|oleCFBMagic' internal/convert/sniff.go internal/convert/docsniff.go` | empty output | ✓ PASS |
| Unit tests: convert, worker, api, e2e | `go test ./internal/convert/... ./internal/worker/... ./internal/api/... ./internal/e2e/...` | all `ok` | ✓ PASS |
| Full repo test suite | `go test ./...` | all `ok` (no regressions elsewhere) | ✓ PASS |
| CFB fixture magic bytes | `head -c 8 <file> \| xxd -p` for both fixtures | `d0cf11e0a1b11ae1` for both | ✓ PASS |

### Probe Execution

No `scripts/*/tests/probe-*.sh` convention exists in this project and none is referenced by the PLAN/SUMMARY files for this phase. Step 7c: SKIPPED (no probes declared or conventional probe path present). The phase's live-verification bar is instead met via the E2E Go test suite (`internal/e2e/e2e_test.go`), executed and recorded in 13-03-SUMMARY.md, which this verifier treats as the phase's equivalent acceptance evidence per the task's explicit instruction not to require re-running the live docker stack.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| CONV-01 | 13-01, 13-03 | docx↔odt, xlsx↔ods, pptx↔odp via existing engine through POST /v1/jobs flow | ✓ SATISFIED | 6 cross pairs registered, filter table complete, live E2E all PASS. |
| CONV-02 | 13-01, 13-03 | Invalid/corrupt non-PDF output detected structurally before `done`, terminal not false success | ✓ SATISFIED | validateDocumentOutput + terminalLibreOfficeSignatures coupling; code review traced no false-done path; WR-01/WR-02 gaps closed post-review. |
| SAFE-01 | 13-02, 13-03 | OLE-CFB files rejected 422 before S3 write; legacy vs encrypted distinction out of scope | ✓ SATISFIED | IsOLECFB + handler branch + live fixtures both rejected 422 pre-storage. |

No orphaned requirements — REQUIREMENTS.md traceability table maps CONV-01/CONV-02/SAFE-01 to Phase 13 exclusively, and all three appear in at least one plan's `requirements:` frontmatter field.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| — | — | None found | — | `grep` scan for TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER/"not yet implemented" across all 9 phase-modified files returned zero matches. |

Code review (13-REVIEW.md) identified 2 warnings (WR-01, WR-02) at review time — **both were fixed** in the post-review commit `78d9535` (verified present in current `libreoffice.go`/`worker.go`/both test files, with regression tests). The review's remaining 4 info-level items (IN-01 masked-cause diagnosability, IN-02 doc-comment overclaim about DuplicateRootPart symmetry, IN-03 lack of compile-time coupling between validator strings and classifier, IN-04 `engineTimout` pre-existing typo) are non-blocking code-quality notes, not must-have failures — they do not affect goal achievement (SC#1-3) and are appropriately left as informational.

## Deferred Items

None. IN-01 through IN-04 from 13-REVIEW.md are code-quality/diagnosability notes, not deferred functional gaps — they don't map to any roadmap success criterion or plan must-have, so they are not tracked as deferred items requiring a later-phase match.

## Human Verification Required

None required by this verification. The phase's live-e2e human checkpoint (13-03-PLAN.md Task 3, `checkpoint:human-verify`) was already executed and approved by the operator on 2026-07-10, per 13-03-SUMMARY.md: "Task 3 human-verify checkpoint approved by the operator on 2026-07-10 — live run accepted (12 conversion pairs + 2 CFB 422 cases, all PASS)." Per this verification task's explicit instruction, the live docker stack run is not re-executed; the evidence trail (result matrix + operator approval) is accepted as sufficient.

## Gaps Summary

No gaps. All 3 roadmap success criteria and all 12 plan-level D-truths are verified against current codebase state (not just SUMMARY claims). The two review warnings (WR-01, WR-02) found during code review were fixed in a dedicated post-review commit with regression test coverage, confirmed present in the current file state by direct code reading. All offline gates (build, vet, gofmt, targeted greps, full test suite) pass when re-run independently by this verifier. The live E2E evidence trail (13-03-SUMMARY.md) plus recorded human approval satisfies the "live e2e verified" bar for all three success criteria without requiring a fresh live run.

---

_Verified: 2026-07-10_
_Verifier: Claude (gsd-verifier)_
