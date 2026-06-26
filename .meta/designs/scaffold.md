---
Status: accepted
Date: 2026-06-26
Accepted: 2026-06-26
Related: postgres-work-unit-queue.md (M1 implements this), valkey-work-unit-queue.md (M3 implements this)
Roadmap: ../roadmap.md (M0)
---

# Scaffold — Desired State

The shared skeleton both backends plug into: a Go module, the **`Backend`
driver interface** (the apples-to-apples contract), a shared worker loop +
load generator that depend *only* on that interface, per-path Docker images,
volatility-split config, and one-command reproducibility (`make load-test`).

The whole point: **the only thing that differs between Path 1 and Path 2 is the
`Backend` implementation.** The worker loop, the load generator, the proofs, the
metrics — identical code, swapped driver. That's what makes the head-to-head a
fair fight.

---

## Repo layout

```
pl-takehome-technical/
├── go.mod                       # module github.com/philosaether/pl-takehome-technical (Go 1.23, pinned)
├── Makefile                     # build / up / load-test / proofs (one-command repro)
├── README.md                    # what+why, quickstart, architecture, results
├── docker-compose.yml           # store + queue-node + loadgen, per path
├── cmd/
│   └── plq/main.go              # single binary; subcommands: worker | loadgen | reap
├── internal/
│   ├── queue/                   # THE CONTRACT — backend-agnostic, never imports a driver
│   │   ├── backend.go           # Backend interface
│   │   ├── types.go             # WorkUnitKey, Task, ClaimedUnit, WorkerID, LeaseToken
│   │   └── worker.go            # the shared claim→drain→process→ack loop
│   ├── postgres/                # Path 1 driver (M1) — implements queue.Backend
│   │   └── backend.go
│   ├── valkey/                  # Path 2 driver (M3) — implements queue.Backend
│   │   └── backend.go
│   ├── loadgen/                 # producers (Zipfian churn), crash injector, metrics
│   │   ├── producer.go
│   │   ├── workload.go
│   │   └── metrics.go
│   └── config/
│       └── config.go            # volatility-split: tunables here, logic stays in queue/
├── deploy/
│   ├── postgres.Dockerfile      # per-path image: go build -tags postgres
│   └── valkey.Dockerfile        # per-path image: go build -tags valkey
└── proofs/                      # M2 deterministic proofs (tests) — stubbed here
    └── .gitkeep
```

Driver selection is by **build tag, not runtime flag** (OQ1): each path's binary
contains exactly one driver — no dead code shipped to a worker pod that may scale
to 10k instances. The wiring is one constructor file per tag:

```go
// cmd/plq/backend_postgres.go      //go:build postgres
func newBackend(c config.Config) (queue.Backend, error) { return postgres.New(c) }
// cmd/plq/backend_valkey.go        //go:build valkey
func newBackend(c config.Config) (queue.Backend, error) { return valkey.New(c) }
// cmd/plq/backend_memory.go        //go:build !postgres && !valkey   (default)
func newBackend(c config.Config) (queue.Backend, error) { return memory.New(c) }
```

`go build` (no tags) → the in-memory binary (dev, CI typecheck of the shared code,
the proofs' correctness oracle). `-tags postgres` / `-tags valkey` → the real
single-driver binaries.

The dependency rule is the load-bearing invariant: **`internal/queue` imports no
driver.** Drivers import `queue`; the worker loop and loadgen import `queue` and
get a `Backend` injected. If `internal/queue` ever imports `internal/postgres`,
the contract has leaked.

---

## The contract — `internal/queue/backend.go`

This is the artifact the rest of M0 exists to support. Every method maps 1:1 onto
an operation already specified in both accepted designs (cross-refs inline).

```go
package queue

import (
	"context"
	"time"
)

// WorkUnitKey identifies a work unit. Workspace is the shard + fairness seam
// (A1 on both designs); the full triple is the unit identity for exclusive claim.
type WorkUnitKey struct {
	Workspace string
	Session   string
	Peer      string
}

type (
	WorkerID   string
	LeaseToken string // opaque; backend validates ack/heartbeat/release against it
)

// Task is one item in a work unit's per-unit FIFO.
type Task struct {
	Seq     int64  // per-unit monotonic sequence, assigned by Enqueue (I1 ordering)
	Payload []byte
	Cost    int64  // token cost; sums toward the unit's threshold T (I3 gate)
}

// ClaimedUnit is an exclusive, time-leased hold on one work unit.
type ClaimedUnit struct {
	Key       WorkUnitKey
	Worker    WorkerID
	Lease     LeaseToken
	LeaseTill time.Time
}

// Backend is the apples-to-apples contract. Postgres (M1) and Valkey (M3) each
// implement it; the shared worker loop and load generator depend ONLY on this.
type Backend interface {
	// Enqueue appends a task to key's FIFO, creating the unit implicitly on first
	// enqueue, maintaining the pending_cost aggregate + flush_deadline. Assigns
	// and returns the per-unit seq. (PG §Enqueue / Valkey enqueue Lua; I1·I3.)
	Enqueue(ctx context.Context, key WorkUnitKey, payload []byte, cost int64) (seq int64, err error)

	// Claim atomically leases the lowest-flush_deadline eligible unit (age-fair).
	// Returns (nil, nil) when nothing is eligible — not an error. Enforces the
	// gate: only eligible units are claimable. (PG §Claim / Valkey claim Lua; I2·I4.)
	Claim(ctx context.Context, worker WorkerID, lease time.Duration) (*ClaimedUnit, error)

	// Drain returns up to max unacked tasks of a claimed unit, in seq order. (I1.)
	Drain(ctx context.Context, unit *ClaimedUnit, max int) ([]Task, error)

	// Ack marks tasks through throughSeq processed (delete-on-ack) and atomically
	// EITHER keeps the lease (unit still has work) OR releases it. Returns whether
	// the unit is still held by this worker. (PG §Drain+Ack / Valkey ack Lua.)
	Ack(ctx context.Context, unit *ClaimedUnit, throughSeq int64) (stillHeld bool, err error)

	// Release relinquishes the lease without acking (clean give-up).
	Release(ctx context.Context, unit *ClaimedUnit) error

	// Heartbeat extends the lease for a long batch. (PG §Claim note; optional, see OQ4.)
	Heartbeat(ctx context.Context, unit *ClaimedUnit, extend time.Duration) error

	// Fail records a head-task failure: increments attempts, routes to DLQ past
	// MaxAttempts (poison), releases the unit. (PG §Failure modes / Valkey PEL.)
	Fail(ctx context.Context, unit *ClaimedUnit, seq int64, reason string) error

	// ReapExpired reclaims expired leases (crash recovery, I5) and promotes
	// flush-eligible units (age-cap). Timer-driven; also safe to call lazily.
	ReapExpired(ctx context.Context, now time.Time) (reclaimed, flushed int, err error)

	Close() error
}
```

### The shared worker loop — `internal/queue/worker.go`

Identical for both backends. This is *why* the interface is shaped the way it is —
the backend stays a pure storage primitive; the loop owns the lifecycle.

```go
func (w *Worker) runOnce(ctx context.Context) (didWork bool, err error) {
	unit, err := w.be.Claim(ctx, w.id, w.cfg.Lease)
	if err != nil || unit == nil {
		return false, err // nil unit → nothing eligible → caller backs off
	}
	for {
		tasks, err := w.be.Drain(ctx, unit, w.cfg.Batch)
		if err != nil {
			return true, err
		}
		if len(tasks) == 0 {
			return true, w.be.Release(ctx, unit)
		}
		// Heartbeat the lease while processing a potentially-slow batch (OQ4:
		// downstream LLM calls can run long, so a batch can outlive the lease).
		hbCtx, stop := context.WithCancel(ctx)
		go w.heartbeatLoop(hbCtx, unit) // ticks Backend.Heartbeat at ~Lease/3
		for _, t := range tasks {
			w.process(t) // simulated downstream work, configurable model (see Process model)
		}
		stop()
		held, err := w.be.Ack(ctx, unit, tasks[len(tasks)-1].Seq)
		if err != nil || !held {
			return true, err // Ack released the unit → done with it
		}
	}
}
```

The producer side reuses the *same* `Backend.Enqueue` — the load generator is
not a second code path into the store.

### Process model — the axis that locates the cutover

`process(t)` is **configurable** (OQ5), because the simulated work profile is not a
detail — it's the axis the PG→Valkey cutover lives on.

```go
type ProcessModel struct {
	Kind    string        // "zero" | "fixed" | "cost"
	Base    time.Duration // fixed/per-batch latency
	PerCost time.Duration // cost mode: sleep = Base + Cost*PerCost
}
func (w *Worker) process(t queue.Task) {
	switch w.cfg.Process.Kind {
	case "zero": // nothing — measure the queue itself
	case "fixed": time.Sleep(w.cfg.Process.Base)
	case "cost": time.Sleep(w.cfg.Process.Base + time.Duration(t.Cost)*w.cfg.Process.PerCost)
	}
}
```

Why it locates the cutover (this is a headline result, feeds M4's migration
triggers): queue throughput ≈ `workers / processing-time`. The backend becomes the
bottleneck only when that rate crosses its op ceiling.

- **Zero-work** maximizes queue-ops/sec for a given worker count → finds each
  backend's *absolute* op ceiling and the **widest** PG↔Valkey gap. This is the
  "pure performance" comparison.
- **Cost-proportional work** places *realistic* operating points on the curve:
  longer per-task work → lower queue pressure → **PG survives longer**. The cutover
  is the arrival rate where `workers / processing-time` crosses the PG ceiling.

Together they **bracket the migration trigger**: zero-work gives the ceiling; the
cost sweep shows how much arrival-rate headroom a real workload has before hitting
it. So a workload with 200 ms LLM calls may never justify Valkey, while one with
2 ms tasks crosses early — the cost sweep is what tells them *which world they're
in*. M2 runs the worker sweep as an inner loop and the process model as an outer
loop; the only cost is cloud-minutes (the $20 cap), so keep the grid small
(e.g. `Base ∈ {0, 2ms, 20ms, 200ms}`).

---

## Config — volatility split (`internal/config`)

Stable logic in `internal/queue` depends on this struct; values come from
env/flags. The contract and the worker loop never change; only these knobs do.

```go
type Config struct {
	// Shared tunables — BOTH backends honor these identically (apples-to-apples).
	Lease            time.Duration // claim lease duration
	Batch            int           // max tasks per Drain
	DefaultThreshold int64         // token cap T (per-tenant override lives in the backend)
	DefaultMaxWait   time.Duration // flush age-cap on the oldest pending task
	MaxAttempts      int           // → DLQ past this (poison handling)
	PollBackoff      BackoffConfig // adaptive idle backoff curve
	Workers          int           // worker-pool size — the sweep knob (1/10/100/1000)
	Process          ProcessModel  // simulated downstream work (zero|fixed|cost) — the cutover axis

	// Backend selection + backend-specific (the volatile, per-path part).
	Backend  string         // "postgres" | "valkey"
	Postgres PostgresConfig // DSN, partition count, autovacuum hints
	Valkey   ValkeyConfig   // addr(s), shard map, maxmemory headroom
}
```

Per-tenant `threshold`/`max_wait` are *not* here — they live in the backend
(`tenant_config` row / `wu:{key}` hash), seeded from `Default*`. Config carries
defaults; the store carries overrides.

---

## Images & topology

**Two per-path images**, each a single-driver binary (build-tagged) that runs any
of the three subcommands. The store is stock; nothing of ours ships both drivers.

| Image | Build | Runs (subcommands) |
|---|---|---|
| stock `postgres` / `valkey` | — | the backing store |
| `plq:postgres` | `go build -tags postgres` | `worker` · `loadgen` · `reap` |
| `plq:valkey` | `go build -tags valkey` | `worker` · `loadgen` · `reap` |

- **worker** = the shared `worker.go` loop + the one baked driver, `WORKERS=N` sets
  pool size. The sweep (1/10/100/1000) is re-running this at different `N`
  (env-per-run, serial, comparable — OQ3).
- **loadgen** = producers (Zipfian churn) + crash injector + metrics aggregator.
  Same source for both paths, compiled against each driver. The apples-to-apples
  guarantee is **identical loadgen source**; the *only* difference between runs is
  the driver under test — which is exactly what we're measuring (so the enqueue
  path *should* differ).
- **reaper** runs as a goroutine inside the worker process by default (calls
  `ReapExpired` on a timer; OQ2 confirmed). The `reap` subcommand keeps the door
  open to a standalone reaper service — flagged as a v2 enhancement.

`docker-compose.yml` wires one path at a time: store + `plq:<path>` worker
(`WORKERS=N`) + `plq:<path>` loadgen.

---

## Makefile — one-command reproducibility

```
make build              # go build (memory) + docker build both path images (-tags)
make up / make down     # compose up/down (defaults to postgres path)
make load-test          # THE one-command repro: bring up a path, run the sweep,
                        #   dump metrics + throughput-vs-workers graph to ./results
make load-test-valkey   # same, valkey path
make proofs             # run the M2 deterministic proof tests (stubbed in M0)
make fmt / make lint    # gofmt + vet/staticcheck
```

`make load-test` is the graded reproducibility promise: clone → one command →
comparable numbers.

---

## README skeleton

`what + why` · `quickstart (make load-test)` · `architecture (the contract +
topology diagram)` · `the four proofs` · `results (graphs + numbers)` · `design
docs (links to .meta/designs)` · `migration triggers (the watch-numbers)`.

---

## What M0 ships vs stubs

**Ships, real:** module + layout, the full `Backend` interface + types, the shared
`worker.go` loop (incl. heartbeat + the configurable process model), `Config` +
loading, both path Dockerfiles + compose, the Makefile, README skeleton. The
**in-memory `Backend`** is the default no-tag build, so the loop and loadgen
compile, run, and are testable before any real driver exists.

**Stubbed (later milestones):** `internal/postgres` (M1), `internal/valkey` (M3),
`internal/loadgen` real producers + metrics (M2), `proofs/` (M2).

The in-memory backend is worth its weight: it lets the worker loop and loadgen be
exercised end-to-end in M0, and it becomes a free correctness oracle for the
proofs (a trivially-correct reference the real drivers must match).

---

## Tradeoffs

**Backend as pure storage primitive vs backend-owns-the-loop.**
*Chosen:* the `Backend` exposes `Claim/Drain/Ack/...` and the *shared* `worker.go`
owns the claim→drain→process→ack lifecycle. *Rejected:* a `Backend.Run(process
func(Task))` that hides the loop inside each driver. The rejected form would let
the two backends' loops drift, quietly destroying the apples-to-apples property —
the loop is exactly what must be identical. Cost of the chosen form: the interface
is wider (8 methods, not 1). Worth it. *Revisit if:* a backend genuinely can't
express one of these primitives (none foreseen — both designs already specify all
eight).

**Build-tag single-driver binaries vs both-drivers + runtime flag.**
*Chosen (OQ1):* build tags (`//go:build postgres|valkey`, default=memory) → each
image contains exactly one driver. Rationale: don't ship dead driver code to a
worker pod that may scale to 10k instances, and it's cleaner. *Rejected:* both
drivers compiled in, `--backend` selecting at runtime — one binary, but it ships
the unused driver everywhere. Cost of the chosen form is small: one `newBackend`
constructor file per tag, and CI builds each tag separately (the no-tag memory
build already typechecks all shared code). The apples-to-apples integrity moves
from "byte-identical image" to "**identical loadgen source compiled against each
driver**" — which is the more honest invariant anyway, since the enqueue path is
part of what we measure.

**One binary w/ subcommands vs three `cmd/` mains.**
*Chosen:* one `cmd/plq` with `worker|loadgen|reap` subcommands — shared flag
parsing/config, smaller surface, images differ only by default command. *Rejected:*
three mains — more duplication for no benefit here.

**Drain + Ack as separate primitives vs a single fetch-process-ack callback.**
*Chosen:* separate, so the backend never runs user code and stays a pure store.
Keeps drivers simple and the process step (simulated work / crash injection) under
the loop's control where the proofs need it.

## Resolved (iteration 1)

1. **Driver selection → build tags** (single-driver binaries; default=memory). Don't
   ship dead driver code to scaled pods; small complexity cost accepted. *Done in
   Repo layout / Images / Tradeoffs above.*
2. **Reaper → in-process goroutine** in v1; `reap` subcommand keeps a standalone
   service possible. Flagged as a v2 enhancement → `enhancements.md`.
3. **Worker sweep → `WORKERS=N` env, re-run per point** (serial, comparable on the
   pinned box). Loadgen-orchestrated ramp flagged as a staging autoscaling
   test/tune enhancement → `enhancements.md`.
4. **Heartbeat → keep and wire it** (LLM calls can run long; a batch can outlive the
   lease). `worker.go` heartbeats at ~`Lease/3` during processing.
5. **Process model → configurable** (`zero|fixed|cost`); M2 sweeps it as the outer
   loop. Zero-work = backend ceiling + pure-perf comparison; cost-proportional =
   where a real workload sits relative to that ceiling → *the* cutover signal. See
   "Process model" above; feeds M4 migration triggers. Trivial code cost; the real
   budget is cloud-minutes, so keep the grid small.
6. **Module path → `github.com/philosaether/pl-takehome-technical`** (matches the
   repo). The path is an import-prefix string, not a second repo; matching the repo
   keeps imports truthful + clickable for reviewers reading the shared repo.
7. **"Flag" → an enhancements backlog** for the 1-page brief's "possible
   enhancements" list. Created `.meta/enhancements.md`; we pick the most compelling
   set space allows at publication time (M4).

## Open Questions

*(none open — iteration 1 resolved all six. New ones land here as we build.)*

## Out of Scope

- Real driver implementations (Postgres = M1, Valkey = M3).
- Real producers / metrics / graphs (M2) — M0 stubs the package, ships the
  in-memory backend so the loop runs.
- The deterministic proofs themselves (M2) — `proofs/` is a placeholder.
- The cloud box + the graded run (M2) — M0 is laptop-local compose only.
- Any per-tenant fairness scheduling (A1 decision: explain, don't build).
