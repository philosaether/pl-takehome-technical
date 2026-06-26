// Package conformance is the shared contract test: the same behavioral scenarios
// run against any queue.Backend. The in-memory backend is the reference; the
// Postgres driver must produce identical observable behavior. This is the
// apples-to-apples *correctness* guarantee, and it pins the semantics (e.g.
// per-head flush) for every backend at once.
package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// Config is what a scenario asks the factory to build.
type Config struct {
	Threshold   int
	MaxWait     time.Duration
	MaxAttempts int
}

// Factory builds a fresh, empty backend with the given config.
type Factory func(Config) queue.Backend

var ctx = context.Background()

// Run executes the full suite against the backends produced by newBackend.
func Run(t *testing.T, newBackend Factory) {
	t.Run("HappyPath_GateClaimDrainAck", func(t *testing.T) { happyPath(t, newBackend) })
	t.Run("GateBelowThreshold", func(t *testing.T) { gateBelowThreshold(t, newBackend) })
	t.Run("InOrderAcrossBatches", func(t *testing.T) { inOrderAcrossBatches(t, newBackend) })
	t.Run("KeepThenRebuffer", func(t *testing.T) { keepThenRebuffer(t, newBackend) })
	t.Run("FlushIsPerHead", func(t *testing.T) { flushIsPerHead(t, newBackend) })
	t.Run("LeaseReclaimRedelivers", func(t *testing.T) { leaseReclaimRedelivers(t, newBackend) })
	t.Run("PoisonToDLQ", func(t *testing.T) { poisonToDLQ(t, newBackend) })
}

var key = queue.WorkUnitKey{Workspace: "ws", Session: "s", Peer: "p"}

func ok(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func enqueueN(t *testing.T, be queue.Backend, n int, cost int64) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := be.Enqueue(ctx, key, []byte("x"), cost)
		ok(t, err, "enqueue")
	}
}

func happyPath(t *testing.T, newBackend Factory) {
	be := newBackend(Config{Threshold: 100, MaxWait: time.Hour, MaxAttempts: 3})
	defer be.Close()
	enqueueN(t, be, 5, 30) // 150 >= 100 → eligible

	c, err := be.Claim(ctx, "w", time.Minute)
	ok(t, err, "claim")
	if c == nil {
		t.Fatal("expected a claim, got nil")
	}
	tasks, err := be.Drain(ctx, c, 10)
	ok(t, err, "drain")
	if len(tasks) != 5 {
		t.Fatalf("drained %d, want 5", len(tasks))
	}
	for i, tk := range tasks {
		if tk.Seq != int64(i) {
			t.Fatalf("task %d has seq %d (order: %v)", i, tk.Seq, seqs(tasks))
		}
	}
	held, err := be.Ack(ctx, c, tasks[4].Seq)
	ok(t, err, "ack")
	if held {
		t.Fatal("expected released after full drain")
	}
	if c2, _ := be.Claim(ctx, "w", time.Minute); c2 != nil {
		t.Fatalf("claimed a drained unit: %+v", c2)
	}
}

func gateBelowThreshold(t *testing.T, newBackend Factory) {
	be := newBackend(Config{Threshold: 100, MaxWait: time.Hour, MaxAttempts: 3})
	defer be.Close()
	enqueueN(t, be, 1, 40) // 40 < 100

	if c, _ := be.Claim(ctx, "w", time.Minute); c != nil {
		t.Fatalf("claimed a sub-threshold unit: %+v", c)
	}
	_, flushed, err := be.ReapExpired(ctx, time.Now().Add(2*time.Hour))
	ok(t, err, "reap")
	if flushed != 1 {
		t.Fatalf("flushed=%d, want 1", flushed)
	}
	if c, _ := be.Claim(ctx, "w", time.Minute); c == nil {
		t.Fatal("unit not claimable after flush")
	}
}

func inOrderAcrossBatches(t *testing.T, newBackend Factory) {
	be := newBackend(Config{Threshold: 1, MaxWait: time.Hour, MaxAttempts: 3})
	defer be.Close()
	const n = 10
	enqueueN(t, be, n, 1) // threshold 1 → stays eligible through the drain

	c, err := be.Claim(ctx, "w", time.Minute)
	ok(t, err, "claim")
	if c == nil {
		t.Fatal("expected a claim")
	}
	var got []int64
	for {
		tasks, err := be.Drain(ctx, c, 3) // batch < n → multiple drain/ack cycles
		ok(t, err, "drain")
		if len(tasks) == 0 {
			ok(t, be.Release(ctx, c), "release")
			break
		}
		for _, tk := range tasks {
			got = append(got, tk.Seq)
		}
		held, err := be.Ack(ctx, c, tasks[len(tasks)-1].Seq)
		ok(t, err, "ack")
		if !held {
			break
		}
	}
	if len(got) != n {
		t.Fatalf("processed %d tasks, want %d (no double-processing)", len(got), n)
	}
	for i, s := range got {
		if s != int64(i) {
			t.Fatalf("out of order at %d: seq=%d (full: %v)", i, s, got)
		}
	}
}

func keepThenRebuffer(t *testing.T, newBackend Factory) {
	be := newBackend(Config{Threshold: 100, MaxWait: time.Hour, MaxAttempts: 3})
	defer be.Close()
	enqueueN(t, be, 5, 30) // 150 >= 100

	c, err := be.Claim(ctx, "w", time.Minute)
	ok(t, err, "claim")
	tasks, err := be.Drain(ctx, c, 3) // drain seq 0,1,2 (cost 90) → remaining 60 < 100
	ok(t, err, "drain")
	held, err := be.Ack(ctx, c, tasks[2].Seq)
	ok(t, err, "ack")
	if held {
		t.Fatal("expected release: remainder (60) below T and not aged")
	}
	if c2, _ := be.Claim(ctx, "w", time.Minute); c2 != nil {
		t.Fatal("re-buffered unit should not be claimable")
	}
	enqueueN(t, be, 2, 30) // +60 → 120 >= 100 → eligible again
	if c3, _ := be.Claim(ctx, "w", time.Minute); c3 == nil {
		t.Fatal("unit should be claimable after crossing T again")
	}
}

// flushIsPerHead pins the resolved semantics: flushing the aged head drains only
// that head; the newer, not-yet-aged tail re-buffers. (A sticky-flush backend
// would keep the whole unit eligible and FAIL this.)
func flushIsPerHead(t *testing.T, newBackend Factory) {
	be := newBackend(Config{Threshold: 1000, MaxWait: 10 * time.Second, MaxAttempts: 3})
	defer be.Close()
	enqueueN(t, be, 2, 1) // both far below T(1000); same unit, head=seq0

	// force the head past its flush deadline
	_, flushed, err := be.ReapExpired(ctx, time.Now().Add(11*time.Second))
	ok(t, err, "reap")
	if flushed != 1 {
		t.Fatalf("flushed=%d, want 1", flushed)
	}
	c, err := be.Claim(ctx, "w", time.Minute)
	ok(t, err, "claim")
	if c == nil {
		t.Fatal("flushed unit should be claimable")
	}
	tasks, err := be.Drain(ctx, c, 1) // drain ONLY the aged head
	ok(t, err, "drain")
	if len(tasks) != 1 || tasks[0].Seq != 0 {
		t.Fatalf("expected head seq 0, got %v", seqs(tasks))
	}
	held, err := be.Ack(ctx, c, tasks[0].Seq)
	ok(t, err, "ack")
	if held {
		t.Fatal("per-head: tail (not yet aged, below T) must re-buffer, not stay held")
	}
	if c2, _ := be.Claim(ctx, "w", time.Minute); c2 != nil {
		t.Fatal("per-head: tail must not be claimable until it ages or crosses T")
	}
}

func leaseReclaimRedelivers(t *testing.T, newBackend Factory) {
	be := newBackend(Config{Threshold: 1, MaxWait: time.Hour, MaxAttempts: 3})
	defer be.Close()
	enqueueN(t, be, 3, 1) // seq 0,1,2

	c, err := be.Claim(ctx, "w", time.Second)
	ok(t, err, "claim")
	tasks, err := be.Drain(ctx, c, 3)
	ok(t, err, "drain")
	if _, err := be.Ack(ctx, c, tasks[0].Seq); err != nil { // ack only seq 0
		t.Fatalf("ack: %v", err)
	}
	// worker "crashes" holding seq 1,2 — lease expires, reaper reclaims.
	reclaimed, _, err := be.ReapExpired(ctx, time.Now().Add(2*time.Second))
	ok(t, err, "reap")
	if reclaimed != 1 {
		t.Fatalf("reclaimed=%d, want 1", reclaimed)
	}
	c2, err := be.Claim(ctx, "w2", time.Minute)
	ok(t, err, "reclaim-claim")
	if c2 == nil {
		t.Fatal("reclaimed unit should be claimable")
	}
	tasks2, err := be.Drain(ctx, c2, 3)
	ok(t, err, "drain2")
	if len(tasks2) != 2 || tasks2[0].Seq != 1 { // 0 acked → gone; redeliver from 1, in order
		t.Fatalf("expected redelivery from seq 1, got %v", seqs(tasks2))
	}
}

func poisonToDLQ(t *testing.T, newBackend Factory) {
	be := newBackend(Config{Threshold: 1, MaxWait: time.Hour, MaxAttempts: 2})
	defer be.Close()
	enqueueN(t, be, 2, 1) // seq 0,1; seq 0 is poison

	for attempt := 1; attempt <= 2; attempt++ {
		c, err := be.Claim(ctx, "w", time.Minute)
		ok(t, err, "claim")
		if c == nil {
			t.Fatalf("attempt %d: expected a claim", attempt)
		}
		tasks, err := be.Drain(ctx, c, 10)
		ok(t, err, "drain")
		if tasks[0].Seq != 0 {
			t.Fatalf("attempt %d: head should be seq 0, got %v", attempt, seqs(tasks))
		}
		ok(t, be.Fail(ctx, c, 0, "boom"), "fail")
	}
	// seq 0 hit max_attempts → DLQ; unit unblocks with head=seq1.
	c, err := be.Claim(ctx, "w", time.Minute)
	ok(t, err, "claim after dlq")
	if c == nil {
		t.Fatal("unit should be claimable after poison head DLQ'd")
	}
	tasks, err := be.Drain(ctx, c, 10)
	ok(t, err, "drain after dlq")
	if len(tasks) != 1 || tasks[0].Seq != 1 {
		t.Fatalf("expected only seq 1 to remain, got %v", seqs(tasks))
	}
}

func seqs(tasks []queue.Task) []int64 {
	out := make([]int64, len(tasks))
	for i, t := range tasks {
		out[i] = t.Seq
	}
	return out
}
