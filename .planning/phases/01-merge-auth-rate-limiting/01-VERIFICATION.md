---
phase: 01-merge-auth-rate-limiting
verified: 2026-07-04T17:40:00Z
status: passed
score: 12/12 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 10/12
  gaps_closed:
    - "main runs the full existing image-conversion vertical slice (merged from feat/scaffold-and-infra); build and existing tests pass [BASE-01 / ROADMAP SC1]"
    - "A coarse pre-auth IP limit throttles flood traffic before the auth middleware / DB lookup runs [RATE-03 / ROADMAP SC5]"
  gaps_remaining: []
  regressions: []
---

# Phase 1: Merge, Auth & Rate Limiting — Verification Report

**Phase Goal:** The working image-conversion vertical slice is on `main`, and every API request must present a valid, client-scoped API key — unauthenticated, invalid, or excessive traffic is rejected before it can affect production.
**Verified:** 2026-07-04T17:40:00Z
**Status:** passed
**Re-verification:** Yes — after gap closure (plan 01-04)

**Note on phase mode:** Same note carried from the initial pass — ROADMAP.md marks this phase `Mode: mvp`, but the phase goal text is not formatted as a User Story. Standard (non-MVP) goal-backward verification was applied, consistent with the initial pass, since a complete goal-backward contract already exists via PLAN frontmatter, ROADMAP Success Criteria, and CONTEXT/REVIEW artifacts.

## Re-verification Summary

Plan 01-04 (gap closure) targeted the exact two truths that failed in the initial pass. Both were re-reproduced independently in this session (not taken on the executor's or reviewer's word):

1. **Gap 1 (BASE-01 / SC1 — jobs FK violation):** Ran `DATABASE_URL=postgres://octo:octo-pass@localhost:5434/octo_db go test ./... -count=1` myself against the live `octoconv-db` container. All packages `ok`, including `internal/jobs`. Ran `TestJobLifecycle`/`TestMarkFailed`/`TestGetNotFound` individually with `-v`: all three actually execute (not skipped) and PASS — no `jobs_client_id_fkey`/23503 error. Confirmed `internal/jobs/repo_test.go` now calls `createTestClient(t, r)` and threads a real client id into `CreateParams.ClientID` at both `Create` sites (`grep -c 'ClientID:\s*createTestClient'` → 2, no `internal/clients` import introduced).

2. **Gap 2 (RATE-03 / SC5 — spoofable IP limiter):** Confirmed `internal/api/routes.go` no longer installs `middleware.RealIP` (0 occurrences) and now installs `middleware.ClientIPFromRemoteAddr` on the global chain, ahead of `ratelimit.ByIP` on `/v1`. Confirmed `internal/ratelimit/ratelimit.go`'s `ByIP` now uses a custom `ipKey` reading `middleware.GetClientIP(r.Context())` (falling back to `net.SplitHostPort(r.RemoteAddr)`, never a client header) instead of `httprate.KeyByIP`. Ran the new `TestByIP_NotEvadedByForwardedForSpoofing` myself — passes (requests 1-2 get 401, requests 3-5 get 429, despite 5 distinct spoofed `X-Forwarded-For` values sharing one `RemoteAddr`). **Went further than the plan's own test** and re-ran the exact original live-attack reproduction from the first verification pass: started a real `cmd/api` process (`RATE_LIMIT_IP_RPM=2`) and sent 5 real `curl` requests over a real TCP connection, each with a distinct spoofed `X-Forwarded-For` header:
   ```
   req1 (spoofed XFF=10.9.8.1): 401
   req2 (spoofed XFF=10.9.8.2): 401
   req3 (spoofed XFF=10.9.8.3): 429
   req4 (spoofed XFF=10.9.8.4): 429
   req5 (spoofed XFF=10.9.8.5): 429
   ```
   This is the inverse of the original repro (which got 0/5 429s); now 3/5 are correctly throttled — the spoofing bypass is closed.

**Regression check on the 10 previously-passed truths:** Full `go test ./...` (live DB) passes for every package (`internal/api`, `internal/auth`, `internal/clients`, `internal/convert`, `internal/jobs`, `internal/queue`, `internal/ratelimit`, `internal/storage`). Additionally re-ran live HTTP checks against a fresh `cmd/api` process with default rate limits: no-key request → 401, invalid-key request → 401, `/healthz` → 200 — matching the initial pass exactly, no regression introduced by the gap-closure changes (only `internal/jobs/repo_test.go`, `internal/api/routes.go`, `internal/ratelimit/ratelimit.go`, and new `internal/api/routes_test.go` were touched; no other production files changed).

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | [BASE-01/SC1] `main` runs the full vertical slice; `go build ./...` and `go test ./...` pass | ✓ VERIFIED | Re-run live: `go build ./...` exit 0, `go vet ./...` exit 0, `DATABASE_URL=...5434... go test ./... -count=1` — all packages `ok`, including `internal/jobs` (`TestJobLifecycle`, `TestMarkFailed`, `TestGetNotFound` all PASS, verbose-confirmed not skipped) |
| 2 | [D-03] Operator can create a client via CLI, raw key shown exactly once | ✓ VERIFIED (regression check — unchanged since initial pass) | `cmd/manage-clients/main.go` unmodified by 01-04; full test suite still green |
| 3 | [D-07/AUTH-04] Raw key never stored/logged; only salted SHA-256 digest persisted | ✓ VERIFIED (regression check) | `internal/auth/hash.go` unmodified; `internal/auth` package tests pass live |
| 4 | [D-05/AUTH-05] Operator can add a second active key and revoke either slot independently | ✓ VERIFIED (regression check) | `internal/clients` unmodified; `internal/clients` package tests pass live |
| 5 | [D-08/AUTH-02/SC2] Missing/malformed/invalid/revoked key → 401 before any handler runs | ✓ VERIFIED | Re-confirmed live against a freshly started `cmd/api`: no-header → 401, bad key → 401; `internal/auth` tests pass |
| 6 | [AUTH-01] Valid key reaches handler with resolved client identity; job creation records client_id | ✓ VERIFIED (regression check) | `internal/api/handlers.go` unmodified by 01-04; `internal/api` tests pass live |
| 7 | [D-09] `/healthz` reachable without an API key | ✓ VERIFIED | Re-confirmed live: `GET /healthz` no header → 200 |
| 8 | [AUTH-03/SC3] Cross-client job lookup → 404, byte-identical to true-not-found, never 403 | ✓ VERIFIED (regression check) | `internal/api/handlers.go` unmodified; `internal/api` test suite (incl. `TestGetJob_CrossClient_NotFound`/`TestGetJob_NotFound`) passes live |
| 9 | [RATE-01] Per-client limiter keyed on `client_id`, not IP | ✓ VERIFIED (regression check) | `clientKey`/`PerClient` untouched by 01-04 (plan explicitly preserved them); `internal/ratelimit` tests pass live |
| 10 | [RATE-02/SC5a] Client exceeding per-client rate → 429 + `Retry-After` | ✓ VERIFIED (regression check) | `limitHandler` untouched; `internal/ratelimit` tests pass live |
| 11 | [RATE-03/SC5b] Coarse pre-auth IP limit throttles flood traffic before auth/DB lookup, and cannot be evaded by spoofed headers | ✓ VERIFIED | **Gap closed.** `middleware.RealIP` fully removed (0 occurrences); `ClientIPFromRemoteAddr` + `ipKey`/`GetClientIP` wired. Re-reproduced live via real `curl` requests over a real TCP connection with distinct spoofed `X-Forwarded-For` per request against `RATE_LIMIT_IP_RPM=2`: 3/5 requests correctly 429'd (previously 0/5). New regression test `TestByIP_NotEvadedByForwardedForSpoofing` passes |
| 12 | Rate-limit thresholds configurable via env vars, conservative defaults | ✓ VERIFIED (regression check) | `.env.example`/`docker-compose.yml`/`cmd/api/main.go` env parsing unmodified by 01-04 |

**Score:** 12/12 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/jobs/repo_test.go` | Integration tests satisfy `jobs_client_id_fkey` via a real client row | ✓ VERIFIED | `createTestClient(t, r)` inserts a minimal `clients` row via the repo's unexported pool; `ClientID: createTestClient(t, r)` used at both `Create` call sites; no `internal/clients` import |
| `internal/api/routes.go` | Pre-auth IP identity from raw TCP peer, not spoofable headers | ✓ VERIFIED | `r.Use(middleware.ClientIPFromRemoteAddr)` replaces `r.Use(middleware.RealIP)`; doc comment updated to explain the trust boundary |
| `internal/ratelimit/ratelimit.go` | `ByIP` keys on unforgeable client IP | ✓ VERIFIED (was ⚠️ PARTIAL) | `ByIP` now uses `httprate.WithKeyFuncs(ipKey)`; `ipKey` reads `middleware.GetClientIP`, falls back to `net.SplitHostPort(RemoteAddr)`; `httprate.KeyByIP` no longer referenced as an active key func |
| `internal/api/routes_test.go` | Regression test proving the /v1 coarse limiter holds under XFF spoofing through the real router | ✓ VERIFIED | New file, `package api`, `TestByIP_NotEvadedByForwardedForSpoofing` exercises `srv.Routes()` end-to-end; PASS |
| (all artifacts from initial pass, unmodified by 01-04) | — | ✓ VERIFIED (regression check) | `internal/db/migrations/*.sql`, `internal/auth/*.go`, `internal/clients/repo.go`, `cmd/manage-clients/main.go` unchanged since initial pass; full test suite green |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/api/routes.go` | `internal/ratelimit.ByIP` + `internal/ratelimit.PerClient` | ordering: ByIP → auth → PerClient, ByIP keyed on unforgeable peer IP | ✓ WIRED (gap closed) | Source order confirmed; live curl spoofing test now correctly throttles (3/5 429), closing the previously defeated guard |
| `internal/jobs/repo.go` | `jobs.client_id` column | insert + select client_id | ✓ WIRED (gap closed) | Production path unaffected (always non-nil via auth middleware); test path now also satisfies the FK via `createTestClient` |
| (all other links from initial pass) | — | — | ✓ WIRED (regression check) | Unmodified files; full suite green, live auth/healthz/cross-client checks re-confirmed |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|---------------------|--------|
| `internal/ratelimit/ratelimit.go` `ByIP` key | IP identity | `middleware.GetClientIP(r.Context())`, set by `ClientIPFromRemoteAddr` from the real TCP peer address; header-based fallback removed | Yes — live-confirmed: spoofed `X-Forwarded-For` no longer changes the bucket key; real requests from one peer correctly accumulate in one bucket | FLOWING (was STATIC/SPOOFABLE) |
| `internal/clients/repo.go` `GetByKeyHash` | `client` | Postgres `clients` table | Yes (regression check, unchanged) | FLOWING |
| `internal/api/handlers.go` `handleGetJob` | `job` | Postgres `jobs` table | Yes (regression check, unchanged) | FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `go build ./...` | `go build ./...` | exit 0 | PASS |
| `go vet ./...` | `go vet ./...` | exit 0, no output | PASS |
| `go test ./...` (live DB) | `DATABASE_URL=postgres://octo:octo-pass@localhost:5434/octo_db go test ./... -count=1` | all packages `ok`, including `internal/jobs` | PASS (gap closed — was FAIL) |
| `TestJobLifecycle`/`TestMarkFailed`/`TestGetNotFound` verbose | `go test ./internal/jobs/... -run '...' -v` | all 3 PASS, none skipped | PASS |
| `TestByIP_NotEvadedByForwardedForSpoofing` | `go test ./internal/api/... -run Spoof -v` | requests 1-2 not 429, 3-5 are 429 | PASS (gap closed) |
| `TestByIP_OverLimitReturns429` (pre-existing) | `go test ./internal/ratelimit/... -run ByIP -v` | PASS | PASS (no regression) |
| Live real-process spoofing repro (independent of the plan's own test) | 5x `curl` w/ distinct `X-Forwarded-For`, `RATE_LIMIT_IP_RPM=2`, real `cmd/api` process | 3/5 → 429 (was 0/5 in initial pass) | PASS |
| No-key request to `/v1/jobs/{id}` | `curl` (no header) against fresh `cmd/api` | 401 | PASS (no regression) |
| Invalid key | `curl -H "Authorization: ApiKey bogus"` | 401 | PASS (no regression) |
| `/healthz` without key | `curl /healthz` | 200 | PASS (no regression) |
| Structural gates | `grep -c 'middleware.RealIP' internal/api/routes.go` → 0; `grep -c 'httprate.KeyByIP' internal/ratelimit/ratelimit.go` (active use) → 0 | as expected | PASS |

### Probe Execution

No `scripts/*/tests/probe-*.sh` files exist in this repository and none are referenced by this phase's plans. SKIPPED (no probes defined for this project).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|--------------|--------|----------|
| BASE-01 | 01-01, 01-04 (gap closure) | Baseline vertical slice merged, build/tests pass | ✓ SATISFIED | `go test ./...` now passes live end-to-end — gap closed |
| AUTH-01 | 01-02 | Client authenticates via API key tied to `clients` | ✓ SATISFIED | Unchanged since initial pass; regression-checked |
| AUTH-02 | 01-02 | Missing/invalid/revoked key → 401 | ✓ SATISFIED | Unchanged since initial pass; re-confirmed live |
| AUTH-03 | 01-02 | Cross-client job → 404, not 403 | ✓ SATISFIED | Unchanged since initial pass; regression-checked |
| AUTH-04 | 01-01 | Keys stored only as salted SHA-256 hashes | ✓ SATISFIED | Unchanged since initial pass; regression-checked |
| AUTH-05 | 01-01 | Two simultaneously active keys per client | ✓ SATISFIED | Unchanged since initial pass; regression-checked |
| RATE-01 | 01-03 | Per-client token bucket keyed on client_id | ✓ SATISFIED | Unchanged since initial pass; regression-checked |
| RATE-02 | 01-03 | 429 + Retry-After on exceed | ✓ SATISFIED | Unchanged since initial pass; regression-checked |
| RATE-03 | 01-03, 01-04 (gap closure) | Coarse pre-auth IP limit protects against flood before DB | ✓ SATISFIED | Gap closed — spoofing bypass eliminated, re-reproduced live |

All 9 requirement IDs declared across this phase's plans (`01-01`, `01-02`, `01-03`, `01-04`) are accounted for and SATISFIED. No orphaned requirements.

**Note (carried from initial pass, still unresolved — documentation-sync only, non-functional):** `.planning/REQUIREMENTS.md`'s checkbox/traceability table still shows `AUTH-01..05` as unchecked/"Pending" (only `BASE-01` and `RATE-01..03` are marked `[x]`/"Complete"). Plan 01-04 was scoped to gap closure only and did not touch this file, so this is expected to remain until phase close updates it. The code evidence above shows AUTH-01..05 are implemented and passing — this is a tracking-doc gap, not a functional one.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/jobs/repo_test.go` | ~33-41 (`createTestClient`) | No `t.Cleanup` deleting the inserted `clients` row after each test run | ⚠️ Warning | Test-only hygiene issue (01-REVIEW-gap-closure.md WR-01, not independently required to block this phase). Re-confirmed live: `SELECT count(*) FROM clients WHERE name='jobs-test-client'` now returns 16 (was 12 at gap-closure review time, growing with each test run). Does not affect production behavior — recommend fixing before this pattern is reused elsewhere, but does not block phase-goal achievement |
| `internal/ratelimit/ratelimit.go` | 80-83 (`ipKey` fallback) | `net.SplitHostPort` failure path returns raw `RemoteAddr` verbatim (unreachable in production; real `net/http` always populates `host:port`) | ℹ️ Info | Cosmetic edge case, not exercised by any test; carried from 01-REVIEW-gap-closure.md IN-01, no fix required |
| `internal/api/handlers.go` | 85, 147 | `client, _ := auth.ClientFromContext(ctx)` discards `ok` | ⚠️ Warning | Carried unchanged from initial pass (01-REVIEW.md WR-01); not touched by 01-04; latent nil-pointer risk only if a future routing change breaks the auth invariant |
| `internal/api/handlers.go` | 79-108 | Uploaded S3 object not cleaned up if `s.repo.Create` fails after upload | ⚠️ Warning | Carried unchanged from initial pass (01-REVIEW.md WR-02); not in scope for this phase's gap closure |
| `internal/api/api.go` | 64-69 | `RATE_LIMIT_*_RPM=0` silently treated as "unset"/default | ⚠️ Warning | Carried unchanged from initial pass (01-REVIEW.md WR-03); config footgun, not a functional gap |

No `TODO`/`FIXME`/`XXX`/`TBD`/`PLACEHOLDER` markers found in any file touched by plan 01-04 (`internal/jobs/repo_test.go`, `internal/api/routes.go`, `internal/ratelimit/ratelimit.go`, `internal/api/routes_test.go`).

None of the above anti-patterns are blockers: none involve unresolved debt markers, and none prevent the phase goal (every request presenting a valid client-scoped key; unauthenticated/invalid/excessive traffic rejected before affecting production) from being true today.

### Human Verification Required

None. Both previously-open gaps were objectively reproduced and closed via live tests and independent live HTTP requests (including a real `cmd/api` process hit with real `curl` traffic, not just the plan's own Go test). No item in this phase requires subjective/visual human judgment.

### Gaps Summary

No gaps remain. Both truths that FAILED in the initial verification pass are now VERIFIED, confirmed via independent reproduction in this session (not by trusting SUMMARY.md or the code-review report):

1. **BASE-01 / SC1** — `go test ./...` now passes against a live Postgres. `internal/jobs/repo_test.go`'s `TestJobLifecycle`/`TestMarkFailed` now attribute jobs to a real `clients` row via `createTestClient`, satisfying `jobs_client_id_fkey`.
2. **RATE-03 / SC5** — The coarse pre-auth IP flood guard is no longer bypassable by spoofed `X-Forwarded-For`. `middleware.RealIP` was fully removed and replaced with `middleware.ClientIPFromRemoteAddr`; `ByIP`'s key func now reads the unforgeable peer IP. Re-verified via a fresh live-process `curl` attack reproduction distinct from the plan's own test, confirming the fix generalizes beyond the specific test harness.

No regressions were introduced in the 10 previously-verified truths — the gap-closure plan touched only 4 files (`internal/jobs/repo_test.go`, `internal/api/routes.go`, `internal/ratelimit/ratelimit.go`, new `internal/api/routes_test.go`), and the full test suite plus targeted live HTTP checks (401 on missing/invalid key, 200 on `/healthz`, cross-client 404 collapse) all still hold.

Two non-blocking warnings remain open from the gap-closure review (WR-01: test client rows not cleaned up — test hygiene only; IN-01: unreachable fallback edge case) plus three carried-over warnings from the initial review (WR-01/WR-02/WR-03 in 01-REVIEW.md — nil-deref latent risk, S3 orphan cleanup, `RPM=0` footgun). None are blockers; none were in scope for this gap-closure plan. Recommend tracking them for a future hardening pass but they do not block phase completion.

The `.planning/REQUIREMENTS.md` traceability table still shows `AUTH-01..05` as "Pending" (documentation-sync gap only, carried from the initial pass) — recommend updating at phase close since the code evidence confirms all are implemented and passing.

---

_Verified: 2026-07-04T17:40:00Z_
_Verifier: Claude (gsd-verifier)_
