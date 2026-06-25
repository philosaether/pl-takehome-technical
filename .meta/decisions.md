# Decisions

Append-only log. Don't edit old entries.

---

## 2026-06-25 — Assess the queue design space clean-room (brief only)

Exclude Honcho's *current Postgres implementation* (solution context) from the
initial assessment, per the brief's "from first principles" + "current state ≠
desired state" framing, and to demonstrate our own reasoning to the screen.
Distinction preserved: *problem context* (workload shape) is fair game — the
brief supplies enough (10⁶ tasks, 10⁴–10⁵ units, $20 cap). Open door:
validate self-derived workload numbers against real `~/Development/meta/`
Honcho figures *after* committing to a design, not before.

## 2026-06-25 — git init, backend not yet chosen

Repo initialized on `main`. Backend (Postgres-done-right vs Redis-primitives
vs custom) deliberately left open after assess — it's the graded decision,
settled at /draft after discussion.

## 2026-06-25 — Three path drilldowns on `meta/queue-backend-scoping`

Wrote path1/2/3 assessments. Confirmed (a): "compare to postgres-as-queue
pattern" = the generic community pattern (pgmq/river/SKIP LOCKED), clean-room
safe. Honcho's *actual* impl comparison is a gated step before building **if
Postgres wins** — guard against handing back their own stack (or worse, minus
features). Path-2 research corrected my framing: Redis durability is no longer a
blanket weakness (WAITAOF, MemoryDB, Jun-2026 ElastiCache-Valkey durable
option); reject/defend the specific config. Path-3 reframed: "custom+WAL" splits
into House A (queue on an embeddable engine — defensible) vs House B (bare WAL —
frontier). Custom path will need a language drilldown if chosen.

## 2026-06-25 — Backend down-select: Path 1 default, Path 2 to earn, Path 3 out

- **Path 3 (custom + WAL): OUT.** Not worth the time investment — for them or us.
- **Path 1 (Postgres done right): DEFAULT.** The safe, defensible choice. /draft
  this first so we know what "safe" looks like in detail.
- **Path 2 (Redis/Valkey): the upgrade we must JUSTIFY.** Phil's secret goal is
  "solve it so well they adopt it" → Path 2's primitive fit serves that. But that
  goal is only attainable *after* clearing "solve it so defensibly they hire me"
  → Path 1 diligence first. Path 1 stands unless we can justify Path 2 over it.
- **Sequence:** /draft Path 1 → iterate a couple passes, close open questions →
  compare to Honcho's actual solution → then decide whether Path 2 is justified.
- Deferred: Sources-section convention → philset inbox.

## 2026-06-25 — Stack: Go (pgx) for implementation + load generator

Chosen for the load-generator-not-the-bottleneck reason (goroutines saturate PG
so the measured plateau is Postgres's, not a GIL-bound client's), honest crash
concurrency model, and continuity into Path-2/sharding — not raw CPU (worker
compute is simulated; PG single-writer ceiling binds). Resolves design OQ#8.

## 2026-06-25 — Path 1 draft: open questions resolved

- **Design posture: loss-tolerant, latency-sensitive.** A Honcho task = one
  observation about a user, not a financial txn → losing/double-recording one is
  survivable; loop latency is the real UX cost. **Never trade loop latency for
  delivery guarantees.** At-least-once + idempotent handlers default; exactly-once
  aspirational; transactional-ack available at no latency cost. *Doubles as a
  Path-2 argument* — Valkey's low-latency/bounded-loss profile fits this posture.
- delete-on-ack (tasks are conversation snapshots; don't keep forever).
- workspace == tenant for thresholds.
- `work_units` lifecycle: **tombstone + TTL reap** (chat keys recur; reuse avoids
  churn, TTL GCs the dead). Validate recurrence vs churn against Honcho later.
- Wakeup: **adaptive poll backoff in v1**, not LISTEN/NOTIFY (global notify-queue
  lock + thundering herd bite hardest at high throughput, help least there).
- Load test: **2 HASH partitions on `tasks`** — ~nil scope, real PoC value,
  pre-stages Path-2 shard-by-key story.

## 2026-06-25 — ACCEPTED: postgres-work-unit-queue design (Path 1)

Blessed via /ship. Implementation deferred — next step is the Honcho-actual
comparison (gated wall-crossing) before any code.

## 2026-06-25 — Honcho-actual comparison: we hold up (we improve)

Ground-truthed against live honcho source (v3.0.9, pushed today).
`assessments/honcho-actual-comparison.md`. Key findings:
- Their buffered gate EXISTS (concept not ours) — but implemented as a **per-poll
  `SUM…GROUP BY` scan**, the exact thing the brief forbids. Our maintained
  aggregate + first-class `work_units` row is the real, defensible improvement
  (the take-home is basically "fix this line in our code").
- We converge on flush (age-cap) and adaptive poll-backoff → validates those
  choices. We add retry/DLQ (they have none) and provable ordering (per-unit seq
  under row lock vs their `ORDER BY id`).
- Honest caveat: their scan is fine at *their* scale; the brief specifies the
  scale where it isn't. Frame as "one change I'd make," not arrogant rewrite.
- **Path 2 impact:** the look-ahead win is now banked in PG, so Redis's ZSET is
  no longer the differentiator. Path 2 must justify on throughput ceiling +
  latency posture only. Bar = "load test shows a PG ceiling Valkey clears."
- Doc bug flagged: `meta/honcho-internals.md` Stage 4 says claim uses SKIP
  LOCKED; live code uses INSERT…ON CONFLICT (SKIP LOCKED only in reaping). Fix
  before CTO conversation.

## 2026-06-25 — Session arc: Honcho comparison is the reset seam

Continue this session through finalizing the Path 1 draft + the Honcho-actual
comparison. Reassess at that committed boundary: if the window is heavy with
Honcho-reading, /ttyl + fresh /hello before the Path 2 (Valkey) draft so it loads
only load-bearing artifacts. Roll straight on if still lean.
