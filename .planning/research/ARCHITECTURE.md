# Architecture Research

**Domain:** Async file-conversion service — fifth engine class (av/video via ffmpeg)
**Researched:** 2026-07-19
**Confidence:** HIGH (codebase-derived; container-format magic-byte facts verified against current specs)

## Standard Architecture

### System Overview (unchanged shape, av is a fifth vertical slice)

```
┌───────────────────────────────────────────────────────────────────────────┐
│  cmd/api  (always-on)                                                      │
│  Sniff/SniffAudio/SniffVideo(new) → EngineFor(detected,target) → route     │
├───────────────────────────────────────────────────────────────────────────┤
│  asynq / Redis  — one queue per engine class                               │
│  image | document | html | audio | av(NEW) | webhook                      │
├──────────┬──────────┬──────────┬──────────┬──────────┬────────────────────┤
│  worker  │ document │chromium  │ audio    │ av(NEW)  │ webhook-worker×2    │
│ (libvips)│ -worker  │ -worker  │ -worker  │ -worker  │ (+ reconciler       │
│          │(LibreOff)│(chromium)│(ffmpeg+  │ (ffmpeg  │  sweeper, advisory  │
│          │          │          │ whisper) │  ONLY)   │  lock elected)      │
├──────────┴──────────┴──────────┴──────────┴──────────┴────────────────────┤
│  Postgres (system of record: jobs, job_inputs/outputs, job_events)         │
│  S3/MinIO (uploads/, results/)                                             │
└───────────────────────────────────────────────────────────────────────────┘
```

The av class does **not** get a new architectural layer — it slots into the
existing `Converter`/`Registry`/`EngineFor` abstraction exactly like
document/html/audio did (v1.2/v1.3/v1.7). The one structural wrinkle worth
calling out up front: **not every "video" pair routes to the new av queue.**
Video→transcript pairs are deliberately registered under `EngineAudio` and
processed by the *existing* audio-worker (see Key Decision A below) — the av
queue only carries transcode/audio-extract/thumbnail.

### Component Responsibilities

| Component | Responsibility | New or modified |
|-----------|----------------|------------------|
| `internal/convert/avsniff.go` (new) | Magic-byte detection for mp4/mov/avi (fixed 12-byte window, extends `sniff.go`'s table) + a separate bounded-peek EBML parser for mkv/webm DocType disambiguation | **New** |
| `internal/convert/av.go` (new) | `AVConverter` — transcode, audio-extract, thumbnail. `Engine() == EngineAV`. Wraps ffmpeg only (no whisper) | **New** |
| `internal/convert/avopts.go` (new) | `AVOpts` — closed allowlist (e.g. `timecode` for thumbnail, `video_bitrate`/`resolution_preset` for transcode), mirrors `AudioOpts`/`HTMLOpts` pattern | **New** |
| `internal/convert/whisper.go` (existing) | `AudioConverter.Pairs()` extended with video-container sources × transcript targets; `Convert()` body **unchanged** (ffmpeg already extracts+normalizes audio from any container it can demux) | **Modified** (Pairs only) |
| `internal/convert/convert.go` | `EngineAV = "av"` constant added to the single source-of-truth block | **Modified** (1 line) |
| `internal/convert/converters.go` | `Default.Register(AVConverter{})` added | **Modified** (1 line) |
| `internal/queue/queue.go` | `TypeAVConvert`/`QueueAV`, `NewAVConvertTask`, `avRetrySchedule`/`AVRetryDelay`, `AVUniqueTTL` — mirrors `Audio*` symbols exactly | **New** (mirrors existing) |
| `internal/worker/worker.go` | `isAVTerminal` (stage-aware classifier, mirrors `isAudioTerminal`), `HandleAVConvert`, `enforceAVGuardBeforeConvert` (duration guard, reuses `convert.EnforceMaxDuration`) | **New** (mirrors existing) |
| `internal/api/handlers.go` | `engine` switch (both the enqueue switch and the opts-dispatch switch) gets an `EngineAV` case | **Modified** (2 switch arms) |
| `internal/reconciler/reconciler.go` | `enqueuer` interface + engine switch gets `EnqueueAVConvert`/`case convert.EngineAV` | **Modified** (2 additions) |
| `cmd/av-worker/main.go` (new) | Entry point — mirrors `cmd/audio-worker/main.go` minus whisper-specific env (no `AV_MODEL_PATH`, no thread-count resolution) | **New** |
| `Dockerfile.av-worker` (new) | `debian:bookworm-slim` + `ffmpeg` only — **no whisper.cpp build stage, no baked model** | **New** (much lighter than `Dockerfile.audio-worker`'s 682MB) |
| `internal/db/migrations/*` | **None needed** — `jobs_engine_check` already allow-lists `'av'` (added speculatively alongside `'cad'`/`'archive'`/`'probe'` in the same migration family as `'audio'`, confirmed live in `0006_audio_engine.sql`) | **No change** |

## Key Design Decisions

### Decision A — video→transcript routes to the EXISTING audio queue, not a new whisper-in-av-container or cross-queue chain

**The question posed three options: whisper baked into av-worker (heavy image), cross-queue job chaining, or routing video targets to the audio queue. Recommendation: the third, with a specific mechanism.**

`AudioConverter.Convert` (`internal/convert/whisper.go`) already runs a
two-stage `ffmpeg` (normalize) → `whisper-cli` (transcribe) pipeline. Stage 1
(`ffmpegNormalizeArgs`) is `ffmpeg -y -i file:<in> -ar 16000 -ac 1 -c:a
pcm_s16le <norm.wav>` — **ffmpeg already demuxes the audio track out of any
container it can decode, video or not.** Nothing in `Convert()` assumes the
input is audio-only. This means video→transcript is not a new capability
that needs a new pipeline — it is the *same* pipeline with a wider `Pairs()`
set.

**Recommended change:** extend `AudioConverter.Pairs()` with a
`videoContainerFormats` × `audioTargetFormats` cross product (mp4, mov, mkv,
webm, avi → txt/srt/vtt/json), keeping `Engine() == EngineAudio` unchanged.
`Registry.Register` iterates `c.Pairs()` and indexes each `(From,To)` pair
independently — nothing about the registry, `EngineFor`, or the audio
queue/worker needs to know these new pairs exist beyond that one `Pairs()`
extension. `EngineFor("mp4", "txt")` then returns `"audio"`, so the job is
enqueued onto the existing `QueueAudio`, processed by the existing
`audio-worker` container, bounded by the existing (RTF-measured)
`AUDIO_ENGINE_TIMEOUT=742s` and `AUDIO_MAX_DURATION_SECONDS=1800s` guard
(`enforceAudioGuardBeforeConvert` is already gated on `job.Engine ==
convert.EngineAudio`, not on source format — it fires correctly for these
jobs with zero code change to the guard itself).

**Why not the other two options:**
- **Whisper baked into av-worker** duplicates the ~682MB whisper.cpp+model
  image layer into a second container, doubles the RTF-measurement/GO-NO-GO
  burden (Phase 32's proof would need to be re-run for a second image), and
  gives the av-worker two completely different resource profiles (fast
  ffmpeg ops vs. whisper's CPU-bound transcription) to size `AV_WORKER_CONCURRENCY`/
  resource limits against — the same "heterogeneous cost in one queue"
  problem Decision C addresses, but worse because it also duplicates the
  binary/model.
- **Cross-queue job chaining** (av-worker extracts audio → enqueues a
  second task on the audio queue → some mechanism marks the *original* job
  done once the second task completes) requires a genuinely new
  orchestration primitive this codebase has never needed: today one
  `jobs` row maps to exactly one queue task for its entire lifecycle
  (`Postgres-first double write`, `guarded status transitions`,
  `asynq.Unique` keyed by `job_id` — all built around single-task-per-job).
  Chaining would need either a sub-job/parent-job schema addition or a
  saga-style intermediate state, new reconciler-routing logic (which queue
  is "the" job currently on?), and a new unique-lock TTL derivation
  spanning two engine timeouts. This is a multi-phase schema change for a
  feature the reused-pipeline approach gets for free.

**Residual work this decision still requires** (tracked as build-order
items below, not free): (1) `ffmpegNormalizeArgs` should gain an explicit
`-vn` flag when the source is a video container (defense-in-depth — without
it ffmpeg still only *writes* an audio-only WAV, since the output container
has no video stream, but explicitly declaring "no video output" makes the
intent self-documenting and matches the `IN-01` file-protocol-prefix
precedent of hardening argv construction even when today's caller can't yet
trigger the gap); (2) a **new RTF measurement is still owed** for
video-source jobs specifically, even though `Convert()` code is unchanged —
video demuxing overhead before whisper's dominant cost is a plausible but
unverified assumption (see Pitfalls-equivalent notes in Decision C/D); (3)
`SniffVideo`/`avsniff.go` (Decision B) is needed regardless of which engine
ultimately handles the job, since content detection happens before
`EngineFor` is even called.

### Decision B — container sniffing: two different techniques for two different container shapes

The API's content-detection chain (`internal/api/handlers.go`) runs, in
order: `Sniff` (fixed 12-byte table) → ZIP central-directory inspection →
`LooksLikeHTML` → `IsOLECFB`/`ClassifyCFB` → `SniffAudio` (mp3's variable-
length ID3v2 tag, bounded 512KiB peek) → reject. Video containers split
across **both** existing techniques, not one:

| Container | Magic shape | Fits `sniff.go`'s fixed 12-byte table? | Verified brand/signature facts |
|-----------|-------------|------------------------------------------|-------------------------------|
| MP4 | ISO BMFF `ftyp` box at offset 4-8, major brand at offset 8-12 | **Yes** — identical shape to the existing `matchHEIC`/`matchM4A` matchers | Major brands: `isom`, `mp41`, `mp42`, `mp4v`, `avc1`, `iso2`-`iso9`, `3gp4`/`3gp5`, etc. — **must** be a closed allowlist disjoint from `heicBrands`/`m4aBrands` (already disjoint by construction: those are `heic`/`heix`/`hevc`/`hevx`/`mif1`/`msf1` and `M4A `/`M4B `) |
| QuickTime `.mov` | Same `ftyp` box shape, major brand `"qt  "` (two trailing spaces) | **Yes** — same 12-byte window | `qt  ` is the QuickTime-specific major brand; ISO BMFF is directly derived from QuickTime's container, so this is the standard disambiguator |
| AVI | RIFF container, `"AVI "` fourCC at offset 8-12 | **Yes** — literally the same shape as the existing `matchWAV`/`matchWebP` matchers (`RIFF` at 0-3, fourCC at 8-11) | No new technique needed at all — copy `matchWAV`'s body with `"AVI "` |
| Matroska (`.mkv`) | EBML header, magic `0x1A45DFA3` at offset 0, then a variable-position `DocType` element (`matroska` vs `webm`) | **No** — DocType is not at a fixed offset (preceded by variable-length `EBMLVersion`/`EBMLReadVersion`/`EBMLMaxIDLength`/`EBMLMaxSizeLength` elements) | Verified against RFC 8794 / Matroska spec: docType MUST be `"matroska"` |
| WebM | Same EBML magic, `DocType` **SHOULD** be `"webm"` | **No** — same variable-position problem as mkv | WebM and Matroska are both EBML; the *only* reliable disambiguator is the DocType string, not the outer magic bytes |

**Recommendation:** add `matchMP4`, `matchMOV`, `matchAVI` directly to
`sniff.go`'s existing `signatures` table (zero new infrastructure, exactly
mirrors `matchHEIC`/`matchWAV`'s existing shape). Build a **separate**
bounded-peek matcher for mkv/webm — `matchEBML`/`SniffVideo` — following
`matchMP3`'s established discipline exactly: peek a bounded window (mp3
used 512KiB for ID3v2's declared-length tag; EBML's header elements are
typically only tens of bytes, so a much smaller bound, e.g. 4KiB, is
comfortably safe while still fail-closed), walk the EBML element IDs to
find the DocType element (ID `0x4282`), read its declared vint length,
extract the ASCII value, and match against `{"matroska": "mkv", "webm":
"webm"}`. A DocType that pushes past the bounded window, or any parse
failure, **fails closed** — `""`, not a guess — exactly mirroring
`matchMP3`'s "never grow the buffer, never seek further, just reject"
philosophy for its own declared-length parse. This is real, non-trivial
parsing work (a minimal vint/EBML element reader), not just registry
wiring — flag it in the build order as its own task, not a one-liner.

**How `EngineFor` handles "same source, different target → different
engine":** this is not a special case that needs new registry mechanism.
`Registry.m` is keyed by the **full** `(From, To)` pair, never by `From`
alone. `mp4→mp3` (audio-extraction, registered by `AVConverter`, `Engine()
== "av"`) and `mp4→txt` (transcription, registered by `AudioConverter`'s
extended `Pairs()`, `Engine() == "audio"`) are two entirely independent map
entries. The only real risk is **accidental pair collision** — if
`AVConverter.Pairs()` and `AudioConverter.Pairs()` ever both claim the same
`(From, To)` tuple, `Register`'s documented "later registration wins,
silently" semantics would make the outcome depend on `init()` ordering in
`converters.go`. Today the target sets are disjoint by design
(`mp3`/`wav`/`m4a`/`mp4`/`jpg`/`png`/`webp` for av vs.
`txt`/`srt`/`vtt`/`json` for audio-via-video), but this must be enforced by
a **unit test that enumerates both converters' `Pairs()` and asserts zero
intersection** — there is no existing precedent for this check because no
two converters have ever shared a source-format family before av.

### Decision C — one shared `AV_ENGINE_TIMEOUT` for the whole queue, not per-pair budgets

Every existing engine class uses exactly **one** timeout constant for its
entire queue, sized to that class's worst-case operation:
`ENGINE_TIMEOUT=120s` (image), `DOCUMENT_ENGINE_TIMEOUT=300s`,
`HTML_ENGINE_TIMEOUT=60s`, `AUDIO_ENGINE_TIMEOUT=742s` (RTF-measured against
whisper transcription, the dominant cost regardless of which of the 16
`(source,target)` audio pairs is used). There is **no precedent anywhere in
this codebase for per-pair timeouts**, and `asynq.Unique` TTL derivation
(`ImageUniqueTTL`/`DocumentUniqueTTL`/`AudioUniqueTTL`) is built entirely
around "one `engineTimeout` value, one `maxRetry` value, one formula" — a
per-pair scheme would need a parallel TTL-derivation and retry-schedule
system with no reusable shape to copy.

**Recommendation:** follow the existing pattern exactly — one
`AV_ENGINE_TIMEOUT`, RTF/wall-clock-measured against the **worst-case
transcode** (the genuinely expensive operation; 10-100× more than
thumbnail per the milestone brief), the same "measure, don't guess" GO/NO-GO
discipline Phase 32 used for audio (`scripts/audio-rtf-measure.sh` →
`scripts/av-transcode-measure.sh` analog). Thumbnail and audio-extraction
jobs will simply finish in a small fraction of that ceiling — the shared
ceiling costs nothing in the correctness dimension, only in "how long we
wait before declaring a genuinely stuck process transient-then-terminal,"
and `isAVTerminal`'s stage classification already fails a corrupted input
fast (ffmpeg exits non-zero almost immediately on unparseable input; it
does not wait out the timeout) — so the oversized ceiling on cheap
operations is paid only in the rare stuck-process case, not on every job.

**What genuinely differs from audio and must be built new:** unlike whisper
(where cost is driven almost entirely by declared duration, hence
`AUDIO_MAX_DURATION_SECONDS` alone bounds worst-case cost), transcode cost
is driven by **duration × resolution × codec complexity**. A duration guard
alone under-bounds a very-high-resolution short clip. This is a genuine gap
relative to the audio precedent, not just a copy — see Decision D.

**Per-pair timeouts were considered and explicitly rejected** for this
milestone: the engineering cost (new per-task-type `asynq.MaxRetry`/timeout
wiring, a second TTL-derivation formula, a second retry schedule, and
`RetryDelayFunc`'s dispatch switch growing a third dimension) is
disproportionate to the benefit (saving wall-clock on retries of jobs that
already fail fast on bad input). If real production data later shows
thumbnail jobs are meaningfully retry-budget-starved by a transcode-sized
ceiling, that is a targeted future revisit, not a Phase-1 requirement.

### Decision D — duration AND resolution guards for video (duration alone is insufficient)

`convert.ProbeDuration`/`convert.EnforceMaxDuration`
(`internal/convert/audioduration.go`) are **already generic** — they call
`ffprobe -show_entries format=duration` and validate in float space
(NaN/Inf/negative/overflow-safe, per the documented amd64/arm64 conversion
pitfall). This is directly reusable verbatim for an `AV_MAX_DURATION_SECONDS`
guard, gated the same way `enforceAudioGuardBeforeConvert` gates on
`job.Engine == convert.EngineAudio` — a new `enforceAVGuardBeforeConvert`
gated on `job.Engine == convert.EngineAV`, run **before** the expensive
ffmpeg operation, same fail-closed ordering discipline (sniff → duration
guard → decode/transcode).

**Where this is NOT sufficient, unlike audio:** whisper's cost is duration-
dominated regardless of the audio codec/bitrate — a duration ceiling alone
correctly bounds worst-case whisper cost. Transcode cost is **not**
duration-dominated alone; a short clip at very high resolution
(e.g. 4K/8K) or an absurd declared resolution in a malformed/adversarial
container can still be a decode-bomb even under a modest duration ceiling.
There is **no existing zero-dependency parser** for arbitrary video
codecs' declared resolution the way `VALID-03` built one for
png/jpg/webp/heic/tiff pixel dimensions — building one for H.264/H.265/VP9/
AV1 bitstreams is out of proportion to this milestone. Two honest options:
(1) accept this as a **documented residual risk**, mirroring the
codebase's existing discipline for LibreOffice/chromium resource-exhaustion
risk (`DOC-V2-05`, the `file://` residual-read risk in Phase 15) — ffmpeg's
own internal resource limits are the backstop; or (2) additionally probe
`ffprobe -show_entries stream=width,height` (same near-instant, bounded-ctx
call pattern as `ProbeDuration`, trivially extendable) and reject
declared resolutions above a configured ceiling before transcode starts.
**Recommendation: do (2)** — it is a small, mechanical extension of the
exact function that already exists (`ProbeDuration`'s sibling), and video
resolution bombs are a well-known, well-understood attack class (unlike
audio, where no equivalent "bomb" concept applies) — treating it as
optional-later residual risk when the guard is this cheap to add is not
consistent with this codebase's established fail-closed bias (VALID-03,
CFB classification, zip-bomb rejection all made the same call: build the
cheap probe rather than accept the risk).

**Upload-size ceiling gap (flag, not solved in this research):**
`MAX_UPLOAD_BYTES` is currently **one global value** enforced uniformly
across every engine class via `http.MaxBytesReader` in `handleCreateJob`,
applied **before** `Sniff`/`EngineFor` even runs — the engine class isn't
known yet when the byte ceiling is enforced. Video files are legitimately
much larger than the other four classes' typical inputs at equivalent
"content amount." Raising the global ceiling to accommodate reasonable
video sizes weakens the DoS posture for image/document/html/audio uploads
too; introducing a genuinely per-engine ceiling would require restructuring
upload-size enforcement to happen **after** content detection (a nontrivial
API-layer reordering, not a config bump) since today's `MaxBytesReader` is
applied to the raw multipart body before any byte of it is inspected. This
research recommends treating it as a **documented open question for
planning**, not silently defaulting to "just raise the global limit" —
raising it is the pragmatic MVP choice (matches the "one flag, all
classes" precedent already in place) but should be an explicit,
named decision in ROADMAP/PROJECT.md, not an implicit side effect of
picking a video-friendly number.

## Recommended Project Structure (new files only)

```
internal/convert/
├── av.go                # AVConverter: Pairs()/Convert()/Engine()==EngineAV
├── av_test.go
├── avopts.go             # AVOpts (timecode, resolution_preset, ...) — mirrors audioopts.go
├── avopts_test.go
├── avduration.go         # thin wrapper reusing ProbeDuration + a new resolution probe (Decision D)
├── avduration_test.go
├── avsniff.go             # matchEBML/SniffVideo (mkv/webm DocType, bounded peek) — mirrors audiosniff.go's discipline
├── avsniff_test.go
├── sniff.go              # MODIFIED: += matchMP4, matchMOV, matchAVI in the existing signatures table
├── whisper.go             # MODIFIED: Pairs() += video-container × transcript-target cross product; ffmpegNormalizeArgs gains explicit -vn for video sources
└── converters.go          # MODIFIED: += Default.Register(AVConverter{})

cmd/av-worker/
└── main.go                # mirrors cmd/audio-worker/main.go, no whisper env vars

Dockerfile.av-worker         # ffmpeg only, no whisper build stage — much lighter than Dockerfile.audio-worker

deploy/chart/octoconv/templates/
├── deployment-av-worker.yaml   # mirrors deployment-audio-worker.yaml
└── scaledobject-av.yaml        # mirrors scaledobject-audio.yaml, WR-01 triad verbatim

scripts/
├── av-transcode-measure.sh     # worst-case-transcode timeout GO/NO-GO measurement (mirrors audio-rtf-measure.sh)
└── keda-av-loadproof.sh        # mirrors keda-audio-loadproof.sh, frozen-script discipline
```

### Structure Rationale

- Every new file mirrors an existing sibling 1:1 (`av.go`↔`libreoffice.go`/
  `chromium.go` shape; `avopts.go`↔`audioopts.go`; `avsniff.go`↔
  `audiosniff.go`) — this is deliberate: the codebase's own convention is
  "one file per responsibility, package name matches directory," and every
  prior engine class (document, html, audio) was added this way with zero
  package restructuring. There is no reason for av to deviate.
- `whisper.go` gets a real (if small) modification rather than a new file,
  because the video→transcript capability genuinely lives in the audio
  engine's existing `Convert()` — creating a separate file/type for it would
  duplicate `ffmpegNormalizeArgs`/`whisperArgs`/`model()`/
  `validateAudioOutput` for no benefit.

## Architectural Patterns

### Pattern 1: Converter interface + Registry (reused unchanged)

**What:** `Pairs()`/`Convert()`/`Engine()` — a converter self-describes its
supported `(from,to)` pairs and its engine class; the process-wide
`Registry` indexes every pair, and `EngineFor` is the single source of
truth for API/reconciler routing.

**When to use:** Every new engine class, without exception — this is the
whole reason av "slots in" rather than requiring a framework change (the
milestone's own Out-of-Scope note confirms: "Полный контракт ядра... —
решено расширять существующий Converter/Registry вместо рефакторинга").

**Trade-off surfaced by av specifically:** the registry has never before
needed to arbitrate between two *different* converters both claiming
pairs from the *same source format family*. It still works correctly
(pair-keyed, not source-keyed), but the "later registration wins silently"
semantics (`Register`'s doc comment) becomes a real hazard for the first
time — mitigate with an explicit pair-disjointness test (Decision B).

### Pattern 2: Stage-aware terminal/transient classification (reused, extended)

**What:** `isAudioTerminal`'s Key Decision 1 — classify by *which stage*
failed (ffmpeg-stage = malformed input = terminal; whisper-stage timeout =
transient) rather than a blanket "any timeout is terminal"
(`isDocumentTerminal`/`isHTMLTerminal`'s simpler shape) or "timeout is
always transient" (image's shape).

**When to use for av:** `isAVTerminal` should mirror `isAudioTerminal`'s
stage-aware shape, not the simpler document/html blanket-timeout shape —
av's ffmpeg invocations (decode/transcode/thumbnail-seek) are the *only*
stage (no second binary like whisper-cli), so the natural mapping is:
ffmpeg failure or timeout on a malformed/adversarial input → terminal
(mirrors the image engine's dimension-bomb terminal philosophy the audio
engine's own comment explicitly cross-references); a *duration/resolution*
guard rejection (`ErrAVDurationExceeded`/`ErrAVResolutionExceeded`, mirroring
`ErrAudioDurationExceeded`) → always terminal (rejection can never
succeed on retry); everything else (S3/Postgres blips) → falls through to
the shared `isTerminal`. Unlike audio (two distinct binaries, two distinct
stage-prefixes to match on), av likely does **not** need audio's
`minFfmpegBudget`-style "insufficient remaining budget" special case in the
same form, since there's only one stage to protect — but the *general*
principle (an attempt-ctx pre-exhausted by a slow S3 download must not be
misattributed to "the engine timed out") still applies and should be
re-derived, not assumed away.

### Pattern 3: Env-only-in-main + setter injection (reused, simplified)

**What:** `internal/convert` never calls `os.Getenv` directly; `cmd/*/main.go`
reads env vars once at startup and injects via a package-level setter
(`SetAudioModelPath`, `SetAudioThreads`) before the asynq server starts
consuming tasks (single-write-before-concurrent-reads, no mutex needed).

**When to use for av:** any AV-specific tunable that `internal/convert`
needs at runtime (e.g. a resolution ceiling for Decision D's guard,
transcode preset/codec defaults) must follow this exact convention. Av
needs meaningfully **fewer** of these than audio did — no model path, no
thread-count cgroup detection (ffmpeg's own `-threads` handling under a
cgroup CPU quota is a separate, real question worth verifying empirically
during the containerize phase rather than assuming ffmpeg self-limits
correctly, but it is not a new *mechanism*, just a new value to measure).

## Data Flow

### Video→transcode/extract/thumbnail (new av queue)

```
POST /v1/jobs (file=video.mp4, target=mp4|mp3|jpg)
    ↓
Sniff() misses (video isn't in the 12-byte table... except mp4/mov/avi ARE
    now added there, Decision B) → detected="mp4"
    ↓
EngineFor("mp4","mp4"|"mp3"|"jpg") → AVConverter.Engine() → "av"
    ↓
Postgres-first double write: jobs row (engine="av", status=queued)
    ↓
EnqueueAVConvert → QueueAV (asynq.Unique keyed by job_id, AVUniqueTTL)
    ↓
av-worker: HandleAVConvert → MarkActive → process()
    → enforceAVGuardBeforeConvert (duration + resolution probe, Decision D)
    → AVConverter.Convert (single ffmpeg invocation, stage-aware terminal check)
    → upload result → AddOutput → MarkDone
    ↓
webhook enqueue (if callback_url set) — unchanged, engine-agnostic
```

### Video→transcript (existing audio queue, Decision A)

```
POST /v1/jobs (file=video.mp4, target=txt|srt|vtt|json)
    ↓
Sniff() → detected="mp4" (same detection as above — content detection is
    engine-agnostic, per-PAIR routing happens one step later)
    ↓
EngineFor("mp4","txt") → AudioConverter.Engine() (Pairs() extended,
    Decision A) → "audio"
    ↓
Postgres-first double write: jobs row (engine="audio", status=queued)
    ↓
EnqueueAudioConvert → QueueAudio (existing queue, existing
    AudioUniqueTTL/AUDIO_ENGINE_TIMEOUT=742s, UNCHANGED)
    ↓
audio-worker (existing container, unchanged image): HandleAudioConvert
    → enforceAudioGuardBeforeConvert (existing AUDIO_MAX_DURATION_SECONDS
        guard fires correctly — gated on job.Engine==EngineAudio, not on
        source format, so zero code change needed here)
    → AudioConverter.Convert: ffmpeg normalize (now also handles video
        demux, -vn added) → whisper-cli transcribe (UNCHANGED)
    → upload result → AddOutput → MarkDone
```

### Key Data Flows

1. **Same source, different queue depending on target** — `mp4→mp3` (av
   queue, audio-extraction) and `mp4→txt` (audio queue, transcription) are
   two independent `Registry` entries; there is no shared "video job" state
   anywhere in Postgres or Redis, no cross-queue awareness, no chaining.
   Each is a completely ordinary single-task job from the reconciler's,
   webhook's, and metrics' point of view — they just happen to share a
   `SourceFormat` value.
2. **Reconciler recovery** — `reconciler.go`'s engine switch already fails
   closed on an unrecognized `jobs.engine` value ("av"/"cad"/"archive"/
   "probe" are explicitly named in the fail-closed comment as future
   engines that must add their own case rather than fall through). Adding
   `case convert.EngineAV: enqueueErr = s.enq.EnqueueAVConvert(...)` is a
   two-line, low-risk addition to an already-designed-for-this switch.

## Scaling Considerations

| Scale | Architecture Adjustments |
|-------|--------------------------|
| Current (internal clients, single-cluster) | One `av-worker` Deployment, KEDA scale-0→N→0 on `octoconv_queue_depth{queue="av"}`, mirrors audio's proven pattern exactly (Phase 33 evidence directly reusable as a template) |
| If transcode volume dominates | `AV_WORKER_CONCURRENCY` and CPU/memory limits are the first knob (mirrors audio's `AUDIO_WORKER_CONCURRENCY=1` choice, driven by whisper's RSS footprint under a fixed cgroup budget — av's equivalent constraint is ffmpeg's CPU-bound transcode competing for the same 2-CPU limit; concurrency=1 is the safe starting assumption until measured otherwise) |
| If thumbnail volume dominates and is retry-budget-starved by the shared transcode-sized `AV_ENGINE_TIMEOUT` | This is the one scenario where Decision C's "one shared timeout" tradeoff bites — revisit with real production metrics before building per-pair timeouts speculatively |

## Anti-Patterns

### Anti-Pattern 1: Assuming "video" needs its own end-to-end pipeline

**What people do:** build a second whisper.cpp integration inside the av
container because "video→transcript" sounds like a new capability.

**Why it's wrong:** it duplicates ~682MB of image weight, doubles the
RTF-measurement/GO-NO-GO burden, and ignores that `ffmpeg -i <video>`
already IS the audio-extraction step the existing `AudioConverter.Convert`
performs — the "new capability" is entirely in `Pairs()`, not `Convert()`.

**Do this instead:** extend `AudioConverter.Pairs()` (Decision A).

### Anti-Pattern 2: Treating mkv/webm sniffing like mp4/mov/avi sniffing

**What people do:** try to add `matchMKV`/`matchWebM` to `sniff.go`'s fixed
12-byte-window `signatures` table, copying `matchHEIC`'s shape.

**Why it's wrong:** EBML's `DocType` element (the only reliable mkv/webm
disambiguator) is not at a fixed offset — it's preceded by a variable
number of variable-length preceding EBML elements. A fixed-offset matcher
will silently misdetect or reject valid files depending on how many
optional header elements a given encoder emitted.

**Do this instead:** a bounded-peek, declared-length-aware parser mirroring
`matchMP3`'s discipline (Decision B) — fail closed on anything past the
bound, never grow/seek further.

### Anti-Pattern 3: Sizing `AV_ENGINE_TIMEOUT` off the cheapest operation

**What people do:** measure thumbnail extraction (fast, easy to test) and
set `AV_ENGINE_TIMEOUT` based on that, then discover transcode jobs
routinely time out and get misclassified.

**Why it's wrong:** the milestone brief itself states transcode is 10-100×
more expensive than thumbnail — sizing off the wrong end of that range
means every real transcode job gets killed mid-attempt and (per
`isAVTerminal`'s ffmpeg-timeout-is-terminal classification, mirroring
audio's Key Decision 1) fails terminally instead of transiently, wasting
the retry budget entirely.

**Do this instead:** measure the worst-case transcode explicitly
(`scripts/av-transcode-measure.sh`), same GO/NO-GO discipline as
`scripts/audio-rtf-measure.sh`.

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| `ffmpeg` (video decode/transcode/thumbnail/audio-extract) | Hardened `os/exec` via `runCommand` (process-group kill on timeout) — **identical** invocation discipline to the existing `ffmpegNormalizeArgs`/`ffprobeDurationArgs` | No new exec-hardening mechanism needed; `runCommand` is already engine-agnostic |
| `ffprobe` (duration + resolution guard) | Same short-bound, separate-ctx-from-whole-attempt pattern as `audioProbeTimeout`/`ProbeDuration` | Resolution probe (Decision D) is a one-field extension of the exact same call shape |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| `internal/api` ↔ `internal/convert` (av) | `EngineFor(detected,target)` at job-creation time; opts dispatch switch gains an `EngineAV` case parallel to the existing `EngineAudio`/`EngineHTML`/default cases | No new coupling mechanism — same pattern as document/html/audio |
| `internal/worker` ↔ `internal/convert` (av) | `Registry.Lookup` inside the engine-agnostic `process()` — `HandleAVConvert` is a thin wrapper choosing `isAVTerminal` and `queue.QueueAV` labels, structurally identical to `HandleAudioConvert` | `process()` itself needs one addition: the `enforceAVGuardBeforeConvert` gate, parallel to the existing `enforceAudioGuardBeforeConvert` call, both invoked unconditionally but internally gated on `job.Engine` |
| `internal/reconciler` ↔ `internal/queue`/`internal/convert` (av) | `enqueuer` interface gains `EnqueueAVConvert`; engine switch gains `case convert.EngineAV` | The switch's `default` branch already documents "av/cad/archive/probe are out of scope this milestone... a future engine must add its own case" — this is that future engine arriving |
| `av-worker` ↔ `audio-worker` (shared audio-queue routing for video→transcript) | **No direct communication** — they are only related by both consuming from the registry; `av-worker` never touches `QueueAudio` and `audio-worker` never touches `QueueAV` | This is the one relationship worth stress-testing explicitly in the build-order's integration phase: confirm a video-source, transcript-target job never lands in `av-worker`'s queue by accident (pair-disjointness test, Decision B) |

## Suggested Build Order

Mirrors the audio engine's own four-phase shape (`Phase 30` standalone
foundation → `Phase 31` async-contour wiring → `Phase 32` containerize +
measure → `Phase 33` KEDA/helm parity), which is directly reusable as a
template since av is architecturally the same kind of addition.

**Phase 1 — AV foundation (standalone, NOT registered into `convert.Default`)**
- `AVConverter` (`Pairs()`/`Convert()`/`Engine()==EngineAV`) for transcode,
  audio-extract, thumbnail — three distinct ffmpeg argv builders, one
  `Convert()` dispatching on target format (mirrors `whisperOutputFlag`'s
  target-driven dispatch shape)
- `AVOpts` closed-allowlist type (timecode for thumbnail; resolution/codec
  preset for transcode) — mirrors `AudioOpts`/`HTMLOpts`
- `matchMP4`/`matchMOV`/`matchAVI` added to `sniff.go`'s existing table
- `avsniff.go`: bounded-peek EBML/DocType parser for mkv/webm (real parsing
  work — budget it as such, not a one-line addition)
- `AudioConverter.Pairs()` extended with video-container × transcript
  targets; `ffmpegNormalizeArgs` gains explicit `-vn` for video sources
- `ProbeDuration`-based `AV_MAX_DURATION_SECONDS` guard + new resolution
  probe (Decision D) as standalone, unit-testable functions
- Unit test: pair-disjointness between `AVConverter` and every other
  registered converter (new test category, no prior precedent)
- Fixture-based sniff tests against real ffmpeg-produced mp4/mov/mkv/webm/avi
  samples (mirrors `30-01`'s audio-fixture test discipline)
- **Registration deferred** — same scope-fence Phase 30 used for
  `AudioConverter` (build and test in isolation before touching the live
  registry/queue)

**Phase 2 — Async contour integration**
- `EngineAV` constant; `Default.Register(AVConverter{})`
- `TypeAVConvert`/`QueueAV`/`NewAVConvertTask`/`avRetrySchedule`/
  `AVRetryDelay`/`AVUniqueTTL` in `internal/queue/queue.go` (mirrors
  `Audio*` symbols exactly; `jobs_engine_check` needs **no migration** —
  `'av'` is already allow-listed)
- `isAVTerminal` (stage-aware, mirrors `isAudioTerminal`'s shape minus the
  two-binary complexity) and `HandleAVConvert` in `internal/worker/worker.go`
- `enforceAVGuardBeforeConvert` spliced into `process()`, gated on
  `job.Engine == convert.EngineAV`
- API routing: `handleCreateJob`'s enqueue switch and opts-dispatch switch
  both gain an `EngineAV` case
- Reconciler: `enqueuer` interface + engine switch gain the av case
- `EnqueueAVConvert` on `queue.Client`
- IN-02-style env-parity sweep: `AV_MAX_RETRY`/`AV_ENGINE_TIMEOUT`/
  `AV_MAX_DURATION_SECONDS` must be added to **every**
  `queue.NewClient()`-constructing service's env (api, worker,
  document-worker, chromium-worker, audio-worker, av-worker,
  webhook-worker×2 — nine services total once av joins)
- Explicit integration test: a video-source/transcript-target job lands on
  `QueueAudio`, not `QueueAV` (verifies Decision A end-to-end, not just at
  the unit level)

**Phase 3 — Containerize + measure**
- `Dockerfile.av-worker` (ffmpeg + ca-certificates only, no whisper stage —
  should land far below `audio-worker`'s 682MB)
- `cmd/av-worker/main.go` (mirrors `cmd/audio-worker/main.go` minus
  whisper-specific env)
- `scripts/av-transcode-measure.sh`: GO/NO-GO measurement against the
  worst-case supported transcode (largest resolution/duration/codec
  combination this milestone intends to support) — mirrors
  `scripts/audio-rtf-measure.sh`'s methodology, but measuring wall-clock
  vs. transcode parameters rather than whisper's RTF concept specifically
- Separately measure/verify: does video-source→transcript (routed to the
  *existing* audio-worker, Decision A) meaningfully change whisper's
  measured RTF assumption? If video demux overhead is non-negligible
  relative to `AUDIO_ENGINE_TIMEOUT`'s existing margin, this surfaces here,
  not as an afterthought
- `docker-compose.yml`: `av-worker` service, resource limits matching the
  measurement container, `stop_grace_period` = `AV_ENGINE_TIMEOUT` + margin
  (mirrors `audio-worker`'s 762s pattern)
- E2E tests (`internal/e2e`): transcode, audio-extract, thumbnail (av
  queue) + video→transcript (audio queue, confirms Decision A live, not
  just via unit test)

**Phase 4 — KEDA/Helm parity**
- `deployment-av-worker.yaml`/`scaledobject-av.yaml` (mirrors audio's
  templates; WR-01 triad verbatim: `ignoreNullValues:"false"`,
  `fallback.replicas:1`, retry-inclusive PromQL)
- `values.yaml` `avWorker` section; `terminationGracePeriodSeconds` =
  `AV_ENGINE_TIMEOUT` + margin
- `QueueAV` added to the api's `NewQueueDepthCollector` call (five engine
  queues + webhook, once av joins)
- `scripts/keda-av-loadproof.sh` (mirrors `keda-audio-loadproof.sh`,
  frozen-script discipline once it passes)
- **No new KEDA proof needed for video→transcript** specifically — it
  inherits the audio queue's already-proven scale-from-zero coverage
  (Phase 33 evidence); only the integration test from Phase 2 needs to
  confirm routing, not autoscaling behavior

**Dependency notes:**
- Phase 1 must fully precede Phase 2 (mirrors the audio milestone's own
  "standalone before registered" scope fence — reduces the blast radius of
  a half-wired engine touching the live registry).
- The EBML sniffer (mkv/webm) is the single highest-uncertainty item in
  Phase 1 — real parsing code, not a lookup table — and should be started
  first within the phase so any surprises (e.g. real-world encoders
  emitting DocType further into the stream than expected) surface early
  rather than blocking Phase 2's integration test at the end.
- Phase 3's transcode-timeout measurement gates Phase 4 the same way
  audio's RTF measurement gated its own KEDA phase (`AV_ENGINE_TIMEOUT`
  feeds `terminationGracePeriodSeconds` and the scale-down stabilization
  window, both of which would need a second pass if the measured value
  changes after Phase 4's templates are written).

## Sources

- Codebase: `internal/convert/convert.go`, `internal/convert/whisper.go`,
  `internal/convert/audiosniff.go`, `internal/convert/audioduration.go`,
  `internal/convert/audioopts.go`, `internal/convert/sniff.go`,
  `internal/convert/converters.go`, `internal/queue/queue.go`,
  `internal/worker/worker.go`, `internal/api/handlers.go`,
  `internal/reconciler/reconciler.go`, `internal/db/migrations/0006_audio_engine.sql`,
  `cmd/audio-worker/main.go`, `Dockerfile.audio-worker`, `docker-compose.yml`,
  `deploy/chart/octoconv/templates/deployment-audio-worker.yaml`,
  `deploy/chart/octoconv/templates/scaledobject-audio.yaml`,
  `deploy/chart/octoconv/values.yaml`, `.planning/PROJECT.md`
- [EBML specification (ietf-wg-cellar)](https://github.com/ietf-wg-cellar/ebml-specification/blob/master/specification.markdown) — EBML magic `0x1A45DFA3`, DocType element mechanism
- [RFC 8794: Extensible Binary Meta Language](https://www.rfc-editor.org/rfc/rfc8794.html) — authoritative EBML structure spec
- [Matroska Basics](https://www.matroska.org/technical/basics.html) — DocType `"matroska"` requirement
- [The WebM Project — Container Guidelines](https://www.webmproject.org/docs/container/) — DocType `"webm"` convention
- [ISO base media file format — Wikipedia](https://en.wikipedia.org/wiki/ISO/IEC_base_media_file_format) — `ftyp` brand mechanism, `isom`/`mp41`/`mp42` generic brands
- [Complete List of all known MP4/QT 'ftyp' designations (ftyps.com)](https://www.ftyps.com/) — authoritative brand-code reference including `"qt  "` (QuickTime)

---
*Architecture research for: OctoConv v1.8 AV Engine (video/ffmpeg)*
*Researched: 2026-07-19*
