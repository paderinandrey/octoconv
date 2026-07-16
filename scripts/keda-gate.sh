#!/usr/bin/env bash
# keda-gate.sh -- Phase 27 (KEDA autoscaling) live hard gate.
#
# Proves, against a REAL OrbStack Kubernetes cluster, D-12:
#   SC1 (D-12a): octoconv_queue_depth resolves via `kubectl get --raw` on the
#     external.metrics.k8s.io API while the image `worker` Deployment is
#     GENUINELY at 0 replicas -- the load-bearing scale-from-zero proof.
#   SC2 (D-12b): all three scaled classes (image/document/html) scale 0->1
#     from a single real conversion job of their own type -- catches
#     doc/html-specific cold-start issues now, not in Phase 28.
#   SC2 cont. (D-12c): the image class (fastest) cycles back to 0 replicas
#     after its cooldownPeriod -- one full 0->1->0 cycle proof.
#   SC3 (D-12d/D-09): webhook-worker holds replicas:2 throughout, with NO
#     ScaledObject ever referencing it -- fail-closed hard gate, checked at
#     start/mid/end.
#
# Burst 0->N->0 with timestamps and long-job graceful-scale-down soak are
# explicitly OUT of scope here -- that is Phase 28 (KEDA-03).
#
# OrbStack discipline (D-13): the CALLER must ensure the compose stack is
# DOWN (`docker compose ... down -v`) and that all app images are
# pre-built SEQUENTIALLY with the pinned `dev` tag BEFORE this script runs.
# This script itself never runs `docker compose up`/`docker build`. It
# tears the k8s stack down (helm uninstall) unconditionally on exit (trap),
# success or failure, so OrbStack is never left hot.
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

# api reachability for job submission -- port-forwarded locally by this
# script (Phase 24/25 sanctioned mechanism for hitting in-cluster services
# from the OrbStack host).
API_LOCAL_PORT="18090"
API_BASE="http://127.0.0.1:${API_LOCAL_PORT}"
DB_LOCAL_PORT="15434"

PASS_COUNT=0
GATE_OK=""
API_PF_PID=""
DB_PF_PID=""

# ---------------------------------------------------------------------------
# Assertion helpers -- every one echoes and exits 1 (loud, non-zero) on
# mismatch; every one echoes a PASS line with the observed value on success.
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
# Teardown -- ALWAYS runs (trap on EXIT), success or failure (D-13: never
# leave the k8s stack hot). Kills any port-forwards first so helm uninstall
# isn't fighting live connections, then uninstalls octoconv, then keda.
# ---------------------------------------------------------------------------
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

	echo "waiting for octoconv workloads to be gone..."
	for i in $(seq 1 30); do
		remaining=$(kubectl get deployment -n "$NAMESPACE" --no-headers 2>/dev/null | wc -l | tr -d '[:space:]')
		if [ "${remaining:-0}" = "0" ]; then
			break
		fi
		sleep 2
	done
	echo "octoconv namespace deployments remaining: ${remaining:-unknown}"

	echo ""
	if [ "$exit_code" -eq 0 ] && [ "$GATE_OK" = "1" ]; then
		echo "✅ PASS -- Phase 27 KEDA live hard gate: all D-12 assertions verified ($PASS_COUNT checks)."
	else
		echo "❌ FAIL -- Phase 27 KEDA live hard gate did not complete (exit=$exit_code, checks passed=$PASS_COUNT)." >&2
	fi
	exit "$exit_code"
}
trap teardown EXIT

echo "=== Phase 27 KEDA autoscaling: live hard gate (D-12) ==="

# ---------------------------------------------------------------------------
# STEP 1: Preflight.
# ---------------------------------------------------------------------------
log "STEP 1: preflight"

kubectl get nodes >/dev/null
echo "PASS: kubectl reaches the OrbStack cluster (context: $(kubectl config current-context))"

COMPOSE_UP=$(docker compose ps --format '{{.Names}}' 2>/dev/null | grep -c '^octoconv-' || true)
if [ "${COMPOSE_UP:-0}" -gt 0 ]; then
	echo "FAIL: compose stack appears to be UP ($COMPOSE_UP octoconv-* containers running) -- compose and k8s stacks must NEVER be hot simultaneously (D-13). Run 'docker compose ... down -v' first." >&2
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
# STEP 2: Install KEDA (idempotent).
# ---------------------------------------------------------------------------
log "STEP 2: helm install KEDA v$KEDA_VERSION into namespace $KEDA_NAMESPACE"

if helm status keda -n "$KEDA_NAMESPACE" >/dev/null 2>&1; then
	echo "keda release already present -- upgrading in place (idempotent)"
	helm upgrade keda kedacore/keda --namespace "$KEDA_NAMESPACE" --version "$KEDA_VERSION" --wait --timeout 5m
else
	helm install keda kedacore/keda --namespace "$KEDA_NAMESPACE" --create-namespace --version "$KEDA_VERSION" --wait --timeout 5m
fi
echo "PASS: KEDA v$KEDA_VERSION installed/upgraded, operator Deployments Available"

# ---------------------------------------------------------------------------
# STEP 3: Poll the external metrics APIService for Available:True. The
# metric LIST is expected to be empty until a ScaledObject exists -- that
# is expected, not a failure signal, at this point.
# ---------------------------------------------------------------------------
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
echo "NOTE: external metrics list is expected EMPTY until a ScaledObject exists -- not a failure."

# ---------------------------------------------------------------------------
# STEP 4: Install octoconv (keda.enabled=true prometheus.enabled=true via
# values-local.yaml), WITHOUT --wait (Phase 24 decision: createbucket
# post-install hook <-> app-readiness chicken-egg). Then kubectl wait per
# always-on Deployment only -- the three scaled workers may already be at 0
# and must NOT be waited on for Available.
# ---------------------------------------------------------------------------
log "STEP 4: helm install octoconv (keda+prometheus enabled), WITHOUT --wait"

helm install octoconv "$CHART_DIR" -f "$VALUES_LOCAL" -n "$NAMESPACE" --create-namespace
echo "PASS: helm install octoconv complete (async install; readiness gated below)"

log "waiting for always-on / min-1 Deployments to become Available"
for d in postgres redis minio api prometheus webhook-worker; do
	kubectl wait --for=condition=Available "deployment/$d" -n "$NAMESPACE" --timeout=240s
	echo "PASS: deployment/$d Available"
done

# ---------------------------------------------------------------------------
# STEP 5 (SC3/D-12d/D-09 part 1 -- checked at START): webhook-worker fixed
# at 2 replicas, no ScaledObject referencing it. Checked again at MID and
# END below (D-12d requires "throughout").
# ---------------------------------------------------------------------------
log "STEP 5: webhook-worker gate check (START) -- D-09 fail-closed"

WEBHOOK_REPLICAS_START=$(kubectl get deployment webhook-worker -n "$NAMESPACE" -o jsonpath='{.spec.replicas}')
assert_eq "2" "$WEBHOOK_REPLICAS_START" "webhook-worker replicas (START)"

WEBHOOK_SO_COUNT_START=$(kubectl get scaledobject -n "$NAMESPACE" -o jsonpath='{.items[*].spec.scaleTargetRef.name}' 2>/dev/null | tr ' ' '\n' | grep -c '^webhook-worker$' || true)
assert_eq "0" "${WEBHOOK_SO_COUNT_START:-0}" "ScaledObjects targeting webhook-worker (START)"

# ---------------------------------------------------------------------------
# STEP 6 (SC1/D-12a -- THE load-bearing proof): confirm the image worker is
# genuinely at 0 replicas, discover the external metric name LIVE (Pitfall
# 5 -- never hardcode), then confirm it resolves via kubectl get --raw.
# ---------------------------------------------------------------------------
log "STEP 6: SC1 -- octoconv_queue_depth resolves at genuinely 0 replicas"

# The worker Deployment's chart-owned spec.replicas defaults to 1 (helm
# renders it that way before KEDA's HPA takes ownership). KEDA only scales
# it down to minReplicaCount=0 once it has observed the queue empty across
# its cooldownPeriod (60s for image) from ScaledObject creation -- this is
# expected startup behavior, not a failure, so poll rather than assert
# immediately. Bounded to cooldownPeriod (60s) + generous margin.
echo "waiting for worker (image) to settle at 0 replicas (KEDA cooldownPeriod=60s + margin)..."
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
assert_eq "0" "$IMAGE_REPLICAS_BEFORE" "worker (image) Deployment status.replicas before any job"

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
	echo "FAIL: SC1 -- external metric '$EXTERNAL_METRIC_NAME' never returned a value after 30s. Last response: $RAW_METRIC" >&2
	exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC1 -- octoconv_queue_depth (image) resolved via kubectl get --raw at 0 replicas: $RAW_METRIC"

# ---------------------------------------------------------------------------
# STEP 7 (SC2/D-12b): submit one real conversion job per scaled class, then
# poll the target Deployment until status.replicas >= 1. Port-forward api
# and postgres to reach them from the OrbStack host (sanctioned mechanism,
# 24-03/25-03 precedent).
# ---------------------------------------------------------------------------
log "STEP 7: SC2 -- per-class 0->1 scale-up from a single real job"

kubectl port-forward -n "$NAMESPACE" svc/api "${API_LOCAL_PORT}:8090" >/tmp/keda-gate-api-pf.log 2>&1 &
API_PF_PID=$!
kubectl port-forward -n "$NAMESPACE" svc/postgres "${DB_LOCAL_PORT}:5432" >/tmp/keda-gate-db-pf.log 2>&1 &
DB_PF_PID=$!
sleep 3

echo "waiting for port-forwarded /healthz..."
healthy=""
for i in $(seq 1 30); do
	code=$(curl -s -o /tmp/keda-gate-healthz.json -w '%{http_code}' "$API_BASE/healthz" || true)
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
echo "PASS: api reachable via port-forward, /healthz 200 ($(cat /tmp/keda-gate-healthz.json))"

export DATABASE_URL="postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db"
export API_KEY_SALT="dev-only-change-me-in-real-deploys"

SUFFIX=$(date +%s)
CLIENT_OUT=$(go run ./cmd/manage-clients create "keda-gate-${SUFFIX}")
CLIENT_KEY=$(printf '%s\n' "$CLIENT_OUT" | awk -F': ' '/^api key/{print $2}')
assert_nonempty "$CLIENT_KEY" "minted gate client + API key"

# postJob submits one real conversion job of the given class's type and
# returns the job_id. HTTP_STATUS is set as a side effect (curl -w).
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

# waitForReplicasAtLeast polls a Deployment's status.replicas until it
# reaches the given floor within a bounded timeout, distinguishing "never
# scaled" (timeout with the metric never having risen) from a slow-but-
# eventual scale-up by printing the last observed value on failure.
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

# waitForReplicasAtMost polls a Deployment's status.replicas until it drops
# to the given ceiling within a bounded timeout.
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

# --- image class: sample.png -> jpg -----------------------------------
IMAGE_JOB_ID=$(postJob "sample.png" "jpg" "image/png")
assert_nonempty "$IMAGE_JOB_ID" "image class job submitted (sample.png -> jpg)"

IMAGE_REPLICAS_AFTER=$(waitForReplicasAtLeast worker 1 120) || {
	echo "FAIL: SC2 -- worker (image) never reached >=1 replica within 120s after job submission (observed: $IMAGE_REPLICAS_AFTER)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC2 -- worker (image) scaled 0->${IMAGE_REPLICAS_AFTER} after job $IMAGE_JOB_ID"

# --- document class: sample.docx -> pdf --------------------------------
DOCUMENT_JOB_ID=$(postJob "sample.docx" "pdf" "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
assert_nonempty "$DOCUMENT_JOB_ID" "document class job submitted (sample.docx -> pdf)"

DOCUMENT_REPLICAS_AFTER=$(waitForReplicasAtLeast document-worker 1 180) || {
	echo "FAIL: SC2 -- document-worker never reached >=1 replica within 180s after job submission (observed: $DOCUMENT_REPLICAS_AFTER)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC2 -- document-worker scaled 0->${DOCUMENT_REPLICAS_AFTER} after job $DOCUMENT_JOB_ID"

# --- html class: sample.html -> pdf -------------------------------------
HTML_JOB_ID=$(postJob "sample.html" "pdf" "text/html")
assert_nonempty "$HTML_JOB_ID" "html class job submitted (sample.html -> pdf)"

HTML_REPLICAS_AFTER=$(waitForReplicasAtLeast chromium-worker 1 150) || {
	echo "FAIL: SC2 -- chromium-worker never reached >=1 replica within 150s after job submission (observed: $HTML_REPLICAS_AFTER)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC2 -- chromium-worker scaled 0->${HTML_REPLICAS_AFTER} after job $HTML_JOB_ID"

# ---------------------------------------------------------------------------
# STEP 8 (SC3/D-12d part 2 -- checked MID, after all three classes scaled
# up): webhook-worker still fixed at 2, still no ScaledObject.
# ---------------------------------------------------------------------------
log "STEP 8: webhook-worker gate check (MID) -- D-09 fail-closed"

WEBHOOK_REPLICAS_MID=$(kubectl get deployment webhook-worker -n "$NAMESPACE" -o jsonpath='{.spec.replicas}')
assert_eq "2" "$WEBHOOK_REPLICAS_MID" "webhook-worker replicas (MID)"

WEBHOOK_SO_COUNT_MID=$(kubectl get scaledobject -n "$NAMESPACE" -o jsonpath='{.items[*].spec.scaleTargetRef.name}' 2>/dev/null | tr ' ' '\n' | grep -c '^webhook-worker$' || true)
assert_eq "0" "${WEBHOOK_SO_COUNT_MID:-0}" "ScaledObjects targeting webhook-worker (MID)"

# ---------------------------------------------------------------------------
# STEP 9 (SC2 cont./D-12c -- image only, fastest class): wait for the job to
# drain, then poll the image worker back down to 0 replicas within a bounded
# window (cooldownPeriod 60s + margin).
# ---------------------------------------------------------------------------
log "STEP 9: image class full-cycle -- poll back down to 0 replicas after cooldown"

echo "waiting for image job $IMAGE_JOB_ID to reach a terminal status..."
job_status=""
for i in $(seq 1 60); do
	code=$(curl -s -o /tmp/keda-gate-image-job.json -w '%{http_code}' -H "Authorization: ApiKey $CLIENT_KEY" "$API_BASE/v1/jobs/$IMAGE_JOB_ID")
	job_status=$(grep -o '"status":"[^"]*"' /tmp/keda-gate-image-job.json | head -1 | cut -d'"' -f4)
	if [ "$job_status" = "done" ] || [ "$job_status" = "failed" ]; then
		break
	fi
	sleep 2
done
assert_eq "done" "$job_status" "image job $IMAGE_JOB_ID reaches done"

IMAGE_REPLICAS_FINAL=$(waitForReplicasAtMost worker 0 180) || {
	echo "FAIL: SC2/D-12c -- worker (image) never returned to 0 replicas within 180s after cooldownPeriod (observed: $IMAGE_REPLICAS_FINAL)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC2/D-12c -- worker (image) full-cycled back to 0 replicas (observed: $IMAGE_REPLICAS_FINAL)"

# ---------------------------------------------------------------------------
# STEP 10 (SC3/D-12d part 3 -- checked END): webhook-worker still fixed at
# 2, still no ScaledObject, at the very end of the gate.
# ---------------------------------------------------------------------------
log "STEP 10: webhook-worker gate check (END) -- D-09 fail-closed"

WEBHOOK_REPLICAS_END=$(kubectl get deployment webhook-worker -n "$NAMESPACE" -o jsonpath='{.spec.replicas}')
assert_eq "2" "$WEBHOOK_REPLICAS_END" "webhook-worker replicas (END)"

WEBHOOK_SO_COUNT_END=$(kubectl get scaledobject -n "$NAMESPACE" -o jsonpath='{.items[*].spec.scaleTargetRef.name}' 2>/dev/null | tr ' ' '\n' | grep -c '^webhook-worker$' || true)
assert_eq "0" "${WEBHOOK_SO_COUNT_END:-0}" "ScaledObjects targeting webhook-worker (END)"

# ---------------------------------------------------------------------------
# Done. Teardown (STEP 11) runs unconditionally via the EXIT trap above.
# ---------------------------------------------------------------------------
GATE_OK="1"
echo ""
echo "=== ALL $PASS_COUNT ASSERTIONS PASSED ==="
echo "SC1 (D-12a): octoconv_queue_depth resolved at genuinely 0 replicas -- PASS"
echo "SC2 (D-12b): image/document/html all scaled 0->1 from their own job type -- PASS"
echo "SC2 (D-12c): image class cycled back to 0 after cooldown -- PASS"
echo "SC3 (D-12d/D-09): webhook-worker held replicas:2, no ScaledObject, START/MID/END -- PASS"
