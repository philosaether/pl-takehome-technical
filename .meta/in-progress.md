# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **M4: the 1-page PDF one-pager** (`feature/one-pager`, started 2026-06-27). Design
  **accepted + building** → `designs/one-pager-construction.md` (+ `honcho-fig2-source.md`).
  Deliverable: a 1-page LaTeX PDF for Phil to email Vineeth + a repo invite. Locked: LaTeX;
  two-stage migration framing (growth spine); 3 figures — Fig 1 look-ahead hero (1,364×),
  Fig 2 the cited diff vs Honcho's real `get_and_claim_work_units`, Fig 3 the workers×process
  manifold (2-D + 3-D, A/B in SII). Copy in Phil's voice (figs+titles his, body collaborative;
  current sample copy in the design doc). Hard cap: 1 page.
  - **Build steps:** (1) redraw the 3 figures from `results/` to deliverable-grade (one
    palette/typeface); (2) LaTeX scaffold (masthead + 2-col body + endnotes band); (3) compile
    → SII loop. Then Phil's voice/layout polish pass.

## Cancelled

- **M-PR (Honcho fork PR)** — cancelled 2026-06-27. Tomorrow is demo prep instead.

## Shipped (all accepted + merged to `main`)

- **M0** scaffold · **M1** Postgres driver · **M2** loadgen + proofs (look-ahead
  **1,364×** at 10⁷) · **M3** Valkey driver · **M3 head-to-head** (run-cloud-1, AWS
  m5.xlarge: PG plateaus ~1.7k vs Valkey 26k→49k→92k over 1/2/4 shards, ~53× at 4;
  `results/run-cloud-1/`).
- **run-cloud-2** (ambitious head-to-head, `feature/ambitious-head-to-head`, merged
  2026-06-27): multi-DSN PG router (shard PG like Valkey) + tuned-PG baseline +
  isolated/saturated topology + `scripts/cloud/` orchestration + plot faceting. Ran
  **quota-constrained** (AWS 32-vCPU → m5.large / 4 shards): PG ×1/2/4 = 2.2k/3.7k/6.5k,
  tuned ~10k, valkey ×1/2/4 = 33k/70k/142k — **both shard ~linearly; Valkey ~15× per
  primary** (one Valkey ≈ 5 sharded PG; even tuned PG loses 3×). `results/run-cloud-2/`.
  7 deployment bugs fixed. Caveats: PG points are producer-bound lower bounds;
  durability dimension failed (now fixed for rerun).

## To Explore

## Parked

## Roadmap pointers (not active)

- **Enhancement flagged** (`enhancements.md`): the full 8-shard + durability run —
  needs an AWS vCPU quota bump, then `make cloud-up` with `TF_VAR_pg_count=8
  TF_VAR_valkey_count=8` + m5.xlarge/m5.2xlarge. Code already supports it.
