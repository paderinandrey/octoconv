---
phase: 26-operator-presets-rest
verified: 2026-07-14T14:05:00Z
status: gaps_found
score: 4/5 must-haves verified
overrides_applied: 0
gaps:
  - truth: "An operator client (ID in OPERATOR_CLIENT_IDS) can create/list/show/update/deactivate system-scope presets via /v1/system/presets"
    status: failed
    reason: "Deactivate-then-recreate of a system preset name always fails with a raw 500 instead of succeeding (or, worst case, returning 409). Root cause is a pre-existing bug in internal/presets/repo.go's Create (hardcoded `const version = 1` + a duplicate check that only looks at ACTIVE rows) combined with the presets_system_uq partial unique index on (name, version), which covers INACTIVE rows too. This bug predates Phase 26 (inherited from Phase 18/20's already scope-agnostic PresetAdmin, which this phase was explicitly instructed to reuse 'unchanged') but Phase 26 is what newly exposes it as an operator-reachable REST workflow for system-scope presets for the first time -- 'deactivate a bad preset, recreate it later' is an ordinary admin action implied by the no-hard-delete design, and it currently has no recovery path short of manual SQL. Confirmed by direct code reading (repo.go:89-110, 0001_init.sql:35), consistent with 26-REVIEW.md CR-01. Not caught by this phase's own test matrix because system_presets_handlers_test.go exercises a fakePresetAdmin, not the real repo -- the deficiency is only visible by tracing into internal/presets/repo.go, which is exactly what code review did and unit tests structurally cannot."
    artifacts:
      - path: "internal/presets/repo.go"
        issue: "Create() (lines 79-111) hardcodes version=1 and its pre-insert existence check only matches `is_active` rows; presets_system_uq (0001_init.sql:35) uniquely indexes (name, version) across ALL rows including inactive ones, so a second Create for a previously-deactivated name collides on the unique index. The resulting pgx unique-violation is not mapped to presets.ErrAlreadyExists, so system_presets_handlers.go's handleCreateSystemPreset (only matches errors.Is(err, presets.ErrAlreadyExists)) falls through to a generic 500."
    missing:
      - "Fix repo.Create to insert at MAX(version)+1 across all rows (active+inactive) for the (scope, client_id, name) tuple -- mirroring Update's bump-on-update semantics -- so deactivate-then-recreate produces a new active version instead of colliding on the unique index."
      - "Map the underlying unique-violation (pgcode 23505) to presets.ErrAlreadyExists so a genuine concurrent-duplicate race also returns 409 instead of 500 (also closes the race noted in 26-REVIEW.md CR-01's last paragraph)."
      - "Add a repo-level test: create -> deactivate -> create (same name, same scope) must succeed with a new version, not error; add a corresponding handler-level 500-check test for the system-scope REST path per 26-REVIEW.md's fix suggestion."
---

# Phase 26: Operator System-Presets REST Verification Report

**Phase Goal:** System-scope presets are manageable over REST by operator clients only, with no new auth model and no schema change.
**Verified:** 2026-07-14T14:05:00Z
**Status:** gaps_found
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | An operator client (ID in OPERATOR_CLIENT_IDS) can create/list/show/update/deactivate system-scope presets via /v1/system/presets (SC1) | ✗ FAILED (partial) | All 5 verbs pass for a fresh preset name (`TestSystemPresets_OperatorSucceedsForAllVerbs`, `go test ./internal/api/...` green). BUT: deactivate → re-create of the SAME name always 500s (confirmed by direct code trace: `internal/presets/repo.go:89-111` hardcodes `version=1` and only excludes active-row duplicates, while `presets_system_uq` on `(name, version)` — `internal/db/migrations/0001_init.sql:35` — covers inactive rows too; the resulting unique-violation is unmapped and surfaces as a raw 500). This is an ordinary operator workflow the new REST surface makes reachable for the first time on system scope. See CR-01 in 26-REVIEW.md, independently reconfirmed here by reading the referenced source lines. |
| 2 | A non-operator client gets a byte-identical no-leak 404 (never 403) on every /v1/system/presets route (SC2) | ✓ VERIFIED | `requireOperator` (`internal/api/system_presets_handlers.go:54-67`) calls the identical `writeError(w, http.StatusNotFound, noSuchPreset)` used for a genuine missing preset. Tests assert byte-for-byte body equality via `bytes.Equal` against a `writeError`-constructed reference response, for both the unit-level gate (`TestRequireOperator_NonOperatorNoLeak404`) and all 5 verbs (`TestSystemPresets_NonOperatorNoLeak404`). No 403 status appears anywhere in the new code. |
| 3 | Empty/unset OPERATOR_CLIENT_IDS = zero operators (fail-closed): even the resolved caller gets 404 (part of SC3) | ✓ VERIFIED | `ParseOperatorClientIDs("")` returns an empty non-nil map, nil error (`system_presets_handlers.go:26-30`); `NewServer` normalizes a nil `Config.OperatorClientIDs` to `map[uuid.UUID]struct{}{}` (`api.go:147-149`). `TestRequireOperator_EmptyAllowlistDeniesEveryone` and `TestSystemPresets_EmptyAllowlistDeniesEveryone` both pass, proving even the resolved test client is denied when the set is empty. |
| 4 | A malformed OPERATOR_CLIENT_IDS aborts API startup loudly (fail-loud), never silently skipping the bad entry (part of SC3) | ✓ VERIFIED | `ParseOperatorClientIDs` returns a non-nil error naming the bad token on ANY malformed UUID, including when it's mixed with good ones (`TestParseOperatorClientIDs/malformed_uuid_errors`, `.../one_bad_token_among_good_ones_errors` — both pass). `cmd/api/main.go:92-95` calls `log.Fatalf("OPERATOR_CLIENT_IDS: %v", err)` on that error, aborting startup before `NewServer` runs. |
| 5 | Operator-ness is never surfaced in any response body (Claude's Discretion, additional truth) | ✓ VERIFIED | `TestSystemPresets_OperatorSucceedsForAllVerbs` explicitly asserts the decoded JSON body of every successful verb contains no `operator`/`is_operator`/`operator_id` key; `presetResponse` (reused from `presets_handlers.go`) has no such field to begin with. |

**Score:** 4/5 truths verified (Truth 1 partially fails on a reachable defect — see Gaps).

**Success criteria mapping:** SC2 and SC3 (env-only gate, zero migrations, no second auth system, fail-closed, fail-loud) are fully verified. SC1 is verified for the literal create/list/show/update/deactivate actions on a fresh name, but the full "manageable" lifecycle (deactivate a preset, later recreate the same name — the explicit purpose of soft-delete/no-hard-delete) is broken by a reachable, code-confirmed defect (CR-01).

### Zero-Migration / No-Second-Auth-System Check

- `git show d185650 e7f1036 d8ef06b --stat` — no files under `internal/db/migrations/` touched by any of the three phase commits; `internal/db/migrations/` still ends at `0005_html_engine.sql` (unrelated, pre-existing). **Confirmed: zero migrations.**
- No new auth/identity source: `requireOperator` reads `auth.ClientFromContext` — the same identity the existing `auth.Middleware` already resolves via API key — and checks membership in an in-memory `map[uuid.UUID]struct{}`. No new table, no new header, no new credential type. **Confirmed: no second auth system.**

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/api/system_presets_handlers.go` | `ParseOperatorClientIDs`, `requireOperator` middleware, 5 system-scope handlers | ✓ VERIFIED | All present; `func (s *Server) requireOperator` confirmed via direct `grep` (the automated artifact-check tool flagged a false-positive "missing pattern" due to a regex-escaping mismatch in its own pattern string — manually reconciled). |
| `internal/api/system_presets_handlers_test.go` | operator vs non-operator vs unset-allowlist matrix + parser strictness tests | ✓ VERIFIED | 8 parser sub-tests + 3 gate sub-tests + 3×5-verb matrix, all passing. |
| `internal/api/routes.go` | `/v1/system/presets` subtree gated by `requireOperator` | ✓ VERIFIED | `r.Route("/system/presets", ...)` with `r.Use(s.requireOperator)` as the first middleware in the subtree, sibling to the existing `/v1/presets` subtree, both inside `/v1` (post-auth). |
| `internal/api/api.go` | `Config.OperatorClientIDs` + `Server.operators` set | ✓ VERIFIED | Both fields present; nil-to-empty normalization at construction confirmed. |
| `deploy/chart/octoconv/templates/configmap.yaml` | `OPERATOR_CLIENT_IDS` env key | ✓ VERIFIED | `OPERATOR_CLIENT_IDS: {{ .Values.api.operatorClientIds | default "" | quote }}` present; `helm template` renders both the empty default and a `--set`-overridden value. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| `cmd/api/main.go` | `api.ParseOperatorClientIDs` | `os.Getenv("OPERATOR_CLIENT_IDS")` parsed once at startup, fail-loud | ✓ WIRED | `main.go:92-95` calls the parser once and `log.Fatalf`s on error. |
| `internal/api/routes.go` | `s.requireOperator` | chi subtree middleware after `auth.Middleware` | ✓ WIRED | Registered via `r.Use(s.requireOperator)` inside `/v1/system/presets`, nested under `/v1`'s `auth.Middleware`. |
| `internal/api/system_presets_handlers.go` | `s.presetAdmin` | Create/Update/Deactivate/Get/List with `presets.ScopeSystem` + nil clientID | ✓ WIRED | Every handler calls the corresponding `PresetAdmin` method with `presets.ScopeSystem` and `nil` — confirmed via direct read of all 5 handlers. |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Operator can CRUD a fresh system preset via the real chi router (httptest, fake PresetAdmin) | `go test ./internal/api/... -run 'SystemPreset\|Operator' -v` | All 15 subtests pass (create 201, list 200, show 200, update 200, deactivate 200) | ✓ PASS |
| Non-operator / empty-allowlist get byte-identical 404 on every verb | same command | All 10 subtests pass, `bytes.Equal` assertions hold | ✓ PASS |
| Deactivate → re-create of the same system preset name (full lifecycle, against the REAL repo, not a fake) | Not runnable without a live Postgres in this environment; verified instead by direct source trace of `internal/presets/repo.go:89-111` + `internal/db/migrations/0001_init.sql:35` | Hardcoded `version=1` + duplicate-check-active-only + unique index covering inactive rows ⇒ deterministic unique-violation ⇒ unmapped ⇒ 500 | ✗ FAIL (see Gaps) |
| `go build ./...`, `go vet ./internal/api/... ./cmd/api/...`, `gofmt -l internal/api cmd/api` | direct execution | All clean, no output | ✓ PASS |
| `helm template` renders `OPERATOR_CLIENT_IDS` (default empty + `--set` override) | direct execution | `OPERATOR_CLIENT_IDS: ""` and `OPERATOR_CLIENT_IDS: "11111111-2222-3333-4444-555555555555"` both rendered | ✓ PASS |

### Probe Execution

No `scripts/*/tests/probe-*.sh` declared by this phase's PLAN/SUMMARY, and none found under `scripts/`. `scripts/presets-rest-acceptance.sh` exists but was NOT extended with a system-scope section in this plan (D-06's live-gate extension is explicitly deferred per 26-01-SUMMARY.md's "Next Phase Readiness" section). Step 7c: **SKIPPED (no probes declared for this phase; existing acceptance script not extended by this plan)**.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| OPER-01 | 26-01-PLAN.md | system-scope presets managed via REST from OPERATOR_CLIENT_IDS allowlist (env-only, zero migrations); non-operator system-write → 404-no-leak | ⚠ PARTIALLY SATISFIED | Env-only gate, zero migrations, 404-no-leak, fail-closed, fail-loud are all fully satisfied. The "managed via REST" clause has a reachable gap: deactivate-then-recreate breaks (CR-01). REQUIREMENTS.md still shows `[ ]` unchecked for OPER-01 — consistent with this being not-yet-fully-closed. |

No orphaned requirements: REQUIREMENTS.md's phase-26 rollup lists only OPER-01, which is exactly what 26-01-PLAN.md's frontmatter declares.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/presets/repo.go` | 89-111 | Reachable correctness bug (CR-01): hardcoded version + active-only duplicate check vs. an all-rows unique index | 🛑 Blocker (for phase-goal "manageable" claim) | Deactivate→recreate of a system preset name 500s, permanently "bricking" that name via the API. Root cause is pre-existing (Phase 18/20), not introduced by Phase 26, but Phase 26 is what exposes it as an operator-facing REST workflow for the first time. |
| `internal/api/system_presets_handlers.go` | 97-103, 184 | WR-01: `target_format` accepted with zero validation on system create/update | ⚠ Warning | An operator typo creates a globally-visible, resolvable-everywhere bad preset; only surfaces at job-submit time to an unrelated client. Non-blocking for this phase's literal SCs but a real operational risk given system scope's blast radius. |
| `internal/api/presets_handlers.go` (shared `validPresetName`) | 97-99 | WR-02: preset name charset unrestricted (allows `/`, `%`, whitespace) | ⚠ Warning | Combined with CR-01, a badly-named system preset can become unmanageable via REST (unfixable without SQL). Pre-existing helper shared with the user-scope path (Phase 20), not introduced by Phase 26. |
| `deploy/chart/octoconv/templates/configmap.yaml`, `docker-compose.yml` | configmap:42, compose api.environment (untouched) | WR-03: `OPERATOR_CLIENT_IDS` not passed through in `docker-compose.yml`'s `api` service | ⚠ Warning | The chart/`.env.example` surface is correct, but the local compose stack — which 26-CONTEXT.md D-06/D-07 designate as this phase's live-gate bed — cannot currently receive the variable at all, meaning the deferred live gate (recreate scenario included) cannot be run as planned until this is fixed. |
| `internal/api/system_presets_handlers.go` | 113-117, 193-197 | IN-01: non-atomic write-then-read-back can 500 or return a stale version under a race | ℹ️ Info | Mirrors the pre-existing user-scope pattern; not a new introduction. |
| `.env.example` | 22 | IN-02: inline comment on `OPERATOR_CLIENT_IDS=` is not tolerated by its own parser (unlike `firstField`-wrapped numeric envs) | ℹ️ Info | Cosmetic/documentation-only risk if the example line is copied verbatim into a real `.env`. |

No `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` markers found in any file this phase created or modified.

### Human Verification Required

None. All findings above are resolvable by code inspection and automated tests; no visual, real-time, or external-service behavior is in scope for this phase.

### Gaps Summary

Phase 26 delivers exactly what it set out to build: a second, narrower authorization gate (`requireOperator`) layered on the existing auth boundary, a `/v1/system/presets` REST subtree that hardcodes `presets.ScopeSystem` + nil `clientID` against the *unchanged* `PresetAdmin` interface, an env-only allowlist that is fail-closed when empty and fail-loud when malformed, and chart/env plumbing for it — all correctly wired and unit-tested, with zero migrations and no second auth system. SC2 and SC3 are fully verified with no caveats.

The one blocking gap is **CR-01**: a deactivate-then-recreate of the same system preset name always 500s, because `internal/presets/repo.go`'s `Create` (a pre-existing method from Phase 18/20 that this phase was explicitly told to reuse *unchanged*) hardcodes `version = 1` and only checks for active-row duplicates, while the DB's partial unique index covers inactive rows too. This is genuinely inherited debt — Phase 26's own code is not at fault, and fixing it does not require touching the `PresetAdmin` interface's signature (only its `Create` implementation) — but it is now reachable by any operator over REST for the first time, on an action ("deactivate a bad preset, recreate it later") that the no-hard-delete design implies is normal. Per the instructions to distinguish phase-goal gaps from pre-existing debt: this is pre-existing debt, but it directly undermines this phase's own "manageable" success criterion for a normal admin lifecycle action, so it is reported as a phase-blocking gap here rather than swept aside. A follow-up plan should fix `repo.Create` (see `missing:` in frontmatter) before Phase 26 / this milestone is considered closed. WR-01/WR-02/WR-03/IN-01/IN-02 are secondary, non-blocking warnings worth tracking but do not gate this verification's status.

---

_Verified: 2026-07-14T14:05:00Z_
_Verifier: Claude (gsd-verifier)_
