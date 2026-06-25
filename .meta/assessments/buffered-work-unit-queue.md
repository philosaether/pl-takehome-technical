# Assessment: Buffered, Work-Unit-Aware Queue (design space)
Date: 2026-06-25
Branch: main
Scope: Clean-room from the brief only. Honcho's current Postgres implementation
deliberately excluded (solution context); problem shape taken from the brief.

## Current State

Greenfield. No code yet — `git init` done, empty tree. This assessment maps the
*problem* and the *design space*, not an existing system.

The deliverable is a from-scratch work queue with two properties off-the-shelf
systems don't give for free:

1. **Buffered gate** — a work unit becomes claimable only once its *aggregate
   pending cost* (token count) crosses threshold T. The aggregate must be
   *maintained incrementally*, never recomputed by scanning tasks.
2. **Exclusive, in-order drain** — at most one worker per unit, strict FIFO,
   ordering survives claim/release churn and worker crashes.

Work units are keyed by `(workspace, session, peer)`, born on first enqueue,
gone when drained — tens of thousands live at once. Tenants are isolated
(separate instances, separate resources).

## The mechanism (backend-agnostic core)

The load-bearing design is independent of backend choice. Two structures:

- **Per work unit:** an ordered (FIFO) task log + a running `pending_cost`
  aggregate + claim/lease state + a flush deadline. Aggregate is updated O(1)
  on enqueue (`+cost`) and on ack (`−cost`) — never summed.
- **A global eligibility index** of units that are `unclaimed AND (pending_cost
  ≥ T OR flush_deadline passed) AND non-empty`. Claim = pop one from this index.

The whole point: the look-ahead ranges over **live work units (10⁴–10⁵)**, not
**pending tasks (10⁶)**. Membership in the index is maintained on the write
path; reads are O(log n) over units, not O(tasks). That is what makes the
look-ahead a first-class O(1)-ish operation instead of a `SUM ... GROUP BY`.

## Backend candidates (the graded decision)

| Backend | Look-ahead fit | In-order exclusive claim | Crash redelivery | Throughput | Ops / reproducibility |
|---|---|---|---|---|---|
| **Postgres** | partial index on maintained `eligible` flag + `pending_cost`; `FOR UPDATE SKIP LOCKED LIMIT 1` | SKIP LOCKED = exclusive claim w/o contention; lease + `head_seq` | lease expiry + reaper; unacked stay pending, re-drain from head | single-writer ceiling; WAL fsync; vacuum/dead-tuple churn on high delete | trivial (docker), ACID makes correctness *provable*, one system to run |
| **Redis** | ZSET of free+eligible units; O(log n) | Streams (XCLAIM/XAUTOCLAIM) give in-order PEL redelivery natively; claim via atomic Lua move | Streams PEL + lease native, in-order | ~100k–1M ops/s single core; cluster-shard by key | durability (AOF everysec), Lua complexity, recovery story to defend |
| **Custom + WAL** | exact-fit heap/index of eligible units | full control of lease + FIFO deques | you build it | highest potential | you build durability/recovery — risk; "not a thesis project" tension |
| **Kafka/Redpanda** | ✗ no cost gate | ✗ static partition set, rebalance storms under ad-hoc key churn | — | — | brief explicitly warns off this |
| **NATS JetStream** | ✗ gate not native | awkward for ad-hoc subjects | partial | — | less primitive leverage than Redis |

**Leaning (to settle in /draft, not now):** the realistic senior shortlist is
**Postgres-done-right** vs **Redis-primitives**.

- *Postgres* is the strongest **correctness + operability + reproducibility**
  story, and fits the brief's "your current Postgres emerged organically — find
  a reason to improve" narrative perfectly: same backend, but maintained
  aggregate + partial index + SKIP LOCKED instead of `SUM GROUP BY`. "Here's how
  it should have been used." Lower wow, fastest to *prove*.
- *Redis* (Streams + ZSET) is the strongest **primitive fit** (ZSET = cheap
  maintained look-ahead, Streams = in-order exclusive drain with native crash
  redelivery) and the **throughput upgrade path**, at the cost of a harder
  durability/recovery defense.
- *Custom engine* is the frontier — most impressive, most risk, hardest to
  prove correct in the time budget. Frame as "where I'd go for max throughput."

Recommendation: pick *one* to build and prove, name the second clearly (the
screen asks "what's your second choice and why didn't it win?"), gesture at the
frontier. The eligibility-index + lease-claim protocol is the thing to prototype
first — it's load-bearing regardless of backend.

## Capacity Estimate

Targets from the brief: **10⁶ pending tasks**, **10⁴–10⁵ live work units**,
isolated per tenant.

- **Per-task row:** key (~16–32B) + payload (200B–2KB) + cost(8B) + seq(8B) +
  timestamps(16B) + status(1B) ≈ **256B–2KB**. At 10⁶ tasks → **0.25–2 GB**
  pending. Fits RAM (Redis) or a modest PG instance on a laptop.
- **Work-unit rows:** 10⁵ × ~100B ≈ **10 MB**. The eligibility index over them
  is tiny. Look-ahead = O(log 10⁵) ≈ **~17 comparisons**, microseconds/poll.
  *Independent of the 10⁶ tasks* — the headline number to demonstrate.
- **Aggregate maintenance:** +1 update per enqueue, −1 per ack. O(1). The real
  cost of the look-ahead is paid here, on the write path, not on read.
- **Throughput:** enqueue ≈ 1 insert + 1 aggregate update (+ maybe index
  membership flip). PG single node realistically **10k–50k tx/s** (higher
  batched); Redis **100k–1M ops/s** single core. At real Honcho scale the
  downstream LLM call dominates, so the queue isn't the throughput limiter — the
  load test must *simulate* work to stress the queue itself, not an LLM.
- **Plateau / first bottleneck:** (a) single-node write ceiling (WAL fsync /
  Redis single core), (b) connection count, (c) poll/reaper frequency, (d) the
  **hot work unit** — one key is a hard serialization point (ordering *forbids*
  parallelizing it). The hot-unit ceiling is fundamental, not a tuning issue;
  it's the cleanest plateau to defend.
- **Storage growth:** queue is transient (drains to ~0 steady-state), so storage
  ≈ in-flight backlog, not cumulative. PG caveat: high insert+delete churn →
  dead tuples/bloat → needs aggressive autovacuum or partition-drop. Real
  operability gap to address, not hand-wave.
- **Cost:** dev on laptop (PG/Redis both free OSS). One beefy cloud VM (8–16
  vCPU, a few hours) for the headline load test ≪ **$20**. No managed service
  needed.

## Gaps / open decisions (feed /draft)

1. **Backend choice** — Postgres-done-right vs Redis-primitives. The graded call.
2. **Claim protocol** — lease + heartbeat vs visibility-timeout; where the ack
   commit boundary sits (defines at-least-once vs exactly-once-effect).
3. **Ordering proof under crash** — the live scenario (3/10 acked, t3 mid-flight,
   lease expiry, re-claim from t3). Must be a *deterministic* test, not argument.
4. **Flush policy** — idle timer vs age cap vs explicit. Defend the choice and
   prove the flush path (stranded unit below T must still eventually run).
5. **Hot-unit / fairness** — what "fairness" means when many units cross T at
   once; head-of-line behavior; whether to cap per-unit batch to share workers.
6. **Runtime threshold change** (per-tenant T) — maintained `eligible` flag
   recomputed on cost change handles this; partial index on a constant T does
   not. Decide the representation now so it's not a retrofit.
7. **Poison task** — drain-blocking by design (FIFO + at-most-one). Need a
   dead-letter / max-attempts escape or one bad task wedges a whole unit.
8. **Load generator** — must include work-unit *churn* (keys born & drained
   constantly), and report: throughput vs worker count (1/10/100/1000),
   look-ahead cost vs pending-task count, eligible-unit count, oldest-below-T age.

## External Input / constraints

- Brief: incremental commits (history is read), 1-page writeup cap, distributed
  extension as stretch, reproducible (`clone → run → comparable numbers`),
  ~$20 cloud cap, pin the environment. Not a wrapper around rq/celery/BullMQ —
  build the queue itself.
- Phil: assess clean-room off the brief; **validate self-derived workload
  numbers against real Honcho figures in `~/Development/meta/` *after* we commit
  to a design** (avoid anchoring before, avoid wrong-order-of-magnitude after).
- Informal note from PL: "He's on WhatsApp, feel free to text questions" — open
  channel for clarifying problem (not solution) context.

## Recommended Next Steps

1. **Settle backend** (Postgres-done-right vs Redis-primitives) — the one
   decision everything hangs off. Discuss before drafting.
2. `/draft` the architecture around the chosen backend: schema/keyspace, the
   eligibility index, the lease-claim-drain-ack protocol, flush, crash recovery.
3. Prototype the **eligibility index + claim protocol** first — load-bearing
   regardless of backend; de-risks the whole build.
4. Then load generator + the four headline proofs (order-under-crash, gate,
   look-ahead cost-curve, throughput-vs-workers).
