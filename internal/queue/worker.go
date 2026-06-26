package queue

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ProcessModel configures the simulated downstream work. It is the axis that
// locates the PG→Valkey cutover (see scaffold design): "zero" measures the queue
// itself; "cost" places realistic operating points on the throughput curve.
type ProcessModel struct {
	Kind    string        // "zero" | "fixed" | "cost"
	Base    time.Duration // fixed/per-batch latency
	PerCost time.Duration // cost mode: sleep = Base + Cost*PerCost
}

func (p ProcessModel) work(t Task) {
	switch p.Kind {
	case "fixed":
		time.Sleep(p.Base)
	case "cost":
		time.Sleep(p.Base + time.Duration(t.Cost)*p.PerCost)
	default: // "zero" or unset — measure the queue, not fake compute
	}
}

// BackoffConfig is the adaptive idle backoff: sleep grows when claims come back
// empty and collapses to zero under load (PG design §Worker wakeup).
type BackoffConfig struct {
	Min time.Duration
	Max time.Duration
}

func (b BackoffConfig) next(d time.Duration) time.Duration {
	if d <= 0 {
		return b.Min
	}
	d *= 2
	if b.Max > 0 && d > b.Max {
		return b.Max
	}
	return d
}

// Recorder receives loop telemetry (optional). metrics.Metrics implements it; the
// queue package only depends on this interface, never on the metrics package.
type Recorder interface {
	Claim(d time.Duration, gotUnit bool) // a claim attempt: latency + whether a unit came back
	Ack(tasks int)                       // tasks acked (throughput)
	Loop(d time.Duration)                // claim/drain→ack wall time for one batch cycle
}

// WorkerConfig is the slice of config the worker loop needs. config.Config builds
// it via WorkerConfig() — this keeps the queue package free of config imports.
type WorkerConfig struct {
	Lease   time.Duration
	Batch   int
	Backoff BackoffConfig
	Process ProcessModel
	// OnProcess, if set, is called for each task just before its simulated work.
	// The seam the M2 ordering/gate proofs hook to record the processing log.
	OnProcess func(WorkUnitKey, Task)
	// Recorder, if set, receives throughput/latency telemetry (M2 metrics).
	Recorder Recorder
}

// Worker runs the claim→drain→process→ack loop against a Backend. The loop is
// identical for every backend — that identity is what makes the head-to-head fair.
type Worker struct {
	id  WorkerID
	be  Backend
	cfg WorkerConfig
}

func NewWorker(id WorkerID, be Backend, cfg WorkerConfig) *Worker {
	return &Worker{id: id, be: be, cfg: cfg}
}

// Run loops until ctx is cancelled, claiming units and backing off adaptively
// when nothing is eligible.
func (w *Worker) Run(ctx context.Context) error {
	var idle time.Duration
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		did, err := w.runOnce(ctx)
		if did && err == nil {
			idle = 0
			continue
		}
		// Nothing claimed, or a backend error — back off rather than hot-spin. A
		// real backend that errors every call must not busy-loop (the memory oracle
		// never errors, so this is dormant today).
		idle = w.cfg.Backoff.next(idle)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(idle):
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) (didWork bool, err error) {
	t0 := time.Now()
	unit, err := w.be.Claim(ctx, w.id, w.cfg.Lease)
	if w.cfg.Recorder != nil {
		w.cfg.Recorder.Claim(time.Since(t0), err == nil && unit != nil)
	}
	if err != nil || unit == nil {
		return false, err // nil unit → nothing eligible → caller backs off
	}
	for {
		loopStart := time.Now()
		tasks, err := w.be.Drain(ctx, unit, w.cfg.Batch)
		if err != nil {
			return true, err
		}
		if len(tasks) == 0 {
			return true, w.be.Release(ctx, unit)
		}
		// Heartbeat the lease while processing a potentially-slow batch: downstream
		// LLM calls can run long, so a batch can outlive the lease (OQ4).
		hbCtx, stop := context.WithCancel(ctx)
		go w.heartbeat(hbCtx, unit)
		for _, t := range tasks {
			if w.cfg.OnProcess != nil {
				w.cfg.OnProcess(unit.Key, t)
			}
			w.cfg.Process.work(t)
		}
		stop()
		held, err := w.be.Ack(ctx, unit, tasks[len(tasks)-1].Seq)
		if err != nil {
			return true, err
		}
		if w.cfg.Recorder != nil {
			w.cfg.Recorder.Ack(len(tasks))
			w.cfg.Recorder.Loop(time.Since(loopStart))
		}
		if !held {
			return true, nil // Ack released the unit → done with it
		}
	}
}

func (w *Worker) heartbeat(ctx context.Context, unit *ClaimedUnit) {
	tick := w.cfg.Lease / 3
	if tick <= 0 {
		return
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = w.be.Heartbeat(ctx, unit, w.cfg.Lease)
		}
	}
}

// Pool runs n identical workers against one Backend until ctx is cancelled.
type Pool struct {
	be  Backend
	cfg WorkerConfig
	n   int
}

func NewPool(be Backend, n int, cfg WorkerConfig) *Pool {
	return &Pool{be: be, cfg: cfg, n: n}
}

func (p *Pool) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := 0; i < p.n; i++ {
		wg.Add(1)
		id := WorkerID(fmt.Sprintf("w%d", i))
		go func() {
			defer wg.Done()
			_ = NewWorker(id, p.be, p.cfg).Run(ctx)
		}()
	}
	wg.Wait()
	return ctx.Err()
}
