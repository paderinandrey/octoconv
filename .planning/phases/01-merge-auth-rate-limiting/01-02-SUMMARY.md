---
phase: 01-merge-auth-rate-limiting
plan: 02
subsystem: auth
tags: [chi, http-middleware, context, postgres, api-key-auth]

# Dependency graph
requires:
  - phase: 01-merge-auth-rate-limiting
    plan: "01"
    provides: "auth.HashKey/GenerateKey, clients.Repo.GetByKeyHash, API_KEY_SALT env var"
provides:
  - "internal/auth.ClientResolver / Resolver / NewResolver / ErrInvalidKey / Middleware: chi auth middleware enforcing ApiKey-scheme keys"
  - "internal/auth.WithClient / ClientFromContext: request-context client helpers"
  - "jobs.client_id threading: CreateParams.ClientID -> INSERT, Job.ClientID <- SELECT"
  - "/v1 route group requires a valid API key (401 hard cutover); /healthz stays public"
  - "cross-client job GET returns 404 identical to true-not-found (AUTH-03)"
  - "cmd/api wiring: clients.NewRepo + auth.NewResolver from API_KEY_SALT, fail-fast if unset"
affects: [rate-limiting, webhook-delivery, reconciler]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Consumer-owned narrow interface (auth.ClientResolver) so internal/api never imports internal/clients directly"
    - "Single unexported context key type (ctxKey{}) with two accessors (WithClient/ClientFromContext) — the only context.WithValue use in the codebase"
    - "Duplicated writeError JSON-error helper in internal/auth (not imported from internal/api) to avoid inverting the dependency direction, mirroring firstField/envInt duplication-per-binary convention"
    - "Hard-cutover auth: 401/500 always short-circuit before next.ServeHTTP; no warn-only or skip-auth path anywhere, including docker-compose dev config"

key-files:
  created:
    - internal/auth/auth.go
    - internal/auth/context.go
    - internal/auth/middleware.go
    - internal/auth/middleware_test.go
  modified:
    - internal/jobs/jobs.go
    - internal/jobs/repo.go
    - internal/api/api.go
    - internal/api/routes.go
    - internal/api/handlers.go
    - internal/api/handlers_test.go
    - cmd/api/main.go
    - docker-compose.yml
    - internal/clients/repo_test.go

key-decisions:
  - "internal/clients/repo_test.go converted from package clients to external package clients_test to break an import cycle: auth.go now imports internal/clients, and the Plan 01 test file (package clients) imported internal/auth for GenerateKey/HashKey. External test packages are the standard Go idiom for this exact situation."
  - "jobs.Get scans client_id via a nullable *uuid.UUID (not a bare uuid.UUID) because the column is `ON DELETE SET NULL` per 0001_init.sql — a client deletion can leave a legacy job with a null client_id, and the ownership check must not panic or misbehave on that row."

patterns-established:
  - "auth.Middleware is applied via r.Use() scoped to the /v1 sub-router only, never globally — /healthz's public reachability (D-09) is structural (outside the group), not an opt-out flag"

requirements-completed: [AUTH-01, AUTH-02, AUTH-03]

# Metrics
duration: 9min
completed: 2026-07-03
---

# Phase 01 Plan 02: Request-Path Auth Enforcement Summary

**chi middleware turning issued API keys into hard-cutover 401 enforcement on `/v1/*`, with `client_id` threaded through job creation and a 404-only (never 403) cross-client ownership guard on job reads.**

## Performance

- **Duration:** 9 min
- **Started:** 2026-07-03T02:49:11+03:00
- **Completed:** 2026-07-03T02:57:34+03:00
- **Tasks:** 3
- **Files modified:** 12 (9 planned + `internal/clients/repo_test.go` deviation)

## Accomplishments
- `internal/auth` resolver + middleware: `Authorization: ApiKey <key>` is hashed via `HashKey` and looked up through `clients.Repo.GetByKeyHash`; missing/malformed/unknown/revoked key → 401 before any handler runs (hard cutover, D-08), resolver-level errors (e.g. DB down) → 500, both short-circuiting before `next.ServeHTTP`
- Request-context plumbing (`WithClient`/`ClientFromContext`) is the codebase's first (and only) `context.WithValue` use, kept to exactly one key type
- `jobs.Job`/`CreateParams` now carry `ClientID`; `Create` binds it into the `jobs` INSERT and `Get` scans it back out (nullable-safe, since `client_id` is `ON DELETE SET NULL`)
- `/v1` route group requires `auth.Middleware(s.resolver)`; `/healthz` stays outside the group and reachable without a key (D-09)
- `handleCreateJob` records the authenticated client's ID; `handleGetJob` adds an ownership guard that returns the exact same `404 {"error":"job not found"}` for a cross-client job as for a truly-missing one — no 403, no distinguishing message (AUTH-03, closes the enumeration/IDOR leak)
- `cmd/api` fails fast if `API_KEY_SALT` is unset, wires `clients.NewRepo` + `auth.NewResolver` into `api.NewServer`; `docker-compose.yml`'s `api` service gets a clearly-fake dev salt (never an auth-skip toggle) so the real auth path is always exercised, even locally

## Task Commits

Each task was committed atomically:

1. **Task 1: internal/auth resolver, context helpers, and chi middleware** - `4feb07f` (test/RED) → `2f2f48c` (feat/GREEN)
2. **Task 2: Thread client_id through jobs + enforce auth and ownership in the API** - `343afe2` (feat)
3. **Task 3: Wire the resolver into cmd/api and docker-compose** - `f3fa109` (feat)

_Task 1 followed TDD: `middleware_test.go` committed first as a compile-failing RED commit (referenced `Middleware`/`ClientFromContext`/`ErrInvalidKey` before they existed), then `auth.go`/`context.go`/`middleware.go` implemented together to turn it GREEN. An additional `d4bf5be` (test) commit was added post-Task-3 to cover a verification requirement the plan's overall `<verification>` block called out (`/healthz` reachable with no auth header) that wasn't itemized in either task's `acceptance_criteria` list — see Deviations._

**Plan metadata:** (to be added by orchestrator after merge)

## Files Created/Modified
- `internal/auth/auth.go` - `ClientResolver` interface, `Resolver`, `NewResolver`, `ErrInvalidKey`; `ResolveClient` hashes the raw key and maps `clients.ErrNotFound` → `ErrInvalidKey`
- `internal/auth/context.go` - unexported `ctxKey{}` + `WithClient`/`ClientFromContext`
- `internal/auth/middleware.go` - `Middleware(resolver)`: `ApiKey` scheme parsing, 401/500 short-circuit, local `writeError` (duplicated, not imported from `internal/api`)
- `internal/auth/middleware_test.go` - pass-through, missing header, wrong scheme (3 cases), invalid key (401), resolver error (500); asserts `next` not called and JSON error bodies never echo the presented key
- `internal/jobs/jobs.go` - `Job.ClientID uuid.UUID`
- `internal/jobs/repo.go` - `CreateParams.ClientID`; Create INSERT and Get SELECT/Scan extended for `client_id` (nullable-safe)
- `internal/api/api.go` - `Server.resolver auth.ClientResolver`; `NewServer` takes it positionally before `Config`
- `internal/api/routes.go` - `r.Use(auth.Middleware(s.resolver))` scoped to the `/v1` group; `/healthz` unaffected
- `internal/api/handlers.go` - `handleCreateJob` sets `ClientID` from `auth.ClientFromContext`; `handleGetJob` adds the cross-client 404 ownership guard
- `internal/api/handlers_test.go` - `fakeResolver`, `authed()` helper, `Authorization` header on all requests; new tests for no-header 401, `ClientID` threading, cross-client 404, same-client 200, and `/healthz` no-auth-required
- `cmd/api/main.go` - fail-fast on missing `API_KEY_SALT`; constructs `clients.NewRepo` + `auth.NewResolver`, passes into `api.NewServer`
- `docker-compose.yml` - `api` service `environment` gets `API_KEY_SALT: "dev-only-change-me-in-real-deploys"`; `worker` service unchanged (no auth there)
- `internal/clients/repo_test.go` - converted to external test package `clients_test` (deviation, see below)

## Decisions Made
- Scanned `jobs.client_id` via `*uuid.UUID` rather than a bare `uuid.UUID` in `Get`, since the column is `ON DELETE SET NULL` (0001_init.sql) — a deleted client can leave a legacy job with a null `client_id`; defaulting to `uuid.Nil` in that case matches the existing nullable-column `deref()` convention used for the other `*string` columns in the same query, without introducing a new panic path.
- Kept `internal/auth`'s `writeError` as a small local duplicate rather than importing `internal/api`'s helper of the same shape, per the plan's explicit instruction — this preserves the one-way dependency direction (`api` → `auth`, never the reverse) and mirrors the existing `firstField`/`envInt` per-binary duplication convention already established in `cmd/api` and `cmd/worker`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Converted `internal/clients/repo_test.go` to an external test package to break an import cycle**
- **Found during:** Task 1 (internal/auth resolver, context, middleware)
- **Issue:** Plan 01 left `internal/clients/repo_test.go` as `package clients`, importing `internal/auth` for `GenerateKey`/`HashKey`. Task 1 makes `internal/auth/auth.go` import `internal/clients` (for `ClientResolver`'s `*clients.Client`/`clients.ErrNotFound`). That created a genuine cycle: `clients` (test) → `auth` → `clients`, which `go vet ./...` / `go test ./...` rejected outright (`go build ./...` alone didn't catch it since it excludes test files, but the plan's overall `<verification>` requires `go test ./...` to pass).
- **Fix:** Converted `internal/clients/repo_test.go` to the external `clients_test` package — the standard Go idiom for this exact situation — and qualified `NewRepo`/`Repo`/`ErrNotFound` with the `clients.` prefix. No behavioral change to the tests themselves.
- **Files modified:** `internal/clients/repo_test.go`
- **Verification:** `go build ./...`, `go vet ./...`, and `go test ./...` all pass cleanly across the whole module after the change.
- **Committed in:** `2f2f48c` (part of Task 1's GREEN commit)

**2. [Rule 2 - Missing Critical] Added a `/healthz` no-auth-required test**
- **Found during:** Post-Task-3 overall verification pass
- **Issue:** The plan's top-level `<verification>` block explicitly requires "a test hitting `/healthz` without a header returns 200," but this specific assertion wasn't listed in either Task 1's or Task 2's `acceptance_criteria`, so it was missed during Task 2's test updates. `/healthz` staying public (D-09) is load-bearing for the health-check-based `docker-compose` dependency ordering and any future k8s liveness probe.
- **Fix:** Added `TestHealthz_NoAuthRequired` to `internal/api/handlers_test.go`, asserting a bare `GET /healthz` (no `Authorization` header) returns 200 through the full `Routes()` chain (i.e., proves the middleware genuinely isn't applied at that path, not just that a unit test of the handler in isolation passes).
- **Files modified:** `internal/api/handlers_test.go`
- **Verification:** `go test ./internal/api/... -run TestHealthz -v` passes; full `go test ./...` still green.
- **Committed in:** `d4bf5be`

---

**Total deviations:** 2 auto-fixed (1 blocking, 1 missing-critical-verification)
**Impact on plan:** Both were necessary to satisfy the plan's own stated verification bar (`go test ./...` passing repo-wide, and the explicit `/healthz` no-auth assertion). No scope creep — no new files, endpoints, or behavior beyond what Task 1/2/3 already specified.

## Issues Encountered
None beyond the two deviations documented above.

## User Setup Required

None - no external service configuration required beyond what Plan 01 already documented (`API_KEY_SALT` in `.env`). Note: `cmd/api` now fails fast (`log.Fatalf`) if `API_KEY_SALT` is unset, so any environment that was previously running without it will need to set it before the API starts. `docker-compose.yml`'s `api` service already provides a fake dev value.

## Next Phase Readiness
- Every `/v1/*` request is now authenticated and client-scoped end-to-end (AUTH-01/02/03 closed); `/healthz` remains an explicit public exception (D-09).
- `internal/auth.ClientResolver` and the resolved-client request context (`auth.ClientFromContext`) are the natural attachment points for Phase 1's rate-limiting work (per-client limiter keyed on `client.ID`), which is the next plan in this phase per ROADMAP.md.
- `internal/clients/repo_test.go` and any other `DATABASE_URL`-gated integration tests were not run against a live Postgres in this sandboxed worktree session (same constraint noted in the 01-01 summary); they compile and vet cleanly and should be exercised against a real database (e.g., `docker compose up -d postgres`) before/during the next plan's work.

---
*Phase: 01-merge-auth-rate-limiting*
*Completed: 2026-07-03*
