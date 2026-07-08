---
phase: 02-webhook-delivery
verified: 2026-07-04T22:15:00Z
status: passed
score: 12/12 must-haves verified
overrides_applied: 0
---

# Phase 2: Webhook Delivery — Verification Report

**Phase Goal:** Clients receive job completion results pushed via signed webhook callbacks, removing the need to poll for status.
**Verified:** 2026-07-04T22:15:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Note on Code Review Fixes

A prior code review (`02-REVIEW.md`) found 2 BLOCKER-level defects, both fixed and committed before this verification:
- **CR-01** (redirect-based SSRF bypass in `internal/webhook/deliver.go`): confirmed fixed — `NewDeliverer` now sets `CheckRedirect` to return `http.ErrUseLastResponse`, so no 3xx redirect is ever followed; a redirect response is correctly treated as a non-2xx failure.
- **CR-02** (off-by-one in `WebhookRetryDelay`): confirmed fixed — `internal/queue/queue.go` now indexes `webhookRetrySchedule` directly with asynq's 0-based `n` (no `-1` shift), and a new regression test `TestWebhookRetryDelaySchedule` in `internal/queue/queue_test.go` asserts the mapping for `n=0,1,5,6,100`. Verified this test exists and passes.

4 warnings (WR-01..04) and 3 info items (IN-01..03) remain open per REVIEW.md. None of them break an observable truth below — see Anti-Patterns section for per-item disposition.

## Goal Achievement

### Observable Truths

Merged from ROADMAP.md Phase 2 success criteria and PLAN frontmatter `must_haves.truths` (02-01/02-02/02-03).

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | [SC1/WEBHOOK-01] When a job with a non-empty `callback_url` completes (done/failed), the service delivers a webhook without the client needing to poll | ✓ VERIFIED | Live end-to-end test (not just unit tests): built and ran the actual `cmd/worker` binary against live Postgres/Redis/MinIO; inserted a `done` job with `callback_url=http://127.0.0.1:9999/hook` directly in Postgres, enqueued `webhook:deliver` via the real `queue.Client.EnqueueWebhookDeliver`, and observed the running worker deliver a real signed HTTP POST to a capture server within ~3s. Also confirmed via code: `HandleImageConvert` calls `h.enqueuer.EnqueueWebhookDeliver` right after both the `MarkDone` and `MarkFailed` paths, guarded by `job.CallbackURL != ""` (`internal/worker/worker.go:84-93`) |
| 2 | [SC2/WEBHOOK-02] The webhook payload is HMAC-SHA256 signed with a timestamp, so receivers can verify authenticity and reject replayed requests | ✓ VERIFIED | Live test: independently recomputed the HMAC-SHA256 signature (`hmac.new(secret, "<ts>.<body>", sha256)`) in Python from the exact captured `X-OctoConv-Timestamp`/body bytes the worker sent, and it byte-for-byte matched the captured `X-OctoConv-Signature` header (`e9d1b408d45b653c60a0cc73ed1604896d77eb15e9c9f21e7d143eb997daaf7b`). Also: `go test ./internal/webhook/ -run SignPayload` (5 tests: determinism, different-secret/timestamp/body, hex format) passes |
| 3 | [SC3/WEBHOOK-03] A failing webhook endpoint receives retried delivery attempts with exponential backoff and jitter, up to a bounded number of attempts | ✓ VERIFIED | `NewWebhookDeliverTask` attaches `asynq.MaxRetry(6)` (bounded). `WebhookRetryDelay`'s off-by-one bug is fixed and regression-tested (`TestWebhookRetryDelaySchedule`, passes). Live test: enqueued a `failed`-status job with `callback_url` pointed at a server returning HTTP 500; the real worker processed it, recorded `status_code=500, delivered=false` in `webhook_deliveries`, and asynq's own stats (`asynq:{webhook}:failed:2026-07-04` key present in Redis) confirm the task was queued for retry rather than silently dropped. Did not wait out the full ~16-30min backoff window to observe exhaustion (impractical for this session) — see truth #4 for dead-letter evidence via integration test instead |
| 4 | [SC4/WEBHOOK-04] Every delivery attempt (status, attempt number, HTTP response code) is recorded in `webhook_deliveries` | ✓ VERIFIED | Live test: both the successful delivery (attempt=1, status_code=200, delivered=true) and the failed delivery (attempt=1, status_code=500, delivered=false) were confirmed via direct `SELECT` against the live `webhook_deliveries` table after real worker processing — not mocked. `internal/webhook/repo.go RecordAttempt` INSERTs one row per attempt with `RETURNING id` |
| 5 | [SC5/WEBHOOK-05] After retries are exhausted, a delivery is marked terminal (dead-letter) rather than silently dropped, and remains available for manual investigation | ✓ VERIFIED | Code + integration test evidence: `HandleWebhookDeliver` calls `h.webhookRepo.MarkDeadLetter(ctx, deliveryID)` when `retryCount >= maxRetry` (`internal/worker/worker.go:170-174`), matching asynq's documented `retried >= maxRetry` exhaustion contract (verified against `hibiken/asynq@v0.26.0` source per REVIEW.md). `TestRecordAttemptAndMarkDeadLetter` (`internal/webhook/repo_test.go`) ran live against Postgres in this session (`go test ./... -count=1` with `DATABASE_URL` set): inserts two attempts, marks one `dead_letter=true`, confirms via direct SELECT the flagged row is `true` and the other remains `false`. Migration 0003 confirmed applied to the live DB (`\d webhook_deliveries` shows `dead_letter boolean NOT NULL DEFAULT false` + partial index) |
| 6 | [02-01] A client can submit `POST /v1/jobs` with a `callback_url` form field and the job is created carrying that URL (per-job, D-02) | ✓ VERIFIED | `internal/api/handlers.go:79-108`: `formFieldCallbackURL` read via `r.FormValue`, threaded into `jobs.CreateParams.CallbackURL`; `internal/jobs/repo.go` INSERT includes `callback_url` column ($7) |
| 7 | [02-01] A `callback_url` pointing at loopback/RFC1918/link-local/metadata (169.254.169.254) is rejected with 400 before any storage write | ✓ VERIFIED | `internal/api/callbackurl.go`: `isBlockedIP` covers `IsLoopback/IsPrivate/IsLinkLocalUnicast/IsLinkLocalMulticast/IsUnspecified`; `validateCallbackURL` call in `handleCreateJob` occurs before `s.storage.Upload` (line order confirmed by reading the file). `go test ./internal/api/ -run 'CallbackURL|BlockedIP'` passes |
| 8 | [02-01] A `callback_url` with a non-https scheme is rejected with 400 unless `WEBHOOK_ALLOW_INSECURE_HTTP=true` | ✓ VERIFIED | `internal/api/callbackurl.go:29-39` — scheme switch rejects non-https/http, http rejected unless env flag set |
| 9 | [02-01] An omitted/empty `callback_url` still creates the job normally (polling path unchanged) | ✓ VERIFIED | `handleCreateJob` only validates when `callbackURL != ""` (`internal/api/handlers.go:80-85`); `GET /v1/jobs/{id}` polling path (`handleGetJob`) untouched by this phase |
| 10 | [02-03] The webhook handler re-reads the job from Postgres and regenerates a FRESH presigned `download_url` per attempt (done only), signs, and delivers | ✓ VERIFIED | Live test: the captured payload's `download_url` was a freshly-generated MinIO presigned URL (`X-Amz-Date=20260704T191141Z`, matching the exact time of the live test run, not a stale/cached value) — confirms `h.store.PresignGet` is called fresh inside `HandleWebhookDeliver`, not once at enqueue time. Code: `internal/worker/worker.go:125-139` |
| 11 | [02-03] Every attempt is recorded; on the final exhausted attempt the row is marked `dead_letter=true` | ✓ VERIFIED | See #4/#5 above — combined live delivery-attempt evidence + `TestRecordAttemptAndMarkDeadLetter` live integration test |
| 12 | Full build/vet/test tree is clean, no regressions introduced | ✓ VERIFIED | `go build ./...` exit 0, `go vet ./...` exit 0, `DATABASE_URL=...5434... go test ./... -count=1` — all packages `ok` (api, auth, clients, convert, jobs, queue, ratelimit, storage, webhook) |

**Score:** 12/12 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/api/callbackurl.go` | SSRF-guarding `validateCallbackURL` + `isBlockedIP` | ✓ VERIFIED | Present, matches plan spec exactly; DNS-free unit tests pass |
| `internal/api/callbackurl_test.go` | DNS-free SSRF/scheme unit tests | ✓ VERIFIED | Present; IP-literal URLs used, no network dependency |
| `internal/jobs/jobs.go` / `repo.go` | `CallbackURL` field on Job/CreateParams, persisted/read | ✓ VERIFIED | Confirmed field present, INSERT column + `deref(cb)` in Get |
| `internal/db/migrations/0003_webhook_dead_letter.sql` | `dead_letter` boolean column + partial index | ✓ VERIFIED | Applied to live DB, confirmed via `\d webhook_deliveries` |
| `internal/webhook/webhook.go` | `Delivery` domain type, package doc | ✓ VERIFIED | Present, mirrors table columns exactly |
| `internal/webhook/sign.go` | `SignPayload` HMAC-SHA256 | ✓ VERIFIED | Present; live-recomputed signature matched a real captured delivery |
| `internal/webhook/repo.go` | `RecordAttempt` + `MarkDeadLetter` | ✓ VERIFIED | Present; live-tested both via direct DB writes from the real worker and via `repo_test.go` |
| `internal/webhook/deliver.go` | Single-attempt HTTPS delivery, 2xx=success, 10s timeout, no-redirect-follow | ✓ VERIFIED | Present; `CheckRedirect` fix (CR-01) confirmed in code; live-tested 200 and 500 outcomes |
| `internal/queue/queue.go` | `TypeWebhookDeliver`/`QueueWebhook`, `WebhookPayload`, `NewWebhookDeliverTask` (MaxRetry=6), `WebhookRetryDelay` | ✓ VERIFIED | Present; off-by-one fix (CR-02) confirmed, regression test passes |
| `internal/worker/worker.go` | `HandleWebhookDeliver` + enqueue-on-completion | ✓ VERIFIED | Present; live end-to-end delivery observed via the real running worker |
| `cmd/worker/main.go` | Handler registration, weighted queues, `RetryDelayFunc`, `WEBHOOK_SIGNING_SECRET` fail-fast | ✓ VERIFIED | Present; live run confirmed startup log `"worker starting (queues=image,webhook)"` and fail-fast behavior (secret required to start) |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/api/handlers.go` | `validateCallbackURL` | call before `s.storage.Upload` | ✓ WIRED | Line order confirmed in file read |
| `internal/jobs/repo.go` | `jobs.callback_url` column | INSERT + SELECT/Scan | ✓ WIRED | Confirmed in Create/Get |
| `internal/worker/worker.go` | `queue.EnqueueWebhookDeliver` | after MarkDone/MarkFailed when `callback_url` set | ✓ WIRED (live-confirmed) | Real worker delivered a webhook for a directly-enqueued task; production call site confirmed at both branches |
| `internal/worker/worker.go` | `storage.PresignGet` | fresh URL per attempt | ✓ WIRED (live-confirmed) | Captured payload's presigned URL timestamp matched the live test's actual execution time |
| `internal/worker/worker.go` | `webhook.Deliverer.Deliver` | HTTP POST with signature/timestamp headers | ✓ WIRED (live-confirmed) | Real HTTP request received and signature independently verified |
| `internal/worker/worker.go` | `webhook.Repo.RecordAttempt` / `MarkDeadLetter` | insert row per attempt / flag on exhaustion | ✓ WIRED (live-confirmed for RecordAttempt; MarkDeadLetter via integration test) | Both a success and a failure attempt were recorded live; dead-letter marking logic verified via `TestRecordAttemptAndMarkDeadLetter` |
| `cmd/worker/main.go` | `WEBHOOK_SIGNING_SECRET` | fail-fast + Handler wiring | ✓ WIRED | Worker required the env var to start in the live test run |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|---------------------|--------|
| `HandleWebhookDeliver` body | `download_url` | `h.store.PresignGet(ctx, outs[0].ObjectKey, h.presignTTL)` against real MinIO | Yes — live-confirmed fresh presigned URL with a timestamp matching the actual delivery time, not a static/cached value | ✓ FLOWING |
| `HandleWebhookDeliver` body | `job.Status`/`error_code`/`error_message` | Re-read via `h.repo.Get(ctx, jobID)` from Postgres per attempt | Yes — live-confirmed job row re-read (status field appeared correctly as `"done"` in the delivered payload) | ✓ FLOWING |
| `webhook_deliveries` rows | `status_code`/`delivered`/`dead_letter` | `RecordAttempt`/`MarkDeadLetter` against real Postgres | Yes — live-confirmed two real rows inserted with correct values from two independent live deliveries (one 200/success, one 500/failure) | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full build/vet | `go build ./... && go vet ./...` | exit 0 | ✓ PASS |
| Full test suite (live DB) | `DATABASE_URL=...5434... go test ./... -count=1` | all packages `ok` | ✓ PASS |
| `WebhookRetryDelay` schedule regression | `go test ./internal/queue/ -run WebhookRetryDelaySchedule -v` | PASS (n=0,1,5,6,100 all map correctly) | ✓ PASS |
| `RecordAttempt`/`MarkDeadLetter` integration | `go test ./internal/webhook/ -run 'Repo\|DeadLetter' -v` (DATABASE_URL set) | PASS | ✓ PASS |
| Live e2e: successful webhook delivery | Ran real `cmd/worker` against live Postgres/Redis/MinIO; inserted a `done` job with `callback_url` pointed at a capture server; enqueued via real `queue.Client`; observed delivery | Capture server received signed POST with correct `download_url`, `job_id`, `status`; DB row `attempt=1, status_code=200, delivered=true` | ✓ PASS |
| Live e2e: failed webhook delivery recorded | Same setup, `callback_url` pointed at a 500-returning server | DB row `attempt=1, status_code=500, delivered=false, dead_letter=false`; asynq recorded the failure for retry (`asynq:{webhook}:failed:...` key present) | ✓ PASS |
| Signature authenticity | Independently recomputed HMAC-SHA256 in Python from captured bytes | Computed signature byte-for-byte matched captured `X-OctoConv-Signature` | ✓ PASS |
| Redirect-following fix (CR-01) | Code inspection: `NewDeliverer`'s `CheckRedirect` returns `http.ErrUseLastResponse` | No redirect followed; 3xx treated as failure | ✓ PASS |

### Probe Execution

No `scripts/*/tests/probe-*.sh` files exist in this repository and none are referenced by this phase's plans. SKIPPED (no probes defined for this project).

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|--------------|--------|----------|
| WEBHOOK-01 | 02-01, 02-03 | Deliver webhook on done/failed with non-empty callback_url, no polling needed | ✓ SATISFIED | Live e2e delivery observed; enqueue-on-completion wired at both branches |
| WEBHOOK-02 | 02-02 | HMAC-SHA256 signed payload with timestamp for replay protection | ✓ SATISFIED | Live-recomputed signature matched; `SignPayload` unit-tested |
| WEBHOOK-03 | 02-03 | Bounded retries with exponential backoff + jitter | ✓ SATISFIED | `MaxRetry(6)`, `WebhookRetryDelay` off-by-one fixed + regression-tested; live failed-delivery correctly recorded as retryable (not prematurely dead-lettered) |
| WEBHOOK-04 | 02-02, 02-03 | Every attempt recorded (status, attempt#, HTTP code) | ✓ SATISFIED | Live-confirmed two real rows (success + failure) with correct fields |
| WEBHOOK-05 | 02-02, 02-03 | Exhausted deliveries marked dead-letter, not dropped, available for investigation | ✓ SATISFIED | `MarkDeadLetter` logic + live integration test (`TestRecordAttemptAndMarkDeadLetter`) confirm the flag is set/preserved correctly |

All 5 requirement IDs declared across this phase's plans (`02-01`, `02-02`, `02-03`) are accounted for and match REQUIREMENTS.md's Phase 2 traceability mapping (`WEBHOOK-01..05`). No orphaned requirements.

**Note (documentation-sync only, non-functional):** `.planning/REQUIREMENTS.md`'s checkbox list still shows `WEBHOOK-01..05` as unchecked (`[ ]`) and the traceability table as "Pending". Verification confirms all five are implemented, tested, and live-demonstrated — this is a tracking-doc gap, not a functional one, consistent with the same pattern noted in Phase 1's verification.

### Anti-Patterns Found

No `TODO`/`FIXME`/`XXX`/`TBD`/`HACK`/`PLACEHOLDER` markers found in any file modified by this phase.

| File | Line | Pattern | Severity | Impact / Disposition |
|------|------|---------|----------|--------|
| `internal/worker/worker.go` | 168-177 (WR-01, REVIEW.md) | `RecordAttempt`'s error (`recErr`) is only checked to gate `MarkDeadLetter`; if it fails on a *successful* delivery, the function returns `nil` with no row inserted and no signal | ⚠️ Warning | Rare edge case (DB write failure at the exact moment of a successful HTTP delivery); does not break the WEBHOOK-04 must-have in the common/observed path (both live test deliveries were correctly recorded). Non-blocking per review disposition |
| `internal/worker/worker.go` | 84-93 (WR-02, REVIEW.md) | Webhook-enqueue failures after job completion (`_ = h.enqueuer.EnqueueWebhookDeliver(...)`) are silently discarded, no log/signal | ⚠️ Warning | Degrades reliability under a Redis hiccup at the exact completion moment; does not break WEBHOOK-01 in the observed/common path (live test enqueue succeeded). Recommended follow-up: enqueue-failure observability or reconciler sweep (noted as Phase 3 candidate in SUMMARY) |
| `internal/api/callbackurl.go` | 53 (WR-03, REVIEW.md) | `net.LookupHost` has no context/timeout, can stall `handleCreateJob` | ⚠️ Warning | Availability/DoS concern on the intake path, not a delivery-correctness gap; does not break any must-have here |
| `internal/worker/worker.go` | 37 (WR-04, REVIEW.md) | `NewHandler` has 9 positional params, 2 same-typed (`time.Duration`) | ⚠️ Warning | Code-quality/maintainability risk only; compiles and behaves correctly today (confirmed via live test), no functional gap |
| `internal/api/callbackurl.go` | 29-36 (IN-01) | `WEBHOOK_ALLOW_INSECURE_HTTP=true` path has zero test coverage | ℹ️ Info | Security-relevant escape hatch untested; recommend adding `t.Setenv` test. Non-blocking |
| `internal/api/callbackurl.go` | 29 (IN-02) | Direct `os.Getenv` read inside validation logic diverges from project's env-var-access convention | ℹ️ Info | Style/testability nit, no functional impact |
| `internal/api/callbackurl.go` | 73-79 (IN-03) | `isBlockedIP` doesn't block CGNAT (100.64.0.0/10) or broader 0.0.0.0/8 | ℹ️ Info | Low-likelihood residual SSRF gap in this deployment; worth a deliberate decision later, not currently exploitable given internal-only client scope (PROJECT.md) |

None of the above anti-patterns are blockers: no unresolved debt markers exist, and none prevent the phase goal (webhook delivery replacing polling, signed, retried, tracked, dead-lettered) from being true today — each was independently confirmed working via live end-to-end testing in this session.

### Human Verification Required

None. All observable truths were confirmed either via live end-to-end testing (running the actual `cmd/worker` binary against live Postgres/Redis/MinIO and a real HTTP capture endpoint, independently verifying the HMAC signature) or via passing automated tests re-run in this session. No item requires subjective/visual human judgment.

### Gaps Summary

No gaps. All 12 observable truths (5 ROADMAP success criteria + 7 supporting PLAN-frontmatter truths) are VERIFIED, not merely claimed by SUMMARY.md. Both BLOCKER-level defects found by the prior code review (CR-01 redirect SSRF bypass, CR-02 retry-schedule off-by-one) were independently re-confirmed fixed in the current code, not just taken on the review's word: `CheckRedirect` returns `http.ErrUseLastResponse` in `internal/webhook/deliver.go`, and `WebhookRetryDelay` in `internal/queue/queue.go` indexes the schedule directly by asynq's 0-based retry count, backed by a passing regression test.

Beyond re-reading code, this verification ran a genuine live end-to-end test distinct from anything in the plans' own test suites: built and ran the real `cmd/worker` binary against the live dev Postgres/Redis/MinIO stack, inserted test job rows directly, enqueued real `webhook:deliver` tasks, and observed:
1. A successful delivery: real signed HTTP POST received by a capture server, with a freshly-minted MinIO presigned URL and a signature independently recomputed and matched byte-for-byte in Python — proving the whole done-path payload/signing/delivery chain, not just its unit-tested pieces in isolation.
2. A failed delivery: correctly recorded (`status_code=500, delivered=false, dead_letter=false`) without premature dead-lettering, and picked up by asynq's own retry bookkeeping.

The 30-minute-scale full-exhaustion path to `dead_letter=true` was not waited out live (impractical for a verification session), but is covered by a passing DB-integration test (`TestRecordAttemptAndMarkDeadLetter`) plus code-level confirmation that the exhaustion condition (`retryCount >= maxRetry`) matches asynq's documented contract.

4 warnings and 3 info items remain open from the code review (non-blocking per the task's explicit instruction) — recommend tracking WR-01/WR-02 (silent failure paths on the webhook hot path) as a near-term hardening follow-up, since they represent the class of gap that would erode WEBHOOK-04/01's guarantees under rare failure conditions, even though they don't fail any must-have as currently observed.

All temporary verification artifacts (test DB rows, background worker/capture processes, a temporary `cmd/verify-tmp` helper binary used only to enqueue a task without modifying production code) were cleaned up; `git status` is clean with no uncommitted changes from this verification session.

---

_Verified: 2026-07-04T22:15:00Z_
_Verifier: Claude (gsd-verifier)_
