# Phase 20: Presets REST & Format Discovery - Context

**Gathered:** 2026-07-13
**Status:** Ready for planning
**Source:** Research-recommended defaults (v1.5 FEATURES/ARCHITECTURE/PITFALLS), confirmed by user at requirements definition and phase kickoff ("продолжай")

<domain>
## Phase Boundary

REST self-service for client-scope presets (`/v1/presets`: create/list/show/update/deactivate) plus a capabilities endpoint (`GET /v1/formats`). Both under the existing API-key auth. This is the discovery substrate Phase 21's MCP tools consume. System-scope presets remain CLI-only. No MCP work in this phase.

</domain>

<decisions>
## Implementation Decisions

### Endpoints & semantics
- D-01: Routes under the authenticated `/v1` group: `POST /v1/presets` (create), `GET /v1/presets` (list, active-only; `?all=true` includes inactive versions), `GET /v1/presets/{name}` (show active version), `PUT /v1/presets/{name}` (update = bump-on-update), `DELETE /v1/presets/{name}` (deactivate — soft, mirrors CLI; no hard delete)
- D-02: ALL REST operations are client-scope ONLY; `client_id` comes exclusively from the auth context (`clients` middleware), scope is hardcoded `'user'`. The request DTO has NO scope/client_id fields — mass-assignment structurally impossible (PITFALLS P4)
- D-03: Create on an existing active name → 409; update/show/deactivate on nonexistent/foreign preset → 404 (resource semantics; note: this differs from job-creation's 422-no-leak because here 404-not-403 is the project's own cross-client convention — a client's OWN namespace lookup). System presets are VISIBLE in list/show as read-only entries (they are usable in jobs) but return 403→no: attempts to update/deactivate a system preset return 404 (no-leak of manageability, consistent with cross-client convention)
- D-04: Response DTO: name, version, scope (informational), operation, target_format, options, description, is_active, created_at/updated_at — never id (uuid stays internal), never client_id
- D-05: Opts validated at write time via existing `presets.ValidateOptsJSON` (D-11 from Phase 18) — same 422 shape as jobs

### Formats endpoint
- D-06: `GET /v1/formats` returns the registry-derived capability map: for each engine class — supported (source, target) pairs, derived from `convert.Default` (`Pairs()` + `EngineFor`); JSON shape: `{"engines": {"image": {"pairs": [["png","jpg"], ...]}, "document": {...}, "html": {...}}}` — exact field naming at planner's discretion but MUST be registry-derived (no hardcoded list that can drift)
- D-07: /v1/formats requires auth like everything else on /v1 (rate limiting applies)

### Architecture
- D-08: New `PresetAdmin` interface in internal/api/api.go (List/Get/Create/Update/Deactivate for client scope) alongside the existing single-method `PresetRepo` (Resolve) — both backed by the same `*presets.Repo`; interface segregation preserved
- D-09: `internal/presets.Repo` already has all needed methods from Phase 18 — REST reuses them (no semantics duplication, PITFALLS "CLI/REST drift" prevention); any missing capability (e.g. merged system+client list for the MCP list_presets need) is added to the repo ONCE and shared
- D-10: List response includes system presets (read-only, marked by scope field) — this IS the merged view MCP-04 needs; Phase 21 consumes GET /v1/presets as-is

### Claude's Discretion
- Handler file layout (extend handlers.go vs new presets_handlers.go)
- Exact JSON error body shapes (follow existing writeError conventions)
- Whether PUT accepts partial updates (suggest: full replace of target_format/options/description, mirroring CLI update)

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Existing code being extended
- `internal/api/routes.go` — /v1 group, auth middleware chain
- `internal/api/api.go` — interface patterns (PresetRepo single-method precedent)
- `internal/api/handlers.go` — writeError/writeJSON conventions, existing preset resolution
- `internal/presets/repo.go` + `presets.go` + `optscheck.go` — the shared semantic layer (Phase 18)
- `internal/convert/convert.go` — Registry/Pairs/EngineFor for /v1/formats
- `cmd/manage-presets/main.go` — CLI semantics REST must mirror
- `internal/api/handlers_test.go` — test patterns (fakes)

### Research
- `.planning/research/FEATURES.md` — REST design recommendations
- `.planning/research/ARCHITECTURE.md` — PresetAdmin interface shape, formats endpoint gap
- `.planning/research/PITFALLS.md` — mass-assignment (P4), CLI/REST drift (P5), rate limits on new endpoints

</canonical_refs>

<specifics>
## Specific Ideas

- Live acceptance can extend scripts/presets-acceptance.sh or add a REST section to it — planner's call; REST CRUD must be provable via curl against the compose stack
- e2e: a TestPresetRESTE2E in internal/e2e is welcome but optional if the live script covers the flows

</specifics>

<deferred>
## Deferred Ideas

- system-scope через REST (PRAPIV2-01)
- Rate-limit карве-аут для /v1/formats (публичный кэшируемый ответ) — не нужен внутренним клиентам
</deferred>

---

*Phase: 20-presets-rest-format-discovery*
*Context gathered: 2026-07-13 (research-derived defaults, user-confirmed)*
