---
phase: 26-operator-presets-rest
reviewed: 2026-07-14T10:55:49Z
depth: standard
files_reviewed: 8
files_reviewed_list:
  - internal/api/system_presets_handlers.go
  - internal/api/system_presets_handlers_test.go
  - internal/api/api.go
  - internal/api/routes.go
  - cmd/api/main.go
  - deploy/chart/octoconv/values.yaml
  - deploy/chart/octoconv/templates/configmap.yaml
  - .env.example
findings:
  critical: 1
  warning: 3
  info: 2
  total: 6
status: issues_found
---

# Phase 26: Code Review Report

**Reviewed:** 2026-07-14T10:55:49Z
**Depth:** standard
**Files Reviewed:** 8
**Status:** issues_found

## Summary

Reviewed the operator-only `/v1/system/presets` REST surface: the `OPERATOR_CLIENT_IDS` parse/gate, the five system-scope handlers, routing, `Server`/`Config` wiring, startup parsing in `cmd/api/main.go`, and the chart/env plumbing. The declared security invariants were verified against the code and against the shared helpers in `presets_handlers.go` and `internal/presets/repo.go`:

- **No-leak 404:** `requireOperator` uses the same `writeError(w, 404, noSuchPreset)` helper as a genuine preset miss — byte-identical body, same `Content-Type`, never 403. Chi runs the sub-router middleware even for unmatched methods inside the subtree, so a non-operator gets 404 on every method. Confirmed. No response body in any handler carries operator-ness, `id`, or `client_id` (DTO is `presetResponse`, which drops both).
- **Fail-closed empty allowlist:** `ParseOperatorClientIDs("")` returns an empty non-nil set; `NewServer` normalizes a nil `Config.OperatorClientIDs` to an empty map; membership check denies all. Confirmed by tests.
- **Fail-loud malformed allowlist:** any bad token aborts the whole parse; `cmd/api/main.go:92-95` `log.Fatalf`s. Confirmed.
- **Mass-assignment impossible:** `presetRequest` has no `scope`/`client_id` fields; system handlers hardcode `presets.ScopeSystem` + nil `ClientID`; the `presets_scope_owner_chk` DDL constraint backs this up. Confirmed.
- **Gate ordering:** `requireOperator` is registered only inside the `/v1/system/presets` sub-route, after `auth.Middleware` (`routes.go:37,55`). Its missing-context branch fails closed with the same 404. Confirmed.

`go vet` is clean and the full `internal/api` test suite passes. However, tracing the handlers into `presets.Repo` surfaced one reachable correctness defect (a deactivated system preset name becomes permanently dead and returns an opaque 500 on re-create) plus robustness gaps in input validation and deployment plumbing.

## Critical Issues

### CR-01: Deactivate-then-create of a system preset name always fails with 500 and the name is permanently unusable via the API

**File:** `internal/api/system_presets_handlers.go:97-111` (create), `:213` (deactivate); root cause `internal/presets/repo.go:89-110` with `internal/db/migrations/0001_init.sql:35`
**Issue:** `DELETE /v1/system/presets/{name}` soft-deactivates the row (comment: "no hard delete"), but a subsequent `POST /v1/system/presets` with the same name is broken:

1. `repo.Create`'s duplicate check only looks at **active** rows (`... AND is_active`), so it passes.
2. `repo.Create` then inserts at a hardcoded `const version = 1`.
3. The partial unique index `presets_system_uq ON presets (name, version) WHERE scope = 'system'` covers **inactive rows too**, so the insert collides with the deactivated version-1 row and fails with a unique violation.
4. The handler cannot match `presets.ErrAlreadyExists` (it's a raw pgx error), so the operator gets an opaque `500 "failed to create preset"`.

There is no recovery path: `PUT /v1/system/presets/{name}` also 404s because `repo.Update` requires an **active** row. Every system preset name that has ever been deactivated is dead forever short of manual SQL. This is an ordinary operator workflow (deactivate a bad preset, recreate it later) that the new endpoints expose for the first time on system scope, and it fails with incorrect behavior and a misleading status code. (The same latent trap exists on the user-scope path from Phase 20 via `presets_user_uq` — fixing the shared root cause fixes both.)

The same missing unique-violation mapping also means two racing `POST`s for the same new name return 500 instead of 409 (the `EXISTS`-then-`INSERT` in `repo.Create` is not atomic; the unique index is the real arbiter, but its violation is never mapped).

**Fix:** In `repo.Create`, insert at the next version instead of constant 1, and map unique violations to `ErrAlreadyExists`:
```go
// replace `const version = 1` + INSERT with:
err := r.pool.QueryRow(ctx,
    `INSERT INTO presets (name, version, scope, client_id, operation, target_format, options, description)
     SELECT $1, COALESCE(MAX(p.version), 0) + 1, $2, $3, $4, $5, $6, $7
     FROM presets p
     WHERE p.scope = $2 AND p.name = $1
       AND (p.client_id = $3 OR ($3::uuid IS NULL AND p.client_id IS NULL))
     RETURNING id, version`, ...)
// and in the error path:
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) && pgErr.Code == "23505" {
    return uuid.Nil, 0, ErrAlreadyExists
}
```
(Any equivalent approach works — e.g. keep `Create` but make it revive/bump like `Update` when only inactive rows exist. Whichever is chosen, add a handler-level test: create → deactivate → create must succeed, not 500.)

## Warnings

### WR-01: `target_format` accepted with zero validation on system create/update — an operator typo produces a globally visible, permanently unusable preset

**File:** `internal/api/system_presets_handlers.go:97-103` (create), `:184` (update)
**Issue:** Neither `handleCreateSystemPreset` nor `handleUpdateSystemPreset` validates `req.TargetFormat` at all — empty string, `"web p"`, or arbitrary garbage is stored verbatim (the DB column is nullable, no CHECK). Unlike a user-scope preset (blast radius: one client), a system preset appears in **every** client's merged `GET /v1/presets` view and is resolvable in job creation, where the bogus target only fails at job-submit time — surfacing the operator's mistake as a confusing 4xx to a different party. A `PUT` that simply omits `target_format` silently replaces a working value with `""` in the new version (bump-on-update means the working v(N) is deactivated in the same transaction).
**Fix:** In both handlers, reject an empty `req.TargetFormat` with 422, and validate it against the known-format surface the API already exposes via `/v1/formats` (e.g. `convert.NormalizeFormat` + a registry membership check), mirroring how `handleCreateJob` validates a client-supplied target.

### WR-02: Preset name charset is unrestricted — names containing `/` (and other non-URL-safe bytes) create system presets that can never be shown, updated, or deactivated via `/{name}` routes

**File:** `internal/api/system_presets_handlers.go:88` with `internal/api/presets_handlers.go:97-99` (`validPresetName`)
**Issue:** `validPresetName` only checks non-empty and ≤128 bytes. `POST /v1/system/presets` with `"name": "reports/thumb"` (or a name containing `%`, whitespace, control characters, raw newlines) succeeds — but the show/update/deactivate routes address presets via the `{name}` path segment, where a literal `/` splits into two segments and a percent-encoded `%2F` does not round-trip through chi's `URLParam` (chi routes on the raw path and does not decode captured params). The result is a system-scope row, visible to all clients, that is unmanageable through the REST API it was created by — combined with CR-01's dead-name trap, it is effectively unfixable without SQL. The deactivate handler also echoes this unvalidated name back in its response body (`system_presets_handlers.go:222`) with `writeJSON`'s `SetEscapeHTML(false)`.
**Fix:** Tighten `validPresetName` to a URL-safe charset, e.g.:
```go
var presetNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
func validPresetName(name string) bool { return presetNameRe.MatchString(name) }
```
The length check stays request-independent of DB state, so the no-leak property of the 400 is preserved.

### WR-03: `OPERATOR_CLIENT_IDS` cannot reach the compose API container — the phase's own deferred live gate (D-06) will not run as planned

**File:** `deploy/chart/octoconv/templates/configmap.yaml:42`, `.env.example:22`; gap in `docker-compose.yml` `api.environment` (lines 77-107, untouched by this phase)
**Issue:** The Helm chart and `.env.example` both expose `OPERATOR_CLIENT_IDS`, but the compose `api` service's inline `environment:` block was not given the variable — and compose does not pass arbitrary host env through. 26-CONTEXT D-06/D-07 designate the **compose stack** as this phase's live-gate bed ("operator's UUID into OPERATOR_CLIENT_IDS via compose env override or export"), but a plain `export` will never reach the container. Fail-closed means this is not a security hole — it means operators are silently impossible to enable in the canonical local deployment, and the still-open live gate documented in 26-01-SUMMARY will hit this wall.
**Fix:** Add a passthrough with a safe default to the `api` service in `docker-compose.yml`:
```yaml
      OPERATOR_CLIENT_IDS: ${OPERATOR_CLIENT_IDS:-}
```

## Info

### IN-01: Non-atomic create/update read-back can 500 after a successful write, or return a different version than the one written

**File:** `internal/api/system_presets_handlers.go:113-117` (create), `:193-197` (update)
**Issue:** Both write handlers perform the write, then issue a separate `Get` to build the response. A concurrent deactivate between the two turns a *successful* create/update into `500 "failed to load created preset"` (and `Get`'s `ErrNotFound` is not even matched there); a concurrent update makes the 201 body report a version the caller did not create. Mirrors the user-scope handlers, so consistency is understood — but `repo.Create`/`repo.Update` already return `(id, version)`, so the response could be built without the second round-trip.
**Fix:** Build the response DTO from the request fields plus the returned id/version (or have Create/Update return the full row), eliminating the read-back.

### IN-02: `.env.example` puts an inline comment on `OPERATOR_CLIENT_IDS`, but its parser — unlike every other numeric/duration env — does not tolerate inline comments

**File:** `.env.example:22`, `internal/api/system_presets_handlers.go:26-43`
**Issue:** Numeric/duration envs are deliberately read through `firstField` to "tolerate trailing inline comments / whitespace from .env files" (`cmd/api/main.go:170-187`) — proof that some load paths preserve them. `ParseOperatorClientIDs` gets the raw value: a populated line copied in the `.env.example` style (`OPERATOR_CLIENT_IDS=<uuid>   # note`) would fail-loud and crash the API at startup under any comment-preserving loader. Fail-loud is the safe direction (it can never silently shrink the operator set — do NOT apply `firstField` here, since it would truncate `"a, b"` at the first space), but the example file itself demonstrates the crash-inducing pattern.
**Fix:** Move the explanation to its own `#`-prefixed line above `OPERATOR_CLIENT_IDS=` in `.env.example`.

---

_Reviewed: 2026-07-14T10:55:49Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
