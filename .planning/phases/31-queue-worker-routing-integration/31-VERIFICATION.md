---
phase: 31-queue-worker-routing-integration
verified: 2026-07-18T15:32:49Z
status: passed
score: 4/4 must-haves verified
overrides_applied: 0
---

# Phase 31: Queue/Worker Routing Integration Verification Report

**Phase Goal:** Audio jobs flow end-to-end through the async pipeline with correct retry/dedup and engine-routing semantics.
**Verified:** 2026-07-18T15:32:49Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | An uploaded audio job is routed to a dedicated `audio` asynq queue by the API's `EngineFor` content-detection path and consumed end-to-end by `cmd/audio-worker` (queued → active → done), with the reconciler routing stranded audio jobs to the same queue | ✓ VERIFIED | `internal/convert/converters.go:9` registers `AudioConverter{}` in `convert.Default`, making `EngineFor` audio-aware. `internal/api/handlers.go:280` splices `SniffAudio(rest)` (chained off the byte-0 re-stitched reader, not the cursor-advanced `file`) between the OLE-CFB check and the final fail-closed 422. `internal/api/handlers.go:533-534` enqueues via `s.queue.EnqueueAudioConvert`. `internal/reconciler/reconciler.go:291-292` routes stranded `engine="audio"` jobs to `s.enq.EnqueueAudioConvert`. `cmd/audio-worker/main.go:89` binds `mux.HandleFunc(queue.TypeAudioConvert, h.HandleAudioConvert)` on a server scoped solely to `queue.QueueAudio`. Live E2E proof captured in 31-04-SUMMARY.md: job `993b5efe-f44e-4bce-a6fb-04f3a8c02746`, `202` on create (not 500), Postgres `job_events` show `queued→active→done` in ~2.8s (2026-07-18 15:05:52→15:05:55 UTC), transcript matched the `jfk.wav` fixture exactly, migration 0006 confirmed live via `\d jobs`. Compose stack is down at verification time per task instructions — evidence trail reviewed rather than re-run; code paths independently confirmed present and wired. |
| 2 | A whisper-stage timeout on already-duration-validated audio is classified transient (asynq retries with fresh CPU), while an ffmpeg-stage failure on malformed input is terminal — verified by a stage-aware classifier unit test | ✓ VERIFIED | `internal/worker/worker.go:292` `isAudioTerminal` is a genuinely new function (does not delegate to `timeoutIsTerminal`). Confirmed via source read: ffmpeg-stage prefix (`"audio: ffmpeg:"`) → terminal; whisper-stage prefix (`"audio: whisper-cli:"`) timeout → falls through to shared `isTerminal` (no DeadlineExceeded arm) → transient. `internal/worker/worker_test.go:220` `TestIsAudioTerminal` explicitly asserts both distinguishing cases (`ffmpegTimeout` → true, `whisperTimeout` → false) plus `ErrAudioDurationExceeded`, WR-01 ffprobe-stage terminal/transient split, WR-03 `terminalAudioSignatures`, and WR-04 insufficient-budget transient case. `go test ./internal/worker/ -run TestIsAudioTerminal -v` passes (full suite run confirmed green). |
| 3 | `AudioUniqueTTL` is derived fresh from the audio timeout/retry budget (never reused from image/document) and a dedicated test asserts it strictly exceeds the worst-case audio attempt lifetime (T-03-10) | ✓ VERIFIED | `internal/queue/queue.go:503-505` `AudioUniqueTTL(maxRetry, engineTimeout) = (maxRetry+1)*engineTimeout + audioBackoffSum(maxRetry) + uniqueTTLSafetyMargin` — reuses only the shared safety-margin const, no image/document TTL value referenced. `internal/queue/queue_test.go:406-437` `TestAudioUniqueTTL` asserts the worked example (2570s), monotonicity in both args, AND the explicit SC3 strict-exceeds-zero-margin-worst-case assertion (`AudioUniqueTTL(...) <= worstCaseNoMargin` fails the test). `go test ./internal/queue/ -run TestAudioUniqueTTL -v` passes. |
| 4 | `RECONCILER_ACTIVE_STALE_AFTER` for audio is set above `AUDIO_ENGINE_TIMEOUT` and a test confirms repeated sweep ticks against a long in-flight audio job fire zero spurious `reconciler_recovery` events | ✓ VERIFIED | `cmd/webhook-worker/main.go:95` `ActiveStaleAfter: envDuration("RECONCILER_ACTIVE_STALE_AFTER", 15*time.Minute)` — 15m default now exceeds `AUDIO_ENGINE_TIMEOUT`'s 600s (10m) default (`cmd/audio-worker/main.go:60`). `.env.example:69` documents the global-raise tradeoff. `internal/reconciler/reconciler_test.go:315` `TestSweepAudioZeroSpuriousRecoveryUnderRepeatedTicks` drives 5 sweep ticks with `enqueueAudioErr = asynq.ErrDuplicateTask` and asserts `requeueStaleCalls == 0` and `recoveryCount[id] == 0` after every tick, plus confirms 5 enqueue attempts were made. `go test ./internal/reconciler/ -run TestSweepAudioZeroSpuriousRecoveryUnderRepeatedTicks -v` passes. |

**Score:** 4/4 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/db/migrations/0006_audio_engine.sql` | `jobs.engine` CHECK accepts 'audio' | ✓ VERIFIED | Extends `jobs_engine_check` to `('image','document','av','cad','archive','probe','html','audio')`; live-confirmed via `\d jobs` in 31-04-SUMMARY.md E2E run. |
| `internal/convert/converters.go` | `AudioConverter` registered | ✓ VERIFIED | `Default.Register(AudioConverter{})` present in `init()`. |
| `internal/convert/whisper.go` | `SetAudioModelPath` + 3-tier fallback | ✓ VERIFIED | `SetAudioModelPath` defined; `model()` consults injected → `audioModelPath` → `defaultAudioModelPath`. |
| `internal/queue/queue.go` | `TypeAudioConvert`, `QueueAudio`, `NewAudioConvertTask`, `AudioUniqueTTL`, RetryDelayFunc case | ✓ VERIFIED | All present; `RetryDelayFunc` dispatches `case TypeAudioConvert: return AudioRetryDelay(...)`. |
| `internal/queue/client.go` | `EnqueueAudioConvert` | ✓ VERIFIED | `func (c *Client) EnqueueAudioConvert` present, uses `c.audioMaxRetry`/`c.audioUniqueTTL`. |
| `internal/worker/worker.go` | `HandleAudioConvert`, `isAudioTerminal`, duration-guard splice | ✓ VERIFIED | `HandleAudioConvert` at line 643; `isAudioTerminal` at line 292; `enforceAudioGuardBeforeConvert` (with `audioProbeTimeout=15s` bound, WR-02) spliced into `process()` between download and Convert at line 905. |
| `internal/api/api.go` / `handlers.go` | `Enqueuer` widened, `SniffAudio` splice, opts/enqueue cases | ✓ VERIFIED | Confirmed all three splice points present and correctly ordered. |
| `internal/reconciler/reconciler.go` | `enqueuer` widened, `EngineAudio` sweep case | ✓ VERIFIED | Confirmed. |
| `cmd/audio-worker/main.go` | Runnable audio-queue consumer | ✓ VERIFIED | Builds (`go build ./cmd/audio-worker/`), `go test ./cmd/audio-worker/` passes, binds only `queue.QueueAudio`, calls `SetAudioModelPath` before `srv.Start`. Live-run-proven in 31-04-SUMMARY.md. |
| `.env.example` | Audio operator documentation | ✓ VERIFIED | `AUDIO_ENGINE_TIMEOUT`, `AUDIO_MAX_RETRY`, `AUDIO_WORKER_CONCURRENCY`, `AUDIO_MAX_DURATION_SECONDS`, `AUDIO_MODEL_PATH`, `RECONCILER_ACTIVE_STALE_AFTER` tradeoff, `MAX_UPLOAD_BYTES` note all present. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/convert/converters.go` | `convert.Default` registry | `init() Default.Register(AudioConverter{})` | WIRED | Confirmed by grep + `EngineFor` test coverage in `internal/api`. |
| `internal/queue/queue.go RetryDelayFunc` | `AudioRetryDelay` | `case TypeAudioConvert` | WIRED | Present before `default`. |
| `internal/queue/client.go EnqueueAudioConvert` | `NewAudioConvertTask` | `audioMaxRetry`/`audioUniqueTTL` | WIRED | Confirmed. |
| `internal/worker/worker.go process()` | `convert.EnforceMaxDuration` | `job.Engine == convert.EngineAudio` gate, `enforceAudioGuardBeforeConvert` | WIRED | Splice sits between `downloadTo` and `conv.Convert`; `audioProbeTimeout` (15s, WR-02) bounds the probe distinct from the whole-attempt deadline. |
| `internal/worker/worker.go HandleAudioConvert` | `isAudioTerminal` | terminal/transient branch | WIRED | Confirmed. |
| `internal/api/handlers.go content-detection chain` | `convert.SniffAudio` | `SniffAudio(rest)` | WIRED | `grep -c "SniffAudio(rest)"` == 1, `grep -c "SniffAudio(file)"` == 0. |
| `internal/api/handlers.go opts switch` | `convert.ParseAudioOpts` | `case convert.EngineAudio` | WIRED | Line 393. |
| `internal/api/handlers.go enqueue switch` | `s.queue.EnqueueAudioConvert` | `case convert.EngineAudio` | WIRED | Lines 533-534. |
| `internal/reconciler/reconciler.go sweep()` | `s.enq.EnqueueAudioConvert` | `case convert.EngineAudio` | WIRED | Lines 291-292. |
| `cmd/audio-worker/main.go` | `queue.TypeAudioConvert / h.HandleAudioConvert` | `mux.HandleFunc` | WIRED | Line 89. |
| `cmd/audio-worker/main.go` | `convert.SetAudioModelPath` | `stripInlineComment(os.Getenv("AUDIO_MODEL_PATH")))` before `srv.Start` | WIRED | WR-06 fix confirmed (defensive inline-comment stripping via dedicated `stripInlineComment`, not generic `firstField`, preserving spaces/embedded `#`). |

### Behavioral Spot-Checks / Test Suite Execution

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Full repo build | `go build ./...` | clean, no errors | ✓ PASS |
| Full repo vet | `go vet ./...` | clean | ✓ PASS |
| gofmt | `gofmt -l .` | no output (clean) | ✓ PASS |
| Queue/worker/api/reconciler/convert/cmd test suites | `go test ./internal/queue/ ./internal/worker/ ./internal/api/ ./internal/reconciler/ ./internal/convert/ ./cmd/... -count=1` | all `ok` | ✓ PASS |
| Stage-aware classifier (SC2) | `go test ./internal/worker/ -run TestIsAudioTerminal -v` | PASS (all sub-cases incl. WR-01/WR-03/WR-04 pinned cases) | ✓ PASS |
| AudioUniqueTTL strict-exceeds (SC3) | `go test ./internal/queue/ -run TestAudioUniqueTTL -v` | PASS | ✓ PASS |
| Reconciler zero-spurious-recovery (SC4) | `go test ./internal/reconciler/ -run TestSweepAudioZeroSpuriousRecoveryUnderRepeatedTicks -v` | PASS | ✓ PASS |
| API audio routing/opts/byte-integrity | `go test ./internal/api/ -run Audio -v` | PASS (`TestCreateJob_AudioDetectedAndAccepted`, `TestCreateJob_AudioOptsAccepted`, `TestCreateJob_AudioOptsRejectedForWrongLanguage`) | ✓ PASS |

### Probe Execution

Not applicable — no `scripts/*/tests/probe-*.sh` conventions declared or referenced by this phase's PLAN/SUMMARY files. Skipped.

### Code Review Fixes (31-REVIEW.md WR-01..WR-06) — Verified Present in Code

| Finding | Fix Claimed | Verified in Code |
|---------|-------------|-------------------|
| WR-01 (ffprobe-stage failures misclassified transient) | Terminal arm for deterministic ffprobe shapes | ✓ `internal/worker/worker.go:305-315` matches `"ffprobe: unparseable duration"`, `"ffprobe: implausible duration"`, `"ffprobe failed:"`; environment/timeout shapes stay transient. Pinned in `TestIsAudioTerminal`. |
| WR-02 (ProbeDuration runs under full attempt timeout) | 15s dedicated probe bound | ✓ `internal/worker/worker.go:829` `const audioProbeTimeout = 15 * time.Second`, applied in `enforceAudioGuardBeforeConvert` at line 848. |
| WR-03 (accidental cross-engine signature matches) | Dedicated `terminalAudioSignatures` list | ✓ `internal/worker/worker.go:112` `terminalAudioSignatures` defined and checked before shared fallthrough; independence proven by `TestIsAudioTerminalOutputSignatures`. |
| WR-04 (ffmpeg-stage terminal-on-timeout can misclassify upstream budget exhaustion) | `minFfmpegBudget` floor before ffmpeg stage | ✓ `internal/convert/whisper.go:58` `const minFfmpegBudget = 30 * time.Second`, guard at line 232. |
| WR-05 (`AUDIO_MAX_DURATION_SECONDS` bare-integer silently falls back) | `envDurationSeconds` accepting both shapes + warn-on-unparseable | ✓ `cmd/audio-worker/main.go:66,177` `envDurationSeconds` defined and used. |
| WR-06 (`AUDIO_MODEL_PATH` inline-comment vulnerability) | `stripInlineComment` defensive parsing | ✓ `cmd/audio-worker/main.go:86,209` `stripInlineComment` defined and applied to `os.Getenv("AUDIO_MODEL_PATH")`. |

All 6 warning-level findings from 31-REVIEW.md are confirmed present in the current codebase, not merely claimed in commit messages.

### Anti-Patterns Found

None. Scanned all 19 phase-modified files (migration SQL, converters/whisper/audioduration, queue.go/client.go/queue_test.go, worker.go/worker_test.go, api.go/handlers.go/handlers_test.go, reconciler.go/reconciler_test.go, cmd/audio-worker/main.go, cmd/worker/document-worker/chromium-worker/webhook-worker main.go, .env.example) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` — zero matches. `[ASSUMED]` markers present (e.g. `AUDIO_ENGINE_TIMEOUT=600s`) are explicitly documented, deliberate, in-scope placeholders per the phase's own threat model and the roadmap's Phase 32 (RTF measurement) deferral — not undocumented debt.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| AUD-05 | 31-01, 31-02, 31-03 (all four plans declare it) | Стадийная классификация таймаутов, собственный AudioUniqueTTL, RECONCILER_ACTIVE_STALE_AFTER выше AUDIO_ENGINE_TIMEOUT | ✓ SATISFIED | All three sub-clauses independently verified above (Truths 2, 3, 4). **Note:** REQUIREMENTS.md still shows `- [ ] **AUD-05**` unchecked and traceability table marks it "Pending" (`.planning/REQUIREMENTS.md:29,82`) — this is a documentation-tracking lag, not a code gap; the implementation evidence is conclusive. Recommend updating REQUIREMENTS.md's checkbox/status as part of phase closeout. |

No orphaned requirements found — REQUIREMENTS.md's traceability table maps AUD-05 solely to Phase 31, and all four plans in this phase declare `requirements: [AUD-05]`.

### Human Verification Required

None. SC1's live end-to-end proof was already captured with timestamped, cross-checkable evidence in 31-04-SUMMARY.md (job id, Postgres `job_events` timestamps, `\d jobs` constraint dump, transcript content match) during the phase's own execution, and the compose stack is intentionally down at verification time (per task instruction, not to be re-run). The evidence trail was reviewed and cross-referenced against the actual code paths (migration file, handler routing, worker binding) rather than re-executed. All other success criteria (SC2, SC3, SC4) are proven by deterministic, already-passing unit tests re-run during this verification — no external infrastructure or human judgment required.

### Gaps Summary

No gaps found. All four ROADMAP success criteria are independently verified through a combination of (a) direct source-code inspection confirming the claimed wiring exists and is correctly ordered/gated, (b) re-running the full relevant test suites in this session (all green), and (c) cross-referencing the 31-REVIEW.md code-review fixes (WR-01..WR-06) against current code to confirm they were actually applied, not just claimed in commit messages. SC1's live infrastructure proof is accepted from 31-04-SUMMARY.md's timestamped Postgres-backed evidence trail per the verification task's explicit instruction not to re-run the (currently torn-down) compose stack.

The four IN-01..IN-04 informational findings from 31-REVIEW.md remain open by design (tracked, not fix-scope) and are correctly out of this phase's fence: IN-04 (no audio-worker compose service) is an explicitly recorded intentional deferral to Phase 32, consistent with this verification's scope fence instructions.

---

_Verified: 2026-07-18T15:32:49Z_
_Verifier: Claude (gsd-verifier)_
