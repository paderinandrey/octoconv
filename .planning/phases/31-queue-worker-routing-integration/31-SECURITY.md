---
phase: 31
slug: queue-worker-routing-integration
status: verified
threats_open: 0
asvs_level: 1
created: 2026-07-18
---

# Phase 31 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.
> This phase wires the Phase-30-built audio engine (`AudioConverter`,
> `SniffAudio`, `AudioOpts`, `EnforceMaxDuration`) live into the queue,
> worker, API routing, and reconciler layers — the first phase where the
> audio vertical is client-facing. It also closes the T-30-08 carry-forward
> (duration guard built but never invoked) and applies six code-review
> hardening fixes (WR-01..WR-06) discovered post-plan-authoring.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|----------------|
| client audio job → jobs table | `engine='audio'` crosses into the persisted CHECK-constrained column | engine string |
| downloaded audio file → EnforceMaxDuration → ffprobe | untrusted downloaded bytes measured before decode | file path, subprocess argv/stdout |
| downloaded audio file → ffmpeg `-i` path arg | path handed to a tool that interprets URL/protocol specifiers | file path argv element |
| engine error string → terminal/transient classifier | error message drives retry vs fail-closed decision | error string |
| client upload bytes → SniffAudio magic-byte peek | untrusted container bytes classified; re-stitched reader must preserve full file | audio container bytes |
| client opts JSON → ParseAudioOpts → engine argv | audio opts must go through the closed-allowlist AudioOpts path, not DocOpts | opts JSON |
| stranded jobs.engine value → reconciler routing switch | engine string drives which queue a recovered job re-enters | engine string |
| AUDIO_MODEL_PATH env → whisper-cli `-m` | operator-set process env (server-controlled), never client bytes | filesystem path |
| uploaded audio → live worker pipeline | first real client-facing exercise of the full audio vertical | end-to-end |
| Go module graph | no new external packages cross into go.mod/go.sum this phase | dependency graph |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-31-01 | Denial of Service | duration guard invoked in `process()` before `Convert` (T-30-08 carry-forward) | mitigate | `enforceAudioGuardBeforeConvert` spliced into `process()` after download, before `conv.Convert`, gated on `job.Engine == convert.EngineAudio`; `ErrAudioDurationExceeded` classifies terminal | closed |
| T-31-02 | Tampering (integrity) | SniffAudio off `rest` reader — no silent truncation | mitigate | `convert.SniffAudio(rest)` (not `file`) spliced between OLE-CFB block and fail-closed 422 | closed |
| T-31-03 | Tampering/SSRF-adjacent | `file:` protocol prefix on ffprobe/ffmpeg path args (IN-01) | mitigate | `ffprobeDurationArgs`/`ffmpegNormalizeArgs` prefix the path argv element with `file:` | closed |
| T-31-04 | Tampering | dedicated `EngineAudio` opts case (`ParseAudioOpts`, not `DocOpts`) | mitigate | `case convert.EngineAudio:` in the opts-parsing switch calls `ParseAudioOpts`/`ValidateAudioApplicability` | closed |
| T-31-05 | DoS/correctness | migration 0006 `jobs.engine` CHECK += `'audio'` | mitigate | `0006_audio_engine.sql` DROP/ADD CHECK includes `'audio'`; live E2E proves 202 (not 500) on create | closed |
| T-31-06 | Tampering (path) | `AUDIO_MODEL_PATH` operator-set env | accept (documented) | Operator-set process env, never client bytes; consumed once at startup via `SetAudioModelPath` | closed |
| T-31-07 | DoS | whisper-stage timeout transient but bounded by `AUDIO_MAX_RETRY` | mitigate | `isAudioTerminal` falls through to shared `isTerminal` (no `DeadlineExceeded` arm) for `"audio: whisper-cli:"`-prefixed errors | closed |
| T-31-08 | Tampering | ffmpeg-stage prefix → terminal (no retry on malformed input) | mitigate | `isAudioTerminal` returns `true` on `strings.Contains(msg, "audio: ffmpeg:")` | closed |
| T-31-09 | DoS | reconciler `EngineAudio` routing + `AudioUniqueTTL` dedup (SC4) | mitigate | `sweep()` `case convert.EngineAudio` routes to `EnqueueAudioConvert`; SC4 test proves zero spurious recoveries under `asynq.ErrDuplicateTask` | closed |
| T-31-10 | Spoofing | MP4-video not sniffed as audio (reuses hardened T-30-04 detector) | accept | `audiosniff.go`/`m4aBrands` untouched this phase (git-log confirmed); Phase 30's hardened `{M4A , M4B }`-only allowlist reused verbatim | closed |
| T-31-11 | DoS | audio-worker bounded: `Queues{QueueAudio:1}`, `ShutdownTimeout`, `Concurrency` | mitigate | `cmd/audio-worker/main.go` asynq.Config: `Queues: map[string]int{queue.QueueAudio: 1}`, `Concurrency: envInt("AUDIO_WORKER_CONCURRENCY", 2)`, `ShutdownTimeout: AUDIO_ENGINE_TIMEOUT + 10s` | closed |
| T-31-SC | Supply chain | no new go modules this phase | accept | `git show --stat` on all 15 phase-31 commits (incl. 6 WR-fix commits) confirms zero `go.mod`/`go.sum` diffs | closed |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Threat Verification Evidence

### Mitigate dispositions (grep/read-verified in cited implementation files + test evidence)

| Threat ID | Evidence |
|-----------|----------|
| T-31-01 | `internal/worker/worker.go:905-912` — `HandleAudioConvert`'s call into `h.process` reaches the splice at `worker.go:893-912`: `downloadTo` (line 893) then `enforceAudioGuardBeforeConvert(attemptCtx, job.Engine, inPath, h.audioMaxDuration, func() error { conv.Convert(...) })` (line 905), strictly before `conv.Convert` runs. `enforceAudioGuardBeforeConvert` itself (`worker.go:843-856`) gates on `engine == convert.EngineAudio`, derives a 15s `audioProbeTimeout` probe-only deadline (WR-02 fix, `worker.go:829,848`), and calls `convert.EnforceMaxDuration`. Test: `internal/worker/worker_test.go:366` `TestEnforceAudioGuardBeforeConvert_IN02` (asserts the guard fires and `convertFn` is never invoked for an over-ceiling fixture) and `:416` `TestEnforceAudioGuardProbeExpiryTransient_WR02`. This closes the exact residual gap flagged in `30-SECURITY.md` T-30-08 ("ordering is documented convention only, not enforced by any code path") — the guard is now actually invoked, not just documented. `go test ./internal/worker/ -run Audio -v` passes. |
| T-31-02 | `internal/api/handlers.go:280-283` — `if audioDetected, audioRest, aerr := convert.SniffAudio(rest); aerr == nil && audioDetected != "" { detected = audioDetected; rest = audioRest }`, chained off `rest` (Sniff's byte-0 re-stitch), never `file` (`grep -c "SniffAudio(file)" internal/api/handlers.go` = 0). Splice point is between the OLE-CFB block (ends line 266) and the final fail-closed 422 (line 285) — reachable, not dead code. Test: `internal/api/handlers_test.go:1912` `TestCreateJob_AudioDetectedAndAccepted` — asserts the stored object bytes equal the uploaded bytes (fakeStorage captures via `io.ReadAll`, not `io.Discard`) using the `sample-id3.mp3` fixture with its large ID3v2 tag. `go test ./internal/api/ -run Audio -v` passes. |
| T-31-03 | `internal/convert/audioduration.go:70` — `ffprobeDurationArgs` returns `..., "file:" + path` as the final argv element; `internal/convert/whisper.go:140` — `ffmpegNormalizeArgs` returns `[]string{"-y", "-i", "file:" + inPath, ...}`. Both extracted into pure argv-builder functions specifically so the prefix is unit-pinned. Tests: `internal/convert/audioduration_test.go:78` `TestFfprobeDurationArgs_FilePrefix`, `internal/convert/whisper_test.go:290` `TestFfmpegNormalizeArgs_FilePrefix` — both assert the exact argv slice including the `file:` prefix. `go test ./internal/convert/` passes. |
| T-31-04 | `internal/api/handlers.go:393-410` — `case convert.EngineAudio:` in the opts-parsing switch: `convert.ParseAudioOpts([]byte(rawOpts))` → 422 "invalid opts" on error; `convert.ValidateAudioApplicability(...)` → 422 "opts not applicable" on error; normalizes via `json.Marshal(audioOpts)` before persisting (never raw client bytes). Tests: `internal/api/handlers_test.go:1965` `TestCreateJob_AudioOptsAccepted` (`{"language":"ru"}` accepted, not rejected as invalid DocOpts), `:1993` `TestCreateJob_AudioOptsRejectedForWrongLanguage`. `go test ./internal/api/ -run Audio -v` passes. |
| T-31-05 | `internal/db/migrations/0006_audio_engine.sql:9-11` — `ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check; ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe', 'html', 'audio'));`. **Live E2E proof (SUMMARY-sourced, not grep, per task instruction):** `31-04-SUMMARY.md` lines 96-121 — `docker exec octoconv-db psql -c "\d jobs"` output shows `jobs_engine_check ... ARRAY[..., 'audio'::text]` live in Postgres; `curl -X POST /v1/jobs` with `jfk.wav` returned `HTTP 202 {"job_id":"993b5efe-...","status":"queued"}` — **202, not 500**, proving the constraint accepts `'audio'` inserts against real infrastructure, not just the migration file's text. |
| T-31-07 | `internal/worker/worker.go:292-337` `isAudioTerminal` — the `"audio: whisper-cli:"`-prefixed timeout path (stage 2) matches none of the explicit terminal arms (`ErrAudioDurationExceeded`, `"audio: ffmpeg:"`, ffprobe-stage signatures, `terminalAudioSignatures`) and falls through to `return isTerminal(err)` (line 336) — the shared base classifier, which per its own doc comment (`worker.go:147-170`) has no `context.DeadlineExceeded` arm, so it stays transient. Retries are then bounded by asynq's `MaxRetry` = `c.audioMaxRetry` read from `AUDIO_MAX_RETRY` (`internal/queue/client.go`), enforced via `NewAudioConvertTask(..., maxRetry, ...)`. Test: `internal/worker/worker_test.go:220` `TestIsAudioTerminal` explicitly asserts `isAudioTerminal(fmt.Errorf("convert: audio: whisper-cli: %w", context.DeadlineExceeded)) == false` (the SC2 distinguishing case). `go test ./internal/worker/ -run TestIsAudioTerminal -v` passes. |
| T-31-08 | `internal/worker/worker.go:302-306` — `if strings.Contains(msg, "audio: ffmpeg:") { return true }`, i.e. any ffmpeg-stage failure OR timeout classifies terminal (Key Decision 1, no retry burned on malformed/adversarial input). Test: `worker_test.go:220` `TestIsAudioTerminal` asserts both `isAudioTerminal(fmt.Errorf("...audio: ffmpeg: %w", context.DeadlineExceeded)) == true` and the non-timeout decode-error variant == true. Post-review WR-04 hardening (`internal/convert/whisper.go`, `minFfmpegBudget` floor) additionally prevents an upstream-stall-induced near-zero-budget ffmpeg kill from being misattributed to this terminal arm — pinned by `internal/convert/whisper_test.go:338` `TestAudioConverter_InsufficientBudgetFailsFast` and a corresponding transient-classification case in `TestIsAudioTerminal`. `go test ./internal/worker/ ./internal/convert/` pass. |
| T-31-09 | `internal/reconciler/reconciler.go:64` — `enqueuer` interface gains `EnqueueAudioConvert(ctx, id) error`; `:291-292` — `case convert.EngineAudio: enqueueErr = s.enq.EnqueueAudioConvert(ctx, j.ID)` in `sweep()`, before the fail-closed `default`. Tests: `internal/reconciler/reconciler_test.go:276` `TestSweepRoutesAudioJobsToAudioQueue` (asserts audio routing AND non-routing to the other three queues), `:315` `TestSweepAudioZeroSpuriousRecoveryUnderRepeatedTicks` (SC4 — 5 sweep ticks against `enqueueAudioErr = asynq.ErrDuplicateTask`, asserts `RequeueStale` called 0 times / `recoveryCount` stays 0 across all ticks, proving the `AudioUniqueTTL` lock, not the staleness threshold, is the actual double-processing safety mechanism). `go test ./internal/reconciler/ -run Audio -v` passes. |
| T-31-11 | `cmd/audio-worker/main.go:91-102` — `asynq.NewServer(redisOpt, asynq.Config{Concurrency: envInt("AUDIO_WORKER_CONCURRENCY", 2), Queues: map[string]int{queue.QueueAudio: 1}, RetryDelayFunc: queue.RetryDelayFunc, ShutdownTimeout: envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second) + 10*time.Second})` — queue map limited to exactly `QueueAudio`, concurrency and shutdown window both env-bounded with sane defaults. Live-proven: `31-04-SUMMARY.md` — `go run ./cmd/audio-worker` consumed exactly one job from the `audio` queue and completed within its `ShutdownTimeout` window (graceful-shutdown log sequence captured). `go build ./cmd/audio-worker/` and `go test ./cmd/audio-worker/` pass. |

### Accept dispositions (documentation verified)

| Threat ID | Verification |
|-----------|--------------|
| T-31-06 | `.env.example:55-63` — `AUDIO_MODEL_PATH` documented with an explicit comment block (whisper.cpp model path passed to whisper-cli `-m`; local-dev vs container-path examples) plus `cmd/audio-worker/main.go:74-86`'s doc comment: "AUDIO_MODEL_PATH is read ONLY here (env-only-in-main)... internal/convert never calls os.Getenv directly," consumed via `convert.SetAudioModelPath(stripInlineComment(os.Getenv("AUDIO_MODEL_PATH")))` before `srv.Start`. Same trust class as `defaultAudioModelPath` (T-30-10, closed in `30-SECURITY.md`) — operator-set process env, never client bytes; no code path assigns `audioModelPath` from any client-controlled value (`grep -n "SetAudioModelPath" internal/convert/whisper.go cmd/audio-worker/main.go` shows only the setter definition and the one os.Getenv-sourced call site). Entry present in Accepted Risks Log below. |
| T-31-10 | `git log --oneline -- internal/convert/audiosniff.go` shows the file's last change is `2a02140 fix(30): remove isom/mp42 MP4-video major brands from m4a allowlist (WR-01)` — a Phase 30 commit; zero commits touch it during Phase 31 (`163fa83`..`d61ca82`, `379f2d4`). Current `m4aBrands` (`audiosniff.go:24-27`) is confirmed still `{"M4A ": true, "M4B ": true}` — the hardened, MP4-video-excluding allowlist from `30-SECURITY.md` T-30-04. Phase 31 only *invokes* this already-hardened detector (`handlers.go:280`); no new detection logic was added. Entry present in Accepted Risks Log below. |
| T-31-SC | `git show --stat` on all 15 Phase-31 commits (`8033e37`, `e7bf1e4`, `9ffb9ec`, `fefcdf0`, `163fa83`, `03fba94`, `7b868d0`, `7a38b71`, `1f4863e`, `d61ca82`, plus the 6 review-fix commits `548c4d5`, `a7c92fd`, `50a60be`, `ac2f074`, `382ae85`, `379f2d4`) shows zero `go.mod`/`go.sum` entries in any diff. `go.mod`/`go.sum`'s most recent change predates Phase 31 entirely (`3972e26 feat(21-02)`, Phase 21). Entry present in Accepted Risks Log below. |

---

## Unregistered Flags

None. `grep -n "Threat Flag" .planning/phases/31-queue-worker-routing-integration/31-0*-SUMMARY.md` returns no matches — none of the four plan SUMMARYs contain a `## Threat Flags` section, so there is no executor-flagged new attack surface to reconcile against the register.

The code-review (`31-REVIEW.md`) surfaced six `warning`-severity findings (WR-01..WR-06) beyond the plans' original threat_model text; all six were fixed in dedicated commits before this audit (verified above: `548c4d5`, `a7c92fd`, `50a60be`, `ac2f074`, `382ae85`, `379f2d4`) and are folded into the T-31-07/T-31-08/T-31-01 evidence rows above since they strengthen those threats' mitigations rather than introduce new ones. Four `info`-severity findings remain open, tracked as non-blocking (not threat-register entries, no client-facing exploitability):
- **IN-01** (`worker.go:701`): `ErrAudioDurationExceeded`'s detail is stored under the misleading key `"engine_stderr"` — diagnostics-only cosmetic issue, no security impact.
- **IN-02** (`internal/queue/client.go`, `.env.example`): `AudioUniqueTTL`'s soundness depends on `AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY` being set identically across the api/webhook-worker/audio-worker processes; drift would reopen a T-31-09-adjacent double-processing window. Pre-existing property shared with document/html engines, not a new Phase-31 defect — operator-documentation gap, tracked for a future `.env.example` sentence, not a code fix.
- **IN-03** (`internal/api/handlers.go:280-284`): a genuine `SniffAudio` transport read error collapses into the 422 "unrecognized file content" path instead of the 400 "invalid multipart form" path Sniff-layer errors take. Cosmetic status-code inconsistency, not an integrity or availability gap — the upload is still rejected either way.
- **IN-04** (`docker-compose.yml`): no `audio-worker` compose service exists yet, so a locally-composed stack accepts audio jobs it cannot complete until Phase 32 adds the service/Dockerfile. Explicitly scope-fenced to Phase 32 in `31-04-PLAN.md`'s interfaces section ("No Dockerfile.audio-worker, no compose service (Phase 32)") — a deliberate, documented deferral, not an oversight, and does not weaken any mitigate-disposition threat above (the live E2E proof used `go run` directly, matching the phase's stated scope).

None of these four rise to BLOCKER severity under this phase's `block_on: high` config — they are diagnostics/documentation/deployment gaps, not absent mitigations for any registered threat.

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-31-01 | T-31-06 | `AUDIO_MODEL_PATH` is an operator-set process environment variable, consumed once at process startup via `SetAudioModelPath` before any asynq worker goroutine starts reading it (happens-before, no mutex needed). No code path in `internal/convert` or `cmd/audio-worker` ever derives this value from client-controlled input (job opts, filenames, or upload bytes) — same trust class as `defaultAudioModelPath`, already closed as T-30-10 in `30-SECURITY.md`. The risk would only materialize if a future phase added a client-controllable model-selector opt, which Phase 30's `audioopts.go` doc comments already flag as an obligation for that future work. | Plan 01/02 authors (register-authored-at-plan-time) | 2026-07-18 |
| AR-31-02 | T-31-10 | `SniffAudio`'s m4a-brand detector was hardened in Phase 30 (T-30-04: `m4aBrands` reduced to `{M4A , M4B }` only, excluding `isom`/`mp42` MP4-video major brands, with `TestMatchM4A_MP4VideoStyleFtypRejected` proving the video-misdetection case is closed). Phase 31 only wires an API-layer call to this already-hardened detector (`convert.SniffAudio(rest)`); no new magic-byte detection logic was authored this phase, confirmed by `audiosniff.go`'s git history showing zero Phase-31 commits. | Plan 03 author (register-authored-at-plan-time) | 2026-07-18 |
| AR-31-03 | T-31-SC | No new external Go module was added to satisfy any Phase 31 requirement — the audio vertical reuses `chi`/`asynq`/`pgx`/`minio-go`, already present and used identically by the three prior engine classes (image/document/html). Verified via `git show --stat` on every Phase-31 commit (task commits + all 6 code-review fix commits): zero `go.mod`/`go.sum` diffs. | Plan 01/02/03/04 authors (register-authored-at-plan-time) | 2026-07-18 |

*Accepted risks do not resurface in future audit runs.*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-18 | 12 | 12 | 0 | gsd-security-auditor |

**Audit notes:**

- All 4 plan `<threat_model>` blocks loaded (31-01 through 31-04); no duplicate threat IDs across plans, and every threat ID from the task's `<threat_register>` prompt table is present and dispositioned identically to the plans' own text.
- **T-30-08 carry-forward explicitly confirmed CLOSED.** `30-SECURITY.md`'s residual note stated: "ordering is documented convention only, not enforced by any code path in this phase... Phase 31's audit must show the guard is actually invoked before Convert, not just documented." Verified in code, not documentation: `internal/worker/worker.go:905` — `HandleAudioConvert` → `h.process` → `enforceAudioGuardBeforeConvert(attemptCtx, job.Engine, inPath, h.audioMaxDuration, func() error { conv.Convert(...) })`, called strictly between `downloadTo` (line 893) and the `convertFn()` closure invocation (`enforceAudioGuardBeforeConvert`'s own body, `worker.go:855`, only reached after the `EnforceMaxDuration` gate passes). Pinned by a real (not mocked) unit test, `TestEnforceAudioGuardBeforeConvert_IN02` (`worker_test.go:366`), which drives an actual over-ceiling fixture through the real `convert.EnforceMaxDuration`/ffprobe path and asserts the fake `convertFn` is never called. This is the guard genuinely running in the request path, not a doc comment.
- Verified against the CURRENT (post-code-review) implementation, not the plans' originally-declared mechanisms, per the task's explicit instruction that WR-01..WR-06 strengthened several mitigations after the plans were written:
  - T-31-07/T-31-08: the plans' original `isAudioTerminal` body (Task 1 of 31-02) did not classify ffprobe-stage errors at all (WR-01 gap) and ran the duration guard's ffprobe under the full attempt-ctx budget rather than a bounded probe timeout (WR-02 gap). Both fixed post-review (`548c4d5`, `a7c92fd`) and re-verified against the current code, not the stale plan text — `audioProbeTimeout = 15 * time.Second` (`worker.go:829`) and the ffprobe-stage terminal arms (`worker.go:307-324`) are both present and tested.
  - T-31-08: WR-03 (`terminalAudioSignatures`, commit `50a60be`) and WR-04 (`minFfmpegBudget` floor in `whisper.go`, commit `ac2f074`) both strengthen the ffmpeg-stage terminal classification's correctness (closing an accidental cross-engine-signature dependency and an upstream-stall misattribution respectively) without changing the mitigation's fundamental shape (`"audio: ffmpeg:"` prefix → terminal). Verified the fixed code and its dedicated pinning tests (`TestIsAudioTerminalOutputSignatures`, `TestAudioConverter_InsufficientBudgetFailsFast`), not the plan's pre-fix description.
  - T-31-05/T-31-11: WR-05 (`envDurationSeconds`, commit `382ae85`) and WR-06 (`stripInlineComment`, commit `379f2d4`) harden `cmd/audio-worker/main.go`'s env parsing (a `AUDIO_MAX_DURATION_SECONDS` bare-integer footgun and an `AUDIO_MODEL_PATH` inline-comment footgun respectively) that could otherwise have silently weakened the T-31-01 duration ceiling or misdirected T-31-06's model path. Both fixes verified present in the current `cmd/audio-worker/main.go` (lines 167-217) with dedicated tests (`TestEnvDurationSeconds`, `TestStripInlineComment` in `cmd/audio-worker/main_test.go`).
- T-31-05's live E2E evidence was read from `31-04-SUMMARY.md` per the task's explicit instruction ("T-31-05... proof was live") rather than re-derived by grep — the `\d jobs` output and the `202`-status `curl` transcript in that SUMMARY are the audit evidence, cross-checked against the migration file's static text for consistency (both list `'audio'` in the same position of the same allow-list).
- All 15 Phase-31 commits (10 plan-task commits + 5... actually 6 review-fix commits, 16 total counting metadata/docs commits) inspected via `git show --stat` for `go.mod`/`go.sum` diffs — zero found, closing T-31-SC without relying on RESEARCH.md's stated intent alone.
- Full test suite for every package touched this phase re-run live during this audit (not assumed from SUMMARY claims): `go build ./...` and `go test ./internal/worker/... ./internal/api/... ./internal/reconciler/... ./internal/queue/... ./internal/convert/... ./cmd/audio-worker/...` — all green.
- No `unregistered_flag` entries found — none of the four plan SUMMARYs contain a `## Threat Flags` section (`grep` returned zero matches across all four).
- Four `info`-severity code-review findings (IN-01..IN-04) remain open by design (non-blocking per the review's own classification and this phase's `block_on: high` config) — recorded under Unregistered Flags above for visibility, not counted as open threats since none map to a registered threat ID and none represent an absent mitigation for a disposition this phase committed to.
- Implementation files (`internal/worker/worker.go`, `internal/api/handlers.go`, `internal/reconciler/reconciler.go`, `internal/queue/{queue,client}.go`, `internal/convert/{whisper,audioduration,converters,audiosniff}.go`, `cmd/audio-worker/main.go`, `internal/db/migrations/0006_audio_engine.sql`, and all `_test.go` siblings) were read-only for this audit; no implementation code was modified.

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-07-18
