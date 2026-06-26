# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **M0 scaffold — building now** on `feature/scaffold` (design accepted via /ship,
  `designs/scaffold.md`). The apples-to-apples contract: `cmd/plq`
  (`worker|loadgen|reap`), the 8-method `queue.Backend` interface, shared
  `worker.go` loop (heartbeat + configurable process model), `internal/config`,
  in-memory default backend, both path Dockerfiles + compose, Makefile, README
  skeleton. Build tags select the driver (default=memory).
  - **First step:** Go installed (brew); next `go mod init
    github.com/philosaether/pl-takehome-technical`.
  - **Isolation/fairness resolved** (2026-06-26): single shared queue, `workspace`
    is the seam; per-tenant fairness is *explained, not built* (A1 on both designs).
  - Remote now exists: `origin` = `github.com/philosaether/pl-takehome-technical`
    (private). `main` + `feature/scaffold` pushed.
  - New backlog artifact: `.meta/enhancements.md` (flagged-improvement bullets for
    the 1-page brief, curated at M4).

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
