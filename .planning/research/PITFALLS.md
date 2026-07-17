# Pitfalls Research

**Domain:** Adding a fourth engine class (offline whisper.cpp audio transcription) to an existing production-hardened Go/asynq/Postgres/S3/KEDA conversion service, plus closing a v1.6 hardening tail
**Researched:** 2026-07-17
**Confidence:** HIGH for OctoConv-internal patterns (verified against `internal/worker/worker.go`, `internal/queue/queue.go`, `internal/reconciler/reconciler.go`, `deploy/chart/octoconv/*`, and the v1.6 phase reviews); MEDIUM for whisper.cpp/ffmpeg-specific external claims (WebSearch, cross-checked against multiple sources but not Context7-verified — no whisper.cpp Context7 entry)

## Critical Pitfalls

### Pitfall 1: ENGINE_TIMEOUT sizing conflates "input is bad" with "input is legitimately long" — a naive copy of the document-class terminal-on-timeout pattern fails ordinary long jobs

**What goes wrong:**
The project's existing lesson (DOC-08) is that a LibreOffice hang on bad input is *deterministic* — the same conversion attempt will always time out again, so `DOCUMENT_ENGINE_TIMEOUT` expiry is classified terminal (`isDocumentTerminal`, `internal/worker/worker.go:192-214`) rather than retried. Audio transcription breaks the assumption that motivated that design: a 1-hour recording at reduced CPU allocation can legitimately take many minutes to tens of minutes to transcribe, and that duration is a property of *legitimate* input, not corruption. If the audio engine copies `isDocumentTerminal`'s pattern verbatim (timeout ⇒ terminal, `SkipRetry`, no asynq retry), then any legitimately-long file that lands even slightly outside the configured `AUDIO_ENGINE_TIMEOUT` is marked permanently failed on the first attempt — indistinguishable in the client's eyes from a genuinely corrupt file, with zero chance of success even on a less-loaded worker.

**Why it happens:**
Pattern-matching the newest, most recently-shipped engine (document, HTML) instead of re-deriving the terminal/transient decision from the actual failure mechanics of whisper.cpp. Both existing "timeout-is-terminal" engines (LibreOffice, chromium) hang because of a bad/malformed *input*, not because the job is legitimately compute-heavy — audio transcription time scales linearly-ish with duration and is compute-heavy by design, not by pathology.

**How to avoid:**
Split the timeout surface into two distinct failure domains and classify each independently, mirroring how `terminalLibreOfficeSignatures`/`terminalChromiumSignatures` already scope termination to *specific stderr signatures*, not "any timeout":
- An ffmpeg preprocessing step timing out (decode taking far longer than the declared/expected duration implies) is a strong signal of malformed/adversarial input → terminal is defensible, same rationale as image dimension-bomb rejection.
- A whisper.cpp inference timeout on audio that has already passed a sane duration/format check is much more likely "legitimately long or resource-starved" → default to transient (mirror the image engine's `isTerminal`, which deliberately has no `context.DeadlineExceeded` arm) so a KEDA-scaled retry with fresh CPU allocation gets a real second chance, bounded by `AUDIO_MAX_RETRY`.
Size `AUDIO_ENGINE_TIMEOUT` from a measured realtime-factor benchmark of the actual model/thread-count combination on the container's real cgroup CPU limit (not the host's core count), with generous headroom — do not reuse `DOCUMENT_ENGINE_TIMEOUT`'s 300s default, it is off by 1-2 orders of magnitude for a 1-hour input.

**Warning signs:**
- Live E2E test only exercises short (seconds-long) audio fixtures, never a fixture near the configured timeout boundary.
- `AUDIO_ENGINE_TIMEOUT` is left at a value copy-pasted from `DOCUMENT_ENGINE_TIMEOUT` (300s) without a benchmarked realtime-factor calculation.
- No distinction in the converter between "ffmpeg step failed/timed out" and "whisper.cpp step failed/timed out" in the wrapped error message — both would fall into one bucket and get the same classification.

**Phase to address:**
Audio engine core phase (converter + worker handler + timeout classifier), before the KEDA/chart integration phase — the classification decision determines what the retry/KEDA-cooldown tuning in the later phase needs to accommodate.

---

### Pitfall 2: asynq unique-lock TTL derived from the wrong (or default) engine timeout re-opens the T-03-10 double-processing race for exactly the class that can least afford it

**What goes wrong:**
Every existing engine class derives its `asynq.Unique` lock TTL from `(maxRetry+1) * engineTimeout` plus margin (see `ImageUniqueTTL`/`DocumentUniqueTTL`/`HTMLUniqueTTL` in `internal/queue/queue.go`, and the `attemptCtx` comment in `internal/worker/worker.go:596-609` that names the race explicitly: "a single attempt that outlives that TTL would let the lock lapse in Redis while this handler is still running, letting the reconciler's next Enqueue...Convert create a second concurrent task for the same job"). If a new `AudioUniqueTTL` function is not written and wired through `queue.Client` (i.e., the audio queue reuses `ImageUniqueTTL` or a hardcoded default), the lock will be sized for a 120s engine timeout while the actual job can legitimately run 30-60+ minutes. The lock expires in Redis mid-transcription; the next reconciler sweep tick (`RECONCILER_SWEEP_INTERVAL`, default 1 minute) enqueues a genuinely-duplicate second task, and asynq happily runs two concurrent whisper.cpp processes against the same job — the exact double-processing race the `attemptCtx` design was built to close, now reopened for the one class with the longest attempt windows and the most expensive compute to duplicate.

**Why it happens:**
The derivation formula is a small, easy-to-miss function that must be written fresh per class (`ImageUniqueTTL`, `DocumentUniqueTTL`, `HTMLUniqueTTL` are three near-identical but distinct functions, not a shared parameterized one) — it is exactly the kind of "add an engine without going through the full checklist" mistake the codebase's own anti-pattern list warns about.

**How to avoid:**
Write `AudioUniqueTTL(maxRetry int, engineTimeout time.Duration) time.Duration` following the same shape and unit-test pattern as `TestDocumentUniqueTTL`/`TestImageUniqueTTL` (`internal/queue/queue_test.go`), wire it through `queue.Client`'s constructor and `EnqueueAudioConvert`, and add an explicit test asserting it strictly exceeds the worst-case retry lifetime for the *audio* timeout value, not a copy of the document one. Given `AUDIO_ENGINE_TIMEOUT` will likely be sized in the tens-of-minutes range (Pitfall 1), the resulting TTL will be correspondingly large (potentially hours) — confirm Redis key TTL of that magnitude is acceptable (it is bounded per in-flight job, not unbounded, so this should be fine, but worth a sanity note in the phase's context doc).

**Warning signs:**
- `queue.Client` constructor for the audio queue passes `imageUniqueTTL` or a hardcoded duration instead of a new `audioUniqueTTL` field.
- No `TestAudioUniqueTTL` monotonicity/lower-bound test analogous to the three existing ones.
- Live E2E only exercises short audio fixtures whose attempt duration never approaches even the (mis-sized) TTL — the bug is invisible until a real long file runs in production.

**Phase to address:**
Audio engine core phase (queue wiring), verified by a dedicated unit test before any live E2E gate.

---

### Pitfall 3: Reconciler's global `RECONCILER_ACTIVE_STALE_AFTER` (5m default) is not engine-aware — sweeps a genuinely-still-running audio job every tick for its entire duration

**What goes wrong:**
`internal/reconciler/reconciler.go`'s `Config` (`QueuedStaleAfter`, `ActiveStaleAfter`) is a single pair of durations applied identically to every engine class via `FindStale(ctx, queuedStaleAfter, activeStaleAfter)` — there is no per-engine override. The 5-minute default was sized for `ENGINE_TIMEOUT=120s` (image) and `DOCUMENT_ENGINE_TIMEOUT=300s` (document), giving comfortable margin. A 30-60 minute audio job will sit "active" past that 5-minute threshold for most of its life. `sweep()` (`internal/reconciler/reconciler.go:246-315`) is *designed* to treat this safely — the enqueue-first + `asynq.ErrDuplicateTask` guard means a correctly-derived unique lock (Pitfall 2) makes every one of these repeated sweep attempts a silent no-op, not a real recovery event. But this only holds if Pitfall 2 is fixed correctly; if it is not, this is the mechanism that actually fires the duplicate task. Even when safe, it means the sweeper queries `RecoveryCount`/`FindStale` for the same in-flight audio job roughly once a minute for up to an hour — harmless today at current scale, but worth flagging as the kind of assumption ("active jobs finish within single-digit minutes") baked into a global config knob that a new long-running class silently violates.

**Why it happens:**
`ActiveStaleAfter` was designed when every engine's worst-case attempt duration was in the same order of magnitude (seconds to a few minutes); nothing in the reconciler's interface signals "this threshold needs to vary by `jobs.engine`."

**How to avoid:**
At minimum, raise the global `RECONCILER_ACTIVE_STALE_AFTER` env default (or override in the audio worker's deployment) to comfortably exceed `AUDIO_ENGINE_TIMEOUT`, and treat Pitfall 2's fix as the actual safety net (the stale threshold only decides *when to attempt* recovery; the unique lock decides whether that attempt is a real duplicate). Add a regression test mirroring the existing reconciler tests that asserts a long-running audio job (active for `> ActiveStaleAfter` but `< AudioUniqueTTL`) produces zero `reconciler_recovery` events across repeated sweep ticks. If per-engine staleness thresholds are ever justified (e.g. a genuinely-stuck audio worker taking 45 minutes to notice vs. an image worker taking 5), that is a larger `Config` refactor — treat it as a documented, accepted limitation for this milestone rather than scope creep, per the existing pattern of accepted residual risks in `PROJECT.md`.

**Warning signs:**
- Prometheus `octoconv_reconciler_actions_total` shows nonzero `recovered` counts for jobs on the audio queue while the *original* attempt is still genuinely in-flight (would indicate Pitfall 2's TTL is undersized, not just sweep noise).
- No test exercises `FindStale` + `RecoveryCount` behavior across a job whose active duration deliberately straddles `ActiveStaleAfter`.

**Phase to address:**
Audio engine core phase, alongside Pitfall 2 (same root cause: timeout-derived values must be re-derived per class, never assumed to transfer).

---

### Pitfall 4: KEDA `cooldownPeriod` only governs the 1→0 transition — a long transcription can be silently killed by the *Kubernetes HPA's* default 300s `scaleDown.stabilizationWindowSeconds` on the N→N-1 step

**What goes wrong:**
This is a lesson the project already learned and documented explicitly in Phase 28 (`deploy/chart/octoconv/templates/scaledobject-document.yaml:11-20`): `cooldownPeriod` in a KEDA `ScaledObject` only governs the "scale to zero" (1→0) decision. Any downscale step where replicas stay above zero (N→N-1, e.g. 2→1) is governed by the *standard Kubernetes HPA*'s `behavior.scaleDown.stabilizationWindowSeconds`, which defaults to 300 seconds and is completely independent of `cooldownPeriod`. If the audio ScaledObject is configured with only `cooldownPeriod` tuned (following the naive assumption that it governs all downscaling) and `maxReplicaCount > 1`, a long-running transcription can be selected as the "victim" pod during an N→N-1 downscale once the default 300s HPA window has elapsed — for a 45-60 minute job, 300s is nowhere near enough protection, and the pod receives SIGTERM mid-transcription (the worker's graceful-shutdown budget, `ShutdownTimeout`, would then have to absorb an entire remaining transcription, which for `ENGINE_TIMEOUT`-scale timeouts is a much larger grace period than any of the three existing classes need).

**Why it happens:**
KEDA's own docs and this project's own code review process (Phase 27's WR-04, Phase 28's own header comment) both independently rediscovered this distinction — it is a genuinely easy trap because `cooldownPeriod` "sounds like" it should cover all downscaling, and the three existing worker classes' short job durations (120s/300s/60s) never exposed the gap in practice until the Phase 28 load-proof specifically engineered a scenario to hit it.

**How to avoid:**
For the audio `ScaledObject`, explicitly set `advanced.horizontalPodAutoscalerConfig.behavior.scaleDown.stabilizationWindowSeconds` (mirroring the `keda.document.scaleDownStabilizationSeconds` knob already added in Phase 28, generalized to the audio class) to a value that exceeds the worst-case single-job duration, not just `cooldownPeriod`. Pair this with `terminationGracePeriodSeconds` sized generously above `AUDIO_ENGINE_TIMEOUT` (the existing per-class invariant documented in Phase 27's WR-04: `ShutdownTimeout` (`ENGINE_TIMEOUT + 10s`) + metrics-shutdown margin must stay under pod grace) — for audio this pushes `terminationGracePeriodSeconds` into the tens-of-minutes range, which is a real chart value, not a rounding-error tweak like the existing 90-330s range.

**Warning signs:**
- Audio `ScaledObject`'s YAML sets only `cooldownPeriod`, with no `scaleDownStabilizationSeconds`/HPA `behavior` block at all.
- `maxReplicaCount` for the audio class is left at 1 (sidesteps the N→N-1 problem entirely but also removes any horizontal scale-up benefit — a legitimate, cheaper mitigation worth considering explicitly rather than by accident) — decide deliberately, don't default into it.
- No load-proof-style test (mirroring Phase 28's SC3) exercises a downscale from N>1 while a long audio job is active on the "victim" pod.

**Phase to address:**
Audio KEDA/chart integration phase — this is purely a chart/HPA-tuning concern, separable from the core engine-conversion logic in Pitfall 1-3.

---

### Pitfall 5: whisper.cpp thread-count defaults to host core count, not the container's cgroup CPU limit — throttling, unpredictable wall time, and OOM risk all trace back to one un-set flag

**What goes wrong (MEDIUM confidence, WebSearch-sourced):**
whisper.cpp (and its Python wrappers) expose a `--threads`/`-t` flag that, left unset, typically defaults to a heuristic based on the *host's* visible core count — which under a Kubernetes/Docker CPU limit (`cpus: "2.0"` in this project's existing worker resource blocks) does not match the cgroup quota actually enforced. Spawning more compute threads than the cgroup CPU quota allows causes CFS throttling: the process appears "running" but makes far less real progress per wall-clock second than the thread count implies, which (a) blows out the realtime-factor assumption behind whatever `AUDIO_ENGINE_TIMEOUT` was benchmarked (Pitfall 1), and (b) can multiply peak memory (each thread keeps its own working buffers) well past what a single-threaded run needs, on the same 1-2Gi memory ceiling this project's other worker classes use — one report from the wider Whisper-family ecosystem describes a Docker container being OOM-killed on audio well under a minute long, purely from thread/memory interaction under a constrained container.

**Why it happens:**
The flag is easy to leave at its default because the binary runs and produces correct output *on a developer's unconstrained laptop* — the failure only appears once wrapped in the project's actual container resource limits, which is exactly the gap between local dev and the target deployment this project has hit before (the LibreOffice/1g-memory sizing in `DOCUMENT_WORKER_CONCURRENCY`'s doc comment: "lower than WORKER_CONCURRENCY's 4 — soffice.bin's heavier per-conversion memory footprint within the same cpus:2.0/memory:1g ceiling").

**How to avoid:**
Explicitly pass a thread-count flag derived from the container's actual CPU allocation (mirror how `DOCUMENT_WORKER_CONCURRENCY` was deliberately set lower than `WORKER_CONCURRENCY` for the same reason — per-conversion resource footprint under a shared ceiling), and size `WORKER_CONCURRENCY` for the audio worker conservatively (likely 1, given whisper.cpp will want to use most/all of the container's CPU budget per job — running 2+ concurrent transcriptions on a 2-4 core container would starve both). Benchmark actual peak RSS for the chosen model size against the container `memory` limit before fixing it, the same way the document/chromium classes' 1Gi/2Gi split was chosen empirically rather than copied.

**Warning signs:**
- Converter shells out to whisper.cpp without an explicit `--threads`/`-t` argument.
- `AUDIO_WORKER_CONCURRENCY` defaults to the same value as `WORKER_CONCURRENCY` (4) without benchmarking concurrent-job memory pressure.
- Live E2E only runs one job at a time — never exercises `WORKER_CONCURRENCY > 1` concurrent transcriptions against the configured memory limit.

**Phase to address:**
Audio engine core phase (converter implementation) for the thread flag; audio KEDA/chart integration phase for the final concurrency/resource-limit values (mirrors how libvips/LibreOffice/chromium resource tuning happened at chart-definition time, not converter-implementation time).

---

### Pitfall 6: ffmpeg preprocessing on untrusted audio containers is a real, currently-active CVE surface, not a hypothetical — needs the same hardened-exec + declared-value sanity check the image pipeline already applies

**What goes wrong (MEDIUM confidence, WebSearch-sourced but corroborated by multiple independent sources including a 2026 disclosure):**
ffmpeg's parser-heavy, format-zoo architecture (this milestone explicitly calls out m4a/ogg's "codec zoo") is a long-running target for crafted-input vulnerabilities — memory-corruption CVEs (e.g. CVE-2021-38171 in ADTS/AAC extradata parsing) and memory-exhaustion CVEs (e.g. CVE-2025-25469, a leak triggered by repeated failed IAMF parses) exist in mainline ffmpeg, and a 2026-disclosed flaw ("PixelSmash", CVE-2026-8461) was reported as allowing crafted media files to compromise the process. Beyond memory-safety bugs, ffmpeg is also the audio analog of the image pipeline's decompression-bomb problem this project already solved once (VALID-03, Phase 7): a WAV/container header can *declare* a sample rate, bit depth, channel count, and duration that implies an enormous decoded PCM size relative to the compressed/container file size on disk — feeding that declared metadata straight into a decode step (rather than validating it first, the way `dimensions.go`'s declared-pixel-ceiling check validates PNG/JPEG/WebP/HEIC/TIFF *before* any engine runs) reopens exactly the class of attack Phase 7 closed for images, this time via ffmpeg's decode step rather than libvips'.

**Why it happens:**
The image engine's decompression-bomb defense (`internal/convert/dimensions.go`) is deliberately scoped by `HasDimensionLimit` to formats with a registered parser — audio formats have no entry in that table today, and nothing forces a new engine class to add an analogous check; it is easy to assume "we already solved decompression bombs" project-wide when the fix was actually format-specific and image-scoped.

**How to avoid:**
(1) Reuse the existing hardened process-exec pattern verbatim — `runCommand` (`internal/convert/exec.go`) already provides process-group SIGKILL-on-timeout, which bounds a hung/malicious ffmpeg invocation the same way it bounds LibreOffice/chromium; no new mechanism is needed there. (2) Add an audio analog of the declared-dimension check: parse each supported container's header for declared duration/sample-rate/channels *before* invoking ffmpeg/whisper.cpp (bounded, in-memory, fail-closed on unparseable/missing fields — same shape as `dimensionParsers`), and reject inputs whose implied decoded PCM size exceeds a configurable ceiling (`MAX_AUDIO_DURATION_SECONDS` or similar), mirroring `MAX_IMAGE_PIXELS`. (3) Pin the ffmpeg version in the audio-worker Dockerfile and track its CVE feed the same deliberate way this project already pins `verapdf/cli` and `chromium-headless-shell` to specific tested versions.

**Warning signs:**
- Audio converter passes the raw client upload straight to ffmpeg with no declared-duration/size sanity check analogous to `Dimensions()`.
- Dockerfile installs ffmpeg via an unpinned `apt-get install ffmpeg` with no version comment (contrast with the project's existing discipline of naming exact tested versions for LibreOffice/chromium/veraPDF).
- No fixture testing a crafted/malformed m4a or ogg container in the audio engine's test suite (mirrors `internal/convert/dimensions_test.go`'s malformed-header fixtures for images).

**Phase to address:**
Audio engine core phase — this is a converter-implementation-time concern, not chart/KEDA tuning, and should land alongside magic-bytes validation (Pitfall 7) as one "content validation" work item, matching how VALID-03 (dimensions) and the original magic-bytes sniff shipped together in Phase 7.

---

### Pitfall 7: MP3's ID3v2 tag precedes the sync word — the project's fixed-offset `sniffLen=12` signature-table pattern cannot detect MP3 as-is

**What goes wrong (HIGH confidence — verified against the ID3v2 spec and corroborated by multiple sources):**
`internal/convert/sniff.go`'s entire detection design assumes every registered format's magic bytes appear within a small, *fixed* leading window (`sniffLen = 12`), matched via simple byte-equality against a prefix (`matchPNG`, `matchJPEG`, `matchWebP`, `matchTIFF`, `matchHEIC`). MP3 breaks this assumption structurally: a very common real-world MP3 starts with an ASCII `"ID3"` tag (ID3v2 header) whose declared size — encoded as a 4-byte *synchsafe* integer (7 usable bits per byte, specifically so the size field itself can never accidentally contain the `0xFF 0xE0`-style sync-word byte pattern) — can push the actual MPEG frame sync word an arbitrary, variable, and potentially large number of bytes into the file (album art embedded in ID3v2 APIC frames alone commonly pushes this well past any small fixed window). A file with no ID3v2 tag starts the sync word at offset 0; a file with a large embedded-art ID3v2 tag can push it hundreds of KB in. Naively adding a `matchMP3` entry to the existing `signatures` table with the same shape as the other five would only detect MP3s with *no* ID3v2 tag (or a very short one) — a large fraction of real-world MP3 files would fail-closed as unrecognized (422), which is safe but user-hostile, or worse, if implemented carelessly (e.g., scanning for the sync-word bit pattern anywhere in a larger buffer instead of properly skipping the declared tag length), could misdetect audio inside an ID3v2 APIC image frame's own JPEG/PNG bytes as the MPEG frame start.

**Why it happens:**
Every format currently registered (png/jpg/webp/heic/tiff, plus the docx/pdf/html signatures added later) happens to have its magic bytes at a small fixed offset from byte 0 — the sniff/dimension architecture was never exercised against a format with a variable-length, self-describing prefix wrapper, so the "read `sniffLen` bytes, byte-compare" pattern was never stress-tested against that shape.

**How to avoid:**
Implement MP3 detection as its own function (not a `signature{format, match}` table entry reusing the fixed-window shape): peek a bounded prefix (reuse the existing `dimPeekLen`-style bounded-read discipline, fail closed if the declared ID3v2 size exceeds the bound rather than growing the buffer, matching `dimensions.go`'s documented `ErrDimensionsUnknown` fail-closed philosophy), detect and skip a leading `"ID3"` + synchsafe-size header if present, then verify an MPEG frame sync word (`0xFF` + top 3 bits of the next byte set) at the resulting offset. Because this needs a *different* signature shape than every other registered format, it is also a natural trigger to generalize `Sniff`'s `match func(buf []byte) bool` type into something that can express "skip N bytes then match" — but keep any such refactor scoped and tested against all five existing formats' behavior unchanged (regression risk: this function is exercised by every existing E2E test).

**Warning signs:**
- MP3 support is added as a one-line entry in the `signatures` table with a fixed-offset matcher, with no test fixture that includes a synthetic ID3v2 tag before the sync word.
- No test exercises an MP3 with an embedded ID3v2 APIC (cover art) frame specifically — the case most likely to push the sync word far enough to break a naive small fixed window.
- `sniffLen` itself is quietly bumped to some larger fixed number "to fit MP3" instead of implementing variable-offset skipping — this only shifts the failure boundary, it does not fix it (a sufficiently large embedded cover image still overflows any fixed window).

**Phase to address:**
Audio engine core phase, as part of the same content-validation work item as Pitfall 6 — magic-bytes detection for every supported audio format (mp3/wav/m4a/ogg/flac, per the milestone's stated codec zoo) needs to be designed together, since m4a (ISOBMFF-based, similar box-walking to the existing HEIC parser) and ogg (page-based) each have their own non-trivial detection shape too.

---

### Pitfall 8: Baking a whisper.cpp model (150MB-1GB+) into the worker image undermines the exact scale-from-zero property Phase 27/28 spent two phases proving

**What goes wrong:**
Phase 27/28's entire KEDA value proposition — measured, timestamped, and committed as evidence (`phases/28/evidence/`) — is fast scale-from-zero: a burst of jobs at true zero replicas produced 4 replicas in 11 seconds (SC1 ≥2/60s). That number assumes a worker pod, once scheduled, starts serving quickly — which in turn assumes the pod's container image is already present on the node or pulls fast. Baking a multi-hundred-MB-to-multi-GB whisper.cpp model file directly into the audio-worker image (the simplest, most tempting approach — no volume/init-container wiring needed) multiplies the image size well past the existing worker images (which are `debian:bookworm-slim` + a CLI binary, order tens of MB), and on OrbStack specifically (the project's documented dev/gate environment, called out as having its own quirks — "четвёртый OrbStack-клин" was hit and documented during the Phase 28 load-proof) a cold image pull for a GB-scale image can dominate or entirely defeat the sub-15-second scale-up SLA the project just finished proving for the other three classes. This would not fail loudly — the audio class would simply "work" while silently having a much worse scale-from-zero characteristic than every sibling class, undetected unless someone re-runs a load-proof-style timestamped measurement specifically for it.

**Why it happens:**
Baking the model in is the path of least resistance (one `COPY` line, no `PersistentVolumeClaim`/init-container/`ConfigMap`-with-download-script plumbing) and works fine in every environment except the one this project has already proven matters (scale-from-zero under load) — the tradeoff is invisible until measured.

**How to avoid:**
Decide explicitly, not by default: (a) bake-in trades startup latency for operational simplicity and offline reproducibility (no external model-download dependency at pod-start time — consistent with this project's "движок локальный (без внешних API)" offline constraint) — acceptable if the model is kept to the smaller end (whisper.cpp's `base`/`small` quantized models are well under 500MB) and the resulting image is pre-pulled/cached on nodes ahead of a burst (defeats pure scale-from-zero but may be acceptable for v1.7's scope); (b) an init-container or volume-mounted model (community best practice per whisper.cpp's own docs) decouples model size from pod cold-start entirely, at the cost of a new failure mode (model volume/init-container itself must be available and populated before the main container can serve — a new dependency to make offline-safe, since the offline constraint rules out a runtime `curl` download from a model registry). Whichever is chosen, re-run a scoped version of the Phase 28 load-proof methodology (burst-from-zero, timestamped) against the audio class specifically before calling KEDA integration done — do not assume the existing image/document/HTML measurements generalize.

**Warning signs:**
- Audio-worker Dockerfile has a `COPY model.bin` / equivalent with no accompanying measurement of resulting image size or pull time.
- No load-proof-style evidence file exists for the audio class analogous to `phases/28/evidence/sc1-sc2-burst-*.csv`.
- `AUDIO_MODEL_PATH` (or equivalent) is hardcoded to a path baked into the image with no volume-mount alternative even documented as a future option (mirrors how the `file://` residual-risk and CFB-parsing-deferred decisions were at least documented as accepted risk, not silently absent).

**Phase to address:**
Audio KEDA/chart integration phase — the decision interacts directly with the scale-from-zero property that phase is responsible for preserving/proving.

---

### Pitfall 9: Non-deterministic transcript output breaks byte-identical E2E assertions this project's test suite otherwise relies on

**What goes wrong:**
Every existing engine class's E2E tests can assert exact or near-exact structural correctness because the outputs are deterministic-enough to check precisely: a resized PNG has exact declared dimensions, a PDF has a `%PDF-` magic-byte header and (for PDF/A) a structurally verifiable `OutputIntent` marker, a docx→pdf conversion can be checked for a valid PDF container. ASR output is fundamentally different: word choice, punctuation, casing, and even segment boundaries can vary run-to-run and model-version-to-model-version for the *same* audio, especially at segment boundaries and in ambiguous acoustic conditions. A test written the way this project's other E2E tests are written — asserting the transcript string equals an exact expected value — will be flaky by construction, and a team used to this project's existing "byte-identical" mindset (explicitly called out as a design goal elsewhere: "byte-identical no-leak 404" in the OPER-01 work) is likely to reach for exact-match assertions out of habit.

**Why it happens:**
Every prior engine class in this codebase was deterministic (or made deterministic — chromium's network isolation, LibreOffice's structural PDF/A check), so "assert exact output" has been a safe, reliable, load-bearing testing pattern up to now; ASR is the first genuinely stochastic/model-dependent output the project has to test against.

**How to avoid:**
Write audio E2E/content assertions against structural and semantic-tolerant properties instead of exact strings: (a) container/contract-level checks (transcript is well-formed per whatever output contract SEED-001 defines — valid JSON, monotonically increasing timestamps, non-empty), which *are* deterministic and should be asserted exactly, same rigor as the PDF/A OutputIntent check; (b) content checks against a small, curated fixture set using substring/keyword presence ("contains the word X spoken clearly mid-file") or word-count-within-a-range rather than exact transcript equality, similar in spirit to how existing dimension checks assert a numeric range/ceiling rather than pixel-exact output; (c) treat any fixture with genuinely ambiguous/quiet audio as unsuitable for a hard content assertion at all — reserve those for the hallucination-specific test in Pitfall 10.

**Warning signs:**
- A committed E2E test asserts `transcript == "exact expected string"` for anything beyond a short, clearly-enunciated, silence-padded fixture.
- Model or whisper.cpp version bumps break previously-passing tests with no clear signal of *why* (output drifted, not a real regression) — a sign assertions were too strict.
- No documented tolerance/fixture-selection rationale for what "correct enough" transcript output means for this project's test suite (contrast with `RESEARCH.md`-documented rationale that exists for e.g. HEIC's max-across-`ispe`-boxes conservatism).

**Phase to address:**
Audio engine core phase, specifically the phase's own test-writing/verification work — should be decided before, not discovered during, the live-E2E gate (matches this project's consistent pattern of documenting test-design rationale, e.g. D-notes, up front).

---

### Pitfall 10: Hallucination on silence/music produces a *structurally valid, exit-0* transcript that is nonetheless garbage — no existing terminal-signature classifier catches it

**What goes wrong (MEDIUM confidence, WebSearch-sourced, corroborated by multiple independent write-ups and an upstream GitHub discussion):**
Whisper-family models are documented to hallucinate — most commonly looping the same short phrase repeatedly — when fed audio segments containing silence, background noise, or music with no clear speech content; this is a known property of the model's training distribution, not a bug that surfaces as a nonzero exit code or a recognizable stderr string. Every existing terminal-classification mechanism in this codebase (`terminalVipsSignatures`, `terminalLibreOfficeSignatures`, `terminalChromiumSignatures`, `terminalVeraPDFSignatures`) works by matching a specific stderr substring the engine emits when it *knows* it failed. A hallucinating whisper.cpp run emits none of that — it exits 0, writes a well-formed output file, and the "failure" is purely semantic (repeated nonsense text), invisible to every mechanism this project currently has for distinguishing success from failure.

**Why it happens:**
Every prior engine class in this codebase fails "loudly" (nonzero exit, or exit-0-but-structurally-detectable-bad-output like an empty/wrong-magic-bytes PDF) — ASR hallucination is the first failure mode where the *process* genuinely succeeds and the *content* is what's wrong, a category this project's terminal/transient classifier architecture was never designed to detect.

**How to avoid:**
Do not attempt full hallucination *detection* in this milestone (out of scope, and a hard, actively-researched problem per the WebSearch results — no simple structural check catches it reliably). Instead: (a) explicitly document this as an accepted residual risk in the milestone's decision log, mirroring how the project already documents accepted risks (`file://` residual read, resource-exhaustion-via-complex-document) rather than silently shipping without acknowledging it; (b) apply the cheapest available mitigation with reasonable confidence — a VAD (voice-activity-detection) preprocessing pass, or whisper.cpp's own no-speech/hallucination-silence-threshold flags if exposed, so segments identified as non-speech are either skipped or flagged rather than transcribed and looped; (c) if SEED-001's transcript contract includes per-segment confidence or no-speech-probability fields, surface them in the output rather than silently dropping the signal, so downstream consumers can filter — this is exactly the kind of "design the contract for a future need" SEED-001 already calls for.

**Warning signs:**
- No mention of VAD, silence handling, or hallucination anywhere in the audio phase's context/decision docs — the risk went unconsidered rather than accepted.
- A live-gate fixture set with no music-bed or silence-padded sample — the failure mode literally cannot be observed if never tested against.
- Transcript contract (SEED-001) has no place to carry per-segment confidence/no-speech signal, foreclosing the cheapest future mitigation before it is even attempted.

**Phase to address:**
Audio engine core phase for the mitigation flags/VAD decision; explicitly log as accepted residual risk in that phase's `Key Decisions`/context doc (matches project convention) rather than a silent gap.

---

### Pitfall 11: Client-supplied model/language selection opts, if added, must go through the same closed-allowlist typed-struct pattern already fought for and won in OPTS-01/02 — a raw string into whisper.cpp's `-m`/argv is a path-traversal and injection surface

**What goes wrong:**
If the audio job API accepts any client-controlled option (model size, language hint, output format) the way document jobs accept `opts` for PDF/A profile selection, the single hardest-won invariant from Phase 14 (OPTS-01/02: "клиентские байты никогда не попадают в argv движка") must be re-applied from scratch for this new engine. A naive implementation that interpolates a client-supplied "model" string directly into a `-m <path>` argv flag (e.g. to let a client pick `tiny`/`base`/`large`) opens a path-traversal / arbitrary-file-read surface (`-m ../../../etc/passwd`-shaped input) exactly analogous to the risk the project already closed once for LibreOffice/chromium option handling.

**Why it happens:**
Model/language selection feels like an obviously-useful, low-risk feature to expose (unlike raw PDF export flags, "pick a model size" sounds harmless) — the temptation to wire it as a raw pass-through string is highest precisely because it seems the least dangerous of the option types this project has handled so far.

**How to avoid:**
Reuse the existing `opts.go`/`DocOptsFromMap`/`HTMLOptsFromMap` pattern verbatim: a typed Go struct with `DisallowUnknownFields`, an explicit closed enum for any model-size selector (mapped server-side to a fixed, pre-validated on-disk path — never client bytes concatenated into a path), and the same re-validation-on-every-delivery discipline `HandleDocumentConvert`/`HandleHTMLConvert` already apply (garbage in `jobs.options` is a terminal, not transient, failure). If model selection is not needed for v1.7's scope, the simplest and safest option is to not expose it at all yet (single fixed model baked into the worker config, not client-selectable) — deferring the feature is strictly safer than shipping a rushed allowlist.

**Warning signs:**
- Any code path builds a whisper.cpp argv slice using string concatenation/`fmt.Sprintf` against a client-supplied opts field instead of a switch/map lookup against a closed Go-level enum.
- No `AudioOptsFromMap` test mirroring `htmlopts_test.go`'s injection-attempt test cases.

**Phase to address:**
Audio engine core phase — decide up front whether model/language selection is even in scope for v1.7; if yes, treat it as its own OPTS-style must-have with its own injection test, not a follow-on afterthought.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|-----------------|------------------|
| Copy `DocumentUniqueTTL`'s formula/timeout-is-terminal pattern verbatim for audio instead of re-deriving from whisper.cpp's actual failure mechanics | Fast to implement, "just works" for short test fixtures | Reopens the T-03-10 double-processing race (Pitfall 2) and fails ordinary long jobs as if corrupted (Pitfall 1) | Never — the derivation must be per-class by design; this is the exact anti-pattern the project's own "Adding an engine without going through the Converter interface" warning generalizes to |
| Bake the whisper.cpp model into the worker image with no measurement | Zero new plumbing (no PVC/init-container), fully offline, matches "движок локальный" constraint most directly | Silently defeats the scale-from-zero property Phase 27/28 spent two phases proving for the other three classes (Pitfall 8) | Acceptable if explicitly measured and documented as a deliberate tradeoff (smaller quantized model, accepted slower scale-up) — not acceptable if simply undocumented |
| Skip a duration/sample-rate declared-value sanity check for audio inputs ("we already solved decompression bombs in Phase 7") | Saves a phase's worth of parser work | Reopens the exact resource-exhaustion class Phase 7 (VALID-03) closed for images, this time via ffmpeg (Pitfall 6) | Never for a client-facing, untrusted-input engine — acceptable only as a temporary gap explicitly tracked as tech debt with a phase reference, mirroring how CFB-differentiation (DOCV3-02) was deferred *with a name and a reason*, not silently |
| Write MP3 detection as a naive fixed-offset `signatures` table entry | Reuses existing code shape with minimal new code | Fails to detect a large fraction of real-world MP3 files (those with ID3v2 tags/embedded art), or worse, misdetects (Pitfall 7) | Never — MP3's structure genuinely requires variable-offset handling; there is no "MVP-acceptable" version of a magic-bytes check that is wrong on common real files |
| Assert exact transcript strings in E2E tests, matching the project's existing "byte-identical" testing culture | Familiar pattern, strong-looking assertions | Flaky test suite from day one; erodes trust in the whole audio-class E2E gate (Pitfall 9) | Never for content assertions; acceptable only for the deterministic contract-shape checks (valid JSON, monotonic timestamps) that genuinely are byte-identical-testable |

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|-----------------|-------------------|
| asynq unique lock (Redis) | Reuse an existing class's TTL-derivation function or a hardcoded default for the audio queue | Write and unit-test a dedicated `AudioUniqueTTL`, sized from the real `AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY` (Pitfall 2) |
| Postgres reconciler | Assume the existing global `RECONCILER_ACTIVE_STALE_AFTER` (5m) "just works" because it already handles three classes | Raise the global default (or accept the sweep-noise tradeoff explicitly) to comfortably exceed `AUDIO_ENGINE_TIMEOUT`; verify via a dedicated test that repeated sweeps against a long in-flight audio job never fire a spurious recovery (Pitfall 3) |
| KEDA `ScaledObject` (Prometheus scaler) | Tune only `cooldownPeriod`, assuming it governs all downscaling the way it does for the three existing (short-job) classes | Explicitly set the HPA `scaleDown.stabilizationWindowSeconds` override (already scaffolded as a values.yaml knob from Phase 28) above the worst-case job duration (Pitfall 4) |
| ffmpeg (new dependency for this milestone) | Install an unpinned version, pass raw client bytes straight into the decode step with no declared-size sanity check | Pin the exact tested version in the Dockerfile (matching the project's discipline for chromium/veraPDF/LibreOffice); add a bounded declared-duration/sample-rate check before decode (Pitfall 6) |
| whisper.cpp CLI (new dependency) | Invoke with no explicit thread-count flag, relying on its host-core-count default | Pass an explicit thread count derived from the container's actual `cpus` limit; size `AUDIO_WORKER_CONCURRENCY` conservatively (likely 1) given whisper.cpp's own internal parallelism (Pitfall 5) |
| Content-Type / magic-bytes sniffing (`internal/convert/sniff.go`) | Extend the existing fixed-offset `signatures` table with an MP3 entry as-is | Implement MP3 (and m4a's ISOBMFF box-walk, ogg's page structure) as their own bounded, variable-offset-aware detectors, not table entries reusing the fixed-window shape (Pitfall 7) |
| Storage upload size limit (`MAX_UPLOAD_BYTES`, currently a single global 100MiB default) | Assume the existing global limit is adequate for audio — an uncompressed 1-hour WAV at CD quality is well over 600MB | Either raise the global limit deliberately (weighing the cost to the other three classes) or introduce a per-format/engine ceiling; do not silently let legitimate long audio uploads 413 with no clear signal of why |

## Performance Traps

| Trap | Symptoms | Prevention | When It Breaks |
|------|----------|------------|-----------------|
| whisper.cpp thread count exceeding the container's real CPU quota | Wall-clock transcription time far exceeds the benchmarked realtime factor; CFS-throttling metrics (if scraped) show high throttled-time | Explicit `--threads` flag sized to the container's actual `cpus` limit, not host core count | Immediately, on any container with a CPU limit below the host's core count — i.e., every production deployment, invisible only on an unconstrained local `go run` |
| `AUDIO_WORKER_CONCURRENCY` copied from `WORKER_CONCURRENCY`'s default of 4 | Multiple concurrent whisper.cpp processes contend for the same 2-4 CPU container, each running far slower than benchmarked, cumulative memory pressure risks OOM | Benchmark actual per-job CPU/memory footprint before fixing concurrency; likely land on 1 for audio, mirroring how document (2) is already lower than image (4) for the same reason | As soon as more than one audio job lands on the same worker pod concurrently under load |
| Model baked into the worker image inflating cold-start image-pull time | Scale-from-zero burst tests show multi-minute time-to-first-replica instead of the ~11s the other three classes achieve | Measure image size/pull time explicitly (Pitfall 8); consider volume/init-container distribution if it dominates | As soon as KEDA scales the audio class from a true zero on a node without the image pre-cached (exactly the scenario Phase 28's evidence was collected under) |
| Reconciler sweep querying `RecoveryCount`/`FindStale` every ~1 minute against every long-running audio job for its entire duration | Elevated Postgres query volume proportional to concurrent long audio jobs × job duration ÷ sweep interval | Accept as a known, bounded cost (Pitfall 3) at current scale; revisit only if audio job volume grows enough to matter | Low risk at internal-service scale this project targets; would matter first if concurrent long audio jobs number in the dozens+ |

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| Passing raw client-supplied ffmpeg/whisper.cpp input with no declared-duration/size sanity check | Decompression-bomb-equivalent resource exhaustion via crafted WAV/container headers implying enormous decoded PCM size (Pitfall 6) | Bounded, fail-closed declared-value check before any decode step, mirroring `dimensions.go`'s pattern for images |
| Unpinned ffmpeg version in the audio-worker Dockerfile | Inherits whatever CVEs are current in the distro-packaged ffmpeg at build time, with no deliberate tracking (multiple ffmpeg CVEs including a 2026-disclosed one exist in the wild) | Pin an exact tested version, same discipline already applied to chromium-headless-shell/veraPDF/LibreOffice |
| Client-supplied model/language opts interpolated into engine argv or a filesystem path | Path traversal / arbitrary file read via a crafted "model" string (Pitfall 11) | Closed-allowlist typed struct (`AudioOptsFromMap`), server-side path lookup, never client bytes in argv — reuse OPTS-01/02's pattern exactly |
| MP3 ID3v2 tag scanning implemented by searching for the sync-word bit pattern anywhere in a larger buffer instead of properly skipping the declared (synchsafe) tag length | Could misdetect bytes inside an embedded ID3v2 APIC image frame as the start of audio, or (less severe) simply fail-open/fail-closed incorrectly on legitimate files | Correct synchsafe-size decode and explicit skip, not a bare pattern search (Pitfall 7) |

## "Looks Done But Isn't" Checklist

- [ ] **Audio class E2E "works":** Often only tested against short, clean, single-speaker fixtures with no ID3v2 tag, no music bed, no silence padding, and no near-timeout-boundary duration — verify a fixture set exists that specifically stresses Pitfalls 1, 7, 9, and 10.
- [ ] **`AUDIO_ENGINE_TIMEOUT`/`AudioUniqueTTL`/reconciler thresholds "wired up":** Often copied from the document class's constants rather than benchmarked/re-derived — verify a dedicated `TestAudioUniqueTTL` exists and `AUDIO_ENGINE_TIMEOUT` traces to an actual realtime-factor measurement, not a copy-paste.
- [ ] **KEDA `ScaledObject` for audio "matches the other three":** Often means only `threshold`/`maxReplicaCount`/`cooldownPeriod` were set, with the HPA `scaleDown.stabilizationWindowSeconds` override left at the Kubernetes 300s default — verify it was set deliberately relative to worst-case job duration (Pitfall 4), not left implicit.
- [ ] **Magic-bytes validation for audio formats "complete":** Often means only the happy-path signature check for each format's simplest case — verify MP3-with-ID3v2, m4a, and ogg each have dedicated malformed/adversarial fixtures, not just one clean sample per format.
- [ ] **Model file "shipped":** Often means "the Dockerfile builds and the container runs" with no measurement of the resulting image's cold-start/scale-from-zero characteristics — verify a load-proof-style timestamped measurement exists for the audio class specifically (Pitfall 8).
- [ ] **v1.6 hardening tail "closed":** Verify WR-01's fix is a deliberate, documented choice (flip `ignoreNullValues` to `false` and accept the fallback-churn tradeoff, *or* keep `true` and add an `absent()` alert) rather than a cosmetic comment change; verify OPER-01's live gate actually exercises the compose `OPERATOR_CLIENT_IDS` passthrough end-to-end (the WR-03 gap from 26-REVIEW), not just that the env var is documented; verify K8S-02's direct-dial re-check is a real re-run against a healthy OrbStack proxy, not re-asserted from the prior degraded-path result.

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|----------------|------------------|
| Undersized `AudioUniqueTTL` causes a real duplicate-processing incident in production | MEDIUM | Both whisper.cpp processes race to `AddOutput`/`MarkDone` — the guarded-transition pattern (`Repo.transition`) already makes the second writer's transition fail cleanly rather than corrupt state; recovery is: fix the TTL derivation, add the regression test, and audit `job_events` for the affected job to confirm no double-billing/double-webhook occurred (webhook dedup already exists via `asynq.Unique` on the webhook queue) |
| Reconciler sweep noise against long audio jobs turns out to matter at scale | LOW | Add a per-engine `ActiveStaleAfter` override to `reconciler.Config` (a config-shape change, not a data-model change) — no migration needed, existing `jobs.engine` column already available for routing |
| Model-baked-in image pull time defeats scale-from-zero in production | MEDIUM | Switch to volume/init-container distribution post-hoc; requires a chart change (new PVC/init-container template) and a re-run of the load-proof measurement, but no application-code change |
| E2E suite turns out to be flaky from exact-transcript assertions | LOW | Rewrite assertions to structural/substring checks (Pitfall 9's prevention) — test-only change, no production code affected |
| MP3 detection ships with the naive fixed-offset bug and silently rejects a chunk of real-world files | LOW-MEDIUM | Fail-closed (422) is the safe direction this bug fails in — no security incident, just a functionality gap; fix is a self-contained function rewrite in `sniff.go` plus new fixtures, no data migration |

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|-------------------|----------------|
| 1. Timeout classification (transient vs terminal) conflates bad-input with legitimately-long | Audio engine core phase | Unit test distinguishing ffmpeg-stage-timeout (terminal) from whisper-stage-timeout-on-validated-input (transient); live fixture near the timeout boundary |
| 2. `AudioUniqueTTL` reuse/undersizing reopens T-03-10 | Audio engine core phase | `TestAudioUniqueTTL` mirroring `TestDocumentUniqueTTL`'s monotonicity/lower-bound assertions |
| 3. Global reconciler staleness threshold not audio-aware | Audio engine core phase | Test: repeated sweep ticks against a long in-flight audio job produce zero `reconciler_recovery` events |
| 4. KEDA cooldown vs HPA stabilization (1→0 vs N→1) | Audio KEDA/chart integration phase | Load-proof-style scenario: downscale from N>1 while a long job is active on the victim pod, confirm no premature SIGTERM |
| 5. whisper.cpp thread count vs cgroup CPU limit | Audio engine core phase (flag) + KEDA/chart phase (concurrency/resource values) | Benchmark realtime-factor and peak RSS under the actual container `cpus`/`memory` limits before fixing `AUDIO_ENGINE_TIMEOUT`/`AUDIO_WORKER_CONCURRENCY` |
| 6. ffmpeg decode-bomb / CVE surface | Audio engine core phase (content validation) | Bounded declared-duration/sample-rate check with malformed-fixture tests; pinned ffmpeg version in Dockerfile |
| 7. MP3 ID3v2-before-sync-word breaks fixed-offset sniff pattern | Audio engine core phase (content validation) | Fixtures: MP3 with no tag, MP3 with a large embedded-art ID3v2 tag, MP3 with a corrupt/oversized declared tag size (fail-closed) |
| 8. Baked-in model defeats scale-from-zero | Audio KEDA/chart integration phase | Timestamped load-proof measurement (Phase-28-style) for the audio class specifically |
| 9. Non-deterministic transcript breaks exact-match E2E assertions | Audio engine core phase (test design) | Test-design rationale documented up front; structural/substring assertions committed alongside the feature, not retrofitted after flakes appear |
| 10. Hallucination on silence/music produces exit-0 garbage | Audio engine core phase | Documented accepted-residual-risk entry; VAD/silence-threshold mitigation applied if in scope; fixture with music/silence segments exists in the test suite |
| 11. Client-supplied model/opts injection risk | Audio engine core phase | `AudioOptsFromMap` with injection-attempt tests mirroring `htmlopts_test.go`, or explicit scope decision to omit client-selectable model/opts entirely |
| WR-01 (empty-PromQL trigger semantics) | v1.6 hardening-tail phase | Explicit chosen fix (flip `ignoreNullValues` or add `absent()` alerting) documented with the tradeoff it accepts, not a silent no-op comment edit |
| OPER-01 (live gate / compose `OPERATOR_CLIENT_IDS` passthrough) | v1.6 hardening-tail phase | Live gate actually exercises an operator-authenticated request through the compose stack end-to-end, closing the WR-03 gap identified in 26-REVIEW |
| Gate-tooling warnings (28-REVIEW WR-02..WR-06, IN-01..IN-09) | v1.6 hardening-tail phase | Each addressed warning has its corresponding script/template diff plus a re-run of the relevant gate |
| K8S-02 direct-dial re-check | v1.6 hardening-tail phase | Fresh host→cluster FQDN dial succeeds without the `kubectl port-forward` workaround, on a verified-healthy OrbStack daemon |

## Sources

- `internal/worker/worker.go` (terminal/transient classification, `attemptCtx`/T-03-10 double-processing race documentation) — HIGH confidence, direct code read
- `internal/queue/queue.go`, `internal/queue/queue_test.go` (per-class `UniqueTTL` derivation pattern and tests) — HIGH confidence, direct code read
- `internal/reconciler/reconciler.go` (global `ActiveStaleAfter`/`QueuedStaleAfter`, engine-routing switch, enqueue-first + `ErrDuplicateTask` guard) — HIGH confidence, direct code read
- `internal/convert/sniff.go`, `internal/convert/dimensions.go` (fixed-offset signature table, bounded declared-dimension parsers, fail-closed philosophy) — HIGH confidence, direct code read
- `internal/convert/chromium.go`, `internal/convert/exec.go` (hardened process-group exec, `/dev/shm` belt-and-suspenders lesson) — HIGH confidence, direct code read
- `deploy/chart/octoconv/values.yaml`, `deploy/chart/octoconv/templates/scaledobject-document.yaml` (KEDA `cooldownPeriod` vs HPA `scaleDown.stabilizationWindowSeconds` distinction, explicitly documented in-repo from Phase 28) — HIGH confidence, direct code read
- `.planning/milestones/v1.6-phases/27-keda-autoscaling/27-REVIEW.md` (WR-01 empty-PromQL/`ignoreNullValues` semantics, WR-04 ShutdownTimeout↔grace-period invariant, WR-06 cooldown-vs-retry-backoff invariant) — HIGH confidence, direct code review artifact
- `.planning/milestones/v1.6-phases/28-autoscale-load-proof/28-REVIEW.md` (gate-tooling warnings, load-proof evidence methodology) — HIGH confidence, direct code review artifact
- `.planning/milestones/v1.6-phases/26-operator-presets-rest/26-REVIEW.md` (OPER-01/WR-03 compose `OPERATOR_CLIENT_IDS` passthrough gap) — HIGH confidence, direct code review artifact
- `.planning/milestones/v1.6-phases/24-helm-chart-core/24-VERIFICATION.md` (K8S-02 direct-dial degraded-path caveat) — HIGH confidence, direct verification artifact
- `.env.example`, `docker-compose.yml`, `Dockerfile.document-worker` (per-class timeout/concurrency/resource defaults, amd64/Rosetta pin precedent) — HIGH confidence, direct code read
- whisper.cpp thread/memory/Docker behavior — MEDIUM confidence, WebSearch (GitHub issues, Docker docs, community Docker images); no Context7 entry available for whisper.cpp
- Whisper hallucination on silence/music — MEDIUM confidence, WebSearch corroborated across an arXiv paper, an OpenAI Whisper GitHub discussion, and a practitioner write-up
- ffmpeg CVEs (CVE-2021-38171, CVE-2025-25469, CVE-2026-8461 "PixelSmash") — MEDIUM confidence, WebSearch (SentinelOne/Hackers-Arise/JFrog vulnerability writeups); not independently verified against the NVD record text
- MP3 ID3v2 synchsafe-size/sync-word-precedence — HIGH confidence, WebSearch corroborated across the Mutagen ID3v2 spec docs, Hydrogenaudio Knowledgebase, and an independent parsing write-up
- whisper.cpp model distribution (bake vs volume) — MEDIUM confidence, WebSearch (official whisper.cpp repo guidance plus several community Docker images taking each approach)

---
*Pitfalls research for: adding an offline whisper.cpp audio-transcription engine class to OctoConv, plus the v1.6 hardening tail*
*Researched: 2026-07-17*
