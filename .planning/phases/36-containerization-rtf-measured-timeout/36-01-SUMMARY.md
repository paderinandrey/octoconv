---
phase: 36-containerization-rtf-measured-timeout
plan: 01
subsystem: convert
tags: [ffmpeg, av, disk-guard, golang.org/x/sys, decompression-bomb, config-threading]

# Dependency graph
requires:
  - phase: 35-queue-worker-routing-integration
    provides: AVConverter registered in convert.Default, av queue/worker wired end-to-end
provides:
  - EnforceMinFreeDisk fail-closed disk-space guard (D-06) with ErrAVInsufficientDiskSpace sentinel
  - AVConverter.MaxSourceDuration/MaxSourceResolutionHeight zero-value-defaulting struct fields (D-09/Pitfall 4)
  - cmd/av-worker env wiring for AV_MAX_DURATION_SECONDS (envDurationSeconds) and AV_DISK_SAFETY_FACTOR (envFloat + setter)
affects: [36-02, 36-03, 36-04, phase-37-keda-tuning]

# Tech tracking
tech-stack:
  added: [golang.org/x/sys/unix (promoted indirect->direct, no new version resolution)]
  patterns:
    - "Setter + effective-resolver pair for env-derived package state (SetAVDiskSafetyFactor/effectiveAVDiskSafetyFactor), mirrors SetAudioThreads/audioThreadCount and SetVeraPDFTimeout/effectiveVeraPDFTimeout"
    - "Zero-value-defaulting struct fields on a Converter (AVConverter.MaxSourceDuration/MaxSourceResolutionHeight), mirrors api.NewServer's Config zero-value-in-constructor pattern"
    - "Guard-before-expensive-work ordering: duration -> resolution -> disk -> dispatch, all fail-closed before any ffmpeg subprocess"

key-files:
  created:
    - internal/convert/avdiskguard.go
    - internal/convert/avdiskguard_test.go
  modified:
    - internal/convert/av.go
    - internal/convert/av_test.go
    - internal/convert/converters.go
    - cmd/av-worker/main.go
    - go.mod

key-decisions:
  - "avDiskSafetyFactorDefault = 3.0 is Claude's Discretion ([ASSUMED]) per 36-CONTEXT.md -- no measured decode/encode disk-usage ratio existed to derive it from; overridable via AV_DISK_SAFETY_FACTOR without a code change"
  - "AVConverter's guard ceilings became fields, not env-read-in-package globals -- keeps internal/convert env-var-free (env-only-in-main convention preserved) while letting cmd/av-worker re-register a configured instance"

patterns-established:
  - "avDiskSafetyFactorOverride/SetAVDiskSafetyFactor/effectiveAVDiskSafetyFactor: identical single-write-before-concurrent-reads contract as SetAudioThreads/SetAudioModelPath/SetVeraPDFTimeout -- call exactly once at process startup, before srv.Start(mux)"

requirements-completed: [AVE-04]

# Metrics
duration: ~15min
completed: 2026-07-22
---

# Phase 36 Plan 01: Disk-Space Guard + AVConverter Config-Threading Summary

**Fail-closed `EnforceMinFreeDisk` disk-space guard (D-06) via `golang.org/x/sys/unix.Statfs`, plus an `AVConverter` struct-field refactor (D-09/Pitfall 4) that lets `cmd/av-worker` re-register a configured instance from `AV_MAX_DURATION_SECONDS`/`AV_DISK_SAFETY_FACTOR` at startup — no Docker, no measurement, pure-Go and fully `go test`-verified.**

## Performance

- **Duration:** ~15 min
- **Completed:** 2026-07-22
- **Tasks:** 3/3 completed
- **Files modified:** 6 (2 created, 4 modified)

## Accomplishments
- New `EnforceMinFreeDisk` guard fail-closes on real available disk space (`Bavail`, not `Bfree`) before any ffmpeg subprocess runs, with 5 pinned behaviors (success, insufficient, exact-boundary, statfs-error, `errors.Is` sentinel match)
- `AVConverter.MaxSourceDuration`/`MaxSourceResolutionHeight` are now zero-value-defaulting struct fields — a bare `AVConverter{}` is provably unchanged at 4h/4320, proven by dedicated field-defaulting tests using real ffmpeg fixtures
- `cmd/av-worker` reads `AV_MAX_DURATION_SECONDS` (via `envDurationSeconds`, cloned verbatim from `cmd/audio-worker`) and `AV_DISK_SAFETY_FACTOR` (via new `envFloat`), re-registering a configured `AVConverter` and calling `convert.SetAVDiskSafetyFactor` before `srv.Start(mux)` — the documented last-write-wins happens-before boundary

## Task Commits

Each task was committed atomically:

1. **Task 1: EnforceMinFreeDisk fail-closed disk guard** - `53d4f41` (feat)
2. **Task 2: AVConverter struct-field refactor + disk-guard call site** - `e83d07a` (feat)
3. **Task 3: cmd/av-worker env wiring** - `74bfd68` (feat)

_No TDD RED/GREEN split was used — each task's tests and implementation landed together, since the plan's `tdd="true"` tasks specified behavior-first design but the acceptance criteria are verified as a single unit per task (all pre-existing tests continued passing throughout; no regression window)._

## Files Created/Modified
- `internal/convert/avdiskguard.go` - `ErrAVInsufficientDiskSpace` sentinel + `EnforceMinFreeDisk(dir, inputSizeBytes, safetyFactor)` via `unix.Statfs`
- `internal/convert/avdiskguard_test.go` - table-driven fail-closed tests (5 sub-cases)
- `internal/convert/av.go` - `MaxSourceDuration`/`MaxSourceResolutionHeight` fields on `AVConverter`; guard-stage resolver (field-or-const); `avDiskSafetyFactorDefault`/`avDiskSafetyFactorOverride`/`SetAVDiskSafetyFactor`/`effectiveAVDiskSafetyFactor`; `EnforceMinFreeDisk` call site inserted after the resolution guard, before the target-format dispatch switch
- `internal/convert/av_test.go` - 3 new tests: bare-struct-matches-defaults, configured-duration-rejects, configured-resolution-rejects
- `internal/convert/converters.go` - comment update noting `cmd/av-worker`'s configured re-registration
- `cmd/av-worker/main.go` - `envDurationSeconds`, `envFloat`, `resolveAVThreads` helpers; re-registration of a configured `convert.AVConverter` + `convert.SetAVDiskSafetyFactor` call, both before `srv.Start(mux)`; startup log line reporting resolved values
- `go.mod` - `golang.org/x/sys` promoted from indirect to direct (no version change; `go.sum` untouched)

## Decisions Made
- `avDiskSafetyFactorDefault = 3.0` is an explicit `[ASSUMED]` placeholder (Claude's Discretion per 36-CONTEXT.md), documented in-code and overridable via `AV_DISK_SAFETY_FACTOR` without further code changes — no measured ffmpeg disk-usage ratio existed at this point in the phase to derive a better default from.
- Chose the setter approach (`SetAVDiskSafetyFactor`) over a bare package var for the disk-safety factor, per Task 3's explicit instruction, keeping the single-write-before-concurrent-reads contract identical to `SetAudioThreads`/`SetVeraPDFTimeout`.

## Deviations from Plan

None - plan executed exactly as written. All acceptance criteria (grep counts, protocol_whitelist non-regression, pair-disjointness, `go.sum` stability) verified directly and matched expectations without needing any fix.

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. `AV_MAX_DURATION_SECONDS` and `AV_DISK_SAFETY_FACTOR` are new optional env vars with safe defaults (4h, 3.0); wiring them into `.env.example`/`docker-compose.yml` is later plans' (36-02/03/04) responsibility, not this one's.

## Next Phase Readiness

- `AVConverter` is now pluggable for the NO-GO lever (`AV_MAX_DURATION_SECONDS`) that 36-04's RTF checkpoint will need, without any further code changes to `internal/convert`.
- The disk-space guard is live and unit-tested; it has no live-container proof yet (no Docker in this plan) — that verification belongs to the containerization plans (36-02/36-03).
- No blockers for 36-02.

---
*Phase: 36-containerization-rtf-measured-timeout*
*Completed: 2026-07-22*
