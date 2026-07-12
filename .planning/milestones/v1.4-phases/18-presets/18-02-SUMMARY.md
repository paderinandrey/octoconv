---
phase: 18-presets
plan: 02
subsystem: presets
tags: [go, cli, flag, presets, opts-validation]

# Dependency graph
requires:
  - phase: 18-presets (plan 01)
    provides: internal/presets.Repo (Create/Update/Deactivate/List/Get/Resolve), Preset domain type, ScopeSystem/ScopeUser/OperationConvert consts, ErrNotFound
provides:
  - internal/presets.ValidateOptsJSON — write-time (D-11) opts allowlist check reusing convert.ParseDocOpts/ParseHTMLOpts
  - cmd/manage-presets operator CLI (create/update/list/show/deactivate, system+client scopes)
affects: [18-03 (API preset resolution — consumes internal/presets.Repo, ValidateOptsJSON is CLI-only)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Write-time convenience validation that accepts ANY of several closed allowlist parsers (ValidateOptsJSON tries ParseDocOpts then ParseHTMLOpts) when the engine isn't known yet at write time"
    - "Scope derived purely from CLI flag presence (--client-id), never re-derived from the DB; DDL CHECK constraint is the actual invariant enforcement"

key-files:
  created:
    - internal/presets/optscheck.go
    - internal/presets/optscheck_test.go
    - cmd/manage-presets/main.go
  modified: []

key-decisions:
  - "ValidateOptsJSON accepts opts if EITHER ParseDocOpts OR ParseHTMLOpts succeeds (engine unknown at write time); explicitly documented as D-11 fail-early convenience, not the D-06 trust boundary"
  - "cmd/manage-presets uses flag.NewFlagSet per subcommand (named flags) rather than manage-clients' positional args, justified by presets needing --name/--target/--client-id/--opts/--description"
  - "Scope selection: absence of --client-id => system scope (nil client id); presence => user scope with parsed uuid — no Go-side re-validation, DDL presets_scope_owner_chk is the enforcement point"
  - "No delete verb implemented; deactivate is the only lifecycle-ending verb (is_active=false), matching D-04"

requirements-completed: [PRST-01]

# Metrics
duration: 4min
completed: 2026-07-12
---

# Phase 18 Plan 02: manage-presets CLI + write-time opts validation Summary

**Operator CLI (`cmd/manage-presets`) for create/update/list/show/deactivate of system- and client-scoped presets, backed by a new `internal/presets.ValidateOptsJSON` helper that fail-early-rejects opts matching neither the document nor HTML print allowlist schema (D-11).**

## Performance

- **Duration:** ~4 min (test commit 21:09:36 → CLI commit 21:12:31 local time)
- **Started:** 2026-07-12T18:09:36Z
- **Completed:** 2026-07-12T18:12:31Z
- **Tasks:** 2
- **Files modified:** 3 (all new)

## Accomplishments
- `internal/presets.ValidateOptsJSON(map[string]any) error` — empty/nil opts always valid; accepts any opts payload that round-trips through `convert.ParseDocOpts` OR `convert.ParseHTMLOpts`; rejects opts matching neither schema (e.g. unknown keys, out-of-range `margin_mm`)
- `cmd/manage-presets/main.go` — full CLI mirroring `cmd/manage-clients` conventions (db.Connect fail-fast, `defer pool.Close()`, `fmt.Println` for user output, `log.Fatalf` for errors, `usage()` helper), with five verbs: `create`, `update`, `list`, `show`, `deactivate`
- Scope (system vs. user) derived solely from `--client-id` flag presence/absence across all five verbs
- `create`/`update` run `presets.ValidateOptsJSON` before touching the DB (D-11); `presets.ErrNotFound` mapped to a clear "no such preset" message on `update`/`show`/`deactivate`
- No delete verb exists anywhere in the CLI (grep-verified)

## Task Commits

Each task was committed atomically (TDD for Task 1 per plan's `tdd="true"`):

1. **Task 1: Write-time opts validation helper (D-11)**
   - `506839d` (test) — failing `TestValidateOptsJSON` covering nil/empty/valid-doc/valid-html/unknown-key/out-of-range cases; confirmed RED (`undefined: ValidateOptsJSON`) before implementation
   - `882039a` (feat) — `ValidateOptsJSON` implementation; all 6 subtests GREEN
2. **Task 2: cmd/manage-presets CLI (create/update/list/show/deactivate)** - `28ad73f` (feat)

**Plan metadata:** (this commit, docs)

## Files Created/Modified
- `internal/presets/optscheck.go` - `ValidateOptsJSON`, the D-11 write-time convenience check
- `internal/presets/optscheck_test.go` - pure-function unit tests for the 5 behavior cases in the plan
- `cmd/manage-presets/main.go` - operator CLI: create/update/list/show/deactivate

## Decisions Made
- Followed the plan's suggested flag surface verbatim (`--name`, `--target`, `--client-id`, `--opts`, `--description`, `--all`) — no deviation from Claude's-discretion items beyond what the plan already specified
- `list` table format: simple fixed-width columns (NAME, VERSION, SCOPE, TARGET, ACTIVE); `--all` includes inactive versions per the plan's documented discretion choice
- `show` prints every field including the full `options` JSON via `json.MarshalIndent` for readability

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None. Live-verified against the running local Postgres (`octoconv-db` on `localhost:5434`):
- Created a system-scoped preset with valid `pdf_profile` opts, listed it, showed full detail, updated it (version bumped 1→2, prior version deactivated), rejected a `{"bogus":1}` opts payload with a clear error, deactivated it, and confirmed `show` afterward correctly reports "no such preset" (ErrNotFound path)
- Repeated the client-scoped path (`--client-id <uuid>`) end-to-end: create (scope=user), list scoped to that client, deactivate
- All test rows deleted from the DB after verification (no test pollution left behind)
- `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` all clean across the whole repo (not just this plan's packages)

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- `internal/presets.Repo` and `ValidateOptsJSON` are both exercised end-to-end via the CLI, confirming the 18-01 repo's write paths (Create/Update/Deactivate/List/Get) work against the live schema
- 18-03 (API preset resolution, running concurrently in a sibling worktree on `internal/api/*`) can proceed independently — this plan touched only `internal/presets/optscheck*.go` and `cmd/manage-presets/`, no overlap
- Operators now have the only management surface for presets in v1.4; no blockers for PRST-01 completion

---
*Phase: 18-presets*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: internal/presets/optscheck.go
- FOUND: internal/presets/optscheck_test.go
- FOUND: cmd/manage-presets/main.go
- FOUND commit: 506839d (test)
- FOUND commit: 882039a (feat)
- FOUND commit: 28ad73f (feat)
