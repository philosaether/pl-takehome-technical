# Talking Points — interesting decisions & tradeoffs

Decisions and tradeoffs made during development that are *interesting* — the kind
of thing worth a line in the 1-page brief or a beat in the presentation/walkthrough
script. Curate a few at publish time (M4); the rest are the depth behind the
answers if asked.

Distinct from [enhancements.md](enhancements.md): that's *future* work we'd do
next; this is *reasoning we already did* and would want to show off.

Append-forward. Don't prune — selection happens at M4.

---

## Flush is an age-cap on the *oldest task*, not a sticky unit flag

When a unit flush-promotes (its oldest task aged past `max_wait`) and a worker
drains the aged prefix, what happens to the rest? Two semantics:

- **Sticky** (the M0 oracle's first cut): once flushed, the unit stays eligible
  until *fully* drained — one old task drains the whole unit.
- **Per-head** (the accepted design, and what we shipped): eligibility is
  recomputed every ack as `remaining ≥ T OR flush_deadline ≤ now()`, where
  `flush_deadline` tracks the *new* head. So after the aged prefix drains, a unit
  whose new head hasn't aged yet **re-buffers**.

We chose per-head, because it's exactly the latency guarantee we make: the age-cap
bounds worst-case latency for *each task individually* (every task flushes when
*it* becomes the oldest and ages out), without letting one old task defeat the
buffering for all the newer work behind it. Sticky-flush over-drains.

The catch this surfaced: the oracle and the design had silently diverged. We caught
it writing the Postgres driver against the contract, and aligned the oracle to the
design — the conformance suite now pins the per-head semantics for *both* backends.
*(Good "how the apples-to-apples contract earns its keep" story: the second
implementation is what flushed out the ambiguity in the first.)*
