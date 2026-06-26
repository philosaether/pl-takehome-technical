package loadgen

import (
	"context"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// RunSpec is one sweep point.
type RunSpec struct {
	Workers   int
	Producers int
	Process   queue.ProcessModel
	Label     string // process label for the CSV/filename (e.g. "zero", "20ms"); distinguishes cost points
	Workload  Workload
	Lease     time.Duration
	Batch     int
	Duration  time.Duration
	Warmup    time.Duration // excluded from the throughput measurement
	SampleCSV io.Writer     // optional: per-second sample rows
}

// Result is the summary of one sweep point (one row of results/sweep.csv).
type Result struct {
	Workers    int
	Process    string
	Throughput float64 // acks/sec over the measurement window
	ClaimP99   time.Duration
	LoopP99    time.Duration
	LoopSamps  int64
	MinBacklog int64 // min eligible-unit backlog over the window (-1 = no Stater)
	MaxBacklog int64
	Saturated  bool // backlog stayed > 0 → the plateau is the backend's, not the producer's
	Enqueued   int64
	Acked      int64
	LeaseExp   int64
}

// Run executes one sweep point: producers + worker pool + reaper + sampler, for
// spec.Duration. The same harness drives PG (M2) and Valkey (M3).
func Run(ctx context.Context, be queue.Backend, spec RunSpec) (Result, error) {
	m := NewMetrics()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); RunProducers(runCtx, be, m, spec.Workload, spec.Producers) }()
	go func() {
		defer wg.Done()
		wcfg := queue.WorkerConfig{
			Lease:    spec.Lease,
			Batch:    spec.Batch,
			Backoff:  queue.BackoffConfig{Min: time.Millisecond, Max: 100 * time.Millisecond},
			Process:  spec.Process,
			Recorder: m,
		}
		_ = queue.NewPool(be, spec.Workers, wcfg).Run(runCtx)
	}()
	go func() { defer wg.Done(); reaperLoop(runCtx, be, m, spec.Lease) }()

	res := sample(runCtx, be, m, spec)
	cancel()
	wg.Wait()
	return res, nil
}

func reaperLoop(ctx context.Context, be queue.Backend, m *Metrics, lease time.Duration) {
	iv := lease / 3
	if iv < 100*time.Millisecond {
		iv = 100 * time.Millisecond
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if r, _, err := be.ReapExpired(ctx, now); err == nil {
				m.LeaseExpired.Add(int64(r))
			}
		}
	}
}

func sample(ctx context.Context, be queue.Backend, m *Metrics, spec RunSpec) Result {
	stater, _ := be.(queue.Stater)
	procLabel := spec.Label
	if procLabel == "" {
		procLabel = spec.Process.Kind
	}
	res := Result{
		Workers: spec.Workers, Process: procLabel,
		MinBacklog: -1, MaxBacklog: -1,
	}
	if spec.SampleCSV != nil {
		fmt.Fprintln(spec.SampleCSV, "elapsed_s,acked,enqueued,eligible_backlog,claim_p99_ms,loop_p99_ms")
	}
	var minB int64 = math.MaxInt64
	var maxB int64 = -1
	var measAcked int64
	var measStart time.Time
	measured := false

	start := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return finalize(res, m, minB, maxB, measAcked, measStart, measured, stater != nil)
		case <-tick.C:
			elapsed := time.Since(start)
			acked := m.Acked.Load()
			backlog := int64(-1)
			if stater != nil {
				if st, err := stater.Stats(ctx); err == nil {
					backlog = st.EligibleUnits
					if elapsed >= spec.Warmup { // only judge saturation after warmup
						if backlog < minB {
							minB = backlog
						}
						if backlog > maxB {
							maxB = backlog
						}
					}
				}
			}
			if !measured && elapsed >= spec.Warmup {
				measured, measAcked, measStart = true, acked, time.Now()
			}
			if spec.SampleCSV != nil {
				fmt.Fprintf(spec.SampleCSV, "%.0f,%d,%d,%d,%.1f,%.1f\n",
					elapsed.Seconds(), acked, m.Enqueued.Load(), backlog,
					ms(m.ClaimP99()), ms(m.LoopP99()))
			}
			if elapsed >= spec.Duration {
				return finalize(res, m, minB, maxB, measAcked, measStart, measured, stater != nil)
			}
		}
	}
}

func finalize(res Result, m *Metrics, minB, maxB, measAcked int64, measStart time.Time, measured, hasStater bool) Result {
	res.ClaimP99 = m.ClaimP99()
	res.LoopP99 = m.LoopP99()
	res.LoopSamps = m.LoopSamples()
	res.Enqueued = m.Enqueued.Load()
	res.Acked = m.Acked.Load()
	res.LeaseExp = m.LeaseExpired.Load()
	if measured && !measStart.IsZero() {
		secs := time.Since(measStart).Seconds()
		if secs > 0 {
			res.Throughput = float64(res.Acked-measAcked) / secs
		}
	}
	if hasStater && minB != math.MaxInt64 {
		res.MinBacklog, res.MaxBacklog = minB, maxB
		res.Saturated = minB > 0 // workers never starved → backend-bound plateau
	}
	return res
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// SweepHeader / CSVRow define results/sweep.csv.
func SweepHeader() string {
	return "workers,process,throughput_acks_s,claim_p99_ms,loop_p99_ms,loop_samples,min_backlog,max_backlog,saturated,enqueued,acked,lease_expired"
}

func (r Result) CSVRow() string {
	return fmt.Sprintf("%d,%s,%.0f,%.2f,%.2f,%d,%d,%d,%t,%d,%d,%d",
		r.Workers, r.Process, r.Throughput, ms(r.ClaimP99), ms(r.LoopP99), r.LoopSamps,
		r.MinBacklog, r.MaxBacklog, r.Saturated, r.Enqueued, r.Acked, r.LeaseExp)
}
