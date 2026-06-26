# In Progress

Current work state. Update constantly, delete items when done.

---

## Active

- **M2 loadgen + proofs — LOCAL HALF DONE, reviewed, merged to `main`**
  (`feature/loadgen`, `designs/loadgen-and-proofs.md` accepted+reconciled). Shipped:
  Zipfian-churn producers, the `loadrun` harness (producers+workers+reaper+sampler,
  saturation self-certified via `Stater` backlog), shared `metrics.go` wired into
  `worker`/`loadgen`/`loadrun`, **wired chaos** (`PLQ_CHAOS`, recovery verified),
  ordering-under-crash proof (3-of-10, memory+PG), the **look-ahead bench**
  (scaling units, 1e4→1e7 = **1,364×** at 10⁷), three charts (throughput, latency,
  look-ahead), Makefile sweep (workers × {zero,2ms,20ms,200ms}), and the Terraform
  harness (validated, **not applied**).
  - **GATED — the next action:** `make cloud-up` (terraform apply) → the canonical
    AWS sweep (3 boxes: worker alone / PG / producer; export `TF_VAR_ssh_public_key`
    + `TF_VAR_ssh_cidr` first) → `make cloud-down`. The only spend/irreversible step;
    ~$1 if torn down promptly. Produces the canonical throughput+latency curves.
  - **Measure-first on that sweep:** the deferred PG round-trips (Drain 2→1, Enqueue
    tenant-cache miss) + histogram-mutex contention — optimize only if they compound
    (before/after = talking-point).

- **M3 Valkey driver — LOCAL DONE, verified** on `feature/valkey`
  (`designs/valkey-driver.md` accepted + reconciled, mirrors the M1 build-design shape).
  Shipped: `internal/valkey` — all 8 `queue.Backend` methods over Streams+ZSET+Hash+Lua
  via `rueidis` (`scripts.go` = 7 embedded Lua scripts), `Stater` (maintained
  `pending_tasks`) + `Resetter` (FLUSHDB), N-shard routing by `hash(workspace)`, the
  `lease_token` ABA handle, the stateless PEL-scan seq→ID ack. **Conformance 8/8 +
  ordering-under-crash green vs live valkey/valkey:8.1**; `loadrun` smoke ~12–14k
  acks/s, loop-p99 5ms; 2-shard routing verified (keys split 252/254). Wired:
  docker-compose `valkey` service (durability config), Makefile `up-valkey` /
  `load-test-valkey` / `head-to-head` / `proofs-valkey` + factored `sweep-postgres`/
  `sweep-valkey`, `backend` column on `sweep.csv` + plot.py PG-vs-Valkey overlay,
  `valkey.Dockerfile` go.sum fix.
  - **Locked decisions (built):** own `next_seq` (HINCRBY−1, 0-based == M1) stored as a
    stream field + stateless PEL-scan ack; N independent standalone primaries +
    `hash(workspace)%N` routing (not Cluster); `lease_token` on the hash; maintained
    `pending_tasks` for Stats; XAUTOCLAIM min-idle=0 (lease is the exclusivity gate);
    explicit `attempts` on the hash (oracle-exact, not PEL delivery_count).
  - **MERGED to `main`** 2026-06-26 (`d1795c8`, --no-ff; reviewed). Not yet pushed.

- **Next milestone — M3 head-to-head (own branch, 2026-06-27).** Reuses the M2 gated
  AWS sweep to produce the canonical PG-vs-Valkey curves (decision gate / proof #4:
  PG plateau vs Valkey 1→4-shard scaling + loop-p99). **Two prerequisites the merged
  code does NOT yet cover — build these first on the branch:**
  1. **Terraform is PG-only** (`deploy/terraform`: `pg`/`worker`/`producer` boxes, no
     Valkey). Add a Valkey box — and for the shard sweep, 1/2/4 instances — running the
     durability config from `docker-compose.yml` (appendonly/everysec/noeviction).
  2. **No shard-COUNT capture in the sweep.** `sweep.csv`'s `backend` column is just
     "valkey" regardless of 1/2/4 shards, so the linearity proof can't be plotted as
     distinct series. Add a `shards` column (Result + CSV + plot.py series key) and run
     `sweep-valkey` at 1/2/4 addrs. `make head-to-head` overlays PG vs Valkey but is
     shard-count-blind until then.
  - Measure-first (not blockers): producer count to saturate Valkey (it's fast — the
    smokes went unsaturated at PLQ_PRODUCERS=8/64); the claim-funnel ceiling + the
    deferred PG round-trips, all on the canonical sweep.

- **M1 Postgres driver — DONE, merged to `main`.** All 8 methods; conformance 8/8;
  per-head flush. `postgres-driver.md` accepted+reconciled. (`talking-points.md`
  holds the curated highlights.)

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
