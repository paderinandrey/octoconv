# Phase 35: Queue, Worker & Routing Integration - Research

**Researched:** 2026-07-21
**Domain:** asynq queue/worker wiring, ffmpeg stream selection, upload-size/timeout budgeting for a 5th (video) engine class
**Confidence:** HIGH (codebase-verified line-by-line + live ffmpeg 8.1.2 empirical tests) for wiring/pitfalls; MEDIUM for the numeric budget recommendations (grounded in measurement, explicitly provisional pending Phase 36)

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Retry classification (av)**

- **D-01: The classifier keys on typed sentinel errors, not string prefixes.** `av.go` must emit distinct sentinels per operation (e.g. `ErrAVTranscodeFailed` / `ErrAVExtractFailed` / `ErrAVThumbnailFailed`) instead of the shared `"av: ffmpeg: %w"` wrapper it uses today, and the classifier matches with `errors.Is`. **This is a required edit to Phase 34 code** — `av.go:481` (transcode), `:528` (audio-extract) and `:566` (thumbnail) all currently emit the *identical* `"av: ffmpeg:"` prefix, so a string-keyed classifier is structurally incapable of telling them apart. Phase 34 tests asserting on those strings must be updated in the same change. Error *wrapping* changes; conversion behavior does not.

- **D-02: Terminal/transient policy.** Transcode timeout → **transient** (it is the expensive operation; a timeout may simply mean the budget ran out under load). Thumbnail and audio-extract timeouts → **terminal** (cheap operations; a timeout there indicates a pathological input). All deterministic failures → terminal: undecodable/malformed input, missing or empty output (`ErrAVOutputMissingOrEmpty`), timecode out of range (`ErrAVTimecodeOutOfRange`), duration/resolution guard rejections.
  - Do NOT port audio's rule verbatim. `isAudioTerminal` (`worker.go:292-337`) treats *any* `"audio: ffmpeg:"` failure as terminal because ffmpeg is audio's cheap normalize stage. Applied to av, that rule would make every engine failure terminal, directly contradicting the transcode-timeout decision above.

- **D-03: Retry budget — fewer attempts, longer pauses.** `AV_MAX_RETRY=2` with a schedule around 30s/2m (vs audio's 3 × 5s/15s/30s). Three executions at full timeout instead of four. The long first backoff lets load drain rather than hammering a busy worker 5 seconds later.
  - **Sequencing note:** `AVUniqueTTL` derives from `AV_ENGINE_TIMEOUT`, which is only *measured* in Phase 36. Phase 35 necessarily uses a provisional timeout value; Phase 36 recomputes. The formula `(maxRetry+1)*timeout + backoffSum + margin` is unaffected — only the input changes. Reuse the shared `uniqueTTLSafetyMargin` (`queue.go:350`), do not introduce a per-engine margin.

**Video-to-transcript coverage**

- **D-04: All five containers get transcription** — `{mp4, mov, avi, mkv, webm} × {txt, srt, vtt, json}` added to `audioSourceFormats` (`whisper.go:67-70`). The ffmpeg normalize stage already demuxes any container ffmpeg can decode, so restricting to a subset would be arbitrary. Keeps the source set symmetric with `AVConverter`.

- **D-05: Raise `minFfmpegBudget` for video sources rather than raising `AUDIO_ENGINE_TIMEOUT`.** Demuxing a multi-gigabyte mkv is materially heavier than demuxing an mp3, and the current guaranteed stage-1 budget is a flat `minFfmpegBudget = 30s` (`whisper.go:90`). Make that floor larger when the source is a video container. This targets the actual difference (demux cost) without inflating the class-wide timeout, which would slow pure-audio jobs and force a recompute of `AudioUniqueTTL` for the whole class.

**Plumbing seams (engine-class wiring)**

- **D-06: Mirror the audio pattern by hand, but add a completeness test.** Do not refactor the API/reconciler routing switches into a shared `queueForEngine` helper in this phase — that touches the hot paths of four working engine classes for the sake of a fifth. Instead, close the risk with a test that iterates every engine constant in `convert.go:19-25` and asserts each one has: a case in the API enqueue switch (`handlers.go:526-543`), a case in the reconciler routing switch (`reconciler.go:284-303`), and an entry in the queue-depth collector list.
  - The collector list at `cmd/api/main.go:92` is the specific reason this test is required: it is a **variadic call**, so omitting `QueueAV` produces no compile error — it silently drops the metric KEDA scales the worker on. The other two switches at least fail closed at runtime.

**Detection chain and upload limits**

- **D-07: Two-tier upload ceiling.** Raise the global hard cap (`MAX_UPLOAD_BYTES`, currently 100 MiB) to a video-appropriate value — it protects memory/disk and is enforced by `http.MaxBytesReader` at `handlers.go:93`, *before* parsing and therefore before the engine class is known. Then add a **second, per-engine check after format detection**: a non-video upload exceeding its own class ceiling gets 413 before any S3 write. This keeps video uploadable without weakening the DoS posture of the four existing engine classes.

- **D-08: Wire `SniffVideo` into the detection chain in the same change that registers `AVConverter`.** Carried forward from the Phase 34 code review (WR-02, deliberately deferred). Today mp4/mov/avi are detectable via the `signatures` table but mkv/webm are not detectable at all, and `SniffVideo` has zero non-test callers. Registering the converter without wiring the sniffer would ship an engine for formats the service cannot recognize.

- **D-09: Map `ErrAVTimecodeOutOfRange` to 4xx, not 5xx.** Carried forward from the Phase 34 contract decision. An explicit out-of-range thumbnail timecode is a client error and is deliberately *not* clamped; without an API-layer mapping a client typo surfaces as an internal error.

### Claude's Discretion

- Exact sentinel error names and their placement in `av.go`.
- Exact numeric values for the raised `minFfmpegBudget` (video), the raised global `MAX_UPLOAD_BYTES`, the per-engine ceilings, and the provisional `AV_ENGINE_TIMEOUT`.
- The shape of the completeness test (table-driven over engine constants vs. reflection over the switch).
- Where exactly `SniffVideo` slots into the existing detection chain order (`handlers.go:188` `Sniff` → `:202` `SniffContainer` → `:228` html → `:280` `SniffAudio`).

### Open questions raised in discussion (this research resolves them — see Open Questions section below)

- **Video with no audio track submitted for transcription** — whisper would receive an empty input. Needs a defined behavior (fail-closed 422 at detection? terminal engine error? empty transcript?). Raised during discussion; deliberately not decided there — RESOLVED below via live testing.
- **Multiple audio tracks in one container** — which track feeds whisper. Same class of question — RESOLVED below via live testing, with a recommendation.
- **`HasDimensionLimit` vs video** — appears to be a non-issue (it is scoped to image formats, so video skips the block, and the video resolution guard correctly lives in the worker via `EnforceMaxResolution`). Planner should confirm rather than assume — CONFIRMED below via direct code read.

### Deferred Ideas (OUT OF SCOPE)

- **Centralizing engine→queue routing behind a `queueForEngine` helper** — explicitly considered and declined for this phase (D-06). Worth revisiting when a sixth engine class appears, or as standalone tech-debt work; the completeness test makes the current duplication safe but does not make it good.
- The av-worker Docker image, ffmpeg version pinning, RTF measurement methodology, Helm, and KEDA — Phases 36 and 37, out of this phase's research scope per the orchestrator's explicit scope fence.
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|--------------------|
| AVE-03 | Отдельная av asynq-очередь + `cmd/av-worker` со своим retry schedule и unique-lock TTL из worst-case бюджета; stage-aware transient/terminal классификация выведена заново для видео; reconciler-роутинг по `jobs.engine='av'` | Architecture Patterns (Pattern 1-3), Don't Hand-Roll, Pitfalls 1-4/6-7, Numeric Recommendations table, Code Examples (`isAVTerminal`, sentinel errors) — full wiring seam list verified against HEAD in Recommended Project Structure |
| AVT-01 | Клиент может получить транскрипт видео через `AudioConverter.Pairs()` расширение; непересечение пар AVConverter/AudioConverter закреплено тестом; RTF-допущение `AUDIO_ENGINE_TIMEOUT` перепроверено для видео-источников | Open Questions 1-3 (live-tested no-audio-track/silent-audio/multi-track behavior), Pitfall 7 (pair-disjointness, verified structurally disjoint via target-set analysis), Pitfall 5 (IN-01 misdetection risk), Code Examples (`ffmpegNormalizeArgs` map fix, `minFfmpegBudget` video floor) |
</phase_requirements>

## Summary

This phase is almost entirely **integration**, not new technology: `AVConverter` (Phase 34) is fully built, unit-tested against live ffmpeg 8.1.2, and deliberately unregistered. The work is (1) registering it and wiring its ~18 hand-maintained seams the same way `AudioConverter` was wired in Phases 30-33, (2) extending `AudioConverter.Pairs()` with video-container sources per the locked Key Decision, (3) deriving a stage-aware transient/terminal classifier for `av` that a straight copy of `isAudioTerminal` would get wrong, and (4) picking defensible provisional numbers for values Phase 36 will later replace with RTF-measured ones.

Three research findings materially change the plan's shape versus what CONTEXT.md assumed, all verified against HEAD and/or live ffmpeg:

1. **The D-01 sentinel refactor's test blast radius is smaller than assumed.** Zero existing `av_test.go` assertions pin the literal `"av: ffmpeg:"` string (grep-verified across every `err.Error()`/`errors.Is` call in the file) — no Phase 34 test needs to change, only new tests need to assert the new sentinels.
2. **`worker.process()` needs no AV-specific branch.** Unlike audio's duration guard (spliced into `process()` via `enforceAudioGuardBeforeConvert`, gated on `job.Engine == EngineAudio`), AV's duration+resolution guard is already **self-contained inside `AVConverter.Convert()`** (`avProbeSource` → `enforceMaxDurationOf`/`enforceMaxResolutionOf`, `av.go:388-400`). `HandleAVConvert` can call the exact same shared `process()` untouched — mirroring `HandleDocumentConvert`'s/`HandleHTMLConvert`'s simple shape, not audio's guard-splice shape.
3. **"No audio track" and "silent audio track" are both already fail-closed correctly today, with zero code changes**, verified by running the exact `ffmpegNormalizeArgs` shape and whisper-cli against live-generated fixtures (see Open Questions below).

**Primary recommendation:** Mirror the audio engine's wiring pattern by hand (per locked D-06), add the completeness test, add three named sentinel errors in `av.go` for D-01, derive `isAVTerminal` as a stage-aware classifier that reuses the shared `isTerminal` fallthrough (do not copy `isAudioTerminal`'s blanket ffmpeg-stage-is-terminal rule), and treat every numeric budget in this phase (`AV_ENGINE_TIMEOUT`, `minFfmpegBudget` for video, `MAX_UPLOAD_BYTES`) as explicitly provisional and env-overridable, following the exact precedent `AUDIO_ENGINE_TIMEOUT` set (600s Go-code placeholder → 742s production value after Phase 32's RTF measurement).

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Video content-type detection (magic bytes) | API / Backend | — | `internal/convert` sniffers, called from `internal/api/handlers.go` before any storage write |
| AV format-pair validation & engine routing | API / Backend | — | `convert.Default.EngineFor` (registry), called synchronously in `handleCreateJob` |
| Upload size enforcement (global + per-engine) | API / Backend | — | `http.MaxBytesReader` (pre-detection) + new post-detection per-engine check |
| Queue routing / task dispatch | API / Backend | Database / Storage | asynq task enqueue backed by Redis; job row is Postgres-first (system of record) |
| AV conversion execution | API / Backend (worker process) | — | `cmd/av-worker` shells out to ffmpeg/ffprobe; no browser/CDN tier involved (fully server-side batch pipeline) |
| Stale/stranded job recovery | API / Backend | Database / Storage | `internal/reconciler` sweeps Postgres, re-enqueues via the same `enqueuer` interface |
| Video→transcript routing | API / Backend | — | Rides the existing `audio` queue/worker; no new tier, just an expanded `Pairs()` source set |

No browser, CDN, or frontend-SSR tier exists in this codebase (OctoConv is a pure async backend service) — every capability in this phase is backend/database tier by construction.

## Standard Stack

No new libraries. This phase adds zero `go.mod`/`go.sum` entries — it wires already-imported packages (`asynq`, `pgx`, `minio-go`) around already-built converter code (`internal/convert/av.go`, Phase 34). ffmpeg/ffprobe are OS binaries invoked via `os/exec`, not Go dependencies.

**Version verification:** `go.mod` unchanged expectation — verify at plan-check time with `git diff --stat go.mod go.sum` showing no changes, mirroring Phase 34's `AR-34-01` disposition ("zero commits touching go.mod/go.sum").

## Package Legitimacy Audit

No external packages are introduced by this phase. `slopcheck`/registry verification is not applicable — the entire change set is `internal/*` Go code plus a new `cmd/av-worker/main.go` binary built from existing imports.

| Package | Registry | Age | Downloads | Source Repo | slopcheck | Disposition |
|---------|----------|-----|-----------|-------------|-----------|-------------|
| — | — | — | — | — | — | N/A — no new packages |

**Packages removed due to slopcheck [SLOP] verdict:** none
**Packages flagged as suspicious [SUS]:** none

## Architecture Patterns

### System Architecture Diagram

```
                    ┌─────────────────────────────────────────────┐
                    │  POST /v1/jobs (multipart upload)             │
                    └───────────────────┬───────────────────────────┘
                                         │
                     MaxBytesReader(global MAX_UPLOAD_BYTES)  <- NEW ceiling raised (D-07)
                                         │
                    ┌────────────────────▼────────────────────────┐
                    │ Sniff() -> SniffContainer(zip) -> LooksLikeHTML │
                    │      -> IsOLECFB -> SniffVideo (NEW, D-08)     │
                    │      -> SniffAudio -> [422 unrecognized]        │
                    └────────────────────┬────────────────────────┘
                                         │ detected format
                    ┌────────────────────▼────────────────────────┐
                    │ EngineFor(detected, target)  (registry lookup) │
                    │   -> engine ∈ {image,document,html,audio,av}   │
                    └────────────────────┬────────────────────────┘
                                         │
                    per-engine upload-size ceiling check (NEW, D-07)
                                         │
                    ┌────────────────────▼────────────────────────┐
                    │ Postgres INSERT job(status=queued)  (system   │
                    │ of record, Postgres-first double write)        │
                    └────────────────────┬────────────────────────┘
                                         │
                    ┌────────────────────▼────────────────────────┐
                    │ switch engine { ...case EngineAV: Enqueue     │
                    │ AVConvert }   (NEW case, D-06 seam #1)         │
                    └────────────────────┬────────────────────────┘
                                         │ asynq task, queue "av"
                    ┌────────────────────▼────────────────────────┐
                    │ cmd/av-worker (NEW binary): asynq.ServeMux     │
                    │  TypeAVConvert -> HandleAVConvert -> process() │
                    │  -> registry.Lookup -> AVConverter.Convert()   │
                    │  (guard stage self-contained inside Convert)   │
                    └────────────────────┬────────────────────────┘
                                         │ done/failed
                    ┌────────────────────▼────────────────────────┐
                    │ isAVTerminal (NEW, stage-aware, D-02)          │
                    │  MarkDone/MarkFailed -> webhook enqueue         │
                    └─────────────────────────────────────────────┘

  Parallel path (video -> transcript, rides EXISTING audio queue):
  detected ∈ {mp4,mov,avi,mkv,webm}, target ∈ {txt,srt,vtt,json}
      -> EngineFor returns EngineAudio (AudioConverter.Pairs() extended, D-04)
      -> EnqueueAudioConvert -> cmd/audio-worker (UNCHANGED binary)
      -> AudioConverter.Convert -> ffmpegNormalizeArgs (minFfmpegBudget
         raised for video sources, D-05) -> whisper-cli

  Reconciler (cmd/webhook-worker, background sweep):
      FindStale -> switch job.Engine { ...case EngineAV: EnqueueAVConvert }
      (NEW case, D-06 seam #2)
```

### Recommended Project Structure

No new packages. New files land in existing directories:

```
cmd/av-worker/main.go        # NEW binary, mirrors cmd/audio-worker/main.go
internal/convert/av.go       # MODIFIED: 3 new sentinel errors (D-01)
internal/convert/whisper.go  # MODIFIED: audioSourceFormats grows (D-04),
                              #   minFfmpegBudget becomes source-aware (D-05),
                              #   ffmpegNormalizeArgs gains explicit -map 0:a:0
internal/queue/queue.go      # MODIFIED: Type/Queue AVConvert consts,
                              #   avRetrySchedule, AVRetryDelay, AVUniqueTTL,
                              #   RetryDelayFunc switch case
internal/queue/client.go     # MODIFIED: avMaxRetry/avUniqueTTL fields,
                              #   EnqueueAVConvert method
internal/worker/worker.go    # MODIFIED: isAVTerminal, HandleAVConvert
internal/api/api.go          # MODIFIED: Enqueuer interface +EnqueueAVConvert
internal/api/handlers.go     # MODIFIED: SniffVideo wired (D-08), enqueue
                              #   switch case, per-engine upload ceiling (D-07)
internal/reconciler/reconciler.go # MODIFIED: enqueuer interface +method,
                              #   routing switch case
internal/convert/converters.go    # MODIFIED: Default.Register(AVConverter{})
cmd/api/main.go              # MODIFIED: queue-depth collector arg (SILENT seam)
```

### Pattern 1: Engine-class wiring by hand (mirror, don't abstract)

**What:** Every new engine class (`image`→`document`→`html`→`audio`→now `av`) touches the same ~18 seams, each a small addition, never a shared abstraction over the switch statements.
**When to use:** Adding engine #5 in a codebase that already has 4 hand-wired examples and a locked decision (D-06) against refactoring this phase.
**Example (task type + queue name, `internal/queue/queue.go`):**
```go
// mirrors TypeAudioConvert/QueueAudio exactly
const TypeAVConvert = "av:convert"
const QueueAV = convert.EngineAV
```

### Pattern 2: Guard stage lives INSIDE Convert() for AV, unlike audio

**What:** `AudioConverter.Convert` has no duration guard of its own — `enforceAudioGuardBeforeConvert` in `worker.go` splices it in front, gated on `job.Engine == convert.EngineAudio`. `AVConverter.Convert` (`av.go:388-400`) already calls `avProbeSource` → `enforceMaxDurationOf`/`enforceMaxResolutionOf` itself, unconditionally, before dispatching to any of the three conversion stages.
**When to use:** This means **no new branch is needed in `worker.process()`** for AV — `HandleAVConvert` should call the shared `process()` exactly as `HandleDocumentConvert`/`HandleHTMLConvert` already do.
**Verified:** `av.go:391-400`, confirmed no external ceiling parameter is threaded into `AVConverter.Convert`'s signature (`opts map[string]any` only) — `avMaxSourceDuration`/`avMaxSourceResolutionHeight` are package constants, env-wiring explicitly deferred to Phase 36 per `av.go:231-246`'s own doc comments.

### Pattern 3: Stage-scoped sentinel errors for a classifier that must distinguish stages (D-01/D-02)

**What:** Today all three ffmpeg call sites in `convertTranscode`/`convertAudioExtract`/`convertThumbnail` wrap identically: `fmt.Errorf("av: ffmpeg: %w", err)` (`av.go:481,528,566`). A classifier that must treat transcode-timeout as transient but thumbnail/audio-extract-timeout as terminal cannot do so from a string prefix alone.
**Recommended shape** (exact names are Claude's Discretion per CONTEXT.md, this is one concrete option consistent with existing `ErrAVOutputMissingOrEmpty`/`ErrAVTimecodeOutOfRange` naming):
```go
var ErrAVTranscodeFailed     = errors.New("av: transcode failed")
var ErrAVAudioExtractFailed  = errors.New("av: audio-extract failed")
var ErrAVThumbnailFailed     = errors.New("av: thumbnail failed")

// convertTranscode:
if _, err := runCommand(ctx, "ffmpeg", args...); err != nil {
    return fmt.Errorf("%w: %w", ErrAVTranscodeFailed, err) // Go 1.20+ multi-%w
}
```
```go
// worker.go, NEW isAVTerminal — reuses the shared isTerminal fallthrough,
// does NOT copy isAudioTerminal's "any ffmpeg-stage failure is terminal" rule.
func isAVTerminal(err error) bool {
    if err == nil {
        return false
    }
    // Deterministic guard/output-validation rejections: always terminal,
    // regardless of which stage produced them.
    if errors.Is(err, convert.ErrAVOutputMissingOrEmpty) ||
        errors.Is(err, convert.ErrAVTimecodeOutOfRange) ||
        errors.Is(err, convert.ErrAVResolutionExceeded) ||
        errors.Is(err, convert.ErrAudioDurationExceeded) { // REUSED sentinel, see note below
        return true
    }
    isTimeout := errors.Is(err, context.DeadlineExceeded)
    switch {
    case errors.Is(err, convert.ErrAVTranscodeFailed):
        // D-02: transcode is the expensive stage -- timeout stays TRANSIENT.
        return !isTimeout
    case errors.Is(err, convert.ErrAVAudioExtractFailed), errors.Is(err, convert.ErrAVThumbnailFailed):
        // D-02: cheap stages -- ANY failure (timeout or not) is TERMINAL.
        return true
    }
    return isTerminal(err) // no-converter / minio.NoSuchKey / shared fallthrough
}
```
**Reuse note (verified):** `AVConverter.Convert`'s duration guard calls the SAME `enforceMaxDurationOf` function audio uses (`av.go:395`), which wraps with the SAME `ErrAudioDurationExceeded` sentinel (`audioduration.go:117`) — not a new AV-specific one. This is deliberate reuse already in Phase 34's code, not a bug. `isAVTerminal` can reuse `errors.Is(err, convert.ErrAudioDurationExceeded)` for the identical "duration_exceeded" client-facing error code `HandleAudioConvert` already emits, keeping the two engines' contracts symmetric. `ErrAVResolutionExceeded` (`avduration.go:15`) is AV-only and always terminal (a rejected declared resolution can never succeed on retry).

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Per-job duplicate-task suppression | Custom Redis lock / DB flag | `asynq.Unique(uniqueTTL)` (already used by every engine class) | Reconciler's enqueue-first recovery pattern depends on `asynq.ErrDuplicateTask` colliding on a live lock; a hand-rolled lock would need to replicate this exactly |
| Retry backoff scheduling | A new per-engine loop/timer | `queue.RetryDelayFunc` dispatch table + a new `avRetrySchedule`/`AVRetryDelay` pair, mirroring `documentRetrySchedule`/`DocumentRetryDelay` | asynq calls `RetryDelayFunc` itself; a custom scheduler would fight the framework |
| Multi-audio-track selection for the shared audio pipeline | ffprobe-probing + custom "pick best track" logic in `whisper.go` | A single explicit `-map 0:a:0` in `ffmpegNormalizeArgs` | Adds zero subprocesses, is a no-op for existing single-track audio sources, and sidesteps ambiguity entirely rather than re-implementing ffmpeg's own (partially undocumented, empirically verified below) selection heuristic |

**Key insight:** Every "new" mechanism this phase seems to need (locking, retry scheduling, stream selection) already has an in-repo precedent from Phases 2-33 or a one-line ffmpeg argv fix — the phase genuinely is wiring, not invention.

## Common Pitfalls

### Pitfall 1: `RetryDelayFunc`'s silent default fallthrough

**What goes wrong:** `queue.RetryDelayFunc` (`queue.go:330-345`) switches on `t.Type()`; a task type with no `case` falls through to `asynq.DefaultRetryDelayFunc` — no compile error, no runtime error, just the wrong backoff schedule silently applied.
**Why it happens:** This is the EXACT documented defect class the function's own doc comment describes fixing for webhook→image ("confirmed defect where every queue silently inherited WebhookRetryDelay").
**How to avoid:** Add the `case TypeAVConvert: return AVRetryDelay(n, e, t)` arm in the SAME commit that introduces `TypeAVConvert`.
**Warning signs:** AV retries land on asynq's exponential-backoff default instead of the locked 30s/2m schedule (D-03) — would only surface as an operational anomaly (unexpectedly long/short retry gaps), never a test failure unless a dedicated test asserts the schedule.

### Pitfall 2: Queue-depth collector's variadic silent omission

**What goes wrong:** `cmd/api/main.go:91-92` calls `metrics.NewQueueDepthCollector(inspector, queue.QueueImage, queue.QueueDocument, queue.QueueHTML, queue.QueueAudio, queue.QueueWebhook)` — a **variadic** call (`func NewQueueDepthCollector(inspector *asynq.Inspector, queues ...string)`, `queue_collector.go:24`). Omitting `queue.QueueAV` compiles cleanly and simply never emits the `av` queue-depth metric.
**Why it happens:** Variadic args have no arity check at compile time.
**How to avoid:** Add `queue.QueueAV` to this call list; cover with the D-06 completeness test (a table-driven test iterating every `convert.Engine*` constant and asserting each has an entry here, per CONTEXT.md's explicit call-out).
**Warning signs:** No compile/test failure — only a missing Prometheus series. This is the single most important reason the D-06 completeness test must exist (the other two switch statements below fail loudly by comparison).

### Pitfall 3: The interface seams ARE compile-time-safe (contrast with Pitfalls 1-2)

**What goes right by construction:** `api.Enqueuer` (`internal/api/api.go:54-59`) and `reconciler.enqueuer` (`internal/reconciler/reconciler.go:59-65`) are Go interfaces. If `handlers.go`'s enqueue switch or `reconciler.go`'s routing switch calls `.EnqueueAVConvert(...)` but the interface doesn't declare that method, or `*queue.Client` doesn't implement it, the build fails immediately.
**Why this matters for planning:** Do not spend a dedicated verification task on these two — a missing method here is caught by `go build ./...`, unlike Pitfalls 1 and 2. Reserve manual/test verification effort for the genuinely silent seams.

### Pitfall 4: `ENGINE_TIMEOUT` vs. the global reconciler `ActiveStaleAfter` ceiling

**What goes wrong:** `RECONCILER_ACTIVE_STALE_AFTER` (default 15m/900s, `cmd/webhook-worker/main.go:95`) is a SINGLE global threshold across every engine class. Its own doc comment states the invariant explicitly: it "must comfortably exceed [each engine's] ENGINE_TIMEOUT or the reconciler would re-enqueue a still-legitimately-running job as stale." This is a **documented near-miss from the audio engine**: `docker-compose.yml`'s comment records that audio's *duration-ceiling-derived* timeout (1349s) "breached the 900s/15m reconciler CAP," forcing `AUDIO_MAX_DURATION_SECONDS` to be lowered from the 14400s placeholder to 1800s as a NO-GO lever, landing the real `AUDIO_ENGINE_TIMEOUT` at 742s — safely under 900s.
**Why it happens:** `AV_ENGINE_TIMEOUT`'s provisional value is chosen independently in this phase, with no coupling check against the global reconciler threshold.
**How to avoid:** Whatever provisional `AV_ENGINE_TIMEOUT` value is chosen (see Numeric Recommendations below), verify it stays comfortably under 900s — a value in the 400-800s range is safe without touching the reconciler threshold; anything approaching or exceeding 900s requires either lowering it or raising `RECONCILER_ACTIVE_STALE_AFTER` as a coupled, explicit decision (exactly the trade audio made).
**Note:** Double-processing safety itself is held by `AVUniqueTTL` + `asynq.ErrDuplicateTask`, NOT by this threshold — a breach only degrades staleness-detection latency, not correctness. Still worth avoiding deliberately, per precedent.

### Pitfall 5: IN-01 — generic-brand `.m4a` misdetection becomes live once `AVConverter` registers

**What goes wrong:** `TestVideoBrandDisjointness` proves `mp4VideoBrands`/`m4aBrands`/`heicBrands` are disjoint as TABLES, but some real-world m4a encoders write a generic ISOBMFF major brand (`isom`, `mp42`) instead of `M4A `/`M4B `. Since `Sniff()` (mp4/mov/avi table) runs before `SniffAudio`, such a file resolves to `mp4` today — harmless while `AVConverter` is unregistered (both paths 422 with only the message differing), but once registered the job routes to the `av` engine and fails at the ffprobe "no video stream found" guard.
**Why it happens:** `matchMP4`'s brand table cannot distinguish "real mp4 video" from "audio-only content boxed in a generic ISOBMFF container" — that information isn't in the brand field for non-`M4A `-branded encoders.
**How to avoid:** `34-REVIEW-FIX.md`'s own "Residual Risk / Follow-ups for Phase 35" section recommends folding this in: after `probeVideoStreams` in the AV guard stage reports zero video streams, reclassify rather than hard-fail — OR add a regression test proving this reclassification composes with `avProbeSource`'s existing `if !ok { return ..., fmt.Errorf("ffprobe: no video stream found") }` path (`av.go:441-444`). At minimum, this needs an explicit fixture-backed test in this phase, not silent deferral — it changes from "cosmetic message difference" to "wrong engine executes and fails confusingly" the moment registration lands.

### Pitfall 6: `asynq.Config.ShutdownTimeout` silently caps at 8s unless overridden

**What goes wrong:** asynq's own default `ShutdownTimeout` is 8s. `cmd/audio-worker/main.go:107-113` deliberately overrides it to `AUDIO_ENGINE_TIMEOUT + 10s` so a long in-flight whisper-cli transcription survives `SIGTERM` instead of being aborted+requeued. `cmd/av-worker` (new this phase) must copy this pattern, not the asynq default.
**How to avoid:** `ShutdownTimeout: envDuration("AV_ENGINE_TIMEOUT", <default>) + 10*time.Second` in the new `cmd/av-worker/main.go`, mirroring `cmd/audio-worker/main.go:113` exactly.
**Verified:** confirmed present at HEAD in `cmd/audio-worker/main.go:103-114`; no equivalent exists yet for AV since the binary doesn't exist.

### Pitfall 7: Registry's silent last-write-wins on pair collision (mitigated, not eliminated, by disjoint target sets)

**What goes wrong:** `Registry.Register` (`convert.go:76-80`) is a bare map assignment with no collision check — a later `Default.Register(AVConverter{})` after `AudioConverter` would silently win any shared `(from, to)` pair with no error/panic/log.
**Why this is currently safe by construction, verified:** `AVConverter.Pairs()`'s target formats are `{mp4,webm,mp3,wav,m4a,jpg,png,webp}` (`av.go:47-73`); `AudioConverter.Pairs()`'s target formats are always `{txt,srt,vtt,json}` (`whisper.go:69`, unchanged by D-04's source-set expansion). These two target sets are disjoint, so no `(from,to)` pair can collide even though both converters may share SOURCE formats (mp4/mov/avi/mkv/webm). The pair-disjointness test the phase requires (AVT-01, D-06 note) is a regression guard against a FUTURE edit accidentally introducing an overlapping target — not fixing a currently-live collision.
**How to avoid:** Write the disjointness test as `for _, p := range AVConverter{}.Pairs() { for _, q := range AudioConverter{}.Pairs() { if p == q { t.Fatal(...) } } }`, mirroring `TestVideoBrandDisjointness`'s shape (`avsniff_test.go:123-140`).

## Code Examples

### Multi-audio-track selection fix for `ffmpegNormalizeArgs` (D-05's sibling concern)

```go
// Source: internal/convert/whisper.go, MODIFIED (verified against live ffmpeg 8.1.2)
func ffmpegNormalizeArgs(inPath, normPath string) []string {
	return []string{"-y", "-nostdin", "-protocol_whitelist", "file,crypto",
		"-i", "file:" + inPath,
		"-map", "0:a:0", // NEW: deterministic first-audio-stream selection,
		// mirrors AVConverter's own "0:a:0[?]" convention (av.go:122,143,163).
		// A no-op for existing single-audio-track sources (mp3/wav/m4a/ogg).
		"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "file:" + normPath}
}
```

### `minFfmpegBudget` becoming video-source-aware (D-05)

```go
// Source: internal/convert/whisper.go, MODIFIED
var videoSourceFormats = map[string]bool{"mp4": true, "mov": true, "avi": true, "mkv": true, "webm": true}

const minFfmpegBudget = 30 * time.Second        // unchanged floor for audio-family sources
const minFfmpegBudgetVideo = 90 * time.Second   // NEW floor for video-container sources (D-05)

// inside Convert, before the existing budget check:
floor := minFfmpegBudget
if videoSourceFormats[NormalizeFormat(filepath.Ext(inPath))] {
	floor = minFfmpegBudgetVideo
}
if dl, ok := ctx.Deadline(); ok && time.Until(dl) < floor {
	return fmt.Errorf("audio: insufficient attempt budget remaining: %w", context.DeadlineExceeded)
}
```

### Per-engine upload ceiling, mirroring the existing `MaxImagePixels`/`MaxDocumentUncompressedBytes` pattern (D-07)

```go
// Source: internal/api/api.go, NEW field on Config/Server, mirrors
// maxImagePixels/maxDocumentUncompressedBytes (api.go:86-93, 108-113)
type Config struct {
	MaxUploadBytes  int64            // raised global ceiling (D-07)
	MaxEngineBytes  map[string]int64 // NEW: per-engine post-detection ceiling
	// ... existing fields unchanged
}

// internal/api/handlers.go, inserted immediately after `engine, ok := convert.Default.EngineFor(...)`
if limit, ok := s.maxEngineBytes[engine]; ok && header.Size > limit {
	log.Printf("content validation rejected: client_id=%s filename=%q reason=engine_size_limit engine=%s size=%d limit=%d", client.ID, filename, engine, header.Size, limit)
	writeError(w, http.StatusRequestEntityTooLarge, "file exceeds size limit for this conversion type")
	return
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| `AVConverter` built, tested, unregistered | `AVConverter` registered into `convert.Default`, all seams wired | This phase | Video jobs become reachable from outside for the first time |
| Single global `MAX_UPLOAD_BYTES` for all engine classes | Two-tier: global hard cap (pre-detection) + per-engine ceiling (post-detection) | This phase (D-07) | Video can use a larger ceiling without weakening DoS posture for image/document/html/audio |
| `AudioConverter.Pairs()` sources = `{mp3,wav,m4a,ogg}` | Sources also include `{mp4,mov,avi,mkv,webm}` | This phase (D-04) | Video→transcript rides the existing audio pipeline; no new whisper/ffmpeg image weight |
| `isAudioTerminal`'s blanket "any ffmpeg-stage failure/timeout is terminal" | `isAVTerminal`'s stage-aware split (transcode-timeout transient, extract/thumbnail-timeout terminal) | This phase (D-02) | Reflects that ffmpeg is CHEAP for audio's normalize stage but EXPENSIVE for AV's transcode stage — the same blanket rule would be wrong for AV |

**Deprecated/outdated:** None — this phase does not remove or replace any existing mechanism, only extends the pattern four prior engine classes already established.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | Provisional `AV_ENGINE_TIMEOUT` default of 600s (mirroring `AUDIO_ENGINE_TIMEOUT`'s original placeholder) | Numeric Recommendations | Too short: legitimate transcode jobs retry more than necessary during Phase 35 dev/test (self-correcting — transcode timeout is TRANSIENT per D-02, so no job is lost, only delayed). Too long: risks the reconciler `ActiveStaleAfter` tension in Pitfall 4 if raised further without a coupled decision. Phase 36 supersedes this value entirely via RTF measurement, per REQUIREMENTS AVE-04 |
| A2 | `minFfmpegBudget` for video sources raised to 90s (3x audio's 30s floor) | Numeric Recommendations | If too low: a slow/contended production disk could still misattribute a stalled download as an ffmpeg failure for large video files (the exact bug the floor exists to prevent). If too high: shrinks the effective retry budget for jobs where the upstream S3 download genuinely used most of `AUDIO_ENGINE_TIMEOUT`. Empirical grounding (local SSD, <1s for ~37MB of dense 1080p) is from Apple Silicon dev hardware, not the production container — recommend a live smoke test with a real large video fixture during Phase 35 execution |
| A3 | Global `MAX_UPLOAD_BYTES` raised to 2 GiB; per-engine ceiling for AV also 2 GiB; image/document/html/audio per-engine ceilings held at the current 100 MiB default | Numeric Recommendations | This is a **business decision** (what video file sizes internal clients actually need to submit), not derivable from code — STATE.md's own Blockers/Concerns log flags this explicitly as "Must be an explicit named decision... not an implicit side effect of picking a video-friendly number." 2 GiB is a defensible engineering default (headroom for a ~30-60 min internal recording at consumer bitrates) but genuinely needs operator/user confirmation before being treated as final |
| A4 | ffmpeg's automatic (no `-map`) audio-stream selection prioritizes the `disposition=default`-flagged stream over raw channel count | Open Questions / Code Examples | LOW-MEDIUM risk if wrong: the recommended fix (`-map 0:a:0`, explicit first-stream selection) sidesteps this entirely and does not depend on which heuristic is correct — this assumption is informational context only, not load-bearing for the recommendation |

## Open Questions (RESOLVED)

### 1. Video with no audio track submitted for transcription — RESOLVED, no code change needed

**What we know (VERIFIED via live ffmpeg 8.1.2):** Generated a 2s video-only mp4 (h264, zero audio streams) and ran the exact `ffmpegNormalizeArgs` shape against it:
```
Output #0, wav, to 'file:norm.wav':
[out#0/wav] Output file does not contain any stream
Error opening output file file:norm.wav.
EXIT CODE: 234
```
No output file is created. `runCommand` returns a non-nil error (`"ffmpeg failed: exit status 234: ..."`), `whisper.go` wraps it `fmt.Errorf("audio: ffmpeg: %w", err)`, and `isAudioTerminal`'s existing `strings.Contains(msg, "audio: ffmpeg:")` check classifies it **terminal** today, with zero Phase 35 code changes.
Re-tested with the recommended `-map 0:a:0` fix added (Code Examples above): still fails closed (exit 234, different message — `"Stream map '' matches no streams"`), same terminal classification outcome.

**Recommendation:** No new sentinel or guard is needed for "genuinely absent audio track." Add a regression test (real ffmpeg fixture, following the `requireLiveAVBinaries` skip-gate precedent) pinning that a video-only source produces a terminal, non-retried failure through the EXISTING `isAudioTerminal` path — this closes the open question with a test, not new code.

### 2. Silent/near-silent audio track — PARTIALLY RESOLVED, existing accepted risk narrower than feared

**What we know (VERIFIED via live ffmpeg 8.1.2 + whisper-cli v1.9.1, `ggml-base.bin`):** Tested three synthetic-silence shapes against a video WITH an audio track: (a) pure digital silence (`anullsrc`), (b) low-amplitude pink noise ("room tone," amplitude 0.001), (c) a pure 440Hz tone (a classic whisper hallucination trigger in other implementations). In **all three cases**, `ffmpegNormalizeArgs` succeeded (produced a valid, non-empty WAV), but `whisper-cli` exited 0 with a **completely empty transcript** (`out.txt` = 0 bytes; JSON `"transcription": []`) — not hallucinated garbage. `validateAudioOutput`'s existing `"audio: output is empty"` check (already in `terminalAudioSignatures`, `worker.go:112-115`) already classifies this **terminal** today, with zero Phase 35 code changes, for these three specific synthetic patterns.
**What's unclear:** This does NOT fully retire the accepted risk STATE.md records (whisper.cpp's internal `no_speech_thold`≈0.6 confidence gate appears to suppress output entirely for near-zero-confidence synthetic audio, but real-world faint background chatter/music with MODERATE confidence could still produce a structurally-valid, non-empty hallucinated transcript that `validateAudioOutput` would NOT catch, since it only checks for emptiness). The whisper-cli v1.9.1 binary genuinely exposes no `no_speech_prob`/`avg_logprob` field to a caller (confirmed in Phase 30's research, unchanged).
**Recommendation:** The accepted risk (STATE.md, Phase 30, carried forward explicitly to v1.8) remains an accepted risk — it is NOT resolved by this phase and does not need to be. What IS resolved: the specific "video source has no meaningful audio" scenarios this phase's open question worried about already fail closed via the existing empty-output check. Add a regression test with a silent-audio-track video fixture (same live-ffmpeg precedent) to pin this behavior explicitly, since it is currently an emergent property of two independent mechanisms (whisper.cpp's internal confidence gate + `validateAudioOutput`'s emptiness check) rather than a designed guarantee.

### 3. Multiple audio tracks in one container — RESOLVED with a recommendation

**What we know (VERIFIED via live ffmpeg 8.1.2):** Built a 2-audio-track fixture (track index 1: mono 300Hz, `disposition.default=1`; track index 2: stereo 900Hz, `disposition.default=0`). `ffmpeg`'s automatic stream selection (no `-map`) picked stream index 1 — the `default`-flagged, LOWER-channel-count stream — contradicting a simpler "most channels wins" summary found via a documentation search (which may describe only the no-default-flag fallback case; this project's own live-binary test is the higher-confidence source here since it is the exact pinned ffmpeg version, VERIFIED not CITED).
**What's unclear:** Whether the true tie-break/priority algorithm always prefers `default` over channel count, or whether other factors (codec, bitrate) also matter in cases not tested. A clean "no default flag anywhere" control test was attempted but did not conclusively isolate the pure channel-count fallback (a `-disposition:a:0 0` remux did not clear the flag as expected — noted here for transparency rather than silently discarded).
**Recommendation:** Do not depend on resolving the exact ffmpeg heuristic. Add `-map 0:a:0` to `ffmpegNormalizeArgs` (Code Examples above) — deterministic, mirrors `AVConverter`'s own explicit-stream-mapping convention, zero extra subprocesses, and a verified no-op for every existing single-track audio source. This sidesteps the open question rather than resolving ffmpeg's internal algorithm.

### 4. `HasDimensionLimit` vs video formats — CONFIRMED non-issue

**What we know (VERIFIED by direct read):** `dimensionParsers` (`dimensions.go:37-43`) is a closed map keyed to exactly `{png, jpg, webp, heic, tiff}` — the five image formats. `HasDimensionLimit(format)` (`dimensions.go:76-79`) is `_, ok := dimensionParsers[NormalizeFormat(format)]`. No video format (`mp4`/`mov`/`avi`/`mkv`/`webm`) is a key in this map, so `HasDimensionLimit` returns `false` for all of them and `handlers.go:322`'s dimension-guard block is skipped entirely for video uploads — exactly as CONTEXT.md's discretion note assumed. The video resolution guard correctly lives in the worker/converter layer via `EnforceMaxResolution`/`enforceMaxResolutionOf` (`avduration.go:166-182`), called inside `AVConverter.Convert` before any decode.
**Recommendation:** No code change needed here. State this confirmation explicitly in the plan so it isn't re-litigated or re-investigated downstream.

## Numeric Recommendations (Claude's Discretion per CONTEXT.md, all provisional/`[ASSUMED]`)

| Value | Recommendation | Grounding |
|-------|-----------------|-----------|
| `AV_ENGINE_TIMEOUT` (Go default in `cmd/av-worker/main.go`) | **600s**, env-overridable | Mirrors `AUDIO_ENGINE_TIMEOUT`'s original literal placeholder (`cmd/audio-worker/main.go:61`, "`[ASSUMED] placeholder, Phase 32 re-derives from RTF measurement`"). Live-measured VP9 transcode (this repo's exact `transcodeToWebMArgs` argv, 1080p30 15s source, 2 threads, Apple M3 Pro): 21.2s wall-clock for 15s of source (~0.7x RTF) — 600s gives roughly 15-20x headroom over that measured rate even before accounting for production hardware being slower per-core. **Must stay comfortably under the 900s `RECONCILER_ACTIVE_STALE_AFTER` default** (Pitfall 4) — do not raise without a coupled decision. Superseded entirely by Phase 36's RTF matrix (AVE-04) |
| `AV_MAX_RETRY` / retry schedule | **2** / **30s, 2m** | LOCKED by D-03 — not open for research, included here only for `AVUniqueTTL` derivation completeness: `(2+1)*600s + (30+120)s + 120s = 2070s` (~34.5 min), well under any operational concern |
| `minFfmpegBudget` for video sources (`whisper.go`) | **90s** (3x the existing 30s audio-family floor) | Empirical: local-SSD demux-only cost for a well-formed container is negligible regardless of resolution/bitrate (37MB/180s of 1080p: 0.146s wall-clock; 60s/11.5MB 1080p: 0.085s) — the guard exists to protect against upstream S3-download-budget exhaustion, not raw demux cost, so a modest raise (not a dramatic one) is defensible. 90s stays a small fraction (15%) of the 600s `AV_ENGINE_TIMEOUT` recommendation, mirroring audio's 30s/600s (5%) ratio with headroom for slower/contended production disk this Apple Silicon test cannot represent |
| Global `MAX_UPLOAD_BYTES` | **2 GiB** (2147483648), raised from 100 MiB | Business-driven, not code-derived — see Assumption A3. Flagged explicitly for operator/user confirmation before being treated as final |
| Per-engine ceiling: `av` | **2 GiB** (equal to the new global ceiling — AV is the class the raise exists for) | Same as above |
| Per-engine ceiling: `image`/`document`/`html`/`audio` | **100 MiB** each (unchanged from today's effective global default) | Satisfies D-07's explicit design goal: raising the global cap for video must not weaken the other four classes' DoS posture — holding their per-engine ceiling at the CURRENT default achieves exactly zero behavior change for them |

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| ffmpeg | `AVConverter.Convert`, `whisper.go` normalize stage, live tests | ✓ (verified on this dev machine) | 8.1.2 | none needed — required for any live-binary test in this phase (`requireLiveAVBinaries` skip-gate pattern) |
| ffprobe | `avProbeSource`, `ProbeDuration` | ✓ | 8.1.2 (bundled with ffmpeg) | none needed |
| whisper-cli | Audio/video→transcript pipeline live tests | ✓ | present at `/opt/homebrew/bin/whisper-cli` | none needed |
| `ggml-base.bin` model | whisper-cli live tests | ✓ | present at `~/.cache/whisper/ggml-base.bin` | none needed |
| Redis / Postgres / MinIO | asynq queue, jobs repo, storage — full integration tests | Not probed this session (no live infra started) | — | Existing `docker-compose.yml` services; unit tests for the classifier/TTL-derivation logic do not require them (mirrors `TestAudioUniqueTTL`'s pure-function shape) |

**Missing dependencies with no fallback:** none identified.
**Missing dependencies with fallback:** none — all required tooling for this phase's live tests is present on this development machine.

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-------------------|
| V2 Authentication | no | Unchanged — existing API-key auth (`internal/auth`) is orthogonal to this phase |
| V3 Session Management | no | Stateless API-key auth, no sessions |
| V4 Access Control | no | Unchanged client/job ownership model |
| V5 Input Validation | yes | `AVOptsFromMap`/`ParseAVOpts` (Phase 34, already ASVS-2 audited) — this phase only REGISTERS the already-hardened converter; no new client-input-parsing surface is introduced |
| V6 Cryptography | no | No new crypto surface |
| V12 File Handling | yes | New trust boundary: `SniffVideo`'s bounded EBML parser goes from zero production callers to live on the upload path (D-08); the existing `-protocol_whitelist file,crypto` invariant (T-34-10, verified closed in `34-SECURITY.md`) must not regress as new call sites (video-source whisper normalize) are added |

### Known Threat Patterns for this stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|-----------------------|
| Registering an already-hardened but never-live converter | Elevation of Privilege (untrusted bytes reach a new subprocess path for the first time) | All of `av.go`'s subprocess invocations already carry `-protocol_whitelist file,crypto` + `-nostdin` + `file:`-prefixed paths, verified closed in `34-SECURITY.md` (T-34-01 through T-34-15, 17/17 threats mitigated). This phase's job is to confirm the registration itself introduces no NEW unguarded call site — it does not, since `AVConverter.Convert` is the only new entry point and it was the audited unit |
| Upload-size ceiling weakening for non-video classes | Denial of Service | The two-tier design (D-07) is itself the mitigation: raising the global `MAX_UPLOAD_BYTES` without a per-engine post-detection ceiling would silently raise every class's DoS ceiling; the per-engine check closes this |
| Generic-brand `.m4a` routed to the wrong engine (IN-01) | Denial of Service (confusing failure) / Tampering (wrong-engine execution) | Not an exploitable vulnerability (both paths fail closed with a 422/failed job), but IS a correctness regression once `AVConverter` registers — see Pitfall 5. Recommend a regression test in this phase's security-relevant test set even though `34-SECURITY.md` did not flag it as a threat register entry (it predates registration) |
| A new engine class inheriting weaker retry/timeout discipline than its neighbors | Denial of Service (resource exhaustion via unbounded retry) | `AVUniqueTTL` must be derived fresh via the same `(maxRetry+1)*engineTimeout + backoffSum + margin` formula every other engine uses (`queue.go:396-505`), reusing `uniqueTTLSafetyMargin` verbatim per D-03 — never a hardcoded constant |

## Sources

### Primary (HIGH confidence — direct codebase read at HEAD)
- `internal/convert/av.go`, `avduration.go`, `avopts.go`, `avsniff.go`, `converters.go`, `convert.go`, `sniff.go`, `audiosniff.go`, `whisper.go`, `exec.go` — full read
- `internal/worker/worker.go`, `internal/queue/queue.go`, `internal/queue/client.go` — full read
- `internal/api/handlers.go`, `internal/api/api.go` — targeted read (upload/detection/enqueue paths)
- `internal/reconciler/reconciler.go` — targeted read (enqueuer interface, routing switch)
- `cmd/audio-worker/main.go`, `cmd/api/main.go`, `cmd/webhook-worker/main.go` — full/targeted read
- `internal/db/migrations/0001_init.sql`, `0005_html_engine.sql`, `0006_audio_engine.sql` — confirmed `'av'` already in `jobs_engine_check`
- `docker-compose.yml` — confirmed audio's real RTF-derived production values and the reconciler-cap near-miss
- `.planning/phases/34-av-engine-foundation/34-REVIEW.md`, `34-REVIEW-FIX.md`, `34-SECURITY.md` — full read

### Primary (HIGH confidence — live execution on this machine)
- ffmpeg 8.1.2 / ffprobe 8.1.2 / whisper-cli (ggml-base.bin): 8 live experiments run in `/private/tmp/.../scratchpad/avtest` covering no-audio-track, silent-audio-track, tone-audio-track, multi-audio-track stream selection, and VP9 transcode timing — full commands and output captured above

### Secondary (MEDIUM confidence)
- [ffmpeg.org Automatic Stream Selection documentation](https://www.ffmpeg.org/ffmpeg.html#Automatic-stream-selection) — via WebFetch paraphrase; PARTIALLY CONTRADICTED by this session's own live test (Open Question 3) which found the `default` disposition flag outweighing channel count in the one scenario tested. Live test is the higher-confidence source for this project's exact ffmpeg version; the doc's general "most channels" rule may describe the no-default-flag fallback case only

## Metadata

**Confidence breakdown:**
- Wiring/seams/pitfalls: HIGH — every claim verified by direct grep/read of HEAD, cross-checked against `34-REVIEW.md`/`34-REVIEW-FIX.md`/`34-SECURITY.md`
- Open Questions 1, 2, 4: HIGH — resolved via live execution or direct code read, not inference
- Open Question 3 (multi-track selection): MEDIUM — live-verified for the one scenario tested, but the general algorithm was not fully isolated; the RECOMMENDATION (explicit `-map`) is HIGH confidence regardless, since it sidesteps the ambiguity
- Numeric recommendations (AV_ENGINE_TIMEOUT, minFfmpegBudget, MAX_UPLOAD_BYTES): MEDIUM — grounded in measurement and precedent, explicitly provisional, flagged `[ASSUMED]` in the Assumptions Log per the CLAUDE.md-equivalent provenance rule

**Research date:** 2026-07-21
**Valid until:** Effectively pinned to Phase 34's code state (stable, no external API) — re-verify only if `internal/convert/av.go`/`whisper.go` change before this phase is planned/executed, or before Phase 36 (numeric recommendations are explicitly superseded there)
