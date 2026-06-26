---
Status: accepted
Date: 2026-06-26
Accepted: 2026-06-26
Implemented: 2026-06-26 (feature/postgres-queue)
Divergences: composite natural key (ws,sess,peer) instead of hashed wu_key bytea (flagged); added lease_ms column so Ack renews the lease without a contract param; Ack `keep` computed via an `elig` CTE not a LATERAL (Postgres can't reference the UPDATE target from a LATERAL in its own FROM). Behavior matches the oracle (8/8 conformance vs live postgres:16).
Deferred: Drain 2→1 round-trip + Enqueue tenant-cache-miss → roadmap M2 (measure first); tombstone-TTL reaping + golang-migrate → enhancements.md.
Implements: postgres-work-unit-queue.md (accepted Path 1 + A1), against the M0 queue.Backend contract
Roadmap: ../roadmap.md (M1)
---

# Postgres Driver (M1) — Build Design

Flesh out `internal/postgres` so it satisfies the accepted `queue.Backend`
contract (the 8 methods) over Postgres, mirroring
[`postgres-work-unit-queue.md`](postgres-work-unit-queue.md). Concrete schema +
SQL + pgx wiring — Phil implements from this. **The in-memory backend is the
reference:** a shared conformance suite runs the same scenarios against both and
asserts identical observable behavior.

---

## Contract → SQL at a glance

| `queue.Backend` method | Postgres realization |
|---|---|
| `Enqueue` | upsert `work_units` (maintain `pending_cost`/`next_seq`/eligibility under the unit row lock) + insert `tasks`, one CTE round-trip |
| `Claim` | `UPDATE … WHERE (ws,sess,peer) = (SELECT … WHERE eligible AND unclaimed ORDER BY flush_deadline FOR UPDATE SKIP LOCKED LIMIT 1)` |
| `Drain` | lease check, then `SELECT … FROM tasks WHERE key ORDER BY seq LIMIT $batch` |
| `Ack` | txn: lease-validate `FOR UPDATE` → `DELETE tasks ≤ seq` → recompute `work_units`, keep-or-release |
| `Release` | `UPDATE work_units SET claimed_by=NULL,… WHERE key AND lease matches` |
| `Heartbeat` | `UPDATE work_units SET lease_expires=now()+$extend WHERE key AND lease matches` |
| `Fail` | txn: bump `attempts`; at cap move head → `dead_letters`; release |
| `ReapExpired` | two bounded `UPDATE`s: reclaim expired leases, flush-promote aged units |

---

## Key representation — the one deliberate divergence from the accepted schema

The accepted design keys on `wu_key bytea = hash(workspace, session, peer)`. **M1
uses the composite natural key `(workspace, session, peer)` directly** instead.
Why: the contract's `WorkUnitKey` is exactly that triple, so a composite key is a
1:1 mapping — no hashing (no collision risk), no encode/decode to recover the
components for `ClaimedUnit.Key` on every `Claim`, and `workspace` is a native
column (it *is* the tenant, so the per-tenant-T update and any workspace shard
routing are plain predicates). Cost: wider index entries than a fixed bytea.
For 10⁴–10⁵ unit rows that's immaterial. *Flagged in Tradeoffs; revisit to a
hashed key only if index width ever bites.*

Snippets below abbreviate the key columns as `(ws, sess, peer)`.

---

## Schema (`internal/postgres/schema.sql`, embedded)

```sql
CREATE TABLE IF NOT EXISTS work_units (
  ws            text   NOT NULL,
  sess          text   NOT NULL,
  peer          text   NOT NULL,
  pending_cost  bigint NOT NULL DEFAULT 0,   -- I3: += on enqueue, -= on ack
  next_seq      bigint NOT NULL DEFAULT 0,   -- per-unit monotonic seq generator
  threshold     int    NOT NULL,             -- denormalized tenant T (hot-path local)
  max_wait_ms   int    NOT NULL,             -- denormalized tenant max_wait (M1 add)
  eligible      boolean NOT NULL DEFAULT false,
  claimed_by    text,                        -- worker id; NULL = free
  lease_token   uuid,                        -- per-claim token (M1 add; ABA-safe lease)
  lease_expires timestamptz,
  oldest_pending_at timestamptz,
  flush_deadline    timestamptz,
  PRIMARY KEY (ws, sess, peer)
);

CREATE TABLE IF NOT EXISTS tasks (
  ws          text   NOT NULL,
  sess        text   NOT NULL,
  peer        text   NOT NULL,
  seq         bigint NOT NULL,               -- per-unit arrival order (I1)
  payload     bytea  NOT NULL,
  cost        int    NOT NULL,
  attempts    int    NOT NULL DEFAULT 0,
  enqueued_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (ws, sess, peer, seq)
) PARTITION BY HASH (ws, sess, peer);         -- spread vacuum churn
-- 2 partitions for M1 (matches the scaffold decision; bump later if needed):
CREATE TABLE IF NOT EXISTS tasks_p0 PARTITION OF tasks FOR VALUES WITH (MODULUS 2, REMAINDER 0);
CREATE TABLE IF NOT EXISTS tasks_p1 PARTITION OF tasks FOR VALUES WITH (MODULUS 2, REMAINDER 1);

-- THE look-ahead index: partial, over work_units (10^4-10^5), never tasks (10^6).
CREATE INDEX IF NOT EXISTS wu_claimable ON work_units (flush_deadline NULLS LAST, ws, sess, peer)
  WHERE eligible AND claimed_by IS NULL;
-- reaper support: find expired leases / aged units cheaply.
CREATE INDEX IF NOT EXISTS wu_leased ON work_units (lease_expires) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS wu_flushable ON work_units (flush_deadline) WHERE NOT eligible AND claimed_by IS NULL;

CREATE TABLE IF NOT EXISTS dead_letters (
  LIKE tasks INCLUDING DEFAULTS, failed_at timestamptz NOT NULL DEFAULT now(), reason text
);
CREATE TABLE IF NOT EXISTS tenant_config (
  ws text PRIMARY KEY, threshold int NOT NULL, max_wait_ms int NOT NULL
);
```

**Per-unit `seq` under the unit row lock** (not `bigserial`): assigned via
`next_seq = next_seq + 1 RETURNING` in the enqueue upsert, which takes the unit's
row lock → seq order == commit order, gap-free, *per unit only*. The load-bearing
ordering decision (verbatim from the accepted design).

**M1 schema additions over the accepted doc** (both additive, neither changes a
decision): `max_wait_ms` denormalized onto `work_units` (parallel to `threshold`,
keeps ack/enqueue local); `lease_token uuid` (the contract models `LeaseToken`
separately from `claimed_by` — the token makes lease validation ABA-safe, so a
unit reclaimed-then-reclaimed by the *same* worker invalidates the old handle).

---

## pgx wiring (`internal/postgres/backend.go`)

```go
type Backend struct {
	pool     *pgxpool.Pool
	defT     int           // DefaultThreshold
	defWait  time.Duration // DefaultMaxWait
	maxTries int
	tenants  *tenantCache  // ws → (threshold, max_wait_ms), cached lookups of tenant_config
}

func New(o Options) (queue.Backend, error) {
	pool, err := pgxpool.New(ctx, o.DSN)          // pgxpool: the shared connection pool
	// … run schema.sql (go:embed, idempotent CREATE IF NOT EXISTS) …
	return &Backend{pool: pool, …}, nil
}
```

- **Driver:** `github.com/jackc/pgx/v5` + `pgxpool`. Single pool shared by the
  worker goroutines (pgxpool is concurrency-safe).
- **Migrations:** `//go:embed schema.sql` run once on `New` — idempotent, no
  external migration tool (reproducibility: clone → `make up` → schema exists).
  *golang-migrate is the production alternative; out of scope for the take-home.*
- **Tenant resolution:** `Enqueue` needs `threshold`/`max_wait` to denormalize
  onto the unit. The driver resolves `(ws → threshold, max_wait_ms)` from a cached
  `tenant_config` lookup, falling back to `DefaultThreshold`/`DefaultMaxWait`.
  Cache keeps the enqueue hot-path local (no join).

---

## Per-method SQL

### Enqueue — one CTE round-trip (upsert aggregate + insert task)

```sql
WITH up AS (
  INSERT INTO work_units AS w (ws, sess, peer, threshold, max_wait_ms,
                               pending_cost, next_seq, eligible,
                               oldest_pending_at, flush_deadline)
  VALUES ($ws,$sess,$peer,$T,$wait, $cost, 1, $cost >= $T,
          now(), now() + ($wait || ' ms')::interval)
  ON CONFLICT (ws, sess, peer) DO UPDATE SET
    pending_cost = w.pending_cost + $cost,
    next_seq     = w.next_seq + 1,                       -- monotonic, never reset
    eligible     = (w.pending_cost + $cost) >= w.threshold OR w.eligible,
    oldest_pending_at = CASE WHEN w.pending_cost = 0 THEN now() ELSE w.oldest_pending_at END,
    flush_deadline    = CASE WHEN w.pending_cost = 0
                             THEN now() + (w.max_wait_ms || ' ms')::interval
                             ELSE w.flush_deadline END
  RETURNING next_seq
)
INSERT INTO tasks (ws, sess, peer, seq, payload, cost)
SELECT $ws,$sess,$peer, next_seq, $payload, $cost FROM up
RETURNING seq;
```
O(1) aggregate maintenance; eligibility flips on the enqueue that crosses T — no
scan. The `pending_cost = 0` arm (re)starts the flush clock for a brand-new *or*
revived-tombstone unit — one code path.

### Claim — `FOR UPDATE SKIP LOCKED` on the partial index

```sql
UPDATE work_units SET claimed_by = $me, lease_token = gen_random_uuid(),
                      lease_expires = now() + ($lease_ms || ' ms')::interval
WHERE (ws, sess, peer) = (
  SELECT ws, sess, peer FROM work_units
  WHERE eligible AND claimed_by IS NULL
  ORDER BY flush_deadline NULLS LAST, ws, sess, peer    -- age-fair (Fairness)
  FOR UPDATE SKIP LOCKED LIMIT 1
)
RETURNING ws, sess, peer, lease_token, lease_expires;
```
`SKIP LOCKED` → exclusivity (I2) + zero claim contention. Index-only over
`wu_claimable` (10⁴–10⁵ rows), never touches `tasks` (I3/I4). `RETURNING` gives
the components + token → build `ClaimedUnit`. No row → `(nil, nil)`.

### Drain — lease check, then ordered batch

```sql
-- 1) lease still ours? (cheap PK lookup) → else ErrLeaseLost
SELECT 1 FROM work_units WHERE (ws,sess,peer)=($ws,$sess,$peer)
  AND claimed_by=$me AND lease_token=$tok;
-- 2) next batch in arrival order
SELECT seq, payload, cost FROM tasks
WHERE (ws,sess,peer)=($ws,$sess,$peer) ORDER BY seq LIMIT $batch;
```
Empty step-2 with a valid lease → the worker loop calls `Release` (distinguished
from `ErrLeaseLost`, matching the oracle).

### Ack — txn: validate, delete-on-ack, recompute, keep-or-release

```sql
BEGIN;
-- validate + lock the unit row (blocks a concurrent reaper reclaim during ack)
SELECT 1 FROM work_units WHERE (ws,sess,peer)=($ws,$sess,$peer)
  AND claimed_by=$me AND lease_token=$tok FOR UPDATE;          -- 0 rows → ROLLBACK, ErrLeaseLost

-- delete-on-ack + recompute in one CTE. The aggregate is self-validating (Σcost of
-- the rows actually deleted), so the worker passes no $acked_cost (OQ2). `keep` is
-- computed in-SQL from the POST-ack state (it can't be a param — it depends on the
-- new pending_cost and the new head's deadline), once, via LATERAL.
WITH del AS (
  DELETE FROM tasks WHERE (ws,sess,peer)=($ws,$sess,$peer) AND seq <= $through RETURNING cost
), acked AS ( SELECT COALESCE(sum(cost),0) AS c FROM del ),
   head  AS ( SELECT min(enqueued_at) AS min_enq FROM tasks WHERE (ws,sess,peer)=($ws,$sess,$peer) )
UPDATE work_units w SET
  pending_cost      = w.pending_cost - acked.c,
  oldest_pending_at = head.min_enq,
  flush_deadline    = head.min_enq + (w.max_wait_ms || ' ms')::interval,
  eligible          = e.keep,
  claimed_by    = CASE WHEN e.keep THEN $me  ELSE NULL END,
  lease_token   = CASE WHEN e.keep THEN $tok ELSE NULL END,
  lease_expires = CASE WHEN e.keep THEN now() + ($lease_ms||' ms')::interval ELSE NULL END
FROM acked, head,
  LATERAL (SELECT ((w.pending_cost - acked.c) >= w.threshold              -- still at/above T
                   OR (head.min_enq IS NOT NULL                            -- …or new head already aged
                       AND head.min_enq + (w.max_wait_ms||' ms')::interval <= now())) AS keep) e
WHERE (w.ws,w.sess,w.peer)=($ws,$sess,$peer)
RETURNING e.keep AS still_held;
COMMIT;
```
- Aggregate decrements by the sum of the *rows the DELETE removed* — no caller-supplied
  cost (OQ2: self-validating).
- `keep` *is* the recomputed eligibility: the unit stays claimed iff it's still
  eligible (≥ T, or the **new head** has aged — per-head flush, OQ1; never a sticky
  flag). Empty unit → `min_enq` NULL → `keep` false → released + **tombstoned in
  place** (row kept for cheap revival; TTL-reaped later).
- Acked rows are gone (I5) → never redeliver. Renewing `lease_expires` on the keep
  path mirrors the oracle review-fix B1.

### Release / Heartbeat

```sql
UPDATE work_units SET claimed_by=NULL, lease_token=NULL, lease_expires=NULL
WHERE (ws,sess,peer)=($ws,$sess,$peer) AND claimed_by=$me AND lease_token=$tok;   -- 0 rows → ErrLeaseLost

UPDATE work_units SET lease_expires = now() + ($extend_ms||' ms')::interval
WHERE (ws,sess,peer)=($ws,$sess,$peer) AND claimed_by=$me AND lease_token=$tok;   -- 0 rows → ErrLeaseLost
```

### Fail — attempts → DLQ at cap, then release

```sql
BEGIN;
SELECT 1 FROM work_units WHERE (ws,sess,peer)=($ws,$sess,$peer)
  AND claimed_by=$me AND lease_token=$tok FOR UPDATE;          -- 0 rows → ErrLeaseLost
UPDATE tasks SET attempts = attempts + 1
  WHERE (ws,sess,peer)=($ws,$sess,$peer) AND seq=$seq
  RETURNING attempts, cost;                                    -- → a, c
-- if a >= max_attempts: move head to DLQ, decrement aggregate, recompute eligibility
WITH moved AS (
  DELETE FROM tasks WHERE (ws,sess,peer)=($ws,$sess,$peer) AND seq=$seq RETURNING *
)
INSERT INTO dead_letters (ws,sess,peer,seq,payload,cost,attempts,enqueued_at,reason)
SELECT ws,sess,peer,seq,payload,cost,attempts,enqueued_at,$reason FROM moved;
-- then UPDATE work_units (pending_cost -= c, recompute oldest/flush/eligible)
-- always: release the unit (claimed_by=NULL, lease_token=NULL) so it redelivers in order
COMMIT;
```
Poison head removed at the cap unblocks the unit (FIFO + exclusive would otherwise
wedge it forever). Below the cap, `Fail` just releases → redeliver in order,
`attempts` now higher.

### ReapExpired — two bounded UPDATEs

```sql
-- (1) reclaim crashed workers' leases (I5)
UPDATE work_units SET claimed_by=NULL, lease_token=NULL, lease_expires=NULL
WHERE claimed_by IS NOT NULL AND lease_expires < $now;                 -- → reclaimed count
-- (2) flush-promote aged units (age-cap escape)
UPDATE work_units SET eligible=true
WHERE NOT eligible AND claimed_by IS NULL AND flush_deadline <= $now AND pending_cost > 0;  -- → flushed count
```
Time can't live in the partial-index predicate (it moves), so the reaper flips the
stored `eligible` flag — microseconds over 10⁴–10⁵ rows, keeping the claim query
index-only. Runs on the in-process timer (M0 wiring) and lazily safe.

---

## The conformance suite — oracle as reference

`internal/queue/conformance` (or a `testkit`): table-driven scenarios exercising
the contract — enqueue/gate/claim-exclusivity/in-order-drain/ack-keep-or-release/
flush/lease-reclaim/poison-DLQ. One function `RunConformance(t, func() Backend)`.

- **memory:** runs in CI, fast, always on.
- **postgres:** same suite, gated behind a `PLQ_TEST_POSTGRES` DSN env (spins a
  container or uses compose). Skipped when unset so the default `go test` stays
  hermetic.

This is the apples-to-apples *correctness* guarantee — both backends pass the
identical suite — and it pre-stages the M2 deterministic proofs (the ordering and
gate proofs become conformance cases).

## go.mod additions

```
require github.com/jackc/pgx/v5 v5.x   // pgxpool, the only new dependency
```
(Brings `puddle`, `golang.org/x/crypto/text` transitively — all standard.) The
postgres Dockerfile's `go mod download` now actually fetches; commit `go.sum`.

---

## Tradeoffs

**Composite natural key vs hashed `wu_key bytea`.** *Chosen:* `(ws,sess,peer)`
composite — 1:1 with the contract type, no hash collisions, no decode-on-claim,
`workspace` native for tenant/shard predicates. *Rejected (for now):* the design's
hashed bytea — compact fixed-width index entries, but needs the components stored
separately anyway to satisfy `ClaimedUnit.Key`, and adds collision handling.
*Revisit if:* `work_units`/`wu_claimable` index width shows up in the load test.

**Embedded idempotent `schema.sql` vs a migration tool.** *Chosen:* `go:embed` +
`CREATE IF NOT EXISTS` on startup — zero deps, clone-and-run reproducibility, fine
for a single-version take-home. *Rejected:* golang-migrate — real production
answer, unnecessary machinery here.
- Flag golang-migrate as an enhancement

**`$acked_cost` passed by the worker vs `DELETE … RETURNING` sum.** *Resolved →
self-validating* (`DELETE … RETURNING cost` summed in the ack CTE). It's *one fewer
statement* in the txn, not more, and makes `pending_cost == Σ remaining cost` a
driver-owned invariant rather than a caller-discipline assumption — strictly better
for ~zero cost; the worker stops passing `$acked_cost`. (The caller-supplied form
was safe — a wrong sum corrupts only that one unit's eligibility *timing*,
flush-backstopped, no loss/ordering/cross-unit impact — but there's no reason to
keep the weaker invariant when the stronger one is cheaper.)

**Lease validation in `Drain` (extra round-trip) vs trust-then-fail-at-ack.**
*Chosen:* explicit cheap lease check in `Drain` so it returns `ErrLeaseLost`
distinctly (matches the oracle). *Alt:* skip it, let `Ack` catch the lost lease —
one fewer query but blurs the empty-vs-lost distinction the loop uses.

## Resolved (iteration 1)

1. **Flush stickiness → per-head (align the oracle to the design).** Eligibility is
   recomputed every ack as `remaining ≥ T OR flush_deadline ≤ now()`; the M0 oracle
   drops its sticky `flushed` bool. Flush is an age-cap on the *oldest task*, so each
   task flushes when it individually ages — the latency bound we promise — and one
   old task can't over-drain the buffered work behind it. **M1 build task:** remove
   `flushed` from `internal/memory` (ReapExpired just sets `eligible=true`; recompute
   uses `flush_deadline ≤ now()`); the conformance suite pins this for both backends.
   Captured in `talking-points.md` (good apples-to-apples story — the 2nd impl
   surfaced the ambiguity in the 1st).
2. **`$acked_cost` → flip to self-validating** (`DELETE … RETURNING cost` summed in
   the ack CTE). Blast radius of the caller-supplied form is small (per-unit
   eligibility timing, flush-backstopped, no loss/ordering/cross-unit impact) — but
   self-validating is *one fewer statement* in the txn AND makes
   `pending_cost == Σ remaining cost` a driver-owned invariant, so it's strictly
   better for ~zero cost. The worker stops passing `$acked_cost` entirely. *(Update
   the Ack SQL to the CTE form when building.)*
3. **Integration-test gating → `PLQ_TEST_POSTGRES` env + compose** (no new dep). The
   real exercise is the loadgen cases anyway; the conformance suite is the unit-level
   net.
4. **Tombstone TTL reaping → defer**, flagged as an enhancement (`enhancements.md`).
   Not on the correctness path (flush + the aggregate don't depend on GC'ing dead
   rows); pairs with the M2 vacuum/bloat story.

Also flagged → `enhancements.md`: golang-migrate (vs the embedded `schema.sql`).

## Open Questions

*(none open — iteration 1 resolved all four.)*

## Out of Scope

- The load generator's real producers + metrics (M2) and the four headline proofs
  as standalone deliverables (M2) — though the conformance suite pre-stages them.
- Valkey (M3); the head-to-head (M3).
- `LISTEN/NOTIFY` wakeup — M0 chose adaptive poll-backoff; unchanged here.
- Read-replica/failover deployment topology (design's operability section; not
  built for the take-home load test).
- Per-tenant fairness scheduling (A1: explain, don't build).
