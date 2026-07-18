# Roadmap: OctoConv

## Milestones

- ✅ **v1.0 Hardening MVP** — Phases 1-4 (shipped 2026-07-08) — see `.planning/milestones/v1.0-ROADMAP.md`
- ✅ **v1.1 Tech Debt Cleanup** — Phases 5-7 (shipped 2026-07-08) — see `.planning/milestones/v1.1-ROADMAP.md`
- ✅ **v1.2 Document Engine Class** — Phases 8-11 (shipped 2026-07-10) — see `.planning/milestones/v1.2-ROADMAP.md`
- ✅ **v1.3 Document Class v2** — Phases 12-16 (shipped 2026-07-12) — see `.planning/milestones/v1.3-ROADMAP.md`
- ✅ **v1.4 CI, Presets & Debt Cleanup** — Phases 17-19 (shipped 2026-07-13) — see `.planning/milestones/v1.4-ROADMAP.md`
- ✅ **v1.5 MCP Access & Document Fidelity** — Phases 20-23 (shipped 2026-07-13) — see `.planning/milestones/v1.5-ROADMAP.md`
- ✅ **v1.6 Kubernetes & KEDA** — Phases 24-28 (shipped 2026-07-17) — see `.planning/milestones/v1.6-ROADMAP.md`
- ⏳ **v1.7 Audio Engine & Hardening** — Phases 29-33 (in progress, started 2026-07-17)

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

### v1.7 Audio Engine & Hardening (Phases 29-33) — IN PROGRESS

- [x] **Phase 29: v1.6 Hardening Tail** - Close WR-01 empty-PromQL semantics, OPER-01 compose passthrough + live gate, gate-tooling warnings, K8S-02 direct-dial recheck (completed 2026-07-17)
- [x] **Phase 30: Audio Engine Foundation** - Standalone AudioConverter (ffmpeg→whisper-cli), magic-bytes (ID3v2-aware) + duration validation, txt/srt/vtt/json Pairs + JSON timestamp contract, validated AudioOpts (completed 2026-07-18)
- [ ] **Phase 31: Queue, Worker & Routing Integration** - audio queue + cmd/audio-worker, stage-aware timeout classification, AudioUniqueTTL, API/reconciler engine routing
- [ ] **Phase 32: Containerization & Local E2E + RTF Gate** - Dockerfile.audio-worker (whisper.cpp from source, baked model), compose service, CI bake, measured RTF→timeout go/no-go
- [ ] **Phase 33: KEDA/Helm Chart Integration** - audio-worker Deployment + ScaledObject, QueueAudio collector, scale-from-zero load-proof with baked model

## Phase Details

### v1.7 Audio Engine & Hardening

**Milestone goal:** Ship the fourth engine class — offline whisper.cpp audio transcription — by the proven engine-class pattern (dedicated queue/worker/binary, hardened exec, KEDA), plus close the four pre-diagnosed v1.6 hardening-tail items. Transcription only; SEED-001 mistake-analysis is the next milestone and consumes the JSON contract designed here.

**Dependency spine:** 29 (independent, first) → 30 → 31 → 32 (RTF go/no-go gate) → 33 (KEDA tuning consumes 32's measured AUDIO_ENGINE_TIMEOUT).

---

### Phase 29: v1.6 Hardening Tail
**Goal**: Close the four pre-diagnosed v1.6 audit findings so the audio ScaledObject (Phase 33) is authored on a fixed chart substrate and the operator live gate is proven.
**Depends on**: Nothing (zero audio dependency — good opener; WR-01 must land before any audio ScaledObject is written)
**Requirements**: HARD-01, HARD-02, HARD-03, HARD-04
**Success Criteria** (what must be TRUE):
  1. The KEDA trigger no longer treats an empty/absent PromQL result (api unavailable) as "queue empty" — a busy engine class with live backlog cannot be scaled down, via a deliberate documented fix (flip `ignoreNullValues`+accept fallback churn, or keep it and add `absent()` alerting), not a cosmetic comment edit.
  2. An operator can run a live acceptance script against the compose stack that exercises `/v1/system/presets` CRUD as an operator plus a byte-identical no-leak 404 for a non-operator, with `OPERATOR_CLIENT_IDS` passed through the compose api service.
  3. All six gate-tooling warnings from 28-REVIEW are closed (falsy-`0` stabilization in the ScaledObject template, SC3 stale-pod race, false-PASS download check without `-f`, orphaned watcher process, pinned interpreter in render_evidence.py, CWD-independent SAMPLE_IMAGE in gen_heavy_docx.py), each with its script/template diff and a gate re-run.
  4. A presigned result URL resolves from the OrbStack host via a direct dial against a verified-healthy daemon, with no `kubectl port-forward` / `curl --connect-to` workaround.
**Plans**: 3 plans
- [x] 29-01-PLAN.md — Chart robustness offline: ignoreNullValues flip + retry-inclusive PromQL + Prometheus checksum + falsy-0 stabilization (HARD-01, HARD-03 fix #1)
- [x] 29-02-PLAN.md — Operator compose acceptance: OPERATOR_CLIENT_IDS passthrough + system-scope acceptance section, live gate (HARD-02)
- [x] 29-03-PLAN.md — Gate-tooling fixes #2-6 + presigned direct-dial recheck, live keda-gate.sh run (HARD-03 remainder, HARD-04)

### Phase 30: Audio Engine Foundation
**Goal**: A standalone `AudioConverter` transcribes a local audio file to txt/srt/vtt/json with fail-closed content validation, built and testable against the binary before any queue/k8s plumbing.
**Depends on**: Phase 29 (sequencing only — no code dependency; validate the novel external-process piece standalone first)
**Requirements**: AUD-01, AUD-02, AUD-03, AUD-04
**Success Criteria** (what must be TRUE):
  1. A local mp3/wav/m4a/ogg file is validated by magic bytes — including a bespoke ID3v2-aware, variable-offset MP3 detector (synchsafe size skip, not a fixed-window table entry) — and content mismatches are rejected fail-closed before any S3 write.
  2. `AudioConverter` transcribes a validated file through the two-stage ffmpeg→whisper-cli pipeline (one `AUDIO_ENGINE_TIMEOUT`-bounded context) to any of txt/srt/vtt/json selected via the existing `Pair` mechanism.
  3. `target=json` output carries segment- and word-level start/end/text timestamps, with the schema verified live against the pinned `whisper-cli` v1.9.1 binary (SEED-001 forward-compatibility hinge).
  4. `AudioOpts{language (closed allowlist), translate}` flows through the validated-opts pattern (OPTS-01 precedent) — an injection test proves client bytes never reach the engine argv.
  5. An input whose ffprobe-measured duration exceeds `AUDIO_MAX_DURATION_SECONDS` is rejected with a predictable terminal/422 (audio analog of the image decompression-bomb guard), and hallucination-on-silence is recorded as an accepted residual risk in the phase decision log.
**Plans**: 3 plans
- [x] 30-01-PLAN.md — Dev setup (whisper-cli v1.9.1 + SHA-256 model) + audio content validation: ID3v2-aware magic bytes + ffprobe duration guard (AUD-01, AUD-04)
- [x] 30-02-PLAN.md — EngineAudio const + validated AudioOpts (closed language allowlist, injection test) (AUD-03)
- [x] 30-03-PLAN.md — AudioConverter two-stage ffmpeg→whisper-cli pipeline + live-verified JSON timestamp schema (AUD-02)

### Phase 31: Queue, Worker & Routing Integration
**Goal**: Audio jobs flow end-to-end through the async pipeline with correct retry/dedup and engine-routing semantics.
**Depends on**: Phase 30 (needs the working converter to wire into the async lifecycle)
**Requirements**: AUD-05
**Success Criteria** (what must be TRUE):
  1. An uploaded audio job is routed to a dedicated `audio` asynq queue by the API's `EngineFor` content-detection path and is consumed end-to-end by `cmd/audio-worker` (queued → active → done), with the reconciler routing stranded audio jobs to the same queue.
  2. A whisper-stage timeout on already-duration-validated audio is classified transient (asynq retries with fresh CPU), while an ffmpeg-stage failure on malformed input is terminal — verified by a stage-aware classifier unit test (Key Decision: stage-aware split, not blanket-terminal).
  3. `AudioUniqueTTL` is derived fresh from the audio timeout/retry budget (never reused from image/document) and a dedicated test asserts it strictly exceeds the worst-case audio attempt lifetime, closing the T-03-10 double-processing race.
  4. `RECONCILER_ACTIVE_STALE_AFTER` for audio is set above `AUDIO_ENGINE_TIMEOUT` and a test confirms repeated sweep ticks against a long in-flight audio job fire zero spurious `reconciler_recovery` events.
**Plans**: TBD

### Phase 32: Containerization & Local E2E + RTF Gate
**Goal**: A running audio-worker container in docker-compose passes a full live E2E, with `AUDIO_ENGINE_TIMEOUT` sized from a measured realtime-factor go/no-go gate rather than a copied constant.
**Depends on**: Phase 31 (needs the queue/worker contract stable before containerizing)
**Requirements**: AUD-06, AUD-07
**Success Criteria** (what must be TRUE):
  1. `Dockerfile.audio-worker` builds whisper.cpp v1.9.1 from source multi-stage with `-DGGML_NATIVE=OFF`, bakes the `base` model pinned by SHA-256, installs pinned apt ffmpeg, and the image is added to the CI bake matrix with its `AUDIO_ENGINE_TIMEOUT`/`AUDIO_WORKER_CONCURRENCY`/ShutdownTimeout env wired.
  2. An `audio-worker` compose service transcribes an uploaded file end-to-end through the live compose stack (upload → poll → presigned transcript download) with a signed webhook confirmed.
  3. RTF is measured on the real resource-limited container and drives a documented go/no-go decision that sizes `AUDIO_ENGINE_TIMEOUT` (veraPDF Phase 23 precedent) — this measured timeout is the hard input to Phase 33's KEDA tuning.
  4. whisper.cpp `--threads` is pinned to the container's cgroup CPU limit (not host core count) and `AUDIO_WORKER_CONCURRENCY` is set from measured per-job CPU/RSS footprint (likely 1), verified against the container `cpus`/`memory` ceiling.
**Plans**: TBD

### Phase 33: KEDA/Helm Chart Integration
**Goal**: The audio class autoscales in the cluster with production parity to the other three classes, and scale-from-zero is live-proven with the baked model.
**Depends on**: Phase 32 (KEDA cooldown/stabilization and grace-period tuning consume Phase 32's measured `AUDIO_ENGINE_TIMEOUT`)
**Requirements**: AUD-08
**Success Criteria** (what must be TRUE):
  1. An `audio-worker` Deployment plus a KEDA `ScaledObject` ship in the chart with `scaleDownStabilizationSeconds` above the worst-case job duration and the WR-01 fix (Phase 29) applied from the first commit.
  2. `QueueAudio` is registered in the always-on api queue-depth collector so KEDA resolves the audio backlog at genuinely zero replicas.
  3. Scale-from-zero is live-proven for the audio class with the model baked into the image, capturing timestamped Phase-28-style evidence that measures image-pull vs scale-from-zero cold-start (bake-vs-volume treated as a reversible, measured decision).
  4. `terminationGracePeriodSeconds` for the audio class exceeds `AUDIO_ENGINE_TIMEOUT` so a long transcription survives a genuine N→N-1 HPA downscale without a premature SIGTERM.
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
| 30. Audio Engine Foundation | v1.7 | 3/3 | Complete   | 2026-07-18 |
| 31. Queue, Worker & Routing Integration | v1.7 | 0/? | Not started | - |
| 32. Containerization & Local E2E + RTF Gate | v1.7 | 0/? | Not started | - |
| 33. KEDA/Helm Chart Integration | v1.7 | 0/? | Not started | - |

---

*v1.6 shipped 2026-07-17. v1.7 (Audio Engine & Hardening) roadmapped 2026-07-17, Phases 29-33. Next: `/gsd:plan-phase 29`.*
