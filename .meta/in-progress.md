# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **Next slice — M4: the 1-page writeup** (data viz + prose). Best done in a fresh
  session: `/hello` reloads the results buckets + `talking-points.md` + the designs,
  then data-viz polish on the run-cloud-1 / run-cloud-2 / lookahead charts + the
  voice pass on the brief. Audition framing (locked): *this month* (look-ahead
  1,364×) / *the future* (Valkey) / *how I know* (head-to-head + proofs). Hard cap:
  1 page. Source: `results/`, `talking-points.md`, `enhancements.md`, the design docs.
  - Then **M-PR**: the Honcho fork PR ("merge this for all of P1's wins" vs their
    real schema) — the biggest adopt-it signal (see `roadmap.md`).

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
