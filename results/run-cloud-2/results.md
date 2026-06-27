# run-cloud-2 — sharded head-to-head (PG vs Valkey, both sharded)

Run: 2026-06-27, AWS us-east-1. **Quota-constrained** to 32 vCPU → m5.large boxes,
4-shard cap (not the designed 8 / m5.xlarge). 16 boxes: 4 sharded-PG + 1 tuned-PG +
4 valkey datastores, 3 worker runners + 4 producer runners. Isolated topology
(`plq loadrun PLQ_PRODUCERS=0` measuring a worker pool against continuous external
`plq loadgen`). Provisioned/torn down via `deploy/terraform`; total spend across all
runs (incl. ~7 debugging cycles) ≈ a few dollars.

Artifacts: `sweep.csv` (raw), per-process `throughput-*.png` / `latency-*.png`.
Headline = `throughput-zero.png`.

## The result — peak throughput (zero-process, acks/s)

| config            | peak acks/s | vs postgres×1 |
|-------------------|-------------|---------------|
| postgres×1        | ~2,200      | 1×            |
| postgres×2        | ~3,660      | 1.7×          |
| postgres×4        | ~6,510      | 3.0×          |
| postgres-tuned×1  | ~10,000     | 4.5×          |
| valkey×1          | ~33,100     | **15×**       |
| valkey×2          | ~69,600     | 32×           |
| valkey×4          | ~142,100    | **65×**       |

## What it shows

1. **Both backends shard ~linearly** (the fair-comparison payoff of the shared
   `hash(workspace)%N` router): PG 1→2→4 = 2.2k→3.7k→6.5k (~3× at 4 shards);
   Valkey 1→2→4 = 33k→70k→142k (~4.3× at 4 shards, near-perfect). So "just shard
   your Postgres" is *real* — and now answered with data.
2. **…but the per-primary gap dominates.** Valkey is **~15× Postgres per shard**.
   The migration case in one line: **one Valkey primary (~33k) ≈ five sharded
   Postgres primaries (×4 = 6.5k)**; matching Valkey×4 (~142k) would take **~20+
   Postgres primaries** — i.e. 20+ databases to operate, back up, fail over, and
   route across, vs 4 Valkey nodes.
3. **Tuning Postgres helps, but doesn't change the verdict.** tuned-PG×1 peaks
   ~10k (4.5× stock PG×1) at *low* concurrency, then **declines** to ~7.3k by 1000
   workers (claim contention) — it never approaches even Valkey×1. Preempts "you
   didn't tune PG": we did; it's still an order of magnitude short.

## Caveats (read these with the numbers)

- **m5.large / 4 shards, not the designed m5.xlarge / 8** (AWS 32-vCPU quota). So
  absolutes are NOT comparable to run-cloud-1; internal PG-vs-Valkey consistency is
  what holds. An 8-shard m5.xlarge run needs a quota bump (the code already supports
  it — `TF_VAR_pg_count=8 TF_VAR_valkey_count=8`, full `m5.xlarge`/`m5.2xlarge`).
- **Postgres points are producer-bound lower bounds** (`saturated=false`): the
  single m5.large producer per PG track couldn't always keep PG's queue full, so
  true PG throughput may be modestly higher than shown. The flat ceiling across
  1→1000 workers (PG×1 ≈ 2.2k throughout) suggests it's close to the real
  single-primary limit regardless. **Valkey points are saturated=true** (solid).
- **Durability sub-experiment FAILED this run** — the live `CONFIG SET` via
  `docker exec` returned permission errors (ec2-user not in the docker group →
  needs `sudo docker exec`; fixed in the script for next time). The off/everysec/
  always points are therefore invalid (all the same fsync) and excluded.
- Data-handling note: the valkey track rows initially landed in `durability.csv`
  (the durability `mv` clobbered the shared worker's `sweep.csv`); re-sorted into
  `sweep.csv` post-hoc, and the script bug is fixed for future runs.
