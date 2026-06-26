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
