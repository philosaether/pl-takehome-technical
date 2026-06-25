---
Status: accepted
Date: 2026-06-25
Accepted: 2026-06-25
Assessment: ../assessments/path1-postgres-done-right-plus-migration.md
---

# Postgres Buffered Work-Unit Queue — Desired State

A work queue on Postgres that delivers the two hard properties — **buffered
cost-threshold gating** and **exclusive in-order draining per work unit** — at
high worker scale, with a maintained aggregate so the scheduler look-ahead never
scans. The "safe, defensible" build; Redis/Valkey is a forward-compatible
upgrade we may justify later (Path 2).

---

## Invariants (the whole design exists to hold these)

- **I1 — Order.** Tasks in a work unit are processed in strict arrival order.
- **I2 — Exclusivity.** At most one worker owns a work unit at any instant.
- **I3 — Exact aggregate.** `pending_cost` equals the sum of unacked task costs
  for the unit, *maintained incrementally*, never recomputed by scanning tasks.
- **I4 — Gate.** A unit is processed below threshold T *only* via the flush path.
- **I5 — Ack integrity.** An acked task is never redelivered; an unacked task is
  never skipped. Order survives claim/release churn and worker crashes.

Delivery semantics: **at-least-once delivery, exactly-once ack, always in-order.**
True exactly-once *effect* is impossible in general (effect + ack aren't atomic
across systems); we offer a **transactional-ack** path for effects that live in
the same Postgres (no added latency — same commit), and assume **idempotent
handlers** otherwise.

**Design posture — loss-tolerant, latency-sensitive (drives every durability
fork).** Per Phil: a Honcho task is one *observation about a user*, not a
financial transaction — losing or double-recording one is survivable;
**latency in the derivation loop is a real UX hit for clients**. So we treat
"exactly-once" as aspirational, not a hard rule, and **never trade loop latency
for stronger delivery guarantees.** Consequence: at-least-once + idempotent
handlers is the default; we do *not* add synchronous-durability latency on the
hot path. (This same requirement is a point *in favor of* Path 2 — Valkey's
low-latency / bounded-loss profile fits "loss-tolerant, latency-sensitive"
better than a zero-loss ledger would. Flag for the Path-2 comparison.)

---

## Data model

```sql
-- One row per live work unit. Carries the maintained aggregate + claim state.
CREATE TABLE work_units (
  wu_key          bytea PRIMARY KEY,         -- hash(workspace, session, peer)
  tenant          bytea NOT NULL,            -- workspace; drives per-tenant T
  pending_cost    bigint NOT NULL DEFAULT 0, -- I3: += cost enqueue, -= cost ack
  next_seq        bigint NOT NULL DEFAULT 0, -- per-unit monotonic seq generator
  threshold       int    NOT NULL,           -- denormalized tenant T (hot-path local)
  eligible        boolean NOT NULL DEFAULT false, -- maintained: cost>=T OR flushed
  claimed_by      text,                      -- worker id; NULL = free
  lease_expires   timestamptz,               -- NULL when free
  oldest_pending_at timestamptz,             -- enqueue time of current head task
  flush_deadline  timestamptz                -- oldest_pending_at + max_wait
);

-- The per-unit FIFO log. Rows deleted on ack (see Reaping).
CREATE TABLE tasks (
  wu_key      bytea  NOT NULL,
  seq         bigint NOT NULL,               -- per-unit arrival order (I1)
  payload     bytea  NOT NULL,
  cost        int    NOT NULL,
  attempts    int    NOT NULL DEFAULT 0,     -- poison-task tracking
  enqueued_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (wu_key, seq)
) PARTITION BY HASH (wu_key);                 -- spread vacuum churn (Reaping)

-- THE look-ahead index: partial, over work_units (10^4-10^5), not tasks (10^6).
CREATE INDEX wu_claimable ON work_units (flush_deadline NULLS LAST, wu_key)
  WHERE eligible AND claimed_by IS NULL;

CREATE TABLE dead_letters (LIKE tasks INCLUDING ALL, failed_at timestamptz);
-- tenant thresholds live in a small config table, denormalized onto work_units:
CREATE TABLE tenant_config (tenant bytea PRIMARY KEY, threshold int NOT NULL,
                            max_wait_ms int NOT NULL);
```

**Why per-unit `seq` assigned under the `work_units` row lock** (not a global
`bigserial`): a `bigserial` is assigned at INSERT, not at commit, so under
concurrent enqueues to one unit a smaller id can commit *after* a larger one —
a worker could process seq 5 while seq 3 is still uncommitted, then seq 3 appears
behind the head. **Ordering hazard.** Assigning `seq` via `UPDATE work_units SET
next_seq = next_seq + 1 RETURNING next_seq` takes the unit's row lock, which
serializes enqueues *for that unit only*: seq order == commit order, gap-free.
Different units don't contend. This is the load-bearing ordering decision.

---

## Operations

### Enqueue — `enqueue(wu_key, payload, cost)`

One transaction, one row lock on the unit (upsert), no scan:

```sql
INSERT INTO work_units (wu_key, tenant, threshold, pending_cost, next_seq,
                        oldest_pending_at, flush_deadline, eligible)
VALUES ($k, $tenant, $T, $cost, 1, now(), now() + $max_wait,  $cost >= $T)
ON CONFLICT (wu_key) DO UPDATE SET
  pending_cost = work_units.pending_cost + $cost,
  next_seq     = work_units.next_seq + 1,                 -- monotonic, never reset
  eligible     = (work_units.pending_cost + $cost) >= work_units.threshold
                 OR work_units.eligible,           -- once flushed, stay eligible
  -- (re)start the flush clock iff the unit was empty/tombstoned (pending_cost=0):
  oldest_pending_at = CASE WHEN work_units.pending_cost = 0
                           THEN now() ELSE work_units.oldest_pending_at END,
  flush_deadline    = CASE WHEN work_units.pending_cost = 0
                           THEN now() + $max_wait ELSE work_units.flush_deadline END
RETURNING next_seq;                                -- → seq for the task row
INSERT INTO tasks (wu_key, seq, payload, cost) VALUES ($k, $seq, $payload, $cost);
```
- O(1) aggregate maintenance (I3). Eligibility flips to true on the enqueue that
  crosses T — no separate scan.
- Cross-unit enqueues are fully parallel; same-unit enqueues serialize (required
  for I1 anyway, and this is the **hot-unit** serialization point — see below).
- The `CASE … pending_cost = 0` arm starts the flush clock for both a brand-new
  unit *and* a revived tombstone (a quiet session that gets a new message) —
  same code path, which is the point of tombstone-reuse.

### Claim — buffered, exclusive, contention-free

```sql
UPDATE work_units SET claimed_by = $me, lease_expires = now() + $lease
WHERE wu_key = (
  SELECT wu_key FROM work_units
  WHERE eligible AND claimed_by IS NULL
  ORDER BY flush_deadline NULLS LAST, wu_key      -- age-fair (Fairness)
  FOR UPDATE SKIP LOCKED LIMIT 1
)
RETURNING wu_key;
```
- `SKIP LOCKED` = exclusivity (I2) + parallelism with **zero claim contention**:
  N workers claim N different units, never block, never double-claim.
- **Look-ahead cost is O(log units) on `wu_claimable`** — index-only, never
  touches `tasks`, independent of the 10⁶ pending tasks (I3/I4). This is the
  first-class look-ahead the brief demands.

### Drain + ack — in-order, batched

```sql
-- drain (read-only): next batch in arrival order
SELECT seq, payload, cost FROM tasks
WHERE wu_key = $k ORDER BY seq LIMIT $batch;
-- ...process each in order, incrementing attempts before the effect...
-- ack the contiguous prefix actually completed, in ONE txn:
DELETE FROM tasks WHERE wu_key = $k AND seq <= $last_acked;
UPDATE work_units SET
  pending_cost = pending_cost - $acked_cost,
  oldest_pending_at = (SELECT min(enqueued_at) FROM tasks WHERE wu_key=$k),
  flush_deadline   = (that min) + $max_wait,
  eligible = (pending_cost - $acked_cost) >= threshold
             OR (that min) + $max_wait <= now(),
  claimed_by = CASE WHEN <continue> THEN $me ELSE NULL END,
  lease_expires = CASE WHEN <continue> THEN now()+$lease ELSE NULL END
WHERE wu_key = $k;
-- if no tasks remain → unit is TOMBSTONED in place (pending_cost=0, eligible=false,
-- claimed_by=NULL, flush_deadline=NULL); the row is NOT deleted here. A revived
-- enqueue reuses it; a TTL reaper GCs tombstones that never come back (Reaping).
```
- Ack is the commit boundary (I5). Acked rows are gone, so they can never
  redeliver. Unacked rows remain → redeliver in order on next claim.
- **Transactional-ack option:** if the task's effect is a write to *this*
  Postgres, do it in the same txn as the DELETE → exactly-once effect for that
  task.
- **Heartbeat:** for long batches, the worker periodically `UPDATE work_units SET
  lease_expires = now()+$lease WHERE wu_key=$k AND claimed_by=$me` so the lease
  doesn't expire mid-batch.

---

## Crash recovery — the 3-of-10 scenario (I1 + I5, provable)

`tasks` t0..t9 in one unit; Worker A claims, acks t0–t2 (deleted), crashes with
t3 in flight:

1. A's lease stops being heartbeated. t3..t9 remain in `tasks`, still ordered.
2. **Reaper** (or lazy check on next claim) finds `claimed_by IS NOT NULL AND
   lease_expires < now()` → resets `claimed_by = NULL, lease_expires = NULL`.
   The unit re-enters `wu_claimable` (still eligible).
3. Worker B claims, drains `WHERE wu_key=k ORDER BY seq` → starts at t3 (t0–t2
   are deleted, so they cannot reappear or be reprocessed). t3 redelivers
   (`attempts` now 2), t4 never precedes t3.
4. B acks t3..t9 → unit empty → `work_units` row **tombstoned** in place
   (Drained; row kept for cheap revival, TTL-reaped if it never returns).

No acked task reprocessed; no task out of order; the in-flight task t3 redelivers
(at-least-once). Demonstrated by the deterministic ordering-under-crash test.

---

## Flush policy (I4 escape — defended)

`flush_deadline = oldest_pending_at + max_wait` (an **age cap on the oldest
pending task**, per-tenant `max_wait`). A unit becomes claimable when
`pending_cost >= T` **OR** `flush_deadline <= now()`.

- **Why age-cap-on-oldest, not an idle timer:** an idle timer resets on every
  enqueue, so a unit that grows slowly-but-steadily below T could **strand
  forever**. Age-cap on the oldest task bounds worst-case latency for *any* task
  regardless of arrival pattern. (I1 still holds — flush changes *when* a unit
  runs, never the *order* within it.)
- **Mechanism:** the time clause can't be baked into the partial index (time
  moves), so a cheap **reaper sweep** flips `eligible = true` where `NOT eligible
  AND flush_deadline <= now()` — over 10⁴–10⁵ unit rows, microseconds. Keeps the
  hot-path claim query index-only.

---

## Failure modes

- **Hot work unit (100× traffic).** By I1 a unit is processed by exactly one
  worker — *cannot* be parallelized. Per-unit throughput is therefore capped at
  one worker's drain rate; if arrival > that, the backlog grows. This is
  **fundamental to ordered-per-key**, not a tuning bug. Honest options: accept +
  backpressure the producer; or relax ordering / shard the key upstream if
  semantics allow (they usually don't within a key). We surface it on the
  dashboard (per-unit `pending_cost`, oldest-task age) rather than hide it.
- **Wedged unit (claimed, not progressing).** If the worker died → lease expiry
  reclaims (above). If it's heartbeating but not acking (stuck mid-task) → add a
  `max_lease_lifetime` / max-claims cap and a progress check (head seq not
  advancing across N heartbeats) → force-release + alert.
- **Stranded unit (never reaches T).** Handled by flush.
- **Poison task.** A task that always fails blocks its unit (FIFO + exclusive).
  `attempts` is incremented before the effect; at `max_attempts` the task is
  moved to `dead_letters`, removed from `tasks`, `pending_cost` decremented, and
  the head advances — unblocking the unit. Without this, one bad task wedges a
  whole unit forever.

---

## Runtime threshold change (per-tenant T)

T lives in `tenant_config` and is **denormalized onto `work_units.threshold`** so
enqueue/claim stay local (no join on the hot path). Changing a tenant's T:
```sql
UPDATE work_units SET threshold = $newT,
  eligible = pending_cost >= $newT OR flush_deadline <= now()
WHERE tenant = $tenant;
```
A bounded UPDATE, not a schema change — *because eligibility is a stored flag,
not a literal in the index predicate.* Per-unit overrides are possible the same
way. (This is why we store `eligible`/`threshold` rather than a partial index on
a constant.)

---

## Reaping / vacuum (the #1 operational tax — and Path 2's main trigger)

delete-on-ack + ad-hoc unit churn generates dead tuples; if autovacuum lags, the
`tasks` table *and* `wu_claimable` bloat — slowing the very claim query we
depend on. Plan:

- `tasks` **HASH-partitioned by `wu_key`** → spreads vacuum across partitions,
  enables parallel autovacuum, localizes index churn.
- Aggressive per-table autovacuum (low scale factor, raised cost limit) on
  `tasks` and `work_units`.
- `work_units` rows **tombstoned on drain, TTL-reaped if they never return**
  (see the lifecycle tradeoff below). The reaper that flips flush-eligible and
  reclaims expired leases also `DELETE`s tombstones idle > `wu_ttl`.
- Monitor dead-tuple ratio + autovacuum lag; when it becomes a standing cost,
  that's a concrete Path-2 cutover signal.

---

## Load generator (demonstrates scale + churn, produces numbers)

- **Producers:** enqueue across many `wu_key`s with **churn** (constantly create
  new keys, drain old), **Zipfian** key distribution to manufacture hot units,
  configurable cost distribution, pinned RNG seed.
- **Workers:** claim → drain(batch) → process(simulated work, *no real LLM*) →
  ack loop; worker count swept across **1 / 10 / 100 / 1000**.
- **Crash injection:** kill workers mid-batch on a schedule.
- **Reported numbers:** throughput (acks/s) vs worker count; claim/look-ahead
  query time (and `EXPLAIN`) at 10⁶ tasks / 10⁴–10⁵ units; eligible-unit count;
  oldest-below-T age; lease-expiry & DLQ rates; per-worker utilization.
- **Reproducible:** docker-compose Postgres (pinned version + tuned config),
  documented box, one `make load-test`, comparable numbers on clone.

---

## The four headline proofs (deterministic, not vibes)

1. **Ordering-under-crash** — the 3-of-10 scenario above; assert the processing
   log is `t0..t9` exactly once, in order, across a forced mid-batch kill.
2. **Gate** — enqueue below T → assert never claimable; cross T → claimable;
   assert nothing processes below T except via flush.
3. **Flush** — enqueue below T, advance time past `max_wait` → assert becomes
   claimable and drains.
4. **Look-ahead cost** — load 10⁶ tasks / 10⁴–10⁵ units; measure claim-query
   time + `EXPLAIN` (index-only on `wu_claimable`); show it's ~flat vs task count,
   contrasted against a naive `SUM(...) GROUP BY` baseline.

---

## Operability

- **Stack:** Go + `pgx` (workers, load generator, tests). Goroutine-per-worker;
  the load generator must out-run PG so the measured plateau is Postgres's, not
  the client's.
- **Deploy:** single primary + streaming replica; docker-compose for the test;
  pinned PG version + tuned `autovacuum`, `commit_delay` (group commit).
- **Monitor:** queue depth, eligible-unit count, oldest-below-T age, claim p99,
  lease-expiry rate, DLQ rate, dead-tuple ratio, replication lag.
- **Worker wakeup:** v1 uses **adaptive poll backoff** (sleep grows when claims
  come back empty, collapses to zero under load), *not* `LISTEN/NOTIFY`. Reason:
  `NOTIFY` takes a global lock on the shared notify queue and wakes *every*
  LISTENer (thundering herd) — both bite hardest at high enqueue rate, which is
  exactly our headline metric, while NOTIFY's only benefit (avoiding idle-poll
  waste) matters only at *low* load. Backoff captures ~all of that benefit
  without the contention. Sharded `LISTEN/NOTIFY` stays a deferred option if
  idle-poll overhead ever shows up.
- **Recovery/upgrades:** failover to replica; reaper makes recovery automatic for
  in-flight claims; rolling worker upgrades are safe (lease expiry covers killed
  workers).

---

## Forward-compat: the Redis/Valkey mapping (Path 2, not built here)

Keep the logical model so migration is a backend swap, not a redesign:

| PG construct | Redis/Valkey equivalent |
|---|---|
| `tasks` per-unit FIFO | one **Stream** per `wu_key` (`XADD`/`XREADGROUP`) |
| `work_units.pending_cost` | counter, `INCRBY`/`DECRBY` in a Lua claim/ack |
| `wu_claimable` partial index | **ZSET** of free+eligible keys (atomic Lua membership) |
| claim lease + reaper | consumer-group PEL + `XAUTOCLAIM min-idle-time` |
| per-unit `seq` order | stream-ID order + PEL |

Cutover trigger (from the assessment): single-node write ceiling *or* vacuum
pain, *and* the workload tolerates Redis-tier durability. Numbers go on the
throughput graph once the load test gives PG's real plateau.

---

## Tradeoffs

- **delete-on-ack vs mark-done.** Chose delete: smaller hot table, simpler drain,
  acked-is-gone makes I5 trivial. Revisit if we need replay/audit → mark +
  archive partition (and then `head_seq` cursor becomes load-bearing).
- **per-unit `seq` under row lock vs global `bigserial`.** Chose per-unit seq:
  guarantees commit-order == seq-order (I1), gap-free. Cost: per-unit enqueue
  serialization (needed for ordering anyway; it *is* the hot-unit point).
  bigserial rejected for the assignment-vs-commit ordering hazard.
- **reaper-flips-eligible vs time-clause-in-claim.** Chose reaper-flips: keeps
  the hot-path claim index-only. Revisit if reaper lag hurts flush latency.
- **lease/visibility-timeout vs advisory locks / held `FOR UPDATE`.** Chose
  lease: survives worker disconnect/crash cleanly without holding a connection or
  long-lived txn. Advisory locks die with the session and pin a connection.
- **age-cap-on-oldest flush vs idle timer.** Chose age-cap: bounds worst-case
  task latency, no starvation. Idle timer can strand a steadily-growing unit.
- **HASH-partition `tasks` vs single table.** Chose partition: spreads the
  vacuum/bloat tax, the known PG-as-queue failure mode.
- **`work_units` lifecycle: delete-on-drain vs tombstone-reuse vs tombstone+TTL.**
  The deciding factor is *key recurrence vs churn*:
  - *delete-on-drain* — remove the row when the unit empties. Pro: no unbounded
    row growth under pure churn. Con: insert+delete churn (bloat/vacuum) for keys
    that drain then immediately return, plus re-pay row creation each time.
  - *tombstone-reuse* — keep the drained row (cost=0, eligible=false), revive on
    next enqueue. Pro: zero churn for recurring keys. Con: **leaks rows forever**
    for keys that never come back → unbounded growth under true churn.
  - **Chosen: tombstone + TTL reap** (hybrid). Honcho keys are `(workspace,
    session, peer)` — a conversation that goes quiet and resumes is the *common*
    case, so most keys recur and benefit from reuse; the TTL reaper GCs the
    genuinely-dead ones so we don't leak. Best of both, at the cost of one extra
    reaper sweep. Calculus changes if real keys turn out to be mostly
    *single-shot* (then delete-on-drain is simpler) — **validate against Honcho's
    real recurrence later.**

---

## Open Questions

1. ~~delete vs mark~~ — **RESOLVED: delete-on-ack.** Tasks are snapshots of real
   user conversation; keeping them indefinitely is weird, and acked-is-gone makes
   I5 trivial. (Brief doesn't prescribe → our call.)
2. ~~Exactly-once~~ — **RESOLVED: at-least-once + idempotent handlers; exactly-once
   is aspirational.** Promoted to the **loss-tolerant, latency-sensitive design
   posture** at the top — losing/double-recording one observation is survivable;
   loop latency is the real UX cost, so we never trade latency for delivery
   guarantees. Transactional-ack stays available at *no* latency cost for
   same-PG effects. Doubles as a Path-2 argument (Valkey's profile fits this).
3. ~~Tenant granularity for T~~ — **RESOLVED: workspace == tenant.** `threshold`/
   `max_wait` are per-workspace, denormalized onto `work_units`. Per-unit
   override remains possible but unused by default.
4. ~~work_units lifecycle~~ — **RESOLVED: tombstone + TTL reap.** Tradeoff laid
   out in Tradeoffs; recurrence-vs-churn is the deciding factor; validate against
   Honcho key recurrence later.
5. ~~Wakeup~~ — **RESOLVED: adaptive poll backoff in v1, not `LISTEN/NOTIFY`.**
   Reason (global notify-queue lock + thundering herd bite hardest at high
   throughput, where NOTIFY helps least) written into Operability. Sharded NOTIFY
   deferred.
6. **Defaults to tune from the load test** — batch cap, lease duration,
   `max_wait`, partition count, autovacuum settings, poll-backoff curve, `wu_ttl`.
   (Resolved-by-measurement; not a blocker.)
7. ~~Partitioning for the load test~~ — **RESOLVED: 2 HASH partitions on `tasks`.**
   Scope cost is ~nil — `PARTITION BY HASH (wu_key)` + two `PARTITION OF` DDL
   lines, **zero application-code change** (PK already leads with `wu_key`; Postgres
   routes/prunes automatically). `work_units` stays unpartitioned (it's small).
   The PoC value is real: demonstrates the vacuum-spread story *and* pre-stages
   the Path-2 shard-by-key narrative. Worth it.
8. ~~Language/stack~~ — **RESOLVED: Go** (`pgx`). Decisive reason isn't raw CPU
   (worker compute is simulated/trivial; PG's single-writer ceiling binds) — it's
   that the **load generator must not become the bottleneck** when measuring PG's
   plateau (Go saturates PG via goroutines; a GIL-bound Python async client can
   itself cap throughput and corrupt the headline numbers), plus an honest
   concurrency model for the crash tests. Carries into Path-2/sharding.

*All open questions resolved or deferred-to-measurement. Draft is ready for a
read-through → `/ship`, then the Honcho-actual comparison.*

---

## Out of Scope

- Path 2 Redis/Valkey build (forward-compat mapping only).
- Path 3 custom WAL engine (cut).
- Multi-machine / distributed (sharding noted as the future direction only).
- Real LLM calls (load test simulates downstream work).
- Comparison against Honcho's *actual* implementation — a gated step *after* a
  couple of draft iterations close the open questions (per the plan).
