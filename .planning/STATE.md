---
gsd_state_version: 1.0
milestone: v1.5
milestone_name: MCP Access & Document Fidelity
status: executing
stopped_at: Roadmap complete (Phases 20-23), ready to plan Phase 20
last_updated: "2026-07-13T02:01:33.867Z"
last_activity: 2026-07-13 -- Phase 21 execution started
progress:
  total_phases: 4
  completed_phases: 1
  total_plans: 5
  completed_plans: 2
  percent: 25
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-07-13 after v1.5 milestone start)

**Core value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML) и получить результат — без риска для стабильности или безопасности продакшена.
**Current focus:** Phase 21 — mcp-server

## Current Position

Phase: 21 (mcp-server) — EXECUTING
Plan: 1 of 3
Status: Executing Phase 21
Last activity: 2026-07-13 -- Phase 21 execution started

## Performance Metrics

**Velocity:**

- Total plans completed: 50 (all v1.0–v1.4)
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

**Recent Trend:**

- Last 5 plans: -
- Trend: -

*Updated after each plan completion*

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table. v1.5-specific decisions flagged by research, to be recorded as Key Decisions before/at implementation:

- Phase 20 (Presets REST): `list_presets` scope-merging shape — how to reproduce `Resolve`'s shadow-precedence (client preset hides same-named system preset) across the REST surface (e.g. a `?include_system=true` param merged in the API layer). Not fully designed in any research file; finalize during Phase 20 planning since it determines what the REST surface must expose.
- Phase 22 (CFB): hand-rolled `ClassifyCFB` over a third-party library (mscfb) — a "why we did NOT add a dependency" Key Decision. Requires (1) sector/entry walk bound cross-checked against reader length, (2) visited-sector set for immediate cycle rejection, (3) fuzz target seeded with Phase 13 fixtures + corrupted variants as a phase-exit gate before merge.
- Phase 23 (veraPDF): CLI-per-job vs daemon/server-mode — resolve with a live JVM cold-start measurement before committing to either shape (daemon fallback documented either way); veraPDF severity policy (all non-compliance terminal vs Error-severity only) decided and re-validated against v1.3 PDF/A-2b fixtures before merge; `terminalVeraPDFSignatures` confirmed against a real invocation, not training-data assumptions.
- Phase 21 (MCP): re-verify the pinned `go-sdk` (≥v1.6.1) `mcp.AddTool`/schema-generation API surface against `go.mod` at execution time (SDK actively evolving); verify progress-notification/keepalive/idle-window mechanics live against the actual target MCP host (Claude Code).

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260712-cqg | Fix Phase 16 verification gaps CR-01/WR-01: advisory-lock connection lifecycle (Release pool slot on TryAcquire error; add PGAdvisoryLock.Close and call at webhook-worker shutdown) | 2026-07-12 | 1f8b22b | [260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-](./quick/260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-/) |

### Pending Todos

- Plan Phase 20 (Presets REST CRUD & Format Discovery): PRAPI-01, PRAPI-02, PRAPI-03.

### Blockers/Concerns

None currently blocking. Sequencing note carried into the roadmap: Phase 20 (Presets REST + `GET /v1/formats`) is a hard prerequisite for two of Phase 21 MCP's five tools (`list_presets`, `list_supported_formats`) — MCP holds zero `internal/presets`/`internal/convert` imports, so those endpoints must exist before Phase 21. Phase 22 (CFB) and Phase 23 (veraPDF) are independent of the Presets/MCP track and of each other; veraPDF is sequenced last as the highest-uncertainty item (new JVM runtime, unverified image/latency impact).

## Deferred Items

Items acknowledged and carried forward at milestone closes (see `.planning/milestones/*-MILESTONE-AUDIT.md` for full detail):

| Category | Item | Status | Deferred At |
|----------|------|--------|-------------|
| tech_debt | docker-compose.yml audit for other stale gaps vs .env.example | Closed as DEBT-05, Phase 12 | v1.0 close (2026-07-08) |
| tech_debt | WR-02: docker-compose.e2e.yml lacks `extra_hosts` on `api` — E2E webhook pair fails on plain-Linux docker | Closed as DEBT-01, Phase 12 | v1.2 close (2026-07-10) |
| tech_debt | WR-03: engine-class string literals duplicated in 4 places — extract exported constants | Closed as DEBT-02, Phase 12 | v1.2 close (2026-07-10) |
| tech_debt | WR-04: E2E HTTP clients lack per-request timeouts | Closed as DEBT-03, Phase 12 | v1.2 close (2026-07-10) |
| tech_debt | gofmt nit in internal/queue/queue_test.go (pre-existing since Phase 9/10) | Closed as DEBT-04, Phase 12 | v1.2 close (2026-07-10) |
| v2_scope | Full ISO 19005 (veraPDF) validation of PDF/A outputs | Now PDFA-01/02, mapped to Phase 23 | v1.3 requirements definition (2026-07-10) |
| v2_scope | Legacy vs encrypted CFB distinction (directory-stream parsing) | Now CFB-01/02, mapped to Phase 22 | v1.3 requirements definition (2026-07-10) |
| v2_scope | Custom fonts / extended CJK-RTL coverage for HTML→PDF | Deferred to v2 (DOCV3-03, carried) | v1.3 requirements definition (2026-07-10) |
| accepted_risk | Active anti-DoS by document complexity (sheets/cells/unzipped size) | Accepted residual risk (DOC-V2-05, carried) | v1.2 requirements definition (2026-07-09) |
| seed | SEED-001: Lesson-recording analysis for tutors and language schools | Dormant | v1.2 close (2026-07-10) |
| seed | SEED-003: MCP-сервер для OctoConv | Now MCP-01..05, mapped to Phase 21 | v1.4 planning (2026-07-12) |
| tech_debt | CACHED-hit log confirmation for CI docker-build (needs gh auth) | Operator-accepted residual | v1.4 close (2026-07-13) |
| ops | Branch-protection required-checks (gate/race/docker-build) — manual GitHub UI step | Open operational follow-up | v1.4 close (2026-07-13) |
| tech_debt | presets D-04 single-active-version: application-transactional only, no DB backstop | Accepted residual | v1.4 close (2026-07-13) |
| seed | SEED-002: Decouple webhook delivery from any specific engine worker binary | ✓ Implemented (v1.3 Phase 16, WEBH-01) | v1.2 close (2026-07-10) |

## Session Continuity

Last session: 2026-07-13 — v1.5 roadmap created
Stopped at: Roadmap complete (Phases 20-23), ready to plan Phase 20
Resume file: .planning/ROADMAP.md

## Operator Next Steps

- Run `/gsd:plan-phase 20` to plan the first v1.5 phase (Presets REST CRUD & Format Discovery).
