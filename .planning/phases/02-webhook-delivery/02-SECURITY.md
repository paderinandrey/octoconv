---
phase: 02
slug: webhook-delivery
status: verified
threats_open: 0
asvs_level: 1
created: 2026-07-04
---

# Phase 02 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|----------------|
| client → API (POST /v1/jobs) | Client supplies an arbitrary `callback_url` string | Untrusted URL string |
| worker → client callback endpoint | Outbound HTTPS POST driven by asynq, to an external/potentially-adversarial receiver | Signed job payload incl. presigned download URL |
| worker → Postgres | Delivery-attempt audit records + dead-letter state | Delivery status, HTTP codes |
| env → worker process | `WEBHOOK_SIGNING_SECRET` loaded from environment | HMAC secret material |
| worker → S3/MinIO (presign) | Fresh presigned download URL minted per delivery attempt | Presigned URL embedded in payload |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-02-01 | Information Disclosure / SSRF | `validateCallbackURL` (internal/api/callbackurl.go) | mitigate | Rejects non-https scheme and any resolved address matching loopback/RFC1918/link-local/metadata/unspecified; called before any storage side-effect | closed |
| T-02-01b | SSRF (DNS rebinding residual) | callback_url resolution at delivery time | accept | Host validated once at creation only; never re-resolved per delivery attempt | closed (accepted) |
| T-02-02 | Spoofing / Tampering / Info Disclosure | HMAC secret usage (sign.go, WEBHOOK_SIGNING_SECRET) | mitigate | HMAC-SHA256 over timestamp+body; secret is a func param, fail-fast at worker startup if unset, never logged/echoed | closed |
| T-02-03 | Information Disclosure | presigned download_url in webhook payload | mitigate | Fresh presigned URL regenerated on every delivery attempt inside `HandleWebhookDeliver`; only included when job status is `done` | closed |
| T-02-04 | Tampering / Replay | signed payload | mitigate | Timestamp embedded in the signed message and sent as `X-OctoConv-Timestamp`, enabling receiver-side replay rejection | closed |
| T-02-04a | Tampering (error leakage) | handleCreateJob 400 response | mitigate | Fixed string `"invalid callback_url"` response; internal validation reason never echoed to the client | closed |
| T-02-05 | Denial of Service (retry storm) | asynq MaxRetry + WebhookRetryDelay | mitigate | `MaxRetry(6)` bounds retries; `WebhookRetryDelay` implements 30s→1m→2m→4m→8m→15m schedule with ±25% jitter. Code-review off-by-one (CR-02) confirmed fixed in current code (0-based index, no `-1` shift) and covered by a passing regression test (`TestWebhookRetryDelaySchedule`, re-run in this audit — PASS for n=0,1,5,6,100) | closed |
| T-02-06 | Repudiation | webhook_deliveries + dead_letter | mitigate | Every delivery attempt is recorded via `RecordAttempt` (INSERT ... RETURNING id); exhausted deliveries flagged `dead_letter=true` via `MarkDeadLetter` inside `pgx.BeginFunc`. **Residual gap** (see Anti-Pattern Note below): WR-01/WR-02 from code review are real, narrow-edge-case silent-failure paths that partially contradict the plan's literal "nothing silently dropped" claim — tracked, not blocking | closed (with residual gap noted) |
| T-02-07 | Denial of Service (slow receiver) | Deliverer.hc timeout | mitigate | `NewDeliverer` builds `http.Client{Timeout: 10 * time.Second}` (D-08); bounds resource hold per attempt | closed |
| T-02-08 | Information Disclosure / SSRF (redirect bypass) | Deliverer HTTP client redirect handling | mitigate | `CheckRedirect` returns `http.ErrUseLastResponse` — no redirect is ever followed, so a 30x response cannot be used to route the worker to a blocked address, closing the full-bypass gap found in code review (CR-01). Confirmed present in current `internal/webhook/deliver.go` (not just claimed in REVIEW.md) | closed |
| T-02-SC | Tampering (supply chain) | new imports | accept | No package-manager installs across all 3 plans — `go.mod`/`go.sum` unchanged since phase 1; only stdlib (`crypto/hmac`, `crypto/sha256`, `net/netip`, `net/url`, `math/rand`, etc.) and already-vendored `asynq`/`pgx`/`uuid`/`minio` used | closed (accepted) |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-02-01 | T-02-01b | callback_url host is resolved and validated once at job creation (`validateCallbackURL`), not re-resolved per delivery attempt. A DNS-rebinding attacker who controls the callback_url's DNS record could repoint it to an internal address after creation passes validation. Accepted because clients are internal-only company services (PROJECT.md constraint), not public/adversarial tenants; per-delivery re-resolution was explicitly deferred (D-03) to avoid added latency/complexity for a threat actor class not present in this deployment. Confirmed in code: `HandleWebhookDeliver` (internal/worker/worker.go) never calls `validateCallbackURL` again, delivering directly to `job.CallbackURL` read from Postgres. | Plan 02-01/02-03 authors | 2026-07-04 |
| AR-02-02 | T-02-SC | No new third-party/package-manager dependencies introduced in phase 2 — only Go stdlib and dependencies already vendored as of phase 1 (asynq, pgx, uuid, minio-go). Confirmed: `go.mod`/`go.sum` last changed in the phase-1 ratelimit commit (`26fc50e`), no diff since. No supply-chain legitimacy gate required. | Plan 02-01/02-02/02-03 authors | 2026-07-04 |

*Accepted risks do not resurface in future audit runs unless the underlying code changes (e.g., a per-delivery re-resolution call is added/removed, or a new dependency is introduced).*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-04 | 11 | 11 | 0 | gsd-security-auditor |

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-07-04

---

## Audit Notes (evidence trail)

All evidence below was gathered by reading the actual implementation files and re-running the cited tests in this audit session — not by trusting SUMMARY.md/REVIEW.md/VERIFICATION.md prose alone.

### T-02-01 — SSRF/scheme guard
- `internal/api/callbackurl.go:16-67` — `validateCallbackURL` parses the URL, enforces `https` (or `http` only when `WEBHOOK_ALLOW_INSECURE_HTTP=true`), resolves IP-literal or hostname, and rejects via `isBlockedIP`.
- `internal/api/handlers.go:79-91` — `validateCallbackURL` call (line 81) precedes `s.storage.Upload` (line 91): confirmed by direct line-order read, not inferred.
- Re-ran `go test ./internal/api/ -run 'CallbackURL|BlockedIP' -v` in this session: all 13 subtests PASS (loopback, RFC1918 x3, metadata, unspecified, public-allowed, empty, not-a-url, insecure-http-rejected-by-default, public-https-allowed).

### T-02-01b — DNS rebinding (accept)
- Confirmed `internal/worker/worker.go`'s `HandleWebhookDeliver` contains no call to `validateCallbackURL` or any re-resolution logic; delivery uses `job.CallbackURL` read fresh from Postgres via `h.repo.Get`, unchanged from creation time. Matches the declared accepted-risk scope exactly.

### T-02-02 — HMAC secret handling
- `internal/webhook/sign.go:19-24` — `SignPayload(secret []byte, timestamp int64, body []byte) string`, secret is a parameter (never read from env inside the package).
- `cmd/worker/main.go:42-45` — fail-fast: `if len(signingSecret) == 0 { log.Fatalf("WEBHOOK_SIGNING_SECRET must be set") }`. Grepped all `log.*` call sites in `cmd/worker/main.go`: none logs the secret value itself, only the fixed diagnostic string.
- Re-ran `go test ./internal/webhook/ -run SignPayload -v`: 5/5 PASS.

### T-02-03 — Fresh presigned URL per attempt
- `internal/worker/worker.go:125-139` — inside `HandleWebhookDeliver`, `h.store.PresignGet` is called on every invocation (i.e., every delivery attempt, since each attempt is a separate asynq task execution), guarded by `job.Status == jobs.StatusDone`. No caching/reuse of a URL across attempts exists in this function.

### T-02-04 — Replay-resistant timestamp
- `internal/webhook/sign.go:20` — message is `<timestamp>.<body>`.
- `internal/webhook/deliver.go:48` — `X-OctoConv-Timestamp` header set from the same `timestamp` value passed into `SignPayload`.

### T-02-04a — No error-detail leakage
- `internal/api/handlers.go:82` — `writeError(w, http.StatusBadRequest, "invalid callback_url")`, fixed string; the actual `err` from `validateCallbackURL` is discarded, matching the codebase's established no-leak convention.

### T-02-05 — Bounded retry + backoff/jitter (CR-02 re-verified)
- `internal/queue/queue.go:65-71` — `NewWebhookDeliverTask` attaches `asynq.MaxRetry(6)`.
- `internal/queue/queue.go:101-119` — `WebhookRetryDelay` indexes `webhookRetrySchedule` directly with `n` (no `-1` shift) — the off-by-one flagged in REVIEW.md (CR-02) is fixed in the code as it exists today, not merely claimed fixed.
- Re-ran `go test ./internal/queue/ -run WebhookRetryDelaySchedule -v` in this session: PASS, asserting n=0,1,5,6,100 map to 30s/1m/15m/15m/15m within ±25% jitter band.

### T-02-06 — Audit trail + dead-letter
- `internal/webhook/repo.go:25-52` — `RecordAttempt` (single INSERT ... RETURNING id) and `MarkDeadLetter` (guarded `pgx.BeginFunc` UPDATE) both present and match the plan's declared shape.
- `internal/worker/worker.go:162-177` — both are called from `HandleWebhookDeliver` on every attempt; `MarkDeadLetter` gated on `recErr == nil && retryCount >= maxRetry`.
- **Residual gap (not a blocker, but flagged per adversarial-audit stance):** if `RecordAttempt` itself fails on a *successful* HTTP delivery (`derr == nil`), the function returns `nil` without ever surfacing `recErr` — no row is written and nothing signals the loss. This is a real, narrow scenario where the plan's own text ("nothing silently dropped") is not literally true. Additionally (WR-02), `HandleImageConvert`'s `_ = h.enqueuer.EnqueueWebhookDeliver(ctx, jobID)` (worker.go:85, 92) silently discards enqueue failures with zero log/signal — if Redis is briefly down at job completion, that job's webhook is lost with no operator visibility. Both were correctly classified as WARNING (not BLOCKER) by 02-REVIEW.md and re-confirmed non-blocking by 02-VERIFICATION.md, since the core "attempt is recorded" mechanism functions correctly on the observed/common path (confirmed via live end-to-end test in 02-VERIFICATION.md: two real rows inserted for a real success and a real failure delivery). Recommend a Phase 3 follow-up (reconciler sweep for `jobs` rows with `callback_url` set, `status IN ('done','failed')`, and no corresponding `webhook_deliveries` row) to close this residual gap fully.

### T-02-07 — Slow-receiver timeout
- `internal/webhook/deliver.go:28-29` — `&http.Client{Timeout: 10 * time.Second, ...}`.

### T-02-08 — Redirect SSRF bypass (CR-01 re-verified)
- `internal/webhook/deliver.go:27-34` — `NewDeliverer`'s `CheckRedirect` returns `http.ErrUseLastResponse` unconditionally, confirmed present in the code as it exists today (not merely claimed fixed in REVIEW.md/VERIFICATION.md prose). A 3xx response is therefore never followed and falls through to the existing non-2xx failure path.
- Re-ran `go test ./internal/webhook/ -run Deliver -v`: 3/3 PASS (success, non-2xx, timeout).

### T-02-SC — Supply chain (accept)
- `git log --oneline -- go.mod go.sum` shows the last change was `26fc50e` (phase 1, `httprate` for rate limiting) — no diff since. All phase-2 files use only stdlib packages plus already-vendored `github.com/google/uuid`, `github.com/jackc/pgx/v5`, `github.com/hibiken/asynq`.

### Full-tree verification (re-run in this audit session)
- `go build ./...` — exit 0.
- `go vet ./...` — clean.
- `go test ./internal/queue/ -run WebhookRetryDelaySchedule -v` — PASS.
- `go test ./internal/webhook/ -run 'SignPayload|Deliver' -v` — PASS (8/8 subtests).
- `go test ./internal/api/ -run 'CallbackURL|BlockedIP' -v` — PASS (13/13 subtests).

### Unregistered Flags (SUMMARY.md `## Threat Flags`)
None of the three plan SUMMARY.md files (02-01, 02-02, 02-03) contain a `## Threat Flags` section — no executor-surfaced new attack surface to reconcile. The one piece of new attack surface that *did* emerge during implementation (the redirect-following SSRF bypass) was caught by code review rather than self-flagged by the executor, and has been folded into the register as T-02-08 (mitigate, closed) rather than left as an unregistered flag, per this task's explicit instruction.
