---
phase: 14-validated-conversion-options-pdf-a-export
plan: 01
subsystem: database
tags: [postgres, jsonb, pgx, jobs-repository]

# Dependency graph
requires: []
provides:
  - "Job.Opts and CreateParams.Opts fields (map[string]any)"
  - "Live round-trip of the jobs.options jsonb column through Create/Get"
affects: [14-02, 14-03]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Opts jsonb round-trip follows the same nil-defaults-to-{} / plain-[]byte-scan idiom already used for job_events.detail, extended to a NOT NULL column"

key-files:
  created: []
  modified:
    - internal/jobs/jobs.go
    - internal/jobs/repo.go
    - internal/jobs/repo_test.go

key-decisions:
  - "Opts placed immediately after CallbackURL in both structs (before ErrorCode in Job, before Input in CreateParams), matching the plan's field-ordering instruction"
  - "nil CreateParams.Opts marshals to the literal two-byte {} rather than SQL NULL, since jobs.options is NOT NULL DEFAULT '{}'::jsonb"

patterns-established:
  - "NOT NULL jsonb columns scan into a plain []byte local, never a *string pointer — distinct from the nullable-column deref() idiom used for source_format/target_format/callback_url/etc."

requirements-completed: [OPTS-01]

# Metrics
duration: 5min
completed: 2026-07-11
---

# Phase 14 Plan 01: Options Column Persistence Summary

**Wired the already-existing, previously-inert `jobs.options jsonb` column into a live round-trip via `Job.Opts` / `CreateParams.Opts` (`map[string]any`), with no schema migration.**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-07-11T01:51:00+03:00 (approx, first commit 01:51:19)
- **Completed:** 2026-07-11T01:53:05+03:00
- **Tasks:** 3 completed
- **Files modified:** 3

## Accomplishments
- `Job` (internal/jobs/jobs.go) and `CreateParams` (internal/jobs/repo.go) both carry a documented `Opts map[string]any` field
- `Repo.Create` marshals `CreateParams.Opts` (defaulting nil to `{}`) and writes it as the 8th INSERT column
- `Repo.Get` scans the NOT NULL `options` column into a plain `[]byte` and unmarshals it into `Job.Opts`
- Round-trip test added and verified live against the local Postgres instance (both `TestOptsRoundTrip` and `TestOptsRoundTripNilDefault` PASS, not just self-skip)

## Task Commits

Each task was committed atomically:

1. **Task 1: Add Opts field to Job and CreateParams** - `b51044e` (feat)
2. **Task 2: Persist options in Create and read it in Get** - `79a3f80` (feat)
3. **Task 3: Round-trip test for the options column** - `66680f2` (test)

**Plan metadata:** committed separately by this executor after this SUMMARY (see final commit)

_Note: Task 3 is marked `tdd="true"` in the plan, but since the persistence implementation was already delivered in Task 2 (a prior, separately-committed task in this same plan), the RED/GREEN split described in the generic TDD execution flow does not apply cleanly here — the plan sequences add-fields → implement → test-the-implementation as three atomic tasks rather than a single RED→GREEN cycle. The test was written once, compiles offline (self-skips without `DATABASE_URL`), and was additionally run live against the local Postgres instance where both cases PASS._

## Files Created/Modified
- `internal/jobs/jobs.go` - Added `Opts map[string]any` field to `Job`
- `internal/jobs/repo.go` - Added `Opts map[string]any` field to `CreateParams`; `Create` marshals and writes the `options` column; `Get` scans and unmarshals it into `Job.Opts`
- `internal/jobs/repo_test.go` - Added `TestOptsRoundTrip` (non-empty opts survive Create→Get) and `TestOptsRoundTripNilDefault` (nil Opts reads back empty)

## Decisions Made
- Followed the plan's exact field placement (Opts immediately after CallbackURL in both structs) and marshal/scan idioms (nil→`{}` on write, plain `[]byte` on read for the NOT NULL column) — no deviation from the interfaces the plan specified.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Plans 02 (API validation of the opts form field) and 03 (worker threading opts into the converter/PDF-A export) can now depend on `Job.Opts` and `CreateParams.Opts` existing and persisting correctly.
- No blockers. `go build ./...` and `go vet ./...` are clean; no new Go dependency introduced (go.mod/go.sum unchanged).

---
*Phase: 14-validated-conversion-options-pdf-a-export*
*Completed: 2026-07-11*

## Self-Check: PASSED

All created/modified files found on disk; all 4 task/summary commits (b51044e, 79a3f80, 66680f2, 2ee56c2) verified present in git log.
