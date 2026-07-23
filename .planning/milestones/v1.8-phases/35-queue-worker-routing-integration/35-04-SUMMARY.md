---
phase: 35-queue-worker-routing-integration
plan: 04
subsystem: api
tags: [http, upload-limits, keda, dos-mitigation, av-engine, go]

# Dependency graph
requires:
  - phase: 35-queue-worker-routing-integration
    plan: 02
    provides: "queue.AllConvertQueues(), (*queue.Client).EnqueueAVConvert"
provides:
  - "Enqueuer.EnqueueAVConvert and the handleCreateJob EngineAV enqueue case"
  - "cmd/api/main.go's queue-depth collector derived from queue.AllConvertQueues() (closes the phase's highest-risk silent seam)"
  - "D-07 two-tier upload ceiling: 2 GiB global pre-parse bound + per-engine post-detection ceiling (Server.maxEngineBytes / Config.MaxEngineBytes)"
affects: [35-06, 35-07]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Per-engine post-detection size gate mirrors the maxImagePixels/maxDocumentUncompressedBytes rejection-logging shape (reason=engine_size_limit), placed strictly between EngineFor and storage.Upload"
    - "Test-only Converter registration (fakeAVConverter, a synthetic (mp4, avtestout) pair) to exercise engine==\"av\" end-to-end through handleCreateJob before the real AVConverter is registered (D-08, a later plan)"

key-files:
  created: []
  modified:
    - internal/api/api.go
    - internal/api/handlers.go
    - internal/api/handlers_test.go
    - cmd/api/main.go
    - .env.example
    - docker-compose.yml

key-decisions:
  - "cmd/api/main.go leaves Config.MaxEngineBytes nil and relies on NewServer's own D-07 defaulting rather than duplicating the five-engine map at the call site (plan explicitly offered both options; chose the single-source-of-truth one)"
  - "MaxEngineBytes map gates known engines only -- an engine key absent from the map is never rejected (explicitly tested), matching the plan's fifth behavior bullet"
  - "D-07 ceiling tests use scaled-down MaxEngineBytes values (100/5<<20 bytes) rather than literal 200 MiB/1 GiB payloads, mirroring the existing TestCreateJob_DimensionLimitExceeded convention of testing the gating logic at a proportionally smaller, fast-running scale"
  - "AV-engine routing is exercised via a test-only fakeAVConverter registered onto convert.Default for a synthetic (mp4, avtestout) pair -- the real AVConverter stays deliberately unregistered per D-08 until a later Phase 35 plan wires SniffVideo into the detection chain; internal/convert itself was not modified (out of this plan's file scope)"

requirements-completed: [AVE-03]

# Metrics
duration: ~40min
completed: 2026-07-21
---

# Phase 35 Plan 04: API Enqueuer AV Seam, Derived Collector, D-07 Upload Ceiling Summary

**Adds `EnqueueAVConvert` to the API's Enqueuer seam and its routing switch case, rewires `cmd/api/main.go`'s queue-depth collector to the derived `queue.AllConvertQueues()` helper (closing the phase's highest-risk silent seam), and implements D-07's two-tier upload ceiling — a 2 GiB global pre-parse bound plus a per-engine post-detection check that holds image/document/html/audio at their prior 100 MiB effective ceiling.**

## Performance

- **Duration:** ~40 min
- **Started:** 2026-07-21 (worktree spawn)
- **Completed:** 2026-07-21
- **Tasks:** 2 completed
- **Files modified:** 6 (`internal/api/api.go`, `internal/api/handlers.go`, `internal/api/handlers_test.go`, `cmd/api/main.go`, `.env.example`, `docker-compose.yml`)

## Accomplishments

- `Enqueuer` gained `EnqueueAVConvert`; `fakeQueue` in `handlers_test.go` mirrors `EnqueueAudioConvert`'s single-field recording shape (`enqueuedAV uuid.UUID`, not the reconciler's slice-plus-error idiom); the enqueue switch in `handlers.go` gained the `EngineAV` case ahead of the fail-closed `default:`.
- `cmd/api/main.go`'s queue-depth collector call is now `append(queue.AllConvertQueues(), queue.QueueWebhook)...` instead of a hand-written variadic list — the specific silent seam D-06/Pitfall 2 named (a variadic call can drop an engine's Prometheus series with zero compile error) is now structurally guarded by `queue.TestAllConvertQueuesCoversEveryEngine` (plan 02).
- D-07's two-tier ceiling: `Server.maxEngineBytes` / `Config.MaxEngineBytes` default (when nil) to image/document/html/audio at 100 MiB and av at 2 GiB; the check runs in `handleCreateJob` immediately after `convert.Default.EngineFor` and strictly before `s.storage.Upload`, logging `reason=engine_size_limit` and returning 413 on violation.
- `cmd/api/main.go`'s `MAX_UPLOAD_BYTES` default raised to `2<<30` (2 GiB); `.env.example` and `docker-compose.yml` updated in lockstep to `2147483648` with D-07 documented inline in `.env.example`'s comment, preventing the DEBT-05-style env drift a prior milestone logged.
- Four new tests cover all five D-07 behavior bullets: `NewServer` zero-value defaulting, a scaled-down oversized-image 413 (with storage-never-called and job-never-created assertions), a scaled-down large-av-upload 202 (via a synthetic test-only `(mp4, avtestout)` converter registration, since the real `AVConverter` is still unregistered per D-08), and an engine-absent-from-map acceptance test proving the map gates rather than allowlists.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add EnqueueAVConvert to the Enqueuer seam and derive the collector queue list** - `1ac6213` (feat)
2. **Task 2: Implement the D-07 two-tier upload ceiling** - `f324d33` (feat)

_Task 2 was `tdd="true"`; production code and its tests landed in the same commit, following the same-task-commit-granularity precedent set in `35-01-SUMMARY.md` (no separate RED/GREEN commits within this plan's tasks)._

## Files Created/Modified

- `internal/api/api.go` - `Enqueuer.EnqueueAVConvert`; `Server.maxEngineBytes` / `Config.MaxEngineBytes`; `NewServer`'s D-07 five-engine defaulting
- `internal/api/handlers.go` - `EngineAV` enqueue-switch case; the D-07 per-engine ceiling check between `EngineFor` and `storage.Upload`
- `internal/api/handlers_test.go` - `fakeQueue.EnqueueAVConvert`/`enqueuedAV`; `paddedPNGFixture`/`mp4BytesFixture`/`paddedMP4Fixture`; `fakeAVConverter` + its test-scope `init()` registration; `TestNewServer_MaxEngineBytesDefaults`, `TestCreateJob_EngineSizeLimitRejectsOversizedImage`, `TestCreateJob_EngineSizeLimitAcceptsLargeAVUpload`, `TestCreateJob_EngineAbsentFromSizeMapNotRejected`
- `cmd/api/main.go` - derived collector arg list (`AllConvertQueues()...` + explicit `QueueWebhook`); `MAX_UPLOAD_BYTES` default raised to `2<<30`
- `.env.example` - `MAX_UPLOAD_BYTES=2147483648` with D-07 rationale documented inline
- `docker-compose.yml` - `MAX_UPLOAD_BYTES: "2147483648"` (sole line touched, verified via `git diff`)

## Decisions Made

- Left `cmd/api/main.go`'s `api.Config` literal with `MaxEngineBytes` unset (nil), relying entirely on `NewServer`'s own defaulting rather than duplicating the five-engine map at two call sites — the plan explicitly offered this as one of two valid choices and asked for the choice to be recorded here.
- Tested D-07's size bullets ("200 MiB image", "1 GiB av upload") via proportionally scaled-down `MaxEngineBytes` overrides (100 bytes / 5 MiB) instead of literal multi-hundred-megabyte in-memory payloads — this is the same convention `TestCreateJob_DimensionLimitExceeded` already uses for `MaxImagePixels` (scaled quota, not literal 10000x10000 pixels), keeps the test suite fast, and still proves the exact gating logic (per-engine ceiling checked post-detection, pre-storage-write).
- Exercised `engine=="av"` through `handleCreateJob` via a test-only `fakeAVConverter` registered onto the process-global `convert.Default` registry for a synthetic, collision-free `(mp4, avtestout)` pair, rather than attempting to register the real `AVConverter` — that registration is explicitly deferred to a later Phase 35 plan (D-08, paired with wiring `SniffVideo` into the detection chain) and `internal/convert` is outside this plan's `files_modified` scope. This is process-local test-binary state only; it does not affect production registration or other packages' test binaries.

## Deviations from Plan

None — plan executed exactly as written, including both explicitly offered discretion points (which of the two `MaxEngineBytes`-population strategies to use in `cmd/api/main.go`, documented above).

## Issues Encountered

None. `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...` (full repo) all pass cleanly at HEAD. `git diff docker-compose.yml` touches only the `MAX_UPLOAD_BYTES` line, as the plan's own `<verification>` block requires. `docker compose config -q` succeeds.

## Known Stubs

None.

## Threat Flags

None — this plan's changes stay inside the threat register `35-04-PLAN.md` already declared (T-35-13 through T-35-16, T-35-SC), all dispositioned `mitigate`/`accept` in the plan itself. No new network endpoints, auth paths, or trust-boundary-crossing file access patterns were introduced.

## User Setup Required

None beyond redeploying with the updated `MAX_UPLOAD_BYTES=2147483648` env value (already reflected in `.env.example` and `docker-compose.yml` for local/dev use); operators running a real deployment with a custom `.env` should raise their own `MAX_UPLOAD_BYTES` to admit video uploads once the `av` engine class is reachable end-to-end (a later Phase 35 plan).

## Next Phase Readiness

- `Enqueuer.EnqueueAVConvert` and the `EngineAV` enqueue-switch case are ready for whichever later Phase 35 plan registers the real `AVConverter` and wires `SniffVideo` into the detection chain (D-08) — once that lands, real video uploads will flow through the exact ceiling-check and enqueue-switch code this plan added, with no further `internal/api` changes required.
- `Server.maxEngineBytes` / `Config.MaxEngineBytes` are available for any future engine class to opt into a per-engine ceiling simply by adding a key to the default map in `NewServer` — no handler-level changes needed.
- The queue-depth collector's derivation from `queue.AllConvertQueues()` means a sixth future engine class only needs to be added to that one helper (and `convert.go`'s engine constants) for its Prometheus series to appear automatically at this call site.
- No blockers for downstream Phase 35 plans (06, 07).

---
*Phase: 35-queue-worker-routing-integration*
*Completed: 2026-07-21*

## Self-Check: PASSED

- FOUND: internal/api/api.go
- FOUND: internal/api/handlers.go
- FOUND: internal/api/handlers_test.go
- FOUND: cmd/api/main.go
- FOUND: .env.example
- FOUND: docker-compose.yml
- FOUND: .planning/phases/35-queue-worker-routing-integration/35-04-SUMMARY.md
- FOUND commit: 1ac6213 (Task 1)
- FOUND commit: f324d33 (Task 2)
- FOUND commit: f434511 (this SUMMARY)
