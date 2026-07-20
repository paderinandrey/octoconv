# Roadmap: OctoConv

## Milestones

- ✅ **v1.0 Hardening MVP** — Phases 1-4 (shipped 2026-07-08) — see `.planning/milestones/v1.0-ROADMAP.md`
- ✅ **v1.1 Tech Debt Cleanup** — Phases 5-7 (shipped 2026-07-08) — see `.planning/milestones/v1.1-ROADMAP.md`
- ✅ **v1.2 Document Engine Class** — Phases 8-11 (shipped 2026-07-10) — see `.planning/milestones/v1.2-ROADMAP.md`
- ✅ **v1.3 Document Class v2** — Phases 12-16 (shipped 2026-07-12) — see `.planning/milestones/v1.3-ROADMAP.md`
- ✅ **v1.4 CI, Presets & Debt Cleanup** — Phases 17-19 (shipped 2026-07-13) — see `.planning/milestones/v1.4-ROADMAP.md`
- ✅ **v1.5 MCP Access & Document Fidelity** — Phases 20-23 (shipped 2026-07-13) — see `.planning/milestones/v1.5-ROADMAP.md`
- ✅ **v1.6 Kubernetes & KEDA** — Phases 24-28 (shipped 2026-07-17) — see `.planning/milestones/v1.6-ROADMAP.md`
- ✅ **v1.7 Audio Engine & Hardening** — Phases 29-33 (shipped 2026-07-18) — see `.planning/milestones/v1.7-ROADMAP.md`
- ⏳ **v1.8 AV Engine (video/ffmpeg)** — Phases 34-37 (in progress, started 2026-07-19)

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

<details>
<summary>✅ v1.0 Hardening MVP (Phases 1-4) — SHIPPED 2026-07-08</summary>

- [x] Phase 1: Merge, Auth & Rate Limiting (4/4 plans) — completed 2026-07-04
- [x] Phase 2: Webhook Delivery (3/3 plans) — completed 2026-07-04
- [x] Phase 3: Retry-Safety & Reconciler (3/3 plans) — completed 2026-07-06
- [x] Phase 4: Content Validation, Storage Lifecycle & Observability (5/5 plans) — completed 2026-07-07

Full details: `.planning/milestones/v1.0-ROADMAP.md`

</details>

<details>
<summary>✅ v1.1 Tech Debt Cleanup (Phases 5-7) — SHIPPED 2026-07-08</summary>

- [x] Phase 5: Webhook SSRF Private-IP Opt-Out (1/1 plans) — completed 2026-07-08
- [x] Phase 6: Reconciler Webhook-Gap Sweep & Staleness Soak Test (4/4 plans) — completed 2026-07-08
- [x] Phase 7: Image Dimension Limit (Decompression-Bomb Protection) (2/2 plans) — completed 2026-07-08

Full details: `.planning/milestones/v1.1-ROADMAP.md`

</details>

<details>
<summary>✅ v1.2 Document Engine Class (Phases 8-11) — SHIPPED 2026-07-10</summary>

- [x] Phase 8: Document Content Safety & Format Detection (2/2 plans) — completed 2026-07-09
- [x] Phase 9: LibreOffice Converter Engine (2/2 plans) — completed 2026-07-09
- [x] Phase 10: Document Worker & Reconciler Integration (4/4 plans) — completed 2026-07-09
- [x] Phase 11: API Routing & End-to-End Document Conversion (4/4 plans, incl. gap closure 11-04) — completed 2026-07-10

Full details: `.planning/milestones/v1.2-ROADMAP.md`

</details>

<details>
<summary>✅ v1.3 Document Class v2 (Phases 12-16) — SHIPPED 2026-07-12</summary>

- [x] Phase 12: Tech Debt Cleanup (1/1 plans) — completed 2026-07-10
- [x] Phase 13: Cross-Format Conversion & Input Safety (3/3 plans) — completed 2026-07-10
- [x] Phase 14: Validated Conversion Options & PDF/A Export (3/3 plans) — completed 2026-07-10
- [x] Phase 15: HTML→PDF Chromium Engine (5/5 plans) — completed 2026-07-11
- [x] Phase 16: Webhook Delivery Decoupling (5/5 plans, incl. gap closure 16-05) — completed 2026-07-12

Full details: `.planning/milestones/v1.3-ROADMAP.md`

</details>

<details>
<summary>✅ v1.4 CI, Presets & Debt Cleanup (Phases 17-19) — SHIPPED 2026-07-13</summary>

- [x] Phase 17: Tech Debt Cleanup (2/2 plans) — completed 2026-07-12
- [x] Phase 18: Presets (4/4 plans) — completed 2026-07-12
- [x] Phase 19: CI Pipeline (2/2 plans) — completed 2026-07-13

Full details: `.planning/milestones/v1.4-ROADMAP.md`

</details>

<details>
<summary>✅ v1.5 MCP Access & Document Fidelity (Phases 20-23) — SHIPPED 2026-07-13</summary>

- [x] Phase 20: Presets REST CRUD & Format Discovery (2/2 plans) — completed 2026-07-13
- [x] Phase 21: MCP Server (3/3 plans) — completed 2026-07-13
- [x] Phase 22: CFB Encrypted-vs-Legacy Classification (2/2 plans) — completed 2026-07-13
- [x] Phase 23: veraPDF ISO 19005 Validation (3/3 plans) — completed 2026-07-13

Full details: `.planning/milestones/v1.5-ROADMAP.md`

</details>

<details>
<summary>✅ v1.6 Kubernetes & KEDA (Phases 24-28) — SHIPPED 2026-07-17</summary>

- [x] Phase 24: Helm Chart Core & Landmine Closure (3/3 plans) — completed 2026-07-14
- [x] Phase 25: MCP Streamable HTTP (3/3 plans) — completed 2026-07-14
- [x] Phase 26: Operator System-Presets REST (2/2 plans) — completed 2026-07-14
- [x] Phase 27: KEDA Autoscaling (3/3 plans) — completed 2026-07-16
- [x] Phase 28: Autoscale Load-Proof (3/3 plans) — completed 2026-07-17

Full details: `.planning/milestones/v1.6-ROADMAP.md`

</details>

<details>
<summary>✅ v1.7 Audio Engine & Hardening (Phases 29-33) — SHIPPED 2026-07-18</summary>

- [x] Phase 29: v1.6 Hardening Tail (3/3 plans) — completed 2026-07-17
- [x] Phase 30: Audio Engine Foundation (3/3 plans) — completed 2026-07-18
- [x] Phase 31: Queue, Worker & Routing Integration (4/4 plans) — completed 2026-07-18
- [x] Phase 32: Containerization & Local E2E + RTF Gate (5/5 plans) — completed 2026-07-18
- [x] Phase 33: KEDA/Helm Chart Integration (3/3 plans) — completed 2026-07-18

Full details: `.planning/milestones/v1.7-ROADMAP.md`

</details>

### v1.8 AV Engine (video/ffmpeg) (Phases 34-37) — IN PROGRESS

- [x] **Phase 34: AV Engine Foundation** - Standalone AVConverter (transcode/audio-extract/thumbnail via ffmpeg), video container sniffers (mp4/mov ftyp, RIFF AVI, EBML mkv/webm), validated AVOpts, duration/resolution guards + protocol-whitelist hardening (completed 2026-07-20)
- [ ] **Phase 35: Queue, Worker & Routing Integration** - av queue + cmd/av-worker, stage-aware retry classification, API/reconciler engine routing, video→transcript pairs routed onto the existing audio queue/worker with a pair-disjointness test
- [ ] **Phase 36: Containerization & RTF-Measured Timeout** - Dockerfile.av-worker (pinned ffmpeg), compose service, RTF-matrix measured AV_ENGINE_TIMEOUT, disk-space guard, CI bake
- [ ] **Phase 37: KEDA/Helm Chart Integration** - av-worker Deployment + ScaledObject (WR-01 triad), QueueAV collector, scale-from-zero load-proof

## Phase Details

### v1.8 AV Engine (video/ffmpeg)

**Milestone goal:** Ship the fifth engine class — video processing via ffmpeg in a dedicated av-worker — following the proven engine-class pattern (own queue/worker/binary/container/KEDA), including a sixth conversion chain (video → transcript) that routes onto the existing audio pipeline rather than duplicating whisper.cpp.

**Dependency spine:** 34 (first, standalone/unregistered — highest-uncertainty EBML sniffer starts here) → 35 (async wiring, needs 34's working converter) → 36 (RTF go/no-go gate — must follow 34's opts allowlist closing, not precede it) → 37 (KEDA tuning consumes 36's measured `AV_ENGINE_TIMEOUT`).

**Key decision (resolved before roadmapping):** video→transcript extends `AudioConverter.Pairs()` to route video-source/transcript-target jobs onto the existing `audio` queue/worker (`Engine()` stays `EngineAudio`) — NOT a duplicate whisper.cpp pipeline inside av-worker. This avoids ~400MB of duplicated whisper.cpp+model image weight and reuses the already-measured `AUDIO_ENGINE_TIMEOUT`/duration-guard machinery. Trade-off: the registry's pair-keyed lookup must for the first time arbitrate between two converters sharing a source-format family, so a pair-disjointness test is a hard requirement of Phase 35, not optional polish.

---

### Phase 34: AV Engine Foundation
**Goal**: A standalone `AVConverter` transcodes, extracts audio, and extracts thumbnails from video files with fail-closed content validation, built and testable directly against ffmpeg before any queue/worker plumbing.
**Depends on**: Nothing (first phase of v1.8 — validate the novel ffmpeg/container-sniffing surface standalone first, mirroring the audio engine's Phase 30 scope fence)
**Requirements**: AVC-01, AVC-02, AVC-03, AVC-04, AVC-05, AVO-01, AVO-02, AVO-03, AVE-01, AVE-02
**Success Criteria** (what must be TRUE):
  1. `AVConverter` transcodes mov/avi/mkv/webm → mp4 (H.264/AAC, `-movflags +faststart`) and mp4 → webm (VP9/Opus), verified by direct invocation against a real ffmpeg binary — every transcode is a full re-encode.
  2. `AVConverter` extracts an audio track to mp3/wav/m4a, using ffprobe-checked stream-copy (`-c:a copy`) when the source is AAC and the target is m4a, and full re-encode otherwise; and extracts a thumbnail frame via fast input-side `-ss` seek at a client-supplied or 1.0s-default (duration-clamped) timecode.
  3. `AVConverter`'s automatic stream-copy fast path remuxes instead of re-encoding whenever ffprobe reports the source codec is already legal in the target container.
  4. Video container sniffers — fixed-offset mp4/mov `ftyp` and RIFF `AVI ` matchers plus a new bounded-peek EBML/DocType parser distinguishing mkv from webm — classify fixtures correctly, and a collision test proves zero overlap with the existing WAV/RIFF, m4a-brand, and heic-brand sniffers.
  5. `AVOpts` (thumbnail timecode, closed resolution-height enum 480/720/1080, HEVC codec choice) is validated through the same `checkStrictObject` closed-allowlist pattern as `AudioOpts`, an injection test proves client bytes never reach ffmpeg argv, and `-protocol_whitelist file,crypto` plus duration/resolution guards block SSRF/LFI and multi-axis decompression-bomb vectors on every ffmpeg/ffprobe invocation (verified by an offline canary test).
**Plans**: 3 plans
- [x] 34-01-PLAN.md — Video container magic-bytes sniffers (mp4/mov/avi fixed-offset + EBML mkv/webm bounded-peek), disjointness test (AVE-01)
- [x] 34-02-PLAN.md — Closed AVOpts allowlist (timecode/resolution/HEVC), video resolution guard, EngineAV const (AVO-01/02/03, AVE-02)
- [x] 34-03-PLAN.md — Standalone AVConverter (transcode/audio-extract/thumbnail, stream-copy fast path, protocol-whitelist canary) (AVC-01..05, AVE-02)

### Phase 35: Queue, Worker & Routing Integration
**Goal**: av-engine jobs (transcode/audio-extract/thumbnail) and video→transcript jobs both flow end-to-end through the async pipeline with correct queue routing, retry classification, and reconciler recovery.
**Depends on**: Phase 34 (needs the working AVConverter to wire into the async lifecycle)
**Requirements**: AVE-03, AVT-01
**Success Criteria** (what must be TRUE):
  1. An uploaded video job targeting mp4/webm/mp3/wav/m4a/jpg/png/webp is routed to a dedicated `av` asynq queue by the API's `EngineFor` content-detection path and consumed end-to-end by `cmd/av-worker` (queued → active → done), with the reconciler routing stranded `jobs.engine='av'` jobs to the same queue.
  2. An uploaded video job targeting txt/srt/vtt/json is routed instead to the existing `audio` queue/worker (video-source pairs added to `AudioConverter.Pairs()`, `Engine()` stays `EngineAudio`), with a dedicated pair-disjointness unit test proving zero overlap between `AVConverter.Pairs()` and `AudioConverter.Pairs()`.
  3. A stage-aware transient/terminal classifier for av jobs is derived fresh (not copied from audio's ffmpeg-timeout-is-terminal precedent, since ffmpeg IS the expensive operation for transcode) and a unit test pins transcode-timeout as transient versus deterministic/malformed-input failures as terminal.
  4. An `AVUniqueTTL` derived from the av engine's own timeout/retry budget prevents duplicate processing, verified by a monotonicity/lower-bound test mirroring `AudioUniqueTTL`.
**Plans**: TBD

### Phase 36: Containerization & RTF-Measured Timeout
**Goal**: A running av-worker container in docker-compose passes a full live E2E, with `AV_ENGINE_TIMEOUT` sized from a measured RTF matrix across the closed opts space rather than a copied or guessed constant.
**Depends on**: Phase 35 (needs the queue/worker contract stable before containerizing; timeout measurement must follow the opts allowlist closing in Phase 34, not precede it)
**Requirements**: AVE-04
**Success Criteria** (what must be TRUE):
  1. `Dockerfile.av-worker` installs a version-pinned Debian `ffmpeg` package (exact `apt-get install ffmpeg=<version>` pin, per STACK.md — Debian backports CVE fixes into 5.1.x; the CVE-2026-8461 backport status is verified against the Debian security tracker during planning, fail-loud if unfixed), and an `av-worker` compose service transcodes/extracts/thumbnails an uploaded file end-to-end through the live compose stack (upload → poll → presigned download) with a signed webhook confirmed.
  2. `AV_ENGINE_TIMEOUT` is derived from a measured RTF matrix spanning the codec × resolution × preset combinations exposed by the closed `AVOpts` allowlist (not a single fixture), with any NO-GO lever applied and documented like Phase 32's audio precedent.
  3. A new disk-space/ephemeral-storage guard and cgroup-derived `-threads`/RAM sizing (mirroring `CgroupCPULimit()`) are wired and verified against the container's real resource ceiling.
  4. The `av-worker` image is added to the CI bake matrix with its `AV_ENGINE_TIMEOUT`/`AV_WORKER_CONCURRENCY`/ShutdownTimeout env wired, and all queue-client-constructing services propagate the new env identically (IN-02 pattern).
**Plans**: TBD

### Phase 37: KEDA/Helm Chart Integration
**Goal**: The av class autoscales in the cluster with production parity to the other four engine classes, and scale-from-zero is live-proven.
**Depends on**: Phase 36 (KEDA cooldown/stabilization and grace-period tuning consume Phase 36's measured `AV_ENGINE_TIMEOUT`)
**Requirements**: AVE-05
**Success Criteria** (what must be TRUE):
  1. An `av-worker` Deployment plus a KEDA `ScaledObject` ship in the chart with `terminationGracePeriodSeconds`/`scaleDownStabilizationSeconds` derived from the measured `AV_ENGINE_TIMEOUT`, and the WR-01 fail-safe triad (`ignoreNullValues:"false"`, fallback replicas, retry-inclusive PromQL) applied verbatim from the first commit.
  2. `QueueAV` is registered in the always-on api queue-depth collector so KEDA resolves the av backlog at genuinely zero replicas.
  3. Scale-from-zero is live-proven for the av class, capturing timestamped Phase-33-style evidence.
  4. env-parity (IN-02 pattern) is confirmed across all queue-client-constructing services for the new `AV_*` env vars, and a long transcode job survives a genuine N→N-1 HPA downscale without a premature SIGTERM.
**Plans**: TBD

## Progress

| Phase | Milestone | Plans Complete | Status | Completed |
|-------|-----------|-----------------|--------|-----------|
| 1. Merge, Auth & Rate Limiting | v1.0 | 4/4 | Complete | 2026-07-04 |
| 2. Webhook Delivery | v1.0 | 3/3 | Complete | 2026-07-04 |
| 3. Retry-Safety & Reconciler | v1.0 | 3/3 | Complete | 2026-07-06 |
| 4. Content Validation, Storage Lifecycle & Observability | v1.0 | 5/5 | Complete | 2026-07-07 |
| 5. Webhook SSRF Private-IP Opt-Out | v1.1 | 1/1 | Complete | 2026-07-08 |
| 6. Reconciler Webhook-Gap Sweep & Staleness Soak Test | v1.1 | 4/4 | Complete | 2026-07-08 |
| 7. Image Dimension Limit (Decompression-Bomb Protection) | v1.1 | 2/2 | Complete | 2026-07-08 |
| 8. Document Content Safety & Format Detection | v1.2 | 2/2 | Complete | 2026-07-09 |
| 9. LibreOffice Converter Engine | v1.2 | 2/2 | Complete | 2026-07-09 |
| 10. Document Worker & Reconciler Integration | v1.2 | 4/4 | Complete | 2026-07-09 |
| 11. API Routing & End-to-End Document Conversion | v1.2 | 4/4 | Complete | 2026-07-10 |
| 12. Tech Debt Cleanup | v1.3 | 1/1 | Complete    | 2026-07-10 |
| 13. Cross-Format Conversion & Input Safety | v1.3 | 3/3 | Complete    | 2026-07-10 |
| 14. Validated Conversion Options & PDF/A Export | v1.3 | 3/3 | Complete    | 2026-07-10 |
| 15. HTML→PDF Chromium Engine | v1.3 | 5/5 | Complete    | 2026-07-11 |
| 16. Webhook Delivery Decoupling | v1.3 | 5/5 | Complete | 2026-07-12 |
| 17. Tech Debt Cleanup | v1.4 | 2/2 | Complete | 2026-07-12 |
| 18. Presets | v1.4 | 4/4 | Complete | 2026-07-12 |
| 19. CI Pipeline | v1.4 | 2/2 | Complete | 2026-07-13 |
| 20. Presets REST CRUD & Format Discovery | v1.5 | 2/2 | Complete | 2026-07-13 |
| 21. MCP Server | v1.5 | 3/3 | Complete | 2026-07-13 |
| 22. CFB Classification | v1.5 | 2/2 | Complete | 2026-07-13 |
| 23. veraPDF ISO 19005 Validation | v1.5 | 3/3 | Complete | 2026-07-13 |
| 24. Helm Chart Core & Landmine Closure | v1.6 | 3/3 | Complete | 2026-07-14 |
| 25. MCP Streamable HTTP | v1.6 | 3/3 | Complete | 2026-07-14 |
| 26. Operator System-Presets REST | v1.6 | 2/2 | Complete    | 2026-07-14 |
| 27. KEDA Autoscaling | v1.6 | 3/3 | Complete    | 2026-07-16 |
| 28. Autoscale Load-Proof | v1.6 | 3/3 | Complete    | 2026-07-17 |
| 29. v1.6 Hardening Tail | v1.7 | 3/3 | Complete    | 2026-07-17 |
| 30. Audio Engine Foundation | v1.7 | 3/3 | Complete    | 2026-07-18 |
| 31. Queue, Worker & Routing Integration | v1.7 | 4/4 | Complete    | 2026-07-18 |
| 32. Containerization & Local E2E + RTF Gate | v1.7 | 5/5 | Complete    | 2026-07-18 |
| 33. KEDA/Helm Chart Integration | v1.7 | 3/3 | Complete    | 2026-07-18 |
| 34. AV Engine Foundation | v1.8 | 3/3 | Complete    | 2026-07-20 |
| 35. Queue, Worker & Routing Integration | v1.8 | 0/TBD | Not started | - |
| 36. Containerization & RTF-Measured Timeout | v1.8 | 0/TBD | Not started | - |
| 37. KEDA/Helm Chart Integration | v1.8 | 0/TBD | Not started | - |

---

*v1.7 shipped 2026-07-18. v1.8 (AV Engine — video/ffmpeg) roadmapped 2026-07-19, Phases 34-37. Next: `/gsd:plan-phase 34`.*
