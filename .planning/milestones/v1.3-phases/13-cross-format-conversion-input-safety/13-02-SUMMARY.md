---
phase: 13-cross-format-conversion-input-safety
plan: 02
subsystem: api
tags: [go, ole-cfb, input-validation, sniff-chain, security]

# Dependency graph
requires:
  - phase: 11-cross-format-conversion-input-safety (or earlier document-format work)
    provides: convert.SniffContainer / docsniff.go ZIP-based office format detection, convert.NormalizeFormat, convert.MIMEType covering all 6 office formats
provides:
  - "convert.IsOLECFB: standalone 8-byte OLE-CFB magic detector, deliberately absent from the registry sniff tables"
  - "Third fail-closed detection branch in handleCreateJob rejecting legacy binary / password-protected Office uploads with a 422 before any S3/Postgres write"
affects: [13-03 (live e2e CFB fixture verification), future v2 DOCV3-02 (legacy vs encrypted distinction)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Fail-closed inline-reject branch (not registry sniff entry) for a detected-but-never-supported format, mirroring the existing macro/zip-bomb 422 branches"

key-files:
  created:
    - internal/convert/olecfb.go
    - internal/convert/olecfb_test.go
  modified:
    - internal/api/handlers.go
    - internal/api/handlers_test.go

key-decisions:
  - "IsOLECFB lives in its own file, NOT wired into sniff.go's signatures table or docsniff.go's ooxmlRootParts/odfMimetypes (D-05) — those tables mean 'detected AND supported'; CFB means 'detected AND always rejected', which would break EngineFor's fail-closed assumption if mixed in."
  - "D-06 422 message text: \"legacy binary or password-protected Office format is not supported; convert to docx/xlsx/pptx or remove the password\" — names both sub-cases and a remedy, matches existing English 422 style. 13-03's live CFB test should assert against this exact string or a substring of it (e.g. contains \"password\")."
  - "Distinct log reason=legacy_or_encrypted_document (vs reason=unrecognized_content for the generic case) for diagnosability, per plan requirement."
  - "CFB branch placed after the ZIP/SniffContainer branch and before the generic unrecognized-content 422, and before s.storage.Upload/s.repo.Create — same ordering discipline as every other content-validation branch."

patterns-established:
  - "New 'detected but always rejected' formats get a standalone single-purpose detector function called directly from handleCreateJob, not a registry table entry — keeps convert.Default.EngineFor's invariant (every detected value maps to a real registered Converter) intact."

requirements-completed: [SAFE-01]

# Metrics
duration: 12min
completed: 2026-07-10
---

# Phase 13 Plan 02: OLE-CFB Fail-Closed Rejection Summary

**Uploads beginning with the OLE-CFB magic byte header (legacy binary .doc/.xls/.ppt or password-protected OOXML) now get an immediate 422 before touching S3/Postgres, via a standalone `convert.IsOLECFB` detector wired as a third fail-closed branch in `handleCreateJob`.**

## Performance

- **Duration:** ~12 min
- **Started:** 2026-07-10T11:05Z (approx)
- **Completed:** 2026-07-10T11:17:35Z
- **Tasks:** 2
- **Files modified:** 4 (2 created, 2 modified)

## Accomplishments
- Added `convert.IsOLECFB`, a single-purpose exported detector for the 8-byte `D0 CF 11 E0 A1 B1 1A E1` OLE-CFB signature, with unit coverage of the true/false/short/empty cases, deliberately kept out of the registry sniff tables (D-05).
- Wired a third fail-closed detection branch into `handleCreateJob`, positioned after the ZIP/SniffContainer branch and before the generic unrecognized-content 422, rejecting with a distinct log reason and a D-06-compliant 422 message — before any `s.storage.Upload`/`s.repo.Create` call.
- Added `TestCreateJob_OLECFBRejected` proving 422 status, no S3 upload, no job creation, and a remedy-bearing response body.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add the convert.IsOLECFB detector + unit tests (D-05)** - `1b6dbe3` (feat)
2. **Task 2: Wire the fail-closed CFB rejection branch into handleCreateJob (D-05, D-06)** - `d00618f` (feat)

**Plan metadata:** (this commit, added after SUMMARY.md)

## Files Created/Modified
- `internal/convert/olecfb.go` - `oleCFBMagic` signature var + `IsOLECFB(io.ReaderAt) bool` detector; doc comment explains the shared-header ambiguity and the v2 deferral.
- `internal/convert/olecfb_test.go` - table-driven test: exact magic (true), magic+trailing (true), ZIP prefix (false), 7-of-8 bytes (false), empty (false).
- `internal/api/handlers.go` - new branch `if detected == "" && convert.IsOLECFB(file) { ... }` between the ZIP branch and the generic 422, logging `reason=legacy_or_encrypted_document` and returning the D-06 message.
- `internal/api/handlers_test.go` - `TestCreateJob_OLECFBRejected`: posts a `.doc`-named file whose bytes start with the CFB magic, asserts 422 + `store.uploaded == false` + `repo.created == nil` + body contains "password".

## Decisions Made
- Exact D-06 message text: `"legacy binary or password-protected Office format is not supported; convert to docx/xlsx/pptx or remove the password"`. 13-03's live-fixture CFB test should match on this string (or a stable substring like `"password"`).
- Kept `IsOLECFB` as a plain function call from `internal/api`, not a `Converter`/registry entry, consistent with D-05's explicit non-pattern instruction — this preserves `EngineFor`'s fail-closed assumption that every `detected` value came from a real registered engine.
- Reused the already-in-scope `file` (`multipart.File`, an `io.ReaderAt`) for the `ReadAt(...,0)` magic check, same pattern as the existing ZIP-prefix peek — no new file handle, no disturbance of the `rest` stream used later for `s.storage.Upload`.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- SAFE-01 offline verification (handler test) is complete. Plan 13-03 must add live e2e verification against REAL legacy `.doc` and password-protected `.docx` fixtures (per the plan's `<verification>` note and D-07), asserting both sub-cases hit this same 422 branch and match the chosen message text.
- No blockers. `internal/convert/olecfb.go` and the `handleCreateJob` branch are ready to be exercised by 13-03's live fixtures without further code changes.

## Self-Check: PASSED

- FOUND: internal/convert/olecfb.go
- FOUND: internal/convert/olecfb_test.go
- FOUND: commit 1b6dbe3
- FOUND: commit d00618f

---
*Phase: 13-cross-format-conversion-input-safety*
*Completed: 2026-07-10*
