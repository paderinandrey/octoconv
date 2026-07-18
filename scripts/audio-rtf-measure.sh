#!/usr/bin/env bash
# audio-rtf-measure.sh -- Phase 32 (AUD-07) RTF go/no-go measurement gate.
#
# Structural clone of scripts/verapdf-measure.sh (Phase 23 precedent), adapted
# for the audio engine:
#   1. Metric is RTF (Real-Time Factor = wall_clock_seconds /
#      audio_duration_seconds), not raw wall-clock -- veraPDF's D-01 budget
#      was a fixed constant to assert against; AUDIO_ENGINE_TIMEOUT is
#      DERIVED FROM this measurement instead (see 32-03-SUMMARY.md Task 2).
#   2. A deterministic, multi-minute synthetic fixture is generated INSIDE
#      the container via ffmpeg by looping/trimming the already-committed
#      internal/convert/testdata/audio/jfk.wav (11s, real speech) to
#      FIXTURE_DURATION_S -- NOT committed as a large binary. A sine-tone
#      floor fixture is also generated as a sanity cross-check.
#   3. The FULL production two-stage pipeline is timed: ffmpeg normalize
#      (internal/convert/whisper.go's ffmpegNormalizeArgs) then whisper-cli
#      transcribe (whisperArgs, matching the "-l auto" default and -otxt
#      output flag), both against an explicit -t <cgroup-derived-n> read
#      live from the container's own /sys/fs/cgroup/cpu.max (matching Plan
#      02's cgroupCPULimit() floor, never assumed).
#   4. Peak memory (cgroup v2 memory.peak) is a companion observation, same
#      as verapdf-measure.sh -- it feeds the AUDIO_WORKER_CONCURRENCY
#      decision (32-03 Task 2), not a hard gate here.
#
# This script's exit code gates MEASUREMENT INTEGRITY ONLY (build/container/
# fixture/pipeline must all succeed) -- unlike veraPDF's fixed-budget assert,
# there is no pre-existing RTF number to compare against; the GO/NO-GO
# decision on the DERIVED AUDIO_ENGINE_TIMEOUT is made separately (32-03
# Task 2 / SUMMARY), reading the p95 RTF this script prints.
#
# No cross-arch --platform pin anywhere: whisper.cpp is source-built with
# -DGGML_NATIVE=OFF (32-01), portable across arm64/amd64 without one --
# unlike document-worker's hard verapdf/cli amd64-only constraint.
set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE_TAG="${AUDIO_RTF_IMAGE_TAG:-octoconv-audio-worker:rtf-measure}"
RUNS="${AUDIO_RTF_MEASURE_RUNS:-10}"
CPUS="${AUDIO_RTF_CPUS:-2.0}"
MEMORY="${AUDIO_RTF_MEMORY:-1g}"
# ~5-minute concatenated-speech fixture -- long enough that model-load /
# process-spawn overhead no longer dominates the RTF signal (RESEARCH.md
# "RTF Measurement Methodology" caveat on jfk.wav's raw 11s being too short).
FIXTURE_DURATION_S="${AUDIO_RTF_FIXTURE_DURATION_S:-300}"
CONTAINER="octoconv-audio-rtf-measure-$$"
# Host-side scratch dir for logs captured from docker exec redirections
# (currently the floor run's whisper output) -- everything written here dies
# with the EXIT trap below, never leaking into host /tmp (WR-04).
WORKDIR=$(mktemp -d)
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; rm -rf "$WORKDIR"' EXIT

FIXTURE_SRC="internal/convert/testdata/audio/jfk.wav"

echo "=== 0. OrbStack discipline: confirm k8s is down before docker build/run work (compose/k8s mutual exclusion) ==="
if command -v kubectl >/dev/null 2>&1 && kubectl cluster-info >/dev/null 2>&1; then
	echo "FAIL: kubectl reports a live cluster -- stop k8s before running this measurement" >&2
	exit 1
fi
echo "k8s confirmed down (or kubectl unavailable) -- proceeding"

echo
echo "=== 1. Build audio-worker image (no platform pin; matches Dockerfile.audio-worker exactly, never stale vs source) ==="
docker build -f Dockerfile.audio-worker -t "$IMAGE_TAG" .

echo
echo "=== 2. Start a long-lived measurement container (--cpus=$CPUS --memory=$MEMORY, matches the eventual compose audio-worker block) ==="
docker run -d --name "$CONTAINER" \
	--cpus="$CPUS" --memory="$MEMORY" \
	--entrypoint sleep "$IMAGE_TAG" infinity >/dev/null

echo
echo "=== 3. Read cgroup v2 cpu.max -> derive whisper-cli's -t thread count (floor of quota/period, min 1; matches Plan 02's cgroupCPULimit()) ==="
CPU_MAX=$(docker exec "$CONTAINER" cat /sys/fs/cgroup/cpu.max)
echo "cpu.max: $CPU_MAX"
THREADS=$(echo "$CPU_MAX" | awk '{ if ($1 == "max") { print 1 } else { n = int($1/$2); print (n < 1) ? 1 : n } }')
echo "Derived -t: $THREADS"

echo
echo "=== 4. Generate a deterministic synthetic multi-minute fixture INSIDE the container (ffmpeg loop of committed jfk.wav; never committed as a binary) ==="
docker exec "$CONTAINER" mkdir -p /tmp/work
docker cp "$FIXTURE_SRC" "$CONTAINER:/tmp/jfk.wav"
docker exec "$CONTAINER" ffmpeg -y -stream_loop -1 -i /tmp/jfk.wav -t "$FIXTURE_DURATION_S" -ar 16000 -ac 1 -c:a pcm_s16le /tmp/work/fixture.wav
FIXTURE_ACTUAL_DURATION_S=$(docker exec "$CONTAINER" ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 /tmp/work/fixture.wav)
echo "Fixture generated: requested=${FIXTURE_DURATION_S}s ffprobe-measured=${FIXTURE_ACTUAL_DURATION_S}s"

echo
echo "=== 4b. Sanity cross-check: sine-tone floor fixture (deterministic silence-adjacent signal; lower-bound RTF signal only, not the primary number) ==="
docker exec "$CONTAINER" ffmpeg -y -f lavfi -i "sine=frequency=440:duration=$FIXTURE_DURATION_S" -ar 16000 -ac 1 -c:a pcm_s16le /tmp/work/floor.wav
FLOOR_START=$(docker exec "$CONTAINER" date +%s%N)
docker exec "$CONTAINER" whisper-cli -m /models/ggml-base.bin -f /tmp/work/floor.wav -of /tmp/work/floor_out -otxt -l auto -t "$THREADS" >"$WORKDIR/floor_whisper.log" 2>&1 || true
FLOOR_END=$(docker exec "$CONTAINER" date +%s%N)
FLOOR_MS=$(( (FLOOR_END - FLOOR_START) / 1000000 ))
FLOOR_RTF=$(awk -v ms="$FLOOR_MS" -v dur="$FIXTURE_ACTUAL_DURATION_S" 'BEGIN { printf "%.6f", (ms/1000)/dur }')
echo "Floor (sine-tone) single-run: ${FLOOR_MS}ms => RTF=${FLOOR_RTF} (sanity lower bound only)"

echo
echo "=== 5. Timed loop INSIDE the container: $RUNS runs of the FULL production pipeline (ffmpeg normalize -> whisper-cli -t $THREADS -l auto -otxt) ==="
RAW_MILLIS=$(docker exec "$CONTAINER" sh -c "
  set -e
  for i in \$(seq 1 $RUNS); do
    start=\$(date +%s%N)
    ffmpeg -y -i file:/tmp/work/fixture.wav -ar 16000 -ac 1 -c:a pcm_s16le /tmp/work/norm_\${i}.wav >/tmp/work/ffmpeg_\${i}.log 2>&1
    whisper-cli -m /models/ggml-base.bin -f /tmp/work/norm_\${i}.wav -of /tmp/work/out_\${i} -otxt -l auto -t $THREADS >/tmp/work/whisper_\${i}.log 2>&1
    end=\$(date +%s%N)
    echo \$(( (end - start) / 1000000 ))
  done
")

echo "Per-run wall-clock (ms), execution order:"
echo "$RAW_MILLIS"

echo
echo "=== 6. Convert ms -> RTF (wall_clock_s / fixture_duration_s) then nearest-rank p95 (rank = ceil(0.95*N), verapdf-measure.sh core, reused verbatim) ==="
RAW_RTF=$(echo "$RAW_MILLIS" | awk -v dur="$FIXTURE_ACTUAL_DURATION_S" '{ printf "%.6f\n", ($1/1000)/dur }')
echo "Per-run RTF, execution order:"
echo "$RAW_RTF"

SORTED=$(echo "$RAW_RTF" | sort -n)
N=$(echo "$SORTED" | wc -l | tr -d ' ')
RANK=$(awk -v n="$N" 'BEGIN { r = n * 0.95; rr = (r == int(r)) ? r : int(r) + 1; print rr }')
P95_RTF=$(echo "$SORTED" | sed -n "${RANK}p")

echo
echo "Sorted ascending RTF:"
echo "$SORTED"
echo "N=$N, nearest-rank p95 rank=$RANK, p95 RTF=${P95_RTF}"

echo
echo "=== 7. Peak memory observation (cgroup v2 memory.peak, inside the ${MEMORY} limit; feeds AUDIO_WORKER_CONCURRENCY sizing, not a hard gate) ==="
PEAK_BYTES=$(docker exec "$CONTAINER" cat /sys/fs/cgroup/memory.peak 2>/dev/null || echo "unavailable")
if [ "$PEAK_BYTES" != "unavailable" ]; then
	PEAK_MB=$(awk -v b="$PEAK_BYTES" 'BEGIN { printf "%.1f", b / 1024 / 1024 }')
	echo "Peak memory (whisper-cli + ffmpeg single-job RSS): ${PEAK_BYTES} bytes (~${PEAK_MB} MiB) of ${MEMORY} limit"
else
	echo "Peak memory: cgroup v2 memory.peak not available in this environment"
fi

echo
echo "=== 8. Image size + measurement-host architecture (Phase-23-style arch caveat) ==="
IMAGE_SIZE=$(docker image inspect "$IMAGE_TAG" --format '{{.Size}}')
IMAGE_SIZE_MB=$(awk -v b="$IMAGE_SIZE" 'BEGIN { printf "%.1f", b / 1024 / 1024 }')
HOST_ARCH=$(uname -m)
echo "Image size: ${IMAGE_SIZE} bytes (~${IMAGE_SIZE_MB} MiB)"
echo "Measurement host architecture: ${HOST_ARCH} -- production amd64 RTF is expected to differ from this number; see SUMMARY caveat (Open Question 2, RESOLVED)"

echo
echo "=== ALL GREEN: measurement integrity checks passed (build, container, cgroup read, fixture generation, timed pipeline loop, p95, memory, image size all captured) ==="
echo "RESULT N=$N p95_RTF=${P95_RTF} threads=${THREADS} cpus=${CPUS} memory=${MEMORY} fixture_duration_s=${FIXTURE_ACTUAL_DURATION_S} peak_mem_bytes=${PEAK_BYTES} image_size_bytes=${IMAGE_SIZE} host_arch=${HOST_ARCH} floor_rtf=${FLOOR_RTF}"
