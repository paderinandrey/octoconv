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
  - "docker-compose.yml asynqmon service pinned to hibiken/asynqmon:0.7.2, bound to 127.0.0.1:8980:8080 only, depending on a healthy redis, platform pinned to linux/amd64 (no arm64 image published)"
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
  - "asynqmon pinned to platform: linux/amd64 (discovered live during Task 3 verification) — hibiken/asynqmon publishes amd64-only images for both 0.7.2 and :latest; without this, docker compose pull fails outright on Apple Silicon/arm64 hosts with 'no matching manifest'"
  - "Task 3's live verification was performed by the orchestrator directly against a temporarily-substituted copy of the shared dev docker-compose stack (stopped the running main-checkout stack, brought up the worktree's code under the same compose project name so named Postgres/MinIO volumes were reused, verified, tore it down, and restored the original stack) rather than by this executor agent, following the same resolution pattern established for plan 04-03's checkpoint: this agent is correctly unable to authenticate a relayed approval from any other agent, so the orchestrator — who received the human's direct, first-hand sign-off after presenting concrete evidence — completed the verification and is finalizing this SUMMARY.md"

patterns-established: []

requirements-completed: [OBS-01, OBS-03]

# Metrics
duration: ~90min (Tasks 1-2 ~25min; Task 3 checkpoint + live verification + unrelated pre-existing bug discovery/fix ~65min)
completed: 2026-07-07
---

# Phase 4 Plan 05: Metrics Exposure + Asynqmon Dashboard Summary

**Second localhost-only `/metrics` HTTP listener mounted in both `cmd/api/main.go` and `cmd/worker/main.go` (promhttp.Handler(), METRICS_ADDR default 127.0.0.1:9090), queue-depth collector registered in the worker, and a pinned `hibiken/asynqmon:0.7.2` dashboard service bound to 127.0.0.1:8980 — all three verified live end-to-end (real conversion job, real metrics scrape, real dashboard query).**

## Performance

- **Tasks:** 3/3 completed
- **Files modified:** 4 (`cmd/api/main.go`, `cmd/worker/main.go`, `.env.example`, `docker-compose.yml`)

## Accomplishments

- `cmd/api/main.go`: second `*http.Server` bound to `METRICS_ADDR` (default `127.0.0.1:9090`) serving `promhttp.Handler()`, started in its own goroutine with the same `log.Printf`/`errors.Is(http.ErrServerClosed)` idiom as the existing `httpSrv`, shut down alongside it in the `<-ctx.Done()` block.
- `cmd/worker/main.go`: `net/http`, `prometheus`, `promhttp`, and `internal/metrics` imports added; `prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage, queue.QueueWebhook))` registered before starting the asynq server; identical second `METRICS_ADDR` listener + goroutine + graceful `Shutdown` (the worker previously had no `net/http` server at all).
- `.env.example`: new `# Observability` section documenting `METRICS_ADDR=127.0.0.1:9090` with a D-19 citation.
- `docker-compose.yml`: `METRICS_ADDR: "127.0.0.1:9090"` on both `api`/`worker` (no host port publish, per D-19); new `asynqmon` service pinned to `hibiken/asynqmon:0.7.2` + `platform: linux/amd64`, `depends_on: redis: condition: service_healthy`, `environment: REDIS_ADDR: redis:6379`, `ports: - "127.0.0.1:8980:8080"` (loopback-only, per D-18).
- **Task 3 live verification, performed end-to-end:**
  1. Built and ran the full stack from this worktree's code (stopped the shared dev stack first, reused its named Postgres/MinIO volumes under the same compose project name to avoid data loss, tore it down and restored the original stack afterward).
  2. Created a test client via `cmd/manage-clients`, submitted a real `POST /v1/jobs` (PNG→WebP), confirmed `status: "done"` via `GET /v1/jobs/{id}`.
  3. Confirmed via `docker exec ... /dev/tcp` raw HTTP GET (no wget/curl in the minimal runtime image) that the worker's `/metrics` now shows real series: `octoconv_job_outcomes_total{engine="image",status="done"} 1`, a populated `octoconv_job_duration_seconds` histogram, and `octoconv_queue_depth{queue="image",state=...}` gauges (all zero post-completion, as expected) — confirming both 04-03's instrumentation and 04-05's exposure wiring work together correctly.
  4. Confirmed the API's `/metrics` serves the standard Go runtime metrics (no `octoconv_*` series, correctly — the API process never calls any `Record*` helper).
  5. Confirmed `docker port octoconv-api`/`octoconv-worker` show **no** host publish for port 9090 at all (stricter than localhost-only — completely unreachable except via exec into the container itself).
  6. Confirmed asynqmon (`curl http://127.0.0.1:8980/api/queues`) returns real queue state including the just-completed job (`"processed":1,"succeeded":1`), and `docker port octoconv-asynqmon` / `docker inspect` confirm the binding is `127.0.0.1:8980` only, not `0.0.0.0`.
  7. Cleaned up the test client/job rows from Postgres before restoring the original stack.

## Task Commits

1. **Task 1: /metrics listeners on API + worker, queue collector, METRICS_ADDR config** — `828f748` (feat)
2. **Task 2: asynqmon dashboard service (localhost-only) in docker-compose** — `bcd56f8` (feat)
3. **Task 3: Verify /metrics scrape + asynqmon dashboard reachability** — `7b63e84` (fix: pin asynqmon to linux/amd64, discovered live during verification); verification itself produced no further code changes (all assertions passed)

## Files Created/Modified

- `cmd/api/main.go` — second `http.Server` on `METRICS_ADDR` (default `127.0.0.1:9090`), `promhttp` import, goroutine + graceful shutdown
- `cmd/worker/main.go` — `net/http`, `prometheus`, `promhttp`, `internal/metrics` imports; `prometheus.MustRegister(metrics.NewQueueDepthCollector(...))`; second `http.Server` on `METRICS_ADDR` with goroutine + graceful shutdown
- `.env.example` — new `# Observability` section, `METRICS_ADDR=127.0.0.1:9090`
- `docker-compose.yml` — `METRICS_ADDR` on `api`/`worker` service blocks (no host port publish); new `asynqmon` service pinned to `hibiken/asynqmon:0.7.2` + `platform: linux/amd64`, bound to `127.0.0.1:8980:8080`

## Decisions Made

- Pinned `hibiken/asynqmon:0.7.2` (not `:latest`) after confirming it exists on Docker Hub directly via the tags API — satisfies T-04-15's supply-chain-hygiene mitigation.
- Added `platform: linux/amd64` to the asynqmon service after live verification revealed `hibiken/asynqmon` publishes amd64-only images (both `0.7.2` and `:latest` — confirmed via `docker manifest inspect`) — without it, `docker compose pull`/`up` fails outright on arm64 hosts (Apple Silicon).
- Split the docker-compose.yml diff into two commits (Task 1: METRICS_ADDR wiring; Task 2: asynqmon block) as previously documented.

## Deviations from Plan

**Checkpoint resolution ownership (process deviation, consistent with plan 04-03's precedent):** Task 3 is a `checkpoint:human-verify` gate requiring a human to run the plan's own verification steps and respond "approved." Per the same reasoning established in 04-03's SUMMARY (an executor agent correctly cannot accept a relayed approval from any other agent as genuine user consent), the orchestrator performed the live verification directly — including safely coordinating with the user around the shared dev docker-compose stack (stop/substitute/restore) — after which the human gave direct, first-hand approval based on concrete evidence (real HTTP responses, real job outcomes, real port bindings), not a bare claim. No task content, acceptance criteria, or verification steps were skipped or altered — every step in the plan's "How to verify" list was executed and its expected outcome confirmed.

**Bug found during verification (fixed, in scope):** `hibiken/asynqmon` has no arm64 image; `platform: linux/amd64` added to the asynqmon service.

**Bug found during verification (fixed, out of scope, separate commit on `main`):** `docker-compose.yml`'s `worker` service was missing `WEBHOOK_SIGNING_SECRET`, required since Phase 2 but never added — masked for months because the worker container hadn't been rebuilt. Surfaced only because this verification pass rebuilt the images. Fixed directly on `main` (commit `36b559b`, outside this plan's file scope) with the human's explicit approval, since it blocked verifying the *actual* dev stack (not just the isolated worktree) and is unrelated to any of this plan's own decisions (D-13 through D-19).

## Issues Encountered

- `hibiken/asynqmon:0.7.2`/`:latest` amd64-only — resolved with `platform: linux/amd64` (documented above).
- Pre-existing `WEBHOOK_SIGNING_SECRET` gap in `docker-compose.yml` (Phase 2 vintage, unrelated to this plan) — resolved separately on `main`, documented above.
- No issues with this plan's own code: `go build ./... && go vet ./...` clean throughout; all plan-specified acceptance-criteria greps matched.

## User Setup Required

None beyond what's already in `.env.example`/`docker-compose.yml` — `METRICS_ADDR` and the `asynqmon` service both have working defaults for local `docker compose up`.

## Next Phase Readiness

- OBS-01 (metrics instrumentation + exposure) and OBS-03 (asynqmon dashboard) are both fully verified end-to-end, live, with real traffic — not just unit tests.
- Phase 4 (Content Validation, Storage Lifecycle & Observability) has no further plans after this one — all 5 plans (04-01 through 04-05) are complete pending orchestrator merge and phase-level verification.
- No blockers.

---
*Phase: 04-content-validation-storage-lifecycle-observability*
*Completed: 2026-07-07*
