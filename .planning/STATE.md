---
gsd_state_version: 1.0
milestone: v1.8
milestone_name: AV Engine (video/ffmpeg)
status: BLOCKED — Task 1 (SC3 av scale-from-zero) failing at STEP 6 x2; Task 2/3 not attempted; see 37-03-SUMMARY.md
stopped_at: Phase 37 Plan 03 BLOCKED at Task 1 (SC3 av scale-from-zero, keda-av-loadproof.sh STEP 6 failed x2) - Task 2/3 not attempted, AVE-05 open
last_updated: "2026-07-23T20:04:25.404Z"
last_activity: 2026-07-23
progress:
  total_phases: 4
  completed_phases: 4
  total_plans: 18
  completed_plans: 18
  percent: 100
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-07-19 after v1.8 milestone start)

**Core value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML, аудио, видео) и получить результат — без риска для стабильности или безопасности продакшена.
**Current focus:** Phase 37 — keda-helm-chart-integration

## Current Position

Phase: 37 (keda-helm-chart-integration) — EXECUTING
Plan: 3 of 3
Status: BLOCKED — Task 1 (SC3 av scale-from-zero) failing at STEP 6 x2; Task 2/3 not attempted; see 37-03-SUMMARY.md
Last activity: 2026-07-23

## Performance Metrics

**Velocity:**

- Total plans completed: 99 (all v1.0–v1.7)
- Average duration: - min
- Total execution time: 0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01 | 4 | - | - |
| 02 | 3 | - | - |
| 03 | 3 | - | - |
| 04 | 5 | - | - |
| 05 | 1 | - | - |
| 06 | 4 | - | - |
| 07 | 2 | - | - |
| 08 | 2 | - | - |
| 09 | 2 | - | - |
| 10 | 4 | - | - |
| 11 | 4 | - | - |
| 12 | 1 | - | - |
| 13 | 3 | - | - |
| 14 | 3 | - | - |
| 15 | 5 | - | - |
| 16 | 5 | - | - |
| 17 | 2 | - | - |
| 18 | 4 | - | - |
| 19 | 2 | - | - |
| 20 | 2 | - | - |
| 21 | 3 | - | - |
| 22 | 2 | - | - |
| 23 | 3 | - | - |
| 26 | 2 | - | - |
| 27 | 3 | - | - |
| 28 | 3 | - | - |
| 29 | 3 | - | - |
| 30 | 3 | - | - |
| 31 | 4 | - | - |
| 32 | 5 | - | - |
| 33 | 3 | - | - |
| 34 | 3 | - | - |
| 35 | 7 | - | - |
| 36 | 5 | - | - |
| 37 | TBD | - | - |

**Recent Trend:**

- Last 5 plans: -
- Trend: -

*Updated after each plan completion*
| Phase 36 P01 | 15min | 3 tasks | 6 files |
| Phase 36 P02 | 30min | 2 tasks | 2 files |
| Phase 36 P03 | 20min | 3 tasks | 3 files |
| Phase 36 P04 | 25min | 1 tasks | 4 files |
| Phase 36 P05 | 20min | 2 tasks | 9 files |
| Phase 37 P01 | 25min | 3 tasks | 4 files |
| Phase 37 P02 | 30min | 2 tasks | 3 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table. v1.8-specific decisions surfaced by research, to be recorded as Key Decisions before/at implementation:

- **Key Decision (resolved before roadmapping) — Video→transcript implementation path:** extend `AudioConverter.Pairs()` with video-container × transcript-target pairs, routing those jobs onto the existing `audio` queue/worker (`Engine()` stays `EngineAudio`); the ffmpeg audio-normalize stage already demuxes any container ffmpeg can decode, video or not. Rejected alternative: baking a second ffmpeg-extract→whisper-transcribe pipeline inside av-worker (duplicates ~400MB whisper.cpp+model image weight, doubles the RTF-measurement/GO-NO-GO burden, couples two CPU-bound classes' resource ceilings). Do NOT resolve this decision differently later — it is a hard input to Phase 35's routing and pair-disjointness test.
- **Phase ordering — opts-allowlist before RTF measurement (Phases 34/35 before Phase 36):** a single RTF fixture does not generalize across video's codec × resolution × preset space the way it did for whisper's roughly content-invariant RTF; the `AVOpts` allowlist and the timeout measurement matrix must be designed together, sequenced, not independent tasks. Measuring first and opening opts later silently reintroduces this pitfall.
- **Stage-aware terminal/transient classification cannot be copied from audio (Phase 35):** audio's `isAudioTerminal` treats ffmpeg-stage timeout as terminal because ffmpeg is audio's *cheap* normalize step; for video transcode, ffmpeg IS the expensive operation, so the classification must be re-derived per av feature (transcode: timeout stays transient; thumbnail/extract: closer to audio's terminal-on-timeout profile).
- **New resource axis — disk-space/ephemeral-storage guard (Phase 36):** video decode/transcode has no prior codebase precedent for a disk-space ceiling (no earlier engine class needed one); must be sized explicitly during containerize/measure, not assumed-safe by analogy to CPU/RAM guards.
- **FFmpeg decoder RCE surface — pin ffmpeg ≥8.1.2, not floating `apt-get install` (Phase 36):** a live June-2026 disclosure (CVE-2026-8461 "PixelSmash") achieved RCE via a 50KB crafted file against comparable services; this project has no existing dependency-advisory-tracking process — flagged as accepted-risk/tech-debt to revisit, not assumed durable after a one-time pin.
- **Open question carried into planning — upload-size ceiling for video:** `MAX_UPLOAD_BYTES` is currently one global value enforced before content-type detection runs; video files are legitimately much larger than other engine classes' typical inputs, and raising the global ceiling weakens DoS posture for all other classes too. Must be an explicit named decision during Phase 34/36 planning, not an implicit side effect of picking a video-friendly number.
- [Phase 36]: avDiskSafetyFactorDefault=3.0 is an explicit [ASSUMED] Claude's Discretion default, overridable via AV_DISK_SAFETY_FACTOR — No measured ffmpeg disk-usage ratio existed at Plan 01 time to derive a better default from
- [Phase 36]: lavfi/testsrc/sine/wrapped_avframe + format/aformat/aresample/zlib/webp-muxer added to Dockerfile.av-worker's minimal ffmpeg build — beyond RESEARCH.md's flag list; required for the RTF measurement script's lavfi fixture synthesis AND for production audio-resample/png/webp-thumbnail paths, confirmed via live smoke test of the full av.go argv suite
- [Phase 36]: audio-worker's own env block also needed the IN-02 AV_MAX_RETRY/AV_ENGINE_TIMEOUT parity pair (constructs queue.NewClient() too) -- caught by the plan's own grep-count-8 acceptance criterion before commit — IN-02 parity must hold across every queue.NewClient()-constructing process, not just the ones named in prose
- [Phase 36]: AV_MAX_DURATION_SECONDS/AV_DISK_SAFETY_FACTOR excluded from IN-02 docker-compose parity — only av-worker's own process reads them; queue.NewClient() never touches either var, so parity does not apply
- [Phase 36]: Worst-case RTF cell is hevc@1080 (p95=4.179133s), NOT VP9 as D-09 assumed -- HEVC dominates VP9 by 1.86x
- [Phase 36]: Path B NO-GO lever selected: AV_MAX_DURATION_SECONDS lowered 14400s->90s, deriving AV_ENGINE_TIMEOUT=753s; Path A (VP9 tuning) rejected as ineffective
- [Phase 36]: Passthrough residual disposition (b): resolution_height==0 re-encode bound to <=1080p source height, fail-closed reject, closing the hevc@2160p OOM-DoS vector
- [Phase 36]: AV_WORKER_CONCURRENCY=1, memory=1g validated from measured peak-RSS/CPU-saturation data
- [Phase 36]: Generalized enforceNoScalePassthroughBound -> enforceReencodeSourceBound: every re-encode (no-scale AND explicit resolution_height alike) bounded on BOTH source Height (>1080) and Width (>1920), closing CR-01/HI-01
- [Phase 36]: ErrAVReencodeResolutionExceeded classified terminal in isAVTerminal (predecessor sentinel lacked this classification -- Rule 2 fix)
- [Phase 37]: 37-01: av chart substrate cloned from audio precedent verbatim per 37-CONTEXT.md D-01..D-06 (grace 783s, non-null stabilization 900s, WR-01 triad, keda.av capacity parity); SC2 confirmed already-satisfied by Phase 35 collector, no code change
- [Phase 37]: 37-02: two av live-proof gate scripts authored (keda-av-loadproof.sh SC3 scale-from-zero, keda-av-downscale-survival.sh SC4 downscale-survival) + values-loadproof.yaml keda.av.scaleDownStabilizationSeconds:15 override, statically verified; AVE-05 deliberately left incomplete pending Plan 03's live run

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260712-cqg | Fix Phase 16 verification gaps CR-01/WR-01: advisory-lock connection lifecycle (Release pool slot on TryAcquire error; add PGAdvisoryLock.Close and call at webhook-worker shutdown) | 2026-07-12 | 1f8b22b | [260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-](./quick/260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-/) |
| 260719-2f5 | OpenAPI 3.1 спека API (docs/openapi.yaml, 14 операций, ApiKey auth, multipart POST /v1/jobs с аудио-примерами) для импорта в Yaak + docs/README.md; redocly lint 0 errors | 2026-07-19 | 43799af | [260719-2f5-openapi-3-1-api-octoconv-docs-yaak](./quick/260719-2f5-openapi-3-1-api-octoconv-docs-yaak/) |

### Pending Todos

- ~~Plan Phase 34 (AV Engine Foundation)~~ — done; all 3 plans executed and code-reviewed (2026-07-20).

**Hard inputs to Phase 35 planning, all originating in 34-REVIEW.md / 34-REVIEW-FIX.md:**

- **Wire `SniffVideo` into the handlers chain in the SAME change that registers `AVConverter`.** WR-02 was deliberately skipped in Phase 34 as a scope fence: mp4/mov/avi went live via the `signatures` table, but `SniffVideo` (the mkv/webm EBML path) has zero production callers, so mkv/webm are currently undetectable. Registering the converter without wiring the sniffer ships a converter for formats the service cannot recognize.
- **Map `ErrAVTimecodeOutOfRange` to 4xx, not 5xx.** Operator-accepted contract decision (2026-07-20): an explicit out-of-range thumbnail timecode is a hard client error, deliberately NOT clamped (silently retargeting a client request is the CR-01/CR-02 defect class). No API-layer mapping exists yet — without it a client typo surfaces as an internal error.
- **`AVOpts.Timecode` is `*float64`, accepted as-is (2026-07-20).** nil = unset → default `min(1.0, dur/2)`; explicit `0` = honored as frame 0; explicit out-of-range = hard error; `NaN`/`±Inf` rejected at parse. Note the default is duration-relative, not a fixed 1.0s — two sources with the same request yield frames at different positions. Accepted knowingly; revisit only if API docs need a deterministic rule.
- **IN-01 (Info tier, unfixed) composes cleanly with the `probeVideoStreams` refactor** — fold it into the Phase 35 plan rather than leaving it loose.

### Blockers/Concerns

currently blocking. Sequencing carried into the roadmap:

- Hard-ordered spine: 34 (independent, first) → 35 → 36 (RTF go/no-go gate) → 37. Phase 36's measured RTF/`AV_ENGINE_TIMEOUT` is a hard input to Phase 37's KEDA cooldown/stabilization/grace-period tuning — keep this ordering.
- Phase 34's `AVOpts` allowlist must be closed before Phase 36 measures the RTF matrix — the opts scope determines the measurement matrix bounds (codec × resolution × preset), not the other way around.
- Phase 35's pair-disjointness test between `AVConverter.Pairs()` and `AudioConverter.Pairs()` is a hard requirement, not optional polish — this is the first time two converters in this codebase share a source-format family, and the registry's "later registration wins silently" semantics is a genuine hazard here.
- `-protocol_whitelist file,crypto` must land on every ffmpeg/ffprobe invocation from Phase 34's first commit (day-one exec-hardening scope, not deferred) — closes an SSRF/LFI vector (HLS/concat/subtitle references embedded in file content) that the existing `"file:"`-prefix argv hardening does not cover.
- Operational discipline (OrbStack): pre-build all images sequentially with non-`latest` tags; never run compose and k8s stacks hot simultaneously (four confirmed daemon wedges on record, per v1.6/v1.7 history).
- `MAX_UPLOAD_BYTES` (global 100 MiB default) will likely 413 legitimate video uploads — decide a per-format/engine ceiling deliberately during Phase 34/36, do not let it fail silently (carried from research Gaps to Address).
- Phase 37 Plan 03 Task 1 (SC3 av scale-from-zero) BLOCKED: keda-av-loadproof.sh failed twice at STEP 6 (av-worker Deployment never settles to 0 replicas within 240s bound, observed replicas=1 both runs). See 37-03-SUMMARY.md for full diagnosis/hypotheses. Task 2/3 not attempted. AVE-05 still open; do not run phase.complete for Phase 37 until a retry passes.

## Deferred Items

Acknowledged at v1.7 close (2026-07-18):

| Category | Item | Status |
|----------|------|--------|
| seed | SEED-001: Lesson-recording analysis (tutors/language schools) | Deferred — not resumed by v1.8 (video processing, not lesson-analysis); still awaiting a future milestone |
| seed | SEED-004: Local k8s + KEDA full-stack validation | Deferred — superseded in practice by per-phase live gates (keda-gate 21/21, load-proofs Phases 28/33); revisit as CI k8s validation (K8SV2-01) |
| tech_debt | WR-05 keda-load-proof.sh BUSY_POD jsonpath defect (kubectl v1.36.2, `deletionTimestamp==""` vs absent key) | Accepted residual (29-REVIEW), empirically confirmed live in Phase 33; forward-fix of the frozen script recommended in a future phase |
| tech_debt | Registry cold-pull time for 682MB audio image | Unmeasurable on OrbStack shared store (Pulling→Pulled ≈0 recorded); measure in a real-registry environment before production KEDA tuning is trusted; same open question now applies to the av-worker image (AVX-02, v2 requirement) |
| tooling_noise | audit-open flags 29-HUMAN-UAT.md (status resolved) and quick task 260712-cqg (complete, SUMMARY present) | Both artifacts closed; audit parser counts them regardless |

Items acknowledged and carried forward at milestone closes (see `.planning/milestones/*-MILESTONE-AUDIT.md` for full detail):

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| tech_debt | docker-compose.yml audit for other stale gaps vs .env.example | Closed as DEBT-05, Phase 12 | v1.0 close (2026-07-08) |
| tech_debt | WR-02: docker-compose.e2e.yml lacks `extra_hosts` on `api` — E2E webhook pair fails on plain-Linux docker | Closed as DEBT-01, Phase 12 | v1.2 close (2026-07-10) |
| tech_debt | WR-03: engine-class string literals duplicated in 4 places — extract exported constants | Closed as DEBT-02, Phase 12 | v1.2 close (2026-07-10) |
| tech_debt | WR-04: E2E HTTP clients lack per-request timeouts | Closed as DEBT-03, Phase 12 | v1.2 close (2026-07-10) |
| tech_debt | gofmt nit in internal/queue/queue_test.go (pre-existing since Phase 9/10) | Closed as DEBT-04, Phase 12 | v1.2 close (2026-07-10) |
| v2_scope | Full ISO 19005 (veraPDF) validation of PDF/A outputs | Closed as PDFA-01/02, Phase 23 | v1.3 requirements definition (2026-07-10) |
| v2_scope | Legacy vs encrypted CFB distinction (directory-stream parsing) | Closed as CFB-01/02, Phase 22 | v1.3 requirements definition (2026-07-10) |
| v2_scope | Custom fonts / extended CJK-RTL coverage for HTML→PDF | Deferred to v2 (DOCV3-03, carried) | v1.3 requirements definition (2026-07-10) |
| accepted_risk | Active anti-DoS by document complexity (sheets/cells/unzipped size) | Accepted residual risk (DOC-V2-05, carried) | v1.2 requirements definition (2026-07-09) |
| accepted_risk | `file://` passive subresource read inside chromium-worker (shared UID nobody) | Accepted residual risk (v1.3 Phase 15) | v1.3 close (2026-07-12) |
| accepted_risk | Hallucination on silence/music (whisper exits 0 with structurally-valid garbage) | Accepted residual risk (Phase 30, carried); applies equally to video→transcript pairs added in v1.8 | v1.7 close (2026-07-18) |
| seed | SEED-001: Lesson-recording analysis for tutors and language schools | Foundation in v1.7 (transcription + JSON timestamp contract); mistake-analysis/deck remains a future milestone, not v1.8 | v1.2 close (2026-07-10) |
| seed | SEED-003: MCP-сервер для OctoConv | ✓ Implemented (v1.5 Phase 21, MCP-01..05) | v1.4 planning (2026-07-12) |
| seed | SEED-004: OctoConv on Kubernetes + KEDA autoscaling | ✓ Delivered (v1.6 Phases 24-28) | v1.6 requirements definition (2026-07-14) |
| infra | k8s-валидация в CI (kind/k3d) | Deferred to v2 (K8SV2-01) | v1.6 requirements definition (2026-07-14) |
| infra | `is_operator` column vs env-allowlist for operators | Deferred to v2 (K8SV2-03) | v1.6 requirements definition (2026-07-14) |
| tech_debt | CACHED-hit log confirmation for CI docker-build (needs gh auth) | Operator-accepted residual | v1.4 close (2026-07-13) |
| ops | Branch-protection required-checks (gate/race/docker-build) — manual GitHub UI step | Open operational follow-up | v1.4 close (2026-07-13) |
| tech_debt | presets D-04 single-active-version: application-transactional only, no DB backstop | Accepted residual | v1.4 close (2026-07-13) |
| seed | SEED-002: Decouple webhook delivery from any specific engine worker binary | ✓ Implemented (v1.3 Phase 16, WEBH-01) | v1.2 close (2026-07-10) |
| v2_scope | AVX-01: Trim/crop as validated closed-opts (start/end timecodes) | Deferred to v2 — only on confirmed demand | v1.8 requirements definition (2026-07-19) |
| v2_scope | AVX-02: Registry cold-pull measurement for heavy images (av-worker) | Deferred to v2 — carries forward the same unmeasured-on-OrbStack gap as the audio image | v1.8 requirements definition (2026-07-19) |

## Session Continuity

Last session: 2026-07-23T20:03:22.144Z
Stopped at: Phase 37 Plan 03 BLOCKED at Task 1 (SC3 av scale-from-zero, keda-av-loadproof.sh STEP 6 failed x2) - Task 2/3 not attempted, AVE-05 open
Resume file: .planning/phases/37-keda-helm-chart-integration/37-03-SUMMARY.md

## Operator Next Steps

- Start Phase 34 with `/gsd:plan-phase 34`
