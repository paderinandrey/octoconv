---
phase: 31-queue-worker-routing-integration
plan: 01
subsystem: infra
tags: [postgres, asynq, redis, go, whisper-cli, migrations]

# Dependency graph
requires:
  - phase: 30-audio-converter
    provides: "AudioConverter (whisper.go) implementing the Converter interface, unregistered into convert.Default"
provides:
  - "Migration 0006: jobs.engine CHECK constraint accepts 'audio'"
  - "AudioConverter registered in convert.Default (EngineFor/Classes()/Lookup audio-aware)"
  - "SetAudioModelPath + 3-tier model-path fallback (injected -> AUDIO_MODEL_PATH -> defaultAudioModelPath)"
  - "queue.TypeAudioConvert, QueueAudio, NewAudioConvertTask, AudioRetryDelay, AudioUniqueTTL"
  - "*queue.Client.EnqueueAudioConvert with fresh audio-derived UniqueTTL"
  - "TestAudioUniqueTTL proving SC3 (strict-exceeds-worst-case-zero-margin-lifetime)"
affects: [31-02, 31-03, 31-04, 32-audio-worker-tuning]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Audio engine class wired through the same four-touchpoint shape as image/document/html: CHECK constraint migration, converter registration, queue task-type/retry/UniqueTTL block, client Enqueue method"

key-files:
  created:
    - internal/db/migrations/0006_audio_engine.sql
  modified:
    - internal/convert/converters.go
    - internal/convert/whisper.go
    - internal/queue/queue.go
    - internal/queue/client.go
    - internal/queue/queue_test.go

key-decisions:
  - "AudioUniqueTTL is derived fresh from AUDIO_MAX_RETRY/AUDIO_ENGINE_TIMEOUT, never reusing image/document/html's TTL (per STATE.md binding decision), reusing the shared uniqueTTLSafetyMargin const verbatim"
  - "AUDIO_ENGINE_TIMEOUT default of 600s is an explicit [ASSUMED] placeholder; Phase 32 re-derives it from real-time-factor measurement against the pinned whisper-cli model"
  - "audioRetrySchedule mirrors documentRetrySchedule/htmlRetrySchedule's 5s/15s/30s no-jitter shape as a defensible default (no audio-specific signal argues for a different cadence)"

patterns-established:
  - "3-tier model/config resolution (test-injected -> env-derived setter -> compile-time default) mirroring verapdf.go's SetVeraPDFTimeout/effectiveVeraPDFTimeout contract"

requirements-completed: [AUD-05]

# Metrics
duration: 5min
completed: 2026-07-18
---

# Phase 31 Plan 01: Queue/Worker/Routing Substrate Summary

**Postgres CHECK-constraint migration, AudioConverter registration, and full asynq queue/task-type/UniqueTTL/client layer that unblocks every downstream audio-engine plan in Wave 2**

## Performance

- **Duration:** ~5 min (commits 06:43:45 -> 06:47:04 UTC+3)
- **Started:** 2026-07-18T03:42:00Z (approx.)
- **Completed:** 2026-07-18T03:47:04Z
- **Tasks:** 3 (Task 3 is `tdd="true"`, split across test/feat commits)
- **Files modified:** 6 (1 created, 5 modified)

## Accomplishments
- Migration 0006 extends `jobs.engine` CHECK constraint to accept `'audio'` — the single most likely "looks done in review, 500s live" gap is closed
- `AudioConverter{}` registered in `convert.Default`, making `EngineFor`/`Classes()`/`Lookup` audio-aware automatically (drives `/v1/formats` and API routing without further code)
- `whisper.go` gained a 3-tier model-path resolution (`SetAudioModelPath` setter, mirroring `verapdf.go`'s `SetVeraPDFTimeout` contract) so local dev resolves the model from `AUDIO_MODEL_PATH` instead of the container-only `/models/ggml-base.bin` default
- Full audio queue/task-type/retry/UniqueTTL block landed in `queue.go`: `TypeAudioConvert`, `QueueAudio`, `NewAudioConvertTask`, `audioRetrySchedule`/`AudioRetryDelay`, `audioBackoffSum`, `AudioUniqueTTL` — wired into `RetryDelayFunc`'s dispatch switch
- `*queue.Client` gained `audioMaxRetry`/`audioUniqueTTL` fields and `EnqueueAudioConvert`, reading `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT` at construction
- `TestAudioUniqueTTL` proves SC3: the derived TTL (2570s for maxRetry=3, engineTimeout=600s) strictly exceeds the zero-margin worst-case retry lifetime, is monotonic in both arguments, and matches the worked-example formula exactly — plus parity tests `TestAudioConvertTaskRoundTrip`/`TestAudioRetryDelaySchedule` mirroring the Document/HTML sibling tests

## Task Commits

Each task was committed atomically:

1. **Task 1: Migration 0006 + register AudioConverter + AUDIO_MODEL_PATH setter** - `8033e37` (feat)
2. **Task 2: Audio task-type/queue/retry/AudioUniqueTTL block in queue.go** - `e7bf1e4` (feat)
3. **Task 3: Client audio fields + EnqueueAudioConvert + TestAudioUniqueTTL** - `9ffb9ec` (test) + `fefcdf0` (feat)

**Plan metadata:** (this commit, pending)

_Note: Task 3 is `tdd="true"` — test commit landed before the feat commit that adds `EnqueueAudioConvert`, per the task_commit_protocol._

## Files Created/Modified
- `internal/db/migrations/0006_audio_engine.sql` - Extends `jobs_engine_check` to accept `'audio'`, mirroring `0005_html_engine.sql`'s DROP/ADD shape verbatim
- `internal/convert/converters.go` - One-line `Default.Register(AudioConverter{})` in `init()`
- `internal/convert/whisper.go` - `audioModelPath` var + `SetAudioModelPath` setter; `model()` widened to 3-tier fallback; outdated "deliberately NOT registered" doc comment corrected
- `internal/queue/queue.go` - `TypeAudioConvert`/`QueueAudio` consts, `NewAudioConvertTask`, `audioRetrySchedule`/`AudioRetryDelay`/`audioBackoffSum`, `AudioUniqueTTL`, `RetryDelayFunc` case arm
- `internal/queue/client.go` - `audioMaxRetry`/`audioUniqueTTL` fields, `NewClient` env reads, `EnqueueAudioConvert` method
- `internal/queue/queue_test.go` - `TestAudioUniqueTTL` (SC3 proof), `TestAudioConvertTaskRoundTrip`, `TestAudioRetryDelaySchedule`

## Decisions Made
- `AudioUniqueTTL` derives fresh from `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT`, never reusing another engine class's TTL — matches the STATE.md binding decision and is proven by the SC3 test assertion.
- `AUDIO_ENGINE_TIMEOUT` default of 600s is an explicit `[ASSUMED]` placeholder documented in both the `NewClient` doc comment and code comment; Phase 32 will re-derive it from measured real-time-factor.
- `audioRetrySchedule` reuses the 5s/15s/30s no-jitter shape from `documentRetrySchedule`/`htmlRetrySchedule` as the most defensible default absent audio-specific signal.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Corrected stale doc comment on `AudioConverter`**
- **Found during:** Task 1
- **Issue:** `AudioConverter`'s type doc comment (from Phase 30) stated it was "deliberately NOT registered into convert.Default by this plan" — now false since this plan registers it.
- **Fix:** Updated the comment to state it is registered by `converters.go` (Phase 31, AUD-05).
- **Files modified:** `internal/convert/whisper.go`
- **Verification:** `go vet`/`go build` clean; comment now matches actual registration state.
- **Committed in:** `8033e37` (Task 1 commit)

**2. [Rule 1 - Bug] Fixed incorrect worked-example constant in `TestAudioUniqueTTL`**
- **Found during:** Task 3 (RED-phase test run)
- **Issue:** Initial draft asserted `want != 3050*time.Second`; running the test against the real formula `(4)*600s + 50s + 120s` showed the correct value is `2570s`, not `3050s` (arithmetic slip during test authoring).
- **Fix:** Corrected the worked-example constant and its doc comment to `2570s`.
- **Files modified:** `internal/queue/queue_test.go`
- **Verification:** `go test ./internal/queue/ -run TestAudioUniqueTTL -v` passes.
- **Committed in:** `9ffb9ec` (Task 3 test commit)

---

**Total deviations:** 2 auto-fixed (2 bug fixes, both Rule 1). No scope creep — both fixes are corrections to documentation/test accuracy discovered while executing the plan as written.

## Issues Encountered
None beyond the two auto-fixed items above.

**TDD note:** Task 3 (`tdd="true"`) targets `AudioUniqueTTL`/`NewAudioConvertTask`/`AudioRetryDelay`, all of which were already implemented in Task 2 per this plan's own file-boundary split (`queue.go` lands in Task 2; `client.go`/`queue_test.go` land in Task 3). As a result, `TestAudioUniqueTTL` and its sibling tests passed against the pre-existing implementation rather than driving a genuine RED-phase failure — this is expected given the plan's task structure, not a bug. `EnqueueAudioConvert` (genuinely new in Task 3) was added after the test commit, preserving test-before-feat commit ordering for the one behavior that was actually new in this task.

## User Setup Required
None - no external service configuration required. `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT`/`AUDIO_MODEL_PATH` are optional env vars with sane defaults; operators may set them later without code changes.

## Next Phase Readiness
- Wave 2 plans (worker handler, API routing, reconciler) can now depend on `queue.TypeAudioConvert`, `queue.QueueAudio`, and `*queue.Client.EnqueueAudioConvert` — all exist and are tested.
- Migration 0006 is present and correctly numbered for the embedded-migration runner's glob; it will apply automatically on next `cmd/api`/`cmd/migrate` startup against a live database.
- `AudioConverter` is registered but its model path still resolves to the container-only default unless a future worker `main.go` calls `SetAudioModelPath` (Wave 2's audio-worker entry point) — no blocker, just the next wiring point.
- No blockers identified for Wave 2.

---
*Phase: 31-queue-worker-routing-integration*
*Completed: 2026-07-18*

## Self-Check: PASSED
