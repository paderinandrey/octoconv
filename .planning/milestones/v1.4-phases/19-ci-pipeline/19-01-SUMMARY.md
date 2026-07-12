---
phase: 19-ci-pipeline
plan: 01
subsystem: infra
tags: [github-actions, docker-buildx, ci, gofmt, go-vet, go-test, race-detector, docker-compose, e2e]

# Dependency graph
requires:
  - phase: 17-tech-debt-cleanup
    provides: "race-clean fakeEnqueuer (DEBT-07) + live image E2E test (DEBT-08), both proven passing, unblocking the -race and e2e tiers"
provides:
  - ".github/workflows/ci.yml — the project's first CI workflow: 4 needs-chained jobs (gate -> race -> docker-build -> e2e)"
  - "Locally-verified hard gates proving the workflow's config and exact tier commands are correct offline, before any GitHub push"
affects: [19-02-live-verification]

# Tech tracking
tech-stack:
  added: ["actions/checkout@v7", "actions/setup-go@v6", "docker/setup-buildx-action@v4", "docker/bake-action@v7", "actions/upload-artifact@v7"]
  patterns:
    - "Single-file needs-chained 4-tier CI pipeline (gate -> race -> docker-build -> e2e), each tier its own GitHub status check"
    - "docker/bake-action reading docker-compose.yml directly as its target definition (no separate docker-bake.hcl)"
    - "Per-target type=gha cache scoping (api/worker/document-worker/chromium-worker/webhook-worker) to avoid one shared cache scope thrashing under the 10GB budget"
    - "Job-level continue-on-error keyed on github.event_name to make a tier advisory-on-PR / required-on-main without duplicating the job"

key-files:
  created: [".github/workflows/ci.yml"]
  modified: []

key-decisions:
  - "e2e tier runs `go test ./internal/e2e/...` only — matching CI-04's exact wording; scripts/presets-acceptance.sh stays a manually-invoked Phase-18 live gate, deliberately not wired into CI (preset logic is already covered by go test ./... in gate+race tiers)"
  - "docker-compose.e2e.yml (SSRF-guard relaxation) is referenced ONLY inside the e2e job's docker compose invocations — verified via grep that every occurrence in ci.yml falls within the e2e job block"
  - "PyYAML is not installed in this environment's system python3 (confirmed during Task 2, step a) — yq is used as the sole YAML-parsing tool for all local verification, consistent with the plan's own environment note; this is a documented environment fact, not a workflow defect"

patterns-established:
  - "New CI workflow files are hard-gated locally (yq parse, bake --print dry-run, exact tier command replay) before being pushed, so a broken workflow is never the first thing discovered on GitHub"

requirements-completed: [CI-01, CI-02, CI-03, CI-04]

# Metrics
duration: ~20min
completed: 2026-07-12
---

# Phase 19 Plan 01: CI Pipeline Workflow Authoring Summary

**Authored `.github/workflows/ci.yml` — OctoConv's first-ever CI workflow, a 4-tier needs-chained pipeline (gate → race → docker-build → e2e) covering CI-01..CI-04, with every tier's exact commands locally hard-gated green before any push.**

## Performance

- **Duration:** ~20 min
- **Completed:** 2026-07-12T20:09:50Z
- **Tasks:** 2 (Task 1: author ci.yml; Task 2: local hard gates — verification-only, no additional file changes)
- **Files modified:** 1 (`.github/workflows/ci.yml`, new file)

## Accomplishments
- Authored `.github/workflows/ci.yml`: `gate` (gofmt/vet/build/test) → `race` (`-race -timeout 10m`) → `docker-build` (bake all 6 compose targets with per-target `type=gha` cache, `timeout-minutes: 20`) → `e2e` (live compose stack via both compose files, `/healthz` poll, migrate, `go test ./internal/e2e/... -timeout 30m`, `if: failure()` log artifact, `if: always()` teardown, `timeout-minutes: 25`, advisory-on-PR/required-on-main via job-level `continue-on-error`)
- Workflow-level `concurrency: {group: ci-${{ github.workflow }}-${{ github.ref }}, cancel-in-progress: true}` prevents superseded runs from piling up
- Free-disk steps precede both `docker-build` and `e2e` (Pitfall 1: this exact stack previously hit disk exhaustion locally per `13-03-SUMMARY.md`)
- All 6 bake targets individually `type=gha`-scoped (`api`, `worker`, `document-worker`, `chromium-worker`, both webhook targets sharing scope `webhook-worker`) — verified via `docker buildx bake --print` dry-run enumerating exactly these 6 target names
- Zero `secrets.*` references; every action pinned to its trusted major tag from STACK.md research

## Task Commits

1. **Task 1: Author .github/workflows/ci.yml (4 tiers, concurrency, e2e advisory/required)** - `904517a` (feat)
2. **Task 2: Local hard gates — validate the workflow + re-run each tier's exact commands** - verification-only; no file changes produced, so no separate commit (see Local Hard Gate Results below for evidence)

**Plan metadata:** committed separately (see final commit below)

## Files Created/Modified
- `.github/workflows/ci.yml` - New 4-tier CI pipeline: gate, race, docker-build, e2e jobs, needs-chained, with concurrency control, per-target Docker layer caching, and a live E2E tier confined to its own job

## Local Hard Gate Results (Task 2 evidence)

**(a) Workflow parse:**
- `yq '.jobs | keys' .github/workflows/ci.yml` → `[gate, race, docker-build, e2e]` — PASS
- `python3 -c "import yaml; ..."` → `ModuleNotFoundError: No module named 'yaml'` (PyYAML not installed in this environment's system python3, confirmed at execution time) — expected per plan's own environment note ("PyYAML is NOT installed in system python3 — use yq for all YAML parsing"); yq's successful parse is the authoritative pass for this gate.
- Structured needs-chain assert (`yq -o=json | python3 -c "..."`) → `NEEDS_OK` — PASS

**(b) Bake dry-run:**
```
$ docker buildx bake -f docker-compose.yml --print 2>/dev/null | python3 -c "import json,sys; t=json.load(sys.stdin)['target']; req={'api','worker','document-worker','chromium-worker','webhook-worker-1','webhook-worker-2'}; missing=req-set(t); print('MISSING',missing) if missing else print('ALL_6_TARGETS_OK')"
ALL_6_TARGETS_OK
```
All 6 target names present in bake's `.target` map — CI-03's bake config parses `docker-compose.yml` correctly, no build performed (dry-run only).

**(c) Compose override parse:**
```
$ docker compose -f docker-compose.yml -f docker-compose.e2e.yml config -q
(exit 0)
```

**(d) Tier-1 (gate) exact commands, replayed locally — all green:**
```
$ gofmt -l .          -> (empty output, exit 0)
$ go vet ./...        -> exit 0
$ go build ./...      -> exit 0
$ go test ./... -timeout 5m
ok  github.com/apaderin/octoconv/internal/api        0.990s
ok  github.com/apaderin/octoconv/internal/auth       1.123s
ok  github.com/apaderin/octoconv/internal/clients    1.485s
ok  github.com/apaderin/octoconv/internal/convert    2.057s
ok  github.com/apaderin/octoconv/internal/e2e        2.368s   (offline self-skip inside package)
ok  github.com/apaderin/octoconv/internal/jobs       2.958s
ok  github.com/apaderin/octoconv/internal/metrics    3.512s
ok  github.com/apaderin/octoconv/internal/presets    2.543s
ok  github.com/apaderin/octoconv/internal/queue      3.046s
ok  github.com/apaderin/octoconv/internal/ratelimit  3.762s
ok  github.com/apaderin/octoconv/internal/reconciler 4.418s
ok  github.com/apaderin/octoconv/internal/storage    4.599s
ok  github.com/apaderin/octoconv/internal/webhook    4.963s
ok  github.com/apaderin/octoconv/internal/worker     4.990s
exit 0
```

**(e) Tier-2 (race) exact command, replayed locally:**
```
$ go test -race ./... -timeout 10m
(all packages ok, no DATA RACE, DATABASE_URL-gated tests self-skipped offline — same package list as above)
exit 0
```

**(f) SC1 negative proof:**
```
$ printf 'package x\nfunc  F(){}\n' > /tmp/gsd_fmt_check.go
$ gofmt -l /tmp/gsd_fmt_check.go
/tmp/gsd_fmt_check.go        <- gate WOULD fail red on this violation
$ rm -f /tmp/gsd_fmt_check.go   (temp file removed, never entered the repo tree)
```

**(g) Tier-4 (live e2e) — cited, not rebuilt:**
- Offline self-skip confirmed live in this session: `go test ./internal/e2e/ -run TestImageConversionE2E -v` → `--- SKIP: TestImageConversionE2E (0.00s)` (`E2E_BASE_URL not set; skipping E2E test`), exit 0.
- Full live-compose pipeline PASS is documented in `.planning/phases/17-tech-debt-cleanup/17-VERIFICATION.md`: `TestImageConversionE2E` — `--- PASS` in 2.14s against a live compose stack (postgres/redis/minio/api/worker/webhook-worker healthy, `/healthz` ready), full upload → convert (libvips) → download → HMAC-verified webhook cycle, plus prior Phase 11/13/15 doc/HTML E2E passes. The authoritative live-green proof for all 4 CI tiers running on real GitHub Actions is the deliverable of plan 19-02, not re-derived here.

## Decisions Made
- e2e tier scope: `go test ./internal/e2e/...` only, matching CI-04's literal wording; `scripts/presets-acceptance.sh` intentionally excluded (remains a manual Phase-18 live gate)
- `docker-compose.e2e.yml` confined strictly to the `e2e` job — verified by grep: every `docker-compose.e2e.yml` occurrence in `ci.yml` (lines 77, 109, 132, 141) falls inside the `e2e` job block (job starts at line 76)
- PyYAML absence in system python3 treated as an expected environment fact (per the plan's own constraint note), not a defect — yq is the sole/authoritative local YAML validator for this and future plans in this phase

## Deviations from Plan

None — plan executed exactly as written. Task 2's step (a) python3/PyYAML sub-check produced a `ModuleNotFoundError` as explicitly anticipated by the plan's environment notes (not a fix-worthy deviation; the plan itself names yq as the deterministic gate and python3-YAML as a secondary check that is known-absent in this environment).

## Issues Encountered
None.

## User Setup Required
None - no external service configuration required. This plan only authored and locally validated a CI workflow file; it does not push to GitHub or require secrets (plan 19-02 handles the live GitHub Actions run).

## Next Phase Readiness
- `.github/workflows/ci.yml` exists, is valid YAML, has the correct 4-job needs-chain, concurrency group, per-target cache scoping, and e2e advisory/required split — ready to be pushed and observed live in plan 19-02.
- All local hard gates are green: bake dry-run enumerates all 6 targets, gate/race tiers replay clean, SC1's negative gofmt proof confirms the gate would correctly fail on a real violation, and the e2e tier's offline self-skip is confirmed alongside a citation to Phase 17's live-PASS evidence for the full pipeline.
- No blockers for 19-02 (the live GitHub Actions run, including pushing this workflow and observing all 4 tiers pass for real).

## Self-Check: PASSED

- FOUND: `.github/workflows/ci.yml`
- FOUND: `.planning/phases/19-ci-pipeline/19-01-SUMMARY.md`
- FOUND: commit `904517a` (Task 1)

---
*Phase: 19-ci-pipeline*
*Completed: 2026-07-12*
