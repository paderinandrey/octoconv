---
gsd_state_version: 1.0
milestone: v1.4
milestone_name: CI, Presets & Debt Cleanup
status: executing
stopped_at: Roadmap complete (Phases 17-19), ready to plan Phase 17
last_updated: "2026-07-12T18:01:02.292Z"
last_activity: 2026-07-12 -- Phase 18 execution started
progress:
  total_phases: 3
  completed_phases: 1
  total_plans: 6
  completed_plans: 2
  percent: 33
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-07-12 after v1.3 milestone)

**Core value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML) и получить результат — без риска для стабильности или безопасности продакшена.
**Current focus:** Phase 18 — presets

## Current Position

Phase: 18 (presets) — EXECUTING
Plan: 1 of 4
Status: Executing Phase 18
Last activity: 2026-07-12 -- Phase 18 execution started

## Performance Metrics

**Velocity:**

- Total plans completed: 42 (all v1.0 + v1.1 + v1.2)
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

**Recent Trend:**

- Last 5 plans: -
- Trend: -

*Updated after each plan completion*

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table. v1.4-specific decisions flagged by research, to be recorded as Key Decisions before implementation:

- Phase 18 (Presets): preset version-resolution semantics (single-active-version-per-name vs. multiple-simultaneously-active) and scope-precedence lookup order (client shadows system) — schema/behavior design decisions specific to this DDL; research flags both as "must be a written decision, not inferred behavior."
- Phase 18 (Presets): `worker.NewHandler` / preset-resolution constructor signature impact — verify during phase planning.
- Phase 19 (CI): GHA cache sizing/scoping across 5 images and repo visibility (public 4vCPU/16GB vs private 2vCPU/8GB) for the live-E2E job — confirm before finalizing resource assumptions.

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260712-cqg | Fix Phase 16 verification gaps CR-01/WR-01: advisory-lock connection lifecycle (Release pool slot on TryAcquire error; add PGAdvisoryLock.Close and call at webhook-worker shutdown) | 2026-07-12 | 1f8b22b | [260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-](./quick/260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-/) |

### Pending Todos

- Plan Phase 17 (Tech Debt Cleanup): DEBT-06, DEBT-07, DEBT-08.

### Blockers/Concerns

None currently blocking. Sequencing note carried into the roadmap: Phase 17's DEBT-07 (fakeEnqueuer race fix) gates Phase 19's `-race` CI tier, and DEBT-08 (image E2E test) gates Phase 19's live-E2E tier — enabling those tiers before the prerequisites land produces a red/blind pipeline.

## Deferred Items

Items acknowledged and carried forward at milestone closes (see `.planning/milestones/v1.0-MILESTONE-AUDIT.md`, `v1.1-MILESTONE-AUDIT.md`, `v1.2-MILESTONE-AUDIT.md` for full detail):

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| tech_debt | docker-compose.yml audit for other stale gaps vs .env.example | Closed as DEBT-05, Phase 12 | v1.0 close (2026-07-08) |
| tech_debt | WR-02: docker-compose.e2e.yml lacks `extra_hosts` on `api` — E2E webhook pair fails on plain-Linux docker | Closed as DEBT-01, Phase 12 | v1.2 close (2026-07-10) |
| tech_debt | WR-03: engine-class string literals duplicated in 4 places — extract exported constants | Closed as DEBT-02, Phase 12 | v1.2 close (2026-07-10) |
| tech_debt | WR-04: E2E HTTP clients lack per-request timeouts | Closed as DEBT-03, Phase 12 | v1.2 close (2026-07-10) |
| tech_debt | gofmt nit in internal/queue/queue_test.go (pre-existing since Phase 9/10) | Closed as DEBT-04, Phase 12 | v1.2 close (2026-07-10) |
| v2_scope | Full ISO 19005 (veraPDF) validation of PDF/A outputs | Deferred to v2 (DOCV3-01) | v1.3 requirements definition (2026-07-10) |
| v2_scope | Legacy vs encrypted CFB distinction (directory-stream parsing) | Deferred to v2 (DOCV3-02) | v1.3 requirements definition (2026-07-10) |
| v2_scope | Custom fonts / extended CJK-RTL coverage for HTML→PDF | Deferred to v2 (DOCV3-03) | v1.3 requirements definition (2026-07-10) |
| accepted_risk | Active anti-DoS by document complexity (sheets/cells/unzipped size) | Accepted residual risk (DOC-V2-05, carried into v1.3) | v1.2 requirements definition (2026-07-09) |
| seed | SEED-001: Lesson-recording analysis for tutors and language schools | Dormant | v1.2 close (2026-07-10) |
| seed | SEED-002: Decouple webhook delivery from any specific engine worker binary | ✓ Implemented (v1.3 Phase 16, WEBH-01) | v1.2 close (2026-07-10) |
| tech_debt | Dead webhook wiring in cmd/document-worker & cmd/chromium-worker (WR-02/WR-03 из 16-REVIEW) | Now DEBT-06, mapped to Phase 17 | v1.3 close (2026-07-12) |
| tech_debt | fakeEnqueuer data race under full-package -race (internal/reconciler test helpers) | Now DEBT-07, mapped to Phase 17 | v1.3 close (2026-07-12) |
| tech_debt | No dedicated image (libvips) E2E test in internal/e2e | Now DEBT-08, mapped to Phase 17 | v1.3 close (2026-07-12) |

## Session Continuity

Last session: 2026-07-12 — v1.4 roadmap created
Stopped at: Roadmap complete (Phases 17-19), ready to plan Phase 17
Resume file: .planning/ROADMAP.md

## Operator Next Steps

- Plan the first v1.4 phase with `/gsd:plan-phase 17`
