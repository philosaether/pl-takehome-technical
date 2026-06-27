---
Status: accepted
Date: 2026-06-27
Accepted: 2026-06-27
Implemented: 2026-06-27 (feature/ambitious-head-to-head) — multi-DSN PG router (conformance 8/8 at 1+2 shards), PLQ_RESET gate, terraform topology, orchestration scripts, plot faceting; ran on AWS as run-cloud-2.
Divergences: AWS 32-vCPU quota forced m5.large / 4 shards (not m5.xlarge / 8); 3 parallel tracks became sequential (fail-loud, after the parallel-wait swallowed errors); 7 deployment bugs fixed (see decisions 2026-06-27). Results: results/run-cloud-2/.
Deferred: the 8-shard point (quota bump → TF_VAR_pg_count=8 TF_VAR_valkey_count=8 + m5.xlarge/m5.2xlarge) and the durability off/everysec/always curve (CONFIG SET fixed to sudo docker exec; failed this run). Both are one quota-bumped rerun away.
Assessment: results/run-cloud-1/results.md (the run this upgrades)
Supersedes: (none — extends loadgen-and-proofs.md + valkey-driver.md)
---

# Ambitious head-to-head (run-cloud-2) — Desired State

Turn run-cloud-1's clean-but-soft proof into the airtight version: an
**isolated, saturation-guaranteed** sweep that shards **both** backends 1/2/4/8
through the **same `hash(workspace)%N` router** (separating per-primary engine
speed from horizontal scaling), includes a **tuned-Postgres baseline** (to preempt
"you didn't tune PG"), maps the **durability tradeoff** (fsync off/everysec/always),
covers the full **process sweep (0/2/20/200ms)**, and reports **$/throughput** and
**p99-at-the-knee** alongside raw throughput.

The output is `results/run-cloud-2/` and an updated decision-gate chart that no
reasonable reviewer can wave away.

---

## What run-cloud-1 left on the table

run-cloud-1 proved the thesis (PG plateaus ~1.7k/s; Valkey scales 26k→49k→92k
over 1/2/4 shards). Its honest soft spots, each now a dimension:

| Soft spot | Fix (dimension) |
|-----------|-----------------|
| Integrated `loadrun` co-located producers+workers → suppressed numbers, several `unsat` points | **Isolated + saturated** topology |
| 3 shard points = a line, not a trend | **8 shards** (1/2/4/8) |
| "1 PG vs N Valkey" conflates engine speed with sharding | **Shard both** (PG & Valkey ×1/2/4/8 via one router) |
| Stock `postgres:16` invites "did you even tune it?" | **Tuned-PG baseline** |
| "But Redis loses data" unanswered | **Durability tradeoff** curve |
| Only zero + 20ms work | **Process sweep** 0/2/20/200ms |
| No business framing | **$/throughput** + **p99-at-knee** analysis |

---

## The matrix

**Main sweep** — 9 configs × 4 process × 6 workers = **216 points**:
- configs: `postgres×1/2/4/8` (stock, sharded via the new multi-DSN router),
  `postgres-tuned` (single primary), `valkey×1/2/4/8`
- process: `0` (zero), `2ms`, `20ms`, `200ms` (LLM-call-scale)
- workers: `1`, `10`, `30`, `100`, `300`, `1000` (30/300 added to sharpen the knee)

Both backends shard through the **same** `hash(workspace)%N` router, so the
comparison isolates two variables: **per-primary engine speed** (PG vs Valkey at
the same shard count) and **horizontal scaling** (the slope as N grows).
`postgres×1` is the stock single-primary baseline; `postgres-tuned` stays single
(the "did you tune it" preempt — not sharded).

> **Chart density:** 9 configs × 4 process is too many series for one overlay.
> plot.py **facets by process model** (one throughput + one latency chart per
> process); the headline decision chart is **process=0** (9 series: pg×1/2/4/8,
> pg-tuned, valkey×1/2/4/8).

**Durability sub-experiment** — isolated axis, **6 points**:
- config: `valkey×1` (per-instance fsync cost; sharding is orthogonal)
- process: `0` (where durability cost is most visible)
- workers: `100`, `1000`
- fsync: `off` (appendonly no), `everysec`, `always`

---

## Topology — the isolated worker (rigor dimension)

run-cloud-1 ran producers and workers in one process. run-cloud-2 separates them
so the worker box measures a **production-match worker pool against externally
supplied load**:

- **Producer box(es):** `plq loadgen` running *continuously* (Zipfian churn),
  pointed at the backend. High `PLQ_PRODUCERS` to outrun the fastest worker.
- **Worker box:** `plq loadrun` with **`PLQ_PRODUCERS=0`** (verified: a clean
  no-op — no internal producers) and **`PLQ_RESET=false`** (see code change).
  The sampler still computes throughput / backlog / claim+loop p99 — now against
  the external queue depth. This is the M2 "worker alone = production-match"
  model, finally measured.

**Saturation protocol.** A point is only reported if `saturated=true`
(`min_backlog > 0` over the window → workers never starved). Over-provision
producers; rerun any `unsat` point with more producer firepower. The hardest
target is `valkey×8` / process=0 / 1000 workers (~180k/s extrapolated) — **budget
3 producer boxes for the Valkey track** (OQ resolved).

**Coordination.** Per backend: `plq reset` once, start `loadgen`, let the queue
fill past the worker's appetite, then run the 16 (process×worker) points back to
back with `PLQ_RESET=false` so the standing depth persists between points.

---

## Code changes (router-first)

Build order: **(1) the multi-DSN PG router + conformance green, BEFORE anything
else** (it's the only correctness-sensitive change; verify it free, locally), then
(2) the `PLQ_RESET` gate, (3) `shardCount` extension, (4) plot.py faceting. Commit
each; run the full test suite once they're all in (Phil's "commit + verify, then
run everything in one go").

### (1) Multi-DSN Postgres router — `internal/postgres` (the ½-day item)

Mirror the Valkey backend's proven shape (`shards []…`, `shardForWorkspace`, an
`rr` rotation cursor). Per-method SQL is **unchanged**; only pool selection + the
cross-shard loops change.

- **`backend.go`** — `Backend.pool *pgxpool.Pool` → `shards []*pgxpool.Pool` +
  `rr atomic.Uint64`.
  - `New(o Options)`: `o.DSN` → `o.DSNs []string`; open a pool per DSN, `applySchema`
    to each, close all on error.
  - `shardForWorkspace(ws) *pgxpool.Pool`: `len==1 → shards[0]`; else
    `fnv32a(ws) % len` — byte-identical to Valkey's router.
  - **Per-unit methods** (Enqueue/Drain/Ack/Release/Heartbeat/Fail): replace
    `b.pool` with `pool := b.shardForWorkspace(key.Workspace)` (key from the arg, or
    `c.Key.Workspace` for ClaimedUnit-based calls). Mechanical.
  - **Claim** (the one new bit of logic): rotate over shards from `rr++ % n`, run
    the existing claim query on each until one returns a unit; nil if all empty.
  - **Stats / ReapExpired**: loop shards, sum. **Reset**: `TRUNCATE` each.
    **Close**: close each.
  - **`tenantCache`**: route `get(ws)` to `shardForWorkspace(ws)` (a workspace's
    `tenant_config` lives on its shard).
- **`cmd/plq/backend_postgres.go`** — comma-split `PLQ_POSTGRES_DSN` into `DSNs`,
  exactly like the Valkey wiring splits addrs.
- **Correctness gate** — extend `tests/conformance` PG setup to accept comma-split
  DSNs (reuse `PLQ_TEST_POSTGRES`); run the 8-scenario suite against a **2-shard**
  PG and confirm **8/8** (a unit claimed on shard X must ack on shard X — the
  `ClaimedUnit.Key` routing guarantees it; the suite *proves* it).

### (2) `PLQ_RESET` gate — `internal/config` + `cmd/plq/main.go`

`runLoadrun` unconditionally `Reset()`s — which would wipe the queue the external
producers fill. Add `Reset bool` (`boolEnv("PLQ_RESET", true)` — needs a `boolEnv`
helper); wrap the reset in `if cfg.Reset { … }`. Default true = back-compat; worker
boxes set `PLQ_RESET=false`.

### (3) `shardCount` extension — `cmd/plq/main.go`

Today `shardCount` counts only `ValkeyAddr`. Extend it to count `PostgresDSN`
entries when the PG backend is active, so the `shards` CSV column is correct for
`postgres×N` too. (One small branch; the comment cross-referencing the Makefile
already exists.)

### (4) plot.py faceting + `$/throughput` — `scripts/plot.py`

Emit one throughput + one latency chart **per process model** (9-config overlay is
too dense for a single axis), plus a small `$/throughput` table/bar from the cost
basis. The series key (`backend, shards, process`) already distinguishes
`postgres×N` from `valkey×N`.

**Series labels (zero-code trick, still used for the non-sharded variants).**
`Result.Backend` is set from `PLQ_BACKEND` (a label; driver is compiled-in via
build tag): `postgres-tuned` → distinct series; durability rows →
`valkey-off|everysec|always` into a separate `durability.csv`.

---

## Infra (terraform)

Keep terraform simple: provision **datastores explicitly** + a **generic runner
pool**; let the orchestration script assign runner roles.

- **`aws_instance.pg` count = `var.pg_count`** (8) — the **sharded-PG pool**, stock
  `postgres:16`, 20 GiB root volume. The router points at 1/2/4/8 of them;
  `postgres×1` = one of these (the stock baseline). Mirrors the Valkey pool exactly.
- **`aws_instance.pg_tuned`** (single) — `postgres:16` with tuned flags via command:
  `-c synchronous_commit=off -c shared_buffers=2GB -c max_connections=400 -c max_wal_size=4GB`,
  `--shm-size=2g`, 20 GiB root. Not sharded (the "did you tune it" preempt).
- **`aws_instance.valkey` count = `var.valkey_count`** (8). (Default stays 4 for
  run-cloud-1 reproducibility; set `TF_VAR_valkey_count=8`.)
- **Runner pool, split by role (no user-data; the script scp's a binary + runs
  worker or loadgen per assignment):**
  - **`aws_instance.worker_runner` count 3, `m5.2xlarge`** — pg-sharded-worker,
    pg-tuned-worker, valkey-worker. The bigger box removes the worker as a confound
    when *driving* valkey×8 (~180k/s) (OQ3 resolved).
  - **`aws_instance.producer_runner` count 6, `m5.xlarge`** — pg-sharded ×2,
    pg-tuned ×1, valkey ×3 (OQ1).
- **PG connection pool stays the M1 default across all PG configs** (apples-to-apples
  with run-cloud-1); *report* it and flag that PG's 1000-worker points may be
  pool-bound — itself part of the single-primary story (OQ2 resolved).
- **Outputs:** `pg_addrs_{1,2,4,8}` (the router DSN strings, same `slice(…,min(N))`
  pattern as valkey), `pg_tuned_dsn`, `valkey_addrs_{1,2,4,8}`, `worker_runner_ips`
  + `producer_runner_ips`.

Total boxes: 8 PG + 1 pg-tuned + 8 valkey + 9 runners = **26** (3 `m5.2xlarge`
workers + 23 `m5.xlarge`). Durability needs no infra — live `CONFIG SET appendonly
yes|no` + `appendfsync everysec|always` on one valkey instance between points.

---

## Orchestration — middle path (parallel PG + sequential Valkey)

```
PG-sharded ×1▶×2▶×4▶×8 (96 pts) sequential on the 8-PG pool   ┐
Valkey     ×1▶×2▶×4▶×8 (96 pts) sequential on the 8-valkey pool├ 3 tracks in
PG-tuned   ━━━━ (24 pts)                                       ┘ parallel
Durability ×1 off▶everysec▶always (6 pts) — tail, after the Valkey track
```

Three parallel tracks (separate box pools), shards sequential within each. Wall-clock
≈ slowest track = 96 pts × ~28s (20s window + 8s warmup) ≈ **45 min** (6 workers ×
4 process × 4 shard configs). Scripts:

- **`scripts/cloud/subsweep.sh`** (runs on a worker box): args = backend binary,
  label, connection env (a DSN-list for PG, an addr-list for Valkey). Loops process
  {0,2,20,200} × workers {1,10,30,100,300,1000}, each `PLQ_PRODUCERS=0
  PLQ_RESET=false … loadrun`, appends to local `sweep.csv`. For the sharded tracks it
  re-points the connection at 1/2/4/8 entries (the `sweep-postgres`/`sweep-valkey`
  Makefile loops gain a `POSTGRES_DSNS_SWEEP` mirroring `VALKEY_ADDRS_SWEEP`).
- **`scripts/cloud/producers.sh`** (runs on a producer box): `PLQ_PRODUCERS=$N
  loadgen` against the backend, continuous.
- **`scripts/cloud/run-cloud-2.sh`** (laptop coordinator): reads terraform
  outputs, assigns runners, scp's both binaries, launches the three tracks
  (PG-sharded, Valkey, PG-tuned) per the diagram, runs the durability tail, pulls +
  merges every worker box's `sweep.csv` into `results/run-cloud-2/sweep.csv`
  (+ `durability.csv`), graphs (faceted by process model).

*(Reuses the run-cloud-1 muscle memory: `DOCKER_CONFIG` workaround on datastore
boxes if needed, `AWS_PROFILE=praxis`, gated apply/destroy.)*

---

## Analysis & deliverables (`results/run-cloud-2/results.md`)

1. **Throughput vs workers** — faceted by process model (process=0 is the headline,
   9 series). **The punchline:** PG×1/2/4/8 and Valkey×1/2/4/8 both scale ~linearly
   (independent primaries), but Valkey's line sits ~15× higher per shard — so
   **~8 sharded Postgres primaries ≈ one Valkey primary**. "You *can* shard
   Postgres — but you'll operate 8–15 databases (backup, failover, pools, the
   routing layer) to match one Valkey node." The migration case in one chart.
   `postgres-tuned` (still plateaus) and `valkey×8` (trend holds) round it out.
2. **$/throughput** — peak acks/s ÷ datastore hourly cost. Basis: `m5.xlarge` ≈
   \$0.192/hr us-east-1; `postgres×N` = N boxes, `valkey×N` = N boxes (now a true
   per-box-cost comparison at equal shard count). Table: acks/s per \$-hour.
3. **p99-at-the-knee** — already in `sweep.csv` (`loop_p99_ms`); call out each
   config's p99 at its saturation point. The latency companion to the plateau.
4. **Durability table** — from `durability.csv`: throughput at off/everysec/always
   (the cost of each durability posture).

---

## Runtime & cost

~45 min sweep + ~10 min provision/setup/teardown ≈ 1 hr of uptime. **26 boxes**
(23× `m5.xlarge` ≈ \$0.19/hr + 3× `m5.2xlarge` ≈ \$0.38/hr ≈ \$5.5/hr) ≈ **\$6–7**
for the hour. Still well under the \$20 cap, with room for a rerun (blessed). The
PG-sharded pool (8 boxes) is the bulk of the bump vs the pre-A estimate — but it
buys the headline chart, and the router is verified locally first so the cloud
spend carries ~zero new risk.

---

## Dry-run plan (local, before any apply)

Correctness vs plumbing are different risks — do both:

1. **Router correctness (must, free):** the gated conformance suite against a
   **2-shard** local PG (`PLQ_TEST_POSTGRES="dsn1,dsn2"`) → **8/8**. This is the
   gate; it runs as part of "commit + verify, then the full suite in one go."
2. **Plumbing smoke (cheap insurance):** a **thin** local run (compose: 2 PG shards
   + 2 valkey shards) of the isolated topology — `PLQ_PRODUCERS=128 plq loadgen`
   then `PLQ_PRODUCERS=0 PLQ_RESET=false plq loadrun` — confirming: non-zero
   throughput + `saturated=true` + the queue is **not** wiped; the router actually
   splits workspaces across DSNs; the `shards` column reads 2 for `postgres×2`; the
   per-process facets render. *Not* the full sweep — just enough to catch a scripting/
   wiring bug before cloud (the class of bug run-cloud-1's smoke caught twice).
   *(Needs compose `postgres-2` on 5434.)*
3. **Tuned PG flags parse:** a local `postgres:16 -c synchronous_commit=off …`
   container; loadrun connects.
4. **`terraform plan`** with `TF_VAR_valkey_count=8 TF_VAR_pg_count=8` + the new
   resources — confirm counts (8 PG + 1 pg-tuned + 8 valkey + 3 worker-runners +
   6 producer-runners = 26) before apply.

Then gated apply → run-cloud-2 → charts + writeup → destroy.

---

## Tradeoffs

- **Isolated (producers=0 + external loadgen) vs integrated loadrun.** Isolated is
  production-match and defensible ("the worker box did nothing but work"); costs
  orchestration complexity + the `PLQ_RESET` gate. Chosen — rigor is the whole
  point of run-cloud-2. *Revisit:* if saturation proves impossible to guarantee
  cheaply, fall back to integrated with heavy producers (run-cloud-1 style).
- **Sharding PG via a multi-DSN router (A) vs external aggregation (B) vs leaving
  it out (C).** Chose **A**: it's dead-fair by construction (both backends shard
  through the *same* router), measured (not summed), and showcases the `Backend`
  abstraction. Cost: ~½-day of `internal/postgres` + a conformance re-verify + 8 PG
  boxes. B avoids the code but the workspace partitioning is fiddly and the total is
  summed; C leaves the "just shard your Postgres" rebuttal unanswered. *Revisit:* if
  the router proves hairy in local conformance, fall back to B (no cloud spent yet).
- **Three parallel tracks (~26 boxes, ~45 min).** Each backend family gets its own
  box pool so PG-sharded, Valkey, and PG-tuned run concurrently; shards are
  sequential within a track. Sequential-everything would be ~2 hr; full-parallel
  (every shard config its own pool) ~30+ boxes for little wall-clock gain. *Revisit:*
  collapse to fewer boxes if AMI capacity/quota bites.
- **Tuned PG = server config only (not driver retune).** We raise
  `synchronous_commit`/`shared_buffers`/etc. but leave the M1 driver's pool/batch
  as-is, for apples-to-apples with run-cloud-1. The pool size is a possible
  confound at 1000 workers — we keep it fixed and *report* it (OQ2). *Revisit:* if
  tuned PG is pool-bound, a pool bump is a fair follow-up — but the architectural
  plateau should show regardless.
- **Backend-label trick vs a `variant` CSV column.** The label trick
  (`postgres-tuned`, `valkey-always`) is zero-code and enough for one run. A
  `variant` column is cleaner and composable. *Revisit:* if variants become a
  standing dimension, add the column (Result + CSV + plot series key).
- **Durability off/everysec/always, no WAITAOF.** The three are free (live CONFIG
  SET). WAITAOF (per-write synchronous AOF ack) needs a gated driver write-path
  change. Deferred — see Out of Scope. *Revisit:* if the durability story needs a
  "synchronous" datapoint, build the WAITAOF path.
- **Generic runner pool vs named role boxes.** Generic `count`-based pool keeps
  terraform simple and reusable; the script owns role assignment. Named resources
  would be more self-documenting in `terraform output`. Chose flexibility.

---

## Open Questions

**Resolved (folded into the body 2026-06-27):** producer firepower → 3 producer
boxes for the Valkey track (saturation protocol reruns any unsat point); PG pool →
keep M1 default across stock+tuned, report it, flag the 1000-worker confound;
worker box → `m5.2xlarge` for the 3 worker-runners; worker granularity → added 30
& 300 (now 1/10/30/100/300/1000).

**RESOLVED — shard Postgres too: yes, option A (multi-DSN router), router-first
(2026-06-27).** Baked into the matrix (9 configs / 216 points), the Code-changes
section (the ½-day router + conformance gate, built first), Infra (8-PG pool), and
Orchestration (3 parallel tracks). Build approach: commit the router + verify
correctness, then run the full suite in one go; smoke question answered in the
Dry-run plan (conformance is the correctness gate; a thin 2-shard local smoke is
cheap plumbing insurance before cloud — do both).

*No open questions remain — the design is ready for `/ship` once Phil's happy.*

## Out of Scope

- **WAITAOF / synchronous-replication durability** — deferred (needs driver change).
- **Redis Cluster** — independent primaries + `hash(workspace)%N` remains the model.
- **Driver retuning** (pool size, batch, pipelining) — server-config tuning only.
- **Multi-AZ / network-latency dimension** — same-AZ, like run-cloud-1.
- **The Honcho PR** — separate milestone (M-PR).
