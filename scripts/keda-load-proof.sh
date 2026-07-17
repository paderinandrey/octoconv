#!/usr/bin/env bash
# keda-load-proof.sh -- Phase 28 (autoscale-load-proof) live flagship gate
# (KEDA-03).
#
# Proves, against a REAL OrbStack Kubernetes cluster, timestamped 0->N->0
# evidence under burst load AND a graceful downscale of an in-flight long
# document conversion:
#
#   SC1 (D-04/D-05):  burst of 20 parallel image (png->jpg) jobs submitted
#     while the image `worker` Deployment is at TRUE zero -- the gate
#     literally asserts >=2 replicas within 60s; reaching maxReplicaCount=4
#     is RECORDED as an observed fact, not asserted (threshold=5/max=4).
#   SC2 (D-06):        after the burst queue drains, the image worker
#     returns to 0 replicas within a bounded window -- one full 0->N->0
#     cycle, sampled the whole way through.
#   SC3 (D-07/D-08/D-09): a long (heavy, calibrated ~200s) document job is
#     submitted FIRST, a short document job SECOND (DOCUMENT_WORKER_
#     CONCURRENCY=1 makes pod1=long/pod2=short deterministic); the gate
#     annotates the busy pod (pod1) with controller.kubernetes.io/
#     pod-deletion-cost=-1000 BEFORE the 2->1 downscale fires (never
#     `kubectl delete pod` -- SC3 requires a genuine KEDA/HPA downscale
#     event); the D-09 triple-check then proves the long job survived the
#     downscale gracefully (SIGTERM before completion, no false retry,
#     graceful exit before terminationGracePeriodSeconds).
#   SC4 (D-01/D-02/D-03): CSV sampler + rendered PNG + gate transcript are
#     the timestamped evidence artifacts, committed under
#     .planning/phases/28-autoscale-load-proof/evidence/.
#
# CALIBRATION MODE (D-07): run with CALIBRATE=1 (env) or --calibrate to
# generate ONE heavy docx (--page-units, default 300) and submit ONLY that
# single job, printing its observed in-cluster LibreOffice conversion
# duration, then exit -- this is the live trial run D-07 requires (there is
# no local soffice/libreoffice binary on this host -- 28-RESEARCH.md
# Pitfall 4 -- calibration MUST run against the real document-worker).
#
# This gate is SELF-CONTAINED (D-12): it installs KEDA itself, layers
# `-f values-local.yaml -f values-loadproof.yaml` on top of the chart, and
# tears everything down via an EXIT trap -- success or failure, OrbStack is
# never left hot. It refuses to run if the docker-compose stack is up.
# `scripts/keda-gate.sh` (Phase 27) is left byte-unchanged (D-12) -- this is
# a SEPARATE script that reuses its helper shapes, not a modification of it.
#
# This script's exit code IS the gate: any failed assertion aborts non-zero
# (set -e) with a loud FAIL message, and every check prints the value it
# observed so the transcript is self-documenting evidence.
set -euo pipefail

cd "$(dirname "$0")/.."

# ---------------------------------------------------------------------------
# Config / constants
# ---------------------------------------------------------------------------
NAMESPACE="octoconv"
KEDA_NAMESPACE="keda"
KEDA_VERSION="2.20.1"
CHART_DIR="deploy/chart/octoconv"
VALUES_LOCAL="deploy/chart/octoconv/values-local.yaml"
VALUES_LOADPROOF="deploy/chart/octoconv/values-loadproof.yaml"

# api/db reachability for job submission and D-09 psql queries --
# port-forwarded locally by this script (same sanctioned Phase 24/25/27
# mechanism as scripts/keda-gate.sh).
API_LOCAL_PORT="18090"
API_BASE="http://127.0.0.1:${API_LOCAL_PORT}"
DB_LOCAL_PORT="15434"

PASS_COUNT=0
GATE_OK=""
API_PF_PID=""
DB_PF_PID=""
SAMPLER_PID=""
# BUSY_POD is set by the SC3 scenario (Task 3 insertion point below); kept
# declared here (empty) so teardown() can unconditionally reference it and
# clear its pod-deletion-cost annotation regardless of which scenario ran.
BUSY_POD=""

# D-03: evidence artifacts (CSV, PNG, gate transcript, SC3 timestamps) are
# committed under this directory alongside the SUMMARY.
EVIDENCE_DIR=".planning/phases/28-autoscale-load-proof/evidence"
RUN_TS=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$EVIDENCE_DIR"
LOG_FILE="$EVIDENCE_DIR/gate-transcript-${RUN_TS}.log"

# Tee the whole run to the timestamped transcript (D-01/D-03) -- every PASS/
# FAIL line, every observed value, becomes part of the committed evidence.
exec > >(tee "$LOG_FILE") 2>&1

# ---------------------------------------------------------------------------
# CALIBRATE mode flag (D-07): CALIBRATE=1 env var OR --calibrate arg.
# PAGE_UNITS (env, default 300) controls the heavy-docx generator's
# calibration knob.
# ---------------------------------------------------------------------------
CALIBRATE="${CALIBRATE:-0}"
for arg in "$@"; do
	case "$arg" in
	--calibrate) CALIBRATE=1 ;;
	esac
done
PAGE_UNITS="${PAGE_UNITS:-300}"

# ---------------------------------------------------------------------------
# Assertion helpers -- copied verbatim from scripts/keda-gate.sh.
# ---------------------------------------------------------------------------
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

# ---------------------------------------------------------------------------
# Teardown -- ALWAYS runs (trap on EXIT), success or failure (D-12: never
# leave the k8s stack hot). Kills the CSV sampler and any port-forwards
# first, clears a BUSY_POD's pod-deletion-cost annotation if one was set
# (SC3 -- no-op in CALIBRATE/SC1-SC2-only runs), then uninstalls octoconv
# and keda.
# ---------------------------------------------------------------------------
teardown() {
	local exit_code=$?
	echo ""
	echo "=== TEARDOWN (D-12: OrbStack must never be left hot) ==="

	if [ -n "$SAMPLER_PID" ]; then
		kill "$SAMPLER_PID" >/dev/null 2>&1 || true
		wait "$SAMPLER_PID" 2>/dev/null || true
	fi
	if [ -n "$BUSY_POD" ]; then
		kubectl annotate pod "$BUSY_POD" -n "$NAMESPACE" \
			controller.kubernetes.io/pod-deletion-cost- >/dev/null 2>&1 || true
	fi
	if [ -n "$API_PF_PID" ]; then
		kill "$API_PF_PID" >/dev/null 2>&1 || true
	fi
	if [ -n "$DB_PF_PID" ]; then
		kill "$DB_PF_PID" >/dev/null 2>&1 || true
	fi

	helm uninstall octoconv -n "$NAMESPACE" >/dev/null 2>&1 || true
	helm uninstall keda -n "$KEDA_NAMESPACE" >/dev/null 2>&1 || true

	echo "waiting for octoconv workloads to be gone..."
	remaining="unknown"
	for i in $(seq 1 30); do
		remaining=$(kubectl get deployment -n "$NAMESPACE" --no-headers 2>/dev/null | wc -l | tr -d '[:space:]')
		if [ "${remaining:-0}" = "0" ]; then
			break
		fi
		sleep 2
	done
	echo "octoconv namespace deployments remaining: ${remaining:-unknown}"

	echo ""
	if [ "$exit_code" -eq 0 ] && { [ "$GATE_OK" = "1" ] || [ "$CALIBRATE" = "1" ]; }; then
		echo "✅ PASS -- Phase 28 load-proof gate run complete ($PASS_COUNT checks). Transcript: $LOG_FILE"
	else
		echo "❌ FAIL -- Phase 28 load-proof gate did not complete (exit=$exit_code, checks passed=$PASS_COUNT)." >&2
	fi
	exit "$exit_code"
}
trap teardown EXIT

echo "=== Phase 28 autoscale load-proof: live flagship gate (KEDA-03) ==="
echo "run timestamp: $RUN_TS"
echo "evidence dir: $EVIDENCE_DIR"
if [ "$CALIBRATE" = "1" ]; then
	echo "mode: CALIBRATE (page-units=$PAGE_UNITS)"
else
	echo "mode: FULL GATE (SC1/SC2 burst + SC3 downscale soak)"
fi

# ---------------------------------------------------------------------------
# STEP 1: Preflight (D-12: OrbStack discipline -- compose and k8s stacks
# must never be hot simultaneously).
# ---------------------------------------------------------------------------
log "STEP 1: preflight"

kubectl get nodes >/dev/null
echo "PASS: kubectl reaches the OrbStack cluster (context: $(kubectl config current-context))"

COMPOSE_UP=$(docker compose ps --format '{{.Names}}' 2>/dev/null | grep -c '^octoconv-' || true)
if [ "${COMPOSE_UP:-0}" -gt 0 ]; then
	echo "FAIL: compose stack appears to be UP ($COMPOSE_UP octoconv-* containers running) -- compose and k8s stacks must NEVER be hot simultaneously (D-12). Run 'docker compose ... down -v' first." >&2
	exit 1
fi
echo "PASS: compose stack is down (0 octoconv-* containers running)"

helm repo add kedacore https://kedacore.github.io/charts >/dev/null 2>&1 || true
helm repo update >/dev/null
if ! helm search repo kedacore/keda --versions | awk '{print $2}' | grep -qx "$KEDA_VERSION"; then
	LATEST_KEDA=$(helm search repo kedacore/keda --versions | awk 'NR==2{print $2}')
	echo "FAIL: KEDA v$KEDA_VERSION is no longer resolvable in kedacore/keda -- current latest is $LATEST_KEDA. Repin KEDA_VERSION in this script and re-run." >&2
	exit 1
fi
echo "PASS: KEDA v$KEDA_VERSION re-verified resolvable in kedacore/keda (live)"

# ---------------------------------------------------------------------------
# STEP 2: Install KEDA (idempotent) -- gate is self-contained (D-12).
# ---------------------------------------------------------------------------
log "STEP 2: helm install KEDA v$KEDA_VERSION into namespace $KEDA_NAMESPACE"

if helm status keda -n "$KEDA_NAMESPACE" >/dev/null 2>&1; then
	echo "keda release already present -- upgrading in place (idempotent)"
	helm upgrade keda kedacore/keda --namespace "$KEDA_NAMESPACE" --version "$KEDA_VERSION" --wait --timeout 5m
else
	helm install keda kedacore/keda --namespace "$KEDA_NAMESPACE" --create-namespace --version "$KEDA_VERSION" --wait --timeout 5m
fi
echo "PASS: KEDA v$KEDA_VERSION installed/upgraded, operator Deployments Available"

log "STEP 3: waiting for v1beta1.external.metrics.k8s.io to become Available"
APISERVICE_READY=""
COND=""
for i in $(seq 1 30); do
	COND=$(kubectl get apiservice v1beta1.external.metrics.k8s.io -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || true)
	if [ "$COND" = "True" ]; then
		APISERVICE_READY=1
		break
	fi
	sleep 2
done
if [ -z "$APISERVICE_READY" ]; then
	echo "FAIL: v1beta1.external.metrics.k8s.io never reported Available:True after 60s (last observed condition: [$COND])" >&2
	exit 1
fi
echo "PASS: v1beta1.external.metrics.k8s.io Available:True"

# ---------------------------------------------------------------------------
# STEP 4: Install octoconv layered on the load-proof overlay (D-07/D-08
# timing knobs: document-class scaleDownStabilizationSeconds=15,
# DOCUMENT_WORKER_CONCURRENCY=1). WITHOUT --wait (Phase 24 decision:
# createbucket post-install hook <-> app-readiness chicken-egg), then
# kubectl wait per always-on Deployment only.
# ---------------------------------------------------------------------------
log "STEP 4: helm install octoconv -f values-local.yaml -f values-loadproof.yaml"

helm install octoconv "$CHART_DIR" -f "$VALUES_LOCAL" -f "$VALUES_LOADPROOF" -n "$NAMESPACE" --create-namespace
echo "PASS: helm install octoconv complete (async install; readiness gated below)"

log "waiting for always-on / min-1 Deployments to become Available"
for d in postgres redis minio api prometheus webhook-worker; do
	kubectl wait --for=condition=Available "deployment/$d" -n "$NAMESPACE" --timeout=240s
	echo "PASS: deployment/$d Available"
done

# ---------------------------------------------------------------------------
# STEP 5: port-forward api+postgres, mint a client key. Reused for job
# submission (SC1/SC2/SC3/calibration) and the D-09 psql triple-check.
# ---------------------------------------------------------------------------
log "STEP 5: port-forward api+postgres, mint client key"

kubectl port-forward -n "$NAMESPACE" svc/api "${API_LOCAL_PORT}:8090" >/tmp/keda-loadproof-api-pf.log 2>&1 &
API_PF_PID=$!
kubectl port-forward -n "$NAMESPACE" svc/postgres "${DB_LOCAL_PORT}:5432" >/tmp/keda-loadproof-db-pf.log 2>&1 &
DB_PF_PID=$!
sleep 3

echo "waiting for port-forwarded /healthz..."
healthy=""
for i in $(seq 1 30); do
	code=$(curl -s -o /tmp/keda-loadproof-healthz.json -w '%{http_code}' "$API_BASE/healthz" || true)
	if [ "$code" = "200" ]; then
		healthy=1
		break
	fi
	sleep 2
done
if [ -z "$healthy" ]; then
	echo "FAIL: /healthz never returned 200 through the port-forward after 60s" >&2
	exit 1
fi
echo "PASS: api reachable via port-forward, /healthz 200 ($(cat /tmp/keda-loadproof-healthz.json))"

# T-28-04: dev-only, throwaway credential pattern (same as keda-gate.sh) --
# never a production secret, never echoed into $EVIDENCE_DIR files.
export DATABASE_URL="postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db"
export API_KEY_SALT="dev-only-change-me-in-real-deploys"

SUFFIX=$(date +%s)
CLIENT_OUT=$(go run ./cmd/manage-clients create "keda-loadproof-${SUFFIX}")
CLIENT_KEY=$(printf '%s\n' "$CLIENT_OUT" | awk -F': ' '/^api key/{print $2}')
assert_nonempty "$CLIENT_KEY" "minted gate client + API key"

# postJob submits a testdata fixture (relative filename under
# internal/e2e/testdata/) -- reused verbatim shape from keda-gate.sh.
postJob() {
	local filename="$1" target="$2" content_type="$3"
	local out_file="/tmp/keda-loadproof-post-${filename//\//_}.json"
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

# postJobPath submits a fixture from an ARBITRARY path (calibration's
# generated heavy docx, the burst's synthesized/temp image) -- same
# multipart shape as postJob, just no internal/e2e/testdata/ prefix.
postJobPath() {
	local path="$1" target="$2" content_type="$3"
	local tag
	tag=$(basename "$path")
	local out_file="/tmp/keda-loadproof-post-${tag//\//_}-$$-${RANDOM}.json"
	HTTP_STATUS=$(curl -s -o "$out_file" -w '%{http_code}' -X POST "$API_BASE/v1/jobs" \
		-H "Authorization: ApiKey $CLIENT_KEY" \
		-F "target=$target" \
		-F "file=@${path};type=${content_type}")
	if [ "$HTTP_STATUS" != "202" ]; then
		echo "FAIL: POST /v1/jobs for $path -> $target returned $HTTP_STATUS, body: $(cat "$out_file")" >&2
		exit 1
	fi
	grep -o '"job_id":"[^"]*"' "$out_file" | head -1 | cut -d'"' -f4
}

# waitForReplicasAtLeast / waitForReplicasAtMost -- bounded polls, copied
# verbatim from keda-gate.sh (Rule-1 fix: KEDA settles after cooldown, poll
# don't assert instantly).
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

waitForReplicasAtMost() {
	local deployment="$1" ceiling="$2" timeout_s="$3" observed="0"
	local waited=0
	while [ "$waited" -lt "$timeout_s" ]; do
		observed=$(kubectl get deployment "$deployment" -n "$NAMESPACE" -o jsonpath='{.status.replicas}' 2>/dev/null || echo "0")
		observed="${observed:-0}"
		if [ "$observed" -le "$ceiling" ]; then
			echo "$observed"
			return 0
		fi
		sleep 3
		waited=$((waited + 3))
	done
	echo "TIMEOUT(last=$observed)"
	return 1
}

# ---------------------------------------------------------------------------
# CALIBRATE MODE (D-07): live in-cluster trial run -- generate ONE heavy
# docx, submit it alone, observe its real conversion duration via psql, then
# exit (teardown runs via the EXIT trap). There is no local soffice binary
# on this host (28-RESEARCH.md Pitfall 4) -- this IS the calibration step,
# it must run against the real document-worker, never a local dry run.
# ---------------------------------------------------------------------------
if [ "$CALIBRATE" = "1" ]; then
	log "CALIBRATION MODE (D-07): single heavy-docx trial run, page-units=$PAGE_UNITS"

	HEAVY_DOCX="/tmp/heavy-${RUN_TS}.docx"
	uv run --with python-docx python3 scripts/fixtures/gen_heavy_docx.py \
		--page-units "$PAGE_UNITS" --out "$HEAVY_DOCX"

	CAL_JOB_ID=$(postJobPath "$HEAVY_DOCX" "pdf" "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	assert_nonempty "$CAL_JOB_ID" "calibration heavy-docx job submitted (page-units=$PAGE_UNITS)"

	echo "waiting for calibration job $CAL_JOB_ID to reach a terminal status (bounded 600s -- must stay < DOCUMENT_ENGINE_TIMEOUT=300s to be useful, but we poll generously in case of cold pod start)..."
	cal_status=""
	for i in $(seq 1 200); do
		code=$(curl -s -o /tmp/keda-loadproof-cal-job.json -w '%{http_code}' -H "Authorization: ApiKey $CLIENT_KEY" "$API_BASE/v1/jobs/$CAL_JOB_ID")
		cal_status=$(grep -o '"status":"[^"]*"' /tmp/keda-loadproof-cal-job.json | head -1 | cut -d'"' -f4)
		if [ "$cal_status" = "done" ] || [ "$cal_status" = "failed" ]; then
			break
		fi
		sleep 3
	done
	assert_eq "done" "$cal_status" "calibration job $CAL_JOB_ID reaches done"

	CAL_DURATION=$(psql "postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db" -tAc \
		"SELECT EXTRACT(EPOCH FROM (finished_at - started_at)) FROM jobs WHERE id='${CAL_JOB_ID}';" | tr -d '[:space:]')

	log "CALIBRATION RESULT"
	echo "page-units=$PAGE_UNITS observed conversion duration=${CAL_DURATION}s"
	echo "Target margin: > scaleDownStabilizationSeconds=15s (values-loadproof.yaml, comfortably) and < DOCUMENT_ENGINE_TIMEOUT=300s."
	echo "Re-run with PAGE_UNITS=<adjusted> CALIBRATE=1 if the observed duration is not comfortably within [30s, 250s]."

	exit 0
fi

# ---------------------------------------------------------------------------
# STEP 6 (SC1/D-04/D-05, SC2/D-06, D-01 sampler): image-class burst
# 0->N->0, sampled the whole way through.
# ---------------------------------------------------------------------------
log "STEP 6: SC1/SC2 -- image-class burst-of-20, 0->N->0 with CSV+PNG evidence"

echo "waiting for worker (image) to settle at 0 replicas (KEDA cooldownPeriod=60s + margin, WR-02 fresh-install settling)..."
IMAGE_REPLICAS_BEFORE="1"
waited=0
while [ "$waited" -lt 150 ]; do
	IMAGE_REPLICAS_BEFORE=$(kubectl get deployment worker -n "$NAMESPACE" -o jsonpath='{.status.replicas}' 2>/dev/null || echo "0")
	IMAGE_REPLICAS_BEFORE="${IMAGE_REPLICAS_BEFORE:-0}"
	if [ "$IMAGE_REPLICAS_BEFORE" = "0" ]; then
		break
	fi
	sleep 5
	waited=$((waited + 5))
done
assert_eq "0" "$IMAGE_REPLICAS_BEFORE" "worker (image) Deployment status.replicas before burst (TRUE zero precondition, D-04)"

# Live-discover the external metric name -- NEVER hardcode (Pitfall 5,
# Phase 27).
EXTERNAL_METRIC_NAME=""
for i in $(seq 1 15); do
	EXTERNAL_METRIC_NAME=$(kubectl get scaledobject worker-image-scaledobject -n "$NAMESPACE" -o jsonpath='{.status.externalMetricNames[0]}' 2>/dev/null || true)
	if [ -n "$EXTERNAL_METRIC_NAME" ]; then
		break
	fi
	sleep 2
done
assert_nonempty "$EXTERNAL_METRIC_NAME" "worker-image-scaledobject discovered external metric name (live, not hardcoded)"

RAW_METRIC=""
for i in $(seq 1 15); do
	RAW_METRIC=$(kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/${NAMESPACE}/${EXTERNAL_METRIC_NAME}?labelSelector=scaledobject.keda.sh%2Fname%3Dworker-image-scaledobject" 2>/dev/null || true)
	if printf '%s' "$RAW_METRIC" | grep -q '"value"'; then
		break
	fi
	sleep 2
done
if ! printf '%s' "$RAW_METRIC" | grep -q '"value"'; then
	echo "FAIL: external metric '$EXTERNAL_METRIC_NAME' never returned a value after 30s (precondition for burst). Last response: $RAW_METRIC" >&2
	exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: octoconv_queue_depth (image) resolved via kubectl get --raw at 0 replicas: $RAW_METRIC"

# D-01: CSV sampler -- appends "timestamp,queue_depth,worker_replicas" every
# ~5s. Runs as a background job so the burst/drain scenario can proceed
# concurrently; killed explicitly (teardown + explicit kill below) once the
# scenario window closes, giving the CSV a clean end marker.
CSV_FILE="$EVIDENCE_DIR/sc1-sc2-burst-${RUN_TS}.csv"
echo "timestamp,queue_depth,worker_replicas" >"$CSV_FILE"
SAMPLE_UNTIL_EPOCH=$(($(date +%s) + 600))

sampleLoop() {
	while [ "$(date +%s)" -lt "$SAMPLE_UNTIL_EPOCH" ]; do
		ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
		qd=$(kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/${NAMESPACE}/${EXTERNAL_METRIC_NAME}?labelSelector=scaledobject.keda.sh%2Fname%3Dworker-image-scaledobject" 2>/dev/null | grep -o '"value":"[0-9]*"' | head -1 | cut -d'"' -f4)
		wr=$(kubectl get deployment worker -n "$NAMESPACE" -o jsonpath='{.status.replicas}' 2>/dev/null || echo 0)
		echo "${ts},${qd:-0},${wr:-0}" >>"$CSV_FILE"
		sleep 5
	done
}

sampleLoop &
SAMPLER_PID=$!
echo "sampler started (pid=$SAMPLER_PID), capturing steady state before burst..."
sleep 15

# Synthesize a medium image fixture so each conversion takes a couple
# seconds and the backlog stays above threshold long enough for the HPA to
# add replicas (D-05, planner discretion). Fallback: 20x sample.png if
# Pillow can't be pulled via uv for any reason.
BURST_FIXTURE="/tmp/loadproof-burst-${RUN_TS}.png"
if ! uv run --with pillow python3 -c "
from PIL import Image
import os
w, h = 2000, 1500
img = Image.frombytes('RGB', (w, h), os.urandom(w * h * 3))
img.save('${BURST_FIXTURE}')
" 2>/tmp/keda-loadproof-pillow.log; then
	echo "NOTE: Pillow-via-uv synthesis failed, falling back to internal/e2e/testdata/sample.png for the burst fixture ($(cat /tmp/keda-loadproof-pillow.log))"
	BURST_FIXTURE="internal/e2e/testdata/sample.png"
fi
echo "burst fixture: $BURST_FIXTURE"

# Fire 20 parallel image jobs. Background subshells can't set parent-shell
# variables, so each subshell appends its job id to a shared file instead
# (heeding the bash `local` multi-assignment word-expansion pitfall
# documented in 27-03-SUMMARY.md deviation 2 -- irrelevant here since we
# avoid `local` entirely in the subshell body, but the file-collection
# approach is the same root workaround for background-subshell scoping).
BURST_JOB_IDS_FILE="/tmp/keda-loadproof-burst-jobids-${RUN_TS}.txt"
: >"$BURST_JOB_IDS_FILE"
for i in $(seq 1 20); do
	(
		jid=$(postJobPath "$BURST_FIXTURE" "jpg" "image/png")
		echo "$jid" >>"$BURST_JOB_IDS_FILE"
	) &
done
wait
BURST_JOB_COUNT=$(wc -l <"$BURST_JOB_IDS_FILE" | tr -d '[:space:]')
assert_eq "20" "$BURST_JOB_COUNT" "burst: 20 parallel image jobs submitted (png->jpg)"

# ASSERT SC1 literally: >=2 replicas within 60s.
IMAGE_REPLICAS_AFTER=$(waitForReplicasAtLeast worker 2 60) || {
	echo "FAIL: SC1 -- worker (image) never reached >=2 replicas within 60s of the burst (observed: $IMAGE_REPLICAS_AFTER)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC1 -- worker (image) scaled 0->${IMAGE_REPLICAS_AFTER} within 60s of the 20-job burst"

# Record (not assert) the peak replicas observed, up to maxReplicaCount=4.
log "Recording peak worker (image) replicas (informational -- reaching 4 is a fact, not an assertion, D-04)"
PEAK_REPLICAS="$IMAGE_REPLICAS_AFTER"
for i in $(seq 1 20); do
	cur=$(kubectl get deployment worker -n "$NAMESPACE" -o jsonpath='{.status.replicas}' 2>/dev/null || echo 0)
	cur="${cur:-0}"
	if [ "$cur" -gt "$PEAK_REPLICAS" ]; then
		PEAK_REPLICAS="$cur"
	fi
	sleep 3
done
echo "OBSERVED (not asserted): peak worker (image) replicas during burst = $PEAK_REPLICAS (maxReplicaCount=4)"

# D-06: drain, then SC2 N->0.
log "SC2/D-06 -- draining burst queue, then confirming N->0"
DRAIN_TIMEOUT=180
waited=0
metric_zero=""
while [ "$waited" -lt "$DRAIN_TIMEOUT" ]; do
	RAW_METRIC_NOW=$(kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/${NAMESPACE}/${EXTERNAL_METRIC_NAME}?labelSelector=scaledobject.keda.sh%2Fname%3Dworker-image-scaledobject" 2>/dev/null || true)
	VAL=$(printf '%s' "$RAW_METRIC_NOW" | grep -o '"value":"[0-9]*"' | head -1 | cut -d'"' -f4)
	if [ "${VAL:-1}" = "0" ]; then
		metric_zero=1
		break
	fi
	sleep 5
	waited=$((waited + 5))
done
assert_nonempty "$metric_zero" "burst queue drained (octoconv_queue_depth image returned to 0)"

IMAGE_REPLICAS_FINAL=$(waitForReplicasAtMost worker 0 180) || {
	echo "FAIL: SC2 -- worker (image) never returned to 0 replicas within 180s after drain (observed: $IMAGE_REPLICAS_FINAL)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC2 -- worker (image) full-cycled back to 0 replicas (observed: $IMAGE_REPLICAS_FINAL)"

# Stop the sampler cleanly so the CSV has a clean end marker, then render
# the D-02 PNG.
if [ -n "$SAMPLER_PID" ]; then
	kill "$SAMPLER_PID" >/dev/null 2>&1 || true
	wait "$SAMPLER_PID" 2>/dev/null || true
	SAMPLER_PID=""
fi

uv run --with matplotlib python3 scripts/fixtures/render_evidence.py \
	--csv "$CSV_FILE" \
	--png "$EVIDENCE_DIR/sc1-sc2-burst-${RUN_TS}.png" \
	--title "Phase 28 SC1/SC2: image-class burst 0->N->0"
echo "PASS: SC1/SC2 evidence rendered -- CSV: $CSV_FILE, PNG: $EVIDENCE_DIR/sc1-sc2-burst-${RUN_TS}.png"

# =============================================================================
# === SC3 INSERTION POINT (Task 3 appends the downscale-soak scenario here,
# === after SC1/SC2, before the final ALL-PASSED summary) ===================
# =============================================================================
