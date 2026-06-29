# Valkey's faster, so why not switch?

Valkey offers a 15x performance improvement over Postgres only for the *zero-cost task*
scenario, when throughput is entirely bounded by the queue.

As per-task time-to-process increases, the queue architecture becomes less and less of
a bottleneck, and Valkey's performance lead vanishes to near parity at 200ms per task.

| per-task work | postgres×1 | valkey×1 | ratio |
|---------------|-----------|----------|-------|
| zero          | ~2,200    | ~33,100  | ~15× |
| 2 ms          | ~2,900    | ~32,800  | ~11×  |
| 20 ms         | ~4,900    | ~29,800  | ~6×   |
| 200 ms    | ~5,200| ~5,700 | ~1.1× |

At LLM-scale latency, with unoptimized, remote models, Valkey is very unlikely to
offer a meaningful improvement to performance. Getting to a degree of worker-unit
optimization where Valkey becomes worth it is a development goal in itself.

## Our recommendation

- **Ship the look-ahead fix immediately.** It's a Postgres-side change representing
  about 150 lines in Honcho's existing code, with no new infrastructure, and it nets
  a ~1000x performance increase over today's implementation at 10⁷ pending tasks.
- **Start tracking production metrics.** Valkey pays off at high worker count and
  low time-to-process. Those are both metrics worth tracking in their own right: one 
  represents our adoption rates and should be a KPI for the business anyway, while the
  other represents the efficiency of our worker processes and directly correlates to
  infrastructure spend.
- **Let Valkey be a reward.** If Honcho ever enters the domain where Valkey offers a
  performance boost, it means Plastic Labs has already succeeded as a business. A
  Valkey queue provides the infrastructure layer for millions of concurrent observations
  being carried out in tens of milliseconds each: that's a goal worth striving for.