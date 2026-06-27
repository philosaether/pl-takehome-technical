# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **Demo prep (tomorrow, 2026-06-28)** — the 30-min screen-share rehearsal: run the
  load test live + the brief's answer set (backend + 2nd choice; eligibility cost +
  enqueue-path aggregate; the 3-of-10 crash trace; hot/wedged/stranded unit; fairness
  under simultaneous threshold-cross; runtime-T change; plateau + first bottleneck;
  multi-machine sharding; "10× the workers — what breaks first?"). Material: the
  designs, `results/`, `talking-points.md`, `one-pager/`. See M4 in `roadmap.md`.

## Shipped (all accepted + merged to `main`)

- **M4 — the 1-page one-pager** (`one-pager/one-pager.pdf`, merged 2026-06-27,
  `4aaaa24`). 1-page LaTeX PDF (tectonic), two-stage migration framing; 3 figures —
  look-ahead hero (1,364×), the cited diff vs Honcho's real `get_and_claim_work_units`,
  the workers×process manifold (2-D, linear interp). Design + Honcho mapping in
  `designs/one-pager-construction.md` + `honcho-fig2-source.md`. Reviewed (README
  refreshed, manifold honesty fix). **Ready to email Vineeth + repo invite.**
  M-PR cancelled.

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
