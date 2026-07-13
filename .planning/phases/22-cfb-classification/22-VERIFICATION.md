---
phase: 22-cfb-classification
verified: 2026-07-13T13:00:00Z
status: passed
score: 9/9 must-haves verified
overrides_applied: 0
---

# Phase 22: CFB Encrypted-vs-Legacy Classification Verification Report

**Phase Goal:** OLE-CFB uploads get distinct, bounded 422s — «password-protected» vs «legacy binary» — via a hand-rolled, fuzz-hardened, fail-closed CFB directory parser.
**Verified:** 2026-07-13T13:00:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `ClassifyCFB(legacy.doc)` == `CFBLegacy`, `ClassifyCFB(encrypted.docx)` == `CFBEncrypted` (real fixtures) | ✓ VERIFIED | `internal/convert/cfb.go`; re-ran `go test ./internal/convert/ -run TestClassifyCFB -v` myself — both subtests PASS |
| 2 | Encrypted marker wins when both `EncryptionInfo`/`EncryptedPackage` and a legacy marker are present (D-03) | ✓ VERIFIED | `classifyCFBNames` (cfb.go:215-234) checks `hasEncrypted` before `hasLegacy`; `cfbBothMarkersSample` unit test passes |
| 3 | Cyclic/self-referential FAT chain, truncated header, invalid SectorShift, OOB FirstDirSectorLocation all return `CFBUnknown` bounded (never hang) | ✓ VERIFIED | `cfbWalkDirectory` visited-set + 4096-sector cap (cfb.go:150-210); unit test asserts the cyclic case returns within 1s via goroutine+select — re-ran, PASS |
| 4 | Any non-CFB bytes / short read / decode error / unrecognized stream set returns `CFBUnknown`, never panics | ✓ VERIFIED | `CFBUnknown` is the zero value (iota); every early-return path in `ClassifyCFB`/`buildCFBFAT`/`cfbWalkDirectory` falls back to it; re-ran full unit table, all pass |
| 5 | `FuzzClassifyCFB` runs a bounded fuzz session crash-free, seeded with real fixtures + crafted corrupt variants (CFB-02 exit-gate) | ✓ VERIFIED | Independently re-ran `go test ./internal/convert/ -run '^$' -fuzz '^FuzzClassifyCFB$' -fuzztime 10s` myself: ~1.27M execs, 0 crashes, exit 0, no artifacts left under `testdata/fuzz/` |
| 6 | `handleCreateJob`'s IsOLECFB branch switches on `ClassifyCFB`: encrypted→"remove the password"/password-protected 422, legacy→"legacy binary .../.doc/.xls/.ppt" 422, unknown→byte-identical original combined 422 (D-06) | ✓ VERIFIED | `internal/api/handlers.go:249-264`; exact message-contract strings confirmed by direct source read |
| 7 | The three cases log distinct `reason=` tags (encrypted_document / legacy_document / legacy_or_encrypted_document) | ✓ VERIFIED | `handlers.go:251,256,261`; observed live in re-run test output |
| 8 | All three branches reject before any S3 upload or Postgres job row | ✓ VERIFIED | `TestCreateJob_CFB` and `TestCreateJob_OLECFBRejected` assert `store.uploaded==false`, `repo.created==nil`; re-ran, PASS |
| 9 | `TestOLECFBRejectionE2E` asserts legacy.doc→legacy message, encrypted.docx→encrypted message, live against the compose stack (D-07 unconditional gate) | ✓ VERIFIED | Independently re-ran against the still-running compose stack (`docker compose -p octoconv ps` showed all services Up): `--- PASS: TestOLECFBRejectionE2E` with both `legacy.doc` and `encrypted.docx` subtests passing |

**Score:** 9/9 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/cfb.go` | `ClassifyCFB(r io.ReaderAt, size int64) CFBClass` + enum, 252 lines | ✓ VERIFIED | Substantive bounded parser (header parse, header-DIFAT FAT build, visited-set directory walk, UTF-16LE decode, D-03 classification); no stubs |
| `internal/convert/cfb_test.go` | Unit table + `FuzzClassifyCFB` | ✓ VERIFIED | `TestClassifyCFB` (8 subtests) + `FuzzClassifyCFB` (7 seeds); all re-run and pass |
| `internal/convert/testdata/legacy.doc`, `encrypted.docx` | Real fixtures | ✓ VERIFIED | Present, match `internal/e2e/testdata/` originals |
| `internal/api/handlers.go` | Three-way `convert.ClassifyCFB` switch | ✓ VERIFIED | `handlers.go:249` calls `convert.ClassifyCFB(file, header.Size)`, switches on all 3 classes |
| `internal/api/handlers_test.go` | `TestCreateJob_CFB` | ✓ VERIFIED | Table-driven test over real fixtures, asserts distinct substrings + absence + no-upload/no-job; re-ran, PASS |
| `internal/api/testdata/legacy.doc`, `encrypted.docx` | Copied fixtures | ✓ VERIFIED | Present |
| `internal/e2e/e2e_test.go` | `TestOLECFBRejectionE2E` distinct-message table | ✓ VERIFIED | `oleCFBFixtures` struct slice with per-fixture `wantSubstr`/`wantAbsent`; re-ran live, PASS |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/api/handlers.go handleCreateJob` | `internal/convert.ClassifyCFB` | `convert.ClassifyCFB(file, header.Size)` inside `IsOLECFB` branch | ✓ WIRED | Confirmed by source read + passing handler tests exercising real fixtures through the full HTTP path |
| `internal/e2e/e2e_test.go TestOLECFBRejectionE2E` | live compose API | `postJobExpectStatus` per-fixture distinct substrings | ✓ WIRED | Re-ran live against the running stack — both subtests pass with exact contract bodies |
| `internal/convert/cfb.go ClassifyCFB` | every `ReadAt` | bounds-check offset+len against `size` before read | ✓ WIRED | `cfbSectorInBounds` called before every sector read (buildCFBFAT, cfbWalkDirectory); header read guarded by `size < cfbHeaderSize` check |
| `internal/convert/cfb.go` FAT/directory walk | cycle rejection | `visited` map + `cfbMaxDirSectors` cap | ✓ WIRED | `cfbWalkDirectory` checks `visited[sector]` and `count >= cfbMaxDirSectors` before every iteration |

### Behavioral Spot-Checks / Independent Re-Runs

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Unit table (fixtures + DoS cases) | `go test ./internal/convert/ -run TestClassifyCFB -v` | 8/8 subtests PASS | ✓ PASS |
| Handler tests (3-way split, fail-closed compat) | `go test ./internal/api/ -run 'TestCreateJob_CFB\|TestCreateJob_OLECFBRejected' -v` | all PASS, `reason=` tags observed distinct | ✓ PASS |
| Fuzz gate (independent 10s re-run) | `go test ./internal/convert/ -run '^$' -fuzz '^FuzzClassifyCFB$' -fuzztime 10s` | ~1.27M execs, 0 crashes, exit 0 | ✓ PASS |
| Live e2e gate (independent re-run against still-up compose stack) | `E2E_BASE_URL=... go test ./internal/e2e/ -run TestOLECFBRejectionE2E -v -timeout 5m` | `--- PASS` both subtests | ✓ PASS |
| Full offline suite | `go build ./... && go test ./...` | all packages ok | ✓ PASS |
| Format/vet | `gofmt -l . && go vet ./...` | clean | ✓ PASS |
| Zero new deps | `git diff 1b4383d..HEAD -- go.mod go.sum` (pre-phase base commit vs HEAD) | empty diff | ✓ PASS |
| Merged-tree coherence | `git log --oneline` | all three plan commits (`83bfdb7`, `7790de6`, `3381f16`) present and reachable on `main`; working tree clean | ✓ PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| CFB-01 | 22-01, 22-02 | Distinct 422s, zero-dep bounded parser, cycle-guard, unknown→generic fallback | ✓ SATISFIED | `cfb.go` parser + `handlers.go` 3-way switch, all live/unit-proven |
| CFB-02 | 22-01 | Fuzz exit-gate | ✓ SATISFIED | Independently re-ran fuzz, 0 crashes |

Note: `.planning/REQUIREMENTS.md` still shows CFB-01/CFB-02 as unchecked `[ ]` — this is a stale tracking artifact (the doc predates this phase's completion commits and hasn't been re-synced); ROADMAP.md already marks Phase 22 as `[x]` complete. This is a documentation-sync nit, not a code gap, and does not affect phase-goal achievement.

### Anti-Patterns Found

None. Scanned all 5 modified/created files (`cfb.go`, `cfb_test.go`, `handlers.go`, `handlers_test.go`, `e2e_test.go`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` — zero matches.

### Human Verification Required

None. All truths were verifiable via automated tests, direct source inspection, and independent live-gate re-runs against the already-running compose stack.

### Gaps Summary

No gaps. All 9 derived truths (roadmap success criteria 1-4 plus plan-level must-haves) verified with fresh, independent evidence — not just SUMMARY.md claims. Both plans' code was read line-by-line and their key claims (fixture classification, cycle-guard bounded time, three-way message split, fail-closed CFBUnknown default, fuzz crash-free, live e2e distinct messages) were independently re-executed by this verifier with matching results.

---

_Verified: 2026-07-13T13:00:00Z_
_Verifier: Claude (gsd-verifier)_
