package loadgen

import (
	"context"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// RunChaosWorkers runs a worker pool where workers are periodically "crashed"
// (their context cancelled mid-flight) and immediately respawned — the load-run
// chaos that demonstrates recovery under load (the reaper reclaims the dropped
// leases; throughput dips and recovers; ordering holds). Goroutine-cancel, not
// process-kill (OQ3); the deterministic crash proof is the rigorous one.
func RunChaosWorkers(ctx context.Context, be queue.Backend, n int, cfg queue.WorkerConfig, killEvery time.Duration) {
	for i := 0; i < n; i++ {
		go runChaosWorker(ctx, be, queue.WorkerID(workerName(i)), cfg, killEvery, i)
	}
	<-ctx.Done()
}

func runChaosWorker(ctx context.Context, be queue.Backend, id queue.WorkerID, cfg queue.WorkerConfig, killEvery time.Duration, i int) {
	// stagger kills so they don't all crash at once
	jitter := time.Duration(i) * killEvery / 8
	for ctx.Err() == nil {
		wctx, cancel := context.WithTimeout(ctx, killEvery+jitter)
		_ = queue.NewWorker(id, be, cfg).Run(wctx) // returns when wctx fires (the "crash")
		cancel()
	}
}

func workerName(i int) string {
	return "chaos-w" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
