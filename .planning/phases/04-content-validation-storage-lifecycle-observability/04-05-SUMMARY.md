---
phase: 04-content-validation-storage-lifecycle-observability
plan: 05
subsystem: observability
tags: [prometheus, promhttp, asynqmon, docker-compose, go]

# Dependency graph
requires:
  - phase: 04-content-validation-storage-lifecycle-observability
    provides: "04-03 (internal/metrics package: Record* helpers, NewQueueDepthCollector)"
provides:
  - "cmd/api/main.go and cmd/worker/main.go each serve promhttp.Handler() on a second, localhost-only METRICS_ADDR listener (default 127.0.0.1:9090), separate from the public API_ADDR"
  - "cmd/worker/main.go registers metrics.NewQueueDepthCollector against the image/webhook queues via prometheus.MustRegister"
  - "docker-compose.yml asynqmon service pinned to hibiken/asynqmon:0.7.2, bound to 127.0.0.1:8980:8080 only, depending on a healthy redis"
  - "METRICS_ADDR documented in .env.example and set on both api/worker compose services without a host port publish"
affects: []

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Second http.Server-per-process pattern for an internal-only surface: same goroutine + ListenAndServe + errors.Is(http.ErrServerClosed) + graceful Shutdown idiom as the primary listener, mounted on a distinct localhost-bound address"

key-files:
  created: []
  modified:
    - cmd/api/main.go
    - cmd/worker/main.go
    - .env.example
    - docker-compose.yml

key-decisions:
  - "asynqmon pinned to hibiken/asynqmon:0.7.2 — verified directly against the Docker Hub tags API at execution time (23 tags total; 0.7.2 pushed 2023-07-04 is the newest non-latest/non-master tag), not :latest, per T-04-15 supply-chain hygiene"
  - "docker-compose.yml's asynqmon-service edit was split into two commits (METRICS_ADDR wiring in Task 1's commit, the asynqmon block itself in Task 2's commit) by temporarily removing the adjacent asynqmon block, committing Task 1, then re-adding it for Task 2 — git's default 3-line diff context merged the two changes into a single hunk, making git add -p insufficient to separate them cleanly"

patterns-established: []

requirements-completed: []  # OBS-01/OBS-03 code changes complete; NOT marked complete pending Task 3 human-verify checkpoint

# Metrics
duration: ~25min (Tasks 1-2; Task 3 is a pending checkpoint)
completed: 2026-07-07
---

# Phase 4 Plan 05: Metrics Exposure + Asynqmon Dashboard Summary

**Second localhost-only `/metrics` HTTP listener mounted in both `cmd/api/main.go` and `cmd/worker/main.go` (promhttp.Handler(), METRICS_ADDR default 127.0.0.1:9090), queue-depth collector registered in the worker, and a pinned `hibiken/asynqmon:0.7.2` dashboard service bound to 127.0.0.1:8980 — checkpoint pending human verification of both surfaces.**

## Performance

- **Tasks:** 2/3 completed; Task 3 is a `checkpoint:human-verify` gate=blocking task, reached and reported per plan (`autonomous: false`)
- **Files modified:** 4 (`cmd/api/main.go`, `cmd/worker/main.go`, `.env.example`, `docker-compose.yml`)

## Accomplishments

- `cmd/api/main.go`: added a second `*http.Server` bound to `METRICS_ADDR` (default `127.0.0.1:9090`) serving `promhttp.Handler()`, started in its own goroutine with the same `log.Printf`/`errors.Is(http.ErrServerClosed)` idiom as the existing `httpSrv`, and shut down alongside it in the `<-ctx.Done()` block.
- `cmd/worker/main.go`: added `net/http`, `prometheus`, `promhttp`, and `internal/metrics` imports; registered `prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage, queue.QueueWebhook))` before starting the asynq server; added the identical second `METRICS_ADDR` listener + goroutine + graceful `Shutdown` (the worker previously had no `net/http` server at all).
- `.env.example`: new `# Observability` section documenting `METRICS_ADDR=127.0.0.1:9090` with a D-19 citation.
- `docker-compose.yml`: `METRICS_ADDR: "127.0.0.1:9090"` added to both `api` and `worker` service environment blocks with no host port publish (container-local only, per D-19); new `asynqmon` service pinned to `hibiken/asynqmon:0.7.2`, `depends_on: redis: condition: service_healthy`, `environment: REDIS_ADDR: redis:6379`, `ports: - "127.0.0.1:8980:8080"` (loopback-only, per D-18).
- Verified `hibiken/asynqmon:0.7.2` exists as a specific published tag via a direct query against the Docker Hub v2 tags API (`hub.docker.com/v2/repositories/hibiken/asynqmon/tags`) before writing it into `docker-compose.yml` — 23 tags total, `0.7.2` (pushed 2023-07-04) is the newest non-`latest`/non-`master` tag.

## Task Commits

1. **Task 1: /metrics listeners on API + worker, queue collector, METRICS_ADDR config** — `828f748` (feat)
2. **Task 2: asynqmon dashboard service (localhost-only) in docker-compose** — `bcd56f8` (feat)
3. **Task 3: Verify /metrics scrape + asynqmon dashboard reachability** — CHECKPOINT REACHED, not yet resolved (see below)

## Files Created/Modified

- `cmd/api/main.go` — second `http.Server` on `METRICS_ADDR` (default `127.0.0.1:9090`), `promhttp` import, goroutine + graceful shutdown
- `cmd/worker/main.go` — `net/http`, `prometheus`, `promhttp`, `internal/metrics` imports; `prometheus.MustRegister(metrics.NewQueueDepthCollector(...))`; second `http.Server` on `METRICS_ADDR` with goroutine + graceful shutdown
- `.env.example` — new `# Observability` section, `METRICS_ADDR=127.0.0.1:9090`
- `docker-compose.yml` — `METRICS_ADDR` on `api`/`worker` service blocks (no host port publish); new `asynqmon` service pinned to `hibiken/asynqmon:0.7.2`, bound to `127.0.0.1:8980:8080`

## Decisions Made

- Pinned `hibiken/asynqmon:0.7.2` (not `:latest`) after confirming it exists on Docker Hub directly via the tags API at execution time — satisfies the `user_setup` requirement in the plan frontmatter ("Confirm a specific published hibiken/asynqmon tag exists before compose up") and T-04-15's supply-chain-hygiene mitigation.
- Split the single logical docker-compose.yml diff into two commits (Task 1: METRICS_ADDR env wiring; Task 2: the asynqmon service block) by temporarily removing the asynqmon block before Task 1's commit and re-adding it for Task 2's commit — git's 3-line default diff context otherwise merges the two changes into one hunk (verified via `git diff -U0 | grep -c '^@@'` going from 3 to a single combined hunk when both were present), making per-task atomic commits impossible via `git add -p` alone.

## Deviations from Plan

None — plan executed exactly as written for Tasks 1 and 2. Both tasks' acceptance criteria were verified via the exact grep/build/vet commands specified in the plan before committing.

## Issues Encountered

None for Tasks 1-2. `go build ./... && go vet ./...` passed clean; all acceptance-criteria greps matched the expected counts (`promhttp.Handler` ×1 in each of `cmd/api/main.go`/`cmd/worker/main.go`, `NewQueueDepthCollector` ×1 in `cmd/worker/main.go`, `METRICS_ADDR` ×1 in `.env.example` and ×2 in `docker-compose.yml`, `hibiken/asynqmon` ×1 and `127.0.0.1:8980:8080` ×1 in `docker-compose.yml`, no `:latest` tag, no `9090` under any `ports:` list).

## CHECKPOINT REACHED

**Type:** human-verify
**Plan:** 04-05
**Progress:** 2/3 tasks complete

### Completed Tasks

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | /metrics listeners on API + worker, queue collector, METRICS_ADDR config | `828f748` | cmd/api/main.go, cmd/worker/main.go, .env.example, docker-compose.yml |
| 2 | asynqmon dashboard service (localhost-only) in docker-compose | `bcd56f8` | docker-compose.yml |

### Current Task

**Task 3:** Verify /metrics scrape + asynqmon dashboard reachability
**Status:** awaiting human verification (gate="blocking")

### Checkpoint Details

**What was built:** Both processes now expose Prometheus `/metrics` on a localhost-only listener (`METRICS_ADDR=127.0.0.1:9090`, container-local, not host-published) and the compose stack includes an asynqmon dashboard bound to `127.0.0.1:8980`.

**How to verify:**
1. `docker-compose up --build -d` and wait for postgres/redis/minio healthchecks to pass.
2. Metrics (OBS-01): `docker-compose exec worker wget -qO- 127.0.0.1:9090/metrics | grep octoconv_` — confirm `octoconv_queue_depth`, `octoconv_job_outcomes_total`, `octoconv_job_duration_seconds`, `octoconv_webhook_deliveries_total`, `octoconv_reconciler_actions_total` appear. Repeat for the api container: `docker-compose exec api wget -qO- 127.0.0.1:9090/metrics | head`.
3. Localhost scoping (D-19): confirm the metrics port is NOT reachable from the host — `curl -sS --max-time 3 localhost:9090/metrics` should fail/refuse (no host publish).
4. asynqmon (OBS-03): open http://127.0.0.1:8980 in a browser and confirm the `image` and `webhook` queues are listed. Confirm it is NOT reachable on the host's external interface (only 127.0.0.1).
5. Optional end-to-end: submit one conversion job (authenticated POST /v1/jobs) and confirm `octoconv_job_outcomes_total{status="done"}` increments and the job is visible in asynqmon.

### Awaiting

A human operator must run the verification steps above and respond "approved" (or describe what failed) before this plan can be marked complete. This SUMMARY.md reflects Tasks 1-2 only; requirements OBS-01/OBS-03 are NOT marked complete pending this checkpoint.

## User Setup Required

**External Docker Compose stack must be brought up and manually inspected** to complete Task 3's checkpoint — see the verification steps above. No environment variables beyond what is already documented in `.env.example`/`docker-compose.yml` are required.

## Next Phase Readiness

- Tasks 1-2 (code + compose changes) are complete, committed, and pass `go build`/`go vet`/all plan-specified grep checks.
- Task 3 (human-verify checkpoint) is unresolved — the plan is NOT complete. A follow-up execution pass (after human approval) should mark `requirements-completed: [OBS-01, OBS-03]` and finalize this SUMMARY, or the orchestrator should resolve the checkpoint per its own protocol.
- No blockers beyond the pending checkpoint itself.

---
*Phase: 04-content-validation-storage-lifecycle-observability*
*Completed: pending Task 3 checkpoint (2026-07-07, Tasks 1-2 complete)*
