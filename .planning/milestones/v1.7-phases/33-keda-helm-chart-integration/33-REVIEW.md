---
phase: 33-keda-helm-chart-integration
reviewed: 2026-07-18T21:51:58Z
depth: standard
files_reviewed: 6
files_reviewed_list:
  - deploy/chart/octoconv/templates/deployment-audio-worker.yaml
  - deploy/chart/octoconv/templates/scaledobject-audio.yaml
  - deploy/chart/octoconv/templates/configmap.yaml
  - deploy/chart/octoconv/values.yaml
  - cmd/api/main.go
  - scripts/keda-audio-loadproof.sh
findings:
  critical: 0
  warning: 2
  info: 3
  total: 5
status: fixes_applied
fixed: 2
---

# Phase 33: Code Review Report

**Reviewed:** 2026-07-18T21:51:58Z
**Depth:** standard
**Files Reviewed:** 6
**Status:** fixes_applied (both Warnings fixed; Info findings tracked, out of fix scope)

## Summary

Reviewed the Phase 33 KEDA/Helm audio-class deliverables: audio-worker Deployment
template, audio ScaledObject, ConfigMap AUDIO_* keys + reconciler fix, values.yaml
audio blocks, the QueueAudio collector splice in `cmd/api/main.go`, and the new
`scripts/keda-audio-loadproof.sh` gate. Cross-referenced against sibling templates
(`deployment-worker.yaml`, `deployment-document-worker.yaml`,
`deployment-chromium-worker.yaml`, `scaledobject-document.yaml`), `_helpers.tpl`,
`internal/queue/queue.go`, `internal/metrics/queue_collector.go`,
`cmd/audio-worker/main.go`, `docker-compose.yml`, `Dockerfile.audio-worker`, and the
frozen sibling gate scripts. Chart rendering was verified live in both modes
(`helm template` with keda/prometheus on and off).

**Verified correct (adversarially cross-checked, not assumed):**

- **PromQL query** `sum(octoconv_queue_depth{queue="audio", state=~"pending|active|retry"})`
  matches the collector exactly: metric name and `{queue, state}` labels
  (`internal/metrics/queue_collector.go:9-11`), state values `pending`/`active`/`retry`
  (`:40-43`), and `queue.QueueAudio = convert.EngineAudio = "audio"`
  (`internal/queue/queue.go:38`, `internal/convert/convert.go:23`).
- **Collector splice** (`cmd/api/main.go:91-92`) registers all five queues
  (image, document, html, audio, webhook); the "ALL FOUR engine-class queues"
  comment remains accurate (four engine classes + webhook).
- **ConfigMap AUDIO_* keys** all match their Go consumers
  (`cmd/audio-worker/main.go`, `internal/queue/client.go:87-88`);
  `AUDIO_MODEL_PATH: "/models/ggml-base.bin"` matches the sha256-pinned bake path in
  `Dockerfile.audio-worker:47-51,69`; all five values mirror
  `docker-compose.yml`'s audio-worker block exactly (including
  `AUDIO_WORKER_CONCURRENCY: "1"`); `RECONCILER_ACTIVE_STALE_AFTER: "15m"` mirrors
  compose and keeps 742s strictly below the 900s stale cap.
- **Timing invariants hold**: grace 772s ≥ asynq `ShutdownTimeout` 752s
  (`AUDIO_ENGINE_TIMEOUT` 742s + 10s, `cmd/audio-worker/main.go:113`);
  `scaleDownStabilizationSeconds: 900` > 742s; `cooldownPeriod: 180` > the
  audioRetrySchedule max backoff of 30s (`internal/queue/queue.go:303-307`),
  satisfying the WR-06 invariant documented in values.yaml.
- **Template correctness vs siblings**: labels/selector helper usage, tier label,
  commonEnv envFrom wiring, probe shape (startup 24×5s like
  document/chromium), replicas-omission-under-KEDA guard, and the falsy-0
  `hasKey`+`ne nil` guard are all faithful to the sibling patterns. `helm template`
  confirms: keda-on renders no `spec.replicas` and a well-formed ScaledObject with
  `stabilizationWindowSeconds: 900`, `ignoreNullValues: "false"`,
  `fallback.replicas: 1`, `threshold: "1"`; keda-off renders `replicas: 1` and no
  ScaledObject.
- **The sibling's WR-05 jsonpath defect was NOT inherited**: the new script selects
  the trigger pod with `--field-selector=status.phase!=Failed` + `.items[0]`
  (`scripts/keda-audio-loadproof.sh:404-406`), not the empirically-broken
  `?(@.metadata.deletionTimestamp=="")` filter from `keda-load-proof.sh:714`.
- **Phase-29 watcher-kill discipline correctly reproduced**: `set -m` before
  backgrounding so the snapshot loop is its own PGID leader (`:444-447`),
  `kill -- -PGID` + `wait` + belt-and-suspenders `pkill -f` on the exact command
  shape, both at the normal stop (`:465-468`, with `SNAPSHOT_PID` reset preventing a
  double-kill in teardown) and in the EXIT trap (`:149-158`). The `pkill` pattern
  was traced against the actual watcher command line and matches.
- Frozen scripts `keda-load-proof.sh` / `keda-gate.sh` were not modified (per the
  byte-unchanged discipline; their internal defects are out of scope here).

No Critical findings. Two Warnings concern failure-path robustness in the new gate
script — both are deviations from the script's own established `|| true` guard
convention, and both turn a transient infrastructure hiccup into a silent
`set -e` abort with no diagnostic FAIL line.

## Warnings

### WR-01: AUDIO_POD discovery drops the `|| true` guard, so a kubectl failure aborts the gate with no diagnostic

**Status:** fixed
**Resolution:** Added `|| true` to the AUDIO_POD command substitution (same guard shape as the REDIS_POD discovery at line 281), so a transient kubectl failure or empty `.items` list falls through to `assert_nonempty`'s labeled FAIL diagnostic instead of a silent `set -e` abort. Commit `2170595`.
**File:** `scripts/keda-audio-loadproof.sh:404-406`
**Issue:** The trigger-pod discovery runs under `set -euo pipefail` with stderr
suppressed but no failure guard:

```bash
AUDIO_POD=$(kubectl get pod -n "$NAMESPACE" -l "app.kubernetes.io/component=audio-worker" \
	--field-selector=status.phase!=Failed \
	-o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
```

If kubectl fails transiently (API server blip) or the item list is empty
(`.items[0]` on an empty list is a kubectl error, e.g. the sole pod already
terminated between the replica-count check and this call), the command substitution
returns non-zero, `set -e` kills the script at the assignment, and — because stderr
is discarded — the committed transcript shows only teardown's generic
"did not complete" line. The `assert_nonempty` on the next line, which exists
precisely to emit a labeled FAIL diagnostic, is never reached. Every other
`.items[0]` discovery in this script and its siblings carries the guard
(this file's own `REDIS_POD` at line 281, `keda-gate.sh:235`).
**Fix:**
```bash
AUDIO_POD=$(kubectl get pod -n "$NAMESPACE" -l "app.kubernetes.io/component=audio-worker" \
	--field-selector=status.phase!=Failed \
	-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
```

### WR-02: Unguarded curl in the STEP 8 status-poll loop (and postJob) — a port-forward drop kills the gate mid-poll

**Status:** fixed
**Resolution:** Added `|| true` to both curls (STEP 8 status poll at :549 and the postJob `HTTP_STATUS` capture at :331-334), matching the healthz loop's guard convention at :301. A dropped port-forward now yields an empty/failed result handled by the loop's own retry/timeout logic (and postJob's existing `!= "202"` check) instead of aborting the whole gate under `set -e`. Commit `ab2e332`.
**File:** `scripts/keda-audio-loadproof.sh:549` (also `:331`)
**Issue:** The terminal-status poll runs for up to ~1000s (200 × 5s) against the
`kubectl port-forward` at `127.0.0.1:18092`:

```bash
code=$(curl -s -o /tmp/keda-audio-loadproof-job.json -w '%{http_code}' -H "Authorization: ApiKey $CLIENT_KEY" "$API_BASE/v1/jobs/$AUDIO_JOB_ID")
```

`kubectl port-forward` is well known to drop its tunnel on transient connection
errors, and this is the longest-lived network loop in the script. A single curl
failure (exit 7/52/56) under `set -e` aborts the entire gate — teardown then
uninstalls the release even if the trigger job was seconds from `done`, wasting a
full multi-minute run. The script's own healthz loop (`:301`) guards its
identical curl with `|| true` and retries; this loop does not. The `postJob` curl
at `:331` has the same unguarded shape (lower exposure — single call — but the
same defect class: it would die without the function's own labeled FAIL message).
**Fix:**
```bash
code=$(curl -s -o /tmp/keda-audio-loadproof-job.json -w '%{http_code}' -H "Authorization: ApiKey $CLIENT_KEY" "$API_BASE/v1/jobs/$AUDIO_JOB_ID" || true)
```
(and `|| true` on the `HTTP_STATUS=$(curl ...)` in `postJob`; its existing
`!= "202"` check already handles the resulting empty/failed status correctly).

## Info

### IN-01: Snapshot watch attaches after the earliest pod lifecycle transitions; header overclaims "every status update"

**File:** `scripts/keda-audio-loadproof.sh:390-391,426-448`
**Issue:** The watch loop starts only after `waitForReplicasAtLeast` (3s poll
granularity) has already seen the scale-up and the pod has been discovered — by
which point Scheduled/Pulling (and on OrbStack's warm store, likely
Pulled/Created/Started) may have already occurred. The STEP 7 header claims the
loop captures "every status update ... a periodic poll can miss a short-lived
pull/create transition entirely", which the late attach cannot guarantee either.
Evidence integrity is not affected — the authoritative timestamps come from the
`kubectl get events` `.firstTimestamp` extraction (`:475-499`), which the script
asserts on.
**Fix:** Soften the comment to state the watch is supplementary readiness/phase
telemetry and the events extraction is the authoritative timeline, or start the
watch against the label selector before the trigger job is submitted.

### IN-02: Teardown's deployment-drain poll is not failure-guarded under `set -euo pipefail`

**File:** `scripts/keda-audio-loadproof.sh:172`
**Issue:** `remaining=$(kubectl get deployment -n "$NAMESPACE" --no-headers 2>/dev/null | wc -l | tr -d '[:space:]')`
— under `pipefail`, an unreachable API server makes the pipeline (and thus the
assignment) fail, and `set -e` is still active inside the EXIT trap: teardown
aborts before the final PASS/FAIL summary and `exit "$exit_code"`, replacing the
run's real exit code. Only reachable when the cluster itself dies mid-teardown;
the shape is copied verbatim from the frozen sibling (`keda-load-proof.sh:192`),
so this is noted for the new file only, not as a sibling defect.
**Fix:** `remaining=$(kubectl get deployment -n "$NAMESPACE" --no-headers 2>/dev/null | wc -l | tr -d '[:space:]' || true)`

### IN-03: audio-worker Deployment omits the `extraEnv` escape hatch that document-worker documents as the D-08 convention

**File:** `deploy/chart/octoconv/templates/deployment-audio-worker.yaml:51`
**Issue:** `deployment-document-worker.yaml:50-61` supports
`documentWorker.extraEnv` as the sanctioned per-overlay env override mechanism
(Phase 28 D-08). The audio template has no equivalent, so a future overlay setting
`audioWorker.extraEnv` (e.g. to force concurrency for a deterministic test, exactly
the document precedent) would silently no-op. No current consumer sets it —
`values-loadproof.yaml` touches only `documentWorker.extraEnv` and the audio gate
uses `values-local.yaml` alone — so this is a consistency gap, not a behavior bug.
**Fix:** Either add the same `{{- if .Values.audioWorker.extraEnv }}` block, or
leave as-is deliberately and note in the template header that audio has no
extraEnv consumer.

---

_Reviewed: 2026-07-18T21:51:58Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
