# Honcho mapping — source material for Fig 2 (the precise change)

Investigation of the real Honcho repo to ground the one-pager's "Stage 1" claim
in their actual code. Clone: `github.com/plastic-labs/honcho` @
`eb386c3ceb77774b29108f9ab114e71d52b7d420` (read-only, in `/tmp`).

## The find: their hot path IS our naive baseline

`src/deriver/queue_manager.py :: get_and_claim_work_units` (L330–445) recomputes,
**on every 1s poll**, two `O(unprocessed rows)` GROUP-BYs over the `queue` table:

- **The cost aggregate** (L347–361): `SUM(messages.token_count) … GROUP BY
  queue.work_unit_key`, joined queue⋈messages because `token_count` lives on
  `messages`, not `queue`.
- **The threshold gate** (L399–418): `COALESCE(total_tokens,0) >= batch_max_tokens
  (=1024)  OR  oldest_created_at <= now() - max_age (=1800s)` — i.e. the literal
  `HAVING SUM(cost) >= T OR age-flush` from the brief.
- Plus a **second** GROUP-BY (`work_units_subq`, L363–371) just to get each unit's
  `oldest_created_at`. The supporting index doesn't carry `token_count`, so the
  SUM always pays the join.

Cost = `Message.token_count`; threshold = `REPRESENTATION_BATCH_MAX_TOKENS=1024`;
poll = 1s; claim/lease = a row in the ephemeral `active_queue_sessions` table via
`INSERT … ON CONFLICT DO NOTHING`. **This is a near-verbatim match to the brief's
"don't do `SUM(...) GROUP BY` every poll" — and it's their production code.**

Scope: the cost-threshold gate applies to **`representation`** work units only;
`summary`/`dream`/`deletion`/`reconciler`/`webhook` are FIFO by `oldest_created_at`.

## The precise change (what Fig 2 depicts)

Honcho has **no persistent per-work-unit row** — `QueueItem` is per-message,
work-unit identity is a repeated string, `active_queue_sessions` is created *after*
the decision. So the maintained-aggregate look-ahead = **introduce a small
`work_units` aggregate table + maintain it on the write path + swap the claim
query.** Four files + one migration:

1. **New model + migration** — `src/models.py` (~15 lines) + `migrations/versions/*`
   (~30 lines): `work_units(work_unit_key PK, task_type, pending_cost bigint,
   oldest_pending_at, eligible bool)` + partial index
   `ix_work_units_eligible (task_type, oldest_pending_at) WHERE eligible`.
2. **Increment on enqueue** — `src/deriver/enqueue.py::enqueue` (L25–78): after the
   `QueueItem` insert, `INSERT … ON CONFLICT (work_unit_key) DO UPDATE SET
   pending_cost += delta, oldest_pending_at = LEAST(…), eligible = (pending_cost >=
   threshold OR age)`. `token_count` already in hand here. ~15 lines.
3. **Decrement on ack/error** — `queue_manager.py::mark_queue_items_as_processed`
   (L1057–1076) + `mark_queue_item_as_errored` (L1089–1107): `pending_cost -=
   delta`, recompute `eligible`, delete row at 0. ~15–20 lines.
4. **Swap the claim query** — `get_and_claim_work_units` (L345–420): delete both
   GROUP-BYs; replace with `SELECT work_unit_key FROM work_units WHERE eligible AND
   NOT EXISTS(active_queue_sessions…) ORDER BY oldest_pending_at LIMIT n FOR UPDATE
   SKIP LOCKED`. ~−70 / +25 lines.

**Honest line count: ~120–170 lines, 4 files + 1 migration. No new infrastructure.**

## Candor (these gate the copy — do not overclaim)

1. **It's a new table + write hooks, NOT "alter ~40 lines of the existing queue."**
   Our take-home schema has a `work_units` table already, so for *us* it's one
   column; for *Honcho* it's a (small) new table. The Honcho-facing number is
   ~150 lines. Use that one in the doc.
2. **The 1,364× is our synthetic measurement** (maintained vs naive at 10⁷), not a
   measurement of Honcho prod. Frame it as the **algorithmic-complexity win** —
   `O(unprocessed tasks)` → `O(eligible work units)`, ~10⁷ → ~10⁴–10⁵ rows scanned
   — i.e. *what your scheduling hot path costs as the backlog grows.* Their default
   adaptive-backoff + age-flush keeps the queue small today, so this is "before it
   bites you on the next growth cycle," not "your prod is 1000× slow now." This
   aligns perfectly with the growth-story lead.
3. **Gate is `representation`-only** — say "representation work units," not "the queue."
4. **Maintaining the aggregate correctly is the real cost** — under multiple deriver
   instances, `SKIP LOCKED`, partial-batch error handling (only `items[0]` errored),
   and the age-flush escape hatch, the increment/decrement + `eligible` recomputation
   is where bugs would live. Worth naming as honest engineering cost, not "mechanical."

## Task-type cost profiles (for §2 + the live walkthrough)

Verified against `src/deriver/consumer.py` (the dispatcher). Which Honcho task
types are queue-bound (Valkey helps) vs worker-bound (it doesn't):

| Task type | Handler | Bound by | Valkey helps? |
|-----------|---------|----------|---------------|
| representation | LLM derivation (`process_representation_batch`) | LLM | no |
| summary | `summarizer.summarize_if_needed` (consumer.py:117) — LLM | LLM | no |
| dream | `process_dream` (consumer.py:138) — LLM | LLM | no |
| webhook | `deliver_webhook` (consumer.py:76) — external HTTP POST | network | **no** (worker blocks on the call) |
| reconciler | "poll on a fixed interval, usually find no work" (consumer.py:46) | low-rate poller | no (not throughput work) |
| deletion | `process_deletion` (consumer.py:151) — DB deletes | DB, cheap | marginal (event-driven, low volume) |

**Conclusion for §2:** no downstream task type is a strong "high-volume cheap
queue-bound" Valkey candidate. The genuinely queue-bound plane is the **ingest +
eligibility-scheduling machinery itself** (every message enqueues; the scheduler
polls all live units each cycle). Stage 1 already relieves the worst of it; Valkey
earns its cost only if that ingest/scheduling rate outgrows one Postgres primary.
Do **not** recommend migrating webhook (network-bound) or reconciler (idle poller)
"for throughput" — that's backwards, and a sharp reviewer will catch it.

## Citations (for the figure caption / endnotes)

- Claim hot path (replace target): `queue_manager.py#L330-L445` (SUM @L347-361, gate @L399-418)
- Polling loop: `queue_manager.py#L517-L568`
- Ack path (decrement hook): `queue_manager.py#L1057-L1107`
- Models (`QueueItem`/`ActiveQueueSession`): `src/models.py#L477-L545`; `Message.token_count` @ `src/models.py#L226`
- Enqueue (increment hook): `src/deriver/enqueue.py#L25-L78`
- Config defaults: `src/config.py#L860-L871`
- Base URL: `https://github.com/plastic-labs/honcho/blob/eb386c3ceb77774b29108f9ab114e71d52b7d420/<path>`
