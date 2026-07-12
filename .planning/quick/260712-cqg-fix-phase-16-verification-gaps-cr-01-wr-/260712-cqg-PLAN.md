---
phase: quick-260712-cqg
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/reconciler/reconciler.go
  - cmd/webhook-worker/main.go
  - internal/reconciler/advisorylock_conn_test.go
autonomous: true
requirements: [WEBH-01]

must_haves:
  truths:
    - "A forced TryAcquire error reclaims the pgxpool slot (AcquiredConns returns to baseline)"
    - "PGAdvisoryLock.Close() releases the dedicated connection (AcquiredConns returns to baseline)"
    - "webhook-worker SIGTERM exits in bounded time (lock.Close runs before pool.Close)"
    - "TryAcquire and Close are safe under the shutdown-time concurrent race"
  artifacts:
    - path: "internal/reconciler/reconciler.go"
      provides: "TryAcquire error-path Release + PGAdvisoryLock.Close + mutex guard"
      contains: "func (l *PGAdvisoryLock) Close()"
    - path: "cmd/webhook-worker/main.go"
      provides: "defer lock.Close() registered after defer pool.Close()"
      contains: "defer lock.Close()"
    - path: "internal/reconciler/advisorylock_conn_test.go"
      provides: "Pool-slot reclaim + Close integration tests (DATABASE_URL-gated)"
      contains: "AcquiredConns"
  key_links:
    - from: "cmd/webhook-worker/main.go"
      to: "PGAdvisoryLock.Close"
      via: "defer lock.Close() after defer pool.Close()"
      pattern: "defer lock\\.Close\\(\\)"
    - from: "internal/reconciler/reconciler.go TryAcquire error path"
      to: "pgxpool slot reclaim"
      via: "conn.Release() after Conn().Close(ctx)"
      pattern: "conn\\.Release\\(\\)"
---

<objective>
Close Phase 16 verification gaps CR-01 and WR-01: the dedicated advisory-lock
connection's lifecycle is currently write-only (acquired but never properly
returned under any code path), which (CR-01) permanently leaks one pgxpool slot
on every transient Postgres fault, and (WR-01) hangs webhook-worker graceful
shutdown forever inside `pool.Close()`.

Purpose: eliminate the silent-webhook-loss failure mode (pool exhaustion) and
the non-terminating SIGTERM that this pre-production internal service exists to
avoid — restoring the "надёжно" clause of the Core Value.
Output: TryAcquire error path reclaims its slot; PGAdvisoryLock gains Close();
webhook-worker releases the lock before closing the pool at shutdown; a mutex
guards the shutdown-time race; a DATABASE_URL-gated test asserts slot reclaim.

## Concurrency decision (explicit, per constraint)

A `sync.Mutex` guard on `l.conn` IS warranted and is included in this plan.

Rationale: `main` spawns `go sweeper.RunWithLock(ctx, lock)` and never joins it.
On SIGTERM, ctx is cancelled and `main` returns; the deferred `lock.Close()`
then runs concurrently with the not-yet-returned sweeper goroutine, whose
`select` may still fire one final `ticker.C` case and call `TryAcquire` before
it observes `ctx.Done()`. `Close()` (writes `l.conn = nil`, calls `Release`) and
`TryAcquire` (reads/writes `l.conn`, runs a query on it) would then touch
`l.conn` with no synchronization — a genuine data race that `go test -race`
would flag, and a real correctness hazard (Close could Release the conn
mid-query). Contention is effectively zero (TryAcquire fires once per sweep
interval, Close once at shutdown), so holding the mutex across the whole
TryAcquire body is acceptable: Close simply waits for any in-flight TryAcquire
to finish, then releases. This is the minimal correct fix.
</objective>

<execution_context>
@/Users/apaderin/dev/octoconv/.claude/get-shit-done/workflows/execute-plan.md
@/Users/apaderin/dev/octoconv/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@.planning/phases/16-webhook-delivery-decoupling/16-REVIEW.md
@.planning/phases/16-webhook-delivery-decoupling/16-VERIFICATION.md
@internal/reconciler/reconciler.go
@cmd/webhook-worker/main.go
@internal/reconciler/advisorylock_test.go
@internal/reconciler/reconciler_soak_test.go

<interfaces>
<!-- Contracts the executor needs. Extracted from the codebase — no exploration required. -->

Current PGAdvisoryLock (internal/reconciler/reconciler.go:108-156):
- struct fields: pool *pgxpool.Pool, conn *pgxpool.Conn
- NewPGAdvisoryLock(ctx, pool) (*PGAdvisoryLock, error) — Acquires one dedicated conn, never returns it during process life.
- TryAcquire(ctx) (bool, error) — lazily re-acquires l.conn if nil; runs SELECT pg_try_advisory_lock($1); on query error currently does l.conn.Conn().Close(ctx); l.conn = nil (the CR-01 leak — no Release()).

pgxpool.Conn (github.com/jackc/pgx/v5/pgxpool):
- .Conn() *pgx.Conn — underlying raw connection; .Close(ctx) hard-closes it.
- .Release() — returns the pool resource; calling it on an already-closed conn destroys the resource rather than recirculating it (this is exactly what reclaims the slot).
- pool.Stat().AcquiredConns() int32 — currently-acquired slot count.

webhook-worker shutdown ordering (cmd/webhook-worker/main.go):
- line 39: defer pool.Close() (registered FIRST -> runs LAST)
- line 93: lock, err := reconciler.NewPGAdvisoryLock(ctx, pool)
- Defers run LIFO. To make lock.Close() run BEFORE pool.Close(), register defer lock.Close() AFTER defer pool.Close() (i.e. right after NewPGAdvisoryLock succeeds).

Test-helper pattern to reuse (internal/reconciler/reconciler_soak_test.go:21-36):
- newSoakTestPool(t) — os.Getenv("DATABASE_URL") == "" -> t.Skip; else db.Connect + db.Migrate + t.Cleanup(pool.Close). Same-package (white-box) test.
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Fix advisory-lock connection lifecycle (CR-01 Release, WR-01 Close, mutex guard)</name>
  <files>internal/reconciler/reconciler.go, cmd/webhook-worker/main.go</files>
  <action>
In internal/reconciler/reconciler.go:

1. Add "sync" to the stdlib import group. Add a `mu sync.Mutex` field to the PGAdvisoryLock struct (guards conn). Extend the struct doc comment with a one-line why: the mutex serializes TryAcquire against a shutdown-time Close() invoked from a different goroutine.

2. In TryAcquire, take l.mu.Lock() / defer l.mu.Unlock() at the top of the method body (before the l.conn == nil lazy re-acquire check) so the entire body — lazy re-acquire, the pg_try_advisory_lock query, and the error path — runs under the lock. Add a why-comment: held across the query because Close() may run concurrently at shutdown and must not Release the conn mid-query; contention is near-zero (once per sweep interval).

3. Fix the CR-01 leak in TryAcquire's query-error path. Replace `l.conn.Conn().Close(ctx); l.conn = nil` with: capture `conn := l.conn`, set l.conn = nil, then conn.Conn().Close(ctx) (hard-close so Postgres releases the session lock immediately) followed by conn.Release() (reclaim the now-dead pgxpool slot — Release on a closed conn destroys the resource rather than recirculating a still-lock-holding session). Keep the existing detailed why-comment and extend it to state that hard-close and Release are complementary, not mutually exclusive (per 16-REVIEW.md CR-01). Preserve the returned error: return false, fmt.Errorf("pg_try_advisory_lock: %w", err).

4. Add a new method Close() on *PGAdvisoryLock (WR-01): takes l.mu.Lock()/defer l.mu.Unlock(), and if l.conn != nil calls l.conn.Release() then sets l.conn = nil. Doc comment (Go convention, starts with "Close"): releases the dedicated connection and with it the session lock; safe to call once at shutdown; TryAcquire must not be called afterwards; this is what lets pool.Close() return in bounded time. Ensure it is idempotent (nil-guarded so a double call is a no-op).

In cmd/webhook-worker/main.go:

5. Immediately after the `lock, err := reconciler.NewPGAdvisoryLock(ctx, pool)` success block (after the err check, ~line 96), add `defer lock.Close()`. It MUST be registered AFTER defer pool.Close() (main.go:39) so that under LIFO it runs FIRST — releasing the dedicated conn before pool.Close() waits on it. Add a why-comment: registered after pool.Close()'s defer specifically so it runs first, otherwise pool.Close() blocks forever on the never-released advisory-lock connection (WR-01).

Follow CLAUDE.md conventions throughout: gofmt, error wrapping with %w, why-comments near non-obvious code, no logging in internal/.
  </action>
  <verify>
    <automated>gofmt -l internal/reconciler/reconciler.go cmd/webhook-worker/main.go | grep -q . && echo FAIL-gofmt || (go vet ./internal/reconciler/... ./cmd/webhook-worker/... && go build ./... && grep -q 'conn.Release()' internal/reconciler/reconciler.go && grep -q 'func (l \*PGAdvisoryLock) Close()' internal/reconciler/reconciler.go && grep -q 'defer lock.Close()' cmd/webhook-worker/main.go && grep -n 'defer pool.Close()\|defer lock.Close()' cmd/webhook-worker/main.go)</automated>
  </verify>
  <done>reconciler.go: struct has a sync.Mutex; TryAcquire is mutex-guarded and its error path calls conn.Release() after Conn().Close(ctx); Close() exists, is nil-guarded/idempotent, and Release()s the dedicated conn under the mutex. main.go: defer lock.Close() appears in source AFTER defer pool.Close(). go build ./..., go vet, gofmt all clean.</done>
</task>

<task type="auto">
  <name>Task 2: DATABASE_URL-gated test proving pool-slot reclaim and Close release</name>
  <files>internal/reconciler/advisorylock_conn_test.go</files>
  <action>
Create internal/reconciler/advisorylock_conn_test.go (white-box, package reconciler) with two DATABASE_URL-gated tests reusing the existing newSoakTestPool(t) helper (same package, directly callable) for the skip guard + connect/migrate/cleanup.

Test A — TestPGAdvisoryLockReleasesSlotOnError (acceptance criterion (a)):
- pool := newSoakTestPool(t); record baseline := pool.Stat().AcquiredConns().
- lock, err := NewPGAdvisoryLock(ctx, pool) (fatal on err). Assert pool.Stat().AcquiredConns() == baseline+1 (dedicated conn is pinned).
- Force a TryAcquire query error deterministically WITHOUT real DB fault injection: hard-close the dedicated conn out from under the lock via lock.conn.Conn().Close(context.Background()) (accessible — same package). The next SELECT pg_try_advisory_lock then fails on a closed connection.
- Call ok, err := lock.TryAcquire(ctx); assert err != nil and ok == false.
- Assert pool.Stat().AcquiredConns() == baseline — the error path Release()d the slot. Before the fix this stays at baseline+1 (the leak), so the test genuinely gates CR-01.

Test B — TestPGAdvisoryLockCloseReleasesSlot (WR-01 unit-level proof):
- pool := newSoakTestPool(t); baseline := pool.Stat().AcquiredConns().
- lock, _ := NewPGAdvisoryLock(ctx, pool); assert AcquiredConns == baseline+1.
- lock.Close(); assert pool.Stat().AcquiredConns() == baseline. Call lock.Close() a second time to confirm idempotency (no panic, conn already nil).

Use context.Background() for ctx and stdlib testing only (no assertion lib, per CLAUDE.md). Match existing test naming/comment style. Add a comment noting acceptance criterion (b) — SIGTERM bounded process exit — was reproduced live in 16-VERIFICATION.md and is structurally guaranteed by the defer ordering asserted in Task 1's verify (not unit-testable without a container).
  </action>
  <verify>
    <automated>gofmt -l internal/reconciler/advisorylock_conn_test.go | grep -q . && echo FAIL-gofmt || (go vet ./internal/reconciler/... && go test ./internal/reconciler/... -run TestPGAdvisoryLock -race -count=1 -v 2>&1 | grep -Eq 'PASS|SKIP|no test files' && go test ./internal/reconciler/... -count=1)</automated>
  </verify>
  <done>advisorylock_conn_test.go exists with both tests. With DATABASE_URL set they PASS (and Test A fails against un-fixed code, proving it gates CR-01); without it they SKIP cleanly. go test ./internal/reconciler/... -race is green; gofmt/vet clean.</done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| webhook-worker process → Postgres pool | The dedicated advisory-lock conn is a finite, shared resource; mismanaging its lifecycle degrades the whole process, not just the sweeper. |
| shutdown goroutine (main defers) ↔ sweeper goroutine (RunWithLock) | Two goroutines touch l.conn at shutdown without prior synchronization. |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-q16-01 | Denial of Service | TryAcquire error path (pool-slot leak, CR-01) | mitigate | Release() the pgxpool.Conn after hard-close so every transient-fault path reclaims its slot; DATABASE_URL-gated test asserts AcquiredConns returns to baseline. |
| T-q16-02 | Denial of Service | webhook-worker graceful shutdown hang (WR-01) | mitigate | Add PGAdvisoryLock.Close(); register defer lock.Close() after defer pool.Close() so the dedicated conn is released before pool.Close() waits on it, bounding SIGTERM exit. |
| T-q16-03 | Tampering (data race) | l.conn accessed by Close() and a final TryAcquire concurrently at shutdown | mitigate | sync.Mutex guards the full TryAcquire body and Close(); go test -race in Task 2 covers the concurrent-access surface. |
| T-q16-SC | Tampering | package installs | accept | No new dependencies added (uses stdlib sync + existing pgx/pgxpool); no install step, so no legitimacy gate needed. |
</threat_model>

<verification>
- go build ./... clean; go vet ./... clean; gofmt -l reports nothing for the three touched files.
- go test ./internal/reconciler/... -race -count=1 green (new tests PASS with DATABASE_URL, SKIP without).
- Source order in cmd/webhook-worker/main.go: defer pool.Close() (line ~39) precedes defer lock.Close() (~after line 96), guaranteeing lock.Close() runs first under LIFO.
- Manual/live (already reproduced in 16-VERIFICATION.md, re-confirm on next redeploy): docker kill -s SIGTERM on a webhook-worker container now exits within the shutdown grace window instead of hanging.
</verification>

<success_criteria>
- CR-01 closed: a forced TryAcquire error reclaims the pool slot (pool.Stat().AcquiredConns() returns to baseline), proven by TestPGAdvisoryLockReleasesSlotOnError.
- WR-01 closed: PGAdvisoryLock.Close() exists and is wired into webhook-worker shutdown after pool.Close()'s defer, so SIGTERM leads to bounded-time process exit.
- Shutdown-time race on l.conn eliminated by a sync.Mutex; go test -race is clean.
- All conventions honored: gofmt, go vet, %w error wrapping, why-comments near code, no new module dependency.
</success_criteria>

<output>
Create `.planning/quick/260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-/260712-cqg-SUMMARY.md` when done.
</output>
