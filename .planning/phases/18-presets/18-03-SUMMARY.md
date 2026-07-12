---
phase: 18-presets
plan: 03
subsystem: api

tags: [go, chi, presets, handleCreateJob, interface-segregation, toctou]

# Dependency graph
requires:
  - phase: 18-presets plan 01
    provides: internal/presets package (Repo.Resolve, Preset struct, ErrNotFound) and jobs.CreateParams.PresetName/PresetVersion columns
provides:
  - "PresetRepo single-method interface in internal/api/api.go, wired through Server/NewServer/cmd/api/main.go"
  - "handleCreateJob preset resolution: preset=<name> resolves to target_format+opts after client auth and before EngineFor"
  - "D-01 XOR enforcement: preset + explicit target/opts is a 422, no merge"
  - "D-03 no-leak: single errUnknownPreset 422 constant for nonexistent/inactive/cross-client preset misses"
  - "D-06 re-validation: preset-sourced opts flow through the SAME ParseDocOpts/ParseHTMLOpts + ValidateApplicability pipeline as ad-hoc opts, no bypass"
  - "Pitfall 8 TOCTOU guard: cheap non-locking re-Resolve immediately before repo.Create rejects a preset deactivated/bumped in the resolve-to-insert window"
  - "D-08 provenance: PresetName/PresetVersion persisted into jobs.CreateParams"
affects: [18-04, future preset REST CRUD (PRST-V2-01, v2)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Interface-segregated single-method PresetRepo (mirrors Repo/Storage/Enqueuer pattern in internal/api/api.go)"
    - "Pre-Create TOCTOU re-check re-uses the SAME Resolve method rather than adding a second interface method"
    - "Opts-source substitution: the existing rawOpts variable is re-sourced from json.Marshal(preset.Options) instead of the client form field, so the validators downstream are completely unaware a preset was used"

key-files:
  created: []
  modified:
    - internal/api/api.go
    - internal/api/handlers.go
    - internal/api/handlers_test.go
    - internal/api/routes_test.go
    - cmd/api/main.go

key-decisions:
  - "PresetRepo stays single-method (Resolve only); the pre-Create active re-check re-uses Resolve rather than adding a second interface method, preserving D-09's narrowness"
  - "Mutual-exclusivity (D-01) and preset-name length checks run before ANY preset DB lookup or client-state dependency, so they never leak existence information"
  - "Preset resolution happens immediately after client auth resolution and before content-detection/Sniff, matching D-07's ordering guarantee"
  - "The pre-Create re-check leaves the already-uploaded object in place on rejection (no delete), mirroring the existing repo.Create-failure no-cleanup path exactly"

patterns-established:
  - "TOCTOU guard via cheap re-call of an existing narrow-interface method rather than adding lock-taking machinery to the API layer"

requirements-completed: [PRST-02, PRST-03, PRST-04]

# Metrics
duration: 35min
completed: 2026-07-12
---

# Phase 18 Plan 03: handleCreateJob Preset Resolution Summary

**POST /v1/jobs now accepts `preset=<name>` which resolves through a narrow PresetRepo interface to target_format+opts, enforcing mutual exclusivity with explicit target/opts, a single non-leaking 422 for any resolution miss, full re-validation of stored opts through the existing engine-keyed parsers, and a pre-insert TOCTOU re-check that rejects a preset deactivated in the resolve-to-create window.**

## Performance

- **Duration:** 35 min
- **Started:** 2026-07-12T17:42:00Z
- **Completed:** 2026-07-12T18:17:25Z
- **Tasks:** 3
- **Files modified:** 5 (internal/api/api.go, internal/api/handlers.go, internal/api/handlers_test.go, internal/api/routes_test.go, cmd/api/main.go)

## Accomplishments
- Added a narrow, single-method `PresetRepo` interface (D-09) wired through `Server`, `NewServer`, and `cmd/api/main.go`
- `handleCreateJob` resolves `preset=<name>` after client auth and before `convert.Default.EngineFor` (D-07), sourcing `target_format` and opts from the resolved preset
- Mutual exclusivity (D-01): `preset` + explicit `target`/non-empty `opts` returns 422 before any DB lookup
- No-existence-leak (D-03): every resolution miss (nonexistent, inactive, cross-client) returns the identical `errUnknownPreset` 422 body, verified byte-for-byte in tests
- Re-validation (D-06): preset-sourced opts are re-marshaled to JSON and fed through the exact same `ParseDocOpts`/`ParseHTMLOpts` + `ValidateApplicability`/`ValidateHTMLApplicability` pipeline used for ad-hoc opts -- a stale/invalid stored preset fails job creation
- TOCTOU guard (Pitfall 8): immediately before `repo.Create`, a second cheap non-locking `Resolve` call re-checks the preset is still the same id+version; a deactivated or bumped preset rejects the job with no cleanup of the already-uploaded object
- Provenance (D-08): `PresetName`/`PresetVersion` flow into `jobs.CreateParams`

## Task Commits

Each task was committed atomically:

1. **Task 1: Add PresetRepo interface, Server field, NewServer param, and main.go wiring** - `bd2fef0` (feat)
2. **Task 2: Preset resolution + mutual-exclusivity + re-validation + pre-Create active re-check in handleCreateJob** - `527bfcb` (feat)
3. **Task 3: Handler tests — fakePresetRepo + resolution/exclusivity/no-leak/re-validation/deactivation-race** - `f352618` (test)

_Note: this plan was executed by an auto-mode agent with no TDD gate (type: execute); no test → feat → refactor gate sequence applies._

## Files Created/Modified
- `internal/api/api.go` - narrow `PresetRepo` interface, `Server.presets` field, `NewServer` positional param (after `queue`, before `resolver`)
- `internal/api/handlers.go` - `handleCreateJob`: `formFieldPreset`/`errUnknownPreset`/`maxPresetNameBytes` constants, XOR gate, preset resolution before `EngineFor`, opts-source substitution feeding the unchanged validators, pre-Create TOCTOU re-check, `PresetName`/`PresetVersion` provenance in `CreateParams`
- `internal/api/handlers_test.go` - `fakePresetRepo` (call-counted, supports a different second-call result), `multipartBodyWithPreset` helper, 7 new preset tests, all `NewServer(...)` call sites updated for the new positional argument
- `internal/api/routes_test.go` - `NewServer(...)` call site updated (Rule 3 blocking-build fix, outside the plan's declared file scope)
- `cmd/api/main.go` - `presets.NewRepo(pool)` constructed and wired into `api.NewServer`

## Decisions Made
- PresetRepo stays single-method (Resolve only) per D-09; the pre-Create re-check re-uses the same method rather than growing the interface
- Preset resolution placed immediately after client-auth resolution, before Sniff/content-detection, so `target` is fully resolved before any content-format logic runs
- The TOCTOU re-check on rejection leaves the uploaded object in place (no delete), exactly mirroring the pre-existing `repo.Create`-failure behavior
- `maxPresetNameBytes=128` bounds the client-supplied name before any DB lookup, and its violation returns 400 (not 422), since length is request-shape-only and leaks nothing about preset existence

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Updated routes_test.go's direct NewServer call**
- **Found during:** Task 3 (handler tests) — `go vet ./internal/api/` failed after the Task 1 signature change
- **Issue:** `internal/api/routes_test.go` (not listed in this plan's `files_modified`) contains its own direct `NewServer(...)` call (`TestByIP_NotEvadedByForwardedForSpoofing`) that did not compile against the new positional `PresetRepo` argument added in Task 1
- **Fix:** Inserted `defaultFakePresetRepo()` into the new positional slot, matching the same default-to-ErrNotFound convention used everywhere else in the package
- **Files modified:** internal/api/routes_test.go
- **Verification:** `go vet ./internal/api/...` and `go test ./internal/api/...` both pass
- **Committed in:** f352618 (Task 3 commit)

---

**Total deviations:** 1 auto-fixed (1 blocking build fix)
**Impact on plan:** Necessary to keep the package compiling after the NewServer signature change; no scope creep — the fix is a one-line argument insertion identical in spirit to the plan's own instruction to update "EVERY direct NewServer(...) call in the test file."

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- POST /v1/jobs preset support is fully wired end-to-end: `internal/api` depends only on the narrow `PresetRepo` interface, `cmd/api/main.go` constructs the real `presets.NewRepo(pool)`
- `go build ./... && go vet ./...` clean; `go test ./...` green across the whole repo (internal/presets, internal/api, and all other packages)
- Ready for 18-04 (whatever remaining presets-phase work depends on this: e.g. E2E preset test, docs, or final phase wrap-up)
- No blockers; `cmd/manage-presets` (owned by the parallel 18-02 executor) was not present in this worktree at execution time and was correctly left untouched

---
*Phase: 18-presets*
*Completed: 2026-07-12*

## Self-Check: PASSED

All 5 modified files and the SUMMARY.md itself verified present on disk; all 3 task commit hashes (bd2fef0, 527bfcb, f352618) verified present in `git log --oneline --all`.
