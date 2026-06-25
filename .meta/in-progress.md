# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **Buffered work-unit queue take-home.** Both backend designs **accepted**, no
  code yet (deliberate). Path 1 (Postgres, Go + pgx) and Path 2 (Valkey,
  Streams+ZSET+Lua, sharded) coexist: Path 1 = safe fallback, Path 2 = gated
  upgrade. Honcho-actual comparison done (we hold up / improve via maintained
  aggregate). The **head-to-head load test is the gate** that picks the build:
  Path 2 only if PG plateaus below target and/or the loop-latency gap is real.
  - **Next:** design work merging to `main`; planning a **deliverables roadmap**
    on `meta/deliverables-planning` before `/ttyl`. Build comes after the roadmap.
  - Deliverable shape: incremental commits, a load generator with numbers, a
    1-page writeup (+ distributed/sharding stretch).
  - Key artifacts: `designs/postgres-work-unit-queue.md` (accepted),
    `designs/valkey-work-unit-queue.md` (accepted),
    `assessments/honcho-actual-comparison.md`, `assessments/path2-redis-durability-recovery.md`.

## To Explore

## Parked
