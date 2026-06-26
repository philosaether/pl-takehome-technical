package loadgen

import (
	"fmt"
	"math/rand"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// CostDist is the per-task token-cost distribution.
type CostDist struct {
	Kind string // "constant" | "uniform"
	Lo   int64
	Hi   int64
}

func (c CostDist) sample(r *rand.Rand) int64 {
	if c.Kind == "uniform" && c.Hi > c.Lo {
		return c.Lo + r.Int63n(c.Hi-c.Lo+1)
	}
	if c.Lo <= 0 {
		return 1
	}
	return c.Lo
}

// Workload parameterizes the producers. A pinned Seed makes the whole workload
// byte-reproducible — the same load drives every sweep point and (M3) both backends.
type Workload struct {
	Seed       int64
	WorkingSet int     // # live wu_keys in the Zipf window
	ZipfS      float64 // skew (>1; larger = hotter heads)
	BirthRate  float64 // P(slide the window by 1) per enqueue → key churn
	Cost       CostDist
	Workspaces int // tenants sharing the queue
}

// DefaultWorkload is a sane local-smoke default.
func DefaultWorkload(seed int64) Workload {
	return Workload{
		Seed:       seed,
		WorkingSet: 1000,
		ZipfS:      1.2,
		BirthRate:  0.02,
		Cost:       CostDist{Kind: "uniform", Lo: 10, Hi: 50},
		Workspaces: 8,
	}
}

// keyFor maps a monotonic key index to a work-unit key. workspace = index %
// Workspaces, so a tenant owns a deterministic slice of keys (the shard/fairness
// seam is observable in the metrics).
func keyFor(idx int64, workspaces int) queue.WorkUnitKey {
	ws := idx % int64(max(workspaces, 1))
	return queue.WorkUnitKey{
		Workspace: fmt.Sprintf("ws-%d", ws),
		Session:   fmt.Sprintf("s-%d", idx),
		Peer:      "p",
	}
}
