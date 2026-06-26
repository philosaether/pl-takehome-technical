package loadgen

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

var payload = []byte("task") // fixed small payload; cost is what matters for the gate

// RunProducers drives `n` Zipfian-churn producer goroutines against the backend
// until ctx is cancelled. A shared sliding `base` (advanced by BirthRate) retires
// old keys (workers drain them) and births new ones — the born/drained churn.
func RunProducers(ctx context.Context, be queue.Backend, m *Metrics, w Workload, n int) {
	if w.WorkingSet < 2 {
		w.WorkingSet = 2
	}
	var base atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			produce(ctx, be, m, w, id, &base)
		}(i)
	}
	wg.Wait()
}

func produce(ctx context.Context, be queue.Backend, m *Metrics, w Workload, id int, base *atomic.Int64) {
	r := rand.New(rand.NewSource(w.Seed + int64(id))) // per-producer stream, reproducible
	zipf := rand.NewZipf(r, w.ZipfS, 1, uint64(w.WorkingSet-1))
	for ctx.Err() == nil {
		if r.Float64() < w.BirthRate {
			base.Add(1) // slide the window → churn
		}
		idx := base.Load() + int64(zipf.Uint64()) // rank 0 = hottest = oldest-active
		key := keyFor(idx, w.Workspaces)
		if _, err := be.Enqueue(ctx, key, payload, w.Cost.sample(r)); err != nil {
			if ctx.Err() != nil {
				return
			}
			continue // transient under load; keep pushing
		}
		m.Enqueued.Add(1)
	}
}
