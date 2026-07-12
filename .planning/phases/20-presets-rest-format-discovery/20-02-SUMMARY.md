---
phase: 20-presets-rest-format-discovery
plan: 02
subsystem: testing
tags: [curl, bash, live-acceptance, presets, rest, postgres, docker-compose]

# Dependency graph
requires:
  - phase: 20-presets-rest-format-discovery (plan 01)
    provides: internal/api/presets_handlers.go, internal/api/formats_handlers.go, /v1/presets and /v1/formats routes
provides:
  - "scripts/presets-rest-acceptance.sh: the live hard gate proving PRAPI-01/02/03 end-to-end against the real compose stack"
affects: [21-mcp-presets-tools, future-rest-regressions]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "REST live-gate script (curl -w '%{http_code}' + jq + docker exec psql), same discipline as scripts/presets-acceptance.sh (Phase 18) and prior phase live gates"

key-files:
  created: [scripts/presets-rest-acceptance.sh]
  modified: []

key-decisions:
  - "Reused scripts/presets-acceptance.sh's exact structural discipline (assert_eq/assert_contains helpers, psql_q, compose bring-up, /healthz wait loop, uuidgen SUFFIX, WORKDIR trap, PASS_COUNT tally) rather than inventing new conventions"
  - "Added assert_not_contains helper (new, not in Phase 18 script) to prove D-04's negative assertions (no id/client_id leak) loudly rather than via a fragile double-negative grep -v pipeline"
  - "Used jq for /v1/formats parsing (available per env_notes) instead of grep -o, since the response is a nested JSON object (engines -> class -> pairs) that grep can't safely traverse"
  - "Chose http_json (method+path+body+key) over the Phase 18 script's job-specific http_post/http_get wrappers, since presets REST needs PUT/DELETE with JSON bodies plus an unauthenticated variant for the 401 assertions (D-07)"

patterns-established:
  - "http_json helper generalizes the multipart-only http_post/http_get pattern from Phase 18 to arbitrary method + optional JSON body + optional Authorization header, usable by any future REST live-gate script in this repo"

requirements-completed: [PRAPI-01, PRAPI-02, PRAPI-03]

# Metrics
duration: ~20min
completed: 2026-07-13
---

# Phase 20 Plan 02: Live REST Acceptance Hard Gate Summary

**curl-driven live gate (scripts/presets-rest-acceptance.sh) proving all five /v1/presets verbs, mass-assignment resistance, byte-identical no-leak 404s, and registry-derived GET /v1/formats against the real compose stack — 42/42 assertions passed on first run**

## Performance

- **Duration:** ~20 min
- **Started:** 2026-07-12T23:10:00Z (approx.)
- **Completed:** 2026-07-12T23:32:36Z
- **Tasks:** 2 (author script, run live gate)
- **Files modified:** 1 created

## Accomplishments
- Authored `scripts/presets-rest-acceptance.sh`, an executable (`chmod +x`) curl/psql/jq-driven live gate modeled directly on `scripts/presets-acceptance.sh`'s structure (`set -euo pipefail`, `assert_eq`/`assert_contains`/`psql_q` helpers, compose bring-up with `-p octoconv --build api`, `/healthz` readiness loop, `uuidgen` SUFFIX naming, `mktemp` WORKDIR + trap cleanup)
- Ran the script end-to-end against the real compose stack (rebuilt `api` image containing the 20-01 handlers): **ALL 42 ASSERTIONS PASSED**, exit code 0, on the first attempt — no handler bugs found
- Re-ran the script a second time to confirm idempotency (fresh uuid-suffixed names each run): also 0 exit / 42 assertions passed
- Proved every D-NN decision the plan enumerated:
  - **D-01/PRAPI-01:** create (201, version:1, scope:user), list (200, merged view), show own (200), show system (200, read-only), update (200, bump to version:2), deactivate (200/2xx, soft)
  - **D-02/P6:** POST body carrying `"scope":"system"` + a foreign client's UUID as `"client_id"` still produces a DB row with `scope='user'` and `client_id` = the CALLING client (psql-verified) — mass-assignment structurally impossible
  - **D-03/PRAPI-02:** duplicate active-name create -> 409; nonexistent / cross-client / system-scope-write all -> 404 with **byte-identical** bodies (`{"error":"preset not found"}`); update bumps version and psql confirms exactly one active row at v2 with v1 inactive; deactivated preset's rows still exist in the DB (count=2, no hard delete)
  - **D-04:** create response body verified to NOT contain `"id"` or `"client_id"` substrings
  - **D-10:** `GET /v1/presets` for client A returns both the caller's own preset AND the seeded system preset marked `"scope":"system"`; `GET /v1/presets/{sys-name}` as client A also shows it read-only with `scope:system`
  - **D-06/PRAPI-03:** `GET /v1/formats` returns a registry-derived `engines` map with 3 classes (`document`, `html`, `image`) each with a non-empty `pairs` array; confirmed the known pair `["png","webp"]` is present under `image` via `jq`
  - **D-07:** unauthenticated `GET /v1/formats` and `GET /v1/presets` both return 401

## Task Commits

Each task was committed atomically:

1. **Task 1: Author scripts/presets-rest-acceptance.sh** - `662d977` (feat)
2. **Task 2: Run the live hard gate against the compose stack** - no additional commit (execution-only task; script unchanged from Task 1 since it passed on the first run)

**Plan metadata:** (this SUMMARY commit, added via `git add -f` since `.planning/` is gitignored)

## Files Created/Modified
- `scripts/presets-rest-acceptance.sh` - Live curl/psql/jq hard gate for `/v1/presets` CRUD + `/v1/formats`; 374 lines, executable, 42 labeled assertions

## Decisions Made
- Reused the Phase 18 `presets-acceptance.sh` script's exact structural discipline (see `key-decisions` above) for consistency with the project's established live-gate pattern
- Added `assert_not_contains` (new helper) for D-04's negative-leak assertions, and a generic `http_json` helper (method + JSON body + optional auth) to cover PUT/DELETE and the unauthenticated 401 cases that the job-upload-specific `http_post`/`http_get` wrappers in the Phase 18 script don't support

## Deviations from Plan

None - plan executed exactly as written. The script passed all 42 assertions on the very first live run against the rebuilt `api` image; no handler bugs were found requiring a follow-up gap in 20-01, and no assertion needed weakening.

## Issues Encountered
None. The stack was already partially up (`octoconv-db` running from a prior session); `docker compose -p octoconv ... up -d --build api` followed by `up -d` correctly rebuilt only the `api` image and brought up the remaining services (worker, minio, redis, chromium-worker, document-worker, webhook-workers, asynqmon, createbucket) without incident, and `/healthz` reported `{"postgres":"ok","redis":"ok","s3":"ok","status":"ok"}` well within the 60s timeout.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `scripts/presets-rest-acceptance.sh` is a durable, rerunnable regression gate for `/v1/presets` and `/v1/formats` (uuid-suffixed names avoid collisions on rerun); any future change to `internal/api/presets_handlers.go` or `internal/api/formats_handlers.go` can be validated against this same script
- Phase 21 (MCP presets tools) can rely on `GET /v1/presets`'s merged read-only system view (D-10) and `GET /v1/formats`'s registry-derived shape as now live-proven, stable contracts
- Compose stack (project `octoconv`) is left running per project convention, for inspection or immediate re-run

---
*Phase: 20-presets-rest-format-discovery*
*Completed: 2026-07-13*
