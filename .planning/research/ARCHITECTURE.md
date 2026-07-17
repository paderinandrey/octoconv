# Architecture Research

**Domain:** Async file-conversion microservice — adding a 4th engine class (offline audio transcription via whisper.cpp) to an established Go/asynq/Postgres/S3 pipeline, plus a v1.6 hardening tail.
**Researched:** 2026-07-17
**Confidence:** HIGH (every claim below is grounded in direct reads of the real `octoconv` codebase — `internal/convert`, `internal/queue`, `internal/worker`, `internal/api`, `internal/reconciler`, `internal/metrics`, `cmd/*-worker`, `deploy/chart/octoconv`, `docker-compose.yml`, `.github/workflows/ci.yml` — cross-referenced against three prior engine-class additions: image (v1.0), document (v1.2), html (v1.3). whisper.cpp CLI/model facts are MEDIUM confidence, WebSearch-verified since there is no existing in-repo precedent for that specific tool.)

## Standard Architecture

### System Overview

octoconv already has a proven, three-times-replicated "engine-class" pattern. Audio is not a new architecture — it is the pattern's 4th instantiation. Everything below shows the SAME shape with `audio` slotted in wherever `image`/`document`/`html` currently appear.

```
┌───────────────────────────────────────────────────────────────────────────┐
│  cmd/api  (single always-on process)                                      │
│  handleCreateJob: multipart → Sniff/SniffContainer/LooksLikeHTML/(NEW:    │
│  audio magic-bytes) → convert.Default.EngineFor(detected,target) →        │
│  S3 upload → jobs.Create (Postgres, status=queued) → route by `engine`    │
│  switch → EnqueueImageConvert|EnqueueDocumentConvert|EnqueueHTMLConvert|   │
│  (NEW) EnqueueAudioTranscribe                                             │
│  Also hosts: queue-depth Prometheus collector (ALL queues, incl. audio)   │
└───────────────────────────┬───────────────────────────────────────────────┘
                             │ asynq / Redis — 5 queues total after this milestone
        ┌───────────┬───────┼────────┬──────────┬──────────────┐
        ▼           ▼       ▼        ▼          ▼              ▼
   image queue  document  html    webhook   (NEW) audio    (existing)
                  queue    queue   queue        queue
        │           │       │        │          │
        ▼           ▼       ▼        ▼          ▼
  cmd/worker  cmd/document- cmd/    cmd/     (NEW)
  (libvips)   worker (LO)  chromium-webhook- cmd/audio-worker
                            worker  worker×2  (ffmpeg + whisper.cpp)
        │           │       │        │          │
        └───────────┴───────┴────────┴──────────┘
                             │ all write back through the SAME
                             ▼ jobs/storage/queue interfaces
                    Postgres (system of record) + S3/MinIO (uploads/results)
```

Cross-cutting singletons every engine class plugs into automatically once registered:
- `convert.Default` registry (`internal/convert/convert.go`) — format-pair → `Converter` lookup, engine-class grouping for `GET /v1/formats`.
- `internal/reconciler` — routes stale-job recovery by `jobs.engine` string; unrecognized engines are a fail-closed skip today (`EngineAudio` must be added as a new `case`, not fall through to `default`).
- `internal/metrics.NewQueueDepthCollector` — registered once in `cmd/api/main.go` with an explicit queue-name list; audio's queue name must be added to that call site.
- Helm chart: ConfigMap (`commonEnv`) + per-class Deployment + per-class ScaledObject, all keyed off `.Values.<class>.*` — audio needs its own block, not a reuse of an existing one.

### Component Responsibilities

| Component | Responsibility | New or Modified for Audio |
|-----------|----------------|----------------------------|
| `internal/convert/whisper.go` (NEW) | `AudioConverter` implementing `Converter`: `Pairs()` = cross-product of 4 source audio formats × 4 target transcript formats; `Convert()` shells out to `ffmpeg` then `whisper-cli`; `Engine()` returns `EngineAudio` | NEW |
| `internal/convert/convert.go` | Adds `EngineAudio = "audio"` const (single source of truth, per its own doc comment) | MODIFIED |
| `internal/convert/converters.go` | One-line `Default.Register(AudioConverter{})` in `init()` | MODIFIED |
| `internal/convert/audiosniff.go` (NEW, or extend `sniff.go`) | Magic-byte signatures for wav/ogg/m4a + the ID3/frame-sync dual-path mp3 detector | NEW |
| `internal/convert/sniff.go` | `MIMEType()` gains cases for mp3/wav/m4a/ogg (inputs) and txt/srt/vtt/json (outputs) | MODIFIED |
| `internal/convert/dimensions.go` | No change — `HasDimensionLimit` already scopes to the closed `dimensionParsers` map; audio formats are absent from it, so the pixel-dimension check is automatically bypassed (same free-ride as documents/html today) | UNCHANGED (verify only) |
| `internal/queue/queue.go` | `TypeAudioTranscribe`, `QueueAudio = convert.EngineAudio`, `NewAudioTranscribeTask`, `audioRetrySchedule`/`AudioRetryDelay`, `audioBackoffSum`/`AudioUniqueTTL`, `RetryDelayFunc` switch case | MODIFIED |
| `internal/queue/client.go` | `audioMaxRetry`/`audioUniqueTTL` fields, `AUDIO_MAX_RETRY`/`AUDIO_ENGINE_TIMEOUT` env reads in `NewClient`, `EnqueueAudioTranscribe` method | MODIFIED |
| `cmd/audio-worker/main.go` (NEW) | Mirrors `cmd/document-worker/main.go` structure exactly: connect Postgres/S3/Redis, build `worker.Handler` via `worker.NewHandler`, register `queue.TypeAudioTranscribe` on the mux, `AUDIO_WORKER_CONCURRENCY`, `ShutdownTimeout = AUDIO_ENGINE_TIMEOUT + margin`, own `/metrics` listener | NEW |
| `internal/worker/worker.go` | `HandleAudioTranscribe` (mirrors `HandleDocumentConvert`), `isAudioTerminal` (mirrors `isDocumentTerminal`/`isHTMLTerminal` — timeout-is-terminal), `terminalWhisperSignatures`/`terminalFFmpegSignatures` slices | MODIFIED |
| `internal/api/api.go` | `Enqueuer` interface gains `EnqueueAudioTranscribe` | MODIFIED |
| `internal/api/handlers.go` | `handleCreateJob`'s engine-routing `switch` gains an `EngineAudio` case; content-type-parity/dimension logic already generalizes (no format-specific branching needed beyond the switch) | MODIFIED |
| `internal/reconciler/reconciler.go` | `enqueuer` interface gains `EnqueueAudioTranscribe`; `sweep()`'s engine-routing `switch` gains an `EngineAudio` case (else it silently fails closed into `unroutable_engine`, which is safe but wrong) | MODIFIED |
| `cmd/api/main.go` | `queue.QueueAudio` added to the `NewQueueDepthCollector(...)` call's queue list | MODIFIED |
| `Dockerfile.audio-worker` (NEW) | Multi-stage: Go build stage (unchanged shape) + whisper.cpp build stage (clone+cmake, NOT an apt package) + baked-in pinned ggml model + `ffmpeg` via apt in the runtime stage + `USER nobody` | NEW |
| `docker-compose.yml` | New `audio-worker` service block (mirrors `document-worker`'s shape: env, resource limits); `api`/other services' `queue.NewClient()`-read env vars gain the audio pair per the existing "every process reads every class's vars" convention (DEBT-05 precedent) | MODIFIED |
| `.github/workflows/ci.yml` | `docker-build` and `e2e` jobs' bake `set:` lists gain `audio-worker.cache-to`/`cache-from` entries | MODIFIED |
| `deploy/chart/octoconv/values.yaml` | New `audioWorker:` block (image, replicas, terminationGracePeriodSeconds, resources) + new `keda.audio:` block (threshold, maxReplicaCount, pollingInterval, cooldownPeriod) | MODIFIED |
| `deploy/chart/octoconv/templates/configmap.yaml` | `AUDIO_ENGINE_TIMEOUT`, `AUDIO_MAX_RETRY`, `AUDIO_WORKER_CONCURRENCY` keys | MODIFIED |
| `deploy/chart/octoconv/templates/deployment-audio-worker.yaml` (NEW) | Mirrors `deployment-document-worker.yaml` (probes on `:9090/metrics`, `octoconv.io/tier: app` label, conditional `spec.replicas` per the WR-02-fixed pattern if that fix lands first) | NEW |
| `deploy/chart/octoconv/templates/scaledobject-audio.yaml` (NEW) | Mirrors `scaledobject-document.yaml`; PromQL `sum(octoconv_queue_depth{queue="audio", state=~"pending|active"})`; **must carry the WR-01 fix** (`ignoreNullValues: "false"`, or the alert-based compensating control) from day one — do not reintroduce the bug this milestone is also fixing | NEW |

## Recommended Project Structure

```
cmd/
├── audio-worker/                    # NEW — mirrors cmd/document-worker exactly
│   └── main.go
internal/
├── convert/
│   ├── convert.go                   # MODIFIED: + EngineAudio const
│   ├── converters.go                # MODIFIED: + Default.Register(AudioConverter{})
│   ├── whisper.go                   # NEW: AudioConverter (Pairs/Convert/Engine)
│   ├── audiosniff.go                # NEW: mp3/wav/m4a/ogg magic-byte signatures
│   ├── sniff.go                     # MODIFIED: + MIMEType cases (in+out formats)
│   └── exec.go                      # UNCHANGED: runCommand reused verbatim for
│                                     #   BOTH ffmpeg and whisper-cli invocations
├── queue/
│   ├── queue.go                     # MODIFIED: audio task/queue/retry/TTL, mirrors
│   │                                 #   Document*/HTML* blocks structurally
│   └── client.go                    # MODIFIED: audioMaxRetry/audioUniqueTTL, Enqueue*
├── worker/
│   └── worker.go                    # MODIFIED: HandleAudioTranscribe, isAudioTerminal,
│                                     #   terminalWhisperSignatures/terminalFFmpegSignatures
├── api/
│   ├── api.go                       # MODIFIED: Enqueuer + EnqueueAudioTranscribe
│   └── handlers.go                  # MODIFIED: engine-routing switch + EngineAudio
├── reconciler/
│   └── reconciler.go                # MODIFIED: enqueuer interface + sweep() switch
└── metrics/                         # UNCHANGED — queue_collector.go is already
                                      #   generic (takes ...queues); only the CALL
                                      #   SITE in cmd/api/main.go changes
Dockerfile.audio-worker              # NEW
docker-compose.yml                   # MODIFIED: + audio-worker service
.github/workflows/ci.yml             # MODIFIED: + audio-worker bake cache entries
deploy/chart/octoconv/
├── values.yaml                      # MODIFIED: + audioWorker, keda.audio blocks
└── templates/
    ├── configmap.yaml               # MODIFIED: + AUDIO_* keys
    ├── deployment-audio-worker.yaml # NEW
    └── scaledobject-audio.yaml      # NEW
```

### Structure Rationale

Every "NEW" file above has a direct, structurally-identical sibling already in the tree (`document-worker`/`chromium-worker` for the Dockerfile+cmd+chart pair, `libreoffice.go`/`chromium.go` for the Converter, `DocumentRetryDelay`/`HTMLRetryDelay` for the queue plumbing). This is deliberate: the codebase's own doc comments repeatedly say "mirrors X exactly" when a new engine class is added, and code review (26/27/28-REVIEW.md) rewards that consistency and flags drift. The audio class should be built by copy-adapt from `document-worker`, not from `image`/`worker` — document/html are the closer analogues because both (a) run an external binary that can legitimately hang on bad input (timeout-should-be-terminal), and (b) need a heavier, non-trivial-to-install runtime dependency baked into a dedicated Dockerfile (LibreOffice / chromium-headless-shell / whisper.cpp+ffmpeg), unlike the `worker`/libvips class which is a single lightweight CLI already in Debian's apt repo.

## Architectural Patterns

### Pattern 1: Engine-class routing (the load-bearing pattern — apply verbatim)

**What:** One `Converter` implementation per engine, registered into a single process-wide `Registry`; one asynq queue + one dedicated worker binary + one Dockerfile + one compose service + one chart Deployment + one chart ScaledObject per engine class. `convert.EngineImage`/`EngineDocument`/`EngineHTML` are the single source of truth for the engine-class string literal, referenced by name in `internal/api`, `internal/reconciler`, and `internal/queue`.
**When to use:** Every new file-conversion capability. This is not optional or up for reinterpretation — three prior milestones (v1.0, v1.2, v1.3) converged on it, and the codebase's own comments call out any place that must stay in lock-step (`internal/convert/convert.go:11-17`).
**Trade-offs:** Verbose (touches ~15 files per new class) but mechanically safe — each touch point is small, typed, and covered by an existing compile-time or test-time check (`go vet`, the `internal/api`/`internal/queue` test suites, `helm template` render checks). The alternative (a generic `Handler`/`Capability` contract) was explicitly rejected in Key Decisions for v1.2 and never revisited.

**Example — the exact shape `AudioConverter` must follow (`internal/convert/libreoffice.go` structure, adapted):**
```go
const EngineAudio = "audio" // add to internal/convert/convert.go's const block

type AudioConverter struct{}

var audioSourceFormats = []string{"mp3", "wav", "m4a", "ogg"}
var audioTargetFormats = []string{"txt", "srt", "vtt", "json"}

func (AudioConverter) Pairs() []Pair {
	pairs := make([]Pair, 0, len(audioSourceFormats)*len(audioTargetFormats))
	for _, from := range audioSourceFormats {
		for _, to := range audioTargetFormats {
			pairs = append(pairs, Pair{From: from, To: to}) // full cross-product,
			// unlike LibvipsConverter's from!=to filter -- source and target
			// sets are disjoint here, so no self-pair exists to exclude
		}
	}
	return pairs
}

func (AudioConverter) Engine() string { return EngineAudio }
```

### Pattern 2: Timeout-is-terminal for engines with deterministic hang behavior (DOC-08's precedent, not image's)

**What:** `isTerminal` (the shared classifier) deliberately treats a `context.DeadlineExceeded` as TRANSIENT so the image path keeps retrying real S3/network blips. `isDocumentTerminal`/`isHTMLTerminal` deliberately DIVERGE from that and treat a timeout as TERMINAL, via the shared `timeoutIsTerminal` helper, because LibreOffice/chromium hangs on bad input are deterministic — retrying burns the whole `*_MAX_RETRY` budget on a render that will time out identically every time.
**When to use for audio:** whisper.cpp transcription time is a near-deterministic function of (audio duration × model size × CPU). An `AUDIO_ENGINE_TIMEOUT` expiry is therefore far more likely to mean "this file is pathologically long/silent/corrupt for this model" than "transient contention" — the SAME reasoning DOC-08 already documents. **Recommendation: `isAudioTerminal` should be `timeoutIsTerminal(err)` (mirroring `isDocumentTerminal`/`isHTMLTerminal` verbatim), not a bespoke transient-timeout classifier.** This is a real design decision, not a formality — get it wrong and a single pathological audio file burns `AUDIO_MAX_RETRY × AUDIO_ENGINE_TIMEOUT` of worker time before finally failing.
**Trade-offs:** A file that times out for a genuinely transient reason (e.g., the audio-worker pod was CPU-starved by a co-scheduled load spike) will fail one attempt sooner than it would under image's transient-timeout policy. This is the same trade-off document/html already accepted; the reconciler's stale-job sweep is the backstop for genuinely-stuck-not-yet-timed-out jobs either way.

### Pattern 3: Two-stage external-process pipeline inside a single `Convert()` call

**What:** Every existing `Converter.Convert()` shells out to exactly ONE external binary via `runCommand` (vips / soffice / chromium-headless-shell). Audio is the first engine class that legitimately needs TWO sequential external processes: `ffmpeg` (resample arbitrary input to 16kHz mono 16-bit PCM WAV — whisper.cpp's hard input requirement, MEDIUM confidence, WebSearch-verified) then `whisper-cli` (the actual transcription).
**When to use:** Keep both invocations INSIDE `AudioConverter.Convert()`, calling `runCommand` twice sequentially, sharing the same `ctx` (and therefore the same `AUDIO_ENGINE_TIMEOUT` budget) for both. Do NOT split ffmpeg preprocessing into a separate queue/task stage — that would break the "one task = one attempt = one Postgres transition" invariant every other engine class relies on (`process()` in `internal/worker/worker.go` wraps the ENTIRE attempt, not just `conv.Convert()`, in one `context.WithTimeout`; a second queue hop would need its own asynq.Unique lock derivation, duplicating `ImageUniqueTTL`'s whole worst-case-lifetime reasoning for no benefit).
**Trade-offs:** `AUDIO_ENGINE_TIMEOUT` must budget for BOTH steps (ffmpeg resampling is fast/near-linear in file size; whisper-cli is the dominant cost). A single combined budget is simpler to reason about than two separate timeouts and matches every existing engine's "one timeout covers the whole attempt" shape.
**Example (Convert() sketch, mirrors chromium.go's direct-argv-write, no-shell-involved discipline):**
```go
func (AudioConverter) Convert(ctx context.Context, inPath, outPath string, opts map[string]any) error {
	workDir := filepath.Dir(outPath)
	wavPath := filepath.Join(workDir, "pcm16.wav")
	if _, err := runCommand(ctx, "ffmpeg", "-y", "-i", inPath, "-ar", "16000", "-ac", "1",
		"-c:a", "pcm_s16le", wavPath); err != nil {
		return fmt.Errorf("audio: ffmpeg: %w", err)
	}
	target := NormalizeFormat(filepath.Ext(outPath)) // txt|srt|vtt|json
	outBase := strings.TrimSuffix(outPath, filepath.Ext(outPath)) // whisper-cli appends its own ext
	if _, err := runCommand(ctx, "whisper-cli", "-m", whisperModelPath,
		"-f", wavPath, "-of", outBase, "--output-"+target, "-nt" /* no timestamps in txt */); err != nil {
		return fmt.Errorf("audio: whisper-cli: %w", err)
	}
	// whisper-cli writes outBase+"."+target deterministically (--output-file
	// pins the basename) -- verify/os.Stat exactly like validatePDF does,
	// no blind "assume success on exit 0" (every existing engine has been
	// bitten by an engine that exits 0 with no/empty output; whisper.cpp
	// should get the same non-trusting treatment).
	return validateAudioOutput(outPath, target)
}
```

## Data Flow

### Request Flow (job creation → transcript delivered)

```
Client POST /v1/jobs (file=recording.mp3, target=srt)
    │
    ▼
handleCreateJob (internal/api/handlers.go)
    │  1. multipart parse, size cap (unchanged)
    │  2. convert.Sniff(file) -- NEW: mp3/wav/ogg/m4a signatures checked here
    │     (ID3-tagged mp3 needs a second, ID3-aware branch -- see Anti-Pattern 3)
    │  3. HasDimensionLimit("mp3") == false -> pixel-dimension check SKIPPED
    │     automatically (dimensionParsers map has no audio entries) -- same
    │     free bypass documents/html already get, VERIFY not IMPLEMENT
    │  4. convert.Default.EngineFor("mp3","srt") -> ("audio", true)
    │  5. S3 upload (uploads/{job_id}/0-recording.mp3)
    │  6. jobs.Create (Postgres, status=queued, engine="audio")
    │  7. switch engine { case EngineAudio: s.queue.EnqueueAudioTranscribe(...) }
    ▼
audio asynq queue (Redis) -- asynq.Unique-locked, AudioUniqueTTL-derived
    ▼
cmd/audio-worker (asynq.ServeMux -> HandleAudioTranscribe)
    │  1. MarkActive (guarded transition, same as every class)
    │  2. process(): download input, registry.Lookup("mp3","srt") -> AudioConverter
    │  3. AudioConverter.Convert(): ffmpeg resample -> whisper-cli transcribe
    │     -- both bounded by the SAME attemptCtx (AUDIO_ENGINE_TIMEOUT)
    │  4. upload output (results/{job_id}/0-out.srt, Content-Type per
    │     MIMEType("srt"))
    │  5. AddOutput + MarkDone (Postgres)
    │  6. EnqueueWebhookDeliver if callback_url set (unchanged, engine-agnostic)
    ▼
Client GET /v1/jobs/{id} -> download_url (presigned) OR webhook delivered
```

### Failure/Retry Flow

```
whisper-cli/ffmpeg failure or AUDIO_ENGINE_TIMEOUT expiry
    │
    ▼
isAudioTerminal(err)  -- timeoutIsTerminal wrapper (Pattern 2 above)
    │
    ├─ terminal (bad input signature match, OR timeout) ──► MarkFailed + SkipRetry
    │                                                        + webhook (if set)
    │                                                        + metrics.RecordJobOutcome
    │
    └─ transient (S3/Postgres blip, non-timeout) ──────────► return unwrapped err,
                                                               job stays "active",
                                                               asynq retries per
                                                               AudioRetryDelay
                                                               (bounded AUDIO_MAX_RETRY)
```

If a task is dropped without a status transition (pod killed mid-attempt), the reconciler's `sweep()` picks it up on the next tick via `FindStale` + the `jobs.engine` routing switch — this is why that switch MUST gain an `EngineAudio` case; without it, a stranded audio job silently degrades into `unroutable_engine` (a safe-but-wrong metric-visible no-op) forever.

## Scaling Considerations

| Concern | Image/Document/HTML (existing) | Audio (new) |
|---------|-------------------------------|--------------|
| CPU cost shape | Bounded, sub-second to low-tens-of-seconds per job | Proportional to audio DURATION × model size; can be the most CPU-hungry class per-job in the whole system (whisper.cpp on CPU is commonly single-digit-multiples of real-time for base/small models — MEDIUM confidence) |
| `AUDIO_ENGINE_TIMEOUT` sizing | N/A | Must be sized generously against the LONGEST audio file the service is expected to accept, not against a "typical" file — unlike document (worst case is a complex-but-bounded office file) audio's worst case scales with a client-controlled duration. Consider whether an upload-time duration/size ceiling belongs alongside `MAX_UPLOAD_BYTES` (the existing size cap already bounds duration indirectly for a given bitrate, but is not an explicit duration guard the way `MAX_IMAGE_PIXELS`/`MAX_DOCUMENT_UNCOMPRESSED_BYTES` are explicit resource-exhaustion guards for their classes) |
| KEDA scaling | `threshold`/`maxReplicaCount` tuned per class already (image 5/4, document 1/2, html 2/2) | Audio's per-job cost is higher and more variable than any existing class — start `keda.audio.threshold` LOW (e.g. `1`, mirroring document's conservative "1 pending job = scale up" posture, not image's "5") given each job can legitimately occupy a worker for minutes |
| Retry-state stranding (27-REVIEW WR-06) | Documented invariant: `cooldownPeriod` must exceed the class's max retry backoff or scale-to-zero can strand a retry-state task | Applies identically to audio — whatever `audioRetrySchedule` is chosen (recommend mirroring `documentRetrySchedule`'s shape: short, no-jitter, e.g. 5s/15s/30s — the timeout-is-terminal policy means retries only ever fire for genuinely transient blips, not long-running work) must stay below `keda.audio.cooldownPeriod` |
| Model/image size | N/A | Baking a whisper.cpp ggml model into `Dockerfile.audio-worker` adds real, fixed image weight (tiny≈75MB, base/small are the realistic production choices, large-v3≈3.1GB — MEDIUM confidence, WebSearch-verified) — pin ONE model explicitly (mirrors the project's existing "never `:latest`, pin everything" discipline: MinIO release tags, veraPDF `v1.30.2`, asynqmon `0.7.2`) rather than downloading at container startup, which would violate the "workers remain offline" constraint restated in PROJECT.md's v1.7 Key context |

## Anti-Patterns

### Anti-Pattern 1: Reusing an existing engine's timeout-classification policy without re-deriving it

**What people might do:** Copy `isTerminal`'s image-path "timeout is always transient, keep retrying" behavior for audio because it's the "default"/first-written classifier in the file.
**Why it's wrong:** whisper.cpp's cost is a deterministic function of input duration/model — a timing-out job will time out again on retry. Retrying it anyway burns `AUDIO_MAX_RETRY × AUDIO_ENGINE_TIMEOUT` of worker capacity (potentially the most expensive class in the system) on guaranteed-repeat failures, exactly the DOC-08 defect class that document/html were deliberately written to avoid.
**Do this instead:** `isAudioTerminal(err) = timeoutIsTerminal(err)`, mirroring `isDocumentTerminal`/`isHTMLTerminal` (see Pattern 2).

### Anti-Pattern 2: Trusting whisper-cli/ffmpeg exit code 0 as proof of a valid output file

**What people might do:** Skip output validation because "the process exited 0."
**Why it's wrong:** Every existing engine in this codebase has been live-tested to exit 0 while producing empty/no/wrong output under some failure mode (LibreOffice's documented "exit 0, no output file"; chromium-headless-shell's one-shot handler silently producing nothing under specific flag combinations). There is no reason to assume whisper.cpp/ffmpeg are exempt, and the codebase's own `terminalLibreOfficeSignatures`/`terminalChromiumSignatures` comments explicitly warn against this assumption.
**Do this instead:** `validateAudioOutput` should `os.Stat` the produced file, reject zero-size, and — for `json`/`srt`/`vtt` — do a cheap structural sanity check (e.g. `srt`/`vtt` start with an expected header/cue pattern, `json` is valid JSON) mirroring `validatePDF`'s magic-byte discipline. Couple any new terminal-signature substrings into `terminalWhisperSignatures`/`terminalFFmpegSignatures` in the SAME commit that introduces the validator, exactly as `libreoffice.go`'s validator and `worker.go`'s signature slice are documented to ship atomically (see the D-04/T-13-02 comment pattern already in the codebase).

### Anti-Pattern 3: Shallow mp3 magic-byte matching that ignores the ID3v2 prefix problem

**What people might do:** Add mp3 to `sniff.go`'s `signatures` table with a single naive check like the existing `matchJPEG`/`matchWebP` shallow-prefix pattern (e.g. only checking for a raw `0xFF 0xE0`-masked frame-sync at byte 0).
**Why it's wrong:** A large fraction of real-world mp3 files begin with an `"ID3"` (0x49 0x44 0x33) tag of ARBITRARY, self-declared length (a syncsafe-encoded size field) BEFORE the actual MPEG frame sync bytes — this is exactly the class of "prefix lies about structure" problem `docsniff.go`/`olecfb.go`/`dimensions.go` already solve carefully for other formats. A frame-sync-only check silently rejects every ID3-tagged mp3 as unrecognized content (422 for a huge share of real client uploads), while a naive `"ID3"`-prefix-only check risks being too loose (though in practice `"ID3"` as a 3-byte literal is a low-collision signature, unlike a single-byte or 2-byte magic).
**Do this instead:** Match EITHER (a) raw frame sync `b[0]==0xFF && (b[1]&0xE0)==0xE0` at offset 0 (untagged mp3) OR (b) the 3-byte `"ID3"` literal at offset 0 (tagged mp3, accepted at the shallow-prefix level — consistent with the codebase's existing shallow-signature precedent for webp/tiff/jpeg, which also don't fully validate structure past the magic). `sniffLen=12` already covers both cases; no buffer-size change is needed for the signature check itself (unlike the separate audio-duration-bomb question under Scaling Considerations, which is a distinct, larger-buffer concern if pursued).

### Anti-Pattern 4: Reintroducing the WR-01 empty-PromQL bug in the new `scaledobject-audio.yaml`

**What people might do:** Copy `scaledobject-document.yaml` verbatim (including its as-shipped `ignoreNullValues: "true"`) before the WR-01 hardening-tail fix lands, then have to patch a 4th file when WR-01 is fixed.
**Why it's wrong:** Doing the audio ScaledObject work before the hardening-tail fix means writing the known-bad pattern once more, guaranteed to need a follow-up edit.
**Do this instead:** Sequence WR-01's chart fix BEFORE `scaledobject-audio.yaml` is authored (see Suggested Build Order below), so audio's ScaledObject is written correctly the first time and the other three are fixed in the same commit/phase.

## Integration Points

### External Tools (new)

| Tool | Integration Pattern | Notes |
|------|---------------------|-------|
| `whisper.cpp` (`whisper-cli` binary) | Built from source in the Dockerfile's builder stage (no Debian apt package exists — confirmed via search; official images build from source too) via `git clone` pinned to a tag/commit + `cmake`/`make`, ggml model downloaded and BAKED into the image at build time (never at container runtime — matches the "offline worker" constraint), invoked via the existing hardened `runCommand` (`internal/convert/exec.go`) — no new exec-wrapper mechanism needed | Pin the model explicitly (recommend `base` or `small`, English or multilingual per requirements) mirroring the project's "never `:latest`" discipline (veraPDF `v1.30.2`, MinIO release tags, asynqmon `0.7.2`). MEDIUM confidence — WebSearch-verified, no in-repo precedent. |
| `ffmpeg` | Installed via `apt-get install ffmpeg` in the runtime stage (available in `debian:bookworm-slim`'s repos, unlike whisper.cpp) — same `runCommand` invocation mechanism, called as the first of two sequential steps inside `AudioConverter.Convert()` | Confirms the project's existing "one apt-installed CLI tool per engine class" pattern (libvips-tools / libreoffice-*-nogui / chromium-headless-shell) extends cleanly to "two CLI tools, one apt + one built-from-source" |

### Internal Boundaries (all pre-existing interfaces — audio is a pure addition of new implementations/cases, no interface redesign)

| Boundary | Communication | Notes |
|----------|---------------|-------|
| `internal/api` ↔ `internal/queue` | `Enqueuer` interface (`internal/api/api.go`) | Add `EnqueueAudioTranscribe(ctx, jobID) error` to the interface AND `handleCreateJob`'s routing switch — both must change together or the switch's `default:` fail-closed branch silently rejects every audio job with a 500 |
| `internal/reconciler` ↔ `internal/queue` | `enqueuer` interface (`internal/reconciler/reconciler.go`) | Same shape, independent interface (interface segregation is deliberate here — do not merge with `api.Enqueuer`) |
| `internal/worker` ↔ `internal/convert` | `registry.Lookup(source, target)` — engine-agnostic, ALREADY handles any registered `Converter` | No change needed — `process()` in `worker.go` is already engine-agnostic; only the per-engine `Handle*` wrapper and terminal-classifier are new |
| `cmd/api` ↔ `internal/metrics` | `NewQueueDepthCollector(inspector, queues...)` variadic call site | Purely additive — add `queue.QueueAudio` to the existing call in `cmd/api/main.go`; the collector itself needs zero changes (already generic) |
| Helm chart ↔ KEDA | `scaledobject-audio.yaml` → in-chart Prometheus → `octoconv_queue_depth{queue="audio",...}` | Gate on `and .Values.keda.enabled .Values.prometheus.enabled` exactly like the other three (co-dependency guard documented in `scaledobject-image.yaml`'s header comment) |

## Hardening-Tail Mapping (v1.6 residual items → concrete files)

The milestone bundles four v1.6 hardening-tail items with the new audio class. Mapped to files:

| Item | File(s) | What changes | Independent of audio work? |
|------|---------|---------------|------------------------------|
| **WR-01** (empty-PromQL semantics) | `deploy/chart/octoconv/templates/scaledobject-image.yaml`, `scaledobject-document.yaml`, `scaledobject-html.yaml` (all three, `ignoreNullValues` line) | Flip `ignoreNullValues: "true"` → `"false"` (sustained api-outage now correctly trips KEDA's `fallback`), or add an `absent(octoconv_queue_depth)` alert as a documented compensating control instead. 27-REVIEW.md gives both options explicitly. | YES, fully independent — pure chart-template edit, zero Go code, zero audio dependency. **Should land BEFORE `scaledobject-audio.yaml` is authored** (Anti-Pattern 4) so the new file is correct from its first commit. |
| **OPER-01** (compose `OPERATOR_CLIENT_IDS` passthrough + live gate) | `docker-compose.yml` (`api.environment` block, currently missing the key entirely — confirmed by direct read) | Add `OPERATOR_CLIENT_IDS: ${OPERATOR_CLIENT_IDS:-}` to the `api` service's `environment:` block (the exact fix 26-REVIEW.md's WR-03 specifies), then run the deferred live gate (set an operator UUID via compose env override, hit `/v1/system/presets`, confirm 200 for the operator / 404 for everyone else) | YES, fully independent — one-line compose edit + a live verification pass, zero audio dependency. Cheap, should land early (low risk, unblocks closing out v1.6's own audit). |
| **Gate-tooling warnings** (28-REVIEW WR-01..WR-06) | `deploy/chart/octoconv/templates/scaledobject-document.yaml:38` (Go-template `0`-is-falsy truthiness bug on `scaleDownStabilizationSeconds`), `scripts/keda-load-proof.sh` (stale-Terminating-pod selection, S3-error-body-as-success download check, orphaned `kubectl -w` watcher), `scripts/fixtures/render_evidence.py` (unpinned Python≥3.11 requirement), `scripts/fixtures/gen_heavy_docx.py` (CWD-relative sample-image path) | Six independent, mechanical fixes — none touch `internal/` Go packages; all are chart-template or shell/Python tooling robustness fixes surfaced by the Phase 28 code review | YES, fully independent — these are load-proof GATE SCRIPT correctness fixes, not product code. No audio dependency. Lowest priority/risk of the four hardening items (tooling only, not shipped product behavior) but cheap to batch together since they're all in `28-REVIEW.md`'s Warnings section already. |
| **K8S-02** (direct-dial recheck) | No file change — this is a LIVE VERIFICATION task against a running OrbStack cluster (confirm a presigned MinIO URL resolves via a direct host→cluster FQDN dial, without the `kubectl port-forward` workaround Phase 24's live gate had to use because of a wedged OrbStack proxy layer) | Re-run the specific `curl` against `minio.octoconv.svc.cluster.local:9000` from the bare host once OrbStack's proxy is healthy; update `24-VERIFICATION.md`'s human-verification item #2 from open to resolved | YES, fully independent — pure operational re-check, zero code change of any kind. Can be done any time the OrbStack daemon is healthy; not gated on any other hardening item. |

**Can these four form one coherent phase?** Yes, with a caveat: they are independent of EACH OTHER and independent of the audio work, but they are NOT independent of the ORDER within themselves relative to audio's chart additions (WR-01 must precede `scaledobject-audio.yaml`, per Anti-Pattern 4). They share no code paths, so batching them into a single "hardening tail" phase carries no coupling risk — but there's little value in NOT batching them either, since they're all small, low-risk, single-file-or-script edits already fully specified by existing review documents (no new research needed — this is closing out already-diagnosed findings, not discovering new ones). Recommend one phase, ordered internally as: WR-01 chart fix → OPER-01 compose fix + live gate → gate-tooling script fixes (batch) → K8S-02 live recheck (can run any time, including in parallel/opportunistically).

## Suggested Build Order

1. **Hardening tail (WR-01, OPER-01, gate-tooling, K8S-02)** — zero dependency on audio, all pre-diagnosed by existing review docs, cheap. Landing this FIRST means `scaledobject-audio.yaml` (step 9 below) is written correctly from its first commit instead of needing a follow-up patch (Anti-Pattern 4). K8S-02's live recheck can float independently/opportunistically since it blocks nothing.
2. **`internal/convert` foundation** — `EngineAudio` const, `audiosniff.go` (mp3/wav/m4a/ogg signatures, including the mp3 ID3-vs-frame-sync dual path), `MIMEType` additions, `AudioConverter` skeleton (`Pairs()`/`Engine()` only, `Convert()` can initially be a thin wrapper while the exec pipeline is built next) — this is the layer every other touch point depends on (`EngineFor` lookups, `Registry.Classes()` for `/v1/formats`).
3. **`AudioConverter.Convert()` — the ffmpeg+whisper-cli pipeline** — build and validate against a local whisper.cpp install BEFORE touching the queue/worker plumbing, since this is the one genuinely new piece of external-process integration (two sequential `runCommand` calls, a real model file, real output-format validation). Get this working as a standalone, testable unit first.
4. **`internal/queue` — task/queue/retry/TTL** — `TypeAudioTranscribe`, `QueueAudio`, `NewAudioTranscribeTask`, `AudioRetryDelay`/`audioBackoffSum`/`AudioUniqueTTL`, `RetryDelayFunc` switch case, `Client.EnqueueAudioTranscribe`. Mechanical, mirrors `Document*`/`HTML*` blocks exactly — low risk once the pattern is followed.
5. **`internal/worker` + `cmd/audio-worker`** — `HandleAudioTranscribe`, `isAudioTerminal` (Pattern 2 — get the timeout-is-terminal decision right here), terminal-signature slices, then the `cmd/audio-worker/main.go` binary itself (copy-adapt `cmd/document-worker/main.go`). This is where the pipeline built in step 3 gets wired into the real async job lifecycle.
6. **`internal/api` + `internal/reconciler` routing** — add `EngineAudio` to both engine-routing switches (`handleCreateJob`, `reconciler.sweep()`) and both `Enqueuer`/`enqueuer` interfaces. Small, mechanical, but easy to forget one of the two switches (reconciler's fail-closed `default:` means a missed case degrades gracefully rather than crashing — but it IS a real gap until added).
7. **`cmd/api/main.go` metrics registration** — add `queue.QueueAudio` to the `NewQueueDepthCollector` call. One line; do this alongside step 6 since both are "make the new engine class visible to the rest of the system" work.
8. **`Dockerfile.audio-worker` + `docker-compose.yml` + CI bake matrix** — get the container building and runnable in compose before touching Kubernetes; this is also where the "bake the ggml model in at build time" decision gets made concrete and where local end-to-end testing becomes possible for the first time.
9. **Helm chart (`values.yaml`, `configmap.yaml`, `deployment-audio-worker.yaml`, `scaledobject-audio.yaml`)** — last, because it depends on the compose service/env contract from step 8 being stable, and because `scaledobject-audio.yaml` should be written with the already-landed WR-01 fix in hand (step 1).
10. **Live E2E verification** — a full audio job (upload mp3 → transcript in each of txt/srt/vtt/json → webhook/download) against the real compose stack, then (if in scope this milestone) against the Helm/KEDA chart mirroring Phase 27/28's live-gate discipline.

This order front-loads the one genuinely novel piece of engineering (the ffmpeg+whisper.cpp pipeline, step 3) before investing in the surrounding plumbing, and defers all Kubernetes/KEDA work (step 9-10's chart half) until the simpler compose path has already proven the pipeline works — matching how document (Phase 10-11) and html (Phase 15) were built before their K8s/KEDA integration arrived three phases later (v1.6), not simultaneously with the engine class itself.

## Sources

- Direct reads of `internal/convert/{convert,converters,libvips,libreoffice,chromium,exec,sniff,docsniff,htmlsniff,dimensions}.go`, `internal/queue/{queue,client}.go`, `internal/worker/worker.go`, `internal/api/{api,handlers}.go`, `internal/reconciler/reconciler.go`, `internal/metrics/queue_collector.go`, `internal/storage/keys.go`, `internal/jobs/jobs.go`, `cmd/{api,worker,document-worker}/main.go` — HIGH confidence, ground truth.
- `docker-compose.yml`, `.github/workflows/ci.yml`, `Dockerfile.{worker,document-worker}`, `deploy/chart/octoconv/{values.yaml,templates/*}` — HIGH confidence, ground truth.
- `.planning/milestones/v1.6-phases/{26-operator-presets-rest/26-REVIEW.md, 27-keda-autoscaling/27-REVIEW.md, 28-autoscale-load-proof/28-REVIEW.md, 24-helm-chart-core/24-VERIFICATION.md}` — HIGH confidence, ground truth for the hardening-tail mapping (WR-01, OPER-01/WR-03, gate-tooling WR-01..06, K8S-02's SC3 direct-dial item).
- whisper.cpp CLI output-format flags (`--output-txt/srt/vtt/json`), input requirement (16kHz mono 16-bit PCM WAV), and lack of an apt package (build-from-source) — MEDIUM confidence, WebSearch-verified against `ggml-org/whisper.cpp` GitHub docs and multiple independent how-to sources (til.simonwillison.net, DeepWiki CLI reference); no in-repo precedent exists to cross-check against, unlike every other claim in this document.
- ggml model size figures (tiny≈75MB, large-v3≈3.1GB) — MEDIUM confidence, WebSearch-verified, used only to inform the "pin one model, bake it in" recommendation, not as a hard requirement.

---
*Architecture research for: octoconv v1.7 (audio engine class + v1.6 hardening tail)*
*Researched: 2026-07-17*
