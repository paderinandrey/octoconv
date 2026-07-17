---
phase: 29-v1-6-hardening-tail
verified: 2026-07-18T00:00:00Z
status: passed
score: 4/4 must-haves verified (1 human-verification item approved-with-deferral)
overrides_applied: 1
override_note: "Human-verification item (live re-run of keda-load-proof.sh SC3 fixes #2/#3/#4) approved-with-deferral to Phase 33 under operator standing authorization — see 29-HUMAN-UAT.md. Phase goal (chart substrate + operator gate) independently live-proven; watcher-kill additionally hardened with a deterministic pkill fallback."
human_verification:
  - test: "Live-run scripts/keda-load-proof.sh (the actual SC1-SC3 scale-from-N-to-0 load-proof scenario) end-to-end against a fresh OrbStack cluster"
    expected: "The gate completes 0-exit with: (1) the stale-pod-exclusion BUSY_POD selection landing on the genuinely-live document-worker pod during SC3, not a Terminating remnant; (2) the D-09(1) result-download check correctly rejecting a non-200 body (can be forced by a synthetic 403 to confirm the FAIL path, or trusted on the 200 happy path); (3) confirmation via `ps -o pid,pgid,cmd` that the corrected parent-level `set -m; snapshotLoop &; set +m` actually makes $SNAPSHOT_PID a process-group leader and that `kill -- -$SNAPSHOT_PID` leaves zero orphaned `kubectl get pod -w` processes after the gate exits"
    why_human: "Phase 29's only live gate execution was scripts/keda-gate.sh (21/21 PASS) — a separate, lighter smoke gate that does not exercise keda-load-proof.sh's SC3 busy-pod selection, the download-status gate, or the SC3 watcher/process-group kill at all. The process-group kill fix went through two iterations: the version exercised by the ORIGINAL 28-03 live run (`( set -m; snapshotLoop ) &`) was proven wrong by 29-REVIEW.md's WR-01 finding (job control enabled inside an already-forked subshell does not retroactively make it a group leader); the corrected version (`set -m` in the parent before backgrounding) was applied in follow-up commit 5440263, authored AFTER 29-03's live gate run and AFTER 29-REVIEW.md. This corrected version has NEVER been exercised by any live run — only `bash -n` and `grep` pattern matches, which cannot detect this class of process-group-targeting bug (the same class of bug that shipped, passed offline checks, and passed a live run in the version it is replacing). ROADMAP.md Phase 29 SC3 explicitly requires each of the six 28-REVIEW warnings be closed 'with its script/template diff AND a gate re-run' — that re-run has not occurred for fixes #2 (stale-pod), #3 (download check), or #4 (watcher kill) specifically."
---

# Phase 29: v1.6 Hardening Tail Verification

**Phase Goal:** Close the four pre-diagnosed v1.6 audit findings so the audio ScaledObject (Phase 33) is authored on a fixed chart substrate and the operator live gate is proven.
**Verified:** 2026-07-18T00:00:00Z
**Status:** human_needed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | HARD-01 (SC1): all three ScaledObjects use `ignoreNullValues: "false"`, sustained metric absence reads as a scaler error, `fallback.replicas: 1` holds one replica per class instead of scale-to-zero on live backlog | VERIFIED | `scaledobject-{image,document,html}.yaml` all contain `ignoreNullValues: "false"` (0 remaining `"true"`); rewritten fail-safe comments (no "genuinely empty queue" language); `helm template` render confirms exactly 3 occurrences, 0 old-value; live-discovered in 29-03 (`cd4dd19`) that the fallback-replicas mechanism actually fires on a fresh install (`ScaledObjectFallbackDeactivated` event observed) and the gate's own STEP 6 precondition depends on this behavior working correctly — i.e. the D-01 fail-safe WAS exercised live, not just rendered |
| 2 | HARD-01 (D-03): all three trigger queries count `state=~"pending\|active\|retry"` so a retry backlog scales a zeroed class up before the reconciler sweep | VERIFIED | `grep` across the trio: exactly 3 occurrences of the retry-inclusive query, 0 of the old `pending\|active"}` form; live keda-gate.sh run (per 29-03-SUMMARY) explicitly "re-verifying 29-01's WR-06 retry-inclusive PromQL behavior (all three classes scaled 0->1 from a real job)" |
| 3 | HARD-01 (D-02/WR-05 supporting fix): Prometheus pod-template `checksum/config` hashes the shared `octoconv.prometheusScrapeConfig` named-template (not a whole-file self-hash), no recursion | VERIFIED | `_helpers.tpl` defines `octoconv.prometheusScrapeConfig`; `prometheus.yaml` references it exactly twice (ConfigMap data + checksum annotation), 0 occurrences of `Template.BasePath`; `helm lint`/`helm template` both exit 0 (a recursive self-hash would hard-fail lint) |
| 4 | HARD-03 (D-06 fix #1): explicit `scaleDownStabilizationSeconds: 0` renders `stabilizationWindowSeconds: 0`; the production `null` default renders ZERO stabilization block | VERIFIED | `helm template` with values-local (real null default): 0 occurrences of `stabilizationWindowSeconds`. With `--set keda.document.scaleDownStabilizationSeconds=0`: exactly 1 occurrence of `stabilizationWindowSeconds: 0`. Guard source: `{{- if and (hasKey ... "scaleDownStabilizationSeconds") (ne ... nil) }}` — both conditions present, not `hasKey` alone |
| 5 | HARD-02 (SC2): operator can run a live acceptance script against compose exercising `/v1/system/presets` CRUD + byte-identical no-leak 404 for a non-operator; `OPERATOR_CLIENT_IDS` passed through compose api | VERIFIED | `docker-compose.yml:95` — `OPERATOR_CLIENT_IDS: "${OPERATOR_CLIENT_IDS:-}"` with fail-closed comment; `presets-rest-acceptance.sh` has a full system-scope section (operator CREATE/LIST/SHOW/UPDATE/DEACTIVATE, non-operator 404 on real preset + nonexistent name + LIST, all three byte-compared via `assert_eq`); `bash -n` clean; per 29-02-SUMMARY, live run against compose: 61/61 assertions PASS (trusted per task instructions — not re-run) |
| 6 | HARD-03 (SC3): all six 28-REVIEW gate-tooling warnings closed with script/template diff | VERIFIED (source), see human-verification item for live re-proof gap | All six confirmed present in current source: WR-01/falsy-0 (chart, `scaledobject-document.yaml:49`), WR-02/stale-pod (`keda-load-proof.sh:706-716`, `deletionTimestamp`/`status.phase=Running`), WR-03/download-check (`keda-load-proof.sh:861-872`, `RESULT_CODE`/`RESULT_BYTES` gated on 200), WR-04/orphaned-watcher (`keda-load-proof.sh:770-773`, corrected parent-level `set -m` per follow-up commit `5440263`), WR-05/interpreter-pin (`render_evidence.py` `.replace("Z","+00:00")` + 4x `uv run --python 3.12` call sites incl. the previously-unpinned pillow burst fixture), WR-06/CWD-relative (`gen_heavy_docx.py` `Path(__file__).resolve().parents[2]` + stderr WARNING). All parse/lint clean (`bash -n`, `ast.parse`). BUT: only the chart fix (WR-01) and, indirectly, the retry-query behavior were exercised by a live gate run in this phase — `keda-load-proof.sh` itself (the script that owns fixes #2/#3/#4) was never re-executed in Phase 29; see human-verification item |
| 7 | HARD-04 (SC4): presigned result URL resolves from OrbStack host via direct dial against a verified-healthy daemon, no port-forward/`--connect-to` workaround | VERIFIED | `keda-gate.sh` STEP 9b: `docker compose ps` empty pre-flight, `docker info` + `kubectl get nodes` daemon health pre-flight (loud-fail), direct `curl` on `download_url` with `--retry`, gated on HTTP 200 + nonzero bytes; 0 occurrences of `--connect-to` anywhere in the file; per 29-03-SUMMARY, live run: HTTP 200, 804 bytes (trusted per task instructions — not re-run) |
| 8 | D-08 (plan grouping): 29-01 is sole owner of ScaledObject/prometheus/values/_helpers.tpl edits; zero chart-file overlap with 29-02 | VERIFIED | `git show --stat` for 29-01 commits (`a07d71e`,`da861fd`) touches only chart files; 29-02 commits (`df6a60e`,`7ffbf55`) touch only `docker-compose.yml`/`scripts/presets-rest-acceptance.sh` — no overlap |

**Score:** 8/8 truths individually VERIFIED on source/offline evidence; 1 unresolved human-verification item (truth #6's live-reproof gap) forces overall status to `human_needed` per the gate decision rule (human verification items take priority over a clean score).

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `deploy/chart/octoconv/templates/scaledobject-image.yaml` | `ignoreNullValues:false` + retry-inclusive PromQL | VERIFIED | Confirmed via direct read + helm render |
| `deploy/chart/octoconv/templates/scaledobject-document.yaml` | same + hasKey/not-nil stabilization guard | VERIFIED | Confirmed via direct read + helm render (both null-default and explicit-0 cases) |
| `deploy/chart/octoconv/templates/scaledobject-html.yaml` | same as image | VERIFIED | Confirmed via direct read + helm render |
| `deploy/chart/octoconv/templates/_helpers.tpl` | `octoconv.prometheusScrapeConfig` named template | VERIFIED | `define` present, body matches original inline scrape-config |
| `deploy/chart/octoconv/templates/prometheus.yaml` | checksum/config via shared named-template, ConfigMap unchanged | VERIFIED | 2 references to the named-template, 0 `Template.BasePath`, `helm lint`/`template` exit 0 |
| `deploy/chart/octoconv/values.yaml` | cooldownPeriod invariant comment | VERIFIED | `INVARIANT (WR-06)` comment present, references `internal/queue/queue.go` retry schedules |
| `docker-compose.yml` | `OPERATOR_CLIENT_IDS` passthrough | VERIFIED | Line 95, fail-closed comment present |
| `scripts/presets-rest-acceptance.sh` | operator system-scope acceptance section | VERIFIED | Full CRUD + no-leak-404 (3-way byte comparison) + cross-client job usability section present, `bash -n` clean |
| `scripts/keda-load-proof.sh` | gate-tooling fixes #2-#5 + interpreter pins | VERIFIED (source only) | All patterns present and internally consistent; NOT re-exercised live in this phase (see human-verification item) |
| `scripts/fixtures/render_evidence.py` | defensive trailing-Z parse | VERIFIED | `.replace("Z", "+00:00")` present, `ast.parse` clean |
| `scripts/fixtures/gen_heavy_docx.py` | `__file__`-relative SAMPLE_IMAGE + warning | VERIFIED | `Path(__file__).resolve().parents[2]` + stderr `WARNING` present, `ast.parse` clean |
| `scripts/keda-gate.sh` | presigned direct-dial step + OrbStack pre-flight | VERIFIED | STEP 9b present: compose-ps pre-flight, daemon health pre-flight, direct curl, 0 `--connect-to` |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `scaledobject-*.yaml` | KEDA Prometheus scaler | `ignoreNullValues` field | WIRED | `ignoreNullValues: "false"` present in all 3, non-comment |
| `prometheus.yaml` (pod-template) | `octoconv.prometheusScrapeConfig` in `_helpers.tpl` | `include ... | sha256sum` | WIRED | Checksum annotation present, same named-template also feeds ConfigMap `data.prometheus.yml` |
| `scripts/presets-rest-acceptance.sh` | `docker-compose.yml` api service env | `OPERATOR_CLIENT_IDS` export + `--force-recreate api` | WIRED | Script exports var and force-recreates api; compose service reads it via `${OPERATOR_CLIENT_IDS:-}` |
| `scripts/presets-rest-acceptance.sh` | `/v1/system/presets` | operator `http_json` CRUD + non-operator 404 assertion | WIRED | Full CRUD sequence + 3-way byte-identical 404 assertion present |
| `scripts/keda-gate.sh` | MinIO presigned FQDN URL | direct `curl`, no `--connect-to` | WIRED | STEP 9b confirmed, 0 `--connect-to` anywhere in file |
| `scripts/keda-load-proof.sh` | SC3 busy-pod selection | `Running` + empty `deletionTimestamp` filter | WIRED (source) | Present at lines 706-716; NOT live-re-exercised this phase |

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|--------------|--------|----------|
| HARD-01 | 29-01 | KEDA null-empty semantics fix | SATISFIED | ignoreNullValues:false + retry query, verified above |
| HARD-02 | 29-02 | Operator compose acceptance + passthrough | SATISFIED | docker-compose.yml + presets-rest-acceptance.sh, live 61/61 per SUMMARY |
| HARD-03 | 29-01 (fix #1), 29-03 (fixes #2-#6) | Six 28-REVIEW gate-tooling warnings closed | SATISFIED (source); live-reproof gap flagged | See truth #6 above and human-verification item |
| HARD-04 | 29-03 | Presigned direct-dial recheck | SATISFIED | keda-gate.sh STEP 9b, live 200/804 bytes per SUMMARY |

No orphaned requirements: `.planning/REQUIREMENTS.md` maps HARD-01..04 exclusively to Phase 29, and all four appear in at least one plan's `requirements:` frontmatter field. Note: `.planning/REQUIREMENTS.md`'s checklist boxes for HARD-01..04 are still unchecked and its tracking table still shows "Pending" (lines 12-15, 74-77) — this is a documentation-sync gap, not a functional gap; flagged for the phase owner to update but not treated as a verification blocker since the code-level evidence is independently conclusive.

### Anti-Patterns Found

No `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER` markers found in any of the 12 files modified/reviewed by this phase. No stub returns, no empty handlers, no hardcoded-empty data flowing to rendered output.

`docker-compose.yml`'s MinIO `:latest` tags (flagged as WR-04 in `29-REVIEW.md`) were confirmed via `git log --follow` to originate in the original project-scaffold commit (`8f7af88`, 2026-06-29) — genuinely pre-existing debt, not introduced or touched by this phase (Phase 29 only added line 95, the `OPERATOR_CLIENT_IDS` line, to this file). Correctly out of scope per the task brief.

### Code-Review Follow-up Verification (29-REVIEW.md)

`29-REVIEW.md` (0 blockers / 5 warnings) was code-reviewed after all three plans landed. Follow-up commit `5440263` ("apply code-review findings") was inspected directly:

- **WR-01 (process-group kill bug):** FIXED. The subshell-internal `( set -m; snapshotLoop ) &` pattern (which does not make the subshell a group leader) was replaced with parent-level `set -m; snapshotLoop &; set +m` — this is the textbook-correct fix (confirmed by direct code reading; the reviewer's own suggested "Option A" was applied verbatim). Not yet live-re-verified (see human-verification item).
- **WR-02 (unguarded grep pipelines):** FIXED. `|| true` appended to the three flagged command substitutions in `keda-gate.sh:458` and `presets-rest-acceptance.sh:504,514` — confirmed via diff.
- **WR-03 (burst-fixture missing interpreter pin):** FIXED. `keda-load-proof.sh:523` now reads `uv run --python 3.12 --with pillow python3 -c` — confirmed all 4 `uv run --python` call sites present.
- **WR-04 (MinIO `:latest`):** Correctly left unfixed — confirmed pre-existing debt (see Anti-Patterns section), out of this phase's scope.
- **WR-05 (BUSY_POD jsonpath fragility):** Left as documented residual risk. The reviewer's own text notes the failure direction is safe-loud (a wrong-pod match causes a downstream `assert_nonempty` to fail rather than silently succeeding) — accepted without further code change, consistent with the task brief's framing.

### Human Verification Required

### 1. Live-run `scripts/keda-load-proof.sh` to reprove gate-tooling fixes #2-#4

**Test:** Execute `bash scripts/keda-load-proof.sh` end-to-end against a fresh OrbStack k8s cluster (the full SC1/SC2/SC3 load-proof scenario), then independently confirm via `ps -o pid,pgid,cmd` during the SC3 window that the `kubectl get pod -w | while read` watcher process shares `$SNAPSHOT_PID` as its process-group ID, and that zero `kubectl -w` processes survive the script's EXIT trap.

**Expected:** Gate exits 0; the busy-pod selection lands on the live long-job pod (not a Terminating remnant); the result-download check correctly gates on HTTP 200; the watcher process group is fully reaped on exit (no orphaned `kubectl get pod -w`).

**Why human:** This is the only unverified link in the phase. Phase 29's sole live gate execution was `scripts/keda-gate.sh` (21/21 PASS, confirmed via SUMMARY + source-code cross-check), which is a distinct, lighter script that does not exercise `keda-load-proof.sh`'s SC3 busy-pod/download/watcher code at all. The watcher-kill fix specifically went through a "passed offline checks AND passed a live run" cycle for a version later proven wrong by code review (29-REVIEW.md WR-01) — meaning bash -n/grep verification is demonstrably insufficient to catch this class of bug, and the corrected version has zero empirical verification of any kind. ROADMAP.md's Phase 29 SC3 wording ("each with its script/template diff and a gate re-run") is not fully satisfied for fixes #2/#3/#4 by repo evidence alone; a human should decide whether to schedule this re-run before Phase 33 depends on the load-proof gate's correctness, or accept the residual risk given the phase's own goal text is centered on the chart substrate (HARD-01, independently proven live) and the operator gate (HARD-02, independently proven live).

### Gaps Summary

No FAILED must-haves. All source-level evidence for HARD-01 through HARD-04 is present, internally consistent, and (for HARD-01's chart behavior, HARD-02, and HARD-04) independently corroborated by a live gate run described in the committed SUMMARYs. The single open item is an UNCERTAIN/WARNING-class gap: three of the six 28-REVIEW gate-tooling fixes living in `scripts/keda-load-proof.sh` (stale-pod exclusion, download-status gate, watcher process-group kill) have never been exercised by any live run in their current, corrected form — only by static analysis. This does not block the phase's stated goal (a fixed chart substrate for Phase 33's audio ScaledObject, and a proven operator gate), both of which rest on artifacts that WERE live-verified, but it is a legitimate gap against the literal ROADMAP SC3 wording and is surfaced for a human decision rather than silently accepted or silently failed.

---

_Verified: 2026-07-18T00:00:00Z_
_Verifier: Claude (gsd-verifier)_
