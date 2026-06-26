---
Status: accepted
Date: 2026-06-25
Accepted: 2026-06-25
Amended: 2026-06-26
Assessment: ../assessments/path2-redis-durability-recovery.md
Related: postgres-work-unit-queue.md (accepted Path 1)
Gated-by: ../assessments/honcho-actual-comparison.md (narrowed the decision bar)
Amendments:
  - id: A1
    title: Isolation model (shared queue) + fairness grain + shard by workspace
    date: 2026-06-26
    status: accepted
---

# Valkey Buffered Work-Unit Queue — Desired State

The same two hard properties as Path 1 — **buffered cost-threshold gating** and
**exclusive in-order draining per work unit** — built on Valkey/Redis primitives
(Streams + ZSETs + Lua), and the **upgrade we must *justify*** over the accepted
Postgres design. This doc defines what "Valkey done right" looks like **and** the
measurement bar that decides whether we build it.

---

## The decision this doc must settle (stated up front, because it's the point)

The Honcho-actual comparison moved the goalposts. **The look-ahead win — a
maintained O(log n) aggregate instead of a per-poll `SUM…GROUP BY` scan — is
already banked in Path 1** (Postgres `work_units.pending_cost` + partial index).
A Redis ZSET would give the same complexity. So "Redis makes the look-ahead
cheap" is **no longer a differentiator.**

That leaves Path 2 with exactly **two** things Postgres structurally cannot
escape, and they are the *only* grounds on which this design earns its keep:

1. **The single-primary write ceiling.** Every Path-1 enqueue/ack funnels through
   one Postgres WAL. Valkey shards by `wu_key` across N independent primaries →
   write throughput scales ~linearly. (§Horizontal scaling.)
2. **The latency posture.** In-memory microsecond claim→drain→ack cycle vs a WAL
   fsync per ack. Path 1's own posture — *"never trade loop latency for delivery
   guarantees"* — is the argument *for* Valkey here. (§Durability & loss.)

**Decision gate (not vibes): build Path 2 only if the head-to-head load test shows
a Path-1 throughput plateau below target *and/or* a loop-latency gap the
derivation loop actually cares about — at a durability config we'll defend.** If
the single-PG ceiling is never reached at the workload's real scale, Path 1's
"it's just ACID" simplicity wins and we ship that. This doc is written so that
gate is decidable, not so that the answer is foreordained.

---

## Invariants (same I1–I5; new mechanisms; one durability delta)

- **I1 — Order.** Tasks in a unit are processed in strict arrival order. *Mechanism:*
  Stream-ID order + exactly one worker per unit (the lease) + `XAUTOCLAIM`
  re-delivers in-flight entries in stream-ID order on takeover.
- **I2 — Exclusivity.** At most one worker owns a unit at any instant. *Mechanism:*
  the unit lease is granted by an **atomic Lua claim** (Redis single-threaded
  execution ⇒ no interleaving, no double-claim).
- **I3 — Exact aggregate.** `pending_cost` = sum of unacked task costs, *maintained*
  `INCRBY`/`DECRBY` inside the enqueue/ack Lua, **never scanned.** (This is the
  property already banked in Path 1 — parity, not novelty.)
- **I4 — Gate.** A unit is claimable only after it enters the `eligible` ZSET, which
  happens *only* via `pending_cost ≥ T` or flush-promotion. Claim pops from
  `eligible` and nowhere else.
- **I5 — Ack integrity.** `XACK` removes an entry from the PEL → never redelivered.
  Unacked entries stay in the PEL/stream → redeliver in order via `XAUTOCLAIM`.

**Durability delta vs Path 1 (the one real difference).** Postgres makes the ack
durable at commit (WAL fsync). Valkey makes `XADD`/`XACK` durable only at the next
AOF fsync — a bounded **≤~1s window** at `appendfsync everysec`. The risk is
**asymmetric and in our favour**: losing a trailing `XACK` → the message
redelivers (benign under at-least-once + idempotent handlers); losing a trailing
`XADD` → the enqueue never happened (the real, *accepted* loss). We accept it
*because of the posture below* — an observation is not a ledger. This asymmetry is
the crux of the durability defense (§Durability & loss).

---

## Design posture — loss-tolerant, latency-sensitive (now load-bearing)

Carried verbatim from Path 1, but here it is the **reason Valkey qualifies at
all**, not a footnote:

> A Honcho task is one *observation about a user*, not a financial transaction —
> losing or double-recording one is survivable; **latency in the derivation loop
> is the real UX cost.** Never trade loop latency for stronger delivery
> guarantees.

Consequence for this design: at-least-once + idempotent handlers is the default,
and **we do not put `WAITAOF` on the hot path** (it would convert an async ack
into a semi-sync disk round-trip — exactly the latency we refuse to trade). The
≤1s `everysec` + replica loss window is the price of the latency win, and the
posture is what makes that price acceptable. Zero-loss exists (managed durable
tier) and we *name* it, but running it would forfeit the very latency edge that
justifies Path 2.

---

## Data model (Valkey structures)

Per work unit `{wu_key}`:

| Key | Type | Holds | Path-1 analog | Invariant |
|---|---|---|---|---|
| `s:{wu_key}` | **Stream** | the per-unit FIFO tasks; entry fields `payload`, `cost` | `tasks` rows | I1 |
| group `g` on `s:{wu_key}` | consumer group | delivery cursor + **PEL** (in-flight, delivery_count) | claim lease + `attempts` | I1·I5 |
| `wu:{wu_key}` | Hash | `pending_cost`, `threshold`, `oldest_pending_ms`, `flush_deadline`, `claimed_by`, `lease_expires` | the `work_units` row | I3·I4 |

Global (per shard — see §Horizontal scaling):

| Key | Type | Holds | Path-1 analog | Invariant |
|---|---|---|---|---|
| `eligible` | **ZSET** | free + claimable units, score = `flush_deadline` | partial index `wu_claimable` | I2·I4 |
| `pending_flush` | ZSET | free units below T, score = `flush_deadline` | rows where `NOT eligible AND flush_deadline IS NOT NULL` | I4 |
| `leases` | ZSET | claimed units, score = `lease_expires` | `lease_expires` column scanned by reaper | I2·I5 |
| `dlq` | Stream | dead-lettered poison entries | `dead_letters` table | — |

Two ZSETs scored by `flush_deadline` (`eligible`, `pending_flush`) replace the one
partial index; one ZSET scored by `lease_expires` (`leases`) replaces the
lease-expiry scan. **`ZPOPMIN`/`ZRANGEBYSCORE` over these is the O(log n)
look-ahead** — same complexity Path 1 gets from the partial index, which is why
this is parity, not advantage.

**Why a unit lease *and* a consumer group (not redundant).** They operate at
different granularities: the **lease** (`eligible`/`leases` ZSET membership)
answers *"which unit, and who owns it"* — it enforces one-worker-per-unit (I2) and
the gate (I4). The **consumer group + PEL** answers *"which messages within that
unit are delivered/in-flight/acked, in order"* (I1·I5) and gives crash-redelivery
+ `delivery_count` for free. The lease re-surfaces the *unit*; the PEL resumes the
*stream mid-flight*. Both participate in recovery (§Crash recovery).

---

## Operations

Atomicity is the whole game. Postgres got it from one transaction + the unit row
lock. **Valkey gets it from Lua: a script runs to completion with no other command
interleaved** (single-threaded). Enqueue, claim, and ack are each one Lua script.
That single-thread guarantee is the load-bearing mechanism here, exactly as the
row lock was in Path 1.

### Enqueue — `enqueue(wu_key, payload, cost)` (one Lua script)

```lua
-- KEYS: s:{wu_key}, wu:{wu_key}, eligible, pending_flush ; ARGV: payload, cost, T, max_wait, now
local id   = redis.call('XADD', KEYS[1], '*', 'p', ARGV[1], 'c', ARGV[2])
local cost = redis.call('HINCRBY', KEYS[2], 'pending_cost', ARGV[2])   -- I3, O(1)
-- (re)start the flush clock iff the unit was empty/tombstoned (pending_cost == cost now):
if tonumber(cost) == tonumber(ARGV[2]) then
  redis.call('HSET', KEYS[2], 'oldest_pending_ms', ARGV[5],
                              'flush_deadline', ARGV[5] + ARGV[4], 'threshold', ARGV[3])
end
local dl = redis.call('HGET', KEYS[2], 'flush_deadline')
if redis.call('HGET', KEYS[2], 'claimed_by') == false then            -- only re-index if free
  if tonumber(cost) >= tonumber(ARGV[3]) then
    redis.call('ZADD', KEYS[3], dl, KEYS[2]); redis.call('ZREM', KEYS[4], KEYS[2])   -- → eligible (I4)
  else
    redis.call('ZADD', KEYS[4], dl, KEYS[2])                                          -- → pending_flush
  end
end
return id
```

O(1) aggregate maintenance; eligibility flips on the enqueue that crosses T — no
scan. Same-unit enqueues serialize on the single Redis thread (required for I1
anyway, and is the **hot-unit serialization point**, §Failure modes); cross-unit
enqueues to *different shards* are fully parallel.

### Claim — buffered, exclusive (one Lua script)

```lua
-- KEYS: eligible, leases ; ARGV: me, lease_ms, now
local hit = redis.call('ZPOPMIN', KEYS[1], 1)          -- lowest flush_deadline = age-fair
if hit[1] == nil then return nil end
local wu = hit[1]
redis.call('HSET', 'wu:'..wu, 'claimed_by', ARGV[1], 'lease_expires', ARGV[3] + ARGV[2])
redis.call('ZADD', KEYS[2], ARGV[3] + ARGV[2], wu)     -- track lease for the reaper
return wu
```

`ZPOPMIN` atomically removes the unit from `eligible` and hands it to one worker —
exclusivity (I2) with no double-claim, age-fair by `flush_deadline` score. The
worker then drains via the consumer group.

> **Honest cost (the contention point moves, it doesn't vanish).** Path 1's `SKIP
> LOCKED` lets N workers claim N *different* rows truly in parallel. Here every
> claim funnels through the **one Redis thread per shard**. Each claim is a
> microsecond `ZPOPMIN`, so the funnel is wide — but it *is* a serialization
> point, and whether it caps throughput at 1000 workers is a **measured** question
> (§Open Questions), and a direct motivation for sharding.

### Drain + ack — in-order, batched

```text
# drain: next batch in arrival order (creates PEL entries)
XREADGROUP GROUP g <me> COUNT <batch> STREAMS s:{wu_key} >
# ...process each in order, in stream-ID order...
```

Ack the contiguous completed prefix in one Lua script:

```lua
-- XACK removes from PEL (I5: never redelivered); XDEL is delete-on-ack (keeps the stream small)
redis.call('XACK', 's:'..wu, 'g', unpack(ids))
redis.call('XDEL', 's:'..wu, unpack(ids))
redis.call('HINCRBY', 'wu:'..wu, 'pending_cost', -acked_cost)         -- I3
-- recompute head age + flush_deadline from the new head (XRANGE … COUNT 1), then:
--   tasks remain & continue  → keep lease (ZADD leases new_expiry)
--   tasks remain & release   → ZREM leases; re-index into eligible|pending_flush (free again)
--   no tasks remain          → TOMBSTONE: clear cost; EXPIRE wu:{wu_key} and s:{wu_key} (wu_ttl)
```

- `XACK` is the commit boundary for I5; `XDEL` makes acked-is-gone literal.
- **Transactional-ack analog:** if the effect is itself a Redis write in the same
  shard, fold it into the ack Lua → exactly-once effect for that task at no extra
  latency. Cross-system effects stay idempotent-handler territory (as in Path 1).
- **Tombstone reaping is *free* here.** Path 1 needs a reaper `DELETE` to GC dead
  units; Valkey just sets `EXPIRE` on the unit hash + stream — Redis key TTL GCs
  the genuinely-dead ones, and a revived enqueue before expiry reuses the keys.
  Small, but a real Path-2 ergonomic win over the PG tombstone+TTL sweep.
- **Heartbeat:** for long batches the worker re-`ZADD leases`/`HSET lease_expires`
  to push the lease out so it doesn't expire mid-batch.

### Reaper — two ZSET sweeps (cheap, periodic)

```text
# 1. flush-flip: promote units whose deadline passed   (= Path 1 "flip eligible WHERE flush_deadline<=now")
ZRANGEBYSCORE pending_flush 0 <now>   → for each: ZREM pending_flush ; ZADD eligible <deadline>
# 2. lease-reclaim: expired leases → unit re-surfaces   (= Path 1 lease reaper)
ZRANGEBYSCORE leases 0 <now>          → for each stale unit: clear claimed_by ;
                                        re-index into eligible|pending_flush
```

Both are `ZRANGEBYSCORE` over 10⁴–10⁵ unit-sized ZSETs — microseconds, never
touches a stream.

---

## Crash recovery — the 3-of-10 scenario (I1 + I5, provable)

Unit with t0..t9; Worker A claims (holds the lease, group consumer = A), `XACK`s
t0–t2 (`XDEL`ed), crashes with t3 in flight (t3 in A's PEL; t4..t9 undelivered).

1. A stops heartbeating → A's entry in the `leases` ZSET ages past `now`.
2. **Reaper sweep 2** finds it (`ZRANGEBYSCORE leases 0 now`) → clears
   `claimed_by`, re-indexes the unit into `eligible` (still cost-eligible or
   flush-due). The *unit* is claimable again.
3. Worker B claims the unit (atomic `ZPOPMIN`). To resume **mid-stream**, B takes
   over A's orphaned PEL entries:
   `XAUTOCLAIM s:{wu_key} g B <min-idle = lease> 0` → reassigns t3 (and any other
   delivered-unacked) to B **in stream-ID order**, then `XREADGROUP … >` continues
   from t4. t0–t2 are `XDEL`ed → cannot reappear. t3 redelivers (`delivery_count`
   now 2); t4 never precedes t3.
4. B acks t3..t9 → unit empty → hash + stream `EXPIRE`d (tombstone; revival before
   TTL reuses the keys).

**The two-granularity point:** lease expiry recovers the *unit* (re-surfaces it);
`XAUTOCLAIM` recovers the *messages* (resumes mid-stream, in order). `min-idle` on
`XAUTOCLAIM` must be ≥ the lease so we never steal from a live worker. No acked
task reprocessed; no task out of order; in-flight t3 redelivers (at-least-once).
Deterministic test, same shape as Path 1's.

---

## Flush policy (I4 escape — same semantics as Path 1)

`flush_deadline = oldest_pending_ms + max_wait` — an **age cap on the oldest
pending task** (per-tenant `max_wait`). A unit becomes claimable when
`pending_cost ≥ T` (enqueue indexes it into `eligible`) **OR** when the reaper's
flush-flip sweep promotes it from `pending_flush` to `eligible` at its deadline.
Age-cap-on-oldest (not idle-timer) for the same reason as Path 1: a steadily-but-
slowly-growing unit must not strand forever. I1 holds — flush changes *when* a
unit runs, never the order within it.

---

## Durability & loss (the defense Path 1 didn't have to make)

Self-hosted config we'd actually run (from the assessment):

```
appendonly yes
appendfsync everysec            # ≤~1s loss window; the sane queue default
aof-use-rdb-preamble yes        # RDB-speed load + AOF-granular durability
min-replicas-to-write 1
min-replicas-max-lag 10         # master refuses writes if the replica falls behind → caps loss
noeviction                      # a full instance REJECTS enqueues, never silently drops queued work
# NOT on the hot path: WAITAOF — it trades the latency we refuse to trade (posture).
```

- **Accepted residual loss:** ≤~1s (`everysec`) + async replication lag on
  unclean failover. Asymmetric in our favour (lost `XACK` = benign redelivery;
  lost `XADD` = the real loss). Acceptable *because* an observation isn't a ledger.
- **`noeviction` is non-negotiable for queue data:** eviction is a propagated
  `DEL` — evicted queue entries are gone forever. Keep `maxmemory` well under host
  RAM; `vm.overcommit_memory=1` so snapshot `fork()` COW doesn't OOM.
- **Zero-loss path, named not run:** managed durable tier (ElastiCache-for-Valkey
  durable, June 2026, or MemoryDB) commits to a multi-AZ log before ack —
  single-digit-ms writes, ~2–3× cost. That *is* the latency we're avoiding, so we
  name it as the production knob and don't run it on the laptop.
- **Recovery time = AOF tail replay** (re-execute commands, CPU-bound, not a
  memmap). No reliable GB/s figure exists — **measure it on our dataset in the
  load test, don't quote it.**

---

## Failure modes

- **Hot work unit (100× traffic).** Same *fundamental* cap as Path 1: I1 ⇒ one
  worker per unit ⇒ per-unit throughput is one worker's drain rate; arrival beyond
  that grows backlog. Not a tuning bug — intrinsic to ordered-per-key. (The
  contention *substrate* differs: here same-unit enqueues serialize on the Redis
  thread rather than a PG row lock, but the per-unit drain ceiling is identical.)
  Surface per-unit `pending_cost` + oldest-age on the dashboard; backpressure the
  producer.
- **Poison task.** PEL `delivery_count` (from `XAUTOCLAIM`/`XPENDING`) is the retry
  counter **for free** — Path 1 maintains `attempts` by hand. At
  `max_delivery_count`: `XACK` + `XDEL` the poison entry, `XADD` it to `dlq`,
  `DECRBY` its cost, advance the head → unit unblocks.
- **Wedged unit (lease held, not progressing).** Crash → lease expiry reclaims
  (above). Heartbeating-but-not-acking → `max_lease_lifetime` / head-not-advancing
  check → force-release + alert. (Same as Path 1.)
- **OOM / eviction.** Covered by `noeviction` + `maxmemory` headroom above.

---

## Horizontal scaling — the actual justification (and the distributed stretch)

This is where Path 2 beats Path 1 or it doesn't beat it at all.

**Shard by `wu_key` across N independent Valkey primaries.** Each shard owns the
units that hash to it **and its own local `eligible` / `pending_flush` / `leases`
ZSETs**. A unit's stream + hash + its shard's coordination ZSETs all live on the
**same instance**, so the enqueue/claim/ack Lua scripts stay single-instance
atomic. Write throughput scales ~linearly with shard count — the thing Postgres's
single WAL structurally cannot do.

**The Cluster cross-slot constraint *forces* this design — and that's a feature,
not a workaround.** A Lua script in Redis Cluster may only touch keys in one hash
slot; our enqueue touches a per-unit stream *and* the global ZSETs, which would
span slots. The shard-local-ZSET layout makes coordination state slot-local by
construction, so atomicity holds *within* a shard and there is never a cross-shard
script. Workers poll per shard (or one worker pool per shard). No work unit ever
spans shards → I1/I2 are unaffected by sharding.

**Why this is the honest spine of the Path-2 case:** Path 1 already pre-staged it
— the "2 HASH partitions on `tasks`" decision was explicitly the shard-by-key
dress rehearsal. Path 2 cashes it: partitions on one primary → independent
primaries. The load test runs **1 shard (per-node ceiling) → 2–4 shards (linear
scaling)** against **1 PG primary's plateau**, apples-to-apples.

---

## Load generator (reuse Path 1's harness — apples-to-apples is the point)

- **Same Go harness, backend behind an interface.** Producers (Zipfian `wu_key`
  churn, pinned seed, configurable cost dist), workers (claim→drain→process-
  simulated→ack), crash injection — all reused; only the queue driver swaps
  Postgres↔Valkey. One harness, two backends → directly comparable numbers, which
  is the whole reason the comparison is credible.
- **Sweeps:** worker count 1/10/100/1000; **shard count 1/2/4** (Path 2 only) vs
  1 PG primary (Path 1).
- **Reported, head-to-head:** throughput (acks/s) vs workers *and* vs shards;
  **claim→ack loop latency p50/p99** (the posture metric); claim-funnel latency at
  1000 workers; AOF fsync lag; replication offset lag; PEL depth; `eligible` ZSET
  cardinality; oldest-below-T age; DLQ rate; AOF-replay recovery time.
- **Reproducible:** docker-compose Valkey (pinned version + the durability config
  above), one `make load-test-valkey`, comparable on a clone.

---

## The four headline proofs (note how #4 shifted — that *is* the story)

1. **Ordering-under-crash** — the 3-of-10 scenario via lease expiry + `XAUTOCLAIM`;
   assert the processing log is `t0..t9` once, in order, across a forced kill.
2. **Gate** — below T → not in `eligible` → never claimed; cross T → claimed;
   nothing processes below T except via flush.
3. **Flush** — below T, advance past `max_wait` → reaper promotes
   `pending_flush`→`eligible` → drains.
4. **Throughput + latency head-to-head** — **the headline moved.** Path 1's #4 was
   *look-ahead cost* (now banked in both, so it no longer separates them). Here #4
   is the **decision gate itself**: 1→4 Valkey shards vs 1 PG primary, on the same
   harness, reporting the write-throughput ceiling *and* loop-latency p99. This is
   the proof that either justifies Path 2 or sends us back to ship Path 1.

---

## Operability

- **Stack:** Go (carries from Path 1's decision — load generator must out-run the
  backend); **`rueidis`** client (built-in pipelining/auto-pipelining +
  client-side caching — both clients unfamiliar, so we pick on throughput); Lua
  via `EVALSHA`. Goroutine-per-worker.
- **Engine:** **Valkey 8.1+** self-hosted via Docker (BSD-licensed, default engine
  on managed clouds, ~20% cheaper, no SSPL/AGPL review). **Reclaim via
  `XAUTOCLAIM`** (available in both). We *do not* pin Redis 8.4 for
  `XREADGROUP … CLAIM`: verified that feature is Redis-8.4-only (Valkey ≤9.1 has
  no `CLAIM` option), and it only optimizes *large*-PEL reclaim — ours is small
  (one worker/unit, bounded batch), so the win is negligible and not worth
  forfeiting Valkey's license/cost/ecosystem edge.
- **Deploy:** primary + replica per shard; docker-compose for the test.
- **Monitor:** per-shard ops/s, claim p99, `eligible`/`pending_flush` cardinality,
  PEL depth, `delivery_count` distribution, DLQ rate, AOF fsync lag, replication
  offset lag, `used_memory` vs `maxmemory`.
- **Wakeup:** adaptive poll backoff (sleep grows on empty claims, collapses under
  load) — same call as Path 1, same reasoning (and what Honcho independently
  does). Streams' blocking `XREADGROUP BLOCK` is available for the *intra-unit*
  drain once a unit is claimed.

---

## When Path 1 wins instead (the off-ramp, stated honestly)

If the load test shows the single-PG primary never plateaus below the workload's
real target, **Path 1 wins and we ship it.** Honcho runs `WORKERS=1` today — the
regime where a single Postgres is not just adequate but *obviously simpler* ("it's
just ACID," one system to operate, no AOF/replication/eviction defense). Path 2's
extra surface — Lua-scripted atomicity, a durability/loss argument, sharded
operations, two coordination ZSETs per shard — is only worth it if the throughput
ceiling or latency gap is real and the workload tolerates ≤1s loss. The doc exists
to make that a measured call, not a preference.

---

## Tradeoffs

- **Consumer-group + PEL vs plain stream + head cursor.** Chose consumer groups:
  `delivery_count` (poison caps) and `XAUTOCLAIM` (in-order crash redelivery) come
  free and idiomatic. Cost: two mechanisms (unit lease + PEL) to reason about.
  *Revisit* → a plain stream with a per-unit `head_seq` cursor mirrors Path 1
  1:1 and is simpler, but re-implements `delivery_count` by hand and forgoes the
  primitive that most distinguishes Streams. Worth prototyping both in the proof.
- **Lua vs `MULTI/EXEC` vs `WATCH`.** Chose Lua: atomic *with* conditional logic
  (eligibility branch, flush-clock branch) in one round trip; `MULTI/EXEC` can't
  branch on intermediate reads, `WATCH`/retry adds latency under contention.
- **Shard-local ZSETs vs one global coordination set.** Chose shard-local: keeps
  every Lua single-slot (Cluster-safe) and makes write scaling linear. Cost:
  age-fairness and "globally oldest unit" are *per-shard*, not global — acceptable
  (fairness within a shard; cross-shard skew is a balancing concern, not a
  correctness one).
- **`everysec` + replica vs `WAITAOF` on the hot path.** Chose `everysec`: the
  posture forbids spending loop latency on durability. `WAITAOF` stays available
  for a *named* critical-enqueue path, not the default. Calculus changes only if
  the workload reclassifies from "observation" to "ledger."
- **delete-on-ack via `XDEL`/`XTRIM MINID` vs keep the stream.** Chose
  delete-on-ack: small hot stream, acked-is-gone makes I5 trivial — same call as
  Path 1. Revisit if replay/audit is needed (then keep + `MINID` cursor).
- **Tombstone via key `EXPIRE` vs reaper `DELETE`.** Chose key TTL: native, free,
  no sweep — a genuine ergonomic win over Path 1's tombstone+TTL reaper.
- **Valkey vs Redis 8.4.** **Resolved → Valkey.** Verified Valkey (≤9.1) has no
  `XREADGROUP CLAIM`; it's Redis-8.4-only. That feature only speeds *large*-PEL
  reclaim, which our small per-unit PEL never hits, so we lose ~nothing by using
  `XAUTOCLAIM` and keep Valkey's license/cost/managed-default advantages.

---

## Open Questions

1. **Does the latency win survive `everysec` + replica + `min-replicas-to-write`?**
   The whole pitch is loop latency; measure that the durability config we'd defend
   doesn't erode it back toward Postgres. *(Resolved by the head-to-head test.)*
2. **Is the single-thread claim funnel a real ceiling at 1000 workers, or
   microsecond-negligible?** If it caps a single shard, that itself is a sharding
   argument — but quantify it. *(Measured.)*
3. ~~**Valkey parity for `XREADGROUP … CLAIM`**~~ — **RESOLVED: no parity; stay on
   Valkey anyway.** Verified against current Valkey docs/9.1 notes — `XREADGROUP`
   options are `GROUP/COUNT/BLOCK/NOACK/STREAMS` only; `CLAIM` is Redis-8.4-only.
   It optimizes *large*-PEL reclaim; our per-unit PEL is small (one worker/unit,
   bounded batch), so `XAUTOCLAIM` costs us ~nothing and we keep Valkey's
   license/cost/ecosystem edge. (Engine + Tradeoffs updated.)
4. **Consumer-group+PEL vs head-cursor** — prototype both in the ordering-under-
   crash proof; pick on simplicity vs free-`delivery_count`. *(Tradeoffs.)*
5. **Shard count for the test** — 1/2/4 proposed; enough to show linearity without
   over-building the harness. *(By measurement.)*
6. ~~**Client:** `rueidis` vs `go-redis`~~ — **RESOLVED: `rueidis`.** Both
   unfamiliar, so pipelining/throughput wins. (Stack updated.)
7. **Defaults to tune from the load test** — lease duration, batch cap, `max_wait`,
   `max_delivery_count`, poll-backoff curve, `wu_ttl`, `maxmemory` headroom.
   *(Resolved-by-measurement; not a blocker.)*

---

## Out of Scope

- **The build itself until the decision gate clears** — this doc defines Valkey-
  done-right and the measurement that justifies (or rejects) building it.
- Managed durable tier (named as the zero-loss production knob, not run).
- Rebuilding Path 1 (accepted; the Postgres design stands as the fallback).
- Real LLM calls (load test simulates downstream work, as in Path 1).
- Multi-region / cross-shard transactions (sharding is single-region, key-local).

---

## Amendments

### A1: Isolation model (shared queue) + fairness grain + shard by workspace (2026-06-26)

**Status:** accepted
**Trigger:** Resolving the parked per-tenant isolation question (gated M3b in
`../roadmap.md`). The graded brief never required per-customer isolation — only
"failure isolation" in the sharding question
(`../inbox/platform-screening-brief.md:175`). The "isolated resources per
customer" language was Phil's meeting capture
(`../inbox/pl-takehome-technical.md:10`), which he **corrected**: it meant *one
instance processes a tenant's unit at a time* (the exclusive lease), not a
cluster per tenant. See `../decisions.md` 2026-06-26. Mirrors A1 on
`postgres-work-unit-queue.md`.

**Refined reasoning:**
- **Isolation = a single shared queue across all tenants.** No deployment-level
  per-customer isolation. The only "isolation" guarantee is the **atomic Lua
  unit lease** already in I2 / §Claim — at-most-one-worker per unit. Confirms the
  existing model; no structure changes.
- **Shard by `hash(workspace)`, not full `wu_key`.** §"The decision this doc must
  settle" and §Horizontal scaling currently say *shard by `wu_key`*; refine to
  **`workspace`** — the full key scatters one workspace's sessions across
  primaries, while `workspace` keeps a whole tenant on one shard (clean seam +
  failure domain). The shard-local coordination ZSETs (`eligible`,
  `pending_flush`, `leases`) are unchanged; only the routing key narrows to the
  `workspace` prefix of `wu_key`. Linearity argument is unaffected (workspaces
  vastly outnumber shards).
- **Fairness is two grains.** `Claim` is `ZPOPMIN eligible` (score =
  `flush_deadline`) → **work-unit age-fairness**: no unit starves, bounded
  head-task latency. This is the fairness the design *implements and defends*. It
  is **not per-tenant fairness**: `ZPOPMIN` is tenant-blind, so a high-volume
  tenant collects worker-seconds proportional to its volume and degrades a light
  tenant's latency (never starves it); the ZSET score-tie breaks lexicographically
  by member (`wu:{wu_key}`), sorting one tenant's units into a contiguous block —
  *where it breaks* on simultaneous threshold-cross. Per-tenant **config** exists
  (`threshold`, `max_wait` in `wu:{wu_key}`); tenant-aware **selection** does not.
- **The extension is one seam, deliberately not built.** Per-tenant fairness
  plugs into the claim Lua (replace the single `ZPOPMIN` with a tenant-weighted
  pick across per-tenant `eligible` sub-ZSETs, + an optional per-tenant in-flight
  lease cap) → deficit-round-robin / weighted lottery. **Decision: explain + name
  the knob, do not build WFQ.** Lands as the fairness paragraph in the writeup
  (M4).

**Unchanged:** Data model (Streams, ZSETs, Hashes), the Lua enqueue/claim/ack
scripts, I1–I5, durability posture, `XAUTOCLAIM` reclaim, flush policy, the
open build/measure questions — all stand. Only the **shard routing key** narrows
(`wu_key` → `workspace`); everything shard-*local* is identical. No new
mechanism; this records isolation/fairness guarantees + the shard-key choice.

**Supersedes:** The "shard by `wu_key`" phrasing in §"The decision this doc must
settle" and §Horizontal scaling, narrowed to "shard by `workspace`." Otherwise
additive.
