---
phase: 02-webhook-delivery
reviewed: 2026-07-04T19:01:44Z
depth: standard
files_reviewed: 17
files_reviewed_list:
  - internal/api/callbackurl.go
  - internal/api/callbackurl_test.go
  - internal/api/handlers.go
  - internal/db/migrations/0003_webhook_dead_letter.sql
  - internal/jobs/jobs.go
  - internal/jobs/repo.go
  - internal/queue/client.go
  - internal/queue/queue.go
  - internal/webhook/deliver.go
  - internal/webhook/deliver_test.go
  - internal/webhook/repo.go
  - internal/webhook/repo_test.go
  - internal/webhook/sign.go
  - internal/webhook/sign_test.go
  - internal/webhook/webhook.go
  - internal/worker/worker.go
  - cmd/worker/main.go
findings:
  critical: 2
  warning: 4
  info: 3
  total: 9
status: issues_found
---

# Phase 2: Code Review Report

**Reviewed:** 2026-07-04T19:01:44Z
**Depth:** standard
**Files Reviewed:** 17
**Status:** issues_found

## Summary

Reviewed the new `internal/webhook` package (HMAC-SHA256 signing, delivery, dead-letter repo), the SSRF-guarded `callback_url` intake in `internal/api`, the asynq retry/backoff wiring in `internal/queue`, and the worker orchestration that ties them together. The signing code (`sign.go`) is correct and well-tested (deterministic, keyed on secret+timestamp+body, unambiguous canonical-string construction — no collision risk from the `timestamp.body` format). The dead-letter detection logic in `HandleWebhookDeliver` correctly matches asynq's documented `retried >= maxRetry` pattern (verified against `hibiken/asynq@v0.26.0` source).

However, two BLOCKER-level defects were found: (1) the webhook HTTP delivery client does not restrict redirects, which lets an attacker-controlled callback endpoint 30x-redirect the worker to a blocked/internal address (e.g. cloud metadata), completely bypassing the SSRF allowlist that `validateCallbackURL` was built to enforce; and (2) the retry backoff schedule (`WebhookRetryDelay`) has an off-by-one indexing bug against asynq's actual `RetryDelayFunc` contract, silently shrinking the documented ~30 minute retry window to ~16 minutes and never using the final (15m) backoff step at all. Neither defect is caught by the existing tests (no test exercises `WebhookRetryDelay`'s numeric output, and no test drives a redirecting callback server).

Several WARNING-level robustness/observability gaps were also found around silently-discarded errors on the webhook delivery hot path, and one constructor-shape quality concern.

## Critical Issues

### CR-01: Webhook delivery follows redirects, bypassing SSRF `callback_url` validation

**File:** `internal/webhook/deliver.go:20-22`
**Issue:** `NewDeliverer` builds `&http.Client{Timeout: 10 * time.Second}` with no `CheckRedirect` policy, so Go's default policy applies: up to 10 automatic redirects are followed for any 3xx response. `validateCallbackURL` (`internal/api/callbackurl.go`) only validates the *original* `callback_url` at job-creation time — it is never consulted again during delivery (this is called out explicitly as an accepted DNS-rebinding risk in `callbackurl.go:11-15`, but that comment does not mention, and this code does not defend against, HTTP redirects).

Since `callback_url` points to a client-controlled (or client-compromised) HTTP endpoint, that endpoint can trivially respond to the webhook POST with `302 Location: https://169.254.169.254/latest/meta-data/iam/security-credentials/...` (or any other blocked/internal target) and the worker will follow it automatically, sending the signed job payload (and, for `307/308`, the full request) to whatever internal address the attacker names — a full SSRF bypass of the allowlist built in `internal/api/callbackurl.go`. This is a materially stronger bypass than the documented DNS-rebinding residual risk: it requires no DNS control at all, just control of the one already-approved HTTP endpoint.
**Fix:**
```go
func NewDeliverer() *Deliverer {
	return &Deliverer{hc: &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Never follow redirects for webhook deliveries: a redirect
			// target has not been through validateCallbackURL and could
			// point at a blocked/internal address (SSRF).
			return http.ErrUseLastResponse
		},
	}}
}
```
Treat any 3xx response as a delivery failure (current status-code check already returns an error for non-2xx, so this is sufficient once redirects are no longer silently followed).

### CR-02: Webhook retry backoff schedule is off-by-one — halves the documented retry window

**File:** `internal/queue/queue.go:99-117`
**Issue:** `WebhookRetryDelay(n int, e error, t *asynq.Task) time.Duration` is registered as asynq's `RetryDelayFunc` (`cmd/worker/main.go:72`). asynq calls this function with `n = msg.Retried`, which is **0-based** — the number of retries already performed *before* this delay is computed (confirmed against `github.com/hibiken/asynq@v0.26.0/processor.go:358`: `p.retryDelayFunc(msg.Retried, e, ...)`, called only when `msg.Retried < msg.Retry`). The doc comment on `WebhookRetryDelay` assumes `n` is already a 1-based "retry number", and the implementation does `idx := n - 1` to convert it — but no such conversion is needed/correct, because the `n < 1 → n = 1` clamp already absorbs the `n == 0` case.

Net effect: every delay after the very first retry is computed one schedule position too early:

| asynq call (`n`) | intended (1-based retry #) | schedule entry used | should be |
|---|---|---|---|
| 0 | 1st retry | `30s` (idx 0) | `30s` ✓ |
| 1 | 2nd retry | `30s` (idx 0) | `1m` (idx 1) ✗ |
| 2 | 3rd retry | `1m` (idx 1) | `2m` (idx 2) ✗ |
| 3 | 4th retry | `2m` (idx 2) | `4m` (idx 3) ✗ |
| 4 | 5th retry | `4m` (idx 3) | `8m` (idx 4) ✗ |
| 5 | 6th retry | `8m` (idx 4) | `15m` (idx 5) ✗ |

The documented "~30 minute total retry window" (D-05, see comment at `queue.go:82-84`) is actually only ~16 minutes (30+30+60+120+240+480s), and the final 15-minute backoff step is never reached at all. This is untested: `internal/queue/queue_test.go` has no test asserting `WebhookRetryDelay`'s numeric output for any `n`.
**Fix:**
```go
func WebhookRetryDelay(n int, e error, t *asynq.Task) time.Duration {
	if n < 0 {
		n = 0
	}
	idx := n // asynq passes msg.Retried (0-based: retries done so far)
	if idx >= len(webhookRetrySchedule) {
		idx = len(webhookRetrySchedule) - 1
	}
	base := webhookRetrySchedule[idx]
	...
}
```
Add a table-driven unit test asserting `WebhookRetryDelay(0, ...)` through `WebhookRetryDelay(5, ...)` map to `30s, 1m, 2m, 4m, 8m, 15m` (ignoring jitter, e.g. by asserting the result falls within ±25% of the expected base).

## Warnings

### WR-01: `RecordAttempt` write failures are silently discarded in the webhook delivery hot path

**File:** `internal/worker/worker.go:168-177`
**Issue:** `deliveryID, recErr := h.webhookRepo.RecordAttempt(...)` — `recErr` is only ever consulted inside the `derr != nil` branch to gate whether `MarkDeadLetter` is safe to call (`recErr == nil && ...`). If `RecordAttempt` fails on a *successful* delivery (`derr == nil`), the function returns `nil` without ever surfacing `recErr` — the delivery genuinely succeeded but leaves **no row** in `webhook_deliveries` for it, and nothing logs or reports this. The audit trail this table exists for (D-10's "operators investigate dead-lettered rows via direct SQL") silently degrades with zero visibility.
**Fix:** At minimum, wrap and return the error so asynq logs it (asynq's `Logger` records handler errors), even though the HTTP delivery itself succeeded — e.g. treat `recErr` as a partial-failure worth surfacing:
```go
if recErr != nil {
    return fmt.Errorf("record webhook attempt for job %s: %w", jobID, recErr)
}
```
placed after the `RecordAttempt` call, before evaluating `derr`. (Since the payload only carries `job_id`, a retry is a correctness-neutral no-op even for an already-delivered job — it just re-attempts recording, or re-delivers idempotently from the client's perspective if it also retries the HTTP call.)

### WR-02: Webhook-enqueue failures after job completion are lost silently, with no log line and no retry path

**File:** `internal/worker/worker.go:84-86, 91-93`
**Issue:** `_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)` discards the error in both the success and failure branches of `HandleImageConvert`. The comment documents this as an intentional "best-effort" trade-off, but unlike the equivalent case in the API (`internal/api/handlers.go:123-127`, where an enqueue failure is at least surfaced to the caller as a 500 and the job stays inspectable in `queued`), there is **no** signal at all here: no log (per convention, `internal/*` packages don't log), no DB flag, nothing. If Redis is briefly unavailable at the exact moment a job finishes, that job's webhook is gone forever with zero operator visibility — directly undermining this phase's "reliable webhook delivery" goal.
**Fix:** Either (a) return the enqueue error unwrapped from `HandleImageConvert` so asynq retries the whole conversion handler — which will cheaply short-circuit on the next attempt via the `MarkActive` illegal-transition guard (already `SkipRetry`-wrapped) after re-attempting the enqueue, or (b) have `cmd/worker/main.go` wire an `asynq.ErrorHandler` that logs `HandleError` results so at least these failures are observable, or (c) add a lightweight periodic reconciler (mentioned as "next steps" for the analogous API-side gap) that scans `jobs` rows with `status IN ('done','failed')`, `callback_url IS NOT NULL`, and no corresponding `webhook_deliveries` row.

### WR-03: SSRF DNS resolution has no timeout/context, exposing a request-handler stall vector

**File:** `internal/api/callbackurl.go:53`
**Issue:** `net.LookupHost(host)` uses the package-level default resolver with no context and no explicit timeout, and `validateCallbackURL` itself takes no `ctx` parameter, so the resolution can't be canceled if the client disconnects. An attacker-controlled (or slow/misconfigured) authoritative DNS server for the `callback_url` host can stall this call for several seconds per attempt (subject to OS resolver retry/timeout behavior), tying up the `handleCreateJob` goroutine for that duration. A handful of concurrent job-creation requests with intentionally slow-resolving hostnames is a cheap resource-exhaustion vector against the API.
**Fix:**
```go
func validateCallbackURL(ctx context.Context, raw string) error {
	...
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	...
}
```
Thread `r.Context()` through from `handleCreateJob`, and consider wrapping with a short (e.g. 2-3s) `context.WithTimeout` bound specifically to the lookup so a single slow DNS server can't consume the full request timeout budget.

### WR-04: `worker.NewHandler` constructor has 9 positional parameters, two of them same-typed

**File:** `internal/worker/worker.go:37`, `cmd/worker/main.go:53-63`
**Issue:** `NewHandler(repo, store, registry, engineTimeout, webhookRepo, deliverer, enqueuer, signingSecret, presignTTL)` now takes 9 positional arguments, including two `time.Duration` values (`engineTimeout` at position 4, `presignTTL` at position 9) that the compiler cannot distinguish if a future edit reorders or adds a parameter near either of them. This is exactly the failure mode the project's own `jobs.CreateParams` / `api.go` interface-segregation conventions elsewhere in this codebase are designed to avoid.
**Fix:** Introduce a `worker.Config` (or `HandlerDeps`) struct with named fields, mirroring `jobs.CreateParams`:
```go
type Config struct {
	Repo          *jobs.Repo
	Store         *storage.Client
	Registry      *convert.Registry
	EngineTimeout time.Duration
	WebhookRepo   *webhook.Repo
	Deliverer     *webhook.Deliverer
	Enqueuer      *queue.Client
	SigningSecret []byte
	PresignTTL    time.Duration
}
func NewHandler(cfg Config) *Handler { ... }
```

## Info

### IN-01: `WEBHOOK_ALLOW_INSECURE_HTTP=true` code path has zero test coverage

**File:** `internal/api/callbackurl.go:29-36`, `internal/api/callbackurl_test.go`
**Issue:** `validateCallbackURL` branches on `os.Getenv("WEBHOOK_ALLOW_INSECURE_HTTP") == "true"` to allow plain `http://` callback URLs, but every case in `TestValidateCallbackURL` runs with the default (unset) environment — the "insecure HTTP explicitly allowed" branch is entirely untested, despite being a security-relevant escape hatch.
**Fix:** Add a test using `t.Setenv("WEBHOOK_ALLOW_INSECURE_HTTP", "true")` asserting `http://8.8.8.8/hook` is accepted and `http://127.0.0.1/hook` is still rejected (i.e., the scheme relaxation doesn't also relax the IP-blocklist check).

### IN-02: Direct `os.Getenv` read inside validation logic diverges from the project's env-var-access convention

**File:** `internal/api/callbackurl.go:29`
**Issue:** Per the documented convention, environment variables are read in `cmd/*/main.go` or in a small explicit list of `internal/{db,queue,storage}` packages; `internal/api` otherwise receives its configuration via `Server` fields (`s.maxUploadByte`, `s.presignTTL`, etc. — see `internal/api/handlers.go:38, 179`). `validateCallbackURL` instead reads `os.Getenv` directly inline, making this one security-relevant setting untestable via dependency injection and inconsistent with how every other toggle in this package is wired.
**Fix:** Thread `allowInsecureHTTP bool` in as a parameter (resolved once in `cmd/api/main.go` and stored on `Server`, alongside `maxUploadByte`/`presignTTL`), e.g. `validateCallbackURL(raw string, allowInsecureHTTP bool) error`.

### IN-03: `isBlockedIP` does not block CGNAT (100.64.0.0/10) or the broader "this network" (0.0.0.0/8) range

**File:** `internal/api/callbackurl.go:73-79`
**Issue:** `isBlockedIP` covers loopback, RFC1918 private space, link-local (including the 169.254.169.254 metadata endpoint), and unspecified — but not RFC 6598 Carrier-Grade NAT space (`100.64.0.0/10`), which cloud providers sometimes use for internal load-balancer/instance-metadata-adjacent traffic, nor the rest of `0.0.0.0/8` beyond the single unspecified address. Low likelihood of being reachable/meaningful in this deployment, but worth a deliberate decision rather than an omission.
**Fix:** If in scope for this project's threat model, add an explicit `netip.MustParsePrefix("100.64.0.0/10").Contains(addr)` check (netip's `IsPrivate`/`IsLoopback`/`IsLinkLocalUnicast` helpers do not cover CGNAT space).

---

_Reviewed: 2026-07-04T19:01:44Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
