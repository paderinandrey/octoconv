---
phase: 35-queue-worker-routing-integration
plan: 03
subsystem: worker
tags: [asynq, ffmpeg, av-engine, error-classification, worker]

# Dependency graph
requires:
  - phase: 35-queue-worker-routing-integration
    provides: "ErrAVTranscodeFailed/ErrAVAudioExtractFailed/ErrAVThumbnailFailed/ErrAVNoVideoStream sentinels (Plan 01, D-01); TypeAVConvert/QueueAV/AVRetryDelay (Plan 02)"
provides:
  - "isAVTerminal: a stage-aware transient/terminal classifier for the av engine (D-02), derived fresh from the sentinels Plan 01 introduced -- NOT a copy of isAudioTerminal"
  - "HandleAVConvert: the asynq handler consuming TypeAVConvert tasks, mirroring HandleDocumentConvert's simple shape (no guard splice)"
  - "avFailureCode: a pure function mapping a terminal av error to a distinguishable client-facing (error_code, message) pair (D-09/IN-01)"
affects: [35-04, 35-05, 36-av-engine-timing]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Stage-aware terminal classifier derived per-engine from typed sentinels via errors.Is, with a contrast test proving genuine disagreement against the nearest analog classifier (isAudioTerminal) rather than merely asserting the new classifier's own behavior in isolation"
    - "Terminal error_code selection factored into a small pure function (avFailureCode) so the client-facing mapping is unit-testable without a live Postgres/S3 Handler"

key-files:
  created: []
  modified:
    - internal/worker/worker.go
    - internal/worker/worker_test.go

key-decisions:
  - "isAVTerminal placed immediately after isAudioTerminal in worker.go, mirroring the file's existing engine-classifier ordering"
  - "avFailureCode factored out as a package-level pure function (not inlined in HandleAVConvert) specifically so the D-09/IN-01 error_code mapping is unit-testable without live infra, matching the plan's fallback instruction for handler-level testability"
  - "HandleAVConvert mirrors HandleDocumentConvert's shape exactly (strict opts re-parse -> MarkActive -> process() -> isAVTerminal branch) with zero guard splice, confirmed by re-reading process() and AVConverter.Convert (av.go:388-400) before writing the handler"

requirements-completed: [AVE-03]

# Metrics
duration: 40min
completed: 2026-07-21
---

# Phase 35 Plan 03: AV Worker Classifier and Handler Summary

**A stage-aware isAVTerminal classifier (transcode-timeout transient, extract/thumbnail-timeout terminal, D-02) and HandleAVConvert with a distinguishable four-way terminal error_code mapping (timecode_out_of_range/duration_exceeded/resolution_exceeded/no_video_stream), pinned by a test proving isAVTerminal genuinely disagrees with isAudioTerminal.**

## Performance

- **Duration:** ~40 min
- **Started:** 2026-07-21T~22:35Z
- **Completed:** 2026-07-21T~23:15Z
- **Tasks:** 2/2 completed
- **Files modified:** 2

## Accomplishments

- `isAVTerminal` (`internal/worker/worker.go`) implements D-02's terminal/transient split derived fresh for video: `ErrAVTranscodeFailed` wrapping a timeout stays transient (transcode is the expensive stage), while the same sentinel wrapping any non-timeout ffmpeg failure, `ErrAVAudioExtractFailed`, `ErrAVThumbnailFailed` (cheap stages, any failure terminal), and every deterministic guard/output-validation sentinel (`ErrAVOutputMissingOrEmpty`, `ErrAVTimecodeOutOfRange`, `ErrAVResolutionExceeded`, `ErrAudioDurationExceeded`, `ErrAVNoVideoStream`) classify terminal regardless of stage.
- `TestIsAVTerminal` includes an explicit contrast assertion: it constructs an `isAudioTerminal`-shaped "audio: ffmpeg:" timeout error and asserts `isAVTerminal(transcode timeout) != isAudioTerminal(audio-shaped timeout)` — verified by temporary edit (`return isTimeout || true`) to confirm the test actually fails if the transient/terminal split is collapsed, then restored byte-identical (diffed to confirm).
- `HandleAVConvert` mirrors `HandleDocumentConvert`'s simple shape exactly — no guard splice into `process()`, confirmed correct because `AVConverter.Convert` self-contains its duration/resolution guard (`av.go:388-400`).
- `avFailureCode`, a new pure function, maps four terminal av error classes to distinguishable client-facing codes: `ErrAVTimecodeOutOfRange` → `timecode_out_of_range` (D-09, a client-fault, not clamped), `ErrAudioDurationExceeded` → `duration_exceeded` (same code `HandleAudioConvert` emits, since AV's duration guard reuses the identical sentinel), `ErrAVResolutionExceeded` → `resolution_exceeded`, `ErrAVNoVideoStream` → `no_video_stream` (IN-01, a generic-brand ISOBMFF audio-only file misrouted to `av` says so instead of a generic engine error), default → `engine_error`.
- Postgres-first webhook ordering preserved exactly: `MarkFailed` first, webhook enqueue only if that write succeeded and `job.CallbackURL != ""`, terminal errors wrapped with `asynq.SkipRetry`; the transient path returns the bare error with no `MarkFailed` call and no outcome metric recorded (confirmed by direct read of the function, see below).

## Task Commits

Each task was committed atomically:

1. **Task 1: Derive isAVTerminal as a stage-aware classifier (D-02)** - `7daecc5` (feat)
2. **Task 2: Add HandleAVConvert with distinguishable terminal error codes** - `526e0e5` (feat)

_No TDD tasks in this plan required multiple RED/GREEN commits — `tdd="true"` on Task 1 was implemented with production code and its table-driven test landing in the same task commit._

## Files Created/Modified

- `internal/worker/worker.go` - `isAVTerminal` (stage-aware D-02 classifier), `avFailureCode` (pure error_code mapping helper), `HandleAVConvert` (asynq handler for `TypeAVConvert`)
- `internal/worker/worker_test.go` - `TestIsAVTerminal` (table-driven, with a contrast row against `isAudioTerminal`), `TestAVFailureCode` (pins the four-way error_code mapping)

## Decisions Made

- Kept `isAVTerminal` positioned directly after `isAudioTerminal` in `worker.go`, matching the file's established ordering of engine-scoped classifiers (image implicit → document → html → audio → av).
- Factored `avFailureCode` out as a standalone package-level pure function rather than inlining the `switch` inside `HandleAVConvert`, per the plan's explicit fallback instruction ("factor the error_code selection into a small package-level pure function... and unit-test that instead") — this made the D-09/IN-01 mapping testable without constructing a live-Postgres/S3 `Handler`.
- No architectural deviations: every element of the plan's specified action (D-02 split, no guard splice, avFailureCode mapping, Postgres-first ordering) matched `35-PATTERNS.md`'s analog closely enough that no Rule 4 decision was needed.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] isAVTerminal's own doc comment literal-matched the acceptance criterion's forbidden-string grep**
- **Found during:** Task 1 (acceptance-criteria self-check, mirroring the exact self-inflicted pitfall Plan 01's SUMMARY documented for the same class of check)
- **Issue:** The function's fallthrough comment originally read `// No strings.Contains fallback: ...`, which caused `grep -c 'strings.Contains' internal/worker/worker.go` to report 11 matches instead of the required unchanged pre-task value of 10 (the literal string appeared in a comment, not executable code).
- **Fix:** Reworded the comment to describe the same intent without spelling out the literal identifier (`strings.Contains` → "substring-based... matching on error text").
- **Files modified:** `internal/worker/worker.go`
- **Verification:** `grep -c 'strings.Contains' internal/worker/worker.go` returns 10 (unchanged from the pre-task baseline).
- **Committed in:** `7daecc5` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1, a self-inflicted comment-wording mismatch, no functional bug — same class of issue Plan 01 documented for the identical acceptance-criteria pattern)
**Impact on plan:** No scope creep; fixed before commit, production behavior unaffected.

## Issues Encountered

None beyond the deviation above.

## Acceptance-Criteria Induced-Failure Verifications

Per plan instructions, verified by temporary edit, test run, and restore (restored byte-identical, confirmed via `diff`):

- **Task 1:** Changing `isAVTerminal`'s transcode-timeout branch from `return !isTimeout` to a form that always returns `true` (`return isTimeout || true`, chosen to keep `isTimeout` referenced and avoid an unrelated compile failure) makes `TestIsAVTerminal` fail with `D-02: expected isAVTerminal(transcode timeout) = false (transient — transcode is the expensive stage)` — confirming the guarding assertion actually pins the D-02 split, not just documents it.

## Verification detail: isAudioTerminal non-regression

Per the plan's own `<verification>` section ("`isAudioTerminal` is byte-identical to HEAD"): `git diff 08b1bc0f4e46c3f6c6ad15bc29dca6aeeddbe816 -- internal/worker/worker.go | grep "^[+-]"` shows the only lines mentioning `isAudioTerminal` are new comment prose inside `isAVTerminal`'s doc comment (explaining the contrast) — no diff hunk touches `isAudioTerminal`'s own function body.

## Transient-path no-MarkFailed confirmation

Per the plan's acceptance criteria, confirmed by direct read of `HandleAVConvert` (`internal/worker/worker.go`): the transient branch (the `return err` statement reached when `isAVTerminal(err)` is false) contains no call to `h.repo.MarkFailed` and no call to `metrics.RecordJobOutcome` — only the terminal branch (`isAVTerminal(err) == true`) calls both.

## Full verification run

```
gofmt -l .                      # no output (clean)
go vet ./...                    # clean
go build ./...                  # clean
go test ./... -count=1          # all packages pass, including internal/worker
git diff --stat go.mod go.sum   # no changes (zero new dependencies)
```

grep-based acceptance criteria, all satisfied:
- `grep -n 'func (h \*Handler) HandleAVConvert' internal/worker/worker.go` → exactly one match (line 835)
- `grep -c 'timecode_out_of_range\|no_video_stream\|resolution_exceeded\|duration_exceeded' internal/worker/worker.go` → 11 (≥ 4)
- `grep -n 'enforceAudioGuardBeforeConvert' internal/worker/worker.go` → 5 (unchanged from pre-task baseline; no av guard splice added)
- `grep -c 'asynq.SkipRetry' internal/worker/worker.go` → 21 (increased from the pre-task baseline of 17; every new terminal path wraps it)
- `grep -c 'strings.Contains' internal/worker/worker.go` → 10 (unchanged from pre-task baseline, after the doc-comment fix above)

## Known Stubs

None.

## Threat Flags

None — this plan's changes stay inside the threat register the plan itself declared (T-35-09 through T-35-12, T-35-SC), all dispositioned `mitigate`/`accept` in the plan. No new network endpoints, auth paths, or trust-boundary-crossing file access patterns were introduced; `AVOptsFromMap`'s strict re-parse (T-35-09) is called exactly as planned, `isAVTerminal` closes T-35-10 (every deterministic sentinel is terminal, wrapped with `asynq.SkipRetry`), `engine_stderr` exposure (T-35-11) is unchanged from the existing four engines' accepted pattern, and the transient path's no-`MarkFailed`/no-metric behavior closes T-35-12 (no double-counted outcome on retry).

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `isAVTerminal` and `HandleAVConvert` are ready for `cmd/av-worker` (a later plan) to register on an `asynq.ServeMux` for `queue.TypeAVConvert`.
- ROADMAP success criterion 3 (a stage-aware transient/terminal classifier exists for av, derived fresh, not copied from audio) is satisfied — pinned by `TestIsAVTerminal`'s explicit contrast row.
- No blockers for downstream Phase 35 plans. `HandleAVConvert` compiles and passes `go vet`/`go build`/`go test` against the current `Handler` struct/`NewHandler` signature unchanged — no signature changes were needed, confirming the plan's stated "unchanged signature" interface anchor.

---
*Phase: 35-queue-worker-routing-integration*
*Completed: 2026-07-21*

## Self-Check: PASSED

- FOUND: internal/worker/worker.go
- FOUND: internal/worker/worker_test.go
- FOUND: .planning/phases/35-queue-worker-routing-integration/35-03-SUMMARY.md
- FOUND commit: 7daecc5 (Task 1)
- FOUND commit: 526e0e5 (Task 2)
