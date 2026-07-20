# Phase 35: Queue, Worker & Routing Integration - Context

**Gathered:** 2026-07-21
**Status:** Ready for planning

<domain>
## Phase Boundary

Video jobs become reachable from outside for the first time. This phase wires the already-built, deliberately-unregistered `AVConverter` (Phase 34) into the async lifecycle: a dedicated `av` asynq queue, `cmd/av-worker`, API and reconciler routing, an av-specific transient/terminal classifier, and `AVUniqueTTL`. In parallel, video containers gain transcript support by riding the existing `audio` queue/worker.

**Not in this phase:** the av-worker Docker image and the RTF-measured `AV_ENGINE_TIMEOUT` (Phase 36); Helm/KEDA (Phase 37).

</domain>

<decisions>
## Implementation Decisions

### Retry classification (av)

- **D-01: The classifier keys on typed sentinel errors, not string prefixes.** `av.go` must emit distinct sentinels per operation (e.g. `ErrAVTranscodeFailed` / `ErrAVExtractFailed` / `ErrAVThumbnailFailed`) instead of the shared `"av: ffmpeg: %w"` wrapper it uses today, and the classifier matches with `errors.Is`. **This is a required edit to Phase 34 code** — `av.go:481` (transcode), `:528` (audio-extract) and `:566` (thumbnail) all currently emit the *identical* `"av: ffmpeg:"` prefix, so a string-keyed classifier is structurally incapable of telling them apart. Phase 34 tests asserting on those strings must be updated in the same change. Error *wrapping* changes; conversion behavior does not.

- **D-02: Terminal/transient policy.** Transcode timeout → **transient** (it is the expensive operation; a timeout may simply mean the budget ran out under load). Thumbnail and audio-extract timeouts → **terminal** (cheap operations; a timeout there indicates a pathological input). All deterministic failures → terminal: undecodable/malformed input, missing or empty output (`ErrAVOutputMissingOrEmpty`), timecode out of range (`ErrAVTimecodeOutOfRange`), duration/resolution guard rejections.
  - Do NOT port audio's rule verbatim. `isAudioTerminal` (`worker.go:292-337`) treats *any* `"audio: ffmpeg:"` failure as terminal because ffmpeg is audio's cheap normalize stage. Applied to av, that rule would make every engine failure terminal, directly contradicting the transcode-timeout decision above.

- **D-03: Retry budget — fewer attempts, longer pauses.** `AV_MAX_RETRY=2` with a schedule around 30s/2m (vs audio's 3 × 5s/15s/30s). Three executions at full timeout instead of four. The long first backoff lets load drain rather than hammering a busy worker 5 seconds later.
  - **Sequencing note:** `AVUniqueTTL` derives from `AV_ENGINE_TIMEOUT`, which is only *measured* in Phase 36. Phase 35 necessarily uses a provisional timeout value; Phase 36 recomputes. The formula `(maxRetry+1)*timeout + backoffSum + margin` is unaffected — only the input changes. Reuse the shared `uniqueTTLSafetyMargin` (`queue.go:350`), do not introduce a per-engine margin.

### Video-to-transcript coverage

- **D-04: All five containers get transcription** — `{mp4, mov, avi, mkv, webm} × {txt, srt, vtt, json}` added to `audioSourceFormats` (`whisper.go:67-70`). The ffmpeg normalize stage already demuxes any container ffmpeg can decode, so restricting to a subset would be arbitrary. Keeps the source set symmetric with `AVConverter`.

- **D-05: Raise `minFfmpegBudget` for video sources rather than raising `AUDIO_ENGINE_TIMEOUT`.** Demuxing a multi-gigabyte mkv is materially heavier than demuxing an mp3, and the current guaranteed stage-1 budget is a flat `minFfmpegBudget = 30s` (`whisper.go:90`). Make that floor larger when the source is a video container. This targets the actual difference (demux cost) without inflating the class-wide timeout, which would slow pure-audio jobs and force a recompute of `AudioUniqueTTL` for the whole class.

### Plumbing seams (engine-class wiring)

- **D-06: Mirror the audio pattern by hand, but add a completeness test.** Do not refactor the API/reconciler routing switches into a shared `queueForEngine` helper in this phase — that touches the hot paths of four working engine classes for the sake of a fifth. Instead, close the risk with a test that iterates every engine constant in `convert.go:19-25` and asserts each one has: a case in the API enqueue switch (`handlers.go:526-543`), a case in the reconciler routing switch (`reconciler.go:284-303`), and an entry in the queue-depth collector list.
  - The collector list at `cmd/api/main.go:92` is the specific reason this test is required: it is a **variadic call**, so omitting `QueueAV` produces no compile error — it silently drops the metric KEDA scales the worker on. The other two switches at least fail closed at runtime.

### Detection chain and upload limits

- **D-07: Two-tier upload ceiling.** Raise the global hard cap (`MAX_UPLOAD_BYTES`, currently 100 MiB) to a video-appropriate value — it protects memory/disk and is enforced by `http.MaxBytesReader` at `handlers.go:93`, *before* parsing and therefore before the engine class is known. Then add a **second, per-engine check after format detection**: a non-video upload exceeding its own class ceiling gets 413 before any S3 write. This keeps video uploadable without weakening the DoS posture of the four existing engine classes.

- **D-08: Wire `SniffVideo` into the detection chain in the same change that registers `AVConverter`.** Carried forward from the Phase 34 code review (WR-02, deliberately deferred). Today mp4/mov/avi are detectable via the `signatures` table but mkv/webm are not detectable at all, and `SniffVideo` has zero non-test callers. Registering the converter without wiring the sniffer would ship an engine for formats the service cannot recognize.

- **D-09: Map `ErrAVTimecodeOutOfRange` to 4xx, not 5xx.** Carried forward from the Phase 34 contract decision. An explicit out-of-range thumbnail timecode is a client error and is deliberately *not* clamped; without an API-layer mapping a client typo surfaces as an internal error.

### Claude's Discretion

- Exact sentinel error names and their placement in `av.go`.
- Exact numeric values for the raised `minFfmpegBudget` (video), the raised global `MAX_UPLOAD_BYTES`, the per-engine ceilings, and the provisional `AV_ENGINE_TIMEOUT`.
- The shape of the completeness test (table-driven over engine constants vs. reflection over the switch).
- Where exactly `SniffVideo` slots into the existing detection chain order (`handlers.go:188` `Sniff` → `:202` `SniffContainer` → `:228` html → `:280` `SniffAudio`).

### Open questions for research (raised, not decided)

- **Video with no audio track submitted for transcription** — whisper would receive an empty input. Needs a defined behavior (fail-closed 422 at detection? terminal engine error? empty transcript?). Raised during discussion; deliberately not decided here.
- **Multiple audio tracks in one container** — which track feeds whisper. Same class of question.
- **`HasDimensionLimit` vs video** — appears to be a non-issue (it is scoped to image formats, so video skips the block, and the video resolution guard correctly lives in the worker via `EnforceMaxResolution`). Planner should confirm rather than assume.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Phase 34 outputs — the code this phase wires up
- `.planning/phases/34-av-engine-foundation/34-REVIEW.md` — 17 findings; WR-02 (`SniffVideo` unwired) and the `ErrAVTimecodeOutOfRange` mapping are explicitly deferred *to this phase*
- `.planning/phases/34-av-engine-foundation/34-REVIEW-FIX.md` — what was fixed and what was skipped, with rationale; documents the `AVOpts.Timecode *float64` contract
- `.planning/phases/34-av-engine-foundation/34-SECURITY.md` — 17/17 threats closed, ASVS 2; the AVE-02 "every ffmpeg/ffprobe invocation is protocol-whitelisted" invariant must not regress when new call sites are added
- `.planning/phases/34-av-engine-foundation/34-VERIFICATION.md` — verified post-fix state of the converter

### Project-level decisions binding on this phase
- `.planning/STATE.md` §Accumulated Context — the video→transcript routing decision ("do NOT resolve differently later"), the stage-aware-classification warning, and the Phase 35 hard inputs recorded at Phase 34 close
- `.planning/ROADMAP.md` §Phase 35 — the four success criteria
- `.planning/REQUIREMENTS.md` — AVE-03, AVT-01

### Codebase patterns to mirror
- `cmd/audio-worker/main.go` — the worker-binary template. Note the env-setter happens-before boundary (setters must run before `srv.Start`) and `ShutdownTimeout = ENGINE_TIMEOUT + 10s`, which overrides asynq's silent 8s default
- `internal/queue/queue.go:471-505` — `AudioUniqueTTL` derivation and the `maxRetry+1` correction
- `internal/queue/queue_test.go:406-436` — `TestAudioUniqueTTL`, the test shape to mirror (exact value + monotonicity + lower bound)
- `internal/worker/worker.go:255-337` — `isAudioTerminal`, the classifier to learn from but explicitly NOT to copy
- `internal/worker/worker.go:844-858` — `enforceAudioGuardBeforeConvert`, the per-engine guard special case inside otherwise engine-agnostic `process()`; av likely needs a sibling branch
- `internal/reconciler/reconciler.go:284-303` — routing switch whose `default:` comment names `av` as the next engine to add

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `EngineAV = "av"` already exists (`convert.go:24`) — the engine constant is not new work.
- `AVConverter` with `Pairs()/Engine()/Convert()`, `avopts.go` (`ParseAVOpts`, `ValidateAVApplicability`, already gated on `engine != EngineAV`), `avsniff.go` (`SniffVideo`), `avduration.go` (duration + resolution guards) — all built, tested against live ffmpeg 8.1.2, and deliberately unregistered.
- **No DB migration needed:** `'av'` has been in the `jobs_engine_check` constraint since `internal/db/migrations/0001_init.sql:48`.
- `uniqueTTLSafetyMargin` (`queue.go:350`) is shared across engines — reuse, don't duplicate.
- Env-parsing helpers in `cmd/audio-worker/main.go` (`envInt`, `envDuration`, `envDurationSeconds`, `stripInlineComment`) copy verbatim.

### Established Patterns
- Engine-class string *values* are centralized in `convert.go:19-25`; queue names alias them (`queue.go:33-39`) so a queue name cannot drift from its engine.
- `internal/convert` never calls `os.Getenv` — configuration enters via setters called from `main()` before the server starts.
- Postgres-first ordering on terminal failure: `MarkFailed` → webhook enqueue only if the DB write succeeded → `asynq.SkipRetry`. Transient path returns the bare error with no `MarkFailed` and no outcome recording (avoids double-counting retries).
- `RetryDelayFunc` (`queue.go:330-345`) dispatches on task type with a `default:` that silently falls back to asynq's own schedule — **a new task type that forgets its case gets asynq defaults with no error.**

### Integration Points
- ~18 hand-maintained seams must gain an `av` entry: engine const (done), queue name, task type, `NewAVConvertTask`, retry schedule + delay, `RetryDelayFunc` case, backoff sum + unique TTL, client fields/env reads/`EnqueueAVConvert`, API opts-dispatch case, API enqueue case, reconciler `enqueuer` interface method + routing case, worker `HandleAVConvert`, av terminal classifier, per-engine guard in `process()`, `converters.go` registration, queue-depth collector arg list, and the `cmd/av-worker` binary.
- **Registration collision hazard:** `Registry.Register` (`convert.go:74-80`) is a bare map assignment — later registrations silently override earlier ones for the same pair, with no error, panic, or log. Adding `Default.Register(AVConverter{})` after `AudioConverter` means AV silently wins any shared pair. The only symptom would be jobs routed to the wrong queue. **The pair-disjointness test is the sole guard; there is no runtime one.**

</code_context>

<specifics>
## Specific Ideas

- The completeness test (D-06) should be understood as the structural replacement for a refactor, not as optional polish — it is what makes "mirror by hand" a defensible choice rather than an accumulation of debt.
- Phase 34's live-binary test style (`requireLiveAVBinaries` skip-gate) is the precedent for any av tests here that need real ffmpeg.

</specifics>

<deferred>
## Deferred Ideas

- **Centralizing engine→queue routing behind a `queueForEngine` helper** — explicitly considered and declined for this phase (D-06). Worth revisiting when a sixth engine class appears, or as standalone tech-debt work; the completeness test makes the current duplication safe but does not make it good.

</deferred>

---

*Phase: 35-Queue, Worker & Routing Integration*
*Context gathered: 2026-07-21*
