---
phase: 27-keda-autoscaling
plan: 01
subsystem: infra
tags: [prometheus, asynq, redis, kubernetes, keda, graceful-shutdown]

# Dependency graph
requires:
  - phase: 24-helm-chart-core
    provides: per-engine-class terminationGracePeriodSeconds (image 150s, document 330s, html 90s, webhook 60s) that this plan's ShutdownTimeout values must stay under
provides:
  - octoconv_queue_depth registered once, on the always-on api process, for all four queues (image/document/html/webhook) — the hard prerequisite for any KEDA ScaledObject at genuine 0 replicas
  - Per-class asynq ShutdownTimeout on all four workers, removing asynq's silent 8s-default cap on graceful shutdown (Phase-28 blocker closed)
  - Live compose-E2E proof (TestQueueDepthMetricRelocationE2E) that the relocation actually works against real Redis, before any k8s work
affects: [28-load-proof, 27-02-keda-scaledobjects]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Single source of truth for pull-based Prometheus collectors: register once on the never-scaled process, never per-scalable-worker."
    - "Per-class asynq.Config.ShutdownTimeout derived from the same env var used for the engine timeout, plus a fixed safety margin, always strictly less than that class's pod terminationGracePeriodSeconds."
    - "E2E test setup helper for asynq: seed asynq's internal Redis queue-registry SET directly (SADD asynq:queues) rather than relying on it being populated as a side effect of a real enqueue — needed because CurrentStats returns NotFound (silently skipped) for never-used queues."

key-files:
  created: []
  modified:
    - cmd/api/main.go
    - cmd/worker/main.go
    - cmd/document-worker/main.go
    - cmd/chromium-worker/main.go
    - cmd/webhook-worker/main.go
    - internal/metrics/metrics_test.go
    - internal/e2e/e2e_test.go
    - docker-compose.e2e.yml

key-decisions:
  - "Queue-depth collector relocates to api, registered once for all four queue name constants in a single prometheus.MustRegister call, matching every worker's existing precedent of never calling Inspector.Close()."
  - "ShutdownTimeout values: image=ENGINE_TIMEOUT+10s (130s), document=DOCUMENT_ENGINE_TIMEOUT+10s (310s), html=HTML_ENGINE_TIMEOUT+10s (70s), webhook=flat 30s (no single clean source env var; covers one HTTP attempt + presign/Postgres read with margin) — each strictly under its class's pod terminationGracePeriodSeconds."
  - "E2E live-checks only the image worker (:9191) for collector absence, not all four worker binaries — Task 1's static grep+vet gate already proves the other three; documented in-code and here per checker WARNING 2 scope guidance."
  - "docker-compose.e2e.yml metrics-port publish (api 9190:9090, worker 9191:9090, METRICS_ADDR=0.0.0.0:9090) is validation-only; base docker-compose.yml is untouched (still 127.0.0.1:9090, unpublished)."

patterns-established:
  - "Relocation pattern for pull-based Prometheus collectors when introducing KEDA: move registration to the process that's guaranteed never to scale to zero, keep the /metrics HTTP listener itself unchanged everywhere."

requirements-completed: [KEDA-01]

# Metrics
duration: 20min
completed: 2026-07-16
---

# Phase 27 Plan 01: Relocate queue-depth metric + fix asynq shutdown timeout Summary

**Moved `octoconv_queue_depth` from the four worker binaries to the always-on api process (single `prometheus.MustRegister` call for all four queues) and set per-class `asynq.Config.ShutdownTimeout` on all four workers, both live-verified against the full compose stack.**

## Performance

- **Duration:** ~20 min (first task commit to last)
- **Started:** 2026-07-16T20:26:50Z (per STATE.md phase start)
- **Completed:** 2026-07-16T20:50:34Z
- **Tasks:** 3 completed (Task 3 required one auto-fix, see Deviations)
- **Files modified:** 8

## Accomplishments

- `cmd/api/main.go` now registers `metrics.NewQueueDepthCollector` for `queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueWebhook` in one call right after `redisOpt` is obtained — the always-on api process is now the single source of truth KEDA (Phase 27 Plan 02) will scrape, even when a worker Deployment is scaled to genuine 0 replicas.
- All four worker mains (`cmd/worker`, `cmd/document-worker`, `cmd/chromium-worker`, `cmd/webhook-worker`) no longer register the collector; their `/metrics` HTTP listener and promauto job/duration metrics are unchanged.
- Each worker now sets `ShutdownTimeout` on its `asynq.Config`, strictly under its class's Phase-24 pod `terminationGracePeriodSeconds`, removing asynq's silent 8s default that made those grace periods dead config.
- D-03 validated: a unit test proves the collector's generic four-queue construction never panics, and a live compose-E2E test (`TestQueueDepthMetricRelocationE2E`) proves api's `:9190/metrics` serves `octoconv_queue_depth` for all four queue labels while the image worker's `:9191/metrics` returns HTTP 200 without it.
- The plan's mandatory live-confirmation step (`docker compose ... up -d --build` → live E2E run → `down -v`) was executed exactly once, following OrbStack discipline (verified no k8s octoconv workloads first, tore the compose stack down afterward, confirmed zero `octoconv-*` containers remain).

## Task Commits

Each task was committed atomically:

1. **Task 1: Relocate the queue-depth collector to cmd/api/main.go (D-01/D-02)** - `f92141a` (feat)
2. **Task 2: Set per-class asynq ShutdownTimeout in the four workers (Pattern 2)** - `c9af169` (feat)
3. **Task 3: Validate relocation (D-03) — unit test + compose-E2E metrics assertion** - `eaef38d` (test), `b5526ff` (fix — Rule 1 auto-fix found during live verification)

**Plan metadata:** committed separately by the orchestrator after merge (worktree mode; STATE.md/ROADMAP.md not touched here).

## Files Created/Modified

- `cmd/api/main.go` - Registers the four-queue `NewQueueDepthCollector`; adds `asynq`, `prometheus`, `internal/metrics` imports.
- `cmd/worker/main.go` - Removes collector registration + unused `prometheus`/`internal/metrics` imports; adds `ShutdownTimeout: ENGINE_TIMEOUT+10s`.
- `cmd/document-worker/main.go` - Same removal; adds `ShutdownTimeout: DOCUMENT_ENGINE_TIMEOUT+10s`.
- `cmd/chromium-worker/main.go` - Same removal; adds `ShutdownTimeout: HTML_ENGINE_TIMEOUT+10s`.
- `cmd/webhook-worker/main.go` - Same removal; adds flat `ShutdownTimeout: 30 * time.Second`.
- `internal/metrics/metrics_test.go` - Adds `TestNewQueueDepthCollectorDescribeAllFourQueues`, extending existing coverage to the four-queue construction now used on api.
- `internal/e2e/e2e_test.go` - Adds `TestQueueDepthMetricRelocationE2E` and the `seedQueueRegistry` helper (see Deviations).
- `docker-compose.e2e.yml` - Adds validation-only `api` port `9190:9090` and a new `worker` override (`METRICS_ADDR=0.0.0.0:9090`, port `9191:9090`).

## Decisions Made

- All four `ShutdownTimeout` values derive from the same env var already used for that class's engine timeout (image/document/html), plus a fixed +10s margin, keeping the derivation self-scaling if the env var changes later — mirrors the existing `*UniqueTTL` derivation pattern in `internal/queue/queue.go`. Webhook uses a flat 30s since there is no single clean source env var for it (documented in-code, matches RESEARCH.md).
- The E2E test's worker-side absence check is scoped to the image worker only (`:9191`), not all four workers — a deliberate, plan-documented choice (checker WARNING 2): Task 1's static `grep -rl "NewQueueDepthCollector" cmd/` + no-unused-import `go vet` already prove the other three workers' identical 3-line deletion.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] E2E test failed on a fresh compose stack: asynq queues are not "known" until first real enqueue**
- **Found during:** Task 3, mandatory live compose confirmation step (plan `<verification>`)
- **Issue:** `TestQueueDepthMetricRelocationE2E` failed with "api :9190/metrics does not contain octoconv_queue_depth at all". Root cause: asynq's `CurrentStats`/`GetQueueInfo` calls `queueExists(qname)`, which checks Redis SET `asynq:queues` — populated ONLY on a queue's first real enqueue (`internal/rdb` `SAdd(ctx, base.AllQueues, msg.Queue)`), never at `asynq.NewServer(Queues: ...)` startup. On a freshly built compose stack with zero jobs ever submitted, all four queues are unknown to asynq, so `queueDepthCollector.Collect()` silently skips every one of them (its documented fail-safe behavior for a Redis blip also fires for "queue never used"). The plan's stated assumption ("series exist at value 0 even for empty queues, since GetQueueInfo succeeds against live compose Redis") does not hold for a truly pristine stack.
- **Fix:** Added `seedQueueRegistry(t, ...)` to `internal/e2e/e2e_test.go`, which `SADD`s the four queue names directly into Redis's `asynq:queues` set via a plain `redis.NewClient` (REDIS_ADDR, default `localhost:6379` matching the already host-published compose port) — zero tasks created, no worker processing triggered, purely priming the same registry state that a real first job submission would create naturally in production.
- **Files modified:** `internal/e2e/e2e_test.go`
- **Verification:** Live-verified against the full compose stack: `go test ./internal/e2e/... -run QueueDepthMetricRelocation` passed after the fix; manual `curl localhost:9190/metrics | grep octoconv_queue_depth` confirmed all 20 expected series (4 queues × 5 states) at value 0, and `curl localhost:9191/metrics` confirmed zero `octoconv_queue_depth` lines.
- **Committed in:** `b5526ff` (separate commit from the `eaef38d` test-scaffolding commit, since it was found and fixed during the mandatory live-verification step that runs after the static test additions)

---

**Total deviations:** 1 auto-fixed (Rule 1 - bug, found during mandatory live verification, not a task-execution blocker)
**Impact on plan:** Necessary correctness fix for the D-03 live proof to actually hold; no scope creep — the fix is entirely confined to E2E test setup, no production code touched.

## Issues Encountered

- The first `docker compose ... up -d --build` attempt failed with a container-name conflict: stale `Exited` containers (`octoconv-db`, `octoconv-redis`, `octoconv-minio`) from a prior session, 22h old, owned by the main-repo compose project (`working_dir=/Users/apaderin/dev/octoconv`), were left over and collided with the worktree's compose run (container names are hardcoded, not project-scoped). Resolved with `docker compose -f docker-compose.yml down -v` from the main repo directory (not `docker rm`, per the auto-mode classifier's guidance to use the standard teardown path rather than pattern-matched container removal), then the worktree's `up -d` succeeded cleanly.

## TDD Gate Compliance

Task 3 was marked `tdd="true"`. Gate sequence in git log: `test(27-01): validate queue-depth relocation...` (`eaef38d`) precedes `fix(27-01): seed asynq queue registry...` (`b5526ff`). No separate `feat(...)` GREEN commit was needed — Task 3 adds zero production code (its `<files>` are test files + a compose config file only); the collector behavior under test was already correctly implemented in Task 1/2. The unit test extension (`TestNewQueueDepthCollectorDescribeAllFourQueues`) passed immediately on first run, since it exercises the collector's pre-existing generic variadic-queue handling rather than new logic — documented here rather than silently treated as a RED-phase failure, per the fail-fast rule's intent (a passing "RED" test here reflects correct pre-existing behavior, not a mis-targeted test).

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- KEDA-01's hard prerequisite is now shipped and live-proven: `octoconv_queue_depth` for all four queues is reachable on the never-scaled api process, so Phase 27 Plan 02 can write ScaledObjects against it without risk of a worker at 0 replicas losing its own metric exposition.
- Phase 28's blocker (asynq's 8s ShutdownTimeout default silently capping Phase 24's pod grace periods) is closed for all four worker classes.
- No blockers carried forward. The `seedQueueRegistry` E2E helper is a test-only artifact; it has no relevance to k8s manifests, which will need their own equivalent consideration (a ScaledObject querying `octoconv_queue_depth{queue="X"}` before any job of class X has ever run will see no series — Prometheus scalers typically default to treating absent series as 0, but this is worth a explicit note for Plan 02's ScaledObject `minReplicaCount`/`ignoreNullValues` design).

---
*Phase: 27-keda-autoscaling*
*Completed: 2026-07-16*
