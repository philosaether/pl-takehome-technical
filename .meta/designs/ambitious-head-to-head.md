---
Status: draft
Date: 2026-06-27
Assessment: results/run-cloud-1/results.md (the run this upgrades)
Likely-supersedes: (none — extends loadgen-and-proofs.md + valkey-driver.md)
---

# Ambitious head-to-head (run-cloud-2) — Desired State

Turn run-cloud-1's clean-but-soft proof into the airtight version: an
**isolated, saturation-guaranteed** PG-vs-Valkey sweep that scales Valkey to
**8 shards**, includes a **tuned-Postgres baseline** (to preempt "you didn't
tune PG"), maps the **durability tradeoff** (fsync off/everysec/always), and
covers the full **process sweep (0/2/20/200ms)** — then reports **$/throughput**
and **p99-at-the-knee** alongside raw throughput.

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
| Stock `postgres:16` invites "did you even tune it?" | **Tuned-PG baseline** |
| "But Redis loses data" unanswered | **Durability tradeoff** curve |
| Only zero + 20ms work | **Process sweep** 0/2/20/200ms |
| No business framing | **$/throughput** + **p99-at-knee** analysis |

---

## The matrix

**Main sweep** — 6 configs × 4 process × 4 workers = **96 points**:
- configs: `postgres` (stock), `postgres-tuned`, `valkey×1`, `valkey×2`, `valkey×4`, `valkey×8`
- process: `0` (zero), `2ms`, `20ms`, `200ms` (LLM-call-scale)
- workers: `1`, `10`, `100`, `1000`

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
target is `valkey×8` / process=0 / 1000 workers (~180k/s extrapolated) — budget
2+ producer boxes for the Valkey track.

**Coordination.** Per backend: `plq reset` once, start `loadgen`, let the queue
fill past the worker's appetite, then run the 16 (process×worker) points back to
back with `PLQ_RESET=false` so the standing depth persists between points.

---

## Code change (the only one): `PLQ_RESET` gate

`runLoadrun` unconditionally calls `Reset()` — which would wipe the queue the
external producers are filling. Add a gate:

- `internal/config/config.go`: new `Reset bool` field; `Reset: boolEnv("PLQ_RESET", true)`
  (needs a `boolEnv` helper alongside `atoi`/`dur`/`env` — `"0"/"false"` → false).
- `cmd/plq/main.go` `runLoadrun`: wrap the existing reset in `if cfg.Reset { … }`.

Default `true` → fully back-compat (run-cloud-1 / local sweeps unchanged). The
worker boxes set `PLQ_RESET=false`.

**Series labels without new columns (zero-code trick).** `Result.Backend` is set
from `PLQ_BACKEND` (a label; the real driver is compiled-in via build tag). So:
- tuned PG rows get `PLQ_BACKEND=postgres-tuned` → distinct series, no code.
- durability rows get `PLQ_BACKEND=valkey-off|valkey-everysec|valkey-always`,
  written to a **separate** `durability.csv` so they don't pollute the main chart.

*(A first-class `variant` column would be cleaner long-term — see Tradeoffs. For
one run, the label trick is enough and touches no Go.)*

---

## Infra (terraform)

Keep terraform simple: provision **datastores explicitly** + a **generic runner
pool**; let the orchestration script assign runner roles.

- **`aws_instance.pg`** (stock, unchanged) + **`aws_instance.pg_tuned`** — new,
  `postgres:16` with tuned flags via command:
  `-c synchronous_commit=off -c shared_buffers=2GB -c max_connections=400 -c max_wal_size=4GB`,
  `--shm-size=2g`, same 20 GiB root volume.
- **`aws_instance.valkey` count = `var.valkey_count`** — set `TF_VAR_valkey_count=8`
  for this run (default stays 4 for run-cloud-1 reproducibility).
- **`aws_instance.runner` count = `var.runner_count`** (default 7) — generic
  `m5.xlarge` compute, no user-data; the script scp's a binary + runs worker or
  loadgen per assignment. Middle-orchestration needs: pg-stock {worker,producer},
  pg-tuned {worker,producer}, valkey {worker, 2× producer} = 7.
- **Outputs:** `pg_stock_dsn`, `pg_tuned_dsn`, `valkey_addrs_{1,2,4,8}`,
  `runner_public_ips` (list). (Add `_8`; the `slice(..., min(8, len))` pattern
  already generalizes.)

Total boxes: 2 PG + 8 valkey + 7 runners = **17**. Durability needs no infra —
live `CONFIG SET appendonly yes|no` + `appendfsync everysec|always` on one valkey
instance between points.

---

## Orchestration — middle path (parallel PG + sequential Valkey)

```
PG-stock  ━━━━ (16 pts) ┐
PG-tuned  ━━━━ (16 pts) ┘ run in parallel (independent box sets)
Valkey ×1▶×2▶×4▶×8 (64 pts) sequential on the shared 8-box pool
                            (×1 uses 1 of the 8, ×2 uses 2, …)
Durability  ×1 off▶everysec▶always (6 pts) — tail, after the Valkey track
```

Wall-clock ≈ slowest track = Valkey 64 pts × ~28s (20s window + 8s warmup) ≈
**30 min**. Scripts:

- **`scripts/cloud/subsweep.sh`** (runs on a worker box): args = backend binary,
  label, connection env. Loops process {0,2,20,200} × workers {1,10,100,1000},
  each `PLQ_PRODUCERS=0 PLQ_RESET=false … loadrun`, appends to local `sweep.csv`.
- **`scripts/cloud/producers.sh`** (runs on a producer box): `PLQ_PRODUCERS=$N
  loadgen` against the backend, continuous.
- **`scripts/cloud/run-cloud-2.sh`** (laptop coordinator): reads terraform
  outputs, assigns runners, scp's binaries, launches the two PG tracks +
  Valkey track per the diagram, runs the durability tail, pulls + merges every
  worker box's `sweep.csv` into `results/run-cloud-2/sweep.csv`
  (+ `durability.csv`), graphs.

*(Reuses the run-cloud-1 muscle memory: `DOCKER_CONFIG` workaround on datastore
boxes if needed, `AWS_PROFILE=praxis`, gated apply/destroy.)*

---

## Analysis & deliverables (`results/run-cloud-2/results.md`)

1. **Throughput vs workers** — the main overlay, now 6 configs × 4 process. The
   headline chart adds `postgres-tuned` (expected: still plateaus) and `valkey×8`
   (expected: ~2× the ×4 ceiling — the trend holds).
2. **$/throughput** — peak acks/s ÷ datastore hourly cost. Basis: `m5.xlarge` ≈
   \$0.192/hr us-east-1; PG configs = 1 box, valkey×N = N boxes. Table: acks/s
   per \$-hour. (The "less iron" talking point, quantified — Valkey×1 likely wins
   even per-dollar despite needing the same box class.)
3. **p99-at-the-knee** — already in `sweep.csv` (`loop_p99_ms`); call out each
   config's p99 at its saturation point. The latency companion to the plateau.
4. **Durability table** — from `durability.csv`: throughput at off/everysec/always
   (the cost of each durability posture).

---

## Runtime & cost

~30 min sweep + ~10 min provision/setup/teardown ≈ 45 min of uptime. 17 boxes ×
~\$0.19/hr × 0.75hr ≈ **\$2.4**; call it **~\$3–4** with margin. Well under the
\$20 cap, with room for a rerun (which Phil has blessed).

---

## Dry-run plan (local, before any apply)

Validate only the *new* mechanics locally (compose):
1. **Isolated measurement:** start `PLQ_PRODUCERS=128 plq loadgen` against compose
   postgres; then `PLQ_PRODUCERS=0 PLQ_RESET=false PLQ_WORKERS=10 plq loadrun` —
   confirm non-zero throughput, `saturated=true`, and the queue is **not** wiped.
2. **Tuned PG flags parse:** run a local `postgres:16 -c synchronous_commit=off …`
   container; conformance/loadrun connects.
3. **`terraform plan`** with `TF_VAR_valkey_count=8` + the new resources — confirm
   counts (2 PG + 8 valkey + 7 runners) before apply.

Then gated apply → run-cloud-2 → charts + writeup → destroy.

---

## Tradeoffs

- **Isolated (producers=0 + external loadgen) vs integrated loadrun.** Isolated is
  production-match and defensible ("the worker box did nothing but work"); costs
  orchestration complexity + the `PLQ_RESET` gate. Chosen — rigor is the whole
  point of run-cloud-2. *Revisit:* if saturation proves impossible to guarantee
  cheaply, fall back to integrated with heavy producers (run-cloud-1 style).
- **Middle orchestration vs sequential / full-parallel.** Sequential is simplest
  but ~45–60 min + babysitting; full-parallel is ~15 min but ~29 boxes and the
  most failure surface. Middle balances both (~17 boxes, ~30 min). *Revisit:* go
  full-parallel if 30 min proves annoying across reruns.
- **Tuned PG = server config only (not driver retune).** We raise
  `synchronous_commit`/`shared_buffers`/etc. but leave the M1 driver's pool/batch
  as-is, for apples-to-apples with run-cloud-1. The pool size is a possible
  confound at 1000 workers (see Open Questions). *Revisit:* if tuned PG is pool-
  bound, a pool bump is a fair follow-up — but the architectural plateau should
  show regardless.
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

1. **Producer firepower for valkey×8 / process=0 / 1000 workers.** Two producer
   boxes enough to keep ~180k/s saturated, or a third? Determined empirically via
   the saturation protocol; budget for a 3rd.
2. **PG connection-pool as a confound** at 1000 workers — measure/note, or bump
   the pool for tuned PG only? (Leaning: note it, keep apples-to-apples.)
3. **Worker-box instance type.** Is one `m5.xlarge` worker box enough to *drive*
   valkey×8 (~180k/s), or does the worker box itself bottleneck? Maybe
   `m5.2xlarge` for the valkey-worker. Empirical.
4. **Finer worker granularity near the PG knee** (add 30, 300)? Sharpens the
   plateau but +points. Optional.

## Out of Scope

- **WAITAOF / synchronous-replication durability** — deferred (needs driver change).
- **Redis Cluster** — independent primaries + `hash(workspace)%N` remains the model.
- **Driver retuning** (pool size, batch, pipelining) — server-config tuning only.
- **Multi-AZ / network-latency dimension** — same-AZ, like run-cloud-1.
- **The Honcho PR** — separate milestone (M-PR).
