---
gsd_state_version: 1.0
milestone: v1.7
milestone_name: Audio Engine & Hardening
status: roadmapped
last_updated: "2026-07-17T17:04:49.402Z"
last_activity: 2026-07-17
progress:
  total_phases: 5
  completed_phases: 0
  total_plans: 0
  completed_plans: 0
  percent: 0
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-07-17 after v1.7 milestone start)

**Core value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML, аудио) и получить результат — без риска для стабильности или безопасности продакшена.
**Current focus:** Phase 29 — v1.6 Hardening Tail (roadmap created, ready to plan)

## Current Position

Phase: 29 — v1.6 Hardening Tail (Not started)
Plan: —
Status: Roadmap created (Phases 29-33), ready for `/gsd:plan-phase 29`
Last activity: 2026-07-17 — v1.7 roadmap created, 12/12 requirements mapped

## Performance Metrics

**Velocity:**

- Total plans completed: 66 (all v1.0–v1.6)
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

**Recent Trend:**

- Last 5 plans: -
- Trend: -

*Updated after each plan completion*

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table. v1.7-specific decisions surfaced by research, to be recorded as Key Decisions before/at implementation:

- **Key Decision 1 — Stage-aware timeout classification (Phase 31, MUST resolve before `isAudioTerminal` is written):** ffmpeg-stage failure/timeout → terminal (malformed/adversarial input signal, same rationale as the image dimension-bomb guard); whisper-stage timeout on audio that already passed the ffprobe duration/format check → transient (mirror the image engine's classifier, no `context.DeadlineExceeded` terminal arm), bounded by `AUDIO_MAX_RETRY`. FEATURES.md/ARCHITECTURE.md recommended blanket-terminal (document precedent); PITFALLS.md's stage-aware split adopted as strictly-more-correct without added complexity. Do NOT copy-paste from a sibling class.
- **Key Decision 2 — Model choice base vs small (Phase 32):** ship `base` (142 MiB) as the default with `small` as a later values/preset opt-in; keep the choice reversible (build-arg/preset variant), do not lock permanently in the Dockerfile with no revisit trigger. Bundling both by default is rejected (pushes image toward ~1GB, hurts Key Decision 3).
- **Key Decision 3 — Model distribution bake-in vs volume (Phase 33):** bake-in is simplest and matches the offline constraint, but risks silently defeating the scale-from-zero SLA Phases 27/28 proved. Require a Phase-28-style timestamped load-proof for the audio class specifically (image-pull vs scale-from-zero measured) before calling KEDA integration done; treat bake-vs-volume as reversible based on that measurement.
- **AudioUniqueTTL (Phase 31):** derive fresh from the real `AUDIO_ENGINE_TIMEOUT`/`AUDIO_MAX_RETRY` (never reuse image/document TTL) — the T-03-10 double-processing race is worst for the most expensive class; ship `TestAudioUniqueTTL` mirroring the three existing monotonicity/lower-bound tests.
- **whisper.cpp threads/concurrency (Phase 32):** pass explicit `--threads` sized to the container's cgroup CPU limit (not host core count); size `AUDIO_WORKER_CONCURRENCY` from measured per-job RSS (likely 1). `-DGGML_NATIVE=OFF` is load-bearing (default `-march=native` SIGILLs on non-build-host CPUs).
- **Accepted residual risk — hallucination on silence/music (Phase 30):** whisper exits 0 with structurally-valid garbage; no terminal-signature classifier catches it. Document as accepted residual risk; surface no-speech-probability in the JSON contract if the pinned binary exposes it cleanly (cheapest mitigation).

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260712-cqg | Fix Phase 16 verification gaps CR-01/WR-01: advisory-lock connection lifecycle (Release pool slot on TryAcquire error; add PGAdvisoryLock.Close and call at webhook-worker shutdown) | 2026-07-12 | 1f8b22b | [260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-](./quick/260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-/) |

### Pending Todos

- Plan Phase 29 (v1.6 Hardening Tail): HARD-01, HARD-02, HARD-03, HARD-04. Zero audio dependency, all four independent and pre-diagnosed by 26/27/28-REVIEW — cheap, mechanical opener. WR-01 must land here so the audio ScaledObject (Phase 33) is authored correctly from its first commit.

### Blockers/Concerns

None currently blocking. Sequencing carried into the roadmap:

- Hard-ordered spine: 29 (independent, first) → 30 → 31 → 32 (RTF go/no-go gate) → 33. AUD-07's measured RTF/`AUDIO_ENGINE_TIMEOUT` (Phase 32) is a hard input to AUD-08's KEDA cooldown/stabilization/grace-period tuning (Phase 33) — keep this ordering.
- WR-01 (Phase 29) must precede authoring `scaledobject-audio.yaml` (Phase 33) — else a known-bad chart pattern gets copied into the new audio ScaledObject.
- Two research phases likely need execution-time research (`--research-phase`): Phase 30 (whisper-cli v1.9.1 JSON schema field names verified live against the pinned binary; MP3 ID3v2 synchsafe-decode correctness) and Phase 32 (RTF/thread/memory sizing empirically measured against the real container `cpus`/`memory` limits). Phase 33 may need a scoped pass on init-container/PVC patterns if measured bake-in pull time proves unacceptable.
- Operational discipline (OrbStack): pre-build all images sequentially with non-`latest` tags; never run compose and k8s stacks hot simultaneously (four confirmed daemon wedges on record). A GB-scale audio image raises cold-pull time — measure it, do not assume it generalizes from the other three classes.
- `MAX_UPLOAD_BYTES` (global 100 MiB default) may 413 legitimate long audio (an uncompressed 1-hour WAV is >600MB) — decide a per-format/engine ceiling deliberately during Phase 30/32, do not let it fail silently.

## Deferred Items

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
| seed | SEED-001: Lesson-recording analysis for tutors and language schools | Foundation in v1.7 (transcription + JSON timestamp contract); mistake-analysis/deck = next milestone (LESN-01/02) | v1.2 close (2026-07-10) |
| seed | SEED-003: MCP-сервер для OctoConv | ✓ Implemented (v1.5 Phase 21, MCP-01..05) | v1.4 planning (2026-07-12) |
| seed | SEED-004: OctoConv on Kubernetes + KEDA autoscaling | ✓ Delivered (v1.6 Phases 24-28) | v1.6 requirements definition (2026-07-14) |
| infra | k8s-валидация в CI (kind/k3d) | Deferred to v2 (K8SV2-01) | v1.6 requirements definition (2026-07-14) |
| infra | `is_operator` column vs env-allowlist for operators | Deferred to v2 (K8SV2-03) | v1.6 requirements definition (2026-07-14) |
| tech_debt | CACHED-hit log confirmation for CI docker-build (needs gh auth) | Operator-accepted residual | v1.4 close (2026-07-13) |
| ops | Branch-protection required-checks (gate/race/docker-build) — manual GitHub UI step | Open operational follow-up | v1.4 close (2026-07-13) |
| tech_debt | presets D-04 single-active-version: application-transactional only, no DB backstop | Accepted residual | v1.4 close (2026-07-13) |
| seed | SEED-002: Decouple webhook delivery from any specific engine worker binary | ✓ Implemented (v1.3 Phase 16, WEBH-01) | v1.2 close (2026-07-10) |

## Session Continuity

Last session: 2026-07-17 — v1.7 roadmap created (Phases 29-33)
Stopped at: Roadmap created, 12/12 requirements mapped
Resume file: .planning/ROADMAP.md (Phase Details → v1.7)

## Operator Next Steps

- Plan the first phase: `/gsd:plan-phase 29`
