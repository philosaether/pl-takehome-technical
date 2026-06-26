# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **M2 loadgen + the four proofs — drafting** on `feature/loadgen`. Real
  producers (Zipfian `wu_key` churn, pinned RNG seed, configurable cost dist),
  crash injection, worker sweep 1/10/100/1000, metrics export + throughput-vs-
  workers graph, and the 4 deterministic proofs (ordering-under-crash, gate,
  flush, look-ahead cost vs naive `SUM…GROUP BY`). Runs on the pinned cloud box
  ($20 cap). The process-model sweep (zero|cost) locates the PG→Valkey cutover.
  - **The deferred PG efficiency items get measured here** (Drain 2→1, Enqueue
    tenant-cache miss) — optimize only if they compound; before/after = talking-point.

- **M1 Postgres driver — DONE, reviewed, merged to `main`** (`feature/postgres-queue`).
  All 8 `queue.Backend` methods over Postgres; conformance 8/8 vs live postgres:16;
  oracle aligned to per-head flush. Designs `postgres-driver.md` (accepted+reconciled).
  - `pgx/v5` added (go directive → 1.25). Interesting decisions → `talking-points.md`.

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
