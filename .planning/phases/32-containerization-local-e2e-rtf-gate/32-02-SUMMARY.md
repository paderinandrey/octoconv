---
phase: 32-containerization-local-e2e-rtf-gate
plan: 02
subsystem: convert
tags: [cgroup, whisper.cpp, threads, audio-worker]

# Dependency graph
requires:
  - phase: 32-containerization-local-e2e-rtf-gate
    plan: 01
    provides: Dockerfile.audio-worker, live-confirmed cgroup v2 cpu.max format ("200000 100000" under --cpus=2.0), confirmed whisper-cli -t flag presence
provides:
  - internal/convert.CgroupCPULimit() (cgroup v2 cpu.max reader, fail-open)
  - internal/convert.SetAudioThreads() / audioThreadCount() (2-tier resolver, mirrors SetAudioModelPath)
  - whisperArgs() now always emits an explicit "-t <n>" pair
  - cmd/audio-worker resolveAudioThreads() (AUDIO_THREADS env -> cgroup -> runtime.NumCPU() precedence, resolved+logged at startup before srv.Start)
  - AUDIO_THREADS documented in .env.example as optional operator escape hatch
affects: [32-03, 32-04, 32-05, 33-keda-audio]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Runtime container-resource introspection: plain os.ReadFile(\"/sys/fs/cgroup/cpu.max\") + strings.Fields parse, zero new dependencies -- first OctoConv engine requiring this (the other three engine classes shell out to single-threaded-by-nature CLI tools)"
    - "Env-only-in-main + setter convention extended a third time (after SetAudioModelPath, SetVeraPDFTimeout): SetAudioThreads(n) single write before srv.Start(mux), no mutex, internal/convert package never reads os.Getenv directly"

key-files:
  created: [internal/convert/cgroup.go, internal/convert/cgroup_test.go]
  modified: [internal/convert/whisper.go, internal/convert/whisper_test.go, cmd/audio-worker/main.go, cmd/audio-worker/main_test.go, .env.example]

key-decisions:
  - "Floor (not ceil) quota/period in parseCPUMax -- rounding up would size --threads beyond the CFS quota the kernel actually grants, inviting throttling rather than avoiding it (PITFALLS.md Pitfall 5)"
  - "audioThreadCount() is a 2-tier resolver (package var > runtime.NumCPU()), not 3-tier like model() -- whisper-cli's -t has no per-Converter test-injection field equivalent; TestWhisperArgs exercises argv construction directly with an explicit threads param instead"

requirements-completed: [AUD-06, AUD-07]

# Metrics
duration: ~25min
completed: 2026-07-18
---

# Phase 32 Plan 02: whisper-cli Thread Sizing (cgroup CPU-limit detection) Summary

**whisper-cli's `--threads` is now sized to the container's real cgroup v2 CPU quota (floor of quota/period, never host core count), resolved once at `cmd/audio-worker` startup via an `AUDIO_THREADS` env override → `CgroupCPULimit()` → `runtime.NumCPU()` precedence chain, and always injected as an explicit `-t <n>` argv pair on every whisper-cli invocation.**

## Performance

- **Duration:** ~25 min
- **Started:** 2026-07-18T16:35Z (continuing from Plan 01)
- **Completed:** 2026-07-18T16:42Z
- **Tasks:** 3/3 completed
- **Files modified:** 7 (2 created, 5 modified)

## Accomplishments

- `internal/convert/cgroup.go`: new file, package-doc-commented per CLAUDE.md convention. `parseCPUMax(s string) (int, bool)` splits the cgroup v2 `cpu.max` two-field format, rejects `"max"` (unlimited) and malformed input, floors `quota/period`, clamps to a minimum of 1. `CgroupCPULimit()` reads `/sys/fs/cgroup/cpu.max` and fails open `(0, false)` on any read error (cgroup v1 host, non-container dev flow).
- `internal/convert/cgroup_test.go`: table-driven `TestCgroupCPULimit` covers every behavior-block case (2.0→2, 1.5→1, 0.5→1, unlimited→fallback, garbage→fallback, empty→fallback) plus `TestCgroupCPULimit_UnreadableFile` pinning the fail-open contract on whatever cgroup shape the test host actually has, without asserting a specific value.
- `internal/convert/whisper.go`: added `audioThreads` package var + `SetAudioThreads(n int)` setter (identical single-write-before-concurrent-reads contract as `audioModelPath`/`SetAudioModelPath`, no mutex) and `audioThreadCount()` 2-tier resolver (`audioThreads` when `> 0`, else `runtime.NumCPU()`). `whisperArgs` gained a `threads int` parameter and now always appends `"-t", strconv.Itoa(threads)` — present regardless of language/translate opts. `Convert`'s call site passes `audioThreadCount()`, mirroring how it already passes `c.model()`.
- `internal/convert/whisper_test.go`: `TestWhisperArgs` updated with a `threads` field on every case (asserting the `-t <n>` pair appears), plus a new `threads=1` case; new `TestSetAudioThreads_AudioThreadCount` pins the 2-tier resolver (restores `audioThreads` afterward to avoid bleeding into sibling tests, since it's process-wide package state).
- `cmd/audio-worker/main.go`: added `resolveAudioThreads() (int, string)` implementing `AUDIO_THREADS` (via existing `envInt` helper) → `convert.CgroupCPULimit()` → `runtime.NumCPU()`, called and wired via `convert.SetAudioThreads(threads)` immediately after the existing `SetAudioModelPath` call and before `srv.Start(mux)` — the same happens-before boundary already documented for the model path. The resolved thread count and its source (`"env override"` / `"cgroup"` / `"NumCPU fallback"`) are logged at startup (`🧵 audio threads=%d (source=%s)`), giving the Phase 32 RTF measurement and Phase 33 operator-visible evidence of what was actually used.
- `cmd/audio-worker/main_test.go`: `TestResolveAudioThreads` covers the env-override branch exactly (`AUDIO_THREADS=5` → `(5, "env override")`) and asserts fallthrough branch selection (not the live cgroup value, since the test host's cgroup shape is not controlled) for unset and `AUDIO_THREADS=0` cases, plus a sanity check on `runtime.NumCPU()`'s own stdlib contract.
- `.env.example`: documented `AUDIO_THREADS` immediately after `AUDIO_MODEL_PATH`, following its multi-line `#`-comment style — OPTIONAL, default unset → cgroup auto-detect with `NumCPU()` fallback, explicitly noting it should match the RTF measurement's thread count if set (Plan 03/04 own the actual measurement).

## Task Commits

Each task was committed atomically:

1. **Task 1: internal/convert/cgroup.go — CgroupCPULimit() with table-tested parse + fail-open fallback** - `de57d96` (feat)
2. **Task 2: whisper.go — SetAudioThreads setter + -t injection in whisperArgs; update TestWhisperArgs** - `9780dbe` (feat)
3. **Task 3: cmd/audio-worker main.go — AUDIO_THREADS resolution wiring + .env.example doc** - `c161d16` (feat)

## Files Created/Modified

- `internal/convert/cgroup.go` (created) - `parseCPUMax`/`CgroupCPULimit`, cgroup v2 `cpu.max` reader with fail-open fallback
- `internal/convert/cgroup_test.go` (created) - table-driven `TestCgroupCPULimit` + fail-open `TestCgroupCPULimit_UnreadableFile`
- `internal/convert/whisper.go` (modified) - `SetAudioThreads`/`audioThreadCount`, `whisperArgs` threads param + `-t` injection, `Convert` call-site update
- `internal/convert/whisper_test.go` (modified) - `TestWhisperArgs` threads param + assertions, new `TestSetAudioThreads_AudioThreadCount`
- `cmd/audio-worker/main.go` (modified) - `resolveAudioThreads`, wiring before `srv.Start(mux)`, startup log line
- `cmd/audio-worker/main_test.go` (modified) - `TestResolveAudioThreads` precedence/fallthrough test
- `.env.example` (modified) - `AUDIO_THREADS` doc block

## Decisions Made

- Floor (not ceil) `quota/period` in `parseCPUMax` — matches the plan's explicit instruction and PITFALLS.md Pitfall 5's oversubscription concern; a `--cpus=1.5` container gets `-t 1`, not `-t 2`.
- `audioThreadCount()` is 2-tier (package var → `runtime.NumCPU()`), not 3-tier like `model()`'s injected-field → package-var → default shape — there is no per-`AudioConverter` test-injection field for threads; `TestWhisperArgs` instead takes `threads` as an explicit function parameter, which is sufficient for argv-construction pinning without needing a converter-level override.
- `resolveAudioThreads`'s source label (`"env override"`/`"cgroup"`/`"NumCPU fallback"`) is returned as a plain string rather than a typed enum — consistent with the codebase's existing preference for untyped string constants over typed enums (`StatusQueued` etc.), and keeps the startup log line simple.

## Deviations from Plan

None — plan executed exactly as written. The 32-01-SUMMARY.md's live-confirmed `cpu.max` format (`"200000 100000"` under `--cpus=2.0`) matched `parseCPUMax`'s expected two-field format exactly, so no format-mismatch surprises arose. `whisper-cli --help`'s confirmed `-t N, --threads N` flag (also from 32-01) matched the `-t` short form used in `whisperArgs`.

## Issues Encountered

One incidental untracked build artifact: `go build ./cmd/audio-worker/...` (run without `-o`, per the plan's own verify command) writes an `audio-worker` binary to the repo root, which is not covered by `.gitignore` (`/bin/` only). Removed before the final commit (`rm -f audio-worker`) rather than committed or added to `.gitignore` — out of this plan's stated file-modification scope (`files_modified` in the frontmatter does not include `.gitignore`), and 32-03 runs concurrently on `scripts/` so touching shared repo config was avoided. Logged here rather than silently left dangling.

## User Setup Required

None — no external service configuration required. `AUDIO_THREADS` remains unset by default in `.env.example` (auto-detect), matching every other env var's documented default.

## Next Phase Readiness

- `whisper-cli` now always receives an explicit `-t <n>` sized to the real cgroup CPU quota — Plan 03's RTF measurement script can now run the real binary with a known, pinned thread count instead of an undocumented default, closing the loop this phase's own success criteria require (RTF is meaningless without a known thread count).
- `AUDIO_WORKER_CONCURRENCY` sizing (currently `.env.example`'s placeholder `2`, flagged "Phase 32 re-sizes from measured per-job RSS") is explicitly NOT touched by this plan — Plan 04 owns that value per this plan's own scope note.
- `AUDIO_ENGINE_TIMEOUT`'s `[ASSUMED] placeholder` value is also untouched — Plan 03/04's RTF measurement is the input that re-derives it.
- No blockers for Plan 03 (RTF measurement script, which can now pass `-t <cgroup-derived-n>` matching this plan's exact resolution chain).

## Self-Check: PASSED

- FOUND: internal/convert/cgroup.go
- FOUND: internal/convert/cgroup_test.go
- FOUND: internal/convert/whisper.go (modified)
- FOUND: internal/convert/whisper_test.go (modified)
- FOUND: cmd/audio-worker/main.go (modified)
- FOUND: cmd/audio-worker/main_test.go (modified)
- FOUND: .env.example (modified)
- FOUND: commit de57d96 (Task 1)
- FOUND: commit 9780dbe (Task 2)
- FOUND: commit c161d16 (Task 3)
- go build ./... : OK
- go vet ./... : OK
- go test ./internal/convert/... -run 'TestCgroupCPULimit|TestWhisperArgs' : PASS
- go test ./cmd/audio-worker/... : PASS
- gofmt -l (scoped files) : clean

---
*Phase: 32-containerization-local-e2e-rtf-gate*
*Completed: 2026-07-18*
