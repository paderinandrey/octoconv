# Project Research Summary

**Project:** OctoConv — v1.8 milestone: "AV Engine (video/ffmpeg)"
**Domain:** Fifth engine class (video/AV processing) added to an existing, hardened, multi-engine async Go file-conversion service (image/libvips, document/LibreOffice, html/chromium, audio/whisper.cpp already shipped)
**Researched:** 2026-07-19
**Confidence:** HIGH (architecture/pitfalls are codebase-derived and cross-checked against live-verified ffmpeg facts; stack/features are MEDIUM-HIGH — well-documented ffmpeg behavior, one genuinely open cross-domain design question)

## Executive Summary

v1.8 adds video processing (container/codec transcode, audio extraction, thumbnail extraction, and video→transcript) as a fifth vertical engine class, following the exact `Converter`/`Registry`/`EngineFor`/queue-per-engine-class pattern already proven four times (image, document, html, audio). The recommended stack is Debian's own `apt-get install ffmpeg` (5.1.9-0+deb12u1, live-verified in-session) on `debian:bookworm-slim` — no static builds, no third-party apt repos, no Go CGo bindings — because it ships `libx264`/native `aac`/`libmp3lame`/`libvpx`/`libwebp` and `ffprobe` for free, with Debian's own CVE-backport security tracking, matching this project's zero-new-attack-surface packaging discipline. All four researchers converge on treating this as a straightforward "fifth slice" that reuses `Converter`/`Registry`, `runCommand` hardened exec, `ProbeDuration`, closed-allowlist opts (`AudioOpts` pattern), and the queue-per-engine-class/KEDA-per-queue shape verbatim — this is the well-trodden 80% of the milestone.

The genuinely new risk surface is video-specific and non-trivial: (1) ffmpeg's decoder/demuxer set is qualitatively larger and more historically vulnerable than any prior engine's parser (a live, disclosed 2026 RCE — CVE-2026-8461 "PixelSmash" — is triggerable by a 50KB crafted file; requires pinning ffmpeg ≥8.1.2 and, separately, `-protocol_whitelist file,crypto` on every invocation to close SSRF/LFI vectors that container-embedded HLS/concat/subtitle references open even with argv-level `"file:"` prefixing already in place); (2) video's decompression-bomb risk is multi-axis (resolution × duration × fps, not resolution alone like images), needing a **new** resolution/fps probe alongside the reusable `EnforceMaxDuration`; (3) a single audio-style RTF-measured timeout constant does not generalize across video's codec/resolution/preset space the way it did for whisper — the opts allowlist and the timeout measurement must be sequenced together, not independently; and (4) the audio engine's "ffmpeg-stage-timeout-is-terminal" classification cannot be copied verbatim, because for video transcode ffmpeg IS the expensive operation (the opposite cost profile from audio's cheap ffmpeg-normalize-then-expensive-whisper split).

One decision is explicitly **not** resolved by this research and must go to roadmapping: how video→transcript is implemented. STACK.md and ARCHITECTURE.md recommend routing video-source/transcript-target pairs onto the *existing* `AudioConverter`/audio queue (extending `Pairs()` only, since ffmpeg-normalize already demuxes any container ffmpeg can decode); PITFALLS.md's Pitfall 8 independently argues for baking a two-stage ffmpeg-extract→whisper-transcribe pipeline *inside* the av-worker as a single job (mirroring `whisper.go`'s existing two-stage-single-job shape), citing schema/reconciler/webhook cost of anything resembling cross-queue job chaining. Both avoid inventing a parent/child job schema; they differ on which existing container/queue owns the whisper stage. This is presented as an explicit open Key Decision below — do not resolve it silently in the roadmap.

## Key Findings

### Recommended Stack

Reuse Debian's packaged `ffmpeg` (7:5.1.9-0+deb12u1) + `ffprobe` (same apt package) on `debian:bookworm-slim`, exactly matching the base image and ffmpeg version the audio engine already depends on for its normalize step — this is not a new dependency, just a new dedicated container for a much larger slice of ffmpeg's functionality. No new Go dependencies: the engine follows the `Converter` shell-out shape via the existing hardened `runCommand` (`internal/convert/exec.go`), never a CGo ffmpeg binding (would violate `CGO_ENABLED=0` project-wide). Live-verified install size is ~416MB additional disk (larger than a prior unverified ~150-200MB estimate) — final `Dockerfile.av-worker` should land around ~500MB, notably lighter than `Dockerfile.audio-worker`'s ~682MB since no whisper.cpp/model needs to be baked in (assuming Option B for video→transcript, see Key Decision below).

**Core technologies:**
- `ffmpeg` 5.1.9-0+deb12u1 (Debian apt package) — video transcode, audio-track extraction, thumbnail extraction, ffmpeg half of video→transcript — same binary already vetted in production for audio's normalize step; ships `--enable-libx264/libx265/libvpx/libwebp/libmp3lame` with zero extra packages
- `ffprobe` (same apt package) — container/stream validation (duration, codec, width/height) before any expensive stage — direct extension of the existing `internal/convert/audioduration.go` pattern
- `libx264` (built into ffmpeg) — H.264 encoding for the mp4 transcode target, CRF single-pass mode (not two-pass, to keep a single bounded timeout)
- Native `aac` encoder (built into ffmpeg, not `libfdk-aac`) — AAC audio, avoids Debian non-free archive entirely
- No new Go dependencies; extends `internal/convert/`'s existing `Converter`/`Registry`/`runCommand` machinery unchanged

### Expected Features

**Must have (table stakes), matches the milestone's own stated target list exactly:**
- Container/codec transcode: mov/avi/mkv/webm → mp4 (H.264/AAC, `+faststart`) — the highest-value, most CPU-expensive operation, needs its own RTF-measured timeout
- mp4 → webm (VP9/Opus) as secondary transcode target, for format-pair symmetry
- Audio extraction: video → mp3/wav/m4a (stream-copy to m4a when source is already AAC; re-encode otherwise)
- Thumbnail/frame extraction at a client-specified or fixed-default timecode (fast input-side `-ss` seek, never output-side)
- Video → transcript (txt/srt/vtt/json) reusing the existing whisper.cpp output contract — implementation path is the one open Key Decision
- Full production-parity wiring: `cmd/av-worker`, `av` asynq queue, fail-closed magic-byte video validation, stage-aware retry classification, ffprobe duration guard, compose + chart + KEDA ScaledObject

**Should have (differentiators, fast-follow not MVP):**
- H.265/HEVC output target — ships free in the same apt package, add once mp4/H.264 baseline is proven
- Auto stream-copy fast path (skip re-encode when source is already target-legal) — big win for RTF/timeout budget, deliberately deferred past MVP so the transcode path has one code path to harden first
- Resolution-capping opt (e.g. `max_height: 720`) — closed enum only, never a raw WxH string

**Defer (v2+ or never):**
- Adaptive bitrate streaming (HLS/DASH) — different product shape entirely, breaks the single-input/single-output-per-job assumption
- In-service video editing (trim/crop/concat/filters/watermarking) — no natural stopping point, direct filtergraph-injection risk
- Live/RTMP streaming ingest — incompatible with the async batch S3-in/S3-out architecture

### Architecture Approach

The av engine is a fifth vertical slice slotting unchanged into the existing `Converter`/`Registry`/`EngineFor` abstraction — new files mirror existing siblings 1:1 (`av.go`↔`libreoffice.go`, `avopts.go`↔`audioopts.go`, `avsniff.go`↔`audiosniff.go`), with `cmd/av-worker`, `Dockerfile.av-worker`, `TypeAVConvert`/`QueueAV`, `isAVTerminal`, and KEDA chart templates all following the audio engine's proven 4-phase build order (foundation → async-contour wiring → containerize+measure → KEDA/Helm parity). The one structural wrinkle: not every "video" pair routes to the new `av` queue — video→transcript pairs are (per the recommended Decision A) registered under `EngineAudio` and processed by the *existing* audio-worker, so `Registry`'s pair-keyed (not source-keyed) lookup must, for the first time, arbitrate between two different converters claiming pairs from the same source-format family, requiring a new pair-disjointness unit test with no prior precedent. Video container sniffing needs two distinct techniques: fixed-offset matchers (mp4/mov `ftyp` box, avi RIFF fourCC) extending `sniff.go`'s existing table, plus a genuinely new bounded-peek EBML/DocType parser for mkv/webm (variable-offset field, no existing codebase precedent for this shape).

**Major components:**
1. `internal/convert/av.go` (new) — `AVConverter`: transcode, audio-extract, thumbnail; `Engine() == EngineAV`, wraps ffmpeg only
2. `internal/convert/avsniff.go` (new) — bounded-peek EBML/DocType parser for mkv/webm disambiguation; `sniff.go` gains fixed-offset mp4/mov/avi matchers
3. `internal/convert/whisper.go` (modified, `Pairs()` only) — extended with video-container × transcript-target pairs if Decision A (audio-queue routing) is chosen
4. `internal/queue/queue.go` + `internal/worker/worker.go` (new symbols mirroring `Audio*`) — `TypeAVConvert`/`QueueAV`/`isAVTerminal`/`HandleAVConvert`
5. `cmd/av-worker/main.go` + `Dockerfile.av-worker` (new) — ffmpeg-only container, no whisper.cpp build stage, much lighter than `Dockerfile.audio-worker`

### Critical Pitfalls

1. **FFmpeg protocol auto-probing (SSRF/LFI via HLS/concat/subtitle-embedded URLs inside file content)** — the existing `"file:"`-prefix argv hardening (IN-01) does not cover protocols referenced *inside* file content once ffmpeg starts demuxing. Avoid with `-protocol_whitelist file,crypto` on every ffmpeg/ffprobe invocation touching client bytes, verified via an offline-canary test (mirrors chromium's HTML-01 canary), landing in the very first phase that adds any ffmpeg/ffprobe call — this is day-one exec-hardening scope, not deferred.
2. **Filtergraph injection (`movie=`/`amovie=` source filters)** — a second, independent SSRF/LFI vector inside `-vf`/`-filter_complex` strings, easy to introduce accidentally via a convenience opt. Avoid by extending the `AudioOpts` closed-allowlist discipline to every video opt — never client-string-to-filtergraph concatenation.
3. **Decoder RCE surface (PixelSmash-class, CVE-2026-8461)** — ffmpeg's decoder set is qualitatively more vulnerable than any prior engine's parser; a 50KB crafted file achieved RCE against comparable services (Jellyfin/Nextcloud) as recently as June 2026. Avoid by pinning ffmpeg ≥8.1.2 (not a floating `apt-get install`) and establishing an ongoing advisory-tracking process (new operational surface for this project — flag as an explicit accepted-risk Key Decision).
4. **Multi-axis decompression bombs** — video's decode cost is width×height×frame_count, not width×height alone; a duration-only guard (naive port of the image engine's dimension-only precedent) misses small-resolution/huge-duration or huge-resolution/small-duration bombs. Ship duration ceiling (`EnforceMaxDuration`, reused as-is) AND a new resolution/fps ceiling together, not duration-only.
5. **RTF-timeout / terminal-classification precedents from audio don't generalize** — video transcode RTF varies by orders of magnitude across codec/resolution/preset (unlike whisper's roughly content-invariant RTF), and ffmpeg IS the expensive operation for transcode (unlike audio's cheap-normalize/expensive-whisper split), so `isAudioTerminal`'s "ffmpeg timeout = terminal" cannot be copied — re-derive per feature (transcode: timeout stays transient; thumbnail/extract: closer to audio's terminal-on-timeout profile), and size `AV_ENGINE_TIMEOUT` against a measured worst-reachable-combination bounded by the opts allowlist, not a single fixture.

## Implications for Roadmap

Based on research, suggested phase structure (mirrors the audio engine's own proven 4-phase shape):

### Phase 1: AV foundation (standalone, not registered)
**Rationale:** Every prior engine class (document, html, audio) built and unit-tested the `Converter` in isolation before touching the live registry/queue — reduces blast radius of a half-wired engine; also the phase where the highest-uncertainty item (EBML sniffer) should start first so surprises surface early.
**Delivers:** `AVConverter` (transcode/audio-extract/thumbnail argv builders), `AVOpts` closed allowlist, `matchMP4`/`matchMOV`/`matchAVI` in `sniff.go`, new `avsniff.go` EBML/DocType parser for mkv/webm, `AV_MAX_DURATION_SECONDS` + new resolution/fps guard, pair-disjointness unit test, `-protocol_whitelist` hardening + offline-canary test on every ffmpeg/ffprobe call.
**Addresses:** transcode/audio-extraction/thumbnail table stakes (FEATURES.md); container sniffing table stakes.
**Avoids:** Pitfall 1 (protocol SSRF), Pitfall 2 (filtergraph injection), Pitfall 4 (multi-axis bombs), Pitfall 7 (sniffer collisions — AVI/WAV RIFF collision, MKV/WebM EBML, MP4-brand disjointness against `m4aBrands`/`heicBrands`).

### Phase 2: Async contour integration
**Rationale:** Mirrors Phase 31's audio wiring — registry/queue/worker/API/reconciler all need the same mechanical extension pattern applied together, verified end-to-end before containerizing.
**Delivers:** `EngineAV` registered, `TypeAVConvert`/`QueueAV`/`isAVTerminal` (feature-specific classification, re-derived not copied — see Pitfall 6), API routing + reconciler engine-switch cases, env-parity sweep across all queue-client-constructing services.
**Uses:** `runCommand` hardened exec (STACK.md), `Registry`/`Converter` pattern (ARCHITECTURE.md).
**Implements:** stage-aware terminal/transient classification, guarded status transitions (existing pattern, extended).

### Phase 3: Containerize + measure
**Rationale:** Timeout/resource sizing must follow, not precede, the opts allowlist closing in Phase 1-2 (Pitfall 5's ordering requirement) — measuring against an undefined opt space reintroduces the exact "single fixture, wrong end of the range" mistake this research flags explicitly.
**Delivers:** `Dockerfile.av-worker` (pinned ffmpeg ≥8.1.2, not floating apt), `cmd/av-worker/main.go`, `scripts/av-transcode-measure.sh` (RTF matrix or explicit narrow-opts NO-GO lever as a documented Key Decision), disk-space/ephemeral-storage guard (new resource axis, no prior precedent), `-threads` wired from `CgroupCPULimit()` + fresh RAM measurement (not copied from audio's `1g`).
**Addresses:** RTF-measured timeout table stakes; disk-space guard (Pitfall 9); cgroup thread/RAM sizing (Pitfall 10).

### Phase 4: KEDA/Helm parity
**Rationale:** Deployment/scaling templates depend on Phase 3's measured `AV_ENGINE_TIMEOUT` (feeds `terminationGracePeriodSeconds`/`scaleDownStabilizationSeconds`) — sequencing this last avoids a second pass if the measured value changes.
**Delivers:** `deployment-av-worker.yaml`/`scaledobject-av.yaml`, values.yaml `avWorker` section, `NewQueueDepthCollector` extension, `scripts/keda-av-loadproof.sh`.
**Uses:** WR-01 triad (existing KEDA pattern), audio's Phase 33 evidence as direct template.

### Phase Ordering Rationale

- Standalone-before-registered (Phase 1→2) directly reuses the audio milestone's own scope-fence discipline and is the established pattern for every prior engine class.
- Opts-allowlist-before-timeout-measurement (Phase 1/2 before Phase 3) is a hard dependency surfaced specifically by PITFALLS.md Pitfall 5 — measuring first and opening opts later silently reintroduces the pitfall.
- Reconciler/webhook/KEDA numeric thresholds (stuck-job timeout, webhook retry window, `scaleDownStabilizationSeconds`) must each be explicitly re-derived from `AV_ENGINE_TIMEOUT` once measured, not copied from audio's constants — flagged repeatedly across PITFALLS.md (Pitfall 12) as an easy-to-miss step because the *routing mechanism* extension is easy to mistake for complete coverage.
- The video→transcript Key Decision (below) should be resolved before Phase 1 work on that specific feature begins, since it determines whether video→transcript needs its own converter/pipeline code or only a `Pairs()` extension on the existing `AudioConverter`.

### Key Decision Required: Video → Transcript Implementation Path (NOT resolved by research — flag for roadmapping)

The four researchers diverged on this. Two mutually exclusive options are on the table, both avoiding a new parent/child job schema:

**Option A — Extend `AudioConverter.Pairs()` to route video sources onto the existing audio queue/worker** (recommended by STACK.md and ARCHITECTURE.md).
- Mechanism: `AudioConverter.Convert()`'s existing two-stage `ffmpeg-normalize → whisper-cli` pipeline (`internal/convert/whisper.go`) is unchanged — `ffmpeg -i <video> -ar 16000 -ac 1 -c:a pcm_s16le` already demuxes audio out of any container ffmpeg can decode, video or not. Only `Pairs()` grows (video-container × transcript-target cross product); `Engine()` stays `EngineAudio`.
- Pros: zero duplication of the ~682MB whisper.cpp+model image layer; reuses the already-measured `AUDIO_ENGINE_TIMEOUT=742s`/`AUDIO_MAX_DURATION_SECONDS` guard verbatim (gated on `job.Engine`, not source format); av-worker stays ffmpeg-only and lightweight (~500MB vs ~900MB+); follows the "one container per engine class" precedent with zero exceptions.
- Cons/residual work: needs a new RTF measurement specifically for video-source jobs (video demux overhead before whisper's dominant cost is unverified); needs an explicit pair-disjointness test between `AVConverter` and `AudioConverter` (first time two converters share a source-format family — no prior precedent); the registry's "later registration wins silently" semantics becomes a real hazard for the first time.

**Option B — Bake ffmpeg-extract-then-whisper-transcribe as two subprocess stages inside a single av-worker job** (recommended by PITFALLS.md Pitfall 8).
- Mechanism: `AVConverter` (in the new av-worker/container) performs both the ffmpeg audio-extraction stage and the whisper-cli transcription stage as one job, mirroring `whisper.go`'s existing two-stage-single-job pattern — single job row, single `AV_ENGINE_TIMEOUT` budget covering both stages, `Engine() == EngineAV`.
- Pros: keeps the whole video→transcript feature inside one engine class/queue conceptually ("video" stays "av"); avoids the cross-converter pair-disjointness collision entirely; avoids routing surprises where a video-source job silently lands on a different queue than other video jobs.
- Cons: duplicates the entire whisper.cpp build stage + pinned model bake-in from `Dockerfile.audio-worker` into `Dockerfile.av-worker` (~400MB extra image weight, ~900MB+ total); creates two independently-updated copies of the same pinned-commit/model discipline that must be kept in sync; couples av-worker's resource/scaling profile (CPU-bound video encode) to whisper's (also CPU-bound) — the two most expensive operations in the system would compete for the same container's CPU ceiling; doubles the RTF-measurement/GO-NO-GO burden for a second image.

**What is NOT on the table (both researchers who addressed it agree):** true cross-queue job chaining (av-worker extracts audio → enqueues a *second*, separate task on the audio queue → some mechanism marks the original job done once the second task completes) is explicitly rejected by both ARCHITECTURE.md and PITFALLS.md — it requires a genuinely new orchestration primitive (parent/child job schema, saga-style intermediate state, new reconciler-routing logic, a unique-lock TTL spanning two engine timeouts) that this codebase has never needed and that neither the schema (`jobs`/single `engine` column/guarded single-row transitions) nor the webhook/reconciler machinery (built around exactly-one-job-per-client-request) currently supports.

**Recommendation for roadmapping:** resolve this as an explicit Key Decision entry (mirroring how audio's Key Decisions 1-3 were resolved) at the start of whichever phase first scopes video→transcript — it blocks downstream design (opts schema, timeout budget, classification, image size/build) for that feature specifically and should not be inherited by default from either research file's framing.

### Research Flags

Phases likely needing deeper research during planning:
- **Video→transcript phase (wherever it lands):** the open Key Decision above must be resolved first; whichever option is chosen has real downstream effects on image size, RTF measurement scope, and classification logic that aren't fully specified here.
- **Container sniffing phase (Phase 1):** the EBML/DocType bounded-peek parser for mkv/webm is genuinely new parsing code (not a lookup-table extension) with no prior codebase precedent — flagged by ARCHITECTURE.md as the single highest-uncertainty item in Phase 1.
- **Containerize/measure phase (Phase 3):** RTF-matrix methodology for video transcode is fundamentally different from audio's single-fixture RTF measurement (codec × resolution × preset axes, not just duration) — needs its own measurement design, not a copy of `audio-rtf-measure.sh`.

Phases with standard patterns (skip research-phase, reuse established shape directly):
- **Async contour integration (Phase 2):** mechanically mirrors Phase 31's audio wiring symbol-for-symbol (`TypeAVConvert`/`QueueAV`/API routing/reconciler switch) — well-documented, established pattern in this codebase.
- **KEDA/Helm parity (Phase 4):** mirrors Phase 33's audio templates and WR-01 triad verbatim, only the numeric constants change.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH for packaging/codec facts (live-verified inside a real container this session); MEDIUM for encode-speed sizing (no first-party benchmark yet — deliberately deferred to a measurement phase, not guessed) |
| Features | MEDIUM-HIGH (ffmpeg CLI behavior well-documented and stable; internal-service scoping judgments are informed synthesis against general-purpose video API baselines, not verified against a specific competitor spec) |
| Architecture | HIGH (codebase-derived; container-format magic-byte facts independently verified against current specs — ISO BMFF, EBML/RFC 8794, Matroska/WebM docs) |
| Pitfalls | MEDIUM-HIGH (ffmpeg security surface cross-checked against current CVE data, including a live June-2026 disclosure; timeout/classification/schema pitfalls verified directly against this repo's own code) |

**Overall confidence:** HIGH — three of four research files are directly grounded in live-session verification (real ffmpeg binary probed, real container-format specs checked) or direct codebase inspection; the one genuine open item (video→transcript path) is explicitly flagged rather than silently resolved, which is itself a confidence-preserving outcome rather than a gap.

### Gaps to Address

- **Video→transcript implementation path** (Option A vs Option B above) — must be resolved as an explicit roadmap/PROJECT.md Key Decision before any phase implementing that feature starts; do not let it default silently to whichever option a later planner happens to read first.
- **RTF/timeout measurement matrix bounds** — depends on which codec/resolution/preset combinations the `AVOpts` allowlist ultimately exposes; the allowlist and the measurement must be designed together (sequencing dependency, not independent tasks) — flagged as a phase-ordering requirement above, not resolved with a number here.
- **Upload-size ceiling for video** — `MAX_UPLOAD_BYTES` is currently one global value enforced before content-type detection even runs; video files are legitimately much larger than other engine classes' typical inputs, and raising the global ceiling weakens DoS posture for all other classes too. ARCHITECTURE.md recommends treating this as an explicit named decision in roadmap/PROJECT.md, not an implicit side effect of picking a video-friendly number.
- **Disk-space/ephemeral-storage guard** — genuinely new resource axis with zero existing precedent in this codebase (no prior engine class needed one); needs explicit sizing during the containerize/measure phase, not assumed-safe by analogy to CPU/RAM guards that already exist.
- **FFmpeg advisory-tracking process** — pinning ffmpeg ≥8.1.2 at ship time is necessary but not sufficient; this project has no existing process for tracking any dependency's ongoing security advisories, and PITFALLS.md recommends flagging this explicitly as accepted risk/tech debt in PROJECT.md rather than silently assuming a one-time pin is durable.

## Sources

### Primary (HIGH confidence)
- Live-verified in-session (2026-07-19) inside a real `debian:bookworm-slim` container: `ffmpeg -version`/`-encoders`/`-hwaccels`/`-muxers`, `apt-cache policy ffmpeg`, `dpkg -L ffmpeg`, `apt-get install --no-install-recommends ffmpeg` dry-run — packaging/codec-availability facts (STACK.md)
- This repository's own code, read directly: `internal/convert/{whisper,audioduration,audiosniff,sniff,dimensions,cgroup,audioopts,exec,convert,converters}.go`, `internal/queue/queue.go`, `internal/worker/worker.go`, `internal/api/handlers.go`, `internal/reconciler/reconciler.go`, `internal/db/migrations/0006_audio_engine.sql`, `cmd/audio-worker/main.go`, `Dockerfile.audio-worker`, `docker-compose.yml`, `deploy/chart/octoconv/*`, `.planning/PROJECT.md` (ARCHITECTURE.md, PITFALLS.md)
- FFmpeg Security (ffmpeg.org/security.html), FFmpeg Protocols Documentation (ffmpeg.org/ffmpeg-protocols.html) — official advisory index and `-protocol_whitelist` reference (PITFALLS.md)
- PixelSmash (CVE-2026-8461) — JFrog disclosure, BleepingComputer patch confirmation — live, current decoder RCE (PITFALLS.md)
- EBML specification (ietf-wg-cellar), RFC 8794, Matroska Basics, WebM Container Guidelines — container-format disambiguation facts (ARCHITECTURE.md)
- packages.debian.org/bookworm/ffmpeg — current bookworm package version confirmation (STACK.md)

### Secondary (MEDIUM confidence)
- FFmpeg Micro Blog, RenderIO Blog, Creatomate — transcode CLI patterns (FEATURES.md)
- Sebastian Aigner — thumbnail seeking benchmark — `-ss` input-vs-output seeking tradeoff, widely corroborated (FEATURES.md, STACK.md)
- Transloadit, Cloudinary video docs, Mux HLS vs DASH — general-purpose video API baseline for table-stakes/anti-feature calibration (FEATURES.md)
- Debian security-tracker mailing-list entries (DSA 5985-1 and related) — CVE backport cadence into ffmpeg 5.1.x (STACK.md)
- vulhub CVE-2017-9993, HLS/m3u8 SSRF writeups — protocol-auto-probe SSRF/LFI mechanics (PITFALLS.md)

### Tertiary (LOW confidence)
- WebSearch-derived ffmpeg upstream release-branch summaries via secondary aggregators (endoflife.date, gyan.dev) — used only to establish upstream vs. Debian-stable version skew, not sourced for technical/flag claims (STACK.md)
- FFmpeg VFR/CFR desync mechanics — general community consensus, not independently spec-verified; flagged for phase-specific validation if video→transcript timestamp accuracy becomes a hard requirement (PITFALLS.md)

---
*Research completed: 2026-07-19*
*Ready for roadmap: yes — with one explicit open Key Decision (video→transcript implementation path) flagged for resolution during roadmapping/planning, not silently defaulted*
