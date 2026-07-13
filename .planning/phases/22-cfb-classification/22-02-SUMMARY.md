---
phase: 22-cfb-classification
plan: 02
subsystem: api
tags: [cfb, ole-cfb, content-validation, e2e, office-formats]

# Dependency graph
requires:
  - phase: 22-cfb-classification
    provides: "convert.ClassifyCFB (CFBEncrypted/CFBLegacy/CFBUnknown) from Plan 22-01"
provides:
  - "handleCreateJob three-way OLE-CFB rejection split (distinct password-protected vs legacy-binary 422s, unchanged fail-closed CFBUnknown default)"
  - "handler tests over real fixtures proving distinct messages, no upload, no job created"
  - "live-proven TestOLECFBRejectionE2E asserting distinct messages against the running compose stack"
affects: [api, e2e]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Classify-then-switch content-rejection branch: cheap magic-byte pre-filter (IsOLECFB) gates a deeper classifier (ClassifyCFB) whose result selects among N distinct 422s, with an explicit fail-closed default arm reusing the pre-existing combined message"

key-files:
  created:
    - internal/api/testdata/legacy.doc
    - internal/api/testdata/encrypted.docx
  modified:
    - internal/api/handlers.go
    - internal/api/handlers_test.go
    - internal/e2e/e2e_test.go

key-decisions:
  - "CFBUnknown keeps the EXACT original combined 422 string/log reason unchanged (byte-identical) rather than introducing a new message, per fail-closed-compat (Pitfall 11)"
  - "No error_code field added to any of the three 422s -- message-only distinction, consistent with every other rejection in handleCreateJob (research anti-feature avoided)"
  - "TestCreateJob_OLECFBRejected (synthetic non-decodable CFB bytes) kept as-is and re-documented as the CFBUnknown fail-closed-compat proof rather than replaced"

requirements-completed: [CFB-01]

# Metrics
duration: 8min
completed: 2026-07-13
---

# Phase 22 Plan 02: handleCreateJob CFB Three-Way Split + Live E2E Gate Summary

**handleCreateJob now returns a distinct "remove the password" 422 for encrypted OOXML and a distinct "legacy binary ... convert to docx/xlsx/pptx" 422 for legacy .doc/.xls/.ppt, via `convert.ClassifyCFB`, proven live end-to-end against the real fixtures through the rebuilt compose stack.**

## Performance

- **Duration:** 8 min (12:37 - 12:45 local, commits 12:40:50 and 12:44:21 CEST)
- **Tasks:** 2 completed
- **Files modified:** 5 (2 created fixtures, 3 modified)

## Accomplishments
- Split the single `IsOLECFB` rejection branch in `internal/api/handlers.go` into a three-way `switch convert.ClassifyCFB(file, header.Size)`: `CFBEncrypted` -> "password-protected Office file is not supported; remove the password and re-upload" (`reason=encrypted_document`); `CFBLegacy` -> "legacy binary Office format (.doc/.xls/.ppt) is not supported; convert to docx/xlsx/pptx" (`reason=legacy_document`); default (`CFBUnknown`) -> the original combined message, byte-identical (`reason=legacy_or_encrypted_document`)
- Added `TestCreateJob_CFB` (table-driven, real `internal/api/testdata/encrypted.docx` + `legacy.doc` fixtures copied from `internal/e2e/testdata/`) asserting per-case distinct substrings, cross-checking legacy's body does NOT contain "password" (proving actual distinctness, not just two non-empty strings), and asserting no storage upload / no job creation for both
- Re-documented (kept green, unmodified behavior) `TestCreateJob_OLECFBRejected` as the CFBUnknown fail-closed-compat case using synthetic CFB-magic bytes with no decodable directory entries
- Updated `TestOLECFBRejectionE2E` to a per-fixture table asserting DISTINCT substrings (`legacy.doc` -> "legacy binary" + must NOT contain "password"; `encrypted.docx` -> "remove the password") instead of the old shared loose "password" substring check
- Ran the D-07 unconditional live hard gate: rebuilt only the `api` image, brought up the full compose stack, and ran `TestOLECFBRejectionE2E` live -- both subtests PASS with the exact contract messages; verified the exact JSON bodies via a direct authenticated `curl` against the running API (see below); left the stack running

## Task Commits

Each task was committed atomically:

1. **Task 1: Split the handleCreateJob OLE-CFB branch on ClassifyCFB + handler tests** - `7790de6` (feat)
2. **Task 2: Update TestOLECFBRejectionE2E to assert distinct messages + run the live hard gate** - `3381f16` (test)

_No plan-metadata commit yet -- per objective, STATE.md/ROADMAP.md are NOT updated by this executor; this SUMMARY.md is committed separately by the orchestrator/next step._

## Files Created/Modified
- `internal/api/handlers.go` - `handleCreateJob`'s `IsOLECFB` branch now calls `convert.ClassifyCFB` and switches on `CFBEncrypted`/`CFBLegacy`/default(`CFBUnknown`), each with its own `log.Printf(...reason=...)` + `writeError` 422, before any storage/job-create
- `internal/api/handlers_test.go` - new `TestCreateJob_CFB` table test (real fixtures); `TestCreateJob_OLECFBRejected` doc-comment updated to describe it as the CFBUnknown case; added `"os"` import
- `internal/api/testdata/legacy.doc`, `internal/api/testdata/encrypted.docx` - copied from `internal/e2e/testdata/` for the handler-level tests
- `internal/e2e/e2e_test.go` - `oleCFBFixtures` changed from `[]string` to a struct slice carrying per-fixture `wantSubstr`/`wantAbsent`; `TestOLECFBRejectionE2E` asserts the distinct pair and the absence check

## Live E2E Gate: Exact Observed 422 Bodies

Compose invocation used (mirrors Phase 20/21 precedent, api rebuilt since only it embeds the new classify code):
```
docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml up -d --build api
docker compose -p octoconv -f docker-compose.yml -f docker-compose.e2e.yml up -d
```
Healthz poll: `GET http://localhost:8090/healthz` returned `{"postgres":"ok","redis":"ok","s3":"ok","status":"ok"}` on the first attempt.

Test run:
```
E2E_BASE_URL=http://localhost:8090 \
DATABASE_URL=postgres://octo:octo-pass@localhost:5434/octo_db \
API_KEY_SALT=dev-only-change-me-in-real-deploys \
go test ./internal/e2e/ -run 'TestOLECFBRejectionE2E' -v -count=1 -timeout 5m
```
Result: `--- PASS: TestOLECFBRejectionE2E (0.04s)` with both `legacy.doc` and `encrypted.docx` subtests passing.

Exact bodies captured via a direct authenticated `curl` against the same running API (real fixtures, `target=pdf`):
- `legacy.doc` (as `in.doc`): `{"error":"legacy binary Office format (.doc/.xls/.ppt) is not supported; convert to docx/xlsx/pptx"}`
- `encrypted.docx` (as `in.docx`): `{"error":"password-protected Office file is not supported; remove the password and re-upload"}`

Both byte-identical to the `<message_contract>` in 22-02-PLAN.md. Stack left running (Phase 18/20/21 precedent) with all containers healthy/Up at time of writing.

## Decisions Made
- CFBUnknown's message and log reason are left byte-identical to the pre-existing combined string -- verified by keeping `TestCreateJob_OLECFBRejected`'s assertions unchanged and green.
- No `error_code` taxonomy added to any CFB 422 -- pure message-text distinction, matching every other 422 in `handleCreateJob` (research anti-feature avoided per the message_contract note).
- `TestCreateJob_CFB` uses the real fixture bytes rather than additional hand-crafted CFB byte slices, since Plan 22-01 already fuzzed the parser directly (`FuzzClassifyCFB`) -- the handler-level test's job is to prove HTTP wiring, not re-prove parser correctness.

## Deviations from Plan

None - plan executed exactly as written. `ClassifyCFB` (from 22-01) was consumed as designed; no new dependencies, no architectural changes, no auto-fixes were required.

## Issues Encountered

None. The offline `go test ./...` and `go vet ./...` were clean on the first pass; the live gate passed on the first run without needing a retry.

## User Setup Required

None - no external service configuration required. The live gate used only the existing compose stack and dev-only credentials already documented in `.env.example`.

## Next Phase Readiness

- CFB-01 (the phase's sole requirement) is now fully delivered end-to-end: parser (22-01) + API integration + live proof (22-02).
- No known stubs or deferred items. The compose stack is left running for any follow-on inspection/verification in this phase.
- Threat register items T-22-05/T-22-06/T-22-07/T-22-SC from 22-02-PLAN.md are all addressed by this plan's changes; no new threat surface was introduced (no new endpoints, no new auth paths, no schema changes).

---
*Phase: 22-cfb-classification*
*Completed: 2026-07-13*

## Self-Check: PASSED

All claimed files exist on disk (`internal/api/handlers.go`, `internal/api/handlers_test.go`, `internal/api/testdata/legacy.doc`, `internal/api/testdata/encrypted.docx`, `internal/e2e/e2e_test.go`) and both task commit hashes (`7790de6`, `3381f16`) resolve in `git log --oneline --all`.
