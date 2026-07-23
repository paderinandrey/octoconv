#!/usr/bin/env bash
# keda-av-downscale-survival.sh -- Phase 37 (AVE-05, D-07 proof #2, SC4)
# av N->N-1 downscale-survival live-cluster proof gate.
#
# Sibling of scripts/keda-av-loadproof.sh -- reuses its header/preflight/
# teardown/postJobPath/waitForReplicas*/fixture-synthesis shapes -- but
# proves the OTHER live half of D-07: with a LONG in-flight av transcode,
# a genuine KEDA/HPA N->N-1 downscale must SIGTERM the victim pod and the
# in-flight job must still complete gracefully (no exit-137/SIGKILL
# mid-job), validating avWorker.terminationGracePeriodSeconds=783 (Phase
# 36's measured AV_ENGINE_TIMEOUT=753s + 30s margin) against a REAL
# cluster event, not a synthetic unit test.
#
# Victim-pod selection mirrors scripts/keda-load-proof.sh's SC3 document
# downscale-soak scenario (read there only, not reproduced verbatim here):
# submit a LONG job FIRST, confirm it is genuinely active, submit a SHORT
# job SECOND (AV_WORKER_CONCURRENCY=1 forces it onto a NEW pod), identify
# the busy pod (earliest creationTimestamp among Running/non-Terminating
# av-worker pods) and best-effort-annotate it with
# controller.kubernetes.io/pod-deletion-cost=-1000 BEFORE the 2->1
# downscale fires so the ReplicaSet controller's victim selection targets
# it -- the downscale itself remains a genuine KEDA/HPA event; this script
# never issues an imperative pod-deletion command.
#
# This gate layers BOTH deploy/chart/octoconv/values-local.yaml AND
# deploy/chart/octoconv/values-loadproof.yaml (keda.av.
# scaleDownStabilizationSeconds:15, this plan's addition) -- production's
# 900s stabilization would push the 2->1 downscale far past any reasonable
# gate window, but the Deployment's PRODUCTION 783s grace is left
# untouched by both overlays (that grace is exactly what this gate
# validates).
#
# AV FIXTURE DIVERGENCE (mirrors keda-av-loadproof.sh): av has NO
# committed video fixture, so a LONG lavfi fixture (near
# AV_MAX_DURATION_SECONDS=90's ceiling) is synthesized entirely
# in-container via ffmpeg testsrc+sine, matching scripts/av-rtf-measure.sh's
# pattern, then `docker cp`'d to the host for a real multipart upload. An
# explicit {"codec":"hevc","resolution_height":1080} request forces a full
# re-encode of the worst measured RTF cell (hevc@1080, p95_RTF=4.179133s,
# 36-04-SUMMARY.md) rather than a fast AVC-05 stream-copy remux, so the
# transcode is still genuinely in-flight when the 15s-stabilized downscale
# fires.
#
# scripts/keda-load-proof.sh, scripts/keda-gate.sh, and
# scripts/keda-audio-loadproof.sh are left byte-unchanged by this phase --
# av logic lives exclusively in this file and its sibling,
# scripts/keda-av-loadproof.sh. Neither frozen script is touched.
#
# This gate is SELF-CONTAINED: it installs KEDA itself, layers both
# overlays on top of the chart, and tears everything down via an EXIT trap
# -- success or failure, OrbStack is never left hot. It refuses to run if
# the docker-compose stack is up (compose and k8s stacks must never be hot
# simultaneously -- four confirmed OrbStack daemon wedges on record).
#
# T-37-06 (DoS via orphaned watcher) / T-37-09 (tampering with production
# grace via the loadproof overlay) from this plan's threat register are
# mitigated below: the EXIT trap always runs and kills the pod-status
# watcher AND the fixture-synthesis container by process-group +
# belt-and-suspenders pkill (Phase-29 WR-01/WR-02/WR-03 pattern); and the
# overlay adds ONLY scaleDownStabilizationSeconds, never a grace-period
# override.
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

# The av-worker image is NOT built by this script -- it is an operator
# precondition (same as scripts/keda-av-loadproof.sh): `docker compose
# build av-worker` (or an equivalent docker build against
# Dockerfile.av-worker) must already have produced this exact tag,
# matching values.yaml's avWorker.image.repository + global.imageTag.
AV_IMAGE_TAG="octoconv-av-worker:dev"

# Distinct local ports from every sibling gate script (keda-gate.sh,
# keda-load-proof.sh, keda-audio-loadproof.sh, keda-av-loadproof.sh) so
# this gate can never collide with a concurrently-running one.
API_LOCAL_PORT="18094"
API_BASE="http://127.0.0.1:${API_LOCAL_PORT}"
DB_LOCAL_PORT="15438"

PASS_COUNT=0
GATE_OK=""
API_PF_PID=""
DB_PF_PID=""
SNAPSHOT_PID=""
FIXTURE_CONTAINER=""
# BUSY_POD is set once the victim pod is identified; kept declared here
# (empty) so teardown() can unconditionally reference it regardless of how
# far the run got before failing.
BUSY_POD=""

EVIDENCE_DIR=".planning/phases/37-keda-helm-chart-integration/evidence"
RUN_TS=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$EVIDENCE_DIR"
LOG_FILE="$EVIDENCE_DIR/gate-transcript-downscale-${RUN_TS}.log"

# Tee the whole run to the timestamped transcript -- every PASS/FAIL line,
# every observed value, becomes part of the committed evidence.
exec > >(tee "$LOG_FILE") 2>&1

# ---------------------------------------------------------------------------
# Assertion helpers -- copied verbatim from scripts/keda-load-proof.sh /
# scripts/keda-gate.sh / scripts/keda-audio-loadproof.sh /
# scripts/keda-av-loadproof.sh.
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

assert_nonempty_redacted() {
	local value="$1" label="$2"
	if [ -z "$value" ]; then
		echo "FAIL: $label -- expected a non-empty value, got empty" >&2
		exit 1
	fi
	PASS_COUNT=$((PASS_COUNT + 1))
	echo "PASS: $label == [REDACTED, ${#value} chars]"
}

log() { echo ""; echo "--- $* ---"; }

# ---------------------------------------------------------------------------
# Teardown -- ALWAYS runs (trap on EXIT), success or failure: OrbStack must
# never be left hot. Kills the pod-status watcher first (process-group +
# belt-and-suspenders pkill, Phase-29 WR-01/WR-02/WR-03), removes the
# throwaway fixture-synthesis container, then the port-forwards, then
# uninstalls octoconv and keda.
# ---------------------------------------------------------------------------
teardown() {
	local exit_code=$?
	echo ""
	echo "=== TEARDOWN (OrbStack must never be left hot) ==="

	if [ -n "$SNAPSHOT_PID" ]; then
		kill -- -"$SNAPSHOT_PID" >/dev/null 2>&1 || true
		wait "$SNAPSHOT_PID" 2>/dev/null || true
		# Belt-and-suspenders (macOS process-group semantics are unreliable):
		# deterministically reap any orphaned watch by its exact command shape.
		[ -n "$BUSY_POD" ] && pkill -f "kubectl get pod ${BUSY_POD} .* -w" >/dev/null 2>&1 || true
	fi
	if [ -n "$FIXTURE_CONTAINER" ]; then
		docker rm -f "$FIXTURE_CONTAINER" >/dev/null 2>&1 || true
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
	if [ "$exit_code" -eq 0 ] && [ "$GATE_OK" = "1" ]; then
		echo "PASS -- Phase 37 av downscale-survival load-proof gate run complete ($PASS_COUNT checks). Transcript: $LOG_FILE"
	else
		echo "FAIL -- Phase 37 av downscale-survival load-proof gate did not complete (exit=$exit_code, checks passed=$PASS_COUNT)." >&2
	fi
	exit "$exit_code"
}
trap teardown EXIT

echo "=== Phase 37 av downscale-survival live-proof gate (AVE-05, D-07 proof #2, SC4) ==="
echo "run timestamp: $RUN_TS"
echo "evidence dir: $EVIDENCE_DIR"

# ---------------------------------------------------------------------------
# STEP 1: Preflight -- compose and k8s stacks must never be hot
# simultaneously.
# ---------------------------------------------------------------------------
log "STEP 1: preflight"

kubectl get nodes >/dev/null
echo "PASS: kubectl reaches the OrbStack cluster (context: $(kubectl config current-context))"

COMPOSE_UP=$(docker compose ps --format '{{.Names}}' 2>/dev/null | grep -c '^octoconv-' || true)
if [ "${COMPOSE_UP:-0}" -gt 0 ]; then
	echo "FAIL: compose stack appears to be UP ($COMPOSE_UP octoconv-* containers running) -- compose and k8s stacks must NEVER be hot simultaneously. Run 'docker compose ... down -v' first." >&2
	exit 1
fi
echo "PASS: compose stack is down (0 octoconv-* containers running)"

if ! docker image inspect "$AV_IMAGE_TAG" >/dev/null 2>&1; then
	echo "FAIL: $AV_IMAGE_TAG not found locally -- run 'docker compose build av-worker' (or an equivalent docker build against Dockerfile.av-worker) before this gate." >&2
	exit 1
fi
echo "PASS: $AV_IMAGE_TAG present locally"

helm repo add kedacore https://kedacore.github.io/charts >/dev/null 2>&1 || true
helm repo update >/dev/null
if ! helm search repo kedacore/keda --versions | awk '{print $2}' | grep -qx "$KEDA_VERSION"; then
	LATEST_KEDA=$(helm search repo kedacore/keda --versions | awk 'NR==2{print $2}')
	echo "FAIL: KEDA v$KEDA_VERSION is no longer resolvable in kedacore/keda -- current latest is $LATEST_KEDA. Repin KEDA_VERSION in this script and re-run." >&2
	exit 1
fi
echo "PASS: KEDA v$KEDA_VERSION re-verified resolvable in kedacore/keda (live)"

# ---------------------------------------------------------------------------
# STEP 2: Install KEDA (idempotent) -- gate is self-contained.
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
# STEP 4: Install octoconv layering BOTH values-local.yaml AND
# values-loadproof.yaml (keda.av.scaleDownStabilizationSeconds:15) --
# WITHOUT --wait (Phase 24 decision: createbucket post-install hook <->
# app-readiness chicken-egg). Then kubectl wait per always-on Deployment
# only -- av-worker (KEDA/HPA-owned) is intentionally excluded from this
# wait.
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
# STEP 4b: seed asynq's queue registry (Redis "asynq:queues" SET),
# including av, so the WR-01 absent-metric fallback resolves to a real
# zero instead of holding fallback.replicas:1 indefinitely on a fresh
# install.
# ---------------------------------------------------------------------------
log "STEP 4b: seed asynq queue registry (Redis), including av"

REDIS_POD=$(kubectl get pod -n "$NAMESPACE" -l "app.kubernetes.io/component=redis" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
assert_nonempty "$REDIS_POD" "redis pod discovered for queue-registry seeding"
kubectl exec -n "$NAMESPACE" "$REDIS_POD" -- redis-cli SADD asynq:queues image document html audio av webhook >/dev/null
echo "PASS: asynq:queues seeded (image, document, html, audio, av, webhook) -- zero tasks created, no worker processing triggered"

# ---------------------------------------------------------------------------
# STEP 5: port-forward api+postgres, mint a client key.
# ---------------------------------------------------------------------------
log "STEP 5: port-forward api+postgres, mint client key"

kubectl port-forward -n "$NAMESPACE" svc/api "${API_LOCAL_PORT}:8090" >/tmp/keda-av-downscale-api-pf.log 2>&1 &
API_PF_PID=$!
kubectl port-forward -n "$NAMESPACE" svc/postgres "${DB_LOCAL_PORT}:5432" >/tmp/keda-av-downscale-db-pf.log 2>&1 &
DB_PF_PID=$!
sleep 3

echo "waiting for port-forwarded /healthz..."
healthy=""
for i in $(seq 1 30); do
	code=$(curl -s -o /tmp/keda-av-downscale-healthz.json -w '%{http_code}' "$API_BASE/healthz" || true)
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
echo "PASS: api reachable via port-forward, /healthz 200 ($(cat /tmp/keda-av-downscale-healthz.json))"

export DATABASE_URL="postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db"
export API_KEY_SALT="dev-only-change-me-in-real-deploys"

SUFFIX=$(date +%s)
CLIENT_OUT=$(go run ./cmd/manage-clients create "keda-av-downscale-${SUFFIX}")
CLIENT_KEY=$(printf '%s\n' "$CLIENT_OUT" | awk -F': ' '/^api key/{print $2}')
assert_nonempty_redacted "$CLIENT_KEY" "minted gate client + API key"

postJobPath() {
	local path="$1" target="$2" content_type="$3" opts="${4:-}"
	local filename
	filename=$(basename "$path")
	local out_file="/tmp/keda-av-downscale-post-${filename}.json"
	local curl_args=(-s -o "$out_file" -w '%{http_code}' -X POST "$API_BASE/v1/jobs" \
		-H "Authorization: ApiKey $CLIENT_KEY" \
		-F "target=$target" \
		-F "file=@${path};type=${content_type}")
	if [ -n "$opts" ]; then
		curl_args+=(-F "opts=$opts")
	fi
	HTTP_STATUS=$(curl "${curl_args[@]}" || true)
	if [ "$HTTP_STATUS" != "202" ]; then
		echo "FAIL: POST /v1/jobs for $filename -> $target returned $HTTP_STATUS, body: $(cat "$out_file")" >&2
		exit 1
	fi
	grep -o '"job_id":"[^"]*"' "$out_file" | head -1 | cut -d'"' -f4
}

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
# STEP 6: confirm av-worker is genuinely at 0 replicas before triggering.
# ---------------------------------------------------------------------------
log "STEP 6: confirm av-worker settles at 0 replicas before triggering"

echo "waiting for av-worker to settle at 0 replicas (KEDA cooldownPeriod=180s + margin)..."
AV_REPLICAS_BEFORE="1"
waited=0
while [ "$waited" -lt 240 ]; do
	AV_REPLICAS_BEFORE=$(kubectl get deployment av-worker -n "$NAMESPACE" -o jsonpath='{.status.replicas}' 2>/dev/null || echo "0")
	AV_REPLICAS_BEFORE="${AV_REPLICAS_BEFORE:-0}"
	if [ "$AV_REPLICAS_BEFORE" = "0" ]; then
		break
	fi
	sleep 5
	waited=$((waited + 5))
done
assert_eq "0" "$AV_REPLICAS_BEFORE" "av-worker Deployment status.replicas before SC4 (TRUE zero precondition)"

# ---------------------------------------------------------------------------
# STEP 7: synthesize the LONG lavfi fixture near AV_MAX_DURATION_SECONDS=90's
# ceiling, in-container (mirrors scripts/av-rtf-measure.sh's testsrc+sine
# pattern), plus a SHORT lavfi fixture for the second (victim-selection)
# job.
# ---------------------------------------------------------------------------
log "STEP 7: synthesize LONG (near-90s, 1080p) + SHORT lavfi fixtures"

WORKDIR=$(mktemp -d)
# 85s: comfortably under AV_MAX_DURATION_SECONDS=90 (ffprobe-enforced,
# fail-closed) while, at the measured worst-cell hevc@1080 p95_RTF=4.179133
# (36-04-SUMMARY.md), yielding a wall-clock transcode of ~hundreds of
# seconds -- comfortably outliving values-loadproof.yaml's 15s
# scaleDownStabilizationSeconds observation window while staying well
# under avWorker.terminationGracePeriodSeconds=783.
LONG_FIXTURE_DURATION_S=85
SHORT_FIXTURE_DURATION_S=3

FIXTURE_CONTAINER="octoconv-av-downscale-fixture-$$"
docker run -d --name "$FIXTURE_CONTAINER" --entrypoint sleep "$AV_IMAGE_TAG" infinity >/dev/null
docker exec "$FIXTURE_CONTAINER" mkdir -p /tmp/work

docker exec "$FIXTURE_CONTAINER" ffmpeg -y -nostdin \
	-f lavfi -i "testsrc=duration=${LONG_FIXTURE_DURATION_S}:size=1920x1080:rate=30" \
	-f lavfi -i "sine=frequency=440:duration=${LONG_FIXTURE_DURATION_S}" \
	-c:v libx264 -preset ultrafast -c:a aac \
	/tmp/work/av-downscale-long.mkv >/dev/null 2>&1
docker cp "$FIXTURE_CONTAINER:/tmp/work/av-downscale-long.mkv" "$WORKDIR/av-downscale-long.mkv"

docker exec "$FIXTURE_CONTAINER" ffmpeg -y -nostdin \
	-f lavfi -i "testsrc=duration=${SHORT_FIXTURE_DURATION_S}:size=640x360:rate=30" \
	-f lavfi -i "sine=frequency=440:duration=${SHORT_FIXTURE_DURATION_S}" \
	-c:v libx264 -preset ultrafast -c:a aac \
	/tmp/work/av-downscale-short.mkv >/dev/null 2>&1
docker cp "$FIXTURE_CONTAINER:/tmp/work/av-downscale-short.mkv" "$WORKDIR/av-downscale-short.mkv"

docker rm -f "$FIXTURE_CONTAINER" >/dev/null 2>&1 || true
FIXTURE_CONTAINER=""
assert_nonempty "$(ls -la "$WORKDIR/av-downscale-long.mkv" 2>/dev/null || true)" "LONG lavfi fixture synthesized (${LONG_FIXTURE_DURATION_S}s, 1920x1080, h264/aac in mkv)"
assert_nonempty "$(ls -la "$WORKDIR/av-downscale-short.mkv" 2>/dev/null || true)" "SHORT lavfi fixture synthesized (${SHORT_FIXTURE_DURATION_S}s, 640x360, h264/aac in mkv)"

# ---------------------------------------------------------------------------
# STEP 8: submit the LONG job FIRST, forcing a genuine hevc@1080 re-encode
# (never the AVC-05 stream-copy remux fast path -- requesting "hevc" when
# the source is h264 guarantees a real re-encode). Confirm it reaches
# status=active before submitting the short job.
# ---------------------------------------------------------------------------
log "STEP 8: submit LONG job (hevc@1080 re-encode), confirm active"

LONG_JOB_ID=$(postJobPath "$WORKDIR/av-downscale-long.mkv" "mp4" "video/x-matroska" '{"codec":"hevc","resolution_height":1080}')
assert_nonempty "$LONG_JOB_ID" "SC4: long av job submitted FIRST (hevc@1080, ${LONG_FIXTURE_DURATION_S}s fixture)"

AV_REPLICAS_1=$(waitForReplicasAtLeast av-worker 1 180) || {
	echo "FAIL: SC4 -- av-worker never reached >=1 replica within 180s of the long-job submission (observed: $AV_REPLICAS_1)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC4 -- av-worker scaled 0->${AV_REPLICAS_1} after long job $LONG_JOB_ID"

echo "waiting for long job $LONG_JOB_ID to reach status=active (confirms it is genuinely being processed before pod1 identification)..."
long_status=""
for i in $(seq 1 60); do
	code=$(curl -s -o /tmp/keda-av-downscale-long-job.json -w '%{http_code}' -H "Authorization: ApiKey $CLIENT_KEY" "$API_BASE/v1/jobs/$LONG_JOB_ID")
	long_status=$(grep -o '"status":"[^"]*"' /tmp/keda-av-downscale-long-job.json | head -1 | cut -d'"' -f4 || true)
	if [ "$long_status" = "active" ] || [ "$long_status" = "done" ] || [ "$long_status" = "failed" ]; then
		break
	fi
	sleep 3
done
assert_eq "active" "$long_status" "SC4: long job $LONG_JOB_ID reaches status=active"

# ---------------------------------------------------------------------------
# STEP 9: submit the SHORT job SECOND (AV_WORKER_CONCURRENCY=1 keeps pod1
# full with the long job, so the short job deterministically goes to a NEW
# pod2 -- scaling av-worker to maxReplicaCount=2). Identify the busy pod
# and best-effort-annotate it for downscale-victim selection (Phase-28 SC3
# document precedent, read-only reused here).
# ---------------------------------------------------------------------------
log "STEP 9: submit SHORT job SECOND, scale to 2, identify + annotate busy pod"

SHORT_JOB_ID=$(postJobPath "$WORKDIR/av-downscale-short.mkv" "mp4" "video/x-matroska")
assert_nonempty "$SHORT_JOB_ID" "SC4: short av job submitted SECOND"

AV_REPLICAS_2=$(waitForReplicasAtLeast av-worker 2 180) || {
	echo "FAIL: SC4 -- av-worker never reached >=2 replicas within 180s of the short-job submission (observed: $AV_REPLICAS_2)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC4 -- av-worker scaled ${AV_REPLICAS_1}->${AV_REPLICAS_2} after short job $SHORT_JOB_ID"

# Identify the busy pod = the av-worker pod with the EARLIEST
# creationTimestamp among Running, non-Terminating pods -- the pod that has
# been running since the long job's submission (AV_WORKER_CONCURRENCY=1
# guarantees it never picked up the short job).
BUSY_POD=$(kubectl get pod -n "$NAMESPACE" -l "app.kubernetes.io/component=av-worker" \
	--field-selector=status.phase=Running \
	--sort-by=.metadata.creationTimestamp \
	-o jsonpath='{.items[?(@.metadata.deletionTimestamp=="")].metadata.name}' 2>/dev/null | awk '{print $1}')
assert_nonempty "$BUSY_POD" "SC4: busy pod (earliest creationTimestamp among Running/non-Terminating) identified"

# Annotate the busy pod IMMEDIATELY -- must land BEFORE the short job
# completes / before the ReplicaSet controller's 2->1 deletion decision.
# Best-effort influence on victim selection; the downscale itself remains
# a genuine KEDA/HPA event -- an imperative pod-deletion command is never
# issued here.
kubectl annotate pod "$BUSY_POD" -n "$NAMESPACE" \
	controller.kubernetes.io/pod-deletion-cost=-1000 --overwrite
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC4 -- pod-deletion-cost=-1000 annotated on busy pod $BUSY_POD (before downscale decision)"

# terminationGracePeriodSeconds is validated by THIS gate's outcome (the
# in-flight job surviving a genuine SIGTERM), not merely echoed -- capture
# the pod spec's grace value for the evidence file as a static cross-check
# against the expected 783.
POD_GRACE=$(kubectl get pod "$BUSY_POD" -n "$NAMESPACE" -o jsonpath='{.spec.terminationGracePeriodSeconds}' 2>/dev/null || true)
assert_eq "783" "$POD_GRACE" "SC4: busy pod $BUSY_POD spec.terminationGracePeriodSeconds is the PRODUCTION value (783) even under the loadproof overlay"

# ---------------------------------------------------------------------------
# STEP 10: continuous watch-based capture of the busy pod's terminal
# container state (a graceful downscale victim exposes it under
# status.containerStatuses[0].state.terminated for a very short window
# between process exit and API-object removal -- a periodic poll can miss
# it entirely).
# ---------------------------------------------------------------------------
log "STEP 10: start continuous pod-state watcher on busy pod $BUSY_POD"

SC4_FILE="$EVIDENCE_DIR/sc4-av-downscale-survival-${RUN_TS}.txt"
{
	echo "# Phase 37 av downscale-survival evidence -- run $RUN_TS"
	echo "# long_job_id=$LONG_JOB_ID short_job_id=$SHORT_JOB_ID busy_pod=$BUSY_POD"
	echo "# expected_termination_grace_period_seconds=783 observed_pod_spec_grace=$POD_GRACE"
} >"$SC4_FILE"

snapshotLoop() {
	while true; do
		kubectl get pod "$BUSY_POD" -n "$NAMESPACE" -w --output-watch-events \
			-o jsonpath='{.type}{" phase="}{.object.status.phase}{" term_reason="}{.object.status.containerStatuses[0].state.terminated.reason}{" term_exit="}{.object.status.containerStatuses[0].state.terminated.exitCode}{" term_finished="}{.object.status.containerStatuses[0].state.terminated.finishedAt}{"\n"}' 2>/dev/null \
			| while IFS= read -r watch_line; do
				echo "read_ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ") pod=${BUSY_POD} ${watch_line}" >>"$SC4_FILE"
			done
		if ! kubectl get pod "$BUSY_POD" -n "$NAMESPACE" >/dev/null 2>&1; then
			break
		fi
		sleep 1
	done
}
# WR-04/29-REVIEW WR-01: enable job control in the PARENT (`set -m`) BEFORE
# backgrounding so `snapshotLoop &` becomes its own process-group leader
# (PGID == $SNAPSHOT_PID). `kill -- -PID` (here and in teardown()) then
# kills the whole group, not just the subshell, so a reparented
# `kubectl get pod -w | while read` pipeline cannot survive it.
set -m
snapshotLoop &
SNAPSHOT_PID=$!
set +m
echo "busy-pod snapshot loop started (pid=$SNAPSHOT_PID, own process group)"

# ---------------------------------------------------------------------------
# STEP 11: wait for the short job to complete, then wait for the genuine
# KEDA/HPA 2->1 downscale (values-loadproof.yaml's
# scaleDownStabilizationSeconds:15 makes it fast and deterministic instead
# of the k8s 300s HPA default).
# ---------------------------------------------------------------------------
log "STEP 11: wait short job done, wait for genuine 2->1 downscale"

echo "waiting for short job $SHORT_JOB_ID to reach a terminal status..."
short_status=""
for i in $(seq 1 200); do
	code=$(curl -s -o /tmp/keda-av-downscale-short-job.json -w '%{http_code}' -H "Authorization: ApiKey $CLIENT_KEY" "$API_BASE/v1/jobs/$SHORT_JOB_ID")
	short_status=$(grep -o '"status":"[^"]*"' /tmp/keda-av-downscale-short-job.json | head -1 | cut -d'"' -f4 || true)
	if [ "$short_status" = "done" ] || [ "$short_status" = "failed" ]; then
		break
	fi
	sleep 3
done
assert_eq "done" "$short_status" "SC4: short job $SHORT_JOB_ID reaches done"

AV_DOWNSCALE_WINDOW=120
AV_REPLICAS_AFTER_DOWNSCALE=$(waitForReplicasAtMost av-worker 1 "$AV_DOWNSCALE_WINDOW") || {
	echo "FAIL: SC4 -- av-worker never downscaled 2->1 within ${AV_DOWNSCALE_WINDOW}s of the short job completing (observed: $AV_REPLICAS_AFTER_DOWNSCALE)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC4 -- av-worker downscaled 2->${AV_REPLICAS_AFTER_DOWNSCALE} after short job completion"

# NOTE: the pod-status watcher stays RUNNING here. The downscale only
# DELIVERS SIGTERM to the busy pod -- the pod keeps running (Terminating)
# until the in-flight long job completes (~hundreds of seconds later at
# hevc@1080's measured RTF), and only THEN does its terminal state appear.

# ---------------------------------------------------------------------------
# STEP 12: TRIPLE-CHECK -- the long job survived the downscale gracefully.
# ---------------------------------------------------------------------------
log "STEP 12: triple-check -- long job survived the downscale gracefully (no exit-137/SIGKILL)"

# (1) long job reaches done.
echo "waiting for long job $LONG_JOB_ID to reach a terminal status (bounded, well under AV_ENGINE_TIMEOUT=753s)..."
long_final_status=""
for i in $(seq 1 200); do
	code=$(curl -s -o /tmp/keda-av-downscale-long-job-final.json -w '%{http_code}' -H "Authorization: ApiKey $CLIENT_KEY" "$API_BASE/v1/jobs/$LONG_JOB_ID")
	long_final_status=$(grep -o '"status":"[^"]*"' /tmp/keda-av-downscale-long-job-final.json | head -1 | cut -d'"' -f4 || true)
	if [ "$long_final_status" = "done" ] || [ "$long_final_status" = "failed" ]; then
		break
	fi
	sleep 5
done
assert_eq "done" "$long_final_status" "SC4(1): long job $LONG_JOB_ID reaches done despite the downscale"

# (2) exactly one queued->active transition (no false retry caused by a
# premature SIGKILL forcing asynq to redeliver the task).
QUEUED_TO_ACTIVE_COUNT=$(psql "postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db" -tAc \
	"SELECT count(*) FROM job_events WHERE job_id='${LONG_JOB_ID}' AND from_status='queued' AND to_status='active';" | tr -d '[:space:]')
assert_eq "1" "$QUEUED_TO_ACTIVE_COUNT" "SC4(2): exactly one queued->active transition for long job $LONG_JOB_ID (no false retry)"

# (3) SIGTERM timestamp from the kubelet's Killing event (NEVER the pod's
# own deletion-deadline field, which is a scheduling deadline, not the
# moment SIGTERM was actually sent), pod exit timestamp/exit-code from the
# continuously-captured snapshot (or a final live read if the pod object
# still exists), and jobs.finished_at via psql -- prove SIGTERM occurred
# BEFORE completion AND the container exited gracefully (never
# exit-137/SIGKILL).
SIGTERM_TS=$(kubectl get events -n "$NAMESPACE" \
	--field-selector involvedObject.name="$BUSY_POD",reason=Killing \
	-o jsonpath='{.items[0].firstTimestamp}' 2>/dev/null || true)
assert_nonempty "$SIGTERM_TS" "SC4(3): kubelet Killing event SIGTERM timestamp captured for $BUSY_POD"

echo "waiting for the pod-status watcher to capture $BUSY_POD's terminal state (bounded 90s)..."
waited=0
while [ "$waited" -lt 90 ]; do
	if grep 'term_finished=' "$SC4_FILE" 2>/dev/null | grep -qv 'term_finished=$'; then
		break
	fi
	if ! kubectl get pod "$BUSY_POD" -n "$NAMESPACE" >/dev/null 2>&1; then
		sleep 3
		break
	fi
	sleep 3
	waited=$((waited + 3))
done
echo "termination capture: watcher lines=$(grep -c 'read_ts=' "$SC4_FILE" 2>/dev/null || echo 0)"
kill -- -"$SNAPSHOT_PID" >/dev/null 2>&1 || true
wait "$SNAPSHOT_PID" 2>/dev/null || true
[ -n "$BUSY_POD" ] && pkill -f "kubectl get pod ${BUSY_POD} .* -w" >/dev/null 2>&1 || true
SNAPSHOT_PID=""

POD_TERM_REASON=$(kubectl get pod "$BUSY_POD" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].state.terminated.reason}' 2>/dev/null || true)
POD_TERM_EXIT=$(kubectl get pod "$BUSY_POD" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}' 2>/dev/null || true)
POD_TERM_FINISHED=$(kubectl get pod "$BUSY_POD" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].state.terminated.finishedAt}' 2>/dev/null || true)
if [ -z "$POD_TERM_FINISHED" ]; then
	POD_TERM_REASON=$(kubectl get pod "$BUSY_POD" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].lastState.terminated.reason}' 2>/dev/null || true)
	POD_TERM_EXIT=$(kubectl get pod "$BUSY_POD" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].lastState.terminated.exitCode}' 2>/dev/null || true)
	POD_TERM_FINISHED=$(kubectl get pod "$BUSY_POD" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].lastState.terminated.finishedAt}' 2>/dev/null || true)
fi
if [ -z "$POD_TERM_FINISHED" ]; then
	# Pod object already removed -- fall back to the watch-captured snapshot
	# file, taking the last non-empty term_finished line written by
	# snapshotLoop.
	POD_TERM_FINISHED=$(grep 'term_finished=' "$SC4_FILE" | grep -v 'term_finished=$' | tail -1 | sed -n 's/.*term_finished=\([^ ]*\).*/\1/p' || true)
	POD_TERM_REASON=$(grep 'term_finished=' "$SC4_FILE" | grep -v 'term_finished=$' | tail -1 | sed -n 's/.*term_reason=\([^ ]*\).*/\1/p' || true)
	POD_TERM_EXIT=$(grep 'term_finished=' "$SC4_FILE" | grep -v 'term_finished=$' | tail -1 | sed -n 's/.*term_exit=\([^ ]*\).*/\1/p' || true)
fi
assert_nonempty "$POD_TERM_FINISHED" "SC4(3): pod $BUSY_POD terminated.finishedAt captured (live or via continuous snapshot)"

JOB_FINISHED_AT=$(psql "postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db" -tAc \
	"SELECT finished_at FROM jobs WHERE id='${LONG_JOB_ID}';" | tr -d '[:space:]')
assert_nonempty "$JOB_FINISHED_AT" "SC4(3): jobs.finished_at captured for long job $LONG_JOB_ID"

{
	echo ""
	echo "# SC4 triple-check raw evidence"
	echo "sigterm_killing_event_ts=$SIGTERM_TS"
	echo "pod_terminated_reason=$POD_TERM_REASON"
	echo "pod_terminated_exit_code=$POD_TERM_EXIT"
	echo "pod_terminated_finished_at=$POD_TERM_FINISHED"
	echo "job_finished_at=$JOB_FINISHED_AT"
	echo "queued_to_active_count=$QUEUED_TO_ACTIVE_COUNT"
} >>"$SC4_FILE"

# ASSERT: SIGTERM occurred before job completion, and the container exited
# gracefully (Completed/exit 0), NEVER exit-137/SIGKILL/OOMKilled -- i.e.
# the process finished the in-flight transcode and exited cleanly inside
# its own shutdown window, well before terminationGracePeriodSeconds=783.
SIGTERM_EPOCH=$(date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$SIGTERM_TS" +%s 2>/dev/null || date -u -d "$SIGTERM_TS" +%s 2>/dev/null || echo "0")
JOB_FINISHED_EPOCH=$(date -u -j -f "%Y-%m-%d %H:%M:%S" "${JOB_FINISHED_AT%%.*}" +%s 2>/dev/null || date -u -d "$JOB_FINISHED_AT" +%s 2>/dev/null || echo "0")
if [ "${SIGTERM_EPOCH:-0}" -le 0 ] || [ "${JOB_FINISHED_EPOCH:-0}" -le 0 ]; then
	echo "FAIL: SC4(3) -- could not parse SIGTERM_TS ($SIGTERM_TS) or JOB_FINISHED_AT ($JOB_FINISHED_AT) into comparable epochs" >&2
	exit 1
fi
if [ "$SIGTERM_EPOCH" -ge "$JOB_FINISHED_EPOCH" ]; then
	echo "FAIL: SC4(3) -- SIGTERM ($SIGTERM_TS) did not occur before job completion ($JOB_FINISHED_AT); the downscale-survives-in-flight-job proof does not hold" >&2
	exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC4(3) -- SIGTERM ($SIGTERM_TS) occurred BEFORE job completion ($JOB_FINISHED_AT)"

if [ "$POD_TERM_EXIT" = "137" ]; then
	echo "FAIL: SC4(3) -- pod $BUSY_POD terminated with exit 137 (SIGKILL) -- the downscale did NOT survive gracefully; terminationGracePeriodSeconds=783 was not enough (or was not honored)" >&2
	exit 1
fi
if [ "$POD_TERM_REASON" != "Completed" ] && [ "$POD_TERM_EXIT" != "0" ]; then
	echo "FAIL: SC4(3) -- pod $BUSY_POD did not terminate gracefully (reason=$POD_TERM_REASON exit=$POD_TERM_EXIT); expected Completed/exit 0, not exit 137/SIGKILL/OOMKilled" >&2
	exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: SC4(3) -- pod $BUSY_POD terminated gracefully (reason=$POD_TERM_REASON exit=$POD_TERM_EXIT, NOT 137/SIGKILL), well before terminationGracePeriodSeconds=783"

echo "PASS: SC4 evidence -- $SC4_FILE"

rm -rf "$WORKDIR"

# =============================================================================
# ALL-PASSED summary -- set only after every assertion above has passed.
# Teardown runs unconditionally via the EXIT trap.
# =============================================================================
GATE_OK="1"
echo ""
echo "=== ALL $PASS_COUNT ASSERTIONS PASSED ==="
echo "SC4 (AVE-05, D-07 proof #2): long av transcode survived a genuine KEDA/HPA 2->1 downscale via deterministic pod-deletion-cost victim selection -- exactly one queued->active transition, graceful SIGTERM-before-completion exit (never exit-137/SIGKILL), validating terminationGracePeriodSeconds=783 live -- PASS"
echo "Evidence: $SC4_FILE, $LOG_FILE"
