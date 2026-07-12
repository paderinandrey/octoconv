# Deferred Items — 260712-cqg

Out-of-scope discoveries found during execution, not fixed (per executor scope
boundary — only auto-fix issues directly caused by the current task's
changes).

## 1. Pre-existing data race in `fakeEnqueuer` (reconciler test helpers)

- **Found during:** Task 2 verification (`go test ./internal/reconciler/... -race -count=1`)
- **Symptom:** `go test ./internal/reconciler/... -race -count=1` (full package,
  DATABASE_URL set) fails with a WARNING: DATA RACE in
  `TestSoakRecoversStrandedQueuedJob`: `fakeEnqueuer.EnqueueImageConvert`
  (`internal/reconciler/reconciler_test.go:94`) writes `imageCalls` from the
  `Sweeper.Run` goroutine while the test's polling loop
  (`internal/reconciler/reconciler_soak_test.go:100`) reads
  `len(enq.imageCalls)` from the main test goroutine with no synchronization.
- **Scope:** `internal/reconciler/reconciler_test.go` and
  `internal/reconciler/reconciler_soak_test.go` — neither file is touched by
  this plan (verified: `git diff f46cdcabeba26c1fa1326ee1228071286958d8ae` shows
  no changes to either). Pre-existing since at least commit `5daef77`
  (Phase 15, `feat(15-01)`), unrelated to the CR-01/WR-01 advisory-lock fix.
- **Verification impact:** `go test ./internal/reconciler/... -count=1`
  (no `-race`, the command this plan's constraints actually require) passes
  cleanly. `go test ./internal/reconciler/... -run TestPGAdvisoryLock -race
  -count=1` (the new Task 2 tests, scoped) also passes cleanly. Only the
  full-package `-race` run trips this pre-existing race.
- **Fix (not applied here):** add a `sync.Mutex` (or atomic counter) guarding
  `fakeEnqueuer.imageCalls`/`webhookCalls`/etc. in
  `internal/reconciler/reconciler_test.go`, and have
  `reconciler_soak_test.go`'s polling loop read through the same guard.
- **Suggested follow-up:** new DEBT item or a small quick-task, e.g.
  `fix(reconciler): guard fakeEnqueuer call counters for -race safety`.
