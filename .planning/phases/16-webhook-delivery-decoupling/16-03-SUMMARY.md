---
phase: 16-webhook-delivery-decoupling
plan: 03
subsystem: infra
tags: [docker-compose, webhook, deployment-topology, e2e, env-config]

# Dependency graph
requires:
  - phase: 16-webhook-delivery-decoupling
    plan: "02"
    provides: "cmd/webhook-worker binary + Dockerfile.webhook-worker: sole webhook-delivery consumer + sole sweeper host (lock-gated)"
provides:
  - "docker-compose.yml with two symmetric webhook-worker services (webhook-worker-1/-2) providing real >=2-consumer redundancy (D-05)"
  - "Image/document/chromium worker compose blocks stripped of webhook/reconciler env (D-03/D-04 fully wired at the deployment layer)"
  - "docker-compose.e2e.yml host-gateway wiring for the webhook-worker services (live E2E receiver reachability)"
  - ".env.example webhook-worker-only config surface documentation (WEBHOOK_WORKER_CONCURRENCY, relocated signing/presign/reconciler vars)"
affects: [16-04-live-verification]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Symmetric named-replica services (webhook-worker-1/-2) instead of deploy.replicas for real multi-consumer redundancy in plain docker-compose (no swarm)"

key-files:
  created: []
  modified:
    - docker-compose.yml
    - docker-compose.e2e.yml
    - .env.example

key-decisions:
  - "webhook-worker-1/-2 both depend_on postgres+redis+minio (not just postgres+redis) because HandleWebhookDeliver's PresignGet call requires storage, correcting the CONTEXT.md D-07 'no S3/MinIO needed' assumption already flagged in 16-PATTERNS.md and confirmed by Plan 16-02"
  - "Two fully symmetric services (identical env except container_name) rather than primary/secondary, matching D-05's horizontal-redundancy intent"
  - "docker-compose.e2e.yml worker:/document-worker: extra_hosts overrides removed outright (not just commented) since neither process dials the E2E host receiver anymore; api: and chromium-worker: overrides left untouched since their rationale (SSRF validation, network-block canary) is unrelated to webhook delivery"
  - ".env.example webhook-worker vars grouped into a new '# Webhook worker' section separate from '# Worker' (image), physically reflecting that these vars are now consumed by a different binary"

patterns-established: []

requirements-completed: [WEBH-01]

# Metrics
duration: 12min
completed: 2026-07-12
---

# Phase 16 Plan 03: Webhook Deployment Topology Summary

**Two named webhook-worker services (webhook-worker-1/-2, Dockerfile.webhook-worker) wired into docker-compose.yml with full storage+webhook+reconciler env, image/document/chromium workers stripped of webhook env, E2E host-gateway reachability moved to the new services, and .env.example documenting the webhook-worker-only config surface.**

## Performance

- **Duration:** ~12 min (commit-to-commit)
- **Started:** 2026-07-12T08:10:00Z (approx)
- **Completed:** 2026-07-12T08:22:00Z (approx)
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments
- Added `webhook-worker-1` and `webhook-worker-2` services to `docker-compose.yml`, both built from `Dockerfile.webhook-worker`, both depending on `postgres`+`redis`+`minio` (`condition: service_healthy`), each carrying the full `S3_*` block, `WEBHOOK_WORKER_CONCURRENCY`, `WEBHOOK_SIGNING_SECRET`, `WEBHOOK_PRESIGN_TTL`, the four `RECONCILER_*` vars, and `METRICS_ADDR` — symmetric replicas providing the real `>=2`-consumer redundancy required by D-05/T-16-09/SC2
- Removed `WEBHOOK_SIGNING_SECRET`, `WEBHOOK_PRESIGN_TTL`, and all four `RECONCILER_*` vars from the image `worker:` block (D-03/D-04) while keeping its `S3_*`, `WORKER_CONCURRENCY`, `ENGINE_TIMEOUT`, `IMAGE_MAX_RETRY`, and DEBT-05 cross-engine vars intact
- Removed the now-resolved inert `WEBHOOK_SIGNING_SECRET`/`WEBHOOK_PRESIGN_TTL` vars and their "revisited by v1.3 webhook-decoupling phase" DEBT-05 comment blocks from both `document-worker:` and `chromium-worker:` (this phase is that revisit)
- Wired `webhook-worker-1`/`webhook-worker-2` `host.docker.internal:host-gateway` overrides into `docker-compose.e2e.yml`; removed the now-obsolete `worker:`/`document-worker:` extra_hosts overrides; kept `api:` (SSRF validation) and `chromium-worker:` (network-block canary) overrides unchanged; rewrote the stale "worker delivers webhooks" comments to reference the webhook-worker services
- Documented `WEBHOOK_WORKER_CONCURRENCY` under a new `# Webhook worker` section in `.env.example`; relocated `WEBHOOK_SIGNING_SECRET`/`WEBHOOK_PRESIGN_TTL` there from `# Worker`; annotated `RECONCILER_*` as webhook-worker-only (D-04) and `WEBHOOK_ALLOW_INSECURE_HTTP`/`WEBHOOK_ALLOW_PRIVATE_IPS` as API-side only (enforced in `validateCallbackURL`)
- Verified both `docker compose -f docker-compose.yml config` and the layered `-f docker-compose.yml -f docker-compose.e2e.yml config` parse cleanly

## Task Commits

Each task was committed atomically:

1. **Task 1: Add webhook-worker-1/-2 services; strip webhook env from image worker** - `0879c7d` (feat)
2. **Task 2: E2E compose override + .env.example webhook-worker section** - `3b8b7e7` (feat)

_No plan-metadata commit yet — orchestrator handles STATE.md/ROADMAP.md updates after the wave completes._

## Files Created/Modified
- `docker-compose.yml` - Added `webhook-worker-1`/`webhook-worker-2` services (Dockerfile.webhook-worker, postgres+redis+minio depends_on, full webhook+reconciler+storage env); stripped webhook/reconciler env from `worker:`, `document-worker:`, `chromium-worker:` blocks
- `docker-compose.e2e.yml` - Added `webhook-worker-1`/`webhook-worker-2` host-gateway overrides; removed `worker:`/`document-worker:` overrides; updated stale comments; `api:`/`chromium-worker:` overrides unchanged
- `.env.example` - Added `# Webhook worker` section with `WEBHOOK_WORKER_CONCURRENCY`; relocated webhook signing/presign vars there; annotated `RECONCILER_*` and `WEBHOOK_ALLOW_*` ownership

## Decisions Made
- Both webhook-worker services depend on `minio` (not just `postgres`/`redis`) because `HandleWebhookDeliver` calls `store.PresignGet` for `done`-status jobs — confirmed by the 16-PATTERNS.md critical finding and Plan 16-02's own storage wiring in `cmd/webhook-worker`.
- Symmetric (not primary/secondary) service definitions for `webhook-worker-1`/`-2`, matching D-05's real horizontal-redundancy requirement rather than `deploy.replicas` (ignored by plain `docker-compose`).
- Removed rather than commented-out the obsolete `worker:`/`document-worker:` E2E overrides, since leaving disabled/stale blocks in an E2E-only file would misdocument the current topology for the next reader (consistent with the plan's explicit instruction).

## Deviations from Plan

None - plan executed exactly as written. Both tasks matched the 16-PATTERNS.md analogs precisely; no auto-fixes, no architectural questions, no auth gates encountered.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required. This plan only edits Docker Compose topology and `.env.example` documentation; no secrets or dashboards need manual setup.

## Next Phase Readiness
- The compose stack is now runnable with the D-05 shape: two symmetric webhook-worker replicas, image/document/chromium workers stripped of webhook responsibility, E2E host-gateway wiring pointed at the correct processes.
- Ready for Plan 16-04's live acceptance testing: SC1 (stop image-worker, webhook still delivered by webhook-worker), SC2 (kill one webhook-worker mid-delivery, the other drains the queue), SC3 (exactly one advisory-lock holder sweeps at a time).
- No blockers.

---
*Phase: 16-webhook-delivery-decoupling*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: .planning/phases/16-webhook-delivery-decoupling/16-03-SUMMARY.md
- FOUND commit: 0879c7d
- FOUND commit: 3b8b7e7
