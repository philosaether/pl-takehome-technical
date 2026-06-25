# Assessment: Path 1 — Postgres Done Right + Redis Migration Plan
Date: 2026-06-25
Branch: meta/queue-backend-scoping
Scope: The "safe, future-proofed" path — build it right on Postgres, define the
Redis cutover threshold with numbers, and (the part Phil cares about) check
whether we *actually improve* on the well-known postgres-as-queue pattern or
just reproduce it. Decision drilldown; the full schema is /draft's job.
Depends on: path2-redis-durability-recovery (the cutover gate).

## The design in one screen

Two tables; the aggregate is *maintained*, never scanned.

```sql
-- tasks: the per-work-unit FIFO log (append on enqueue, delete/mark on ack)
tasks(
  id           bigint,             -- global monotonic (enqueue order tiebreak)
  wu_key       text/bytea,         -- (workspace, session, peer) hashed
  seq          bigint,             -- per-wu_key arrival order (FIFO key)
  payload      bytea,
  cost         int,
  enqueued_at  timestamptz,
  PRIMARY KEY (wu_key, seq)
)

-- work_units: one row per live key; carries the MAINTAINED aggregate + claim
work_units(
  wu_key         text/bytea PRIMARY KEY,
  pending_cost   bigint NOT NULL,   -- maintained: += cost enqueue, -= cost ack
  head_seq       bigint NOT NULL,   -- next unacked seq; only advances on ack
  tail_seq       bigint NOT NULL,   -- next seq to assign on enqueue
  eligible       boolean NOT NULL,  -- maintained: pending_cost >= T OR flush_due
  claimed_by     text,              -- worker id, NULL = free
  lease_expires  timestamptz,
  flush_deadline timestamptz        -- set on first enqueue below T
)

-- THE look-ahead index: partial, over work_units (10^4-10^5 rows), not tasks (10^6)
CREATE INDEX wu_claimable ON work_units (wu_key)
  WHERE eligible AND claimed_by IS NULL;
```

**Claim (exclusive, contention-free):**
```sql
UPDATE work_units SET claimed_by=$me, lease_expires=now()+$lease
WHERE wu_key = (
  SELECT wu_key FROM work_units
  WHERE eligible AND claimed_by IS NULL
  ORDER BY flush_deadline NULLS LAST, wu_key      -- fairness knob
  FOR UPDATE SKIP LOCKED LIMIT 1
) RETURNING wu_key, head_seq;
```
`SKIP LOCKED` is the load-bearing primitive: N workers claim N *different*
units with no lock contention and no double-claim. **Look-ahead cost is O(log
units) on the partial index — independent of the 10⁶ pending tasks.** That is
the whole point, and it's index-only, never `SUM … GROUP BY`.

**Drain:** `SELECT … FROM tasks WHERE wu_key=$k AND seq>=$head ORDER BY seq
LIMIT $batch`. **Ack:** delete (or mark) acked rows, `head_seq := last_acked+1`,
`pending_cost -= acked_cost`, recompute `eligible`, extend or release lease — all
in one transaction. **Order survives crash** because `head_seq` only advances on
committed ack; a crashed worker's lease expires and the unit re-claims, draining
from the same `head_seq`. At-most-one-worker via the claim lease.

**Flush:** on first enqueue that leaves a unit below T, set `flush_deadline =
now() + max_wait`. A unit becomes `eligible` when `pending_cost >= T` **OR**
`flush_deadline <= now()`. A cheap reaper flips `eligible` on units whose
deadline passed (and reclaims expired leases). Stranded units therefore always
run — provably, via the flush path.

**Runtime threshold change (per-tenant T):** because eligibility is a
*maintained boolean*, not a constant baked into the index predicate, changing T
means recomputing `eligible` for that tenant's units — a bounded UPDATE, not a
schema change. (A partial index on a literal `pending_cost >= 500` could *not*
do per-tenant T — this is why we store the flag.)

## Why this is the strongest *correctness + operability* story

- **ACID makes the ordering-under-crash proof deterministic**, not argued. The
  live scenario (3/10 acked, t3 mid-flight, lease expiry, re-claim from t3) is a
  transaction boundary test we can write and run repeatably. This is exactly
  what the screen says it wants ("demonstrated with a deterministic test, not
  vibes").
- **`SKIP LOCKED` is *the* canonical exclusive-claim primitive** — no Lua, no
  custom protocol, no consensus. Parallel-across-units falls out for free.
- **One system to operate, durable by default, trivially reproducible** (docker
  postgres, pinned). Best fit for "operationally feasible, not a thesis project."

## Where Postgres bites (defend these, don't hide them)

- **Single-writer ceiling.** All enqueues/acks funnel through one primary's WAL
  fsync. Realistic sustained write throughput ~**10k–50k tx/s** (higher with
  batched commits / `commit_delay` group commit). This is the throughput plateau
  to defend.
- **Dead-tuple / vacuum churn — the classic postgres-as-queue tax.** High
  insert+delete rate generates dead tuples; autovacuum must keep up or the table
  (and the partial index) bloat, slowing the very claim query we depend on.
  Mitigations to design in: aggressive per-table autovacuum, **partition `tasks`
  by time or hash and drop whole partitions** instead of row-deletes, or mark
  acked + batch-reap. This is real operational work — name it, don't wave.
- **Poll vs push.** Claim is a poll. Use `LISTEN/NOTIFY` (notify on
  enqueue-crossing-threshold) to wake idle workers instead of busy-polling, and
  cap poll frequency. Look-ahead itself is cheap; the cost is poll *frequency ×
  workers* turning into transaction load.

## Did we *improve*, or just hand back pgmq? (Phil's central question)

The generic postgres-as-queue pattern — **pgmq, river, Brandur's SKIP LOCKED
articles** — gives you: row-level `FOR UPDATE SKIP LOCKED` claim, a visibility
timeout, archive-on-ack. What it does **not** give natively, and what we build
*on top*:

| Capability | Generic PG-queue (pgmq/river) | Ours |
|---|---|---|
| Claim granularity | **individual row/message** | **whole work-unit, exclusively** |
| In-order per-key drain | ✗ (rows claimed independently, no per-key FIFO) | ✓ `head_seq` + per-key lease |
| Cost-threshold gate | ✗ ("process whatever arrives") | ✓ maintained `pending_cost` + `eligible` |
| Cheap look-ahead over *keys* | ✗ (no key aggregate) | ✓ partial index on `work_units` |
| Flush of stranded keys | ✗ | ✓ `flush_deadline` |

**So yes, we improve — the delta is real and it's exactly "what we built on
top":** the *work-unit abstraction* (per-key exclusive in-order lease) + the
*maintained aggregate + eligibility gate* + the *cheap key-level look-ahead*.
pgmq is a row queue; ours is a work-unit-aware buffered queue. We are **not**
handing back a job runner.

> ⚠️ **The wall (per Phil's decision).** This comparison is against the *generic
> community pattern* (clean-room-safe). **Before we build, if Postgres wins, we
> must compare against Honcho's *actual* implementation** — because if their
> emergent design already does maintained-aggregate + per-key lease, "improve"
> collapses to "reproduce," and "reproduce minus features" would be doubly bad.
> That comparison is a gated, explicitly-flagged step, not part of this assess.

## The Redis migration plan — and *when cutover is worth it*

Future-proofing = design the PG schema so the **logical model maps cleanly onto
Redis** (so migration is a backend swap, not a redesign):

| PG construct | Redis/Valkey equivalent |
|---|---|
| `tasks` per-key FIFO | one **Stream** per `wu_key` (`XADD`/`XREADGROUP`) |
| `work_units.pending_cost` | counter, `INCRBY`/`DECRBY` in a Lua claim/ack |
| `wu_claimable` partial index | **ZSET** of free+eligible keys (atomic Lua membership) |
| claim lease | consumer-group + PEL + `min-idle-time` `XAUTOCLAIM` |
| `head_seq` ordering | stream ID order + PEL |

**Cutover threshold (numbers, gated on durability from path 2):** migrate when
*either*

1. **Write ceiling:** sustained `enqueue + ack` rate approaches PG's single-node
   write ceiling (~tens of thousands tx/s) and batching/partitioning no longer
   buys headroom. Redis/Valkey single-core does **~100k–1M ops/s** — roughly a
   **5–20× headroom jump** — *but* (path 2) only at an at-least-once durability
   posture (`everysec` + replica + selective `WAITAOF`; non-zero lag-bounded
   loss). If the workload needs zero-loss, the comparison is against
   ElastiCache-durable/MemoryDB, which adds single-digit-ms write latency and
   ~2–3× cost — and that often *erases* the throughput win, which is itself the
   argument for staying on Postgres longer.
2. **Vacuum pain:** when dead-tuple churn forces partition-drop gymnastics and
   autovacuum tuning becomes a standing operational cost — Redis's
   delete-is-free model removes that whole class of toil.

**The honest cutover line:** Postgres until you hit *either* the single-node
write wall *or* the vacuum-operability wall, and the workload tolerates
Redis-tier durability. Below that, PG's correctness/ops simplicity wins. We can
put a concrete crossover number on the graph once the load generator gives us
PG's real plateau on the test box.

## Verdict for the decision

- **Path 1 is the lowest-risk, highest-provability build**, and it answers the
  brief's "your Postgres emerged organically — find a reason to improve" with a
  *crisp, demonstrable* improvement over the generic pattern (the comparison
  table above), not a vibe.
- **It is future-proofed** by a logical model that maps 1:1 onto Redis, with a
  **numeric, durability-gated cutover** rather than "rewrite later."
- **Biggest residual risk:** the Honcho-actual-impl comparison (the wall). If
  their current design already maintains the aggregate + per-key lease, Path 1's
  novelty thins and Redis/custom gets more attractive on differentiation
  grounds. Resolve that gate before committing to build.

## Open items

1. Run the Honcho-actual-impl comparison (gated, post-design, before build).
2. Decide `tasks` reaping strategy (partition-drop vs batch-delete vs
   mark+archive) — drives the vacuum-pain ceiling and the cutover #1 threshold.
3. Pin the PG plateau number from the load generator → put the crossover on the
   throughput graph.
