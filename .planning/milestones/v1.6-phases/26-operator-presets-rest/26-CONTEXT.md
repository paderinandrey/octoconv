# Phase 26: Operator System-Presets REST - Context

**Gathered:** 2026-07-14
**Status:** Ready for planning
**Source:** v1.6 research (synthesis Key Decision: env-allowlist + 404), user-approved roadmap

<domain>
## Phase Boundary

system-scope presets manageable over REST by operator clients only. `OPERATOR_CLIENT_IDS` env allowlist (comma-separated client UUIDs); non-operators attempting system-scope writes get 404-no-leak. Zero migrations. CLI stays the alternative management path. Small phase: extend existing presets REST handlers.

</domain>

<decisions>
## Implementation Decisions

- D-01: Operator identity = `OPERATOR_CLIENT_IDS` env (comma-separated UUIDs), parsed once in cmd/api/main.go (env-only-in-main convention), passed into NewServer as a set (map[uuid]struct{} or []uuid — small); empty/unset = NO operators (fail-closed)
- D-02: Surface shape: extend the EXISTING /v1/presets endpoints with an optional `scope=system` query/body dimension for operators (NOT a separate /v1/system/presets tree — reuse handlers, one scope switch), OR a separate subtree if the planner finds the existing DTO/queries cleaner that way — planner's call, but the semantics are fixed: operator + scope=system → system CRUD via the same presets.Repo (scope='system', client_id NULL); non-operator + scope=system write → the SAME no-leak 404 as foreign presets (project convention); reads of system presets remain available to everyone (merged view since Phase 20, unchanged)
- D-03: Bump-on-update / single-active-version / no-hard-delete semantics identical to client scope (same Repo methods with system scope args — verify Phase 18's Repo supports scope='system' writes via CLI paths already; REST reuses exactly those)
- D-04: 409 on duplicate system create; opts write-time validation same as client scope
- D-05: Chart: OPERATOR_CLIENT_IDS added to values (empty default) + api deployment env; .env.example entry
- D-06: Verification: handler tests (operator vs non-operator vs unset-allowlist matrix); LIVE GATE — extend scripts/presets-rest-acceptance.sh with a system-scope section (operator key CRUDs system preset; non-operator gets 404; system preset usable in a job by any client) run against the COMPOSE stack (cheaper than k8s; this phase touches no k8s-specific behavior — compose is the canonical API test bed; k8s chart env addition proven by helm template assert only)
- D-07: OrbStack/k8s NOT required for this phase's live gate (compose stack); remember compose-vs-k8s never both hot

### Claude's Discretion
- DTO/routing details; whether operator-ness is exposed in any response (suggest: no)
- Log tag for operator actions (suggest reason=system_preset_write log line consistent with existing patterns)

</decisions>

<canonical_refs>
## Canonical References

- `internal/api/presets_handlers.go` + api.go (PresetAdmin — the seam; Phase 20 semantics)
- `internal/presets/repo.go` (system-scope write support — CLI already does it)
- `cmd/manage-presets/main.go` (system-scope CLI semantics being mirrored)
- `cmd/api/main.go` (env parse point + NewServer wiring)
- `scripts/presets-rest-acceptance.sh` (live gate to extend)
- `deploy/chart/octoconv/values.yaml` + templates/deployment-api.yaml + configmap (D-05)
- `.planning/research/SUMMARY.md` (Key Decision rationale) + FEATURES.md (operator-auth options)

</canonical_refs>

<specifics>
## Specific Ideas

- The live gate needs TWO client keys (operator + regular): manage-clients ×2, operator's UUID into OPERATOR_CLIENT_IDS via compose env override or export
- K8SV2-03 (is_operator column) stays the documented future alternative

</specifics>

<deferred>
## Deferred Ideas

- is_operator column (K8SV2-03), per-operator audit trail, REST for operator management itself
</deferred>

---

*Phase: 26-operator-presets-rest*
*Context gathered: 2026-07-14*
