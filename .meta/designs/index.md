# Designs Index

| Doc | Status | Date | Summary |
|-----|--------|------|---------|
| [postgres-work-unit-queue.md](postgres-work-unit-queue.md) | accepted | 2026-06-25 | Path 1 — buffered, work-unit-aware queue on Postgres (the safe, defensible build) |
| [valkey-work-unit-queue.md](valkey-work-unit-queue.md) | accepted | 2026-06-25 | Path 2 — same queue on Valkey/Redis (Streams+ZSET+Lua); the upgrade to justify on throughput+latency |
