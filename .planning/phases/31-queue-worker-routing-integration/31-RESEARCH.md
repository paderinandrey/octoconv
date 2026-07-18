# Phase 31: Queue, Worker & Routing Integration - Research

**Researched:** 2026-07-18
**Domain:** Integration wiring for a fourth async engine class (audio/whisper.cpp) into an existing Go/asynq/Postgres/S3 conversion pipeline — no new external libraries, pure splice-point mapping of existing code.
**Confidence:** HIGH (every finding below is a direct code read of this repository at the current commit; no external library research was needed for this phase)

## Summary

Phase 30 built `AudioConverter`, `SniffAudio`, `EnforceMaxDuration`, and `AudioOpts` as **standalone, unregistered** components — verified by `grep -c 'Default.Register(AudioConverter' internal/convert/converters.go` = 0 and `grep -rn 'EnforceMaxDuration\|SniffAudio' --include=*.go | grep -v _test.go` showing zero call sites outside `internal/convert`. Phase 31's entire job is **wiring**: register the converter, splice `SniffAudio` into the API's content-detection chain, add an `audio` queue/task-type/worker, write a stage-aware terminal/transient classifier, derive `AudioUniqueTTL`, extend the reconciler's engine-routing switch, and — critically — a **Postgres migration** that the phase description's research priorities did not explicitly call out but that blocks everything else: `jobs.engine`'s CHECK constraint does not yet accept `'audio'`.

Every existing engine class (image/document/html) was added by touching the same ~9 files in the same shapes; this research maps every one of those splice points to an exact file:line for the audio class, flags two integration bugs that a naive "copy the pattern" pass would silently introduce (the opts-parsing switch defaulting audio opts through `ParseDocOpts`, and a peek-chaining bug in `SniffAudio` wiring that would silently truncate the first 12 bytes of every audio upload), and documents the CURRENT, BINDING classifier design (Key Decision 1: stage-aware split) which **supersedes** the milestone-level `ARCHITECTURE.md`'s recommendation to blanket-copy `isDocumentTerminal`.

**Primary recommendation:** Follow the document/html engine-class-addition pattern file-for-file (it is proven three times over), but do NOT copy `isDocumentTerminal`'s single-classifier shape for `isAudioTerminal` — the stage-aware split requires a genuinely different function shape keyed on the `"audio: ffmpeg:"` vs `"audio: whisper-cli:"` error-message prefixes `whisper.go` already emits for exactly this purpose.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Audio format detection (magic bytes) | API / Backend | — | `handleCreateJob` must classify content before any storage write (existing D-01/D-02 pattern); `SniffAudio` already built, needs splicing into the same handler |
| Engine-class routing (`EngineFor`) | API / Backend | — | Registry-driven (`convert.Default.EngineFor`), automatic once `AudioConverter` registers — no new code needed here |
| Opts validation (language/translate) | API / Backend | Worker (re-validation) | API validates once at write time (D-10 single-validation-authority); worker re-validates the persisted JSON strictly before `MarkActive`, mirroring `HandleDocumentConvert`/`HandleHTMLConvert` |
| Duration guard (`EnforceMaxDuration`) | Worker | — | Requires the downloaded local file path (ffprobe needs a real file); cannot run at API-upload time without buffering the whole upload first — must run worker-side, after download, before `Convert()` |
| Queue routing / task dispatch | API / Backend + Worker | — | `queue.Client.EnqueueAudioConvert` (API side, enqueue) and `cmd/audio-worker` (consume side) |
| Terminal/transient classification | Worker | — | `internal/worker/worker.go`, engine-scoped function alongside `isImageTerminal`/`isDocumentTerminal`/`isHTMLTerminal` |
| Stranded-job recovery | Reconciler (fleet-wide sweeper, runs in `cmd/webhook-worker`) | — | `internal/reconciler/reconciler.go`'s engine-routing `switch` needs an `EngineAudio` case |
| Persistence / state machine | Database | — | `jobs` table `engine` CHECK constraint needs a migration; no other schema change |

## Standard Stack

No new external dependencies this phase — every library involved (chi, asynq, pgx, minio-go) is already in `go.mod` and used identically to the other three engine classes. `AudioConverter`, `SniffAudio`, `EnforceMaxDuration`, `AudioOpts` are all already-built, already-tested internal code from Phase 30.

**Version verification:** N/A — no new packages.

## Package Legitimacy Audit

Not applicable — this phase adds zero new external packages (verified: no new `go.mod`/`go.sum` entries needed for queue/worker/routing wiring; whisper.cpp/ffmpeg toolchain provisioning was Phase 30's concern and is host-local, not a Go module).

## Architecture Patterns

### System Architecture Diagram

```
Client                         API (cmd/api)                      Postgres              Redis/asynq            cmd/audio-worker
  |  POST /v1/jobs                |                                  |                       |                        |
  |------------------------------>|                                  |                       |                        |
  |                                | 1. convert.Sniff(file)          |                       |                        |
  |                                |    -> "" (no image match)       |                       |                        |
  |                                | 2. ZIP/HTML/OLE-CFB checks      |                       |                        |
  |                                |    -> "" (none match)           |                       |                        |
  |                                | 3. convert.SniffAudio(rest)  <- NEW splice point         |                        |
  |                                |    -> "mp3"/"wav"/"m4a"/"ogg"   |                       |                        |
  |                                | 4. EngineFor(detected,target)   |                       |                        |
  |                                |    -> "audio" (auto, once        |                       |                        |
  |                                |       AudioConverter registers) |                       |                        |
  |                                | 5. opts switch: NEW case         |                       |                        |
  |                                |    convert.EngineAudio ->        |                       |                        |
  |                                |    ParseAudioOpts/               |                       |                        |
  |                                |    ValidateAudioApplicability    |                       |                        |
  |                                | 6. s.storage.Upload(rest)         --------------------->|(S3/MinIO, not shown)  |
  |                                | 7. repo.Create(engine="audio")  ---> INSERT jobs         |                       |
  |                                |    (needs migration: CHECK       |  (status=queued)      |                       |
  |                                |     constraint must allow        |                       |                        |
  |                                |     'audio')                     |                       |                        |
  |                                | 8. enqueue switch: NEW case      |                       |                        |
  |                                |    EnqueueAudioConvert            ------------------------->| audio queue task     |
  |<-------------------------------| 202 Accepted, job_id             |                       |                        |
  |                                                                    |                       |<-----------------------| consume task
  |                                                                    |                       |                        | 9. MarkActive
  |                                                                    |<--------------------------------------------------| 10. AudioOptsFromMap
  |                                                                    |                       |                        |     (strict re-parse,
  |                                                                    |                       |                        |      terminal on garbage)
  |                                                                    |                       |                        | 11. download input
  |                                                                    |                       |                        | 12. EnforceMaxDuration <- NEW splice point (T-30-08/IN-02)
  |                                                                    |                       |                        |     (terminal if exceeded)
  |                                                                    |                       |                        | 13. AudioConverter.Convert
  |                                                                    |                       |                        |     (ffmpeg -> whisper-cli,
  |                                                                    |                       |                        |      one AUDIO_ENGINE_TIMEOUT)
  |                                                                    |                       |                        | 14. isAudioTerminal(err)?
  |                                                                    |                       |                        |     ffmpeg-stage -> terminal
  |                                                                    |                       |                        |     whisper-stage timeout
  |                                                                    |                       |                        |       -> transient (retry)
  |                                                                    |<--------------------------------------------------| 15. MarkDone/MarkFailed
  |                                                                    |                       |                        |
GET /v1/jobs/{id}  ------------------------------------------------->| repo.Get              |                       |
  |<----------------------------------------------------------------- status/download_url      |                       |

cmd/webhook-worker (reconciler sweep, every RECONCILER_SWEEP_INTERVAL):
  FindStale(queuedStaleAfter, activeStaleAfter) -- SAME activeStaleAfter for ALL classes (see Pitfall below)
    -> stale audio job found -> switch j.Engine { case convert.EngineAudio: EnqueueAudioConvert } <- NEW case
    -> asynq.Unique lock (AudioUniqueTTL) still live? -> ErrDuplicateTask -> no-op (safe)
    -> lock expired? -> genuine re-enqueue -> RequeueStale -> reconciler_recovery event
```

### Recommended Project Structure (files touched, no new directories)

```
internal/db/migrations/
└── 0006_audio_engine.sql        # NEW — CHECK constraint: add 'audio' to jobs.engine allow-list

internal/convert/
├── converters.go                 # MODIFIED — register AudioConverter{} (real baked model path)
└── whisper.go                    # already built (Phase 30) — no change needed for routing

internal/queue/
├── queue.go                      # MODIFIED — TypeAudioConvert, QueueAudio, NewAudioConvertTask,
│                                  #   audioRetrySchedule, AudioRetryDelay, audioBackoffSum, AudioUniqueTTL
└── client.go                     # MODIFIED — audioMaxRetry/audioUniqueTTL fields, EnqueueAudioConvert

internal/worker/
└── worker.go                     # MODIFIED — HandleAudioConvert, isAudioTerminal (stage-aware,
                                   #   NOT timeoutIsTerminal-based), duration-guard splice in process()

internal/reconciler/
└── reconciler.go                 # MODIFIED — enqueuer interface + sweep() switch: EngineAudio case

internal/api/
├── api.go                        # MODIFIED — Enqueuer interface: EnqueueAudioConvert
└── handlers.go                   # MODIFIED — SniffAudio splice (content detection) +
                                   #   opts switch: EngineAudio case + enqueue switch: EngineAudio case

cmd/audio-worker/                 # NEW — mirrors cmd/document-worker/main.go structure exactly
└── main.go

.env.example                       # MODIFIED — AUDIO_ENGINE_TIMEOUT, AUDIO_MAX_RETRY,
                                   #   AUDIO_WORKER_CONCURRENCY, AUDIO_MAX_DURATION_SECONDS,
                                   #   RECONCILER_ACTIVE_STALE_AFTER (note only, see Pitfall)
```

### Pattern 1: Registering the fourth converter (mechanical, low-risk)

**What:** `internal/convert/converters.go`'s `init()` currently registers three converters:
```go
func init() {
	Default.Register(LibvipsConverter{})
	Default.Register(LibreOfficeConverter{})
	Default.Register(ChromiumConverter{})
	// Future engines (one line each):
	// Default.Register(FFmpegConverter{})
}
```
Add `Default.Register(AudioConverter{})` — but `AudioConverter{}` zero-value uses `c.model()`'s fallback (`defaultAudioModelPath = "/models/ggml-base.bin"`, `internal/convert/whisper.go:28`), a path that only exists inside the (not-yet-built, Phase 32) container image. For **this phase's** local `go run ./cmd/audio-worker` E2E target, mirror `internal/convert/verapdf.go`'s exact, already-proven setter pattern (verified read in full):

```go
// Source: internal/convert/verapdf.go:10-35 (verified read, this repo) -- the shape to replicate
var verapdfTimeout time.Duration

func SetVeraPDFTimeout(d time.Duration) { verapdfTimeout = d } // called once at startup, before srv.Start

func effectiveVeraPDFTimeout() time.Duration {
	if verapdfTimeout > 0 {
		return verapdfTimeout
	}
	return 60 * time.Second
}
```

Add `convert.SetAudioModelPath(path string)` + a package-level `audioModelPath string` var in a new small file (or alongside `whisper.go`) following this shape exactly, and change `AudioConverter.model()` (`whisper.go:47-52`) to consult it as a THIRD fallback tier: injected `c.modelPath` (test-only, unchanged) → `audioModelPath` (set once via `SetAudioModelPath` from `cmd/audio-worker/main.go`, reading `AUDIO_MODEL_PATH`) → `defaultAudioModelPath` (compile-time container constant, unchanged fallback). This is a strict superset of the existing two-tier fallback — no existing test-injection behavior changes. `converters.go`'s `init()` itself stays untouched (still a bare `Default.Register(AudioConverter{})`, zero-value `modelPath`); the setter is called from `main()`, exactly mirroring how `SetVeraPDFTimeout` is called from `cmd/document-worker/main.go:79` — `internal/convert` never reads `os.Getenv` directly (WARNING-3 convention, `verapdf.go:10-14`'s own doc comment states this explicitly).

**Why it matters:** `EngineFor`, `GET /v1/formats` (`internal/api/formats_handlers.go:21`, walks `convert.Default.Classes()`), and `Registry.Lookup` all become audio-aware **automatically** the moment this one line lands — verified by reading `Classes()` (`internal/convert/convert.go:108-125`), which has no engine-specific code, only a generic `for pair, c := range r.m` walk. **No handler-side code needs to change for `/v1/formats` or `EngineFor`.**

### Pattern 2: Content-detection splice — SniffAudio peek-chaining (HIGH-RISK correctness detail)

**What:** `internal/api/handlers.go:184-283` runs a chain of content detectors when `detected == ""`: `Sniff(file)` (image, `sniffLen=12` fixed window) → ZIP/office (`file.ReadAt`) → HTML (`file.ReadAt`, gated on `source=="html"`) → OLE-CFB (`file.ReadAt`). Every detector after the first uses `file.ReadAt` — deliberately, per the existing comment at `handlers.go:198-199`: *"ReadAt never disturbs Sniff's sequential cursor, so `rest` remains valid below."*

`SniffAudio` (`internal/convert/audiosniff.go:99`) does **not** follow that shape — it mirrors `Sniff`'s own `io.ReadFull` + `io.MultiReader` re-stitch pattern (sequential-consuming, produces its own `rest`), because it needs a much larger peek window (`mp3PeekLen = 512 * 1024`) than a `ReadAt`-based check would need to re-derive on every call.

**The correctness trap:** By the time the audio branch is reached, `file`'s own sequential-Read cursor has already advanced past `sniffLen` (12) bytes (consumed by the very first `convert.Sniff(file)` call at `handlers.go:188`). Calling `convert.SniffAudio(file)` directly would start its 512 KiB peek from byte 12, not byte 0 — and its own re-stitched `rest2` would then be `[bytes 12..12+n) + file-from-(12+n)]`, silently **dropping the first 12 bytes of the uploaded file** the moment it reaches `s.storage.Upload(ctx, key, rest, ...)` later in the handler. This would be an invisible, silent data-corruption bug — every audio upload would be truncated by exactly `sniffLen` bytes, and no existing test would catch it unless it specifically diffs upload byte-length or replays the stored object.

**Correct wiring:** chain off the **existing** `rest` (which already correctly represents the full file from byte 0 — `Sniff`'s own re-stitch), not off `file`:
```go
if detected == "" {
	if audioDetected, audioRest, aerr := convert.SniffAudio(rest); aerr == nil && audioDetected != "" {
		detected = audioDetected
		rest = audioRest
	}
}
```
Place this branch alongside the existing OLE-CFB check (no `source` gate needed — unlike the HTML branch, `SniffAudio` self-identifies from magic bytes with no declared-source hint required, matching the ZIP/OLE-CFB branches' unconditional shape). `mp3PeekLen` (512 KiB) is materially more expensive to buffer than the other `ReadAt`-based checks, so ordering it **last** (after the cheaper checks fail) is correct and cheap for non-audio uploads (never reached).

**Verification for the planner:** a task/test should assert `header.Size == <stored object size>` for an audio upload with a large ID3v2 tag, or more directly assert the first N bytes of the round-tripped stored object match the original upload byte-for-byte — this is the kind of regression that "the transcription still works because the guard rejects garbage anyway" would NOT catch (whisper-cli might still produce *some* output on a truncated file).

### Pattern 3: Opts-validation switch — audio needs its own case (currently silently mis-routed)

**What:** `internal/api/handlers.go:373-415`'s opts-parsing switch has exactly two branches:
```go
switch engine {
case convert.EngineHTML:
	htmlOpts, err := convert.ParseHTMLOpts(...)
	...
default:
	docOpts, err := convert.ParseDocOpts(...)
	...
}
```
`default` is reached today by `EngineImage` and `EngineDocument` alike (image jobs never send `opts` in practice, so this branch is effectively dead for image — the `if rawOpts != "" && rawOpts != "{}"` guard above it skips the switch entirely when no opts are sent). **Once `AudioConverter` is registered, `EngineAudio` also falls into `default` unless a case is added.** `AudioOpts{Language, Translate}` (`internal/convert/audioopts.go:46-49`) shares zero JSON field names with `DocOpts` (`pdf_profile`) — `ParseDocOpts`'s `DisallowUnknownFields()` strict-parse would reject **every** audio job that sends `{"language":"ru"}` with a generic "invalid opts" 422, defeating AUD-03 (which Phase 30 already built and tested standalone) the moment a real client tries to use it through the live API.

**Fix — add a third case:**
```go
case convert.EngineAudio:
	audioOpts, err := convert.ParseAudioOpts([]byte(rawOpts))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid opts")
		return
	}
	if err := convert.ValidateAudioApplicability(engine, detected, target, audioOpts); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "opts not applicable to this conversion")
		return
	}
	normalizedRaw, err = json.Marshal(audioOpts)
	...
```
Mirror the `EngineHTML` branch's shape exactly (`ParseHTMLOpts`/`ValidateHTMLApplicability` → `ParseAudioOpts`/`ValidateAudioApplicability`, both already built and unit-tested by Phase 30 Plan 02).

### Pattern 4: Terminal/transient classifier — stage-aware split (Key Decision 1, BINDING)

**What exists today** (`internal/worker/worker.go:114-230`): a shared `isTerminal(err)` (no `context.DeadlineExceeded` arm — image path) and a `timeoutIsTerminal(err)` wrapper (`context.DeadlineExceeded` → terminal, everything else delegates to `isTerminal`) reused verbatim by `isDocumentTerminal`/`isHTMLTerminal`.

**⚠ Supersession note:** `.planning/research/ARCHITECTURE.md:152` (milestone-level research, written before the audio pitfalls were cross-checked) recommends `isAudioTerminal := timeoutIsTerminal(err)` — a blanket copy of the document/html shape. **This is superseded.** `.planning/STATE.md`'s Key Decision 1 (binding for this phase) explicitly overrides it: *"FEATURES.md/ARCHITECTURE.md recommended blanket-terminal (document precedent); PITFALLS.md's stage-aware split adopted as strictly-more-correct without added complexity."* Do not implement the `ARCHITECTURE.md` recommendation.

**The exact error shape to key on** (verified in `internal/convert/whisper.go`):
- Stage 1 (ffmpeg) failure, line 171: `fmt.Errorf("audio: ffmpeg: %w", err)`
- Stage 2 (whisper-cli) failure, line 181: `fmt.Errorf("audio: whisper-cli: %w", err)`
- Both stages run through `runCommand` (`internal/convert/exec.go:49`), whose timeout-kill path wraps `ctx.Err()` (i.e. `context.DeadlineExceeded`) as `fmt.Errorf("%s killed: %w", name, ctx.Err())` — this preserves `errors.Is(err, context.DeadlineExceeded)` all the way up through both `"audio: ffmpeg: %w"`/`"audio: whisper-cli: %w"` and `process()`'s outer `fmt.Errorf("convert: %w", err)` (`internal/worker/worker.go:636`).

**Recommended `isAudioTerminal` shape** (genuinely different from `isDocumentTerminal`/`isHTMLTerminal` — do not reuse `timeoutIsTerminal`):
```go
func isAudioTerminal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, convert.ErrAudioDurationExceeded) {
		return true // pre-decode guard rejection — no retry can fix an oversized declared duration
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "audio: ffmpeg:") {
		return true // ffmpeg-stage failure OR timeout: malformed/adversarial-input signal (Key Decision 1)
	}
	// whisper-cli-stage failure/timeout on already-duration-validated audio: transient by default —
	// mirrors the image engine's isTerminal (NO context.DeadlineExceeded arm here), bounded by
	// AUDIO_MAX_RETRY. Deliberately does NOT delegate to timeoutIsTerminal.
	return isTerminal(err) // still catches "no converter for"/minio.NoSuchKey via the shared classifier
}
```
This correctly implements: ffmpeg-stage failure (any reason, including timeout) → terminal; whisper-stage timeout on validated audio → transient (retried, bounded by `AUDIO_MAX_RETRY`); duration-guard rejection → terminal (a third, non-stage-prefixed case the roadmap's SC2 wording doesn't explicitly name but which must classify the same way as the ffmpeg-stage case — see Pattern 5). A non-timeout whisper-cli-stage failure (e.g. a corrupted model file, a whisper-cli crash) is **not** given its own terminal-signature list in this design — by the time whisper-cli runs, its input is a server-produced normalized WAV (ffmpeg already validated the client bytes decode cleanly), so a whisper-cli-stage failure that isn't a timeout is most plausibly an environment/config problem, not malformed client input — defaulting it to transient (via the shared `isTerminal` fallthrough) is consistent with "mirror the image engine's classifier" (Key Decision 1's own phrasing) and avoids inventing an unverified terminal-signature list this phase's success criteria don't require. **Flag this specific sub-decision to the planner as `Claude's Discretion` if `CONTEXT.md` doesn't already resolve it** — it is a reasonable extrapolation from Key Decision 1's stated text, not something the roadmap/STATE.md decided explicitly.

### Pattern 5: Duration-guard wiring — closes T-30-08/IN-02 (the "actually invoked" requirement)

**What exists today:** `EnforceMaxDuration` (`internal/convert/audioduration.go:81`) is fully built, unit-tested, and **has zero call sites in production code** (verified by grep). `30-SECURITY.md`'s T-30-08 entry explicitly carries forward: *"Phase 31's audit must show the guard is actually invoked before Convert, not just documented."*

**Where it must run:** `process()` (`internal/worker/worker.go:596-657`) is the single shared engine-agnostic pipeline every `Handle*Convert` method calls: `inputKey → registry.Lookup → downloadTo → conv.Convert → uploadFrom → AddOutput → MarkDone`. The duration guard needs the **downloaded local file** (`inPath`, only valid after `downloadTo` at line 631) — it cannot run earlier (API upload time) without buffering the entire multipart upload into a temp file first, which none of the three existing engine classes do and which would be a much larger, separately-risky change. **Recommended splice point:** inside `process()`, immediately after `downloadTo` (line 631-633) and before `conv.Convert` (line 635), gated on `job.Engine == convert.EngineAudio` (the `job` struct is already in scope — no signature change needed):
```go
if job.Engine == convert.EngineAudio {
	if err := convert.EnforceMaxDuration(attemptCtx, inPath, h.audioMaxDuration); err != nil {
		return err
	}
}
```
Add `audioMaxDuration time.Duration` as a new field on `Handler` (`internal/worker/worker.go:233-243`), threaded through `NewHandler`'s existing wide-positional-parameter constructor (already has 9 params; the three non-audio `cmd/*/main.go` call sites already pass irrelevant fields as `nil`/`0` for engine-scoped fields they don't use — e.g. `cmd/document-worker/main.go:60-64` passes `nil, nil, ..., nil, 0` for the webhook-only fields — so a 10th audio-only field follows the established pattern exactly, non-audio callers pass `0`).

**Classification:** `EnforceMaxDuration` returns `ErrAudioDurationExceeded` (`internal/convert/audioduration.go:19`, `errors.Is`-matchable) — this must classify **terminal** in `isAudioTerminal` (Pattern 4 above already includes this check) since no retry fixes an oversized declared duration; it is the audio analog of the image dimension-bomb rejection, which is also terminal (`isTerminal`'s `terminalVipsSignatures`/`"no converter for"` pattern — same fail-closed philosophy, VALID-03 precedent).

**Note on "predictable 422":** `AUD-04`'s original phrasing (Phase 30, already marked Complete) framed this as producing an HTTP 422 — but a worker-side rejection (post-upload, post-`queued`) cannot synchronously return an HTTP status to the original `POST /v1/jobs` caller; it can only mark the job `failed` with a sanitized `error_code`/`error_message` (same pattern every other terminal engine error uses, e.g. `"engine_error"`/`"unsupported or corrupted input format"` at `worker.go:300`). Recommend a dedicated `error_code` (e.g. `"duration_exceeded"`) distinct from the generic `"engine_error"` so a polling client can distinguish "your 90-minute file was rejected for being too long" from "the file was corrupt" — this is a UX/API-contract decision the planner should make explicitly, not silently reuse the generic terminal-error code for.

### Pattern 6: Queue/task/UniqueTTL — a 4th near-identical function set (proven shape, zero ambiguity)

`internal/queue/queue.go` has three parallel, independently-written (not shared/parameterized) implementations for image/document/html of: task-type const, queue-name const, `New{Class}ConvertTask`, `{class}RetrySchedule` var, `{Class}RetryDelay` func, `{class}BackoffSum` func, `{Class}UniqueTTL` func. Add a fourth, following the **document** shape exactly (document is the closer analog — no jitter, `(maxRetry+1)*engineTimeout + backoffSum + uniqueTTLSafetyMargin` formula, reuses the shared `uniqueTTLSafetyMargin` constant verbatim per every existing class's own doc comment: *"REUSES the shared uniqueTTLSafetyMargin const verbatim — no {class}-specific margin constant"*).

**Naming convention correction vs. milestone research:** `.planning/research/ARCHITECTURE.md:57-58` uses `TypeAudioTranscribe`/`EnqueueAudioTranscribe` naming. **Recommend `TypeAudioConvert`/`EnqueueAudioConvert`/`NewAudioConvertTask`/`QueueAudio` instead**, matching the codebase's own explicit single-source-of-truth discipline: `convert.go:11-18`'s doc comment states engine-class literals are referenced by "the queue-name constants (`internal/queue/queue.go`)" with **no distinct verb per engine** — every existing task type is `{class}:convert` (`TypeImageConvert = "image:convert"`, etc.), and `AudioConverter` itself implements the shared `Converter` interface's `Convert()` method (not a `Transcribe()` method) — `Converter.Convert` is the operation name throughout the codebase regardless of what the underlying engine class actually does semantically (LibreOffice "converts", chromium "renders", but the method/task/queue name is uniformly `Convert`). Diverging naming for audio alone would break the DEBT-02 single-source-of-truth discipline `convert.go` explicitly calls out.

```go
// queue.go additions (mirror DocumentUniqueTTL's shape/doc-comment exactly):
const TypeAudioConvert = "audio:convert"
const QueueAudio = convert.EngineAudio

func NewAudioConvertTask(jobID uuid.UUID, maxRetry int, uniqueTTL time.Duration) (*asynq.Task, error) { ... }

var audioRetrySchedule = []time.Duration{ /* pick a schedule — see Discretion below */ }
func AudioRetryDelay(n int, e error, t *asynq.Task) time.Duration { ... }
func audioBackoffSum(maxRetry int) time.Duration { ... }
func AudioUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration {
	return time.Duration(maxRetry+1)*engineTimeout + audioBackoffSum(maxRetry) + uniqueTTLSafetyMargin
}
```
Also add `TypeAudioConvert` to `RetryDelayFunc`'s dispatch switch (`queue.go:281-294`) — a missed case here silently falls through to `asynq.DefaultRetryDelayFunc`, not a compile error, so it is easy to forget.

**`client.go` additions:** `audioMaxRetry`/`audioUniqueTTL` fields on `Client`, populated in `NewClient()` from `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT` env vars (mirror `documentMaxRetry`/`documentUniqueTTL`'s two-line block exactly, `client.go:69-79`), plus `EnqueueAudioConvert` method (mirror `EnqueueDocumentConvert`, `client.go:114-123`).

**Test template (mandatory per Pitfalls research + AUD-05 SC3):** `TestAudioUniqueTTL` in `internal/queue/queue_test.go`, mirroring `TestDocumentUniqueTTL` (`queue_test.go:262-283`) exactly: worked-example assertion, monotonicity-vs-`maxRetry`, monotonicity-vs-`engineTimeout`, and — the SC3-specific addition — an explicit assertion that the derived TTL **strictly exceeds** `(maxRetry+1)*engineTimeout + backoffSum` (the worst-case retry lifetime with zero margin), i.e. the safety-margin term is genuinely load-bearing, not accidentally zero.

### Pattern 7: `Enqueuer`/`enqueuer` interfaces — two separate interfaces, both need extending

Two distinct, independently-declared interfaces both gate on the enqueue methods and both need a fourth method:
- `internal/api/api.go:53-57` — `Enqueuer interface { EnqueueImageConvert; EnqueueDocumentConvert; EnqueueHTMLConvert }` (consumed by `handleCreateJob`'s enqueue switch)
- `internal/reconciler/reconciler.go:59-64` — `enqueuer interface { EnqueueImageConvert; EnqueueWebhookDeliver; EnqueueDocumentConvert; EnqueueHTMLConvert }` (consumed by `sweep()`'s recovery switch)

Both need `EnqueueAudioConvert(ctx context.Context, jobID uuid.UUID) error` added. `*queue.Client` satisfies both structurally (Go interfaces) the moment `EnqueueAudioConvert` is added to it — no explicit `implements` declaration needed, but **both test doubles** (`internal/reconciler/reconciler_test.go`'s `fakeEnqueuer`, ~line 89-133, and any API-layer test fakes for `Enqueuer`) need a matching `EnqueueAudioConvert` method added or they will fail to compile against the widened interface.

### Pattern 8: Reconciler engine-routing switch

`internal/reconciler/reconciler.go:282-302`'s `sweep()` switches on `j.Engine` (a plain string read from the `jobs.engine` column, `StaleJob.Engine`, `reconciler.go:41`) to decide which `Enqueue*Convert` to call for a stranded job. Add:
```go
case convert.EngineAudio:
	enqueueErr = s.enq.EnqueueAudioConvert(ctx, j.ID)
```
alongside the existing three cases. The `default:` branch (fail-closed, `metrics.RecordReconcilerAction("unroutable_engine")`, no enqueue) is exactly what currently happens to an audio job today (since `'audio'` isn't even a legal `jobs.engine` value yet) — once the migration lands and this case is added, stranded audio jobs recover the same way every other class does.

**Test template:** `TestSweepRoutesAudioJobsToAudioQueue`, mirroring `TestSweepRoutesDocumentJobsToDocumentQueue`/`TestSweepRoutesHTMLJobsToHTMLQueue` (`reconciler_test.go:202-254`) exactly — construct a `fakeStore` with `jobs.StaleJob{Engine: "audio"}`, assert `enq.audioCalls` (new field on `fakeEnqueuer`) fires and the other three `Enqueue*` methods do not.

### Pattern 9: `cmd/audio-worker/main.go` — template is `cmd/document-worker/main.go` verbatim, one field swap

`cmd/document-worker/main.go` (166 lines, read in full) is the exact template: connect Postgres → connect storage → connect Redis/queue → build `worker.NewHandler(repo, store, convert.Default, envDuration("AUDIO_ENGINE_TIMEOUT", ...), nil, nil, qc, nil, 0)` (webhook-only fields nil, per Pattern 5's new `audioMaxDuration` field also threaded here from `envDuration("AUDIO_MAX_DURATION_SECONDS", ...)` if that setter design is chosen — see Pattern 5's note on where the ceiling value comes from) → `mux.HandleFunc(queue.TypeAudioConvert, h.HandleAudioConvert)` → `asynq.NewServer` with `Concurrency: envInt("AUDIO_WORKER_CONCURRENCY", ...)`, `Queues: map[string]int{queue.QueueAudio: 1}`, `ShutdownTimeout: envDuration("AUDIO_ENGINE_TIMEOUT", ...) + 10*time.Second` → own localhost `/metrics` listener. **No sweeper is constructed here** (the existing comment at `document-worker/main.go:67-70` explains why: the sweeper runs solely in `cmd/webhook-worker` under the Postgres advisory lock, to avoid a double-sweep race — this applies identically to audio, do not add a second sweeper instance).

**Default values — explicitly NOT this phase's job to finalize.** Per the roadmap's scope fence and `PITFALLS.md` Pitfall 1/5, `AUDIO_ENGINE_TIMEOUT`/`AUDIO_WORKER_CONCURRENCY` real sizing is Phase 32's RTF-measurement gate. This phase needs *some* placeholder default so `go run ./cmd/audio-worker` and unit tests function — recommend an explicit, clearly-commented placeholder (e.g. `envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second)` — an order of magnitude above `DOCUMENT_ENGINE_TIMEOUT`'s 300s, matching `PITFALLS.md` Pitfall 1's warning not to copy the document default verbatim) with an explicit code comment that Phase 32 will replace it with a measured value, mirroring how `VERAPDF_TIMEOUT`'s comment explains its relationship to `DOCUMENT_ENGINE_TIMEOUT`. **Flag this default value as `[ASSUMED]`** — no RTF measurement exists yet to justify a specific number.

### Anti-Patterns to Avoid

- **Copying `isDocumentTerminal`/`isHTMLTerminal`'s `timeoutIsTerminal(err)` one-liner for `isAudioTerminal`.** This is the literal `ARCHITECTURE.md` recommendation and is explicitly superseded by the binding Key Decision 1 (Pattern 4).
- **Calling `SniffAudio(file)` instead of `SniffAudio(rest)`.** Silently truncates every audio upload by `sniffLen` (12) bytes (Pattern 2).
- **Letting audio opts fall through the `default:` case of the opts-parsing switch.** Every audio job with `opts` gets a spurious "invalid opts" 422 (Pattern 3).
- **Registering `AudioConverter{}` with its zero-value `modelPath`** in a way that only resolves at container-bake time (Phase 32) with no local-dev override — blocks this phase's own local E2E target (`go run ./cmd/audio-worker` against compose infra + local ffmpeg/whisper-cli, already verified present on this dev machine).
- **Forgetting the `jobs.engine` CHECK-constraint migration.** Every other splice point can be code-complete and still 500 at `repo.Create` with a raw Postgres constraint-violation error the moment the first audio job is created (Pattern 10, below) — this is the single most likely "looks done in code review, fails on first live test" gap for this phase.
- **Adding `QueueAudio` to `cmd/api/main.go`'s `NewQueueDepthCollector` call this phase.** That registration is explicitly `AUD-08`'s job (Phase 33, per `REQUIREMENTS.md`'s traceability table and the roadmap's own Phase 33 SC2) — Phase 31's scope fence excludes KEDA/chart work. Adding it early is low-risk but is scope creep against the phase boundary the roadmap drew; flag it to the planner as an explicit inclusion/exclusion decision rather than silently doing it.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Per-job duplicate-task prevention | A custom Redis lock/mutex for audio jobs | `asynq.Unique(uniqueTTL)` via `AudioUniqueTTL` (Pattern 6) | Every other engine class already solved this; a bespoke lock would duplicate `ImageUniqueTTL`'s entire worst-case-lifetime reasoning for no benefit and risks getting the "+1 attempt" archive-timing correction wrong (`queue.go:318-325`'s documented gotcha) |
| Stranded-job recovery for audio | A dedicated audio-only sweep goroutine in `cmd/audio-worker` | The existing fleet-wide `reconciler.Sweeper` (Pattern 8) | A second independent sweeper would race the existing advisory-lock-gated one; `document-worker/main.go`'s own comment explicitly warns against this shape |
| Terminal/transient error signatures | Regex/heuristic scanning of raw whisper-cli/ffmpeg stderr | The existing `strings.Contains(msg, "audio: ffmpeg:")`/`"audio: whisper-cli:"` prefix convention `whisper.go` already emits for exactly this purpose | The prefixes were deliberately engineered in Phase 30 (see `whisper.go:164-168`'s comment: *"lets a FUTURE worker-layer classifier (Phase 31...) split ffmpeg-stage failures... from whisper-stage failures"*) — building anything more elaborate ignores work already done for this exact purpose |

**Key insight:** This phase's risk surface is almost entirely *integration correctness* (did every one of the ~9 files that changed for html/document also get its audio-analog change, in the same shape), not novel engineering — every "Don't Hand-Roll" item above is a reminder that the pattern already exists three times in this codebase and a fourth near-identical implementation is the correct amount of code, not a shared abstraction.

## Common Pitfalls

### Pitfall 1: `jobs.engine` CHECK constraint blocks every audio job at INSERT time — easy to miss because nothing in the phase description names it

**What goes wrong:** `internal/db/migrations/0001_init.sql:47-48` declares `CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe'))`. `0005_html_engine.sql` already had to `DROP CONSTRAINT`/`ADD CONSTRAINT` to admit `'html'` when Phase 15 shipped the chromium engine — the exact same migration shape is needed now for `'audio'`. Every other splice point in this research (converter registration, queue wiring, worker handler, reconciler routing) can be 100% code-complete and still fail the first live E2E test with a raw Postgres `CHECK constraint "jobs_engine_check" violated` error surfaced as a generic `"failed to create job"` 500 at `handlers.go:480` — a confusing failure mode that gives no hint the root cause is a missing migration.

**Why it happens:** The phase description's research priorities list explicitly named "converters.go registration", API/worker/reconciler wiring, and env vars — but not the database schema, because every other engine-class addition's migration was a one-off historical event (0005) that isn't visible from reading the currently-live code paths (only from reading the migration files directory, which nothing in the phase description pointed at).

**How to avoid:** Add `internal/db/migrations/0006_audio_engine.sql`:
```sql
-- Add 'audio' to the jobs.engine allow-list (AUD-05).
ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check
    CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe', 'html', 'audio'));
```
mirroring `0005_html_engine.sql` verbatim (including its own doc-comment noting the constraint name is Postgres's auto-generated name for the unnamed inline CHECK from `0001_init.sql`). `db.Migrate()` runs automatically at `cmd/api/main.go:52` on every API startup (embedded-migration pattern, `internal/db/db.go`) — no separate deploy step needed, but the migration file must exist and be numbered correctly (`0006_`, next after `0005_`) for the embed glob to pick it up.

**Warning signs:** Any live test that gets as far as `POST /v1/jobs` with an audio file and receives a generic 500 instead of `202 Accepted` — check Postgres logs / the actual wrapped error (not the sanitized client-facing message) for `jobs_engine_check` before assuming the routing/opts code is broken.

### Pitfall 2: Global `RECONCILER_ACTIVE_STALE_AFTER` is not per-engine — SC4's literal wording is achievable but changes staleness detection for image/document/html too

**What exists:** `internal/reconciler/reconciler.go`'s `Config{QueuedStaleAfter, ActiveStaleAfter}` is a single pair of durations applied identically to every engine class via one query (`jobs.Repo.FindStale`, `internal/jobs/repo.go:221-246`, `WHERE (status='queued' AND created_at < $1) OR (status='active' AND started_at < $2)` — one `$2` cutoff for the whole table, not per-engine). `ActiveStaleAfter` is read **once**, in `cmd/webhook-worker/main.go:83`, from a single `RECONCILER_ACTIVE_STALE_AFTER` env var — there is no per-engine override anywhere in the schema or config shape.

**What this means for SC4:** "`RECONCILER_ACTIVE_STALE_AFTER` for audio is set above `AUDIO_ENGINE_TIMEOUT`" is literally satisfiable by raising the **global** env var default — but doing so also raises the staleness threshold for image (currently comfortably covered by the 5-minute default against a 120s `ENGINE_TIMEOUT`) and document (currently *exactly equal* to the 5-minute default against its 300s `DOCUMENT_ENGINE_TIMEOUT` — already the tightest-margin case in the system today, worth independently flagging). If `AUDIO_ENGINE_TIMEOUT` lands anywhere near the tens-of-minutes range `PITFALLS.md` Pitfall 1 predicts (a realistic RTF-based estimate, not yet measured), raising the *global* threshold to comfortably exceed it means image/document/html jobs would need to hang for potentially 30-60+ minutes before the reconciler even attempts recovery — a real, if low-probability-of-mattering-in-practice, regression in how quickly those classes' stranded jobs get noticed.

**Two real options, not decided here (flag to planner/CONTEXT.md):**
1. **Raise the global default** (simplest, zero schema change, matches SC4's literal wording) — accept the tradeoff explicitly, document why, and rely on the existing enqueue-first + `asynq.ErrDuplicateTask` guard (already proven safe by `TestSweepSkipsDuplicateEnqueue`, `reconciler_test.go:280-303`) to make the *actual* safety property hold regardless of how long the staleness window is (a correctly-derived `AudioUniqueTTL` is what prevents double-processing, not the staleness threshold — the threshold only decides *when to attempt* recovery, per `PITFALLS.md` Pitfall 3's own framing).
2. **Make `ActiveStaleAfter` per-engine** — a `Config` shape change (`map[string]time.Duration` or four named fields) threaded through `FindStale`'s query (would need either a per-engine SQL `CASE` or four separate queries unioned) — larger, more correct, more invasive; `PITFALLS.md`'s own "Recovery Strategies" table (line 295) explicitly names this as the low-cost fallback if sweep noise turns out to matter, implying it's an acceptable-to-defer refactor, not a same-phase requirement.

**Recommendation:** Option 1 for this phase, sized deliberately (not copy-pasted), with the SC4 test (Pattern 8 above / `TestSweepSkipsDuplicateEnqueue`-style) proving the *actual* safety property (zero spurious `reconciler_recovery` events) holds under the new global value — this is what SC4's test description literally asks for, and it does not require the larger per-engine refactor to satisfy. Document the image/document staleness-detection-latency tradeoff explicitly in the phase's own decision log rather than leaving it an unstated side effect.

### Pitfall 3: `MAX_UPLOAD_BYTES` (global 100 MiB) may 413 legitimate long audio — surfaced, not decided

`internal/api/handlers.go:93` caps every upload (all engine classes) at `s.maxUploadByte` (`cfg.MaxUploadBytes`, default `100 << 20` = 100 MiB, set in `cmd/api/main.go:125`). An uncompressed 1-hour WAV at CD quality (44.1kHz/16-bit/stereo) is ~635 MB — well over the current global ceiling; even a typical 128kbps MP3 of the same length is ~57.6 MB, comfortably under, but a longer or higher-bitrate legitimate recording could still 413. This is the same global-vs-per-class tension as Pitfall 2, one level up the stack (byte-size ceiling vs. duration ceiling). `STATE.md`'s Accumulated Context already names this as an open concern ("decide a per-format/engine ceiling deliberately during Phase 30/32, do not let it fail silently") but attributes it to Phase 30/32, not 31 — worth a one-line note in this phase's own context/decisions doc pointing at where it will actually be resolved (likely Phase 32, alongside the RTF-driven `AUDIO_ENGINE_TIMEOUT` sizing, since duration and byte-size are correlated for a given bitrate) so it isn't silently dropped between phases. **This research does not recommend a specific limit — surfacing only, per the phase description's explicit instruction.**

### Pitfall 4: `AudioConverter{}`'s model path has no local-dev-friendly default for THIS phase's own E2E target

The phase's stated E2E target is `go run ./cmd/audio-worker` against compose infra (Postgres/Redis/MinIO) plus **locally-installed** ffmpeg/whisper-cli — verified already present on this dev machine (`/opt/homebrew/bin/ffmpeg`, `/opt/homebrew/bin/whisper-cli`, `~/.cache/whisper/ggml-base.bin`, all provisioned by Phase 30 Plan 01). But `defaultAudioModelPath = "/models/ggml-base.bin"` (`whisper.go:28`) is a container-bake-time path that does not exist on this machine. If `converters.go`'s `init()` registers a bare `AudioConverter{}` (zero-value `modelPath`), every local worker run will fail at `whisper-cli`'s `-m` flag with a file-not-found error, blocking this phase's own verification. Resolve via Pattern 1's env-only setter (mirroring `SetVeraPDFTimeout`) reading e.g. `AUDIO_MODEL_PATH` (already referenced as an env override possibility in `30-03-SUMMARY.md`'s "Model path via injected struct field" note) with a fallback to `defaultAudioModelPath` for the (Phase-32-built) container case.

## Code Examples

### `TestDocumentUniqueTTL` — the exact template for `TestAudioUniqueTTL`

```go
// Source: internal/queue/queue_test.go:262-283 (verified read, this repo)
func TestDocumentUniqueTTL(t *testing.T) {
	maxRetry := 3
	engineTimeout := 300 * time.Second
	backoffSum := 5*time.Second + 15*time.Second + 30*time.Second // i=0..2

	want := time.Duration(maxRetry+1)*engineTimeout + backoffSum + uniqueTTLSafetyMargin
	got := DocumentUniqueTTL(maxRetry, engineTimeout)
	if got != want {
		t.Errorf("DocumentUniqueTTL(%d, %v) = %v, want %v", maxRetry, engineTimeout, got, want)
	}
	// Monotonicity: raising either argument must never shrink the TTL.
	if DocumentUniqueTTL(maxRetry+1, engineTimeout) <= DocumentUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("DocumentUniqueTTL must grow monotonically with maxRetry")
	}
	if DocumentUniqueTTL(maxRetry, engineTimeout+time.Second) <= DocumentUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("DocumentUniqueTTL must grow monotonically with engineTimeout")
	}
}
```

### `TestSweepSkipsDuplicateEnqueue` — the exact template for SC4's "zero spurious recovery" proof

```go
// Source: internal/reconciler/reconciler_test.go:280-303 (verified read, this repo)
func TestSweepSkipsDuplicateEnqueue(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusQueued, Engine: "image"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{enqueueImageErr: asynq.ErrDuplicateTask}
	s := NewSweeper(store, enq, testConfig())
	s.sweep(context.Background())
	if store.requeueStaleCalls != 0 {
		t.Fatalf("RequeueStale should NOT be called on duplicate enqueue, got %d calls", store.requeueStaleCalls)
	}
	if store.recoveryCount[id] != 0 {
		t.Fatalf("recovery count = %d, want 0 (no spurious recovery recorded)", store.recoveryCount[id])
	}
}
// Audio-specific SC4 variant: set Engine: "audio", enqueueAudioErr: asynq.ErrDuplicateTask,
// and call s.sweep() repeatedly (multiple ticks) asserting recoveryCount stays 0 across all of them.
```

### `0005_html_engine.sql` — the exact template for the required `0006_audio_engine.sql`

```sql
-- Source: internal/db/migrations/0005_html_engine.sql (verified read, this repo)
ALTER TABLE jobs DROP CONSTRAINT jobs_engine_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_engine_check
    CHECK (engine IN ('image', 'document', 'av', 'cad', 'archive', 'probe', 'html'));
-- Audio migration (0006) adds 'audio' to this same list, same DROP/ADD shape.
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|---------------|--------|
| Blanket `timeoutIsTerminal` for a new engine class (document/html precedent) | Stage-aware split for audio (Key Decision 1) | Decided during this milestone's discuss/research phase (2026-07-17/18), before Phase 31 execution | `isAudioTerminal` must NOT reuse `timeoutIsTerminal` — it needs its own function keyed on the `"audio: ffmpeg:"`/`"audio: whisper-cli:"` prefixes |

**Deprecated/outdated:** `.planning/research/ARCHITECTURE.md`'s `isAudioTerminal := timeoutIsTerminal(err)` recommendation (line 152) — superseded by `STATE.md`'s Key Decision 1. Do not implement as written in `ARCHITECTURE.md`.

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | `AUDIO_ENGINE_TIMEOUT` placeholder default should be materially larger than `DOCUMENT_ENGINE_TIMEOUT` (e.g. 600s) pending Phase 32's RTF measurement | Pattern 9 | If set too low, this phase's own E2E test against a real audio fixture could spuriously time out; if set too high, `AudioUniqueTTL`/`ShutdownTimeout` inherit an oversized placeholder — low risk either way since Phase 32 explicitly re-derives it |
| A2 | Non-timeout whisper-cli-stage failures should default to transient (no dedicated terminal-signature list this phase) | Pattern 4 | If wrong (some whisper-cli failure mode actually is deterministic/malformed-input-driven), an audio job could retry `AUDIO_MAX_RETRY` times against a guaranteed-repeat failure before finally failing — bounded cost (not unbounded), and correctable in a later phase by adding a `terminalWhisperSignatures` list mirroring `terminalLibreOfficeSignatures` |
| A3 | Error code `"duration_exceeded"` (distinct from generic `"engine_error"`) is the right client-facing signal for a duration-guard rejection | Pattern 5 | Low risk — purely an API-contract naming choice; either code correctly marks the job terminal/failed, this only affects how precisely a polling client can distinguish failure reasons |
| A4 | `EnqueueAudioConvert`/`TypeAudioConvert`/`QueueAudio` naming (not `*Transcribe*`, contra `ARCHITECTURE.md`) is the correct convention to follow | Pattern 6 | Low risk — a naming-only divergence from one research doc; the DEBT-02 single-source-of-truth precedent in `convert.go`'s own doc comment supports this reading, but the planner/CONTEXT.md should confirm if not already locked |

## Open Questions

1. **How should `AudioConverter`'s model path resolve locally vs. in-container?**
   - What we know: `defaultAudioModelPath` is a container-bake-time constant; Phase 30 injected a test-only `modelPath` struct field; `SetVeraPDFTimeout` is the existing precedent for an env-only setter pattern for `internal/convert`-internal config.
   - What's unclear: whether the planner wants a new `convert.SetAudioModelPath` setter (consistent with the VeraPDF precedent) or a different mechanism (e.g. registering `AudioConverter{modelPath: ...}` directly with a value read in `converters.go`'s `init()` — harder, since `init()` runs before flags/env parsing in `main()` in the general case, though `os.Getenv` inside `init()` would technically work since env vars are process-inherited before `main` runs).
   - Recommendation: mirror `SetVeraPDFTimeout` exactly (setter called explicitly in `cmd/audio-worker/main.go` before `srv.Start`, not read inside `converters.go`'s `init()`) — matches the codebase's stated convention that `internal/convert` never calls `os.Getenv` directly.

2. **Does the reconciler's global `ActiveStaleAfter` need a per-engine override in THIS phase, or is the global-raise (Pitfall 2, option 1) sufficient?**
   - What we know: SC4's wording is satisfiable either way; the enqueue-first + `ErrDuplicateTask` guard is the actual safety mechanism regardless.
   - What's unclear: whether the discuss-phase/CONTEXT.md has an opinion on the document-class staleness-detection-latency tradeoff a global raise introduces.
   - Recommendation: option 1 (global raise) unless CONTEXT.md says otherwise — matches `PITFALLS.md`'s own explicit recommendation and avoids an unscoped `Config` shape refactor.

## Environment Availability

| Dependency | Required By | Available | Version | Fallback |
|------------|------------|-----------|---------|----------|
| Go toolchain | Everything in this phase | ✓ | go1.26.5 darwin/arm64 | — |
| ffmpeg | Worker-side `AudioConverter.Convert` stage 1 | ✓ | `/opt/homebrew/bin/ffmpeg` (Phase 30-provisioned) | — |
| ffprobe | `EnforceMaxDuration` | ✓ | `/opt/homebrew/bin/ffprobe` | — |
| whisper-cli v1.9.1 + `ggml-base.bin` | `AudioConverter.Convert` stage 2 | ✓ | `/opt/homebrew/bin/whisper-cli`, `~/.cache/whisper/ggml-base.bin` (Phase 30-provisioned, SHA-256 verified) | — |
| Docker (for compose infra) | Postgres/Redis/MinIO for local E2E | ✓ | Docker 29.4.0 (OrbStack context) | — |
| octoconv's own `docker-compose` stack (postgres:5434, redis, minio) | Local E2E against real Postgres/Redis/S3 | ✗ (not currently running — only an unrelated project's containers and a k8s cluster are up) | — | `docker compose up -d` before running the phase's E2E gate; per `STATE.md`'s own operational-discipline note, never run compose and k8s stacks hot simultaneously on this OrbStack setup |
| redis-cli | Manual Redis inspection during debugging | ✗ (not installed) | — | Use `docker compose exec redis redis-cli` or asynq's own `asynq.Inspector` (already used by the queue-depth collector) instead of a bare CLI |

**Missing dependencies with no fallback:** None — the one "✗" (octoconv's own compose stack) has a direct, documented fallback (`docker compose up -d`) already part of this project's normal dev workflow.

**Missing dependencies with fallback:** `redis-cli` (use `docker compose exec` or the asynq Inspector API instead); octoconv's compose stack (start it before running the phase's live E2E gate — not currently running because a k8s cluster is currently up, per the project's own "never run both hot" discipline).

## Security Domain

### Applicable ASVS Categories

| ASVS Category | Applies | Standard Control |
|---------------|---------|-----------------|
| V2 Authentication | no (unchanged — existing API-key auth middleware already gates every `/v1` route, this phase adds no new auth surface) | — |
| V3 Session Management | no | — |
| V4 Access Control | no (unchanged — job ownership check at `handleGetJob` already applies uniformly regardless of engine class) | — |
| V5 Input Validation | yes | Already-built `SniffAudio` (magic bytes, fail-closed) + `EnforceMaxDuration` (declared-duration ceiling, fail-closed) — this phase's job is correctly *invoking* both, not building new validation (Patterns 2 and 5) |
| V6 Cryptography | no | — |

### Known Threat Patterns for this phase's stack

| Pattern | STRIDE | Standard Mitigation |
|---------|--------|---------------------|
| Duration guard built but never invoked (T-30-08 carried-forward) | Denial of Service | Wire `EnforceMaxDuration` into `process()` before `Convert()` (Pattern 5) — this phase's core security-relevant task, already flagged by `30-SECURITY.md` as requiring live audit verification, not just a code read |
| `SniffAudio` peek-chaining bug silently truncating uploads | Tampering (data integrity, not directly an attacker-controlled exploit but a correctness/integrity defect) | Chain off `rest`, not `file` (Pattern 2) |
| ffmpeg/ffprobe treating path arguments as URL specifiers (IN-01, `30-REVIEW.md`) | Tampering / potential SSRF-adjacent (`concat:`/`http:`/`pipe:` protocol specifiers) | Both call sites (`audioduration.go:57-58`'s `ProbeDuration`, `whisper.go:169-172`'s ffmpeg stage) pass server-generated, workdir-rooted absolute paths today — never client bytes directly — but IN-01 recommends defense-in-depth now, while this phase is already touching the wiring: either prefix with `"file:"` (ffmpeg/ffprobe both support the `file:` protocol specifier) or assert the path is absolute and rooted under the caller's `workDir` before invoking. Cheap, two call sites, no functional behavior change for the current (always-safe) caller. |
| Audio opts silently mis-validated as `DocOpts` (Pattern 3) | Tampering (opts injection surface reopens if the closed-allowlist `AudioOpts` path is bypassed) | Add the missing `EngineAudio` switch case — without it, every audio opts request 422s (fails safe, not an actual injection risk today, but defeats AUD-03's shipped feature) |
| `jobs.engine` CHECK constraint gap (Pitfall 1) | N/A (availability/correctness, not a security threat) | Migration `0006_audio_engine.sql` |

## Sources

### Primary (HIGH confidence — direct code reads, this repository, current commit)
- `internal/api/handlers.go` (content-detection chain, opts-parsing switch, enqueue switch) — full file read
- `internal/worker/worker.go` (all four `Handle*Convert` methods, `isTerminal`/`timeoutIsTerminal`/`isDocumentTerminal`/`isHTMLTerminal`, shared `process()`) — full file read
- `internal/queue/queue.go`, `internal/queue/client.go` (task types, queue names, retry schedules, UniqueTTL derivations for all three existing classes) — full files read
- `internal/reconciler/reconciler.go` (engine-routing switch, `Config`/`FindStale` global-threshold shape, advisory-lock sweeper) — full file read
- `internal/convert/convert.go`, `converters.go`, `whisper.go`, `audioopts.go`, `audioduration.go`, `audiosniff.go`, `exec.go`, `sniff.go`, `dimensions.go` — full files read
- `internal/db/migrations/0001_init.sql`, `0005_html_engine.sql` (jobs.engine CHECK constraint history) — full files read
- `cmd/api/main.go`, `cmd/document-worker/main.go`, `cmd/webhook-worker/main.go` (existing worker-entrypoint templates) — full files read
- `internal/reconciler/reconciler_test.go`, `internal/queue/queue_test.go` (existing test templates for the SC3/SC4 proofs) — relevant sections read
- `.planning/phases/30-audio-engine-foundation/30-01-SUMMARY.md`, `30-02-SUMMARY.md`, `30-03-SUMMARY.md`, `30-REVIEW.md`, `30-SECURITY.md` — full files read
- `.planning/STATE.md`, `.planning/REQUIREMENTS.md`, `.planning/ROADMAP.md` — relevant sections read

### Secondary (MEDIUM confidence)
- `.planning/research/PITFALLS.md` (milestone-level research, Pitfalls 1-3 directly relevant to this phase, cross-checked against and consistent with the direct code reads above)
- `.planning/research/ARCHITECTURE.md` (milestone-level research; its `isAudioTerminal` recommendation is explicitly identified as superseded by this phase's binding Key Decision 1 — flagged, not silently followed)

### Tertiary (LOW confidence)
None — this phase required no external/WebSearch research; it is entirely an internal-codebase integration mapping exercise.

## Metadata

**Confidence breakdown:**
- Standard stack: N/A — no new dependencies
- Architecture/splice-points: HIGH — every claim is a direct file:line code read, cross-verified against the three existing engine-class implementations
- Pitfalls: HIGH for Pitfall 1 (migration gap, directly verified via the SQL files) and Pitfall 4 (model path, directly verified via `ls ~/.cache/whisper/`); MEDIUM for Pitfall 2/3 (both are genuine open architectural tensions with two defensible resolutions, not a single verified "correct" answer — appropriately left for planner/CONTEXT.md decision)

**Research date:** 2026-07-18
**Valid until:** Stable — this research is a snapshot of the current codebase's splice points, not time-sensitive external documentation; revalidate only if Phase 30's code changes before Phase 31 execution begins (unlikely, Phase 30 is marked complete and reviewed).
