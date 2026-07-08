---
phase: 02-webhook-delivery
plan: 03
subsystem: worker
tags: [asynq, webhook, hmac, retry-backoff, jitter, go, postgres]

# Dependency graph
requires:
  - phase: 02-webhook-delivery
    provides: "02-01: jobs.Job.CallbackURL populated by Repo.Get; 02-02: internal/webhook package (SignPayload, Repo.RecordAttempt/MarkDeadLetter, Deliverer.Deliver)"
provides:
  - "internal/queue: TypeWebhookDeliver/QueueWebhook, WebhookPayload, NewWebhookDeliverTask (MaxRetry=6), WebhookRetryDelay (30s->15m schedule + +/-25% jitter), Client.EnqueueWebhookDeliver"
  - "internal/worker: Handler.HandleWebhookDeliver (re-read job -> fresh presigned URL per attempt for done -> sign -> deliver -> record -> dead-letter on exhaustion); HandleImageConvert enqueues webhook:deliver best-effort after MarkDone/MarkFailed when callback_url set"
  - "cmd/worker: registers HandleWebhookDeliver, weighted queues (image:2, webhook:1), RetryDelayFunc, WEBHOOK_SIGNING_SECRET/WEBHOOK_PRESIGN_TTL wiring"
affects: [phase-3-retry-safety-reconciler]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Bounded exponential backoff + jitter as an asynq RetryDelayFunc living in the queue package (queue.WebhookRetryDelay), not in cmd/worker, mirroring how RedisOpt lives in queue.go"
    - "Handler gains a second Handle<Noun> method (HandleWebhookDeliver) on the same struct rather than a separate handler type, keeping one asynq.ServeMux registration site"

key-files:
  created: []
  modified:
    - internal/queue/queue.go
    - internal/queue/client.go
    - internal/worker/worker.go
    - cmd/worker/main.go
    - .env.example

key-decisions:
  - "Attempt number and dead-letter decision are computed from asynq.GetRetryCount(ctx)/asynq.GetMaxRetry(ctx) read at the start of the current invocation, before calling Deliver; final attempt = retryCount >= maxRetry (7th call when MaxRetry=6, since GetRetryCount is 0 on the first attempt)"
  - "MarkDeadLetter is only called when RecordAttempt succeeded (guards against calling it with a Nil deliveryID if the insert itself failed) — a small defensive addition beyond the plan's literal pseudocode, with no behavioral downside (an UPDATE on a nonexistent id would just no-op)"
  - "Documented WEBHOOK_SIGNING_SECRET (required) and WEBHOOK_PRESIGN_TTL (optional, default 6h) in .env.example, matching how API_KEY_SALT is documented there, since the worker now fails fast without the secret"

patterns-established:
  - "Postgres-first webhook enqueue: MarkDone/MarkFailed commits before EnqueueWebhookDeliver is attempted, and the enqueue call is best-effort (error discarded) so a Redis hiccup never fails a conversion that already succeeded"
  - "Delivery failures returned unwrapped (not asynq.SkipRetry) so asynq's own retry/backoff applies; only unparseable payloads and no-callback_url jobs are terminal"

requirements-completed: [WEBHOOK-01, WEBHOOK-03, WEBHOOK-04, WEBHOOK-05]

# Metrics
duration: ~25min
completed: 2026-07-04
---

# Phase 2 Plan 3: Webhook Delivery Worker Wiring Summary

**Completing jobs with a callback_url now trigger a signed, retried, tracked webhook end-to-end: `webhook:deliver` enqueued after MarkDone/MarkFailed, delivered with a freshly-presigned URL per attempt, retried by asynq with bounded exponential backoff + jitter, and dead-lettered after 6 exhausted retries.**

## Performance

- **Duration:** ~25 min
- **Tasks:** 3 completed
- **Files modified:** 5 (4 modified per plan + `.env.example`)

## Accomplishments
- `internal/queue` gained `TypeWebhookDeliver`/`QueueWebhook`, a minimal `WebhookPayload{JobID}`, `NewWebhookDeliverTask` (attaches `asynq.MaxRetry(6)`), `ParseWebhookPayload`, `Client.EnqueueWebhookDeliver`, and `WebhookRetryDelay` — an `asynq.RetryDelayFunc` implementing the 30s→1m→2m→4m→8m→15m schedule with ±25% jitter (D-04/D-05).
- `internal/worker.Handler` gained webhook dependencies (`webhookRepo`, `deliverer`, `enqueuer`, `signingSecret`, `presignTTL`, defaulting to 6h if zero) and a new `HandleWebhookDeliver` method: re-reads the job from Postgres, regenerates a fresh presigned `download_url` per attempt for `done` jobs only (D-09, never reused across retries), signs the body via `webhook.SignPayload`, delivers it, records every attempt via `webhookRepo.RecordAttempt`, and calls `webhookRepo.MarkDeadLetter` on the final exhausted attempt.
- `HandleImageConvert` now enqueues `webhook:deliver` (best-effort, error discarded) right after `MarkDone`/`MarkFailed` commits, whenever `job.CallbackURL != ""` — same Postgres-first discipline as the API's job-creation double-write.
- `cmd/worker/main.go` reads `WEBHOOK_SIGNING_SECRET` fail-fast (`log.Fatalf` if unset), constructs a `queue.Client` enqueuer, `webhook.NewRepo`/`webhook.NewDeliverer`, wires everything into `worker.NewHandler`, registers `HandleWebhookDeliver` on the mux, and configures the `asynq.Server` with weighted queues (`image:2`, `webhook:1`) and `RetryDelayFunc: queue.WebhookRetryDelay`.

## Task Commits

Each task was committed atomically:

1. **Task 1: webhook task type, payload, retry-delay, and producer method** - `ecbd843` (feat)
2. **Task 2: HandleWebhookDeliver + enqueue-on-completion in the worker** - `6f5a2f4` (feat)
3. **Task 3: Register handler, multi-queue weights, retry func, and secret in cmd/worker** - `8329aa5` (feat)

_Note: SUMMARY.md commit follows this file per worktree execution convention._

## Files Created/Modified
- `internal/queue/queue.go` - Added `TypeWebhookDeliver`, `QueueWebhook`, `WebhookPayload`, `NewWebhookDeliverTask`, `ParseWebhookPayload`, `webhookRetrySchedule`, `WebhookRetryDelay`
- `internal/queue/client.go` - Added `Client.EnqueueWebhookDeliver`
- `internal/worker/worker.go` - Extended `Handler`/`NewHandler` with webhook deps; added `HandleWebhookDeliver`; `HandleImageConvert` now enqueues webhook delivery after MarkDone/MarkFailed
- `cmd/worker/main.go` - `WEBHOOK_SIGNING_SECRET` fail-fast, `queue.NewClient`, `webhook.NewRepo`/`NewDeliverer` wiring, mux registration, weighted queue config, `RetryDelayFunc`
- `.env.example` - Documented `WEBHOOK_SIGNING_SECRET` (required) and `WEBHOOK_PRESIGN_TTL` (optional, default 6h)

## Decisions Made
- Guarded `MarkDeadLetter` on `RecordAttempt` succeeding first (`recErr == nil`) rather than blindly calling it with a possibly-`uuid.Nil` id — minor defensive addition beyond the plan's literal pseudocode, no behavioral change in the success path.
- Added `.env.example` entries for the two new env vars even though the plan's `files_modified` list didn't name that file, since `WEBHOOK_SIGNING_SECRET` is now a required fail-fast var (matches the existing `API_KEY_SALT` documentation precedent) and would otherwise silently break local worker startup for anyone following the README's `.env` setup flow.

## Deviations from Plan

None beyond the two minor additions noted above (defensive `recErr` guard, `.env.example` documentation) — both are small, net-positive, no-scope-creep additions; all three tasks' acceptance criteria (grep checks, `go build ./...`, `go vet`, `go test ./...`) pass as specified.

## Issues Encountered

None. Verified `asynq.GetRetryCount`/`asynq.GetMaxRetry`/`RetryDelayFunc` signatures directly against the vendored `github.com/hibiken/asynq@v0.26.0` source before use, matching the plan's interface notes exactly.

## User Setup Required

**`WEBHOOK_SIGNING_SECRET` must be set for the worker to start** (fail-fast `log.Fatalf` if empty) — added to `.env.example` with a placeholder value; operators must set a real random secret before running `cmd/worker` against a live callback-consuming client. `WEBHOOK_PRESIGN_TTL` is optional (defaults to 6h) and only needs overriding if the retry window or client-recovery assumptions change.

## Next Phase Readiness

- The full webhook delivery slice is now live end-to-end: `POST /v1/jobs` with `callback_url` (02-01) → conversion → signed webhook delivered, retried, tracked, dead-lettered (02-02 + 02-03).
- Manual end-to-end verification (per plan's `<verification>` block) requires a live docker-compose stack with `WEBHOOK_SIGNING_SECRET` set and a request-capture endpoint as `callback_url` — not run in this worktree (no live infra available here); recommend the orchestrator or a follow-up session runs this manual check before considering Phase 2 fully done.
- No blockers for Phase 3 (Retry-Safety & Reconciler) — its sweep query can extend naturally to also cover stuck `webhook_deliveries` rows, as already noted in STATE.md's roadmap decisions.

---
*Phase: 02-webhook-delivery*
*Completed: 2026-07-04*
