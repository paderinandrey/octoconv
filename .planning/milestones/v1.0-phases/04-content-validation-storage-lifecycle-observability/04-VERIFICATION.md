---
phase: 04-content-validation-storage-lifecycle-observability
verified: 2026-07-07T22:00:00Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
---

# Phase 4: Content Validation, Storage Lifecycle & Observability Verification Report

**Phase Goal:** Uploaded files are verified by their actual content rather than trusted metadata, storage doesn't grow unbounded, and operators can see the real health and behavior of the system.
**Verified:** 2026-07-07
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A file whose magic bytes don't match its declared format/extension is rejected with 422 before it's written to S3 | ✓ VERIFIED | `internal/convert/sniff.go` implements a hardcoded 5-signature table (png/jpg/webp/heic/tiff) matching exactly `imageFormats` in `internal/convert/libvips.go:9`. `internal/api/handlers.go:120-150` calls `convert.Sniff(file)` and returns 422 for both unrecognized content (`detected==""`) and declared/detected mismatch, *before* `s.storage.Upload` (line 169) and before `s.repo.Create` (line 176). Tests `TestCreateJob_ContentMismatch`, `TestCreateJob_UnrecognizedContent` in `internal/api/handlers_test.go` pass. |
| 2 | Uploaded files and results are auto-deleted from S3/MinIO after configured TTL, no manual cleanup | ✓ VERIFIED | `internal/storage/storage.go` defines `lifecycleConfig`/`EnsureLifecycle`, applying two `Enabled` MinIO ILM rules on prefixes `uploads/` and `results/` with day-granular, clamped-to-≥1 expiration. `cmd/api/main.go:59-61` calls `store.EnsureLifecycle(ctx, envDuration("STORAGE_TTL", 168*time.Hour))` at every API startup (idempotent full-document PUT — no manual `mc` step). `STORAGE_TTL=168h` documented in `.env.example` and set on the `api` service in `docker-compose.yml`; deletion itself is server-side MinIO ILM, not app code, matching "no manual cleanup." `TestLifecycleConfig` passes. |
| 3 | Operator can view Prometheus metrics for queue depth, job outcomes, webhook delivery success/failure | ✓ VERIFIED | `internal/metrics/metrics.go` defines `octoconv_job_outcomes_total`, `octoconv_job_duration_seconds`, `octoconv_webhook_deliveries_total`, `octoconv_reconciler_actions_total`; `internal/metrics/queue_collector.go` defines `octoconv_queue_depth` (pull-based, per-queue/state). Instrumentation call sites confirmed in `internal/worker/worker.go` (`RecordJobOutcome` at both genuine terminal branches only, `RecordWebhookDelivery` after `Deliver`) and `internal/reconciler/reconciler.go` (`RecordReconcilerAction("exhausted"/"recovered")` at the correct branches, never on the `ErrDuplicateTask` no-op). Both `cmd/api/main.go` and `cmd/worker/main.go` serve `promhttp.Handler()` on a second, localhost-only `METRICS_ADDR` listener (default `127.0.0.1:9090`), and the worker registers `metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage, queue.QueueWebhook)`. 04-05-SUMMARY.md documents a live end-to-end scrape (real job, real `octoconv_job_outcomes_total{status="done"} 1`, real queue-depth gauges) which is consistent with the source-level wiring reviewed here. |
| 4 | Health endpoint reflects real dependency status (Postgres, Redis, S3/MinIO), not static `{"status":"ok"}` | ✓ VERIFIED | `internal/api/handlers.go:33-68` `handleHealth` pings `s.health.Postgres/Redis/S3` under a shared 3s timeout, returns 200/`"ok"` only when all three succeed, else 503/`"degraded"` with per-dependency `"ok"`/`"unreachable"` detail. `internal/api/api.go` defines the narrow `Pinger` interface and `HealthDeps` struct. `cmd/api/main.go` wires the real `*pgxpool.Pool`, a dedicated `redis.Client` adapter, and `*storage.Client` (which has a read-only `Ping` via `BucketExists`, added in 04-02). Tests `TestHealthz_Degraded` (S3 and Redis failure cases) and `TestHealthz_NoAuthRequired` pass. The static stub is fully gone (`grep` for the old literal returns nothing). |
| 5 | Operator can visually inspect the asynq queue via the asynqmon dashboard | ✓ VERIFIED | `docker-compose.yml` defines an `asynqmon` service: `image: hibiken/asynqmon:0.7.2` (pinned, not `:latest`), `platform: linux/amd64` (fixes the no-arm64-image issue documented in 04-05-SUMMARY.md), `depends_on: redis: condition: service_healthy`, `environment: REDIS_ADDR: redis:6379`, `ports: - "127.0.0.1:8980:8080"` (loopback-only, no host `0.0.0.0` exposure). 04-05-SUMMARY.md documents a live `curl http://127.0.0.1:8980/api/queues` returning real queue state (`"processed":1,"succeeded":1"`) and confirms via `docker port`/`docker inspect` the binding is `127.0.0.1:8980` only. |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/sniff.go` | Magic-byte table + `Sniff` + `MIMEType` | ✓ VERIFIED | Present, substantive (114 lines), exported `Sniff`/`MIMEType`, `sniffLen=12`, 6-brand HEIC allow-list, peek-and-restitch via `io.MultiReader` |
| `internal/convert/sniff_test.go` | Per-format detection tests | ✓ VERIFIED | 11 test functions covering all formats, foreign-brand rejection, unrecognized content, short input, full-stream preservation, MIMEType |
| `internal/api/handlers.go` (`handleCreateJob`) | Detect-then-validate ordering | ✓ VERIFIED | `convert.Sniff` (line 120) precedes `convert.Default.Supports` (line 146) and `s.storage.Upload` (line 169); client resolved before rejection logging (D-08) |
| `internal/storage/storage.go` | `EnsureLifecycle` + `Ping` | ✓ VERIFIED | Both present, wrap errors consistently, `Ping` is read-only (`BucketExists`) |
| `internal/metrics/metrics.go`, `queue_collector.go` | 4 metric families + collector | ✓ VERIFIED | All defined via `promauto`/custom `prometheus.Collector`; `metrics_test.go` covers all via `testutil` |
| `internal/api/api.go` (`Pinger`/`HealthDeps`) | Health probe seam | ✓ VERIFIED | Present, narrow interface, wired through `NewServer` |
| `cmd/api/main.go`, `cmd/worker/main.go` | `/metrics` listeners, queue collector registration | ✓ VERIFIED | Both processes run a second localhost-bound `http.Server` with `promhttp.Handler()`; worker registers `NewQueueDepthCollector` |
| `docker-compose.yml` | `STORAGE_TTL`, `METRICS_ADDR` on api/worker, `asynqmon` service, `WEBHOOK_SIGNING_SECRET` on worker | ✓ VERIFIED | All present; both documented bug fixes (asynqmon `platform: linux/amd64` and worker `WEBHOOK_SIGNING_SECRET`) confirmed present on the checked-out `main` tree |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/api/handlers.go` | `internal/convert/sniff.go` | `convert.Sniff` + `convert.MIMEType` before pair-check/upload | ✓ WIRED | Confirmed by line-order read of `handleCreateJob` |
| `internal/worker/worker.go` | `internal/convert/sniff.go` | `convert.MIMEType` replaces private `contentTypeFor` | ✓ WIRED | `contentTypeFor` no longer exists in worker.go; `convert.MIMEType` used at both output-content-type sites |
| `cmd/api/main.go` | `internal/storage/storage.go` | `EnsureLifecycle` called once after `storage.New` | ✓ WIRED | Confirmed at `cmd/api/main.go:59` |
| `internal/worker/worker.go` | `internal/metrics/metrics.go` | `RecordJobOutcome`/`RecordWebhookDelivery` at terminal exit points | ✓ WIRED | Confirmed at both terminal branches (done/failed) and post-`Deliver` |
| `internal/reconciler/reconciler.go` | `internal/metrics/metrics.go` | `RecordReconcilerAction` at exhausted+recovered branches | ✓ WIRED | Confirmed; correctly excludes the `ErrDuplicateTask` no-op path |
| `cmd/worker/main.go` | `internal/metrics/queue_collector.go` | `prometheus.MustRegister(NewQueueDepthCollector(...))` | ✓ WIRED | Confirmed before `srv.Start(mux)` |
| `cmd/api/main.go` / `cmd/worker/main.go` | promhttp exposition | second `http.Server` on `METRICS_ADDR` | ✓ WIRED | Confirmed in both files, with graceful shutdown |
| `docker-compose.yml` | `asynqmon` | `redis:6379` + loopback port binding | ✓ WIRED | Confirmed |

### Data-Flow Trace (Level 4)

Not applicable in the UI-rendering sense (this phase has no frontend component); the equivalent check — "do the metrics carry real, non-static values?" — is satisfied by the source-level review above (counters/histograms are incremented from real terminal state transitions, not hardcoded) and corroborated by the live scrape evidence in 04-05-SUMMARY.md (`octoconv_job_outcomes_total{status="done"} 1` after a real job).

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Module builds | `go build ./...` | exit 0 | ✓ PASS |
| Module vets clean | `go vet ./...` | exit 0 | ✓ PASS |
| Content-validation, storage, metrics, api, worker, reconciler tests pass | `go test ./internal/convert/... ./internal/storage/... ./internal/metrics/... ./internal/api/... ./internal/worker/... ./internal/reconciler/...` | all `ok` | ✓ PASS |
| Sniff table matches registered formats | `grep imageFormats internal/convert/libvips.go` vs. `signatures` table in `sniff.go` | both list exactly png/jpg/webp/heic/tiff | ✓ PASS |
| Both 04-05-documented bug fixes present on `main` | `grep platform docker-compose.yml`, `grep WEBHOOK_SIGNING_SECRET docker-compose.yml` | `platform: linux/amd64` present on asynqmon; `WEBHOOK_SIGNING_SECRET` present on worker | ✓ PASS |

Live Docker-stack scrape/dashboard reachability itself was not re-run by this verifier (would require starting the full compose stack); the orchestrator's documented live verification (real job, real `/metrics` scrape via `docker exec .../dev/tcp`, real asynqmon `/api/queues` query, `docker port`/`docker inspect` confirming loopback-only bindings) is accepted as strong evidence, and is corroborated by an independent source-level review confirming the code paths that would produce those exact observations actually exist and are wired as claimed.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| VALID-01 | 04-01 | Content checked by magic bytes before storage/processing | ✓ SATISFIED | `convert.Sniff` runs before any S3/Postgres write |
| VALID-02 | 04-01 | Declared/detected mismatch rejected 422 before S3 write | ✓ SATISFIED | `handleCreateJob` mismatch branch, tested |
| STOR-01 | 04-02 | Auto-expire uploads/results via lifecycle TTL | ✓ SATISFIED | `EnsureLifecycle` + MinIO ILM rules, wired at API startup |
| OBS-01 | 04-03, 04-05 | Prometheus metrics for queue depth, job outcomes, webhook delivery | ✓ SATISFIED | `internal/metrics` package instrumented + exposed via `promhttp` on both processes |
| OBS-02 | 04-04 | Health endpoint reflects real dependency status | ✓ SATISFIED | Real `handleHealth` pinging Postgres/Redis/S3, 503 on degradation |
| OBS-03 | 04-05 | asynqmon dashboard for visual queue inspection | ✓ SATISFIED | `asynqmon` service in `docker-compose.yml`, loopback-bound |

**Note:** `.planning/REQUIREMENTS.md`'s tracking table (lines 49-60, 124-129) still marks VALID-01/02, STOR-01, OBS-01/02/03 as `Pending` — this is a documentation-sync lag in the requirements tracker, not a code gap. All six requirements are demonstrably satisfied in the current codebase per the evidence above. Recommend the orchestrator update `REQUIREMENTS.md`'s status column as a housekeeping follow-up; this does not block the phase goal and is not treated as a gap.

### Anti-Patterns Found

None. `grep` for `TODO|FIXME|XXX|HACK|PLACEHOLDER|not yet implemented|coming soon` across all phase-modified files (`sniff.go`, `storage.go`, `metrics.go`, `queue_collector.go`, `handlers.go`, `api.go`, `worker.go`, `reconciler.go`, `cmd/api/main.go`, `cmd/worker/main.go`) returned no matches. `go build ./...`, `go vet ./...` clean.

### Human Verification Required

None. All five success criteria are supported by direct source-level evidence (artifact existence, substantive implementation, and wiring), and the two criteria most dependent on live infrastructure behavior (OBS-01 exposure and OBS-03 dashboard reachability) are additionally corroborated by the orchestrator's own documented live end-to-end verification (real job submitted, real metrics scraped via `docker exec`, real asynqmon API query, real `docker port`/`docker inspect` confirming loopback-only bindings) — this verifier independently confirmed the underlying code paths exist and match that narrative rather than accepting the narrative on its own.

### Gaps Summary

No gaps found. All 5 ROADMAP success criteria for Phase 4 are verified against the actual codebase (not just SUMMARY.md claims):

1. Magic-byte content validation gates uploads before any S3 write, with a hardcoded signature table that was independently confirmed to match the actual registered format set (png/jpg/webp/heic/tiff) in `internal/convert/libvips.go`, correcting the original CONTEXT.md-locked placeholder list as documented in 04-01-SUMMARY.md.
2. MinIO ILM lifecycle rules are declared via the SDK at API startup and cover both `uploads/` and `results/` prefixes.
3. Prometheus metrics for job outcomes, durations, webhook deliveries, reconciler actions, and queue depth are all instrumented at the correct (and only the correct) event points and exposed via a localhost-only `/metrics` listener on both processes.
4. `/healthz` performs real, time-bounded, read-only pings of Postgres, Redis, and S3, returning 503 with per-dependency detail on any failure.
5. The asynqmon dashboard is deployed, pinned to a specific tag, platform-corrected for arm64 hosts, and bound to `127.0.0.1` only.

Both unrelated bugs discovered during 04-05's live verification (asynqmon missing `platform: linux/amd64`; worker missing `WEBHOOK_SIGNING_SECRET` in `docker-compose.yml`) were independently confirmed present in the fixed form on the current `main` tree.

One non-blocking housekeeping item: `.planning/REQUIREMENTS.md`'s status tracker has not been updated to reflect these six requirements as satisfied (still shows `Pending`). This is a documentation gap, not a code gap, and does not affect the phase verdict.

---

*Verified: 2026-07-07*
*Verifier: Claude (gsd-verifier)*
