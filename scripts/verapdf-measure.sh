#!/usr/bin/env bash
# verapdf-measure.sh -- Phase 23 (veraPDF validation) go/no-go measurement gate.
#
# Proves, against the REAL built document-worker image (with the pinned
# verapdf/cli:v1.30.2 bundled per Dockerfile.document-worker), BEFORE any Go
# wiring:
#   1. veraPDF's JVM cold-start wall-clock cost over >=10 runs on a REAL
#      PDF/A-2b export produced by soffice from internal/e2e/testdata/sample.docx
#      (the exact FilterOptions suffix internal/convert/opts.go uses,
#      pdfaFilterOptionsSuffix). Nearest-rank p95 is computed and asserted
#      <= 10s (D-01 budget).
#   2. The compliant export's report says isCompliant=true -- the Pitfall 9
#      regression canary (a real LibreOffice PDF/A-2b export that veraPDF
#      rejects is a STOP condition, not a downgrade).
#   3. A deliberately non-compliant PDF (a plain, non-PDF/A soffice export)
#      is confirmed flagged non-compliant by veraPDF.
#   4. Peak memory inside the container's 1g limit is observed and recorded
#      (JVM + idle LibreOffice footprint, D-10 CI-impact companion check).
#
# This script's exit code IS the gate: any failed assertion aborts non-zero
# (set -e) with a loud FAIL message. It builds the image fresh (matching the
# committed Dockerfile.document-worker exactly) so the measurement is never
# stale relative to source. veraPDF is amd64-only on Docker Hub (no arm64
# manifest, 23-01) -- everything below runs under --platform linux/amd64,
# which on Apple Silicon means Rosetta-class emulation (OrbStack), not qemu;
# this is recorded in the SUMMARY as a caveat on the raw numbers.
set -euo pipefail

cd "$(dirname "$0")/.."

IMAGE_TAG="octoconv-document-worker:verapdf-measure"
RUNS="${VERAPDF_MEASURE_RUNS:-10}"
CONTAINER="octoconv-verapdf-measure-$$"
WORKDIR=$(mktemp -d)
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; rm -rf "$WORKDIR"' EXIT

FIXTURE_SRC="internal/e2e/testdata/sample.docx"
COMPLIANT_MRR="internal/convert/testdata/verapdf_compliant.mrr.xml"
NONCOMPLIANT_MRR="internal/convert/testdata/verapdf_noncompliant.mrr.xml"
NONCOMPLIANT_PDF="internal/convert/testdata/verapdf_noncompliant.pdf"

# Mirrors internal/convert/opts.go's pdfaFilterOptionsSuffix verbatim (D-07):
# SelectPdfVersion=2 forces PDF/A output, EmbedStandardFonts=true is required
# alongside it for PDF/A-2b conformance (Pitfall 7).
PDFA_SUFFIX='{"SelectPdfVersion":{"type":"long","value":"2"},"EmbedStandardFonts":{"type":"boolean","value":true}}'

echo "=== 1. Build document-worker image (--platform linux/amd64, matches verapdf/cli's only published arch) ==="
docker build --platform linux/amd64 -f Dockerfile.document-worker -t "$IMAGE_TAG" .

echo
echo "=== 2. Start a long-lived measurement container (memory=1g/cpus=2.0, matches docker-compose.yml limits) ==="
docker run -d --name "$CONTAINER" --platform linux/amd64 \
	--memory=1g --cpus=2.0 \
	--entrypoint sleep "$IMAGE_TAG" infinity >/dev/null

docker exec "$CONTAINER" mkdir -p /tmp/work /tmp/work-plain
docker cp "$FIXTURE_SRC" "$CONTAINER:/tmp/sample.docx"

echo
echo "=== 3. Capture the CLI contract (--help) against the pinned image ==="
docker exec "$CONTAINER" verapdf --help | tee "$WORKDIR/verapdf-help.txt"

echo
echo "=== 4. Generate a REAL PDF/A-2b compliant export (soffice, same FilterOptions suffix as internal/convert/opts.go) ==="
docker exec "$CONTAINER" sh -c "
  soffice --headless --nologo --nofirststartwizard --norestore \
    -env:UserInstallation=file:///tmp/work/lo-profile \
    --convert-to 'pdf:writer_pdf_Export:${PDFA_SUFFIX}' \
    --outdir /tmp/work /tmp/sample.docx
"
docker exec "$CONTAINER" test -s /tmp/work/sample.pdf

echo
echo "=== 5. Generate a deliberately non-compliant PDF (plain soffice PDF export, no PDF/A FilterOptions) ==="
docker exec "$CONTAINER" sh -c "
  soffice --headless --nologo --nofirststartwizard --norestore \
    -env:UserInstallation=file:///tmp/work-plain/lo-profile \
    --convert-to pdf:writer_pdf_Export \
    --outdir /tmp/work-plain /tmp/sample.docx
"
docker exec "$CONTAINER" test -s /tmp/work-plain/sample.pdf
docker cp "$CONTAINER:/tmp/work-plain/sample.pdf" "$NONCOMPLIANT_PDF"

echo
echo "=== 6. Measure JVM cold-start cost: $RUNS runs of 'verapdf -f 2b --format xml' on the compliant PDF/A-2b export ==="
# Timed entirely INSIDE the container (single docker exec, GNU date +%s%N) so
# the measured cost matches what the real worker would pay shelling out to
# verapdf as a subprocess -- not inflated by repeated docker-exec IPC
# overhead from the host.
RAW_MILLIS=$(docker exec "$CONTAINER" sh -c "
  set -e
  for i in \$(seq 1 $RUNS); do
    start=\$(date +%s%N)
    verapdf -f 2b --format xml /tmp/work/sample.pdf > /tmp/work/run_\${i}.xml
    end=\$(date +%s%N)
    echo \$(( (end - start) / 1000000 ))
  done
")

echo "Per-run wall-clock (ms):"
echo "$RAW_MILLIS"

SORTED=$(echo "$RAW_MILLIS" | sort -n)
N=$(echo "$SORTED" | wc -l | tr -d ' ')
# Nearest-rank p95: sort ascending, take the value at 1-based rank
# ceil(0.95*N). For N=10 that's rank 10 (the slowest of the 10 runs).
RANK=$(awk -v n="$N" 'BEGIN { r = n * 0.95; rr = (r == int(r)) ? r : int(r) + 1; print rr }')
P95_MS=$(echo "$SORTED" | sed -n "${RANK}p")
P95_S=$(awk -v ms="$P95_MS" 'BEGIN { printf "%.3f", ms / 1000 }')

echo
echo "N=$N, nearest-rank p95 rank=$RANK, p95=${P95_MS}ms (${P95_S}s)"

BUDGET_MS=10000
if [ "$P95_MS" -gt "$BUDGET_MS" ]; then
	echo "FAIL: p95=${P95_MS}ms exceeds the D-01 10s budget (${BUDGET_MS}ms) -- NO-GO" >&2
	exit 1
fi
echo "PASS: p95=${P95_MS}ms <= ${BUDGET_MS}ms budget (D-01)"

echo
echo "=== 7. Pitfall 9 regression canary: assert the compliant export validates isCompliant=true ==="
docker cp "$CONTAINER:/tmp/work/run_1.xml" "$COMPLIANT_MRR"
if ! grep -q 'isCompliant="true"' "$COMPLIANT_MRR"; then
	echo "FAIL: real LibreOffice PDF/A-2b export did NOT validate isCompliant=true -- Pitfall 9 regression, NO-GO" >&2
	echo "--- captured report ---" >&2
	cat "$COMPLIANT_MRR" >&2
	exit 1
fi
echo "PASS: compliant export validates isCompliant=true (captured to $COMPLIANT_MRR)"

echo
echo "=== 8. Confirm the non-compliant fixture is actually flagged non-compliant ==="
docker exec "$CONTAINER" sh -c "verapdf -f 2b --format xml /tmp/work-plain/sample.pdf > /tmp/work-plain/report.xml" || true
docker cp "$CONTAINER:/tmp/work-plain/report.xml" "$NONCOMPLIANT_MRR"
if grep -q 'isCompliant="true"' "$NONCOMPLIANT_MRR"; then
	echo "FAIL: the plain (non-PDF/A) export unexpectedly validated isCompliant=true -- fixture is not a valid non-compliant canary" >&2
	cat "$NONCOMPLIANT_MRR" >&2
	exit 1
fi
echo "PASS: non-compliant fixture confirmed flagged non-compliant (captured to $NONCOMPLIANT_MRR, PDF fixture at $NONCOMPLIANT_PDF)"

echo
echo "=== 9. Peak memory observation (cgroup v2 memory.peak, inside the 1g container limit) ==="
PEAK_BYTES=$(docker exec "$CONTAINER" cat /sys/fs/cgroup/memory.peak 2>/dev/null || echo "unavailable")
if [ "$PEAK_BYTES" != "unavailable" ]; then
	PEAK_MB=$(awk -v b="$PEAK_BYTES" 'BEGIN { printf "%.1f", b / 1024 / 1024 }')
	echo "Peak memory (JVM + idle LibreOffice footprint): ${PEAK_BYTES} bytes (~${PEAK_MB} MiB) of 1024 MiB limit"
else
	echo "Peak memory: cgroup v2 memory.peak not available in this environment"
fi

echo
echo "=== ALL GREEN: p95=${P95_MS}ms (${P95_S}s) <= 10s budget; compliant canary PASS; non-compliant fixture PASS ==="
