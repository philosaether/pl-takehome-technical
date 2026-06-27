# Designs Index

| Doc | Status | Date | Summary |
|-----|--------|------|---------|
| [postgres-work-unit-queue.md](postgres-work-unit-queue.md) | accepted · amended 2026-06-26 (A1) | 2026-06-25 | Path 1 — buffered, work-unit-aware queue on Postgres (the safe, defensible build). A1: shared-queue isolation + fairness grain |
| [valkey-work-unit-queue.md](valkey-work-unit-queue.md) | accepted · amended 2026-06-26 (A1) | 2026-06-25 | Path 2 — same queue on Valkey/Redis (Streams+ZSET+Lua); the upgrade to justify on throughput+latency. A1: isolation + fairness + shard by workspace |
| [scaffold.md](scaffold.md) | accepted | 2026-06-26 | M0 — Go module, the Backend driver interface (apples-to-apples contract), shared worker loop + loadgen, per-path images, config, make load-test |
| [postgres-driver.md](postgres-driver.md) | accepted | 2026-06-26 | M1 — internal/postgres build design: schema + per-method SQL + pgx wiring against the Backend contract; conformance suite vs the oracle |
| [valkey-driver.md](valkey-driver.md) | accepted · implemented 2026-06-26 | 2026-06-26 | M3 — internal/valkey build design: keyspace + per-method Lua + rueidis wiring against the Backend contract; conformance vs the oracle; the throughput/latency head-to-head overlay |
| [loadgen-and-proofs.md](loadgen-and-proofs.md) | accepted | 2026-06-26 | M2 — Zipfian churn loadgen, worker sweep + throughput graph, the 4 proofs (incl. look-ahead vs naive SUM…GROUP BY at 10⁶), process-model cutover sweep; Terraform harness |
| [ambitious-head-to-head.md](ambitious-head-to-head.md) | accepted | 2026-06-27 | run-cloud-2 — isolated/saturated topology, **both** backends sharded 1/2/4/8 via one router (multi-DSN PG router), tuned-PG baseline, durability tradeoff, process sweep 0/2/20/200ms; 3 parallel tracks; $/throughput + p99 analysis |
| [one-pager-construction.md](one-pager-construction.md) | accepted | 2026-06-27 | M4 — construction plan for the 1-page PDF deliverable: structure, 3 figures (look-ahead hero / precise Honcho diff / the manifold), LaTeX, sample copy in Phil's voice. Two-stage migration + the queue-bound-vs-compute-bound decision rule |
| [honcho-fig2-source.md](honcho-fig2-source.md) | reference | 2026-06-27 | Fig-2 source — maps our look-ahead onto Honcho's *real* code (`get_and_claim_work_units` is our naive baseline); the precise ~150-line change + citations + candor (honest line count, complexity-win framing, representation-only scope) |
