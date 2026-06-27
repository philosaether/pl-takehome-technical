#!/usr/bin/env python3
"""Render the one-pager's deliverable-grade figures from results/ CSVs.

    one-pager/.venv/bin/python one-pager/figures.py

Outputs (vector PDF, for \\includegraphics in the LaTeX):
  figures/fig1_lookahead.pdf       — the hero: maintained look-ahead vs naive SUM…GROUP BY
  figures/fig3_manifold_2d.pdf     — Valkey/Postgres throughput ratio over (workers × per-task work)
  figures/fig3_manifold_3d.pdf     — same, as a 3-D surface (A/B against the 2-D in the SII loop)

One palette, one type family, no chartjunk. Fig 2 (the cited Honcho diff) is a
LaTeX listing, not a generated image — see one-pager.tex.
"""
import csv
import os

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
from matplotlib import cm
from matplotlib.colors import LogNorm
from scipy.interpolate import griddata

# ---- house style -----------------------------------------------------------
HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
OUT = os.path.join(HERE, "figures")
os.makedirs(OUT, exist_ok=True)

INK = "#1c2330"          # near-black text/axes
GRID = "#d7dce3"         # faint grid
PG = "#2f6f9f"           # Postgres — slate blue
VALKEY = "#d1495b"       # Valkey — warm red
ACCENT = "#e08a1e"       # annotation amber

plt.rcParams.update({
    "font.family": "sans-serif",
    "font.sans-serif": ["Helvetica Neue", "Helvetica", "Arial", "DejaVu Sans"],
    "font.size": 10,
    "text.color": INK,
    "axes.edgecolor": INK,
    "axes.labelcolor": INK,
    "axes.linewidth": 0.8,
    "xtick.color": INK,
    "ytick.color": INK,
    "axes.spines.top": False,
    "axes.spines.right": False,
    "figure.dpi": 150,
})


# ---- Figure 1 — the look-ahead hero ----------------------------------------
def fig1_lookahead():
    tasks, ours, naive = [], [], []
    with open(os.path.join(ROOT, "results", "lookahead", "lookahead.csv")) as f:
        for r in csv.DictReader(f):
            tasks.append(float(r["tasks"]))
            ours.append(float(r["ours_ms"]))
            naive.append(float(r["naive_ms"]))
    tasks, ours, naive = map(np.array, (tasks, ours, naive))

    fig, ax = plt.subplots(figsize=(4.9, 2.4))
    ax.loglog(tasks, naive, "-o", color=VALKEY, lw=2, ms=4,
              label="naive  SUM(cost) … GROUP BY  (Honcho today)")
    ax.loglog(tasks, ours, "-o", color=PG, lw=2, ms=4,
              label="maintained look-ahead (proposed)")

    # the annotation IS the chart: the gap at 10^7
    x_end = tasks[-1]
    ax.annotate(
        "", xy=(x_end, naive[-1]), xytext=(x_end, ours[-1]),
        arrowprops=dict(arrowstyle="<->", color=ACCENT, lw=1.6),
    )
    ax.text(x_end * 0.62, np.sqrt(ours[-1] * naive[-1]), "1,364×",
            color=ACCENT, fontweight="bold", fontsize=15, ha="right", va="center")
    ax.text(x_end, naive[-1] * 1.5, "2.5 s", color=VALKEY, fontsize=8.5, ha="center")
    ax.text(x_end, ours[-1] * 0.5, "1.9 ms", color=PG, fontsize=8.5, ha="center")

    ax.set_xlabel("pending tasks")
    ax.set_ylabel("scheduling look-ahead cost (ms / poll)")
    ax.grid(True, which="major", color=GRID, lw=0.6)
    ax.grid(True, which="minor", color=GRID, lw=0.3, alpha=0.5)
    ax.legend(loc="upper left", fontsize=7.5, frameon=False)
    ax.set_title("The look-ahead stays flat as the backlog grows",
                 fontsize=10.5, fontweight="bold", loc="left", pad=8)
    fig.tight_layout()
    fig.savefig(os.path.join(OUT, "fig1_lookahead.pdf"))
    plt.close(fig)
    print("wrote fig1_lookahead.pdf")


# ---- shared: build the Valkey/Postgres ratio grid at shards=4 --------------
PROC_LEVELS = [0, 2, 20, 200]          # per-task work, ms
WORKER_LEVELS = [1, 10, 30, 100, 300, 1000]


def _ratio_grid(shards=4):
    """Return ratio[proc_idx][worker_idx] = valkey_tput / postgres_tput."""
    rows = {}
    with open(os.path.join(ROOT, "results", "run-cloud-2", "sweep.csv")) as f:
        for r in csv.DictReader(f):
            if int(r["shards"]) != shards:
                continue
            key = (r["backend"], r["process"], int(r["workers"]))
            rows[key] = float(r["throughput_acks_s"])
    proc_name = {0: "zero", 2: "2ms", 20: "20ms", 200: "200ms"}
    ratio = np.full((len(PROC_LEVELS), len(WORKER_LEVELS)), np.nan)
    for i, p in enumerate(PROC_LEVELS):
        for j, w in enumerate(WORKER_LEVELS):
            v = rows.get(("valkey", proc_name[p], w))
            pg = rows.get(("postgres", proc_name[p], w))
            if v and pg:
                ratio[i, j] = v / pg
    return ratio


def _interp_surface(ratio, n=120):
    """Interpolate log10(ratio) over (log10 workers, process-ordinal 0..3)."""
    pts, vals = [], []
    for i in range(len(PROC_LEVELS)):
        for j in range(len(WORKER_LEVELS)):
            if not np.isnan(ratio[i, j]):
                pts.append((np.log10(WORKER_LEVELS[j]), i))   # process as even ordinal
                vals.append(np.log10(ratio[i, j]))
    pts, vals = np.array(pts), np.array(vals)
    wx = np.linspace(np.log10(WORKER_LEVELS[0]), np.log10(WORKER_LEVELS[-1]), n)
    py = np.linspace(0, len(PROC_LEVELS) - 1, n)
    WX, PY = np.meshgrid(wx, py)
    Z = griddata(pts, vals, (WX, PY), method="cubic")
    Zlin = griddata(pts, vals, (WX, PY), method="linear")  # fill cubic NaNs at hull edge
    Z = np.where(np.isnan(Z), Zlin, Z)
    return WX, PY, Z  # Z is log10(ratio)


def _proc_ticks():
    return list(range(len(PROC_LEVELS))), [f"{p} ms" for p in PROC_LEVELS]


def fig3_manifold_2d():
    ratio = _ratio_grid()
    WX, PY, Z = _interp_surface(ratio)
    R = 10 ** Z  # back to linear ratio

    fig, ax = plt.subplots(figsize=(4.6, 3.7))
    levels = np.array([0.5, 1, 2, 4, 8, 16, 26])
    cf = ax.contourf(10 ** WX, PY, R, levels=levels, norm=LogNorm(),
                     cmap="RdYlBu_r", extend="both")
    # the crossover line: where Valkey stops being worth it (parity)
    cl = ax.contour(10 ** WX, PY, R, levels=[1.0], colors=[INK], linewidths=2)
    ax.clabel(cl, fmt={1.0: "parity"}, fontsize=8)
    c2 = ax.contour(10 ** WX, PY, R, levels=[2.0], colors=[INK],
                    linewidths=1.0, linestyles="--")
    ax.clabel(c2, fmt={2.0: "2×"}, fontsize=7.5)

    # mark the measured points
    for i, p in enumerate(PROC_LEVELS):
        for j, w in enumerate(WORKER_LEVELS):
            if not np.isnan(ratio[i, j]):
                ax.plot(w, i, "o", ms=2.5, color=INK, alpha=0.35)

    ax.set_xscale("log")
    ax.set_xlabel("workers")
    ax.set_yticks(*_proc_ticks())
    ax.set_ylabel("per-task downstream work")
    cb = fig.colorbar(cf, ax=ax, ticks=[0.5, 1, 2, 4, 8, 16, 26])
    cb.ax.set_yticklabels(["0.5×", "1×", "2×", "4×", "8×", "16×", "26×"])
    cb.set_label("Valkey throughput ÷ Postgres", fontsize=8.5)
    ax.set_title("Valkey's edge: cheap tasks, many workers",
                 fontsize=9.5, fontweight="bold", loc="left", pad=6)
    fig.tight_layout()
    fig.savefig(os.path.join(OUT, "fig3_manifold_2d.pdf"))
    plt.close(fig)
    print("wrote fig3_manifold_2d.pdf")


def fig3_manifold_3d():
    ratio = _ratio_grid()
    WX, PY, Z = _interp_surface(ratio)

    fig = plt.figure(figsize=(5.2, 3.9))
    ax = fig.add_subplot(111, projection="3d")
    surf = ax.plot_surface(WX, PY, Z, cmap="RdYlBu_r", linewidth=0,
                           antialiased=True, rstride=2, cstride=2)
    # parity plane (log10(1) = 0)
    ax.contour(WX, PY, Z, levels=[0], colors=[INK], linewidths=2, offset=None)

    ax.set_xlabel("workers", labelpad=2)
    ax.set_xticks([0, 1, 2, 3])
    ax.set_xticklabels(["1", "10", "100", "1000"], fontsize=7.5)
    ax.set_yticks(*_proc_ticks())
    ax.tick_params(axis="y", labelsize=7.5)
    ax.set_zlabel("Valkey ÷ Postgres", labelpad=2)
    ax.set_zticks([0, 1, np.log10(26)])
    ax.set_zticklabels(["1×", "10×", "26×"], fontsize=7.5)
    ax.view_init(elev=24, azim=-122)
    ax.set_title("The performance manifold", fontsize=10.5,
                 fontweight="bold", loc="left", pad=2)
    fig.tight_layout()
    fig.savefig(os.path.join(OUT, "fig3_manifold_3d.pdf"))
    plt.close(fig)
    print("wrote fig3_manifold_3d.pdf")


if __name__ == "__main__":
    fig1_lookahead()
    fig3_manifold_2d()
    fig3_manifold_3d()
    print("all figures written to", OUT)
