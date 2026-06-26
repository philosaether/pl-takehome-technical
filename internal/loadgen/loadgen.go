// Package loadgen produces work for the queue. M0 ships a minimal placeholder:
// it enqueues a small fixed workload so the binary exercises Enqueue end-to-end.
// The real generator — Zipfian wu_key churn, configurable cost distribution,
// pinned RNG seed, crash injection, and metrics export — lands in M2.
package loadgen

import (
	"context"
	"fmt"

	"github.com/philosaether/pl-takehome-technical/internal/config"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// Run enqueues the M0 placeholder workload via the shared Backend.Enqueue path
// (loadgen is not a second code path into the store).
func Run(ctx context.Context, be queue.Backend, cfg config.Config) error {
	perKey := cfg.LoadTasks / max(cfg.LoadKeys, 1)
	cost := cfg.LoadCost / int64(max(perKey, 1)) // ~LoadCost per key, spread across its tasks
	if cost < 1 {
		cost = 1
	}
	produced := 0
	for k := 0; k < cfg.LoadKeys; k++ {
		key := queue.WorkUnitKey{
			Workspace: fmt.Sprintf("ws-%d", k%8), // 8 tenants, sharing the queue
			Session:   fmt.Sprintf("sess-%d", k),
			Peer:      "peer-0",
		}
		for i := 0; i < perKey; i++ {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, err := be.Enqueue(ctx, key, []byte("task"), cost); err != nil {
				return err
			}
			produced++
		}
	}
	fmt.Printf("loadgen (M0 placeholder): produced %d tasks across %d keys\n", produced, cfg.LoadKeys)
	return nil
}
