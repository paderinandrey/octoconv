---
phase: 20-presets-rest-format-discovery
verified: 2026-07-13T00:00:00Z
status: passed
score: 10/10 must-haves verified (roadmap SC 1-4 + PLAN 20-01 D-01..D-10 truths, deduplicated)
overrides_applied: 0
---

# Phase 20: Presets REST & Format Discovery Verification Report

**Phase Goal:** Clients self-service manage client-scope presets + discover formats over authenticated REST — the discovery substrate for Phase 21's MCP tools.
**Verified:** 2026-07-13
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | ROADMAP SC1: client can create/list/show/update/deactivate own client-scope presets via `/v1/presets`, scope+client_id derived solely from auth context, mass-assignment via body `scope`/`client_id` has no effect | VERIFIED | `internal/api/presets_handlers.go` `presetRequest` DTO has no scope/client_id fields (lines 40-45); every handler hardcodes `presets.ScopeUser` and `&client.ID` from `auth.ClientFromContext`. Live gate assertion "mass-assignment: DB row scope is 'user'" and "client_id is calling client A" both PASS on independent re-run. |
| 2 | ROADMAP SC2: REST mirrors CLI via shared `internal/presets.Repo`; update bumps version and echoes new number; duplicate active-create → 409; no hard delete | VERIFIED | `presets_handlers.go` `handleUpdatePreset`/`handleCreatePreset` call the SAME `Repo.Update`/`Repo.Create` the CLI (`cmd/manage-presets/main.go`) uses; live gate: UPDATE bumps to version:2, exactly one active row confirmed via psql; duplicate CREATE → 409 (`errors.Is(err, presets.ErrAlreadyExists)`); DELETE live-asserted as soft (row count=2 after deactivate, no DELETE SQL in `Deactivate`). |
| 3 | ROADMAP SC3: requesting another client's or nonexistent preset returns no-leak response (404-style, never 403, never distinguishable) | VERIFIED | `presets_handlers.go` uses a single `noSuchPreset = "preset not found"` constant across show/update/deactivate misses. Live gate: nonexistent vs cross-client vs system-scope-write 404 bodies asserted byte-identical (`{"error":"preset not found"}`), independently re-run and passing. |
| 4 | ROADMAP SC4: `GET /v1/formats` returns supported (source,target) pairs + engine classes from a read-only registry walk | VERIFIED | `internal/convert/convert.go` `Registry.Classes()` walks `r.m` (the same map `Register`/`Lookup` populate) with no hardcoded literals; `internal/api/formats_handlers.go` reshapes it directly. Live gate: 3 engine classes (document/html/image) each non-empty, known pair `["png","webp"]` present under image. |
| 5 | D-01/D-07: routes mounted inside `/v1` auth+rate-limit chain | VERIFIED | `internal/api/routes.go` mounts `/presets` and `/formats` inside `r.Route("/v1", ...)` after `ratelimit.ByIP`/`auth.Middleware`/`ratelimit.PerClient`. Live gate: unauthenticated GET to both → 401. |
| 6 | D-04: response DTO never carries id or client_id | VERIFIED | `presetResponse` struct (presets_handlers.go:51-62) has no ID/ClientID fields. Live gate asserts CREATE body does not contain `"id"` or `"client_id"` substrings. |
| 7 | D-05: opts validated at write time via `presets.ValidateOptsJSON`, 422 shape | VERIFIED | `handleCreatePreset`/`handleUpdatePreset` call `presets.ValidateOptsJSON(req.Options)` → 422 on error. Live gate: invalid `margin_mm:9999` → 422. |
| 8 | D-08: `PresetAdmin` is a NEW interface, `PresetRepo` unchanged (single-method) | VERIFIED | `internal/api/api.go`: `PresetRepo` still has only `Resolve` (lines 28-30); `PresetAdmin` is a separate 7-method interface (lines 37-45); both parameters passed separately to `NewServer`, both backed by the same `presets.NewRepo(pool)` value in `cmd/api/main.go:86,101`. |
| 9 | D-09/D-10: merged shadow-precedence view added ONCE in repo, reused by handlers (no duplicated ownership logic) | VERIFIED | `internal/presets/repo.go` `ListForClient`/`GetForClient` encode shadow precedence entirely in SQL (WHERE/NOT EXISTS, ORDER BY scope='user' DESC); handlers only call these methods, no Go-side ownership branch. Live gate: LIST for client A shows own preset + system preset marked `scope:system`; SHOW system-only name → 200 read-only. |
| 10 | Zero new deps, zero migrations, CLI regression-free | VERIFIED | `git diff 285e55e~1 HEAD -- go.mod go.sum` empty; `internal/db/migrations/` unchanged (last file `0005_html_engine.sql`, predates phase); `cmd/manage-presets/main.go` still calls `presets.NewRepo` directly and its `errors.Is` checks are untouched; `go build ./...` and full `go test` green. |

**Score:** 10/10 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/convert.go` | `Registry.Classes()` registry-walk | VERIFIED | Present, walks `r.m`, sorted deterministically (lines 106-123) |
| `internal/presets/repo.go` | `ListForClient`/`GetForClient`, `Create` returns `ErrAlreadyExists` | VERIFIED | Present (lines 79-111, 277-350) |
| `internal/presets/presets.go` | `ErrAlreadyExists` sentinel | VERIFIED | Line 35 |
| `internal/api/api.go` | `PresetAdmin` interface + `Server.presetAdmin` field | VERIFIED | Lines 37-45, 81, 117 |
| `internal/api/presets_handlers.go` | 5 CRUD handlers, narrow DTO, no-leak mapping | VERIFIED | All 5 handlers present, DTOs narrow, no-leak constant reused |
| `internal/api/formats_handlers.go` | `handleListFormats` registry-walk | VERIFIED | Present, calls `convert.Default.Classes()` |
| `internal/api/routes.go` | routes mounted inside `/v1` auth+ratelimit group | VERIFIED | Lines 41-48 |
| `scripts/presets-rest-acceptance.sh` | executable live hard gate, ≥120 lines | VERIFIED | 374 lines, executable, re-run independently: 42/42 assertions PASS |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `presets_handlers.go` | `internal/presets.Repo` (via `PresetAdmin`) | `s.presetAdmin.Create/Update/Deactivate/GetForClient/ListForClient` | WIRED | Confirmed by direct code read and passing handler tests |
| `presets_handlers.go` | `auth.ClientFromContext` | clientID+scope derivation | WIRED | Called in every handler |
| `formats_handlers.go` | `convert.Default.Classes` | registry walk | WIRED | `convert.Default.Classes()` called directly, no literals |
| `routes.go` | `/v1` middleware chain | routes mounted inside `r.Route("/v1", ...)` | WIRED | Verified by code read + live 401 assertions |

### Behavioral Spot-Checks / Probe Execution

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Unit + DB-gated integration tests | `DATABASE_URL=... go test ./internal/api/... ./internal/presets/... ./internal/convert/... -count=1` | `ok` all 3 packages | PASS |
| `go build ./...` | clean | no output | PASS |
| `go vet ./...` | clean | no output | PASS |
| `gofmt -l internal/ cmd/` | clean | no files listed | PASS |
| Live REST acceptance gate (independently re-run by verifier, not just trusting SUMMARY) | `bash scripts/presets-rest-acceptance.sh` | `=== ALL 42 ASSERTIONS PASSED ===`, exit 0 | PASS |
| Anti-pattern scan (TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER/stub phrases) on all phase-touched files | `grep -n -E ...` | no matches in any of the 9 phase files | PASS (clean) |
| go.mod/go.sum diff since pre-phase commit | `git diff 285e55e~1 HEAD -- go.mod go.sum` | empty | PASS (zero new deps confirmed) |
| Migrations diff | `ls internal/db/migrations/` | unchanged, last is `0005_html_engine.sql` (pre-phase) | PASS (zero new migrations confirmed) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| PRAPI-01 | 20-01, 20-02 | Client-scope CRUD via `/v1/presets`, auth-derived ownership, narrow DTO | SATISFIED | Handlers + live gate assertions 1-11 |
| PRAPI-02 | 20-01, 20-02 | CLI-mirror semantics (bump-on-update, single active version, no hard delete), 409 dup, no-leak | SATISFIED | Shared repo reuse + live gate assertions 2, 8-10 |
| PRAPI-03 | 20-01, 20-02 | `GET /v1/formats` registry-derived | SATISFIED | `Classes()` + live gate assertion 12 |

Note: `.planning/REQUIREMENTS.md` still shows PRAPI-01/02/03 as unchecked `[ ]` checkboxes and `.planning/ROADMAP.md`'s plan-list bullets for 20-01/20-02 are unchecked `[ ]`, even though the phase-list line 87 is marked `[x]` complete. This is a documentation-bookkeeping lag, not a code gap — informational only, does not block phase completion since the actual code/tests/live-gate evidence is conclusive.

### Anti-Patterns Found

None. Scanned `internal/api/presets_handlers.go`, `formats_handlers.go`, `routes.go`, `internal/presets/repo.go`, `presets.go`, `internal/convert/convert.go`, `internal/api/api.go`, `cmd/api/main.go`, `scripts/presets-rest-acceptance.sh` for TBD/FIXME/XXX/TODO/HACK/PLACEHOLDER/stub phrases/empty-return stubs — zero matches.

### Human Verification Required

None. All must-haves are verifiable via code inspection, automated tests, and a live curl/psql gate that was independently re-executed by this verifier (not just trusted from SUMMARY.md), producing the same 42/42 pass result reported by the executor.

### Gaps Summary

No gaps. All ROADMAP Phase 20 success criteria (4) and all PLAN 20-01/20-02 `must_haves.truths` (D-01 through D-10) are verified against actual code:
- `PresetAdmin` is additive, `PresetRepo` untouched (interface segregation preserved).
- Mass-assignment is structurally impossible (narrow DTO, not just runtime validation) — confirmed both by code read and live psql-verified proof.
- No-leak 404 uses one shared constant across nonexistent/cross-client/system-scope-write misses — confirmed byte-identical live.
- Merged shadow-precedence view lives in SQL inside `internal/presets.Repo`, not duplicated in handlers.
- `/v1/formats` is registry-derived with zero hardcoded pair literals.
- Zero new dependencies, zero new migrations, CLI untouched and still passing.
- Full test suite (unit + DB-gated integration) green; live acceptance gate independently re-run by the verifier and passing 42/42.

---

_Verified: 2026-07-13_
_Verifier: Claude (gsd-verifier)_
