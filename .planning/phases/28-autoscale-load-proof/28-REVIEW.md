---
phase: 28-autoscale-load-proof
reviewed: 2026-07-16T21:45:00Z
depth: standard
files_reviewed: 10
files_reviewed_list:
  - deploy/chart/octoconv/templates/deployment-worker.yaml
  - deploy/chart/octoconv/templates/deployment-document-worker.yaml
  - deploy/chart/octoconv/templates/deployment-chromium-worker.yaml
  - deploy/chart/octoconv/templates/scaledobject-document.yaml
  - deploy/chart/octoconv/values.yaml
  - deploy/chart/octoconv/values-loadproof.yaml
  - scripts/keda-gate.sh
  - scripts/keda-load-proof.sh
  - scripts/fixtures/gen_heavy_docx.py
  - scripts/fixtures/render_evidence.py
findings:
  critical: 0
  warning: 6
  info: 9
  total: 15
status: issues_found
---

# Phase 28: Code Review Report

**Reviewed:** 2026-07-16T21:45:00Z
**Depth:** standard
**Files Reviewed:** 10
**Status:** issues_found

## Summary

Reviewed the Phase 28 autoscale load-proof surface: the WR-02 conditional
`spec.replicas` on the three KEDA-scaled Deployments, the values-gated HPA
scaleDown stabilization override on the document ScaledObject, the load-proof
values overlay, the load-proof gate script (`keda-load-proof.sh`), and the two
Python fixture scripts. `keda-gate.sh` was verified via `git diff 4b9bc23^..4b9bc23`
to be comment-only (STEP 6 comment rewrite; assertions and loop bytes unchanged),
as claimed.

Cross-references verified sound:

- `octoconv.commonEnv` emits only `envFrom:` (`_helpers.tpl`), so the new
  `extraEnv`-driven `env:` block in `deployment-document-worker.yaml` does NOT
  create a duplicate YAML key, and the "explicit env beats envFrom" precedence
  claim in its comment is correct Kubernetes behavior.
- Helm map-merge of `values-loadproof.yaml` over `values.yaml` preserves
  `worker.resources.limits.memory` while overriding only `cpu` — the
  "memory is inherited" comment is accurate.
- `jobs.started_at`, `jobs.finished_at`, `job_events.from_status/to_status`
  all exist (`internal/db/migrations/0001_init.sql:63-64,107-108`) — the psql
  triple-check queries are valid.
- The `result_url`→`download_url` fallback in the gate matches the API's actual
  field (`internal/api/handlers.go:570`).
- `worker-image-scaledobject` name used by the gate matches
  `scaledobject-image.yaml:17`.
- Redaction works in practice: the committed transcript
  (`evidence/gate-transcript-20260717T100342Z.log`) contains only
  `[REDACTED, 43 chars]` for the API key; no `X-Amz-Signature` or raw key
  appears in any committed evidence file.

No critical/security findings. Six warnings — mostly assertion-robustness gaps
in the gate (one false-PASS path, one race in victim selection, one leaked
watcher process) and two portability landmines in the Python fixtures — plus
nine informational items.

## Narrative Findings (AI reviewer)

## Warnings

### WR-01: `scaleDownStabilizationSeconds: 0` is silently treated as "unset"

**File:** `deploy/chart/octoconv/templates/scaledobject-document.yaml:38`
**Issue:** `{{- if .Values.keda.document.scaleDownStabilizationSeconds }}` uses
Go-template truthiness, in which `0` is falsy. `stabilizationWindowSeconds: 0`
is a legitimate HPA value meaning "scale down immediately, no stabilization" —
an operator setting it to `0` would get the exact opposite: the block is
omitted and the Kubernetes 300s default applies, with no error or warning.
The long header comment documents null-vs-set semantics but not this edge.
**Fix:**
```yaml
{{- if ne .Values.keda.document.scaleDownStabilizationSeconds nil }}
advanced:
  ...
        stabilizationWindowSeconds: {{ .Values.keda.document.scaleDownStabilizationSeconds }}
```
(or document explicitly next to the value in `values.yaml:157` that `0` is not
a supported value for this knob).

### WR-02: SC3 busy-pod identification can select a stale Terminating pod

**File:** `scripts/keda-load-proof.sh:699-705`
**Issue:** `BUSY_POD` is chosen as the document-worker pod with the earliest
`creationTimestamp`. But the fresh-install document-worker pod (WR-02
omitted-replicas default of 1) is scaled to 0 before SC3; the settle loop at
lines 641-652 breaks the instant `status.replicas` reaches 0, and
`status.replicas` excludes pods that merely carry a `deletionTimestamp`. With
`terminationGracePeriodSeconds: 330`, that old pod can still exist in
Terminating state when the long job's new pod comes up seconds later — and it
has the earliest `creationTimestamp`, so the `pod-deletion-cost=-1000`
annotation lands on a dying pod instead of the long-job pod, silently
invalidating the deterministic victim selection SC3 depends on. An idle worker
usually exits fast on SIGTERM, so this passed live, but it is a real flake
window.
**Fix:** exclude terminating pods when identifying pod1:
```bash
BUSY_POD=$(kubectl get pod -n "$NAMESPACE" -l "app.kubernetes.io/component=document-worker" \
  --field-selector=status.phase=Running \
  --sort-by=.metadata.creationTimestamp \
  -o jsonpath='{.items[?(@.metadata.deletionTimestamp=="")].metadata.name}' 2>/dev/null | awk '{print $1}')
```
(or filter `-o json` through a `deletionTimestamp == null` check.)

### WR-03: D-09(1) result-download check accepts an S3 error body as success

**File:** `scripts/keda-load-proof.sh:836-844`
**Issue:** The download probe uses `curl -s ... -w '%{size_download}'` with no
`-f` and no HTTP-status capture. A MinIO `403 AccessDenied` / `404 NoSuchKey`
XML error body is a few hundred bytes, so `RESULT_BYTES > 0` and the gate
prints `PASS: D-09(1) -- long job result downloads` for a download that
actually failed. For a script whose exit code "IS the gate", this is a
false-PASS path on the flagship graceful-downscale proof.
**Fix:**
```bash
RESULT_META=$(curl -s --connect-to "${RESULT_HOST}:${RESULT_PORT}:127.0.0.1:${MINIO_LOCAL_PORT}" \
  -o /tmp/keda-loadproof-long-result.bin -w '%{http_code} %{size_download}' "$RESULT_URL")
RESULT_CODE=${RESULT_META%% *}; RESULT_BYTES=${RESULT_META##* }
if [ "$RESULT_CODE" != "200" ] || [ "${RESULT_BYTES:-0}" -le 0 ]; then
  echo "FAIL: D-09(1) -- result download returned HTTP $RESULT_CODE / $RESULT_BYTES bytes" >&2
  exit 1
fi
```

### WR-04: killing `SNAPSHOT_PID` leaks an orphaned `kubectl get pod -w` watcher

**File:** `scripts/keda-load-proof.sh:735-751, 885-887, 162-165`
**Issue:** `snapshotLoop &` runs as a background subshell; its body spawns a
`kubectl get pod -w | while read` pipeline as child processes.
`kill "$SNAPSHOT_PID"` (both the STEP 8 stop and the teardown path) signals
only the subshell — the pipeline is reparented and keeps running. A watch on a
named pod does not exit after the DELETED event, so the orphaned `kubectl -w`
can outlive the entire gate, contradicting the script's own guarantee at line
77 ("nothing left running after EXIT" / D-12), and its `while read` half can
keep appending `read_ts=` lines to the committed `SC3_TIMESTAMPS_FILE` after
the gate believes capture has stopped.
**Fix:** run the watcher in its own process group and kill the group:
```bash
( set -m; snapshotLoop ) &   # or: setsid bash -c snapshotLoop &
SNAPSHOT_PID=$!
...
kill -- -"$SNAPSHOT_PID" >/dev/null 2>&1 || true
```
(alternatively `pkill -P "$SNAPSHOT_PID"` before killing the subshell).

### WR-05: `render_evidence.py` requires Python >= 3.11 but the interpreter is unpinned

**File:** `scripts/fixtures/render_evidence.py:72` (caller: `scripts/keda-load-proof.sh:622`)
**Issue:** The sampler writes timestamps as `%Y-%m-%dT%H:%M:%SZ`, and
`datetime.fromisoformat(row["timestamp"])` only accepts the trailing `Z` on
Python 3.11+. The script is invoked as `uv run --with matplotlib python3 ...`
with no `--python` pin — on a host whose `python3` resolves below 3.11, every
row raises `ValueError`, the render dies, and (because rendering happens after
all live SC1/SC2 assertions passed) `set -e` converts a fully-passed live run
into a gate FAIL with all its cluster time wasted.
**Fix:** either pin the interpreter in the caller
(`uv run --python 3.12 --with matplotlib ...`) or parse defensively:
```python
ts.append(datetime.fromisoformat(row["timestamp"].replace("Z", "+00:00")))
```

### WR-06: `gen_heavy_docx.py` silently drops images when CWD is not the repo root

**File:** `scripts/fixtures/gen_heavy_docx.py:33, 63, 72`
**Issue:** `SAMPLE_IMAGE` is a CWD-relative path. The gate happens to `cd` to
the repo root first, but any other invocation (the docstring's own example
command run from elsewhere, a future caller, manual calibration) silently sets
`have_sample_image = False` and generates a materially lighter document while
reporting the same `--page-units`. Since the entire point of this file is D-07
calibration ("page-units N ≈ 200s conversion"), a silently image-free fixture
invalidates the calibration with zero signal — a later gate run would inherit
wrong timing assumptions.
**Fix:** resolve the path relative to the script, and warn when absent:
```python
from pathlib import Path
SAMPLE_IMAGE = str(Path(__file__).resolve().parents[2] / "internal" / "e2e" / "testdata" / "sample.png")
...
if not have_sample_image:
    print(f"WARNING: {SAMPLE_IMAGE} not found -- generating WITHOUT images; "
          "calibration will not match image-bearing runs", file=sys.stderr)
```

## Info

### IN-01: metric-value grep only matches integer quantities

**File:** `scripts/keda-load-proof.sh:496, 589`
**Issue:** `grep -o '"value":"[0-9]*"'` cannot match a Kubernetes
milli-quantity (e.g. `"1500m"`), which the external metrics API emits for
non-integral values. The sampler would record such a sample as `0` (CSV
evidence corruption); the drain loop safely treats it as "not drained". With
`sum()` over integer gauges this is currently unreachable, but any future
`avg()`/rate-based trigger query breaks it silently.
**Fix:** `grep -o '"value":"[0-9m.]*"'` plus normalization, or parse with
`jq -r '.items[0].value'`.

### IN-02: `code=` assigned but never used in status-poll loops

**File:** `scripts/keda-load-proof.sh:416, 674, 757, 796`
**Issue:** The `code=$(curl -s -o ... -w '%{http_code}' ...)` capture is dead
in all four job-status polls — only the body grep is consumed, so a persistent
401/500 shows up as an opaque timeout instead of a loud HTTP failure. (Same
pre-existing pattern in `keda-gate.sh:419`, out of scope per the comment-only
diff.)
**Fix:** either drop the capture or check `[ "$code" = "200" ]` and FAIL fast.

### IN-03: `grep -c ... || echo 0` duplicates the zero

**File:** `scripts/keda-load-proof.sh:884`
**Issue:** `grep -c` prints `0` AND exits 1 on zero matches, so the fallback
`|| echo 0` produces `0\n0` inside the command substitution, garbling the
"watcher lines:" transcript line.
**Fix:** `grep -c 'read_ts=' "$SC3_TIMESTAMPS_FILE" 2>/dev/null || true` (the
count is already printed) or use `awk 'END{print NR}'`.

### IN-04: one-time replicas reset on the first upgrade that crosses WR-02

**File:** `deploy/chart/octoconv/templates/deployment-worker.yaml:23-31` (same in document/chromium variants)
**Issue:** Upgrading an existing release from a chart revision that rendered
`replicas:` to this one makes Helm's three-way merge delete the field, which
the API server re-defaults to `1` — a one-time scale-up of any class currently
at 0. All subsequent upgrades are clean (field absent on both sides). The
in-template comment describes steady-state behavior but not this migration
step.
**Fix:** add one sentence to the template comment / phase SUMMARY noting the
expected one-time bump on the first post-WR-02 upgrade.

### IN-05: dead fallback label selector `app=document-worker`

**File:** `scripts/keda-load-proof.sh:702-703`
**Issue:** The chart labels pods only with `app.kubernetes.io/*` keys
(`_helpers.tpl`); no template sets a bare `app:` label, so the fallback lookup
can never match and only obscures the real failure mode of the primary lookup.
**Fix:** delete the fallback branch.

### IN-06: graceful-exit check uses AND, accepting mixed reason/exit states

**File:** `scripts/keda-load-proof.sh:952`
**Issue:** `[ "$POD_TERM_REASON" != "Completed" ] && [ "$POD_TERM_EXIT" != "0" ]`
fails only when BOTH mismatch — `reason=Error exit=0` or `reason=Completed
exit=1` would pass. Container runtimes couple the two in practice, and the
snapshot-file fallback can leave one field empty (which this lenience
absorbs), so this may be intentional — but it is undocumented lenience in a
proof assertion.
**Fix:** if intentional, say so in the comment; otherwise assert
`reason==Completed || exit==0` explicitly per-field with a NOTE when the other
field is empty.

### IN-07: `tr -d '[:space:]'` mangles the psql timestamp before parsing

**File:** `scripts/keda-load-proof.sh:912-913, 940`
**Issue:** `finished_at` comes back as `2026-07-17 10:03:42.123+00`; piping
through `tr -d '[:space:]'` removes the internal space, producing
`2026-07-1710:03:42...`. Parsing then only succeeds because strptime treats a
literal space in the format as "zero or more whitespace" and caps `%d`/`%H` at
two digits — a coincidence, not a design. Any change (locale, `to_char`,
different `tr`) breaks D-09(3) epoch comparison.
**Fix:** make the DB emit an unambiguous format:
```sql
SELECT to_char(finished_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM jobs WHERE id='...';
```
then parse with the same `%Y-%m-%dT%H:%M:%SZ` format as `SIGTERM_TS`.

### IN-08: script exit does not wait for the `tee` process substitution

**File:** `scripts/keda-load-proof.sh:90`
**Issue:** `exec > >(tee "$LOG_FILE") 2>&1` — bash does not wait for process
substitutions on exit, so the final teardown PASS/FAIL lines can be truncated
from the committed transcript (the artifact D-01/D-03 depend on).
**Fix:** in `teardown()`, `sleep 1`/`exec 1>&- 2>&-` before `exit`, or on
bash>=4.4 `wait $!` after the `exec`.

### IN-09: sampler hard-stops at 600s with no truncation signal

**File:** `scripts/keda-load-proof.sh:489-501`
**Issue:** `SAMPLE_UNTIL_EPOCH = now + 600` silently ends the CSV if fixture
synthesis (a 90MP Pillow gradient) + burst + peak window + drain + downscale
exceeds 10 minutes; the SC1/SC2 evidence PNG would be missing its tail with no
FAIL or NOTE. Current live timings fit, but nothing detects the truncation.
**Fix:** after killing the sampler, compare the CSV's last timestamp against
`SAMPLE_UNTIL_EPOCH` and print a loud NOTE if the cap was hit (or raise the
cap and rely solely on the explicit kill).

---

_Reviewed: 2026-07-16T21:45:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
