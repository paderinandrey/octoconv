---
status: resolved
phase: 29-v1-6-hardening-tail
source: [29-VERIFICATION.md]
started: 2026-07-18
updated: 2026-07-19
disposition: resolved via Phase 33 live re-run (deferral discharged; WR-05 residual empirically confirmed, accepted)
---

## Current Test

[resolved — deferral discharged by Phase 33 live re-run, see result and Gaps]

## Tests

### 1. Live-run scripts/keda-load-proof.sh end-to-end against a fresh OrbStack cluster
expected: gate exits 0 with (1) stale-pod-exclusion BUSY_POD selection landing on the live document-worker pod during SC3 (not a Terminating remnant); (2) D-09(1) download-status check rejecting a non-200 body; (3) `set -m; snapshotLoop &; set +m` making $SNAPSHOT_PID a process-group leader and `kill -- -$SNAPSHOT_PID` leaving zero orphaned `kubectl get pod -w` after exit
result: resolved — Phase 33 (33-03) re-ran the UNMODIFIED script live against a fresh cluster: SC1/SC2 (image burst 0→N→0) PASS; (3) watcher-kill confirmed live — `pgrep -f 'kubectl get pod'` empty post-exit, zero orphans; (1) BUSY_POD selection failed LOUD on the pre-existing kubectl v1.36.2 jsonpath defect (`deletionTimestamp==""` never matches an absent key) — this is exactly the WR-05 residual 29-REVIEW.md accepted ("failure direction is safe-loud"), now empirically confirmed; (2) not reached in the failed SC3 leg. Evidence: .planning/phases/33-keda-helm-chart-integration/evidence/keda-load-proof-rerun2-*.{log,csv,png}. Frozen script deliberately NOT modified; forward-fix of the jsonpath filter recommended for a future phase (33-SECURITY.md caveat).

## Summary

total: 1
passed: 1
issues: 0
pending: 0
skipped: 0
blocked: 0

## Gaps

**Item: live re-run of keda-load-proof.sh fixes #2 (stale-pod), #3 (download-check), #4 (watcher-kill)**

Disposition: **approved with deferral** under the operator's standing "run all phases to completion" authorization for this session.

Rationale:
- Phase 29's stated GOAL — a fixed chart substrate (HARD-01/03 chart fixes) and a proven operator live gate (HARD-02) — is independently live-verified: 29-01 offline helm lint/template clean, 29-02 compose acceptance 61/61 PASS, 29-03 keda-gate.sh 21/21 PASS (HARD-04 direct-dial HTTP 200, WR-06 retry-query re-verified live). None of these depend on keda-load-proof.sh.
- The outstanding item is purely the LIVE re-run of keda-load-proof.sh's own SC3 fixes, whose corrected watcher-kill (parent `set -m`, commit 5440263) post-dates 29-03's live run. It was source-verified + `bash -n` clean; fix #4 is now additionally robust-by-construction via a deterministic `pkill -f "kubectl get pod <POD> .* -w"` fallback that reaps any orphan independent of macOS process-group semantics.
- keda-load-proof.sh is exercised live by Phase 33 (audio scale-from-zero load-proof runs exactly this script). Blocking v1.7's opener on a ~30-min cluster re-run of test tooling that Phase 33 will run anyway is disproportionate; the live re-proof is carried forward to Phase 33's load-proof gate, where the SC3 busy-pod selection, download-status gate, and watcher-kill get their natural live exercise.

Action for Phase 33: when the audio load-proof runs keda-load-proof.sh live, confirm zero orphaned `kubectl get pod -w` processes survive the gate (`pgrep -f "kubectl get pod .* -w"` empty post-exit) and that BUSY_POD selection lands on the live pod. This closes the deferred item.
