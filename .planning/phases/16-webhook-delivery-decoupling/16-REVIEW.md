---
phase: 16-webhook-delivery-decoupling
reviewed: 2026-07-12T05:47:17Z
depth: standard
files_reviewed: 10
files_reviewed_list:
  - cmd/chromium-worker/main.go
  - cmd/document-worker/main.go
  - cmd/webhook-worker/main.go
  - cmd/worker/main.go
  - internal/reconciler/advisorylock_test.go
  - internal/reconciler/reconciler.go
  - docker-compose.yml
  - docker-compose.e2e.yml
  - .env.example
  - Dockerfile.webhook-worker
findings:
  critical: 1
  warning: 4
  info: 4
  total: 9
status: issues_found
---

# Phase 16: Code Review Report

**Reviewed:** 2026-07-12T05:47:17Z
**Depth:** standard
**Files Reviewed:** 10
**Status:** issues_found

## Summary

Reviewed the Phase 16 webhook-delivery decoupling: the new `cmd/webhook-worker` binary (sole webhook consumer + advisory-lock-gated sweeper), the webhook/sweeper removal from `cmd/worker`, the `PGAdvisoryLock` session-lock gate in `internal/reconciler`, and the compose topology with two webhook-worker replicas. Cross-referenced against `internal/worker/worker.go` (nil-registry safety of `HandleWebhookDeliver`), `internal/queue/client.go` (env-derived unique-lock TTLs), and `internal/db/db.go` (pool sizing/close semantics). `go build ./...`, `go vet`, and `gofmt` are clean; the `RunWithLock` gating tests are sound and race-free.

The advisory-lock design itself is correct (fail-safe skip on error/not-leader, dedicated session-scoped connection, correct failover on leader death). However, the `TryAcquire` error path leaks a pool connection slot on every transient DB fault (Critical — cumulative, eventually starves the entire process of Postgres connections with no crash/restart), and the dedicated connection is never releasable, so the webhook-worker's graceful shutdown hangs forever on `pool.Close()`. Two secondary issues: the D-03 "clean cut" was applied only to `cmd/worker` (document/chromium workers still wire live-but-dead webhook dependencies), and the webhook-worker compose services silently omit the env vars that `queue.NewClient` uses to derive the reconciler's unique-lock TTLs.

## Critical Issues

### CR-01: `PGAdvisoryLock.TryAcquire` error path leaks a pool connection slot on every failure

**File:** `internal/reconciler/reconciler.go:151-152`
**Issue:** On a `pg_try_advisory_lock` query error, the code hard-closes the underlying `pgx.Conn` and sets `l.conn = nil`, but never calls `l.conn.Release()`:

```go
l.conn.Conn().Close(ctx)
l.conn = nil
```

Closing the raw `pgx.Conn` does NOT return the `pgxpool` resource to the pool — a `*pgxpool.Conn` must always be `Release()`d, and skipping it leaves the puddle resource permanently in the acquired state. Every transient fault on this path (Postgres restart, failover, network blip — exactly the events this reconnect path exists to survive) permanently burns one pool slot in that replica. `db.Connect` uses `pgxpool.New` with a bare DSN and compose's `DATABASE_URL` sets no `pool_max_conns`, so the default cap is `max(4, NumCPU)` — typically 4 in these containers, with one slot already permanently pinned by the lock itself. After as few as ~3 transient faults over the process lifetime, the pool is exhausted and **every** DB operation in the process — `repo.Get`/`Outputs` in `HandleWebhookDeliver`, `webhookRepo.RecordAttempt`, the sweep queries, and the lock's own lazy re-`Acquire` — blocks indefinitely on `pool.Acquire`. The webhook-worker then silently stops delivering webhooks and sweeping without crashing, so `restart: always` never recovers it. Both replicas run this code every sweep tick (leader and non-leader alike), so both degrade identically.

The comment justifying the hard-close (don't return a lock-holding session to general circulation) is correct — but hard-close and release are not mutually exclusive: releasing an already-closed connection destroys the resource instead of recirculating it.
**Fix:**
```go
// Hard-close the underlying pgconn so Postgres releases the session lock
// immediately, THEN release the (now-dead) pgxpool resource so the pool
// slot is reclaimed — Release() on a closed conn destroys the resource
// rather than returning it to circulation.
conn := l.conn
l.conn = nil
conn.Conn().Close(ctx)
conn.Release()
return false, fmt.Errorf("pg_try_advisory_lock: %w", err)
```

## Warnings

### WR-01: Webhook-worker graceful shutdown hangs forever — the advisory-lock connection is never released and `PGAdvisoryLock` has no Close/Release API

**File:** `internal/reconciler/reconciler.go:108-123`, `cmd/webhook-worker/main.go:39,93`
**Issue:** `NewPGAdvisoryLock` checks out a dedicated pool connection "for the life of the process," and `PGAdvisoryLock` exposes no way to release it. `pgxpool.Pool.Close()` blocks until all acquired connections are returned, so on SIGTERM `cmd/webhook-worker` runs `srv.Shutdown()`, prints `bye 👋`, then blocks forever inside the deferred `pool.Close()` (main.go:39). Worse, `stop()` is the outermost defer and has not yet run, so subsequent SIGTERM/SIGINTs are still swallowed by the (already-fired) `signal.NotifyContext` — the process only dies when the container runtime escalates to SIGKILL after the stop grace period. Every deploy/restart of both replicas takes the full kill timeout and never exits cleanly. The other three worker binaries are unaffected (they never pin a connection).
**Fix:** Add a release method and call it in main after the signal fires (registered as a defer after `pool.Close()`'s, so it runs first):
```go
// reconciler.go
// Close releases the dedicated connection (and with it the session lock).
// Safe to call once at shutdown; TryAcquire must not be called afterwards.
func (l *PGAdvisoryLock) Close() {
	if l.conn != nil {
		l.conn.Release()
		l.conn = nil
	}
}

// cmd/webhook-worker/main.go, right after NewPGAdvisoryLock:
defer lock.Close()
```

### WR-02: D-03 "clean cut" incomplete — document-worker and chromium-worker still wire live webhook-delivery dependencies

**File:** `cmd/document-worker/main.go:55,70-74`, `cmd/chromium-worker/main.go:55,70-74`
**Issue:** `cmd/worker` was properly cleaned (nil `webhookRepo`/`deliverer`/`signingSecret`, zero `presignTTL`), but `cmd/document-worker` and `cmd/chromium-worker` still read `WEBHOOK_SIGNING_SECRET`, construct `webhook.NewRepo(pool)` and `webhook.NewDeliverer()`, and read `WEBHOOK_PRESIGN_TTL` — all dead wiring, since neither binary registers `HandleWebhookDeliver`. This contradicts the phase's own claim in `cmd/webhook-worker/main.go:55-57` ("webhook-worker is now the ONLY signer") and `.env.example:30` ("webhook-worker-only"). It is also a latent hazard, not just dead code: compose does not set `WEBHOOK_SIGNING_SECRET` for these services, so `signingSecret` is an empty byte slice — if a future edit ever registers the webhook handler on either mux (the deps are right there, inviting it), it would HMAC-sign deliveries with an empty key, producing well-formed but unverifiable signatures, bypassing exactly the fail-closed startup check webhook-worker added. The "sole signer" invariant is currently enforced only by mux registration.
**Fix:** Mirror `cmd/worker`'s clean cut in both binaries — drop the `WEBHOOK_SIGNING_SECRET` read and the `internal/webhook` import, and pass `nil, nil, qc, nil, 0` for the webhook-only parameters of `worker.NewHandler`.

### WR-03: webhook-worker compose services omit the env vars that derive the reconciler's unique-lock TTLs — silent default fallback can reopen the T-03-10 double-processing race

**File:** `docker-compose.yml:167-182` (webhook-worker-1), `docker-compose.yml:197-212` (webhook-worker-2)
**Issue:** `queue.NewClient()` (constructed at `cmd/webhook-worker/main.go:63`) derives `imageUniqueTTL`/`documentUniqueTTL`/`htmlUniqueTTL` from `IMAGE_MAX_RETRY`, `ENGINE_TIMEOUT`, `DOCUMENT_MAX_RETRY`, `DOCUMENT_ENGINE_TIMEOUT`, `HTML_MAX_RETRY`, and `HTML_ENGINE_TIMEOUT` (`internal/queue/client.go:67-72`). The webhook-worker is now the ONLY process that runs the sweeper, i.e. the only process that re-enqueues stranded convert tasks — yet its two compose services set none of these vars, so the TTLs silently derive from hardcoded defaults. Today the defaults happen to match the values set on the engine workers, so nothing breaks — but the whole point of the derived TTL (per `ImageUniqueTTL`'s doc: the lock "can never silently drift under asynq's true worst-case retry lifetime if either env var changes later") is defeated: an operator raising e.g. `ENGINE_TIMEOUT` or `IMAGE_MAX_RETRY` on the `worker` service gets no propagation here, and the sweeper's re-enqueue unique lock can then lapse while the engine worker is still legitimately retrying — the exact double-processing race the derivation exists to prevent. Every other producer service (`api`, `worker`, `document-worker`, `chromium-worker`) both sets these vars AND carries an explicit DEBT-05 comment; the two new services have neither.
**Fix:** Add the six vars (matching the values used on the engine-worker services) plus the standard DEBT-05 comment to both `webhook-worker-1` and `webhook-worker-2` environment blocks:
```yaml
      IMAGE_MAX_RETRY: "4"
      ENGINE_TIMEOUT: "120s"
      DOCUMENT_MAX_RETRY: "3"
      DOCUMENT_ENGINE_TIMEOUT: "300s"
      HTML_MAX_RETRY: "3"
      HTML_ENGINE_TIMEOUT: "60s"
```

### WR-04: Exhaustion path enqueues the webhook and records the "exhausted" metric even when `MarkFailed` did not commit (pre-existing, contradicts its own comment)

**File:** `internal/reconciler/reconciler.go:220-226`
**Issue:** Pre-existing code (not introduced this phase, but now the sole sweep implementation hosted by webhook-worker). The cap-exceeded branch discards the `MarkFailed` error and then unconditionally records `RecordReconcilerAction("exhausted")` and enqueues the webhook whenever `job.CallbackURL != ""`:
```go
_ = s.store.MarkFailed(ctx, j.ID, "reconciler_exhausted", ...)
metrics.RecordReconcilerAction("exhausted")
if job != nil && job.CallbackURL != "" {
    _ = s.enq.EnqueueWebhookDeliver(ctx, j.ID)
}
```
The comment above it claims "the failed status is already committed by MarkFailed above" — false: nothing gates on `MarkFailed` succeeding. If `MarkFailed` fails (Postgres blip), `HandleWebhookDeliver` re-reads the row and delivers status `queued`/`active` — a non-terminal status the webhook contract never defines. This is exactly the WR-04 hazard the worker handlers defend against by gating the enqueue on `ferr == nil` (`internal/worker/worker.go:282`, `:347`, `:374`). The metric also over-counts exhaustions that never committed.
**Fix:** Mirror the worker handlers' Postgres-first gating:
```go
ferr := s.store.MarkFailed(ctx, j.ID, "reconciler_exhausted", "recovery attempts exhausted", map[string]any{"action": "reconciler_exhausted"})
if ferr != nil {
    continue // next tick retries; job is still stale
}
metrics.RecordReconcilerAction("exhausted")
if job != nil && job.CallbackURL != "" {
    _ = s.enq.EnqueueWebhookDeliver(ctx, j.ID)
}
```

## Info

### IN-01: `envInt`/`envDuration`/`firstField` duplicated into a fourth binary

**File:** `cmd/webhook-worker/main.go:149-174`
**Issue:** The identical helper trio now exists in `cmd/api`, `cmd/worker`, `cmd/document-worker`, `cmd/chromium-worker`, `cmd/webhook-worker`, and `internal/queue/client.go` — six copies. The per-package-copy convention is documented, but each new binary compounds the drift risk.
**Fix:** Extract to a small `internal/envutil` package on the next refactor pass.

### IN-02: Typo `engineTimout` propagated into a new comment

**File:** `cmd/webhook-worker/main.go:75`
**Issue:** The comment `HandleWebhookDeliver never reads h.engineTimout` faithfully reproduces the pre-existing misspelled field name `Handler.engineTimout` (`internal/worker/worker.go:210`). Renaming the field (`engineTimeout`) would fix both.
**Fix:** Rename the field in `internal/worker/worker.go` and update the comment.

### IN-03: `PGAdvisoryLock` itself has zero test coverage — including the error/reconnect path where CR-01 lives

**File:** `internal/reconciler/advisorylock_test.go`
**Issue:** The tests exercise `RunWithLock`'s gating solely through `fakeLock`; the real `PGAdvisoryLock` (lazy re-acquire, hard-close-on-error, session re-acquire semantics) is untested. The CR-01 connection leak would have been observable in a pool-stat assertion.
**Fix:** Add an integration-style test (guarded by a `DATABASE_URL`-present skip, or against a `pgxmock`-style stub) asserting `pool.Stat().AcquiredConns()` returns to baseline after a forced `TryAcquire` error.

### IN-04: webhook-worker-1/-2 are hand-duplicated compose blocks with no resource limits

**File:** `docker-compose.yml:154-212`
**Issue:** The two replicas duplicate ~30 lines of identical environment/build config (drift between replicas would be silent — e.g. a secret rotated on only one), and both trigger a separate image build of the same Dockerfile. Unlike `worker`/`document-worker`/`chromium-worker`, neither replica carries `deploy.resources.limits`.
**Fix:** Use a YAML extension anchor (`x-webhook-worker: &webhook-worker` with `<<: *webhook-worker` per replica) and share one built `image:` name; add resource limits consistent with the other workers (webhook delivery is lightweight — e.g. `cpus: "0.5"`, `memory: 256m`).

---

_Reviewed: 2026-07-12T05:47:17Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
