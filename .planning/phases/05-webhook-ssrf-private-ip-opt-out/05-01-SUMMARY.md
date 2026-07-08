---
phase: 05-webhook-ssrf-private-ip-opt-out
plan: 01
subsystem: api
tags: [ssrf, webhook, security, env-config, go]

# Dependency graph
requires:
  - phase: 02-webhook-delivery
    provides: "validateCallbackURL / isBlockedIP SSRF guard and the WEBHOOK_ALLOW_INSECURE_HTTP inline-env-read idiom to mirror"
provides:
  - "WEBHOOK_ALLOW_PRIVATE_IPS operator opt-in that narrowly relaxes the RFC1918 check in isBlockedIP"
  - "Startup-visible warning (⚠️) when the flag is enabled"
  - ".env.example documentation for both WEBHOOK_ALLOW_PRIVATE_IPS and the previously-undocumented WEBHOOK_ALLOW_INSECURE_HTTP"
affects: [webhook-delivery, ssrf-guard, deployment-config]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Inline os.Getenv boolean-flag read at point of use (no config struct), matching the existing WEBHOOK_ALLOW_INSECURE_HTTP idiom"
    - "cmd/*/main.go-only logging preserved — internal/api/callbackurl.go still never logs"

key-files:
  created: []
  modified:
    - internal/api/callbackurl.go
    - internal/api/callbackurl_test.go
    - cmd/api/main.go
    - .env.example

key-decisions:
  - "D-01: only addr.IsPrivate() is gated on WEBHOOK_ALLOW_PRIVATE_IPS; loopback/link-local (incl. 169.254.169.254)/unspecified remain unconditional hard-blocks"
  - "D-02: default is disabled (unset/false) — safe by default, explicit opt-in"
  - "D-03: single global operator-level env var, no config struct, no per-client override"
  - "D-04: cmd/api/main.go emits a one-time ⚠️-prefixed log.Printf warning at startup when the flag is true"

patterns-established:
  - "Env-gated boolean SSRF/security flags are read inline with os.Getenv(...) == \"true\", never threaded through a config struct or new parameter"

requirements-completed: [WEBHOOK-06]

# Metrics
duration: 6min
completed: 2026-07-08
---

# Phase 5 Plan 1: Webhook SSRF Private-IP Opt-Out Summary

**Added `WEBHOOK_ALLOW_PRIVATE_IPS` operator opt-in that narrowly relaxes only the RFC1918 check inside `isBlockedIP`, with a startup warning and both-sides test coverage, while loopback/link-local/unspecified stay hard-blocked.**

## Performance

- **Duration:** ~6 min (test commit 05:25:30 → final feat commit 05:26:43)
- **Started:** 2026-07-08T05:25:30+03:00 (RED commit)
- **Completed:** 2026-07-08T05:26:43+03:00 (final GREEN commit)
- **Tasks:** 2 completed
- **Files modified:** 4

## Accomplishments
- `isBlockedIP` now reads `WEBHOOK_ALLOW_PRIVATE_IPS` inline and skips only the `addr.IsPrivate()` term when true; loopback, link-local (incl. `169.254.169.254`), and unspecified remain unconditional
- Added explicit flag-on and flag-off test coverage (`TestIsBlockedIPAllowPrivate`, `TestValidateCallbackURLAllowPrivate`), closing the IN-01-style gap called out in CONTEXT.md (no test previously exercised `t.Setenv` for either webhook opt-out flag)
- `cmd/api/main.go` emits a one-time `⚠️`-prefixed startup warning when the flag is enabled, placed before `api.NewServer(...)`
- `.env.example` now documents both `WEBHOOK_ALLOW_PRIVATE_IPS=false` and the previously-undocumented `WEBHOOK_ALLOW_INSECURE_HTTP=false` (drive-by fix, planned in PATTERNS.md as option (a))

## Task Commits

Each task was committed atomically, following the TDD RED/GREEN cycle for Task 1:

1. **Task 1 (RED): add failing coverage for WEBHOOK_ALLOW_PRIVATE_IPS opt-out** - `e4047f5` (test)
2. **Task 1 (GREEN): gate RFC1918 SSRF block on WEBHOOK_ALLOW_PRIVATE_IPS** - `7bfe27d` (feat)
3. **Task 2: add startup visibility warning (D-04) and document flags** - `fef7193` (feat)

_No REFACTOR commit was needed — the GREEN implementation was already minimal and clean._

## Files Created/Modified
- `internal/api/callbackurl.go` - `isBlockedIP` gates `addr.IsPrivate()` on `os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true"`; doc comment updated to describe conditional vs. unconditional blocks
- `internal/api/callbackurl_test.go` - added `TestIsBlockedIPAllowPrivate` (flag-off and flag-on subtests) and `TestValidateCallbackURLAllowPrivate` (RFC1918 allowed, http-scheme/invalid-URL/loopback still rejected)
- `cmd/api/main.go` - one-time `⚠️` startup warning when `WEBHOOK_ALLOW_PRIVATE_IPS=true`, placed near the `salt`/`clientRepo`/`resolver` block
- `.env.example` - documented `WEBHOOK_ALLOW_PRIVATE_IPS=false` and `WEBHOOK_ALLOW_INSECURE_HTTP=false` under the `# API` section

## Decisions Made
- Followed PATTERNS.md option (a): documented both `WEBHOOK_ALLOW_PRIVATE_IPS` and the pre-existing but undocumented `WEBHOOK_ALLOW_INSECURE_HTTP` together in `.env.example`, closing a real doc gap while already editing this exact section (explicitly sanctioned as a drive-by fix in the plan's Task 2 action).
- No new architecture, no signature changes, no config struct — matches D-03 exactly as specified.

## Deviations from Plan

None - plan executed exactly as written. The `.env.example` two-entry addition and the `⚠️` startup-warning placement were both explicitly called for in the plan's Task 2 action text, not independent deviations.

## Issues Encountered
- `go build ./cmd/api/` (run during verification) produced a stray `api` binary artifact in the repo root; it was untracked and removed (`rm -f api`) before staging so it was never committed. Not a code change, just build-verification housekeeping.

## User Setup Required

None - no external service configuration required. `WEBHOOK_ALLOW_PRIVATE_IPS` defaults to false/unset; operators who need it set it in their own `.env`.

## Next Phase Readiness

WEBHOOK-06 is fully addressed: default posture is unchanged (SC1), the opt-in works and is tested both ways (SC2), scheme/URL validation is untouched by the flag (SC3), and the flag is documented with a safe default (SC4). D-01 and D-04 are both verified by tests and by `go vet`/`go build` running clean across the whole module. No blockers for Phase 6 or Phase 7.

---
*Phase: 05-webhook-ssrf-private-ip-opt-out*
*Completed: 2026-07-08*
