---
phase: 26-operator-presets-rest
plan: 02
subsystem: database
tags: [presets, repo, postgres, versioning, gap-closure, tdd]

# Dependency graph
requires:
  - phase: 26-operator-presets-rest (plan 01)
    provides: operator-facing system-preset REST surface built on internal/presets.Repo
provides:
  - Race-safe, scope-generic Repo.Create that bumps version across active+inactive rows so a deactivated preset name is recreatable
  - 23505 (unique_violation) -> ErrAlreadyExists backstop mapping at the repo layer
  - Regression test proving create->deactivate->create works for both system and user scope
affects: [26-operator-presets-rest, future presets work]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "INSERT ... SELECT COALESCE(MAX(...),0)+1 for race-tolerant next-version computation, mirroring Update's bump-on-update pattern"
    - "pgconn.PgError code backstop (23505 -> sentinel error) as a second line of defense behind an application-level pre-check"

key-files:
  created: []
  modified:
    - internal/presets/repo.go
    - internal/presets/repo_test.go

key-decisions:
  - "Kept the existing active-duplicate EXISTS pre-check (unique index alone can't enforce it, since v2-active + v3-new don't collide) and added the version-bump INSERT ... SELECT as a second, independent fix for the inactive-row case"
  - "Used an aggregate SELECT (COALESCE(MAX(p.version),0)+1) rather than a locking transaction (unlike Update) — plan explicitly accepted this as a backstopped race (T-26-03): the partial unique index is the real arbiter, and a lost race now maps to ErrAlreadyExists/409 instead of crashing with 500"

patterns-established:
  - "Any future write path with a version-per-name axis should re-use the two-part fix: (1) EXISTS pre-check for the user-facing 409 message, (2) COALESCE(MAX)+1 INSERT for correctness, (3) 23505 backstop for the race window between them"

requirements-completed: [OPER-01]

# Metrics
duration: 25min
completed: 2026-07-14
---

# Phase 26 Plan 02: Gap Closure — CR-01 Preset Recreate 500 Summary

**Fixed `presets.Repo.Create` to compute the next version via `COALESCE(MAX(version),0)+1` across active AND inactive rows, closing the "deactivate a preset then it's permanently unusable" 500 bug for both system and user scope, with a `pgconn` 23505 backstop mapping any residual race to `ErrAlreadyExists` (409).**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-14T11:00:00Z (approx, per worktree init)
- **Completed:** 2026-07-14T11:20:46Z
- **Tasks:** 2 (TDD: RED + GREEN)
- **Files modified:** 2

## Accomplishments
- Added `TestCreateAfterDeactivateBumpsVersion` (system + user scope subtests) reproducing the CR-01 500 as a RED failing test against the pre-fix `repo.go`
- Reworked `Create` to insert at `COALESCE(MAX(p.version), 0) + 1` scoped to `(scope, client_id, name)` across all rows (mirrors `Update`'s bump-on-update semantics), so fresh names still start at version 1 and revived names get the next version instead of colliding
- Added a `pgconn.PgError` `23505` (unique_violation) backstop mapping any lost create race to `ErrAlreadyExists`, so the REST layer (which already does `errors.Is(err, ErrAlreadyExists) -> 409`) never surfaces a raw 500 for this path
- Corrected stale `CreateParams`/`Create` doc comments that claimed "always version 1"
- Full `./internal/presets` DB-backed suite green (14 test functions, including the new one); `go build ./...`, `go vet ./...`, `gofmt -l .` all clean repo-wide

## Task Commits

Each task was committed atomically:

1. **Task 1: Regression test — create -> deactivate -> create must bump version (RED)** - `2f23da5` (test)
2. **Task 2: Fix repo.Create — version bump across all rows + 23505 -> ErrAlreadyExists (GREEN)** - `1a6728a` (fix)

**Plan metadata:** (this commit, added after SUMMARY.md)

## Files Created/Modified
- `internal/presets/repo_test.go` - Added `TestCreateAfterDeactivateBumpsVersion` (system-scope and user-scope subtests) asserting bumped version, correct `Get` result, and exact active/total row counts (1 active, 2 total, no hard delete)
- `internal/presets/repo.go` - `Create` now imports `github.com/jackc/pgx/v5/pgconn`; replaced `const version = 1` + fixed-value `INSERT` with `INSERT ... SELECT COALESCE(MAX(p.version), 0) + 1 ... RETURNING id, version`; added a `23505 -> ErrAlreadyExists` error-mapping backstop; corrected doc comments on `CreateParams` and `Create`

## Decisions Made
- Kept the pre-existing active-duplicate `EXISTS` pre-check intact (still returns the friendly `active preset %q already exists ...: %w ErrAlreadyExists` message) rather than relying solely on the unique index, since the unique index cannot detect an active-vs-active collision when versions differ (e.g., active v2 vs. attempted new v3) — only the version-computation fix handles the inactive-row revival case.
- Followed the plan's explicit disposition (T-26-03, "accept, backstopped") to leave the version pre-computation non-transactional/non-locking rather than wrapping `Create` in a `pgx.BeginFunc` + row lock like `Update`. This matches the plan's stated rationale: over-engineering for an internal operator surface, and the partial unique index is the real correctness arbiter regardless of what Go computes.

## Deviations from Plan

None - plan executed exactly as written. One transient authoring mistake (an unused `$2` placeholder from an intermediate edit of the `INSERT ... SELECT`) was caught and corrected before verification/commit via `go build`/`go test`, not left as a runtime bug — not counted as a plan deviation since it never reached a committed state.

## Issues Encountered
None - both tasks executed cleanly against the live Postgres at `localhost:5434` (env sourced read-only from the main checkout's `.env` per executor instructions).

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- CR-01 from `26-REVIEW.md`/`26-VERIFICATION.md` is closed: the deactivate-then-recreate workflow now works over REST for both system and user scope, satisfying OPER-01's "manageable via REST" clause for that lifecycle.
- No migration, `PresetAdmin` interface, or API-layer changes were needed or made — the fix is entirely contained in `internal/presets/repo.go` as scoped.
- Remaining non-blocking items from `26-REVIEW.md` (WR-01/WR-02/WR-03/IN-01/IN-02) are unaddressed by design (explicitly out of scope for this gap-closure plan) and remain tracked separately.

---
*Phase: 26-operator-presets-rest*
*Completed: 2026-07-14*
