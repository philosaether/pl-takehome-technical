# riff/m3-head-to-head

M3 head-to-head prerequisites: shard-count capture in the sweep + a Terraform
Valkey box (1/2/4 shards), then a full local dry run of the head-to-head before
any cloud spend.

Started: 2026-06-27

## Targets

From `in-progress.md` (M3 head-to-head, the two prerequisites the merged code
doesn't cover) + the gated cloud run:

1. **Shard-count capture in the sweep** — `Result.Shards` + CSV column + plot.py
   series key + Makefile sweep at 1/2/4 addrs + local multi-instance Valkey.
   (Prereq for a meaningful local dry run — do first.)
2. **Terraform Valkey box(es)** — 1/2/4 instances running the compose durability
   config (appendonly/everysec/noeviction). (Cloud-only — second.)
3. **Full local dry run** of `make head-to-head` at 1/2/4 shards, end to end,
   before the cloud run. Includes the `TF_VAR_*` walk-through for when we go up.

Played notes graduate matching `in-progress.md` items.

---

## Note 1: shard-count capture in the sweep

**Why.** `sweep.csv`'s `backend` column is just "valkey" regardless of 1/2/4
shards, so the linearity proof (the M3 decision gate — Valkey scales 1→4 shards
where one PG primary plateaus) can't be plotted as distinct series. The driver
*already* supports N shards (`PLQ_VALKEY_ADDR` comma-split → `len(addrs)`); only
the **measurement plumbing and the sweep loop** are shard-count-blind. This note
makes shard count a first-class dimension end to end.

**What I'll change (5 spots, smallest viable):**

1. **`internal/loadgen/harness.go` — `Result.Shards int`.** New field; add
   `shards` to `SweepHeader()` and `CSVRow()`. Placement: right after `backend`
   in the header (`backend,shards,workers,…`) so the two series keys sit
   together. *(This breaks the column order of any pre-existing `sweep.csv` — the
   sweep targets all `rm -f sweep.csv` first, so no live data is harmed; plot.py
   reads by header name, not position.)*

2. **`cmd/plq/main.go` — set `res.Shards` in `runLoadrun`.** Next to the existing
   `res.Backend = cfg.Backend`. Value = the configured shard count. Cleanest
   source: count the comma-split, non-empty entries of `cfg.ValkeyAddr`; if zero
   (postgres path, or memory), default to `1`. Postgres = "1 primary" is the
   honest series label for the head-to-head, so 1 is correct there too.
   - *Decision point:* compute it inline in main.go, OR expose a `Shards() int`
     off the backend. **Recommend inline** — main.go already owns the
     `cfg.Backend` series-key assignment, the addr string lives in config, and a
     new interface method for one int is over-plumbing. (Flag if you'd rather the
     backend own it.)

3. **`scripts/plot.py` — fold shards into the series key.** `label_for` and both
   `series[...]` keys become `(backend, shards, process)`. Label reads e.g.
   `valkey×4 process=zero` for shards>1, and stays `postgres process=zero` /
   `valkey process=zero` when shards≤1 (don't clutter the PG curve or legacy
   single-shard runs with `×1`). Missing/empty `shards` column → treat as 1
   (back-compat with any old CSV).

4. **`Makefile` — sweep across a shard set.** Add `VALKEY_ADDRS_SWEEP`, a list of
   addr-strings, each entry = one shard count:
   ```
   VALKEY_ADDRS_SWEEP ?= localhost:6379 \
                         localhost:6379,localhost:6380 \
                         localhost:6379,localhost:6380,localhost:6381,localhost:6382
   ```
   `sweep-valkey` gains an outer loop over these (inner loops = workers × process,
   unchanged), setting `PLQ_VALKEY_ADDR` per entry. One entry (the default-overridable
   single addr) keeps `load-test-valkey` a 1-shard baseline. `head-to-head` calls
   `sweep-postgres` once + `sweep-valkey` across the set.

5. **`docker-compose.yml` — local multi-instance Valkey.** Add `valkey-2`,
   `valkey-3`, `valkey-4` services (ports 6380/6381/6382), identical durability
   command to `valkey`. `up-valkey` brings up all four so the local dry run can
   exercise the full 1/2/4 sweep. *(Alternative: a `--scale`-friendly single
   service — rejected: fixed host ports are simpler to point `PLQ_VALKEY_ADDR` at,
   and 4 instances is trivial locally.)*

**Out of scope (defer if they surface):** the cloud Terraform shards (Note 2),
producer-count tuning to saturate Valkey (measure-first, on the real sweep).

**Verify:** `make up-valkey` (4 instances healthy) → a short
`WORKERS_SWEEP="1 10" PROCESS_MS="" make head-to-head` → confirm `sweep.csv` has
a `shards` column with 1/2/4 rows for valkey + 1 for postgres, and `latency.png`/
`throughput.png` render five distinct series. (Full dry run is Note 3.)
