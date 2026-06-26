# Logical Architecture

Codebase map for navigation and review. Maintained by the team; corrected over
time. Reflects **M0 (scaffold)** — Postgres (M1) and Valkey (M3) drivers and the
load-test harness (M2) are stubbed.

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
| `internal/loadgen` | Zipfian-churn producers, the `loadrun` harness, metrics (`queue.Recorder`), chaos. | **real** |
| `internal/postgres` | Path 1 driver — all 8 methods + `Stater`/`Resetter`. | **real** |
| `internal/valkey` | Path 2 driver (Streams+ZSET+Lua). Same stub shape. | stub → M3 |
| `cmd/plq` | CLI (`worker\|loadgen\|loadrun\|reap\|reset`) + build-tagged `newBackend`. | **real** |
| `tests/conformance` | The contract suite (8 scenarios) run vs memory + PG. | **real** |
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
- `make build | test | vet | proofs`; `make up` (postgres path, functional from M1);
  `make load-test` (the M2 sweep + graph — wired, stubbed today).

## Where the milestones land

- **M1 (Postgres):** flesh out `internal/postgres` against the `Backend` contract —
  schema, maintained aggregate, `FOR UPDATE SKIP LOCKED` claim, reaper. Add `pgx`
  to `go.mod`.
- **M2 (loadgen + proofs):** real `internal/loadgen` (Zipfian churn, crash
  injection, metrics) + `proofs/` (the four deterministic proofs) + the
  throughput-vs-workers graph and the process-model sweep.
- **M3 (Valkey):** `internal/valkey` (Streams + ZSETs + Lua via rueidis); shard by
  `workspace`. Head-to-head vs Path 1.
