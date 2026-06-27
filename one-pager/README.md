# one-pager

The M4 deliverable: a 1-page PDF brief for Honcho. Design + rationale in
`.meta/designs/one-pager-construction.md`; Honcho-mapping evidence in
`.meta/designs/honcho-fig2-source.md`.

## Build

```sh
# figures (matplotlib) — one-time venv:
python3.13 -m venv .venv && .venv/bin/pip install matplotlib numpy scipy
.venv/bin/python figures.py          # → figures/*.pdf

# the PDF (tectonic — self-contained LaTeX, brew install tectonic):
tectonic one-pager.tex               # → one-pager.pdf
```

## Files

- `one-pager.tex` — the brief. Two-column research-note layout; Fig 2 (the cited
  Honcho diff) is an inline `listings` block, not a generated image.
- `figures.py` — renders Fig 1 (look-ahead hero), Fig 3 (the workers×work
  manifold) from `../results/`. Also emits `fig3_manifold_3d.pdf` for the
  2-D-vs-3-D A/B; the `.tex` embeds the 2-D by default.
- `figures/` — generated vector PDFs (committed so the doc builds without Python).
