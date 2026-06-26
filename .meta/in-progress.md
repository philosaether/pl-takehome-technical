# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **M1 Postgres driver — drafting** on `feature/postgres-queue`. Flesh out
  `internal/postgres` against the accepted `queue.Backend` contract: schema
  (`work_units`, HASH-partitioned `tasks`, `dead_letters`, `tenant_config`,
  `wu_claimable` partial index), maintained `pending_cost` aggregate on enqueue
  (the I3 look-ahead win), `FOR UPDATE SKIP LOCKED` claim, in-order drain +
  delete-on-ack, reaper (flush-flip + lease reclaim), poison→DLQ. Add `pgx` to
  `go.mod`. Mirrors `designs/postgres-work-unit-queue.md` (+A1).
  - The in-memory oracle is the reference the PG driver must match.

- **M0 scaffold — DONE, reviewed, merged to `main`** (`feature/scaffold`,
  `designs/scaffold.md` accepted + reconciled). The apples-to-apples contract is
  live: `cmd/plq` (`worker|loadgen|reap`), the 8-method `queue.Backend`, shared
  `worker.go` loop (heartbeat + configurable process model), `internal/config`,
  the in-memory backend (full working oracle), both Dockerfiles + compose,
  Makefile, README. Review fixes applied (lease-renew on ack-keep, worker backoff,
  recompute helper). `logical-architecture.md` written.
  - **Isolation/fairness resolved** (2026-06-26): single shared queue, `workspace`
    is the seam; per-tenant fairness is *explained, not built* (A1 on both designs).
  - `origin` = `github.com/philosaether/pl-takehome-technical` (private); `main`
    current. New backlog artifact: `.meta/enhancements.md` (curated at M4).

- **Take-home overall — design phase done, both backends accepted + merged to `main`.**
  Plan in `roadmap.md` (M0 scaffold → M1 Postgres → M2 loadgen+proofs → M3 Valkey
  head-to-head → M-PR Honcho fork PR → M4 writeup); build both paths, Path 1 first.
  - **Framing locked:** the "audition" package — *this month* (look-ahead table) /
    *the future* (Valkey) / *how I know* (head-to-head + proofs).
  - Key artifacts: `roadmap.md`, `designs/postgres-work-unit-queue.md`,
    `designs/valkey-work-unit-queue.md`, `designs/scaffold.md`,
    `assessments/honcho-actual-comparison.md`,
    `assessments/path2-redis-durability-recovery.md`.

## To Explore

## Parked
