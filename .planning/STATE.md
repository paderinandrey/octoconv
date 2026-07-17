---
gsd_state_version: 1.0
milestone: v1.7
milestone_name: Audio Engine & Hardening
status: planning
last_updated: "2026-07-17T17:04:49.402Z"
last_activity: 2026-07-17
progress:
  total_phases: 0
  completed_phases: 0
  total_plans: 0
  completed_plans: 0
  percent: 0
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-07-14 after v1.6 milestone start)

**Core value:** Внутренние сервисы компании могут безопасно (через аутентификацию по API-ключу) и надёжно поставить задачу конвертации файла (изображения, офисные документы, HTML) и получить результат — без риска для стабильности или безопасности продакшена.
**Current focus:** Milestone complete

## Current Position

Phase: Not started (defining requirements)
Plan: —
Status: Defining requirements
Last activity: 2026-07-17 — Milestone v1.7 started

## Performance Metrics

**Velocity:**

- Total plans completed: 66 (all v1.0–v1.5)
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

Decisions are logged in PROJECT.md Key Decisions table. v1.6-specific decisions surfaced by research, to be recorded as Key Decisions before/at implementation:

- Phase 24 (Helm Chart Core): flat single chart (no subcharts, no Bitnami/MinIO-Operator deps) — hand-roll Postgres/Redis/MinIO as plain Deployment+PVC+Service matching the compose file's non-HA shape. `METRICS_ADDR=0.0.0.0` bind change and its compensating NetworkPolicy must ship together, never as a follow-up. `S3_ENDPOINT` uses the FQDN `<service>.<namespace>.svc.cluster.local` form. Per-engine-class `terminationGracePeriodSeconds` derived from real worst-case timeouts (image ≥120s, document ≥300s, html ≥60s), never the 30s default.
- Phase 25 (MCP HTTP): per-request caller-key pass-through auth (Decision 2, resolved — not a single pod-held key). Open Key Decision to fix at planning: `local_path` contract gap for remote callers — three options (omit in HTTP mode / presigned-only / download-proxy tool), no default recommended. Live-verify go-sdk v1.6.1 `Stateless: true` + progress-notification streaming (LOW confidence).
- Phase 26 (Operator presets REST): `OPERATOR_CLIENT_IDS` env allowlist + 404-no-leak (Decision 1, resolved — not an `is_operator` column + 403). Document `is_operator` column as future option (K8SV2-03).
- Phase 27 (KEDA): Prometheus scaler against relocated `octoconv_queue_depth` (never asynq's internal Redis list keys). Queue-depth exposition relocation (KEDA-01) is the phase's first plan and a hard prerequisite for any ScaledObject. webhook-worker excluded from KEDA entirely — fixed `replicas: 2` (sole host of the advisory-lock sweeper). Per-class `pollingInterval`/`cooldownPeriod` tuning needs execution-time research (demo defaults are starting points only).
- Phase 28 (Load-Proof): timestamped 0→N→0 evidence is a hard deliverable; the 0→N leg must be proven with the worker at genuine 0 replicas (easy to fake otherwise). Scale-down soak with a long document job in flight validates Phase 24's grace-period choice.

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260712-cqg | Fix Phase 16 verification gaps CR-01/WR-01: advisory-lock connection lifecycle (Release pool slot on TryAcquire error; add PGAdvisoryLock.Close and call at webhook-worker shutdown) | 2026-07-12 | 1f8b22b | [260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-](./quick/260712-cqg-fix-phase-16-verification-gaps-cr-01-wr-/) |

### Pending Todos

- Plan Phase 24 (Helm Chart Core & Landmine Closure): K8S-01, K8S-02, K8S-03. Highest-risk, most novel, SEED-004-flagged work — must go first (every other v1.6 phase deploys through this chart or reuses its conventions).

### Blockers/Concerns

None currently blocking. Sequencing carried into the roadmap:

- Hard-ordered arc: 24 → 27 → 28. The chart must exist and expose a NetworkPolicy-scoped `/metrics` (24) before the queue-depth metric can be relocated and validated at zero replicas (27, first plan = KEDA-01), which must complete before any ScaledObject is written (27), which must complete before the load-proof can meaningfully run (28).
- Phases 25 (MCP HTTP) and 26 (operator presets REST) are fully independent of the KEDA spine and of each other — freely reorderable/interleavable. Phase 26 needs zero k8s context.
- Two milestone-critical fail-closed gates, both baked into their phases as deliverables (not follow-ups): (1) queue-depth exposition must move to the always-on api process before any ScaledObject exists — else a worker at 0 replicas has no pod exposing the metric KEDA needs; (2) webhook-worker must be excluded from KEDA entirely — scaling it to zero silently stops the fleet-wide reconciler sweeper.
- Operational discipline (OrbStack): pre-build all 5 images sequentially with non-`latest` tags; never run compose and k8s stacks hot simultaneously (three confirmed daemon wedges on record).

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
| seed | SEED-001: Lesson-recording analysis for tutors and language schools | Dormant | v1.2 close (2026-07-10) |
| seed | SEED-003: MCP-сервер для OctoConv | ✓ Implemented (v1.5 Phase 21, MCP-01..05) | v1.4 planning (2026-07-12) |
| seed | SEED-004: OctoConv on Kubernetes + KEDA autoscaling | Now K8S/KEDA/MCPH/OPER reqs, mapped to Phases 24-28 | v1.6 requirements definition (2026-07-14) |
| infra | k8s-валидация в CI (kind/k3d) | Deferred to v2 (K8SV2-01) | v1.6 requirements definition (2026-07-14) |
| infra | `is_operator` column vs env-allowlist for operators | Deferred to v2 (K8SV2-03) | v1.6 requirements definition (2026-07-14) |
| tech_debt | CACHED-hit log confirmation for CI docker-build (needs gh auth) | Operator-accepted residual | v1.4 close (2026-07-13) |
| ops | Branch-protection required-checks (gate/race/docker-build) — manual GitHub UI step | Open operational follow-up | v1.4 close (2026-07-13) |
| tech_debt | presets D-04 single-active-version: application-transactional only, no DB backstop | Accepted residual | v1.4 close (2026-07-13) |
| seed | SEED-002: Decouple webhook delivery from any specific engine worker binary | ✓ Implemented (v1.3 Phase 16, WEBH-01) | v1.2 close (2026-07-10) |

## Session Continuity

Last session: 2026-07-17T00:59:51.010Z
Stopped at: Phase 28 context gathered
Resume file: .planning/phases/28-autoscale-load-proof/28-CONTEXT.md

## Operator Next Steps

- Start the next milestone with /gsd-new-milestone
