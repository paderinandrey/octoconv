# Pitfalls Research

**Domain:** Adding ffmpeg-based video processing (transcode, audio extraction, thumbnail, video→transcript) as a fifth engine class to OctoConv — an existing hardened, multi-engine async Go conversion service
**Researched:** 2026-07-19
**Confidence:** MEDIUM-HIGH (ffmpeg security surface verified against current CVE data and official docs; timeout/classification/schema pitfalls verified against this repo's own code — `internal/convert/whisper.go`, `internal/convert/audioduration.go`, `internal/convert/audiosniff.go`, `internal/convert/sniff.go`, `internal/convert/dimensions.go`, `internal/convert/cgroup.go`, `internal/convert/exec.go`, `internal/worker/worker.go`)

## Critical Pitfalls

### Pitfall 1: FFmpeg auto-probes protocols embedded IN file content — `"file:" + path` on argv is necessary but not sufficient

**What goes wrong:**
The audio engine's `ffmpegNormalizeArgs` (`internal/convert/whisper.go:163-173`) prefixes the input path with `"file:"` so the **argv element itself** can never be reinterpreted as a protocol/URL specifier. This closes IN-01 (argv-level injection) but does **nothing** about protocols referenced **inside the file's own content** once ffmpeg starts demuxing it: a crafted HLS `.m3u8` playlist, an MP4/MOV edit-list or external-data-reference box, a WebVTT/SRT subtitle track with an `http://` reference, or the `concat:` protocol chained through a subtitle/playlist entry can all make ffmpeg open a second, attacker-chosen resource — SSRF against internal services, or arbitrary local file read (`file://` re-read of e.g. `/etc/passwd` or another job's workDir). This is a well-documented, still-current class of FFmpeg vulnerability (CVE-2016-1897/CVE-2017-9993 and their HLS-m3u8 variants), not a hypothetical.

**Why it happens:**
The audio engine never needed this defense because its only untrusted input format was a small, already-sniffed audio container fed straight to a **local** ffmpeg normalize step with no playlist/streaming demuxers in the accepted format set (mp3/wav/m4a/ogg). Video source formats (mov/avi/mkv/webm and friends) route through much richer demuxers, some of which (HLS, concat, several subtitle formats) are inherently protocol-aware. Copying the audio engine's `"file:"`-prefix-only pattern verbatim silently under-protects the av engine.

**How to avoid:**
Pass `-protocol_whitelist file,crypto` (extend only if a specific accepted format genuinely needs another protocol, document why) on **every** ffmpeg/ffprobe invocation that touches client-controlled bytes — inputs and any subtitle/attachment streams. Do not rely on the demuxer's own `concat`-demuxer `safe=1` default (present since FFmpeg 4.x) as sufficient — it only restricts the concat **demuxer**'s own directive parsing, not the protocol layer generally, and does not cover HLS or subtitle-embedded URLs. Verify with a live-canary test mirroring the chromium engine's offline-rendering canary (`v1.3 Phase 15, HTML-01`): craft an m3u8/concat/subtitle-with-URL fixture and confirm zero outbound network connections and zero out-of-workDir file reads.

**Warning signs:**
Any ffmpeg/ffprobe invocation in the av converter that omits `-protocol_whitelist`; any test suite that only covers "normal" video files (no adversarial-container fixtures); code review that treats the existing `"file:"`-prefix precedent as already covering this class of risk.

**Phase to address:**
The phase that introduces the first `ffmpeg`/`ffprobe` invocation for the av engine (mirrors where `-protocol_whitelist` and the offline-canary test should land before any transcode/thumbnail/extract feature ships) — do not defer to a later hardening phase, this is day-one exec-hardening scope, same tier as `internal/convert/exec.go`'s process-group kill.

---

### Pitfall 2: FFmpeg filtergraphs (`movie=`/`amovie=`/`avsrc=`) are a second, independent SSRF/LFI vector — never accept a raw client filter string

**What goes wrong:**
Even with `-protocol_whitelist` locked down on `-i`, ffmpeg's `-vf`/`-af`/`-filter_complex` syntax includes source filters (`movie=`, `amovie=`) that open an **additional, arbitrary** file or URL from inside the filtergraph string itself, independent of the main input protocol whitelist enforcement path. If any feature (e.g. a "watermark" or "overlay" option, or a future thumbnail/transcode opt that concatenates a client value into a filter string) lets client-supplied text reach a filtergraph argv element, this is a direct SSRF/LFI/RCE-adjacent primitive — worse than the `-i` vector because it is easy to introduce accidentally while building convenience opts.

**Why it happens:**
The existing `AudioOpts`/`DocOpts`/`HTMLOpts` pattern (`internal/convert/audioopts.go`, `opts.go`) already defends against string-concatenation-into-argv for the *known* opt shapes (language codes, filter suffixes) via closed allowlists. Video naturally invites "power user" opts (custom filter strings, arbitrary crop/scale expressions, watermark text) that feel like reasonable feature requests but, unlike a language code, have no small closed enum — the temptation to accept a semi-free-form string ("just the timecode, or a simple expression") is exactly how this class of bug gets introduced.

**How to avoid:**
Extend the same `checkStrictObject` + closed-allowlist discipline already proven for `AudioOpts` (`internal/convert/audioopts.go:9-27`, `:46-49`): every video opt (timecode, target resolution/codec, thumbnail format) must be a typed, range/enum-validated field that the converter maps to a **server-constructed** argv slice — never a client string interpolated into `-vf`/`-filter_complex`. If cropping/scaling/overlay is in scope, express it as discrete numeric fields (width, height, x, y) that get formatted server-side into a fixed filter template, never accept the filter expression itself.

**Warning signs:**
Any opt field typed as a free-form string that ends up substring-concatenated into a `-vf`/`-filter_complex` argv element; any code path that does `fmt.Sprintf` to build a filter string from more than one client-supplied numeric field without range validation first.

**Phase to address:**
The phase that defines `VideoOpts`/`ThumbnailOpts` (mirrors `OPTS-01/02` from v1.4 Phase 14) — the allowlist-closed-struct pattern must be locked in before any opt reaches argv, and should have its own injection test (mirrors the PDF/A `FilterOptions` injection test).

---

### Pitfall 3: FFmpeg decoders are themselves an active RCE/DoS attack surface — hardened exec (timeout + process-group kill) does not prevent exploitation, only limits blast radius

**What goes wrong:**
Unlike libvips/LibreOffice/chromium/whisper.cpp (all comparatively narrow, more heavily audited parsers for this project's purposes), ffmpeg bundles dozens of historically-vulnerable video/audio decoders. A live, current example: **CVE-2026-8461 ("PixelSmash")**, disclosed June 2026 — a heap out-of-bounds write in the MagicYUV decoder triggerable by a **50 KB** crafted AVI/MKV/MOV file, demonstrated to achieve full RCE against Jellyfin and Nextcloud via nothing more than an upload-triggered library scan/thumbnail generation — exactly this project's threat model (an internal service uploads an attacker-influenced or attacker-controlled file, the worker decodes it). `runCommand`'s `Setpgid` + `SIGKILL`-on-timeout (`internal/convert/exec.go`) protects against a process that *hangs*; it does nothing to prevent a heap overflow from corrupting memory and achieving code execution **before** the timeout ever fires, and does nothing to prevent memory-corruption bugs that crash the whole ffmpeg process (a crash is not caught by timeout logic at all — it is a normal non-zero-exit `runCommand` error path).

**Why it happens:**
The other four engine classes' threat models were dominated by resource-exhaustion (decompression bombs) and structural/format confusion, which magic-bytes sniffing + declared-dimension ceilings adequately cover. FFmpeg's decoder surface is qualitatively different: memory-safety bugs in C decoders reachable straight from file bytes, independent of declared dimensions being "sane." A pixel-ceiling check (Pitfall 4 below) does not protect against PixelSmash-class bugs at all — the crafted file can declare small, plausible dimensions.

**How to avoid:** Pin the exact ffmpeg build/version in `Dockerfile.av-worker` (mirrors the whisper.cpp commit-hash pin in `Dockerfile.audio-worker:29-33`, not a floating `apt-get install ffmpeg`), require `>= 8.1.2` (the PixelSmash-fixed release) or later at first ship, and establish a process for tracking `https://ffmpeg.org/security.html` / FFmpeg release notes going forward (this project has no such process for any dependency today — worth flagging as a new operational surface, not just a one-time pin). Keep `USER nobody` (already the convention) and treat it as blast-radius reduction, not prevention. Consider (as a Phase-flagged future hardening item, not necessarily v1.8 scope) `--cap-drop=ALL`/`no-new-privileges`/seccomp on the av-worker container specifically, since it is the first engine whose decode surface has a demonstrated RCE track record this recent.

**Warning signs:**
`Dockerfile.av-worker` installing ffmpeg via a floating Debian package version instead of a pinned build; no documented process/reminder for periodically checking ffmpeg security advisories; treating magic-bytes validation + dimension ceilings as "the video input is now safe to decode."

**Phase to address:**
The phase that writes `Dockerfile.av-worker` — pin the version there, same tier of decision as the whisper.cpp commit-pin in Phase 32. Flag the ongoing-advisory-tracking gap as an explicit accepted-risk/tech-debt entry in `PROJECT.md`'s Key Decisions, matching the project's existing honesty pattern (e.g. the `file://` residual-risk entry for chromium).

---

### Pitfall 4: Video decompression bombs are multi-dimensional — pixel ceiling alone (the image-engine precedent) is insufficient

**What goes wrong:**
`internal/convert/dimensions.go`'s `HasDimensionLimit`/`Dimensions` pattern rejects a declared width×height above `MAX_IMAGE_PIXELS` for the five *image* formats. Naively porting "declared width×height ceiling" to video misses that video's actual decode cost is **width × height × frame_count** (equivalently, `width × height × duration × fps`), and video containers can declare a tiny, plausible frame size while declaring an enormous duration or frame rate, or vice versa — a 500×500 video declared at 1,000,000 fps or 100 hours duration is just as much a resource-exhaustion bomb as an oversized single frame, and is not caught by a frame-dimension-only check. Additionally, thumbnail generation must not decode the entire stream to reach a target timecode — a naive `-i input -vf "select=..."` approach without seeking can be forced into full-stream decode by a crafted file even for a "just grab one frame" job.

**Why it happens:**
The image engine's threat model (one static raster, one width/height pair) does not have a duration or frame-count axis at all, so the existing precedent's mental model ("check declared dimensions before touching pixels") only covers one of video's two multiplicative bomb axes. It is easy to port the *shape* of the check (peek bytes, parse a declared field, reject over ceiling) without recognizing a second field must also be checked.

**How to avoid:**
Reuse the `EnforceMaxDuration`/`ProbeDuration` pattern already built for audio (`internal/convert/audioduration.go`) verbatim for video's duration axis (it is already format-agnostic — ffprobe, not a per-format parser). Add a **separate** resolution/frame-rate ceiling check via a bounded, fail-closed `ffprobe`-based (or format-specific, mirroring `dimensions.go`'s in-process parsers if avoiding a second subprocess call is preferred) width/height/fps probe *before* any full-frame decode. Enforce both axes independently — a file must pass both the duration ceiling and the resolution ceiling — and enforce the *product* is bounded too (a file just under both individual ceilings can still multiply to an enormous total decode cost). For thumbnail jobs specifically, seek with `-ss <timecode> -i <path>` (input-side seeking, decode-light) rather than `-i <path> -ss <timecode>` (output-side seeking, which decodes from the start) — this is also the correct pattern for legitimate cheap thumbnails, not just a security concern.

**Warning signs:**
A duration guard with no accompanying resolution/fps guard (or vice versa); a thumbnail converter invocation with `-ss` placed after `-i` in argv; no test fixture exercising "small resolution, huge duration" or "small duration, huge resolution" as a rejected case.

**Phase to address:**
The phase that adds fail-closed content validation for the av engine (mirrors `VALID-03`/Phase 7's image-dimension-bomb closure) — should ship duration ceiling AND resolution/fps ceiling together, not duration-only (the easier-to-port half of the pattern) with resolution deferred.

---

### Pitfall 5: A single RTF-measured timeout (the audio precedent) does not generalize across video codec/resolution combinations

**What goes wrong:**
`AUDIO_ENGINE_TIMEOUT=742s` was derived from a single measured p95 RTF (`scripts/audio-rtf-measure.sh`) because whisper.cpp's RTF is comparatively stable across audio content (same model, same normalize step, speech-content-dependent but not wildly so). Video transcode RTF varies by **orders of magnitude** across the encoder axis alone: `-preset ultrafast` vs `-preset veryslow` on x264 can differ 10-20x; H.264→H.265/AV1 encode is dramatically slower per pixel than H.264→H.264; 4K source is ~4x the pixel-work of 1080p at the same duration; hardware-accelerated encode (not assumed available in this container-resource-limited, `--cpus 2`-precedent deployment) vs software encode differs another order of magnitude. A single measured constant (copying the audio pattern literally: "run once, take p95, done") will be wrong for most of the actual (codec × resolution × preset) space — either rejecting legitimate heavy jobs as timed-out-terminal, or, if tuned generously enough to cover the worst case, leaving a timeout so large that a cheap malicious job (Pitfall 4) can occupy a worker slot for most of that window before any ceiling check catches it, and delaying KEDA scale-to-zero for the av queue on every job.

**Why it happens:**
The audio engine's RTF-measurement methodology (fixture generation, p95-over-N-runs, GO/NO-GO gate) is the most recent, most concrete, most copyable precedent in the codebase for "how do we size ENGINE_TIMEOUT for an expensive operation" — but its underlying assumption (RTF is roughly content-invariant for a fixed model+hardware) does not hold for video encode, where RTF is a function of *client-chosen* codec/resolution/preset options, not just content.

**How to avoid:**
Do not port `audio-rtf-measure.sh`'s single-fixture methodology unchanged. Either (a) measure an RTF **matrix** across the actual accepted (source-codec, target-codec, resolution-tier, preset) combinations the av engine will expose as opts, and derive `AV_ENGINE_TIMEOUT` from the **worst** cell actually reachable through the opts allowlist (closing Pitfall 2's allowlist and this measurement together — the allowlist defines the timeout's input domain), or (b) constrain the exposed opts to a narrow, cheap preset/resolution/codec set for v1.8 (mirroring the `AUDIO_MAX_DURATION_SECONDS` NO-GO lever already exercised once in this project — Phase 32 cut max duration 14400→1800s rather than inflating the timeout) and explicitly defer broader codec/preset choice to a future milestone. Whichever is chosen, document the choice as a Key Decision the same way Phase 32's NO-GO lever was documented — this is a recurring, expected design fork, not a one-off surprise.

**Warning signs:**
A single fixture (one codec, one resolution) feeding the RTF measurement script; `AV_ENGINE_TIMEOUT` treated as a flat constant with no accompanying opt-space ceiling (max resolution, closed codec/preset enum) that bounds what that constant actually has to cover.

**Phase to address:**
The phase that measures/sets `AV_ENGINE_TIMEOUT` (mirrors Phase 32) — must be preceded by (not follow) the phase that closes `VideoOpts`'s codec/resolution/preset allowlist, since the allowlist defines the measurement matrix's bounds. Sequencing these in the wrong order (measure timeout first, open up opts later) reintroduces this pitfall retroactively.

---

### Pitfall 6: The audio engine's "ffmpeg-stage timeout = terminal" classification (Key Decision 1) cannot be copied to video transcode — ffmpeg IS the expensive operation, not a cheap normalize step

**What goes wrong:**
`isAudioTerminal` (`internal/worker/worker.go:255-...`) encodes Key Decision 1: an `"audio: ffmpeg:"`-prefixed failure or timeout is classified **terminal**, because in the audio pipeline ffmpeg is only a fast (16kHz mono PCM) normalize step — a timeout there signals malformed/adversarial input, not legitimate expensive work, and the *actually* expensive stage (whisper transcription) is what gets the transient/retryable classification. For video **transcode**, ffmpeg is not a cheap pre-processing step — it **is** the expensive operation the whole pipeline exists to run. Copying `isAudioTerminal`'s shape onto an `isAVTerminal` that treats any ffmpeg-stage timeout as terminal would misclassify a legitimate heavy 4K/slow-preset transcode that simply needs more wall-clock as a permanent, non-retryable failure — the opposite of the desired behavior, and inconsistent with how the image/document engines already treat their own core-operation timeout as transient (mirrors `internal/worker/worker.go`'s existing image-engine-timeout-stays-transient precedent, which Key Decision 1 explicitly diverged from *because* audio's ffmpeg stage is cheap — that precondition does not hold for video).

**Why it happens:**
Key Decision 1 is the most recent, most heavily-narrated classification precedent in the codebase (extensive doc comments, a dedicated STATE.md Key Decision entry), making it the most likely thing to be copied by pattern-matching on "audio added a stage-aware classifier, video should too" without re-deriving *why* the audio split landed where it did.

**How to avoid:**
Re-derive the classification per **feature**, not per **stage-name**, because the av engine bundles four different features (transcode, audio-extract, thumbnail, video→transcript) with different cost profiles on the same `ffmpeg` binary:
- Transcode: ffmpeg timeout on legitimate input → **transient** (mirrors image/document's own-core-op-timeout-stays-transient precedent), bounded by its own max-retry budget; only a *deterministic* ffmpeg failure (bad exit before any real work, e.g. unsupported codec/corrupt container caught early) is terminal.
- Thumbnail/audio-extract: much closer to audio's normalize-step cost profile (fast, bounded seek+short-decode) — a timeout here is more plausibly malformed input → terminal is defensible, but should still be its own explicit decision, not inherited by string-prefix accident.
- Video→transcript: whichever architecture is chosen (Pitfall 8) determines whether this is one classifier or two chained ones.

Write the classifier's doc comment with the same "why" discipline as `isAudioTerminal`'s (`internal/worker/worker.go`) — explicitly state the cost-profile assumption for each ffmpeg invocation, so a future engine addition doesn't inherit an unstated assumption a third time.

**Warning signs:**
An `isAVTerminal` implementation whose branch structure is a search-and-replace of `isAudioTerminal` with `"audio: ffmpeg:"` swapped for `"av: ffmpeg:"` and no accompanying re-justification comment; a single classifier function covering all four av features without acknowledging their different cost profiles.

**Phase to address:**
The phase that wires the av engine into the async worker/queue contour (mirrors Phase 31's audio wiring) — this classification decision should get its own explicit Key Decision entry in `PROJECT.md`, same as Key Decision 1 did, precisely because it is expected to diverge from the audio precedent rather than copy it.

---

### Pitfall 7: The three new container sniffers (MP4-video, MKV/WebM, AVI) each collide with an existing signature or with each other — cannot be written independently

**What goes wrong:**
Three separate, well-documented signature collisions are all in play simultaneously, and none of them can be resolved by writing a new sniffer in isolation:

1. **AVI vs WAV (RIFF collision):** `matchWAV` (`internal/convert/audiosniff.go:31-33`) matches `RIFF....WAVE` at bytes 0-4/8-12. AVI is *also* `RIFF....` with a form-type of `AVI ` at the identical byte offset — the only difference is the 4-byte form-type value. A naive AVI matcher that checks `RIFF` at offset 0 without also checking bytes 8-12 (or that is registered/consulted in a way that races `matchWAV`) risks either (a) a WAV file misdetecting as AVI (silently routed to the wrong engine class), or (b) — more dangerous — an AVI *video* file misdetecting as WAV *audio*, at which point `SniffAudio` hands it to `AudioConverter`, whose ffmpeg normalize step can often still successfully decode the audio track out of an AVI container, producing a "successful" transcription of a video file's audio with no error anywhere, silently defeating the point of format-pair validation.
2. **MKV vs WebM (shared EBML magic):** both start with the identical 4-byte EBML magic `0x1A45DFA3` — WebM is a *profile* of Matroska, not a distinct container format at the magic-bytes level. They are distinguished only by the `DocType` element (`"webm"` vs `"matroska"`), a variable-length EBML string element at a **variable offset** inside the EBML header (not a fixed offset like ISOBMFF's `ftyp`, unlike every existing sniffer in this codebase — `sniff.go`'s and `audiosniff.go`'s signatures all assume fixed-offset fields). A fixed-offset check (the pattern every existing matcher uses) structurally cannot distinguish them; a bounded EBML-element walker is required (mirrors `dimensions.go`'s `walkBoxes` ISOBMFF-box-walker precedent in *shape*, but must decode EBML's variable-length ID/size encoding, not ISOBMFF's fixed 8/16-byte box headers).
3. **MP4-video brand explosion, and its collision with `m4aBrands`/`heicBrands`:** the existing `m4aBrands` allowlist (`internal/convert/audiosniff.go:16-27`) deliberately *excludes* the common video major brands (`isom`, `mp42`, etc.) with an explicit comment noting they are "the most common major brand of ordinary MP4 *video* files" — i.e. the audio engine's authors already anticipated this exact collision and fenced it off, but only from the audio side. Building the video-side MP4 brand allowlist requires (a) a much larger table than `m4aBrands`'/`heicBrands`' 2/6 entries (real-world MP4 encoders emit dozens of major brands: `isom`, `mp41`, `mp42`, `avc1`, `iso2`/`iso4`/`iso5`/`iso6`, `M4V `, `3gp*`, `qt  ` is actually MOV not MP4 and must NOT be folded in, `dash`, etc.), and (b) an explicit disjointness check against `m4aBrands` and `heicBrands` — the three tables share the identical `ftyp`+brand box structure (`matchM4A`/`matchHEIC` in `audiosniff.go`/`sniff.go`) and **first-match ordering** in whichever combined dispatch table results is a latent bug waiting to happen if the tables are ever allowed to overlap.

**Why it happens:**
Every existing sniffer in this codebase was written to detect one narrow, disjoint format family at a time, each with its own comment explicitly reasoning about the *one* known collision relevant to that family at the time it was written (`matchM4A`'s comment about MP4/MOV, `matchHEIC`'s comment about the same). Adding video reintroduces three MORE collisions against those same already-narrow tables plus a genuinely new one (EBML), and none of the existing per-format doc comments were written anticipating a *fifth* format family showing up later — the disjointness reasoning has to be redone holistically, not additively.

**How to avoid:**
Before writing any new matcher, enumerate every existing ISOBMFF-family matcher (`matchM4A`, `matchHEIC`, and the eventual `matchMP4Video`) and their brand tables side-by-side and assert (ideally via a unit test, not just a comment) that the tables are pairwise disjoint. For AVI, the matcher **must** check the form-type field (bytes 8-12) against `AVI ` and be structured so it can never be reached for a buffer `matchWAV` would also accept — either by checking form-type equality (not `RIFF`-prefix alone) or by placing both matchers in one shared dispatch table with explicit tests asserting no input matches both. For MKV/WebM, build a bounded (fixed max-peek-window, fail-closed on overrun — mirroring `dimPeekLen`'s and `mp3PeekLen`'s discipline) EBML header walker that reads the `DocType` element; if distinguishing webm from mkv is not actually load-bearing for routing (i.e. ffmpeg handles both identically for every accepted target format), consider detecting a single `"matroska"` family instead of splitting them — that is a legitimate scope-reduction, not a shortcut, if the roadmap doesn't need the distinction.

**Warning signs:**
A new AVI/MP4-video/MKV matcher added to `sniff.go` or a new `videosniff.go` without a cross-reference comment against the existing audio/image tables; no unit test asserting a real-world WAV fixture does NOT match the new AVI matcher and vice versa; no test asserting `m4aBrands`/`heicBrands`/the new video-brand table are disjoint.

**Phase to address:**
The phase that adds fail-closed magic-bytes validation for video containers — should explicitly re-open and cross-test `internal/convert/sniff.go` and `internal/convert/audiosniff.go` together, not treat video sniffing as an independent addition (mirrors how `SniffAudio` was deliberately kept separate from `Sniff` in Phase 30 rather than folded in, because of the ID3v2 variable-offset problem — the same "this format family doesn't fit the existing fixed-window assumption" reasoning applies to MKV/WebM now).

---

## Moderate Pitfalls

### Pitfall 8: Video→transcript spans two engine classes with no chained-job schema — the milestone context flags this as unresolved for a reason

**What goes wrong:**
`PROJECT.md`'s own milestone context explicitly defers this: "способ реализации видео→транскрипт (whisper внутри av-контейнера vs межочередная цепочка) решается на research/planning." Two implementation shapes are on the table, and each has a distinct pitfall: (a) **bake whisper.cpp into the av-worker image too** — duplicates the entire whisper.cpp build stage + model bake-in from `Dockerfile.audio-worker` into `Dockerfile.av-worker`, doubling image size/build time and creating two independently-updated copies of the same pinned-commit/pinned-model discipline that must be kept in sync; or (b) **chain two real jobs across two queues** (av extracts audio → enqueues an audio-engine job → audio-worker transcribes) — but the current schema (`jobs`, single `engine` column, guarded single-row status transitions per `internal/jobs/repo.go`) has no concept of a parent/child job relationship, no "job B depends on job A's output" linkage, and the webhook/reconciler machinery is built around exactly-one-job-per-client-request. Retrofitting a chain means either a synthetic client-invisible second job (needs new schema + new reconciler-routing rules + new webhook-suppression-until-final-stage logic) or collapsing it into a single job whose `engine` column is ambiguous (av? audio? a new composite value?).

**Why it happens:**
Every job class so far (image, document, html, audio) is a single (source-format, target-format) → single-engine mapping. Video→transcript is the first feature in this project's history that inherently spans two of the existing engine classes' own tooling (ffmpeg extraction + whisper transcription) rather than needing one new tool.

**How to avoid:**
Resolve this explicitly as a Key Decision before implementation, the same way Key Decision 1/2/3 were resolved for audio. Given the schema and reconciler cost of option (b), option (a) — bake ffmpeg-extraction-then-whisper-transcribe as two subprocess stages *inside a single av-worker job*, mirroring `whisper.go`'s existing two-stage-single-job pattern exactly (extract with ffmpeg, transcribe with whisper-cli, single job row, single `AV_ENGINE_TIMEOUT` budget covering both stages) — is very likely the lower-risk path and should be the default unless a concrete reason emerges to prefer cross-queue chaining. If chosen, this duplicates whisper.cpp's toolchain into the av image; treat that duplication as an accepted, documented tradeoff (mirrors the project's existing "duplicate what you must, don't over-engineer a shared-library abstraction for a second consumer" bias visible in how `Dockerfile.document-worker`/`Dockerfile.chromium-worker`/`Dockerfile.audio-worker` are each independently self-contained rather than sharing a base image).

**Warning signs:**
Implementation starting on video→transcript before this decision is written down; a design that introduces a new `job_id` parent/child column "just for this one feature" without weighing it against the single-job two-stage alternative.

**Phase to address:**
Must be the FIRST decision made in whichever phase scopes video→transcript — blocks all downstream design (opts schema, timeout budget, classification) for that feature specifically.

---

### Pitfall 9: No disk-space guard exists anywhere in the codebase today — video's per-job storage footprint is qualitatively larger than every prior engine's

**What goes wrong:**
`MAX_UPLOAD_BYTES` bounds a single client upload, but nothing in the codebase today bounds or even observes **workDir disk usage** during a job (input + any intermediate scratch file + output, simultaneously present on disk per the `whisper.go`/`libreoffice.go`/`chromium.go` convention of writing to `filepath.Dir(outPath)`). Audio's intermediate file (`norm.wav`, 16kHz mono PCM) is tiny regardless of input size; video transcode's intermediate/output files can be gigabytes, and a burst of concurrent large-video jobs (or a single very large one) can exhaust the worker container's writable-layer/ephemeral-storage budget — crashing the worker process (and, in the k8s deployment per `deploy/chart/octoconv`, potentially triggering node-level `ephemeral-storage` eviction pressure affecting other pods on the same node, not just this worker).

**Why it happens:**
Disk was never the bottleneck resource for any of the first four engine classes (image/document/html conversions in this project's use case are all comparatively small files); CPU/RAM/timeout have all been carefully measured (RTF gates, cgroup CPU detection) but disk has simply never come up as a constraint worth measuring, so there is no existing pattern to even copy incorrectly — this is a genuinely new resource axis.

**How to avoid:**
Add an explicit disk-space check (either a declared-size ceiling analogous to `MAX_UPLOAD_BYTES` scoped tighter for video, or a live `syscall.Statfs`-based check on the workDir's filesystem before starting a job) and size the av-worker's `docker-compose.yml`/k8s ephemeral-storage limit deliberately (mirroring the audio-worker's explicit `--cpus 2` / `1g` RAM precedent, not left at Docker/k8s defaults). Budget for at least input + output + any intermediate simultaneously (worse for multi-target features like transcode-and-thumbnail-in-one-job, if that combination is ever offered).

**Warning signs:**
`Dockerfile.av-worker`/its compose or chart entry with no explicit ephemeral-storage/disk limit; no test fixture exercising a large-file scenario; `AV_MAX_UPLOAD_BYTES` (or equivalent) left at the same default as image/document despite video files being routinely 10-100x larger.

**Phase to address:**
The phase that containerizes the av-worker (mirrors Phase 32's audio containerization + RTF measurement) — disk should be measured/bounded alongside CPU/RAM in that same phase, not deferred.

---

### Pitfall 10: FFmpeg's own thread/RAM sizing under cgroup limits will repeat whisper-cli's pre-Phase-32 bug unless explicitly wired

**What goes wrong:**
`internal/convert/cgroup.go`'s existence is a direct result of discovering that whisper-cli defaults its `-t`/thread count to the **host's** full core count, causing CFS throttling under a `--cpus 2` container limit rather than respecting the container's real budget. FFmpeg has the exact same failure mode: its own `-threads`/filter-thread auto-detection also defaults to host core count unless explicitly overridden, and its encoders' RAM usage (particularly x264/x265 with lookahead buffers at higher `-preset` quality settings, or any multi-pass encode) scales with resolution and preset in ways not proportional to whisper.cpp's RAM profile at all — copying audio's proven `1g` RAM limit to the av-worker without re-measurement is very likely insufficient for even a modest 1080p transcode.

**Why it happens:**
This is precisely the kind of "looks like a solved problem because we solved it once" trap — `CgroupCPULimit()` already exists and is directly reusable for ffmpeg's `-threads` flag, which makes it easy to assume the *thread* half of this pitfall is automatically closed by reuse, while the *RAM* half (a genuinely different resource-scaling curve for encoders vs. a fixed-size transformer model) gets silently assumed-solved by analogy instead of re-measured.

**How to avoid:**
Reuse `CgroupCPULimit()` (`internal/convert/cgroup.go`) for ffmpeg's `-threads` flag — this part **is** safe to copy verbatim, it's already container-resource-limit-aware and format-agnostic. Do NOT reuse the `1g`/`--cpus 2` RAM/CPU envelope itself without a dedicated measurement pass (mirrors `audio-rtf-measure.sh`'s "peak memory... feeds the WORKER_CONCURRENCY decision" methodology) across the actual resolution/preset ceiling chosen in Pitfall 5's opts allowlist.

**Warning signs:**
`ffmpegNormalizeArgs`-equivalent argv construction for video that omits an explicit `-threads` flag (relying on ffmpeg's own host-core-count default); `docker-compose.yml`'s av-worker service copy-pasting `cpus: "2.0"` / `memory: 1g` from the audio-worker entry without a measurement citation.

**Phase to address:**
Same phase as Pitfall 9 (av-worker containerization/RTF measurement) — CPU-thread wiring and RAM ceiling should both be closed there, with the thread half reusing existing code and the RAM half getting fresh measurement.

---

### Pitfall 11: Thumbnail of a corrupt/truncated video, or a client-supplied out-of-range timecode, needs the same "exit 0 but empty/wrong output" guard as `validateAudioOutput` — plus explicit bounds-checking against probed duration

**What goes wrong:**
A thumbnail request for a timecode past the file's actual (post-truncation) end, or inside a corrupted/keyframe-less region, is a very plausible legitimate-looking request that ffmpeg can exit 0 on while producing an empty or garbage output file — the exact class of failure `validateAudioOutput` (`internal/convert/whisper.go:297-311`) already exists to catch for the audio engine ("exit 0 but empty/missing output"), but for thumbnails the failure can *also* manifest as a **structurally valid but semantically wrong** image (e.g. ffmpeg silently clamping to the last available frame instead of erroring) rather than an empty file, which a size-only check would miss entirely.

**Why it happens:**
`validateAudioOutput`'s `size>0` check is the most recent, directly-adjacent precedent, and porting it verbatim (as a starting point) is correct but incomplete for thumbnails specifically — video adds a failure mode (silent-clamp-to-nearest-frame) that has no audio-engine analog, because audio transcription has no equivalent "seek to a point that doesn't exist" client input at all.

**How to avoid:**
Port `validateAudioOutput`'s size>0 check as a floor, and additionally: (a) validate the client-supplied timecode against the file's `ProbeDuration`-reported length (reusing `internal/convert/audioduration.go`'s ffprobe pattern) **before** invoking the thumbnail extraction, rejecting out-of-range requests with a clear client-facing error rather than letting ffmpeg silently clamp; (b) treat the extracted thumbnail through the same magic-bytes `Sniff` (`internal/convert/sniff.go`) used for uploads, confirming the output is actually a valid image of the requested target format before marking the job done — this also doubles as defense against a corrupted source producing a corrupted (but non-empty) thumbnail.

**Warning signs:**
A thumbnail converter with no pre-flight duration check on the requested timecode; no post-extraction `Sniff` validation on the output file; test coverage only exercising in-range timecodes on well-formed fixtures.

**Phase to address:**
The phase implementing the thumbnail feature — should ship the duration bounds-check and output re-validation together with the feature itself, not as a follow-up hardening pass (mirrors how `validateAudioOutput` shipped in the same phase as the whisper pipeline itself, not deferred).

---

### Pitfall 12: Progress-less long transcode jobs strain both the reconciler's stuck-job assumptions and client webhook-wait expectations

**What goes wrong:**
The reconciler (Phase 3/6/`RECON-04`/`RECON-05`) recovers jobs stuck in `queued`/`active` past some elapsed-time threshold, and the webhook system fires exactly once, at `done`/`failed`. Both were tuned against this project's prior engine classes' duration profiles (image: sub-second to seconds; document: up to ~178s observed in Phase 28's KEDA downscale test; audio: 742s measured ceiling). A legitimate video transcode can run **longer** than any prior engine's `*_ENGINE_TIMEOUT`, with **zero** intermediate signal — no `job_events` row is appended between `active` and the final `done`/`failed` transition for any existing engine, so an operator (or an over-eager reconciler threshold copied from a shorter-duration engine) cannot distinguish "still legitimately transcoding a large 4K file" from "actually hung." A reconciler stuck-job threshold sized for audio's ~742s-class jobs, applied unchanged to an av queue whose legitimate jobs can run considerably longer (depending on where Pitfall 5's ceiling lands), will falsely reclaim/retry genuinely-in-progress video jobs.

**Why it happens:**
The reconciler's stuck-job threshold and its per-engine routing (`jobs.engine`-based, per `DOC-09`'s and audio's reconciler-routing precedent) are the right shape to extend, but the *threshold value itself* is not obviously engine-generic the way the routing logic is — it is easy to add `av` to the routing switch (Pitfall 13) while leaving the numeric threshold untouched because "the routing mechanism already handles new engines."

**How to avoid:**
Size the reconciler's av-queue stuck-job threshold explicitly against `AV_ENGINE_TIMEOUT` (once measured per Pitfall 5), the same derivation discipline already used for `AudioUniqueTTL` (`> worst-case 2450s` per the T-03-10 precedent cited in `PROJECT.md`'s Phase 31 entry) — never left at whatever the image/document/audio-derived default happens to be. If client-facing expectations matter (internal services polling or waiting on the webhook), document the expected max wait explicitly wherever job creation is documented, so callers can set their own reasonable client-side timeouts rather than assuming audio's sub-15-minute experience carries over.

**Warning signs:**
A reconciler stuck-job threshold constant shared unchanged across image/document/audio/av; no `AVUniqueTTL`-equivalent explicitly derived from a measured worst-case; no documentation update warning internal clients that video jobs can legitimately take substantially longer than every prior engine class.

**Phase to address:**
The phase that wires av into the reconciler (mirrors `DOC-09`/audio's reconciler-routing phase) — threshold sizing should be an explicit line item in that phase's plan, not assumed covered by the routing-mechanism reuse.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|-----------------|------------------|
| Copy `isAudioTerminal`'s branch shape into `isAVTerminal` with string-prefix swapped | Fast to write, looks consistent with existing code | Misclassifies legitimate heavy transcodes as terminal (Pitfall 6) — silent correctness bug, not a build error | Never for the transcode feature; acceptable only for thumbnail/extract if independently re-derived and documented as such |
| Skip resolution/fps ceiling, ship duration-only guard (porting only the `EnforceMaxDuration` half of Pitfall 4) | Faster to ship, reuses existing code untouched | Leaves a live decompression-bomb vector (small-duration/huge-resolution or vice versa) | Never — both axes are equally load-bearing |
| Bake ffmpeg via floating `apt-get install ffmpeg` instead of a pinned/verified version (skip Pitfall 3's pin) | Simpler Dockerfile, no commit-hash bookkeeping | Inherits whatever CVEs are unpatched in Debian's packaged ffmpeg at build time, with no tracked upgrade trigger | Never for first ship; acceptable only as an explicitly-flagged interim step with a tracked follow-up |
| Accept a client-supplied raw filter/codec string opt "just for this one power-user feature" (skip Pitfall 2's allowlist) | Faster to expose flexible transcode options | Reopens the OPTS-01/02 injection class this project has closed three times already (LibreOffice, audio, HTML) | Never |
| Single measured-once RTF constant for `AV_ENGINE_TIMEOUT` (skip Pitfall 5's matrix) | Matches the audio precedent exactly, less measurement work | Either false-terminal on legitimate heavy jobs or an overlong timeout that weakens Pitfall 4's ceiling's practical effect | Acceptable ONLY if paired with a narrow, explicitly-capped opts allowlist (few codec/resolution/preset combinations) for v1.8, with broadening deferred as its own future milestone decision |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|-----------------|-------------------|
| `jobs.engine` CHECK constraint + reconciler engine-routing switch | Add the `av` migration and register the converter, but forget to add the `av` branch to the reconciler's engine-routing switch (mirrors `DOC-09`) — routing fails closed (skip + metric) rather than erroring, so stuck av jobs silently never auto-recover instead of throwing an obvious error | Add `av` to the reconciler routing switch in the SAME plan/commit as the `jobs.engine` migration and converter registration — treat them as one atomic unit of work, verified by a reconciler test asserting zero false-skips for the `av` engine (mirrors `SC4`'s "zero false-recovery" test cited for audio) |
| Webhook-worker / `WEBH-01`'s `MaxRetry`/backoff window | Assume the existing ~30-minute webhook retry window is still generous relative to an av job's total lifecycle once the (potentially much longer) `AV_ENGINE_TIMEOUT` + retry budget is added on top | Re-derive whether the webhook delivery window still comfortably exceeds worst-case av job completion time (queued→active→retries→done/failed), the same class of arithmetic already done once for `AudioUniqueTTL`'s `> worst-case` derivation |
| KEDA `scaleDownStabilizationSeconds` for the av `ScaledObject` | Copy audio's `900`-second stabilization window (chosen specifically because 742s-class jobs don't fit HPA's 300s default) verbatim | Re-derive against `AV_ENGINE_TIMEOUT` once measured (Pitfall 5) — if video jobs run longer than audio's, 900s may itself be too short and repeat the exact HPA-default mistake Phase 33 already found and fixed once |
| `Content-Type`/`MIMEType` table (`internal/convert/sniff.go:99-155`) | Add video/container MIME types as an afterthought once the converter works, rather than up front | Extend `MIMEType` in the same change that adds the video sniffer signatures, mirroring how every prior engine's format additions kept `MIMEType` in lockstep (audit note: currently covers image/document/html/audio but has zero video entries) |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|-----------------|
| Output-side seeking for thumbnails (`-i input -ss timecode` instead of `-ss timecode -i input`) | Thumbnail jobs take roughly as long as a full decode regardless of requested timecode; av-worker CPU pegged on trivially simple thumbnail requests | Always seek input-side (`-ss` before `-i`) for thumbnail extraction | Any file longer than a few seconds; gets worse linearly with source duration |
| Single flat `AV_ENGINE_TIMEOUT`/RAM envelope sized for the cheapest opt combination but exposed for the most expensive one | Legitimate 4K/slow-preset jobs intermittently time out (misclassified as malformed input per Pitfall 6) under normal, non-malicious load | Bound the opts allowlist to the measured matrix (Pitfall 5), or measure the actual worst reachable combination | As soon as any client requests the upper end of the exposed codec/resolution/preset space |
| No disk-space ceiling on workDir (Pitfall 9) | Worker OOMs/crashes or (in k8s) triggers node eviction under concurrent large-video load, with no prior warning signal | Explicit ephemeral-storage limit + pre-flight or declared-size disk check | First burst of concurrent large uploads at real internal-service traffic volume, not visible in single-job manual testing |
| ffmpeg thread auto-detection ignoring cgroup CPU quota (Pitfall 10) | Wall-clock transcode time is inconsistent/slower than expected under the container's actual CPU allocation, RTF measurements taken on an unconstrained dev machine don't reproduce in the resource-limited container | Explicit `-threads` from `CgroupCPULimit()`, RTF measurement run inside the real resource-limited container (mirrors `audio-rtf-measure.sh`'s own container-based methodology) | Any container CPU limit below the measurement host's core count — i.e. every real deployment given the `--cpus 2` precedent |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Treating `"file:" + path` argv prefixing (IN-01) as sufficient protocol hardening for video (Pitfall 1) | SSRF against internal services / arbitrary local file read via HLS playlists, concat directives, or subtitle-embedded URLs inside otherwise-valid-looking video files | Explicit `-protocol_whitelist file,crypto` on every ffmpeg/ffprobe invocation touching client bytes, verified by an offline-canary test (mirrors chromium's `HTML-01` canary) |
| Accepting any client-influenced string into a filtergraph (`-vf`/`-filter_complex`) argv element (Pitfall 2) | `movie=`/`amovie=` source filters can open arbitrary files/URLs independent of `-protocol_whitelist` on the main input | Typed, closed-enum/range-validated `VideoOpts` mapped server-side to fixed filter templates — never client-string-to-filtergraph concatenation |
| Treating decompression-bomb-style ceilings as the only relevant "untrusted video decode" risk (Pitfall 3) | Memory-corruption/RCE-class decoder bugs (e.g. CVE-2026-8461 "PixelSmash") are unaffected by dimension/duration ceilings and triggerable by files as small as 50 KB | Pin and track ffmpeg's exact version against upstream security advisories; do not treat magic-bytes + dimension validation as a completeness guarantee for decode safety |
| Porting the image-engine's single-axis dimension ceiling to video without a duration/frame-count axis (Pitfall 4) | A file with plausible per-frame dimensions but extreme duration/fps (or vice versa) bypasses a dimension-only check | Independent duration ceiling (`EnforceMaxDuration`, reused as-is) AND resolution/fps ceiling, both fail-closed, both required |
| Three ISOBMFF-family sniffers (`m4aBrands`, `heicBrands`, a new video-brand table) with no cross-table disjointness test (Pitfall 7) | A crafted or ordinary file could be misdetected across engine classes (e.g. a video file accepted as audio input), defeating format-pair validation at the input | Explicit disjointness unit test across all `ftyp`-brand allowlists in the codebase whenever a new one is added |

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-------------------|
| Silent thumbnail clamp-to-nearest-frame on an out-of-range timecode (Pitfall 11) instead of an explicit error | Client receives a "successful" thumbnail that is not actually from the requested timecode, with no signal anything went wrong | Pre-flight bounds-check the requested timecode against probed duration; reject out-of-range requests with a clear 422-class error, mirroring the existing format-pair-mismatch 422 convention |
| No progress signal for long-running transcode jobs (Pitfall 12) | Internal-service clients polling `GET /v1/jobs/{id}` or waiting on a webhook have no way to distinguish "queued behind other work," "actively transcoding, expect several more minutes," and "something is wrong" | Out of scope to build full progress-percentage reporting in v1.8 (would require a new `Converter`/`Handler` contract dimension, explicitly deferred per `PROJECT.md`'s existing "no Handler/Capability/Progress refactor" decision) — but document expected worst-case wait time for video jobs wherever job creation is documented, so client-side timeout expectations are set correctly up front |
| Vague single "conversion failed" error for what could be a rejected-dimensions bomb, a rejected-duration bomb, a malformed-container SSRF attempt, or a genuine transient failure | Internal-service developers integrating against the API cannot distinguish "fix your input" from "retry later" from "this file is not supported," slowing their own debugging | Distinct, stable error codes per rejection class (mirrors the existing `ErrAudioDurationExceeded`/`ErrDimensionsUnknown` sentinel-error pattern) surfaced distinguishably enough that API consumers can branch on them, not just a single generic 422/500 |

## "Looks Done But Isn't" Checklist

- [ ] **`-protocol_whitelist` on ffmpeg/ffprobe invocations:** Often present on the primary `-i` input but missing on a secondary invocation (e.g. a thumbnail-only or ffprobe-duration-guard call added later) — verify EVERY subprocess invocation that touches client bytes, not just the first one written.
- [ ] **Video decompression-bomb guard:** Often ships with a duration ceiling only (the easier-to-port half, reusing `audioduration.go` verbatim) with the resolution/fps ceiling deferred or forgotten — verify both axes are independently enforced and tested.
- [ ] **Container sniffer disjointness:** A new video sniffer often looks complete once it correctly detects real-world video fixtures — verify it is also tested AGAINST the existing audio/image fixture corpus (a WAV file must never match the new AVI matcher; an M4A file must never match the new MP4-video matcher) as an explicit negative-test suite, not just positive detection tests.
- [ ] **RTF/timeout measurement:** Often measured against one "representative" fixture and treated as done — verify it covers the actual worst-reachable-combination given the shipped opts allowlist, not just a single happy-path codec/resolution.
- [ ] **Reconciler + webhook + KEDA threshold reuse:** Engine routing (the mechanism) is often correctly extended to `av` while the numeric thresholds/windows (stuck-job timeout, webhook retry window, `scaleDownStabilizationSeconds`) are left at values derived for a shorter-duration engine — verify every numeric threshold, not just the routing switch, was re-derived for av's actual worst-case duration.
- [ ] **Disk-space bound:** Almost certainly absent by default given no prior engine needed one — verify an explicit check/limit exists before large-file load testing, not discovered via an actual worker crash.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|-----------------|-----------------|
| Missing `-protocol_whitelist` shipped to production (Pitfall 1) | MEDIUM | Add the flag to every ffmpeg/ffprobe invocation, redeploy; audit `job_events`/logs for any prior job that triggered an unexpected outbound connection or read outside its workDir (would require enabling network/file-access observability retroactively — treat any gap here as a "cannot rule out" incident, not a "confirmed clean" one) |
| Client-influenced filtergraph string shipped (Pitfall 2) | HIGH | Same class of fix as the original OPTS-01/02 closure (LibreOffice) — replace the free-form field with a closed-allowlist typed opt, re-validate all persisted `jobs.options` rows containing the old field shape (mirrors the preset re-validation-on-use precedent already built for `PRST-01..04`) |
| Unpinned/unpatched ffmpeg version in production when a decoder CVE lands (Pitfall 3) | MEDIUM | Bump the pinned version in `Dockerfile.av-worker`, rebuild, redeploy — cost is proportional to how quickly the advisory is noticed, which is why establishing the tracking process up front (not retrofitting it after an incident) matters |
| RTF-derived timeout wrong for the actual opt space (Pitfall 5) | LOW-MEDIUM | Re-run the measurement matrix, adjust `AV_ENGINE_TIMEOUT`/reconciler threshold/KEDA stabilization together (mirrors Phase 32's NO-GO-lever precedent — cutting `AV_MAX_DURATION_SECONDS` or the opts allowlist is a legitimate faster fix than re-measuring/inflating the timeout) |
| Video→transcript shipped as an ad-hoc cross-queue chain without schema support (Pitfall 8) | HIGH | Retrofitting parent/child job linkage into the existing single-row-per-job schema after the fact is a genuine migration + reconciler + webhook-suppression redesign — strongly prefer resolving this BEFORE implementation (see Pitfall 8's "how to avoid") over recovering from it after |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|-------------------|----------------|
| 1. FFmpeg protocol auto-probing SSRF/LFI | First phase adding any ffmpeg/ffprobe invocation | Offline-canary test: adversarial m3u8/concat/subtitle-URL fixtures produce zero outbound connections and zero out-of-workDir reads |
| 2. Filtergraph `movie=`/`amovie=` injection | Phase defining `VideoOpts`/`ThumbnailOpts` | Injection test mirroring PDF/A `FilterOptions` test: a crafted opt value cannot reach argv as a filtergraph fragment |
| 3. Decoder RCE/memory-corruption surface (PixelSmash-class) | Phase writing `Dockerfile.av-worker` | Pinned ffmpeg version >= 8.1.2 (or later patched release) documented with source; advisory-tracking process noted as an explicit ongoing commitment in `PROJECT.md` |
| 4. Multi-axis video decompression bomb | Phase adding fail-closed content validation for av | Tests: small-resolution/huge-duration rejected, huge-resolution/small-duration rejected, thumbnail uses input-side seek |
| 5. RTF/timeout doesn't generalize across codec/resolution | Phase measuring `AV_ENGINE_TIMEOUT` (after opts allowlist is closed) | RTF matrix (or explicit narrow-opts NO-GO lever) documented as a Key Decision, mirrors Phase 32 |
| 6. Terminal/transient classification copied incorrectly from audio | Phase wiring av into the worker/queue contour | `isAVTerminal` (or per-feature equivalents) has its own documented cost-profile reasoning per ffmpeg invocation, own pinning tests |
| 7. Container sniffer collisions (AVI/WAV, MKV/WebM EBML, MP4 brands) | Phase adding magic-bytes validation for video | Cross-fixture negative tests against existing audio/image sniffers; brand-table disjointness test |
| 8. Video→transcript cross-engine architecture | First phase touching video→transcript, before any code | Explicit Key Decision entry in `PROJECT.md` resolving single-job-two-stage vs cross-queue-chain |
| 9. No disk-space guard | Phase containerizing av-worker | Explicit ephemeral-storage limit + large-file load test |
| 10. FFmpeg thread/RAM sizing under cgroup limits | Same phase as 9 | `-threads` wired from `CgroupCPULimit()`; RAM ceiling measured inside the real resource-limited container, not copied from audio |
| 11. Thumbnail of corrupt video / out-of-range timecode | Phase implementing thumbnail feature | Duration bounds-check pre-flight test; output re-`Sniff`-validation test |
| 12. Progress-less long jobs vs reconciler/webhook assumptions | Phase wiring av into the reconciler | `AVUniqueTTL`/stuck-job threshold explicitly derived from measured worst-case, not copied from audio's constant |

## Sources

- [FFmpeg Security](https://ffmpeg.org/security.html) — official advisory index (HIGH confidence)
- [FFmpeg Protocols Documentation](https://ffmpeg.org/ffmpeg-protocols.html) — `-protocol_whitelist`/`-protocol_blacklist` official reference (HIGH confidence)
- [Understanding FFmpeg Vulnerabilities: Exploiting HLS m3u8 Files for SSRF and Arbitrary File Read](https://www.ids-sax2.com/understanding-ffmpeg-vulnerabilities-exploiting-hls-m3u8-files-for-ssrf-and-arbitrary-file-read/) — HLS/m3u8 SSRF mechanics (MEDIUM confidence, community writeup, cross-checked against CVE record)
- [vulhub CVE-2017-9993](https://github.com/vulhub/vulhub/blob/master/ffmpeg/CVE-2017-9993/README.md) — concat-protocol incomplete-fix history for CVE-2016-1897 (MEDIUM confidence)
- [SSRF vulnerability via FFmpeg HLS processing](https://krevetk0.medium.com/ssrf-vulnerability-via-ffmpeg-hls-processing-f3823c16f3c7) — corroborating writeup (MEDIUM confidence)
- [PixelSmash (CVE-2026-8461): Critical FFmpeg Flaw — JFrog](https://jfrog.com/blog/pixelsmash-critical-ffmpeg-vulnerability-turns-media-files-into-weapons/) — original disclosure, MagicYUV decoder heap overflow, 50 KB crafted AVI/MKV/MOV trigger, RCE demonstrated against Jellyfin/Nextcloud (HIGH confidence, vendor disclosure)
- [FFmpeg fixes PixelSmash flaw in widely used video decoder — BleepingComputer](https://www.bleepingcomputer.com/news/security/ffmpeg-fixes-pixelsmash-flaw-in-widely-used-video-decoder/) — patch confirmation, FFmpeg 8.1.2 (2026-06-17) (HIGH confidence)
- [FFmpeg PixelSmash Flaw Allows RCE on Video Players, Media Servers, NAS Appliances — SecurityWeek](https://www.securityweek.com/ffmpeg-pixelsmash-flaw-allows-rce-on-video-players-media-servers-nas-appliances/) — corroborating coverage (MEDIUM confidence)
- FFmpeg VFR/CFR desync mechanics — general community consensus across multiple tutorial sources (LOW-MEDIUM confidence, not independently spec-verified in this pass; flagged for phase-specific validation if video→transcript timestamp accuracy becomes a hard requirement)
- MP4 (ISOBMFF) `ftyp`/major-brand structure, MKV/WebM shared EBML magic + `DocType`-based differentiation, RIFF `WAVE`/`AVI ` form-type collision — established container-format specifications (HIGH confidence, consistent with this project's own existing in-code documentation of the adjacent ISOBMFF collisions in `internal/convert/audiosniff.go` and `internal/convert/sniff.go`)
- This repository's own code and doc comments, read directly: `internal/convert/exec.go`, `internal/convert/whisper.go`, `internal/convert/audioduration.go`, `internal/convert/audiosniff.go`, `internal/convert/sniff.go`, `internal/convert/dimensions.go`, `internal/convert/cgroup.go`, `internal/convert/audioopts.go`, `internal/worker/worker.go`, `Dockerfile.audio-worker`, `scripts/audio-rtf-measure.sh`, `.planning/PROJECT.md` (HIGH confidence — primary source for all "this project's own precedent" claims)

---
*Pitfalls research for: adding a video/ffmpeg engine class to a hardened multi-engine Go async file-conversion service*
*Researched: 2026-07-19*
