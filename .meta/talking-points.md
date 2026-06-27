# Talking Points — interesting decisions & tradeoffs

Decisions and tradeoffs made during development that are *interesting* — the kind
of thing worth a line in the 1-page brief or a beat in the presentation/walkthrough
script. Curate a few at publish time (M4); the rest are the depth behind the
answers if asked.

Distinct from [enhancements.md](enhancements.md): that's *future* work we'd do
next; this is *reasoning we already did* and would want to show off.

Append-forward. Don't prune — selection happens at M4.

---

## The maintained aggregate: ~flat look-ahead vs a naive scan that hits 2.5s at 10⁷

Measured (single Postgres, two queries, units scaling with task count to match the
brief's regime): our look-ahead — an indexed `ORDER BY flush_deadline LIMIT 1` over
the maintained `work_units.pending_cost` aggregate — stays **~0.3–1.9 ms** from 10⁴
to **10⁷** pending tasks. The naive per-poll `SUM(cost) … GROUP BY … HAVING` (Honcho's
current code) climbs to **2,524 ms** at 10⁷ — a **1,364× speedup**, three orders of
magnitude on the log-log plot. This *is* the take-home's thesis, and it's the one
chart that makes the case on its own (`results/lookahead.png`).

## A flaky test that's actually the production contract in disguise

Co-locating the integration suites surfaced an intermittent `Claim` → `nil` under
`FOR UPDATE SKIP LOCKED` after heavy prior DB churn. The fix wasn't to paper over
it — it was to make the *proof* claim the way a real *worker* claims: poll with
backoff until a unit comes back. A single nil-claim is expected and handled by the
worker loop; asserting the first claim succeeds instantly was stricter than the
contract. Nice "our tests model the real system" point — and a reminder that
`SKIP LOCKED` trades a strict-first-claim guarantee for contention-free parallelism.

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

## A Valkey primary needs less iron than a PG primary — the asymmetry is the point

For the cloud head-to-head, each Valkey primary is provisioned on the *same* box
class as the single PG primary (`m5.xlarge`) — a deliberately fair per-primary
comparison. But Valkey executes commands single-threaded, so a primary won't
*use* an `m5.xlarge` the way Postgres does (no multi-core query parallelism to
saturate). That asymmetry isn't a flaw in the benchmark — it's a finding: the
Valkey path hits its throughput on smaller, cheaper instances, so the real
per-throughput cost gap is *wider* than the same-box comparison shows. The
same-class run is the conservative number; the honest footnote is "and Valkey
gets there on less."

## The head-to-head, measured: PG plateaus at ~1.7k/s, Valkey scales linearly to ~92k

The take-home's thesis, proven on cloud iron (7× m5.xlarge, AWS, torn down for
≪$1). Pure queue throughput (zero simulated work), acks/s:

| postgres | valkey×1 | valkey×2 | valkey×4 |
|----------|----------|----------|----------|
| ~1,723   | ~25,771  | ~49,301  | ~92,064  |

Postgres plateaus at ~1.7k and *declines* past 100 workers (claim contention).
Valkey scales **near-linearly with shard count** — 1.9× at 2 shards, 3.6× at 4 —
because the design is N independent primaries with `hash(workspace)%N` routing, not
a single contended store. That's both the "how I know it works" evidence and the
answer to "what's the migration worth": ~15× at one shard, ~53× at four, and the
slope says it keeps going. The cost-mode runs add the kicker — at 1000 workers PG
can't even keep them fed (stalls at ~1.4k while Valkey hits the ~50k work ceiling).
Full data + charts: `.meta/assessments/m3-head-to-head/`.
