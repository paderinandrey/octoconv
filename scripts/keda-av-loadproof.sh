#!/usr/bin/env bash
# keda-av-loadproof.sh -- Phase 37 (AVE-05, D-07 proof #1) av scale-from-zero
# live-cluster proof gate.
#
# Structural clone of scripts/keda-audio-loadproof.sh -- same
# self-containment / assertion-helper / teardown shapes -- adapted for the
# av (video/ffmpeg) engine class with:
#
#   NARROWER SCOPE: only one run mode (the full live gate), NO separate
#   calibration/trial mode. Unlike scripts/keda-load-proof.sh's optional
#   env-var-gated single-job trial run, there is nothing to pre-measure
#   here -- AV_ENGINE_TIMEOUT=753s already comes from Phase 36's RTF
#   measurement gate (scripts/av-rtf-measure.sh); a preliminary timing pass
#   would be redundant.
#
#   AV FIXTURE DIVERGENCE: av has NO committed video fixture (unlike
#   audio's internal/e2e/testdata/jfk.wav). This gate synthesizes a SHORT
#   video fixture entirely in-container via ffmpeg lavfi (testsrc + sine),
#   mirroring scripts/av-rtf-measure.sh's in-container fixture-synthesis
#   pattern (lines 105-124): a throwaway container built FROM the already
#   locally-built octoconv-av-worker:dev image runs ffmpeg, and the
#   resulting file is `docker cp`'d out to the host so it can be POSTed
#   through the API's multipart upload, exactly the way a real client
#   would submit it.
#
#   NEW SCOPE: a Phase-28-style timestamped kubectl pod-event-timeline
#   capture (Scheduled -> Pulling -> Pulled -> Created -> Started),
#   separating image-pull time from orchestration time for the av-worker
#   image (ffmpeg source-built, Dockerfile.av-worker).
#
#   CAVEAT (mirrors keda-audio-loadproof.sh's Open Question 2 resolution --
#   record verbatim in the evidence file): image-pull is expected to be ~= 0
#   on OrbStack's shared Docker store, since the av-worker image is already
#   present locally (imagePullPolicy IfNotPresent, a pre-built :dev/:local
#   tag) before this gate runs. A genuine registry-backed cold pull of the
#   av-worker image is unmeasurable locally and is deferred to a
#   real-registry environment. If Pulling->Pulled below is near-zero, THAT
#   is the measured evidence answering this environment's cold-pull
#   question: bake-in imposes ~0 extra pull cost on OrbStack's shared
#   store.
#
# scripts/keda-load-proof.sh, scripts/keda-gate.sh, and
# scripts/keda-audio-loadproof.sh are left byte-unchanged by this phase --
# av logic lives exclusively in this file and its sibling,
# scripts/keda-av-downscale-survival.sh. Neither frozen script is touched.
#
# This gate is SELF-CONTAINED: it installs KEDA itself, layers
# `-f values-local.yaml` on top of the chart, and tears everything down via
# an EXIT trap -- success or failure, OrbStack is never left hot. It
# refuses to run if the docker-compose stack is up (compose and k8s stacks
# must never be hot simultaneously -- four confirmed OrbStack daemon
# wedges on record).
#
# T-37-06 (DoS via orphaned watcher) / T-37-07 (DoS via stuck fresh-install
# fallback) / T-37-08 (tampering with the frozen gate scripts) from this
# plan's threat register are mitigated below: the EXIT trap always runs and
# kills the pod-status watcher AND the fixture-synthesis container by
# process-group + belt-and-suspenders pkill (Phase-29 WR-01/WR-02/WR-03
# pattern); the asynq queue registry is seeded (including av) before
# expecting a genuine zero; and neither frozen script is touched by this
# file.
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

# The av-worker image is NOT built by this script -- it is an operator
# precondition (same as keda-audio-loadproof.sh's ~650MB audio-worker image
# caveat): `docker compose build av-worker` (or an equivalent `docker
# build -f Dockerfile.av-worker`) must already have produced this exact
# tag, matching values.yaml's avWorker.image.repository + global.imageTag.
AV_IMAGE_TAG="octoconv-av-worker:dev"

# api/db reachability for job submission -- port-forwarded locally by this
# script (same sanctioned mechanism as scripts/keda-gate.sh,
# scripts/keda-load-proof.sh, scripts/keda-audio-loadproof.sh). Distinct
# local ports from all three sibling scripts so this gate can never
# collide with a concurrently-running one.
API_LOCAL_PORT="18093"
API_BASE="http://127.0.0.1:${API_LOCAL_PORT}"
DB_LOCAL_PORT="15437"

PASS_COUNT=0
GATE_OK=""
API_PF_PID=""
DB_PF_PID=""
SNAPSHOT_PID=""
FIXTURE_CONTAINER=""
# AV_POD is set once the trigger job's pod is discovered; kept declared here
# (empty) so teardown() can unconditionally reference it regardless of how
# far the run got before failing.
AV_POD=""

# Evidence artifacts (CSV/PNG/log/txt naming convention) are committed
# alongside the SUMMARY, mirroring .planning/milestones/v1.6-phases/
# 28-autoscale-load-proof/evidence/.
EVIDENCE_DIR=".planning/phases/37-keda-helm-chart-integration/evidence"
RUN_TS=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p "$EVIDENCE_DIR"
LOG_FILE="$EVIDENCE_DIR/gate-transcript-${RUN_TS}.log"

# Tee the whole run to the timestamped transcript -- every PASS/FAIL line,
# every observed value, becomes part of the committed evidence.
exec > >(tee "$LOG_FILE") 2>&1

# ---------------------------------------------------------------------------
# Assertion helpers -- copied verbatim from scripts/keda-load-proof.sh /
# scripts/keda-gate.sh / scripts/keda-audio-loadproof.sh.
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

# assert_nonempty_redacted -- same non-empty check as assert_nonempty, but
# NEVER echoes the raw value into the (committed) gate transcript: this
# whole run is teed to $EVIDENCE_DIR/gate-transcript-*.log, so secrets like
# $CLIENT_KEY must never be printed verbatim.
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
		# WR-04/29-REVIEW WR-01: kill the whole process group (own group via
		# parent `set -m` at the launch site below) so a reparented
		# `kubectl -w` pipeline cannot survive this EXIT trap.
		kill -- -"$SNAPSHOT_PID" >/dev/null 2>&1 || true
		wait "$SNAPSHOT_PID" 2>/dev/null || true
		# Belt-and-suspenders (macOS process-group semantics are unreliable):
		# deterministically reap any orphaned watch by its exact command shape.
		[ -n "$AV_POD" ] && pkill -f "kubectl get pod ${AV_POD} .* -w" >/dev/null 2>&1 || true
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
		echo "PASS -- Phase 37 av scale-from-zero load-proof gate run complete ($PASS_COUNT checks). Transcript: $LOG_FILE"
	else
		echo "FAIL -- Phase 37 av scale-from-zero load-proof gate did not complete (exit=$exit_code, checks passed=$PASS_COUNT)." >&2
	fi
	exit "$exit_code"
}
trap teardown EXIT

echo "=== Phase 37 av scale-from-zero live-proof gate (AVE-05, D-07 proof #1) ==="
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
# STEP 4: Install octoconv (keda.enabled=true prometheus.enabled=true via
# values-local.yaml), WITHOUT --wait (Phase 24 decision: createbucket
# post-install hook <-> app-readiness chicken-egg). Then kubectl wait per
# always-on Deployment only -- av-worker (KEDA/HPA-owned, may already be
# settling toward 0) is intentionally excluded from this wait.
# ---------------------------------------------------------------------------
log "STEP 4: helm install octoconv -f values-local.yaml"

helm install octoconv "$CHART_DIR" -f "$VALUES_LOCAL" -n "$NAMESPACE" --create-namespace
echo "PASS: helm install octoconv complete (async install; readiness gated below)"

log "waiting for always-on / min-1 Deployments to become Available"
for d in postgres redis minio api prometheus webhook-worker; do
	kubectl wait --for=condition=Available "deployment/$d" -n "$NAMESPACE" --timeout=240s
	echo "PASS: deployment/$d Available"
done

# ---------------------------------------------------------------------------
# STEP 4b (Pitfall 7): seed asynq's queue registry (Redis "asynq:queues"
# SET) for all six queues via a direct redis-cli exec into the redis pod.
# WR-01 (Phase 29): ignoreNullValues=false means a queue that has NEVER had
# a real task (asynq only adds a queue name to "asynq:queues" on its FIRST
# real enqueue) reports an ABSENT PromQL result, not a genuine zero -- KEDA
# then holds fallback.replicas:1 INDEFINITELY on a truly fresh install
# rather than ever settling to 0. Seeding the registry directly (zero
# tasks created, no worker processing triggered) makes GetQueueInfo return
# real zero-valued counts, exactly mirroring what happens naturally the
# moment the first real av job is submitted in production.
# ---------------------------------------------------------------------------
log "STEP 4b: seed asynq queue registry (Redis), including av, so the absent-metric fallback resolves to a real zero"

REDIS_POD=$(kubectl get pod -n "$NAMESPACE" -l "app.kubernetes.io/component=redis" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
assert_nonempty "$REDIS_POD" "redis pod discovered for queue-registry seeding"
kubectl exec -n "$NAMESPACE" "$REDIS_POD" -- redis-cli SADD asynq:queues image document html audio av webhook >/dev/null
echo "PASS: asynq:queues seeded (image, document, html, audio, av, webhook) -- zero tasks created, no worker processing triggered"

# ---------------------------------------------------------------------------
# STEP 5: port-forward api+postgres, mint a client key. Reused for the
# trigger job submission and status polling below.
# ---------------------------------------------------------------------------
log "STEP 5: port-forward api+postgres, mint client key"

kubectl port-forward -n "$NAMESPACE" svc/api "${API_LOCAL_PORT}:8090" >/tmp/keda-av-loadproof-api-pf.log 2>&1 &
API_PF_PID=$!
kubectl port-forward -n "$NAMESPACE" svc/postgres "${DB_LOCAL_PORT}:5432" >/tmp/keda-av-loadproof-db-pf.log 2>&1 &
DB_PF_PID=$!
sleep 3

echo "waiting for port-forwarded /healthz..."
healthy=""
for i in $(seq 1 30); do
	code=$(curl -s -o /tmp/keda-av-loadproof-healthz.json -w '%{http_code}' "$API_BASE/healthz" || true)
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
echo "PASS: api reachable via port-forward, /healthz 200 ($(cat /tmp/keda-av-loadproof-healthz.json))"

# Dev-only, throwaway credential pattern (same as keda-gate.sh /
# keda-load-proof.sh / keda-audio-loadproof.sh) -- never a production
# secret, never echoed into $EVIDENCE_DIR files.
export DATABASE_URL="postgres://octo:octo-pass@127.0.0.1:${DB_LOCAL_PORT}/octo_db"
export API_KEY_SALT="dev-only-change-me-in-real-deploys"

SUFFIX=$(date +%s)
CLIENT_OUT=$(go run ./cmd/manage-clients create "keda-av-loadproof-${SUFFIX}")
CLIENT_KEY=$(printf '%s\n' "$CLIENT_OUT" | awk -F': ' '/^api key/{print $2}')
assert_nonempty_redacted "$CLIENT_KEY" "minted gate client + API key"

# postJobPath submits an ABSOLUTE-path fixture (unlike the testdata-relative
# postJob shape in keda-audio-loadproof.sh -- av fixtures are synthesized at
# gate-run time into $WORKDIR, never committed under internal/e2e/testdata).
postJobPath() {
	local path="$1" target="$2" content_type="$3" opts="${4:-}"
	local filename
	filename=$(basename "$path")
	local out_file="/tmp/keda-av-loadproof-post-${filename}.json"
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

# waitForReplicasAtLeast / waitForReplicasAtMost -- bounded polls, copied
# verbatim from keda-gate.sh/keda-load-proof.sh/keda-audio-loadproof.sh
# (KEDA settles after cooldown, poll don't assert instantly).
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
# STEP 6: confirm av-worker is genuinely at 0 replicas BEFORE the trigger
# job is submitted -- the load-bearing precondition for a true
# scale-FROM-ZERO proof, not just an already-warm scale-up.
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
assert_eq "0" "$AV_REPLICAS_BEFORE" "av-worker Deployment status.replicas before any job (genuine zero)"

# ---------------------------------------------------------------------------
# STEP 7: synthesize a SHORT lavfi fixture entirely in-container (av has no
# committed video binary, unlike audio's jfk.wav). Mirrors
# scripts/av-rtf-measure.sh's testsrc+sine pattern (lines 105-124): a
# throwaway container built FROM the already-built octoconv-av-worker:dev
# image runs ffmpeg, and the output is `docker cp`'d to a host scratch dir
# so it can be POSTed through the API's multipart upload like a real
# client file.
#
# Container format mkv (a legal AVC-01 transcode-to-mp4 SOURCE, av.go's
# avTranscodeToMP4Sources), video libx264 + audio aac: this makes the
# resulting mkv->mp4 job eligible for the AVC-05 stream-copy remux fast
# path (h264+aac already legal in mp4, avStreamCopyLegal) -- deliberately
# fast so the full 0->1->0 scale cycle observed by this gate completes
# quickly. A genuine slow re-encode is exercised instead by the sibling
# scripts/keda-av-downscale-survival.sh gate (SC4), not needed here (SC3
# only needs a real job to land on the av queue and trigger scale-up).
# ---------------------------------------------------------------------------
log "STEP 7: synthesize short lavfi fixture, trigger av scale-from-zero, capture pod event timeline"

WORKDIR=$(mktemp -d)
FIXTURE_DURATION_S=5
FIXTURE_CONTAINER="octoconv-av-loadproof-fixture-$$"
docker run -d --name "$FIXTURE_CONTAINER" --entrypoint sleep "$AV_IMAGE_TAG" infinity >/dev/null
docker exec "$FIXTURE_CONTAINER" mkdir -p /tmp/work
docker exec "$FIXTURE_CONTAINER" ffmpeg -y -nostdin \
	-f lavfi -i "testsrc=duration=${FIXTURE_DURATION_S}:size=640x360:rate=30" \
	-f lavfi -i "sine=frequency=440:duration=${FIXTURE_DURATION_S}" \
	-c:v libx264 -preset ultrafast -c:a aac \
	/tmp/work/av-loadproof-fixture.mkv >/dev/null 2>&1
docker cp "$FIXTURE_CONTAINER:/tmp/work/av-loadproof-fixture.mkv" "$WORKDIR/av-loadproof-fixture.mkv"
docker rm -f "$FIXTURE_CONTAINER" >/dev/null 2>&1 || true
FIXTURE_CONTAINER=""
assert_nonempty "$(ls -la "$WORKDIR/av-loadproof-fixture.mkv" 2>/dev/null || true)" "short lavfi fixture synthesized (${FIXTURE_DURATION_S}s, 640x360, h264/aac in mkv)"

AV_JOB_ID=$(postJobPath "$WORKDIR/av-loadproof-fixture.mkv" "mp4" "video/x-matroska")
assert_nonempty "$AV_JOB_ID" "av trigger job submitted (av-loadproof-fixture.mkv -> mp4)"

AV_REPLICAS_AFTER=$(waitForReplicasAtLeast av-worker 1 180) || {
	echo "FAIL: av-worker never scaled 0->1 within 180s of submitting $AV_JOB_ID" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: av-worker scaled 0->${AV_REPLICAS_AFTER} after trigger job $AV_JOB_ID"

AV_POD=$(kubectl get pod -n "$NAMESPACE" -l "app.kubernetes.io/component=av-worker" \
	--field-selector=status.phase!=Failed \
	-o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
assert_nonempty "$AV_POD" "av-worker pod identified for event-timeline capture"

SC3_AV_FILE="$EVIDENCE_DIR/sc3-av-scale-from-zero-${RUN_TS}.txt"
{
	echo "# Phase 37 av scale-from-zero event-timeline evidence -- run $RUN_TS"
	echo "# av_job_id=$AV_JOB_ID av_pod=$AV_POD"
	echo "#"
	echo "# CAVEAT (mirrors keda-audio-loadproof.sh Open Question 2 resolution):"
	echo "# image-pull is expected to be ~= 0 on OrbStack's shared Docker store,"
	echo "# since the av-worker image is already present locally"
	echo "# (imagePullPolicy IfNotPresent, a pre-built :dev/:local tag) before"
	echo "# this gate runs. A genuine registry-backed cold pull of the"
	echo "# av-worker image is unmeasurable locally and is deferred to a"
	echo "# real-registry environment. If Pulling->Pulled below is near-zero,"
	echo "# THAT is the measured evidence answering this environment's"
	echo "# cold-pull question."
} >"$SC3_AV_FILE"

# Watch-based capture (not a sampling poll): kubectl get pod -w
# --output-watch-events streams every status update including transient
# pull/create/start transitions that a periodic poll can miss entirely.
snapshotLoop() {
	while true; do
		kubectl get pod "$AV_POD" -n "$NAMESPACE" -w --output-watch-events \
			-o jsonpath='{.type}{" phase="}{.object.status.phase}{" ready="}{.object.status.containerStatuses[0].ready}{"\n"}' 2>/dev/null \
			| while IFS= read -r watch_line; do
				echo "read_ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ") pod=${AV_POD} ${watch_line}" >>"$SC3_AV_FILE"
			done
		if ! kubectl get pod "$AV_POD" -n "$NAMESPACE" >/dev/null 2>&1; then
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
echo "pod snapshot loop started (pid=$SNAPSHOT_PID, own process group)"

echo "waiting for av-worker pod $AV_POD readiness (bounded 300s, well under AV_ENGINE_TIMEOUT=753s + cold-start margin)..."
ready=""
waited=0
while [ "$waited" -lt 300 ]; do
	ready=$(kubectl get pod "$AV_POD" -n "$NAMESPACE" -o jsonpath='{.status.containerStatuses[0].ready}' 2>/dev/null || true)
	if [ "$ready" = "true" ]; then
		break
	fi
	sleep 3
	waited=$((waited + 3))
done
echo "av-worker pod $AV_POD ready=${ready:-unknown} after ${waited}s"

# Stop the watcher (process-group kill, same discipline as teardown()) now
# that the pull/create/start lifecycle has had time to complete.
kill -- -"$SNAPSHOT_PID" >/dev/null 2>&1 || true
wait "$SNAPSHOT_PID" 2>/dev/null || true
[ -n "$AV_POD" ] && pkill -f "kubectl get pod ${AV_POD} .* -w" >/dev/null 2>&1 || true
SNAPSHOT_PID=""

# kubectl describe pod's Events section renders relative "age", not an
# absolute timestamp -- `kubectl get events` carries the real
# .firstTimestamp per event, matching Phase 28's SIGTERM_TS extraction
# pattern (never trust a relative-age rendering for a timestamped proof).
podEventTimestamp() {
	local pod="$1" reason="$2"
	kubectl get events -n "$NAMESPACE" \
		--field-selector involvedObject.name="$pod",reason="$reason" \
		-o jsonpath='{.items[0].firstTimestamp}' 2>/dev/null || true
}

{
	echo ""
	echo "# Full kubectl describe pod (human-readable narrative)"
	kubectl describe pod "$AV_POD" -n "$NAMESPACE" 2>&1
	echo ""
	echo "# Extracted event timestamps (real .firstTimestamp, not relative 'age')"
} >>"$SC3_AV_FILE"

for reason in Scheduled Pulling Pulled Created Started; do
	ts=$(podEventTimestamp "$AV_POD" "$reason")
	echo "event_${reason}_ts=${ts}" >>"$SC3_AV_FILE"
done

SCHEDULED_TS=$(podEventTimestamp "$AV_POD" "Scheduled")
PULLING_TS=$(podEventTimestamp "$AV_POD" "Pulling")
PULLED_TS=$(podEventTimestamp "$AV_POD" "Pulled")
CREATED_TS=$(podEventTimestamp "$AV_POD" "Created")
STARTED_TS=$(podEventTimestamp "$AV_POD" "Started")
assert_nonempty "$SCHEDULED_TS" "av pod Scheduled event timestamp captured"
assert_nonempty "$STARTED_TS" "av pod Started event timestamp captured"

# Portable epoch conversion (BSD `date -j -f` on macOS/OrbStack host, GNU
# `date -d` fallback) -- same pattern already proven in
# scripts/keda-load-proof.sh's D-09(3) SIGTERM-before-completion check.
toEpoch() {
	local ts="$1"
	if [ -z "$ts" ]; then
		echo "0"
		return
	fi
	date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$ts" +%s 2>/dev/null || date -u -d "$ts" +%s 2>/dev/null || echo "0"
}

SCHEDULED_EPOCH=$(toEpoch "$SCHEDULED_TS")
PULLING_EPOCH=$(toEpoch "$PULLING_TS")
PULLED_EPOCH=$(toEpoch "$PULLED_TS")
CREATED_EPOCH=$(toEpoch "$CREATED_TS")
STARTED_EPOCH=$(toEpoch "$STARTED_TS")

{
	echo ""
	echo "# Deltas (seconds) -- orchestration time vs image-pull time separation"
	if [ "$PULLING_EPOCH" -gt 0 ] && [ "$PULLED_EPOCH" -gt 0 ]; then
		echo "pull_duration_s=$((PULLED_EPOCH - PULLING_EPOCH))"
	else
		echo "pull_duration_s=unavailable (no Pulling/Pulled event -- image already present locally, imagePullPolicy IfNotPresent)"
	fi
	if [ "$SCHEDULED_EPOCH" -gt 0 ] && [ "$STARTED_EPOCH" -gt 0 ]; then
		echo "scheduled_to_started_s=$((STARTED_EPOCH - SCHEDULED_EPOCH))"
	fi
	if [ "$CREATED_EPOCH" -gt 0 ] && [ "$STARTED_EPOCH" -gt 0 ]; then
		echo "created_to_started_s=$((STARTED_EPOCH - CREATED_EPOCH))"
	fi
} >>"$SC3_AV_FILE"

PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: av scale-from-zero event-timeline captured -- $SC3_AV_FILE"

# ---------------------------------------------------------------------------
# STEP 8: confirm the trigger job itself completes end-to-end (not just
# orchestration timing) -- generous bound, well under AV_ENGINE_TIMEOUT=753s
# plus cold-start margin.
# ---------------------------------------------------------------------------
log "STEP 8: confirm trigger job $AV_JOB_ID reaches a terminal status"

av_job_status=""
for i in $(seq 1 200); do
	code=$(curl -s -o /tmp/keda-av-loadproof-job.json -w '%{http_code}' -H "Authorization: ApiKey $CLIENT_KEY" "$API_BASE/v1/jobs/$AV_JOB_ID" || true)
	av_job_status=$(grep -o '"status":"[^"]*"' /tmp/keda-av-loadproof-job.json | head -1 | cut -d'"' -f4 || true)
	if [ "$av_job_status" = "done" ] || [ "$av_job_status" = "failed" ]; then
		break
	fi
	sleep 5
done
assert_eq "done" "$av_job_status" "trigger job $AV_JOB_ID reaches done"

# ---------------------------------------------------------------------------
# STEP 9: confirm av-worker drains back to zero after the job completes --
# the "0->N->0" half of a genuine scale-from-zero proof.
# ---------------------------------------------------------------------------
log "STEP 9: confirm av-worker drains back to 0 replicas after the trigger job completes"

AV_REPLICAS_FINAL=$(waitForReplicasAtMost av-worker 0 240) || {
	echo "FAIL: av-worker never returned to 0 replicas within 240s after job completion (observed: $AV_REPLICAS_FINAL)" >&2
	exit 1
}
PASS_COUNT=$((PASS_COUNT + 1))
echo "PASS: av-worker full-cycled back to 0 replicas (observed: $AV_REPLICAS_FINAL)"

rm -rf "$WORKDIR"

# =============================================================================
# ALL-PASSED summary -- set only after every assertion above has passed.
# Teardown runs unconditionally via the EXIT trap.
# =============================================================================
GATE_OK="1"
echo ""
echo "=== ALL $PASS_COUNT ASSERTIONS PASSED ==="
echo "SC3-av (AVE-05, D-07 proof #1): av-worker scaled 0->1 from a genuine zero on an ffmpeg-lavfi-synthesized mkv->mp4 submission, drained back to 0; event-timeline (Scheduled->Pulling->Pulled->Created->Started) captured with real timestamps -- PASS"
echo "Evidence: $SC3_AV_FILE, $LOG_FILE"
