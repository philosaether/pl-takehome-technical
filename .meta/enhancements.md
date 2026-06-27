# Enhancements — candidate bullets for the 1-page brief

A staging area for "possible enhancements" we flag during build. At publication
time (M4) we pick the most compelling set the one-page brief has room for; the
rest are the honest "here's what v2 looks like" tail. Each entry: the idea, why
it's compelling, and where it came from.

Append-forward. Don't prune — pruning happens at selection time in M4.

---

## Flagged

- **Standalone reaper service.** v1 runs the reaper as an in-process goroutine in
  each worker; the `reap` subcommand already exists. Pulling it into its own
  deployment isolates flush-promotion + lease-reclaim from worker scaling and
  failure (the reaper keeps running even if the worker pool is being rolled).
  *Source: scaffold OQ2.*

- **Loadgen-orchestrated ramp (autoscaling test harness).** v1 sweeps worker count
  via `WORKERS=N` re-runs (serial, comparable — the right call for graded numbers).
  A loadgen that instead drives a *continuous* arrival-rate ramp would let us test
  and tune autoscaling behavior on staging — find the scale-up/down thresholds and
  the settling time under real ramps, not just discrete points. *Source: scaffold
  OQ3.*

---

- **golang-migrate for schema migrations.** M1 embeds an idempotent `schema.sql`
  (`go:embed` + `CREATE IF NOT EXISTS`) run on startup — zero deps, clone-and-run.
  The production answer is versioned migrations (golang-migrate / goose): ordered
  up/down, schema-version tracking, safe rollouts. *Source: postgres-driver M1.*

- **Tombstone TTL reaping.** Drained units are tombstoned in place (row kept for
  cheap revival). A `DELETE FROM work_units WHERE pending_cost=0 AND idle > wu_ttl`
  sweep GCs tombstones that never come back — keeps `work_units` from accumulating
  dead rows under high key churn. Deferred from M1 (not on the correctness path);
  pairs with the M2 vacuum/bloat story. *Source: postgres-driver M1.*

- **The full 8-shard + durability run (run-cloud-2 v2).** run-cloud-2 ran
  quota-constrained (AWS 32-vCPU → m5.large / 4 shards), and the durability
  off/everysec/always curve failed on a `CONFIG SET` permission bug (since fixed:
  `sudo docker exec`). The code already supports the designed run — a quota bump
  (Service Quotas → "Running On-Demand Standard instances" ≥ ~116) + `make cloud-up`
  with `TF_VAR_pg_count=8 TF_VAR_valkey_count=8` and m5.xlarge/m5.2xlarge types gets:
  the **8-shard linearity point** (proves the trend continues past 4, and sharpens
  "~20 PG primaries ≈ 1 Valkey") and the **durability tradeoff** (throughput cost of
  fsync off→everysec→always). Compelling v2: turns the already-strong 4-shard result
  into the airtight one the design scoped. *Source: run-cloud-2, 2026-06-27.*

## Candidates already living in the designs (pull in if room)

- **Per-tenant fairness scheduling (WFQ).** The A1 amendment names the one-seam
  extension (claim `ORDER BY` / eligible-ZSET score + per-tenant in-flight lease
  cap → deficit-round-robin / weighted lottery). Decision was *explain, don't
  build* — so it's a natural enhancement bullet.
- **Managed durable tier (Valkey).** Named in the Path 2 design as the zero-loss
  production knob, not run. A candidate bullet for the durability story.
