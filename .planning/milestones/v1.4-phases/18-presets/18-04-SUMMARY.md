---
phase: 18-presets
plan: 04
subsystem: testing
tags: [presets, live-e2e, docker-compose, bash, postgres, manage-presets, manage-clients]

# Dependency graph
requires:
  - phase: 18-presets (18-01/18-02/18-03)
    provides: internal/presets package, cmd/manage-presets CLI, handleCreateJob preset resolution/provenance/no-leak/re-validation wiring
provides:
  - "scripts/presets-acceptance.sh — a re-runnable live hard gate proving PRST-01..04/SC1-SC5 against the real docker-compose stack"
affects: [19-presets-followups, future-live-gate-authors]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Bash live-acceptance hard gate (set -euo pipefail, loud assert_eq/assert_contains helpers, uuid-suffixed names for re-run idempotency) — mirrors Phase 16/17 live-gate precedent"
    - "On-the-fly fixture derivation via the running image worker's vips CLI (docker cp in -> vips copy -> docker cp out) when the committed e2e fixture set lacks a needed source format (png->png is not a valid libvips pair; jpg source was needed for the shadowing test)"

key-files:
  created: [scripts/presets-acceptance.sh]
  modified: []

key-decisions:
  - "Added a 5th preset (S, user-scope only, no system counterpart) beyond the plan's P/Q/R so the no-leak test (item 9) has a genuinely nonexistent-for-client-B target — using P for that comparison would have hit the shadowing system fallback (200), not a 422, exactly as the plan's own fallback note anticipated"
  - "Generated a jpg fixture at runtime via the octoconv-worker container's vips CLI rather than adding a new binary fixture to internal/e2e/testdata — png->png is not a valid libvips conversion pair (Pairs() requires from != to), so the shadowing test (P: user target=png, system target=webp) needed a non-png source"

requirements-completed: [PRST-01, PRST-02, PRST-03, PRST-04]

duration: 45min
completed: 2026-07-12
---

# Phase 18 Plan 04: Presets Live Acceptance Hard Gate Summary

**scripts/presets-acceptance.sh — a 33-assertion live hard gate proving all five manage-presets CLI verbs plus preset resolution/provenance/shadowing/no-leak/re-validation against the real compose stack, passing twice in a row (fresh run + idempotent re-run) with a rebuilt api image.**

## Performance

- **Duration:** 45 min
- **Started:** 2026-07-12T18:29:00Z
- **Completed:** 2026-07-12T19:14:10Z
- **Tasks:** 1
- **Files modified:** 1 (created)

## Accomplishments
- Built `scripts/presets-acceptance.sh`: brings up the compose stack (`api` rebuilt with `--build`, everything else from existing images per the retry note), waits for `/healthz`, mints two API clients via `cmd/manage-clients`, and drives `cmd/manage-presets` through all five verbs with real assertions.
- SC1 (PRST-01): create (system + user scope, asserts version==1 five times), list (system scope shows P/Q/R; client-A scope shows the user-shadowed P with target png), show (asserts `target_format: webp` and `version: 1`), update (asserts printed `new version: 2`, then SQL-confirms exactly one active row at v2 and v1 now inactive), deactivate (asserts confirmation text, zero active rows, rows still present — no hard delete — then a POST with that preset → 422).
- SC2/D-08: POST `preset=Q` reaches `done`; `SELECT preset_name||'/'||preset_version FROM jobs` returns exactly `Q/1`.
- SC3/D-02: preset `P` resolves to `png` (user-scope override) for client A and `webp` (system) for client B — both proven via `SELECT target_format FROM jobs`.
- SC4/D-01/D-03: `preset`+`target` together → 422; a nonexistent preset and a cross-client-only preset (`S`, user-scoped for client A with no system counterpart) both return the byte-identical body `{"error":"unknown or inactive preset"}`.
- SC5/D-06: a preset inserted directly via SQL with `options={"margin_mm":9999}` (out of the current `[0,50]` range) is rejected 422 at job-creation time — proving stored opts are re-validated, never trusted.
- Ran the script twice back-to-back against the live stack; both runs exited 0 with all 33 assertions passing (uuid-suffixed preset/client names make it safely re-runnable).

## Task Commits

1. **Task 1: Scripted live acceptance hard gate (all five CLI verbs + resolution/no-leak/re-validation)** - `f60bbf5` (test)

**Plan metadata:** (this summary's commit, following)

## Files Created/Modified
- `scripts/presets-acceptance.sh` - live compose acceptance hard gate; `set -euo pipefail`, `assert_eq`/`assert_contains` loud-fail helpers, `psql_q`/`http_post`/`http_get` helpers, 10 numbered steps matching the plan's script spec plus a fixture-preparation step

## Decisions Made
- Added a fifth preset `S` (user-scope only, client A, no system counterpart) so the no-leak comparison in step 9 has a target that is genuinely absent for client B — reusing `P` (which has both a system and a user version) would have resolved successfully for client B via the system fallback, exactly the ambiguity the plan itself flagged as a fallback case.
- Derived a `jpg` fixture at script runtime from the committed `sample.png` via the running `octoconv-worker` container's `vips` CLI (`docker cp` in → `vips copy` → `docker cp` out), rather than committing a new binary fixture — `LibvipsConverter.Pairs()` only registers `from != to` pairs, so the shadowing test (system target `webp`, user target `png`) needed a source format that isn't `png` itself.
- Left the compose stack running after a successful run (per the plan's explicit instruction, matching the Phase 16/17 precedent) — the script's exit code is the gate, not the stack's lifecycle.

## Deviations from Plan

None — plan executed exactly as written; the one addition (preset `S`) was explicitly anticipated by the plan's own fallback language in step 9 ("if the system P makes this ambiguous, instead create a user-only preset for A with a name that has NO system counterpart").

## Issues Encountered

- **Worktree cwd drift (self-caught, no impact):** Early in this session, several exploratory `Read`/`Bash` calls used `cd /Users/apaderin/dev/octoconv` (the main checkout) instead of staying in the worktree. At the time both refs pointed at the identical commit (`194b246`), so no divergent state was read or built from the wrong tree; the `docker compose --build api` invocation that happened during this window built from content identical to the worktree. All file writes (the script itself) and the final `git commit` were correctly performed from the worktree path. No corrective action was needed beyond re-verifying `git rev-parse --abbrev-ref HEAD` returned `worktree-agent-a6c592c7c6eaa1c32` before committing.
- **Live run log (second/idempotent run), full assertion list:**
  ```
  === Phase 18 presets: live acceptance hard gate ===
  PASS: /healthz ready ({"postgres":"ok","redis":"ok","s3":"ok","status":"ok"})
  PASS: fixtures ready (sample.png, sample.html, sample.jpg)
  PASS: minted client A (8ded5afa-5aa7-4dd0-9eaa-3c1e466161d7) and client B (4b791d44-f608-42a7-ab02-5930514bdc22)
  PASS: create P (system) prints version 1 == 1
  PASS: create P (user, client A) prints version 1 == 1
  PASS: create Q (system) prints version 1 == 1
  PASS: create R (system) prints version 1 == 1
  PASS: create S (user-only, client A, no system counterpart) prints version 1 == 1
  PASS: list (system) contains P contains [pa-p-ce6414bc-6427-4627-a1b3-ace888f04a07]
  PASS: list (system) contains Q contains [pa-q-ce6414bc-6427-4627-a1b3-ace888f04a07]
  PASS: list (system) contains R contains [pa-r-ce6414bc-6427-4627-a1b3-ace888f04a07]
  PASS: list (client A) contains user-scope P contains [pa-p-ce6414bc-6427-4627-a1b3-ace888f04a07]
  PASS: list (client A) shows P's user-scope target png contains [png]
  PASS: show Q reports target_format webp contains [target_format: webp]
  PASS: show Q reports version 1 contains [version: 1]
  PASS: update R prints new version 2 == 2
  PASS: exactly one active R row after update == 1
  PASS: active R row is version 2 == 2
  PASS: R v1 is now inactive == f
  PASS: deactivate R confirms contains [deactivated: pa-r-ce6414bc-6427-4627-a1b3-ace888f04a07]
  PASS: zero active R rows after deactivate == 0
  PASS: R rows still exist after deactivate (count=2, no hard delete)
  PASS: POST with deactivated preset R -> 422 == 422
  PASS: POST preset=Q (client A) -> 202 == 202
  PASS: provenance job reaches done == done
  PASS: jobs table records preset provenance Q/1 == pa-q-ce6414bc-6427-4627-a1b3-ace888f04a07/1
  PASS: POST preset=P (client A) -> 202 == 202
  PASS: POST preset=P (client B) -> 202 == 202
  PASS: client A resolves preset P to user-scope target png (shadowing) == png
  PASS: client B resolves preset P to system-scope target webp == webp
  PASS: POST preset+target together -> 422 == 422
  PASS: POST nonexistent preset -> 422 == 422
  PASS: POST cross-client preset S (as client B) -> 422 == 422
  PASS: nonexistent-preset body matches the single no-leak error text == {"error":"unknown or inactive preset"}
  PASS: nonexistent vs cross-client 422 bodies are byte-identical (no leak) == {"error":"unknown or inactive preset"}
  PASS: POST preset with stored opts failing current validation -> 422 == 422

  === ALL 33 ASSERTIONS PASSED ===
  SC1 (all five CLI verbs): create/list/show/update/deactivate — PASS
  SC2 (provenance pa-q-ce6414bc-6427-4627-a1b3-ace888f04a07/1 persisted, job done): PASS
  SC3 (shadowing: A->png, B->webp): PASS
  SC4 (mutual exclusivity 422 + byte-identical no-leak 422): PASS
  SC5 (stored-opts re-validation 422): PASS
  ```

## User Setup Required

None - no external service configuration required. The script drives the existing local docker-compose stack directly; no new environment variables or manual dashboard steps were introduced.

## Next Phase Readiness
- Phase 18 (presets) now has a scripted, re-runnable live hard gate proving SC1-SC5 hold end-to-end on the real stack, closing out the phase's acceptance requirements (PRST-01..04).
- `scripts/presets-acceptance.sh` can be re-run at any time against the local compose stack (idempotent via uuid-suffixed names) as a regression check before merging future preset-related changes.
- No blockers for closing Phase 18; the compose stack was left running per plan instruction for inspection.

---
*Phase: 18-presets*
*Completed: 2026-07-12*

## Self-Check: PASSED
- FOUND: scripts/presets-acceptance.sh
- FOUND: f60bbf5 (git log --oneline --all)
- FOUND: .planning/phases/18-presets/18-04-SUMMARY.md
