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


def plot_throughput(results_dir):
    path = os.path.join(results_dir, "sweep.csv")
    if not os.path.exists(path):
        print(f"skip throughput: {path} not found")
        return
    # group throughput by process model → [(workers, throughput, saturated)]
    series = defaultdict(list)
    with open(path) as f:
        for row in csv.DictReader(f):
            series[row["process"]].append(
                (int(row["workers"]), float(row["throughput_acks_s"]), row["saturated"] == "true")
            )
    fig, ax = plt.subplots(figsize=(8, 5))
    for proc, pts in sorted(series.items()):
        pts.sort()
        xs = [w for w, _, _ in pts]
        ys = [t for _, t, _ in pts]
        ax.plot(xs, ys, marker="o", label=f"process={proc}")
        # mark non-saturated points (the number may be load-gen-bound)
        for w, t, sat in pts:
            if not sat:
                ax.annotate("unsat", (w, t), fontsize=7, color="red")
    ax.set_xscale("log")
    ax.set_xlabel("workers")
    ax.set_ylabel("throughput (acks/s)")
    ax.set_title("Throughput vs workers")
    ax.legend()
    ax.grid(True, which="both", alpha=0.3)
    out = os.path.join(results_dir, "throughput.png")
    fig.savefig(out, dpi=120, bbox_inches="tight")
    print("wrote", out)


def plot_latency(results_dir):
    """loop_p99 vs workers — the latency companion to the throughput plateau.
    Past saturation, throughput flattens while this climbs (workers contend on
    claims/acks). In zero-work mode this is the queue's own loop overhead; in
    cost mode it's dominated by the simulated work (read it in the M3 PG-vs-Valkey
    comparison, where that cancels)."""
    path = os.path.join(results_dir, "sweep.csv")
    if not os.path.exists(path):
        print(f"skip latency: {path} not found")
        return
    series = defaultdict(list)
    with open(path) as f:
        for row in csv.DictReader(f):
            p99 = float(row["loop_p99_ms"])
            if p99 <= 0:  # log scale can't plot 0 (no samples at that point)
                continue
            series[row["process"]].append(
                (int(row["workers"]), p99, row["saturated"] == "true")
            )
    if not series:
        print("skip latency: no loop_p99 samples")
        return
    fig, ax = plt.subplots(figsize=(8, 5))
    for proc, pts in sorted(series.items()):
        pts.sort()
        xs = [w for w, _, _ in pts]
        ys = [p for _, p, _ in pts]
        ax.plot(xs, ys, marker="o", label=f"process={proc}")
        for w, p, sat in pts:
            if not sat:
                ax.annotate("unsat", (w, p), fontsize=7, color="red")
    ax.set_xscale("log")
    ax.set_yscale("log")
    ax.set_xlabel("workers")
    ax.set_ylabel("loop p99 (ms)")
    ax.set_title("Loop-latency p99 vs workers (claim→drain→process→ack)")
    ax.legend()
    ax.grid(True, which="both", alpha=0.3)
    out = os.path.join(results_dir, "latency.png")
    fig.savefig(out, dpi=120, bbox_inches="tight")
    print("wrote", out)


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
