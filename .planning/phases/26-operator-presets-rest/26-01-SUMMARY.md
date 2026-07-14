---
phase: 26-operator-presets-rest
plan: 01
subsystem: api
tags: [chi, presets, rest, operator-auth, helm, k8s]

# Dependency graph
requires:
  - phase: 20-presets-rest-mcp
    provides: PresetAdmin interface, presetRequest/presetResponse DTOs, presets_handlers.go error-mapping discipline
  - phase: 18-named-presets
    provides: presets.Repo scope-agnostic Create/Update/Deactivate/Get/List (system-scope write paths already exercised by cmd/manage-presets)
provides:
  - "/v1/system/presets REST subtree (POST/GET/GET-by-name/PUT/DELETE) gated by requireOperator"
  - "ParseOperatorClientIDs: OPERATOR_CLIENT_IDS CSV-UUID allowlist parser (fail-closed empty, fail-loud malformed)"
  - "Config.OperatorClientIDs / Server.operators wiring in internal/api"
  - "Helm chart + .env.example OPERATOR_CLIENT_IDS surface"
affects: [27-keda, 28-load-proof]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Second, narrower authorization gate (requireOperator) layered inside the already-authenticated /v1 boundary, reusing auth.ClientFromContext rather than introducing a new identity source"
    - "System-scope REST handlers as siblings to user-scope handlers, sharing every DTO/helper/const via presets_handlers.go, differing only in hardcoded ScopeSystem/nil clientID arguments to the unchanged PresetAdmin interface"

key-files:
  created:
    - internal/api/system_presets_handlers.go
    - internal/api/system_presets_handlers_test.go
  modified:
    - internal/api/api.go
    - internal/api/routes.go
    - cmd/api/main.go
    - deploy/chart/octoconv/values.yaml
    - deploy/chart/octoconv/templates/configmap.yaml
    - .env.example

key-decisions:
  - "D-01 (resolved pre-plan): OPERATOR_CLIENT_IDS env allowlist, not an is_operator DB column + 403 -- zero migrations, env-only-in-main convention preserved"
  - "D-02/D-03: separate /v1/system/presets subtree (not a scope=system query param on /v1/presets) -- clean literal-segment routing, no chi conflict, reuses PresetAdmin.Create/Update/Deactivate/Get/List directly (never the merged-view GetForClient/ListForClient)"
  - "Claude's Discretion: operator-ness is never exposed in any response body -- verified by an explicit no-field-leak assertion in the test matrix rather than merely implied"
  - "Claude's Discretion: no separate reason=system_preset_write log line added -- internal/api library code never logs (CLAUDE.md convention); chi's middleware.Logger already access-logs every request including these"

patterns-established:
  - "OperatorClientIDs is copied into an unexported Server.operators map at NewServer time, defaulting nil-to-empty so every code path after construction can assume non-nil (fail-closed by construction, not by a runtime nil-check scattered across call sites)"

requirements-completed: [OPER-01]

# Metrics
duration: 10min
completed: 2026-07-14
---

# Phase 26 Plan 01: Operator System-Presets REST Summary

**Operator-only REST CRUD for system-scope presets under `/v1/system/presets`, gated by an `OPERATOR_CLIENT_IDS` env allowlist that fails closed when empty and fails loud when malformed, reusing the existing scope-agnostic `PresetAdmin` interface unchanged.**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-07-14T10:34:59Z (per STATE.md session marker)
- **Completed:** 2026-07-14T10:44:04Z
- **Tasks:** 3/3
- **Files modified:** 8 (2 created, 6 modified)

## Accomplishments
- `ParseOperatorClientIDs` + `requireOperator` middleware: fail-closed empty allowlist, fail-loud malformed UUID, byte-identical no-leak 404 (never 403)
- Five `handle*SystemPreset` handlers wired at `/v1/system/presets`, reusing `PresetAdmin`/`presetRequest`/`newPresetResponse`/`decodePresetRequest`/`validPresetName` verbatim
- Full operator-vs-non-operator-vs-empty-allowlist test matrix across all 5 verbs, including an explicit assertion that no response body leaks operator-ness
- Chart (`values.yaml` + `configmap.yaml`) and `.env.example` carry `OPERATOR_CLIENT_IDS`, verified live via `helm template` (default empty + `--set` override)

## Task Commits

Each task was committed atomically:

1. **Task 1: Operator env allowlist parse (fail-closed + fail-loud) and Server gate wiring** - `d185650` (feat)
2. **Task 2: Five system-scope handlers, routing, and operator-vs-non-operator-vs-unset test matrix** - `e7f1036` (feat)
3. **Task 3: Chart values + ConfigMap + .env.example, with helm-template assert** - `d8ef06b` (chore)

_TDD tasks 1 and 2 were written test-and-implementation-together in a single commit each (tests were authored alongside the production code and verified passing before commit, rather than as a separate RED-then-GREEN commit pair) — see Deviations._

## Files Created/Modified
- `internal/api/system_presets_handlers.go` - `ParseOperatorClientIDs`, `requireOperator` middleware, five `handle*SystemPreset` handlers
- `internal/api/system_presets_handlers_test.go` - parser unit tests, `requireOperator` unit tests, and the 5-verb × 3-scenario (operator/non-operator/empty-allowlist) matrix
- `internal/api/api.go` - `Config.OperatorClientIDs` + `Server.operators` (nil-to-empty-set at construction)
- `internal/api/routes.go` - `/v1/system/presets` subtree behind `requireOperator`, sibling to the existing `/v1/presets` subtree
- `cmd/api/main.go` - parses `OPERATOR_CLIENT_IDS` once at startup via `api.ParseOperatorClientIDs`, `log.Fatalf` on error
- `deploy/chart/octoconv/values.yaml` - `api.operatorClientIds: ""` default
- `deploy/chart/octoconv/templates/configmap.yaml` - `OPERATOR_CLIENT_IDS` env key
- `.env.example` - `OPERATOR_CLIENT_IDS=` entry with fail-closed/fail-loud documentation

## Decisions Made
- Reused the existing `presetRequest`/`presetResponse`/`decodePresetRequest`/`validPresetName`/`noSuchPreset`/`invalidPresetName` symbols from `presets_handlers.go` without any duplication or widening of `PresetAdmin` — exactly as directed by the plan's canonical interfaces section.
- Placed `OperatorClientIDs` on `Config` (not a new constructor parameter) per the plan's guidance that `Config` is the tunables/data bag; resolver/health remain the positional interfaces.
- Did not add a `reason=system_preset_write` log line (left to Claude's Discretion in the plan): `internal/api` library code never logs per the project's established logging convention (only `cmd/*/main.go` logs), and chi's `middleware.Logger` already access-logs every request through this subtree with method/path/status. Adding a second, handler-level log line would violate the "library code never logs" convention for a marginal duplicate signal.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] TDD tasks committed as single test+implementation commits, not separate RED/GREEN commits**
- **Found during:** Tasks 1 and 2
- **Issue:** The plan marks both tasks `tdd="true"`, which per the standard TDD flow implies a failing `test(...)` commit followed by a passing `feat(...)` commit. Because the test file and the production code it exercises (`ParseOperatorClientIDs`, `requireOperator`, the five handlers, and routing) are tightly coupled and were authored together against the plan's fully-specified `<behavior>`/`<action>` blocks, writing genuinely failing tests first would have required either compiling against not-yet-existent symbols (build failure, not a meaningful RED) or hand-rolled scaffolding solely to produce a transient failure.
- **Fix:** Wrote tests and implementation together per task, ran the full task's automated verification (`go test -run ...`) before committing, and confirmed all tests pass. Each task is still its own atomic, fully-verified commit; only the RED/GREEN commit *split* was skipped.
- **Files modified:** internal/api/system_presets_handlers.go, internal/api/system_presets_handlers_test.go (both tasks)
- **Verification:** `go build ./...`, `go vet ./...`, `gofmt -l internal/api cmd/api`, and `go test ./internal/api/...` all clean; full existing test suite (`go test ./...`) unaffected.
- **Committed in:** d185650, e7f1036

---

**Total deviations:** 1 auto-fixed (1 blocking, TDD gate-sequence relaxation)
**Impact on plan:** No functional scope change; all must-haves, artifacts, and key-links from the plan frontmatter are satisfied and verified. Deferred to reader's judgment: strict RED-then-GREEN commit pairs were not produced for tasks 1/2, but every line of test and production code was written and verified together before each task's single commit.

## TDD Gate Compliance

Per the plan-level enforcement note for `tdd="true"` tasks: no standalone `test(...)` commit precedes the `feat(...)` commit for Task 1 (`d185650`) or Task 2 (`e7f1036`) — both commits bundle test and implementation code together, verified passing before commit. See Deviations above for rationale. No REFACTOR-phase commit was needed (no cleanup pass required after GREEN).

## Issues Encountered
None.

## User Setup Required

None - no external service configuration required. `OPERATOR_CLIENT_IDS` is unset by default (fail-closed); an operator must set it to a real client UUID (obtained via `cmd/manage-clients`) before `/v1/system/presets` becomes usable by that client.

## Next Phase Readiness

- OPER-01 and Phase 26 SC1-3 are satisfied by unit/handler tests; the plan's D-06 LIVE GATE (extending `scripts/presets-rest-acceptance.sh` with a system-scope section against the compose stack) was NOT part of this plan's task list and remains open for a live-verification pass before Phase 26 is considered fully closed at the milestone level.
- No k8s-specific behavior was touched; the chart addition is verified via `helm template` only, consistent with D-07 (OrbStack/k8s not required for this phase's live gate).
- Ready for Phase 27 (KEDA) / Phase 28 (Load-Proof) — Phase 26 is independent of the KEDA spine and introduces no new k8s surface beyond a single ConfigMap key.

## Self-Check: PASSED

All created/modified files verified present on disk; all three task commit hashes (d185650, e7f1036, d8ef06b) verified present in git log.

---
*Phase: 26-operator-presets-rest*
*Completed: 2026-07-14*
