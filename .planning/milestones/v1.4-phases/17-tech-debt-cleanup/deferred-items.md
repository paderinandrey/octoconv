# Deferred Items — Phase 17

## DEFER-17-01: TestPGAdvisoryLockTryAcquireCloseRace hangs under `-race` + live Postgres, blocking whole-package test runs

**Discovered during:** 17-01 Task 2 (DEBT-07) LIVE-DB HARD GATE verification.

**Scope:** Out of scope for 17-01. `internal/reconciler/reconciler.go` (`PGAdvisoryLock`) and
`internal/reconciler/advisorylock_conn_test.go` are pre-existing files from Phase 16
(commits `4d47f30`, `2880488`, `1f8b22b`) — not in 17-01's `files_modified` list
(`cmd/document-worker/main.go`, `cmd/chromium-worker/main.go`,
`internal/reconciler/reconciler_test.go`, `internal/reconciler/reconciler_soak_test.go`).

**Symptom:** `go test ./internal/reconciler/... -race -count=1` against a live Postgres
hangs indefinitely inside `TestPGAdvisoryLockTryAcquireCloseRace`'s `t.Cleanup(pool.Close)`
— the test itself completes, but the pool's own `Close()` blocks forever in
`puddle.Pool.Close()`'s `sync.WaitGroup.Wait()`, because a pgxpool connection was
acquired and never released.

**Root cause (probable):** In `TestPGAdvisoryLockTryAcquireCloseRace`, `lock.Close()` is
called on the main goroutine WHILE a background goroutine is still looping on
`lock.TryAcquire(acqCtx)` (the `stop` channel is only closed AFTER `Close()` returns).
`PGAdvisoryLock.TryAcquire` lazily re-acquires a fresh connection via `l.pool.Acquire(ctx)`
whenever `l.conn == nil` — which is exactly the state `Close()` leaves it in. If a
`TryAcquire` call races in between `Close()` setting `l.conn = nil` and `close(stop)`
being observed by the goroutine, it can acquire a brand-new connection from the pool,
store it in `l.conn`, and then the test ends without ever calling `Close()` again —
leaking that connection. `pool.Close()` then blocks forever waiting for it to be
returned. This is a genuine correctness bug (a connection leak race), reproduced
deterministically across three isolated repro runs (full package, `-run 'TestPGAdvisoryLock'`,
and `-run` limited to just `TestPGAdvisoryLockCloseReleasesSlot|TestPGAdvisoryLockTryAcquireCloseRace`).

**Impact:** Blocks running `go test ./internal/reconciler/... -race` as a single whole-package
invocation against a live Postgres — the test binary never reaches the alphabetically-later
`TestSoakRecoversStrandedQueuedJob` / `TestSoakExhaustsAtCap` in that mode. 17-01's DEBT-07
LIVE-DB HARD GATE was instead proven with
`go test ./internal/reconciler/... -race -skip 'TestPGAdvisoryLockTryAcquireCloseRace' -count=1 -timeout 300s -v`
(see 17-01-SUMMARY.md for the log), which still exercises every other reconciler test
including both soak tests under `-race` with a live DB — genuinely proving the fakeEnqueuer
mutex fix — while skipping only the one pre-existing, unrelated hanging test.

**Suggested next step:** File as a new tech-debt item (candidate v1.4 phase or v2 backlog):
fix the `Close()` / `TryAcquire()` race in `PGAdvisoryLock` (e.g., have `Close()` set a
`closed` flag checked before lazy re-acquire, or have the test stop the goroutine before
calling `Close()`), then re-verify the full-package `-race` run passes without `-skip`.

**Status:** Deferred, not fixed (out of scope for 17-01).

---

**RESOLVED (same phase, orchestrator follow-up):** fixed by making `PGAdvisoryLock.Close()`
terminal via a `closed` flag — `TryAcquire` after `Close` now errors (fail-safe closed)
instead of lazily resurrecting a dedicated connection. Regression test
`TestPGAdvisoryLockCloseIsTerminal` added. Proven by the full unskipped package run:
`DATABASE_URL=... go test ./internal/reconciler/... -race -count=1` → 23/23 PASS incl.
`TestPGAdvisoryLockTryAcquireCloseRace` (previously hanging) — 2026-07-12.
