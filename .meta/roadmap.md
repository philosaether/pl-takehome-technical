# Roadmap — Buffered Work-Unit Queue Take-Home

Future work and deliverables, sequenced. Append-forward via `/defer`.
Designs accepted (`designs/postgres-work-unit-queue.md`,
`designs/valkey-work-unit-queue.md`); this is the **build plan** that ships them.

---

## The audition (why this shape)

The role: Plastic Labs is research-forward, just came through an
orders-of-magnitude growth spurt, put out the immediate fires, raised, and now
wants someone to **mature them into a full-scale enterprise.** This challenge is
transparently *"solve a real problem in our architecture"* — their pg-as-queue
runs a `SUM…GROUP BY` on every poll that **will not survive two more orders of
magnitude.** They want someone who can solve that.

We don't just solve it — we **oversolve** it, as a *package*:

> **This month** — add the maintained look-ahead table; it fixes the scan now.
> **The future** — migrate to Valkey when the numbers justify it; here are the
> numbers to watch and the values where you switch.
> **How I know it works** — both backends built, a head-to-head load test, and
> deterministic proofs. Not vibes.

That maps 1:1 onto the **1-page writeup**: ¶1 = the look-ahead fix + watch-
numbers + switch-thresholds (+ the Honcho PR); the rest = the Valkey stack.

**And we satisfy the literal brief** — all 7 required capabilities + every
"what good looks like" bullet (compliance checklist at the bottom). Oversolve
*on top of* the literal terms, never instead of them.

---

## Build spine (what changed from v1)

- **Build *both* paths and run a head-to-head** — the comparison *is* the "how I
  know it works" evidence, and the answer to "what's your second choice?" The
  measurement is the senior signal, so it's wanted regardless.
- **Path 1 first** — it's the this-month answer *and* the baseline; if we run out
  of days, Path 1 + writeup still ships a complete, defensible deliverable.
- **Path 2 is time-gated behind Path 1, not measurement-gated.** (The old
  "decision gate" became a *deliverable* — migration triggers we hand them —
  rather than a fork in our own build. See M4.)
- **Each path is a standalone Docker image**; the load generator is shared and
  pointed at either. Clean separation + apples-to-apples.
- **Canonical numbers come from a pinned cloud box, not the laptop** — laptop's
  "mid stats" would pollute the head-to-head. Laptop for dev; cloud for the
  graded run. (Within the $20 cap; track spend.)
- **New deliverable: a PR to the Honcho repo** — "merge this to get all of P1's
  wins" against their *real* schema. The this-month fix, made tangible. (Biggest
  adopt-it signal; see M-PR.)

---

## M0 — Scaffold ✅ DONE (merged to `main` 2026-06-26) · `feature/scaffold`

- [x] Go module, repo layout, `Makefile`, pinned versions.
- [x] **Backend driver interface** — 8 methods (`Enqueue/Claim/Drain/Ack/Release/
      Heartbeat/Fail/ReapExpired`) behind one shared worker loop + loadgen (the
      apples-to-apples contract). `internal/queue` imports no driver.
- [x] **Per-path standalone images** via build tags (single-driver binaries,
      default=memory) + compose for local dev. *(Refined from "shared loadgen
      image" → build-tag single-driver per OQ1.)*
- [x] Config (volatility-based: `PLQ_*` env tunables, logic stable).
- [x] README skeleton + `make load-test` wired (harness fills in M2).
- [x] **Bonus:** full working in-memory backend (correctness oracle) + M0 smoke
      proofs (in-order drain, gate); reviewed + fixed before merge.

## M1 — Path 1: Postgres queue ✅ DONE (merged to `main` 2026-06-26) · `feature/postgres-queue`

The this-month answer *and* the baseline. Mirrors the accepted PG design + driver design.
Built, reviewed, verified 8/8 conformance vs live postgres:16.

- [x] Schema: `work_units`, `tasks` (HASH-partitioned, 2 parts), `dead_letters`,
      `tenant_config`, `wu_claimable` partial index (+ `wu_leased`/`wu_flushable`).
- [x] `enqueue` — maintained `pending_cost` aggregate under the unit row lock
      (the I3 win — kills the `SUM…GROUP BY`); per-unit `seq` (0-based).
- [x] `claim` — `FOR UPDATE SKIP LOCKED` on the partial index (exclusive,
      contention-free).
- [x] `drain + ack` — in-order batch, delete-on-ack, self-validating ack CTE,
      keep-or-release.
- [x] Reaper — flush-flip (per-head age-cap) + lease-expiry reclaim.
- [x] Failure modes: poison→DLQ (head-guarded), lease reclaim.
- [x] **Bonus:** the conformance suite (8 scenarios) — oracle as reference; the
      apples-to-apples *correctness* guarantee.

## M2 — Load generator + the four proofs (must) · `feature/loadgen`

- [ ] Producers: Zipfian `wu_key` churn (keys born/drained), pinned RNG seed,
      configurable cost distribution.
- [ ] Workers: claim→drain→process(simulated, no LLM)→ack; sweep **1/10/100/1000**.
- [ ] Crash injection: kill workers mid-batch on a schedule.
- [ ] Metrics export + throughput-vs-workers graph; **runs on the pinned cloud box.**
- [ ] **Deterministic proofs** (tests, not vibes):
      1. ordering-under-crash (the 3-of-10 scenario)
      2. gate (nothing runs below T except via flush)
      3. flush (age-cap fires, unit drains)
      4. look-ahead cost (~flat vs task count at 10⁶/10⁴–10⁵; `EXPLAIN`; contrast
         a naive `SUM…GROUP BY` baseline — *their current code, measured*)

- [ ] **Post-test optimization candidates (deferred from M1 review — measure first).**
      Two correct-but-extra round-trips in the PG driver; only worth resolving if the
      load test shows they compound under contention. If they do, the before/after
      numbers are a strong **talking-point** (see `talking-points.md`).
      Deferred from: pl-takehome-technical/feature/postgres-queue (2026-06-26).
      Blocker: M2 load-test numbers (don't optimize blind).
  - **Drain: collapse 2 round-trips → 1** (lease-check SELECT + task fetch) via an
    `EXISTS`/`FOR UPDATE` subquery.
  - **Enqueue: kill the tenant-cache-miss round-trip** on first enqueue per
    workspace — pre-warm `tenant_config` at startup, or lazy-load on `Claim`
    instead of the enqueue hot path.

## M-PR — The Honcho PR (high-value stretch) · against a **fork** of `plastic-labs/honcho`

"Merge this to get all of P1's wins" — the this-month fix against their real
code (SQLAlchemy + asyncpg; `queue` + `active_queue_sessions` tables).

- [ ] Add a maintained per-work-unit aggregate row + maintain it on enqueue/ack;
      replace the per-poll `SUM(token_count)…GROUP BY` claim subquery with the
      indexed lookup. (Optionally: `attempts`/DLQ; provable per-unit ordering.)
- [ ] Bonus goodwill: fix the doc-vs-code bug we found (`honcho-internals.md`
      Stage 4 says claim uses `SKIP LOCKED`; live code uses `INSERT…ON CONFLICT`).
- [ ] **Fork the repo; open the PR against our fork; link it from the writeup.**
      Never opened against their real repo (presumptuous unprompted) — the fork
      makes it a real, reviewable, mergeable artifact without touching their tree.

## M3 — Path 2: Valkey, time-gated (stretch) · `feature/valkey-queue`

Mirrors the accepted Valkey design. Depth set by remaining days, not measurement.

- [ ] **M3a (single node):** Valkey driver behind the M0 interface — Streams +
      `eligible`/`pending_flush`/`leases` ZSETs + Lua enqueue/claim/ack;
      `XAUTOCLAIM` reclaim; `rueidis`. Standalone image. **Head-to-head vs Path 1**
      (throughput + loop-latency p99) on the same cloud box. Proves the latency half.
- [ ] **M3b (sharded — top stretch):** shard by **`workspace`** (`hash(workspace)`,
      *not* full `wu_key` — keeps a whole tenant on one shard: clean seam + failure
      domain) across 2–4 primaries, shard-local coordination ZSETs; ~linear write
      scaling vs 1 PG primary. *Doubles as the brief's distributed-extension
      deliverable — built, not argued.*

## M4 — Writeup + migration triggers + walkthrough prep (must) · `meta/writeup`

- [ ] **1-page** writeup in the thesis shape: ¶1 = look-ahead fix (this month) +
      **the migration triggers** (the watch-numbers + the plateau/arrival-crossing
      where you switch) + the Honcho PR; rest = the Valkey stack (the future) with
      head-to-head numbers + sharding. (Hard cap: 1 page.)
- [ ] **Migration-trigger table** (the old "decision gate," now a deliverable):
      which metrics to watch (PG write-throughput plateau vs sustainable arrival at
      target scale, loop-latency p99, vacuum/bloat tax) and the values that say
      "switch to Valkey now."
- [ ] **Fairness paragraph** (brief asks "what does fairness mean in this system,"
      and we run a *single shared queue across all tenants* — no deployment
      isolation). Define the two grains: **work-unit age-fairness** (built —
      `flush_deadline` claim order → no unit starves, bounded head latency) vs
      **per-tenant (workspace) fairness** (not built — tenant-blind selection lets a
      high-volume tenant collect worker-seconds proportional to its volume and
      degrade a light tenant's latency; `wu_key` tiebreak blocks one tenant's units
      together → *that's where it breaks* on simultaneous threshold-cross). Show the
      one-seam extension (claim `ORDER BY` / ZSET score + optional per-tenant
      in-flight lease cap → deficit-round-robin / weighted lottery). Decision:
      **explain + name the knob, don't build WFQ.**
- [ ] Rehearse the 30-min screen answers (all in our designs): backend + second
      choice; eligibility cost & enqueue-path aggregate cost; the 3-of-10 crash
      trace; hot/wedged/stranded unit; fairness under simultaneous threshold-cross;
      runtime-T change; plateau + first bottleneck; multi-machine sharding; "10× the
      workers — what breaks first?"

---

## Risks / watch-items

- **Scope vs timeline.** M0–M2 + M4 is the honest must-ship; M3 and M-PR are the
  oversolve. Path 1 first so a complete deliverable always exists. **If days get
  tight, Path 2 (M3) before M-PR — signal before snark:** the enterprise-scale
  vision is the role; the "one line I'd change in your code" PR can read as snark,
  so it earns its place only after the signal lands. Don't let either sink M4.
  (Salience over plan.)
- **Load generator must out-run the backend** (the Go decision) — else the
  measured plateau is the client's, not the queue's. Verify saturation first.
- **Cloud box is the canonical env, $20 cap.** Pin the instance type; run the
  head-to-head sequentially on the *same* box for comparability; track + report
  spend. Laptop only for dev.
- **The Honcho PR goes against a fork, never their real repo** — keeps it a real
  reviewable artifact (link from the writeup) without an unprompted push to their
  tree.

## Open / to talk out

- ~~**Per-tenant isolation model** (pinned, discuss thoroughly).~~ **RESOLVED
  2026-06-26.** Constraint was never in the graded brief (only "failure
  isolation," `:175`); the "isolated resources per customer" note was Phil's
  capture, which he corrected → *one instance processes a tenant's unit at a time*
  (the exclusive claim), not a cluster per tenant. **Model: a single shared queue
  across all tenants; `workspace` is the seam** (tenant/fairness grain + shard key
  `hash(workspace)`). Surfaced a fairness gap (per-tenant fairness not built) →
  handled as an explicit writeup paragraph (M4), not a build. See `decisions.md`
  2026-06-26 + the design amendments.

## Client Questions (kickoff / WhatsApp — accrue here)

- **Timeline / availability?** Sets how much of M3 + M-PR is in scope.
- **Per-tenant isolation model** — one instance per tenant, or shared with
  resource isolation? (The pinned item above.)
- Target-scale envelope sanity-check (10⁶ tasks / 10⁴–10⁵ units / 1000 workers) —
  is that the regime they're scaling *into*?

---

## Out of scope (deliberate, defend if asked)

- Real LLM calls (simulated downstream work).
- Path 3 custom-WAL engine (cut earlier — not worth the time).
- Managed durable tier (named as the zero-loss production knob, not run).
- Building a consensus protocol (the brief explicitly says don't).

---

## Brief-compliance checklist (the literal terms — don't lose these to the oversolve)

Required capabilities: [ ] enqueue (implicit unit creation) · [ ] buffered
exclusive claim · [ ] in-order drain across claim/release + crash · [ ] parallel
across units · [ ] cheap look-ahead (first-class, no full scans) · [ ] flush
policy (defended) · [ ] load generator with churn + numbers.
"Good looks like": [ ] ordering provable (deterministic) · [ ] gate provable ·
[ ] look-ahead cheap at 10⁶/10⁴–10⁵ · [ ] throughput-vs-workers graph + defended
plateau · [ ] failure modes (crash/hot/stranded/poison) · [ ] backend choice
defensible (+ 2nd choice) · [ ] operationally feasible (deploy/monitor/recover).
Deliverables: [ ] incremental commits · [ ] loadgen + numbers · [ ] 1-page
writeup + distributed-extension stretch.
