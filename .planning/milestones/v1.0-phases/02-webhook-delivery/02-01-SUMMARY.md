---
phase: 02-webhook-delivery
plan: 01
subsystem: api
tags: [ssrf, callback_url, webhooks, go, netip, pgx]

# Dependency graph
requires:
  - phase: 01-merge-auth-rate-limiting
    provides: authenticated POST /v1/jobs with resolved client in request context
provides:
  - jobs.Job / jobs.CreateParams carry CallbackURL, persisted/read via existing nullable *string + deref() idiom
  - internal/api/callbackurl.go: validateCallbackURL + isBlockedIP, DNS-free SSRF/scheme guard (D-03)
  - POST /v1/jobs accepts an optional per-job callback_url form field (D-02), validated before any storage write
affects: [02-02, 02-03]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "SSRF guard: parse URL, resolve IP-literal or hostname, reject loopback/RFC1918/link-local/unspecified via net/netip predicates — validated once at creation, not re-checked per delivery"

key-files:
  created:
    - internal/api/callbackurl.go
    - internal/api/callbackurl_test.go
  modified:
    - internal/jobs/jobs.go
    - internal/jobs/repo.go
    - internal/api/handlers.go

key-decisions:
  - "callback_url stays per-job (CreateParams field), not per-client, per D-02"
  - "SSRF validation runs once at job creation only; no per-delivery re-resolution (accepted residual risk for internal-only clients, D-03)"
  - "http scheme allowed only when WEBHOOK_ALLOW_INSECURE_HTTP=true env var is set, to support local dev without opening SSRF risk in production default"

patterns-established:
  - "isBlockedIP(netip.Addr) as a pure, directly-unit-testable predicate separated from the URL-parsing/resolution shell (validateCallbackURL) — keeps DNS-free tests possible for the IP-range logic"

requirements-completed: [WEBHOOK-01]

# Metrics
duration: ~20min
completed: 2026-07-04
---

# Phase 2 Plan 1: callback_url intake + SSRF guard Summary

**POST /v1/jobs now accepts a per-job `callback_url`, rejecting SSRF targets (loopback/RFC1918/link-local/metadata) and non-https schemes with a fixed 400 before any storage write, and persists/reads it through Postgres via the existing nullable-column idiom.**

## Performance

- **Duration:** ~20 min
- **Tasks:** 3 completed
- **Files modified:** 5 (2 created, 3 modified)

## Accomplishments
- `jobs.Job` and `jobs.CreateParams` gained `CallbackURL`; `Repo.Create` writes it to the already-existing `jobs.callback_url` column and `Repo.Get` reads it back via the established `*string` + `deref()` nullable-column pattern.
- New `internal/api/callbackurl.go` provides `validateCallbackURL` (scheme + host validation, IP-literal or resolved-hostname check) and a pure `isBlockedIP` predicate covering loopback, RFC1918, link-local (which covers the 169.254.169.254 cloud metadata endpoint), link-local multicast, and unspecified addresses — proven by DNS-free unit tests.
- `handleCreateJob` reads the optional `callback_url` form field, validates it before `s.storage.Upload` (same validate-before-side-effects discipline as the format-pair check), and threads it into `jobs.CreateParams`. Invalid values return a fixed `400 "invalid callback_url"` with no internal detail leaked; an empty/omitted value leaves the existing create/poll behavior completely unchanged.

## Task Commits

Each task was committed atomically:

1. **Task 1: Surface callback_url through the jobs domain type and repository** - `51b2510` (feat)
2. **Task 2: SSRF-guarding callback_url validator (D-03)** - `e28a3a2` (feat, TDD: validator + DNS-free tests in one commit)
3. **Task 3: Wire callback_url intake into handleCreateJob (D-02)** - `ee3bf46` (feat)

_Note: SUMMARY.md commit follows this file per worktree execution convention._

## Files Created/Modified
- `internal/jobs/jobs.go` - Added `CallbackURL string` to `Job`
- `internal/jobs/repo.go` - Added `CallbackURL` to `CreateParams`; INSERT now writes `callback_url`; `Get` SELECT/Scan reads it back via `deref(cb)`
- `internal/api/callbackurl.go` - New: `validateCallbackURL(raw string) error` + `isBlockedIP(addr netip.Addr) bool`
- `internal/api/callbackurl_test.go` - New: DNS-free unit tests for both functions, including IP-literal URLs for the metadata endpoint
- `internal/api/handlers.go` - New `formFieldCallbackURL` const; `handleCreateJob` reads/validates/threads `callback_url` before the storage upload

## Decisions Made
None beyond what was already fixed in 02-CONTEXT.md (D-02, D-03) — plan executed exactly as specified, including the `WEBHOOK_ALLOW_INSECURE_HTTP` env var name for the dev-mode http-scheme escape hatch.

## Deviations from Plan

None - plan executed exactly as written. All three tasks' acceptance criteria (grep checks, `go build`, `go vet`, `go test -run 'CallbackURL|BlockedIP'`, and full `go test ./...`) were verified and pass.

## Issues Encountered

Initial `go test` invocation in this worktree accidentally ran against the parent repo checkout path instead of the worktree path (a leftover absolute `cd` from the plan's verify block, written before the worktree existed) and reported "no tests to run" since callbackurl_test.go doesn't exist on that branch. Re-ran from the correct worktree cwd and all tests passed as expected — no code issue, purely a path artifact of running in an isolated worktree.

## User Setup Required

None - no external service configuration required. (Note: `WEBHOOK_ALLOW_INSECURE_HTTP` is an optional dev-only env var with a safe default of unset/false; no `.env.example` entry was added in this plan since it's not required for normal operation.)

## Next Phase Readiness

- `jobs.callback_url` is now readable end-to-end (API → domain → Postgres), unblocking plan 02-03 (webhook delivery worker) which needs to read it back per job.
- The SSRF guard (`validateCallbackURL`) is a standalone, reusable function — no further wiring needed elsewhere in this phase.
- No blockers for the next plan in this phase (webhook signing/delivery, migration for `webhook_deliveries.dead_letter`).

---
*Phase: 02-webhook-delivery*
*Completed: 2026-07-04*
