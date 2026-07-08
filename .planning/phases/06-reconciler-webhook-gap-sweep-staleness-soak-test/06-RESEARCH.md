# Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test - Research

**Researched:** 2026-07-08
**Domain:** Go backend — asynq task-queue uniqueness locks, Postgres anti-join queries, real-wall-clock Go integration testing
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

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

### Deferred Ideas (OUT OF SCOPE)
- **A dedicated new staleness threshold/env var for webhook-gap detection** — explicitly rejected in favor of reusing `ActiveStaleAfter` (D-04); revisit only if operational experience shows the two need to diverge.
- **General-purpose circuit breaker or manual replay tooling for webhook delivery** — still out of scope, unchanged from Phase 2's original deferral.
- **Re-resolving/re-validating `callback_url` before each delivery attempt** — still out of scope, unchanged from Phase 2's original D-03 acceptance.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| RECON-04 | Reconciler находит задачи в статусе `done`/`failed` с непустым `callback_url`, для которых нет ни одной записи в `webhook_deliveries`, и инициирует доставку вебхука (закрывает гонку потери вебхука при сбое Redis в момент завершения задачи) | Pattern 2 (`FindWebhookGaps` NOT EXISTS query), Pattern 3 (`RecordWebhookGapRecovered` event write), Pattern 4 (`sweep()` extension), Pattern 1 (`WebhookUniqueTTL`/D-01 prerequisite), Don't Hand-Roll (supporting index) |
| RECON-05 | Восстановление зависших `queued`/`active` задач подтверждено реальным wall-clock soak-тестом (не только интеграционными тестами против живой БД) | Common Pitfalls 3-5, Code Examples (soak test skeleton), Pattern discussion of `RequeueStale`/`created_at` interaction |
</phase_requirements>

## Summary

This phase is a pure extension of Phase 3's reconciler and Phase 2's webhook delivery, both already implemented and stable. Nothing here requires a new library, a new architectural layer, or a new external dependency — it is three additions to existing files (`internal/queue/queue.go`, `internal/jobs/repo.go`, `internal/reconciler/reconciler.go`) plus two new/extended test files, all following patterns already established and tested in this codebase.

The three technical unknowns flagged in CONTEXT.md are now resolved by direct source inspection, not guesswork:

1. **`WebhookUniqueTTL` formula** — mirrors `ImageUniqueTTL` exactly, but the naive port has a real bug risk: `WebhookRetryDelay` has ±25% jitter (`ImageRetryDelay` does not), so a backoff-sum helper that calls `WebhookRetryDelay(i, nil, nil)` in a loop (the way `imageBackoffSum` calls `ImageRetryDelay`) produces a **non-deterministic, average-case** value, not a worst-case bound. The derivation must sum `webhookRetrySchedule[i] * 1.25` explicitly. Verified worst-case TTL: **~41m17.5s**, meaningfully longer than the ~30min figure quoted in Phase 2's context (which ignored jitter, the "+1 attempt" correction, and the safety margin).
2. **`FindWebhookGaps` SQL** — a `NOT EXISTS` anti-join against `webhook_deliveries` (deliberately checking ALL rows, not filtering on `delivered`/`dead_letter`, which is what makes dead-lettered jobs correctly excluded per D-05). The existing `webhook_deliveries_pending_idx` is a **partial** index (`WHERE delivered = false`) and cannot serve this query efficiently — a new non-partial index on `webhook_deliveries(job_id)` is recommended.
3. **Real wall-clock soak test** — the critical, non-obvious finding: `ImageUniqueTTL`'s `uniqueTTLSafetyMargin` is a **hardcoded 2-minute constant**, so any soak test that uses a real `queue.Client` (real Redis, real `asynq.Unique` lock) cannot exercise repeated recoveries in "well under a minute" — each recovery cycle would need to wait out at least a 2-minute lock. The correct design (confirmed against CONTEXT.md's own wording, "against a live Postgres" — no mention of live Redis) is to pair a **real `jobs.Repo`** (live Postgres, proving real `FindStale`/`RecoveryCount`/`RequeueStale`/`MarkFailed` behavior under genuine elapsed time) with the **existing in-memory `fakeEnqueuer`** already defined in `internal/reconciler/reconciler_test.go` (no live Redis needed, no asynq.Unique lock lifecycle to fight against). This keeps the whole soak test in the single-digit-seconds range.

**Primary recommendation:** Implement `WebhookUniqueTTL` in `internal/queue/queue.go` using an explicit worst-case-jitter backoff sum (not a call to the jittered `WebhookRetryDelay`); implement `FindWebhookGaps`/`RecordWebhookGapRecovered` in `internal/jobs/repo.go` as a `NOT EXISTS` query and a plain (non-guarded-transition) event insert; extend `Sweeper.sweep()` with a second enqueue-first loop; write the soak test as a new file in the `reconciler` package using a live Postgres `jobs.Repo` + the existing fake enqueuer, never a real `queue.Client`.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Webhook-gap detection (SQL query) | Database / Storage | API/Backend (Repo method) | The anti-join is inherently a data-layer query; `jobs.Repo` already owns all job-state queries (`FindStale`, `RecoveryCount`) |
| Webhook-gap recovery orchestration | API/Backend | — | `Sweeper.sweep()` is the single existing orchestration point for all reconciler actions — no new component |
| Duplicate-delivery guard (`asynq.Unique`) | API/Backend | Database / Storage (Redis-backed lock) | Lock lives in Redis but is a property of task *creation*, owned by `internal/queue` (already the sole owner of task-shape/uniqueness policy for the image queue) |
| Real-wall-clock soak verification | API/Backend (test-only) | — | A Go integration test against a live Postgres — no production runtime component |
| Observability (job_events + metric) | Database / Storage (job_events) + API/Backend (metrics) | — | Extends the existing dual-write pattern from Phase 3/4 exactly |

This phase introduces no new tier and no new capability outside "API/Backend" — consistent with CONTEXT.md's framing ("backend-only phase, same character as Phase 3").

## Standard Stack

No new libraries. This phase uses only what is already in `go.mod`:

| Library | Version | Purpose | Why Standard (already used) |
|---------|---------|---------|------------------------------|
| `github.com/hibiken/asynq` | v0.26.0 (pinned) | `asynq.Unique`, `asynq.ErrDuplicateTask` on the webhook queue | Already used identically for the image queue (`ImageUniqueTTL`, Phase 3) |
| `github.com/jackc/pgx/v5` | v5.10.0 (pinned) | `NOT EXISTS` anti-join query, plain `job_events` insert | Already the sole DB driver; no ORM in this codebase |
| Go stdlib `testing` | go1.26 toolchain | Real-wall-clock soak test (`time.Sleep`, polling loop) | Already the only test framework used repo-wide (no testify) |

**Version verification:** `go.mod:hibiken/asynq v0.26.0` and `jackc/pgx/v5 v5.10.0` confirmed present and unchanged via `grep go.mod` `[VERIFIED: local go.mod]`. No `npm view`/`pip index` equivalent applies — this is a pure Go, no-new-dependency phase.

**Installation:** None — no new packages.

## Package Legitimacy Audit

**Not applicable.** This phase adds zero new external packages (no `go.mod` changes). All work happens inside existing files (`internal/queue/queue.go`, `internal/jobs/repo.go`, `internal/reconciler/reconciler.go`) plus new/extended `_test.go` files and possibly one new SQL migration file. The Package Legitimacy Gate protocol is skipped as a hard "no packages" case, not a degraded/assumed case.

## Architecture Patterns

### System Architecture Diagram

```text
                         ┌─────────────────────────────┐
                         │      Sweeper.Run(ctx)        │
                         │  (ticker: SweepInterval)     │
                         └──────────────┬───────────────┘
                                        │ tick
                                        ▼
                         ┌─────────────────────────────┐
                         │        Sweeper.sweep()       │
                         └──────────────┬───────────────┘
                    ┌───────────────────┼────────────────────┐
                    ▼                   ▼                    ▼ (NEW, this phase)
        FindStale(queued/active)   RecoveryCount        FindWebhookGaps(ActiveStaleAfter)
        [EXISTING, Phase 3]        [EXISTING]           done/failed + callback_url set +
                    │                                    zero webhook_deliveries rows +
                    ▼                                    finished_at < cutoff
        EnqueueImageConvert                                       │
        (asynq.Unique guard,                                      ▼
         ImageUniqueTTL)                              EnqueueWebhookDeliver
                    │                                  (NEW: asynq.Unique guard,
          ┌─────────┴─────────┐                         WebhookUniqueTTL)
          │ ErrDuplicateTask? │                                    │
          │  yes → skip       │                         ┌──────────┴──────────┐
          │  no  → RequeueStale                          │ ErrDuplicateTask?   │
          │        + metrics("recovered")                │  yes → skip         │
          └───────────────────┘                          │  no  → RecordWebhook│
                                                            │        GapRecovered │
                                                            │        + metrics    │
                                                            │        ("webhook_   │
                                                            │        gap_recovered")│
                                                            └──────────────────────┘

  At MaxRecoveries cap: MarkFailed + metrics("exhausted") [EXISTING, unchanged]
```

A reader can trace: ticker fires → `sweep()` runs the EXISTING queued/active scan (unchanged, Phase 3) AND the NEW webhook-gap scan (this phase) → each scan applies the same enqueue-first + `asynq.ErrDuplicateTask`-guard idiom → each successful recovery emits a `job_events` row + a `RecordReconcilerAction` metric call.

### Recommended Project Structure

No new directories. Changes land in existing files:
```
internal/
├── queue/
│   ├── queue.go          # + webhookMaxRetry, webhookPerAttemptTimeout consts,
│   │                       # webhookBackoffSum(), WebhookUniqueTTL(); modify
│   │                       # NewWebhookDeliverTask() signature to add asynq.Unique
│   ├── client.go          # + webhookUniqueTTL field on Client, computed once in NewClient()
│   └── queue_test.go      # + TestWebhookUniqueTTL, TestEnqueueWebhookDeliverDuplicate (live Redis)
├── jobs/
│   ├── repo.go            # + WebhookGapJob type, FindWebhookGaps(), RecordWebhookGapRecovered()
│   └── repo_test.go       # + TestFindWebhookGaps (live Postgres, mirrors TestFindStale)
├── reconciler/
│   ├── reconciler.go      # + jobStore gains 2 methods; sweep() gains a 2nd loop
│   ├── reconciler_test.go # + fakeStore/fakeEnqueuer extended; 2-3 new unit tests
│   └── reconciler_soak_test.go   # NEW FILE: real-wall-clock integration test
└── db/migrations/
    └── 0004_webhook_deliveries_job_idx.sql   # OPTIONAL, recommended (see Don't Hand-Roll)
```

### Pattern 1: Worst-case-jitter backoff sum (do NOT reuse `imageBackoffSum`'s shape verbatim)

**What:** `imageBackoffSum` sums `ImageRetryDelay(i, nil, nil)` in a loop because `ImageRetryDelay` is deterministic (no jitter). `WebhookRetryDelay` has ±25% jitter, so calling it in a loop to build a "worst case" sum is wrong — each call returns a *different random value*, and on average it under-estimates the true worst case by up to 25% per term.

**When to use:** Any time a derived-TTL helper must be a genuine upper bound and the underlying per-step delay function is randomized.

**Example (recommended implementation):**
```go
// Source: derived from internal/queue/queue.go's existing webhookRetrySchedule
// and ImageUniqueTTL pattern; jitter bound verified in WebhookRetryDelay's own
// doc comment ("up to ±25% jitter").
const webhookJitterCeiling = 1.25 // WebhookRetryDelay's documented +25% max jitter

// webhookBackoffSum sums the WORST-CASE (jitter-inflated) backoff for i in
// [0, maxRetry), using the raw schedule directly rather than calling the
// jittered WebhookRetryDelay — calling WebhookRetryDelay here would bake in a
// random sample each time, silently violating the "always exceeds worst case"
// contract this TTL must satisfy (see ImageUniqueTTL's doc comment for the
// same contract on the image side, where it holds trivially because
// ImageRetryDelay has no jitter).
func webhookBackoffSum(maxRetry int) time.Duration {
	var sum time.Duration
	for i := 0; i < maxRetry; i++ {
		idx := i
		if idx >= len(webhookRetrySchedule) {
			idx = len(webhookRetrySchedule) - 1
		}
		sum += time.Duration(float64(webhookRetrySchedule[idx]) * webhookJitterCeiling)
	}
	return sum
}

// webhookMaxRetry and webhookPerAttemptTimeout are the two inputs
// WebhookUniqueTTL is derived from. Unlike the image queue's
// IMAGE_MAX_RETRY/ENGINE_TIMEOUT, these are NOT env-configurable (D-05/D-08
// of Phase 2 fixed them as constants) — kept here as named constants (not
// magic numbers) so NewWebhookDeliverTask and WebhookUniqueTTL can never
// silently drift apart.
const webhookMaxRetry = 6
// webhookPerAttemptTimeout MUST match internal/webhook/deliver.go's
// NewDeliverer HTTP client Timeout field. There is no shared exported
// constant between the two packages (internal/webhook does not export one,
// and internal/queue does not currently import internal/webhook) — this is a
// manually-maintained invariant, same discipline as detailActionRecovery's
// single-source-of-truth comment in internal/jobs/repo.go. If deliver.go's
// timeout ever changes, this constant must change with it.
const webhookPerAttemptTimeout = 10 * time.Second

// WebhookUniqueTTL derives the per-job asynq.Unique lock TTL for webhook
// delivery tasks, mirroring ImageUniqueTTL's derivation exactly: (maxRetry+1)
// total attempts (asynq's archive check runs AFTER each failed attempt, same
// as the image queue) times the per-attempt bound, plus the worst-case
// (jitter-inflated) backoff sum, plus the shared safety margin.
//
// Worst-case formula: (maxRetry+1) * perAttemptTimeout + webhookBackoffSum(maxRetry) + margin.
// For the fixed webhook constants (maxRetry=6, perAttemptTimeout=10s):
//   7*10s + (37.5+75+150+300+600+1125)s + 120s = 70s + 2287.5s + 120s = 2477.5s (~41m17.5s)
// — meaningfully longer than the "~30 minute" backoff-window figure quoted in
// Phase 2's context, because that figure did not account for jitter, the
// "+1 attempt" correction, or the safety margin.
//
// SOUNDNESS CAVEAT (weaker than ImageUniqueTTL's): ImageUniqueTTL's
// correctness depends on Plan 02 (03-02) wrapping the ENTIRE image-conversion
// attempt in a single context.WithTimeout(ctx, ENGINE_TIMEOUT). No equivalent
// wrapping exists for HandleWebhookDeliver (internal/worker/worker.go) — only
// the outbound HTTP POST itself is bounded, at 10s, via
// webhook.Deliverer's http.Client.Timeout. The Postgres reads (Get, Outputs)
// and presign-URL generation that happen before the POST are NOT wrapped in
// any per-attempt deadline. This is an accepted, pre-existing residual risk
// (out of this phase's scope per CONTEXT.md's "do NOT research the webhook
// delivery mechanism itself"), documented here rather than silently assumed.
func WebhookUniqueTTL(maxRetry int, perAttemptTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*perAttemptTimeout + webhookBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}
```

### Pattern 2: `NOT EXISTS` anti-join for gap detection (mirrors `FindStale`'s shape)

**What:** Find `done`/`failed` jobs with a callback but zero delivery rows, past the staleness cutoff.
**When to use:** Exactly this query — cutoff computed in Go (matching `FindStale`'s existing convention of binding a precomputed `timestamptz` rather than doing arithmetic in SQL).
**Example:**
```go
// Source: internal/jobs/repo.go's existing FindStale (pattern to mirror)
// WebhookGapJob is a lightweight row returned by FindWebhookGaps: enough for
// the sweeper to enqueue a delivery and record the recovery event.
type WebhookGapJob struct {
	ID     uuid.UUID
	Status string // "done" or "failed" — carried through unchanged into the
	              // job_events row written by RecordWebhookGapRecovered.
}

// FindWebhookGaps returns done/failed jobs with a non-empty callback_url and
// ZERO rows in webhook_deliveries (any row — delivered, undelivered, or
// dead-lettered — excludes a job; see D-05), whose finished_at is older than
// activeStaleAfter (reusing the existing reconciler threshold, D-04, so a
// job whose webhook enqueue is legitimately still in flight through the same
// tick that marked it done/failed is not falsely flagged).
func (r *Repo) FindWebhookGaps(ctx context.Context, activeStaleAfter time.Duration) ([]WebhookGapJob, error) {
	cutoff := time.Now().Add(-activeStaleAfter)
	rows, err := r.pool.Query(ctx, `
		SELECT j.id, j.status FROM jobs j
		WHERE j.status IN ('done', 'failed')
		  AND j.callback_url IS NOT NULL AND j.callback_url <> ''
		  AND j.finished_at < $1
		  AND NOT EXISTS (
		      SELECT 1 FROM webhook_deliveries wd WHERE wd.job_id = j.id
		  )`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("query webhook gaps: %w", err)
	}
	defer rows.Close()

	var out []WebhookGapJob
	for rows.Next() {
		var g WebhookGapJob
		if err := rows.Scan(&g.ID, &g.Status); err != nil {
			return nil, fmt.Errorf("scan webhook gap: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
```

**Why `NOT EXISTS` and not `LEFT JOIN ... WHERE wd.id IS NULL`:** functionally equivalent in Postgres, but `NOT EXISTS` matches this codebase's existing idiom of self-documenting "no row for this predicate" checks (`transition`'s `SELECT ... FOR UPDATE` + allow-list check is conceptually the same "no valid path" shape) and avoids any risk of accidental row multiplication if `webhook_deliveries` ever gains more than one matching row (it will, once retries land — `LEFT JOIN` would then need a `DISTINCT`/`GROUP BY` that `NOT EXISTS` never needs).

### Pattern 3: Event write WITHOUT going through `Repo.transition` (correct, not an anti-pattern violation)

**What:** `RecordWebhookGapRecovered` writes a `job_events` row with `from_status == to_status` (D-06: "to_status unchanged") because the job's actual `jobs.status` value does not change — the job is already `done`/`failed`, and stays that way. `Repo.transition` is built around actual state *changes* (it takes a `to` status and an allow-list of valid `from` statuses, and calls an `apply` closure that mutates `jobs.status`); forcing this event through `transition` would require inventing a fake "self-transition" that adds no value, and the CLAUDE.md anti-pattern "bypassing the guarded transition helper" is specifically about avoiding illegal STATUS changes via ad-hoc UPDATEs — it does not apply here because no status is being updated at all.

**Correctness note:** the DB-level guard against a duplicate recovery event is NOT needed here, because the actual correctness guard is the `asynq.Unique` lock (D-01/D-03) checked BEFORE this method is ever called (enqueue-first ordering) — by the time `RecordWebhookGapRecovered` runs, a fresh, non-duplicate task has already been proven to not exist previously. This mirrors `FindStale`'s existing precedent: `FindStale` itself is a plain `SELECT`, not `SELECT ... FOR UPDATE` — the row lock in `transition()` exists only where `jobs.status` is actually mutated.

**Example:**
```go
// Source: internal/jobs/repo.go's existing detailActionRecovery constant pattern
const detailActionWebhookGapRecovered = "webhook_gap_recovered"

// RecordWebhookGapRecovered appends a job_events row documenting that the
// reconciler detected and recovered a silently-dropped webhook enqueue
// (RECON-04). Unlike every other write in this file, this does NOT go
// through transition(): the job's status is NOT changing (it is already
// done/failed and stays that way), so from_status == to_status == status,
// and no row lock is needed — the correctness guard against a duplicate
// delivery is the asynq.Unique lock the sweeper already checked before
// calling this (enqueue-first, D-03), not a DB-level lock.
func (r *Repo) RecordWebhookGapRecovered(ctx context.Context, id uuid.UUID, status string) error {
	detail := map[string]any{"action": detailActionWebhookGapRecovered}
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal webhook gap detail: %w", err)
	}
	if _, err := r.pool.Exec(ctx,
		`INSERT INTO job_events (job_id, from_status, to_status, detail) VALUES ($1, $2, $2, $3)`,
		id, status, detailJSON,
	); err != nil {
		return fmt.Errorf("record webhook gap recovery for job %s: %w", id, err)
	}
	return nil
}
```

### Pattern 4: `Sweeper.sweep()` extension (enqueue-first, mirrors the existing image-recovery loop exactly)

```go
// Source: internal/reconciler/reconciler.go's existing sweep() loop shape
gaps, err := s.store.FindWebhookGaps(ctx, s.cfg.ActiveStaleAfter)
if err == nil {
	for _, g := range gaps {
		if err := s.enq.EnqueueWebhookDeliver(ctx, g.ID); err != nil {
			if errors.Is(err, asynq.ErrDuplicateTask) {
				// A delivery is already live/queued for this job (this
				// sweep tick raced the previous tick's own enqueue, or a
				// normal HandleImageConvert/HandleWebhookDeliver-triggered
				// delivery is already in flight) — NOT a genuine gap.
				continue
			}
			continue // transient enqueue error: best-effort, retry next tick
		}
		_ = s.store.RecordWebhookGapRecovered(ctx, g.ID, g.Status)
		metrics.RecordReconcilerAction("webhook_gap_recovered")
	}
}
// Best-effort on FindWebhookGaps error too, same as FindStale above it.
```

**Why the `asynq.Unique` lock (D-01) is a hard prerequisite, not just a nice-to-have:** after a sweep tick successfully enqueues a webhook delivery, the corresponding `webhook_deliveries` row is written by `HandleWebhookDeliver` **asynchronously**, whenever a worker actually processes the task — not synchronously as part of the sweep. Between "sweep enqueues" and "worker records the delivery row," `FindWebhookGaps` will match the SAME job again on the very next tick (zero rows still exist). Without `asynq.Unique`, every tick in that window would enqueue a duplicate live task. With it, the second (and any subsequent) attempt within the ~41-minute TTL window correctly returns `asynq.ErrDuplicateTask` and is skipped. **This must be implemented together with, not after, the gap-sweep loop** — an implementation order where the sweep loop lands before the `asynq.Unique` addition would be observably broken (duplicate deliveries) even though each half looks correct in isolation.

### Anti-Patterns to Avoid
- **Calling `WebhookRetryDelay` inside a backoff-sum loop:** produces a random, average-case (not worst-case) TTL. Use the raw `webhookRetrySchedule` slice with an explicit `* 1.25` ceiling instead (Pattern 1).
- **Filtering `FindWebhookGaps`'s `NOT EXISTS` on `delivered = false` or `dead_letter = false`:** would incorrectly re-flag jobs whose delivery already failed/dead-lettered as "gaps," violating D-05. The subquery must match ANY row for the job, unconditionally.
- **Forcing the gap-recovery event through `Repo.transition`:** no status is changing; inventing a same-to-same "transition" adds row-locking overhead with no correctness benefit and awkwardly contorts an API designed for actual state changes (Pattern 3).
- **Soak-testing the `MaxRecoveries` exhaustion path with a real `queue.Client`:** the hardcoded 2-minute `uniqueTTLSafetyMargin` makes each real recovery cycle take at least 2 minutes to naturally free the lock, incompatible with a "well under a minute" test budget. Use the existing in-memory `fakeEnqueuer` instead (Pattern 5 / Common Pitfalls).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Preventing two concurrent webhook enqueues for one job | A custom Postgres advisory lock or a `SELECT ... FOR UPDATE`-guarded "in-flight" flag column | `asynq.Unique` (already the pattern for the image queue) | Redis-native, TTL-bound, zero new schema, and the exact mechanism this phase's own D-01 already specifies |
| Detecting "no row exists for this job in another table" | A cached/derived boolean column on `jobs` (e.g. `webhook_enqueued`) kept in sync by triggers or app code | A `NOT EXISTS` subquery evaluated at sweep time | The existing schema has no such column, and introducing one adds a second source of truth that can drift; `NOT EXISTS` is always correct by construction |
| Efficient anti-join against `webhook_deliveries` | Nothing to hand-roll, but a supporting index is currently missing | Add `CREATE INDEX webhook_deliveries_job_id_idx ON webhook_deliveries (job_id);` in a new migration (the existing `webhook_deliveries_pending_idx` is partial on `delivered = false` and cannot serve a query that must also see `delivered = true`/dead-lettered rows) | Postgres does not auto-index foreign-key columns; without this, `FindWebhookGaps`'s `NOT EXISTS` seq-scans `webhook_deliveries` for every candidate job |

**Key insight:** every piece of this phase already has a direct precedent somewhere in the existing reconciler/webhook code. The main risk is not "picking the wrong library" (there is no library choice here) but **mis-porting a pattern that looks identical but has one hidden difference** — jitter in the retry schedule, a missing index, or a status-transition helper that doesn't fit a no-status-change event.

## Common Pitfalls

### Pitfall 1: Non-deterministic `webhookBackoffSum` from reusing `WebhookRetryDelay`
**What goes wrong:** `WebhookUniqueTTL` returns a different value on every call (or, worse, an average-case value that sometimes falls BELOW the true worst-case retry lifetime), silently breaking the "lock always outlives the retry budget" invariant `ImageUniqueTTL`'s own doc comment establishes as load-bearing.
**Why it happens:** Copy-pasting `imageBackoffSum`'s shape (`sum += XRetryDelay(i, nil, nil)`) without noticing `WebhookRetryDelay` — unlike `ImageRetryDelay` — has `rand.Float64()` jitter baked in.
**How to avoid:** Sum the raw `webhookRetrySchedule[i] * 1.25` values directly (Pattern 1), never call `WebhookRetryDelay` for TTL derivation.
**Warning signs:** A `TestWebhookUniqueTTL` test that asserts an exact value will flake (fail intermittently) if this bug is present — that flakiness IS the detection signal.

### Pitfall 2: Missing index makes `FindWebhookGaps` a full scan on every sweep tick
**What goes wrong:** As `webhook_deliveries` grows, every sweep tick's `NOT EXISTS` subquery degrades from an index lookup to a sequential scan per candidate job.
**Why it happens:** Foreign keys are not auto-indexed in Postgres, and the one existing index on `webhook_deliveries(job_id)` is partial (`WHERE delivered = false`), which the planner cannot use to prove "no row at all" (it can only prove "no *undelivered* row").
**How to avoid:** Add a plain `CREATE INDEX webhook_deliveries_job_id_idx ON webhook_deliveries (job_id);` migration.
**Warning signs:** `EXPLAIN ANALYZE` on the `FindWebhookGaps` query showing `Seq Scan on webhook_deliveries`.

### Pitfall 3: Soak-testing exhaustion with a real `queue.Client` blows the time budget
**What goes wrong:** Using `queue.NewClient()` (real Redis, real `ImageUniqueTTL`) in the soak test means the FIRST successful recovery creates a live `asynq.Unique` lock with a minimum 2-minute TTL (`uniqueTTLSafetyMargin`, hardcoded, not overridable). Every subsequent recovery attempt within that window collides with `asynq.ErrDuplicateTask` and is correctly skipped by the sweeper (by design — this is the anti-flapping guard, RECON-01) — meaning `MaxRecoveries` can never be reached in "well under a minute."
**Why it happens:** It's tempting to reuse the exact production wiring (`jobs.Repo` + `queue.Client`) for "maximum realism," but the `asynq.Unique` lock's 2-minute floor is specifically NOT something the soak test's `Config` durations control (`Config` only has `QueuedStaleAfter`/`ActiveStaleAfter`/`SweepInterval`/`MaxRecoveries` — no TTL knob).
**How to avoid:** Pair a **real `jobs.Repo`** (live Postgres — this is the part CONTEXT.md's D-07 actually requires to be genuine) with the **existing in-memory `fakeEnqueuer`** from `reconciler_test.go` (no live Redis, no lock lifecycle at all — every `EnqueueImageConvert`/`EnqueueWebhookDeliver` call just succeeds). The real-Redis/`asynq.Unique` interaction is already covered by `TestEnqueueImageConvert`-style tests elsewhere; the soak test's unique job is proving `Sweeper.Run`'s real ticker against real elapsed Postgres time, not re-proving asynq's own locking semantics.
**Warning signs:** A soak test that needs `time.Sleep(2 * time.Minute)` or longer to pass — that duration itself is the signal something is wrong, given the phase's own "well under a minute" guidance.

### Pitfall 4: `RequeueStale` never resets `created_at`, so recovery reasons mix mid-test
**What goes wrong:** A soak test that starts from an `active`-stale job and expects every recorded recovery `reason` to be `"stale_active"` will fail after the first recovery, because `RequeueStale` transitions the job back to `queued` (never back to `active`) — and `jobs.created_at` (set once at `Create()`) is almost certainly ALREADY older than `QueuedStaleAfter` by the time the first `active`-path recovery happens (since the job existed for at least `ActiveStaleAfter` before that point). So the SECOND sweep tick after the first recovery finds the job stale via the `queued` branch (`reason = "stale_queued"`), not the `active` branch again.
**Why it happens:** `RequeueStale`'s `UPDATE jobs SET status = 'queued'` never touches `created_at`, and `FindStale`'s queued-branch cutoff (`created_at < now - QueuedStaleAfter`) is a one-way ratchet once tripped — it stays true forever after, since only forward-moving wall-clock time affects it.
**How to avoid:** Assert on cumulative `RecoveryCount` / final terminal status, not on a specific sequence of `reason` values. If a specific reason must be asserted, only assert it for the FIRST recovery event in the sequence.
**Warning signs:** A soak test with a hardcoded loop asserting N identical `"stale_active"` events failing intermittently or entirely after the first iteration.

### Pitfall 5: Comparing Go-side and Postgres-side clocks
**What goes wrong:** `FindStale`/`FindWebhookGaps` compute cutoffs in Go (`time.Now().Add(-d)`) and compare against timestamps set by Postgres's `now()` — any clock skew between the test process and the Postgres server shifts effective staleness by that skew.
**Why it happens:** Two separate clock sources (Go process wall clock vs. Postgres server wall clock).
**How to avoid:** Not a concern for this project's deployment model (single-host `docker-compose`, confirmed running locally with `DATABASE_URL`/`REDIS_ADDR` both live during this research session) — but if the soak test flakes only in CI and not locally, clock skew between containers is the first thing to check. Use generous polling timeouts (10-15s wall-clock budget) rather than tight, exact-boundary sleeps.

## Code Examples

### Live-DB test setup convention (already established — reuse verbatim)

```go
// Source: internal/webhook/repo_test.go / internal/jobs/repo_test.go (existing pattern)
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
```

### Live-Redis test skip convention (for the NEW `TestEnqueueWebhookDeliverDuplicate`)

```go
// Source: internal/queue/queue_test.go's existing TestEnqueueImageConvert (pattern to mirror)
func TestEnqueueWebhookDeliverDuplicate(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("REDIS_ADDR not set; skipping integration test")
	}
	cl, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cl.Close()

	id := uuid.New()
	if err := cl.EnqueueWebhookDeliver(context.Background(), id); err != nil {
		t.Fatalf("first EnqueueWebhookDeliver: %v", err)
	}
	err = cl.EnqueueWebhookDeliver(context.Background(), id)
	if !errors.Is(err, asynq.ErrDuplicateTask) {
		t.Fatalf("second EnqueueWebhookDeliver = %v, want asynq.ErrDuplicateTask", err)
	}
	// cleanup: delete the pending task via asynq.Inspector (see
	// TestEnqueueImageConvert for the ListPendingTasks/DeleteTask idiom).
}
```

### Soak test skeleton (NEW FILE, `internal/reconciler/reconciler_soak_test.go`, package `reconciler`)

```go
// Source: composed from internal/jobs/repo_test.go's newTestRepo/createTestClient
// pattern + internal/reconciler/reconciler_test.go's existing fakeEnqueuer type
// (same package, no import needed) + internal/reconciler/reconciler.go's Run().
func TestSoakRecoversStrandedQueuedJob(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set; skipping soak test")
	}
	pool := newSoakTestPool(t) // local helper mirroring newTestRepo, package reconciler
	repo := jobs.NewRepo(pool)
	clientID := createSoakTestClient(t, pool)

	jobID, err := repo.Create(context.Background(), jobs.CreateParams{
		ClientID: clientID, Operation: "convert", Engine: "image",
		SourceFormat: "png", TargetFormat: "webp",
		Input: jobs.Input{ObjectKey: "uploads/soak/0-in.png", Filename: "in.png", Format: "png"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	enq := &fakeEnqueuer{} // reused from reconciler_test.go, no live Redis
	cfg := Config{
		QueuedStaleAfter: 1 * time.Second,
		ActiveStaleAfter: 1 * time.Second,
		SweepInterval:    300 * time.Millisecond,
		MaxRecoveries:    2,
	}
	s := NewSweeper(repo, enq, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		j, err := repo.Get(context.Background(), jobID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if j.Status == jobs.StatusQueued && len(enq.imageCalls) >= 1 {
			return // recovered — real wall-clock proof, no SQL backdating used
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job was not recovered within 10s of real elapsed time")
}
```

## State of the Art

Not applicable — no library-version or ecosystem drift concerns; this is a same-repo, same-conventions extension. The one "state of the art" fact worth recording: **asynq v0.26.0's uniqueness key is `{queue, task-type, md5(payload)}`** (verified directly in the vendored source, `internal/base/base.go:200-206`, function `UniqueKey`), so adding `asynq.Unique` to a second queue requires zero additional asynq configuration — the image queue's lock namespace (`asynq:{image}:unique:image:convert:<hash>`) and the webhook queue's (`asynq:{webhook}:unique:webhook:deliver:<hash>`) are structurally disjoint by queue name alone, even before task type is considered.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `WebhookUniqueTTL(6, 10s)` should reuse the existing `uniqueTTLSafetyMargin` (2 min) constant rather than a webhook-specific margin | Pattern 1 | Low — same margin already protects the image queue's much shorter worst-case lifetime (~757s) with headroom; reusing it for webhook's much longer (~2477s) worst case is conservative, not risky, but the planner should confirm this is an acceptable shared constant rather than wanting a distinct, possibly larger margin for the longer-lived queue |
| A2 | A new non-partial index `webhook_deliveries_job_id_idx` is worth adding in this phase rather than deferred | Don't Hand-Roll | Low — CONTEXT.md does not lock this decision either way; if omitted, correctness is unaffected, only query efficiency at scale, which matters little for this internal, low-QPS service today |
| A3 | The soak test should use `jobs.Repo` (live Postgres) + `fakeEnqueuer` (no live Redis), not a full live-Redis+live-Postgres stack | Common Pitfalls (Pitfall 3), Code Examples | Medium — if a reviewer expects the soak test to also exercise the real `asynq.Unique` lock end-to-end, this design would need to be revisited; documented rationale (the 2-minute margin makes that combination incompatible with the stated time budget) is given so this can be explicitly re-litigated at plan-check time if needed |

## Open Questions

1. **Should `RecoveryCount`'s cap-check distinguish webhook-gap recoveries from queued/active recoveries?**
   - What we know: `RecordWebhookGapRecovered` deliberately does NOT use `detailActionRecovery` (it uses a new, distinct `detailActionWebhookGapRecovered` tag), so `RecoveryCount` (which filters on `detailActionRecovery` specifically) will never count webhook-gap recoveries toward `MaxRecoveries`.
   - What's unclear: whether this is the desired behavior (webhook-gap recovery is a one-shot, self-terminating action per D-05, so a "cap" arguably doesn't apply — there is nothing to retry once a delivery row exists) or whether the planner wants an explicit statement of this non-interaction in the plan for clarity.
   - Recommendation: confirm in planning that webhook-gap recovery is explicitly uncapped/single-shot (matches D-05's "never re-swept once any row exists" framing) — no code change needed, just document the intent.

2. **Exact migration file number for the optional new index.**
   - What we know: the next unused migration number is `0004` (`0001_init.sql`, `0002_client_api_keys.sql`, `0003_webhook_dead_letter.sql` already exist).
   - What's unclear: whether the planner wants this index bundled into this phase's migration or deferred as its own small housekeeping item.
   - Recommendation: bundle it into this phase (`0004_webhook_deliveries_job_idx.sql`) since it directly supports this phase's new query.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| PostgreSQL | `jobs.Repo`, soak test, `TestFindWebhookGaps` | ✓ | postgres:18 (docker-compose, confirmed running) | — |
| Redis | `queue.Client` integration tests (`TestEnqueueWebhookDeliverDuplicate`) | ✓ | redis:8 (docker-compose, confirmed running) | — |
| Go toolchain | All code/tests | ✓ | go1.26.4 darwin/arm64 (matches `go 1.26.4` in go.mod) | — |
| `vips` CLI | Not needed by this phase (no conversion-engine code touched) | — | — | — |

**Missing dependencies with no fallback:** none.
**Missing dependencies with fallback:** none — both required live services (Postgres, Redis) were confirmed running locally during this research session via `docker compose ps`.

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | No | This phase touches no auth code path |
| V3 Session Management | No | N/A — no session concept in this service |
| V4 Access Control | No | Reconciler runs as an internal background goroutine within the trusted worker process; no new externally-reachable surface |
| V5 Input Validation | No new surface | The new SQL query (`FindWebhookGaps`) takes only an internally-computed `time.Duration` cutoff as its sole parameter — no user-controlled input reaches this query |
| V6 Cryptography | No | No new cryptographic operations; signing/HMAC (Phase 2) is unchanged and out of scope |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| SQL injection via job id / status values | Tampering | Already mitigated project-wide via `pgx` parameterized queries (`$1`, `$2`); the new `FindWebhookGaps`/`RecordWebhookGapRecovered` queries follow the identical parameterization convention as every other `internal/jobs/repo.go` method — no string concatenation introduced |
| Duplicate/replay webhook delivery (already Phase 2's threat model) | Tampering / Denial of Service (redundant load on client endpoint) | `asynq.Unique` (this phase's D-01) is itself the mitigation being added — a sweep-tick race without it would cause duplicate outbound webhook POSTs to the client's `callback_url` |

This phase introduces no new attack surface — it closes a reliability gap (silently dropped webhook enqueue), not a security gap, and the one relevant security-adjacent property (preventing duplicate outbound requests to a client-controlled URL) is exactly what D-01/D-03's `asynq.Unique` guard already addresses.

## Sources

### Primary (HIGH confidence)
- `internal/queue/queue.go` (this repo) — `ImageUniqueTTL`, `imageBackoffSum`, `WebhookRetryDelay`, `webhookRetrySchedule`, `uniqueTTLSafetyMargin` — read directly, lines 92-228
- `internal/queue/client.go` (this repo) — `Client` struct, `NewClient`, `EnqueueImageConvert`/`EnqueueWebhookDeliver` — read directly
- `internal/reconciler/reconciler.go` (this repo) — `Sweeper`, `jobStore`, `enqueuer`, `sweep()` — read directly
- `internal/reconciler/reconciler_test.go` (this repo) — `fakeStore`, `fakeEnqueuer`, existing test patterns — read directly
- `internal/jobs/repo.go` (this repo) — `FindStale`, `RequeueStale`, `RecoveryCount`, `transition`, `detailActionRecovery` — read directly
- `internal/jobs/repo_test.go`, `internal/webhook/repo_test.go`, `internal/queue/queue_test.go` (this repo) — live-DB/live-Redis test conventions (`newTestRepo`/`newTestPool`, `t.Skip` guards, `TestEnqueueImageConvert`, `TestImageUniqueTTL`, `TestFindStale`) — read directly
- `internal/db/migrations/0001_init.sql`, `0003_webhook_dead_letter.sql` (this repo) — schema, existing indexes, migration-comment conventions — read directly
- `internal/webhook/deliver.go`, `internal/webhook/repo.go`, `internal/webhook/webhook.go` (this repo) — 10s HTTP timeout, `MarkDeadLetter`, `Delivery` struct — read directly
- `internal/worker/worker.go` (this repo) — `HandleWebhookDeliver`'s lack of a wrapping per-attempt context timeout — read directly, lines 160-210
- `/Users/apaderin/go/pkg/mod/github.com/hibiken/asynq@v0.26.0/internal/base/base.go:200-206` (vendored dependency source, pinned version matching go.mod) — `UniqueKey(qname, tasktype, payload)` composition, confirming per-queue lock namespace isolation `[VERIFIED: asynq v0.26.0 vendored source]`
- `/Users/apaderin/go/pkg/mod/github.com/hibiken/asynq@v0.26.0/client.go:382` — confirms `uniqueKey = base.UniqueKey(opt.queue, task.Type(), task.Payload())` is computed at enqueue time using the task's actual queue `[VERIFIED: asynq v0.26.0 vendored source]`
- Local shell verification (this session): `docker compose ps` confirms Postgres/Redis/MinIO/api/worker all running; `go test ./internal/reconciler/... ./internal/queue/... ./internal/jobs/...` passes against the live stack with `DATABASE_URL`/`REDIS_ADDR` set `[VERIFIED: local execution]`

### Secondary (MEDIUM confidence)
- [Unique Tasks · hibiken/asynq Wiki](https://github.com/hibiken/asynq/wiki/Unique-Tasks) — corroborates the `{type, payload, queue}` uniqueness scope and TTL/`ErrDuplicateTask` semantics described in the wiki, cross-verified against and superseded in confidence by direct vendored-source inspection above

### Tertiary (LOW confidence)
- None — every load-bearing claim in this document was either read directly from this repository's own source/tests or verified against the exact pinned dependency version's vendored source.

## Metadata

**Confidence breakdown:**
- Standard stack: N/A (no new stack) — HIGH by default, nothing to verify
- Architecture: HIGH — every pattern is a direct, source-verified mirror of an existing implemented pattern in this same repo
- Pitfalls: HIGH — all five pitfalls were derived from tracing actual code paths (jitter in `WebhookRetryDelay`, the hardcoded `uniqueTTLSafetyMargin`, `RequeueStale`'s non-reset of `created_at`) rather than generic domain knowledge

**Research date:** 2026-07-08
**Valid until:** No expiry driver — this research is tied to this repository's current state (asynq v0.26.0, pgx v5.10.0 pinned in go.mod, schema through migration 0003), not to an external, time-decaying ecosystem. Re-verify only if `go.mod`'s asynq/pgx versions change or if `internal/queue/queue.go`'s retry schedules/constants are edited before this phase is planned.
