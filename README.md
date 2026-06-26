# pl-takehome-technical — buffered work-unit queue

A buffered, work-unit-aware queue: tasks accrue a token cost and are gated until a
work unit crosses a threshold (or its flush age-cap fires), then drained
**exclusively, in order** by one worker — many units in parallel. Built on two
backends behind one interface so they can be measured head-to-head.

> **Status:** M0 scaffold. The contract, the shared worker loop, the in-memory
> oracle, config, and the CLI are real and tested. Postgres (M1) and Valkey (M3)
> drivers and the load-test harness (M2) are stubbed. See
> [`.meta/roadmap.md`](.meta/roadmap.md).

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
make build    # compile + typecheck the shared code (default in-memory build)
make test     # run the M0 proofs against the in-memory oracle
make up       # bring up the postgres path locally (functional from M1)
```

`make load-test` is the one-command reproducibility promise (the worker sweep +
throughput-vs-workers graph); it lands in M2.

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
