---
phase: 01-merge-auth-rate-limiting
plan: 04
subsystem: jobs-integration-tests, api-rate-limiting
tags: [gap-closure, security, rate-limiting, testing]
dependency-graph:
  requires: [01-02, 01-03]
  provides: [BASE-01-verified, RATE-03-verified]
  affects: [internal/jobs, internal/api, internal/ratelimit]
tech-stack:
  added: []
  patterns:
    - "chi/middleware.ClientIPFromRemoteAddr for unforgeable pre-auth IP identity (no trusted reverse proxy in front of this service)"
    - "httprate custom KeyFunc reading middleware.GetClientIP with a net.SplitHostPort(RemoteAddr) fallback for direct/unit-test callers"
key-files:
  created:
    - internal/api/routes_test.go
  modified:
    - internal/jobs/repo_test.go
    - internal/api/routes.go
    - internal/ratelimit/ratelimit.go
decisions:
  - "Kept ClientIPFromRemoteAddr rather than pre-building XFF-trust config: this service has no reverse proxy today, and speculative trusted-proxy config would re-open the very spoofing hole being closed (documented as accepted residual risk in the plan's threat model, to be revisited if a proxy is ever introduced)."
  - "createTestClient inserts only the clients.name column (leaving api_key_hash NULL) to avoid coupling internal/jobs's tests to internal/clients or any hashing machinery."
metrics:
  duration_minutes: 15
  completed: 2026-07-04
---

# Phase 1 Plan 04: Gap Closure — Jobs FK Test Fix & Spoof-Proof IP Rate Limiter Summary

Fixed two verified-but-unfixed gaps from `01-VERIFICATION.md`: jobs integration tests violating the new `jobs_client_id_fkey` when run against a live Postgres, and the pre-auth `ratelimit.ByIP` guard being fully bypassable via spoofed `X-Forwarded-For` because of chi's deprecated `middleware.RealIP`.

## What Was Built

**Task 1 — Gap 1 (BASE-01 / ROADMAP SC1):** `internal/jobs/repo_test.go`'s `TestJobLifecycle` and `TestMarkFailed` previously called `Repo.Create` with a `CreateParams` that omitted `ClientID` (defaulting to `uuid.Nil`), which violates the `jobs_client_id_fkey` foreign key added when Plan 02 threaded `client_id` into the jobs INSERT. Added an unexported `createTestClient(t, r)` helper that inserts a minimal `clients` row (name only, leaving `api_key_hash` NULL to avoid any UNIQUE constraint) via the repo's existing unexported `pool` field, and wired its returned id into `CreateParams.ClientID` at both `Create` call sites. No new import of `internal/clients` was introduced.

**Task 2 — Gap 2 (RATE-03 / ROADMAP SC5 / 01-REVIEW.md CR-01):** `internal/api/routes.go` installed chi's deprecated, spoofable `middleware.RealIP` globally, which rewrites `r.RemoteAddr` from client-supplied `X-Forwarded-For`/`X-Real-IP`/`True-Client-IP`. `ratelimit.ByIP` keyed on `httprate.KeyByIP`, which read that mutated address — so varying the forwarded header per request let a single attacker evade the coarse flood guard entirely (reproduced live in `01-VERIFICATION.md`: 0/5 requests hit 429 despite a 2/min limit).

Fixed by:
- Replacing `r.Use(middleware.RealIP)` with `r.Use(middleware.ClientIPFromRemoteAddr)` in `Routes()`, which trusts only the raw TCP peer address.
- Adding an unexported `ipKey` func in `internal/ratelimit/ratelimit.go` that reads `middleware.GetClientIP(r.Context())`, falling back to `net.SplitHostPort(r.RemoteAddr)` (or the raw `RemoteAddr` on parse failure) when the middleware hasn't run — this keeps the existing `TestByIP_OverLimitReturns429` (which sets `RemoteAddr` directly with no middleware chain) passing unchanged.
- Pointing `ByIP` at `httprate.WithKeyFuncs(ipKey)` instead of `httprate.KeyByIP`.
- Adding `internal/api/routes_test.go` with `TestByIP_NotEvadedByForwardedForSpoofing`, which builds a real server via `NewServer(..., Config{IPRateLimitRPM: 2, ...})`, gets `srv.Routes()`, and sends 5 sequential requests sharing one `httptest` peer address but each carrying a distinct spoofed `X-Forwarded-For` — asserting requests 1-2 are not 429'd (401 from auth is expected/acceptable) and requests 3-5 are 429'd.

No new third-party dependency was introduced — `middleware.ClientIPFromRemoteAddr`/`GetClientIP` ship inside the already-vendored `github.com/go-chi/chi/v5` v5.3.0 middleware package; `go.mod`/`go.sum` are unchanged.

## Verification Evidence

- `go build ./...` and `go vet ./...` — exit 0.
- `DATABASE_URL=postgres://octo:octo-pass@localhost:5434/octo_db go test ./...` — **all packages `ok`**, including `internal/jobs` (previously failed with `jobs_client_id_fkey` / SQLSTATE 23503 against a live DB). Note: this repo's local `.env` remaps Postgres to host port `5434` (docker-compose maps `5434:5432`), not the `5433` in the plan's stock verify command; both point at the same live database.
- `go test ./internal/ratelimit/... ./internal/api/... -run 'ByIP|Spoof|Forwarded' -count=1 -v` — `TestByIP_NotEvadedByForwardedForSpoofing` and `TestByIP_OverLimitReturns429` both PASS.
- Structural gates: `grep -c 'middleware.RealIP' internal/api/routes.go` → 0; `grep -c 'ClientIPFromRemoteAddr' internal/api/routes.go` → 2; `grep -c 'GetClientIP(r.Context())' internal/ratelimit/ratelimit.go` → 1; `grep -c 'httprate.KeyByIP' internal/ratelimit/ratelimit.go` → 1 (only inside a doc comment explaining what was removed — no live usage remains).
- `grep -c 'ClientID:\s*createTestClient' internal/jobs/repo_test.go` → 2; `grep -c 'internal/clients' internal/jobs/repo_test.go` → 0.
- `git diff --stat go.mod go.sum` — empty (no dependency changes).

## Deviations from Plan

None — plan executed exactly as written. The plan's own acceptance-criteria grep for `ClientID:\s*createTestClient` required the client-id assignment to inline the `createTestClient(...)` call directly at the struct-literal site (`ClientID: createTestClient(t, r),`) rather than via an intermediate local variable; the implementation matches this exact form in both `TestJobLifecycle` and `TestMarkFailed`.

## Self-Check: PASSED

- FOUND: internal/api/routes_test.go
- FOUND: commit 00d68a5 (Task 1 — jobs FK fix)
- FOUND: commit 619004e (Task 2 — spoof-proof IP guard + regression test)
- All acceptance-criteria greps and both automated verification gates (build/vet, live-DB `go test ./...`, spoofing regression) passed as shown above.
