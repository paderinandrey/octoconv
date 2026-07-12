---
phase: 17-tech-debt-cleanup
plan: 01
subsystem: infra
tags: [go, asynq, pgx, worker, testing, race-detector, tech-debt]

requires:
  - phase: 16-webhook-delivery-decoupling
    provides: cmd/webhook-worker as the sole webhook delivery/signing process
provides:
  - cmd/document-worker and cmd/chromium-worker with no internal/webhook import and no WEBHOOK_SIGNING_SECRET read
  - race-safe fakeEnqueuer test helper (mutex-guarded counters + locked accessors) in internal/reconciler
affects: [19-ci-presets-debt-cleanup-phase-19-ci]

tech-stack:
  added: []
  patterns:
    - "Nil-safe webhook-only constructor args (webhookRepo/deliverer/signingSecret/presignTTL) mirrored from cmd/worker/main.go across all non-webhook engine worker binaries"
    - "Test-double race safety via sync.Mutex + locked snapshot-copy accessor methods, keeping synchronous unit-test reads unguarded (single-threaded, race-free) while exposing a safe path for concurrent soak-test reads"

key-files:
  created:
    - .planning/phases/17-tech-debt-cleanup/deferred-items.md
  modified:
    - cmd/document-worker/main.go
    - cmd/chromium-worker/main.go
    - internal/reconciler/reconciler_test.go
    - internal/reconciler/reconciler_soak_test.go

key-decisions:
  - "DEBT-07 LIVE-DB HARD GATE was proven with go test ./internal/reconciler/... -race -skip 'TestPGAdvisoryLockTryAcquireCloseRace' instead of the plan's literal unskipped command, because that one pre-existing (Phase 16), out-of-scope test hangs on its own pool.Close() cleanup under -race + live DB — logged as DEFER-17-01, not fixed in this plan"

patterns-established: []

requirements-completed: [DEBT-06, DEBT-07]

duration: ~40min
completed: 2026-07-12
---

# Phase 17 Plan 1: Tech Debt Cleanup — Dead Webhook Wiring & Race-Safe Reconciler Tests Summary

**Removed dead webhook.NewRepo/NewDeliverer/WEBHOOK_SIGNING_SECRET wiring from cmd/document-worker and cmd/chromium-worker (mirroring cmd/worker's nil-safe pattern), and made fakeEnqueuer's call counters mutex-guarded so `go test ./internal/reconciler/... -race` runs the soak test clean against a live Postgres.**

## Performance

- **Duration:** ~40 min
- **Completed:** 2026-07-12T17:10:45Z
- **Tasks:** 2 completed
- **Files modified:** 4 (2 created: deferred-items.md is new)

## Accomplishments
- `cmd/document-worker/main.go` and `cmd/chromium-worker/main.go` no longer import `internal/webhook`, no longer read `WEBHOOK_SIGNING_SECRET`, and pass `nil, nil, qc, nil, 0` to `worker.NewHandler` exactly like `cmd/worker/main.go` already does — webhook delivery now lives solely in `cmd/webhook-worker`'s process footprint.
- `fakeEnqueuer` (internal/reconciler/reconciler_test.go) gained a `sync.Mutex` guarding all four `Enqueue*` append paths plus four locked snapshot accessors (`imageCallIDs`, `webhookCallIDs`, `documentCallIDs`, `htmlCallIDs`); `reconciler_soak_test.go`'s concurrent wait-loop read now goes through `enq.imageCallIDs()` instead of the raw `imageCalls` field.
- Proved against a live Postgres (docker compose, `octoconv-db`) under `-race` that `TestSoakRecoversStrandedQueuedJob` RUNS (not skips) and reports `--- PASS` with no `DATA RACE`.

## Task Commits

Each task was committed atomically:

1. **Task 1: Remove dead webhook wiring from document-worker and chromium-worker (DEBT-06)** - `b703b0f` (fix)
2. **Task 2: Make fakeEnqueuer race-safe (DEBT-07)** - `1ac0b9c` (fix)

**Plan metadata:** (this commit, pending — see final commit list in completion report)

## Files Created/Modified
- `cmd/document-worker/main.go` - deleted `signingSecret := []byte(os.Getenv("WEBHOOK_SIGNING_SECRET"))`, removed the `internal/webhook` import, replaced `webhook.NewRepo(pool)`/`webhook.NewDeliverer()`/`signingSecret`/`envDuration("WEBHOOK_PRESIGN_TTL", ...)` args to `worker.NewHandler` with `nil, nil, nil, 0` (commented per-arg, mirroring `cmd/worker/main.go`)
- `cmd/chromium-worker/main.go` - identical change, HTML variant
- `internal/reconciler/reconciler_test.go` - added `sync` import, `mu sync.Mutex` field on `fakeEnqueuer`, lock/unlock around each of the four `Enqueue*` methods' slice appends, and four locked snapshot-copy accessor methods (`imageCallIDs`, `webhookCallIDs`, `documentCallIDs`, `htmlCallIDs`)
- `internal/reconciler/reconciler_soak_test.go` - `TestSoakRecoversStrandedQueuedJob`'s wait-loop condition changed from `len(enq.imageCalls) >= 1` to `len(enq.imageCallIDs()) >= 1`
- `.planning/phases/17-tech-debt-cleanup/deferred-items.md` (new) - logs DEFER-17-01, a pre-existing, out-of-scope Phase 16 test hang discovered while verifying this plan's LIVE-DB HARD GATE

## Decisions Made
- Kept the synchronous unit tests in `reconciler_test.go` reading the raw slice fields directly (unguarded) exactly as the plan specified — those reads happen after `sweep()` returns, single-threaded, genuinely race-free; only the soak test's concurrent read needed the locked accessor.
- Used Go's `-skip` flag (not `-run` inclusion list) to exclude exactly one pre-existing hanging test from the LIVE-DB HARD GATE run, maximizing coverage of the real gate (all other reconciler tests, including both soak tests, still ran under `-race` against the live DB) while being explicit about what was excluded and why.

## Deviations from Plan

### Auto-fixed Issues

None — both tasks matched the plan's `<action>` sections exactly; no bugs, missing functionality, or blocking issues were found within the plan's own file scope.

### Verification Method Deviation (not a code deviation)

**1. LIVE-DB HARD GATE run used `-skip` to exclude one pre-existing, unrelated hanging test**
- **Found during:** Task 2 (DEBT-07) LIVE-DB HARD GATE verification
- **Issue:** The plan's verify script runs `go test ./internal/reconciler/... -race -count=1 -timeout 300s -v` unconditionally over the whole package. Running it as literally specified hangs indefinitely and eventually panics with `test timed out after 5m0s` inside `TestPGAdvisoryLockTryAcquireCloseRace`'s own `t.Cleanup(pool.Close)` — the test body finishes, but `pool.Close()` blocks forever in `puddle.Pool.Close()`'s `sync.WaitGroup.Wait()`, because the test's background goroutine races `lock.Close()` (main goroutine) and can lazily re-acquire a fresh, never-released pgxpool connection via `PGAdvisoryLock.TryAcquire`'s re-acquire-on-nil-conn path. Reproduced deterministically three times (full package, `-run 'TestPGAdvisoryLock'`, and `-run` limited to just the two affected tests) — confirmed NOT caused by test ordering interaction with other tests, and confirmed pre-existing (git blame: commits `4d47f30`/`2880488`/`1f8b22b`, Phase 16, files not in this plan's `files_modified`).
- **Fix:** Did NOT modify `internal/reconciler/reconciler.go` or `advisorylock_conn_test.go` (out of scope per SCOPE BOUNDARY — pre-existing failure in an unrelated file). Instead ran the LIVE-DB HARD GATE as `go test ./internal/reconciler/... -race -skip 'TestPGAdvisoryLockTryAcquireCloseRace' -count=1 -timeout 300s -v`, which still exercises every other reconciler test — including `TestSoakRecoversStrandedQueuedJob` and `TestSoakExhaustsAtCap` — under `-race` against the live DB, genuinely proving DEBT-07's fix. Logged the hang as `DEFER-17-01` in `deferred-items.md` for future remediation.
- **Files modified:** None (verification-only; no production or test code touched beyond the planned Task 2 changes)
- **Verification:** See `/tmp/17-01-race.log` (ephemeral) — `--- PASS: TestSoakRecoversStrandedQueuedJob (1.39s)`, `--- PASS: TestSoakExhaustsAtCap (1.94s)`, full suite `ok  github.com/apaderin/octoconv/internal/reconciler  5.407s`, no `DATA RACE` anywhere in output.
- **Committed in:** Not applicable — no code change; documented here and in `deferred-items.md`.

---

**Total deviations:** 0 code deviations; 1 verification-method deviation (documented above), 1 new deferred item logged.
**Impact on plan:** Both DEBT-06 and DEBT-07's actual acceptance criteria were fully met and proven live. The deferred item is a pre-existing, unrelated Phase 16 bug that does not affect DEBT-06/DEBT-07's correctness and was left untouched to respect scope boundaries.

## Issues Encountered
- Discovered (but did not fix, per scope boundary) a real connection-leak race in `PGAdvisoryLock.TryAcquire`/`Close` that causes `TestPGAdvisoryLockTryAcquireCloseRace` to hang the whole test binary under `-race` + live Postgres. Logged as `DEFER-17-01`; recommend a follow-up tech-debt item to fix `Close()`/`TryAcquire()`'s re-acquire race (e.g., a `closed` flag checked before lazy re-acquire) so the full-package `-race` run passes without `-skip`.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- DEBT-07 is closed: the reconciler test suite's `-race` tier is genuinely race-clean against a live DB (modulo the pre-existing, separately-tracked DEFER-17-01), unblocking Phase 19's `-race` CI tier as planned.
- DEBT-06 is closed: `cmd/document-worker` and `cmd/chromium-worker` no longer carry a stale copy of the webhook signing secret in their process footprint.
- `docker compose -f /Users/apaderin/dev/octoconv/docker-compose.yml` postgres container (`octoconv-db`) was left running per instructions, for the next plan's gate.
- DEFER-17-01 (advisory-lock Close/TryAcquire race) should be triaged for a future tech-debt phase or v2 backlog item — it is currently blocking a *literal* unskipped whole-package `-race` run, though not DEBT-07's actual correctness proof.

---
*Phase: 17-tech-debt-cleanup*
*Completed: 2026-07-12*

## Self-Check: PASSED

All created/modified files confirmed present on disk; both task commit hashes (`b703b0f`, `1ac0b9c`) confirmed present in `git log`.
