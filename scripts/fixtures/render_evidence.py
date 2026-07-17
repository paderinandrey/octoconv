#!/usr/bin/env python3
"""render_evidence.py -- CSV -> PNG dual-axis timeline renderer for Phase 28 (D-02).

Turns the load-proof gate's CSV sampler output into a dual-axis PNG: queue
depth and pod (replica) count plotted on one shared time axis, headless
(Agg backend -- no DISPLAY required).

Invoked ONLY via ephemeral uv, e.g.:
    uv run --with matplotlib python3 scripts/fixtures/render_evidence.py \
        --csv .planning/phases/28-autoscale-load-proof/evidence/sc1-sc2-burst-<ts>.csv \
        --png .planning/phases/28-autoscale-load-proof/evidence/sc1-sc2-burst-<ts>.png

This script imports only stdlib + matplotlib. It is NEVER added to any
persisted dependency manifest -- matplotlib is pulled fresh by `uv run
--with` on every invocation.

Expected CSV columns (written by scripts/keda-load-proof.sh's sampleLoop):
    timestamp,queue_depth,worker_replicas[,doc_replicas,html_replicas,...]

`timestamp` must be ISO-8601 parseable via datetime.fromisoformat.
`queue_depth` is plotted on the left y-axis; the first replica-count column
found (worker_replicas, or the first column ending in "_replicas") is
plotted on the right y-axis via twinx().
"""
import argparse
import csv
import sys
from datetime import datetime

import matplotlib

matplotlib.use("Agg")  # headless -- must be set before importing pyplot

import matplotlib.dates as mdates  # noqa: E402
import matplotlib.pyplot as plt  # noqa: E402


def pick_pod_count_column(fieldnames):
    """Prefer worker_replicas (the burst/drain scenario's primary series);
    otherwise fall back to the first column ending in _replicas."""
    if "worker_replicas" in fieldnames:
        return "worker_replicas"
    for name in fieldnames:
        if name != "timestamp" and name != "queue_depth" and name.endswith("_replicas"):
            return name
    return None


def main():
    parser = argparse.ArgumentParser(
        description="Render a Phase 28 load-proof CSV sampler file to a dual-axis PNG timeline."
    )
    parser.add_argument("--csv", required=True, help="Path to the sampler CSV.")
    parser.add_argument("--png", required=True, help="Output path for the rendered PNG.")
    parser.add_argument(
        "--title",
        default="Phase 28: 0->N->0 autoscale timeline",
        help="Chart title (default: 'Phase 28: 0->N->0 autoscale timeline').",
    )
    args = parser.parse_args()

    ts, queue_depth, pod_count = [], [], []
    pod_count_label = "pod count"

    with open(args.csv, newline="") as f:
        reader = csv.DictReader(f)
        fieldnames = reader.fieldnames or []
        pod_col = pick_pod_count_column(fieldnames)
        if pod_col:
            pod_count_label = pod_col
        for row in reader:
            ts.append(datetime.fromisoformat(row["timestamp"]))
            queue_depth.append(int(row.get("queue_depth") or 0))
            pod_count.append(int(row.get(pod_col) or 0) if pod_col else 0)

    if not ts:
        print(f"FAIL: no rows read from {args.csv}", file=sys.stderr)
        sys.exit(1)

    fig, ax1 = plt.subplots(figsize=(12, 5))
    ax1.plot(ts, queue_depth, color="tab:blue", label="queue depth")
    ax1.set_ylabel("queue depth", color="tab:blue")
    ax1.set_xlabel("time")

    ax2 = ax1.twinx()
    ax2.plot(ts, pod_count, color="tab:red", label=pod_count_label)
    ax2.set_ylabel(pod_count_label, color="tab:red")

    ax1.xaxis.set_major_formatter(mdates.DateFormatter("%H:%M:%S"))
    fig.autofmt_xdate()

    plt.title(args.title)
    fig.tight_layout()
    plt.savefig(args.png, dpi=120)

    print(f"Rendered evidence PNG: {args.png}")
    print(f"rows: {len(ts)}, pod-count column: {pod_count_label}")


if __name__ == "__main__":
    main()
