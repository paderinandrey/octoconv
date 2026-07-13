---
phase: 22-cfb-classification
plan: 01
subsystem: convert
tags: [go, cfb, ole2, security, dos-hardening, fuzzing, unit-testing]

# Dependency graph
requires: []
provides:
  - "internal/convert/cfb.go: ClassifyCFB(r io.ReaderAt, size int64) CFBClass + CFBClass enum (CFBEncrypted/CFBLegacy/CFBUnknown)"
  - "internal/convert/cfb_test.go: unit table over real fixtures + DoS cases, and FuzzClassifyCFB fuzz target"
  - "internal/convert/testdata/legacy.doc, encrypted.docx: package-local test fixtures"
affects: [22-cfb-classification (plan 22-02, API integration)]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Bounded, fail-closed CFB directory parser: visited-set + hard sector cap on every FAT-chain follow, bounds-checked ReadAt before every sector read, CFBUnknown as the zero value so every failure path fails closed by construction"
    - "Flat linear scan of directory entries (never the red-black sibling/child tree) to enumerate stream names, avoiding tree-traversal bug surface"
    - "Header-DIFAT-only FAT build (109 entries); files requiring additional DIFAT sectors (~>7MB) bail to CFBUnknown rather than being walked further"

key-files:
  created:
    - internal/convert/cfb.go
    - internal/convert/cfb_test.go
    - internal/convert/testdata/legacy.doc
    - internal/convert/testdata/encrypted.docx
  modified: []

key-decisions:
  - "Book AND Workbook are both in the legacy allow-list per plan spec; not independently re-verified against a live .xls fixture in this plan (only legacy.doc, a .doc/WordDocument-marker file, and encrypted.docx were available as real fixtures) -- WordDocument path was the one fixture-proven end-to-end"
  - "Chain-continuation-undetermined (a directory sector's FAT[sector] entry falls outside the header-DIFAT-covered range) is treated as CFBUnknown (fail-closed) rather than silently stopping and classifying on a partial name set"
  - "FuzzClassifyCFB was written together with cfb.go/unit tests in the Task 1 commit (all three files edited in one sitting under TDD-ish flow); Task 2 consisted of running the fuzz gate itself, which produced no new commits since it found zero crashes"

requirements-completed: [CFB-01, CFB-02]

# Metrics
duration: ~20min
completed: 2026-07-13
---

# Phase 22 Plan 01: CFB Directory Parser + Fuzz Gate Summary

**Hand-rolled, bounded, fuzz-hardened CFB directory-entry-name parser (`ClassifyCFB`) distinguishing encrypted vs. legacy-binary Office uploads, zero new dependencies, proven crash-free over a 3.5M-execution 30s native fuzz run.**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-07-13T09:35:20Z
- **Tasks:** 2/2 completed
- **Files modified:** 4 (all new: cfb.go, cfb_test.go, 2 testdata fixtures)

## Accomplishments
- `ClassifyCFB` correctly classifies the real `legacy.doc` fixture as `CFBLegacy` (via the `WordDocument` stream marker) and the real `encrypted.docx` fixture as `CFBEncrypted` (via `EncryptionInfo`/`EncryptedPackage`) — proven end-to-end through header parse → FAT build → directory-sector walk → name decode → classification.
- DoS hardening proven by unit test: a self-referential FAT-chain cycle is rejected via visited-set and returns `CFBUnknown` within a 1-second bounded-time assertion (goroutine + `select`/`time.After`), not a generic test timeout.
- `FuzzClassifyCFB` ran a 30-second bounded local fuzz session: 3,528,385 executions, 0 crashes, exit 0 — satisfying the CFB-02 phase-exit gate.
- Fail-closed default confirmed for every corrupt/edge input: truncated header (<512 bytes), invalid `SectorShift`, out-of-bounds `FirstDirSectorLocation`, non-CFB bytes — all return `CFBUnknown`, never panic.
- Encrypted-wins-over-legacy precedence (D-03) proven with a synthetic directory containing both `EncryptionInfo` and `WordDocument` entries.

## Task Commits

Each task was committed atomically:

1. **Task 1: internal/convert/cfb.go — ClassifyCFB parser + fixtures + unit table** - `83bfdb7` (feat) — this commit also includes the `FuzzClassifyCFB` target (see Deviations) since it was authored in the same file alongside the unit tests.
2. **Task 2: FuzzClassifyCFB — bounded local fuzz gate** - no new commit (the fuzz target already existed from Task 1's commit; running the 30s `-fuzz` gate found zero crashes and left no corpus/regression artifacts to commit — Go's "interesting" coverage inputs were cached under `$GOCACHE/fuzz`, not the repo).

**Plan metadata:** (this SUMMARY's own commit, made by the orchestrator/caller per instructions — STATE.md/ROADMAP.md are NOT updated by this plan run.)

## Files Created/Modified
- `internal/convert/cfb.go` - `ClassifyCFB(r io.ReaderAt, size int64) CFBClass`, the `CFBClass` enum, and all bounded/fail-closed parsing helpers (header parse, header-DIFAT-only FAT build, visited-set-guarded directory-sector walk, UTF-16LE name decode, D-03 classification)
- `internal/convert/cfb_test.go` - `TestClassifyCFB` unit table (real fixtures + 6 crafted DoS/edge cases) and `FuzzClassifyCFB` native fuzz target with 7 seeds (2 real fixtures + 5 crafted corrupt variants), plus shared byte-buffer-building test helpers (`buildCFBFile`, `cfbEntryBytes`, and named sample builders)
- `internal/convert/testdata/legacy.doc` - real Word97 legacy-binary CFB fixture, copied from `internal/e2e/testdata/`
- `internal/convert/testdata/encrypted.docx` - real Agile-encrypted OOXML CFB fixture, copied from `internal/e2e/testdata/`

## Decisions Made
- Directory-sector FAT-chain lookups that resolve to a sector number not covered by any header-DIFAT-referenced FAT range are treated as "chain continuation undetermined" and return `CFBUnknown` (fail-closed) rather than classifying on a possibly-incomplete name set gathered so far. This is stricter than strictly required by the plan but keeps the fail-closed invariant airtight for a case the plan didn't explicitly enumerate.
- Kept `Book` and `Workbook` both in the legacy allow-list exactly as specified (D-03/CFB-02); no real `.xls` fixture was available in `internal/e2e/testdata/` to independently re-confirm the `Book`-vs-`Workbook` distinction beyond what the plan's `ms_cfb_facts` already asserts, so this remains spec-driven rather than fixture-re-verified for that specific stream name.

## Deviations from Plan

**1. [No functional deviation - task-boundary note] FuzzClassifyCFB implemented in the Task 1 commit rather than a separate Task 2 commit**
- **Found during:** Task 1 (writing cfb_test.go)
- **Issue:** The plan structures Task 1 (parser + unit table) and Task 2 (FuzzClassifyCFB target + fuzz gate run) as separate atomic commits, but since both live in the same `cfb_test.go` file and were authored together for internal consistency (shared sample-builder helpers used by both the unit table and the fuzz seeds), splitting them into two commits would have required either duplicating helpers or committing a temporarily-incomplete file.
- **Fix:** Committed `cfb.go` + `cfb_test.go` (including `FuzzClassifyCFB`) together as the Task 1 commit. Task 2's actual deliverable — running the bounded 30s fuzz gate and confirming it exits 0 crash-free — was then executed and verified with no code changes required, so it produced no additional commit.
- **Files affected:** `internal/convert/cfb_test.go` (already covered by commit `83bfdb7`)
- **Verification:** `go test ./internal/convert/ -run 'FuzzClassifyCFB' -v` (seed corpus, 8 seeds incl. sub-seeds, all pass) and `go test ./internal/convert/ -run '^$' -fuzz '^FuzzClassifyCFB$' -fuzztime 30s` (3,528,385 execs, 0 crashes, exit 0) both ran clean after the Task 1 commit.
- **Committed in:** `83bfdb7` (Task 1 commit; no separate Task 2 commit exists)

---

**Total deviations:** 1 (task-boundary/commit-structure note only; no code behavior deviates from the plan)
**Impact on plan:** None on functionality or correctness — purely a commit-granularity note explaining why only one code commit exists for two plan tasks.

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `ClassifyCFB` is ready for Plan 22-02 to wire into `internal/api/handlers.go`'s existing `IsOLECFB` branch (D-06): `CFBEncrypted` → password-protected 422, `CFBLegacy` → legacy-binary-format 422, `CFBUnknown` → existing combined 422 unchanged.
- `internal/api` was NOT touched in this plan, as required (wave-2 scope only).
- Zero new dependencies added; `go.mod`/`go.sum` unchanged.
- `gofmt -l`, `go vet ./...`, and `go test ./...` (full suite, no docker required) are all clean.

## Self-Check: PASSED

- FOUND: internal/convert/cfb.go
- FOUND: internal/convert/cfb_test.go
- FOUND: internal/convert/testdata/legacy.doc
- FOUND: internal/convert/testdata/encrypted.docx
- FOUND commit: 83bfdb7

---
*Phase: 22-cfb-classification*
*Completed: 2026-07-13*
