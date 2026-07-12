---
phase: 02-webhook-delivery
plan: 02
subsystem: api
tags: [hmac-sha256, postgres, pgx, http-client, httptest, webhook]

# Dependency graph
requires:
  - phase: 01-merge-auth-rate-limiting
    provides: "clients table + API-key auth conventions this phase's env-var config follows"
provides:
  - "internal/webhook package: SignPayload (HMAC-SHA256), Delivery domain type, Repo (RecordAttempt/MarkDeadLetter), Deliverer (single-attempt HTTPS POST)"
  - "Migration 0003 adding webhook_deliveries.dead_letter"
affects: [02-03-worker-wiring, webhook-delivery, reconciler]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "HMAC-SHA256 over '<timestamp>.<body>' via crypto/hmac+crypto/sha256, hex-encoded, mirroring auth.HashKey's pure-function idiom"
    - "Guarded pgx.BeginFunc transaction discipline applied to MarkDeadLetter even without a status enum to lock against"
    - "Queue-agnostic HTTP deliverer (no import on the task-queue library) returning plain errors so the caller's retry policy applies"

key-files:
  created:
    - internal/db/migrations/0003_webhook_dead_letter.sql
    - internal/webhook/webhook.go
    - internal/webhook/sign.go
    - internal/webhook/sign_test.go
    - internal/webhook/repo.go
    - internal/webhook/repo_test.go
    - internal/webhook/deliver.go
    - internal/webhook/deliver_test.go
  modified: []

key-decisions:
  - "Deliver()'s doc comments avoid the literal word 'asynq' so the plan's grep -c \"asynq\" == 0 acceptance criterion (queue-agnostic guarantee) holds even in prose, not just imports."
  - "Deliverer's timeout test constructs a &Deliverer{hc: ...} variant directly (in-package test) with a 20ms client timeout instead of routing through NewDeliverer's fixed 10s D-08 timeout, keeping the test fast and deterministic."

patterns-established:
  - "Pure-function HMAC signing (SignPayload) with secret as a parameter, never read from env inside the package — mirrors auth.HashKey's salt parameter."
  - "Repo.RecordAttempt is a plain single-statement INSERT ... RETURNING id (no transaction needed, no state machine to guard); Repo.MarkDeadLetter uses pgx.BeginFunc for the lock-then-update discipline even without an allow-list, since webhook_deliveries has no status enum."

requirements-completed: [WEBHOOK-02, WEBHOOK-04, WEBHOOK-05]

# Metrics
duration: 25min
completed: 2026-07-04
---

# Phase 2 Plan 2: Webhook Delivery Primitives Summary

**HMAC-SHA256 payload signing, a Postgres delivery-attempt repository with dead-lettering, and a single-attempt HTTPS deliverer (2xx-only, 10s timeout), each independently unit/integration tested.**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-04T18:40:00Z
- **Completed:** 2026-07-04T19:05:00Z
- **Tasks:** 3
- **Files modified:** 8 (all new)

## Accomplishments
- `internal/webhook.SignPayload` produces a deterministic 64-char hex HMAC-SHA256 digest over `<timestamp>.<body>`, letting receivers verify authenticity and reject replays (D-01, WEBHOOK-02)
- `internal/webhook.Repo` persists delivery attempts (`RecordAttempt`, nullable `status_code` for network/timeout failures) and flags exhausted deliveries `dead_letter=true` (`MarkDeadLetter`) under a guarded `pgx.BeginFunc` transaction (WEBHOOK-04/05, D-10)
- `internal/webhook.Deliverer` POSTs a signed body with `X-OctoConv-Signature`/`X-OctoConv-Timestamp` headers, classifies only HTTP 2xx as success, and bounds each attempt to a 10s timeout (D-07/D-08)
- Migration `0003_webhook_dead_letter.sql` adds `webhook_deliveries.dead_letter boolean NOT NULL DEFAULT false` plus a partial index for the dead-letter investigation query path

## Task Commits

Each task was committed atomically:

1. **Task 1: Migration 0003, Delivery domain type, and HMAC signing** - `df8d1a8` (feat)
2. **Task 2: webhook_deliveries repository (RecordAttempt + MarkDeadLetter)** - `5598f29` (feat)
3. **Task 3: Single-attempt HTTPS deliverer (D-07/D-08)** - `7486a5d` (feat)

_Note: no TDD split — each task's implementation + tests were written and verified together, then committed as one `feat` commit per task, matching how this plan's `<verify>` blocks are scoped._

## Files Created/Modified
- `internal/db/migrations/0003_webhook_dead_letter.sql` - Adds `dead_letter` column + partial index to `webhook_deliveries`
- `internal/webhook/webhook.go` - Package doc + `Delivery` domain type mirroring the table columns
- `internal/webhook/sign.go` - `SignPayload(secret, timestamp, body) string`, HMAC-SHA256 hex digest
- `internal/webhook/sign_test.go` - Determinism, different-secret/timestamp/body, output-format tests
- `internal/webhook/repo.go` - `Repo{RecordAttempt, MarkDeadLetter}` backed by pgx pool
- `internal/webhook/repo_test.go` - Integration test (DATABASE_URL-gated): two attempts + dead-letter flag verified via direct SELECT
- `internal/webhook/deliver.go` - `Deliverer{Deliver}`: signed HTTPS POST, 2xx=success, 10s timeout
- `internal/webhook/deliver_test.go` - httptest-based success/non-2xx/timeout coverage

## Decisions Made
- Avoided the literal string "asynq" anywhere in `deliver.go` (including comments) so the plan's `grep -c "asynq"` == 0 acceptance criterion holds exactly — not just "no import", but no textual reference at all, keeping the queue-agnostic intent unambiguous to future readers/greps.
- `TestDeliverTimeout` builds a short-timeout `&Deliverer{hc: ...}` directly rather than adding a timeout parameter to `NewDeliverer` — keeps the constructor's public API minimal (D-08's 10s is fixed, not configurable) while still allowing a fast, deterministic in-package test.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
- Initial `deliver.go` doc comments referenced "asynq" in prose (e.g. "asynq.SkipRetry", "asynq's own retry policy") even though the file has zero imports on the asynq package. This failed the acceptance criterion `grep -c "asynq" internal/webhook/deliver.go == 0`. Fixed by rewording comments to refer to "the task queue" / "the queue's own retry policy" instead — same meaning, no literal match. Re-verified: `grep -c "asynq"` now returns `0`.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- `internal/webhook` compiles and exposes everything Plan 02-03 needs: `SignPayload`, `Delivery`, `Repo{RecordAttempt, MarkDeadLetter}`, `Deliverer{Deliver}`.
- Migration 0003 is in place; `internal/jobs` still needs `callback_url` API-surface wiring (D-02/D-03) and `internal/queue`/`internal/worker`/`cmd/worker` still need the `webhook:deliver` task type, enqueue-on-completion, and handler registration — all explicitly deferred to Plan 02-03 per this plan's stated purpose.
- No blockers. `go build ./...`, `go vet ./internal/webhook/`, and the full `go test ./...` (with `DATABASE_URL` set against the local dev Postgres on port 5434) all pass.

## Self-Check: PASSED

- `[ -f internal/webhook/webhook.go ]`, `[ -f internal/webhook/sign.go ]`, `[ -f internal/webhook/repo.go ]`, `[ -f internal/webhook/deliver.go ]`, `[ -f internal/db/migrations/0003_webhook_dead_letter.sql ]` — all present.
- `git log --oneline --all --grep="02-02"` → 3 commits (`df8d1a8`, `5598f29`, `7486a5d`).
- All task-level `<acceptance_criteria>` re-verified passing (grep checks + targeted `go test` runs) after the `asynq` wording fix.
- Plan-level `<verification>`: `go build ./...` clean, `go vet ./internal/webhook/` clean, `go test ./internal/webhook/ -run 'SignPayload|Deliver'` passes with no DB/network, `go test ./internal/webhook/ -run 'Repo|DeadLetter'` passes with `DATABASE_URL` set (verified against local Postgres on port 5434) and skips cleanly when unset.

---
*Phase: 02-webhook-delivery*
*Completed: 2026-07-04*
