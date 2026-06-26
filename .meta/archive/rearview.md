# Rearview — Completed Items

Completed work, archived with a date + branch stamp. Append-forward.

---

## M0 — Scaffold (the apples-to-apples contract)

**Completed: 2026-06-26 (feature/scaffold → main)**

Go module + the `queue.Backend` 8-method contract, the shared worker loop
(heartbeat + configurable process model), the full in-memory backend as
correctness oracle, build-tag single-driver selection (default=memory),
`internal/config`, the `cmd/plq` CLI (`worker|loadgen|reap`), both path
Dockerfiles + compose, Makefile, README, and `logical-architecture.md`. M0 smoke
proofs (in-order drain, gate) pass. Reviewed (lease-renew on ack-keep, worker
error backoff, recompute helper) before merge. Design: `designs/scaffold.md`
(accepted + reconciled). Resolved roadmap M0.

## M1 — Postgres driver (Path 1)

**Completed: 2026-06-26 (feature/postgres-queue → main)**

`internal/postgres` implements all 8 `queue.Backend` methods over Postgres:
embedded idempotent schema (work_units + HASH-partitioned tasks + dead_letters +
tenant_config + partial indexes), the maintained-aggregate Enqueue CTE (the I3
look-ahead win), `FOR UPDATE SKIP LOCKED` claim, self-validating Ack CTE with
per-head flush, head-guarded poison→DLQ, two-UPDATE reaper, lease_token (ABA-safe)
+ tenant cache. Aligned the in-memory oracle to per-head flush (dropped sticky
`flushed`). New `internal/conformance` suite (8 scenarios) runs vs memory (always)
+ postgres (gated by `PLQ_TEST_POSTGRES`) — verified 8/8 against live postgres:16;
full loadgen→worker stack drained 400 tasks to 0. Reviewed (B1 Fail head-guard,
I1 Ack keep-CTE). Designs: `postgres-driver.md` (accepted+reconciled), implements
`postgres-work-unit-queue.md`. pgx/v5 added. Resolved roadmap M1.
