---
Status: accepted
Date: 2026-06-27
Accepted: 2026-06-27
Assessment: results/run-cloud-1/, results/run-cloud-2/, results/lookahead/
---

# One-Pager Construction — Desired State

A **construction plan** for the 1-page PDF deliverable Phil sends to Vineeth
(+ a GitHub repo invite). This doc is metadata: it settles *structure, layout,
figures, toolchain,* and *proposed sample copy* so the actual one-pager falls
out of an SII iteration loop. **It is not the one-pager.** The deliverable is a
single physical page; this doc can be as long as it needs to be.

---

## The job of the document (read this before judging any choice)

It has two jobs, and they mostly align:

1. **Literal deliverable** — the brief asks for a "1-page writeup, including the
   distributed extension design as a stretch discussion." Hard cap: one page.
2. **Social object** — the email's message is *"natural point for stakeholder
   feedback; this is the high-touch relationship you'd get."* Subtext: *"done
   two days early, and it's excellent."* The page has to *radiate competence at
   a glance* before a word is read.

Where they tension: the brief explicitly **de-emphasizes the doc** ("the code,
the numbers, the operability story, and the live walkthrough are the
deliverables. Not the doc") and warns off lit-surveys/theses. So "impressive"
must read as **crisp, evidence-forward, confident** — a Stripe/Linear
engineering-note register — *not* a glossy pitch deck. Restraint signals
seniority; decoration signals compensating. Every polish decision serves
clarity or it's cut.

## Narrative spine (locked)

| Beat | Content | Carries |
|------|---------|---------|
| **This month** | The look-ahead: maintained aggregate + partial index. 1,364× over the naive `SUM…GROUP BY`. A ~40–80 line change to the Postgres you already run. | Figure 1 (hero) + Figure 2 (the patch) |
| **The future** | When/why to move to Valkey. ~15×/primary, shards linearly — *but* the edge collapses as per-task work grows. A decision rule, not a sales pitch. | Figure 3 (the manifold — secret hero) |
| **Methods** | How the numbers were produced: identical contract + shared loadgen, deterministic order/gate/crash proofs, the fair sharded head-to-head, cloud spend ≪$20. | §3 "Methods" + endnotes band |

Spine is now framed by the **growth story** (Phil's lead): an orders-of-magnitude
user explosion → "operational maturity requires a deliberate choice" → the
two-stage plan. Stage 1 = the Postgres look-ahead (ship now); Stage 2 = move the
queue-bound work to Valkey. The brief-graded operability/fairness/flush items
live in the **endnotes** (the *walkthrough* carries them, per the brief).

## Toolchain — **DECIDED: LaTeX** (figures rendered out, embedded)

Settled (Phil): **LaTeX**. The "this paper proposes…" academic register in the
voice-pass copy is native to it; the growth-→-experiment-→-recommendation arc
reads as a research note, which is exactly the Plastic-Labs key.

- **Why LaTeX** — research-forward audience with academic roots; typographic
  authority for free; figures + an endnotes apparatus are first-class; "one
  dense beautiful page" is its home turf.
- **Figure escape hatch (resolves the old open question):** charts are rendered
  to image **outside** LaTeX (matplotlib/whatever gives the cleanest result) and
  **embedded** — so a custom manifold rendering never fights pgfplots/TikZ. We
  get LaTeX's prose/layout authority without its plotting constraints.
- **All three figures get redrawn** — the current `results/*.png` are functional
  matplotlib grey, not deliverable-grade. One palette, one type family, high-DPI
  (vector where the embed allows). This is the data-viz slice.

## Layout — single page, portrait

Full-width masthead + lead, then a 2-column body (the classic dense-but-readable
academic one-pager). Sketch:

```
┌──────────────────────────────────────────────────────────────┐
│  MASTHEAD: title · Phil Bascom · Platform Eng take-home      │
│            · 2026-06-27 · github.com/…/pl-takehome (repo link) │
├──────────────────────────────────────────────────────────────┤
│  LEAD (full width, ~4 lines): the problem in one sentence +    │
│  the two-part recommendation up top. Busy reader is done here. │
├───────────────────────────────┬──────────────────────────────┤
│  §1 THIS MONTH                 │  §2 THE FUTURE               │
│  look-ahead prose              │  when-to-switch prose        │
│  ┌─────────────────────────┐   │  ┌────────────────────────┐  │
│  │ FIG 1  look-ahead       │   │  │ FIG 3  the manifold    │  │
│  │ 1,364× log-log (HERO)   │   │  │ gap vs workers×work    │  │
│  └─────────────────────────┘   │  │ (secret hero)          │  │
│  ┌─────────────────────────┐   │  └────────────────────────┘  │
│  │ FIG 2  the minimal patch│   │  §3 HOW I KNOW (proofs +     │
│  └─────────────────────────┘   │  operability, compressed)    │
├───────────────────────────────┴──────────────────────────────┤
│  ENDNOTES band: reproducibility · cloud spend (≪$20, torn     │
│  down) · "sharpening the manifold" · backend 2nd choice        │
└──────────────────────────────────────────────────────────────┘
```

Layout is a hypothesis — figures may force a reflow. Hold the discipline of one
side; if it overflows, **cut copy, don't shrink type**.

**Does enhancements.md / talking-points.md justify endnotes or a §3?** Yes —
there's real brief-graded content there, and it argues §3 *earns its space* (now
the "Methods" section). The candidate pool, mapped:

- **Flush-policy defense** (talking-points) — per-head age-cap: every task flushes
  when *it* becomes the oldest and ages out, so one stale task can't defeat
  buffering for the work behind it. The brief explicitly grades "defend your flush
  mechanism." → one endnote line.
- **Fairness / WFQ** (enhancements) — per-tenant in-flight cap + claim-`ORDER BY` /
  ZSET-score is the one-seam extension; decision was *explain, don't build*. The
  brief asks "what does fairness mean in your system?" → one endnote line.
- **"Valkey gets there on less iron"** (talking-points) — the head-to-head puts each
  Valkey primary on the *same* box class as PG, but Valkey is single-threaded and
  won't use it; so the same-class result is conservative and the real $/throughput
  gap is *wider*. → strengthens §2; one endnote line.
- **Stage-2 hardening tail** (enhancements) — standalone reaper service, versioned
  migrations (golang-migrate), managed durable Valkey tier. → one "what stage-2
  adds" endnote.
- **Correctness proofs** (order/gate/crash, deterministic) → §3 Methods.

Selection happens at layout (endnotes are dense small-type, but still finite).

## Figures (the long pole — lock these first)

### Figure 1 — the look-ahead (HERO)
- **Source:** `results/lookahead/lookahead.csv`. Log-log, pending tasks 10⁴→10⁷
  on x, ms-per-poll on y. Two series: maintained look-ahead (~flat 0.3–1.9 ms)
  vs naive `SUM…GROUP BY…HAVING` (climbs to 2,524 ms at 10⁷).
- **The annotation IS the chart:** a vertical callout at 10⁷ reading **1,364×**.
  Three orders of magnitude of daylight on a log axis — the one chart that makes
  the case alone.

### Figure 2 — the precise change *against Honcho's real code* (the "that's it?" beat)
**Upgraded from "our distilled patch" → "the surgical diff against their actual
repo."** Full mapping + citations in [`honcho-fig2-source.md`](honcho-fig2-source.md).
This is the M-PR signal without the PR — and stronger, because it's a cited,
bounded recommendation rather than a merge ask.

- **The find:** Honcho's `get_and_claim_work_units`
  (`src/deriver/queue_manager.py#L330-L445`) already runs *our naive baseline* —
  two `SUM(messages.token_count) … GROUP BY work_unit_key … HAVING ≥ 1024`
  scans over the `queue` table on **every 1s poll**. Their hot path *is* the
  thing Fig 1 measures.
- **The figure:** an essence-excerpt of the change — their current GROUP-BY
  (cite L347–361) above, the indexed `work_units` lookup below. Caption carries
  the honest scope: **~150 lines across 4 files + 1 migration — a small
  `work_units` aggregate table maintained on enqueue/ack, swapping the per-poll
  GROUP-BY for an indexed `FOR UPDATE SKIP LOCKED` lookup. No new infrastructure.**
- **Honesty corrections baked in** (see source doc §Candor):
  - **Not "~40 lines / one column."** That's *our* schema (we already have the
    table). Honcho has no persistent work-unit row, so the real change is a new
    (small) table + write hooks ≈ 150 lines. Cite the Honcho number, not ours.
  - **Scope = `representation` work units** (their other task types are FIFO).
  - **1,364× is the algorithmic-complexity win** (`O(tasks)`→`O(work units)`),
    our measurement — *what their hot path costs as the backlog grows*, not a
    benchmark of their prod. Lands as "before it bites your next growth cycle."
- *Why include it:* a code-reading audience trusts a cited diff over a claim. It
  turns "a deliberate, low-risk improvement to the Postgres you already run" from
  assertion into something they can `git blame`.

### Figure 3 — the manifold (SECRET HERO)
- **Source:** `results/run-cloud-2/sweep.csv`, the process sweep {0,2,20,200} ms
  × workers {1…1000} × shards. Plot the **Valkey/Postgres throughput ratio** as
  a surface (or filled contour) over (workers, per-task work-ms), with the
  **crossover contour** traced — the line where Valkey stops being worth its
  durability+ops cost.
- **The story it tells at a glance:** the gap is a canyon at cheap/fast work
  (26× at 1000 workers, 0 ms) and **closes to ~1.1× at 200 ms** — because at
  200 ms/task throughput is pinned at `workers × 5/s` and the *work*, not the
  *queue*, is the bottleneck. Whoever reads this sees a person who found the
  *precise* condition under which their expensive migration pays off — and is
  honest that sometimes it doesn't.
- **Build honesty:** use only `saturated=true` points; PG has producer-bound
  lower bounds + a few noisy spikes (`pg,4,30,2ms=12246`). Interpolate the
  surface from clean points; flag the gaps in endnotes. 4 work-points is enough
  for a *plausible* surface, not a publication one — say so.
  - **Decision (Phil):** build it from the existing 4 work-points first; decide on
    a rerun *after* seeing it render. Mild preference against rerunning.
- **3-D vs 2-D:** Phil pictures a 3-D manifold. 3-D surfaces read as impressive
  but often *worse* (occlusion, unreadable axes in print). **Open question
  below** — strong default is a 2-D filled-contour/heatmap with the crossover
  line, which prints crisp and reads in 2 seconds. Can render the 3-D as the
  "impressive" version and A/B in the SII loop.

---

## Proposed sample copy (structure + titles are Phil's, per inbox/one-pager-copy.md)

The **Lead/Abstract and section titles are Phil's** — voice-pass source of truth
lives in [`.meta/inbox/one-pager-copy.md`](../inbox/one-pager-copy.md). The body
copy below is rewritten to match that structure and the measured, "this paper
proposes…" research register. React/replace freely.

### Title + masthead
> **A Two-Stage Queue Migration Plan for Honcho**
> Phil Bascom · philbas.com · Platform Engineering take-home · 2026-06-27
> `github.com/philosaether/pl-takehome-technical`

### Lead / Abstract — *Phil's, verbatim from `one-pager-copy.md`*
This year, the Honcho application saw an orders-of-magnitude explosion in use, from ~200 active users to ~30,000. While its existing proof-of-concept architecture has held up admirably, operational maturity will require a deliberate choice of implementation methodology.

In this paper, we propose a two-stage queue migration plan for the Honcho application. The first stage represents a minimal, ~150-line alteration to the existing Postgres queue code, introducing an additional table, two write-path hooks, and no new infrastructure. We expect this change to result in a ~1000x faster scheduling lookahead, sufficient to support another orders-of-magnitude growth cycle.

The second stage would move some or all of the work processed by Honcho to a Valkey-backed in-memory queue. While it increases operational complexity and increases the risk of data loss, the Valkey implementation has been experimentally shown to offer up to 15x performance over Postgres under certain conditions.

### §1 — "Low-hanging fruit is the most nutritious"  *(figs 1 + 2)*
By design, the Honcho deriver processes representation work units only once they have crossed a certain threshold of pending cost. At time of writing, that pending-cost value is calculated by summing message token counts across the queue, grouped by work unit, once per second. This is an O(N) operation which scales on queue depth: as the backlog grows, the scan will dominate the scheduler, reaching ~2.5 s at 10⁷ pending tasks (Fig. 1).

The remedy is to stop recomputing the answer and start maintaining it. Our proposed methodology keeps each work unit's pending cost in a small aggregate table, updated incrementally on enqueue and acknowledgement, and gates eligibility with a partial index that the claim query reads directly. The scan becomes bounded by the number of eligible work units, with a consistent (small) cost from 10⁴ to 10⁷ pending tasks.

The change necessary to reap this benefit is bounded and surgical: roughly 150 lines across four files and a migration, with no new infrastructure (Fig. 2). It is a strict, low-risk improvement to the existing system, and is shippable in a matter of days.


### §2 — "Efficiency is (sometimes) worth the effort"  *(fig 3)*
While Postgres-as-queue is a durable pattern with guaranteed data retention at the OS layer, Postgres is optimized for transaction integrity, not raw throughput. A dedicated hot-queue system, such as Valkey, offers significantly improved performance under load. However, that increase in performance comes at a cost: increased operational complexity, and increased risk of data loss via the fsync window.

Whether or not Honcho should adopt a Valkey-based queue is an open question with a nuanced answer. When tasks are light-weight, and workers execute quickly, Valkey offers a 15x increase in throughput per primary, and as much as a 26x performance improvement over Postgres at equal shard counts under peak load. However, Valkey's lead shrinks to near parity (~1.1x) as per-task time-to-process reaches 200 ms.

The precise point at which Valkey fails to outperform Postgres is a function of both worker count and time-to-process (Fig. 3). Whether the performance gap is sufficient to justify a Valkey backend is a function of those variables plus stakeholder judgement, highly sensitive to business conditions and production metrics, and therefore outside the scope of this paper.

### §3 — "Methods"
Our lookahead performance tests were run using near-identical queues, differing only by a single SQL query, isolating the variance to algorithmic complexity. The tests were carried out locally, in a Docker container running on a 2.6 GHz MacBook Pro. While reproductions of this test (under tests/bench) may result in different absolute numbers depending on available resources, we expect a similar relative performance gap to persist.

Our head-to-head comparison test ran two near-identical Docker images implementing a shared queue contract against a common loadgen module on an AWS architecture specified under the deploy/terraform package. Zipfian key distribution in the loadgen simulated realistic usage patterns by continuously creating and draining keys, while correctness against system specifications was established deterministically by the shared tests/conformance package.

Figure 3 interpolates four per-task work levels (0, 2, 20, 200 ms) across worker counts from 1 to 1,000; Postgres points that ran producer-bound are reported as conservative lower bounds. The total experiment ran in ~90 minutes, for cloud spend < $10.

### Endnotes band  *(dense small-type; select at layout — candidate pool)*
> **Reproducible:** `make load-test` clones and reproduces the numbers; environment
> pinned. **Cloud:** all runs torn down via Terraform, < $10 total. **Backend
> second choice:** Postgres — and the *first* choice for Stage 1 (§1). **Flush
> policy:** a per-head age cap — every task is promoted when *it* becomes the
> oldest and ages out, so one slow task cannot strand the work behind it.
> **Fairness:** a per-tenant in-flight cap plus a weighted claim order is the
> one-seam extension (designed, not built). **Valkey on less iron:** each Valkey
> primary was benchmarked on the *same* box class as Postgres though it is
> single-threaded and cannot use it — so the gap is a conservative floor; the real
> cost-per-throughput difference is wider. **Stage-2 hardening:** a standalone
> reaper service, versioned migrations, and a managed durable Valkey tier.
> **Sharpening Fig 3:** 5/50/100 ms work-points and the durability
> {off/everysec/always} curve (deferred) would resolve the crossover precisely.

---

## Tradeoffs

- **LaTeX vs HTML/CSS.** *Decided:* LaTeX — the "this paper proposes…" research
  register is the Plastic-Labs key, and the old risk (custom plotting fighting
  pgfplots) is neutralized by rendering charts to image *outside* LaTeX and
  embedding them.
- **Fig 2 against Honcho's real code vs our own distilled patch.** *Chosen:* their
  real code — the find (their `get_and_claim_work_units` is our naive baseline)
  makes the cited diff far stronger, and recovers the M-PR "adopt-it" signal we
  cancelled, without the PR. *Cost:* the honest line count rises to ~150 (new
  table) and scope narrows to `representation` work units — worth it; honesty here
  is the whole brand of the doc.
- **Two-part recommendation vs "go Valkey."** A blanket "migrate to Valkey"
  is louder but less defensible and contradicts the brief's own "Postgres has
  been fine" + "justify cost." The two-part rule (ship PG look-ahead now; move
  only the queue-bound plane to Valkey) reads more senior and is what the data
  actually supports. *Chosen:* two-part.
- **Manifold as 3-D surface vs 2-D contour.** 3-D *looks* impressive but prints
  worse (occlusion, axis legibility). 2-D filled-contour + traced crossover line
  reads in seconds. *Lean:* 2-D, A/B the 3-D in the SII loop. (See Open Q.)
- **Include the literal patch (Fig 2) vs prose-only.** The diff costs space but
  converts "small change" from claim to fact for a code-reading audience.
  *Chosen:* include, kept tight.
- **One hero vs two.** Per Phil: look-ahead is the *explicit* hero ("this chart
  solves your problem"); the manifold is the *secret* hero, placed as a
  supporting figure so it lands as "…oh, he solved the whole thing." *Chosen:*
  two, asymmetric billing.

## Open Questions

**Resolved this pass:**
- ~~Manifold dimensionality~~ → render **both** 2-D contour and 3-D surface, A/B in SII.
- ~~Repo link~~ → `github.com/philosaether/pl-takehome-technical`.
- ~~Fig 2 footprint~~ → **essence-excerpt** on the page (their GROUP-BY → the indexed
  lookup), full mapping in `honcho-fig2-source.md` + the repo.
- ~~Does §3 earn its space~~ → **yes, as "Methods"** (Phil's title); brief-graded
  operability/fairness/flush items go to the endnotes band.
- ~~Name/branding~~ → neutral; `philbas.com` in the masthead.

**Open — need Phil's call (the two honesty seams):**
1. **The user-growth numbers** in the abstract ("~200 → ~30,000 active users"). If
   these are real/defensible, keep them — they're a strong frame. If they're
   illustrative, soften to "from hundreds to tens of thousands" (no hard figures),
   because the page goes *to Honcho*, who know their own numbers. **Which is it?**
   - These are the exact numbers Vineeth gave me in our meeting last week
2. **The Honcho line-count / framing correction.** The find upgrades Fig 2 but
   moves the number: Stage 1 is **~150 lines + a new (small) `work_units` table**,
   not "~40 lines altering the existing queue." And `~1000×` is the *complexity*
   win their hot path grows into (our measurement), not a benchmark of their prod.
   I've written the body copy to that honest framing — **confirm you're good with
   the abstract's `[N]` → ~150 and "new aggregate table" wording**, since it
   slightly softens "minimal alteration." (Still no new infra; still surgical.)
   - Incorporated

## Out of Scope

- **The email itself** — Phil writes it.
- **The actual one-pager content** — this doc plans it; we build it after /ship.
- **New data runs** — we render from existing `results/`. The 8-shard + durability
  rerun stays a flagged enhancement (endnote material, not a blocker).
- **M-PR (Honcho fork PR)** — cancelled this session; demo prep is tomorrow.
