---
phase: 04-content-validation-storage-lifecycle-observability
plan: 04
subsystem: api
tags: [go, health-check, pgxpool, go-redis, minio, observability]

# Dependency graph
requires:
  - phase: 04-content-validation-storage-lifecycle-observability
    provides: "storage.Client.Ping (04-02) — the S3 reachability probe wired into HealthDeps"
provides:
  - "Real /healthz endpoint probing Postgres, Redis, and S3/MinIO reachability under a shared 3s timeout"
  - "Pinger interface + HealthDeps struct seam in internal/api/api.go for dependency-agnostic health checks"
  - "Dedicated go-redis/v9 client in cmd/api/main.go for health-only Redis pings (asynq.Client has no public Ping)"
affects: [observability, api]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Narrow Pinger interface (single Ping(ctx) error method) satisfied directly by *pgxpool.Pool and *storage.Client, with a one-line redisPinger adapter for *redis.Client"
    - "Health probes are read-only observers: Ping/BucketExists only, never a write, matching the codebase's guarded-transition discipline"

key-files:
  created: []
  modified:
    - internal/api/api.go
    - internal/api/handlers.go
    - internal/api/handlers_test.go
    - internal/api/routes_test.go
    - cmd/api/main.go
    - go.mod

key-decisions:
  - "Redis health-ping client is a small dedicated *redis.Client in cmd/api/main.go (not asynq.Client, which exposes no public Ping) — go-redis/v9 promoted from indirect to direct dependency via go mod tidy"
  - "HealthDeps threaded as a new positional NewServer parameter between resolver and Config, consistent with the existing interfaces-then-Config convention"

patterns-established:
  - "Pinger/HealthDeps seam: any future dependency needing a health check only needs a Ping(ctx context.Context) error method"

requirements-completed: [OBS-02]

# Metrics
duration: 25min
completed: 2026-07-07
---

# Phase 04 Plan 04: Real Health Check Summary

**GET /healthz now pings Postgres, Redis, and S3/MinIO under a shared 3s timeout, returning 200/ok when all reachable and 503/degraded with per-dependency detail otherwise.**

## Performance

- **Duration:** 25 min
- **Started:** 2026-07-07T09:55:00Z
- **Completed:** 2026-07-07T10:20:15Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments
- Replaced the static `{"status":"ok"}` health stub with a real, read-only dependency probe (Postgres `pgxpool.Ping`, Redis `PING` via a dedicated go-redis client, S3 `BucketExists` via the existing `storage.Client.Ping` from plan 04-02)
- Added a narrow `Pinger` interface + `HealthDeps` struct to `internal/api/api.go`, following the existing `Repo`/`Storage`/`Enqueuer` interface-segregation convention
- Wired the three real pingers in `cmd/api/main.go`; `go-redis/v9` promoted to a direct `go.mod` dependency

## Task Commits

1. **Task 1: Pinger seam + real handleHealth + health tests (api package)** - `8d82ba8` (feat)
2. **Task 2: Wire real Postgres/Redis/S3 pingers in cmd/api/main.go** - `8392dc9` (feat)

## Files Created/Modified
- `internal/api/api.go` - Added `Pinger` interface, `HealthDeps` struct, `health` field on `Server`, new `NewServer` parameter
- `internal/api/handlers.go` - `handleHealth` now pings all three dependencies under `context.WithTimeout(r.Context(), 3*time.Second)`, returns 200/ok or 503/degraded with per-dependency JSON detail
- `internal/api/handlers_test.go` - Added `fakePinger`/`healthyDeps()` helpers, `TestHealthz_Degraded` (S3 and Redis failure cases), updated `newTestServer` and two direct `NewServer` calls
- `internal/api/routes_test.go` - Updated the third direct `NewServer` call (not listed in plan interfaces, found during Task 1 build) to pass `healthyDeps()`
- `cmd/api/main.go` - Constructs a dedicated `*redis.Client` for health pings (`redisPinger` adapter), passes `api.HealthDeps{Postgres: pool, Redis: redisPinger{...}, S3: store}` into `api.NewServer`
- `go.mod` - `github.com/redis/go-redis/v9` promoted from indirect to direct require (`go mod tidy`)

## Decisions Made
- Redis health check uses a small dedicated `*redis.Client` built from the same `REDIS_ADDR` (via `queue.RedisOpt()`) rather than trying to extract a ping path from `asynq.Client`, per RESEARCH.md Open Question 2's recommendation.
- `HealthDeps` is a new positional `NewServer` parameter (between `resolver` and `cfg`) rather than a `Config` field, to keep the existing "narrow interfaces positional, tunables in Config" convention intact.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Updated a third direct `NewServer` call site not listed in the plan's interfaces**
- **Found during:** Task 1 (build/test gate)
- **Issue:** The plan's `<interfaces>` section named only two direct `NewServer` calls needing an update (`TestGetJob_DonePresigned`, `TestGetJob_SameClient_OK` in `handlers_test.go`), but `internal/api/routes_test.go` (`TestByIP_NotEvadedByForwardedForSpoofing`) also calls `NewServer` directly and failed to compile once the signature changed.
- **Fix:** Updated the call to pass `healthyDeps()` in the new `HealthDeps` position, identical to the other updated call sites.
- **Files modified:** `internal/api/routes_test.go`
- **Verification:** `go test ./internal/api/ -run 'TestHealthz'` and full `go test ./internal/api/` pass.
- **Committed in:** `8d82ba8` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (blocking build fix)
**Impact on plan:** Necessary to keep the api package compiling after the `NewServer` signature change; no scope creep beyond the plan's own intent.

## Issues Encountered
None beyond the deviation above.

## User Setup Required
None - no external service configuration required. The Redis health-ping client reuses the existing `REDIS_ADDR` environment variable already required by the queue client.

## Next Phase Readiness
- OBS-02 is fully closed: `/healthz` reflects real Postgres/Redis/S3 reachability, is read-only and time-bounded, and stays unauthenticated (D-09/D-16/D-17).
- `go build ./... && go vet ./... && go test ./...` all pass across the whole module (verified, not just the `internal/api` package).
- Pre-existing `gofmt` issue in `internal/queue/queue_test.go` (unrelated to this plan's file scope) noted but not touched, per scope boundary rules.

---
*Phase: 04-content-validation-storage-lifecycle-observability*
*Completed: 2026-07-07*
