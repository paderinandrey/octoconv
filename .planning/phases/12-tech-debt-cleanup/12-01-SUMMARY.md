---
phase: 12-tech-debt-cleanup
plan: 01
subsystem: infra
tags: [go, docker-compose, asynq, e2e, gofmt]

# Dependency graph
requires:
  - phase: 11-api-routing-end-to-end-document-conversion
    provides: Converter.Engine()/Registry.EngineFor engine-class routing pattern, the 11-REVIEW.md advisory findings this plan closes
provides:
  - Exported convert.EngineImage/EngineDocument as the single source of truth for engine-class string literals
  - api/reconciler/queue engine-class code paths referencing the shared constants
  - E2E compose override (docker-compose.e2e.yml) with api-scoped extra_hosts for plain-Linux docker
  - E2E HTTP clients (postJob, pollUntilDone, downloadClient) with explicit per-request timeouts
  - docker-compose.yml fully reconciled against .env.example (every var wired or explicitly commented as omitted)
affects: [13-passworded-legacy-document-detection, 14-pdf-a-export, 15-html-to-pdf-chromium-engine, 16-webhook-delivery-decoupling]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Engine-class literals centralized in internal/convert (EngineImage/EngineDocument); no other package may hold a raw quoted engine-class string"
    - "docker-compose.yml env audit convention: every .env.example var either wired to its consuming service (traced through cmd/*/main.go and internal/queue/client.go) or carries an inline comment explaining deliberate omission"

key-files:
  created: []
  modified:
    - internal/convert/convert.go
    - internal/convert/libvips.go
    - internal/convert/libreoffice.go
    - internal/api/handlers.go
    - internal/reconciler/reconciler.go
    - internal/queue/queue.go
    - internal/queue/queue_test.go
    - internal/e2e/e2e_test.go
    - docker-compose.e2e.yml
    - docker-compose.yml

key-decisions:
  - "queue.NewClient() is constructed at three sites (cmd/api, cmd/worker, cmd/document-worker) and reads IMAGE_MAX_RETRY/ENGINE_TIMEOUT/DOCUMENT_MAX_RETRY/DOCUMENT_ENGINE_TIMEOUT unconditionally at all three -- so all four vars are now wired into all three services, not just the ones that 'obviously' own that engine class"
  - "WEBHOOK_SIGNING_SECRET wired into document-worker (previously only on worker) since cmd/document-worker/main.go signs its own webhook callbacks and must not silently fall back to an empty secret"
  - "WEBHOOK_ALLOW_PRIVATE_IPS/WEBHOOK_ALLOW_INSECURE_HTTP intentionally NOT wired into base docker-compose.yml -- documented via inline comment as the production SSRF guard staying default-off; only docker-compose.e2e.yml relaxes them"

requirements-completed: [DEBT-01, DEBT-02, DEBT-03, DEBT-04, DEBT-05]

duration: 20min
completed: 2026-07-10
---

# Phase 12 Plan 01: Tech Debt Cleanup Summary

**Closed all 5 inherited advisory tech-debt items (v1.0 docker-compose audit + v1.2 WR-02/WR-03/WR-04 + gofmt nit) with zero new features, before v1.3 engine work begins.**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-07-10
- **Tasks:** 3/3 completed
- **Files modified:** 10

## Accomplishments
- Engine-class string literals ("image"/"document") centralized into `convert.EngineImage`/`convert.EngineDocument`; api routing switch, reconciler recovery switch, and queue-name constants all now reference the shared constants instead of hand-duplicated literals (WR-03/DEBT-02), and the two `Engine()` doc comments that also quoted the literal were reworded.
- E2E harness hardened: `docker-compose.e2e.yml` now gives the `api` service (not just worker/document-worker) the `host.docker.internal:host-gateway` alias, since `validateCallbackURL` runs inside the api process at job-creation time (WR-02/DEBT-01); every E2E HTTP call (`postJob`, `pollUntilDone`, `downloadClient`'s both branches) now carries an explicit per-request timeout instead of `http.DefaultClient`'s zero timeout (WR-04/DEBT-03).
- Pre-existing gofmt nit in `internal/queue/queue_test.go` cleared (DEBT-04).
- `docker-compose.yml` fully reconciled against `.env.example`: every documented variable is either wired into its consuming service(s) — traced through `cmd/*/main.go` and the three `queue.NewClient()` construction sites — or carries an inline comment explaining a deliberate omission (DEBT-05).

## Task Commits

Each task was committed atomically:

1. **Task 1: Centralize engine-class constants + clear gofmt nit** - `805c692` (refactor)
2. **Task 2: Harden E2E harness (api extra_hosts + HTTP timeouts)** - `ed29167` (fix)
3. **Task 3: Reconcile docker-compose.yml against .env.example** - `e3016a2` (chore)

**Plan metadata:** committed separately by the orchestrator after this SUMMARY is written.

## Files Created/Modified
- `internal/convert/convert.go` - Added exported `EngineImage`/`EngineDocument` const block, doc'd as the single source of truth for engine-class literals
- `internal/convert/libvips.go` - `Engine()` returns `EngineImage`; doc comment reworded to drop the quoted literal
- `internal/convert/libreoffice.go` - `Engine()` returns `EngineDocument`; doc comment reworded to drop the quoted literal
- `internal/api/handlers.go` - Removed local `engineImage`/`engineDocument` consts; routing switch now uses `convert.EngineImage`/`convert.EngineDocument`
- `internal/reconciler/reconciler.go` - Added `internal/convert` import; recovery-routing switch now uses the shared constants
- `internal/queue/queue.go` - Added `internal/convert` import; `QueueImage`/`QueueDocument` now derived from `convert.EngineImage`/`convert.EngineDocument`
- `internal/queue/queue_test.go` - gofmt-formatted (no logic change)
- `internal/e2e/e2e_test.go` - Added shared `e2eHTTP` client (30s timeout) used by `postJob`/`pollUntilDone`; `downloadClient()` both branches now carry an explicit 60s timeout
- `docker-compose.e2e.yml` - Added `extra_hosts: host.docker.internal:host-gateway` to the `api` service
- `docker-compose.yml` - Wired `MAX_IMAGE_PIXELS`/`MAX_DOCUMENT_UNCOMPRESSED_BYTES` into `api`; wired `IMAGE_MAX_RETRY`/`ENGINE_TIMEOUT`/`DOCUMENT_MAX_RETRY`/`DOCUMENT_ENGINE_TIMEOUT` into all three services that construct `queue.NewClient()`; wired `WEBHOOK_PRESIGN_TTL` into `worker`+`document-worker`; wired `WEBHOOK_SIGNING_SECRET` into `document-worker`; wired all four `RECONCILER_*` vars into `worker`; documented the deliberate omission of `WEBHOOK_ALLOW_PRIVATE_IPS`/`WEBHOOK_ALLOW_INSECURE_HTTP` from base compose

## Decisions Made
- `queue.NewClient()` is constructed at three sites (`cmd/api/main.go:63`, `cmd/worker/main.go:56`, `cmd/document-worker/main.go:56`) and unconditionally reads all four of `IMAGE_MAX_RETRY`/`ENGINE_TIMEOUT`/`DOCUMENT_MAX_RETRY`/`DOCUMENT_ENGINE_TIMEOUT` regardless of which engine that process runs — so all four vars now appear in all three services' env blocks, not just the "obvious" owner.
- `WEBHOOK_SIGNING_SECRET` wired into `document-worker` (previously only on `worker`) since `cmd/document-worker/main.go:54` reads it to sign its own webhook callbacks — a genuine pre-existing gap, now closed with the same dev placeholder value already used on `worker`.
- `WEBHOOK_ALLOW_PRIVATE_IPS`/`WEBHOOK_ALLOW_INSECURE_HTTP` deliberately left unset in base `docker-compose.yml` (both default `false`, the production SSRF guard) — documented via inline comment rather than wired, since only `docker-compose.e2e.yml` is permitted to relax them for the in-test webhook receiver.

## Deviations from Plan

None - plan executed exactly as written. All three tasks matched the plan's `<action>` steps precisely; no Rule 1-4 auto-fixes were needed.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. All changes are code/config hygiene with no new runtime dependencies.

## Next Phase Readiness

- `gofmt -l .`, `go vet ./...`, and `go build ./...` are all clean; `go test ./...` passes in full (E2E self-skips offline as designed).
- `docker compose config` (base) and `docker compose -f docker-compose.yml -f docker-compose.e2e.yml config` (merged) both render successfully; the merged config confirms `api.extra_hosts` now includes `host.docker.internal=host-gateway`.
- No blockers for Phase 13 (passworded/legacy document detection) — this plan touched no document-parsing or validation code paths.
