package queue

import (
	"context"
	"errors"
	"time"
)

// ErrLeaseLost is returned by Drain/Ack/Release/Heartbeat/Fail when the lease no
// longer belongs to the caller (reclaimed by the reaper or reassigned).
var ErrLeaseLost = errors.New("queue: lease lost (unit reclaimed or reassigned)")

// Backend is the apples-to-apples contract. Postgres (M1) and Valkey (M3) each
// implement it; the shared worker loop and load generator depend ONLY on this.
// Every method maps 1:1 onto an operation in the accepted designs.
type Backend interface {
	// Enqueue appends a task to key's FIFO, creating the unit implicitly on first
	// enqueue, maintaining the pending_cost aggregate + flush_deadline. Assigns and
	// returns the per-unit seq. (PG §Enqueue / Valkey enqueue Lua; I1·I3.)
	Enqueue(ctx context.Context, key WorkUnitKey, payload []byte, cost int64) (seq int64, err error)

	// Claim atomically leases the lowest-flush_deadline eligible unit (age-fair).
	// Returns (nil, nil) when nothing is eligible — not an error. Enforces the gate:
	// only eligible units are claimable. (PG §Claim / Valkey claim Lua; I2·I4.)
	Claim(ctx context.Context, worker WorkerID, lease time.Duration) (*ClaimedUnit, error)

	// Drain returns up to max unacked tasks of a claimed unit, in seq order. (I1.)
	Drain(ctx context.Context, unit *ClaimedUnit, max int) ([]Task, error)

	// Ack marks tasks through throughSeq processed (delete-on-ack) and atomically
	// EITHER keeps the lease (unit still has eligible work) OR releases it. Returns
	// whether the unit is still held by this worker. (PG §Drain+Ack / Valkey ack Lua.)
	Ack(ctx context.Context, unit *ClaimedUnit, throughSeq int64) (stillHeld bool, err error)

	// Release relinquishes the lease without acking (clean give-up).
	Release(ctx context.Context, unit *ClaimedUnit) error

	// Heartbeat extends the lease for a long-running batch.
	Heartbeat(ctx context.Context, unit *ClaimedUnit, extend time.Duration) error

	// Fail records a head-task failure: increments attempts, routes to DLQ past
	// MaxAttempts (poison), releases the unit. (PG §Failure modes / Valkey PEL.)
	Fail(ctx context.Context, unit *ClaimedUnit, seq int64, reason string) error

	// ReapExpired reclaims expired leases (crash recovery, I5) and promotes
	// flush-eligible units (age-cap). Timer-driven; also safe to call lazily.
	ReapExpired(ctx context.Context, now time.Time) (reclaimed, flushed int, err error)

	Close() error
}
