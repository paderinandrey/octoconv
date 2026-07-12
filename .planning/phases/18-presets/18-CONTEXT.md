# Phase 18: Presets - Context

**Gathered:** 2026-07-12
**Status:** Ready for planning
**Source:** Research-recommended defaults (FEATURES.md/ARCHITECTURE.md v1.4), confirmed by user at requirements definition ("Да, фиксируем" for PRST-01..04) and phase kickoff ("продолжай" with these semantics)

<domain>
## Phase Boundary

Named server-side conversion presets: operators manage them via `cmd/manage-presets` CLI; clients create jobs with `preset=<name>` instead of `target_format`+`opts`. Activates the dormant `presets` table and `jobs.preset_name`/`jobs.preset_version` columns from `0001_init.sql` — no schema migration. No REST CRUD for presets (v2, PRST-V2-01).

</domain>

<decisions>
## Implementation Decisions

### Resolution semantics
- D-01: `preset` and explicit `target_format`/`opts` are mutually exclusive — supplying both → 422 (no merge, no precedence guessing)
- D-02: a client-scoped preset shadows a system preset of the same name for its owning client; system presets are usable by any client. Lookup: `ORDER BY (scope='user') DESC, version DESC LIMIT 1` with `WHERE (scope='system' AND client_id IS NULL) OR (scope='user' AND client_id = $client)` — the cross-client filter lives in SQL, not a post-lookup Go branch
- D-03: nonexistent, inactive, or cross-client preset → the SAME 422 error text with no existence leak (mirrors the project's 404-not-403 convention)

### Versioning & lifecycle
- D-04: versions are immutable; update = insert new row with version+1 and deactivate the old one (bump-on-update); exactly one active version per (scope, client_id, name); deactivate = `is_active=false`, никакого hard delete — mirrors the client key-rotation pattern
- D-05: `operation` column: this phase uses only `'convert'`; other enum values remain dormant

### Trust & validation
- D-06: opts resolved from a preset are re-run through the SAME fail-closed validation (`ParseDocOpts`/`ParseHTMLOpts` + `ValidateApplicability`) on EVERY use — stored opts are never trusted (allowlist may have changed since the preset was created); no bypass branch
- D-07: preset resolution happens in `handleCreateJob` after client auth, resolving to `target_format`+`opts` BEFORE `EngineFor` format-pair validation, then flows through the exact existing pipeline (upload gating, engine dispatch, jobs.options snapshot)
- D-08: jobs created via preset persist provenance in the existing `jobs.preset_name`/`jobs.preset_version` columns; jobs.options still stores the RESOLVED opts snapshot (audit = both)

### Architecture
- D-09: new `internal/presets` package (repo mirroring `internal/clients` shape); `internal/api/api.go` gets a narrow `PresetRepo` interface for handler testability
- D-10: `cmd/manage-presets` CLI mirrors `cmd/manage-clients` verbs/UX: create / update / list / show / deactivate; CLI is the only management surface in v1.4
- D-11: CLI `create`/`update` validates opts through the same allowlist parsers at write time TOO (fail early for operators), but D-06 re-validation at use time remains the enforcement point

### Claude's Discretion
- Exact CLI flag names/output formatting (follow manage-clients)
- Whether `list` shows inactive versions (suggest: `--all` flag)
- Unit-test layout; whether an E2E preset test is added to internal/e2e (nice-to-have, not required by PRST-01..04)

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Schema & precedent
- `internal/db/migrations/0001_init.sql` — presets table DDL (scope CHECK, presets_scope_owner_chk), jobs.preset_name/preset_version columns
- `cmd/manage-clients/main.go` — CLI pattern to mirror (verbs, flags, output, exit codes)
- `internal/clients/repo.go` — repo shape to mirror

### Integration points
- `internal/api/handlers.go` — handleCreateJob: multipart parsing, engine detection order, opts dispatch (ParseDocOpts/ParseHTMLOpts), storage/repo write sequence
- `internal/api/api.go` — interface-segregation pattern for PresetRepo
- `internal/convert/opts.go`, `internal/convert/htmlopts.go` — the validation pipeline preset opts must flow through
- `internal/jobs/repo.go` — CreateParams (needs preset_name/preset_version fields wired to the existing columns)

### Research
- `.planning/research/FEATURES.md` — preset semantics rationale (Cloudinary/CloudConvert patterns)
- `.planning/research/ARCHITECTURE.md` — resolution placement, repo query shape
- `.planning/research/PITFALLS.md` — TOCTOU, trust-boundary, scope-precedence, existence-leak pitfalls

</canonical_refs>

<specifics>
## Specific Ideas

- Error text for all preset-resolution failures: one constant string, e.g. "unknown or inactive preset" (no name echo of other scopes)
- manage-presets `create` for client scope takes `--client-id`; system scope takes no client flag (DDL CHECK enforces the invariant)
- SEED-003 (MCP server, v1.5 candidate) will consume `list_presets` — keep repo List method shaped for reuse

</specifics>

<deferred>
## Deferred Ideas

- REST CRUD `/v1/presets` (PRST-V2-01, v2)
- `operation` values beyond 'convert' (extract/archive/inspect/render — future engine classes)
- Preset usage metrics/analytics

</deferred>

---

*Phase: 18-presets*
*Context gathered: 2026-07-12 (research-derived defaults, user-confirmed)*
