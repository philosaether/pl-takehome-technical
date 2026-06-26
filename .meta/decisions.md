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

## 2026-06-25 — Path 2 draft: engine = Valkey, client = rueidis (two paper OQs closed)

`/draft` of `designs/valkey-work-unit-queue.md` (Streams + ZSET + Lua; sharded
for the throughput pitch). The two open questions that don't need a build/measure
loop, resolved:
- **Engine: Valkey (not Redis 8.4).** *Verified* `XREADGROUP … CLAIM` is
  Redis-8.4-only — Valkey ≤9.1 has no `CLAIM` option (docs list
  GROUP/COUNT/BLOCK/NOACK/STREAMS; 9.1 notes add no consumer-group features). It
  only optimizes *large*-PEL reclaim; our per-unit PEL is small (one worker/unit,
  bounded batch), so `XAUTOCLAIM` loses ~nothing and we keep Valkey's
  license/cost/managed-default edge. Closes the assessment's open item #1.
- **Client: rueidis.** Both clients unfamiliar → pick on auto-pipelining/throughput.
- Remaining OQs (#1 latency-under-everysec, #2 claim-funnel ceiling, #4
  group+PEL-vs-head-cursor, #5 shard count, #7 tunables) are all build/measure or
  prototype-in-build. Draft is paper-complete; gated on the head-to-head load test.

## 2026-06-25 — ACCEPTED: valkey-work-unit-queue design (Path 2)

Blessed via /ship. Both backend designs (Path 1 Postgres, Path 2 Valkey) now
accepted and coexist: Path 1 is the safe fallback, Path 2 the gated upgrade.
**Neither is implemented** — the head-to-head load test is the deliberate next
gate that decides which we build. Implementation NOT started here by choice;
merging design work to main + planning a deliverables roadmap instead.

## 2026-06-25 — Session arc: Honcho comparison is the reset seam

Continue this session through finalizing the Path 1 draft + the Honcho-actual
comparison. Reassess at that committed boundary: if the window is heavy with
Honcho-reading, /ttyl + fresh /hello before the Path 2 (Valkey) draft so it loads
only load-bearing artifacts. Roll straight on if still lean.

## 2026-06-25 — Designs merged to main; deliverables plan set (`roadmap.md`)

Both designs accepted → fast-forward merged `feature/postgres-queue` to `main`.
Opened `meta/deliverables-planning`; wrote `roadmap.md`. Plan framed as **the
audition**: Plastic Labs (research-forward, post-growth-spurt, freshly funded)
hiring someone to mature them to enterprise; the challenge = "fix the real
problem" (their per-poll `SUM…GROUP BY` won't survive 2 more orders of
magnitude). We **oversolve as a package**: *this month* = add the maintained
look-ahead table; *the future* = migrate to Valkey when numbers justify; *how I
know* = build both + head-to-head + deterministic proofs. Maps onto the 1-page
writeup (¶1 fix + migration triggers + Honcho PR; rest = Valkey stack).

Key build decisions:
- **Build both paths, head-to-head** — the comparison IS the evidence + the
  "second choice" answer. Old "decision gate" demoted from a build-fork to a
  **deliverable** (migration-trigger table we hand them). Path 2 is **time-gated
  behind Path 1**, not measurement-gated.
- **Path 1 first** — complete deliverable always exists if days run out.
- **Each path = standalone Docker image**; shared load generator.
- **Canonical numbers on a pinned cloud box** (not laptop — its "mid stats"
  pollute the head-to-head); within the $20 cap, track spend.
- **Honcho PR** ("merge this for all of P1's wins") — but against a **fork**, PR
  to the fork, link from the writeup; never opened unprompted on their real repo.
- **If days get tight: Path 2 before the PR — "signal before snark"** (scale
  vision is the role; the "one line I'd change" PR can read as snark, so it earns
  its slot only after the signal lands).
- Parked for a thorough talk: **per-tenant isolation model** (Phil:
  one-DB-per-tenant is overkill) — decides per-tenant-ceiling vs fleet-aggregate
  throughput, which sets how hard the sharding story must work.
- Still no implementation code — next session starts at M0 (scaffold).

## 2026-06-26 — RESOLVED: isolation model = shared queue; fairness gap surfaced

Resolved the parked per-tenant isolation question (gated M3b). **The constraint
was never in the graded brief** — `platform-screening-brief.md` only says
"failure isolation" (sharding failure domains, `:175`). The "isolated instances
/ isolated resources per customer" language lived solely in Phil's meeting
capture (`inbox/pl-takehome-technical.md:10`), and **Phil corrected it**: he
meant *one honcho instance processes a given tenant's work unit at a time*
(exclusive claim), **not** a separate cluster per tenant. So:

- **Isolation model: a single shared queue across all tenants.** No
  deployment-level per-customer isolation. Worker grabs a unit belonging to
  exactly one tenant and processes it exclusively — that exclusivity is the only
  "isolation" the note ever meant.
- **`workspace` is the one consistent seam** — tenant/fairness grain, shard key,
  and `wu_key = (workspace, session, peer)` prefix all align (consistent with the
  earlier "workspace == tenant for thresholds" decision).
- **Shard seam = `hash(workspace)`, NOT full `wu_key`.** Sharding by the full
  `wu_key` scatters one workspace's sessions across shards; sharding by
  `workspace` keeps a whole tenant on one shard (clean seam + failure domain).
  Corrected M3b in `roadmap.md` accordingly. (Separately: worker isolation during
  processing comes from the **exclusive lease/claim**, not from any hash.)

**Fairness finding (the brief asks "what does fairness mean," `:172`):** our
accepted designs implement **age-fairness at the work-unit grain** only — claim
`ORDER BY flush_deadline NULLS LAST, wu_key` (PG `:134`; Valkey eligible-ZSET
score). That gives *no unit starves* + a defensible burst answer. It does **not**
implement **per-tenant (workspace) fairness**: tenant-blind selection means a
flooding/high-volume tenant collects worker-seconds proportional to its volume
and degrades a light tenant's latency (never starves it). The `wu_key` tiebreak
sorts one tenant's units into a contiguous block — *that's where it breaks* on
simultaneous threshold-cross. Per-tenant **config** exists (`tenant_config`:
threshold, max_wait); tenant-aware **selection** does not.

**Decision: do NOT build WFQ.** The brief says *explain* fairness, not build it.
Define the two grains, defend the age-fair mechanism (bounded latency, no
starvation), name the tenant gap + breakage, and show the **one-seam extension**
(the claim `ORDER BY` / ZSET score + an optional per-tenant in-flight lease cap →
deficit-round-robin / weighted lottery). Built-mechanism + named-policy-knob is
the senior answer; a half-built WFQ is worse than a well-defended deferral.
Captured as an explicit fairness paragraph in M4 (writeup + 30-min prep) and
amended into both design docs.

**Amended (not superseded)** both accepted designs — A1 on each: records the
shared-queue isolation model + the two-grain fairness finding; the Valkey A1 also
narrows the shard key from `wu_key` → `workspace`. Decisions/structure stand;
additive reasoning + one shard-key narrowing.

## 2026-06-26 — ACCEPTED: scaffold design (M0)

Blessed via /ship; implementation starting now on `feature/scaffold`. The M0
contract: a single Go binary (`cmd/plq`, subcommands `worker|loadgen|reap`) whose
**`Backend` interface** (8 methods: Enqueue/Claim/Drain/Ack/Release/Heartbeat/
Fail/ReapExpired) is the apples-to-apples seam — `internal/queue` imports no
driver; the shared `worker.go` loop + loadgen depend only on the interface. Key
resolutions from the draft:
- **Build tags, not runtime flag** — single-driver binaries (`-tags
  postgres|valkey`, default=memory); don't ship dead driver code to scaled pods.
  Apples-to-apples = identical loadgen *source* compiled per-driver.
- **In-memory backend = default no-tag build** — dev, CI typecheck, and the
  proofs' correctness oracle.
- **Heartbeat kept + wired** (~Lease/3) — LLM batches can outlive the lease.
- **Process model configurable** (`zero|fixed|cost`) — *the axis that locates the
  PG→Valkey cutover*: zero-work finds the backend ceiling, cost-proportional shows
  where a real workload sits vs it. M2 sweeps it (outer loop); feeds M4 migration
  triggers. Code cost trivial; cloud-minutes ($20 cap) is the real budget → small grid.
- **Reaper in-process** (v2: standalone service — flagged). **Sweep via
  `WORKERS=N` re-run** (v2: loadgen-orchestrated ramp for autoscaling — flagged).
- **Module path = `github.com/philosaether/pl-takehome-technical`** (matches the
  shared repo; the path is an import prefix, not a second repo).
- New artifact: `.meta/enhancements.md` — backlog of flagged "possible
  enhancements" bullets; curate the most compelling set into the 1-page brief at M4.
- Go toolchain absent on the box → installing via brew as the first build step.

## 2026-06-26 — ACCEPTED: postgres-driver design (M1)

Blessed via /ship; building now on `feature/postgres-queue`. Build design for
`internal/postgres` against the M0 `queue.Backend` contract (concrete schema +
per-method SQL + pgx wiring). `designs/postgres-driver.md`. Key resolutions from
the draft iteration:
- **Composite natural key `(ws,sess,peer)`** instead of the accepted design's
  hashed `wu_key bytea` — 1:1 with the contract type, no hash/decode, `workspace`
  native for tenant predicates. The one deliberate divergence (flagged; revisit
  only if index width bites).
- **`lease_token uuid` + `max_wait_ms`** added to `work_units` (ABA-safe lease
  validation that the contract's `LeaseToken` models; denormalized max_wait).
- **Self-validating ack** — `DELETE … RETURNING cost` summed in the ack CTE (no
  caller-supplied cost); `keep` computed in-SQL via LATERAL (it depends on post-ack
  state, can't be a param). Fewer statements *and* a driver-owned aggregate invariant.
- **Per-head flush (OQ1)** — eligibility recomputed every ack as `remaining ≥ T OR
  new-head aged`; **drops the M0 oracle's sticky `flushed` bool** (M1 touches M0
  code — not purely additive). The conformance suite pins per-head for both
  backends. Logged in `talking-points.md` (new artifact: interesting
  decisions/tradeoffs to curate into the 1-pager at M4).
- **Conformance suite** `RunConformance(t, factory)` — same scenarios vs memory
  (CI, always) and postgres (gated by `PLQ_TEST_POSTGRES`). The apples-to-apples
  *correctness* guarantee; pre-stages the M2 proofs.
- **Embedded idempotent `schema.sql`** (`go:embed`, no migration tool); deferred:
  tombstone-TTL reaping + golang-migrate → `enhancements.md`.
- `pgx/v5` added (bumped the go directive to 1.25 → Dockerfiles follow).
