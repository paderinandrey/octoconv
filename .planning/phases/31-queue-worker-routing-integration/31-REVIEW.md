---
phase: 31-queue-worker-routing-integration
reviewed: 2026-07-18T15:16:34Z
depth: standard
files_reviewed: 12
files_reviewed_list:
  - cmd/audio-worker/main.go
  - internal/api/api.go
  - internal/api/handlers.go
  - internal/worker/worker.go
  - internal/reconciler/reconciler.go
  - internal/queue/queue.go
  - internal/queue/client.go
  - internal/convert/converters.go
  - internal/convert/whisper.go
  - internal/convert/audioduration.go
  - internal/db/migrations/0006_audio_engine.sql
  - .env.example
findings:
  critical: 0
  warning: 6
  info: 4
  total: 10
fixed: 6
status: fixes_applied
---

# Phase 31: Code Review Report

**Reviewed:** 2026-07-18T15:16:34Z
**Depth:** standard
**Files Reviewed:** 12
**Status:** fixes_applied (all 6 warnings fixed 2026-07-18; IN-01..IN-04 remain tracked, out of fix scope)

## Summary

Reviewed the Phase 31 audio queue/worker/routing integration across all 12
in-scope files, cross-referencing the non-scope call targets they depend on
(`internal/convert/convert.go`, `audiosniff.go`, `olecfb.go`, `htmlsniff.go`,
`dimensions.go`, `sniff.go`, `exec.go`, `cmd/chromium-worker/main.go`,
`cmd/webhook-worker/main.go`, `docker-compose.yml`, migration 0005, and
`internal/worker/worker_test.go`). Build, `go vet`, and all five affected
package test suites pass.

Facts verified sound (checked, not assumed):

- **SniffAudio splice (T-31-02):** correctly chained off `rest`, and the
  upstream ZIP/HTML/CFB checks all read via `io.ReaderAt` (`ReadAt(buf, 0)`),
  never disturbing the sequential cursor `rest` wraps. On a sniff miss the
  partially-consumed `rest` is never used again — every path reaches the
  fail-closed 422. No unreachable code, no offset corruption.
- **AudioUniqueTTL vs worst-case lifetime (T-03-10):** for the defaults
  (AUDIO_MAX_RETRY=3, AUDIO_ENGINE_TIMEOUT=600s), TTL = 4×600s + 50s + 120s
  = 2570s vs worst-case attempt lifetime 4×600s + 50s = 2450s. The 120s
  margin holds, and the derivation scales with env changes within one
  process (but see IN-02 for cross-process drift).
- **whisper.go's `NormalizeFormat(filepath.Ext(outPath))`:** NormalizeFormat
  strips the leading dot (`convert.go:50`), so the fail-fast target check is
  reachable and correct — not the dead-`".txt"`-mismatch bug it superficially
  resembles.
- **Reconciler switch:** `EngineAudio` case present, fail-closed
  `unroutable_engine` default retained; enqueuer interface extended
  consistently.
- **Migration 0006:** byte-for-byte mirror of the live-confirmed 0005
  drop/re-add pattern; only the added `'audio'` literal differs.
- **cmd/audio-worker:** faithful structural mirror of cmd/chromium-worker
  (env helpers, ShutdownTimeout = engine timeout + 10s, no sweeper,
  metrics listener, graceful shutdown ordering). `SetAudioModelPath` runs
  before `srv.Start`, satisfying the happens-before requirement.
- **NewHandler wiring:** nil webhookRepo/deliverer/signingSecret and 0
  presignTTL are genuinely never read by `HandleAudioConvert`; the enqueuer
  (needed for webhook enqueue on terminal failure) is correctly non-nil.
- **MIMEType** covers all four audio inputs and all four transcript outputs.

The findings below concentrate on the stage-aware classifier's blind spots
(ffprobe-stage errors, accidental cross-engine signature matches), the
duration guard's unbounded ffprobe invocation, and two env-var handling traps.

## Narrative Findings (AI reviewer)

## Warnings

### WR-01: Deterministic ffprobe-stage failures classify transient, burning the full retry + reconciler budget

**File:** `internal/worker/worker.go:256-272` (isAudioTerminal), `internal/convert/audioduration.go:38-79`
**Issue:** The stage-aware classifier handles three cases: `ErrAudioDurationExceeded` (terminal), `"audio: ffmpeg:"` prefix (terminal), everything else → shared `isTerminal` (transient). But the duration guard runs a *third* stage — ffprobe — whose errors carry the prefix `"ffprobe:"` (`fmt.Errorf("ffprobe: %w", err)`, `"ffprobe: unparseable duration %q"`, `"ffprobe: implausible duration %v"`). None of these match any classifier arm, so a corrupt-but-sniffable audio file (valid magic bytes, broken container metadata — exactly the adversarial-input class Key Decision 1 declares terminal at the ffmpeg stage) is classified **transient**. The job burns AUDIO_MAX_RETRY attempts (up to ~41 min with the 600s timeout), stays `active`, then cycles through up to RECONCILER_MAX_RECOVERIES reconciler requeues (15m staleness each) before finally failing hours later as `reconciler_exhausted` — the wrong error code for what was a deterministic bad-input rejection at the very first probe. The ffprobe stage runs *before* ffmpeg on the same input, so this is an internal inconsistency in the classifier's own stated philosophy, and `worker_test.go`'s otherwise-thorough `isAudioTerminal` test has no ffprobe-error case.
**Fix:** Classify at minimum the deterministic parse-level ffprobe failures terminal, mirroring the ffmpeg arm:
```go
// after the "audio: ffmpeg:" check in isAudioTerminal:
if strings.Contains(msg, "ffprobe: unparseable duration") ||
	strings.Contains(msg, "ffprobe: implausible duration") ||
	strings.Contains(msg, "ffprobe failed:") {
	// duration-guard stage: malformed container metadata — terminal.
	return true
}
```
(Keep `"start ffprobe:"` / `"ffprobe killed:"` — environment/timeout shapes — transient if desired, but document the split.) Add the corresponding cases to `TestIsAudioTerminal`.

**Status:** fixed
**Resolution:** commit 548c4d5 — added a terminal arm in `isAudioTerminal` for the deterministic ffprobe shapes audioduration.go actually emits (`"ffprobe failed:"`, `"ffprobe: unparseable duration"`, `"ffprobe: implausible duration"`); `"start ffprobe:"`/`"ffprobe killed:"` documented and pinned transient. Both sides covered in `TestIsAudioTerminal`.

### WR-02: ProbeDuration runs under the full AUDIO_ENGINE_TIMEOUT, violating its own documented short-bound contract (T-30-03)

**File:** `internal/worker/worker.go:765-772` (enforceAudioGuardBeforeConvert), `internal/convert/audioduration.go:49-55`
**Issue:** `ProbeDuration`'s doc comment is explicit: "ctx should carry a SHORT bound distinct from the full engine timeout … it must never be allowed to run for the full AUDIO_ENGINE_TIMEOUT budget — see T-30-03." The only production caller, `enforceAudioGuardBeforeConvert`, passes `attemptCtx` — the full 600s whole-attempt deadline — straight through. A hung/adversarial ffprobe (the guard's own threat model: the probe runs on untrusted input *before* any decode) can now consume the entire attempt budget, and does so on every retry. The documented invariant exists nowhere in code.
**Fix:**
```go
if engine == convert.EngineAudio {
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second) // ffprobe reads container metadata; near-instant even for large files
	err := convert.EnforceMaxDuration(probeCtx, inPath, audioMaxDuration)
	cancel()
	if err != nil {
		return err
	}
}
```
Note the interaction with WR-01: a probe-timeout expiry then surfaces as `"ffprobe killed: context deadline exceeded"` and needs a deliberate classification decision.

**Status:** fixed
**Resolution:** commit a7c92fd — `enforceAudioGuardBeforeConvert` now derives a 15s probe-only deadline (`audioProbeTimeout` const, documented) from the attempt ctx before calling `EnforceMaxDuration`. Deliberate classification decision: a probe-ctx expiry (`"ffprobe: ffprobe killed:"`) stays TRANSIENT (WR-01's documented split), pinned by `TestEnforceAudioGuardProbeExpiryTransient_WR02`.

### WR-03: Accidental cross-engine signature matches silently make whisper-stage "no output" failures terminal, contradicting isAudioTerminal's documented contract

**File:** `internal/worker/worker.go:51-66,88-92,256-272`, `internal/convert/whisper.go:231-240`
**Issue:** `isAudioTerminal`'s doc comment (and 31-RESEARCH.md A2, cited in it) asserts that non-timeout whisper-stage failures fall through to `isTerminal` and stay **transient** ("no dedicated terminalWhisperSignatures list is introduced this phase"). That is false for the exit-0-but-no-output failure mode: `validateAudioOutput` returns `"audio: output is empty"` and `"audio: stat output: …"`, which substring-match `terminalLibreOfficeSignatures`/`terminalChromiumSignatures` entries `"output is empty"` and `"stat output"` inside the shared `isTerminal` loop — so these audio failures classify **terminal** via a foreign engine's signature list. The outcome is plausibly the desirable one (deterministic empty output should not retry), but it is achieved by accident: rewording a LibreOffice/Chromium signature or `validateAudioOutput`'s message would silently flip audio retry behavior with no failing test (worker_test.go pins `"convert: chromium: output is empty"` but no audio-prefixed variant). The same mechanism cuts the other way: whisper-cli/ffmpeg stderr is folded verbatim into the error (`exec.go:52`) and matched against *all four* engine signature lists, so any engine's stderr containing e.g. "output is empty" or "no export filter for" flips transient→terminal.
**Fix:** Introduce an explicit `terminalAudioSignatures = []string{"audio: output is empty", "audio: stat output"}` checked in `isAudioTerminal` (before the `isTerminal` fallthrough), correct the doc comment's "stays transient" claim, and add pinning tests for both messages — same commit-coupling discipline the LibreOffice/veraPDF lists already document.

**Status:** fixed
**Resolution:** commit 50a60be — introduced `terminalAudioSignatures` exactly as suggested, checked in `isAudioTerminal` before the shared fallthrough; corrected the doc comment's stale "stays transient" claim. `TestIsAudioTerminalOutputSignatures` pins both messages terminal via the audio path AND re-verifies with the LibreOffice/Chromium lists emptied (independence proof — rewording a foreign signature can no longer flip audio behavior).

### WR-04: ffmpeg-stage terminal-on-timeout can permanently misclassify upstream budget exhaustion as corrupt input

**File:** `internal/worker/worker.go:266-269,786-828`
**Issue:** Key Decision 1 classifies any `"audio: ffmpeg:"` error — including a timeout kill — as terminal, on the premise that an ffmpeg-stage timeout signals malformed/adversarial input. But `attemptCtx` is a single whole-attempt deadline covering the S3 download and ffprobe as well (worker.go:786). If a transient S3 stall consumes most of the 600s budget and the download then completes, ffmpeg is killed near-instantly with `"convert: audio: ffmpeg: ffmpeg killed: context deadline exceeded"` → terminal → the job is permanently MarkFailed with `engine_error` / "unsupported or corrupted input format" and never retried — for a failure that was actually network-transient and would have succeeded on retry. The document/html engines share the whole-attempt deadline but classify *all* timeouts terminal by design, so they cannot be internally inconsistent this way; audio's stage-aware split introduces the possibility that the stage blamed is not the stage that consumed the budget.
**Fix:** Cheapest mitigation: before invoking ffmpeg, require a minimum remaining budget so a near-exhausted deadline surfaces as a transient generic timeout instead of an ffmpeg-stage terminal:
```go
// in whisper.go Convert, before the stage-1 runCommand:
if dl, ok := ctx.Deadline(); ok && time.Until(dl) < minFfmpegBudget {
	return fmt.Errorf("audio: insufficient attempt budget remaining: %w", context.DeadlineExceeded)
}
```
Alternatively, document this explicitly as an accepted residual of Key Decision 1 (it currently isn't — the doc comments attribute ffmpeg-stage timeouts solely to input badness).

**Status:** fixed
**Resolution:** commit ac2f074 — implemented the review's cheapest mitigation: `whisper.go`'s `Convert` now requires `minFfmpegBudget` (30s) remaining on the attempt ctx before starting stage 1; below the floor it returns `"audio: insufficient attempt budget remaining: %w(context.DeadlineExceeded)"` — no `"audio: ffmpeg:"` prefix, so it classifies transient and asynq retries. Residual documented in the code comment: an upstream stall leaving >= 30s can still be misattributed to ffmpeg; the floor removes only the near-total-exhaustion case deterministically. Pinned by `TestAudioConverter_InsufficientBudgetFailsFast` (convert side, ungated) and a transient-classification case in `TestIsAudioTerminal` (worker side).

### WR-05: AUDIO_MAX_DURATION_SECONDS name invites bare-seconds values that silently fall back to the 4h default

**File:** `cmd/audio-worker/main.go:65,152-159`, `.env.example:54`
**Issue:** The variable is named `…_SECONDS` but is parsed by `envDuration` → `time.ParseDuration`, which **rejects** a bare number ("time: missing unit in duration"). An operator who writes `AUDIO_MAX_DURATION_SECONDS=7200` — the exact form the name advertises — gets a silent fallback to the 4-hour default with no log line, weakening (or unexpectedly loosening) a fail-closed resource guard. `.env.example` ships the working-but-self-contradictory `14400s`, i.e. a duration string under a `_SECONDS` name; no other duration env in the codebase carries a `_SECONDS` suffix (peers are `*_TIMEOUT=300s`, `*_STALE_AFTER=15m`).
**Fix:** Rename to `AUDIO_MAX_DURATION=4h` (matching the codebase's duration-env convention) before any deployment depends on the name, or parse it as integer seconds to match the name. At minimum, log a warning when an env value is present but unparseable instead of silently using the default (this fallback pattern is codebase-wide, but here it guards a security ceiling).

**Status:** fixed
**Resolution:** commit 382ae85 — kept the name (no rename churn) and made parsing match it: new `envDurationSeconds` accepts both Go duration syntax (`4h`/`14400s`) and bare non-negative integer seconds (`14400`), with `firstField` inline-comment tolerance; a set-but-unparseable value now logs a warning before defaulting (never silent — security ceiling). `.env.example` ships the bare-seconds form and documents both accepted shapes. Covered by `TestEnvDurationSeconds` (new `cmd/audio-worker/main_test.go`).

### WR-06: AUDIO_MODEL_PATH read without firstField while .env.example ships it with an inline comment — env-file loaders would pass a garbage path to whisper-cli

**File:** `cmd/audio-worker/main.go:80`, `.env.example:55`
**Issue:** Every numeric/duration env in this codebase is defended against trailing inline `# comments` via `firstField`, precisely because non-shell loaders (`docker run --env-file`, compose `env_file:`, k8s configmap-from-env-file) do **not** strip them. `AUDIO_MODEL_PATH` is read raw (`convert.SetAudioModelPath(os.Getenv("AUDIO_MODEL_PATH"))`) yet is the only string env in `.env.example` documented *with* a long inline comment on the same line (line 55). Under an env-file-style loader, `audioModelPath` becomes `/models/ggml-base.bin   # whisper.cpp model path passed to whisper-cli -m; …`, and every transcription fails at whisper-cli model load with a confusing error. Today's compose file uses inline `environment:` maps (and has no audio-worker service yet — see IN-04), so this is latent, but the Phase 32 container wiring is exactly when an env_file is most likely to appear.
**Fix:** Either strip the value defensively (`strings.TrimSpace` + the same inline-comment convention: `convert.SetAudioModelPath(firstField(os.Getenv("AUDIO_MODEL_PATH")))` — noting paths with spaces would then be unsupported, which is acceptable for an operator-set container path), or move the comment in `.env.example` to its own line above the assignment. Doing both is cheapest.

**Status:** fixed
**Resolution:** commit 379f2d4 — did both, with one improvement over the suggested `firstField`: new `stripInlineComment` only cuts at a `#` preceded by whitespace (the only shape a .env-style inline comment takes), so paths containing spaces or embedded `#` survive intact. `.env.example`'s comment moved to its own lines above the assignment. Covered by `TestStripInlineComment`.

## Info

### IN-01: Duration-guard rejection detail stored under the misleading key "engine_stderr"

**File:** `internal/worker/worker.go:636`
**Issue:** For `ErrAudioDurationExceeded`, `MarkFailed`'s detail map records the guard error under `"engine_stderr"`, but no engine ran and there is no stderr — the value is the ffprobe-derived guard message. Diagnostics readers filtering job_events by that key will misattribute the failure to an engine.
**Fix:** Use a neutral key for the duration branch, e.g. `map[string]any{"duration_error": err.Error()}`.

### IN-02: AudioUniqueTTL soundness silently depends on AUDIO_ENGINE_TIMEOUT/AUDIO_MAX_RETRY matching across three processes; .env.example scopes them to the audio worker only

**File:** `internal/queue/client.go:87-88`, `.env.example:50-55`
**Issue:** `queue.NewClient` derives `audioUniqueTTL` from env independently in the api (enqueue path), webhook-worker (reconciler enqueue path), and audio-worker processes. `.env.example` files AUDIO_ENGINE_TIMEOUT/AUDIO_MAX_RETRY under the header "# Audio worker (cmd/audio-worker; Phase 31)", inviting an operator to raise the timeout on the audio-worker deployment alone — in which case the api/webhook-worker TTLs (computed from the 600s default) undershoot the real worst-case attempt lifetime, reopening the T-03-10 double-processing window the TTL derivation exists to close. Same pre-existing property as the document/html engines, so consistency drift, not a new defect.
**Fix:** Add one sentence to the `.env.example` audio block: "must be set identically in the api, webhook-worker, and audio-worker environments — the uniqueness-lock TTL is derived from these values in every enqueuing process."

### IN-03: SniffAudio transport read errors collapse into the 422 "unrecognized file content" instead of the 400 path Sniff errors take

**File:** `internal/api/handlers.go:280-284`
**Issue:** `convert.Sniff`'s error at line 188-191 maps to 400 "invalid multipart form", but `convert.SniffAudio`'s error (`aerr` — a genuine mid-body read failure; EOF/ErrUnexpectedEOF are already tolerated inside SniffAudio) is discarded by the `aerr == nil &&` condition and falls through to the 422 "unrecognized file content" rejection. Same failure class, two different statuses/messages depending on where in the chain the read broke.
**Fix:** Handle `aerr != nil` explicitly with the same 400 "invalid multipart form" response used for Sniff errors.

### IN-04: No audio-worker service exists in docker-compose.yml — the audio queue has documented env vars but no deployable consumer

**File:** `.env.example:50-55`, `cmd/audio-worker/main.go` (vs `docker-compose.yml` services: api, worker, webhook-worker, document-worker, chromium-worker)
**Issue:** Phase 31 wires the full pipeline (API accepts and enqueues audio jobs; migration accepts engine='audio') but compose defines no audio-worker service, so in the standard local stack an accepted audio job sits queued until reconciler exhaustion. The `AUDIO_MODEL_PATH` comments mark container bake-in as "Phase 32 target," so this appears intentional — recording it so the gap is a tracked deferral, not an oversight: until Phase 32 lands, the API accepts audio jobs it cannot complete in the compose deployment.
**Fix:** Confirm the Phase 32 deferral covers the compose service (not just the model bake-in), or gate audio pair registration until a consumer is deployable.

---

_Reviewed: 2026-07-18T15:16:34Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
