// Package queue is the backend-agnostic contract: the Backend interface plus the
// shared worker loop. It imports no driver — drivers depend on this package, never
// the reverse. If queue ever imports internal/postgres or internal/valkey, the
// apples-to-apples contract has leaked.
package queue

import "time"

// WorkUnitKey identifies a work unit. Workspace is the shard + fairness seam (A1
// on both designs); the full triple is the unit identity for exclusive claim.
type WorkUnitKey struct {
	Workspace string
	Session   string
	Peer      string
}

// String renders the key as workspace/session/peer — used for deterministic
// tie-breaking and logging.
func (k WorkUnitKey) String() string {
	return k.Workspace + "/" + k.Session + "/" + k.Peer
}

type (
	// WorkerID names a worker for lease ownership.
	WorkerID string
	// LeaseToken is an opaque handle the backend validates on ack/heartbeat/release.
	LeaseToken string
)

// Task is one item in a work unit's per-unit FIFO.
type Task struct {
	Seq     int64 // per-unit monotonic sequence, assigned by Enqueue (I1 ordering)
	Payload []byte
	Cost    int64 // token cost; sums toward the unit's threshold T (I3 gate)
}

// ClaimedUnit is an exclusive, time-leased hold on one work unit.
type ClaimedUnit struct {
	Key       WorkUnitKey
	Worker    WorkerID
	Lease     LeaseToken
	LeaseTill time.Time
}
