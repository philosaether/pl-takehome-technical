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

## Note 1: shard-count capture in the sweep ✅ PLAYED (5bac539)

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

---

## Note 2: Terraform Valkey box(es) for the cloud head-to-head ✅ PLAYED

**Why.** `deploy/terraform` provisions PG-only (`pg` / `worker` / `producer`).
The cloud head-to-head needs Valkey primaries — and for the linearity proof, up
to 4 — running the *same* durability config as compose, in the same security
group so the worker box can reach them. This is the cloud mirror of Note 1's
local `valkey-2/3/4`.

**What I'll add (terraform only — no app code):**

1. **`main.tf` — `aws_instance.valkey` with `count = var.valkey_count`** (default
   4). Each runs `valkey/valkey:8.1` via docker in user-data, with the exact
   compose durability flags (`--appendonly yes --appendfsync everysec
   --aof-use-rdb-preamble yes --maxmemory 512mb --maxmemory-policy noeviction`),
   `-p 6379:6379`, `--restart always`. Tagged `plq-valkey-${count.index+1}`.
   Same `key_name` + `vpc_security_group_ids` as the others — the SG already
   allows all intra-group traffic (`self = true`), so worker→valkey:6379 is open
   with no new rule.

2. **`variables.tf` —** `valkey_count` (default 4) and `valkey_type` (default
   `m5.xlarge`, matching `pg_type` so a single Valkey primary vs the single PG
   primary is a fair per-box comparison; dial down to cut spend).

3. **`outputs.tf` —** `valkey_private_ips` (the list) plus three convenience
   joined addr strings the worker box pastes straight into `PLQ_VALKEY_ADDR`:
   `valkey_addrs_1` / `_2` / `_4` (e.g. `10.0.1.5:6379,10.0.1.6:6379`). These map
   1:1 onto the local `VALKEY_ADDRS_SWEEP` entries.

4. **`deploy/terraform/README.md` —** extend the runbook: build *both* static
   binaries (`-tags postgres` and `-tags valkey`), ship both to the worker box,
   run `sweep-postgres` against the PG DSN then `sweep-valkey` at each
   `valkey_addrs_{1,2,4}`, pull one combined `sweep.csv`, graph. Note the spend:
   default adds 4× `m5.xlarge` — still ≈$1 for a sub-hour torn-down run, but call
   it out, and note `valkey_count=1` for a baseline-only run.

**Decision points (defaults chosen, flag to override):**
- **`count`-based fleet of 4, not a single resized box.** N independent primaries
  *is* the architecture under test (`hash(workspace)%N` routing); modeling it as
  real separate instances keeps the cloud run honest. The sweep uses 1/2/4 of
  them via the addr-string outputs.
- **`valkey_type = m5.xlarge` (= pg_type).** Fair per-primary comparison. Valkey
  is single-threaded for command exec, so it won't *use* the box like PG — that
  asymmetry is itself a talking point (a Valkey primary needs less iron).
  - Add it to talking-points.md and press on with the m5.xlarge for the test

**Out of scope:** standing up real Valkey Cluster (the design names it, doesn't
run it — independent primaries is the test); auto-running the sweep from
user-data (operator-driven, same as the PG box today).

**Verify (no apply — that's gated/Note 3):** `terraform -chdir=deploy/terraform
validate` + `terraform fmt -check`, and `terraform plan` *iff* TF_VARs are set
(otherwise eyeball the plan during the Note 3 dry-run walk-through). No `apply`.
