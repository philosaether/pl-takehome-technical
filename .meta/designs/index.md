# Designs Index

| Doc | Status | Date | Summary |
|-----|--------|------|---------|
| [postgres-work-unit-queue.md](postgres-work-unit-queue.md) | accepted · amended 2026-06-26 (A1) | 2026-06-25 | Path 1 — buffered, work-unit-aware queue on Postgres (the safe, defensible build). A1: shared-queue isolation + fairness grain |
| [valkey-work-unit-queue.md](valkey-work-unit-queue.md) | accepted · amended 2026-06-26 (A1) | 2026-06-25 | Path 2 — same queue on Valkey/Redis (Streams+ZSET+Lua); the upgrade to justify on throughput+latency. A1: isolation + fairness + shard by workspace |
| [scaffold.md](scaffold.md) | accepted | 2026-06-26 | M0 — Go module, the Backend driver interface (apples-to-apples contract), shared worker loop + loadgen, per-path images, config, make load-test |
| [postgres-driver.md](postgres-driver.md) | accepted | 2026-06-26 | M1 — internal/postgres build design: schema + per-method SQL + pgx wiring against the Backend contract; conformance suite vs the oracle |
| [valkey-driver.md](valkey-driver.md) | accepted · implemented 2026-06-26 | 2026-06-26 | M3 — internal/valkey build design: keyspace + per-method Lua + rueidis wiring against the Backend contract; conformance vs the oracle; the throughput/latency head-to-head overlay |
| [loadgen-and-proofs.md](loadgen-and-proofs.md) | accepted | 2026-06-26 | M2 — Zipfian churn loadgen, worker sweep + throughput graph, the 4 proofs (incl. look-ahead vs naive SUM…GROUP BY at 10⁶), process-model cutover sweep; Terraform harness |
