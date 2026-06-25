# Assessment: Path 3 — Custom + WAL Engine
Date: 2026-06-25
Branch: meta/queue-backend-scoping
Scope: What a from-scratch durable queue engine would actually look like, and
*which bricks are available to build the house*. Answers Phil's "I have no idea
what it would look like; I'm not sure which bricks exist." Decision drilldown,
not a design — viability + honest effort + where it breaks.

## The key reframe: there are two houses, not one

Phil's instinct ("custom + WAL") blends two genuinely different builds:

- **House A — queue *on top of* an existing embeddable transactional engine**
  (RocksDB, Pebble, SQLite-WAL, redb). The engine gives you the WAL, crash
  recovery, and sorted/range iteration **for free**. You build *only* queue
  semantics. **This is what virtually all real "durable queue" prior art does.**
- **House B — custom engine on a *bare* WAL/log.** You own durability *and*
  indexing down to the bytes. Maximum control and throughput ceiling, far more
  to get subtly wrong.

For a take-home graded on *provable correctness + operability + reproducibility
in a bounded time budget*, **House A is the defensible "custom" answer** and
House B is the "if I had a month / for max throughput" frontier. Build B only
with a specific reason (custom on-disk layout, no-dependency constraint,
throughput past what an off-the-shelf engine gives).

## The bricks that already exist (so we don't rebuild them)

### Embeddable engines = WAL + recovery + ordered iteration, free

| Engine | Lang | Type | Ordered scan | Crash recovery | Maturity |
|---|---|---|---|---|---|
| **RocksDB** | C++ (FFI) | LSM | yes (prefix iters) | built-in WAL + manifest | the reference; most-deployed |
| **Pebble** | Go | LSM | yes | own WAL | powers every CockroachDB node |
| **badger v4** | Go | LSM (KV-separated) | yes (+ parallel Stream) | per-memtable WAL (since v2 rewrite) | backs Dgraph; past crash bugs fixed |
| **bbolt** | Go | COW B+tree | yes (cursors) | **no WAL** — atomic page-swap, "no recovery needed" | backs etcd; rock-solid, read-optimized |
| **redb** | Rust | COW B+tree | yes | **no WAL** (COW) | stable, committed file format; cleanest no-C Rust pick |
| **fjall** | Rust | LSM | yes | own WAL | younger (v3 2025), actively shipping |
| **sled** | Rust | log-structured | yes | own | **beta; 1.0 rewrite stalled — higher risk** |
| **LMDB / libmdbx** | C (FFI) | COW B+tree | yes | **no WAL** (COW) | extremely robust, tiny, single-writer |
| **SQLite (WAL mode)** | C (FFI) | B-tree + WAL | yes (SQL) | WAL + checkpoint | ubiquitous; fastest path to a prototype |

**Two framing corrections worth internalizing:**
1. **"Custom engine on a WAL" and "embeddable engine" are largely substitutes,
   not a stack.** If we pick RocksDB/Pebble we are *not* also hand-writing a WAL.
2. **bbolt / LMDB / redb have no WAL at all** — they're copy-on-write B-trees;
   durability comes from atomically swapping the page tree's root, so a crash
   just leaves the previous valid root. They're durable but don't fit a
   "WAL-backed" narrative. If the story is specifically *WAL*, that's
   LSM-engines (RocksDB/Pebble/badger/fjall) or a hand-rolled log.

### Off-the-shelf WAL/commit-log libraries (if we want House B without byte-bashing)

- **Go:** `tidwall/wal` (segmented, configurable sync; companion `raft-wal`),
  `rosedblabs/wal` (append-only, segment-based). So even House B doesn't require
  hand-writing record framing.
- **Rust:** people usually use an engine's internal WAL or hand-roll; recent
  userspace-WAL research exists but nothing canonical.

### Consensus, for the distributed stretch (do NOT hand-roll Raft)

- `hashicorp/raft` (Go — pluggable LogStore/StableStore, most ergonomic),
  `etcd/raft` (Go — minimal, battle-tested core, more assembly),
  `lni/dragonboat` (Go — high-throughput **multi-group** Raft, fits one
  group-per-shard), `tikv/raft-rs` + `openraft` (Rust).

## What the hand-rolled WAL actually looks like (House B mechanics)

This is the "what does the house look like" Phil asked for. The canonical design:

1. **Append-only segment files.** The log is fixed-size segments (Kafka rolls at
   1 GB; typical DBs 16–32 MB; tidwall default 20 MB). Only the *active* tail
   segment is written; closed segments immutable. Sequential writes are fast and
   crash-friendly (only the tail can tear).
2. **Record framing + checksums.** Each record = `[length][CRC-32C][payload]`
   (often a `[type]` tag for full/first/middle/last fragments). CRC32C
   (hardware-accelerated) lets recovery detect a torn/partial tail and stop there.
3. **fsync discipline.** `write()` to page cache, then `fsync`/`fdatasync`
   **before acking the commit** — that's what makes it durable.
4. **Group commit** — batch many pending appends into **one fsync**. This is the
   single biggest throughput lever ("10k+ commits/sec despite ms-scale fsync").
5. **Recovery replay** — on startup scan from last checkpoint, validate CRCs,
   replay into in-memory state, **truncate the torn tail**.
6. **Checkpoint + truncation** — periodically snapshot materialized state so
   replay only covers records *after* the checkpoint; then delete/compact old
   segments.

### The fsync primitives underneath (the throughput reality)

- **`fdatasync` ≈ 2× faster than `fsync`** (skips timestamp/metadata flush);
  append-heavy WALs prefer it.
- **`O_SYNC`/`O_DSYNC` per-write benchmark *worse* (>2× slower)** than explicit
  write-then-fsync because you lose batching. So: buffer + group `fsync`, don't
  open the file sync.
- **macOS trap:** plain `fsync()` does **not** force the drive cache — you need
  `fcntl(F_FULLFSYNC)` for true durability (same reason SQLite needs
  `fullfsync=1` on macOS). Real portability footgun for laptop reproducibility.
- **io_uring (2026):** great for async read/write, **not yet a clean win for the
  durable *commit*** — Postgres deliberately calls fsync *outside* io_uring;
  only NVMe passthrough with explicit flush is truly-async durable. Don't sell
  io_uring as the durability story.

## What you build yourself either way (no off-the-shelf brick)

This is the honest effort estimate — the engine gives storage, **none of this**:

1. **Per-key FIFO over tens of thousands of live keys.** Encode keys as
   `work_unit_key | seq` and lean on the engine's *sorted iteration* for per-key
   order + cheap range scans. (This is the one piece the engine helps with.) You
   own key lifecycle (born/drained ad hoc).
2. **Maintained per-key cost aggregate + eligible-key index.** No KV/LSM
   maintains "sum of pending cost per key" or an "aggregate ≥ T AND unclaimed"
   secondary index. You update it **transactionally in the same atomic batch** as
   the data on every enqueue/claim/ack — a hand-rolled materialized view.
3. **Lease-based exclusive claim + visibility timeout.** Claim sets exclusive
   lock + lease expiry; on expiry/crash the unit returns to the front of the
   order. You implement lease records, expiry sweeping, extension.
4. **In-order redelivery + in-flight claim recovery — the genuinely hard part.**
   The subtle trap: **delivery/redelivery count must be persisted *before*
   delivery**, else after a crash you redeliver with `redelivered=false` when it
   should be true. Claim state must be in the WAL; recovery must re-derive which
   leases were live and re-queue them at the head, preserving per-key order.
   *This is exactly the live ordering-under-crash scenario the screen drills on
   — and in House B we'd be proving it against our own recovery code.*

## Distributed extension — the natural story is sharding, not consensus

Because our ordering guarantee is **per work-unit**, the clean scale-out is:

> Run **N independent single-node WAL engines**, route each `work_unit_key` to
> one shard by hash, thin coordinator for shard assignment/failover. **No
> consensus on the hot path** — per-key FIFO holds because a key lives on exactly
> one shard.

This is how Kafka (partitions) actually scales. You only need Raft *per shard*
if each shard must survive node loss — and then it's `dragonboat`'s multi-group
model (one Raft group per shard), not one global log. **Lead with sharding; name
per-shard Raft as the HA upgrade.** Far less risk than a global replicated log.

## Closest prior art to read (or reuse)

- **SQLite-based queues** — `litements/litequeue`, `khepin/liteq`, and recent
  "SQS visibility-timeout/lease model on SQLite" write-ups. **Lowest-effort path
  to a working durable prototype** — directly models our lease/redelivery need.
- **RocksDB-backed queues** — `ryanpbrewster/rust-dq`, `artiship/rocks-queue-java`:
  the "encode FIFO into sorted keys, let the LSM handle WAL+iteration" pattern,
  exactly House A.
- **NATS JetStream file store** — production "durable log with subjects(≈keys) +
  consumers(≈leases)"; ~39 bytes overhead for a 5-byte msg; read the
  `nats-server` filestore source.
- **Kafka log-segment design** — the canonical append-only log (rolling
  segments, sparse indexes, tail-truncation recovery). Best single study for the
  WAL mechanics above.

## Viability verdict for the take-home

- **House A (queue-on-RocksDB/Pebble, Go) or queue-on-SQLite-WAL** is *viable
  and defensible* in the time budget. SQLite-WAL is the fastest route to a
  provable prototype; Pebble/RocksDB is the higher-throughput "real engine"
  answer. Either lets us own the queue semantics (aggregate, eligibility index,
  lease, in-order recovery) — the interesting part — without owning durability.
- **House B (bare WAL)** is the frontier: most impressive walkthrough, highest
  throughput ceiling, **but the in-order-recovery proof is now against our own
  code** — high-variance for a screen. Frame as "where I'd go for max
  throughput; here's the design," not the thing we ship and prove.
- **Distributed:** shard-by-key (no hot-path consensus) is the strong,
  low-risk story; per-shard Raft via `dragonboat` is the HA upgrade.
- **Language flag (Phil's path-5 note):** House A's brick choice forces a
  language — Go (Pebble/badger/bbolt + hashicorp/raft + dragonboat is the
  richest, most coherent ecosystem here) vs Rust (redb/fjall/rocksdb, openraft)
  vs the SQLite-FFI-from-anything prototype. **If this path advances, a
  language-pick drilldown comes next**, as Phil anticipated.

## Recommended framing if we pick this path

Build **House A on Pebble or SQLite-WAL**, own the four bespoke pieces above,
shard-by-key for scale, name House B + per-shard Raft as the frontier. That's a
"custom engine" story that's honest, buildable, and provable — without
pretending we reimplemented RocksDB or Raft in a week.

## Sources

bbolt: https://pkg.go.dev/go.etcd.io/bbolt · badger v4:
https://pkg.go.dev/github.com/dgraph-io/badger/v4 (WAL rewrite PR
https://github.com/dgraph-io/badger/pull/1555) · Pebble:
https://www.cockroachlabs.com/blog/pebble-rocksdb-kv-store · redb:
https://github.com/cberner/redb · fjall:
https://fjall-rs.github.io/post/fjall-3/ · sled:
https://github.com/spacejam/sled · LMDB: http://www.lmdb.tech/doc/ · libmdbx:
https://github.com/erigontech/libmdbx · SQLite WAL: https://sqlite.org/wal.html ·
SQLite fsync/macOS: https://avi.im/blag/2025/sqlite-fsync/ · tidwall/wal:
https://pkg.go.dev/github.com/tidwall/wal · rosedblabs/wal:
https://github.com/rosedblabs/wal · disk IO primitives:
https://transactional.blog/how-to-learn/disk-io · io_uring durability study:
https://arxiv.org/html/2512.04859v1 · group commit:
https://ayende.com/blog/174565/ · hashicorp raft:
https://www.hashicorp.com/en/resources/distributed-consensus-hashicorp-raft ·
dragonboat: https://github.com/lni/dragonboat · tikv raft-rs:
https://tikv.org/blog/implement-raft-in-rust/ · litequeue:
https://github.com/litements/litequeue · liteq: https://github.com/khepin/liteq ·
rust-dq: https://github.com/ryanpbrewster/rust-dq · JetStream filestore:
https://docs.nats.io/using-nats/developer/develop_jetstream/model_deep_dive ·
Kafka storage: https://www.conduktor.io/blog/understanding-kafka-s-internal-storage-and-log-retention
