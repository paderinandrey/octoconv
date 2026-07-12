# Project Research Summary

**Project:** OctoConv
**Domain:** Internal async file-conversion service — CI/CD pipeline (GitHub Actions) + server-side named conversion presets + v1.3 tech-debt cleanup
**Researched:** 2026-07-12
**Confidence:** HIGH

## Executive Summary

v1.4 is an integration milestone, not a greenfield build: it bolts a 4-tier GitHub Actions CI pipeline (gate to -race to 5-image Docker build to live compose E2E) and a server-side presets mechanism onto an already production-hardened Go service (v1.0-v1.3 shipped, 3 engine classes: libvips images, LibreOffice documents, chromium HTML-to-PDF). Neither feature area touches the fixed core stack (Go 1.26, chi, asynq/Redis, Postgres 18, MinIO) — CI is pure GitHub Actions YAML (actions/checkout v7, actions/setup-go v6, docker/setup-buildx-action v4 + docker/bake-action v7, actions/upload-artifact v7), and presets require zero new Go dependencies: the presets table and jobs.preset_name/preset_version columns already exist in 0001_init.sql, dormant since Phase 1, and cmd/manage-presets is a straight structural copy of the existing cmd/manage-clients CLI.

The recommended approach is deliberately narrow-scope: build all 5 images via docker buildx bake (not docker compose build, which silently bypasses the GHA cache — the single most consequential stack pitfall found), split live E2E into advisory-on-PR / required-on-main / scheduled-nightly (never required-blocking on every PR — it's the flakiest tier by nature), and resolve preset=<name> as a pure alternate source for the same validated-opts pipeline Phase 14 already built (ParseDocOpts/ParseHTMLOpts + ValidateApplicability) — never a parallel or weaker validation path. Presets and CI are architecturally independent of each other; only two build-order constraints exist, both already identified by the project owner: the fakeEnqueuer race must be fixed before the -race CI tier is enabled, and a dedicated image/libvips E2E test must exist before the live-E2E CI tier is enabled (it's the last gap in the E2E matrix).

The dominant risks are operational, not architectural. This exact stack has already hit disk exhaustion locally building these same 5 images (LibreOffice + Chromium payloads on top of golang:1.26-bookworm, documented in 13-03-SUMMARY.md) — a GitHub-hosted runner has less headroom than a dev laptop, so disk mitigation must ship in the same phase as the build tier, not as a follow-up. On the presets side, the highest-severity risks are security-adjacent regressions of invariants this project already fought hard to establish: presets must never bypass the opts-validation allowlist (re-derivation of the exact injection class OPTS-01/02 closed), and cross-client preset access must return byte-identical 404s (never 403, never a distinguishable error message) to preserve the existing "cross-client access to 404, never 403" convention set in Phase 1.

## Key Findings

### Recommended Stack

No new runtime dependencies anywhere in this milestone. CI is entirely GitHub Actions YAML using current major-version actions (verified live via GitHub's Releases API, 2026-07-12); presets reuse stdlib (flag, os) plus the existing internal/db (pgx) pattern, mirroring cmd/manage-clients file-for-file.

**Core technologies:**
- actions/checkout@v7 + actions/setup-go@v6 (go-version-file: go.mod, built-in module cache) — clone + toolchain install with zero second version-string to keep in sync
- docker/setup-buildx-action@v4 + docker/bake-action@v7 reading docker-compose.yml directly — builds all 5 images (api, worker, document-worker, chromium-worker, webhook-worker x2) from the existing compose file with no parallel Dockerfile list; bake does NOT provision Buildx itself, setup-buildx-action must run first in the same job
- GHA cache backend (type=gha, per-service scope=) — must be wired explicitly via bake/build-push-action; docker compose build does NOT use it automatically (biggest single stack gotcha)
- actions/upload-artifact@v7 — captures compose logs on live-E2E failure for diagnosability
- Go stdlib + existing internal/db (pgx) for cmd/manage-presets — confirmed zero new Go module needed

**What NOT to add:** any registry push step (milestone is build-verify only, never publish); docker/login-action (defer until Docker Hub 429s are actually observed); a new validation/config library for presets (the closed opts allowlist already covers it).

### Expected Features

**Must have (table stakes):**
- CI tiers 1-3 (gofmt/vet/build/test, -race, 5-image Docker build) as required PR checks
- CI tier 4 (live E2E) as advisory-on-PR, required-on-main, nightly-scheduled — never PR-blocking
- preset=<name> resolvable at job creation, routed through the same validated-opts pipeline as explicit target/opts (no parallel path)
- cmd/manage-presets CLI: create/list/show/deactivate, mirroring cmd/manage-clients
- System presets usable by any client; client-scoped presets usable only by the owning client (already modeled in DDL — wiring, not schema work)
- Job-record provenance: resolved preset name+version stored on the job (jobs.preset_name/preset_version columns already exist, unused since Phase 1)

**Should have (competitive/differentiator):**
- preset show <name> with full version history
- Scheduled nightly live-E2E run independent of PR traffic, with failure notification (a cron job nobody is notified about provides zero signal)
- CI job names split 1:1 with logical checks (scannable PR dashboard)
- Preset validate/dry-run CLI verb (reuse existing validation functions, no new logic)

**Defer (v2+):**
- Deep-merge of preset baseline + partial client override opts (Cloudinary/imgix-style) — opts is a closed, typed, validated struct; merge semantics reopen exactly the injection surface Phase 14 closed. Ship XOR (preset OR explicit target+opts, never both) for v1.4.
- Self-service preset creation via a public API endpoint — stays operator/CLI-only, matching the clients table's own trust model.
- Mutable preset versions / hard delete — versions are immutable (bump-on-update), deactivate-only (no destructive delete), mirroring the clients key-rotation convention.

### Architecture Approach

This is an integration atop the existing dependency graph (cmd/* -> internal/{api,worker} -> internal/{jobs,storage,queue,convert} -> internal/db), not a new layer. Presets get their own package (internal/presets, mirroring internal/clients) with a narrow PresetRepo interface consumed by internal/api — no import relationship with internal/jobs in either direction; presets resolve once at creation time and their result is copied as plain fields into jobs.CreateParams, so a later preset edit/deactivation never retroactively changes an already-created job. handleCreateJob gains a preset-resolution branch that runs BEFORE format-pair validation (EngineFor) and feeds the exact same ParseDocOpts/ParseHTMLOpts + ValidateApplicability calls used for manual opts. CI is a single .github/workflows/ci.yml with four needs:-chained jobs (not four separate workflow files — the tiers are strictly sequential/escalating in cost, and single-file keeps branch-protection required-checks configuration simple).

**Major components:**
1. internal/presets (NEW) — Postgres-backed repo: GetActiveByNameAndClient (client-scope-shadows-system-scope lookup via ORDER BY (scope='user') DESC), CRUD for the CLI
2. cmd/manage-presets (NEW) — operator CLI mirroring cmd/manage-clients: create/list/show/deactivate, transactional single-active-version enforcement
3. internal/api/handlers.go (MODIFIED) — preset-resolution stage as a pre-validation branch, XOR against explicit target/opts
4. .github/workflows/ci.yml (NEW) — 4-tier pipeline consuming existing docker-compose.yml/docker-compose.e2e.yml unchanged, no new secrets required
5. Debt-cleanup touch points (MODIFIED) — dead webhook wiring removed from cmd/document-worker/cmd/chromium-worker; mutex added to fakeEnqueuer; TestImageConversionE2E added to internal/e2e

### Critical Pitfalls

1. **Runner disk exhaustion building 5 images (LibreOffice + Chromium payloads)** — already happened locally on this exact stack (13-03-SUMMARY.md). Mitigate with an explicit disk-cleanup step (remove preinstalled toolcache dirs, docker system prune -af) shipped in the same phase as the build tier, not as a follow-up.
2. **docker compose build silently bypasses the GHA cache backend** — must use docker buildx bake/docker/build-push-action with explicit cache-from/cache-to: type=gha,scope=<service> per image; verify by confirming CACHED layers appear on a second identical run, don't assume wiring worked.
3. **Presets bypassing the validated-opts pipeline** — trusting presets.options as "already safe because an operator created it" reopens the exact injection class OPTS-01/02 closed. Every preset resolution must re-run stored options through ParseDocOpts/ParseHTMLOpts at read time, with no bypass branch, exactly like DocOptsFromMap already re-validates persisted jobs.options.
4. **Cross-client preset existence leak breaking the established 404-not-403 convention** — ownership check must be a SQL WHERE client_id=... filter, not a post-lookup Go branch, so "doesn't exist" and "belongs to another client" are literally the same code path and same response bytes.
5. **Healthcheck-vs-readiness gap in live E2E** — Compose service_healthy only covers infra containers (postgres/redis/minio); api/worker/document-worker/chromium-worker have no healthcheck. Gate the E2E test start on GET /healthz returning 200, not just on Compose's fixed retry budget, since CI runners are noisier/slower than local dev.

## Implications for Roadmap

Presets and CI are functionally independent (no code-level dependency either direction), but two hard sequencing constraints exist, both already identified by the project owner. Suggested phase structure:

### Phase 1: Tech Debt Cleanup
**Rationale:** Smallest, most isolated work; two of its three items are hard prerequisites for later CI tiers. Doing this first keeps the diff surface small and isolated from unrelated preset changes.
**Delivers:** Dead webhook wiring removed from cmd/document-worker/cmd/chromium-worker; fakeEnqueuer mutex added (race-safe); TestImageConversionE2E added to internal/e2e.
**Addresses:** All three "Tech debt v1.3" active requirements from PROJECT.md.
**Avoids:** Pitfall — enabling -race CI or live-E2E CI tiers before their prerequisites land produces a red/blind pipeline on day one.

### Phase 2: Presets
**Rationale:** Fully independent of CI; can technically run in parallel with Phase 1 but sequencing after keeps debt-cleanup diffs isolated. Reuses 100% of the existing validated-opts machinery — no new validation logic, no new migration (schema already exists, dormant since Phase 1).
**Delivers:** internal/presets package, cmd/manage-presets CLI (create/list/show/deactivate), PresetRepo interface + handleCreateJob preset-resolution branch (XOR against explicit target/opts), single-active-version-per-name enforcement, job-record provenance population.
**Addresses:** All presets-related table-stakes features from FEATURES.md (named preset resolution, system/client scoping, CLI management, provenance).
**Avoids:** Pitfalls 3/4/8/9/10/11/12 from PITFALLS.md — opts-validation bypass, scope-precedence bugs, TOCTOU on deactivation, version-resolution ambiguity, cross-client existence leak. These should be explicit test cases in this phase, not implicit side effects.

### Phase 3: CI Pipeline
**Rationale:** Built last so its docker-build/e2e-live tiers exercise the final v1.4 codebase (presets included) from day one, and so it's green on first run instead of needing an immediate follow-up fix (both the race-fix and image-E2E-test prerequisites from Phase 1 must already be merged).
**Delivers:** .github/workflows/ci.yml — 4 needs:-chained jobs (gate, race, docker-build, e2e-live); disk-space mitigation step; per-service GHA cache scoping via docker buildx bake; if: always() teardown + if: failure() log-artifact capture on the E2E job; concurrency group with cancel-in-progress for PR runs only.
**Uses:** actions/checkout@v7, actions/setup-go@v6, docker/setup-buildx-action@v4 + docker/bake-action@v7, actions/upload-artifact@v7 — no new Go dependencies.
**Implements:** Pattern 3 from ARCHITECTURE.md (single tiered workflow, not four files).

### Phase Ordering Rationale

- Debt cleanup first because its two CI-blocking prerequisites (fakeEnqueuer race fix, image E2E test) must exist before Phase 3's tiers 2 and 4 can be enabled without shipping a permanently-red or false-green pipeline.
- Presets second (not third) purely to keep its diff surface isolated from debt-cleanup's touch points in cmd/document-worker/chromium-worker/internal/reconciler/internal/e2e — there is no functional dependency forcing this order.
- CI last so it validates the complete, final v1.4 codebase on its very first run rather than a partial slice, avoiding an immediate "add presets support to CI" follow-up phase.
- This ordering directly avoids the "Anti-Pattern 4" trap from ARCHITECTURE.md (assuming the workflow file alone gates merges) — branch protection configuration is a manual operational follow-up after Phase 3 lands, not a code deliverable, and should be flagged explicitly at that point.

### Research Flags

Phases likely needing deeper research during planning:
- **Presets phase:** the version-resolution semantics (single-active-version-per-name vs. multiple-simultaneously-active) and scope-precedence lookup order are schema/behavior design decisions specific to this project's DDL, not verifiable against an external authority — FEATURES.md and PITFALLS.md both flag these as "must be a written decision, not inferred behavior." Recommend an explicit Key Decision entry before implementation, plus dedicated test cases for both shadowing directions.
- **CI pipeline phase:** GHA cache sizing/scoping across 5 images (10GB default per-repo budget, LRU eviction) is a real but graceful-degradation risk, not a hard blocker — worth a design note but not blocking research; runner disk/private-repo sizing should be confirmed against the actual repo visibility setting before finalizing the E2E job's resource assumptions.

Phases with standard patterns (skip research-phase):
- **Tech debt cleanup phase:** all three items are narrow, already-diagnosed, single-file-scope fixes with a clear "do this instead" already spelled out in ARCHITECTURE.md/PITFALLS.md — no further research needed.
- **CLI implementation (cmd/manage-presets):** direct structural mirror of cmd/manage-clients, an existing, already-battle-tested in-repo pattern.

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | All GitHub Action versions verified live via GitHub's Releases API on 2026-07-12; presets confirmed zero-new-deps by direct inspection of 0001_init.sql and cmd/manage-clients |
| Features | MEDIUM | CI tiering/concurrency mechanics are HIGH-confidence (sourced from GitHub's own docs); preset resolution/versioning semantics are bespoke schema design decisions — informed by analogous external patterns (Cloudinary/CloudConvert/imgix) and existing in-repo conventions, not verifiable against an external authority for this exact schema |
| Architecture | HIGH | All findings verified directly against current repo source (handlers.go, api.go, repo.go, 0001_init.sql, etc.) — no external ecosystem claims in this milestone's architecture research |
| Pitfalls | MEDIUM-HIGH | GitHub Actions runner limits verified via current official sources; presets pitfalls derived directly from this repo's existing DDL/code conventions (HIGH); general CI flakiness patterns MEDIUM, cross-referenced against official docs where cited |

**Overall confidence:** HIGH

### Gaps to Address

- **Version-resolution semantics for presets** (single-active-version vs. multiple-simultaneously-active): the DDL's is_active column (singular per row) suggests "one active version at a time" is intended, but this must be an explicit written decision recorded before the resolution query is implemented — flag for the roadmapper to make this a stated deliverable of the presets phase, not an implicit assumption.
- **worker.NewHandler's constructor signature** for the webhook-wiring debt removal: unclear whether the webhook-repo/deliverer params can become fully optional/nil-safe or need a signature change — verify during phase planning, not assumed resolved by this research.
- **Repo visibility (public vs. private) for runner sizing**: standard ubuntu-latest is 4 vCPU/16GB for public repos but only 2 vCPU/8GB for private repos on the free tier — confirm OctoConv's actual GitHub repo visibility/plan before finalizing E2E job resource assumptions; flagged as MEDIUM confidence that the smaller spec is sufficient for the current one-format-pair-at-a-time E2E shape.
- **GHA cache budget (10GB default) across 5 images including LibreOffice/Chromium layers**: plausible periodic eviction thrashing — not a blocker (graceful degradation to slower rebuilds), but worth monitoring via the cache-usage dashboard after the first several CI runs post-launch.

## Sources

### Primary (HIGH confidence)
- GitHub Actions Releases API (live query, 2026-07-12) — actions/checkout, actions/setup-go, actions/cache, docker/setup-buildx-action, docker/bake-action, actions/upload-artifact, docker/metadata-action current versions
- docs.github.com — "GitHub-hosted runners reference" (runner hardware specs), "Caching dependencies to speed up workflows" (10GB cache quota/LRU/rate limits)
- Direct repository inspection: internal/api/handlers.go, internal/api/api.go, internal/jobs/repo.go, internal/clients/repo.go, cmd/manage-clients/main.go, internal/db/migrations/0001_init.sql, internal/convert/opts.go, internal/reconciler/reconciler_test.go/reconciler_soak_test.go, internal/e2e/e2e_test.go, docker-compose.yml, docker-compose.e2e.yml, .planning/PROJECT.md
- .planning/milestones/v1.3-phases/13-cross-format-conversion-input-safety/13-03-SUMMARY.md — direct project evidence of Docker Desktop VM disk exhaustion during this exact multi-image build sequence

### Secondary (MEDIUM confidence)
- Docker Docs — "GitHub Actions cache backend" (type=gha behavior, mode=max vs mode=min)
- GitHub Changelog — "Actions cache size can now exceed 10GB per repository" (2025-11-20)
- actions/runner-images community discussions (#9329, #13719, #10386) — empirical disk-space findings vs. documented specs
- Cloudinary/CloudConvert/imgix official docs — named-preset/transformation patterns, preset+override precedence models

### Tertiary (LOW confidence)
- None flagged — all research this milestone was either verified against official sources or against this project's own primary source code.

---
*Research completed: 2026-07-12*
*Ready for roadmap: yes*
