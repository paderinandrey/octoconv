---
phase: 11-api-routing-end-to-end-document-conversion
plan: 04
subsystem: api
tags: [content-type, mime, go, document-conversion, libreoffice]

# Dependency graph
requires:
  - phase: 11-api-routing-end-to-end-document-conversion
    provides: engine-aware routing, document upload/download path, live E2E suite (11-01/02/03)
provides:
  - Content-Type parity between image and document jobs for both stored uploads and worker-produced outputs
  - Regression coverage (unit + handler-level) preventing this MIME-table gap from silently regressing
affects: [11-api-routing-end-to-end-document-conversion, future-engine-classes]

# Tech tracking
tech-stack:
  added: []
  patterns: []

key-files:
  created: []
  modified:
    - internal/convert/sniff.go
    - internal/convert/sniff_test.go
    - internal/api/handlers_test.go

key-decisions:
  - "Fixed convert.MIMEType() directly rather than adding a parallel document-specific MIME table, since internal/api/handlers.go and internal/worker/worker.go both already call the shared MIMEType() function -- extending its switch statement fixes the input-upload path and the worker's PDF-output path with a single change, exactly as specified in 11-REVIEW.md WR-01."
  - "Scoped this gap-closure plan strictly to the Content-Type parity gap flagged in 11-VERIFICATION.md's gaps section (the only item scored gaps_found); the code review's other warnings (WR-02 E2E extra_hosts, WR-03 engine-name literal duplication, WR-04 E2E client timeouts) are pre-existing, out-of-scope issues from a prior phase, not part of this plan's mandate, and were left untouched per the scope-boundary rule."

requirements-completed: [DOC-10]

# Metrics
duration: 12min
completed: 2026-07-09
---

# Phase 11 Plan 04: Content-Type Parity Gap Closure Summary

**Extended `convert.MIMEType` with the six document formats and `pdf`, closing the last gap in DOC-10 so document job uploads/downloads are served with the correct MIME type instead of `application/octet-stream`, exactly like image jobs.**

## Performance

- **Duration:** ~12 min
- **Completed:** 2026-07-09
- **Tasks:** 2 (source fix + regression test, handler test coverage)
- **Files modified:** 3

## Accomplishments
- `convert.MIMEType()` now returns the correct canonical MIME type for `pdf` (`application/pdf`) and all six document source formats (`docx`, `xlsx`, `pptx`, `odt`, `ods`, `odp`), fixing both the stored input Content-Type (`internal/api/handlers.go:231`) and the worker's PDF output Content-Type (`internal/worker/worker.go:409,420`) via the single shared function both already call.
- Added the missing regression coverage the verification report called out as the reason this gap survived: `TestMIMEType` in `internal/convert/sniff_test.go` now asserts all seven new format→MIME mappings, and `TestCreateJob_DocumentDetectedAndAccepted`/`TestCreateJob_ODFDetectedAndAccepted` in `internal/api/handlers_test.go` now assert `store.contentType` for docx and odt uploads, mirroring the pre-existing PNG assertion pattern.
- Full test suite (`go build ./... && go vet ./... && go test ./... -count=1`) is green across all 14 packages, including the newly-asserted cases.

## Task Commits

There was no formal PLAN.md for this gap-closure round (the phase's own verification report — `11-VERIFICATION.md` — served as the spec, per its own recommendation: "a small gap-closure plan extending `convert.MIMEType` per the fix already specified in 11-REVIEW.md WR-01"). Work was committed in two atomic commits:

1. **Fix: extend `convert.MIMEType` with document formats and pdf** - `2b34b50` (fix)
2. **Test: assert document Content-Type parity in handler tests** - `19e1c45` (test)

## Files Created/Modified
- `internal/convert/sniff.go` - `MIMEType()` switch extended with `pdf` and the six document formats (canonical OOXML/ODF MIME types), matching the exact fix specified in `11-REVIEW.md` WR-01
- `internal/convert/sniff_test.go` - `TestMIMEType` extended with the seven new format→MIME cases
- `internal/api/handlers_test.go` - `TestCreateJob_DocumentDetectedAndAccepted` and `TestCreateJob_ODFDetectedAndAccepted` now assert `store.contentType` for the docx and odt fixtures respectively

## Decisions Made
- Fixed the shared `convert.MIMEType()` function rather than introducing a separate document-only MIME table, since both call sites (`internal/api/handlers.go` for uploads, `internal/worker/worker.go` for worker-produced PDF outputs) already route through this single function — one change closes both halves of the gap (input Content-Type and output Content-Type) simultaneously, matching the single-source-of-truth intent already documented on the function.
- Did not add a full worker-level integration test exercising `HandleDocumentConvert` end-to-end for output Content-Type, because `internal/worker/worker_test.go` has no existing fake-based integration harness for that handler (its tests are scoped to `isTerminal` classification logic only) and `worker.go`'s output-upload call site is a direct, unconditional `convert.MIMEType(job.TargetFormat)` invocation with no additional logic — the new `TestMIMEType` case for `"pdf"` fully covers the behavior that call site depends on, without requiring new test infrastructure out of scope for a narrow gap-closure plan.
- Left the code review's other three warnings (WR-02 missing `extra_hosts` on the E2E `api` service, WR-03 duplicated engine-class string literals, WR-04 unbounded E2E HTTP client timeouts) untouched — none of them were flagged as a gap in `11-VERIFICATION.md` (the phase's verification report, which is what "gap closure" refers to here); they remain valid code-review debt for a future pass but are out of this plan's scope per the executor's scope-boundary rule.

## Deviations from Plan

None — there was no formal PLAN.md to deviate from. This plan's scope was derived directly from `11-VERIFICATION.md`'s single `gaps_found` item (Content-Type parity) and its explicit recommendation to extend `convert.MIMEType` per `11-REVIEW.md` WR-01's already-specified fix. The implementation matches that specification exactly (same case list, same MIME strings).

## Issues Encountered
None. `go build ./...`, `go vet ./...`, and `go test ./... -count=1` were all green on the first attempt after the change; `gofmt -l .` reports one pre-existing unrelated file (`internal/queue/queue_test.go`, last touched in an unrelated `10-01` commit) which was not modified by this plan and is out of scope.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

DOC-10's "identically to image jobs" clause is now fully satisfied: document uploads and their PDF outputs are stored/served with the correct canonical Content-Type, matching image job behavior exactly. The Content-Type gap flagged in `11-VERIFICATION.md` (status `gaps_found`) is closed; a re-verification pass would be expected to score this truth as fully `✓ VERIFIED` rather than `⚠️ PARTIAL`.

Remaining known debt (out of this plan's scope, tracked in `11-REVIEW.md`): WR-02 (E2E harness portability on Linux docker engines), WR-03 (engine-class string literal duplication), WR-04 (E2E HTTP client timeouts), and the informational note that `.planning/REQUIREMENTS.md` (not present in this worktree) may still show DOC-10 as unchecked and should be updated by whoever owns that file next.

---
*Phase: 11-api-routing-end-to-end-document-conversion*
*Completed: 2026-07-09*

## Self-Check: PASSED

All claimed files/commits verified present:
- FOUND: internal/convert/sniff.go
- FOUND: internal/convert/sniff_test.go
- FOUND: internal/api/handlers_test.go
- FOUND: commit 2b34b50 (fix)
- FOUND: commit 19e1c45 (test)
