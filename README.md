# pl-takehome-technical — buffered work-unit queue

A buffered, work-unit-aware queue: tasks accrue a token cost and are gated until a
work unit crosses a threshold (or its flush age-cap fires), then drained
**exclusively, in order** by one worker — many units in parallel. Built on two
backends behind one interface so they can be measured head-to-head.

> **Status:** M0 scaffold + M1 Postgres driver + M2 load harness & proofs (local
> half). The contract, worker loop, in-memory oracle, the Postgres backend, the
> Zipfian load generator, metrics, and the deterministic proofs are real and
> tested. Headline so far: the maintained-aggregate look-ahead stays ~flat
> (0.35→0.48 ms) while the naive `SUM…GROUP BY` grows with task count
> (6.4→74 ms) — **154× at 10⁶ tasks**. The canonical AWS sweep (M2 cloud half) and
> Valkey (M3) are next. See [`.meta/roadmap.md`](.meta/roadmap.md).

## What & why

Honcho's queue runs a `SUM…GROUP BY` on every poll to find eligible work — it
won't survive two more orders of magnitude. We replace it with a maintained
per-unit aggregate (the look-ahead stays O(log n)), then justify a Valkey upgrade
on throughput + latency with a measured head-to-head. The design docs:
[`postgres-work-unit-queue`](.meta/designs/postgres-work-unit-queue.md),
[`valkey-work-unit-queue`](.meta/designs/valkey-work-unit-queue.md),
[`scaffold`](.meta/designs/scaffold.md).

## Quickstart

```sh
make build      # compile + typecheck the shared code (default in-memory build)
make test       # unit + conformance tests (in-memory oracle)
make up         # bring up a local postgres for the sweep
make proofs     # ordering + look-ahead proofs (set PLQ_TEST_POSTGRES for the PG + 10^6 bench)
make load-test  # the integrated local sweep → results/sweep.csv → results/*.png (needs matplotlib)
```

The canonical numbers run on AWS via `deploy/terraform/` (`make cloud-up` /
`make cloud-down`) — the gated, spend-incurring step; everything above is verified
locally against a Docker Postgres first.

## Architecture

One binary (`cmd/plq`), three subcommands (`worker | loadgen | reap`). The driver
is chosen at **build time** via `-tags` (`postgres` / `valkey`; default = the
in-memory backend) — each image ships exactly one driver.

```
internal/queue     the contract: Backend interface + the shared worker loop
                   (imports no driver — that's the load-bearing invariant)
internal/memory    in-memory Backend — dev, CI, and the correctness oracle
internal/postgres  Path 1 driver (M1)        internal/valkey  Path 2 driver (M3)
internal/loadgen   producers + metrics (M2)  internal/config  the tunable surface
cmd/plq            CLI + build-tagged newBackend wiring
```

The **only** thing that differs between Path 1 and Path 2 is the `Backend`
implementation. The worker loop, load generator, proofs, and metrics are identical
code — that's what makes the head-to-head a fair fight.

## The four proofs (deterministic)

Ordering-under-crash · gate (nothing runs below T except via flush) · flush
(age-cap fires) · look-ahead cost (~flat vs task count). M0 ships the smoke
versions of ordering + gate in `internal/queue/worker_test.go`; the full set lands
in M2 under `proofs/`.

## Results

Throughput-vs-workers graphs + the migration-trigger numbers land in M2/M4
(`./results`).

## Migration triggers (the watch-numbers)

When to cut over from Postgres to Valkey — the metrics to watch and the values
that say "switch." Derived from the head-to-head and the process-model sweep; see
M4 in [`.meta/roadmap.md`](.meta/roadmap.md).
