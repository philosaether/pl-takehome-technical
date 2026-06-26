# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **M1 Postgres driver — building** on `feature/postgres-queue` (design accepted
  via /ship, `designs/postgres-driver.md`). `internal/postgres` against the
  `queue.Backend` contract: embedded `schema.sql`, per-method SQL (self-validating
  ack CTE, `SKIP LOCKED` claim, two-UPDATE reaper, poison→DLQ), pgx pool + tenant
  cache. Plus: **align the oracle to per-head flush** (drop sticky `flushed` from
  `internal/memory`), and a **conformance suite** (`RunConformance(t, factory)`)
  run vs memory (always) + postgres (gated by `PLQ_TEST_POSTGRES`).
  - `pgx/v5` added (go directive → 1.25; Dockerfiles bumped to match).
  - Composite key `(ws,sess,peer)`; `lease_token`/`max_wait_ms` schema adds.
  - Interesting decisions → `talking-points.md` (curate at M4).

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
