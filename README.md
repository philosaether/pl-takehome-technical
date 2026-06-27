# pl-takehome-technical — buffered work-unit queue

A buffered, work-unit-aware queue: tasks accrue a token cost and are gated until a
work unit crosses a threshold (or its flush age-cap fires), then drained
**exclusively, in order** by one worker — many units in parallel. Built on two
backends (Postgres and Valkey) behind one interface, so they can be measured
head-to-head as a fair fight.

> **The 1-page writeup is [`one-pager/one-pager.pdf`](one-pager/one-pager.pdf)** —
> the findings + the recommendation. Everything below is how to run the system and
> reproduce the numbers.

## The result, in one line

Honcho's scheduler answers "which work units are eligible?" with a `SUM…GROUP BY`
over the queue on every poll — an `O(tasks)` scan. We replace it with a maintained
per-unit aggregate read through a partial index, so the look-ahead stays flat as
the backlog grows:

- **Look-ahead:** ~0.3–1.9 ms from 10⁴ to 10⁷ pending tasks vs the naive scan's
  2,524 ms at 10⁷ — **1,364×** (`results/lookahead/`).
- **Head-to-head (cloud, AWS, torn down for < $20 total):** sharded both backends
  through the same `hash(workspace)%N` router. Postgres plateaus and Valkey scales
  ~linearly — **~15× per primary** (`results/run-cloud-1/`, `results/run-cloud-2/`).

## Quickstart

```sh
make build      # compile + typecheck the shared code (default in-memory build)
make test       # unit + conformance tests (against the in-memory oracle)
make up         # bring up a local Postgres
make proofs     # ordering/gate/flush proofs + the look-ahead bench
                #   (set PLQ_TEST_POSTGRES to run the PG path + the 10^7 curve)
make load-test  # integrated local sweep → results/sweep.csv → results/*.png (needs matplotlib)
```

Valkey path: `make up-valkey`, `make proofs-valkey`, `make load-test-valkey`. The
canonical sharded numbers run on AWS via `deploy/terraform/` (`make cloud-up` /
`make cloud-down`) — the gated, spend-incurring step; everything else runs on a
laptop against Docker first.

## Architecture

One binary (`cmd/plq`), five subcommands
(`worker | loadgen | loadrun | reap | reset`). The driver is chosen at **build
time** via `-tags` (`postgres` / `valkey`; default = the in-memory backend) — each
image ships exactly one driver.

```
internal/queue     the contract: Backend interface + the shared worker loop
                   (imports no driver — that's the load-bearing invariant)
internal/memory    in-memory Backend — dev, CI, and the correctness oracle
internal/postgres  Path 1 driver           internal/valkey  Path 2 driver
internal/loadgen   producers + metrics     internal/config  the tunable surface
cmd/plq            CLI + build-tagged newBackend wiring
```

The **only** thing that differs between Path 1 and Path 2 is the `Backend`
implementation. The worker loop, load generator, proofs, and metrics are identical
code — that's what makes the head-to-head fair.

## The four proofs (deterministic)

Ordering-under-crash · gate (nothing runs below T except via flush) · flush
(age-cap fires) · look-ahead cost (~flat vs task count). They live in
`tests/conformance/` (gate + flush, both backends vs the oracle), `tests/proofs/`
(ordering under crash), and `tests/bench/` (the look-ahead curve). Run via
`make proofs` / `make proofs-valkey`.

## Results

Reproducible run buckets in [`results/`](results/) (tracked — cloud numbers cost
money to regenerate):

- `lookahead/` — the maintained look-ahead vs the naive scan, 10⁴→10⁷.
- `run-cloud-1/` — first PG-vs-Valkey head-to-head (sharded Valkey).
- `run-cloud-2/` — both backends sharded 1/2/4 through one router, + a process-time
  sweep {0,2,20,200 ms}; the data behind the one-pager's manifold.
- `run-local-1/` — the local sweep used to validate the harness before cloud spend.

Each bucket has a `results.md` with the headline numbers and the caveats.
