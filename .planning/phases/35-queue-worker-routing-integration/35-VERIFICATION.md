---
phase: 35-queue-worker-routing-integration
verified: 2026-07-22T02:33:49Z
status: passed
score: 4/4 must-haves verified
overrides_applied: 0
---

# Phase 35: Queue, Worker & Routing Integration Verification Report

**Phase Goal:** av-engine jobs (transcode/audio-extract/thumbnail) and video→transcript jobs both flow end-to-end through the async pipeline with correct queue routing, retry classification, and reconciler recovery.
**Verified:** 2026-07-22T02:33:49Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | A video job targeting mp4/webm/mp3/wav/m4a/jpg/png/webp routes to a dedicated `av` asynq queue via `EngineFor`, is consumed end-to-end (queued→active→done) by `cmd/av-worker`, and the reconciler routes stranded `jobs.engine='av'` jobs to the same queue | ✓ VERIFIED | Code paths confirmed at HEAD: `internal/api/handlers.go:637-638` (`case convert.EngineAV: enqueueErr = s.queue.EnqueueAVConvert`), `internal/convert/converters.go:20` (`Default.Register(AVConverter{})`), `cmd/av-worker/main.go:79` (`mux.HandleFunc(queue.TypeAVConvert, h.HandleAVConvert)`, `Queues: map[string]int{queue.QueueAV: 1}`), `internal/reconciler/reconciler.go:294-295` (`case convert.EngineAV: enqueueErr = s.enq.EnqueueAVConvert`). Live E2E was executed during 35-07 (not re-run per task instructions, treated as recorded evidence): mp4→webm job reached DB status `done` via `engine=av` in ~3.4s (35-07-SUMMARY.md). `TestSweepRoutesAVJobsToAVQueue`, `TestCreateJobRoutesEveryEngineToItsQueue/av` pass at HEAD (re-run by verifier). |
| 2 | A video job targeting txt/srt/vtt/json routes instead to the existing `audio` queue/worker (video pairs added to `AudioConverter.Pairs()`, `Engine()` stays `EngineAudio`), with a dedicated pair-disjointness test proving zero overlap | ✓ VERIFIED | `internal/convert/whisper.go` `audioSourceFormats` extended with `mp4/mov/avi/mkv/webm` (9 sources × 4 targets = 36 pairs, `Engine()` returns `EngineAudio`). `internal/convert/pairs_disjoint_test.go::TestAVAudioPairDisjointness` re-run by verifier: PASS (identity loop + union-cardinality check, normalized via `NormalizeFormat` per the 5dfbb05 security-observation fix). `internal/api/handlers_test.go::TestCreateJobRoutesEveryEngineToItsQueue/mp4_to_srt_routes_audio_not_av` and `/mp4_to_webm_routes_av_not_audio` both PASS (re-run by verifier) — proving the exact split at the API layer. Live E2E (35-07) recorded the same split at the DB level for identical source bytes (`engine=audio` vs `engine=av`). |
| 3 | A stage-aware transient/terminal classifier for av jobs is derived fresh (not copied from audio's ffmpeg-timeout-is-terminal precedent) and a unit test pins transcode-timeout transient vs. deterministic/malformed-input failures terminal | ✓ VERIFIED | `internal/worker/worker.go:354-387` `isAVTerminal`: `ErrAVTranscodeFailed` wrapping a timeout returns `!isTimeout` (transient); `ErrAVAudioExtractFailed`/`ErrAVThumbnailFailed` always terminal; all deterministic guard sentinels (`ErrAVOutputMissingOrEmpty`, `ErrAVTimecodeOutOfRange`, `ErrAVResolutionExceeded`, `ErrAudioDurationExceeded`, `ErrAVNoVideoStream`) always terminal regardless of stage; no `strings.Contains` fallback (falls through to shared `isTerminal`). `go test ./internal/worker/ -run TestIsAVTerminal -v` re-run by verifier: PASS, includes an explicit contrast assertion that `isAVTerminal` and `isAudioTerminal` disagree on a transcode-timeout-shaped error. |
| 4 | An `AVUniqueTTL` derived from the av engine's own timeout/retry budget prevents duplicate processing, verified by a monotonicity/lower-bound test mirroring `AudioUniqueTTL` | ✓ VERIFIED | `internal/queue/queue.go:591-593`: `AVUniqueTTL(maxRetry, engineTimeout) = (maxRetry+1)*engineTimeout + avBackoffSum(maxRetry) + uniqueTTLSafetyMargin` — the identical formula shape as `AudioUniqueTTL`, reusing the shared safety margin constant (no new per-engine margin introduced). `go test ./internal/queue/ -run TestAVUniqueTTL -v` re-run by verifier: PASS. |

**Score:** 4/4 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/convert/av.go` | `ErrAVTranscodeFailed`/`ErrAVAudioExtractFailed`/`ErrAVThumbnailFailed` sentinels | ✓ VERIFIED | Declared; three ffmpeg call sites rewrapped with Go 1.20+ multi-`%w`; zero remaining `"av: ffmpeg:"` occurrences confirmed by grep |
| `internal/convert/avduration.go` | `ErrAVNoVideoStream` sentinel (IN-01) | ✓ VERIFIED | Declared; three anonymous call sites replaced |
| `internal/convert/whisper.go` | Video source formats, `-map 0:a:0`, `minFfmpegBudgetVideo` | ✓ VERIFIED | `AudioConverter{}.Pairs()` == 36; `-map 0:a:0` pinned by argv test |
| `internal/convert/pairs_disjoint_test.go` | `TestAVAudioPairDisjointness` | ✓ VERIFIED | Exists, PASSes, normalized-comparison hardening present (5dfbb05) |
| `internal/queue/queue.go` | `TypeAVConvert`, `QueueAV`, `NewAVConvertTask`, `avRetrySchedule`, `AVRetryDelay`, `avBackoffSum`, `AVUniqueTTL`, `AllConvertQueues` | ✓ VERIFIED | All present and compiling; `AllConvertQueues()` returns exactly the 5 engine queues |
| `internal/queue/client.go` | `EnqueueAVConvert`, `AV_MAX_RETRY`/`AV_ENGINE_TIMEOUT` env reads | ✓ VERIFIED | Present |
| `internal/worker/worker.go` | `isAVTerminal`, `HandleAVConvert`, `avFailureCode` | ✓ VERIFIED | All present; four distinguishable terminal error codes (`timecode_out_of_range`/`duration_exceeded`/`resolution_exceeded`/`no_video_stream`) confirmed by grep |
| `internal/api/api.go` / `handlers.go` | `Enqueuer.EnqueueAVConvert`, `maxEngineBytes` two-tier ceiling, `EngineAV` enqueue + opts-dispatch cases | ✓ VERIFIED | All present; ceiling check runs post-`EngineFor`, pre-`storage.Upload` |
| `internal/reconciler/reconciler.go` | `enqueuer.EnqueueAVConvert`, `case convert.EngineAV` routing | ✓ VERIFIED | Present; `default:` arm fail-closed behavior unchanged |
| `cmd/av-worker/main.go` | av engine's worker binary | ✓ VERIFIED | Builds clean (`go build -o /dev/null ./cmd/av-worker`); binds `QueueAV`; serves `TypeAVConvert` via `HandleAVConvert`; `ShutdownTimeout = AV_ENGINE_TIMEOUT + 10s`; `RetryDelayFunc: queue.RetryDelayFunc` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/api/handlers.go` enqueue switch | `s.queue.EnqueueAVConvert` | `case convert.EngineAV` | WIRED | Line 637-638, confirmed by direct read |
| `internal/api/handlers.go` detection chain | `convert.SniffVideo` | `if detected == ""` gate, before `SniffAudio` | WIRED | Placed after `ClassifyCFB`, before `SniffAudio`; `rest` unconditionally rebound on `verr == nil` (CR fixed during 35-06 execution, verified via passing `TestCreateJob_AudioDetectedAndAccepted`) |
| `internal/convert/converters.go` init | `convert.Default` | `Default.Register(AVConverter{})` after `AudioConverter` | WIRED | Confirmed by direct read; ordering hazard documented inline, guarded by `TestAVAudioPairDisjointness` |
| `internal/reconciler/reconciler.go` sweep | `s.enq.EnqueueAVConvert` | `case convert.EngineAV` | WIRED | Line 294-295, confirmed by direct read |
| `cmd/av-worker/main.go` | `worker.Handler.HandleAVConvert` | `mux.HandleFunc(queue.TypeAVConvert, h.HandleAVConvert)` | WIRED | Confirmed by direct read |
| `queue.RetryDelayFunc` | `AVRetryDelay` | `case TypeAVConvert` | WIRED | Confirmed by direct read + `TestRetryDelayFuncRoutesAVConvert` PASS |
| `queue.AllConvertQueues` | `cmd/api/main.go` collector | `append(queue.AllConvertQueues(), queue.QueueWebhook)...` | WIRED | Confirmed by direct read; hand-written variadic list is gone |

### Behavioral Spot-Checks / Live E2E

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| `go build ./...` | build entire repo | clean, no errors | ✓ PASS |
| `go vet ./...` | static analysis | clean, no warnings | ✓ PASS |
| `gofmt -l .` | formatting check | no output (all files formatted) | ✓ PASS |
| `go test ./... -count=1` (full suite, live ffmpeg/ffprobe 8.1.2 + whisper-cli present) | full test suite | all 21 testable packages `ok`, zero failures | ✓ PASS |
| `TestAVAudioPairDisjointness` | targeted re-run | PASS | ✓ PASS |
| `TestIsAVTerminal` | targeted re-run | PASS | ✓ PASS |
| `TestAVUniqueTTL`, `TestRetryDelayFuncRoutesAVConvert`, `TestAllConvertQueuesCoversEveryEngine` | targeted re-run | all PASS | ✓ PASS |
| `TestSweepRoutesAVJobsToAVQueue`, `TestSweepRoutesEveryEngineConstant` (5 engines) | targeted re-run | all PASS | ✓ PASS |
| `TestCreateJobRoutesEveryEngineToItsQueue` (5 engines + 2 video-split rows) | targeted re-run | all 7 subtests PASS | ✓ PASS |
| Live mp4→webm job (recorded, 35-07-SUMMARY.md) | `go run ./cmd/api` + `go run ./cmd/av-worker` against docker-compose infra | reached `done`, `engine=av`, ~3.4s | ✓ PASS (recorded evidence, not re-run per task instructions) |
| Live mp4→srt job (recorded, 35-07-SUMMARY.md) | same infra, `target_format=srt` | routed `engine=audio`, consumed by `cmd/audio-worker` | ✓ PASS (recorded evidence — proves the SC1/SC2 differential) |
| `pgrep ffmpeg` after worker idle (recorded) | process check | empty — no orphaned ffmpeg | ✓ PASS (recorded evidence) |

### Review / Security Fix Verification (CR-01, WR-01, security observation)

| Item | Claimed Fix | HEAD Verification |
|------|-------------|--------------------|
| CR-01 (multipart in-memory budget) | Decouple `ParseMultipartForm` maxMemory from the 2 GiB upload ceiling; fixed 32 MiB budget | Commit `8a0003a` present; `internal/api/handlers.go:44` declares `multipartInMemoryBudget = 32 << 20`; line 106 calls `r.ParseMultipartForm(multipartInMemoryBudget)` (not `s.maxUploadByte`) — confirmed by direct read |
| WR-01 (redundant ffprobe in `convertAudioExtract`) | Thread `src avSourceProbe` through instead of re-probing | Commit `49b1043` present; `internal/convert/av.go:546-548` `convertAudioExtract(ctx, inPath, outPath, target string, src avSourceProbe)` uses `src.audioCodec` directly, no second `probeAudioCodec` call — confirmed by direct read |
| Security observation #1 (disjointness test normalization) | Normalize pairs via `NormalizeFormat` before comparing | Commit `5dfbb05` present; `internal/convert/pairs_disjoint_test.go` `norm()` helper applies `NormalizeFormat` to both `From`/`To` before comparison — confirmed by direct read; test still PASSes |

All three fixes are present at HEAD, functionally correct, and did not regress: full `go test ./... -count=1` passes with all three commits included in history.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|--------------|--------|----------|
| AVE-03 | 35-01 through 35-07 | Dedicated `av` asynq queue + `cmd/av-worker` with own retry schedule and unique-lock TTL from worst-case budget; stage-aware transient/terminal classification derived fresh for video; reconciler routing by `jobs.engine='av'` | ✓ SATISFIED | All sub-clauses verified directly: `TypeAVConvert`/`QueueAV`/`avRetrySchedule` (30s/2m, D-03 locked), `AVUniqueTTL` formula, `cmd/av-worker` binary, `isAVTerminal` fresh derivation with contrast test, reconciler `case convert.EngineAV` |
| AVT-01 | 35-01, 35-06 | Client can get a video transcript (mp4/mov etc. → txt/srt/vtt/json, whisper verbatim contract); video sources added to `AudioConverter.Pairs()` (Engine stays audio); jobs ride the existing audio worker; pair disjointness pinned by explicit test; `AUDIO_ENGINE_TIMEOUT` RTF assumption re-verified for video sources (demux overhead) | ✓ SATISFIED | `AudioConverter.Pairs()` == 36 (9 sources × 4 targets), `Engine()` stays `EngineAudio`; `TestAVAudioPairDisjointness` PASSes; RTF/demux-overhead concern addressed via `minFfmpegBudgetVideo` (90s floor, D-05) rather than raising `AUDIO_ENGINE_TIMEOUT` itself — a deliberate, documented design choice (35-CONTEXT.md D-05) that targets the actual demux-cost difference without a class-wide timeout/TTL recompute; `TestSelectMinFfmpegBudget` pins the floor selection |

No orphaned requirements: REQUIREMENTS.md maps exactly AVE-03 and AVT-01 to Phase 35, and both appear in plan frontmatter `requirements:` fields across the seven plans.

**Note (non-blocking, informational):** `.planning/REQUIREMENTS.md`'s v1 checklist still shows `[ ]` unchecked boxes for AVE-03 and AVT-01, and `.planning/STATE.md`'s `stopped_at`/`last_activity` lines still read "Phase 35 planned... ready to execute" / "Phase 35 execution started" — both are stale tracking artifacts (the phase's own ROADMAP.md entry correctly shows all 7 plans checked `[x]`, and 35-REVIEW.md/35-SECURITY.md both show `status: resolved`/`status: verified`). This is a documentation-sync gap, not a code gap, and does not affect goal achievement.

### Anti-Patterns Found

None. Scanned all twelve phase-modified files (`internal/convert/{av,avduration,whisper,converters}.go`, `internal/convert/pairs_disjoint_test.go`, `internal/queue/{queue,client}.go`, `internal/worker/worker.go`, `internal/api/{api,handlers}.go`, `internal/reconciler/reconciler.go`, `cmd/av-worker/main.go`, `cmd/api/main.go`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` — zero matches. No debt markers requiring the debt-marker gate.

### Scope Fence Compliance (deliberately NOT gaps, per Phase 36/37 boundary)

Confirmed absent at HEAD, as required by the phase's own scope fence: no `Dockerfile.av-worker`, no `av-worker` compose service (`grep -c av-worker docker-compose.yml` == 0), no Helm chart changes, no CI workflow changes, `AV_ENGINE_TIMEOUT` remains `[ASSUMED]` 600s pending Phase 36's RTF matrix, `AV_MAX_DURATION_SECONDS` deferred per `.env.example` comment. `git diff --stat go.mod go.sum` shows zero changes across the entire phase (last touch: `3972e26`, Phase 21) — zero new dependencies introduced.

### Human Verification Required

None. The one item that would normally require human/live-infra verification (SC1's "consumed end-to-end, queued→active→done") was already executed live during 35-07 and is documented with concrete evidence (job IDs, DB `engine` column values, timing, metrics scrape output, `pgrep ffmpeg` check) in 35-07-SUMMARY.md. Per the verification task's explicit instruction, this recorded live result is treated as evidence rather than requiring re-execution, and the underlying code paths that produced that behavior were independently re-verified at HEAD by this report (build/vet/test clean, all routing switches present, all guarding tests passing).

### Gaps Summary

No gaps. All four ROADMAP success criteria are directly verifiable in the codebase at HEAD, not merely claimed in SUMMARY prose:

- Routing: three independent D-06 completeness tests (queue-depth collector, reconciler switch, API enqueue switch) all pass and were each confirmed to fail-on-removal during execution (documented in the respective SUMMARYs, and the completeness tests themselves were re-run clean by this verification).
- Classification: `isAVTerminal` is structurally distinct from `isAudioTerminal` (contrast test proves genuine disagreement on the exact case that matters — transcode timeout).
- TTL: `AVUniqueTTL` reuses the shared formula and safety margin, pinned by a monotonicity/lower-bound test.
- The one code-review blocker (CR-01) and one warning (WR-01), plus one security-audit observation, were all fixed in dedicated follow-up commits (`8a0003a`, `49b1043`, `5dfbb05`) present at HEAD, verified by direct code read (not SUMMARY trust), and did not regress the full test suite.
- `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./... -count=1` (with live ffmpeg 8.1.2/ffprobe/whisper-cli available, so live-gated tests executed for real) are all clean at HEAD.

---

*Verified: 2026-07-22T02:33:49Z*
*Verifier: Claude (gsd-verifier)*
