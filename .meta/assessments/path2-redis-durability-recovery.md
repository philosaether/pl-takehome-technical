# Assessment: Path 2 — Redis/Valkey Durability & Recovery
Date: 2026-06-25
Branch: meta/queue-backend-scoping
Scope: Honest accounting of what production-grade durability actually costs with
Redis/Valkey as a queue backend — so we can defend or reject it on the *config
we'd actually run*, not a stale blanket claim. Written to be learned from.
Feeds: Path 1's Redis-migration cutover (durability is the thing that gates it).

## TL;DR

"Redis isn't durable" was true for self-hosted OSS Redis through ~2023. Three
things moved the line since: **`WAITAOF`** (Redis 7.2, 2023 — replica-fsync
acks), **AWS MemoryDB** (durable multi-AZ log), and — **as of June 2026** — a
**durable multi-AZ option for ElastiCache for Valkey** with a *synchronous*
mode advertising zero data loss on failover. So the honest statement is:

> Self-hosted OSS Redis/Valkey still has a real, **non-zero, lag-bounded,
> not-consensus-safe** write-loss window on failover. Managed durable variants
> have largely closed it, at a ~2–3× cost premium and added write latency.

For an **at-least-once** queue (where redelivery is acceptable), self-hosted
Valkey at a sane config is *fine*. For a zero-loss ledger, you pay for the
managed durable tier. That trade is the whole decision.

## How Redis persistence actually works (the part to learn)

Two mechanisms, often combined:

- **RDB** — periodic fork-and-dump point-in-time snapshots. Compact, fast
  restart, great for backups. Guarantee: you **lose everything since the last
  snapshot** (default `save 60 1000` ⇒ up to 60s). Not the durability story for
  a queue.
- **AOF (append-only file)** — logs every write command, replayed at startup.
  This is the durability-relevant mechanism. Knob is `appendfsync`:
  - `always` — fsync before replying. Loss window ≈ one write. **Throughput
    collapses to disk-fsync rate** — but Redis groups concurrent writes into one
    fsync ("group commit"), so under high concurrency it's less catastrophic
    than naive per-op math. Still: avoid for a high-throughput queue.
  - `everysec` (default) — fsync once/sec. **Loss window ≤ ~1s.** The sane queue
    default.
  - `no` — OS decides (~30s on Linux). Too loose.
- **Multi-Part AOF (≥7.0):** AOF is now a *base* file + *incremental* files +
  *manifest*, swapped atomically on rewrite (rewrite compacts the unbounded log).
- **Hybrid `aof-use-rdb-preamble` (default yes since 5.0):** the AOF base is
  written in compact RDB binary, then AOF commands for the tail. RDB-speed load +
  AOF-granular durability. (NB: "hybrid RDB+AOF" overloads two ideas — this
  *preamble inside the AOF*, vs running RDB snapshots *and* AOF together. Keep
  them distinct.)
- **Torn tail on unclean kill:** `aof-load-truncated yes` (default) discards a
  partial trailing command and loads anyway. Mid-file *corruption* (not just
  truncation) refuses to start — see recovery runbook.

## Replication & the consistency ceiling (the load-bearing point)

- **Async by default.** Master replies to the client, *then* propagates. If the
  master dies before a replica has the write, and that replica is promoted, the
  acknowledged write is **lost forever**. This is the core hazard.
- **`WAIT numreplicas timeout`** blocks until prior writes are *received* by N
  replicas. Crucial: it **does not make Redis strongly consistent** — it cannot
  roll back a write that didn't replicate; it only *informs* you. The write
  already executed on the master. antirez's own loss scenario: a write reaches 2
  replicas, master crashes before the *next* write propagates, a *different*
  replica is elected → divergence.
- **`WAITAOF numlocal numreplicas timeout`** (Redis 7.2+) is the missing piece:
  blocks until writes are **fsynced to AOF** locally and/or on N replicas. WAIT
  proves *in-memory receipt*; WAITAOF proves *durable on disk*. Same caveat
  though — "unless waiting for all members to fsync, data can still be lost
  during a failover or restart. However, WAITAOF does improve real-world data
  safety."
- **Failover loss is the same story under both Sentinel and Cluster:** async
  replication ⇒ a lag-bounded window of acknowledged writes can vanish on
  promotion. Neither gives Raft/Paxos committed-write safety. Mitigation (not
  cure): `min-replicas-to-write N` + `min-replicas-max-lag S` make the master
  *refuse writes* unless N replicas are caught up — caps the loss window and
  stops an isolated minority master accepting doomed writes, trading
  availability.

**The honest line for the writeup:** the loss window for an individual
acknowledged write under default async replication is bounded by replication lag
(sub-ms to seconds under load). It is **not zero and not consensus-bounded.**

## Redis Streams durability (this is what our queue would use)

Our Redis design = **Streams** for per-key FIFO + consumer groups for
claim/ack, plus a **ZSET** for the maintained eligibility index. So Stream
durability is what matters:

- **Consumer groups + the PEL are first-class persisted data.** When
  `XREADGROUP` delivers a message it creates a pending entry in the group's
  **Pending Entries List (PEL)** recording stream ID, owning consumer, delivery
  time, delivery count — and it stays until `XACK`. Group membership,
  last-delivered-ID, and the PEL are **serialized in RDB and AOF**, so they
  survive a crash + recovery. A restarted consumer resumes its in-flight work
  with `XREADGROUP ... 0`.
- **What's lost on unclean shutdown:** nothing Stream-special — just the trailing
  `appendfsync` window (≤~1s at `everysec`) of `XADD`s *and* `XACK`s. **Risk
  asymmetry favors us:** losing an `XACK` → message redelivers (benign for
  at-least-once); losing an `XADD` → the enqueue never happened (the real loss).
- **In-order redelivery after a consumer crash:** another consumer reclaims
  orphaned entries via `XAUTOCLAIM`/`XCLAIM` filtered by `min-idle-time`; claim
  resets idle time so two consumers can't both win the same entry. Reclaim
  re-delivers in PEL/stream-ID order, and `delivery_count` gives us retry caps /
  dead-letter routing for the **poison-task** failure mode.
  - **Caveat for strict per-key FIFO:** during the idle-timeout window a crashed
    consumer's in-flight message isn't yet reclaimable, so *strict* global
    in-order processing needs app-level sequencing — claim ordering is by stream
    ID. Our exclusive-per-key lease handles this, but it's a thing to prove, not
    assume.
- **2026 upgrade:** Redis **8.4** added `XREADGROUP ... CLAIM min-idle-time`,
  collapsing the old XPENDING+XCLAIM+XREADGROUP recovery loop into one round
  trip (~22× faster claim on large-PEL workloads). If we go Streams, target
  ≥8.4 — **but verify Valkey parity** (this is a Redis-8.4 feature; Valkey 9.0
  predates it).

## Redis vs Valkey in 2026 (operational currency — Phil's "I need to learn this")

- **The fork:** Mar 2024 Redis dropped BSD-3 for dual RSALv2/SSPLv1
  (source-available, not OSI-open). Days later the **Linux Foundation** launched
  **Valkey**, a BSD-3 fork from Redis 7.2.4, backed by AWS/Google/Oracle.
- **Twist (don't state "Redis is closed" — it's stale):** Redis **8** (May 2025)
  re-added an **AGPLv3** open-source option. So it's OSI-open again, but
  *copyleft* AGPL, not the permissive BSD it was. (Flag for primary-source check
  if load-bearing.)
- **Valkey state 2026:** current major **9.0** (Oct 2025) — hash-field
  expiration, atomic slot migration, ~40% more throughput vs 8.1, scales to
  ~2000 nodes. Strong momentum (≈50 companies, 5M+ Docker pulls in year one).
- **Operational implication for choosing today:** Valkey is the **default engine**
  on AWS ElastiCache/MemoryDB and Google Memorystore, ~20% cheaper, permissive
  BSD, no SSPL/AGPL legal review. **For a greenfield queue, Valkey is the
  lower-friction default**; pick Redis only for a Redis-8.x-only feature (e.g.
  `XREADGROUP CLAIM`). For *our take-home* (self-hosted, reproducible), Valkey
  via Docker is the clean call — and saying so signals current awareness.

## The managed durable tier (when "non-zero loss" isn't acceptable)

- **AWS MemoryDB** keeps the full dataset in memory **and** commits writes to a
  distributed **multi-AZ transactional log before acknowledging** → "data is not
  lost across failovers." Cost: microsecond reads, **single-digit-ms writes**
  (the durable-log commit), at roughly **2–3× ElastiCache per-GB**.
- **June 2026:** AWS added a **durable storage option to ElastiCache for Valkey**
  (9.0+) with a multi-AZ transactional log and a choice of **synchronous
  durability (zero loss, higher latency)** or **async (lower latency, up to
  ~10s of acked writes lost on failover)**. AWS now "recommends ElastiCache for
  new workloads," repositioning MemoryDB as niche. *This means zero-loss no
  longer strictly requires MemoryDB's premium.* (Most Q1-2026 comparison
  articles predate this and are now stale — cite the AWS primary source.)

## What an honest self-hosted durability setup looks like

Realistic config:
```
appendonly yes
appendfsync everysec
aof-use-rdb-preamble yes
# + at least one replica:
min-replicas-to-write 1
min-replicas-max-lag 10
# + on critical enqueues, in the client:
WAITAOF 1 1 <timeout_ms>     # local AOF fsync + 1 replica fsync
noeviction                    # full instance REJECTS enqueues, never silently drops queued work
```
- **Residual loss you still accept:** without WAITAOF, ≤~1s (everysec) + async
  replication lag on failover. With `WAITAOF 1 1`, near-zero for the waited
  write in the common case — **still not zero** (not consensus). Zero-loss ⇒
  managed durable tier.
- **Recovery time:** on restart, load RDB preamble (fast) then **replay the AOF
  tail by re-executing commands** (CPU-bound, *not* a memmap) — grows with
  dataset/tail size. No reliable "GB/s" figure exists; **budget and test it on
  our real dataset.**
- **Throughput tax:** `everysec` AOF is a modest bounded cost. The real tax is
  per-write `WAIT`/`WAITAOF` (turns an async ack into a semi-sync one — a
  network/disk round-trip of latency). Use it only on writes that must survive;
  batch where possible. Avoid `appendfsync always`.

## Failure-mode runbook (the operability story the screen wants)

- **Primary crash:** replica/Sentinel/Cluster promotes; accept the lag-bounded
  loss window; reconnect clients. Un-replicated trailing writes gone.
- **Promotion needs majority** (Sentinel quorum / Cluster master-vote majority):
  size ≥3 voting nodes across AZs. Cluster waits up to `2 × node-timeout`
  (min 2s) — that's the failover-detection floor.
- **Split-brain:** minority master's partition-window writes discarded on heal.
  Prevent with `min-replicas-to-write`/`-max-lag` + proper quorum.
- **AOF corruption:** truncated tail auto-loads; mid-file corruption refuses
  start. Runbook: `redis-check-aof` **without** `--fix` first to see the break
  point; back up; only then `--fix` (it truncates corruption→EOF, so early
  corruption = big loss; prefer restoring from a replica/RDB).
- **OOM/eviction:** eviction is itself a write (propagated DEL) — evicted queue
  entries are **permanently gone**. `fork()` for snapshots transiently ~doubles
  memory (COW) → can trigger OOM exactly during a snapshot. Mitigate: keep
  `maxmemory` well under host RAM, `vm.overcommit_memory=1`, and **`noeviction`
  for queue data** so a full instance rejects enqueues rather than dropping work.

## What this means for our decision

- Redis/Valkey is a **legitimate** backend for an at-least-once work queue. The
  durability objection is real but *bounded and configurable*, not
  disqualifying. Reject/defend the **specific config**, not the brand.
- Its **primitive fit is excellent** (Streams = in-order exclusive drain + PEL
  crash redelivery; ZSET = O(log n) maintained look-ahead). The cost is a more
  involved durability/recovery defense than Postgres's "it's just ACID."
- **For the take-home:** self-hosted **Valkey 8.1+ (or Redis 8.4 if we want
  `XREADGROUP CLAIM`)** via Docker, `everysec` + replica + selective `WAITAOF`,
  is the honest, reproducible, $0 config. The zero-loss story (managed durable
  tier) is the production upgrade we'd *name*, not run on a laptop.

## Open items / verify before publishing

1. Valkey parity for `XREADGROUP CLAIM` (Redis 8.4 feature). Affects whether we
   pin Redis or Valkey for the Streams design.
2. Redis-8 AGPL re-licensing detail (primary source).
3. ElastiCache-durable async loss figure ("up to ~10s" per AWS — verify current).
4. AOF replay time on *our* dataset size — measure in the load test, don't quote.

## Sources

Redis persistence: https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/ ·
WAIT: https://redis.io/docs/latest/commands/wait/ · WAITAOF:
https://redis.io/docs/latest/commands/waitaof/ · antirez on WAIT:
https://antirez.com/news/66 · Sentinel:
https://redis.io/docs/latest/operate/oss_and_stack/management/sentinel/ ·
Cluster spec:
https://redis.io/docs/latest/operate/oss_and_stack/reference/cluster-spec/ ·
Streams 8.4 CLAIM:
https://redis.io/blog/single-shot-reliable-consumers-with-xreadgroup-claim-in-redis-84/ ·
XAUTOCLAIM: https://redis.io/docs/latest/commands/xautoclaim/ · A Year of
Valkey: https://www.linuxfoundation.org/blog/a-year-of-valkey · Valkey 9.0:
https://www.linuxfoundation.org/press/valkey-9.0-delivers-performance-and-resiliency-for-real-time-workloads ·
MemoryDB FAQ: https://aws.amazon.com/memorydb/faqs/ · ElastiCache durability
(Jun 2026): https://aws.amazon.com/blogs/database/announcing-durability-for-amazon-elasticache/
