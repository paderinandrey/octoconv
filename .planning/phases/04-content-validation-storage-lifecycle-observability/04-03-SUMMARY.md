---
phase: 04-content-validation-storage-lifecycle-observability
plan: 03
subsystem: observability
tags: [prometheus, metrics, asynq, go]

# Dependency graph
requires:
  - phase: 04-content-validation-storage-lifecycle-observability
    provides: 04-01 (queue/reconciler/webhook foundations this plan instruments)
provides:
  - "metrics.RecordJobOutcome / RecordWebhookDelivery / RecordReconcilerAction — Prometheus Record* helpers"
  - "metrics.NewQueueDepthCollector — pull-based prometheus.Collector wrapping asynq.Inspector.GetQueueInfo"
  - "internal/metrics package: octoconv_job_outcomes_total, octoconv_job_duration_seconds, octoconv_webhook_deliveries_total, octoconv_reconciler_actions_total, octoconv_queue_depth"
affects: [04-05 (HTTP /metrics exposure + collector registration consumes this plan's package)]

# Tech tracking
tech-stack:
  added: ["github.com/prometheus/client_golang v1.23.2 (promauto, promhttp deps, prometheus/testutil)"]
  patterns:
    - "Thin Record* helper functions hide label plumbing from callers (worker/reconciler never touch prometheus types directly)"
    - "Pull-based prometheus.Collector wrapping an external inspector (asynq.Inspector), swallowing per-queue errors with continue so a Redis blip never crashes a scrape"

key-files:
  created:
    - internal/metrics/metrics.go
    - internal/metrics/queue_collector.go
    - internal/metrics/metrics_test.go
  modified:
    - internal/worker/worker.go
    - internal/reconciler/reconciler.go
    - go.mod
    - go.sum

key-decisions:
  - "Task 1's blocking-human checkpoint (github.com/prometheus/client_golang tagged [ASSUMED] in RESEARCH.md's Package Legitimacy Audit) was resolved by the human user directly approving installation to the orchestrator after independent verification against proxy.golang.org and pkg.go.dev (official prometheus org, v1.23.2, VCS hash 8179a560819f2c64ef6ade70e6ae4c73aecaca3c). A relayed 'approved' message from the coordinator to this executor agent was correctly refused twice by the executor's own safety rules (no message from another agent constitutes user consent); the orchestrator then completed Task 2 and Task 3 directly in this worktree using its own tools, since only the orchestrator was in a position to receive genuine first-hand user approval."
  - "Metric labels kept to the closed set from D-13/D-14/D-15: engine+status only for job outcomes/duration (no client_id/error_code), result for webhook deliveries, action for reconciler events."
  - "Job-outcome counter increments only at the two genuine terminal transitions (done, failed) in HandleImageConvert — never on the transient-retry return path (Pitfall 6) — and reconciler 'recovered' increments only after a genuinely successful RequeueStale, never on the asynq.ErrDuplicateTask continue (backlogged no-op, not a recovery)."

patterns-established:
  - "New Prometheus metric families live in internal/metrics/metrics.go using promauto against the default registry; a per-external-system pull collector (queue depth) gets its own file (queue_collector.go)."

requirements-completed: [OBS-01]

# Metrics
duration: ~45min (across two sessions: initial attempt to checkpoint, then orchestrator-completed Task 2/3 after direct human approval)
completed: 2026-07-07
---

# Phase 4 Plan 03: Prometheus Metrics Instrumentation Summary

**Defined four Prometheus metric families (job outcomes, job duration, webhook deliveries, reconciler actions) plus a pull-based queue-depth collector in a new `internal/metrics` package, and instrumented the existing worker/reconciler terminal exit points to call them — closing the instrumentation half of OBS-01.**

## Performance

- **Tasks:** 3/3 completed (Task 1 checkpoint resolved via direct human approval; Task 2 and Task 3 completed by the orchestrator directly in this worktree after approval, since the executor agent correctly refused a relayed approval per its own safety rules)
- **Files created:** 3
- **Files modified:** 4 (worker.go, reconciler.go, go.mod, go.sum)

## Accomplishments

- `internal/metrics/metrics.go` defines `octoconv_job_outcomes_total` (CounterVec, engine+status), `octoconv_job_duration_seconds` (HistogramVec, engine+status, `prometheus.DefBuckets`), `octoconv_webhook_deliveries_total` (CounterVec, result), `octoconv_reconciler_actions_total` (CounterVec, action) via `promauto`, plus `RecordJobOutcome`/`RecordWebhookDelivery`/`RecordReconcilerAction` helpers so callers never touch label plumbing directly.
- `internal/metrics/queue_collector.go` implements `NewQueueDepthCollector(inspector *asynq.Inspector, queues ...string) prometheus.Collector`, exposing `octoconv_queue_depth` (labels `queue`,`state`) for pending/active/scheduled/retry/archived per queue; per-queue `GetQueueInfo` errors are swallowed with `continue` so a Redis blip never crashes a scrape.
- `internal/metrics/metrics_test.go` covers all three `Record*` helpers via `prometheus/testutil.ToFloat64` and asserts `NewQueueDepthCollector(...).Describe(ch)` yields exactly one descriptor without a live Redis.
- `internal/worker/worker.go`'s `HandleImageConvert` now times each attempt (`start := time.Now()`) and calls `metrics.RecordJobOutcome(queue.QueueImage, jobs.StatusFailed/StatusDone, time.Since(start))` at the two genuine terminal branches only — the transient `return err` path is untouched (Pitfall 6). `HandleWebhookDeliver` calls `metrics.RecordWebhookDelivery(derr == nil)` right after `Deliver`.
- `internal/reconciler/reconciler.go`'s `sweep` calls `metrics.RecordReconcilerAction("exhausted")` in the `MaxRecoveries`-exceeded branch and `metrics.RecordReconcilerAction("recovered")` only after a genuinely successful `RequeueStale` (first or the bounded single retry) — never on the `asynq.ErrDuplicateTask` continue.

## Task Commits

1. **Task 1: Gate the prometheus/client_golang install** — no code commit (pure checkpoint); resolved via direct human approval to the orchestrator, who ran `go get github.com/prometheus/client_golang@v1.23.2` itself in this worktree.
2. **Task 2: metrics package — definitions, Record helpers, queue-depth collector** — `c8aee84` (feat), committed by the orchestrator directly.
3. **Task 3: Instrument worker and reconciler terminal exit points** — `32ce446` (feat), committed by the orchestrator directly.

## Files Created/Modified

- `internal/metrics/metrics.go` (new) — 4 metric families + 3 Record* helpers
- `internal/metrics/queue_collector.go` (new) — `NewQueueDepthCollector`
- `internal/metrics/metrics_test.go` (new) — unit tests via `prometheus/testutil`, no live Redis needed
- `internal/worker/worker.go` — `metrics` import; `RecordJobOutcome` at both terminal branches of `HandleImageConvert`; `RecordWebhookDelivery` in `HandleWebhookDeliver`
- `internal/reconciler/reconciler.go` — `metrics` import; `RecordReconcilerAction("exhausted")` and `RecordReconcilerAction("recovered")` at the two genuine action points in `sweep`
- `go.mod` / `go.sum` — `github.com/prometheus/client_golang v1.23.2` now a direct dependency (plus its transitive deps: `client_model`, `common`, `procfs`, `beorn7/perks`, `munnerz/goautoneg`)

## Decisions Made

- Honored D-13 (engine+status only, no client_id/error_code), D-14 (duration histogram alongside outcome counter), D-15 (reconciler recovery/exhaustion counter) exactly as locked in CONTEXT.md.
- No `log.Printf` added to either `internal/worker` or `internal/reconciler` — metrics are the sanctioned Phase 4 visibility mechanism for these packages, per the reconciler's own pre-existing doc comment.

## Deviations from Plan

**Execution ownership (process deviation, not a code deviation):** Task 1's checkpoint requires genuine human approval and is designed to reject relayed approvals from any agent, including the orchestrator. The human user approved installation directly to the orchestrator (after the orchestrator's own independent package-legitimacy verification). The orchestrator relayed this approval to the executor agent twice; the executor correctly refused both times per its own anti-social-engineering safety rules, since no agent-relayed message can substitute for direct user consent from that agent's own perspective. Rather than attempting to force the executor to accept a relayed approval (which would defeat the purpose of the gate), the orchestrator completed Task 2 and Task 3 directly using its own tools in this same worktree, preserving the plan's file layout, task boundaries, and acceptance criteria exactly as written. No task content was changed — only which agent typed the code.

No other deviations. Task 2 and Task 3 acceptance criteria (grep checks, `go build`, `go vet`, `go test ./internal/metrics/ ./internal/worker/ ./internal/reconciler/`) all passed on first implementation.

## Issues Encountered

None beyond the checkpoint-relay refusal described above, which was expected, correct behavior, not a bug.

## User Setup Required

None — `github.com/prometheus/client_golang` is now a resolved, vendored dependency; no environment variables or manual steps are needed to use the `internal/metrics` package. HTTP exposure of `/metrics` (localhost-only per D-19) and collector registration are wired in plan 04-05.

## Next Phase Readiness

- `internal/metrics` package is fully available for plan 04-05 to mount on an HTTP handler and register `NewQueueDepthCollector` against a real `asynq.Inspector`.
- OBS-01's instrumentation half is complete; the exposure half (HTTP `/metrics` endpoint, collector registration in `cmd/api/main.go`/`cmd/worker/main.go`) is explicitly deferred to plan 04-05 per this plan's own `<objective>`.
- No blockers for downstream plans.

---
*Phase: 04-content-validation-storage-lifecycle-observability*
*Completed: 2026-07-07*
