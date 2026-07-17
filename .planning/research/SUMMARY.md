# Project Research Summary

**Project:** OctoConv
**Domain:** Async internal file-conversion microservice — milestone v1.7 adds a 4th engine class (offline whisper.cpp audio transcription) plus the v1.6 hardening tail
**Researched:** 2026-07-17
**Confidence:** MEDIUM-HIGH (in-repo architecture/patterns are HIGH confidence, direct code reads; whisper.cpp/ffmpeg external facts are HIGH for versions/CLI flags/build flags — live-verified against upstream sources — but MEDIUM for CPU realtime-factor sizing, JSON schema field names, and hallucination/thread-limit behavior, which are WebSearch-triangulated with no in-repo precedent to cross-check against)

## Executive Summary

OctoConv v1.7 is not a new product shape — it is the 4th application of an already-proven "engine-class" pattern (image -> document -> html -> **audio**), plus closing four small, pre-diagnosed v1.6 hardening items (WR-01 PromQL semantics, OPER-01 operator-passthrough live gate, gate-tooling warnings, K8S-02 direct-dial recheck). Every research file agrees the audio class should be built by copy-adapting the **document/html precedent**, not the image one: whisper.cpp and ffmpeg are external binaries that can legitimately hang or run long on valid input, run inside their own dedicated container (`Dockerfile.audio-worker`, mirroring `Dockerfile.document-worker`'s shape), and touch the same ~15 files every prior engine class touched (convert registry, queue/client, worker handler, API/reconciler routing switches, Helm chart Deployment + ScaledObject). The stack is locked at `whisper.cpp v1.9.1` (built from source, `-DGGML_NATIVE=OFF` is load-bearing to avoid SIGILL on non-build-host CPUs) plus system `ffmpeg` as a mandatory, separate preprocessing `runCommand` step (whisper.cpp's own README states it only accepts 16-bit WAV; MP3/M4A/OGG/Opus must be normalized first). Model choice (`base` vs `small`) is flagged as an **open product decision**, not resolved by research — `base` is recommended as the shipped default with `small` as a later opt-in, but this should be confirmed against real transcript-quality feedback, not assumed.

The single most important design decision for the whole milestone is getting the `target_format=json` transcript contract right on day one: it is the SEED-001 hinge point (this milestone ships transcription only; the next milestone's mistake-analysis/spaced-repetition consumer reads this contract without any `Converter`/`Registry` change). Segment- and ideally word-level timestamps must be included now, even though nothing consumes the extra fields yet, because retrofitting them later would be a breaking contract change exactly when the next milestone starts. The research files surface one real, unresolved tension that the roadmap must decide explicitly: FEATURES.md and ARCHITECTURE.md both recommend a single, document-style "timeout = terminal" classification for `AUDIO_ENGINE_TIMEOUT`, while PITFALLS.md argues this naively conflates "input is corrupt" (ffmpeg hang — genuinely terminal) with "input is legitimately long" (a 45-minute lesson recording exceeding a tight timeout — should be transient/retryable with fresh CPU allocation). This is called out below as a **Key Decision** the roadmap phase must resolve, not a research gap.

The most significant net-new risks, none seen in any prior engine class, are: (1) MP3's ID3v2 tag structurally breaks the project's fixed-offset magic-bytes sniffing pattern and needs bespoke variable-offset detection; (2) whisper.cpp's threading defaults to host core count, not the container's cgroup CPU limit, risking throttling/OOM under the project's existing `cpus: "2.0"` worker ceiling; (3) baking a 150MB-500MB ggml model into the worker image risks silently defeating the sub-15-second scale-from-zero property that Phase 27/28 spent two phases proving for the other three engine classes — this needs its own timestamped load-proof measurement, not an assumption that it "just works" like the others; (4) ASR output is the project's first genuinely non-deterministic engine output, breaking the "assert exact string" E2E testing habit used everywhere else, and hallucination-on-silence is a documented whisper-family failure mode that exits 0 with a structurally valid but semantically garbage transcript — no existing terminal-signature classifier can catch it, so it must be logged as an explicit accepted residual risk, not silently shipped.

## Key Findings

### Recommended Stack

The stack is narrow and mostly LOCKED by the milestone framing: `ggml-org/whisper.cpp` v1.9.1 (built from source in a Dockerfile builder stage — no Debian apt package exists), invoked via its `whisper-cli` binary through the project's existing hardened `runCommand` exec wrapper with zero new Go dependencies (no CGo bindings — the project is `CGO_ENABLED=0` everywhere). `ffmpeg` (Debian bookworm's apt-pinned `7:5.1.9-0+deb12u1`) is a hard, confirmed prerequisite — whisper.cpp's own README states it "currently runs only with 16-bit WAV files." The ggml model file (`ggml-base.bin`/`ggml-small.bin`, NOT the GGUF format used by llama.cpp) must be baked into the image at build time, pinned by SHA-256, never fetched at container runtime — this both matches the offline-worker constraint and avoids a KEDA scale-from-zero cold-start network dependency.

**Core technologies:**
- `whisper.cpp v1.9.1` (`whisper-cli` binary) — CPU-only offline speech-to-text — the only implementation fitting the "one hardened CLI shell-out per engine class" pattern; build with `-DGGML_NATIVE=OFF` explicitly (default `ON` compiles `-march=native` for the build host and SIGILLs on any runtime host lacking those exact CPU extensions — a well-documented, recurring whisper.cpp/llama.cpp Docker footgun)
- `ggml-base.bin` / `ggml-small.bin` — acoustic+language model weights, pinned by SHA-256 from HuggingFace (the canonical source; the old CDN is dead) — **model choice (base vs small) is an open product decision**, not resolved here; `base` (142 MiB, ~388 MB RAM, RTF ~0.2-0.5 on modern arm64) is the recommended default, `small` (466 MiB, ~2-3x slower) as a later opt-in if transcript-quality complaints emerge
- `ffmpeg` (bookworm apt, pinned) — mandatory pre-conversion of arbitrary input audio (mp3/m4a/ogg/opus) to 16kHz mono 16-bit PCM WAV; kept as its own separate `runCommand` step rather than whisper.cpp's `WHISPER_COMMON_FFMPEG` compiled-in flag, preserving the "one external tool per hardened boundary" discipline

### Expected Features

This milestone scopes strictly to transcription (SEED-001's "фундамент" — mistake-analysis is explicitly deferred to the next milestone). The central feature-design finding: whisper.cpp's own output-format flags (`-otxt`/`-osrt`/`-ovtt`/`-oj`) map directly onto OctoConv's existing `(source_format, target_format)` `Pair` mechanism — output-format selection needs **zero new API surface**, just more `Pairs()` in the audio `Converter`.

**Must have (table stakes):**
- mp3/wav/m4a/ogg/opus input, normalized via ffmpeg to 16kHz mono WAV (covers phone/Zoom/voice-note sources; whisper.cpp's native miniaudio decoder does NOT handle M4A/AAC or Opus)
- Magic-bytes content validation for all five input containers (parity with every other engine's fail-closed posture) — **MP3 is the hard case**, flagged for explicit resolution (see Key Decisions)
- `target_format` in {`txt`, `srt`, `vtt`, `json`} as ordinary `Converter.Pairs()`
- `json` target with segment-level (ideally word-level) start/end/text — **the SEED-001 hinge point**, must not be cut for scope even though nothing consumes it yet
- `AudioOpts{Language, Translate}` via the existing validated-opts mechanism, default `Language="auto"` (explicitly overriding whisper.cpp's own CLI default of `en`)
- Upfront duration guard via `ffprobe` before the expensive step (resource-exhaustion parity with the image engine's pixel-count guard)
- Dedicated `audio` asynq queue, `cmd/audio-worker`, KEDA ScaledObject, reconciler routing entry — mirrors the document-engine-class precedent exactly

**Should have (competitive):**
- Named presets for common transcription configs (trivial once `AudioOpts` exists)
- Confidence/no-speech-probability fields in the `json` contract if the pinned whisper.cpp version exposes them cleanly (also the cheapest available hallucination-mitigation signal, see Key Decisions)

**Defer (v2+):**
- Real speaker diarization — whisper.cpp's own options are English-only-experimental (`tinydiarize`) or require pre-split stereo channels; incompatible with single-track bilingual tutor/student recordings and with the "no heavy new runtime per engine" pattern
- Real-time/streaming transcription — foreign to the post-hoc upload transport SEED-001 actually uses
- Arbitrary target-language translation — whisper.cpp's `--translate` is hardcoded to English-only
- Finer-grained streaming progress (`--print-progress`) — needs a new streaming-exec primitive; the existing `runCommand` buffers until process exit
- Mistake detection / spaced-repetition deck generation — explicitly the next milestone's job, not this one's

### Architecture Approach

Audio is the pattern's 4th instantiation, not a new architecture: one `Converter` implementation registered into `convert.Default`, one dedicated asynq queue + worker binary + Dockerfile + compose service + Helm Deployment + ScaledObject, touching the same ~15 files every prior engine class touched (`internal/convert`, `internal/queue`, `internal/worker`, `internal/api`, `internal/reconciler`, `cmd/api/main.go`'s metrics collector, the Helm chart). It should be built by copy-adapting `document-worker`/`chromium-worker`, not `image`/`libvips` — both document and audio run an external binary that can legitimately hang on bad input and need a heavier, non-trivial-to-install runtime dependency baked into a dedicated Dockerfile.

**Major components:**
1. `internal/convert/whisper.go` (NEW) — `AudioConverter` implementing `Pairs()`/`Convert()`/`Engine()`; `Convert()` runs TWO sequential `runCommand` invocations (ffmpeg resample, then whisper-cli transcribe) inside one `AUDIO_ENGINE_TIMEOUT`-bounded context — the first engine class needing two external processes per job, not one
2. `internal/convert/audiosniff.go` (NEW) — magic-byte detection for wav/ogg/m4a (straightforward, reuses HEIC's ISOBMFF box-walk pattern for m4a) plus a bespoke MP3 detector (see Key Decisions)
3. `internal/queue` + `internal/worker` + `cmd/audio-worker` — `TypeAudioTranscribe`/`QueueAudio`, a from-scratch `AudioUniqueTTL` derivation (never reuse another class's), `HandleAudioTranscribe`, and the terminal/transient timeout classifier (the milestone's central open decision)
4. `internal/api`/`internal/reconciler` engine-routing switches — both must gain an `EngineAudio` case or audio jobs 500 (API) or silently degrade to `unroutable_engine` (reconciler)
5. `Dockerfile.audio-worker` + Helm chart (`deployment-audio-worker.yaml`, `scaledobject-audio.yaml`) — multi-stage build (Go stage + whisper.cpp-from-source stage + ffmpeg-via-apt runtime stage), model baked in at build time, `USER nobody`

### Critical Pitfalls

1. **Timeout classification conflates "input is bad" with "input is legitimately long"** — a naive copy of the document class's blanket "timeout = terminal" fails ordinary long recordings on the first attempt with zero retry chance. Avoid by splitting the failure domain: ffmpeg-stage timeout -> terminal (malformed/adversarial input signal); whisper-stage timeout on already-duration-validated audio -> transient (mirror the image engine's classifier), bounded by `AUDIO_MAX_RETRY`. **This is the Key Decision below — FEATURES.md/ARCHITECTURE.md's "just mirror document" recommendation and PITFALLS.md's stage-aware argument must be reconciled explicitly in the roadmap, not left ambiguous.**
2. **`AudioUniqueTTL` reuse/undersizing reopens the T-03-10 double-processing race** — every existing class derives its asynq unique-lock TTL fresh from `(maxRetry+1) x engineTimeout`; if audio reuses `ImageUniqueTTL` or a hardcoded default sized for a 120s timeout while real jobs run 30-60+ minutes, the Redis lock expires mid-transcription and the reconciler enqueues a second concurrent whisper.cpp process against the same job — the exact race the `attemptCtx` design exists to prevent, now reopened for the most expensive class to duplicate.
3. **MP3's ID3v2 tag breaks the fixed-offset `sniffLen=12` signature-table pattern structurally** — a large fraction of real-world MP3s carry a variable-length, self-describing ID3v2 header (synchsafe-encoded size, can push the true MPEG frame sync hundreds of KB in via embedded album art) that no existing `matchPNG`/`matchJPEG`-style fixed-window matcher can handle. Needs its own bounded, variable-offset detector — never a naive table entry.
4. **KEDA `cooldownPeriod` only governs 1->0 scale-down; the Kubernetes HPA's default 300s `scaleDown.stabilizationWindowSeconds` governs N->N-1** and is nowhere near enough protection for a 45-60 minute job — this is a lesson the project already learned and documented in Phase 28 for the document class; audio must reuse the `scaleDownStabilizationSeconds` chart knob explicitly, sized above worst-case job duration, not just tune `cooldownPeriod`.
5. **Baking the ggml model into the worker image can silently defeat the scale-from-zero property Phase 27/28 spent two phases proving** (11s to 4 replicas) — a multi-hundred-MB image adds real cold-pull time that could dominate the sub-15s SLA on OrbStack specifically. This is not automatically disqualifying (bake-in is still the simplest, most "offline" option) but must be a deliberate, measured decision with its own load-proof-style evidence, not an unmeasured default.
6. **Whisper-family hallucination on silence/music produces a structurally valid, exit-0 transcript that is semantically garbage** — no existing terminal-signature classifier (`terminalVipsSignatures` et al.) can catch a "successful" process that looped nonsense text. Not solvable this milestone; must be logged as an explicit accepted residual risk (mirroring the project's existing `file://` residual-risk pattern), with the cheapest mitigation (VAD/no-speech-probability surfaced in the `json` contract) applied if in scope.

## Implications for Roadmap

### Key Decision 1: Stage-aware timeout classification (resolves FEATURES/ARCHITECTURE vs PITFALLS conflict)

FEATURES.md ("`AUDIO_ENGINE_TIMEOUT` classified terminal — document precedent, NOT image's transient-timeout precedent") and ARCHITECTURE.md ("`isAudioTerminal` should be `timeoutIsTerminal(err)`, mirroring `isDocumentTerminal`/`isHTMLTerminal` verbatim") both recommend a single blanket terminal classification, reasoning that whisper.cpp's cost is "a near-deterministic function of (audio duration x model size x CPU)." PITFALLS.md directly challenges this: unlike LibreOffice/chromium hangs (which are pathological — bad input, will time out identically on retry), a whisper.cpp timeout on a long-but-valid recording is not pathological, it's the expected cost of legitimate work; blanket-terminal means any file landing even slightly outside the configured timeout is permanently failed on the first attempt with zero chance of success even on a less-loaded worker.

**Resolution for the roadmap:** adopt PITFALLS.md's stage-aware split, since it is strictly more correct without added complexity (both stages already produce distinguishable wrapped errors): ffmpeg-stage failure/timeout -> terminal (signal of malformed/adversarial input, same rationale as the image dimension-bomb guard); whisper-stage timeout on audio that has already passed the ffprobe duration/format check -> transient (mirror the image engine's classifier, no `context.DeadlineExceeded` terminal arm), bounded by `AUDIO_MAX_RETRY`. This must be decided and documented in the audio engine core phase's Key Decisions before `isAudioTerminal` is implemented — it is not boilerplate copy-paste from either sibling class.

### Key Decision 2: Model choice (base vs small) is an open product decision, not a research conclusion

STACK.md recommends baking `base` as the sole default model, offering `small` as a later opt-in preset — but flags this explicitly as unresolved pending real transcript-quality feedback on the actual (likely accented/bilingual, per SEED-001's tutor/student framing) audio population. The roadmap should treat model selection as a decision to make with a cheap reversibility path (build-arg/preset variant), not something to lock permanently in the Dockerfile with no revisit trigger. Bundling both models by default is explicitly rejected (pushes image size toward ~1GB, undermining Key Decision 3 below).

### Key Decision 3: Model distribution (bake-in vs volume) must be a measured tradeoff, not a default

Bake-in is simplest and matches the "workers stay offline" constraint most directly, but risks silently defeating the scale-from-zero SLA Phase 27/28 already proved for the other three classes. The roadmap should require a Phase-28-style timestamped load-proof measurement for the audio class specifically before calling KEDA integration done, and treat the bake-in-vs-volume choice as reversible based on that measurement, not fixed a priori.

### Suggested Phase Structure

1. **v1.6 Hardening Tail** (WR-01 empty-PromQL semantics, OPER-01 compose `OPERATOR_CLIENT_IDS` passthrough + live gate, gate-tooling script warnings, K8S-02 direct-dial recheck)
   **Rationale:** Zero dependency on the audio work; all four items are already fully diagnosed by prior phase reviews (26/27/28-REVIEW.md), cheap, mechanical. Landing WR-01 first specifically means `scaledobject-audio.yaml` (a later phase) is written correctly from its first commit instead of needing a follow-up patch.
   **Delivers:** Closed v1.6 audit findings; chart `ignoreNullValues` fix applied to all three existing ScaledObjects; verified operator-passthrough live gate; fixed gate-tooling scripts; resolved K8S-02 direct-dial verification.
   **Avoids:** Reintroducing a known-bad chart pattern into the new audio ScaledObject (Anti-Pattern: copying `scaledobject-document.yaml` verbatim before the fix lands).

2. **Audio Engine Foundation** (`internal/convert`: `EngineAudio` const, `audiosniff.go`, `MIMEType` additions, `AudioConverter` skeleton + the ffmpeg->whisper-cli `Convert()` pipeline as a standalone testable unit)
   **Rationale:** This is the one genuinely novel piece of engineering (two sequential external processes, a real model file, real output validation) — get it working standalone before investing in surrounding plumbing (queue/worker/chart).
   **Delivers:** A working `AudioConverter` that can transcribe a local file to txt/srt/vtt/json, with magic-bytes validation (including the bespoke MP3 detector) and a duration/size sanity guard.
   **Addresses:** Table-stakes input formats, `target_format` pairs, `json` contract (SEED-001 hinge), magic-bytes parity, duration guard.
   **Avoids:** Pitfall 6 (ffmpeg decompression-bomb-equivalent CVE surface), Pitfall 7 (MP3 fixed-offset sniff failure), Pitfall 11 (opts-injection if model/language selection is exposed).

3. **Queue, Worker & Routing Integration** (`internal/queue` task/TTL, `internal/worker` handler + stage-aware terminal classifier per Key Decision 1, `cmd/audio-worker`, `internal/api`/`internal/reconciler` engine-routing switches, `cmd/api/main.go` metrics registration)
   **Rationale:** Mechanical once the pattern is followed, but this is where the milestone's most consequential open decision (timeout classification, Key Decision 1) and the `AudioUniqueTTL` derivation (Pitfall 2) must be gotten right — both are easy-to-miss, hard-to-notice-until-production bugs.
   **Delivers:** Audio jobs flow end-to-end through the async pipeline with correct retry/dedup semantics.
   **Uses:** `internal/queue/queue.go`'s `Document*`/`HTML*` blocks as structural templates (not literal reuse).
   **Implements:** Engine-class routing pattern, stage-aware timeout classification (Key Decision 1).

4. **Containerization & Local E2E** (`Dockerfile.audio-worker`, `docker-compose.yml` service, CI bake matrix, an explicit RTF (realtime-factor) measurement against the container's real CPU allocation)
   **Rationale:** Get the container building and runnable in compose before touching Kubernetes; this is also where `AUDIO_ENGINE_TIMEOUT` and `AUDIO_WORKER_CONCURRENCY` get sized from a *measured* benchmark rather than a copy-pasted document-class constant that would be off by 1-2 orders of magnitude.
   **Delivers:** A running audio-worker in compose, a measured RTF-to-timeout mapping, an explicit whisper.cpp `--threads` flag sized to the container's cgroup CPU limit (not host core count).
   **Addresses:** Sizing inputs for `AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY`/`AUDIO_WORKER_CONCURRENCY`.
   **Avoids:** Pitfall 5 (thread-count-vs-cgroup-limit throttling/OOM), the "copy-pasted timeout constant" trap flagged in Pitfall 1.

   *This phase should treat its RTF benchmark as a measured go/no-go gate, mirroring the project's own precedent for veraPDF's JVM startup cost in Phase 23 — do not ship `AUDIO_ENGINE_TIMEOUT` as an assumption.*

5. **KEDA/Helm Chart Integration & Production Hardening** (chart `audioWorker`/`keda.audio` blocks, `deployment-audio-worker.yaml`, `scaledobject-audio.yaml` with the WR-01 fix and the `scaleDownStabilizationSeconds` override applied from day one, model-distribution decision per Key Decision 3, reconciler staleness threshold adjustment, live E2E against the real cluster)
   **Rationale:** Depends on the stable compose service/env contract from Phase 4; the ScaledObject should be written with WR-01 already fixed (Phase 1) so it's correct on first commit.
   **Delivers:** Full production wiring including scale-from-zero measurement specific to the audio class, HPA N->N-1 protection sized above worst-case job duration, and a documented accepted-residual-risk entry for hallucination-on-silence.
   **Addresses:** Production readiness parity with the other three engine classes.
   **Avoids:** Pitfall 4 (KEDA cooldown-vs-HPA-stabilization gap), Pitfall 8 (unmeasured scale-from-zero regression), Pitfall 3 (reconciler global staleness threshold not audio-aware), Pitfall 9/10 (non-deterministic-output test design, hallucination accepted-risk documentation).

### Phase Ordering Rationale

- Hardening tail is sequenced first purely because it is zero-cost, zero-risk, and prevents a guaranteed follow-up patch on the new audio ScaledObject (WR-01 must precede its authoring).
- The converter/pipeline logic (Phase 2) is sequenced before queue/worker plumbing (Phase 3) because it is the one genuinely novel piece of external-process integration — validate it as a standalone unit before wiring it into the async lifecycle, mirroring how document (Phase 10-11) and html (Phase 15) engines were built before their KEDA integration arrived three phases later in v1.6, not simultaneously.
- Containerization/RTF measurement (Phase 4) is deliberately placed before Helm/KEDA (Phase 5) because `AUDIO_ENGINE_TIMEOUT` sizing, `AUDIO_WORKER_CONCURRENCY`, and the model-bake-vs-volume decision all depend on a real measurement that can only happen once the container exists — chart tuning without that measurement would be guesswork.
- This ordering directly avoids Pitfall 1 (unmeasured timeout copy-paste), Pitfall 2 (TTL derivation skipped under time pressure), Pitfall 5 (thread/cgroup mismatch discovered only in production), and Pitfall 8 (scale-from-zero regression discovered only after chart work is "done").

### Research Flags

Phases likely needing deeper research during planning (`--research-phase`):
- **Phase 2 (Audio Engine Foundation):** JSON output schema field names for `-oj`/`-ojf` are LOW-MEDIUM confidence (extrapolated from adjacent tooling, not confirmed against the actual pinned v1.9.1 binary's output) — must be verified live against the pinned binary before the SEED-001-facing contract is finalized. MP3 ID3v2 detection design also warrants a focused look at synchsafe-integer decoding correctness.
- **Phase 4 (Containerization & Local E2E):** RTF/thread-count/memory sizing is MEDIUM confidence, WebSearch-triangulated with no first-party benchmark on comparable hardware — needs empirical measurement against the project's actual container CPU/memory limits, not just literature review.
- **Phase 5 (KEDA/Helm):** Model-distribution tradeoff (bake vs volume/init-container) and its interaction with OrbStack's specific pull-time characteristics is under-researched relative to the rest of the chart work; may need a scoped research pass on init-container/PVC patterns if bake-in's measured pull time proves unacceptable.

Phases with standard patterns (skip research-phase):
- **Phase 1 (Hardening Tail):** Fully pre-diagnosed by existing review documents (26/27/28-REVIEW.md) — closing known findings, not discovering new ones.
- **Phase 3 (Queue/Worker/Routing):** Mechanically mirrors three prior engine classes' code shape almost exactly; the one real decision (Key Decision 1) is already resolved above.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH for versions/CLI/build flags (live-verified against GitHub Releases API, live README, live HuggingFace HEAD requests); MEDIUM for CPU realtime-factor sizing (no first-party benchmark on comparable modest cloud/OrbStack cores) |
| Features | MEDIUM (CLI flags/build options verified against upstream README/discussions; exact `-oj`/`-ojf` JSON field names are extrapolated from adjacent tooling, not confirmed against the pinned binary — flagged as a Phase 2 research item) |
| Architecture | HIGH for in-repo patterns (every claim grounded in direct reads of `internal/convert`, `internal/queue`, `internal/worker`, `internal/api`, `internal/reconciler`, Helm chart, cross-referenced against three prior engine-class additions); MEDIUM for whisper.cpp-specific facts (WebSearch-verified, no in-repo precedent) |
| Pitfalls | HIGH for OctoConv-internal patterns (verified against `internal/worker/worker.go`, `internal/queue/queue.go`, `internal/reconciler/reconciler.go`, chart templates, v1.6 phase reviews); MEDIUM for whisper.cpp/ffmpeg-specific external claims (thread-limit behavior, hallucination, CVE surface — WebSearch, cross-checked across multiple sources but not Context7-verified) |

**Overall confidence:** MEDIUM-HIGH — the architectural shape and in-repo integration points are HIGH confidence and well-precedented; the genuinely new external-tool behavior (RTF sizing, JSON schema, hallucination, thread/cgroup interaction) is consistently MEDIUM confidence across all four files and should be treated as planning input requiring empirical validation during phase execution, not as ship-blocking guarantees.

### Gaps to Address

- **JSON output schema (`-oj`/`-ojf` field names):** Not confirmed against the pinned v1.9.1 binary's actual output — verify live in Phase 2 before finalizing the SEED-001-facing contract; this is the highest-leverage unresolved gap since it is a forward-compatibility commitment to a future milestone.
- **Realtime-factor (RTF) sizing:** Triangulated from third-party/community sources on non-comparable hardware — treat as a planning input requiring a measured go/no-go gate in Phase 4 (mirrors the project's own veraPDF JVM-startup-cost precedent from Phase 23), not a fixed constant to ship on faith.
- **Model choice (base vs small):** Open product decision per Key Decision 2 — research recommends `base` as default but flags this as unresolved pending real transcript-quality feedback on the actual (bilingual, tutor/student) audio population.
- **Model distribution (bake vs volume):** Open per Key Decision 3 — bake-in is simplest but risks an unmeasured scale-from-zero regression; requires a Phase 5 load-proof-style measurement before the tradeoff can be called resolved.
- **Timeout classification (terminal vs transient):** Resolved in this document as Key Decision 1 (stage-aware split) — the roadmap must carry this resolution forward explicitly into the Phase 3 context doc, since two of the four research files recommended the simpler-but-wrong blanket approach.
- **Hallucination-on-silence:** Explicitly unsolvable this milestone (no simple structural detection exists per current research) — must be logged as an accepted residual risk in the Phase 2/5 decision log, with the cheapest available mitigation (no-speech-probability surfaced in the `json` contract) applied if the pinned whisper.cpp version supports it cleanly.
- **whisper.cpp thread/memory behavior under cgroup limits:** MEDIUM confidence, one anecdotal OOM report in the wider ecosystem — needs an explicit benchmark against the actual `cpus`/`memory` chart values before `AUDIO_WORKER_CONCURRENCY` is fixed (Phase 4).

## Sources

### Primary (HIGH confidence)
- `https://github.com/ggml-org/whisper.cpp/releases` + GitHub Releases API — live-verified tag `v1.9.1`, published `2026-06-19`
- `https://raw.githubusercontent.com/ggml-org/whisper.cpp/v1.9.1/{README.md,examples/cli/README.md,models/download-ggml-model.sh,.devops/main.Dockerfile,ggml/CMakeLists.txt}` — live-fetched at the pinned tag: 16-bit-WAV requirement, `GGML_NATIVE`/`GGML_CPU_ALL_VARIANTS` cmake options, ggml (not GGUF) model format, no built-in checksum verification
- `https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-{tiny,base,small,medium}.bin` — live HEAD/GET requests confirming exact byte sizes and SHA-256
- `debian:bookworm-slim` live `apt-cache policy ffmpeg` — `ffmpeg 7:5.1.9-0+deb12u1`
- Direct reads of `internal/convert/*.go`, `internal/queue/*.go`, `internal/worker/worker.go`, `internal/api/{api,handlers}.go`, `internal/reconciler/reconciler.go`, `internal/metrics/queue_collector.go`, `cmd/{api,worker,document-worker}/main.go`, `docker-compose.yml`, `.github/workflows/ci.yml`, `Dockerfile.{worker,document-worker}`, `deploy/chart/octoconv/{values.yaml,templates/*}` — ground truth for every in-repo architectural claim
- `.planning/milestones/v1.6-phases/{26-operator-presets-rest/26-REVIEW.md, 27-keda-autoscaling/27-REVIEW.md, 28-autoscale-load-proof/28-REVIEW.md, 24-helm-chart-core/24-VERIFICATION.md}` — ground truth for the hardening-tail mapping (WR-01, OPER-01/WR-03, gate-tooling WR-01..06, K8S-02)

### Secondary (MEDIUM confidence)
- `https://github.com/ggml-org/llama.cpp/discussions/10230` and related ggml/llama.cpp issues — `GGML_NATIVE`/`-march=native` Docker illegal-instruction failures, consistent across multiple independent issue reports
- `https://github.com/mackron/miniaudio` — whisper.cpp's native decoder format support (WAV/FLAC/MP3/OGG-Vorbis; no native M4A/AAC/Opus), corroborated by whisper.cpp's own ffmpeg-conversion guidance
- MP3 ID3v2 synchsafe-size/sync-word-precedence — corroborated across Mutagen ID3v2 spec docs, Hydrogenaudio Knowledgebase, and an independent parsing write-up (treated as HIGH confidence within Pitfalls research specifically, given multi-source agreement on a spec-level fact)
- ffmpeg CVEs (CVE-2021-38171, CVE-2025-25469, CVE-2026-8461 "PixelSmash") — SentinelOne/Hackers-Arise/JFrog vulnerability writeups, not independently verified against NVD record text
- whisper hallucination on silence/music — corroborated across an arXiv paper, an OpenAI Whisper GitHub discussion, and a practitioner write-up

### Tertiary (LOW-MEDIUM confidence, needs validation)
- CPU realtime-factor (RTF) benchmarks — triangulated from a legacy-hardware community discussion, a third-party Apple M2 blog benchmark, and general "6x real-time" community claims on unspecified hardware; explicitly flagged for empirical re-measurement during Phase 4 against the project's real container CPU allocation
- whisper.cpp JSON output field names (`-oj`/`-ojf`) — the concrete example surfaced was from an adjacent tool (whisper-timestamped), not confirmed as whisper.cpp's own schema verbatim; must be verified against the actual pinned binary before the SEED-001 contract is finalized
- whisper.cpp thread-count-vs-cgroup-CPU-limit OOM behavior — one anecdotal report from the wider Whisper-family Docker ecosystem, not independently reproduced

---
*Research completed: 2026-07-17*
*Ready for roadmap: yes*
