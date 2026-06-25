# Assessment: Our Design vs Honcho's Actual Queue (the wall-crossing)
Date: 2026-06-25
Branch: feature/postgres-queue
Scope: Compare our accepted Path 1 design against Honcho's *real* queue
implementation (ground-truthed from `github.com/plastic-labs/honcho` `main`,
v3.0.9, last pushed 2026-06-25). The gate Phil set: do we *improve*, or just hand
back their stack — or worse, their stack minus features?

## Verdict (up top)

**We improve — concretely, on the exact axis the brief calls "the whole point" —
and we do not hand back their stack minus features.** We match or converge on
every property they have, and we add a structural fix they haven't made plus two
robustness features they lack. The one headline improvement is real and defensible:

> Honcho **already has buffered cost-threshold gating** (so the *concept* is not
> our novelty). But they evaluate it every poll with a **`SUM(token_count) … JOIN
> … GROUP BY work_unit_key` scan** — which is precisely the thing the brief says
> must never happen on the hot path ("No `SUM(...) GROUP BY` over millions of rows
> per poll"). Our design's maintained per-unit aggregate + first-class
> `work_units` row + partial eligibility index turns that O(scan) look-ahead into
> an O(log n) indexed one. **The take-home is, in effect, asking us to fix this
> specific line in their own code — and we do.**

## Honcho's actual implementation (ground truth)

Postgres-only (SQLAlchemy + asyncpg), no broker. Two tables:
- `queue` (`QueueItem`): `id` bigint identity, `work_unit_key` TEXT, `payload`
  JSONB, `processed` bool, `error` TEXT, `message_id` FK. Index
  `(work_unit_key, processed, id)`. **No per-work-unit row exists** — a work unit
  is *implicit* in its queue rows.
- `active_queue_sessions`: `work_unit_key` TEXT **UNIQUE**, `last_updated`. This
  row *is* the exclusive lease.

Mechanics:
- **Enqueue:** plain `INSERT` of queue rows. Nothing maintained.
- **Claim:** eligibility computed per poll via a `SUM(messages.token_count) …
  WHERE NOT processed … GROUP BY work_unit_key` subquery + a `MIN(created_at)`
  subquery, filtered `total_tokens >= REPRESENTATION_BATCH_MAX_TOKENS (1024) OR
  oldest_created_at <= now() - 1800s`, `NOT EXISTS` against active leases,
  `ORDER BY oldest_created_at LIMIT (workers - owned)`. Claim itself =
  `INSERT INTO active_queue_sessions … ON CONFLICT DO NOTHING RETURNING` (the
  UNIQUE key is the mutual-exclusion; losers are skipped). **`FOR UPDATE SKIP
  LOCKED` is used only in stale-lease reaping, not in claiming.**
- **Gate:** `REPRESENTATION_BATCH_MAX_TOKENS=1024`, age flush `…MAX_AGE=1800s`,
  global `FLUSH_ENABLED` bypass. (Non-representation work is always eligible.)
- **Ordering:** within a unit, `ORDER BY id`; one lease per key → serial per
  `(workspace, session, observed)`, parallel across. (Note: the rep key
  *excludes the observer* because one derivation fans out to all observers.)
- **Crash recovery:** `last_updated` heartbeat + `cleanup_stale_work_units`
  deletes leases older than **5 min** (`FOR UPDATE SKIP LOCKED` + DELETE) →
  unacked rows redeliver. At-least-once.
- **Poison:** on batch error, mark the **first** item `processed=True,
  error=<msg>` and continue. **No retry counter, no backoff, no DLQ** — the
  errored row lingers 30 days then is GC'd.
- **Wakeup:** polling, **adaptive exponential backoff (1s→30s) + jitter**; no
  LISTEN/NOTIFY.
- **Lifecycle:** processed rows deleted (errored retained 30 days, 12h sweep);
  lease deleted on drain (no persistent unit row to tombstone).

> ⚠️ **Doc-vs-code discrepancy worth flagging (interview-relevant).** Phil's study
> doc `~/Development/meta/.meta/honcho-internals.md` (Stage 4) states claiming
> uses `FOR UPDATE SKIP LOCKED`. The **live code claims via UNIQUE +
> `INSERT … ON CONFLICT DO NOTHING`**; SKIP LOCKED appears only in stale-lease
> reaping. Worth correcting before the CTO conversation — he wrote this code.

## Axis-by-axis

| Property | Honcho (actual) | Ours (Path 1) | Who wins |
|---|---|---|---|
| Backend | Postgres, 2 tables | Postgres, 2 tables | tie (same family) |
| Look-ahead aggregate | **per-poll `SUM … GROUP BY` scan** | **maintained `pending_cost` + partial index** | **ours (the headline)** |
| Persistent work-unit row | none (unit implicit) | `work_units` row (enables the above) | ours (structural) |
| Exclusive claim | UNIQUE + `ON CONFLICT DO NOTHING` | `FOR UPDATE SKIP LOCKED` on eligible index | tie (both valid) |
| Ordering | `ORDER BY id` (id at insert) | per-unit `seq` under row lock | **ours (closes the commit-vs-assignment hazard)** |
| Flush | age cap on oldest (1800s) | age cap on oldest | tie — **convergent, validates us** |
| Crash recovery | heartbeat + 5-min stale reap | lease + reaper, equivalent | tie |
| Poison/retry | mark-first-errored, **no retry, no DLQ** | `attempts` + max + `dead_letters` | **ours** |
| Wakeup | adaptive poll backoff + jitter | adaptive poll backoff | tie — **convergent, validates us** |
| Runtime per-tenant T | global config constant | per-tenant denormalized + bounded UPDATE | ours (minor) |
| Vacuum/bloat | autovacuum + 12h GC | + HASH partitioning | ours (minor) |

## The one improvement that matters, stated precisely

Honcho has **no per-work-unit row**, so there is nowhere to *maintain* the cost
aggregate — they are *forced* to recompute it by scanning unprocessed rows every
poll. Our `work_units` row is the enabling structure: `pending_cost` is updated
`+cost`/`−cost` on enqueue/ack (O(1)), `eligible` is a maintained flag, and the
partial index `WHERE eligible AND claimed_by IS NULL` makes "which units are
claimable?" an index seek independent of pending-task count. **That is the brief's
stated hot-path requirement, delivered.**

The cost of our approach (intellectual honesty): we maintain the aggregate under
the unit's row lock on enqueue, which **serializes same-unit enqueues**. Honcho's
plain INSERT does not. But (a) same-unit ordering requires serialization anyway,
and (b) it closes the commit-vs-assignment ordering hazard that `ORDER BY id` has
latent (benign for them because session messages arrive sequentially; we don't
want to *rely* on that for a "provable ordering" deliverable). Right tradeoff for
what the brief grades.

## The honest caveat (say this in the screen — it's senior signal)

Honcho's per-poll scan is **probably fine at their current scale**: `WORKERS=1`
by default, the scan is over *unprocessed* rows (the queue drains, it's not
millions), and the `(work_unit_key, processed, id)` index helps. We are **not**
claiming their code is wrong. The brief explicitly specifies a regime — **10⁶
pending tasks, 10⁴–10⁵ live units, high worker scale** — where that scan *does*
hurt, and asks for a design that holds there. Our answer: "at your scale the scan
is reasonable; at the scale you're describing it isn't, and here's the structural
change — a maintained aggregate on a first-class unit row — that makes the
look-ahead independent of queue size." That framing is the opposite of
arrogant-rewrite; it's "I read your code, here's the one thing I'd change and
exactly why."

## Implications for the Path 2 (Valkey) decision — important

This comparison **shifts the Path 2 case**:
- The **look-ahead/aggregate win is already banked in Path 1** (Postgres maintained
  aggregate gives the same O(log n) eligibility a Redis ZSET would). So "Redis
  makes the look-ahead cheap" is **no longer a differentiator** — we got it in PG.
- Therefore Path 2 must justify itself on the **two things Postgres can't escape**:
  (1) the **single-primary write ceiling** (every enqueue/ack through one WAL),
  and (2) the **loss-tolerant / latency-sensitive posture** — Valkey's low-latency,
  bounded-loss profile fits Honcho's actual requirement (an observation, not a
  ledger) better than PG's heavier durability. Path 2's pitch narrows to
  *throughput + latency*, not *look-ahead*.
- Net: the bar for Path 2 is now "show a throughput ceiling on Path 1 that Valkey
  clears, at a latency the loop wants" — which the load test can actually measure.

## Bottom line

We are not reproducing Honcho, and we are not handing back their stack minus
features. We hand back their stack **plus the structural fix their own brief
asks for** (maintained aggregate), **plus** retry/DLQ and provable ordering,
while **converging** with them on flush and poll-backoff (independent
convergence = the design is sound). Path 1 holds up. Proceed to give Path 2 the
same treatment and decide if throughput+latency justify the upgrade.
