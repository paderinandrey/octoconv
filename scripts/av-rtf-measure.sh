#!/usr/bin/env bash
# av-rtf-measure.sh -- Phase 36 (AVE-04) RTF go/no-go measurement gate.
#
# Structural clone of scripts/audio-rtf-measure.sh (Phase 32 precedent),
# adapted for the av (video/ffmpeg) engine:
#   1. Metric is RTF (Real-Time Factor = wall_clock_seconds /
#      fixture_duration_seconds), same convention as the audio script --
#      AV_ENGINE_TIMEOUT is DERIVED FROM this measurement, not asserted
#      against a fixed budget.
#   2. UNLIKE audio's roughly content-invariant RTF (whisper transcription
#      cost barely depends on what's being said), video transcode RTF is
#      sensitive to BOTH codec AND resolution -- this script sweeps a
#      MATRIX (codec x resolution), not a single fixture (D-04).
#   3. Fixtures are synthesized ENTIRELY in-container via ffmpeg lavfi
#      (testsrc + sine), one per resolution cell -- never a large committed
#      binary (unlike audio's looped jfk.wav). Each fixture's ACTUAL
#      duration is re-measured via ffprobe, never assumed from the request.
#   4. The FULL production argv shapes are timed -- transcodeToMP4Args'
#      libx264/libx265 invocation for the h264/hevc cells, and
#      transcodeToWebMArgs' libvpx-vp9 invocation for the vp9 cell
#      (internal/convert/av.go) -- all carrying the same -nostdin
#      -protocol_whitelist file,crypto -i file:... hardening prefix every
#      production ffmpeg invocation in this codebase uses (AVE-02).
#   5. D-09: the VP9/webm cell is measured FIRST, not assumed secondary to
#      HEVC -- prior project data (35-RESEARCH.md) already showed VP9's
#      default (untuned) encode mode running slower than real time.
#   6. A PASSTHROUGH cross-check cell (no "-vf scale") is included per
#      codec, at a large-but-plausible resolution (default 2160p) -- this
#      is the legal client path where resolution_height==0 and the output
#      resolution equals the SOURCE's, bounded only by the 4320 decode-bomb
#      ceiling, NOT the {480,720,1080} AVOpts enum (36-REVIEW blocker,
#      RESEARCH.md Open Q3).
#   7. Peak memory (cgroup v2 memory.peak) is a companion observation, same
#      as audio-rtf-measure.sh -- it feeds the AV_WORKER_CONCURRENCY
#      decision, not a hard gate here.
#
# This script's exit code gates MEASUREMENT INTEGRITY ONLY (build/container/
# fixture/pipeline must all succeed) -- there is no pre-existing RTF number
# to compare against here either; the GO/NO-GO decision on the DERIVED
# AV_ENGINE_TIMEOUT / AV_MAX_DURATION_SECONDS is made separately (Plan 04),
# reading the per-cell p95 RTF this script prints. No RTF value, however
# high, causes this script itself to exit non-zero.
#
# No cross-arch --platform pin: ffmpeg's runtime CPU-capability detection is
# enabled by DEFAULT (36-RESEARCH.md Pattern 3, verified by reading n8.1.2's
# configure script) -- portable across arm64/amd64 build/run hosts with the
# plain default configure invocation Dockerfile.av-worker already uses.
set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE_TAG="${AV_RTF_IMAGE_TAG:-octoconv-av-worker:rtf-measure}"
RUNS="${AV_RTF_MEASURE_RUNS:-10}"
CPUS="${AV_RTF_CPUS:-2.0}"
MEMORY="${AV_RTF_MEMORY:-1g}"
# Per-fixture synthetic duration -- long enough that ffmpeg process-spawn /
# encoder-init overhead no longer dominates the RTF signal (same rationale
# as audio-rtf-measure.sh's FIXTURE_DURATION_S caveat), short enough that a
# 3-codec x 3-resolution x N-run matrix (plus the passthrough cells)
# completes in a reasonable operator-supervised session.
FIXTURE_DURATION_S="${AV_RTF_FIXTURE_DURATION_S:-30}"
# Matrix axes (D-04): codec x resolution. VP9 listed FIRST -- D-09, do not
# reorder to put HEVC first; prior project data (35-RESEARCH.md) already
# flags VP9/webm as the likely worst-case cell, not HEVC.
CODECS="${AV_RTF_CODECS:-vp9 hevc h264}"
RESOLUTIONS="${AV_RTF_RESOLUTIONS:-480 720 1080}"
# Passthrough cross-check height (36-REVIEW blocker / RESEARCH.md Open Q3):
# the legal resolution_height==0 client path is bounded by the 4320
# decode-bomb ceiling, NOT the {480,720,1080} enum above -- 2160 (4K) is a
# large-but-plausible input; true 8K (4320) synth is impractically slow to
# measure and 4K is the defensible large cross-check per RESEARCH.md.
PASSTHROUGH_HEIGHT="${AV_RTF_PASSTHROUGH_HEIGHT:-2160}"
CONTAINER="octoconv-av-rtf-measure-$$"
# Host-side scratch dir for logs captured from docker exec redirections --
# everything written here dies with the EXIT trap below, never leaking into
# host /tmp (mirrors audio-rtf-measure.sh's WR-04 discipline).
WORKDIR=$(mktemp -d)
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; rm -rf "$WORKDIR"' EXIT

echo "=== 0. OrbStack discipline: confirm k8s is down before docker build/run work (compose/k8s mutual exclusion, D-05) ==="
if command -v kubectl >/dev/null 2>&1 && kubectl cluster-info >/dev/null 2>&1; then
	echo "FAIL: kubectl reports a live cluster -- stop k8s before running this measurement" >&2
	exit 1
fi
echo "k8s confirmed down (or kubectl unavailable) -- proceeding"

echo
echo "=== 1. Build av-worker image (no platform pin; matches Dockerfile.av-worker exactly, never stale vs source) ==="
docker build -f Dockerfile.av-worker -t "$IMAGE_TAG" .

echo
echo "=== 2. Start a long-lived measurement container (--cpus=$CPUS --memory=$MEMORY, matches the eventual compose av-worker block) ==="
docker run -d --name "$CONTAINER" \
	--cpus="$CPUS" --memory="$MEMORY" \
	--entrypoint sleep "$IMAGE_TAG" infinity >/dev/null

echo
echo "=== 3. Read cgroup v2 cpu.max -> derive ffmpeg's -threads count (floor of quota/period, min 1; matches av.go's avThreadCount()/CgroupCPULimit()) ==="
CPU_MAX=$(docker exec "$CONTAINER" cat /sys/fs/cgroup/cpu.max)
echo "cpu.max: $CPU_MAX"
THREADS=$(echo "$CPU_MAX" | awk '{ if ($1 == "max") { print 1 } else { n = int($1/$2); print (n < 1) ? 1 : n } }')
echo "Derived -threads: $THREADS"

echo
echo "=== 4. Generate deterministic synthetic fixtures INSIDE the container via ffmpeg lavfi (testsrc + sine), one per resolution cell PLUS the passthrough height -- never a committed binary ==="
docker exec "$CONTAINER" mkdir -p /tmp/work

# Approximate 16:9 width for a given height, rounded to an even number
# (required by most encoders' chroma subsampling) -- exact standard
# resolution naming (854x480 vs computed 852x480) is not load-bearing for a
# synthetic lavfi fixture; only realistic pixel-count matters for RTF.
width_for_height() {
	awk -v h="$1" 'BEGIN { w = int((h * 16 / 9) / 2) * 2; print w }'
}

ALL_HEIGHTS="$RESOLUTIONS $PASSTHROUGH_HEIGHT"
declare -A FIXTURE_DURATION_ACTUAL
for H in $ALL_HEIGHTS; do
	W=$(width_for_height "$H")
	FIXTURE="/tmp/work/fixture_${H}p.mp4"
	echo "--- Synthesizing ${W}x${H} fixture (requested ${FIXTURE_DURATION_S}s) ---"
	docker exec "$CONTAINER" ffmpeg -y -nostdin \
		-f lavfi -i "testsrc=duration=${FIXTURE_DURATION_S}:size=${W}x${H}:rate=30" \
		-f lavfi -i "sine=frequency=440:duration=${FIXTURE_DURATION_S}" \
		-c:v libx264 -preset ultrafast -c:a pcm_s16le \
		"$FIXTURE" >/dev/null 2>&1
	ACTUAL=$(docker exec "$CONTAINER" ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 "$FIXTURE")
	FIXTURE_DURATION_ACTUAL[$H]="$ACTUAL"
	echo "Fixture ${H}p generated: requested=${FIXTURE_DURATION_S}s ffprobe-measured=${ACTUAL}s"
done

echo
echo "=== 5. Timed loop INSIDE the container: $RUNS runs per matrix cell, FULL production argv (transcodeToMP4Args/transcodeToWebMArgs shape, av.go) ==="
echo "Matrix: codecs=[$CODECS] resolutions=[$RESOLUTIONS] plus passthrough(no -vf scale)@${PASSTHROUGH_HEIGHT}p for every codec"

# codec_args CODEC -> prints "EXT VCODEC EXTRA_ARGS..." (space-joined, one
# line) describing the production argv tail for that codec. mp4 targets
# (h264/hevc) mirror transcodeToMP4Args; the vp9/webm target mirrors
# transcodeToWebMArgs -- both from internal/convert/av.go, including the
# EXACT x264DefaultCRF=23 / x265DefaultCRF=28 constants (avopts.go).
codec_ext() {
	case "$1" in
	h264 | hevc) echo "mp4" ;;
	vp9) echo "webm" ;;
	*)
		echo "FAIL: unknown codec '$1' in AV_RTF_CODECS" >&2
		exit 1
		;;
	esac
}

run_cell() {
	codec="$1"
	height="$2"
	cell_label="$3"
	fixture="/tmp/work/fixture_${height}p.mp4"
	ext=$(codec_ext "$codec")
	dur="${FIXTURE_DURATION_ACTUAL[$height]}"

	case "$codec" in
	h264)
		ENC_ARGS="-c:v libx264 -preset veryfast -crf 23 -c:a aac -b:a 128k -movflags +faststart"
		;;
	hevc)
		ENC_ARGS="-c:v libx265 -preset veryfast -crf 28 -c:a aac -b:a 128k -movflags +faststart"
		;;
	vp9)
		ENC_ARGS="-c:v libvpx-vp9 -b:v 1M -c:a libopus"
		;;
	esac

	echo
	echo "--- Cell: cell=${cell_label} (codec=$codec, source=${height}p, ext=$ext) ---"
	RAW_MILLIS=$(docker exec "$CONTAINER" sh -c "
    set -e
    for i in \$(seq 1 $RUNS); do
      start=\$(date +%s%N)
      ffmpeg -y -nostdin -protocol_whitelist file,crypto -i file:${fixture} \
        -map 0:0 -map 0:a:0? \
        ${ENC_ARGS} \
        -threads $THREADS \
        file:/tmp/work/out_${cell_label}_\${i}.${ext} >/tmp/work/ffmpeg_${cell_label}_\${i}.log 2>&1
      end=\$(date +%s%N)
      echo \$(( (end - start) / 1000000 ))
    done
  ")

	echo "Per-run wall-clock (ms), execution order:"
	echo "$RAW_MILLIS"

	RAW_RTF=$(echo "$RAW_MILLIS" | awk -v dur="$dur" '{ printf "%.6f\n", ($1/1000)/dur }')
	SORTED=$(echo "$RAW_RTF" | sort -n)
	N=$(echo "$SORTED" | wc -l | tr -d ' ')
	RANK=$(awk -v n="$N" 'BEGIN { r = n * 0.95; rr = (r == int(r)) ? r : int(r) + 1; print rr }')
	P95_RTF=$(echo "$SORTED" | sed -n "${RANK}p")

	echo "Sorted ascending RTF: $SORTED"
	echo "N=$N, nearest-rank p95 rank=$RANK, p95 RTF=${P95_RTF}"
	echo "RESULT cell=${cell_label} N=$N p95_RTF=${P95_RTF} threads=${THREADS} cpus=${CPUS} memory=${MEMORY} fixture_duration_s=${dur} source_height=${height}"
}

for CODEC in $CODECS; do
	for H in $RESOLUTIONS; do
		run_cell "$CODEC" "$H" "${CODEC}@${H}"
	done
done

echo
echo "=== 6. Passthrough cross-check cells (no -vf scale; legal resolution_height==0 client path, RESEARCH.md Open Q3) ==="
for CODEC in $CODECS; do
	run_cell "$CODEC" "$PASSTHROUGH_HEIGHT" "${CODEC}@passthrough"
done

echo
echo "=== 7. Peak memory observation (cgroup v2 memory.peak, inside the ${MEMORY} limit; feeds AV_WORKER_CONCURRENCY sizing, not a hard gate) ==="
PEAK_BYTES=$(docker exec "$CONTAINER" cat /sys/fs/cgroup/memory.peak 2>/dev/null || echo "unavailable")
if [ "$PEAK_BYTES" != "unavailable" ]; then
	PEAK_MB=$(awk -v b="$PEAK_BYTES" 'BEGIN { printf "%.1f", b / 1024 / 1024 }')
	echo "Peak memory (ffmpeg single-job RSS, across all cells run so far in this container): ${PEAK_BYTES} bytes (~${PEAK_MB} MiB) of ${MEMORY} limit"
else
	echo "Peak memory: cgroup v2 memory.peak not available in this environment"
fi

echo
echo "=== 8. Image size + measurement-host architecture (Phase-23/32-style arch caveat) ==="
IMAGE_SIZE=$(docker image inspect "$IMAGE_TAG" --format '{{.Size}}')
IMAGE_SIZE_MB=$(awk -v b="$IMAGE_SIZE" 'BEGIN { printf "%.1f", b / 1024 / 1024 }')
HOST_ARCH=$(uname -m)
echo "Image size: ${IMAGE_SIZE} bytes (~${IMAGE_SIZE_MB} MiB)"
echo "Measurement host architecture: ${HOST_ARCH} -- production amd64 RTF is expected to differ from this number, same caveat as audio-rtf-measure.sh"

echo
echo "=== ALL GREEN: measurement integrity checks passed (build, container, cgroup read, fixture generation x$(echo "$ALL_HEIGHTS" | wc -w | tr -d ' '), timed matrix sweep, per-cell p95, memory, image size all captured) ==="
echo "This script's exit code reflects MEASUREMENT INTEGRITY ONLY -- no RESULT line's p95_RTF value, however high, causes a non-zero exit here. The GO/NO-GO on AV_ENGINE_TIMEOUT/AV_MAX_DURATION_SECONDS derived from these numbers is a separate, operator-reviewed decision (Plan 04)."
echo "peak_mem_bytes=${PEAK_BYTES} image_size_bytes=${IMAGE_SIZE} host_arch=${HOST_ARCH}"
