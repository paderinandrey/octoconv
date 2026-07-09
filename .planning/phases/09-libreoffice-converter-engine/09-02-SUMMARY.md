---
phase: 09-libreoffice-converter-engine
plan: 02
subsystem: infra
tags: [docker, libreoffice, soffice, tini, process-reaping, dockerfile]

# Dependency graph
requires:
  - phase: 09-libreoffice-converter-engine
    plan: 01
    provides: LibreOfficeConverter implementation + soffice-gated unit/live test suite (skipping until soffice is on PATH)
provides:
  - Worker runtime image (Dockerfile.worker) with LibreOffice + fonts installed, soffice on PATH, USER nobody preserved
  - Dockerfile.worker-test - repeatable Go+LibreOffice+procps image for live-running the internal/convert soffice-gated tests
  - Live-executed (not skipped) proof of DOC-06's zero-survivors process-group-kill guarantee
  - tini as PID 1 in both images, fixing a zombie-process reaping gap discovered by the live DOC-06 test
affects: [10-worker-reconciler-integration, 11-api-routing-e2e]

# Tech tracking
tech-stack:
  added:
    - "tini (apt package, debian bookworm) - minimal PID-1 init/reaper"
  patterns:
    - "Dockerfile ENTRYPOINT wraps the real entrypoint with tini (`[\"/usr/bin/tini\", \"--\", ...]`) whenever the container spawns processes that may fork children (LibreOffice's oosplash->soffice.bin) and rely on a hardened group-kill for timeout enforcement"

key-files:
  created:
    - Dockerfile.worker-test
  modified:
    - Dockerfile.worker

key-decisions:
  - "Added tini as PID 1 to both Dockerfile.worker and Dockerfile.worker-test after the live DOC-06 test revealed a real zombie-process leak: SIGKILL to the process group DOES terminate soffice.bin (confirmed via ps STAT=Z), but without a reaper at PID 1, the orphaned zombie is never waited on and persists indefinitely — a genuine correctness gap for a long-running worker container that will time out conversions repeatedly over its uptime"
  - "Scoped the tini fix to the two Dockerfiles already in this plan's declared file scope (Rule 1/2 auto-fix: minimal, well-established containerization idiom, not an architectural change) rather than adding a Go-level SIGCHLD reaper in cmd/worker/main.go, which would have expanded the plan's declared files_modified"
  - "Logged an unrelated pre-existing gofmt issue in internal/queue/queue_test.go (from Phase 6, commit 6af87c1) to deferred-items.md rather than fixing it - out of this task's scope per Scope Boundary rules"

patterns-established:
  - "Any Dockerfile ENTRYPOINT for a container that shells out to a process capable of forking children (not just execing) and relies on group-SIGKILL-on-timeout must run under a proper PID-1 init/reaper (tini) or orphaned children become permanent zombies"

requirements-completed: [DOC-04, DOC-05, DOC-06]

# Metrics
duration: 13min
completed: 2026-07-09
---

# Phase 09 Plan 02: LibreOffice Converter Engine - Docker Provisioning & Live Verification Summary

**Provisioned LibreOffice + fonts into Dockerfile.worker, built a repeatable Dockerfile.worker-test harness, and proved DOC-06's zero-survivors process-group-kill guarantee via a live-executed (not skipped) integration test — discovering and fixing a real zombie-process reaping gap (tini added as PID 1) along the way.**

## Performance

- **Duration:** 13 min
- **Started:** 2026-07-09T11:07:58Z
- **Completed:** 2026-07-09T11:21:07Z
- **Tasks:** 2
- **Files modified:** 3 (1 created, 2 modified across both task commits; deferred-items.md also created)

## Accomplishments

- `Dockerfile.worker`'s runtime stage now installs `libreoffice-writer-nogui`, `libreoffice-calc-nogui`, `libreoffice-impress-nogui`, and the three font packages (`fonts-crosextra-carlito`, `fonts-crosextra-caladea`, `fonts-liberation2`) on the existing single `RUN apt-get` line, so `soffice` is on `PATH` at runtime; `USER nobody` and the two-stage `CGO_ENABLED=0` build are preserved unchanged
- New `Dockerfile.worker-test`: `golang:1.26-bookworm` + the same LibreOffice/font packages + `libvips-tools` + `procps` (for the `ps`-based survivor check), a dedicated, repeatable, committed test harness that runs `go test ./internal/convert/ -v` — explicitly documented as not a deployment artifact
- Both images build successfully via `docker build`
- The two soffice-gated tests authored in plan 09-01 (`TestLibreOfficeConverter_TimeoutKillsRealProcess`, `TestLibreOfficeConverter_ConvertProducesValidPDF`) were executed live (not skipped) inside `octoconv-lo-test:phase9` and both PASS, proving DOC-06 (zero surviving `soffice`/`soffice.bin`/`oosplash` after a timeout kill) and DOC-04/DOC-05 (real docx-derived odt -> valid `%PDF-` output) live against a real installed LibreOffice `4:7.4.7-1+deb12u13`
- Full `./internal/convert/` suite (62 tests) passes live inside the image, exit 0
- **Discovered and fixed a real bug**: the first live run of the kill test failed — `ps` showed a surviving `soffice.bin` process after the SIGKILL. Root-caused to a container PID-1 reaping gap (see Deviations below), fixed by adding `tini` as PID 1 in both Dockerfiles; re-verified PASS across multiple repeated runs afterward

## DOC-06 Evidence (live test output, captured after the tini fix)

```
=== RUN   TestLibreOfficeConverter_TimeoutKillsRealProcess
--- PASS: TestLibreOfficeConverter_TimeoutKillsRealProcess (0.11s)
=== RUN   TestLibreOfficeConverter_ConvertProducesValidPDF
--- PASS: TestLibreOfficeConverter_ConvertProducesValidPDF (1.19s)
PASS
ok  	github.com/apaderin/octoconv/internal/convert	1.302s
```

No `--- SKIP:` lines for either test (soffice was present and both tests actually ran). Full-suite run (`go test ./internal/convert/ -v`, all 62 tests) also exits 0.

## Task Commits

Each task was committed atomically:

1. **Task 1: Add LibreOffice to Dockerfile.worker and create Dockerfile.worker-test** - `41085ba` (feat)
2. **Task 2 deviation fix: Add tini as PID 1 to reap orphaned soffice.bin zombies** - `6f60c49` (fix) — discovered and resolved during Task 2's live verification; Task 2 itself is verification-only (no separate commit needed beyond this fix and this SUMMARY)

## Files Created/Modified

- `Dockerfile.worker` - LibreOffice + font packages added to the single `apt-get install` line; `tini` added and wired as `ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/worker"]`
- `Dockerfile.worker-test` (new) - Go + LibreOffice + procps + tini test harness image, `ENTRYPOINT ["/usr/bin/tini", "--"]` / `CMD ["go", "test", "./internal/convert/", "-v"]`
- `.planning/phases/09-libreoffice-converter-engine/deferred-items.md` (new) - logs an unrelated pre-existing `gofmt` issue in `internal/queue/queue_test.go`

## Decisions Made

- Followed RESEARCH.md's exact package set for both Dockerfiles (live-verified 2026-07-09 against `debian:bookworm-slim`, version `4:7.4.7-1+deb12u13`)
- Did not add `HOME`/fontconfig provisioning to the Dockerfile (per RESEARCH.md Pitfall 3 and CONTEXT.md's explicit guidance — degrades gracefully, not a Dockerfile-level correctness fix)
- Added `tini` as PID 1 in both images (see Deviations) rather than a Go-level SIGCHLD reaper, keeping the fix within this plan's declared Dockerfile-only file scope

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1/2 - Bug / Missing critical functionality] Zombie soffice.bin process after group SIGKILL — no PID-1 reaper**

- **Found during:** Task 2's first live run of `TestLibreOfficeConverter_TimeoutKillsRealProcess` inside `octoconv-lo-test:phase9`
- **Issue:** The test failed 3/3 consecutive runs with `surviving LibreOffice process after timeout: <pid> soffice.bin`. Root-caused via an instrumented reproduction (`ps -eo pid,ppid,pgid,stat,comm` snapshots before/after kill): the SIGKILL to the negative process group DOES successfully terminate `soffice.bin` (confirmed by `STAT=Z`, i.e. genuinely dead, not running) — but because `soffice.bin`'s direct parent (`oosplash`) is killed simultaneously, the kernel reparents `soffice.bin` to the container's PID 1. Neither the `go test` driver process nor (in the real worker image) the compiled `worker` binary calls `wait()`/`wait4()` for arbitrary orphaned grandchildren, so the zombie is never reaped and persists indefinitely, still visible in `ps`. A manual shell reproduction (bash as PID 1, with its own job-control reaping) did NOT reproduce the survivor after a 1s settle delay, confirming the gap is specifically "no init/reaper at container PID 1", not a flaw in `runCommand`'s Setpgid+SIGKILL mechanism itself.
- **Fix:** Added the `tini` apt package to both `Dockerfile.worker` and `Dockerfile.worker-test`'s existing single `RUN apt-get` line, and changed each image's entrypoint to run under `tini` as PID 1 (`ENTRYPOINT ["/usr/bin/tini", "--", ...]`), so `tini`'s reaping loop collects orphaned grandchildren after a group kill.
- **Files modified:** `Dockerfile.worker`, `Dockerfile.worker-test`
- **Verification:** Rebuilt both images; re-ran the kill test 3 consecutive times (all PASS, 0.03s-0.12s each) plus the full `./internal/convert/` suite (62 tests, exit 0)
- **Commit:** `6f60c49`
- **Note:** This is a real, previously-undiscovered operational gap in the worker container generally (not specific to this plan's new LibreOffice packages) — without an init/reaper, any timed-out conversion whose engine forks a child (LibreOffice's `oosplash`->`soffice.bin` topology; libvips does not fork, which is why this was never observed before) leaves a permanent zombie process-table entry. Over a long-running worker's uptime with repeated engine timeouts, this would accumulate zombie entries (a slow PID-table leak, not an active-resource leak since zombies consume negligible resources). The `tini` fix closes this for both the runtime and test images.

## Out-of-Scope Discoveries (logged, not fixed)

- `internal/queue/queue_test.go` is not `gofmt`-clean (pre-existing since Phase 6 commit `6af87c1`, unrelated to this plan's Dockerfile changes) — logged to `.planning/phases/09-libreoffice-converter-engine/deferred-items.md` per Scope Boundary rules.

## Issues Encountered

None beyond the auto-fixed zombie-reaping deviation documented above, which was resolved within the fix-attempt budget (1 root-cause investigation + 1 fix + re-verification).

## User Setup Required

None — Docker was available throughout (OrbStack Engine 29.4.0) and both images built/ran successfully without any external configuration.

## Next Phase Readiness

- `Dockerfile.worker` now ships a working `soffice` on `PATH` under `USER nobody`, with `tini` as PID 1 ensuring correct process reaping for any future forking-engine timeout.
- `Dockerfile.worker-test` is a committed, repeatable artifact any future phase can reuse to re-run the live LibreOffice-gated test suite (e.g., as a regression guard if the base image or LibreOffice version ever changes, per RESEARCH.md's own stated re-verification rationale).
- DOC-04/DOC-05/DOC-06 are now proven live, not just implemented — Phase 10 can proceed to wire `LibreOfficeConverter` into the actual document queue/worker routing with high confidence in the underlying process-safety guarantee.
- No blockers.

---
*Phase: 09-libreoffice-converter-engine*
*Completed: 2026-07-09*
