---
Status: accepted
Date: 2026-06-26
Accepted: 2026-06-26
Implemented: 2026-06-26 (feature/loadgen) — local half, verified vs Docker postgres:16
Divergences: conformance + proofs moved to tests/ (tests/conformance, tests/proofs, tests/bench — the look-ahead bench got its own binary to avoid shared-DB flakes); process points labeled by per-task latency (zero/2ms/20ms/200ms), 4-point axis; added a loop-p99-vs-workers latency chart; ordering proof claims with retry (models the worker loop); look-ahead units scale with task count; chaos wired via PLQ_CHAOS env (not a --chaos flag).
Deferred: the canonical EC2 sweep (gated — terraform apply + the run); histogram-mutex sharding + the M1 PG round-trips → measure-first on the cloud sweep.
Implements: ../roadmap.md (M2)
Builds-on: scaffold.md (the worker loop + ProcessModel), postgres-driver.md (the backend under test)
Build-order: local half first (loadgen/proofs/metrics/graph, verified vs Docker PG); the EC2 canonical sweep is a gated step
---

# Load Generator + The Four Proofs (M2) — Build Design

The evidence layer: a churning Zipfian load generator, a worker-count sweep that
produces the throughput-vs-workers graph, and the deterministic proofs — capped by
the **look-ahead cost** bench (our maintained aggregate ~flat at 10⁶ tasks vs a
naive `SUM…GROUP BY` that scans). Runs on a pinned cloud box within the $20 cap.

The conformance suite (M1) already proves *gate* and *flush* deterministically at
the backend level. M2 adds the two **presentable, scale** artifacts — the
ordering-under-crash logged trace and the 10⁶-row look-ahead bench — plus the load
run that yields the headline numbers.

---

## What M2 delivers

| Deliverable | Where | Form |
|---|---|---|
| Zipfian churn producers | `internal/loadgen/producer.go` | enqueue load with born/drained keys |
| Worker-count sweep (1/10/100/1000) + saturation check | `internal/loadgen/harness.go`, `plq loadrun` | `results/sweep.csv` |
| Metrics (throughput, claim p99, backlog, lease/DLQ rates, loop p99) | `internal/loadgen/metrics.go` | periodic CSV + end summary |
| Throughput-vs-workers graph + loop-p99-vs-workers companion | `scripts/plot.py` | `results/throughput.png`, `results/latency.png` |
| Proof 1 — ordering-under-crash (3-of-10, logged trace) | `proofs/ordering_test.go` | deterministic test |
| Proofs 2,3 — gate, flush | already in `internal/conformance` | deterministic tests |
| Proof 4 — look-ahead cost vs naive `SUM…GROUP BY` | `proofs/lookahead_test.go` | EXPLAIN + time-vs-taskcount table/graph |
| Process-model sweep (zero \| cost) → the cutover axis | harness param | second sweep dimension |
| Cloud provisioning (3 boxes, atomic teardown) | `deploy/terraform/` | `make cloud-up` / `make cloud-down` |

---

## The load harness — `plq loadrun`

Two run topologies, same code:

- **Dev / smoke — integrated `loadrun`:** producers + worker pool + metrics in one
  process. One command, one clock — convenient for laptop iteration.
- **Canonical / cloud — split (the graded run):** the **worker pool alone on its
  box** (`plq worker`, production-match — nothing else competes for its CPU),
  producers on a separate box (`plq loadgen`), PG on a third (or RDS). The headline
  metrics (throughput, loop-latency p99) live *entirely* in the worker process, so
  nothing important is split; the producer box only owns enqueue-rate. **Saturation
  is self-certified on the worker box:** via the `Stater` backlog query, if the
  eligible backlog stays > 0 the whole run, the workers were never starved → the
  number is the backend's plateau, not the producer's. No cross-process correlation
  needed.

Shared `metrics.go` is threaded into `loadrun`, `worker`, and `loadgen` alike (the
worker exports throughput + loop p99 + backlog; the producer exports enqueue-rate).

```go
// internal/loadgen/harness.go
type RunSpec struct {
	Workers   int                // the sweep knob
	Process   queue.ProcessModel // zero | cost (the cutover axis)
	Duration  time.Duration      // measurement window
	Producers int                // enough to out-run the backend (saturation)
	Workload  Workload
}

func Run(ctx context.Context, be queue.Backend, spec RunSpec) (Result, error) {
	m := metrics.New()
	// 1. producers: Zipfian churn enqueue, full tilt (or rate-capped)
	// 2. worker pool: spec.Workers × the shared queue.Worker loop (OnProcess → metrics)
	// 3. reaper goroutine (lease reclaim + flush) as in the worker subcommand
	// 4. sampler: every 1s snapshot → CSV row
	// run for spec.Duration, then cancel and drain the summary
}
```

The sweep itself is a thin loop (Makefile or a `--sweep` flag): for
`N ∈ {1,10,100,1000} × process ∈ {zero,cost}` → reset the backend → `Run` →
append a `results/sweep.csv` row. **Env-per-run** (the M0 OQ3 decision): each point
is a fresh process, serial, comparable.

### Saturation check (the Go-was-the-right-call gate)

The measured plateau must be the *backend's*, not the load generator's. Each run
asserts saturation: **enqueue-rate ≥ ack-rate** at steady state **and** the
eligible backlog stays > 0 (producers keep the queue non-empty). If not saturated,
bump `Producers` and re-run; the harness logs `saturated: true/false` per point so
a non-saturated number is never reported as a plateau.

---

## Producers — Zipfian churn — `internal/loadgen/producer.go`

```go
type Workload struct {
	Seed         int64   // pinned RNG → reproducible
	WorkingSet   int     // # live wu_keys at once (10^4–10^5)
	ZipfS        float64 // skew (s>1; bigger = hotter heads)
	BirthRate    float64 // P(new key) per enqueue → churn (keys born/drained)
	Cost         CostDist // constant | uniform(lo,hi) | lognormal — token cost per task
	Workspaces   int     // tenants sharing the queue (fairness realism)
}
```

- **Working set + churn:** keep a slice of live `wu_key`s. Each enqueue: with
  `BirthRate`, mint a new key (new `(ws,sess,peer)`, `ws` round-robin over
  `Workspaces`); otherwise pick an existing key by **`math/rand.Zipf`** (hot heads).
  A key is *drained* (retired from the live set) when producers stop targeting it
  and workers empty it — manufacturing the born/drained churn the brief asks for.
- **Pinned seed** (`rand.New(rand.NewSource(Seed))`) → byte-reproducible workload;
  the *same* workload drives every sweep point and (M3) both backends.
- Cost from `CostDist` so the gate/flush behavior is realistic and the cost-sweep
  has something to scale.

---

## Metrics — `internal/loadgen/metrics.go`

Atomic counters + a coarse latency histogram (bucketed, no dep):

```go
type Metrics struct {
	Enqueued, Acked, Claims, ClaimEmpty, LeaseExpired, DLQ atomic.Int64
	claimLat, loopLat *hist // claim() wall time; loop = claim→final-ack per unit
}
```

- **Throughput** = Δ`Acked`/Δt, sampled each second.
- **Loop latency p99** = claim→drain→process→ack wall time per unit batch — *the*
  latency the PG→Valkey cutover trades on (recorded via the worker's `OnProcess` +
  ack hooks).
- **claim / look-ahead p99** = time of the `Claim` call (the hot query).
- **Backlog / eligible-unit count / oldest-below-T age** via an optional
  `Stater` interface (postgres implements it with a cheap stats query; loadgen
  type-asserts and skips if absent — keeps `queue.Backend` clean).
- Lease-expiry + DLQ rates from the counters.
- Export: a CSV row/second to `results/<run>.csv` + an end-of-run summary line into
  `results/sweep.csv`.

---

## The four proofs — `proofs/`

Deterministic, presentable. Proofs 2–3 are already the conformance suite's
`Gate*`/`Flush*` scenarios (both backends) — M2 doesn't re-implement them, it cites
them. M2 *adds*:

### Proof 1 — ordering-under-crash (the 3-of-10 trace) — `proofs/ordering_test.go`
One unit, tasks `t0..t9`. Worker A claims, drains, acks `t0–t2`, then **crashes**
(stop without acking `t3–t9`). Reaper reclaims the expired lease; Worker B claims,
drains from `t3`. Assert the **processing log is exactly `t0..t9`, once each, in
order** across the crash. Runs against memory *and* postgres (gated). The narrative
artifact behind conformance's `LeaseReclaimRedelivers`.

### Proof 4 — look-ahead cost (the headline) — `proofs/lookahead_test.go` (postgres)
Seed **10⁶ tasks across 10⁴–10⁵ units** (Zipfian) via `pgx.CopyFrom` (bulk — bypass
the enqueue path; we're benching *claim*, and one-by-one inserts of 10⁶ rows is its
own afternoon), with correct `work_units.pending_cost` aggregates.

- **Ours:** `EXPLAIN (ANALYZE)` the claim subquery → assert **index-only scan on
  `wu_claimable`**, and that claim time is **~flat** as task count grows (measure at
  10⁴/10⁵/10⁶).
- **Their current code:** the naive `SELECT wu_key FROM tasks GROUP BY wu_key HAVING
  sum(cost) >= T …` → `EXPLAIN (ANALYZE)` shows the **seq scan + aggregate** that
  grows with task count.
- Output: `results/lookahead.csv` (claim-ms vs task-count, ours vs naive) → the
  second graph. *This is the take-home's thesis, measured.*

---

## Crash injection — two distinct things

1. **Deterministic proof** (above): in-process, controlled — stop a worker mid-batch
   by cancelling its context without acking, advance time, let the reaper reclaim.
2. **Load-run chaos** (`loadrun --chaos`): during a live sweep run, a chaos goroutine
   periodically cancels a random worker's context (simulating crash) on a schedule.
   Observational, not asserted — the metrics show throughput dip-and-recover and
   `LeaseExpired` rate, demonstrating recovery *under load*. (Process-`kill -9` is
   more realistic but harder to orchestrate in one process — noted, not v1.)

---

## Where the deferred M1 efficiency items get measured

The two deferred round-trips (Drain 2→1, Enqueue tenant-cache miss) are measured
**here**: run the sweep, read claim/enqueue p99 + throughput; if they show up as a
ceiling, implement the collapse and re-run → **before/after numbers** (the
talking-point). If they don't move the plateau, we say so — measured, not assumed.

---

## Reproducibility & the cloud box

- `make load-test` → run the sweep (`plq loadrun --sweep`) → `results/sweep.csv` →
  `make graph` → `results/throughput.png`. `make proofs` → `go test ./proofs/...`
  (PG-scale proofs gated by `PLQ_TEST_POSTGRES`).
- **Canonical numbers on pinned AWS EC2** (not the laptop). Topology:
  - **Worker box:** `m5.xlarge` (4 vCPU / 16 GiB) — a plausible EKS general-purpose
    worker-node size; runs `plq worker` **alone** (production-match).
  - **PG box:** separate `m5.xlarge` (or RDS `db.m5.large`) — so PG never competes
    with the workers for CPU.
  - **Producer box:** `m5.large` running `plq loadgen`.
  - On-demand, same AZ. The dominant cost is instance *uptime*, not the test
    (3 boxes ≈ $0.6/hr → the whole exercise is **~$1**); the $20 risk is purely
    forgetting to tear down.
- **Provisioning → Terraform** (`deploy/terraform/`): the 3 instances + a security
  group + a key pair + IP outputs (~60 lines). `make cloud-up` = `terraform apply`,
  **`make cloud-down` = `terraform destroy`** — teardown is one atomic command, which
  is the real spend control. Doubles as an IaC artifact (reproducible canonical box,
  pinned in code) — on-theme for "mature toward enterprise." *(CLI-script fallback
  only if TF setup runs long.)* PG box runs the stock `postgres` container via
  user-data; RDS `db.m5.large` is the more-managed option, noted not required.
- **Matrix (OQ5): full matrix at 60 s/point.** `workers {1,10,100,1000} ×
  process {zero, 2ms, 20ms, 200ms}` = **16 points × 60 s ≈ 16 min** (~$1). The
  process axis spans the cutover: `zero`/`2ms` saturate PG (the curve **plateaus** —
  the defended ceiling); `20ms`/`200ms` are worker-bound within 1000 workers (curve
  rises **linearly**, PG idle — "at this latency you don't need Valkey, just add
  workers"). `2000ms` (LLM-call scale) is an optional flourish — `PROCESS_MS="2 20
  200 2000"` — but it adds no new regime and its 1-worker point is ~27 samples
  (thin; `loop_samples` makes that visible). Sample filenames + the `process` CSV
  column are labeled by per-task latency (`zero`/`2ms`/…) so cost points are distinct.

---

## Tradeoffs

**Integrated `loadrun` vs split worker/loadgen for the canonical run.** *Resolved →
both.* Integrated `loadrun` for dev/smoke (convenience); **split processes across
boxes for the canonical run** (worker pool alone on its box = production-match).
Cheap because the headline metrics (throughput, loop p99) live entirely in the
worker process and saturation self-certifies via the worker-box backlog query — so
nothing important is split. Only new work: thread `metrics.go` into the `worker` +
`loadgen` subcommands (wanted anyway). See "Two run topologies" above.

**Bulk `CopyFrom` seed for the look-ahead bench vs enqueue path.** *Chosen:* COPY —
10⁶ rows in seconds, and the bench targets *claim* look-ahead, not enqueue.
*Rejected:* seeding via `Enqueue` — honest end-to-end but turns a 5s setup into
minutes and benches the wrong thing.

**Coarse bucketed histogram vs an hdrhistogram dep.** *Chosen:* a small bucketed
histogram (no dependency) — p99 to within a bucket is plenty for these graphs.
*Revisit if:* we need tight tail percentiles.

## Resolved (iteration 1)

1. **Graph renderer → matplotlib** (`scripts/plot.py` → PNG); Python dep accepted.
2. **Cloud box → AWS EC2**, worker on `m5.xlarge` (plausible EKS node) alone, PG on
   a separate `m5.xlarge`/RDS, producer on `m5.large`. **Provisioned via Terraform**
   (`make cloud-up`/`cloud-down`) — atomic teardown is the spend control + IaC
   audition signal. (Their prod is GCP, but AWS for simplicity here.)
3. **Load-run chaos → goroutine-cancel** (the deterministic crash proof is the
   rigorous one).
4. **`Stater` optional interface → add it** (postgres implements; `queue.Backend`
   unchanged). Doubles as the worker-box saturation signal.
5. **Matrix → 60 s/point, run the full matrix** (uptime is the cost, not the test —
   ~$1 total). Extend to richer cost points (20/200 ms) if the curve's boring.

## Open Questions

*(none open — iteration 1 resolved all five + the 2-process topology.)*

## Out of Scope

- Valkey + the head-to-head (M3) — `loadrun` is built backend-agnostic so M3 just
  points it at the Valkey driver.
- The 1-page writeup + migration-trigger table (M4) — M2 produces the numbers it cites.
- Real LLM work (process model stays simulated).
- Distributed multi-box load generation (single pinned box for the graded run).
- Per-tenant fairness scheduling (A1: explain, don't build) — though the workload's
  `Workspaces` knob makes the fairness story *observable* in the metrics.
