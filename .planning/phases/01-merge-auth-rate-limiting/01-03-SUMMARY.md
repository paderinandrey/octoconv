---
phase: 01-merge-auth-rate-limiting
plan: 03
subsystem: rate-limiting
tags: [chi, http-middleware, httprate, rate-limiting, dos-protection]

# Dependency graph
requires:
  - phase: 01-merge-auth-rate-limiting
    plan: "02"
    provides: "internal/auth.ClientFromContext / auth.Middleware / auth.WithClient; /v1 route group with auth enforced"
provides:
  - "internal/ratelimit.ByIP(rpm) / ratelimit.PerClient(rpm) — chi middleware, 429 + Retry-After on exceed"
  - "/v1 middleware chain: ratelimit.ByIP -> auth.Middleware -> ratelimit.PerClient (coarse pre-auth flood guard, then per-client fair quota keyed on client_id)"
  - "api.Config.IPRateLimitRPM / ClientRateLimitRPM with 60/120 defaults; RATE_LIMIT_IP_RPM / RATE_LIMIT_CLIENT_RPM env vars"
affects: [webhook-delivery, reconciler, observability]

# Tech tracking
tech-stack:
  added: ["github.com/go-chi/httprate v0.15.0"]
  patterns:
    - "In-process rate limiting via httprate.Limit with a pluggable KeyFunc; upgrade path to a Redis-backed LimitCounter noted in package doc, not implemented (single API instance today)"
    - "Rate-limit identity MUST be verified identity (client_id from auth.ClientFromContext), never IP/X-Forwarded-For, for any post-auth limiter"
    - "internal/ratelimit stays os.Getenv-free; thresholds arrive as already-parsed ints from cmd/api/main.go, mirroring internal/worker's ENGINE_TIMEOUT convention"

key-files:
  created:
    - internal/ratelimit/ratelimit.go
    - internal/ratelimit/ratelimit_test.go
  modified:
    - go.mod
    - go.sum
    - internal/api/api.go
    - internal/api/routes.go
    - cmd/api/main.go
    - docker-compose.yml
    - .env.example

key-decisions:
  - "Duplicated the ~8-line JSON error writer (limitHandler) locally in internal/ratelimit rather than importing internal/api's writeError, preserving the established one-way dependency direction (api/auth -> deeper packages, never the reverse) and the per-package duplication convention already used by internal/auth."
  - "Both limiters share a single fixed window (time.Minute) via an unexported `window` const rather than taking a window parameter, since RATE_LIMIT_IP_RPM/RATE_LIMIT_CLIENT_RPM are both specified as requests-per-minute in the plan and .env.example; keeps the exported API to a single rpm int per limiter."

patterns-established:
  - "Coarse pre-auth IP limiter always precedes auth.Middleware in any future route group needing DoS protection; per-client limiters always follow auth.Middleware and key on auth.ClientFromContext(...).ID, never r.RemoteAddr/X-Forwarded-For (PITFALLS Pitfall 9)."

requirements-completed: [RATE-01, RATE-02, RATE-03]

# Metrics
duration: 8min
completed: 2026-07-03
---

# Phase 01 Plan 03: Rate Limiting Summary

**In-process `go-chi/httprate` middleware (`internal/ratelimit`) with a coarse pre-auth IP flood guard and a per-client fair-use limiter keyed on the authenticated `client_id`, wired into `/v1` as `ByIP -> auth -> PerClient` with env-configurable 60/120 rpm defaults.**

## Performance

- **Duration:** 8 min
- **Started:** 2026-07-03T04:56:00+03:00
- **Completed:** 2026-07-03T04:58:48+03:00
- **Tasks:** 2
- **Files modified:** 8 (2 created, 6 modified)

## Accomplishments
- `github.com/go-chi/httprate` pinned at v0.15.0 (`go mod verify` clean, moved to a direct dependency by `go mod tidy`)
- `internal/ratelimit.ByIP(rpm)` — coarse pre-auth flood guard keyed on `httprate.KeyByIP`; needs no auth context, safe to run before any DB lookup (closes Anti-Pattern 3 / RATE-03)
- `internal/ratelimit.PerClient(rpm)` — per-client limiter whose `KeyFunc` reads `auth.ClientFromContext(r.Context()).ID`, never IP/X-Forwarded-For (closes PITFALLS Pitfall 9 / RATE-01); isolation between different clients verified by test
- Both limiters emit `429` + `Retry-After` (window length in whole seconds) + JSON `{"error": "rate limit exceeded"}` on exceed (RATE-02), via a shared unexported `limitHandler`
- `/v1` middleware chain in `internal/api/routes.go` is now exactly `ratelimit.ByIP -> auth.Middleware -> ratelimit.PerClient -> handlers`; `/healthz` stays outside the group, unaffected
- Thresholds are env-configurable: `RATE_LIMIT_IP_RPM` (default 60) and `RATE_LIMIT_CLIENT_RPM` (default 120), read in `cmd/api/main.go` via the existing `envInt64` helper and defaulted again in `api.NewServer` for any non-`cmd/api` caller (same zero-value-default idiom as `PresignTTL`/`MaxUploadBytes`); documented in `docker-compose.yml` and `.env.example`

## Task Commits

Each task was committed atomically:

1. **Task 1: Add httprate; build internal/ratelimit (ByIP + PerClient, 429 + Retry-After)** - `f9f1faf` (test/RED) -> `26fc50e` (feat/GREEN)
2. **Task 2: Wire ByIP -> auth -> PerClient into /v1; env-configurable thresholds** - `8702b9b` (feat)

**Plan metadata:** (to be added after this SUMMARY commit)

_Task 1 followed TDD: `ratelimit_test.go` was committed first as a compile-failing RED commit (referenced `PerClient`/`ByIP` before they existed — confirmed via `go test ./internal/ratelimit/...` failing to build), then `ratelimit.go` was implemented to turn it GREEN (`go test`, `go vet`, `go mod verify` all clean). No REFACTOR commit was needed — the GREEN implementation matched the plan's target shape._

## Files Created/Modified
- `internal/ratelimit/ratelimit.go` - `ByIP`, `PerClient`, shared `limitHandler` (429 + Retry-After + JSON body), package doc noting the Redis-backed upgrade path for horizontal scaling
- `internal/ratelimit/ratelimit_test.go` - per-client over-limit 429+Retry-After, per-client cross-client isolation, ByIP over-limit 429
- `go.mod` / `go.sum` - `github.com/go-chi/httprate v0.15.0` added as a direct dependency
- `internal/api/api.go` - `Server.ipRateRPM`/`clientRateRPM`; `Config.IPRateLimitRPM`/`ClientRateLimitRPM` with 60/120 zero-value defaults in `NewServer`
- `internal/api/routes.go` - `/v1` group middleware order: `ratelimit.ByIP(s.ipRateRPM)` -> `auth.Middleware(s.resolver)` -> `ratelimit.PerClient(s.clientRateRPM)`
- `cmd/api/main.go` - reads `RATE_LIMIT_IP_RPM`/`RATE_LIMIT_CLIENT_RPM` via `envInt64`, passes into `api.Config`
- `docker-compose.yml` - `api` service environment gets `RATE_LIMIT_IP_RPM: "60"` / `RATE_LIMIT_CLIENT_RPM: "120"`
- `.env.example` - documents both vars with inline-comment convention

## Decisions Made
- Kept `internal/ratelimit`'s 429 JSON-error writer as a small local duplicate of `internal/api`'s `writeError` shape rather than importing it, per the plan's explicit instruction — preserves the one-way dependency direction and matches the `internal/auth` precedent from Plan 02.
- Used a single unexported `window = time.Minute` constant shared by both limiters instead of a parameter, since both env vars are specified in requests-per-minute; keeps `ByIP`/`PerClient` signatures to a single `rpm int` argument as the plan's interface contract specifies.

## Deviations from Plan

None - plan executed exactly as written. Both tasks' acceptance criteria were verified directly (grep checks for `go.mod` pin, exported function names, `ClientFromContext` reference, absence of `RemoteAddr`/`X-Forwarded-For`/`os.Getenv` in `internal/ratelimit`, middleware source order in `routes.go`, env var presence in `cmd/api/main.go`/`docker-compose.yml`/`.env.example`) and all passed without needing fixes.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. The new `RATE_LIMIT_IP_RPM`/`RATE_LIMIT_CLIENT_RPM` env vars have safe defaults (60/120) baked into both `cmd/api/main.go` and `api.NewServer`, so existing `.env` files that predate this plan continue to work unchanged; `docker-compose.yml`'s `api` service already sets them explicitly for local dev.

## Next Phase Readiness
- RATE-01/02/03 closed: `/v1` is protected end-to-end by a coarse pre-auth IP flood guard and a per-client fair-use quota keyed on verified `client_id`, both returning 429 + `Retry-After` on exceed.
- Phase 1 (Merge, Auth & Rate Limiting) is now fully implemented across all 3 waves (merge, auth, rate limiting); ready for `/gsd:transition` to close the phase and move to Phase 2 (Webhook Delivery) per ROADMAP.md.
- `internal/ratelimit`'s package doc explicitly calls out the Redis-backed `LimitCounter` upgrade path (`WithLimitCounter`) for whenever the API is horizontally scaled — relevant context for the KEDA/Kubernetes future-scope note in PROJECT.md, but no action needed now.
- Integration tests gated on a live `DATABASE_URL` (e.g. `internal/clients`) were not re-run against Postgres in this sandboxed worktree session, consistent with Plans 01/02; `go build`, `go vet`, and `go test ./...` (unit-level) are all green.

---
*Phase: 01-merge-auth-rate-limiting*
*Completed: 2026-07-03*
