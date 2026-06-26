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

## Candidates already living in the designs (pull in if room)

- **Per-tenant fairness scheduling (WFQ).** The A1 amendment names the one-seam
  extension (claim `ORDER BY` / eligible-ZSET score + per-tenant in-flight lease
  cap → deficit-round-robin / weighted lottery). Decision was *explain, don't
  build* — so it's a natural enhancement bullet.
- **Managed durable tier (Valkey).** Named in the Path 2 design as the zero-loss
  production knob, not run. A candidate bullet for the durability story.
