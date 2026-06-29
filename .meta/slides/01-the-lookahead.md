# The look-ahead — maintained aggregate vs a naive scan

According to the brief, cheap lookahead is "the whole point" of this exercise. We
took that personally.

## Three moves

**1. Maintain the aggregate, never compute it (I3).**

`work_units.pending_cost` is
kept current on every write, in the one `Enqueue` statement:

```sql
ON CONFLICT (ws, sess, peer) DO UPDATE SET
  pending_cost = w.pending_cost + $cost,                                -- O(1)
  eligible     = (w.pending_cost + $cost) >= w.threshold OR w.eligible, -- gate, maintained
  ...
```

**2. The partial index *is* the look-ahead (I4).**

```sql
CREATE INDEX wu_claimable ON work_units (flush_deadline NULLS LAST, ws, sess, peer)
  WHERE eligible AND claimed_by IS NULL;
```

The `wu_claimable` index indexes *only claimable units,* a tiny fraction of the total
`work_units` table. Work units are added to or removed from this index only when they
cross the eligibility threshold `T`, and the operation is `O(log N)` *on the number of eligible units.*

This is a massive improvement in performance over the existing linear scaling across
all work units, and sufficient to hold throughput flat across 3 orders of magnitude.

**3. The flush path — sub-threshold units don't strand.**

Each work unit is enqueued with a `flush_deadline = oldest_pending_at + max_wait`,
while a reaper process promotes units over their deadline to eligible even when
`pending cost` is under `T`.

This ensures sub-threshold units don't strand (I4.b) and, combined with our preference
for grabbing the *oldest* eligible work-unit first, helps to protect fairness in the
system.