# Phase 28: Autoscale Load-Proof - Pattern Map

**Mapped:** 2026-07-17
**Files analyzed:** 9 (3 new scripts, 3 modified chart Deployments, 1 modified ScaledObject, 1 modified values.yaml, 1 new values overlay; evidence/ artifacts are non-code outputs, not classified)
**Analogs found:** 7 / 9 (2 new Python evidence scripts have no in-repo language analog — RESEARCH.md Code Examples are the primary source for those)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|----------------|
| `scripts/keda-load-proof.sh` (new) | test/gate script | event-driven (kubectl poll + HTTP submit + CSV sample loop) | `scripts/keda-gate.sh` | exact — same script family, install/teardown/discovery/job-submit helpers reused verbatim |
| `scripts/fixtures/gen_heavy_docx.py` (new) | utility / fixture generator | file-I/O (Postgres/job-agnostic; pure local file write) | none in-repo (new language); partial-match on calibration-loop shape: `scripts/verapdf-measure.sh` | no direct analog — see RESEARCH.md Code Examples §"Heavy docx generator" |
| `scripts/fixtures/render_evidence.py` (new) | utility / transform (CSV→PNG) | transform / batch | none in-repo | no direct analog — see RESEARCH.md Code Examples §"PNG evidence render" |
| CSV sampler loop (function inside `scripts/keda-load-proof.sh`) | utility / streaming sampler | streaming (periodic poll-and-append) | `scripts/keda-gate.sh` `waitForReplicasAtLeast`/`waitForReplicasAtMost` bounded poll loops | role-match — same bounded `while`-loop-with-`sleep` shape, extended to append instead of compare-and-return |
| `deploy/chart/octoconv/templates/deployment-worker.yaml` (modify) | config / k8s template | CRUD (declarative render) | `deploy/chart/octoconv/templates/scaledobject-image.yaml` (conditional-gate pattern) | role-match — borrowing the `{{- if and .Values.keda.enabled .Values.prometheus.enabled }}` guard shape, applied at field level not whole-resource level |
| `deploy/chart/octoconv/templates/deployment-document-worker.yaml` (modify) | config / k8s template | CRUD | same as above (`scaledobject-document.yaml`) | role-match |
| `deploy/chart/octoconv/templates/deployment-chromium-worker.yaml` (modify) | config / k8s template | CRUD | same as above (`scaledobject-html.yaml`) | role-match |
| `deploy/chart/octoconv/templates/scaledobject-document.yaml` (modify) | config / k8s template | CRUD | itself (baseline) + `deployment-asynqmon.yaml` (`{{- if .Values.X.enabled }}` optional-block pattern) | exact (self) for structure, role-match (asynqmon) for the new optional nested block |
| `deploy/chart/octoconv/values.yaml` (modify) | config | CRUD | itself — existing `keda:` block | exact |
| `deploy/chart/octoconv/values-loadproof.yaml` (new) | config overlay | CRUD | `deploy/chart/octoconv/values-e2e.yaml` | exact — same "layered overlay on top of values-local.yaml" pattern |

## Pattern Assignments

### `scripts/keda-load-proof.sh` (test/gate script, event-driven)

**Analog:** `scripts/keda-gate.sh` (full file, 453 lines — read in full this session)

**Header/intent-comment pattern** (lines 1-29):
```bash
#!/usr/bin/env bash
# keda-gate.sh -- Phase 27 (KEDA autoscaling) live hard gate.
#
# Proves, against a REAL OrbStack Kubernetes cluster, D-12:
#   SC1 (D-12a): ...
# ...
# This script's exit code IS the gate: any failed assertion aborts non-zero
# (set -e) with a loud FAIL message, and every check prints the value it
# observed so the transcript is self-documenting evidence.
set -euo pipefail

cd "$(dirname "$0")/.."
```
Copy this shape exactly for `keda-load-proof.sh`: a scenario-by-scenario doc comment mapping to CONTEXT.md's D-01..D-12 IDs, `set -euo pipefail`, `cd` to repo root.

**Config/constants block** (lines 34-53):
```bash
NAMESPACE="octoconv"
KEDA_NAMESPACE="keda"
KEDA_VERSION="2.20.1"
CHART_DIR="deploy/chart/octoconv"
VALUES_LOCAL="deploy/chart/octoconv/values-local.yaml"

API_LOCAL_PORT="18090"
API_BASE="http://127.0.0.1:${API_LOCAL_PORT}"
DB_LOCAL_PORT="15434"

PASS_COUNT=0
GATE_OK=""
API_PF_PID=""
DB_PF_PID=""
```
For the load-proof gate, reuse identical port numbers/pattern but add a values overlay list: `-f "$VALUES_LOCAL" -f "$VALUES_LOADPROOF"` (see values-loadproof.yaml pattern below), and add an `EVIDENCE_DIR=".planning/phases/28-autoscale-load-proof/evidence"` constant + a run-timestamp variable for filenames (D-03).

**Assertion helpers — copy verbatim** (lines 56-77):
```bash
assert_eq() {
	local expected="$1" actual="$2" label="$3"
	if [ "$expected" != "$actual" ]; then
		echo "FAIL: $label -- expected [$expected], got [$actual]" >&2
		exit 1
	fi
	PASS_COUNT=$((PASS_COUNT + 1))
	echo "PASS: $label == $actual"
}

assert_nonempty() {
	local value="$1" label="$2"
	if [ -z "$value" ]; then
		echo "FAIL: $label -- expected a non-empty value, got empty" >&2
		exit 1
	fi
	PASS_COUNT=$((PASS_COUNT + 1))
	echo "PASS: $label == $value"
}

log() { echo ""; echo "--- $* ---"; }
```

**Teardown-via-EXIT-trap — copy verbatim shape** (lines 82-119):
```bash
teardown() {
	local exit_code=$?
	echo ""
	echo "=== TEARDOWN (D-13: OrbStack must never be left hot) ==="

	if [ -n "$API_PF_PID" ]; then
		kill "$API_PF_PID" >/dev/null 2>&1 || true
	fi
	if [ -n "$DB_PF_PID" ]; then
		kill "$DB_PF_PID" >/dev/null 2>&1 || true
	fi

	helm uninstall octoconv -n "$NAMESPACE" >/dev/null 2>&1 || true
	helm uninstall keda -n "$KEDA_NAMESPACE" >/dev/null 2>&1 || true
	# ... wait-for-gone loop ...

	if [ "$exit_code" -eq 0 ] && [ "$GATE_OK" = "1" ]; then
		echo "✅ PASS -- ..."
	else
		echo "❌ FAIL -- ..." >&2
	fi
	exit "$exit_code"
}
trap teardown EXIT
```
Add cleanup of any pod-deletion-cost annotation state and any background `kubectl get events -w`/sampler-loop PIDs the load-proof script spawns (D-12 discipline: nothing left running after EXIT).

**Job submission helper — copy verbatim, reuse for burst** (lines 306-319):
```bash
postJob() {
	local filename="$1" target="$2" content_type="$3"
	local out_file="/tmp/keda-gate-post-${filename}.json"
	HTTP_STATUS=$(curl -s -o "$out_file" -w '%{http_code}' -X POST "$API_BASE/v1/jobs" \
		-H "Authorization: ApiKey $CLIENT_KEY" \
		-F "target=$target" \
		-F "file=@internal/e2e/testdata/${filename};type=${content_type}")
	if [ "$HTTP_STATUS" != "202" ]; then
		echo "FAIL: POST /v1/jobs for $filename -> $target returned $HTTP_STATUS, body: $(cat "$out_file")" >&2
		exit 1
	fi
	grep -o '"job_id":"[^"]*"' "$out_file" | head -1 | cut -d'"' -f4
}
```
D-04's burst-of-20 needs a parallel variant: fire 20 `postJob` calls into background subshells (`&`) and `wait`, collecting 20 job IDs into an array/file rather than a single variable — note bash's `local` multi-assignment word-expansion pitfall already documented in `27-03-SUMMARY.md` (deviation 2): split `local` declarations across statements when a later variable references an earlier one via `${...}`.

**Bounded-poll helpers — copy verbatim, this IS the CSV sampler's base shape** (lines 325-359):
```bash
waitForReplicasAtLeast() {
	local deployment="$1" floor="$2" timeout_s="$3" observed="0"
	local waited=0
	while [ "$waited" -lt "$timeout_s" ]; do
		observed=$(kubectl get deployment "$deployment" -n "$NAMESPACE" -o jsonpath='{.status.replicas}' 2>/dev/null || echo "0")
		observed="${observed:-0}"
		if [ "$observed" -ge "$floor" ]; then
			echo "$observed"
			return 0
		fi
		sleep 3
		waited=$((waited + 3))
	done
	echo "TIMEOUT(last=$observed)"
	return 1
}
```
The D-01 CSV sampler is this same `while` + `sleep` shape but with no early-return condition — it runs for the full scenario duration, and instead of comparing-and-returning it appends one CSV row per iteration:
```bash
# sampleLoop appends "timestamp,queue_depth,worker_replicas,doc_replicas,html_replicas"
# rows to $CSV_FILE every ~5s until $SAMPLE_UNTIL_EPOCH is reached. Run as a
# background job (&) so the main script can drive the burst/drain scenario
# concurrently; killed explicitly (not just via EXIT trap) once the scenario
# window closes, so the CSV has a clean end marker.
sampleLoop() {
	while [ "$(date +%s)" -lt "$SAMPLE_UNTIL_EPOCH" ]; do
		ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
		qd=$(kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/${NAMESPACE}/${EXTERNAL_METRIC_NAME}?labelSelector=scaledobject.keda.sh%2Fname%3Dworker-image-scaledobject" 2>/dev/null | grep -o '"value":"[0-9]*"' | head -1 | cut -d'"' -f4)
		wr=$(kubectl get deployment worker -n "$NAMESPACE" -o jsonpath='{.status.replicas}' 2>/dev/null || echo 0)
		echo "${ts},${qd:-0},${wr:-0}" >> "$CSV_FILE"
		sleep 5
	done
}
```
This directly reuses the live-metric-discovery pattern from `keda-gate.sh` STEP 6 (lines 242-250, `EXTERNAL_METRIC_NAME` discovered via `status.externalMetricNames[0]`, never hardcoded — Pitfall 5) and the `kubectl get --raw` metric-read pattern (lines 252-265).

**External-metric live discovery — copy verbatim** (lines 242-250):
```bash
EXTERNAL_METRIC_NAME=""
for i in $(seq 1 15); do
	EXTERNAL_METRIC_NAME=$(kubectl get scaledobject worker-image-scaledobject -n "$NAMESPACE" -o jsonpath='{.status.externalMetricNames[0]}' 2>/dev/null || true)
	if [ -n "$EXTERNAL_METRIC_NAME" ]; then
		break
	fi
	sleep 2
done
assert_nonempty "$EXTERNAL_METRIC_NAME" "worker-image-scaledobject discovered external metric name (live, not hardcoded)"
```

**Client-mint + port-forward pattern — copy verbatim** (lines 275-303):
```bash
kubectl port-forward -n "$NAMESPACE" svc/api "${API_LOCAL_PORT}:8090" >/tmp/keda-gate-api-pf.log 2>&1 &
API_PF_PID=$!
kubectl port-forward -n "$NAMESPACE" svc/postgres "${DB_LOCAL_PORT}:5432" >/tmp/keda-gate-db-pf.log 2>&1 &
DB_PF_PID=$!
sleep 3
# ... /healthz poll ...
export DATABASE_URL="postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db"
export API_KEY_SALT="dev-only-change-me-in-real-deploys"
SUFFIX=$(date +%s)
CLIENT_OUT=$(go run ./cmd/manage-clients create "keda-gate-${SUFFIX}")
CLIENT_KEY=$(printf '%s\n' "$CLIENT_OUT" | awk -F': ' '/^api key/{print $2}')
```
D-09's psql triple-check reuses this exact `DB_LOCAL_PORT` port-forward — no new port-forward needed, just `psql "postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db" -tAc "..."` (see RESEARCH.md Code Examples §"D-09 triple-check queries" for the two concrete queries against `job_events`/`jobs.finished_at`).

**pod-deletion-cost annotation (D-08, net-new — no direct in-repo analog, but follows the same `kubectl` imperative-command style already used throughout `keda-gate.sh` for annotate/wait/get):**
```bash
# Set BEFORE the downscale fires (Pitfall: must precede the ReplicaSet
# controller's deletion decision, not run concurrently with or after it).
kubectl annotate pod "$BUSY_POD" -n "$NAMESPACE" \
  controller.kubernetes.io/pod-deletion-cost=-1000 --overwrite
```

**SIGTERM timestamp read (D-09, net-new — `kubectl get events`, not `deletionTimestamp`):**
```bash
kubectl get events -n "$NAMESPACE" \
  --field-selector involvedObject.name="$BUSY_POD",reason=Killing \
  -o jsonpath='{.items[0].firstTimestamp}'
```

---

### `scripts/fixtures/gen_heavy_docx.py` (new — utility/generator, file-I/O)

**No in-repo analog** (first Python file in the repo; CLAUDE.md confirms Go is the only application language and explicitly scopes this as a one-off evidence/fixture script, not app code).

**Source pattern:** RESEARCH.md Code Examples §"Heavy docx generator (uv-ephemeral, python-docx)" — use verbatim as the starting skeleton (imports `docx`, `docx.oxml.ns.qn`, `docx.oxml.OxmlElement`; `PAGE_UNITS` calibration knob; reuses `internal/e2e/testdata/sample.png` for embedded images so no new binary fixture needs to be committed).

**Calibration-loop shape analog:** `scripts/verapdf-measure.sh` (header lines 1-40, read this session) — establishes the project's convention for a "measure against the REAL system, N runs, derive a threshold" gate: `RUNS="${VERAPDF_MEASURE_RUNS:-10}"` env-var-overridable iteration count, `mktemp -d` workdir, `trap ... EXIT` cleanup. Apply the same shape to the docx calibration step: one live trial run against the real in-cluster document-worker (never local — no `soffice` binary on this host, Pitfall 4 in RESEARCH.md), parameterize `PAGE_UNITS` so it can be re-tuned without editing generator internals, and record the observed conversion duration in the gate's evidence log.

**Invocation convention** (RESEARCH.md, verified live this session):
```bash
uv run --with python-docx python3 scripts/fixtures/gen_heavy_docx.py --page-units 300 --out /tmp/heavy.docx
```
Per RESEARCH.md's Security Domain note: generate `heavy.docx` at gate-run time into `/tmp`, never commit the generated multi-hundred-page artifact — only the generator script itself is committed.

---

### `scripts/fixtures/render_evidence.py` (new — utility/transform, CSV→PNG)

**No in-repo analog.**

**Source pattern:** RESEARCH.md Code Examples §"PNG evidence render (uv-ephemeral, matplotlib)" — use verbatim as the starting skeleton: `matplotlib.use("Agg")` (headless-safe), `csv.DictReader` row iteration, `datetime.fromisoformat` timestamp parsing, dual-axis `ax1`/`ax2` (`twinx()`) plot for queue-depth vs. pod-count, `mdates.DateFormatter` for the x-axis, `plt.savefig(..., dpi=120)`.

**Invocation convention:**
```bash
uv run --with matplotlib python3 scripts/fixtures/render_evidence.py \
  --csv .planning/phases/28-autoscale-load-proof/evidence/sc1-sc2-burst-<ts>.csv \
  --png .planning/phases/28-autoscale-load-proof/evidence/sc1-sc2-burst-<ts>.png
```
Called by `keda-load-proof.sh` as the final evidence-generation step, after the CSV sampler loop is killed and the scenario has completed (matches D-02: "PNG output is mandatory, tool choice is planner's discretion").

---

### `deploy/chart/octoconv/templates/deployment-{worker,document-worker,chromium-worker}.yaml` (modify, config/k8s template, WR-02/D-10)

**Analog:** `deploy/chart/octoconv/templates/scaledobject-image.yaml` (full file, 40 lines) and `scaledobject-document.yaml` (full file, 41 lines) — both read in full this session.

**Co-dependency guard pattern to reuse** (from `scaledobject-image.yaml` lines 1-14):
```yaml
{{/*
CO-DEPENDENCY GUARD: gated on BOTH keda.enabled AND prometheus.enabled
together, not keda.enabled alone. The trigger's serverAddress points at the
in-chart prometheus Service (templates/prometheus.yaml), which only renders
when prometheus.enabled=true — the `and` guard prevents a ScaledObject from
ever dangling against a Service that was never created.
*/}}
{{- if and .Values.keda.enabled .Values.prometheus.enabled }}
```

**Current unconditional render to fix** (`deployment-worker.yaml` lines 22-23, identical shape in `deployment-document-worker.yaml`/`deployment-chromium-worker.yaml`):
```yaml
spec:
  replicas: {{ .Values.worker.replicas }}
```

**Target pattern (WR-02 fix) — field-level conditional, not whole-resource gating** (the Deployment itself must always render; only the `replicas:` field is conditional, since KEDA's HPA needs the Deployment object to exist before it can take ownership — matches `27-03-SUMMARY.md`'s documented "KEDA takes ownership only after cooldown" behavior):
```yaml
spec:
  {{- if and .Values.keda.enabled .Values.prometheus.enabled }}
  # spec.replicas intentionally omitted when KEDA/HPA owns this Deployment
  # (WR-02, Phase 28 D-10): rendering a fixed value here would make every
  # `helm upgrade` reset a scaled-to-zero class back to this default,
  # fighting the HPA. Once KEDA/HPA has taken ownership it manages
  # spec.replicas directly via the scale subresource.
  {{- else }}
  replicas: {{ .Values.worker.replicas }}
  {{- end }}
```
`deployment-webhook-worker.yaml` (lines 1-25, read this session) is the deliberate NON-scaled contrast: it always renders `replicas: {{ .Values.webhookWorker.replicas }}` unconditionally and its header comment explicitly says "do NOT ever attach a KEDA ScaledObject to it" — do not touch this file for WR-02; it validates that the fix must be scoped only to the three KEDA-scaled classes.

**Idempotency check to run after the fix** (per D-10): `helm template ...` offline render with `keda.enabled=false` must still show `replicas: N`; with `keda.enabled=true && prometheus.enabled=true` must omit the field entirely; then a live `helm upgrade` against an already-scaled-to-0 class must NOT reset it to 1 (this replaces the `keda-gate.sh` STEP 6 poll-for-settling workaround, lines 220-240, which existed specifically because of this bug — after the fix that poll can be simplified since a fresh install/upgrade will never re-render `replicas: 1` over an existing 0).

---

### `deploy/chart/octoconv/templates/scaledobject-document.yaml` (modify, config/k8s template, Pattern 1 from RESEARCH.md)

**Analog:** itself (baseline structure, full file read this session) + `deployment-asynqmon.yaml` for the optional-nested-block-gated-on-a-single-values-key idiom.

**Baseline to extend** (full current file, lines 20-30):
```yaml
spec:
  scaleTargetRef:
    name: document-worker
  minReplicaCount: 0
  maxReplicaCount: {{ .Values.keda.document.maxReplicaCount }}
  pollingInterval: {{ .Values.keda.document.pollingInterval }}
  cooldownPeriod: {{ .Values.keda.document.cooldownPeriod }}
  fallback:
    failureThreshold: 3
    replicas: 1
  triggers:
    - type: prometheus
```

**Values-gated optional block to add** (RESEARCH.md Pattern 1, `{{- if .Values.X }}` idiom borrowed from `deployment-asynqmon.yaml`'s `{{- if .Values.asynqmon.enabled }}` whole-block gate, applied here to a nested `advanced.horizontalPodAutoscalerConfig.behavior.scaleDown` block):
```yaml
  {{- if .Values.keda.document.scaleDownStabilizationSeconds }}
  advanced:
    horizontalPodAutoscalerConfig:
      behavior:
        scaleDown:
          stabilizationWindowSeconds: {{ .Values.keda.document.scaleDownStabilizationSeconds }}
          policies:
            - type: Pods
              value: 1
              periodSeconds: 15
  {{- end }}
```
Critical framing from RESEARCH.md Pitfall 1: this key defaults to `null`/omitted in `values.yaml` (production behavior — the real Kubernetes 300s default `scaleDown.stabilizationWindowSeconds` — stays untouched), and is set ONLY via the load-proof gate's values overlay (see `values-loadproof.yaml` below). `cooldownPeriod` governs the 1→0 transition only; this new key governs the N→N-1 (N>1) transition that KEDA's `cooldownPeriod` does NOT cover — do not conflate the two when writing the doc-comment header for this template.

---

### `deploy/chart/octoconv/values.yaml` (modify, config)

**Analog:** itself — existing `keda:` block (lines 145-161, full block read this session):
```yaml
keda:
  enabled: false
  image:
    threshold: "5"
    maxReplicaCount: 4
    pollingInterval: 5
    cooldownPeriod: 60
  document:
    threshold: "1"
    maxReplicaCount: 2
    pollingInterval: 15
    cooldownPeriod: 120
  html:
    threshold: "2"
    maxReplicaCount: 2
    pollingInterval: 10
    cooldownPeriod: 90
```
Add one new key under `document:` only (image/html are untouched — RESEARCH.md's Pitfall 1 is specifically a `document`-class 2→1 timing problem; image/html never exercise an N>1 downscale in this phase's scenarios per D-05):
```yaml
  document:
    threshold: "1"
    maxReplicaCount: 2
    pollingInterval: 15
    cooldownPeriod: 120
    scaleDownStabilizationSeconds: null   # NEW (D-10/Pattern 1) — production default OFF; load-proof gate overrides via values-loadproof.yaml
```
Follow the file's existing top-of-file convention (lines 1-6) of documenting WHY a value exists inline as a comment, and the `keda:` block's own header comment style (lines 138-144) that already explains the co-dependency/gating rationale for sibling keys.

---

### `deploy/chart/octoconv/values-loadproof.yaml` (new, config overlay)

**Analog:** `deploy/chart/octoconv/values-e2e.yaml` (full file, 25 lines, read this session) — the established "overlay layered on top of values-local.yaml, never standalone" pattern:
```yaml
# E2E overlay — layered ON TOP of values-local.yaml, NEVER standalone:
#
#   helm upgrade octoconv deploy/chart/octoconv \
#     -f deploy/chart/octoconv/values-local.yaml \
#     -f deploy/chart/octoconv/values-e2e.yaml --wait --timeout 10m
#
# Enables the in-cluster E2E Job (templates/job-e2e.yaml, gated on
# e2e.enabled) and relaxes ONLY the api's SSRF/rate-limit guards ...

e2e:
  enabled: true

api:
  extraEnv:
    WEBHOOK_ALLOW_PRIVATE_IPS: "true"
    ...
```
**Target file** — same header convention, same nested-key-override shape, scoped to exactly the one new knob from RESEARCH.md Pattern 1:
```yaml
# Load-proof overlay — layered ON TOP of values-local.yaml, NEVER standalone
# (same convention as values-e2e.yaml):
#
#   helm install octoconv deploy/chart/octoconv \
#     -f deploy/chart/octoconv/values-local.yaml \
#     -f deploy/chart/octoconv/values-loadproof.yaml -n octoconv
#
# Overrides ONLY the document class's HPA scaleDown stabilization window
# (Pattern 1, Phase 28 RESEARCH.md Pitfall 1) so the SC3 2->1 downscale
# transition is deterministic and fast for the test, instead of depending
# on the Kubernetes 300s default. Never applied to a production values file.

keda:
  document:
    scaleDownStabilizationSeconds: 15
```
`keda-load-proof.sh`'s `helm install`/`helm upgrade` invocation (analog: `keda-gate.sh` line 193, `helm install octoconv "$CHART_DIR" -f "$VALUES_LOCAL" -n "$NAMESPACE" --create-namespace`) extends to `-f "$VALUES_LOCAL" -f "$VALUES_LOADPROOF"`, matching the exact multi-`-f` chaining shown in `values-e2e.yaml`'s own header comment.

---

## Shared Patterns

### Live-gate script shape (bash, exit-code-is-the-gate)
**Source:** `scripts/keda-gate.sh` (whole file); secondary precedent `scripts/presets-acceptance.sh` (lines 1-30), `scripts/verapdf-measure.sh` (lines 1-40)
**Apply to:** `scripts/keda-load-proof.sh`
```bash
set -euo pipefail
cd "$(dirname "$0")/.."
# assert_eq / assert_nonempty helpers, PASS_COUNT tally, log() section headers
# trap teardown EXIT  -- runs on success AND failure, never leaves OrbStack hot
```

### Never hardcode the external metric name — always live-discover
**Source:** `scripts/keda-gate.sh` lines 242-250 (Pitfall 5, Phase 27)
**Apply to:** `scripts/keda-load-proof.sh`'s CSV sampler and burst-submission steps — read `status.externalMetricNames[0]` from the relevant ScaledObject once, cache it, reuse for every sample-loop iteration and for `kubectl get --raw` calls.

### Bounded poll, never a fixed sleep, for any "wait for k8s state X" check
**Source:** `scripts/keda-gate.sh` `waitForReplicasAtLeast`/`waitForReplicasAtMost` (lines 325-359), and the documented Rule-1 fix in `27-03-SUMMARY.md` (deviation 1: an instant assertion failed because KEDA hadn't taken ownership yet — replaced with a bounded poll)
**Apply to:** every new gate assertion — the SC1 zero-replica precondition, the burst-triggered scale-up, the drain-to-zero leg, the SC3 pod-identification step, and the pod-termination timestamp reads (RESEARCH.md Pitfall 3: poll continuously through the window, don't read once at the end — terminated pods can be GC'd).

### Values-gated, production-safe optional chart knobs
**Source:** `deploy/chart/octoconv/templates/deployment-asynqmon.yaml` (`{{- if .Values.asynqmon.enabled }}`), `scaledobject-image.yaml`/`scaledobject-document.yaml` (`{{- if and .Values.keda.enabled .Values.prometheus.enabled }}`), `values.yaml` header comment (lines 1-6: "Real dev-credential values live in values-local.yaml, never here")
**Apply to:** the new `scaleDownStabilizationSeconds` key (default `null`/omitted, production-inert) and the WR-02 conditional `replicas:` field — every new knob this phase introduces follows the same "off by default in `values.yaml`, turned on only by a dev/test overlay" discipline already established for `keda.enabled`/`prometheus.enabled`/`e2e.enabled`/`asynqmon.enabled`.

### Overlay chaining (`-f base.yaml -f overlay.yaml`)
**Source:** `deploy/chart/octoconv/values-e2e.yaml` header comment (lines 1-9)
**Apply to:** `keda-load-proof.sh`'s helm invocation — `values-local.yaml` (dev credentials + keda/prometheus enabled) stays the base; `values-loadproof.yaml` layers the one new SC3-timing override on top, matching the exact precedent instead of inventing a new overlay convention.

### Port-forward + minted client key for job submission from the host
**Source:** `scripts/keda-gate.sh` lines 275-303 (`kubectl port-forward svc/api`, `kubectl port-forward svc/postgres`, `go run ./cmd/manage-clients create ...`)
**Apply to:** all job submission in `keda-load-proof.sh` (burst-of-20, the two document jobs for SC3) and the D-09 psql triple-check (reuses the same `svc/postgres` port-forward, no new one needed).

### `ShutdownTimeout` / graceful-shutdown invariant being validated (not modified) by SC3
**Source:** `cmd/worker/main.go:88`, `cmd/document-worker/main.go:94`
```go
ShutdownTimeout: envDuration("DOCUMENT_ENGINE_TIMEOUT", 300*time.Second) + 10*time.Second,
```
**Apply to:** D-09's triple-check calibration — the long-job budget must stay comfortably under this 310s ceiling (already the case per D-07's own framing); this code is read-only context for the gate, not touched by this phase.

## No Analog Found

| File | Role | Data Flow | Reason |
|------|------|-----------|--------|
| `scripts/fixtures/gen_heavy_docx.py` | utility/generator | file-I/O | First Python file in the repo — no in-language analog; use RESEARCH.md's verified Code Example as the base, borrow calibration-loop discipline from `scripts/verapdf-measure.sh` (bash, cross-language role-match only) |
| `scripts/fixtures/render_evidence.py` | utility/transform | transform (CSV→PNG) | Same as above — no in-repo CSV/charting precedent at all; RESEARCH.md Code Example is the sole source |

## Metadata

**Analog search scope:** `scripts/`, `deploy/chart/octoconv/templates/`, `deploy/chart/octoconv/values*.yaml`, `cmd/worker/main.go`, `cmd/document-worker/main.go`, `internal/db/migrations/0001_init.sql`, `.planning/phases/27-keda-autoscaling/27-03-SUMMARY.md`
**Files scanned:** `scripts/keda-gate.sh` (full), `scripts/presets-acceptance.sh` (header), `scripts/verapdf-measure.sh` (header), `deploy/chart/octoconv/templates/deployment-{worker,document-worker,chromium-worker,webhook-worker,asynqmon}.yaml` (full), `deploy/chart/octoconv/templates/scaledobject-{image,document}.yaml` (full), `deploy/chart/octoconv/values.yaml` / `values-local.yaml` / `values-e2e.yaml` (full), `internal/db/migrations/0001_init.sql` (jobs/job_events schema section)
**Pattern extraction date:** 2026-07-17
