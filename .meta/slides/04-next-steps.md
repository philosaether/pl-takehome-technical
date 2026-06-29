# One run is not enough
Initial results provide a proof-of-concept and validate expectations, but they do
 *not* provide absolute numbers we can base production decisions on.

To get those numbers, we must:

## Rerun the benchmark test on a canonical cloud box
- Local testing (per the terms of the brief) produces jittery, unreliable numbers
- The ~1000x order-of-magnitude improvement is consistent (and consistent with theory),
- But a laptop is not a stable and reliable test bench
- We should redo this test on real node architecture to get precise values

## Rerun the head-to-head with:

### More data points
- A sweep at every 2*10^N ms of simulated work verified the topology of the landscape,
- but we still don't know the exact edge of the canyon.
- To pinpoint *exactly* where Valkey starts paying off, we need a higher-fidelity scan.

### Longer data collection periods per point
- We ran each data point for 20s on the cloud box
- That's not enough to start seeing performance loss from vacuum pressure on the Postgres nodes
- So, Postgres is likely overperforming in the canonical run.

### Better saturation for Postgres nodes
- On the other hand, most of our Postgres data points are unsaturated
- That means queue depth hit zero at least once during the run; i.e., acks/s is producer-bound
- So, Postgres is likely underperforming in the canonical run.

Since these two causes will have exactly opposite effects, it's vital that we rerun the test
and see how they cancel out before deciding on exact cutover conditions.