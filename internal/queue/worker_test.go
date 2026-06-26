package queue_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/memory"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// TestWorkerDrainsInOrder is the M0 proof-of-life: a worker pool drains a unit's
// tasks exactly once, in seq order, across the in-memory oracle. Pre-stages the
// M2 ordering proof via the OnProcess hook.
func TestWorkerDrainsInOrder(t *testing.T) {
	// threshold=1: the unit stays eligible throughout the drain — this test isolates
	// in-order draining from the gate (the gate has its own test below).
	be := memory.New(memory.Options{DefaultThreshold: 1, DefaultMaxWait: time.Hour, MaxAttempts: 3})
	key := queue.WorkUnitKey{Workspace: "ws", Session: "s", Peer: "p"}

	const n = 10
	for i := 0; i < n; i++ {
		if _, err := be.Enqueue(context.Background(), key, []byte{byte(i)}, 20); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	var mu sync.Mutex
	var seen []int64
	cfg := queue.WorkerConfig{
		Lease:   time.Second,
		Batch:   3, // force multiple drain/ack cycles within the claim
		Backoff: queue.BackoffConfig{Min: time.Millisecond, Max: 10 * time.Millisecond},
		OnProcess: func(_ queue.WorkUnitKey, task queue.Task) {
			mu.Lock()
			seen = append(seen, task.Seq)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { _ = queue.NewPool(be, 4, cfg).Run(ctx); close(done) }()

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		got := len(seen)
		mu.Unlock()
		if got >= n {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout: processed %d/%d", got, n)
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != n {
		t.Fatalf("processed %d tasks, want exactly %d (no double-processing)", len(seen), n)
	}
	for i, s := range seen {
		if s != int64(i) {
			t.Fatalf("out of order at %d: seq=%d, want %d (full order: %v)", i, s, i, seen)
		}
	}
}

// TestGateBelowThreshold asserts a unit below T is never claimable until flushed.
func TestGateBelowThreshold(t *testing.T) {
	be := memory.New(memory.Options{DefaultThreshold: 100, DefaultMaxWait: time.Hour, MaxAttempts: 3})
	key := queue.WorkUnitKey{Workspace: "ws", Session: "s", Peer: "p"}
	if _, err := be.Enqueue(context.Background(), key, []byte("x"), 40); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// 40 < 100 → not eligible → Claim returns nothing.
	if u, _ := be.Claim(context.Background(), "w", time.Second); u != nil {
		t.Fatalf("claimed a sub-threshold unit: %+v", u)
	}
	// Flush age-cap fires → now claimable.
	if _, flushed, _ := be.ReapExpired(context.Background(), time.Now().Add(2*time.Hour)); flushed != 1 {
		t.Fatalf("flushed=%d, want 1", flushed)
	}
	if u, _ := be.Claim(context.Background(), "w", time.Second); u == nil {
		t.Fatal("unit not claimable after flush")
	}
}
