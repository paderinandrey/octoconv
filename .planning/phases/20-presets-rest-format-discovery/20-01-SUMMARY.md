---
phase: 20-presets-rest-format-discovery
plan: 01
subsystem: api
tags: [chi, presets, rest, mass-assignment, postgres, registry]

# Dependency graph
requires:
  - phase: 18-presets
    provides: internal/presets.Repo (Create/Update/Deactivate/Get/List, ValidateOptsJSON, ScopeUser/ScopeSystem)
provides:
  - PresetAdmin interface (internal/api/api.go) alongside the unchanged single-method PresetRepo
  - Five /v1/presets REST handlers (create/list/show/update/deactivate) mounted inside the /v1 auth+rate-limit chain
  - GET /v1/formats registry-derived capability endpoint
  - convert.Registry.Classes() registry-walk method
  - presets.Repo.ListForClient/GetForClient merged shadow-precedence reads
  - presets.ErrAlreadyExists sentinel for 409 mapping
affects: [21-mcp-tools]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Second, narrower interface (PresetAdmin) added alongside an existing interface (PresetRepo) rather than widening it -- interface segregation preserved per ARCHITECTURE Anti-Pattern 3"
    - "Merged effective-view reads (ListForClient/GetForClient) encode shadow precedence entirely in SQL, never a post-query Go ownership branch"
    - "Narrow request/response DTOs as the mass-assignment and info-disclosure boundary: presetRequest has no scope/client_id fields; presetResponse never carries id/client_id"
    - "Single byte-identical no-leak 404 body across nonexistent/cross-client/system-scope-write misses"

key-files:
  created:
    - internal/api/presets_handlers.go
    - internal/api/presets_handlers_test.go
    - internal/api/formats_handlers.go
    - internal/api/formats_handlers_test.go
  modified:
    - internal/convert/convert.go
    - internal/convert/convert_test.go
    - internal/presets/presets.go
    - internal/presets/repo.go
    - internal/presets/repo_test.go
    - internal/api/api.go
    - internal/api/routes.go
    - internal/api/handlers_test.go
    - internal/api/routes_test.go
    - cmd/api/main.go

key-decisions:
  - "PresetAdmin is a brand-new interface, not a widened PresetRepo (D-08) -- both interfaces are backed by the same *presets.Repo concrete type"
  - "REST write handlers hardcode scope=presets.ScopeUser and derive clientID solely from auth.ClientFromContext; the request DTO structurally has no scope/client_id fields (D-02, mass-assignment closed by construction, not by runtime validation)"
  - "Update/Deactivate/Show all reuse the SAME no-leak 404 body string so nonexistent, cross-client, and system-scope-write attempts are byte-identical responses (D-03)"
  - "GET /v1/formats is reshaped from convert.Registry.Classes(), a new registry-walk method -- no hardcoded engine/pair literal exists anywhere in the API layer"

requirements-completed: [PRAPI-01, PRAPI-02, PRAPI-03]

# Metrics
duration: ~15min
completed: 2026-07-13
---

# Phase 20 Plan 01: PresetAdmin + /v1/presets CRUD + /v1/formats Summary

**Authenticated REST self-service for client-scope presets (create/list/show/update/deactivate) plus a registry-derived GET /v1/formats capability endpoint, both mounted inside the existing /v1 auth+rate-limit chain.**

## Performance

- **Duration:** ~15 min (commit span 02:13–02:24 UTC+3, plus prior read/context-gathering)
- **Started:** 2026-07-13T02:13:10+03:00
- **Completed:** 2026-07-13T02:24:30+03:00
- **Tasks:** 3/3 completed
- **Files modified:** 14 (4 created, 10 modified)

## Accomplishments
- `convert.Registry.Classes()` — a deterministic, registry-derived engine→pairs walk (D-06), the single source `GET /v1/formats` reshapes into its JSON envelope.
- `presets.Repo.ListForClient`/`GetForClient` — merged effective-view reads (user shadows system) encoded entirely in SQL, reused by both the REST list/show handlers and (per D-10) available as-is for Phase 21's MCP `list_presets`.
- `presets.ErrAlreadyExists` sentinel wired through `Create`'s existing-active branch via `errors.Is`, mapped to REST's 409.
- A new `PresetAdmin` interface (D-08) added alongside the unchanged single-method `PresetRepo`, both backed by the same `*presets.Repo` — interface segregation preserved.
- Five `/v1/presets` handlers (create/list/show/update/deactivate) with a narrow write DTO (no scope/client_id fields — mass-assignment structurally impossible) and a narrow read DTO (never id/client_id).
- `GET /v1/formats`, mounted inside `/v1`, returning `{"engines":{"image":{"pairs":[["png","webp"],...]},...}}`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Shared foundation — registry walk + merged-view repo reads + 409 sentinel** - `285e55e` (feat)
2. **Task 2: PresetAdmin interface, /v1/presets CRUD handlers, routes + wiring, handler tests** - `ba4441b` (feat)
3. **Task 3: GET /v1/formats registry-walk endpoint + route + test** - `ac52651` (feat)

Plus one small gofmt-alignment fixup: `0dd76d2` (style)

_Note: no TDD RED/GREEN split commits were used — tests were written alongside each task's implementation and verified green before committing, per the plan's `tdd="true"` marker interpreted as "tests included", consistent with the rest of this codebase's testing convention (stdlib `testing`, no separate red-commit discipline elsewhere in the repo)._

## Files Created/Modified
- `internal/convert/convert.go` — added `Registry.Classes()` registry-walk method (D-06)
- `internal/convert/convert_test.go` — `TestRegistryClasses` (image class contains a known libvips pair, stable across calls)
- `internal/presets/presets.go` — added `ErrAlreadyExists` sentinel
- `internal/presets/repo.go` — `Create` now wraps `ErrAlreadyExists`; added `ListForClient`/`GetForClient` merged shadow-precedence reads
- `internal/presets/repo_test.go` — `TestListForClientShadowing`, `TestGetForClientShadowing`, `TestCreateDuplicateReturnsErrAlreadyExists` (DB-gated, run live against Postgres, all pass)
- `internal/api/api.go` — new `PresetAdmin` interface + `Server.presetAdmin` field + `NewServer` positional arg
- `internal/api/presets_handlers.go` — five CRUD handlers, narrow request/response DTOs, no-leak 404 constant
- `internal/api/presets_handlers_test.go` — 12 handler tests (create/dup/mass-assignment/bad-opts/list/show/update/deactivate/no-leak/auth)
- `internal/api/formats_handlers.go` — `handleListFormats`, registry-derived JSON envelope
- `internal/api/formats_handlers_test.go` — registry-derived content + auth-group membership
- `internal/api/routes.go` — mounted `/presets` and `/formats` inside the existing `/v1` auth+rate-limit group
- `internal/api/handlers_test.go` — new `fakePresetAdmin` + `defaultFakePresetAdmin()`; every existing `NewServer` call site updated for the new positional arg
- `internal/api/routes_test.go` — its one direct `NewServer` call site updated
- `cmd/api/main.go` — passes the same `presetRepo` value for both `PresetRepo` and `PresetAdmin` positional args

## Decisions Made
- Kept `PresetAdmin` as a strictly additive interface (never touched `PresetRepo`'s single-method shape) — matches ARCHITECTURE's explicit anti-pattern warning against widening it.
- Response DTOs for create/update fetch the full row via `GetForClient` after the write, rather than hand-assembling a partial DTO from `Create`/`Update`'s narrow return values — guarantees the response always reflects exactly what the merged-view read path would show, with zero duplicated field-mapping logic.
- `GET /v1/formats` field naming (`engines` / `pairs` / two-element `[from, to]` arrays) chosen at implementer's discretion per D-06, since the context explicitly left exact naming open.

## Deviations from Plan

None - plan executed exactly as written. All three tasks, their `<action>` and `<verify>` steps, and every `must_haves.truths`/`artifacts`/`key_links` item were implemented as specified.

## Issues Encountered

None. All DB-gated `internal/presets` integration tests (both pre-existing and newly added) were run live against a local Postgres container (`docker compose -p octoconv up -d postgres`, `DATABASE_URL=postgres://octo:octo-pass@localhost:5434/octo_db`) and pass; the container was left running per environment instructions.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- `GET /v1/presets` (merged view, system presets read-only via `scope`) is ready for Phase 21's MCP `list_presets` tool to consume as-is (D-10).
- `PresetAdmin` and the registry-derived `/v1/formats` are both fully wired and tested; no known stubs or deferred wiring.
- Live/e2e acceptance against the docker-compose stack (curl-based REST CRUD proof) is deferred to 20-02 per the plan's scope split.

---
*Phase: 20-presets-rest-format-discovery*
*Completed: 2026-07-13*

## Self-Check: PASSED

All created/modified files verified present on disk; all 4 task/style commit hashes (285e55e, ba4441b, ac52651, 0dd76d2) verified present in git log.
