# Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test - Context

**Gathered:** 2026-07-08
**Status:** Ready for planning

<domain>
## Phase Boundary

The reconciler gains a second sweep responsibility — detecting `done`/`failed` jobs whose completion webhook was silently never enqueued (e.g. a Redis blip at the exact moment `HandleImageConvert` tried to fire `EnqueueWebhookDeliver`) — and triggers exactly one delivery attempt for them, guarded by a new `asynq.Unique` lock on the webhook queue so a racing sweep tick can never create a duplicate concurrent delivery. Separately, the existing `queued`/`active` staleness recovery paths (built in Phase 3, currently validated only via mocked-clock integration tests) get a real wall-clock automated soak test using short-but-genuine real durations. This phase covers: RECON-04 (webhook-gap sweep), RECON-05 (soak test), and the `asynq.Unique` addition to the webhook queue that RECON-04 depends on for correctness. It does NOT cover: any change to the webhook delivery mechanism itself (signing, backoff schedule, dead-letter logic — all Phase 2, unchanged), any change to the existing `queued`/`active` recovery logic itself (Phase 3, unchanged — RECON-05 only adds a test proving it), or a general-purpose circuit breaker / manual replay tool (both already deferred in Phase 2's context).

</domain>

<decisions>
## Implementation Decisions

### Duplicate-delivery guard
- **D-01:** Add `asynq.Unique` to the webhook queue's task creation (`NewWebhookDeliverTask` in `internal/queue/queue.go`), mirroring Phase 3's `ImageUniqueTTL` pattern exactly — a per-job uniqueness lock so two concurrent enqueue attempts for the same job's webhook delivery collide safely (`asynq.ErrDuplicateTask`) instead of creating two live tasks. This closes the race not just for the new gap-sweep, but for any future code path that might call `EnqueueWebhookDeliver` twice for the same job.
- **D-02:** The webhook-unique-lock TTL is DERIVED the same way as `ImageUniqueTTL` (not hardcoded): `(maxRetry+1) * perAttemptTimeout + webhookBackoffSum(maxRetry) + safety margin`, using the existing `MaxRetry=6` / 10s-per-attempt / ~30min-backoff-window constants from Phase 2. Exact constant name/derivation function left to Claude's Discretion (see below) — the principle (derived, not hardcoded, scales if `MaxRetry`/timeout constants change) is locked.
- **D-03:** The gap-sweep's re-enqueue is **enqueue-first**, exactly mirroring Phase 3's reconciler pattern for image jobs: attempt `EnqueueWebhookDeliver` first; only if it succeeds (not `asynq.ErrDuplicateTask`) does the sweep proceed to record the gap-recovery event. A `asynq.ErrDuplicateTask` result means a delivery is already live/queued for that job — not actually a gap, skip silently (same reasoning as Phase 3's RECON-01 duplicate-guard).

### Staleness threshold for gap detection
- **D-04:** A `done`/`failed` job with zero `webhook_deliveries` rows is only considered a genuine gap once `ActiveStaleAfter` has elapsed since the job's `finished_at` (reusing the existing reconciler config value, not a new dedicated threshold/env var) — this avoids false-positiving on a job whose webhook enqueue is legitimately still in flight through the same tick that just marked it done/failed.
- **D-05 (locked, inherited from ROADMAP SC2 — not re-litigated):** A job with ANY existing `webhook_deliveries` row — including a fully dead-lettered one — is never re-swept, even if delivery ultimately failed. The gap-sweep only fires for the "enqueue never happened at all" case, not for "delivery was attempted and exhausted."

### Observability
- **D-06:** Gap detection/recovery is logged the same way as Phase 3's existing reconciler actions: a `job_events` row (e.g. `to_status` unchanged, `detail` describing the gap-recovery action) AND a call to `metrics.RecordReconcilerAction` with a new action label value (e.g. `"webhook_gap_recovered"`) alongside the existing `"recovered"`/`"exhausted"` values — extends the existing Phase 4 `octoconv_reconciler_actions_total` metric rather than introducing a new metric family.

### Soak test (RECON-05)
- **D-07:** An automated Go integration test (not a manual runbook) that constructs a real `Sweeper` with `Config` staleness/interval values set to short-but-genuinely-real durations (seconds, not the production-scale ~5 minute default) via the existing `Config` struct — NOT a mocked/fake clock. The test calls `Sweeper.Run` in a goroutine and uses real `time.Sleep`/polling to observe the real wall-clock recovery and exhaustion behavior against a live Postgres (same live-DB-required convention as other integration tests in this repo).
- **D-08:** The soak test covers both ROADMAP success criteria: (a) a genuinely stranded `queued`/`active` job is recovered within the real sweep interval, and (b) a job exceeding `MaxRecoveries` under real elapsed time is terminally failed with the failure recorded in `job_events`.

### Claude's Discretion
- Exact name/derivation function for the webhook `asynq.Unique` TTL (D-02) — follow `ImageUniqueTTL`'s naming convention (e.g. `WebhookUniqueTTL`) and doc-comment style; the planner/executor should verify the exact worst-case formula against the actual `WebhookRetryDelay` backoff schedule and `MaxRetry=6` constant already in `internal/queue/queue.go`.
- Exact SQL/repo method name for finding webhook-gap jobs (e.g. `FindWebhookGaps` on `*jobs.Repo`, mirroring `FindStale`'s existing shape) — technical detail.
- Exact `job_events` `detail` JSON shape for the new gap-recovery event and the exact string value used for the new `RecordReconcilerAction` label — technical detail, should read naturally alongside the existing `"recovered"`/`"exhausted"` values.
- Exact short-duration values used in the D-07 soak test (e.g. 2-5 real seconds for staleness/interval) — planner/executor to pick values that keep the test fast (well under a minute total) while still being genuinely real, unmocked time.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Current Milestone v1.1 section
- `.planning/REQUIREMENTS.md` — `RECON-04`, `RECON-05` (locked v1.1 scope for this phase)
- `.planning/ROADMAP.md` — Phase 6 goal, success criteria (SC1/SC2 lock the "zero rows only, never re-trigger an existing/dead-lettered row" behavior; SC3/SC4 lock the real-wall-clock soak requirement)

### Prior Phase Context (patterns this phase extends)
- `.planning/milestones/v1.0-phases/03-retry-safety-reconciler/03-CONTEXT.md` — the original reconciler design (D-08 through D-15): staleness thresholds, enqueue-first + `asynq.ErrDuplicateTask`-guard pattern (D-03 in this phase mirrors this exactly), recovery-cap philosophy
- `.planning/milestones/v1.0-phases/02-webhook-delivery/02-CONTEXT.md` — webhook delivery mechanism (D-04/D-05: `MaxRetry=6`, backoff schedule) that this phase's `asynq.Unique` TTL derivation (D-02) must be consistent with

### Existing Codebase (reference patterns to follow)
- `internal/queue/queue.go` — `ImageUniqueTTL` (lines ~193-227) is the exact pattern D-02 mirrors for a new `WebhookUniqueTTL`; `NewWebhookDeliverTask` (line ~73-80) is where `asynq.Unique` gets added (D-01); `WebhookRetryDelay` (line ~104) and the `MaxRetry=6` constant are the inputs to the TTL derivation
- `internal/reconciler/reconciler.go` — `Sweeper.sweep`, `jobStore`/`enqueuer` interfaces, the existing enqueue-first + `asynq.ErrDuplicateTask` pattern (D-03 extends this to webhook gaps), `Config` struct (D-07's soak test constructs this with short durations)
- `internal/jobs/repo.go` — `FindStale`/`RecoveryCount`/`RequeueStale`/`MarkFailed` — the pattern a new gap-finding repo method should follow
- `internal/metrics/metrics.go` — `RecordReconcilerAction(action string)` — D-06 extends this with a new action value, no new metric family
- `internal/db/migrations/0001_init.sql` — `webhook_deliveries` table (lines 114-124, `job_id`/`delivered` columns, existing `webhook_deliveries_pending_idx` partial index on `delivered = false`) and `jobs` table (`finished_at`, `callback_url`, `status`) — the join/anti-join this phase's new sweep query needs
- `internal/reconciler/reconciler_test.go` — existing in-memory-fake unit test pattern; D-07's soak test is a NEW, separate test file/style (real DB, real clock) rather than extending the existing fake-based tests

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `queue.ImageUniqueTTL(maxRetry, engineTimeout) time.Duration` — the exact derivation-function shape to replicate for webhook (D-02)
- `Repo.transition` / guarded-transition pattern — reused implicitly via `MarkFailed`/`RequeueStale`, no new transition types needed since gap-recovery doesn't change job status (job is already `done`/`failed`)
- `metrics.RecordReconcilerAction` — already accepts an arbitrary action string, no signature change needed for D-06

### Established Patterns
- Enqueue-first + `asynq.ErrDuplicateTask`-guard (Phase 3) — D-03 is a direct application of this same pattern to a new job class (webhook gaps instead of stranded conversions)
- Derived (not hardcoded) unique-lock TTLs that scale with the actual retry-budget constants (Phase 3's `ImageUniqueTTL`) — D-02 replicates this for webhook
- `job_events` + Prometheus counter dual observability for every reconciler action (Phase 3/4) — D-06 extends this same convention to the new action type

### Integration Points
- `internal/queue/queue.go` — `NewWebhookDeliverTask` (add `asynq.Unique`), new `WebhookUniqueTTL` derivation function
- `internal/reconciler/reconciler.go` — `sweep` gains a second scan (webhook gaps) alongside the existing `queued`/`active` staleness scan; `jobStore` interface gains a new method
- `internal/jobs/repo.go` — new method to find `done`/`failed` jobs with a non-empty `callback_url`, zero `webhook_deliveries` rows, and `finished_at` older than `ActiveStaleAfter`
- New soak-test file (likely `internal/reconciler/reconciler_soak_test.go` or similar, planner to decide) — real DB, real clock, separate from the existing fake-based `reconciler_test.go`

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — backend-only phase, same character as Phase 3. Concrete asks: `asynq.Unique` on webhook queue derived the same way as image queue's, gap-sweep reuses `ActiveStaleAfter` rather than introducing a new threshold, dual observability (job_events + metrics) matching existing reconciler-action conventions, and a real (not mocked) but short automated soak test.

</specifics>

<deferred>
## Deferred Ideas

- **A dedicated new staleness threshold/env var for webhook-gap detection** — explicitly rejected in favor of reusing `ActiveStaleAfter` (D-04); revisit only if operational experience shows the two need to diverge.
- **General-purpose circuit breaker or manual replay tooling for webhook delivery** — still out of scope, unchanged from Phase 2's original deferral.
- **Re-resolving/re-validating `callback_url` before each delivery attempt** — still out of scope, unchanged from Phase 2's original D-03 acceptance.

</deferred>

---

*Phase: 6-Reconciler Webhook-Gap Sweep & Staleness Soak Test*
*Context gathered: 2026-07-08*
