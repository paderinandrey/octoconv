---
phase: 29-v1-6-hardening-tail
plan: 02
subsystem: testing
tags: [compose, operator-presets, acceptance-gate, system-presets, no-leak-404, bash]

# Dependency graph
requires:
  - phase: 26-operator-presets-rest
    provides: "/v1/system/presets REST surface + requireOperator/ParseOperatorClientIDs (the operator authorization + no-leak 404 semantics this plan proves live)"
provides:
  - "docker-compose.yml api service reads OPERATOR_CLIENT_IDS from the shell env (fail-closed empty default) — closes WR-03 compose-passthrough gap"
  - "presets-rest-acceptance.sh operator system-scope section: operator CRUD on /v1/system/presets, byte-identical non-operator no-leak 404, cross-client system-preset job usability, proven live against compose (61/61 assertions)"
affects: [operator-presets, compose-acceptance, hardening]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Optional-env ${VAR:-} passthrough inside a compose YAML scalar (first use in docker-compose.yml)"
    - "Live operator-authorization acceptance via compose api force-recreate to reload OPERATOR_CLIENT_IDS env"

key-files:
  created: []
  modified:
    - docker-compose.yml
    - scripts/presets-rest-acceptance.sh

key-decisions:
  - "Used docker compose up -d --force-recreate api (not down/up) to reload OPERATOR_CLIENT_IDS — minimal disruption, keeps postgres/redis/minio and their data warm across the operator-env swap"
  - "Ran the live gate against the COMPOSE stack (not k8s) — cheaper, and the operator authorization path has no k8s-specific API behavior"

patterns-established:
  - "Operator no-leak 404 asserted three ways: non-operator GET of a REAL active system preset, GET of a genuinely-nonexistent name, and non-operator LIST — all three bodies byte-identical to prove requireOperator leaks no existence oracle"
  - "System-preset job usability proven by having the REGULAR (non-operator) client submit a real image job referencing the operator-created preset and polling it to done"

requirements-completed: [HARD-02]

# Metrics
duration: ~40min
completed: 2026-07-18
---

# Phase 29 Plan 02: Operator Compose Acceptance Summary

**Wired the missing `OPERATOR_CLIENT_IDS` compose passthrough and proved the operator system-presets path end-to-end against the live compose stack: operator CRUD on `/v1/system/presets`, a byte-identical non-operator no-leak 404, and cross-client system-preset job usability (61/61 acceptance assertions passing).**

## Performance

- **Duration:** ~40 min (incl. live compose build + gate run)
- **Started:** 2026-07-18T (plan execution)
- **Completed:** 2026-07-18
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- `docker-compose.yml` api service now reads `OPERATOR_CLIENT_IDS: "${OPERATOR_CLIENT_IDS:-}"` from the shell env with a fail-closed empty default — the first `${VAR:-}` shell-passthrough entry in the file, closing WR-03 from 26-REVIEW (D-05).
- Extended `scripts/presets-rest-acceptance.sh` with a Phase 29 operator system-scope section that mints an operator + a regular client, exports the operator UUID into `OPERATOR_CLIENT_IDS`, force-recreates the compose api to load it, then drives the full operator matrix (D-04).
- Live compose gate passes end-to-end: **ALL 61 ASSERTIONS PASSED** (exit 0), including operator create/list/show/update/deactivate, the byte-identical non-operator no-leak 404 (real vs nonexistent, plus LIST), and a real image job submitted by the non-operator client using the operator-created system preset reaching `done`.

## Task Commits

Each task was committed atomically:

1. **Task 1: OPERATOR_CLIENT_IDS compose passthrough (D-05/WR-03)** - `df6a60e` (feat)
2. **Task 2: operator system-scope acceptance section + live gate (D-04)** - `7ffbf55` (feat)

## Files Created/Modified
- `docker-compose.yml` - Added `OPERATOR_CLIENT_IDS: "${OPERATOR_CLIENT_IDS:-}"` to the api service `environment:` block near `API_KEY_SALT`, with a fail-closed-on-empty comment mirroring the Go-side `values.yaml` wording.
- `scripts/presets-rest-acceptance.sh` - Added a Phase 29 HARD-02/OPER-01 operator system-scope section (reusing the existing `assert_*`, `http_json`, client-mint, and no-leak-404 harness primitives; added a small `http_post_job` multipart helper), plus header docstring and PASS-footer updates. Fixed no Go code.

## Decisions Made
- **api reload via `--force-recreate api`** (not down/up): reloads the operator env with minimal disruption, keeping postgres/redis/minio and their volumes warm across the swap (Claude's discretion per D-05/CONTEXT).
- **Compose live gate, not k8s**: the operator authorization path has no k8s-specific behavior; compose is the cheaper, sufficient substrate (D-04/D-05).
- **Non-operator 404 asserted three ways** (real active preset, nonexistent name, and LIST) all byte-identical — a stronger no-leak proof than a single comparison, matching the T-29-11 threat-register mitigation.

## Deviations from Plan

None - plan executed exactly as written. No Go code touched; only `docker-compose.yml` and `scripts/presets-rest-acceptance.sh` as specified.

## Issues Encountered
- **api crash-loop on first stack bring-up (compose orchestration, not a code issue):** During the initial background bring-up the `octoconv-api` container crash-looped with `storage: bucket "octoconv" does not exist` because the one-shot `createbucket` service had not yet created the bucket when api first started. Resolved by bringing up the full stack (so `createbucket` ran and exited 0), confirming the `octoconv` bucket exists in MinIO, then `docker compose up -d --force-recreate api` to clear the restart backoff — api became healthy immediately (`/healthz` → `{"s3":"ok",...}`). The acceptance script's own `up -d` sequence then re-created the bucket idempotently and the full run passed 61/61.

## Threat Flags

None found — no new security-relevant surface introduced. The change is a compose env passthrough plus a bash acceptance section exercising the existing (Phase 26) operator surface. Threat-register mitigations T-29-10 (EoP: requireOperator gate) and T-29-11 (info-disclosure: byte-identical no-leak 404) are now positively asserted by the live gate.

## OrbStack Discipline
- Verified no k8s octoconv workloads were running before bringing compose up (`kubectl get pods -A | grep octoconv` empty).
- Tore the stack down after the acceptance run: `docker compose ... down -v` — confirmed zero remaining `octoconv-*` containers and volumes removed.

## Next Phase Readiness
- HARD-02 (OPER-01) closed: the operator system-presets path is now provable on compose, and compose declares operators via `OPERATOR_CLIENT_IDS`.
- No blockers. Plan 29-01 (chart robustness, wave-1 parallel) touches only `deploy/chart/...` — zero overlap with the two files this plan modified.

## Self-Check: PASSED

- `docker-compose.yml` — present, `OPERATOR_CLIENT_IDS: "${OPERATOR_CLIENT_IDS:-}"` on line 95.
- `scripts/presets-rest-acceptance.sh` — present, operator system-scope section added.
- `29-02-SUMMARY.md` — present.
- Commit `df6a60e` (Task 1) — found.
- Commit `7ffbf55` (Task 2) — found.
- Live gate: ALL 61 ASSERTIONS PASSED, exit 0.

---
*Phase: 29-v1-6-hardening-tail*
*Completed: 2026-07-18*
