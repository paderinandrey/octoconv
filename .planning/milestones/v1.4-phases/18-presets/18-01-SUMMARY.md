---
phase: 18-presets
plan: 01
subsystem: database
tags: [postgres, pgx, presets, jobs, provenance]

# Dependency graph
requires:
  - phase: 18-presets (context)
    provides: D-02..D-05, D-08, D-09 locked decisions; presets table + jobs.preset_name/preset_version columns already in 0001_init.sql (dormant)
provides:
  - internal/presets package (Preset, Repo, ErrNotFound) mirroring internal/clients shape
  - Repo.Resolve implementing scope-precedence shadowing (D-02) entirely in SQL, with SQL-side no-leak filtering (D-03)
  - Repo.Create/Update/Deactivate/List/Get implementing bump-on-update versioning (D-04) with no hard delete
  - jobs.CreateParams.PresetName/PresetVersion wired to existing jobs.preset_name/preset_version columns (D-08)
affects: [18-02 (CLI cmd/manage-presets), 18-03 (API preset resolution in handleCreateJob), 18-04 (live gate)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Scope-precedence resolution entirely in SQL WHERE + ORDER BY (no post-lookup Go ownership branch) to prevent existence-leak across trust boundaries"
    - "Bump-on-update versioning: immutable rows, Update = lock current active row FOR UPDATE + deactivate + insert new version, all in one pgx.BeginFunc transaction"

key-files:
  created:
    - internal/presets/presets.go
    - internal/presets/repo.go
    - internal/presets/repo_test.go
  modified:
    - internal/jobs/repo.go
    - internal/jobs/repo_test.go

key-decisions:
  - "Resolve is a single non-locking SELECT with WHERE (scope='system' AND client_id IS NULL) OR (scope='user' AND client_id=$1), is_active, operation='convert', ORDER BY (scope='user') DESC, version DESC LIMIT 1 — shadowing and no-leak semantics both live in SQL, not Go"
  - "Create/Update/Deactivate rely on the DDL presets_scope_owner_chk CHECK constraint for the scope/client_id invariant rather than re-deriving it in Go"
  - "Accepted residual risk: the at-most-one-active-version invariant (D-04) is enforced only application-transactionally by Repo.Update, with no DB backstop (no partial unique index on is_active) — see note below"

patterns-established:
  - "Nullable client_id equality filter pattern: (client_id = $N OR ($N::uuid IS NULL AND client_id IS NULL)) used consistently across Create/Update/Deactivate/List/Get for scope+client isolation"

requirements-completed: [PRST-02, PRST-03, PRST-04]

# Metrics
duration: 15min
completed: 2026-07-12
---

# Phase 18 Plan 01: Presets persistence foundation Summary

**New `internal/presets` package with SQL-only scope-precedence preset resolution (shadowing + no-leak), bump-on-update versioning, and jobs.preset_name/preset_version provenance wiring — all backed by 11 DB-gated tests against live Postgres.**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-07-12T21:01Z (approx, from prior commit)
- **Completed:** 2026-07-12T21:07Z
- **Tasks:** 3/3 completed
- **Files modified:** 5 (3 created, 2 modified)

## Accomplishments
- `internal/presets` package mirroring `internal/clients`'s `Repo{pool}`/`NewRepo`/`ErrNotFound` shape, with `Preset` struct, `Scope*`/`OperationConvert` string consts, and doc comments tying constants to the DB CHECK constraints
- `Resolve` implements D-02 (client-scoped preset shadows same-name system preset) and D-03 (nonexistent/inactive/cross-client all return the identical `ErrNotFound`, no distinguishable Go branch) as a single parameterized SQL statement
- `Create`/`Update`/`Deactivate`/`List`/`Get` implement the full D-04 bump-on-update lifecycle (immutable versions, exactly one active row per (scope, client_id, name), no hard delete)
- `jobs.CreateParams` gained `PresetName`/`PresetVersion`; `Repo.Create`'s INSERT now writes `preset_name`/`preset_version`, staying NULL for non-preset jobs
- 9 DB-gated test functions in `internal/presets/repo_test.go` + 2 new test functions in `internal/jobs/repo_test.go` (11 total), all passing against live Postgres

## Task Commits

Each task was committed atomically:

1. **Task 1: Create internal/presets package (types + repo)** - `f13dd5a` (feat)
2. **Task 2: DB-gated unit tests for resolution, shadowing, lifecycle, List/Get** - `269b4fe` (test)
3. **Task 3: Wire preset provenance into jobs.CreateParams and Repo.Create** - `cf0f5de` (feat)

## Files Created/Modified
- `internal/presets/presets.go` - Preset struct, Scope/Operation consts, ErrNotFound
- `internal/presets/repo.go` - Repo{pool}, NewRepo, Resolve/Create/Update/Deactivate/List/Get
- `internal/presets/repo_test.go` - 9 DB-gated test functions covering shadowing (both directions), no-leak (3 cases), version determinism, deactivate-not-delete, List active-vs-all + isolation, Get found/misses
- `internal/jobs/repo.go` - CreateParams.PresetName/PresetVersion; Create's INSERT extended with preset_name/preset_version (nullable, NULL for non-preset jobs)
- `internal/jobs/repo_test.go` - TestPresetProvenanceRoundTrip, TestPresetProvenanceNullForNonPreset

## Decisions Made
- Followed the plan's exact SQL shape for `Resolve` (single query, all filtering in WHERE) rather than any post-lookup Go-side ownership check, per D-02/D-03/T-18-01.
- Used a `(client_id = $N OR ($N::uuid IS NULL AND client_id IS NULL))` predicate (with an explicit `::uuid` cast on the NULL check) for every nullable-clientID-scoped query, so Postgres never has to guess the parameter type for a bare `NULL` comparison.
- `Create`'s pre-insert existence check queries `is_active` non-locking (matching the plan's "non-locking" instruction) — the DDL CHECK + UNIQUE partial indexes remain the real backstop against a genuinely concurrent double-create race for a *new* name; that race is out of scope for this plan (Update's bump-on-update *is* locked via `FOR UPDATE`, since that path competes over an existing row).

## Deviations from Plan

None - plan executed exactly as written. All acceptance criteria (SQL grep patterns, ErrNotFound presence, no hard DELETE, PresetName/PresetVersion field count, provenance round-trip test) verified directly.

## Accepted residual risk (D-04 single-active-version invariant)

The "at most one active version per (scope, client_id, name)" invariant is enforced **only application-transactionally**: `Repo.Update`'s bump-on-update runs deactivate-old + insert-new inside one `pgx.BeginFunc` transaction, using `SELECT ... FOR UPDATE` to serialize concurrent `Update` calls against the same active row. There is **no DB backstop** — no partial unique index on `is_active` (e.g. `UNIQUE (scope, client_id, name) WHERE is_active`) — because this phase ships zero migrations (the presets table DDL is dormant/pre-existing from `0001_init.sql`).

Consequently, a raw-SQL writer, an operational `UPDATE presets SET is_active = true ...` run outside `Repo`, or a future code path that bypasses `Repo.Update`/`Repo.Deactivate` could create two simultaneously-active versions of the same preset. The invariant holds for **all writes that go through `Repo`**, which is the only write path this phase (and the downstream CLI in 18-02) uses. This is a **deliberately accepted residual risk**, not an oversight — consistent with the project's convention of recording accepted residual risks (cf. PROJECT.md's file:// residual-read note).

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `internal/presets.Repo` exposes the full five-verb surface (Resolve/Create/Update/Deactivate/List/Get) needed by 18-02's `cmd/manage-presets` CLI (List/Get signatures kept stable per SEED-003 note) and by 18-03's API integration (Resolve is directly usable in `handleCreateJob`).
- `jobs.CreateParams.PresetName/PresetVersion` is ready for 18-03 to populate once a preset is resolved; no further jobs-side schema or repo work needed.
- No blockers for 18-02/18-03. The accepted residual risk above (no DB-level uniqueness backstop for `is_active`) should be kept in mind if 18-02/18-03 ever add a write path that does not go through `Repo`.

---
*Phase: 18-presets*
*Completed: 2026-07-12*
