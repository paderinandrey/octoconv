---
phase: 19-ci-pipeline
plan: 02
status: complete
subsystem: ci
tags: [github-actions, live-run, e2e, rate-limiting]

requires:
  - phase: 19-ci-pipeline
    provides: ".github/workflows/ci.yml authored and locally hard-gated (19-01)"
provides:
  - "Live-proven 4-tier CI pipeline on GitHub Actions: two real runs on main, second fully green"
  - "E2E-only rate-limit relaxation in docker-compose.e2e.yml (RATE_LIMIT_IP_RPM/CLIENT_RPM=6000) — production limits untouched"
  - "Branch-protection follow-up documented (manual operator step)"
affects: [ci, e2e]

key-files:
  created: []
  modified:
    - docker-compose.e2e.yml

requirements-completed: [CI-01, CI-02, CI-03, CI-04]

execution-mode: "inline by orchestrator — a push-main + observe plan cannot run in worktree isolation"
---

# 19-02 Summary — push + live-run observation

**Executed inline by the orchestrator** (the plan's core actions are `git push origin main` and observing the resulting run — inherently main-branch/outward-facing, not worktree-isolatable).

## Live run evidence

**Run 1 — `15d1c3e` (workflow's first ever run), ID 29207275908: completed/failure — and the failure itself proved the failure-path machinery.**
- gate: success · race: success · docker-build: success (cache-warming run)
- e2e: FAILURE at the "Run E2E suite" step — every infra step succeeded (stack up, healthz readiness poll, migrations), and the failure path executed exactly as designed: "Dump compose logs on failure" ✓, upload-artifact (compose-logs, 5.6KB) ✓, "Tear down" under `if: always()` ✓.
- Root cause (diagnosed from the compose-logs artifact, fetched via nightly.link since gh is unauthenticated): **429 cascade** — the suite's status polling, all from the single docker-bridge gateway IP (172.18.0.1), exceeded the production `RATE_LIMIT_IP_RPM=60` on the fast public runner. Local runs had never tripped this (slower machine, borderline under the limit).
- Fix: `docker-compose.e2e.yml` (the designated test-only override, same place as the SSRF opt-outs) now sets `RATE_LIMIT_IP_RPM: "6000"` / `RATE_LIMIT_CLIENT_RPM: "6000"` on the api service. Production `docker-compose.yml` limits unchanged. Commit `92c43a0`.

**Run 2 — `92c43a0`, ID 29207810893: completed/success — all four tiers green.**
- gate: success (18s) · race: success (61s) · docker-build: success (268s, warmed gha cache from run 1) · e2e: success (badge flipped to "passing" on main).
- Observed via anonymous GitHub API (repo is **public** → 4c/16GB runners, confirming the conservative timeout assumptions) until the API rate limit cut in, then via the un-quota'd workflow badge endpoint. `gh` CLI remained unauthenticated throughout — the plan's conditional resolved to "anonymous-API observation", stronger than the human-verify fallback, weaker than `gh run watch`; conclusion evidence (badge: passing) is unambiguous because a badge flip from failing→passing can only come from a newer completed-successful run on main.

## Success criteria disposition

- **SC1 (red on violation):** deterministic gate commands proven locally (19-01 negative gofmt test) + gate ran green twice live.
- **SC2 (-race required):** race tier green on both live runs (61s).
- **SC3 (bake + per-target gha cache + disk step):** all 6 targets built on both runs; run 1 warmed per-scope caches, run 2 rebuilt with cache-from. Granular `CACHED`-line log inspection was NOT possible anonymously (job-log download requires auth) — duration alone is not cited as proof since image pulls dominate; the bake config (6 targets, per-target scopes, shared webhook-worker scope ×6 occurrences) was hard-gate-verified in 19-01, and both live builds succeeded within the 20-minute job bound. Residual: an operator with `gh auth` can confirm CACHED lines in run 29207810893's docker-build log — noted as optional corroboration, not a gap.
- **SC4 (live E2E advisory-on-PR/required-on-main, teardown, artifact, concurrency):** e2e green on main (run 2); teardown + failure-artifact machinery live-proven by run 1; concurrency group present (hard-gated in 19-01) — no superseded-run cancellation was observed live (no overlapping pushes), noted as exercised-by-construction.

## Operational follow-up (NOT automatable without gh auth) — for the operator

Branch protection must be configured manually once: GitHub → Settings → Branches → protect `main` → require status checks **gate**, **race**, **docker-build** (leave **e2e** unrequired on PRs — it is advisory there by design, required on main pushes by the workflow itself). URL: https://github.com/paderinandrey/octoconv/settings/branches

## Deviations

1. Plan executed inline by the orchestrator (rationale above) — tasks/gates followed as written.
2. Run-observation used anonymous API + badge instead of `gh run watch` (gh unauthenticated) — the plan's own designed fallback, upgraded from human-verify since the repo turned out public.
3. Unplanned but required fix commit `92c43a0` (docker-compose.e2e.yml rate limits) — discovered by run 1, confined to the test-only override file.

## Self-Check: PASSED

- .github/workflows/ci.yml on main, both runs referenced: 29207275908 (failure, diagnostic), 29207810893 (success, all tiers)
- Badge on main: passing
- docker-compose.e2e.yml change committed (92c43a0) and pushed
