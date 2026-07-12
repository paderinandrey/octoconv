---
phase: 19-ci-pipeline
verified: 2026-07-12T00:00:00Z
status: passed
score: 10/11 must-haves verified
overrides_applied: 0
human_verification:
  - test: "Confirm 'CACHED' layer lines actually appear in run 29207810893's docker-build job log (second, warm run)"
    expected: "The gha-cache log shows `CACHED` (or equivalent buildkit cache-hit) markers for layers reused from the scopes warmed in run 29207275908, not a full rebuild-from-scratch that merely happened to finish inside the 20-minute bound"
    why_human: "Anonymous GitHub API access is rate-limited and cannot download job logs; `gh` CLI was unauthenticated throughout 19-02. Verifying an actual cache HIT (vs. a cache MISS that still succeeds, just slower, e.g. due to the 10GB per-repo gha-cache quota being exceeded across 6 scopes) requires either `gh auth login` + `gh run view <id> --log`, or opening the docker-build job log in the GitHub UI."
---

# Phase 19: CI Pipeline Verification Report

**Phase Goal:** Every push/PR is validated automatically, escalating from a cheap gate up to a live compose E2E, so the full v1.4 codebase (presets included) is exercised green from the pipeline's first run.
**Verified:** 2026-07-12
**Status:** passed (operator override 2026-07-13)
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `ci.yml` defines exactly 4 needs-chained jobs: gate → race → docker-build → e2e | ✓ VERIFIED | `.github/workflows/ci.yml:9-142` — `race.needs: gate`, `docker-build.needs: race`, `e2e.needs: docker-build`; needs-chaining natively skips downstream jobs when an upstream job fails, satisfying "before any escalating tier runs" (SC1 clause) without extra logic |
| 2 | Gate tier runs gofmt/vet/build/test — the exact deterministic commands that go red on any violation (SC1) | ✓ VERIFIED | Lines 22-25: `test -z "$(gofmt -l .)"`, `go vet ./...`, `go build ./...`, `go test ./... -timeout 5m`. No real broken PR was created to prove this live (see Judgment Calls below) — accepted on the basis these are stdlib/toolchain commands with well-known deterministic non-zero exit on violation, plus the 19-01 local negative-proof (`gofmt -l` on a deliberately malformed temp file printed the path) |
| 3 | Race tier runs `go test -race ./...` with an explicit non-default timeout (SC2) | ✓ VERIFIED | Line 42: `go test -race ./... -timeout 10m` (not the 10-min *default* — an explicit override per Pitfall 7); green on both live runs per 19-02-SUMMARY (61s each) |
| 4 | Docker-build tier builds all 6 compose bake targets via bake over docker-compose.yml, each with per-target `type=gha` cache-to/cache-from, preceded by a free-disk step (SC3, structural half) | ✓ VERIFIED | Lines 47-74: `files: docker-compose.yml`, 6× `cache-to=type=gha,mode=max,scope=<x>` + 6× `cache-from=type=gha,scope=<x>` (api, worker, document-worker, chromium-worker, webhook-worker×2 sharing scope `webhook-worker`); `grep -c 'scope=webhook-worker'` = 6 (2 cache-to + 2 cache-from in docker-build + 2 cache-from in e2e); free-disk step at lines 53-56; `docker-compose.yml` confirmed to have exactly 6 `build:` blocks (api/worker/document-worker/chromium-worker/webhook-worker-1/webhook-worker-2), with webhook-worker-1 and -2 both building `Dockerfile.webhook-worker` — reconciling ROADMAP's "5 Docker images" (one shared image, two replica services) with the workflow's "6 bake targets" |
| 5 | CACHED layers actually appear on the second identical docker-build run | ? UNCERTAIN | Config is provably correct (item 4) and two live runs occurred (run 1 cold-warmed the scopes, run 2 rebuilt in 268s and succeeded); but no log-line inspection of "CACHED" was possible — `gh` was unauthenticated and anonymous API access was rate-limited throughout 19-02, so the 19-02-SUMMARY itself only claims "both live builds succeeded within the 20-minute bound," not a confirmed cache hit. A per-target-scope config can still silently cache-miss (e.g. if 6 scopes collectively exceed the ~10GB gha-cache quota) while the job still succeeds by rebuilding from scratch — success alone doesn't prove a hit. Routed to human verification below. |
| 6 | e2e tier brings the stack up via both compose files, polls `/healthz`, runs `internal/e2e`, tears down under `if: always()`, uploads compose logs on failure, is advisory on PR / required on main (SC4) | ✓ VERIFIED | Lines 80-141: `docker compose -f docker-compose.yml -f docker-compose.e2e.yml up -d` (line 109); `curl --retry 30 ... /healthz` (line 114); migrations via `go run ./cmd/migrate` (115); `go test ./internal/e2e/... -timeout 30m` (120); `if: failure()` logs-dump (131) + `upload-artifact@v7 if: failure()` (133-138); `if: always()` teardown (139-141); job-level `continue-on-error: ${{ github.event_name == 'pull_request' }}` (84) — advisory-on-PR / required-on-main by construction. Run 1 live-proved the *failure path* exactly (stack up + healthz + migrations succeeded, suite failed, logs artifact uploaded 5.6KB, teardown ran); run 2 live-proved the *success path* (e2e green, badge flipped to passing) |
| 7 | Workflow-level concurrency group keyed on `github.ref` with `cancel-in-progress: true` | ✓ VERIFIED | Lines 5-7: `group: ci-${{ github.workflow }}-${{ github.ref }}`, `cancel-in-progress: true`. No overlapping pushes occurred live to observe an actual cancellation — exercised-by-construction only, consistent with 19-02-SUMMARY's own characterization |
| 8 | All actions pinned to trusted major tags; no unvetted third-party actions; zero `secrets.*` references | ✓ VERIFIED | `grep 'uses:'` shows only `actions/checkout@v7`, `actions/setup-go@v6`, `docker/setup-buildx-action@v4`, `docker/bake-action@v7`, `actions/upload-artifact@v7` — exactly the STACK-pinned set; `grep -c 'secrets\.'` = 0 |
| 9 | `docker-compose.e2e.yml` confined strictly to the e2e job; rate-limit relaxation is e2e-only and documented, production limits untouched | ✓ VERIFIED | All 3 `docker-compose.e2e.yml` occurrences in `ci.yml` (lines 109, 132, 141) fall inside the `e2e` job (job starts line 80). `docker-compose.e2e.yml:26-27` sets `RATE_LIMIT_IP_RPM: "6000"` / `RATE_LIMIT_CLIENT_RPM: "6000"` on the `api` service only, with an explanatory comment (lines 21-25) citing the exact 429-cascade root cause; `docker-compose.yml:90-91` still has the production `RATE_LIMIT_IP_RPM: "60"` / `RATE_LIMIT_CLIENT_RPM: "120"` unchanged |
| 10 | Workflow is committed and pushed to `origin/main`, triggering a live GitHub Actions run confirmed green | ✓ VERIFIED | `git log origin/main` shows `92c43a0` (rate-limit fix) and `904517a` (workflow authored) reachable on `origin/main`; independent badge check performed by this verifier (`curl .../ci.yml/badge.svg?branch=main` → `passing`, 3× confirmed) corroborates 19-02-SUMMARY's run-2 "all four tiers green" claim without spending an API-rate-limited request |
| 11 | Branch-protection follow-up (operator action, not a code deliverable per ROADMAP) is documented with both UI and CLI routes, naming gate/race (not e2e) as required | ✓ VERIFIED | `19-02-SUMMARY.md:50-52` documents the UI path (Settings → Branches → protect `main` → require `gate`, `race`, `docker-build`) and references the exact settings URL; ROADMAP explicitly scopes this as "Operational follow-up (not a code deliverable)" — matches the plan's own success criteria (documentation, not execution) |

**Score:** 10/11 truths verified, 1 uncertain (routed to human verification)

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `.github/workflows/ci.yml` | 4-tier CI pipeline (gate/race/docker-build/e2e) | ✓ VERIFIED | Exists, valid YAML (confirmed structurally by direct read + prior `yq` gate in 19-01), all 4 jobs present and needs-chained, substantive (real commands, not stubs), wired (pushed to origin/main and executed live twice) |
| `docker-compose.e2e.yml` | e2e-only override w/ rate-limit fix | ✓ VERIFIED | Modified (commit `92c43a0`), e2e-scoped, documented, production compose untouched |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `ci.yml` (gate/race/e2e) | `go.mod` | `setup-go` `go-version-file: go.mod` | ✓ WIRED | Present at lines 19, 39, 91 |
| `ci.yml` (docker-build) | `docker-compose.yml` | `bake-action` `files:` input | ✓ WIRED | `files: docker-compose.yml` at line 60; bake `--print` dry-run (19-01) enumerated all 6 targets from this exact file |
| `ci.yml` (e2e only) | `docker-compose.e2e.yml` | `docker compose -f ... -f docker-compose.e2e.yml` | ✓ WIRED | All 3 occurrences confined to e2e job (lines 109, 132, 141); no other job references it |
| `ci.yml` (e2e) | `internal/e2e/e2e_test.go` env contract | `env:` block in "Run E2E suite" step | ✓ WIRED | `E2E_BASE_URL`, `DATABASE_URL`, `API_KEY_SALT`, `WEBHOOK_SIGNING_SECRET`, `E2E_S3_DIAL_ADDR` (lines 122-129) match exactly the contract documented in `internal/e2e/e2e_test.go:7-21` (required: `E2E_BASE_URL`, `DATABASE_URL`, `API_KEY_SALT`; optional: `E2E_S3_DIAL_ADDR`, `WEBHOOK_SIGNING_SECRET`) |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|--------------|-------------|--------------|--------|----------|
| CI-01 | 19-01, 19-02 | Gate check (gofmt/vet/build/test), required | ✓ SATISFIED | Truths #1, #2, #10 |
| CI-02 | 19-01, 19-02 | `-race` full-package required check | ✓ SATISFIED | Truth #3 |
| CI-03 | 19-01, 19-02 | All 5 images build via bake w/ per-target gha cache | ✓ SATISFIED (CACHED-hit confirmation pending) | Truths #4, #5 |
| CI-04 | 19-01, 19-02 | Live E2E advisory/required, teardown, artifact, concurrency | ✓ SATISFIED | Truths #6, #7 |

Note: `.planning/REQUIREMENTS.md` checkboxes for CI-01..04 are still unchecked (`[ ]`) and the requirements-coverage table lists them "Pending" — this is a tracking-file staleness issue, not a code gap; the underlying implementation and live-run evidence satisfy all four. Recommend the orchestrator update `REQUIREMENTS.md` checkboxes once this verification lands.

### Anti-Patterns Found

None. `grep` for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER|placeholder|coming soon|not yet implemented` across `.github/workflows/ci.yml` and `docker-compose.e2e.yml` returned no matches.

### Human Verification Required

#### 1. Confirm gha cache actually HITS on the second docker-build run

**Test:** With `gh auth login`, run `gh run view 29207810893 --log | grep -i cached` (or open the `docker-build` job log for run `92c43a0` / ID `29207810893` in the GitHub UI) and look for buildkit `CACHED` markers on the layers reused from run `29207275908`'s warmed scopes.
**Expected:** `CACHED` (or equivalent cache-hit indicators) appear for the bulk of build layers; the 268s run-2 duration reflects reused cache, not a from-scratch rebuild that merely finished inside the 20-minute bound.
**Why human:** Anonymous GitHub API access is rate-limited and cannot download job logs; `gh` was unauthenticated throughout plan 19-02 execution. This verifier deliberately did not re-query the rate-limited API per the orchestrator's explicit instruction. The workflow's cache *configuration* is provably correct by static reading of `ci.yml` (per-target scopes, matching cache-to/cache-from keys across the docker-build and e2e jobs) — what remains unconfirmed is only the runtime cache-HIT behavior, which a config bug (e.g., 6 scopes collectively exceeding the ~10GB per-repo gha-cache quota) could silently defeat while the job still succeeds by falling back to a full rebuild.

### Judgment Calls (per verification scope)

- **"Green from the pipeline's first run" (goal narrative) vs. the two-run reality:** The literal first live run (`15d1c3e`, ID 29207275908) had gate/race/docker-build green but e2e red, due to a rate-limit misconfiguration in the E2E-only compose override (production rate limits were never wrong; the fast public runner simply polled faster than local dev ever had). I do not treat this as a phase-goal failure: (a) the 4 enumerated ROADMAP Success Criteria — the operative verification contract — do not themselves say "on the first run"; that phrasing lives only in the descriptive goal sentence. (b) The run-1 failure exercised exactly the SC4 failure-path machinery (compose-logs artifact, `if: always()` teardown) that a clean first run would have left unproven. (c) The fix was confined to a test-only file (`docker-compose.e2e.yml`), diagnosed from the failure artifact as designed, and the very next run was fully green — this is the pipeline "exercising the codebase" and catching a real (if environmental, not application) bug, arguably the intended purpose of a CI pipeline's first run rather than a violation of it. Recorded here, not scored as a gap.
- **SC1 "red required check" without a real broken PR:** Accepted as VERIFIED on the strength of the commands being simple, well-known, deterministic toolchain invocations (`gofmt -l`, `go vet`, `go build`, `go test`) whose non-zero-exit-on-violation behavior needs no live proof, combined with the 19-01 local negative-proof (`gofmt -l` on a deliberately malformed file printed its path). No override needed — this is a reasoned engineering judgment, not a deviation from an SC.
- **SC3 CACHED-layer evidence:** See Human Verification item above — routed as UNCERTAIN rather than silently accepted, because runtime cache-hit behavior is not deducible from static config alone (unlike SC1/SC2's deterministic commands) and the specific "6 scopes vs. ~10GB quota" thrashing risk was explicitly named as a design concern in 19-01's own SUMMARY.

## Gaps Summary

No blocking gaps. One must-have (CACHED-layer confirmation on the second docker-build run) is UNCERTAIN and requires an operator with `gh auth` (or manual browser log inspection) to close out — everything else, including the live green run (independently corroborated via the un-rate-limited badge endpoint), the rate-limit fix scoping, and all structural/wiring checks, is VERIFIED.

---

*Verified: 2026-07-12*
*Verifier: Claude (gsd-verifier)*

## Operator Resolution (2026-07-13)

The operator reviewed the human_needed item and directed phase close on the strength of
run 2 (29207810893) being fully green (badge: passing on main). The CACHED-log-line
inspection of the docker-build job is recorded as an ACCEPTED OPTIONAL RESIDUAL: cache
config is structurally verified (10/11 truths), both live builds succeeded within their
20-minute bound, and a silent-full-rebuild scenario affects only build speed, not
correctness. Corroboration path remains documented: `gh run view 29207810893 --log |
grep -i cached` after `gh auth login`. Status flipped to passed per the operator's
checkpoint decision.
