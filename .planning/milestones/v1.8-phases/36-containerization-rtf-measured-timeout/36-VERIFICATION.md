---
phase: 36-containerization-rtf-measured-timeout
verified: 2026-07-23T13:45:00Z
status: passed
score: 12/12 must-haves verified
overrides_applied: 0
---

# Phase 36: Containerization & RTF-Measured Timeout Verification Report

**Phase Goal:** A running av-worker container in docker-compose passes a full live E2E, with `AV_ENGINE_TIMEOUT` sized from a measured RTF matrix across the closed opts space rather than a copied or guessed constant.
**Verified:** 2026-07-23T13:45:00Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Fail-closed disk-space guard (D-06) exists and pins 5 behaviors | VERIFIED | `internal/convert/avdiskguard.go` (`EnforceMinFreeDisk`, `ErrAVInsufficientDiskSpace`); `go test ./internal/convert/ -run TestEnforceMinFreeDisk -v` → 5/5 subtests pass live |
| 2 | AVConverter duration/resolution ceilings are zero-value-defaulting struct fields; bare `AVConverter{}` unchanged at 4h/4320 | VERIFIED | `internal/convert/av.go:56-57` fields, `:512-518` resolver against `avMaxSourceDuration`/`avMaxSourceResolutionHeight` consts; full `go test ./internal/convert/` passes |
| 3 | cmd/av-worker reads `AV_MAX_DURATION_SECONDS`/`AV_DISK_SAFETY_FACTOR` and re-registers a configured converter before serving | VERIFIED | `cmd/av-worker/main.go:66-80`; live container log line `🧵 AV_MAX_DURATION_SECONDS=1m30s AV_DISK_SAFETY_FACTOR=3.00 threads=2 (source=cgroup)` observed from the actually-running `octoconv-av-worker` container |
| 4 | `Dockerfile.av-worker` builds ffmpeg from PINNED SOURCE (n8.1.2, peeled commit `38b88335f99e76ed89ff3c93f877fdefce736c13`), not apt, with a fail-loud rev-parse guard + runtime version assertion | VERIFIED | Dockerfile inspected (ARG/checkout/rev-parse-equality guard, `ffmpeg -version` grep-assert); **live-built image on this host** (`octoconv-av-worker:phase36`) run directly: `ffmpeg -version` → `ffmpeg version n8.1.2`, configure string confirms `--disable-everything`/`--enable-protocol=file,crypto`/no `--cpu=host` |
| 5 | RTF matrix script sweeps VP9 + HEVC + a no-scale passthrough cell, gates measurement integrity only | VERIFIED | `scripts/av-rtf-measure.sh` executable, `bash -n` valid; grep confirms vp9(6)/hevc(6)/protocol_whitelist(2)/passthrough(4)/kubectl cluster-info(1) all present |
| 6 | av-worker compose service + IN-02 `AV_ENGINE_TIMEOUT`/`AV_MAX_RETRY` parity across all 8 `queue.NewClient()` services | VERIFIED | `grep -c "AV_ENGINE_TIMEOUT:" docker-compose.yml` = 8, `sort -u` = 1 unique value (`"753s"`); `docker compose config -q` validates |
| 7 | CI bakes av-worker in both docker-build and e2e jobs, no platform pin | VERIFIED | `.github/workflows/ci.yml`: `av-worker.cache-to` ×1, `av-worker.cache-from` ×2 |
| 8 | `.env.example` documents `AV_MAX_DURATION_SECONDS`/`AV_DISK_SAFETY_FACTOR` with the Phase-32-style derivation comment, passthrough-bound disposition, and the hevc@2160p OOM finding | VERIFIED | `.env.example:88-95` — full derivation formula, worst-cell p95, disposition (b) wording, OOM-KILL note all present |
| 9 | `AV_ENGINE_TIMEOUT` is derived from the measured worst-case RTF cell (not assumed), and the NO-GO lever (Path B) is applied and documented like Phase 32 | VERIFIED | `36-04-SUMMARY.md` measured matrix (N=10, hevc@1080 p95=4.179133, worst BOUNDED cell — contradicting D-09's VP9 assumption) + `docker-compose.yml`/`.env.example` comments recording `ceil(90 × 4.179133 × 2.0) = 753s` |
| 10 | `AV_ENGINE_TIMEOUT` (753s) stays strictly under the 900s `RECONCILER_ACTIVE_STALE_AFTER` cap; cap itself unchanged | VERIFIED | `753 < 900`; `RECONCILER_ACTIVE_STALE_AFTER: "15m"` unchanged at both existing sites |
| 11 | Passthrough residual disposition (b) "bound the path" is implemented as a fail-closed guard on the re-encode branch only, tested, and AVE-02 flags are unchanged | VERIFIED | `internal/convert/av.go` `enforceNoScalePassthroughBound`/`ErrAVNoScalePassthroughExceeded`/`avNoScalePassthroughMaxHeight=1080`, wired in `convertTranscode`'s re-encode branch (stream-copy exempt); `TestConvertTranscode_NoScalePassthroughBound` (4 subtests) + `TestConvertTranscode_NoScaleBound_PreservesAVE02Flags` both pass; `grep -v '^\s*//' av.go \| grep -c protocol_whitelist` = 3 (unchanged) |
| 12 | A full live compose E2E (upload → poll → presigned download) passes against the running av-worker container | VERIFIED (independently re-confirmed, not just SUMMARY narrative) | See "Independent Live E2E Re-Verification" below |

**Score:** 12/12 truths verified

**Accepted deviation (per explicit phase context, not scored as a gap):** SC1's "signed webhook confirmed" clause was not exercised by the operator's E2E job (job carried no `callback_url`; `webhook_deliveries` correctly holds 0 rows for that job). Signed-webhook delivery itself is engine-agnostic machinery already verified in Phase 2 and Phase 16, and this was an explicit, informed operator decision at E2E time, not an oversight. Per the phase-launch instructions this is not treated as a FAILED must-have.

### Independent Live E2E Re-Verification

The verifier did not rely on `36-04-SUMMARY.md`'s narrative alone. The live compose stack was still running on this host and was independently inspected:

- `docker ps`: `octoconv-api`, `octoconv-av-worker`, `octoconv-webhook-worker-1`, `octoconv-redis`, `octoconv-db`, `octoconv-minio` all `Up`.
- `docker logs octoconv-av-worker`: `🧵 AV_MAX_DURATION_SECONDS=1m30s AV_DISK_SAFETY_FACTOR=3.00 threads=2 (source=cgroup)` — matches the finalized 90s/753s config, not the provisional 600s/14400s placeholder.
- Postgres query directly against `octoconv-db`: `SELECT id, status, source_format, target_format, engine FROM jobs WHERE id='846357a3-6799-4359-83ea-1bb6bea99bc2'` → `status=done, source_format=mp4, target_format=webm, engine=av`.
- `job_outputs` row for that job: `object_key=results/846357a3-6799-4359-83ea-1bb6bea99bc2/0-out.webm, size_bytes=608065` — matches the SUMMARY exactly.
- **Direct S3 `GetObject` fetch** (via a small Go program using `minio-go`, bypassing both the API and MinIO's raw on-disk bitrot-chunked format) against `127.0.0.1:9100` bucket `octoconv`: first 4 bytes = `1a45dfa3` (EBML/WebM magic), total size = `608065` bytes, `sha256 = eea7c3e570198b9b26e836ed1582eabb2eb3f76ec9ccdd677ebb9f37cbb8ae7f` — **byte-for-byte identical** to the SUMMARY's claimed evidence.
- `docker inspect octoconv-av-worker`: container image built ~26 minutes prior to this verification, i.e. from the finalized post-`b208634` code, not a stale pre-finalization image.

This is independent, reproducible, cryptographic-hash-level confirmation that the claimed live E2E genuinely occurred and used the finalized (not provisional) configuration — not merely a SUMMARY claim.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/avdiskguard.go` | `EnforceMinFreeDisk` + sentinel | VERIFIED | exists, `unix.Statfs` used, tests pass |
| `internal/convert/av.go` | struct fields + disk-guard call site + passthrough bound | VERIFIED | all present, ordered guard-before-dispatch |
| `cmd/av-worker/main.go` | env wiring | VERIFIED | `AV_MAX_DURATION_SECONDS`/`AV_DISK_SAFETY_FACTOR` read, registered before `srv.Start` |
| `Dockerfile.av-worker` | 3-stage pinned-source build | VERIFIED | builds and runs live on this host, reports `n8.1.2` |
| `scripts/av-rtf-measure.sh` | RTF matrix gate | VERIFIED | executable, syntactically valid, VP9+HEVC+passthrough present |
| `docker-compose.yml` | av-worker service + 8-way IN-02 parity | VERIFIED | `docker compose config -q` validates; grep counts match |
| `.github/workflows/ci.yml` | av-worker bake | VERIFIED | cache-to ×1, cache-from ×2 |
| `.env.example` | AV_* docs + derivation | VERIFIED | derivation comment + disposition + OOM finding present |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| `cmd/av-worker/main.go` | `convert.Default.Register(convert.AVConverter{...})` | startup re-registration | WIRED | before `srv.Start(mux)`, confirmed by line order and live log output |
| `internal/convert/av.go Convert` | `EnforceMinFreeDisk` | guard call after duration/resolution, before dispatch | WIRED | single call site, correct order |
| `docker-compose.yml av-worker` | `Dockerfile.av-worker` | `build.dockerfile` | WIRED | `dockerfile: Dockerfile.av-worker` present, image actually built and running |
| all 8 queue-client services | `AVUniqueTTL` derivation | identical `AV_ENGINE_TIMEOUT`/`AV_MAX_RETRY` | WIRED | grep-count 8/8, single unique value each |
| `scripts/av-rtf-measure.sh` output | `AV_ENGINE_TIMEOUT` | `ceil(max_duration × p95_worstcell × 2.0)` | WIRED | 753s recorded in compose/.env matches the documented formula against the measured hevc@1080 p95 |
| `convertTranscode` re-encode branch | `enforceNoScalePassthroughBound` | guard before argv dispatch, `o.ResolutionHeight == 0` gate | WIRED | stream-copy branch correctly exempted; AVE-02 flags re-asserted by a dedicated test |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Built ffmpeg reports pinned version | `docker run --rm --entrypoint /usr/local/bin/ffmpeg octoconv-av-worker:phase36 -version` | `ffmpeg version n8.1.2 ...` | PASS |
| `go build`/`go vet`/`go test` clean | `go build ./... && go vet ./... && go test ./...` | all packages `ok` | PASS |
| Passthrough-bound + AVE-02 tests | `go test ./internal/convert/ -run NoScale -v` | 6/6 subtests PASS | PASS |
| Disk guard tests | `go test ./internal/convert/ -run TestEnforceMinFreeDisk -v` | 5/5 subtests PASS | PASS |
| Compose config valid | `docker compose config -q` | exit 0 | PASS |
| Live av-worker container running finalized config | `docker logs octoconv-av-worker` | `AV_MAX_DURATION_SECONDS=1m30s ... threads=2` | PASS |
| Live E2E job byte-identical to claim | Go `minio-go` `GetObject` + sha256 | `1a45dfa3` magic, 608065 bytes, sha256 matches SUMMARY | PASS |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| AVE-04 | 36-01, 36-02, 36-03, 36-04 | av-worker контейнеризован, версия ffmpeg пиненная, `AV_ENGINE_TIMEOUT` измерен RTF-матрицей worst-case по методологии Phase 32, с NO-GO-рычагами | SATISFIED | Full chain verified above: pinned source build, measured matrix, Path B lever, finalized 753s parity, live E2E |

No orphaned requirements found — `REQUIREMENTS.md` maps only AVE-04 to Phase 36, and it appears in all 4 plans' `requirements` frontmatter.

### Anti-Patterns Found

None. Scanned all files touched by phase-36 commits (`internal/convert/av.go`, `av_test.go`, `avdiskguard.go`, `avdiskguard_test.go`, `converters.go`, `cmd/av-worker/main.go`, `Dockerfile.av-worker`, `docker-compose.yml`, `.env.example`, `.github/workflows/ci.yml`, `scripts/av-rtf-measure.sh`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` and placeholder-language patterns — zero matches.

### Documentation Staleness (process finding, not a code gap)

**Finding (INFO, non-blocking):** `.planning/STATE.md` (line 28-30) and `.planning/ROADMAP.md` (line 124) still read "PHASE NOT COMPLETE — live compose E2E ... PENDING (operator-run, D-05)" / "live compose E2E PENDING". This text was written in commit `198ea24`, before the live E2E was actually run and before `36-04-SUMMARY.md` was updated in commit `9aac00c` to record the PASSED evidence. The underlying phase goal is verifiably achieved (see Truth #12 and the independent re-verification above) — this is a documentation-sync lag, not a functional or code gap. Recommend the orchestrator update `STATE.md`'s Current Position and `ROADMAP.md`'s Phase 36 checkbox/status line to reflect completion before formally closing the phase.

### Human Verification Required

None. All items that would normally require human/operator judgment (the live E2E, the RTF measurement run, the go/no-go decision) were already completed by the operator in a supervised checkpoint per D-05, and the verifier additionally re-confirmed the live E2E's concrete artifacts independently (database row, container logs, byte-identical S3 object fetch) rather than relying on the SUMMARY narrative alone.

### Gaps Summary

No gaps block phase-goal achievement. One process/documentation item is flagged (STATE.md/ROADMAP.md staleness) for the orchestrator to close out as part of finalizing the phase, but it does not affect the correctness or completeness of the delivered engineering work.

---

_Verified: 2026-07-23T13:45:00Z_
_Verifier: Claude (gsd-verifier)_

## Addendum — Plan 05 gap-closure (post-verification code review)

After this verification passed, the advisory code review (`36-REVIEW.md`) surfaced two findings the plan's own must-haves under-specified:

- **CR-01 (CRITICAL) — RESOLVED (36-05):** the disposition-(b) OOM-DoS bound was implemented for the no-scale (`resolution_height==0`) path only; an explicit `resolution_height` request from an oversized source decoded unbounded. Generalized to `enforceReencodeSourceBound`, now fired unconditionally on every re-encode branch (`av.go:662`), stream-copy remux exempt.
- **HI-01 (HIGH) — RESOLVED (36-05):** Height-only bound; Width was probed but never bounded. Now bounds both axes (`H>1080 || W>1920`).

Also fixed a latent deviation: the fail-closed sentinel (`ErrAVReencodeResolutionExceeded`) was not classified in `isAVTerminal` (`worker.go:365`) → a legitimate reject would have been retried; now terminal/non-retryable, with a `worker_test.go` case. Docs (`.env.example`, `docker-compose.yml`, `36-04-SUMMARY.md`) corrected so the "OOM DoS closed" claim is accurate (≤1920x1080 re-encode envelope; downscaling from larger sources is a named, deliberate capability limitation pending a future measured decode-then-downscale envelope).

Re-check (orchestrator spot-verify + full suite): `go build/vet/test ./...` green, AVE-02 `protocol_whitelist` grep-count unchanged (3), timeout parity 8/8 single value, terminal classification present. **Phase status remains `passed`, now with CR-01/HI-01 closed.** MEDIUM (av-worker env-parser unit tests; wav-demuxer justification) deferred as tracked follow-ups.
