---
phase: 16-webhook-delivery-decoupling
plan: 02
subsystem: infra
tags: [asynq, redis, postgres, docker, webhook, reconciler, advisory-lock]

# Dependency graph
requires:
  - phase: 16-webhook-delivery-decoupling
    plan: "01"
    provides: "AdvisoryLock interface, PGAdvisoryLock, Sweeper.RunWithLock in internal/reconciler"
provides:
  - "cmd/webhook-worker binary: sole webhook-delivery consumer + sole sweeper host (lock-gated)"
  - "Dockerfile.webhook-worker: clean debian-slim runtime with no engine packages"
  - "cmd/worker demoted to image-only (no webhook role, no sweeper)"
affects: [16-03-compose-topology, 16-04-live-verification]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Trimmed-copy entry point (cmd/webhook-worker mirrors cmd/worker's wiring order: Postgres -> storage -> Redis opt -> signing secret -> queue client -> repo -> handler) — same convention as cmd/document-worker/cmd/chromium-worker"
    - "Inert engine-only worker.NewHandler args passed as nil/0 when a binary doesn't need that role (webhook-worker passes nil registry/0 engineTimeout; cmd/worker now passes nil webhookRepo/deliverer/signingSecret/0 presignTTL)"

key-files:
  created:
    - cmd/webhook-worker/main.go
    - Dockerfile.webhook-worker
  modified:
    - cmd/worker/main.go
    - cmd/document-worker/main.go
    - cmd/chromium-worker/main.go

key-decisions:
  - "cmd/webhook-worker wires full storage.New(ctx) (D-07 corrected) — HandleWebhookDeliver calls store.PresignGet for done-job download URLs, so storage cannot be nil despite webhook-worker doing no file conversion"
  - "cmd/webhook-worker fails closed at startup (log.Fatalf) if WEBHOOK_SIGNING_SECRET is unset — it is now the sole signer of webhook deliveries fleet-wide"
  - "cmd/worker (image) drops internal/webhook and internal/reconciler imports entirely — webhookRepo/deliverer/signingSecret/presignTTL passed as nil/0 to worker.NewHandler since HandleImageConvert never reads h.webhookRepo/h.deliverer/h.signingSecret (verified via grep before wiring nil)"
  - "cmd/worker's image queue concurrency restored to a single-queue map {QueueImage: 4} now that it no longer shares the queue map slot with QueueWebkook:1"
  - "Sweeper moved fully into cmd/webhook-worker via Sweeper.RunWithLock(ctx, lock) (not the unlocked Run) — exactly one webhook-worker replica sweeps at a time under the Plan 16-01 Postgres advisory lock"

patterns-established:
  - "Comment-only stale-reference fixes applied to both cmd/document-worker AND cmd/chromium-worker in the same task, since both carried the identical 'cmd/worker remains the sole webhook consumer' phrasing (WR-1) that this task's cmd/worker edits made false"

requirements-completed: [WEBH-01]

# Metrics
duration: 8min
completed: 2026-07-12
---

# Phase 16 Plan 02: Dedicated Webhook Worker Binary Summary

**New `cmd/webhook-worker` binary (trimmed from `cmd/worker`, wired with storage for PresignGet) consumes `TypeWebhookDeliver` and runs the reconciler sweeper under the Plan 16-01 Postgres advisory lock; `cmd/worker` is demoted to image-only with the webhook role and sweeper fully removed.**

## Performance

- **Duration:** ~8 min (commit-to-commit)
- **Started:** 2026-07-12T00:20:00Z (approx, Task 1 build)
- **Completed:** 2026-07-11T21:24:18Z (Task 3 commit)
- **Tasks:** 3
- **Files modified:** 5 (2 created, 3 modified)

## Accomplishments
- Created `cmd/webhook-worker/main.go`: consumes only `queue.TypeWebhookDeliver` (no image/document/html handler registered), wires `storage.New(ctx)` for `HandleWebhookDeliver`'s `PresignGet` call on done-status jobs (D-07 corrected), fails closed with `log.Fatalf("WEBHOOK_SIGNING_SECRET must be set")` at startup, and runs the reconciler sweeper via `sweeper.RunWithLock(ctx, lock)` gated by `reconciler.NewPGAdvisoryLock(ctx, pool)` from Plan 16-01
- Created `Dockerfile.webhook-worker`: two-stage build mirroring `Dockerfile.worker`'s shape, installing only `ca-certificates` in the runtime stage — verified zero engine packages (no tini/libvips/libreoffice/chromium)
- Removed the webhook consumer and sweeper entirely from `cmd/worker/main.go` (D-03 clean cut): no `TypeWebhookDeliver` registration, no `reconciler`/`webhook` imports, single-queue `{QueueImage: 4}` concurrency map, single-queue metrics collector and start log (`"queue=%s"` instead of `"queues=%s,%s"`)
- Fixed the stale "cmd/worker remains the sole webhook consumer" comment in both `cmd/document-worker/main.go` and `cmd/chromium-worker/main.go` (WR-1 — the same stale phrasing was present in both files, not just document-worker) to reference `cmd/webhook-worker` as the sole webhook consumer and sole sweeper host
- Confirmed via `go build ./...`, `go vet`, `gofmt -l`, and `go test ./...` (full suite, including `internal/worker`, `internal/reconciler`) that the full repository remains green with zero new Go module dependencies (no `go.mod`/`go.sum` diff)

## Task Commits

Each task was committed atomically:

1. **Task 1: Create cmd/webhook-worker/main.go (webhook consumer + sweeper under advisory lock, with storage)** - `46add0f` (feat)
2. **Task 2: Add Dockerfile.webhook-worker (clean debian-slim, no engine packages)** - `01881f4` (feat)
3. **Task 3: Remove webhook consumer + sweeper from cmd/worker; fix stale sole-consumer comments in cmd/document-worker AND cmd/chromium-worker** - `e2eb066` (feat)

_No plan-metadata commit yet — orchestrator handles STATE.md/ROADMAP.md updates after the wave completes._

## Files Created/Modified
- `cmd/webhook-worker/main.go` - New binary: webhook queue consumer + advisory-lock-gated sweeper host, with storage wired for PresignGet, fails closed on missing signing secret
- `Dockerfile.webhook-worker` - New clean debian-slim runtime image, no engine packages
- `cmd/worker/main.go` - Webhook role and sweeper removed; image-only, single-queue concurrency, dropped `internal/reconciler`/`internal/webhook` imports
- `cmd/document-worker/main.go` - Two stale comments corrected (`:50-53` signing-secret comment, `:76-78` sweeper-location comment) to reference `cmd/webhook-worker`; no behavior change
- `cmd/chromium-worker/main.go` - Same two stale comments corrected (WR-1); no behavior change

## Decisions Made
- **Inert nil args over inert-value pattern:** For `cmd/worker`'s webhook-related `worker.NewHandler` params, chose `nil`/`nil`/`nil`/`0` (webhookRepo, deliverer, signingSecret, presignTTL) rather than reading a live (but unused) `WEBHOOK_SIGNING_SECRET` and constructing live `webhook.NewRepo`/`webhook.NewDeliverer` instances — this let `internal/reconciler` and `internal/webhook` imports be dropped from `cmd/worker` entirely (matching D-03's "clean cut" framing more literally than document-worker/chromium-worker's existing inert-but-present pattern, which those two binaries retain unchanged since only `cmd/worker`'s webhook role was in scope for removal here). Verified safe via grep: `h.webhookRepo`/`h.deliverer`/`h.signingSecret` are referenced only inside `HandleWebhookDeliver`, never `HandleImageConvert`.
- **Fixed both stale sole-consumer comments in the same task:** The plan's WR-1 finding flagged that `cmd/chromium-worker/main.go:50-54` carries the identical stale phrasing to `cmd/document-worker/main.go:50-53`, missed by the original pattern-mapping pass. Both were corrected together since they're the same defect caused by the same root change (webhook role leaving `cmd/worker`).
- **Also fixed the D-05 sweeper-location comment in document-worker (Rule 1 — bug, in-scope):** Beyond the plan's explicitly named `:50-53` comment, `cmd/document-worker/main.go:76-78` and the analogous block in `cmd/chromium-worker/main.go:77-80` also asserted "the stale-job sweep loop runs solely in cmd/worker" — this became false the moment Task 3's own `cmd/worker` edits landed (sweeper moved to `cmd/webhook-worker`). Corrected both since leaving them stale would misdocument the exact topology this task establishes.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Corrected a second stale sweeper-location comment in cmd/document-worker and cmd/chromium-worker beyond the plan's named line range**
- **Found during:** Task 3
- **Issue:** The plan explicitly named `cmd/document-worker/main.go:50-53` (and the WR-1 equivalent in chromium-worker) for the "sole webhook consumer" comment fix, but a second nearby comment block in both files ("the stale-job sweep loop runs solely in cmd/worker") became equally false the instant this same task's `cmd/worker/main.go` edits removed the sweeper from that binary.
- **Fix:** Updated both blocks to state the sweeper now runs solely in `cmd/webhook-worker` under the Postgres advisory lock (D-04/D-05).
- **Files modified:** cmd/document-worker/main.go, cmd/chromium-worker/main.go
- **Verification:** `grep -n "cmd/worker" cmd/document-worker/main.go cmd/chromium-worker/main.go` no longer returns a false "sweeper runs in cmd/worker" claim; full build/vet/test green.
- **Committed in:** e2eb066 (Task 3 commit)

---

**Total deviations:** 1 auto-fixed (1 bug — stale documentation directly caused by this task's own edits)
**Impact on plan:** Pure documentation accuracy fix scoped to files this task was already modifying. No behavior change, no scope creep beyond the plan's own stated intent (correct stale webhook-topology comments).

## Issues Encountered
- Initial verification attempt used `cd /Users/apaderin/dev/octoconv && ...` which navigated out of the worktree into the main repo checkout, causing a false-negative grep read of stale (pre-edit) file contents (worktree cwd-drift, #3097). Corrected by re-running all verification commands from the worktree's default cwd (no `cd`); confirmed via `git rev-parse --show-toplevel` that the worktree path was in effect for the actual commits and final verification. No code was affected — this was purely a verification-tooling mistake caught and corrected before committing.

## User Setup Required

None - no external service configuration required. Compose wiring (adding the `webhook-worker-1`/`-2` services, dropping the webhook env vars from `worker`/`document-worker`/`chromium-worker`) is explicitly out of scope for this plan (Plan 16-03).

## Next Phase Readiness
- `cmd/webhook-worker` and `Dockerfile.webhook-worker` are ready for Plan 16-03 to wire into `docker-compose.yml`/`docker-compose.e2e.yml` as two named services (`webhook-worker-1`/`webhook-worker-2` per D-05) with Postgres+Redis+MinIO `depends_on`.
- `cmd/worker`, `cmd/document-worker`, `cmd/chromium-worker` no longer carry any webhook-delivery responsibility in code or comments; the only remaining webhook-related env vars needed by any of them post-Phase-16 compose cleanup are none (document-worker/chromium-worker's inert `WEBHOOK_SIGNING_SECRET` read stays, but is unused — Plan 16-03's discretion whether to strip the env var from those compose blocks too).
- No blockers. Live e2e verification of SC1 (stop image-worker, document webhook still delivered), SC2 (kill one webhook-worker mid-delivery), SC3 (exactly one advisory-lock holder) is explicitly Plan 16-04's responsibility.

---
*Phase: 16-webhook-delivery-decoupling*
*Completed: 2026-07-12*

## Self-Check: PASSED

- FOUND: cmd/webhook-worker/main.go
- FOUND: Dockerfile.webhook-worker
- FOUND: cmd/worker/main.go
- FOUND: cmd/document-worker/main.go
- FOUND: cmd/chromium-worker/main.go
- FOUND: .planning/phases/16-webhook-delivery-decoupling/16-02-SUMMARY.md
- FOUND commit: 46add0f
- FOUND commit: 01881f4
- FOUND commit: e2eb066
