package queue

import (
	"context"
	"time"
)

// Stats is a point-in-time snapshot of queue depth — used by the load harness for
// the backlog/saturation signal and end-of-run reporting.
type Stats struct {
	TotalUnits    int64         // live work_units rows
	EligibleUnits int64         // claimable right now (the look-ahead backlog)
	PendingTasks  int64         // undrained tasks
	DeadLetters   int64         // tasks in the DLQ
	OldestBelowT  time.Duration // age of the oldest pending task not yet eligible (0 if none)
}

// Stater is an OPTIONAL backend capability: backends that can cheaply report depth
// implement it. The load harness type-asserts for it; a backend without it just
// skips the backlog metrics. Keeps the core Backend contract minimal.
type Stater interface {
	Stats(ctx context.Context) (Stats, error)
}

// Resetter is an OPTIONAL bench helper: wipe the queue between sweep points.
// Not part of the production contract.
type Resetter interface {
	Reset(ctx context.Context) error
}
