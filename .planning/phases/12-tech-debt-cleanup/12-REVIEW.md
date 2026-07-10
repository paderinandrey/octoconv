---
phase: 12-tech-debt-cleanup
reviewed: 2026-07-10T09:18:35Z
depth: standard
files_reviewed: 10
files_reviewed_list:
  - internal/convert/convert.go
  - internal/convert/libvips.go
  - internal/convert/libreoffice.go
  - internal/api/handlers.go
  - internal/reconciler/reconciler.go
  - internal/queue/queue.go
  - internal/queue/queue_test.go
  - internal/e2e/e2e_test.go
  - docker-compose.yml
  - docker-compose.e2e.yml
findings:
  critical: 0
  warning: 3
  info: 4
  total: 7
status: issues_found
---

# Phase 12: Code Review Report

**Reviewed:** 2026-07-10T09:18:35Z
**Depth:** standard
**Files Reviewed:** 10
**Status:** issues_found

## Summary

Reviewed the three 12-01 task commits (805c692 engine-constant centralization, ed29167 E2E harness hardening, e3016a2 compose/.env.example reconciliation) plus the full content of the 10 in-scope files. Mechanical verification is clean: `gofmt -l` reports nothing, `go vet ./...` passes, and unit tests for `internal/convert`, `internal/queue`, `internal/api`, `internal/reconciler` all pass. The engine-constant refactor is correct and complete in non-test Go source (verified by grep: no raw `"image"`/`"document"` engine-class literals remain outside `internal/convert/convert.go`), there is no import cycle (`queue → convert` is new but acyclic), and the E2E timeout changes and `api` `extra_hosts` addition are functionally sound — I verified that `WEBHOOK_ALLOW_PRIVATE_IPS`/`WEBHOOK_ALLOW_INSECURE_HTTP` are read only by the API process (`internal/api/callbackurl.go:29,78`, `cmd/api/main.go:90`), so setting them only on `api` in the e2e override is correct.

However, the compose reconciliation commit ships a factually false security comment (WR-01): it claims document-worker signs its own webhooks, directly contradicted by the source line it cites — the "real gap" the commit message claims to fix does not exist. Two robustness defects in the engine-routing fail-closed paths and the reconciler's exhausted path are also flagged.

## Warnings

### WR-01: docker-compose.yml comment falsely claims document-worker signs its own webhook callbacks

**File:** `docker-compose.yml:181-183`
**Issue:** The comment added in commit e3016a2 states: *"document-worker signs its own webhook callbacks (cmd/document-worker/main.go:54) -- it must not fall back to an empty secret (DEBT-05)"*. This is factually wrong, and the very line it cites says the opposite. `cmd/document-worker/main.go:50-54` reads: *"document-worker neither delivers nor signs webhooks (D-06 — cmd/worker remains the sole webhook consumer), so a missing signing secret here is non-fatal; it is passed through to worker.NewHandler only to satisfy its shared signature and is inert for HandleDocumentConvert."* Verified against the code: document-worker's mux registers only `queue.TypeDocumentConvert` and binds only `QueueDocument`; `HandleDocumentConvert` merely enqueues `EnqueueWebhookDeliver` tasks (`internal/worker/worker.go:261,275`), and the only code path that signs (`webhook.SignPayload`, `internal/worker/worker.go:339`) lives in `HandleWebhookDeliver`, registered exclusively in `cmd/worker/main.go`. `WEBHOOK_PRESIGN_TTL` in document-worker is inert for the same reason (`presignTTL` is only used at `internal/worker/worker.go:318`, inside `HandleWebhookDeliver`).

Setting the env vars is harmless defense-in-depth, but the comment misdocuments a security-relevant mechanism: an operator rotating `WEBHOOK_SIGNING_SECRET` or debugging signature mismatches will be misled into believing document-job webhooks are signed by document-worker's secret. They are signed by cmd/worker's secret — if the two services ever carried different values, this comment points diagnosis at the wrong process. The commit message's claim of fixing "a real gap" is also false and should not be trusted by future archaeology.
**Fix:** Correct the comment to match reality:
```yaml
      # document-worker neither delivers nor signs webhooks (cmd/worker is the
      # sole webhook consumer; see cmd/document-worker/main.go:50-54). These
      # two vars are inert here and wired only as defense-in-depth for
      # worker.NewHandler's shared constructor signature (DEBT-05).
      WEBHOOK_SIGNING_SECRET: "dev-only-change-me-in-real-deploys"
      WEBHOOK_PRESIGN_TTL: "6h"
```

### WR-02: Unroutable-engine fail-closed paths combine into a permanent zombie job row

**File:** `internal/api/handlers.go:269-275` (and `internal/reconciler/reconciler.go:138-149`)
**Issue:** `handleCreateJob` commits the job row (`repo.Create`, status `queued`) *before* the engine-routing switch. If the `default` branch ever fires (a future converter registers an engine class without a matching queue case — exactly the drift scenario this phase's constants exist to prevent), the handler returns 500 but the committed row is left in `queued` with no terminal transition. The reconciler cannot rescue it: its own `default` branch (`reconciler.go:148-149`) deliberately records `unroutable_engine` and `continue`s without `RequeueStale` or `MarkFailed`, so `RecoveryCount` never grows and the `MaxRecoveries`-exhaustion path is unreachable. Net effect: a permanently-`queued` row that the client polls as "queued" forever, re-scanned by `FindStale` on every sweep tick indefinitely. Both fail-closed branches are individually documented and deliberate (T-11-02 / T-10-03), but their composition has no terminal path for the job or the client.
**Fix:** In the API, close the gap before it can create the orphan — the engine is known before `repo.Create`, so validate routability first:
```go
// before repo.Create:
switch engine {
case convert.EngineImage, convert.EngineDocument:
default:
	writeError(w, http.StatusInternalServerError, "failed to enqueue job")
	return
}
```
Alternatively (or additionally), have the API's existing `default` branch `MarkFailed` the just-created job (`internal_error`/`unroutable_engine`) so the client observes a terminal status instead of eternal `queued`.

### WR-03: Reconciler exhausted path swallows MarkFailed error, then records metric and fires webhook anyway

**File:** `internal/reconciler/reconciler.go:115-120`
**Issue:** In the cap-exceeded branch, `MarkFailed`'s error is discarded (`_ =`), yet `metrics.RecordReconcilerAction("exhausted")` is recorded unconditionally and `EnqueueWebhookDeliver` fires based on a `Get` snapshot taken *before* `MarkFailed`. If the job legally completed between `FindStale` and here (the same race the recovery loop's comments discuss at length), `MarkFailed` returns an illegal-transition error — but the code still counts an "exhausted" action that never happened (skewing the metric this pattern exists to make visible) and may enqueue a webhook delivery for a job that was already completed and already notified (a duplicate delivery once the asynq unique lock has lapsed; tolerable under at-least-once semantics but avoidable). Pre-existing (not introduced by this phase's diff to this file), flagged because the file is in scope.
**Fix:**
```go
if err := s.store.MarkFailed(ctx, j.ID, "reconciler_exhausted", "recovery attempts exhausted", map[string]any{"action": "reconciler_exhausted"}); err != nil {
	continue // job already terminal — nothing was exhausted
}
metrics.RecordReconcilerAction("exhausted")
if job != nil && job.CallbackURL != "" {
	_ = s.enq.EnqueueWebhookDeliver(ctx, j.ID)
}
```

## Info

### IN-01: "No other file may hold a raw literal" doc comment overclaims

**File:** `internal/convert/convert.go:15-16`
**Issue:** The comment asserts *"No other file may hold a raw 'image'/'document' engine-class literal."* This is false as written: `internal/db/migrations/0001_init.sql:48` necessarily holds `CHECK (engine IN ('image', 'document', ...))`, and numerous `_test.go` files (`internal/api/handlers_test.go:543`, `internal/reconciler/reconciler_test.go`, `internal/jobs/repo_test.go`, `internal/webhook/repo_test.go`, `internal/metrics/metrics_test.go`) intentionally assert the wire values. More importantly, the DB CHECK constraint is a silent runtime co-owner of these values — changing `EngineImage` compiles cleanly but breaks every insert at runtime. The "SINGLE compile-time source of truth" claim should acknowledge that boundary.
**Fix:** Reword to "no other non-test Go source file", and add a cross-reference: "these values are mirrored by the `jobs.engine` CHECK constraint in `internal/db/migrations/0001_init.sql` — changing them requires a migration."

### IN-02: METRICS_ADDR 127.0.0.1 binding inside containers makes /metrics unscrapeable

**File:** `docker-compose.yml:100,144,185`
**Issue:** All three services set `METRICS_ADDR: "127.0.0.1:9090"`. The `.env.example` rationale ("localhost-only listener", D-19) is written for processes running directly on the host; inside a container's network namespace, a loopback bind means `/metrics` is unreachable from every other container and from the host — metrics are effectively disabled in the compose deployment except via `docker exec`. Pre-existing, but this phase's stated goal was reconciling compose against `.env.example`, and the value was copied without adapting the trust reasoning to container networking.
**Fix:** In compose, bind to `0.0.0.0:9090` (containers are not host-exposed unless a `ports:` mapping is added, so the D-19 trust model is preserved by simply not publishing the port), or document that metrics are intentionally exec-only in dev compose.

### IN-03: E2E suite leaks provisioned client rows in the live Postgres

**File:** `internal/e2e/e2e_test.go:89-114`
**Issue:** `provisionClient` inserts a real `clients` row (`e2e-test-client-<rand>`) with a live, working API key into the stack's Postgres and never deletes it. Every E2E run accumulates another credentialed client row. For a long-lived shared stack this is both clutter and a slowly-growing set of valid API keys.
**Fix:** Add `t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM clients WHERE id = $1", id) })` using the id returned by `repo.Create`.

### IN-04: writeJSON disables HTML escaping while error bodies echo attacker-controlled filenames

**File:** `internal/api/handlers.go:164-165,172-173,356`
**Issue:** `writeJSON` sets `SetEscapeHTML(false)` globally (needed for presigned URLs), and the 422 rejection paths echo the client-supplied `filename` and declared/detected formats into the response body unescaped (e.g. `"unrecognized file content for "+filename`). The response carries `Content-Type: application/json` and no route sets `X-Content-Type-Options: nosniff`. With internal-only clients the practical XSS risk is low, but a `<script>`-bearing filename now round-trips byte-for-byte into a response body; any downstream consumer that embeds the error string into HTML inherits the problem. Pre-existing, not from this phase's diff.
**Fix:** Add `middleware.SetHeader("X-Content-Type-Options", "nosniff")` (or equivalent) to the router, and/or scope `SetEscapeHTML(false)` to the responses that actually carry presigned URLs (`handleGetJob`), keeping default escaping for error bodies.

---

_Reviewed: 2026-07-10T09:18:35Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
