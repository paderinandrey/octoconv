---
phase: 04-content-validation-storage-lifecycle-observability
plan: 02
subsystem: infra
tags: [minio, s3, lifecycle, ttl, storage, go]

# Dependency graph
requires:
  - phase: 01-auth-hardening
    provides: existing storage.Client wrapper and cmd/api/main.go startup wiring conventions
provides:
  - "storage.Client.EnsureLifecycle — idempotent MinIO bucket lifecycle (ILM) rule setter"
  - "storage.lifecycleConfig — pure, unit-tested day-math builder for the two prefix-scoped rules"
  - "storage.Client.Ping — read-only BucketExists probe reserved for the future health endpoint"
  - "STORAGE_TTL env var (default 168h) wired into API startup, .env.example, and docker-compose api service"
affects: [04-03, 04-04, 04-05]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "MinIO ILM lifecycle rule declared via minio-go SDK at API startup (D-12), not a manual mc CLI step"
    - "Single-owner startup side effect: API calls EnsureLifecycle, mirroring the existing db.Migrate boot pattern; worker's storage.New stays a plain client (Pitfall 4)"

key-files:
  created: []
  modified:
    - internal/storage/storage.go
    - internal/storage/storage_test.go
    - cmd/api/main.go
    - .env.example
    - docker-compose.yml

key-decisions:
  - "STORAGE_TTL default 168h (7 days), single TTL for both uploads/ and results/ prefixes (D-10/D-11)"
  - "lifecycleConfig clamps sub-day/zero TTLs up to a minimum of 1 day since MinIO Expiration.Days is whole-day granular (Pitfall 3) — a 0-day rule can never be emitted"
  - "API is the single owner of EnsureLifecycle; worker is intentionally not given STORAGE_TTL (Pitfall 4, avoids redundant near-simultaneous PUTs)"

patterns-established:
  - "Ping(ctx) on storage.Client: a read-only BucketExists probe, no test PUT/GET — reserved for the D-16 health endpoint in a later plan"

requirements-completed: [STOR-01]

# Metrics
duration: 12min
completed: 2026-07-07
---

# Phase 4 Plan 02: MinIO Bucket Lifecycle (TTL) Summary

**MinIO ILM lifecycle rule (7-day default TTL on uploads/ and results/) applied declaratively via minio-go's SetBucketLifecycle at API startup, plus a read-only storage.Ping probe for the future health endpoint.**

## Performance

- **Duration:** 12 min
- **Started:** 2026-07-07T09:59:00Z (approx, worktree init)
- **Completed:** 2026-07-07T10:13:21Z
- **Tasks:** 2/2 completed
- **Files modified:** 5

## Accomplishments
- `storage.Client.EnsureLifecycle` applies an idempotent, full-document `SetBucketLifecycle` PUT covering both `uploads/` and `results/` prefixes with a single configurable TTL
- Pure `lifecycleConfig` builder unit-tested against the exact D-10/D-11/Pitfall-3 behaviors (168h → 7 days, sub-day/zero TTL clamped to 1 day, distinct non-empty rule IDs)
- `storage.Client.Ping` added as a read-only `BucketExists` probe, reserved for the health endpoint plan
- `STORAGE_TTL` wired into `cmd/api/main.go` startup (API is the single owner per Pitfall 4), documented in `.env.example`, and set on the compose `api` service only — the `worker` service deliberately does not get it

## Task Commits

Each task was committed atomically:

1. **Task 1: EnsureLifecycle + pure config builder + read-only Ping on storage.Client** - `169ef74` (feat)
2. **Task 2: Wire EnsureLifecycle into API startup + STORAGE_TTL config** - `417dc58` (feat)

## Files Created/Modified
- `internal/storage/storage.go` - added `lifecycleConfig`, `EnsureLifecycle`, `Ping`, and the `minio-go/v7/pkg/lifecycle` import (already vendored, no go.mod change)
- `internal/storage/storage_test.go` - added `TestLifecycleConfig` covering day-math, sub-day/zero clamping, and rule-ID uniqueness (no live MinIO needed)
- `cmd/api/main.go` - calls `store.EnsureLifecycle(ctx, envDuration("STORAGE_TTL", 168*time.Hour))` right after `storage.New` succeeds; added the `envDuration` helper (copied from `cmd/worker/main.go`, which already had it)
- `.env.example` - new `# Storage lifecycle` section documenting `STORAGE_TTL=168h`
- `docker-compose.yml` - `STORAGE_TTL: "168h"` added to the `api` service's `environment:` block only

## Decisions Made
- Followed D-10/D-11/D-12 exactly as locked in CONTEXT.md: single TTL, 7-day default, SDK-at-startup mechanism
- Followed Pitfall 3's clamp-to-1-day guidance and Pitfall 4's single-owner (API) guidance from RESEARCH.md verbatim — no deviation needed since the plan already encoded these into the task actions

## Deviations from Plan

None - plan executed exactly as written. Both tasks' acceptance criteria (grep checks, `go test`, `go vet`, `go build`) passed on first implementation with no auto-fixes required.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. `STORAGE_TTL` has a working default (168h); operators only need to override it if a different retention window is desired. The MinIO lifecycle rule is applied automatically on every API startup (idempotent, no manual `mc` step).

## Next Phase Readiness

- `storage.Client.Ping` is now available for plan 04-03 (health endpoint, OBS-02/D-16/D-17) to consume without needing to add a new MinIO probe method
- STOR-01 is fully satisfied: no manual cleanup, no application-side sweeper — MinIO's native ILM expiration owns object deletion for both `uploads/` and `results/` prefixes
- No blockers for downstream plans in this phase

---
*Phase: 04-content-validation-storage-lifecycle-observability*
*Completed: 2026-07-07*
