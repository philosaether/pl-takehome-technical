# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **Buffered work-unit queue take-home.** Branch `feature/postgres-queue`.
  Status: Path 1 (Postgres, Go + pgx) **design accepted**; **Honcho-actual
  comparison done — we hold up / improve** (maintained aggregate vs their
  per-poll SUM scan). No implementation code yet (deliberate).
  - **Next action:** `/draft` Path 2 (Valkey) — same depth as Path 1. Decision
    bar narrowed by the Honcho comparison: the look-ahead win is already banked
    in Postgres, so Path 2 must justify on **throughput ceiling + latency
    posture** only. The load test settles it ("show a PG ceiling Valkey clears").
  - Then: if Path 2 justified → build it; else build Path 1. Either way the
    deliverable wants incremental commits, a load generator with numbers, and a
    1-page writeup (+ distributed-extension stretch).
  - Key artifacts: `designs/postgres-work-unit-queue.md` (accepted),
    `assessments/honcho-actual-comparison.md`, `assessments/path2-redis-durability-recovery.md`.

## To Explore

## Parked
