---
phase: 26-operator-presets-rest
verified: 2026-07-14T11:25:39Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 4/5
  gaps_closed:
    - "An operator client (ID in OPERATOR_CLIENT_IDS) can create/list/show/update/deactivate system-scope presets via /v1/system/presets — INCLUDING deactivate → re-create of the same name (CR-01)"
  gaps_remaining: []
  regressions: []
---

# Phase 26: Operator System-Presets REST Verification Report

**Phase Goal:** System-scope presets are manageable over REST by operator clients only, with no new auth model and no schema change.
**Verified:** 2026-07-14T11:25:39Z
**Status:** passed
**Re-verification:** Yes — after gap closure (26-02-PLAN.md / 26-02-SUMMARY.md)

## Gap Closure Verification (CR-01)

The prior verification (2026-07-14T14:05:00Z, `status: gaps_found`, 4/5) found ONE blocking gap: `internal/presets/repo.go`'s `Create` hardcoded `version = 1` and only pre-checked ACTIVE-row duplicates, while `presets_system_uq`/`presets_user_uq` (0001_init.sql:35-36) uniquely index `(name, version)` across ALL rows including inactive ones. A deactivate-then-recreate of the same preset name therefore always collided on the unique index, and the resulting pgx unique-violation was never mapped to `presets.ErrAlreadyExists` — it surfaced as a raw 500.

Gap-closure plan 26-02 executed two commits on `main`:
- `2f23da5` (test, RED) — added `TestCreateAfterDeactivateBumpsVersion` (system + user scope subtests), which failed against the pre-fix `repo.go`.
- `1a6728a` (fix, GREEN) — reworked `Create` to insert at `COALESCE(MAX(p.version), 0) + 1` across active+inactive rows for `(scope, client_id, name)`, and added a `pgconn.PgError` `23505` → `ErrAlreadyExists` backstop.

**Code read directly (not summary-trusted):**

- `internal/presets/repo.go:89-130` — `Create` now: (1) keeps the pre-existing `EXISTS ... AND is_active` duplicate pre-check, returning `fmt.Errorf(...: %w, ErrAlreadyExists)` on an active collision; (2) replaces the old `const version = 1` fixed-value INSERT with `INSERT INTO presets (...) SELECT $1, COALESCE(MAX(p.version), 0) + 1, ... FROM presets p WHERE p.scope = $2 AND p.name = $1 AND (p.client_id = $3 OR ($3::uuid IS NULL AND p.client_id IS NULL)) RETURNING id, version` — an aggregate `SELECT` with no `GROUP BY` always returns exactly one row (`NULL` → `COALESCE` → `0` → `+1` → `1` for a genuinely fresh name), so fresh-name semantics are unchanged and revived names get `MAX+1`; (3) on INSERT error, `var pgErr *pgconn.PgError; if errors.As(err, &pgErr) && pgErr.Code == "23505" { return uuid.Nil, 0, ErrAlreadyExists }` before the generic wrap — confirmed present at lines 122-127.
- `internal/presets/repo_test.go:469-534` — `TestCreateAfterDeactivateBumpsVersion` exists, is DB-backed (uses `newTestRepo`/`createTestClient`/`uniqueName`, skips without `DATABASE_URL`), and asserts for BOTH `t.Run("system scope", ...)` and `t.Run("user scope", ...)`: first `Create` returns version 1; `Deactivate` succeeds; second `Create` (after deactivate) returns `nil` error and version 2 (not the same id as the deactivated row); `Get` returns the bumped active row; `count(*) FILTER (WHERE is_active)` = 1 and total row count = 2 (no hard delete). This is a substantive, non-trivial regression test — not a stub.
- `internal/api/system_presets_handlers.go:105-106` — confirms the REST layer already maps `errors.Is(err, presets.ErrAlreadyExists)` → `http.StatusConflict` (409), so the repo-level fix flows end-to-end to the REST boundary: a lost create-race or an active-duplicate now yields 409, never 500.

**Live verification (not just code reading):** Ran against a live Postgres (`octoconv-db` container on `localhost:5434`, env sourced from `.env`):

```
go test ./internal/presets/ -v
```
Full result: 15 test functions (including `TestCreateAfterDeactivateBumpsVersion/system_scope` and `/user_scope`) — **all PASS**, `ok github.com/apaderin/octoconv/internal/presets 1.050s`.

```
go test ./internal/api/ -v
```
Full result: all subtests including `TestSystemPresets_OperatorSucceedsForAllVerbs` (create/list/show/update/deactivate), `TestSystemPresets_NonOperatorNoLeak404`, `TestSystemPresets_EmptyAllowlistDeniesEveryone`, `TestParseOperatorClientIDs`, `TestRequireOperator_*` — **all PASS**, `ok github.com/apaderin/octoconv/internal/api 0.421s`.

```
go test ./...
```
Every package in the repo (`internal/api`, `internal/auth`, `internal/clients`, `internal/convert`, `internal/e2e`, `internal/jobs`, `internal/mcpserver`, `internal/metrics`, `internal/presets`, `internal/queue`, `internal/ratelimit`, `internal/reconciler`, `internal/storage`, `internal/webhook`, `internal/worker`, `cmd/mcp-http`) — **all PASS, no regressions anywhere in the repo.**

```
go build ./... && go vet ./... && gofmt -l internal/presets internal/api cmd/api
```
All clean, no output.

**Gap status: CLOSED.** Truth 1 ("An operator client can create/list/show/update/deactivate system-scope presets via /v1/system/presets") now holds for the FULL lifecycle including deactivate → re-create of the same name, confirmed by direct code reading (not summary-trusted) plus live DB-backed test execution in this verification pass.

## Goal Achievement (Full Re-Check)

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | An operator client can create/list/show/update/deactivate system-scope presets via /v1/system/presets, INCLUDING deactivate → re-create of the same name (SC1) | ✓ VERIFIED | All 5 verbs pass for a fresh name AND for a revived (deactivated-then-recreated) name. `TestSystemPresets_OperatorSucceedsForAllVerbs` (fake admin, REST layer) + `TestCreateAfterDeactivateBumpsVersion` (real repo, live Postgres) both green. REST 409 mapping confirmed at `system_presets_handlers.go:105-106`. |
| 2 | A non-operator client gets a byte-identical no-leak 404 (never 403) on every /v1/system/presets route (SC2) | ✓ VERIFIED | Unchanged since prior verification. `requireOperator` (`internal/api/system_presets_handlers.go`) calls the identical `writeError(w, http.StatusNotFound, noSuchPreset)`. `TestSystemPresets_NonOperatorNoLeak404` passes live in this pass. |
| 3 | Empty/unset OPERATOR_CLIENT_IDS = zero operators (fail-closed) (part of SC3) | ✓ VERIFIED | Unchanged. `TestSystemPresets_EmptyAllowlistDeniesEveryone` + `TestRequireOperator_EmptyAllowlistDeniesEveryone` pass live in this pass. |
| 4 | A malformed OPERATOR_CLIENT_IDS aborts API startup loudly (fail-loud) (part of SC3) | ✓ VERIFIED | Unchanged. `TestParseOperatorClientIDs/malformed_uuid_errors` and `/one_bad_token_among_good_ones_errors` pass live in this pass; `cmd/api/main.go` still calls `log.Fatalf` on parse error (confirmed unmodified by `git diff` scope of 26-02, which touched only `internal/presets/`). |
| 5 | Operator-ness is never surfaced in any response body (Claude's Discretion) | ✓ VERIFIED | Unchanged. `presetResponse` has no such field; matrix test asserts no `operator`/`is_operator` keys. |

**Score:** 5/5 truths verified.

**Success criteria mapping:**
- SC1 (manageable via REST, all 5 verbs, including deactivate→recreate): **fully verified**, gap closed.
- SC2 (non-operator no-leak 404, never 403): **fully verified**, no change.
- SC3 (env-only gate, zero migrations, no second auth system, fail-closed, fail-loud): **fully verified**, no change; the gap-closure plan touched only `internal/presets/repo.go` and its test file — no migration, no `PresetAdmin` signature change, no API-layer change (confirmed: `26-02-SUMMARY.md`'s `files_modified` list matches `git show --stat` for both commits, restricted to `internal/presets/`).

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/api/system_presets_handlers.go` | `ParseOperatorClientIDs`, `requireOperator`, 5 system-scope handlers, 409 mapping | ✓ VERIFIED | Unchanged since prior pass; re-confirmed present and the 409 mapping at lines 105-106 read directly. |
| `internal/api/system_presets_handlers_test.go` | operator/non-operator/unset-allowlist matrix + parser tests | ✓ VERIFIED | All subtests pass live in this run. |
| `internal/api/routes.go` | `/v1/system/presets` subtree gated by `requireOperator` | ✓ VERIFIED | Unchanged; confirmed by passing route-level tests. |
| `internal/api/api.go` | `Config.OperatorClientIDs` + `Server.operators` | ✓ VERIFIED | Unchanged. |
| `deploy/chart/octoconv/templates/configmap.yaml` | `OPERATOR_CLIENT_IDS` env key | ✓ VERIFIED | Unchanged from prior pass (not touched by 26-02). |
| `internal/presets/repo.go` | `Create` bumps version at `COALESCE(MAX(version),0)+1` across active+inactive rows; 23505→ErrAlreadyExists | ✓ VERIFIED (new) | Read directly: lines 89-130. `COALESCE(MAX` present, `23505` present, doc comments corrected. |
| `internal/presets/repo_test.go` | `TestCreateAfterDeactivateBumpsVersion` (system + user scope) | ✓ VERIFIED (new) | Read directly: lines 469-534. Substantive assertions (version bump, id change, active/total row counts), not a stub. Passes live against real Postgres. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| `internal/presets/repo.go` Create | presets table | `INSERT ... SELECT COALESCE(MAX(p.version),0)+1 ...` | ✓ WIRED | Confirmed by direct read + live test proving the second `Create` after `Deactivate` returns version 2, not an error. |
| `internal/presets/repo.go` Create error path | `presets.ErrAlreadyExists` | `pgconn.PgError.Code == "23505"` backstop | ✓ WIRED | Confirmed by direct read (lines 122-127); active-duplicate pre-check test (`TestCreateDuplicateReturnsErrAlreadyExists`) still passes live, proving the primary path; the 23505 backstop is a secondary defense for the race window (T-26-03, accepted-and-backstopped per plan, not independently exercised by a forced-race test — this is a reasonable, explicitly-scoped acceptance, not a gap). |
| `internal/api/system_presets_handlers.go` handleCreateSystemPreset | `presets.ErrAlreadyExists` | `errors.Is(err, presets.ErrAlreadyExists) → 409` | ✓ WIRED | Confirmed by direct read; end-to-end path from repo fix to REST response is intact. |
| `cmd/api/main.go` | `api.ParseOperatorClientIDs` | fail-loud `log.Fatalf` on startup | ✓ WIRED | Unchanged from prior pass; 26-02 did not touch `cmd/api/main.go`. |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Deactivate → re-create of the same system preset name, against the REAL repo (live Postgres) | `set -a && . ./.env && set +a && go test ./internal/presets/ -run TestCreateAfterDeactivateBumpsVersion -v` | Both `system_scope` and `user_scope` subtests PASS | ✓ PASS |
| Full `internal/presets` DB-backed suite (all 15 test functions) | `go test ./internal/presets/ -v` | All PASS, 1.050s | ✓ PASS |
| Full `internal/api` suite (operator gate + system-preset handler matrix) | `go test ./internal/api/ -v` | All PASS, 0.421s | ✓ PASS |
| Full repo-wide test suite, no regressions | `go test ./...` | Every package PASS or `[no test files]`; none FAIL | ✓ PASS |
| `go build ./...`, `go vet ./...`, `gofmt -l internal/presets internal/api cmd/api` | direct execution | All clean, no output | ✓ PASS |

### Probe Execution

No `scripts/*/tests/probe-*.sh` declared by 26-01-PLAN.md or 26-02-PLAN.md, and none found under `scripts/`. Consistent with prior verification pass. Step 7c: **SKIPPED (no probes declared for this phase)**.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| OPER-01 | 26-01-PLAN.md, 26-02-PLAN.md | system-scope presets managed via REST from OPERATOR_CLIENT_IDS allowlist (env-only, zero migrations); non-operator system-write → 404-no-leak | ✓ SATISFIED | Env-only gate, zero migrations, 404-no-leak, fail-closed, fail-loud all verified (unchanged). The previously-open "managed via REST" clause (deactivate→recreate) is now closed: `TestCreateAfterDeactivateBumpsVersion` passes live for both scopes; REST-layer 409 mapping confirmed. REQUIREMENTS.md still shows `[ ]` unchecked for OPER-01 pending this verification's sign-off — recommend the roadmap owner check it off now that this report is `passed`. |

No orphaned requirements: REQUIREMENTS.md's phase-26 rollup lists only OPER-01, matching both plans' frontmatter.

### Anti-Patterns Found

No new anti-patterns introduced by the gap-closure plan. `internal/presets/repo.go`'s CR-01 defect (previously the sole 🛑 Blocker) is now fixed. Carrying forward the non-blocking items from the prior verification pass, still valid and still tracked (out of scope for 26-02, which was explicitly limited to `internal/presets/repo.go` + its test file):

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/api/system_presets_handlers.go` | 97-103, 184 (approx, unchanged) | WR-01: `target_format` accepted with zero validation on system create/update | ⚠ Warning | An operator typo creates a globally-visible, resolvable-everywhere bad preset; only surfaces at job-submit time to an unrelated client. Non-blocking; tracked separately. |
| `internal/api/presets_handlers.go` (shared `validPresetName`) | ~97-99 (unchanged) | WR-02: preset name charset unrestricted (allows `/`, `%`, whitespace) | ⚠ Warning | Now that CR-01 is fixed, a badly-named system preset CAN be recreated with a corrected name via deactivate+recreate — this lowers WR-02's severity somewhat (no longer permanently unmanageable), but the charset gap itself is unchanged and still worth tracking. |
| `deploy/chart/octoconv/templates/configmap.yaml`, `docker-compose.yml` | configmap:42, compose api.environment (untouched) | WR-03: `OPERATOR_CLIENT_IDS` not passed through in `docker-compose.yml`'s `api` service | ⚠ Warning | Unchanged; the local compose stack still cannot receive the variable, meaning any future live-gate REST acceptance script extension remains blocked until fixed. |
| `internal/api/system_presets_handlers.go` | ~113-117, 193-197 (unchanged) | IN-01: non-atomic write-then-read-back can 500 or return a stale version under a race | ℹ️ Info | Mirrors the pre-existing user-scope pattern; not new. |
| `.env.example` | 22 (unchanged) | IN-02: inline comment on `OPERATOR_CLIENT_IDS=` not tolerated by its own parser | ℹ️ Info | Cosmetic/documentation-only risk. |

No `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` markers found in any file touched by 26-01 or 26-02.

### Human Verification Required

None. All findings are resolvable by code inspection and automated tests (now confirmed live against Postgres, not just code-traced); no visual, real-time, or external-service behavior is in scope for this phase.

### Gaps Summary

No gaps remain. The single blocking gap from the prior verification pass (CR-01: deactivate-then-recreate 500) is closed by 26-02's two commits (`2f23da5` RED test, `1a6728a` GREEN fix), verified here by direct code reading of both `internal/presets/repo.go` and `internal/presets/repo_test.go`, plus live execution against a running Postgres instance (`go test ./internal/presets/ -v`, `go test ./internal/api/ -v`, `go test ./...` — all green, zero regressions across the entire repository). WR-01/WR-02/WR-03/IN-01/IN-02 remain non-blocking, unchanged, and tracked separately as before; none of them gate this verification's `passed` status.

---

_Verified: 2026-07-14T11:25:39Z_
_Verifier: Claude (gsd-verifier)_
