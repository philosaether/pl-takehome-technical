#!/usr/bin/env python3
"""Render the M2 graphs from the CSVs the load harness produces.

    python3 scripts/plot.py [results_dir]   # default: ./results

Reads results/sweep.csv (throughput vs workers, per process model) and
results/lookahead.csv (ours vs naive SUM…GROUP BY vs task count), writing
throughput.png and lookahead.png. Requires matplotlib (`pip install matplotlib`).
"""
import csv
import sys
import os
from collections import defaultdict

try:
    import matplotlib
    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
except ImportError:
    sys.exit("matplotlib not installed — run: pip install matplotlib")


def config_label(backend, shards):
    """Series label for the faceted charts (process is the facet, in the title):
    `postgres` / `postgres-tuned` / `valkey×4`. Legacy no-backend CSVs → "series"."""
    if not backend:
        return "series"
    return f"{backend}×{shards}" if shards > 1 else backend


def _shards(row):
    """Shard count from the row; missing/empty (legacy CSV) → 1."""
    try:
        return int(row.get("shards") or 1)
    except ValueError:
        return 1


def _read_facets(path, value_col, skip_nonpositive):
    """sweep.csv → {process: {(backend, shards): [(workers, value, saturated)]}}."""
    facets = defaultdict(lambda: defaultdict(list))
    with open(path) as f:
        for row in csv.DictReader(f):
            v = float(row[value_col])
            if skip_nonpositive and v <= 0:  # log scale can't plot 0
                continue
            facets[row["process"]][(row.get("backend", ""), _shards(row))].append(
                (int(row["workers"]), v, row["saturated"] == "true")
            )
    return facets


def _facet_plot(results_dir, value_col, ylabel, title, prefix, ylog, skip_nonpositive=False):
    """One chart per process model — 9 configs × 4 process is too dense for a single
    axis, so process is the facet (in the title + filename). Writes
    <prefix>-<process>.png; the process=zero chart is the headline."""
    path = os.path.join(results_dir, "sweep.csv")
    if not os.path.exists(path):
        print(f"skip {prefix}: {path} not found")
        return
    facets = _read_facets(path, value_col, skip_nonpositive)
    if not facets:
        print(f"skip {prefix}: no samples")
        return
    for proc, series in sorted(facets.items()):
        fig, ax = plt.subplots(figsize=(8, 5))
        for (backend, shards), pts in sorted(series.items()):
            pts.sort()
            xs = [w for w, _, _ in pts]
            ys = [v for _, v, _ in pts]
            ax.plot(xs, ys, marker="o", label=config_label(backend, shards))
            for w, v, sat in pts:  # mark load-gen-bound points
                if not sat:
                    ax.annotate("unsat", (w, v), fontsize=7, color="red")
        ax.set_xscale("log")
        if ylog:
            ax.set_yscale("log")
        ax.set_xlabel("workers")
        ax.set_ylabel(ylabel)
        ax.set_title(f"{title} — process={proc}")
        ax.legend()
        ax.grid(True, which="both", alpha=0.3)
        out = os.path.join(results_dir, f"{prefix}-{proc}.png")
        fig.savefig(out, dpi=120, bbox_inches="tight")
        print("wrote", out)


def plot_throughput(results_dir):
    _facet_plot(results_dir, "throughput_acks_s", "throughput (acks/s)",
                "Throughput vs workers", "throughput", ylog=False)


def plot_latency(results_dir):
    """loop_p99 vs workers — the latency companion to the throughput plateau. Past
    saturation throughput flattens while this climbs (workers contend on claims/acks)."""
    _facet_plot(results_dir, "loop_p99_ms", "loop p99 (ms)",
                "Loop-latency p99 vs workers", "latency", ylog=True, skip_nonpositive=True)


def plot_lookahead(results_dir):
    path = os.path.join(results_dir, "lookahead.csv")
    if not os.path.exists(path):
        print(f"skip lookahead: {path} not found")
        return
    tasks, ours, naive = [], [], []
    with open(path) as f:
        for row in csv.DictReader(f):
            tasks.append(int(row["tasks"]))
            ours.append(float(row["ours_ms"]))
            naive.append(float(row["naive_ms"]))
    fig, ax = plt.subplots(figsize=(8, 5))
    ax.plot(tasks, ours, marker="o", label="ours (maintained aggregate)")
    ax.plot(tasks, naive, marker="s", label="naive SUM…GROUP BY")
    ax.set_xscale("log")
    ax.set_yscale("log")
    ax.set_xlabel("pending tasks")
    ax.set_ylabel("look-ahead query time (ms)")
    ax.set_title("Look-ahead cost: maintained aggregate (flat) vs naive scan (grows)")
    ax.legend()
    ax.grid(True, which="both", alpha=0.3)
    out = os.path.join(results_dir, "lookahead.png")
    fig.savefig(out, dpi=120, bbox_inches="tight")
    print("wrote", out)


if __name__ == "__main__":
    rd = sys.argv[1] if len(sys.argv) > 1 else "results"
    plot_throughput(rd)
    plot_latency(rd)
    plot_lookahead(rd)
