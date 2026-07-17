---
phase: 29-v1-6-hardening-tail
reviewed: 2026-07-18T00:00:00Z
depth: standard
files_reviewed: 12
files_reviewed_list:
  - deploy/chart/octoconv/templates/scaledobject-image.yaml
  - deploy/chart/octoconv/templates/scaledobject-document.yaml
  - deploy/chart/octoconv/templates/scaledobject-html.yaml
  - deploy/chart/octoconv/templates/_helpers.tpl
  - deploy/chart/octoconv/templates/prometheus.yaml
  - deploy/chart/octoconv/values.yaml
  - docker-compose.yml
  - scripts/presets-rest-acceptance.sh
  - scripts/keda-load-proof.sh
  - scripts/keda-gate.sh
  - scripts/fixtures/render_evidence.py
  - scripts/fixtures/gen_heavy_docx.py
findings:
  critical: 0
  warning: 5
  info: 3
  total: 8
status: issues_found
---

# Phase 29: Code Review Report

**Reviewed:** 2026-07-18T00:00:00Z
**Depth:** standard
**Files Reviewed:** 12
**Status:** issues_found

## Summary

Phase 29 (v1.6 hardening tail) touches Helm KEDA robustness (ignoreNullValues,
retry-inclusive PromQL, prometheus config checksum, cooldown invariant),
compose operator passthrough, and six gate-tooling fixes plus a presigned
direct-dial step. The Helm templates and Python fixtures are correct and
well-annotated. The findings are concentrated in the bash gate tooling: a
process-group kill that is unlikely to actually reach the orphaned watcher it
targets, and several `set -euo pipefail` robustness gaps that break the
project's own established `|| true` discipline. None are production-shipping
BLOCKERs (all findings live in test/gate/compose tooling), but two of them
undercut the very hardening this phase claims to deliver.

No hardcoded production secrets, injection vectors, or logic errors were found
in the shipping Helm chart. The `scaledobject-document.yaml` falsy-0 guard
(`hasKey` AND `ne ... nil`) is correct, the prometheus named-template checksum
correctly breaks the self-reference recursion, and the ScaledObject
co-dependency gate (`and keda.enabled prometheus.enabled`) is sound.

## Warnings

### WR-01: `set -m` inside a backgrounded subshell likely does NOT create a killable process group — orphaned `kubectl -w` watcher can survive the gate

**File:** `scripts/keda-load-proof.sh:768` (and the kills at `:166`, `:913`)
**Issue:** The watcher is launched as `( set -m; snapshotLoop ) &` and later
killed with `kill -- -"$SNAPSHOT_PID"`. This is intended to kill the whole
process group so the reparented `kubectl get pod -w | while read` pipeline
cannot outlive the gate. But the parent script does not enable job control
(`set -m`) before backgrounding the subshell, so at fork time the subshell is
placed in the parent's process group, not its own — `$SNAPSHOT_PID` is not a
process-group leader. Enabling `set -m` *inside* the already-forked subshell
cannot retroactively make the subshell its own group leader; instead it causes
the subshell's *children* (the `kubectl | while` pipeline) to be spawned into
yet another new process group whose PGID is the pipeline leader's PID, not
`$SNAPSHOT_PID`. Consequently `kill -- -"$SNAPSHOT_PID"` targets a group that
either does not exist (silently swallowed by `|| true`) or does not contain the
kubectl watch pipeline. The exact orphan the WR-04 fix set out to eliminate can
still leak.
**Fix:** Make the subshell a real group leader from the parent, or use a new
session:
```bash
# Option A: enable monitor mode in the PARENT around the launch
set -m
( snapshotLoop ) &
SNAPSHOT_PID=$!
set +m
# ... kill -- -"$SNAPSHOT_PID"   # now $SNAPSHOT_PID IS the PGID

# Option B (simpler, no job-control juggling): own session via setsid
setsid bash -c 'snapshotLoop' &   # or capture the kubectl pipeline PID directly
```
At minimum, verify with `ps -o pid,pgid,cmd` during a live run that the kubectl
watch process actually shares `$SNAPSHOT_PID` as its PGID before trusting the
group kill.

### WR-02: Job-status / job-id `grep` command-substitutions omit `|| true` — an unexpected response body silently aborts the gate with no FAIL message

**File:** `scripts/presets-rest-acceptance.sh:504`, `scripts/presets-rest-acceptance.sh:514`; `scripts/keda-gate.sh:458`
**Issue:** Under `set -euo pipefail`, a pipeline `grep -o … | head -1 | cut …`
returns non-zero when `grep` finds no match (pipefail propagates it), and a
failing command substitution in a bare assignment (`status=$(…)`) triggers
`set -e` and exits the script *with no diagnostic*. The happy path always has a
`status`/`job_id` field so this never fires in normal runs, but any unexpected
response (5xx HTML error page, empty body, rate-limit JSON without `status`)
kills the gate confusingly instead of producing a loud `FAIL:` line. This is
inconsistent with the project's own established discipline: `keda-gate.sh:512`
and `keda-load-proof.sh:420,514,828` all append `|| true` to the identical
pattern precisely to avoid this (see the explanatory comment at
`keda-load-proof.sh:823-827`).
**Fix:** Append `|| true` to each of these command substitutions so emptiness is
caught by the subsequent explicit assertion rather than by a silent `set -e`
death:
```bash
status=$(grep -o '"status":"[^"]*"' "$WORKDIR/resp-opsys-job-poll.json" | head -1 | cut -d'"' -f4 || true)
```

### WR-03: Burst-fixture `uv run` is missing the `--python 3.12` interpreter pin applied to every other invocation

**File:** `scripts/keda-load-proof.sh:523`
**Issue:** Phase 29 pinned the Python interpreter (`--python 3.12`) on the
gen_heavy_docx and render_evidence invocations (`:410`, `:625`, `:660`), but the
Pillow burst-fixture synthesis at `:523` reads `uv run --with pillow python3 -c`
with no `--python` pin. `uv` will resolve whatever interpreter it discovers,
which can differ across hosts and defeats the reproducibility the pin was added
for. The blast radius is limited (a resolution failure falls back to
`sample.png`), but the fixture that actually reproduces the 90MP burst load is
the one left unpinned.
**Fix:** Pin it consistently:
```bash
if ! uv run --python 3.12 --with pillow python3 -c "
```

### WR-04: `docker-compose.yml` uses mutable `:latest` tags for MinIO, contradicting the pinning discipline documented and applied elsewhere

**File:** `docker-compose.yml:33` (`minio/minio:latest`), `docker-compose.yml:53` (`minio/mc:latest`)
**Issue:** `values.yaml:108-112` pins MinIO to concrete RELEASE tags with an
explicit warning ("Concrete RELEASE tags — never :latest. OrbStack re-pulls
:latest even from its shared local image store"), and compose itself pins
`asynqmon:0.7.2`, `postgres:18`, `redis:8`. The MinIO server and `mc` client in
compose remain on `:latest`, so the local dev stack silently drifts and can
diverge from the Helm-tested MinIO release — the same "OrbStack re-pull"
landmine the chart comment calls out, now living in the file this phase edited
for the OPERATOR_CLIENT_IDS passthrough.
**Fix:** Pin compose to the same RELEASE tags the chart uses:
```yaml
minio:
  image: minio/minio:RELEASE.2025-09-07T16-13-09Z
createbucket:
  image: minio/mc:RELEASE.2025-08-13T08-35-41Z
```

### WR-05: `BUSY_POD` selection depends on kubectl jsonpath missing-field semantics (`deletionTimestamp==""`)

**File:** `scripts/keda-load-proof.sh:711`, `scripts/keda-load-proof.sh:716`
**Issue:** `--field-selector=status.phase=Running` does NOT exclude Terminating
pods (a terminating pod keeps `phase=Running` and gains a `deletionTimestamp`),
so correctness of the "earliest live document-worker pod" selection rests
entirely on the jsonpath filter `{.items[?(@.metadata.deletionTimestamp=="")]}`.
Comparing an *absent* field to `""` in client-go jsonpath is
implementation-fragile and has varied across kubectl versions. If the filter
ever matches the terminating 330s-grace remnant the comment describes (earlier
`creationTimestamp`), the annotation and all downstream SC3 termination
assertions target the wrong pod. In the current code the failure direction is
safe-loud (the wrong pod is already gone → `assert_nonempty POD_TERM_FINISHED`
fails), but the SC3 proof is silently invalidated on a kubectl behavior change.
**Fix:** Select non-terminating pods robustly, e.g. filter client-side rather
than trusting the missing-field comparison:
```bash
BUSY_POD=$(kubectl get pod -n "$NAMESPACE" -l app.kubernetes.io/component=document-worker \
  --field-selector=status.phase=Running --sort-by=.metadata.creationTimestamp \
  -o go-template='{{range .items}}{{if not .metadata.deletionTimestamp}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}' \
  | head -1)
```

## Info

### IN-01: `RESULT_PORT` fallback to 80 is wrong for an https presigned URL

**File:** `scripts/keda-load-proof.sh:846-848`
**Issue:** When the presigned URL carries no explicit port, `RESULT_PORT`
defaults to `80`. For an `https://` presigned URL the correct default is `443`.
Harmless today because presigned URLs are `http://…:9000` (S3_USE_SSL=false with
an explicit port), but it silently breaks if TLS is ever enabled on MinIO.
**Fix:** Derive the default from the URL scheme (`443` when the URL starts with
`https://`, else `80`).

### IN-02: Dev credentials are hardcoded in compose and chart defaults (acceptable, noted for completeness)

**File:** `docker-compose.yml:89,182,212` and `deploy/chart/octoconv/values.yaml:114-115`
**Issue:** `API_KEY_SALT`/`WEBHOOK_SIGNING_SECRET` (`dev-only-change-me-in-real-deploys`)
and `minio` root credentials (`minioadmin`) are checked in. These are clearly
labelled dev-only and CLAUDE.md documents that real values live in
`values-local.yaml`, so this is consistent with the internal-only trust model —
flagged only so it is not mistaken for a leak. Ensure no non-local deployment
path sources these files.
**Fix:** None required for local dev; confirm production overlays never inherit
these defaults.

### IN-03: `keda-gate.sh:359` (`postJob` helper) shares the same unguarded `grep | head | cut` pattern

**File:** `scripts/keda-gate.sh:359`
**Issue:** The `job_id` extraction inside `postJob` has no `|| true`. It is
reached only after an explicit `HTTP_STATUS == 202` check (so a body without
`job_id` is unlikely), which is why this is Info rather than folded into WR-02,
but it is the same latent fragility.
**Fix:** Add `|| true` for consistency with the guarded call sites.

---

_Reviewed: 2026-07-18T00:00:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
