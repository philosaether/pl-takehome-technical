// Package memory is an in-process Backend implementation. It is the default
// (no-build-tag) driver: used for local dev, CI typechecking of the shared code,
// and as the correctness oracle the real drivers (Postgres M1, Valkey M3) must
// match. Correctness over performance — a single mutex guards all state.
package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

type unit struct {
	key           queue.WorkUnitKey
	tasks         []queue.Task // unacked tasks, in seq order (head = tasks[0])
	enqAt         []time.Time  // parallel to tasks: per-task enqueue time
	nextSeq       int64
	pendingCost   int64
	threshold     int64
	maxWait       time.Duration
	oldestPending time.Time
	flushDeadline time.Time
	eligible      bool
	claimedBy     queue.WorkerID
	lease         queue.LeaseToken
	leaseDur      time.Duration // lease window from the current claim (for ack-renew)
	leaseExpires  time.Time
	attempts      int // consecutive failures of the current head task
}

// DeadLetter is a task that exhausted its retry budget (poison).
type DeadLetter struct {
	Key    queue.WorkUnitKey
	Task   queue.Task
	Reason string
}

// Options configures the backend's defaults (per-tenant overrides are M1+).
type Options struct {
	DefaultThreshold int64
	DefaultMaxWait   time.Duration
	MaxAttempts      int
}

// Backend is the in-memory queue.
type Backend struct {
	mu           sync.Mutex
	units        map[queue.WorkUnitKey]*unit
	dlq          []DeadLetter
	leaseCounter int64
	threshold    int64
	maxWait      time.Duration
	maxAttempts  int
}

// New builds an in-memory backend. It always succeeds.
func New(o Options) *Backend {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	return &Backend{
		units:       map[queue.WorkUnitKey]*unit{},
		threshold:   o.DefaultThreshold,
		maxWait:     o.DefaultMaxWait,
		maxAttempts: o.MaxAttempts,
	}
}

var _ queue.Backend = (*Backend)(nil)

func (b *Backend) Enqueue(_ context.Context, key queue.WorkUnitKey, payload []byte, cost int64) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	u := b.units[key]
	if u == nil {
		u = &unit{key: key, threshold: b.threshold, maxWait: b.maxWait}
		b.units[key] = u
	}
	seq := u.nextSeq
	u.nextSeq++
	now := time.Now()
	if len(u.tasks) == 0 {
		u.oldestPending = now
		u.flushDeadline = now.Add(u.maxWait)
	}
	u.tasks = append(u.tasks, queue.Task{Seq: seq, Payload: payload, Cost: cost})
	u.enqAt = append(u.enqAt, now)
	u.pendingCost += cost
	if u.pendingCost >= u.threshold {
		u.eligible = true // I4 gate: cost crossed T
	}
	return seq, nil
}

func (b *Backend) Claim(_ context.Context, worker queue.WorkerID, lease time.Duration) (*queue.ClaimedUnit, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Age-fair: lowest flush_deadline among eligible & free units; ties broken by
	// key for determinism (the wu_key tiebreak from the fairness A1).
	var best *unit
	for _, u := range b.units {
		if !u.eligible || u.claimedBy != "" || len(u.tasks) == 0 {
			continue
		}
		if best == nil || u.flushDeadline.Before(best.flushDeadline) ||
			(u.flushDeadline.Equal(best.flushDeadline) && u.key.String() < best.key.String()) {
			best = u
		}
	}
	if best == nil {
		return nil, nil // nothing eligible — not an error
	}
	b.leaseCounter++
	tok := queue.LeaseToken(fmt.Sprintf("%s-%d", worker, b.leaseCounter))
	best.claimedBy = worker
	best.lease = tok
	best.leaseDur = lease
	best.leaseExpires = time.Now().Add(lease)
	return &queue.ClaimedUnit{Key: best.key, Worker: worker, Lease: tok, LeaseTill: best.leaseExpires}, nil
}

// held validates that the claim still owns the unit (exclusive-claim invariant).
func (b *Backend) held(c *queue.ClaimedUnit) (*unit, error) {
	u := b.units[c.Key]
	if u == nil || u.claimedBy != c.Worker || u.lease != c.Lease {
		return nil, queue.ErrLeaseLost
	}
	return u, nil
}

func (b *Backend) Drain(_ context.Context, c *queue.ClaimedUnit, max int) ([]queue.Task, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	u, err := b.held(c)
	if err != nil {
		return nil, err
	}
	n := max
	if n > len(u.tasks) {
		n = len(u.tasks)
	}
	out := make([]queue.Task, n)
	copy(out, u.tasks[:n]) // in-order, from the head
	return out, nil
}

func (b *Backend) Ack(_ context.Context, c *queue.ClaimedUnit, throughSeq int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	u, err := b.held(c)
	if err != nil {
		return false, err
	}
	// delete-on-ack: drop tasks through throughSeq from the head.
	i := 0
	for i < len(u.tasks) && u.tasks[i].Seq <= throughSeq {
		u.pendingCost -= u.tasks[i].Cost
		i++
	}
	u.tasks = u.tasks[i:]
	u.enqAt = u.enqAt[i:]
	u.attempts = 0
	if len(u.tasks) == 0 {
		delete(b.units, u.key) // tombstone (oracle drops it; revived key starts fresh)
		return false, nil
	}
	b.recompute(u)
	if u.eligible {
		// Keep the claim — and renew the lease, so a unit whose batches each finish
		// faster than the heartbeat interval can't have its lease expire mid-drain.
		u.leaseExpires = time.Now().Add(u.leaseDur)
		return true, nil
	}
	// dropped below T and not flushed → re-buffer (release).
	u.claimedBy = ""
	u.lease = ""
	return false, nil
}

// recompute refreshes a unit's head-derived metadata after the task list is
// mutated from the front. Assumes len(u.tasks) > 0. Per-head flush: a unit stays
// eligible iff it's still at/above T OR its *new* head has already aged past the
// flush deadline — never a sticky flag (so draining an aged head doesn't drag the
// newer, not-yet-aged work behind it out of the buffer).
func (b *Backend) recompute(u *unit) {
	u.oldestPending = u.enqAt[0]
	u.flushDeadline = u.oldestPending.Add(u.maxWait)
	u.eligible = u.pendingCost >= u.threshold ||
		(!u.flushDeadline.IsZero() && !time.Now().Before(u.flushDeadline))
}

func (b *Backend) Release(_ context.Context, c *queue.ClaimedUnit) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	u, err := b.held(c)
	if err != nil {
		return err
	}
	u.claimedBy = ""
	u.lease = ""
	return nil
}

func (b *Backend) Heartbeat(_ context.Context, c *queue.ClaimedUnit, extend time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	u, err := b.held(c)
	if err != nil {
		return err
	}
	u.leaseExpires = time.Now().Add(extend)
	return nil
}

func (b *Backend) Fail(_ context.Context, c *queue.ClaimedUnit, seq int64, reason string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	u, err := b.held(c)
	if err != nil {
		return err
	}
	u.attempts++
	if u.attempts >= b.maxAttempts && len(u.tasks) > 0 && u.tasks[0].Seq == seq {
		// poison head → DLQ, then unblock the unit.
		b.dlq = append(b.dlq, DeadLetter{Key: u.key, Task: u.tasks[0], Reason: reason})
		u.pendingCost -= u.tasks[0].Cost
		u.tasks = u.tasks[1:]
		u.enqAt = u.enqAt[1:]
		u.attempts = 0
		if len(u.tasks) == 0 {
			delete(b.units, u.key)
			return nil
		}
		b.recompute(u)
	}
	// Release so the unit can be retried/redelivered in order.
	u.claimedBy = ""
	u.lease = ""
	return nil
}

func (b *Backend) ReapExpired(_ context.Context, now time.Time) (int, int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var reclaimed, flushed int
	for _, u := range b.units {
		if u.claimedBy != "" && now.After(u.leaseExpires) {
			u.claimedBy = "" // I5: reclaim crashed worker's lease
			u.lease = ""
			reclaimed++
		}
		if u.claimedBy == "" && !u.eligible && len(u.tasks) > 0 &&
			!u.flushDeadline.IsZero() && !now.Before(u.flushDeadline) {
			u.eligible = true // flush age-cap fired (the oldest task aged out)
			flushed++
		}
	}
	return reclaimed, flushed, nil
}

func (b *Backend) Close() error { return nil }

// DeadLetters returns a snapshot of the DLQ (for tests/proofs).
func (b *Backend) DeadLetters() []DeadLetter {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]DeadLetter, len(b.dlq))
	copy(out, b.dlq)
	return out
}
