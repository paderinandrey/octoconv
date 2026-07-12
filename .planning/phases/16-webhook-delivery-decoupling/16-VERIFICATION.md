---
phase: 16-webhook-delivery-decoupling
verified: 2026-07-12T06:05:00Z
status: gaps_found
score: 7/7 truths verified (roadmap SC1-3 + plan must_haves); 1 unresolved Critical + 1 live-confirmed Warning carried over from code review, treated as a phase-goal gap
overrides_applied: 0
gaps:
  - truth: "Webhook delivery does not silently stop working for any single-process failure mode (the phase's core value: 'без риска для стабильности... надёжно')"
    status: partial
    reason: "PGAdvisoryLock.TryAcquire's error path (internal/reconciler/reconciler.go:141-154) hard-closes the dedicated pgxpool.Conn but never calls conn.Release(), permanently burning one pgxpool slot on every transient Postgres fault (CR-01 in 16-REVIEW.md, confirmed present in the current code — not fixed since the review ran). db.Connect uses pgxpool defaults (no pool_max_conns override in compose), so the default cap is small (~4-8); after a handful of transient faults over the process lifetime, pool.Acquire blocks forever for every DB operation in that replica (repo.Get, webhookRepo.RecordAttempt, sweep queries, and the lock's own lazy re-Acquire) — the webhook-worker then silently stops delivering webhooks and sweeping WITHOUT crashing, so `restart: always` never recovers it. This reintroduces a silent-webhook-loss failure mode (via pool exhaustion) that is structurally identical in spirit to the failure mode Phase 16 exists to eliminate (worker-absence causing silent webhook loss) — just triggered by a different root cause. Independently reproduced live in this verification: a plain SIGTERM to a webhook-worker container (simulating a normal graceful redeploy) never allows the process to exit — it logs '🛑 shutting down webhook-worker...' and 'bye 👋' but the container stays 'Up' indefinitely (confirmed for 20+s beyond the app's own shutdown log lines) because `defer pool.Close()` in cmd/webhook-worker/main.go blocks forever on the never-released dedicated advisory-lock connection (WR-01 in 16-REVIEW.md). Every graceful redeploy of either replica will hang until the orchestrator/container runtime force-kills it (SIGKILL after grace period) — operationally significant for a service whose stated purpose is surviving partial-fleet deploys."
    artifacts:
      - path: "internal/reconciler/reconciler.go"
        issue: "TryAcquire error path (lines ~141-154) closes the raw pgconn but never Release()s the pgxpool.Conn wrapper, permanently leaking a pool slot per fault; PGAdvisoryLock has no Close()/Release method at all, so cmd/webhook-worker cannot release the dedicated connection at shutdown either"
      - path: "cmd/webhook-worker/main.go"
        issue: "No lock.Close() (or equivalent) call before/around `defer pool.Close()`, so graceful shutdown hangs forever holding the dedicated advisory-lock connection open"
    missing:
      - "Release() the pgxpool.Conn wrapper (not just Conn().Close()) in TryAcquire's error path, per 16-REVIEW.md CR-01's suggested fix, so a hard-closed connection's pool slot is actually reclaimed"
      - "Add PGAdvisoryLock.Close()/Release() and call it from cmd/webhook-worker/main.go before/around pool.Close() so SIGTERM/SIGINT lead to a clean, bounded-time process exit (16-REVIEW.md WR-01)"
deferred: []
---

# Phase 16: Webhook Delivery Decoupling Verification Report

**Phase Goal:** Webhook-доставка результата переживает отсутствие или падение любого одного engine-воркер-процесса — деплой любого подмножества воркеров больше не может молча терять вебхуки.
**Verified:** 2026-07-12T06:05:00Z
**Status:** gaps_found
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria — the contract)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | SC1: stopping `cmd/worker` (image) does not prevent a document/html job's completion webhook from being delivered | ✓ VERIFIED | Structural guarantee confirmed by code: `cmd/worker/main.go` registers only `mux.HandleFunc(queue.TypeImageConvert, ...)` and has zero webhook wiring (`grep TypeWebhookDeliver cmd/worker/main.go` → no match); `cmd/webhook-worker/main.go` is the only binary registering `mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)` (confirmed: exactly one match across `cmd/*`). Live evidence embedded in 16-04-SUMMARY.md (receiver hit + per-worker `/metrics` delta + `webhook_deliveries` row with `delivered=t`) is consistent with this structural guarantee. Not independently re-run end-to-end in this verification (full E2E harness + document-worker build was out of scope for the time budget) but the code-level guarantee that makes SC1 true is unconditional, not test-dependent. |
| 2 | SC2: killing one of ≥2 redundant webhook-consumer processes mid-delivery loses/duplicates zero webhooks; survivor drains the queue | ✓ VERIFIED | Two real named services (`webhook-worker-1`/`-2`, not `deploy.replicas`) confirmed in `docker-compose.yml`. asynq at-least-once redelivery + `webhook_deliveries`/`asynq.Unique` idempotency (D-06) reused verbatim from `internal/worker/worker.go`, unchanged this phase. Live evidence in 16-04-SUMMARY.md: exactly one `delivered=true` row after killing the in-flight consumer, human-approved including the at-least-once raw-socket nuance. Not independently re-run in this verification. |
| 3 | SC3: exactly one reconciler-sweeper instance active fleet-wide; no duplicate-sweep race; auto-failover | ✓ VERIFIED (independently reproduced live in this verification) | Brought up `postgres`+`redis`+`minio`+`webhook-worker-1`+`webhook-worker-2` fresh via `docker compose up -d --build`. Waited one sweep interval: `SELECT count(*) FROM pg_locks WHERE locktype='advisory'` = **1**, held by pid 92 / `192.168.147.5` = `octoconv-webhook-worker-1`. Killed that container (`docker kill octoconv-webhook-worker-1`); lock count immediately dropped to 0 (session-close auto-release). Polled until failover: count returned to **1** at a **different backend pid (94)**, `client_addr 192.168.147.6` = `octoconv-webhook-worker-2`, ~49s after the kill (well within the 1m `RECONCILER_SWEEP_INTERVAL` + one extra tick bound). This is genuine, independently-confirmed auto-failover, not a restart artifact (webhook-worker-1 was not yet restarted when the new pid appeared). |

### Plan Must-Haves (16-01 through 16-04 frontmatter)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 4 | Exactly one holder sweeps (advisory lock gate); fail-safe on lock-check error | ✓ VERIFIED | `internal/reconciler/reconciler.go` `RunWithLock` only calls `s.sweep(ctx)` when `TryAcquire` returns `(true, nil)`; unit tests `TestRunWithLockSweepsWhenAcquired/SkipsWhenNotAcquired/FailSafeSkipsOnLockError/StopsOnContextCancel` all pass (`go test ./internal/reconciler/... -count=1` — 15 passed, 2 soak tests self-skip without `DATABASE_URL`, none failed). |
| 5 | Lock is Postgres session-level on a dedicated connection separate from the repo pool, so leader death auto-releases it | ✓ VERIFIED | `NewPGAdvisoryLock` acquires one `*pgxpool.Conn` and never returns it during process life; confirmed live in SC3 re-test above — killing the holder's container immediately dropped the fleet-wide advisory lock count to 0. |
| 6 | Zero new Go module dependency | ✓ VERIFIED | `git diff go.mod go.sum` across the phase's commit range shows no `require` changes; `go build ./...` and `go vet ./...` both clean at HEAD. |
| 7 | `cmd/webhook-worker` is the sole webhook consumer + sole sweeper host, with storage wired for `PresignGet`, and fails closed without `WEBHOOK_SIGNING_SECRET`; `cmd/worker` fully demoted to image-only | ✓ VERIFIED | `grep storage.New cmd/webhook-worker/main.go` matches; `grep 'WEBHOOK_SIGNING_SECRET must be set'` matches; `cmd/worker/main.go` has zero `TypeWebhookDeliver`/`reconciler`/`NewSweeper`/`QueueWebhook` references. Confirmed each of the four worker binaries registers exactly one `mux.HandleFunc` handler type. |

**Score:** 7/7 individual truths verified true — but see the Gaps section below: a Critical + a live-confirmed Warning from 16-REVIEW.md constitute a genuine, unaddressed threat to the phase's core value (silent webhook-delivery loss), reintroduced via a different mechanism (Postgres pool exhaustion / non-terminating graceful shutdown) than the one this phase set out to close.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/reconciler/reconciler.go` | `AdvisoryLock`/`PGAdvisoryLock`/`RunWithLock` | ✓ VERIFIED | All present, wired, unit-tested; contains the unfixed CR-01 leak (see Gaps) |
| `internal/reconciler/advisorylock_test.go` | Gate-logic unit tests | ✓ VERIFIED | 4 subtests, all pass, deterministic (context-cancel bounded) |
| `cmd/webhook-worker/main.go` | Sole webhook consumer + lock-gated sweeper, storage-wired | ✓ VERIFIED | Builds, `go vet` clean, `gofmt` clean; runs live (confirmed in this verification) |
| `Dockerfile.webhook-worker` | Clean debian-slim, no engine packages | ✓ VERIFIED | Grep for `tini\|libvips\|libreoffice\|chromium\|chrome` → no matches; builds successfully (rebuilt live in this verification) |
| `cmd/worker/main.go` | Image-only, webhook/sweeper fully removed | ✓ VERIFIED | No `TypeWebhookDeliver`/`reconciler`/`NewSweeper`/`QueueWebhook` references |
| `docker-compose.yml` | `webhook-worker-1`/`-2` services, image worker stripped of webhook env | ✓ VERIFIED | Both services present, both depend on postgres+redis+minio, both build `Dockerfile.webhook-worker`; `worker:` block has no `RECONCILER_`/`WEBHOOK_SIGNING_SECRET`/`WEBHOOK_PRESIGN_TTL` lines |
| `docker-compose.e2e.yml` | Host-gateway wiring on webhook-worker services | ✓ VERIFIED | `extra_hosts: host.docker.internal:host-gateway` present on both; `docker compose -f docker-compose.yml -f docker-compose.e2e.yml config` parses cleanly |
| `.env.example` | `WEBHOOK_WORKER_CONCURRENCY` + webhook-worker-only annotations | ✓ VERIFIED | Present with a dedicated `# Webhook worker` section |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `cmd/webhook-worker/main.go` mux | `worker.HandleWebhookDeliver` | `mux.HandleFunc(queue.TypeWebhookDeliver, ...)` | WIRED | Confirmed via grep and live run |
| `cmd/webhook-worker/main.go` sweeper | `reconciler.PGAdvisoryLock` + `RunWithLock` | `NewPGAdvisoryLock(ctx, pool)` then `go sweeper.RunWithLock(ctx, lock)` | WIRED | Confirmed via grep and live run (SC3 reproduction above) |
| `cmd/webhook-worker/main.go` store | `storage.New(ctx)` | passed into `worker.NewHandler` | WIRED | Confirmed via grep; not independently load-tested against a live `PresignGet` call in this verification (deferred to the SC1 evidence already captured in 16-04-SUMMARY.md) |
| `docker-compose.yml webhook-worker-1/-2` | `Dockerfile.webhook-worker` | `build.dockerfile` | WIRED | Confirmed; images built and ran successfully live |

### Data-Flow Trace (Level 4)

Not applicable in the strict UI-rendering sense — this phase is infra/backend topology, not a data-rendering component. The equivalent check (does the advisory lock actually reflect real Postgres session state, not a stubbed/hardcoded value) was performed live: `pg_locks` counts and `pg_stat_activity` backend correlation were queried against a running Postgres instance and matched expected values before and after a kill, confirming the lock state genuinely flows from Postgres, not a fake/hardcoded return.

### Behavioral Spot-Checks / Live Reproduction (performed in this verification, beyond SUMMARY claims)

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Steady-state: exactly one advisory-lock holder fleet-wide | `docker compose up -d --build postgres redis minio createbucket webhook-worker-1 webhook-worker-2`, then `SELECT count(*) FROM pg_locks WHERE locktype='advisory'` | `1`, held by webhook-worker-1 (client_addr 192.168.147.5, pid 92) | ✓ PASS |
| Auto-failover after killing the lock holder | `docker kill octoconv-webhook-worker-1`; poll `pg_locks` | Count dropped to 0 immediately, returned to 1 at a **different pid (94)** on webhook-worker-2 ~49s later | ✓ PASS |
| Graceful shutdown terminates in bounded time | `docker kill -s SIGTERM octoconv-webhook-worker-2`; poll container status | Container logged its full app-level shutdown sequence ("🛑 shutting down..." → asynq graceful shutdown → "bye 👋") but **never exited** — still `Up` 20+s later | ✗ FAIL — confirms 16-REVIEW.md WR-01 live |

### Requirements Coverage

| Requirement | Source Plan(s) | Description | Status | Evidence |
|-------------|-----------------|--------------|--------|----------|
| WEBH-01 | 16-01, 16-02, 16-03, 16-04 | Webhook delivery survives any single-process failure across a subset of engine workers | ✓ SATISFIED (functionally, per SC1-3) with a residual reliability gap (see Gaps) | Code + live evidence above. **Tracking discrepancy (info, not a code gap):** `.planning/REQUIREMENTS.md` still shows `WEBH-01` as `[ ]` unchecked / "Pending" in its coverage table (lines 40, 83), while `ROADMAP.md` marks Phase 16 `[x]` complete — these two tracking documents are out of sync; not a code defect, likely pending an orchestrator bookkeeping pass. |

No orphaned requirements found for Phase 16 — WEBH-01 is the only requirement mapped, and all 4 plans declare it.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/reconciler/reconciler.go` | ~141-154 | `TryAcquire` error path hard-closes but never `Release()`s the pgxpool.Conn wrapper | 🛑 Blocker (carried over from 16-REVIEW.md CR-01, confirmed present, unresolved) | Permanent pool-slot leak per transient fault → eventual silent, crash-free halt of webhook delivery and sweeping |
| `cmd/webhook-worker/main.go` | 39, 93 | No `lock.Close()`/`Release()` call before/around `defer pool.Close()` | ⚠️ Warning (16-REVIEW.md WR-01, confirmed live in this verification) | Graceful shutdown never completes; every redeploy relies on the orchestrator's force-kill timeout |
| `cmd/document-worker/main.go`, `cmd/chromium-worker/main.go` | ~55, 70-74 | Dead webhook wiring (`WEBHOOK_SIGNING_SECRET` read, `webhook.NewRepo`/`NewDeliverer` constructed) never exercised (mux never registers `TypeWebhookDeliver` in these binaries) | ⚠️ Warning (16-REVIEW.md WR-02, confirmed present) | Not a functional gap today (verified no mux registration) but a latent hazard if either binary is later wired for webhooks with an empty/inert secret |
| `docker-compose.yml` | 154-212 | `webhook-worker-1`/`-2` omit `IMAGE_MAX_RETRY`/`ENGINE_TIMEOUT`/`DOCUMENT_*`/`HTML_*` vars that `queue.NewClient()` uses to derive unique-lock TTLs | ⚠️ Warning (16-REVIEW.md WR-03, confirmed present) | Currently harmless (defaults match engine-worker values) but silently defeats the anti-drift purpose of the derived TTL if an operator changes those vars only on the engine-worker services |

No unreferenced `TBD`/`FIXME`/`XXX` debt markers found in the phase's modified files.

### Human Verification Required

None beyond what Plan 16-04's blocking checkpoint already captured (human already approved SC1/SC2/SC3 live evidence per 16-04-SUMMARY.md). This verification independently reproduced SC3 and found no reason to re-open that approval.

### Gaps Summary

All three ROADMAP success criteria (SC1/SC2/SC3) and all plan-level must-haves are genuinely true in the codebase — SC3 was independently reproduced live in this verification session (fresh stack, real `pg_locks` failover), and SC1/SC2's structural preconditions (mux registration exclusivity, D-06 idempotency reuse) were independently confirmed in code even though the full document/html E2E drill was not re-run end-to-end here.

However, this verification also independently reproduced a live-blocking consequence of 16-REVIEW.md's already-documented Critical finding (CR-01) and its companion Warning (WR-01): the `PGAdvisoryLock`'s dedicated connection is hard-closed but never `Release()`d on error, permanently leaking a pgxpool slot per transient Postgres fault, and is never released at all at shutdown, so graceful termination (SIGTERM) never completes. Both defects share the same root cause (the dedicated advisory-lock connection's lifecycle is write-only — acquired but never properly returned under any code path) and both were confirmed to reproduce on live infrastructure in this session, not just in static review. Because the project's own stated goal for this phase is eliminating *silent* webhook-delivery loss, and CR-01 reintroduces exactly that failure mode via pool exhaustion (crash-free, restart-does-not-help), this is reported as a phase-goal gap rather than filed purely as a code-quality note — it directly undermines the "надёжно" (reliably) clause of the project's Core Value statement, even though it does not falsify SC1/SC2/SC3 as literally worded.

**Recommendation:** Do not treat Phase 16 as fully closed until a follow-up plan lands the two fixes 16-REVIEW.md already specifies (Release() the conn on the TryAcquire error path; add PGAdvisoryLock.Close() and call it from cmd/webhook-worker's shutdown path before pool.Close()). This is a small, well-understood fix with a clear acceptance test (pool.Stat().AcquiredConns() returns to baseline after a forced TryAcquire error; SIGTERM causes process exit within a bounded time). If the team judges the residual risk acceptable for the current milestone (e.g., because this is explicitly a not-yet-production-ready internal service, per CLAUDE.md), add a verification override recording that decision instead of treating this as a blocking gap:

```yaml
overrides:
  - must_have: "Webhook delivery does not silently stop working for any single-process failure mode"
    reason: "CR-01/WR-01 pool-leak and shutdown-hang accepted as known tech debt for this pre-production milestone; tracked for a dedicated follow-up fix plan before broader deployment"
    accepted_by: "{name}"
    accepted_at: "{ISO timestamp}"
```

---

_Verified: 2026-07-12T06:05:00Z_
_Verifier: Claude (gsd-verifier)_
