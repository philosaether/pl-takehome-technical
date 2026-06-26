// Package loadgen is the M2 evidence layer: Zipfian-churn producers, the worker
// sweep harness, telemetry, and chaos injection — all backend-agnostic (they take
// a queue.Backend). cmd/plq builds Workload/RunSpec from config and calls in here:
//
//   - RunProducers — producer-only (the canonical split topology's loadgen box)
//   - Run          — integrated producers+workers+reaper+sampler (one sweep point)
//   - Metrics      — shared telemetry (implements queue.Recorder)
//   - Chaos        — periodic worker-cancel for load-run chaos
//
// The deterministic proofs live under proofs/, not here.
package loadgen
