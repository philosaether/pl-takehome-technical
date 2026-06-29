# Two engines, one topology

The obvious rebuttal to "Valkey is faster" is "just shard your Postgres."
So, we sharded both.

## The numbers

Our canonical run, `run-cloud-2`, ran on AWS m5.large for both engines. It produced these results for the zero-process peak:

| config | peak acks/s | vs PG×1 |
|--------|-------------|---------|
| postgres×1 | ~2,200 | 1× |
| postgres×2 | ~3,660 | 1.7× |
| postgres×4 | ~6,510 | 3.0× |
| postgres-tuned×1 | ~10,000 | 4.5× |
| valkey×1 | ~33,100 | 15× |
| valkey×2 | ~69,600 | 32× |
| valkey×4 | ~142,100 | 65× |

## The meaning
- **Both engines shard linearly.** Sharding Postgres does improve performance
 (roughly) linearly, but it scales from a base that's ~15× lower.
- **Per-primary gap ≈ 15×** Matching Valkey×4 would take **~20+ Postgres primaries**,
which represents 5x more surface area for failure scenarios.
- **Tuning Postgres is not enough.** Tuned PG×1 (~10k) is still ~3.3× behind
  Valkey×1, and *declines* as worker count climbs, due to claim contention.