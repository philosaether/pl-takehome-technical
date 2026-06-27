# M3 head-to-head — canonical cloud results

Run: 2026-06-27, AWS us-east-1. 7 boxes (worker + producer + pg + 4× valkey),
all `m5.xlarge` except producer (`m5.large`). Integrated `loadrun` on the worker
box (producers+workers in one process, 256 producers), 15s/point + 3s warmup.
Provisioned + torn down via `deploy/terraform` (`make cloud-up`/`cloud-down`);
total uptime ~20 min, ≪ $1.

Artifacts here: `sweep.csv` (raw), `throughput.png`, `latency.png`. Regenerate
the charts with `python3 scripts/plot.py .meta/assessments/m3-head-to-head`.

## The decision-gate proof (zero-process — the queue's own ceiling)

| backend   | peak acks/s | vs PG  |
|-----------|-------------|--------|
| postgres  | ~1,723      | 1×     |
| valkey×1  | ~25,771     | ~15×   |
| valkey×2  | ~49,301     | ~29×   |
| valkey×4  | ~92,064     | ~53×   |

- **Postgres plateaus** at ~1.7k acks/s (100 workers) and *declines* by 1000
  workers (~1.4k) — claim contention. This is the wall the take-home is about.
- **Valkey scales near-linearly with shards**: 25.8k → 49.3k → 92.1k = 1.9× then
  3.6× (4 shards). Independent primaries + `hash(workspace)%N` routing deliver the
  linear scaling the design promised.

## Cost-mode (20ms simulated work) — the secondary story

At 20ms/task the throughput is work-bound (worker_count / 0.02s ceiling), so it
*cancels* between backends — except where a backend can't feed its workers:
- 100 workers, 20ms: all backends ≈ 4.8–5.0k (the 5k work ceiling). Parity.
- 1000 workers, 20ms: Valkey reaches ~47–49k (≈ the 50k ceiling); **Postgres
  stalls at ~1.4k** — it can't keep 1000 workers fed. The claim funnel, not the
  work, is PG's limit.

## Caveats (why this is the *shape*, not an absolute SLA)

- Integrated loadrun co-locates producers with workers on one box, so absolute
  acks/s are suppressed vs a producer-isolated topology — but identically for both
  backends, so the comparison holds. (The producer box is provisioned for the
  worker-alone model; this run used the Makefile's integrated harness for
  apples-to-apples with the local sweep.)
- Low-worker Valkey points are producer/queue-fill-bound (huge backlog, workers
  starved on count not backend) — read the 100/1000-worker points for the ceiling.
