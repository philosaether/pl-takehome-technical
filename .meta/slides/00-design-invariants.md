# Five Invariants

Early in development, we distilled the parameters of the screening brief into five
invariants. Claude and I referenced these fixed points continuously while designing
and building the system.

- **I1 — Order.** Tasks in a work unit are processed in strict arrival order.
- **I2 — Exclusivity.** At most one worker owns a work unit at any instant.
- **I3 — Exact aggregate.** `pending_cost` = sum of unacked task costs, maintained
  incrementally, never recomputed by scanning tasks.
- **I4 — Gate.** A unit is processed below threshold T *only* via the flush path.
- **I5 — Ack integrity.** An acked task is never redelivered; an unacked task is
  never skipped. Order survives claim/release churn and worker crashes.

## Invariant → mechanism

Each invariant is guaranteed by a different mechanism on each backend implementation.

| Invariant | **Postgres** | **Valkey** |
|-----------|--------------|------------|
| **I1 Order** | `seq`; `tasks` PK `(ws,sess,peer,seq)`; `Drain ORDER BY seq` | stream-ID order == seq; `XAUTOCLAIM` redelivers in order |
| **I2 Exclusivity** | `claimed_by` + `lease_token` + `FOR UPDATE SKIP LOCKED` | single-thread Lua claim + `lease_token` in `wu:` hash |
| **I3 Aggregate** | `work_units.pending_cost`, one Enqueue/Ack CTE | `HINCRBY pending_cost` inside the enqueue/ack Lua |
| **I4 Gate** | `eligible` + `wu_claimable` partial index; flush by `flush_deadline` | `eligible`/`pending_flush` ZSETs scored by `flush_deadline` |
| **I5 Ack integrity** | delete-on-ack; reaper reclaims expired leases; re-`SELECT` survivors | `XACK`/`XDEL`; PEL + `XAUTOCLAIM`; `leases` ZSET reap |

## Conformance guaranteed deterministically

Both the Valkey and Postgres backends are guaranteed to satisfy these invariants by
the test suite in tests/conformance. We used an in-memory backend as an oracle to
verify the test suite itself, then built each backend against it.