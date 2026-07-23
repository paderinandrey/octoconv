---
phase: 36-containerization-rtf-measured-timeout
plan: 04
subsystem: infra
tags: [ffmpeg, rtf-measurement, docker-compose, timeout-derivation, go-no-go, av-engine]

# Dependency graph
requires:
  - phase: 36-01
    provides: "AV_MAX_DURATION_SECONDS/AV_DISK_SAFETY_FACTOR env wiring, cmd/av-worker/main.go"
  - phase: 36-02
    provides: "Dockerfile.av-worker (from-source ffmpeg n8.1.2) + scripts/av-rtf-measure.sh RTF matrix"
  - phase: 36-03
    provides: "av-worker compose service + IN-02 AV_ENGINE_TIMEOUT/AV_MAX_RETRY parity across all 8 queue-client services (provisional 600s)"
provides:
  - "Operator-measured, RTF-derived AV_ENGINE_TIMEOUT=753s finalized identically across all 8 queue-client services"
  - "Path B NO-GO lever applied: AV_MAX_DURATION_SECONDS lowered 14400s -> 90s"
  - "disposition (b) passthrough bound: internal/convert/av.go rejects any resolution_height==0 re-encode against a source taller than 1080p (fail-closed), closing the hevc@2160p OOM-KILL DoS vector"
  - "AV_WORKER_CONCURRENCY=1, memory=1g validated from measured peak-RSS/CPU-saturation data"
affects: ["37 (or later)", "any future av-engine timeout/duration re-derivation", "operator-run live E2E follow-up"]

tech-stack:
  added: []
  patterns:
    - "Passthrough-bound guard (enforceNoScalePassthroughBound): scoped to the re-encode branch only, never the stream-copy remux branch, mirrors enforceMaxResolutionOf/enforceMaxDurationOf's fail-closed-before-expensive-work shape"
    - "Two-commit separation for NO-GO lever code changes (Pitfall 2): code change (av.go) and value finalization (compose/.env.example) are always separate commits"

key-files:
  created: []
  modified:
    - internal/convert/av.go
    - internal/convert/av_test.go
    - docker-compose.yml
    - .env.example

key-decisions:
  - "Worst-case cell is hevc@1080 (p95_RTF=4.179133s), NOT VP9 as 36-RESEARCH.md's D-09 assumed -- HEVC dominates VP9 by 1.86x at every measured resolution"
  - "NO-GO lever = Path B (lower AV_MAX_DURATION_SECONDS to 90s); Path A (VP9 argv tuning) rejected as ineffective because it cannot touch the HEVC/mp4 path that dominates"
  - "Passthrough residual disposition = (b) bound the path: resolution_height==0 re-encode requests are fail-closed rejected when source height exceeds 1080p, rather than (a) folding an unmeasurable fixture-sized number into the timeout or (c) accepting the gap as documented residual risk"
  - "AV_WORKER_CONCURRENCY=1 (threads=2 saturates cpus=2.0/job, mirrors AUDIO_WORKER_CONCURRENCY's provenance); memory=1g validated (every bounded cell ran clean, OOM only on the now-bounded 2160p no-scale path)"

requirements-completed: [AVE-04]

duration: 25min
completed: 2026-07-23
---

# Phase 36 Plan 04: RTF-Measured Go/No-Go Gate Summary

**AV_ENGINE_TIMEOUT finalized at 753s (Path B lever, AV_MAX_DURATION_SECONDS 14400s->90s) from a measured hevc@1080 worst case (4.179133s p95 RTF, contradicting the plan's VP9-dominance assumption), plus a new fail-closed guard bounding the unbounded no-scale passthrough path to <=1080p to close a live hevc@2160p OOM-DoS vector the measurement discovered.**

## Performance

- **Duration:** ~25 min (Task 3 only; Tasks 1-2 were operator-run at the Docker daemon in a prior session)
- **Started:** 2026-07-23T01:00:00Z (approx, Task 3 start)
- **Completed:** 2026-07-23T01:22:00Z
- **Tasks:** 1 of 3 (Task 3; Tasks 1-2 are supervised checkpoints already completed by the operator)
- **Files modified:** 4

## Accomplishments

- Applied the operator's locked go/no-go decision: Path B NO-GO lever (`AV_MAX_DURATION_SECONDS` 14400s -> 90s), deriving `AV_ENGINE_TIMEOUT = ceil(90 x 4.179133 x 2.0) = 753s` (16.3% margin under the 900s/15m reconciler cap)
- Wrote `AV_ENGINE_TIMEOUT=753s` identically into all 8 queue-client services (parity verified: grep-count==8, single unique value)
- Implemented disposition (b) "bound the path": a new fail-closed guard (`enforceNoScalePassthroughBound`) in `internal/convert/av.go` rejects any `resolution_height==0` re-encode request whose probed source height exceeds 1080p — closing the hevc@2160p OOM-DoS vector the RTF measurement discovered, in a commit fully separate from the value-finalization commit (Pitfall 2)
- Finalized `AV_WORKER_CONCURRENCY=1` and confirmed `memory=1g` from the measured picture (mirrors `AUDIO_WORKER_CONCURRENCY`'s provenance)
- Updated `stop_grace_period` to 773s (753s + 20s margin, mirrors audio-worker's exact convention)
- Documented the full derivation, the HEVC-vs-VP9 contradiction, and the passthrough-bound disposition in `.env.example` and `docker-compose.yml` comments so the residual is not silently unaddressed

## Task Commits

Task 3 was split into the two commits the plan explicitly requires (Pitfall 2 separation):

1. **Commit 1 (code, disposition (b)):** `e94ff4e` — `fix(36-04): bound resolution_height==0 passthrough to <=1080p (AVE-04)`
   - `internal/convert/av.go`: new `avNoScalePassthroughMaxHeight` (1080) const, `ErrAVNoScalePassthroughExceeded` sentinel, `enforceNoScalePassthroughBound` guard, wired into `convertTranscode`'s re-encode branch only (stream-copy remux stays exempt)
   - `internal/convert/av_test.go`: `TestConvertTranscode_NoScalePassthroughBound` (4 subtests: rejects >1080p no-scale, allows exactly-1080p, explicit resolution_height bypasses the bound, stream-copy remux is exempt) + `TestConvertTranscode_NoScaleBound_PreservesAVE02Flags` (re-asserts `-nostdin`/`-protocol_whitelist file,crypto`/`file:` prefixes survive on the affected argv builders)
   - Touches NO env/compose file

2. **Commit 2 (config, value finalization):** `b208634` — `docs(36-04): finalize RTF-measured AV_ENGINE_TIMEOUT=753s (AVE-04)`
   - `docker-compose.yml`: `AV_ENGINE_TIMEOUT` 600s->753s across all 8 services; av-worker `AV_MAX_DURATION_SECONDS` 14400->90, `AV_WORKER_CONCURRENCY` 2->1, `stop_grace_period` 620s->773s, memory comment updated (validated, not provisional)
   - `.env.example`: `AV_ENGINE_TIMEOUT`, `AV_MAX_DURATION_SECONDS`, `AV_WORKER_CONCURRENCY` finalized with a Phase-32-style derivation comment (formula, p95, worst cell, N, cpus/memory, margin) plus the passthrough-bound disposition and hevc@2160p OOM finding documented alongside

**Plan metadata:** (this commit, docs: complete 36-04 plan — to follow after STATE/ROADMAP/REQUIREMENTS updates)

## Files Created/Modified

- `internal/convert/av.go` — new fail-closed no-scale passthrough bound (disposition (b))
- `internal/convert/av_test.go` — 2 new test functions covering the bound + AVE-02 re-assertion
- `docker-compose.yml` — 8x `AV_ENGINE_TIMEOUT: "753s"` parity + av-worker block finalization
- `.env.example` — `AV_ENGINE_TIMEOUT`/`AV_MAX_DURATION_SECONDS`/`AV_WORKER_CONCURRENCY` finalized with derivation comments

## Measured RTF Matrix (Task 1, operator-run, already complete)

N=10, threads=2, `--cpus=2.0`/`--memory=1g`, arm64, pinned ffmpeg n8.1.2, production argv (`transcodeToMP4Args`/`transcodeToWebMArgs`). All bounded (enum) cells exited 0 cleanly.

p95 RTF:

| codec | 480 | 720 | 1080 | passthrough@2160 |
|---|---|---|---|---|
| h264 | 0.073633 | 0.157200 | 0.262900 | (not measured; ~30x faster, never worst) |
| vp9  | 0.705633 | 1.247333 | 2.253267 | 9.267067 (clean) |
| hevc | 1.017967 | 2.081367 | 4.179133 | OOM-KILLED at 1g (exit 137) — did not complete |

**Worst BOUNDED cell = hevc@1080 = 4.179133** (contradicts the plan's D-09 VP9-dominance assumption; HEVC dominates VP9 by 1.86x). Passthrough is unmeasurable for HEVC (OOM at the compose 1g limit) — a real memory-safety DoS signal, not just slowness.

## Decisions Made (Task 2, operator-locked, applied this task)

1. **NO-GO lever = Path B** (lower `AV_MAX_DURATION_SECONDS`). Path A (VP9 tuning of `transcodeToWebMArgs`) was explicitly rejected as ineffective — it cannot touch the HEVC/mp4 path that dominates. `AV_MAX_DURATION_SECONDS=90`; `AV_ENGINE_TIMEOUT = ceil(90 x 4.179133 x 2.0) = 753s` (16.3% margin under the 900s cap).
2. **Passthrough disposition = (b) bound the path.** `resolution_height==0` (no-scale) is now bounded to the enum ceiling of 1080p: a re-encode against a source taller than 1080p is fail-closed rejected (`ErrAVNoScalePassthroughExceeded`). Stream-copy remux is exempt (no decode/encode, no measured risk). AVE-02 flags (`-nostdin -protocol_whitelist file,crypto`, `file:` prefixes) are untouched and re-asserted by tests.
3. **Memory = 1g** (validated: every bounded cell ran clean at 1g; the only OOM was the now-bounded 2160p no-scale path). `AV_WORKER_CONCURRENCY=1` (threads=2 saturates 2 CPUs/job at `cpus=2.0`, mirrors `AUDIO_WORKER_CONCURRENCY`'s exact provenance/rationale).

## Deviations from Plan

### Auto-fixed / Applied Issues

**1. [Task-3 scope: applying operator disposition (b), not a plan deviation per se] Added a new fail-closed guard beyond the plan's literal Task-3 wording**

- **Found during:** Applying operator decision #2 (passthrough disposition)
- **Issue:** The plan's Task 3 `<action>` text describes only Path A/Path B timeout-value work; the passthrough-residual-disposition checkpoint (Task 2) required disposition (b) to be *implemented*, which necessarily means new production code (`internal/convert/av.go`), not just a config/comment change
- **Fix:** Implemented `enforceNoScalePassthroughBound` scoped precisely to the re-encode branch of `convertTranscode`, exempting stream-copy remux (no decode/encode, no measured OOM/RTF risk) — matches the existing guard idiom (`enforceMaxResolutionOf`/`enforceMaxDurationOf`, fail-closed before expensive ffmpeg work)
- **Files modified:** `internal/convert/av.go`, `internal/convert/av_test.go`
- **Verification:** `go test ./internal/convert/` green (4 new subtests + 1 AVE-02 re-assertion test); AVE-02 `protocol_whitelist` grep-count unchanged (3, vs HEAD~2)
- **Committed in:** `e94ff4e` (separate from the value-finalization commit, per Pitfall 2)

**2. [Environment constraint, not a code defect] `go build ./...` transiently failed under parallel linking due to near-full local disk (only ~1-3.8Gi free on a 460Gi volume)**

- **Found during:** Post-commit verification
- **Issue:** `strip`/dwarf-combining ran out of scratch space when building all ~12 `cmd/*` binaries in parallel (`go build ./...` default parallelism)
- **Fix:** Ran `go clean -cache` to reclaim ~2.9G, then verified with `go build -p=1 ./...` (sequential linking) — passed cleanly; also individually verified all 12 `cmd/*` binaries build with `-o /dev/null`, and `go vet ./...` passed unconditionally (no linking required)
- **Files modified:** none (build-cache-only, no repo changes)
- **Verification:** `go build -p=1 ./...` = exit 0; `go vet ./...` = exit 0; `go test ./internal/convert/` = ok
- **Committed in:** N/A (not a code change)

---

**Total deviations:** 2 (1 disposition-application beyond literal Task-3 text; 1 environment disk-space constraint, resolved without code changes)
**Impact on plan:** The passthrough-bound code is directly required by the operator's own locked decision (Task 2, decision #2) — not scope creep, but the literal implementation of a decision the plan's checkpoint explicitly gates on. The disk-space issue was purely local-environment and did not affect the plan's actual deliverables.

## Issues Encountered

None beyond the disk-space transient noted above (resolved).

## Live E2E Evidence: PENDING (operator-run)

Per D-05 (SUPERVISED), the live compose E2E is NOT run by this executor. The following is the precise runbook for the operator/orchestrator to execute and then fill in this section with the actual evidence (job id, terminal status, output size, webhook confirmation).

### Runbook

1. **Ensure no k8s cluster is live** (OrbStack wedge risk, D-05): `kubectl cluster-info` should fail/be absent before starting compose.
2. **Bring up the stack** (av-worker + its dependencies; the webhook-worker sweeper covers the reconciler):
   ```bash
   docker compose up -d --build postgres redis minio createbucket api av-worker webhook-worker-1
   ```
3. **Confirm av-worker started cleanly** (no OOM/crash-loop at the finalized `memory: 1g` / `AV_WORKER_CONCURRENCY: 1`):
   ```bash
   docker compose logs --tail=50 av-worker
   docker compose ps av-worker
   ```
4. **Upload a real video job** through the API (mirrors the Phase 35 07-plan live E2E checkpoint) — a small legal mp4/mov/mkv/webm source, targeting `mp4` or `webm`, using a real API key from the `clients` table:
   ```bash
   curl -sS -X POST http://localhost:8090/v1/jobs \
     -H "Authorization: Bearer <API_KEY>" \
     -F "file=@/path/to/real-sample.mp4" \
     -F "target=webm"
   ```
   Capture the returned `job_id`.
5. **Poll to terminal status** (should reach `done` well within the finalized 753s `AV_ENGINE_TIMEOUT`, and definitely within the new 90s `AV_MAX_DURATION_SECONDS` duration ceiling — use a source under 90s):
   ```bash
   curl -sS http://localhost:8090/v1/jobs/<job_id> -H "Authorization: Bearer <API_KEY>"
   ```
   Repeat until `status` is `done` (or `failed` — investigate if so).
6. **Download the presigned result:**
   ```bash
   curl -sS http://localhost:8090/v1/jobs/<job_id>/result -H "Authorization: Bearer <API_KEY>"
   # then curl the returned presigned URL to fetch the actual bytes, record size/checksum
   ```
7. **Confirm the signed webhook fired** (webhook-worker delivery log or the configured receiver endpoint) — capture the delivery timestamp and signature-verification result.
8. **Record evidence** in this section: job id, terminal status, output file size/checksum, webhook delivery confirmation, and total wall-clock time observed (sanity-check against the 753s timeout / 90s duration ceiling).

### Evidence (to be filled in by the operator/orchestrator)

- Job ID: _pending_
- Terminal status: _pending_
- Output size/checksum: _pending_
- Signed webhook confirmed: _pending_
- Observed wall-clock time: _pending_

## Next Phase Readiness

- All static verification is green: `go test ./internal/convert/`, `go build ./...` (sequential), `go vet ./...`, `docker compose config`, AV_ENGINE_TIMEOUT parity (8/8, single value), AVE-02 flag-count non-regression.
- The RTF-derived `AV_ENGINE_TIMEOUT=753s` and the passthrough-bound fix are both committed and ready for the live E2E.
- **Blocker for phase completion:** the live compose E2E (upload -> poll -> presigned download + signed webhook) has NOT been run — this is the one remaining gate before `36-04` (and Phase 36 overall) can be marked fully complete. See the runbook above.

---
*Phase: 36-containerization-rtf-measured-timeout*
*Plan: 04*
*Completed (static verification): 2026-07-23*
*Live E2E: PENDING*

## Self-Check: PASSED

- FOUND: internal/convert/av.go
- FOUND: internal/convert/av_test.go
- FOUND: docker-compose.yml
- FOUND: .env.example
- FOUND: .planning/phases/36-containerization-rtf-measured-timeout/36-04-SUMMARY.md
- FOUND commit: e94ff4e (fix(36-04): bound resolution_height==0 passthrough to <=1080p)
- FOUND commit: b208634 (docs(36-04): finalize RTF-measured AV_ENGINE_TIMEOUT=753s)
