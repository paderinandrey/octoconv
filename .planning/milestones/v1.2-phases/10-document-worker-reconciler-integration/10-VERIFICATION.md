---
phase: 10-document-worker-reconciler-integration
verified: 2026-07-09T23:30:00Z
status: passed
score: 17/17 must-haves verified
overrides_applied: 0
deferred:
  - truth: "Full container image build + live document conversion smoke test (docker-compose up + real LibreOffice conversion)"
    addressed_in: "Phase 11"
    evidence: "Plan 10-04 verification section states: 'Full container image build + live document conversion smoke is intentionally deferred to Phase 11's end-to-end verification (roadmap Phase 11 success criterion 4)'; Phase 11 success criterion 4: 'A live end-to-end test converts all 6 supported format pairs ... successfully through the full upload → convert → download pipeline.'"
---

# Phase 10: Document Worker & Reconciler Integration Verification Report

**Phase Goal:** Document conversions run in their own resource-isolated process, respect their own timeout budget, and are recovered correctly if they get stranded.
**Verified:** 2026-07-09T23:30:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Document conversion jobs are processed by a separate `cmd/document-worker` binary/container, resource-isolated from the image worker (Roadmap SC1, DOC-07) | ✓ VERIFIED | `cmd/document-worker/main.go` exists, builds only `queue.TypeDocumentConvert` (`mux.HandleFunc` count = 1, no `TypeWebhookDeliver`); `Dockerfile.document-worker` is a separate image; `docker-compose.yml` `document-worker` service has `deploy.resources.limits: cpus: "2.0", memory: 1g` — identical envelope to `worker` service but a distinct container |
| 2 | A document conversion exceeding `DOCUMENT_ENGINE_TIMEOUT` is classified as a terminal failure rather than retried forever (Roadmap SC2, DOC-08) | ✓ VERIFIED | `internal/worker/worker.go:115-123` `isDocumentTerminal` returns `true` for `errors.Is(err, context.DeadlineExceeded)`; `HandleDocumentConvert` (line 251) branches on `isDocumentTerminal` → `MarkFailed` + `fmt.Errorf("%w: %v", asynq.SkipRetry, err)` (no asynq retry); `TestIsDocumentTerminal` passes |
| 3 | A stranded document job (`jobs.engine == 'document'`) is recovered by the reconciler through the document queue, never misrouted onto the image queue (Roadmap SC3, DOC-09) | ✓ VERIFIED | `internal/reconciler/reconciler.go:131-149` `switch j.Engine { case "image": ...; case "document": EnqueueDocumentConvert; default: fail-closed skip }`; `TestSweepRoutesDocumentJobsToDocumentQueue` and `TestSweepRoutesImageJobsToImageQueue`-equivalent (`TestSweepRecoversUnderCap` with `Engine:"image"`) both pass, asserting no cross-contamination between `imageCalls`/`documentCalls` |
| 4 | A stranded job with an unrecognized engine value fails closed — non-fatal, metric-visible skip, never misrouted, never crashes the sweep (DOC-09 security addendum) | ✓ VERIFIED | `reconciler.go` `default` arm calls `metrics.RecordReconcilerAction("unroutable_engine")` and `continue`s without enqueue/RequeueStale; `TestSweepSkipsUnknownEngine` passes |
| 5 | A document task can be built and routed to a dedicated `document` asynq queue (`TypeDocumentConvert`/`QueueDocument`), separate from the image queue | ✓ VERIFIED | `internal/queue/queue.go:20,28` consts present; `NewDocumentConvertTask` builds task with `asynq.Queue(QueueDocument)`; `TestDocumentConvertTaskRoundTrip` passes |
| 6 | Document tasks carry a per-job `asynq.Unique` lock TTL DERIVED from `DOCUMENT_MAX_RETRY`/`DOCUMENT_ENGINE_TIMEOUT` (never hardcoded) | ✓ VERIFIED | `DocumentUniqueTTL(maxRetry, engineTimeout)` formula matches `ImageUniqueTTL`'s derivation, reuses shared `uniqueTTLSafetyMargin`; `TestDocumentUniqueTTL` asserts exact 1370s value plus monotonicity in both args |
| 7 | `queue.Client` exposes `EnqueueDocumentConvert` using the derived retry budget/TTL | ✓ VERIFIED | `internal/queue/client.go:100-109`; `NewClient` populates `documentMaxRetry`/`documentUniqueTTL` from env with correct defaults (3 / 300s) |
| 8 | `RetryDelayFunc` dispatches document tasks to a document-specific no-jitter backoff, not webhook's jittered schedule or asynq's default | ✓ VERIFIED | `queue.go:228-239` `case TypeDocumentConvert: return DocumentRetryDelay(...)`; `documentRetrySchedule` has no jitter (5s/15s/30s fixed); `TestDocumentRetryDelaySchedule`/`TestRetryDelayFuncDispatch` pass |
| 9 | `StaleJob.Engine` populated from `jobs.engine` via `FindStale`, no migration needed | ✓ VERIFIED | `internal/jobs/repo.go:38-42` `Engine string` field; `FindStale` SQL is `SELECT id, status, engine FROM jobs`, scan is `rows.Scan(&j.ID, &j.Status, &j.Engine)`; `WebhookGapJob`/`FindWebhookGaps` untouched |
| 10 | `cmd/document-worker` bounds each attempt by `DOCUMENT_ENGINE_TIMEOUT` (default 300s) and uses a separate `DOCUMENT_WORKER_CONCURRENCY` (default 2, not shared with image) | ✓ VERIFIED | `cmd/document-worker/main.go:68,84`: `envDuration("DOCUMENT_ENGINE_TIMEOUT", 300*time.Second)`, `envInt("DOCUMENT_WORKER_CONCURRENCY", 2)` |
| 11 | A NON-timeout transient document failure is retried, bounded by `DOCUMENT_MAX_RETRY` | ✓ VERIFIED | `HandleDocumentConvert`'s non-terminal branch returns the raw error unwrapped so asynq retries on `documentRetrySchedule`, bounded by the task's `MaxRetry` set from `documentMaxRetry`; classifier logic unit-tested (`TestIsDocumentTerminal`'s "transient default" case) |
| 12 | `cmd/document-worker` does NOT consume the webhook queue; it PRODUCES webhook-delivery tasks on completion | ✓ VERIFIED | `grep -c 'HandleFunc' cmd/document-worker/main.go` = 1 (only `TypeDocumentConvert`); `grep 'TypeWebhookDeliver' cmd/document-worker/main.go` = 0; `HandleDocumentConvert` calls `h.enqueuer.EnqueueWebhookDeliver` on completion (lines 261, 274) |
| 13 | `cmd/document-worker` does NOT run a reconciler sweep loop | ✓ VERIFIED | `grep -c 'reconciler' cmd/document-worker/main.go` = 0; explicit comment at line 76-78 documents this is intentional (D-05) |
| 14 | `Dockerfile.worker` is libvips-only again — LibreOffice/fonts/tini genuinely removed | ✓ VERIFIED | `grep -v '^#' Dockerfile.worker \| grep -c 'libreoffice\|tini\|fonts-'` = 0; `ENTRYPOINT ["/usr/local/bin/worker"]` (no tini wrapper); `libvips-tools` still present |
| 15 | `Dockerfile.document-worker` builds `cmd/document-worker` with LibreOffice + fonts + tini-as-PID-1 | ✓ VERIFIED | Builds `-o /out/document-worker ./cmd/document-worker`; runtime installs all 3 `libreoffice-*-nogui` packages + 3 font packages + `tini`; `ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/document-worker"]` |
| 16 | `docker-compose.yml` runs a `document-worker` service with its own timeout/concurrency and the SAME `cpus:2.0`/`memory:1g` limits as the image worker | ✓ VERIFIED | `docker-compose.yml:126-154`; identical `deploy.resources.limits` block to the `worker` service; `docker compose config -q` exits 0 (valid config, confirmed live in this environment) |
| 17 | `.env.example` documents `DOCUMENT_WORKER_CONCURRENCY`, `DOCUMENT_ENGINE_TIMEOUT`, `DOCUMENT_MAX_RETRY` | ✓ VERIFIED | `.env.example:32-35`, all three present with inline comments, matching established style |

**Score:** 17/17 truths verified

### Deferred Items

| # | Item | Addressed In | Evidence |
|---|------|-------------|----------|
| 1 | Full container image build + live document conversion smoke test (real docker build + actual LibreOffice conversion end-to-end) | Phase 11 | Plan 10-04's own `<verification>` section explicitly defers this: "Full container image build + live document conversion smoke is intentionally deferred to Phase 11's end-to-end verification (roadmap Phase 11 success criterion 4)." Phase 11 SC4 confirms: "A live end-to-end test converts all 6 supported format pairs ... through the full upload → convert → download pipeline." |

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/queue/queue.go` | `TypeDocumentConvert`, `QueueDocument`, `NewDocumentConvertTask`, `documentRetrySchedule`, `DocumentRetryDelay`, `documentBackoffSum`, `DocumentUniqueTTL`, `RetryDelayFunc` document arm | ✓ VERIFIED | All six identifiers present; no `webhookBackoffSum`/`webhookJitterCeiling` references in document code |
| `internal/queue/client.go` | `documentMaxRetry`/`documentUniqueTTL` fields, `NewClient` wiring, `EnqueueDocumentConvert` | ✓ VERIFIED | Present with correct defaults (3, 300s) |
| `internal/queue/queue_test.go` | `TestDocumentUniqueTTL`, `TestDocumentRetryDelaySchedule`, `TestDocumentConvertTaskRoundTrip` | ✓ VERIFIED | All three present and passing |
| `internal/jobs/repo.go` | `StaleJob.Engine` field + `FindStale` selecting/scanning engine | ✓ VERIFIED | Present, correct SQL/scan |
| `internal/reconciler/reconciler.go` | `enqueuer.EnqueueDocumentConvert` + engine-routing switch with fail-closed default | ✓ VERIFIED | Present |
| `internal/reconciler/reconciler_test.go` | `fakeEnqueuer.EnqueueDocumentConvert` + document-routing and unknown-engine-skip tests | ✓ VERIFIED | `documentCalls`, `TestSweepRoutesDocumentJobsToDocumentQueue`, `TestSweepSkipsUnknownEngine` all present and passing |
| `internal/worker/worker.go` | `HandleDocumentConvert` + engine-scoped `isDocumentTerminal` + LibreOffice terminal signatures | ✓ VERIFIED | All present, wired correctly; shared `isTerminal` correctly left without a timeout arm (image path regression-safe) |
| `internal/worker/worker_test.go` | document handler + terminal-classification tests | ⚠️ PARTIAL | `isDocumentTerminal`/`isTerminal` classifier tests present and passing (`TestIsDocumentTerminal`, `TestIsTerminalLibreOfficeSignatures`, `TestIsTerminalTimeoutUnchanged`); **no end-to-end `HandleDocumentConvert` handler test exists** (confirmed: `grep HandleDocumentConvert internal/worker/worker_test.go` returns nothing) — see Anti-Patterns/Gaps below (code review WR-03) |
| `cmd/document-worker/main.go` | document-worker entrypoint consuming only the document queue | ✓ VERIFIED | Present, builds, matches all plan substitutions |
| `Dockerfile.worker` | reverted libvips-only image worker runtime | ✓ VERIFIED | Confirmed 0 libreoffice/tini/fonts- references |
| `Dockerfile.document-worker` | LibreOffice document-worker runtime image | ✓ VERIFIED | Present, correct package set + entrypoint |
| `docker-compose.yml` | document-worker service definition | ✓ VERIFIED | Present, `docker compose config -q` validates clean |
| `.env.example` | document worker env var documentation | ✓ VERIFIED | Present |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/queue/client.go` | `internal/queue/queue.go` | `NewClient` calls `DocumentUniqueTTL(...)`; `EnqueueDocumentConvert` calls `NewDocumentConvertTask` | ✓ WIRED | Confirmed by reading both files |
| `internal/reconciler/reconciler.go` | `internal/jobs/repo.go` | `sweep` switches on `j.Engine` (`StaleJob.Engine` populated by `FindStale`) | ✓ WIRED | Confirmed |
| `internal/reconciler/reconciler.go` | `internal/queue/client.go` | `enqueuer` interface method `EnqueueDocumentConvert` satisfied by `*queue.Client` | ✓ WIRED | `go build ./...` succeeds, confirming interface satisfaction |
| `cmd/document-worker/main.go` | `internal/worker/worker.go` | `mux.HandleFunc(queue.TypeDocumentConvert, h.HandleDocumentConvert)` | ✓ WIRED | Confirmed at line 81 |
| `internal/worker/worker.go` | `internal/convert` | `h.process` reuses `registry.Lookup` — `LibreOfficeConverter` already registered in `convert.Default` | ✓ WIRED | `process()` is unchanged/shared, confirmed engine-agnostic in the code |
| `docker-compose.yml` | `Dockerfile.document-worker` | `document-worker` service `build.dockerfile` | ✓ WIRED | Confirmed, `docker compose config -q` passes |
| `Dockerfile.document-worker` | `cmd/document-worker` | `go build -o /out/document-worker ./cmd/document-worker` | ✓ WIRED | Confirmed |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full module builds | `go build ./...` | exit 0 | ✓ PASS |
| Full module vets clean | `go vet ./...` | exit 0, no output | ✓ PASS |
| Queue/reconciler/worker/jobs unit tests pass | `go test ./internal/queue/... ./internal/reconciler/... ./internal/worker/... ./internal/jobs/...` | all non-integration tests PASS (integration/soak tests SKIP — no `DATABASE_URL`/`REDIS_ADDR` in this environment, expected) | ✓ PASS |
| docker-compose config valid | `docker compose config -q` | exit 0 | ✓ PASS |
| Dockerfile.worker attack-surface removal | `grep -v '^#' Dockerfile.worker \| grep -c 'libreoffice\|tini\|fonts-'` | 0 | ✓ PASS |
| Live document conversion through actual containers | N/A — not runnable in this environment (requires full docker-compose stack + real .docx/.odt fixtures) | — | ? SKIP (explicitly deferred to Phase 11 per plan) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| DOC-07 | 10-03, 10-04 | Document conversion runs in a separate worker process/container, resource-isolated from image worker | ✓ SATISFIED | `cmd/document-worker` binary + `Dockerfile.document-worker` + compose service with own `DOCUMENT_WORKER_CONCURRENCY`/resource limits |
| DOC-08 | 10-01, 10-03 | Document conversion uses separate `DOCUMENT_ENGINE_TIMEOUT`, timeout classified as terminal | ✓ SATISFIED | `DOCUMENT_ENGINE_TIMEOUT` env var, `isDocumentTerminal` treats `context.DeadlineExceeded` as terminal, unit-tested |
| DOC-09 | 10-02 | Reconciler correctly recovers stranded document jobs via document queue (engine-aware), not image queue | ✓ SATISFIED | Engine-routing switch in `sweep()`, unit-tested for both document/image routing and unknown-engine fail-closed skip |

Note: `.planning/REQUIREMENTS.md` still shows DOC-07/DOC-08/DOC-09 as unchecked (`[ ]`) and "Pending" in the Traceability table — this is a documentation/traceability bookkeeping gap (the requirements ARE satisfied in code per the evidence above), not a code gap. Recommend updating REQUIREMENTS.md's checkboxes and Traceability table to reflect Phase 10 completion.

No orphaned requirements: all three phase requirement IDs (DOC-07, DOC-08, DOC-09) are claimed across the four plans' frontmatter `requirements:` fields.

### Anti-Patterns Found

No `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` markers found in any file modified by this phase.

The prior code review (`10-REVIEW.md`, status: `issues_found`) flagged four WARNING-level issues, independently re-confirmed by this verification by reading the actual code:

| File | Line(s) | Pattern | Severity | Impact |
|------|---------|---------|----------|--------|
| `internal/worker/worker.go` | 256 | `HandleDocumentConvert`'s terminal branch hardcodes the client-facing message `"unsupported or corrupted input format"` even when the actual cause is a `DOCUMENT_ENGINE_TIMEOUT` expiry (not corrupted input) | ⚠️ WARNING | Client-facing `GET /jobs/{id}` / webhook payload reports a misleading reason for a legitimately-timed-out (possibly valid, large) document — a UX/diagnostics defect, not a functional violation of DOC-08 (the job IS still correctly terminally-failed, not retried forever) |
| `docker-compose.yml` | 108-119 (worker), 139-149 (document-worker) | `IMAGE_MAX_RETRY`/`ENGINE_TIMEOUT`/`DOCUMENT_MAX_RETRY`/`DOCUMENT_ENGINE_TIMEOUT` are not explicitly set on every service that constructs a `queue.Client` (`api`, `worker`) — only rely on matching hardcoded Go defaults | ⚠️ WARNING | Currently harmless (defaults line up across services), but fragile: an operator changing `DOCUMENT_ENGINE_TIMEOUT` on `document-worker` alone would silently desync the enqueue-time TTL derivation in `api`/`worker`, reopening the double-processing race the code's own comments warn against |
| `internal/worker/worker_test.go` | (entire file) | `HandleDocumentConvert` has no handler-level test exercising the full success/terminal/transient paths with fakes — only the pure predicate functions (`isTerminal`, `isDocumentTerminal`) are tested | ⚠️ WARNING | Real classifier logic IS covered and passing; but this exact test gap is why the WR-01 message-mismatch defect shipped undetected |
| `internal/reconciler/reconciler.go` | 137-149 | An unroutable `engine` value is swept forever with no recovery-count increment, so it can never reach `MaxRecoveries`/terminal exhaustion | ⚠️ WARNING (accepted per plan's own threat model, T-10-04, disposition "accept") | Intentional per-plan trade-off; documented in-code; genuinely non-fatal and metric-visible, matching the plan's explicit fail-closed design |

None of these rise to BLOCKER: no must-have truth is falsified by them (DOC-07/08/09 and all roadmap success criteria are functionally satisfied — terminal classification correctly happens, retries correctly bounded, routing correctly engine-aware); they are code-quality/robustness gaps a maintainer should address, not a broken goal.

### Human Verification Required

None. All must-haves are verifiable from source, passing unit tests, and validated tooling (`go build`, `go vet`, `go test`, `docker compose config`). The one item that genuinely requires a live environment (an actual container build + real LibreOffice conversion smoke test) is explicitly and reasonably deferred to Phase 11 by the phase's own plan, and Phase 11's roadmap success criteria explicitly cover it.

### Gaps Summary

No blocking gaps. All 3 roadmap Success Criteria for Phase 10 and all 3 requirement IDs (DOC-07, DOC-08, DOC-09) are verified against actual code: a dedicated `cmd/document-worker` binary exists and is resource-isolated via its own Dockerfile and Docker Compose service; a `DOCUMENT_ENGINE_TIMEOUT` expiry is deterministically classified terminal (not retried forever) via the engine-scoped `isDocumentTerminal`; and the reconciler recovers stranded document jobs exclusively through `EnqueueDocumentConvert`, with a fail-closed, metric-visible skip for any other/unknown engine value. `go build ./...`, `go vet ./...`, and the full non-integration test suite for `internal/queue`, `internal/reconciler`, `internal/worker`, and `internal/jobs` all pass. `docker compose config -q` validates the new `document-worker` service.

Four WARNING-level issues carried over from the prior code review were independently re-confirmed in this verification (see Anti-Patterns table above) — none of them falsify a must-have truth, but they represent real latent risk (a misleading client-facing timeout message, and docker-compose env-var drift risk) that should be fixed before this ships to production, and a test-coverage gap that should be closed to prevent regressions. Recommend either fixing these in a follow-up plan before Phase 11, or explicitly accepting them via a VERIFICATION.md override with a stated rationale and owner.

Additionally, `.planning/REQUIREMENTS.md` has not been updated to mark DOC-07/DOC-08/DOC-09 as done (still shows `[ ]`/"Pending") — a documentation bookkeeping gap, not a code gap.

---

_Verified: 2026-07-09T23:30:00Z_
_Verifier: Claude (gsd-verifier)_
