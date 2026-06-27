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
  - **MERGED + pushed to `main`** 2026-06-26 (`d1795c8`, --no-ff; reviewed).

- **M3 head-to-head — DONE, run on cloud** (`riff/m3-head-to-head`, merged 2026-06-27).
  Built the two prerequisites the merged M3 code didn't cover (shard-count capture as a
  sweep series key; terraform Valkey primaries 1/2/4), dry-ran locally, then ran the
  gated AWS head-to-head. **Canonical result (proof #4):** PG plateaus ~1.7k acks/s and
  *declines* past 100 workers; Valkey scales near-linearly 26k→49k→92k across 1/2/4
  shards (~53× PG at 4). Artifacts: `results/run-cloud-1/` (+ results.md). Two infra bugs
  found+fixed live (compose PG port 5432→5433; pg root volume 8→20 GiB). `results/` now
  tracked as per-run buckets. Cloud torn down, ≪$1.

- **Next milestone — the ambitious data run (run-cloud-2, `feature/ambitious-head-to-head`).**
  Design drafted + accepted approach: `designs/ambitious-head-to-head.md`. Upgrades the
  head-to-head to airtight — isolated/saturated topology (worker-alone + external
  loadgen, `PLQ_PRODUCERS=0` + a new `PLQ_RESET` gate), 1/2/4/**8** shards, a **tuned-PG**
  baseline, a **durability tradeoff** curve (fsync off/everysec/always), the full process
  sweep 0/2/20/200ms, and $/throughput + p99-at-knee analysis. Middle orchestration
  (PG-stock+PG-tuned parallel; Valkey shards sequential on a shared 8-box pool), ~17
  boxes, ~30 min, ~$3-4. **Build: the `PLQ_RESET` gate + terraform (pg_tuned, valkey_count=8,
  generic runner pool) + orchestration scripts; dry-run the isolated topology locally
  before the gated apply.**

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
