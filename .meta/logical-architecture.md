# Logical Architecture

Codebase map for navigation and review. Maintained by the team; corrected over
time. Reflects **M0–M3 complete** — both real drivers (Postgres M1, Valkey M3) and
the load-test harness (M2) are built, merged, and run head-to-head on cloud.

## The one idea

Everything hangs off a single seam: **`queue.Backend`**. The worker loop, the load
generator, the proofs, and the metrics depend *only* on that interface; the two
real backends (Postgres, Valkey) are the *only* thing that differs between paths.
That identity is what makes the head-to-head a fair fight.

**Load-bearing invariant:** `internal/queue` imports no driver. Drivers depend on
`queue`; never the reverse. If `queue` ever imports `internal/postgres` or
`internal/valkey`, the contract has leaked.

## Dependency direction

```
            cmd/plq  (CLI + build-tagged newBackend wiring)
            /   |   \
   internal/config  internal/loadgen   internal/{memory,postgres,valkey}  (drivers)
            \   |   /                          |
             internal/queue  ◄─────────────────┘   (the contract; stdlib only)
```

- `queue` is the leaf: it imports only the standard library.
- `config` and the drivers import `queue`. `config` does **not** import any driver;
  drivers do **not** import `config` (the CLI bridges them via `newBackend`).
- `cmd/plq` is the only place that knows a concrete driver — and only one at a
  time, chosen by build tag.

## Packages

| Path | Role | State |
|---|---|---|
| `internal/queue` | The contract: `Backend` interface, core types, the shared worker loop. | **real** |
| `internal/memory` | In-memory `Backend` — dev, CI typecheck, **correctness oracle**. | **real** |
| `internal/config` | Volatility-split tunables from `PLQ_*` env; projects `WorkerConfig`. | **real** |
| `internal/loadgen` | Zipfian-churn producers, the `loadrun` harness, metrics (`queue.Recorder`), chaos. `Result`/`sweep.csv` carry a `shards` dimension (the head-to-head series key, with `backend`). | **real** |
| `internal/postgres` | Path 1 driver — all 8 methods + `Stater`/`Resetter`; **multi-DSN N-shard routing by `hash(workspace)%N`** (run-cloud-2), the same router shape as valkey. | **real** |
| `internal/valkey` | Path 2 driver — Streams+ZSET+Hash+Lua via rueidis; all 8 methods + `Stater`/`Resetter`; N-shard routing by workspace. | **real** |
| `cmd/plq` | CLI (`worker\|loadgen\|loadrun\|reap\|reset`) + build-tagged `newBackend`. | **real** |
| `scripts/cloud` | run-cloud-2 orchestration: `run-cloud-2.sh` (laptop coordinator), `subsweep.sh` (worker box, isolated `PLQ_PRODUCERS=0` loop), `producers.sh` (producer box, continuous loadgen). | **real** |
| `tests/conformance` | The contract suite (8 scenarios) run vs memory + PG (1+N shards) + Valkey. | **real** |
| `tests/proofs` | Ordering-under-crash (correctness) + the `claimRetry` harness. | **real** |
| `tests/bench` | Look-ahead scaling bench (its own binary; PG-gated). | **real** |

Tests: unit tests live next to their code (`internal/queue/worker_test.go`). The
**cross-backend integration suites** live under `tests/` — and each runs in its own
`go test` invocation (`make proofs`) so they never hit the shared DB concurrently.

## Key files

- `internal/queue/backend.go` — the **`Backend` interface** (8 methods:
  Enqueue/Claim/Drain/Ack/Release/Heartbeat/Fail/ReapExpired) + `ErrLeaseLost`.
  Every method maps 1:1 onto an operation in the accepted designs.
- `internal/queue/types.go` — `WorkUnitKey` (workspace = shard + fairness seam),
  `Task` (seq/payload/cost), `ClaimedUnit`, `WorkerID`, `LeaseToken`.
- `internal/queue/worker.go` — the shared loop: `Worker.runOnce` (claim → drain →
  process → ack), heartbeat goroutine (~Lease/3), `ProcessModel` (zero|fixed|cost,
  the cutover axis), `BackoffConfig`, `WorkerConfig` (with the `OnProcess` proof
  hook), and `Pool` (N workers).
- `internal/queue/unimplemented.go` — `Unimplemented` stub the M1/M3 drivers embed
  so they satisfy `Backend` before every method exists.
- `internal/memory/backend.go` — the working oracle; one mutex, a `map[key]*unit`.
  Honors: implicit unit creation, maintained `pendingCost`, the eligibility gate,
  age-fair claim (lowest `flushDeadline`, `wu_key` tiebreak), delete-on-ack with
  keep-or-release, flush age-cap, lease reclaim, poison→DLQ.
- `internal/config/config.go` — `Config` (all `PLQ_*` knobs) + `WorkerConfig()`.
- `cmd/plq/main.go` — subcommand dispatch; runs the reaper in-process with workers.
- `cmd/plq/backend_{memory,postgres,valkey}.go` — build-tagged `newBackend`
  (`!postgres && !valkey` = memory default).

## Lifecycle (the worker loop)

```
Claim(worker, lease) ─► nil? back off (adaptive) and retry
       │ got a ClaimedUnit (exclusive lease)
       ▼
   ┌─ Drain(unit, batch) ─► []Task in seq order
   │       │ empty? Release and done
   │       ▼  start heartbeat (Lease/3)
   │   process each task (OnProcess hook, then ProcessModel sleep)
   │       ▼  stop heartbeat
   │   Ack(unit, throughSeq) ─► stillHeld?
   └────── yes: loop      no: unit released/emptied, done
```

Gate: a unit is claimable only when `pendingCost ≥ threshold` **or** flush-promoted
(age-cap via `ReapExpired`). Exclusivity: the lease (`held` check) — not any hash.

## Build & run

- Driver = **build tag**: `go build` (memory) · `-tags postgres` · `-tags valkey`.
  Each binary contains exactly one driver.
- One binary `plq`, subcommands `worker | loadgen | reap`.
- Config via `PLQ_*` env vars (see `config.go` for the full list + defaults).
- `make build | test | vet | proofs`; `make up` (postgres path) / `make up-valkey`
  (4 local Valkey instances for the shard sweep); `make load-test` /
  `load-test-valkey` / `head-to-head` (the M2/M3 sweep + graph — real).
- **Cloud:** `deploy/terraform` provisions the AWS boxes; `make cloud-up`/`cloud-down`
  are the gated apply/destroy. run-cloud-1 (M3) = `pg` + `worker` + `producer` + N
  `valkey`. run-cloud-2 = the sharded head-to-head: a sharded-PG pool (`var.pg_count`)
  + tuned-PG + valkey pool + split worker/producer runner pools, driven by
  `scripts/cloud/run-cloud-2.sh`. Outputs land in `results/`, tracked as per-run
  buckets (`run-cloud-N/`, `run-local-N/`, `lookahead/`; see `results/README.md`).

## Milestones (all landed)

- **M1 (Postgres):** `internal/postgres` against the `Backend` contract — schema,
  maintained aggregate, `FOR UPDATE SKIP LOCKED` claim, reaper. Merged to main.
- **M2 (loadgen + proofs):** `internal/loadgen` (Zipfian churn, crash injection,
  metrics) + `tests/proofs` + the throughput/latency graphs + look-ahead bench +
  the Terraform harness. Merged to main.
- **M3 (Valkey + head-to-head):** `internal/valkey` (Streams + ZSETs + Lua via
  rueidis), shard by `workspace`; the head-to-head sweep adds the `shards` series
  dimension + terraform Valkey provisioning, run on cloud (`results/run-cloud-1/`).
- **run-cloud-2 (ambitious head-to-head):** the multi-DSN PG router (shard PG like
  valkey) + tuned-PG + isolated/saturated topology + `scripts/cloud/` orchestration;
  ran quota-constrained (4-shard, m5.large) — `results/run-cloud-2/`. Both backends
  shard ~linearly; valkey ~15× per primary. 8-shard + durability deferred (see
  `designs/ambitious-head-to-head.md` + `enhancements.md`).
