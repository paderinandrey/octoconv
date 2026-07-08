---
phase: 06-reconciler-webhook-gap-sweep-staleness-soak-test
plan: 01
subsystem: queue
tags: [asynq, redis, go, uniqueness-lock, webhook, jitter]

# Dependency graph
requires:
  - phase: 03-retry-safety-reconciler
    provides: ImageUniqueTTL / imageBackoffSum / uniqueTTLSafetyMargin pattern this plan mirrors for the webhook queue
provides:
  - "WebhookUniqueTTL(maxRetry, perAttemptTimeout) — derived, jitter-corrected worst-case asynq.Unique TTL for the webhook queue"
  - "asynq.Unique(uniqueTTL) applied to NewWebhookDeliverTask, closing the duplicate-delivery race"
  - "Client.webhookUniqueTTL field, derived once at construction, threaded through EnqueueWebhookDeliver"
affects: [06-02-reconciler-gap-sweep, 06-03-staleness-soak-test]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Jitter-inflated worst-case backoff sum (webhookBackoffSum) — sums the raw schedule times a fixed 1.25 jitter ceiling instead of calling the randomized per-call retry-delay function, to preserve a genuine upper bound"

key-files:
  created: []
  modified:
    - internal/queue/queue.go
    - internal/queue/client.go
    - internal/queue/queue_test.go

key-decisions:
  - "webhookBackoffSum sums webhookRetrySchedule[i] * webhookJitterCeiling (1.25) directly rather than calling WebhookRetryDelay, since WebhookRetryDelay's ±25% rand.Float64() jitter would make backoffSum non-deterministic and could under-estimate the true worst case"
  - "webhookMaxRetry (6) and webhookPerAttemptTimeout (10s) are fixed named constants, not env-configurable, matching Phase 2's D-05 decision to keep webhook retry/timeout fixed"
  - "WebhookUniqueTTL reuses the existing uniqueTTLSafetyMargin (2 min) constant rather than introducing a webhook-specific margin, per RESEARCH.md assumption A1"

patterns-established:
  - "Derived (not hardcoded) per-queue asynq.Unique TTL, with an explicit SOUNDNESS CAVEAT doc comment when the underlying per-attempt timeout assumption is weaker than the analogous image-queue case"

requirements-completed: [RECON-04]

# Metrics
duration: ~20min
completed: 2026-07-08
---

# Phase 6 Plan 1: Webhook Queue Uniqueness Lock Summary

**Derived, jitter-corrected `WebhookUniqueTTL` (2477.5s for MaxRetry=6/10s) wired into `asynq.Unique` on the webhook delivery task, closing the duplicate-enqueue race RECON-04's gap sweep depends on**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-07-08T20:30:00Z (approx.)
- **Completed:** 2026-07-08T20:51:37Z
- **Tasks:** 2/2 completed
- **Files modified:** 3

## Accomplishments
- Added `webhookJitterCeiling`, `webhookMaxRetry`, `webhookPerAttemptTimeout` constants and a `webhookBackoffSum`/`WebhookUniqueTTL` derivation pair in `internal/queue/queue.go`, mirroring `ImageUniqueTTL`'s shape and doc-comment conventions exactly, but correctly avoiding `WebhookRetryDelay`'s baked-in jitter in the backoff-sum loop
- Verified `WebhookUniqueTTL(6, 10s)` evaluates to exactly `2477500 * time.Millisecond` (2477.5s, ~41m17.5s), is deterministic across calls, monotonic in both arguments, and strictly exceeds the worst-case retry lifetime
- Applied `asynq.Unique(uniqueTTL)` to `NewWebhookDeliverTask` (new `uniqueTTL time.Duration` parameter) and threaded a `webhookUniqueTTL` field through `Client`, derived once in `NewClient`
- Confirmed against a live Redis instance that a second `EnqueueWebhookDeliver` call for the same job id, while the first task/lock is still live, returns `asynq.ErrDuplicateTask`

## Task Commits

Each task was committed atomically:

1. **Task 1: Derive WebhookUniqueTTL with a jitter-inflated worst-case backoff sum** - `841a3dc` (test)
2. **Task 2: Wire asynq.Unique onto the webhook task and Client** - `6af87c1` (feat)

## Files Created/Modified
- `internal/queue/queue.go` - Added `webhookJitterCeiling`/`webhookMaxRetry`/`webhookPerAttemptTimeout` constants, `webhookBackoffSum`, `WebhookUniqueTTL`; changed `NewWebhookDeliverTask` signature to accept `uniqueTTL time.Duration` and apply `asynq.Unique`
- `internal/queue/client.go` - Added `webhookUniqueTTL time.Duration` field to `Client`; `NewClient` derives it via `WebhookUniqueTTL(webhookMaxRetry, webhookPerAttemptTimeout)`; `EnqueueWebhookDeliver` passes it through to `NewWebhookDeliverTask`
- `internal/queue/queue_test.go` - Added `TestWebhookUniqueTTL` (exact-value/worst-case/monotonicity/determinism) and `TestEnqueueWebhookDeliverDuplicate` (live-Redis duplicate-guard integration test)

## Decisions Made
- Followed the plan's mandated deviation from `imageBackoffSum`'s shape: `webhookBackoffSum` sums the raw `webhookRetrySchedule` values times a fixed `1.25` jitter ceiling instead of calling `WebhookRetryDelay` in the loop — this was the single most load-bearing correctness requirement called out in the plan/research and was implemented exactly as specified
- No new env var introduced for `webhookMaxRetry`/`webhookPerAttemptTimeout` — kept as fixed constants per D-05/Phase 2, matching the plan's explicit instruction not to parameterize `MaxRetry`

## Deviations from Plan

None — plan executed exactly as written. Both tasks' `<action>` and `<behavior>` specifications were implemented verbatim, including the doc-comment worked example and SOUNDNESS CAVEAT language.

## Issues Encountered
- The worktree had no local `.env`/live Redis; the live-Redis integration test (`TestEnqueueWebhookDeliverDuplicate`) was verified by sourcing the main repo's `.env` (`/Users/apaderin/dev/octoconv/.env`) against the already-running `docker-compose` stack (`octoconv-redis`), confirming the test passes and the duplicate-guard behaves as required. In a clean environment without `REDIS_ADDR` set, the test skips cleanly as designed.
- A pre-existing `gofmt` formatting nit on an unrelated line in `internal/queue/queue_test.go` (line ~50, extra space before a trailing comment) was found but is out of scope for this plan (present before any changes in this plan) and was left untouched per the scope-boundary rule; logged here for visibility, not fixed.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- `WebhookUniqueTTL`/`asynq.Unique` on the webhook queue is now in place and verified, satisfying the hard prerequisite Plan 06-02 (RECON-04 gap sweep) needs before it can safely call `EnqueueWebhookDeliver` from a reconciler sweep tick without risking duplicate concurrent deliveries
- No blockers identified for downstream plans in this phase

---
*Phase: 06-reconciler-webhook-gap-sweep-staleness-soak-test*
*Completed: 2026-07-08*

## Self-Check: PASSED

- FOUND: internal/queue/queue.go
- FOUND: internal/queue/client.go
- FOUND: internal/queue/queue_test.go
- FOUND: .planning/phases/06-reconciler-webhook-gap-sweep-staleness-soak-test/06-01-SUMMARY.md
- FOUND: 841a3dc (test commit)
- FOUND: 6af87c1 (feat commit)
- FOUND: 4b957f0 (docs/summary commit)
