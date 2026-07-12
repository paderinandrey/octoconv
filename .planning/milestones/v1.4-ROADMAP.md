# Roadmap: OctoConv

## Milestones

- ✅ **v1.0 Hardening MVP** — Phases 1-4 (shipped 2026-07-08) — see `.planning/milestones/v1.0-ROADMAP.md`
- ✅ **v1.1 Tech Debt Cleanup** — Phases 5-7 (shipped 2026-07-08) — see `.planning/milestones/v1.1-ROADMAP.md`
- ✅ **v1.2 Document Engine Class** — Phases 8-11 (shipped 2026-07-10) — see `.planning/milestones/v1.2-ROADMAP.md`
- ✅ **v1.3 Document Class v2** — Phases 12-16 (shipped 2026-07-12) — see `.planning/milestones/v1.3-ROADMAP.md`
- 🚧 **v1.4 CI, Presets & Debt Cleanup** — Phases 17-19 (in progress)

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

### 🚧 v1.4 CI, Presets & Debt Cleanup (Phases 17-19) — IN PROGRESS

**Goal:** Каждый push проверяется автоматически вплоть до живого E2E, клиенты используют именованные пресеты вместо ручных opts, и хвост v1.3-долга закрыт.

**Granularity:** coarse — 3 phases, one per requirement cluster (debt / presets / CI). Ordering enforces the two hard sequencing constraints from research: the fakeEnqueuer race fix (DEBT-07) must precede the `-race` CI tier, and the image E2E test (DEBT-08) must precede the live-E2E CI tier.

- [x] **Phase 17: Tech Debt Cleanup** — dead webhook wiring removed, fakeEnqueuer race-safe, image E2E test added (unblocks CI tiers 2 & 4) — completed 2026-07-12
- [x] **Phase 18: Presets** — named server-side presets: `cmd/manage-presets` CLI + `preset=<name>` job creation, activating the dormant `presets` DDL — completed 2026-07-12
- [x] **Phase 19: CI Pipeline** — 4-tier GitHub Actions (gate → `-race` → 5-image Docker build → live compose E2E), validating the full v1.4 codebase on first run

#### Phase Details

### Phase 17: Tech Debt Cleanup
**Goal**: Close the v1.3 tail-debt so the later `-race` and live-E2E CI tiers can land green instead of red/blind on day one.
**Depends on**: Nothing (first phase of v1.4; builds directly on v1.3 `main`)
**Requirements**: DEBT-06, DEBT-07, DEBT-08
**Success Criteria** (what must be TRUE):
  1. `cmd/document-worker` and `cmd/chromium-worker` no longer construct `webhook.NewRepo`/`NewDeliverer` nor read `WEBHOOK_SIGNING_SECRET`; both binaries still build and start cleanly (dead wiring gone, not just unused).
  2. `go test ./internal/reconciler/... -race` runs (not skips) and reports clean — `fakeEnqueuer`'s call counters are mutex/atomic-guarded.
  3. A new image-engine E2E test in `internal/e2e` drives a full upload → convert (libvips) → download → HMAC-verified webhook cycle against a live compose stack and passes — closing the last gap in the E2E matrix.
**Plans**: 2 plans
- [ ] 17-01-PLAN.md — Remove dead webhook wiring from document/chromium workers (DEBT-06) + make fakeEnqueuer race-safe (DEBT-07)
- [ ] 17-02-PLAN.md — Add image-engine (libvips) E2E test with PNG fixture (DEBT-08)

### Phase 18: Presets
**Goal**: Clients create conversion jobs by named preset instead of hand-supplying `target_format`/`opts`, and operators manage those presets through a CLI — reusing the existing validated-opts pipeline with zero new validation logic and zero new migration.
**Depends on**: Nothing functionally (independent of Phase 17); sequenced second only to keep its diff surface isolated from the debt-cleanup touch points.
**Requirements**: PRST-01, PRST-02, PRST-03, PRST-04
**Success Criteria** (what must be TRUE):
  1. An operator can create / update / list / show / deactivate both system-scoped and client-scoped presets via `cmd/manage-presets` (no hard delete; a single active version per name, bump-on-update) — mirroring `cmd/manage-clients`.
  2. `POST /v1/jobs` with `preset=<name>` converts using the preset's stored target/opts, and the resulting job record carries `preset_name` / `preset_version` provenance.
  3. A client-scoped preset shadows a system preset of the same name for its owning client (scope-precedence lookup), and system presets remain usable by any client.
  4. Supplying `preset` together with explicit `target_format`/`opts` returns 422 (mutually exclusive); a nonexistent, inactive, or cross-client preset returns the same 422 with no existence leak (SQL `WHERE client_id` filter, not a post-lookup Go branch).
  5. Opts resolved from a preset are re-run through the same fail-closed `ParseDocOpts`/`ParseHTMLOpts` validation on every use — stored opts are never trusted, with no bypass branch.
**Plans**: 4 plans
- [ ] 18-01-PLAN.md — internal/presets package (resolution query, CRUD) + jobs provenance columns [wave 1]
- [ ] 18-02-PLAN.md — cmd/manage-presets CLI + write-time opts validation [wave 2]
- [ ] 18-03-PLAN.md — handleCreateJob preset resolution (XOR, no-leak, re-validation, provenance) + PresetRepo interface [wave 2]
- [ ] 18-04-PLAN.md — live compose acceptance hard gate (SC2/SC3/SC4/SC5) [wave 3]

### Phase 19: CI Pipeline
**Goal**: Every push/PR is validated automatically, escalating from a cheap gate up to a live compose E2E, so the full v1.4 codebase (presets included) is exercised green from the pipeline's first run.
**Depends on**: Phase 17 (DEBT-07 gates the `-race` tier; DEBT-08 gates the live-E2E tier) and Phase 18 (so the docker-build/E2E tiers exercise the final v1.4 code).
**Requirements**: CI-01, CI-02, CI-03, CI-04
**Success Criteria** (what must be TRUE):
  1. A PR containing a gofmt/vet/build/test violation gets a red required check (tier 1 gate) before any escalating tier runs.
  2. `go test ./... -race` runs as a full-package required check and is green on the v1.4 codebase (tier 2).
  3. All 5 Docker images (api, worker, document-worker, chromium-worker, webhook-worker) build in CI via `docker buildx bake` over `docker-compose.yml` with per-target `type=gha` layer cache — CACHED layers appear on a second identical run, and a disk-cleanup step keeps the LibreOffice+Chromium build within runner disk.
  4. Live E2E brings up the full compose stack and runs `internal/e2e` against it — advisory on PR, required on main; teardown runs under `if: always()` even when tests fail, stack logs upload as an artifact on failure, and stale runs are cancelled by a concurrency group.
**Plans**: 2 plans
- [ ] 19-01-PLAN.md — Author .github/workflows/ci.yml (gate → race → docker-build → e2e) + local hard gates (YAML parse, bake --print dry-run, tier-1/2 replay, SC1 negative proof) [wave 1]
- [ ] 19-02-PLAN.md — Push to main + live-run observation (scripted gh watch OR human-verify checkpoint) + CACHED-second-run proof + branch-protection follow-up doc [wave 2]

**Operational follow-up (not a code deliverable):** After Phase 19 lands, branch-protection required-checks must be configured manually in GitHub — the workflow file alone does not gate merges.

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
| 19. CI Pipeline | v1.4 | 0/2 | Not started | - |

---

*Next: run `/gsd:plan-phase 17` to plan the first v1.4 phase.*
