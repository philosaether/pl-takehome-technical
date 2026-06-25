# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **Buffered work-unit queue take-home — design phase complete, build not started.**
  Both designs accepted (Path 1 Postgres, Path 2 Valkey) and **merged to `main`**.
  On branch `meta/deliverables-planning`; `roadmap.md` written and folded through
  one full iteration. No implementation code yet (deliberate).
  - **Next session starts at M0 (scaffold)** — see `roadmap.md` for the full plan
    (M0 scaffold → M1 Path 1 → M2 loadgen+proofs → M3 Path 2 head-to-head →
    M-PR Honcho fork PR → M4 writeup). Build both paths, Path 1 first.
  - **Framing locked:** the "audition" package — *this month* (look-ahead table) /
    *the future* (Valkey) / *how I know* (head-to-head + proofs). Maps to the
    1-page writeup.
  - **Open / to talk out first thing:** per-tenant isolation model (gates the
    sharding story). Parked deliberately for a thorough discussion.
  - Key artifacts: `roadmap.md`, `designs/postgres-work-unit-queue.md`,
    `designs/valkey-work-unit-queue.md`, `assessments/honcho-actual-comparison.md`,
    `assessments/path2-redis-durability-recovery.md`.

## To Explore

## Parked
