---
Status: accepted
Date: 2026-06-26
Accepted: 2026-06-26
Implemented: 2026-06-26 (feature/valkey)
Assessment: ../assessments/path2-redis-durability-recovery.md
Implements: valkey-work-unit-queue.md (accepted Path 2 + A1), against the M0 queue.Backend contract
Mirrors: postgres-driver.md (the M1 build-design shape)
Roadmap: ../roadmap.md (M3)
Divergences: seq is 0-based (HINCRBY next_seq − 1) to match the oracle/PG (not the doc's raw HINCRBY); XAUTOCLAIM min-idle=0 not ≥lease (the lease, not PEL idle time, is the exclusivity gate — required for the reclaim path under wall-clock; Drain's lease check is best-effort fail-fast, Ack's token re-check is the real boundary); attempts maintained explicitly on the hash (oracle-exact) rather than read from PEL delivery_count (which counts deliveries, incl. reclaim-redeliveries, not logical failures); tombstone via DEL (immediate) not EXPIRE/TTL — matches the oracle's immediate delete + avoids carrying stale next_seq/consumer-group state into a revival (forfeits the design's "free tombstone reaping" ergonomic, which was never load-bearing); keys referenced by literal/constructed name (not KEYS[]) — fine for the standalone-per-shard test topology, a Cluster deploy needs per-shard hash tags. Conformance 8/8 + ordering-under-crash + large-PEL paged-ack green vs live valkey/valkey:8.1.
Deferred: the canonical cloud head-to-head numbers (reuses M2's gated AWS sweep); cached seq→ID map (PEL scan measured-fine at batch sizes); Redis Cluster deploy.
---

# Valkey Driver (M3) — Build Design

Flesh out `internal/valkey` so it satisfies the accepted `queue.Backend` contract
(the 8 methods) over Valkey, mirroring
[`valkey-work-unit-queue.md`](valkey-work-unit-queue.md). Concrete keyspace + Lua +
`rueidis` wiring — Phil implements from this. **The in-memory backend is the
reference:** the same conformance suite that pins Postgres now runs against Valkey
and asserts identical observable behavior. The payoff is the **head-to-head**: the
backend-agnostic `loadrun` harness points at Valkey, and its curves overlay the M2
Postgres throughput/latency charts (proof #4 — the decision gate itself).

---

## Contract → Valkey at a glance

| `queue.Backend` method | Valkey realization |
|---|---|
| `Enqueue` | one **enqueue Lua**: `XADD s:{k}` + `HINCRBY pending_cost` + (re)start flush clock + index into `eligible`/`pending_flush` ZSET. Returns our own `seq` (see §seq). |
| `Claim` | one **claim Lua** per shard: `ZPOPMIN eligible` (age-fair) → set `claimed_by`/`lease_token`/`lease_expires` on the hash → `ZADD leases`. Round-robin shards until a hit or all empty; `(nil,nil)` when none. |
| `Drain` | lease-validate (HGET token), then `XREADGROUP GROUP g <me> COUNT batch STREAMS s:{k} >` → `[]Task` in stream-ID (= seq) order. |
| `Ack` | one **ack Lua**: scan this consumer's PEL, `XACK`+`XDEL` entries with `seq ≤ through`, `HINCRBY -acked_cost`, recompute head age + flush_deadline, keep-or-release (re-index or tombstone via `EXPIRE`). |
| `Release` | lease-validate, clear `claimed_by`/`lease_token`, `ZREM leases`, re-index unit into `eligible`/`pending_flush`. |
| `Heartbeat` | lease-validate, `HSET lease_expires`, `ZADD leases <new>` (push the reaper out). |
| `Fail` | bump `delivery_count` (read from PEL); at `MaxAttempts` `XACK`+`XDEL` head → `XADD dlq`, `HINCRBY -cost`; always release the unit. |
| `ReapExpired` | per shard, two `ZRANGEBYSCORE` sweeps: flush-flip `pending_flush`→`eligible`; reclaim `leases` past `now` → re-index. |

Each method is a single round trip to one instance (`EVALSHA`), exactly as each M1
method was one SQL round trip. Atomicity comes from Lua's run-to-completion on the
single Redis thread — the structural analog of M1's unit row lock.

---

## Keyspace (per shard)

Per work unit, keyed by the full triple rendered `{ws}|sess|peer` (the `{ws}`
hash-tag pins all of a unit's keys to one Cluster slot — see §Sharding):

| Key | Type | Holds |
|---|---|---|
| `s:{ws}|sess|peer` | Stream | the per-unit FIFO; entry fields `seq`, `p` (payload), `c` (cost) |
| group `g` on that stream | consumer group | delivery cursor + PEL (in-flight, `delivery_count`) |
| `wu:{ws}|sess|peer` | Hash | `pending_cost`, `next_seq`, `threshold`, `max_wait_ms`, `oldest_pending_ms`, `flush_deadline`, `claimed_by`, `lease_token`, `lease_expires` |

Per shard (global within the instance):

| Key | Type | Holds | Path-1 analog |
|---|---|---|---|
| `eligible` | ZSET | free + claimable units, score = `flush_deadline` | partial index `wu_claimable` |
| `pending_flush` | ZSET | free units below T, score = `flush_deadline` | `wu_flushable` |
| `leases` | ZSET | claimed units, score = `lease_expires` | `wu_leased` |
| `dlq` | Stream | dead-lettered poison entries | `dead_letters` table |

This is the architecture doc's data model verbatim, plus three M3 build additions
flagged below (`next_seq`, `lease_token`, `max_wait_ms` on the hash — the exact
parallel to M1's `next_seq`/`lease_token`/`max_wait_ms` additions on `work_units`).

---

## The one deliberate divergence: `seq` vs the Redis stream ID

The architecture doc leaves the contract's monotonic `int64 seq` (Task.Seq, the I1
ordering key, and `Ack`'s `throughSeq` argument) implicit. Redis streams identify
entries by a `<ms>-<n>` stream ID, **not** a small int64 — so the driver must
bridge the two. This is the M3 analog of M1's composite-key divergence and gets the
same first-class treatment.

**Decision: maintain our own `seq` exactly as Postgres does** — `next_seq =
HINCRBY wu:{k} next_seq 1` inside the enqueue Lua (the unit hash is the single
serialization point, so seq order == XADD order == arrival order, gap-free,
per-unit). The `seq` is stored as a stream entry field; `XADD * ` still assigns the
native stream ID we use for `XACK`/`XDEL`/`XAUTOCLAIM`.

**Ack maps `throughSeq` → stream IDs statelessly.** Because the single Redis thread
assigns `next_seq` and the `XADD *` ID in the *same* script, **stream-ID order ==
seq order** (the I1 guarantee made literal). So the ack Lua reads this consumer's
PEL in order (`XPENDING s g - + <batch> <me>` — bounded by the drain batch, which
is small), reads each entry's `seq` field, and `XACK`+`XDEL`s exactly those with
`seq ≤ throughSeq`. No driver-side seq→ID map, no extra contract param, and it
**survives crash recovery for free**: a takeover process rebuilds nothing — it scans
the PEL it inherited via `XAUTOCLAIM` the same way. *(Revisit → cache the batch's
seq→ID pairs on the in-flight unit if the per-ack PEL scan ever shows up; it can't
at our batch sizes.)*

This keeps `next_seq` a shared talking point with M1 (same invariant, two
substrates) rather than an awkward stream-ID leak into the contract.

---

## `rueidis` wiring (`internal/valkey/backend.go`)

```go
type Backend struct {
	shards   []rueidis.Client // one client per primary; len==1 is the baseline head-to-head point
	scripts  scripts          // *rueidis.Lua: enqueue, claim, ack, release, heartbeat, fail, reapFlush, reapLease
	defT     int64            // DefaultThreshold
	defWait  time.Duration    // DefaultMaxWait
	maxTries int              // MaxAttempts (poison cap)
	rr       atomic.Uint64    // round-robin cursor for Claim across shards
}

func New(o Options) (queue.Backend, error) {
	addrs := o.Addrs            // PLQ_VALKEY_ADDR, comma-split; falls back to single Addr
	shards := make([]rueidis.Client, len(addrs))
	for i, a := range addrs {
		c, err := rueidis.NewClient(rueidis.ClientOption{InitAddress: []string{a}})
		// … one consumer group create per stream is lazy (see Enqueue) …
		shards[i] = c
	}
	return &Backend{shards: shards, scripts: loadScripts(), …}, nil
}
```

- **Client:** `github.com/redis/rueidis` (resolved in the architecture doc:
  auto-pipelining + the load generator must out-run the backend). Lua via
  `rueidis.NewLuaScript(src)` → `.Exec(ctx, client, keys, args)`, which does
  `EVALSHA` and falls back to `EVAL` on `NOSCRIPT` automatically — no manual script
  cache management.
- **Routing:** `shard(workspace) = xxhash(workspace) % len(shards)` (A1: shard by
  **workspace**, not the full key — keeps a tenant on one shard). Enqueue / Drain /
  Ack / Release / Heartbeat / Fail all route by the unit's workspace → always the
  same instance → every Lua is single-instance atomic.
- **Claim** has no unit yet, so it consults shards: starting at `rr++ % n`, run the
  claim Lua (`ZPOPMIN eligible`) on each in turn, return the first hit; `(nil,nil)`
  only when every shard is empty. Round-robin keeps the funnel fair across shards.
- **Consumer group bootstrap:** the group `g` is created lazily — the enqueue Lua
  does `XGROUP CREATE s:{k} g $ MKSTREAM` guarded by `pcall` (ignore BUSYGROUP), so
  a unit's group exists by the time any worker drains it. No global setup step.

### Options + config

```go
type Options struct {
	Addrs            []string      // ≥1 primary; len 1 = baseline, 2/4 = the scaling sweep
	DefaultThreshold int64
	DefaultMaxWait   time.Duration
	MaxAttempts      int
}
```

`internal/config` already exposes `PLQ_VALKEY_ADDR` (single string today); M3 makes
it comma-splittable into `Addrs` (additive, no new knob). Time is supplied by the
client as a unix-ms `ARGV` (`Claim`/`Enqueue`/`ReapExpired` pass `now`), **not**
Redis `TIME` — matches `ReapExpired(ctx, now)`'s contract signature and keeps the
conformance suite's time control deterministic.

---

## Per-method Lua (deltas from the architecture doc)

The architecture doc already carries the enqueue / claim / ack / reaper Lua. The
build deltas are all about wiring the **lease_token** (ABA-safety, contract models
`LeaseToken` separately from `claimed_by`) and the **seq field** through every
script — the exact parallel to M1 adding `lease_token` + per-head recompute to the
SQL.

### Enqueue — one Lua (architecture §Enqueue + `next_seq` + lazy group)

```lua
-- KEYS: s:{k}, wu:{k}, eligible, pending_flush ; ARGV: payload, cost, T, max_wait, now
redis.call('XGROUP','CREATE',KEYS[1],'g','$','MKSTREAM')        -- pcall-guarded; ignore BUSYGROUP
local seq  = redis.call('HINCRBY', KEYS[2], 'next_seq', 1)      -- our int64 seq (== M1 next_seq)
redis.call('XADD', KEYS[1], '*', 'seq', seq, 'p', ARGV[1], 'c', ARGV[2])
local cost = redis.call('HINCRBY', KEYS[2], 'pending_cost', ARGV[2])    -- I3, O(1)
if tonumber(cost) == tonumber(ARGV[2]) then                    -- empty/tombstoned → (re)start flush clock
  redis.call('HSET', KEYS[2], 'oldest_pending_ms', ARGV[5],
             'flush_deadline', ARGV[5] + ARGV[4], 'threshold', ARGV[3], 'max_wait_ms', ARGV[4])
end
local dl = redis.call('HGET', KEYS[2], 'flush_deadline')
if redis.call('HGET', KEYS[2], 'claimed_by') == false then     -- only re-index if free
  if tonumber(cost) >= tonumber(ARGV[3])
    then redis.call('ZADD',KEYS[3],dl,KEYS[2]); redis.call('ZREM',KEYS[4],KEYS[2])  -- eligible (I4)
    else redis.call('ZADD',KEYS[4],dl,KEYS[2]) end                                   -- pending_flush
end
return seq
```

### Claim — one Lua per shard (architecture §Claim + lease_token)

```lua
-- KEYS: eligible, leases ; ARGV: me, lease_token, lease_ms, now
local hit = redis.call('ZPOPMIN', KEYS[1], 1)        -- lowest flush_deadline = age-fair (I2·I4)
if hit[1] == nil then return nil end
local wu = hit[1]
redis.call('HSET', wu, 'claimed_by', ARGV[1], 'lease_token', ARGV[2], 'lease_expires', ARGV[4]+ARGV[3])
redis.call('ZADD', KEYS[2], ARGV[4]+ARGV[3], wu)     -- track for the reaper
return wu                                            -- driver builds ClaimedUnit{Key, Worker, Lease=token, LeaseTill}
```

The driver generates a fresh `lease_token` (uuid) per claim, so a unit
reclaimed-then-reclaimed by the same worker invalidates the old handle — identical
ABA-safety to M1's `lease_token uuid`.

### Drain — lease check, then ordered batch

```text
HGET wu:{k} lease_token            -- != our token → ErrLeaseLost (cheap, before the read)
XREADGROUP GROUP g <me> COUNT <batch> STREAMS s:{k} >   -- creates PEL entries, stream-ID = seq order
```
Empty read with a valid lease → the worker loop calls `Release` (distinguished from
`ErrLeaseLost`, matching the oracle and M1).

### Ack — one Lua: PEL-scan, ack prefix, recompute, keep-or-release

```lua
-- KEYS: s:{k}, wu:{k}, eligible, pending_flush, leases ; ARGV: me, token, through, max_wait, now, wu_ttl
if redis.call('HGET',KEYS[2],'lease_token') ~= ARGV[2] then return redis.error_reply('LEASELOST') end
local pend = redis.call('XPENDING', KEYS[1], 'g', '-', '+', 1000, ARGV[1])  -- our in-flight, in order
local acked = 0
for _,e in ipairs(pend) do
  local r = redis.call('XRANGE', KEYS[1], e[1], e[1])         -- read seq field of this id
  local seq = -- field 'seq' from r[1][2]
  if tonumber(seq) <= tonumber(ARGV[3]) then
    redis.call('XACK', KEYS[1], 'g', e[1]); redis.call('XDEL', KEYS[1], e[1])   -- I5: gone, never redelivered
    acked = acked + -- the entry's 'c' (cost)
  end
end
local cost = redis.call('HINCRBY', KEYS[2], 'pending_cost', -acked)            -- I3
-- recompute head age + flush_deadline from new head (XRANGE s - + COUNT 1's seq/age), then:
--   work remains & still eligible (cost>=T or head aged) → keep lease: HSET lease_expires; ZADD leases
--   work remains & not eligible                          → release: clear claimed_by/token; ZREM leases; re-index
--   stream empty                                         → TOMBSTONE: clear cost; EXPIRE wu:{k}+s:{k} wu_ttl
return -- still_held bool
```

- Mirrors M1's ack exactly: self-validating cost (sum the entries actually
  removed — no caller-supplied `$acked_cost`), per-head flush recompute (not a
  sticky flag), keep-or-release in one atomic step.
- **Tombstone is free** (architecture win over M1's reaper `DELETE`): `EXPIRE` on
  the hash + stream; a revival before TTL reuses the keys. No tombstone sweep.

### Release / Heartbeat / Fail / ReapExpired

- **Release / Heartbeat:** lease-validate by token; Release clears the lease +
  `ZREM leases` + re-indexes the unit free; Heartbeat `HSET lease_expires` + re-`ZADD
  leases`. `0`-effect (token mismatch) → `ErrLeaseLost`.
- **Fail:** `delivery_count` comes from `XPENDING` **for free** (M1 maintained
  `attempts` by hand). At `MaxAttempts`: `XACK`+`XDEL` the head, `XADD dlq`,
  `HINCRBY -cost`, recompute, release → unit unblocks (FIFO+exclusive would
  otherwise wedge). Below cap: just release → redeliver in order, `delivery_count`
  now higher.
- **ReapExpired** (per shard, two `ZRANGEBYSCORE` sweeps — architecture §Reaper):
  flush-flip `pending_flush`→`eligible` for `score ≤ now`; reclaim `leases ≤ now`
  → clear `claimed_by`, re-index into `eligible`/`pending_flush`. Driver fans both
  out across all shards and sums the counts. `XAUTOCLAIM` for the *messages* happens
  on the next Drain after takeover (architecture §Crash recovery), with
  `min-idle ≥ lease` so we never steal from a live worker.

---

## Stater + Resetter (the optional capabilities the harness/conformance use)

```go
func (b *Backend) Stats(ctx) (queue.Stats, error)  // sum across shards
func (b *Backend) Reset(ctx) error                  // FLUSHDB each shard (bench-only)
```

- **`Stats`** (the `loadrun` saturation/backlog signal): per shard, `ZCARD eligible`
  (→ EligibleUnits, the look-ahead backlog), `ZCARD eligible+pending_flush+leases`
  (→ TotalUnits), `XLEN dlq` (→ DeadLetters), and `ZRANGE pending_flush 0 0
  WITHSCORES` (oldest below-T deadline → OldestBelowT). PendingTasks needs a sum of
  `XLEN s:{k}`; **flagged** — keep a maintained `pending_tasks` counter on enqueue/ack
  rather than scanning streams (the M3 parallel to "never scan"), or approximate.
- **`Reset`** = `FLUSHDB` per shard — the conformance suite's between-scenario wipe,
  mirroring the Postgres `TRUNCATE`. Both gated to the test/bench path only.

---

## Conformance wiring (`tests/conformance/conformance_test.go`)

Add a third entry alongside `TestMemory` / `TestPostgres`, gated behind
`PLQ_TEST_VALKEY` so default `go test` stays hermetic:

```go
func TestValkey(t *testing.T) {
	addr := os.Getenv("PLQ_TEST_VALKEY")            // e.g. localhost:6379
	if addr == "" { t.Skip("set PLQ_TEST_VALKEY to run the Valkey conformance suite") }
	conformance.Run(t, func(cfg conformance.Config) queue.Backend {
		be, _ := valkey.New(valkey.Options{Addrs: []string{addr}, DefaultThreshold: int64(cfg.Threshold),
			DefaultMaxWait: cfg.MaxWait, MaxAttempts: cfg.MaxAttempts})
		be.(queue.Resetter).Reset(context.Background())   // FLUSHDB between scenarios
		return be
	})
}
```

This is the apples-to-apples **correctness** guarantee — three backends, one suite,
identical observable behavior — and it absorbs the ordering-under-crash / gate /
flush proofs as conformance cases (the architecture doc's proofs #1–#3) at
**N=1**, which is exactly the architecture's single-instance design.

---

## go.mod + docker-compose + Makefile + the head-to-head

- **go.mod:** `require github.com/redis/rueidis v1.x` (+ `xxhash` for the shard
  hash, likely already transitive). The valkey Dockerfile's `go mod download` now
  fetches; commit `go.sum`.
- **docker-compose:** a `valkey` service on `valkey/valkey:8.1` with the durability
  config from the architecture doc (`appendonly yes`, `appendfsync everysec`,
  `aof-use-rdb-preamble yes`, `noeviction`, `maxmemory` headroom). Single instance
  for the laptop; the 2/4-shard sweep is N services (or N ports) the harness reads
  from `PLQ_VALKEY_ADDR=a,b,c,d`.
- **Makefile:** `make up-valkey` (compose up the valkey path), `make
  load-test-valkey` (the M2 sweep pointed at Valkey via `PLQ_BACKEND=valkey` /
  `-tags valkey`), `make proofs-valkey` (conformance + ordering-under-crash vs
  Valkey). Reuses the M2 `loadrun` harness unchanged — it's already
  backend-agnostic.
- **The overlay (proof #4):** `scripts/plot.py` gains a Valkey series so
  `results/throughput.png` and `results/loop-p99.png` show **PG (1 primary) vs
  Valkey (1/2/4 shards)** on the same axes. That chart *is* the decision gate: a
  Path-1 plateau below target and/or a loop-p99 gap justifies Path 2; if PG never
  plateaus at the real scale, Path 1 ships (architecture §"When Path 1 wins").

---

## Tradeoffs

**Own `seq` (HINCRBY) vs leaking the stream ID into the contract.** *Chosen:*
maintain `next_seq` like M1, store it as a stream field, map `throughSeq`→IDs by an
ordered PEL scan at ack. Keeps the contract's `int64 seq` honest, shares the
`next_seq` invariant + talking point with Postgres, and the scan is crash-safe and
batch-bounded. *Rejected:* exposing `<ms>-<n>` stream IDs through `Task.Seq`/`Ack`
— would fork the contract for one backend and break apples-to-apples. *Revisit:*
cache the batch's seq→ID pairs on the in-flight unit if the PEL scan ever measures.

**N independent primaries + client-side workspace routing vs Redis Cluster.**
*Chosen (for the test):* N standalone Valkey instances, driver routes by
`hash(workspace) % N`. Every Lua is trivially single-instance atomic — no
cross-slot concern at all — and it models "N independent primaries" (the linear
write-scaling claim) directly. The `{ws}` hash-tag is kept in the key layout so the
*same* design runs on real Cluster unchanged. *Rejected (for the take-home):*
standing up Redis Cluster — operational weight that proves nothing the standalone
sweep doesn't. *Flagged:* Cluster is the production deploy; named, not run.

**Consumer-group + PEL vs plain stream + head cursor.** Carried from the
architecture doc (open prototype). *Chosen for M3:* consumer groups —
`delivery_count` (poison caps) and `XAUTOCLAIM` (in-order crash redelivery) come
free, and the ordering-under-crash proof is the place that proof #1 lives. The
head-cursor alternative mirrors M1 1:1 but re-implements `delivery_count` by hand;
prototype only if the PEL bookkeeping bites.

**`lease_token` on the hash (M3 add) vs `claimed_by` alone.** *Chosen:* add it —
the contract models `LeaseToken` separately, and it makes lease validation ABA-safe
(same reasoning + same shape as M1's `lease_token uuid`). Additive; no decision
change to the architecture doc.

**Stateless ack PEL-scan vs maintained `pending_tasks` for Stats.** Ack stays
stateless (PEL scan); but `Stats.PendingTasks` would force a stream scan, so Stats
keeps a maintained `pending_tasks` counter instead (never scan streams — the
look-ahead principle applied to the depth metric). Slight asymmetry, called out.

---

## Resolved (iteration 1)

3. **PEL-scan ack cost → build the stateless PEL scan; measure, don't pre-optimize.**
   Ship the `XPENDING`+`XRANGE` ordered scan; the cached seq→ID map is the named
   fallback only if the per-ack loop measures at our batch sizes (it shouldn't —
   batch is bounded). Same measure-first discipline as the M2 deferred PG items.
4. **`pending_tasks` counter → maintain it.** Stats keeps a maintained
   `pending_tasks` counter (HINCRBY on enqueue/ack) rather than scanning streams —
   the look-ahead "never scan" principle applied to the depth metric. The slight
   asymmetry with the stateless ack is accepted and called out in Tradeoffs.
5. **Shard count for the graded run → 1/2/4.** Enough to show linearity without
   over-building the compose (N=1 is the baseline + the conformance config).

## Open Questions

*(none open as build blockers — the two below are resolved-by-measurement on the
head-to-head, not gates on writing the driver.)*

1. **Is the single-thread claim funnel a real ceiling at 1000 workers, or
   microsecond-negligible?** The architecture's headline measured question. If a
   single shard's `ZPOPMIN` funnel caps throughput, that *is* the sharding argument
   — quantify it on the 1/2/4-shard sweep. *(Resolved by the head-to-head.)*
2. **Does the latency win survive `everysec` + replica + `min-replicas-to-write`?**
   Measure loop-p99 at the durability config we'd defend, not bare. *(Head-to-head.)*

---

## Out of Scope

- The canonical cloud sweep numbers (M2's gated AWS run produces the PG baseline;
  the Valkey overlay reuses that harness — running it is the M3 *measurement* step,
  not this build).
- Redis Cluster deploy, multi-region, cross-shard transactions (sharding is
  single-region, workspace-local — architecture §Out of Scope).
- Managed durable tier / `WAITAOF` on the hot path (named as the zero-loss knob,
  not run — the posture forbids it).
- Per-tenant fairness scheduling (A1: explain the seam, don't build WFQ).
- Real LLM calls (the harness simulates downstream work, as in M1/M2).
