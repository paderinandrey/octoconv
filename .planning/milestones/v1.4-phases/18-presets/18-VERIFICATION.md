---
phase: 18-presets
verified: 2026-07-12T22:20:00Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
---

# Phase 18: Presets Verification Report

**Phase Goal:** Clients create conversion jobs by named preset instead of hand-supplying target_format/opts, and operators manage those presets through a CLI — reusing the existing validated-opts pipeline with zero new validation logic and zero new migration.
**Verified:** 2026-07-12T22:20:00Z (HEAD 117beab)
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Operator can create/update/list/show/deactivate system- and client-scoped presets via `cmd/manage-presets`, no hard delete, single active version per name, bump-on-update, mirroring `manage-clients` | VERIFIED | `cmd/manage-presets/main.go` implements all 5 verbs dispatching to `internal/presets.Repo`; no `delete`/`DELETE` verb exists; scope derived from `--client-id` presence (`scopeAndClient`). Live gate step 5 (create/list/show/update/deactivate) all pass with real CLI-output + SQL assertions (33-assertion run, re-executed live, exit 0). |
| 2 | `POST /v1/jobs` with `preset=<name>` converts using the preset's stored target/opts; job carries `preset_name`/`preset_version` provenance | VERIFIED | `handlers.go` resolves preset via `s.presets.Resolve`, sets `target = p.TargetFormat`, feeds `p.Options` through the identical opts pipeline, and passes `PresetName`/`PresetVersion` into `jobs.CreateParams`; `jobs/repo.go` Create INSERT includes `preset_name, preset_version` columns (NULL when unset). `TestPresetProvenanceRoundTrip`/`TestPresetProvenanceNullForNonPreset` (jobs pkg) pass; live gate: preset `Q` job reaches `done`, `SELECT preset_name||'/'||preset_version` == `Q/1`. |
| 3 | Client-scoped preset shadows same-name system preset for owning client; system presets usable by any client | VERIFIED | `Resolve` SQL: `ORDER BY (scope='user') DESC, version DESC LIMIT 1` with WHERE covering both scope branches. `TestResolveShadowing` + `TestResolveSystemOnlyFallback` (DB-gated, both pass). Live gate: client A resolves preset `P` to user target `png`, client B resolves to system target `webp`. |
| 4 | preset + explicit target_format/opts → 422; nonexistent/inactive/cross-client preset → same 422, no existence leak (SQL WHERE filter, not Go branch) | VERIFIED | D-01 XOR check in handler (`usingPreset && (rawTarget != "" \|\| rawOpts not empty)` → 422 distinct text); D-03 collapse to single `errUnknownPreset` constant, no `p.ClientID != client.ID` branch present (confirmed by reading full handler). `TestCreateJob_PresetAndTargetMutuallyExclusive`, `TestCreateJob_PresetAndOptsMutuallyExclusive`, `TestCreateJob_UnknownPreset422NoLeak` (byte-identical body assertion) all pass. Live gate: byte-identical `{"error":"unknown or inactive preset"}` bodies for nonexistent vs. cross-client-only preset `S`. |
| 5 | Preset-resolved opts re-run through same fail-closed ParseDocOpts/ParseHTMLOpts validation on every use, no bypass branch | VERIFIED | Handler substitutes `rawOpts` source only (`json.Marshal(presetOptsMap)`) then feeds the SAME `ParseDocOpts`/`ParseHTMLOpts` + `ValidateApplicability`/`ValidateHTMLApplicability` switch used for ad-hoc opts — confirmed by reading the diff context (single switch block, no second parser). `TestCreateJob_PresetOptsRevalidated` (unknown key rejected) + `TestCreateJob_PresetHTMLOptsResolved` (valid preset opts flow through HTML path) pass. Live gate: a preset with SQL-inserted `margin_mm:9999` (out of the [0,50] range) is rejected 422 at use time. |

**Score:** 5/5 truths verified

### PLAN Frontmatter Must-Haves (D-01..D-11, all 4 plans)

| Decision | Plan | Status | Evidence |
|---|---|---|---|
| D-09 (new `internal/presets` package mirroring `internal/clients`) | 18-01 | VERIFIED | `internal/presets/{presets.go,repo.go}` — `Repo{pool}`, `NewRepo`, `ErrNotFound`, `pgx.BeginFunc` transactions, package doc comment. |
| D-02 (shadowing, single SQL query, `ORDER BY (scope='user') DESC, version DESC LIMIT 1`) | 18-01 | VERIFIED | `grep` confirms exact ORDER BY clause in `repo.go:55`. |
| D-03 (no-leak, filter entirely in SQL WHERE) | 18-01/18-03 | VERIFIED | Resolve WHERE has both scope branches + `is_active` + `operation='convert'`; handler has no post-lookup ownership branch. |
| D-04 (bump-on-update, no hard delete, single active version) | 18-01/18-02/18-04 | VERIFIED | `Update`/`Deactivate` use `pgx.BeginFunc`; zero `DELETE FROM presets` in repo.go; live gate SQL-confirms exactly one active row at v2 after update, zero after deactivate, rows still present (count=2). Accepted residual risk (no DB-level unique-index backstop on `is_active`) explicitly documented in 18-01-SUMMARY.md — reasonable given zero-migration constraint. |
| D-05 (operation defaults/filters to 'convert' only) | 18-01 | VERIFIED | `OperationConvert` const; Resolve/Create hardcode it. |
| D-06 (re-validation, no bypass) | 18-03 | VERIFIED | See SC5 row above. |
| D-07 (resolution after auth, before EngineFor) | 18-03 | VERIFIED | Handler: `client, _ := auth.ClientFromContext(ctx)` (line 151) → preset Resolve (164-182) → Sniff/detection (188+) → `EngineFor` (273). Order confirmed by direct read. |
| D-08 (provenance in jobs columns, opts snapshot unchanged) | 18-01/18-03 | VERIFIED | See SC2 row; `jobs.options` still stores `normalizedOpts` unconditionally. |
| D-10 (CLI mirrors manage-clients, scope via --client-id absence/presence) | 18-02 | VERIFIED | `cmd/manage-presets/main.go` — direct `db.Connect`, `log.Fatalf` fail-fast, `scopeAndClient` helper. |
| D-11 (write-time opts validation, accepts either schema) | 18-02 | VERIFIED | `internal/presets/optscheck.go` `ValidateOptsJSON`; called in CLI `create`/`update`; 6 test cases pass. |
| Pitfall 8 (pre-Create active re-check, TOCTOU) | 18-03 | VERIFIED | Second `s.presets.Resolve` call after `storage.Upload`, before `repo.Create` (handlers.go:425-439); gates on `ErrNotFound` OR changed id/version. `TestCreateJob_PresetDeactivatedDuringCreate` proves `repo.Create` never invoked and upload is left in place. |

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/presets/presets.go` | Preset struct + consts + ErrNotFound | VERIFIED | Present, matches interface spec exactly. |
| `internal/presets/repo.go` | Resolve/Create/Update/Deactivate/List/Get | VERIFIED | All 6 methods present, substantive SQL, no stubs. |
| `internal/presets/repo_test.go` | ≥6 DB-gated test functions | VERIFIED | 9 `func Test` functions; all pass with `DATABASE_URL` set. |
| `internal/presets/optscheck.go` | `ValidateOptsJSON` | VERIFIED | Present, calls both allowlist parsers. |
| `internal/presets/optscheck_test.go` | Pure-function tests | VERIFIED | 6 subtests, all pass. |
| `cmd/manage-presets/main.go` | CLI 5 verbs | VERIFIED | `func main` dispatches create/update/list/show/deactivate; no delete verb; builds/vets clean. |
| `internal/api/api.go` | `PresetRepo` interface, `Server.presets` field, `NewServer` param | VERIFIED | Single-method interface (`Resolve`), positional param after `queue Enqueuer`. |
| `internal/api/handlers.go` | Preset resolution flow in `handleCreateJob` | VERIFIED | `formFieldPreset`, XOR gate, resolution, re-validation, pre-Create re-check, provenance — all present and correctly ordered. |
| `internal/api/handlers_test.go` | `fakePresetRepo` + ≥5 preset tests | VERIFIED | 7 new preset-specific `Test` functions (resolved-image, mutual-exclusivity x2, no-leak, opts-revalidated, deactivated-during-create, HTML-opts); all pass. |
| `internal/jobs/repo.go` | `preset_name`/`preset_version` in INSERT | VERIFIED | Column list extended; nullable-pointer pattern for empty provenance. |
| `cmd/api/main.go` | `presets.NewRepo(pool)` wired into `NewServer` | VERIFIED | Confirmed. |
| `scripts/presets-acceptance.sh` | Live hard gate | VERIFIED | Executable, 358 lines, 33 assertions; re-executed live in this verification session — exit 0, all 33 PASS. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/presets/repo.go Resolve` | presets table | scope-precedence SQL | WIRED | `ORDER BY (scope='user') DESC, version DESC LIMIT 1` confirmed by direct read + DB-gated shadowing tests. |
| `internal/jobs/repo.go Create` | `jobs.preset_name`/`preset_version` | INSERT column list | WIRED | Confirmed by grep + `TestPresetProvenanceRoundTrip`. |
| `internal/api/handlers.go handleCreateJob` | `internal/presets.Repo.Resolve` (via PresetRepo) | `s.presets.Resolve(ctx, client.ID, presetName)` | WIRED | Called twice (resolution + pre-Create re-check); confirmed by grep (2 occurrences) and passing tests. |
| `handleCreateJob preset opts` | `convert.ParseDocOpts`/`ParseHTMLOpts` | re-marshaled preset opts fed into existing engine-keyed switch | WIRED | Single switch block reused, no bypass; confirmed by reading the diff context around lines 338-404. |
| `handleCreateJob repo.Create` | `jobs.CreateParams.PresetName`/`PresetVersion` | provenance passed to Create | WIRED | Confirmed lines 452-453. |
| `cmd/manage-presets/main.go` | `internal/presets.Repo` | `presets.NewRepo(pool)` | WIRED | Confirmed. |
| `cmd/manage-presets create/update` | `internal/presets.ValidateOptsJSON` | write-time opts validation | WIRED | Confirmed in `runCreate`/`runUpdate`. |

### Data-Flow Trace (Level 4)

Not applicable in the UI-rendering sense (backend-only feature); data flow was traced end-to-end instead via the live acceptance script: CLI-created preset row → `Resolve` SQL query → HTTP response → job row → DB assertion, all confirmed with real Postgres data (not fakes) in the live re-run.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `go build ./...` | `go build ./...` | exit 0 | PASS |
| `go vet ./...` | `go vet ./...` | exit 0 | PASS |
| `gofmt -l .` | `gofmt -l .` | no output | PASS |
| Full repo test suite | `go test ./... -count=1` | all packages `ok` | PASS |
| DB-gated presets/jobs/api tests | `DATABASE_URL=... go test ./internal/presets/... ./internal/api/... ./internal/jobs/... -count=1 -v` | all pass (9 presets, 7 new api, 2 jobs provenance tests) | PASS |
| Live acceptance hard gate | `bash scripts/presets-acceptance.sh` | 33/33 assertions PASS, exit 0 (re-executed live in this session against the running compose stack, independent of the executor's prior two runs) | PASS |
| Zero new deps | `git diff 796301c..HEAD --stat -- go.mod go.sum` | empty diff | PASS |
| Zero new migrations | `git diff 796301c..HEAD --stat -- internal/db/migrations/` | empty diff; presets table + jobs.preset_name/preset_version predate this phase in `0001_init.sql` | PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|---|---|---|---|---|
| PRST-01 | 18-02 | Operator manages presets via CLI (5 verbs, no hard delete) | SATISFIED | `cmd/manage-presets/main.go`; live gate step 5. |
| PRST-02 | 18-03 | Client creates job via preset name, shadowing | SATISFIED | `handlers.go` preset resolution + shadowing tests/live gate. |
| PRST-03 | 18-03 | Mutual exclusivity + no-leak 422 | SATISFIED | D-01/D-03 tests + live gate byte-identical bodies. |
| PRST-04 | 18-03 | Re-validation of resolved opts | SATISFIED | D-06 tests + live gate SC5. |

Note: `.planning/REQUIREMENTS.md`'s traceability table still shows PRST-01..04 as unchecked `[ ]` / "Pending" — this is a stale tracking artifact, not a code gap; all four requirements are functionally satisfied per the evidence above. Recommend the roadmap/requirements sync step check these boxes when this phase is closed out (informational, not a blocking finding).

### Anti-Patterns Found

None. Scanned all phase-18 modified files (`internal/presets/*`, `cmd/manage-presets/main.go`, `internal/api/{api,handlers,handlers_test}.go`, `internal/jobs/{jobs,repo,repo_test}.go`, `cmd/api/main.go`, `scripts/presets-acceptance.sh`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` and stub-shaped returns — zero matches.

### Human Verification Required

None. All observable truths are verifiable programmatically (code inspection, unit/integration tests, and a live-stack scripted hard gate that was independently re-executed in this verification session against the real Postgres/API/worker stack), and every gate produced concrete pass evidence.

### Gaps Summary

None. All 5 ROADMAP success criteria and all 11 locked decisions (D-01..D-11) trace to real, substantive, wired code. `go build`/`go vet`/`gofmt` clean; full test suite green including DB-gated tests; the live acceptance script was re-run independently in this verification (not just trusted from SUMMARY.md) and passed 33/33 assertions. Zero new Go dependencies and zero new migrations confirmed via `git diff` across the entire phase-18 commit range. REQUIREMENTS.md traceability checkboxes are stale but this is a documentation-sync item, not a functional gap.

---

_Verified: 2026-07-12T22:20:00Z_
_Verifier: Claude (gsd-verifier)_
