# results/

Benchmark outputs, one directory per run. Tracked in git so canonical numbers
survive (especially the cloud runs, which cost money to reproduce).

## Layout

- `run-cloud-N/` — a cloud sweep (AWS, via `deploy/terraform`). The graded numbers.
- `run-local-N/` — a local sweep (laptop / Docker compose). Dev + dry runs; the
  absolute numbers aren't canonical (laptop iron), but the shape is.
- `lookahead/` — the look-ahead bench (proof #1: maintained aggregate vs naive
  `SUM…GROUP BY`). Not a worker-sweep, so it gets a named bucket, not a number.

Each run bucket holds `sweep.csv` (raw), the rendered `throughput.png` /
`latency.png`, the per-second `sample-*.csv` telemetry, and (optionally) a
`results.md` writeup of what the run showed.

## Producing a run

Point the harness at a fresh bucket via `PLQ_RESULTS`, then graph it:

```sh
PLQ_RESULTS=results/run-cloud-2 make head-to-head   # or sweep-postgres / sweep-valkey
python3 scripts/plot.py results/run-cloud-2
```

(The Makefile's default sweep targets still write to `./results` top-level; that's
treated as ignored scratch — promote a keeper by running it into a `run-*` bucket
or moving it there.)

## What's tracked

`run-*/`, `lookahead/`, and this README are tracked. Loose files at the top level
of `results/` are gitignored scratch.
