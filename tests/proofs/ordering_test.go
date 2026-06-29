package proofs_test

import (
	"testing"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// TestOrderingUnderCrash is the headline ordering proof — the 3-of-10 scenario.
// One unit, tasks t0..t9. Worker A processes t0..t2 and acks them, then CRASHES
// (abandons the lease without acking t3..t9). The reaper reclaims the expired
// lease; Worker B drains from t3. The processing log must be exactly t0..t9, once
// each, in order — no acked task reprocessed, no task out of order, the in-flight
// task t3 redelivered (at-least-once). Runs against memory AND postgres.
func TestOrderingUnderCrash(t *testing.T) {
	forEachBackend(t, 1, time.Hour, func(t *testing.T, be queue.Backend) {
		defer be.Close()
		key := queue.WorkUnitKey{Workspace: "ws", Session: "s", Peer: "p"}
		const n = 10
		for i := 0; i < n; i++ {
			if _, err := be.Enqueue(ctx, key, []byte{byte(i)}, 1); err != nil {
				t.Fatalf("enqueue: %v", err)
			}
		}
		t.Logf("enqueued t0..t%d into one unit %s (FIFO, single ordered stream)", n-1, key)

		var log []int64

		// Worker A: short lease, processes & acks t0..t2, then crashes.
		ca := claimRetry(t, be, "A", time.Second)
		t.Logf("Worker A claimed the unit (exclusive, 1s lease)")
		batch, err := be.Drain(ctx, ca, n)
		if err != nil {
			t.Fatalf("A drain: %v", err)
		}
		for _, tk := range batch[:3] { // "process" t0,t1,t2
			log = append(log, tk.Seq)
		}
		if _, err := be.Ack(ctx, ca, 2); err != nil { // ack through t2
			t.Fatalf("A ack: %v", err)
		}
		t.Logf("Worker A processed & acked t0..t2 (3 of 10), then 💥 CRASHED holding t3..t9 (lease abandoned, never released)")
		// CRASH: A stops heartbeating and abandons t3..t9 (no further calls).

		// Reaper reclaims A's expired lease.
		reclaimed, _, err := be.ReapExpired(ctx, time.Now().Add(2*time.Second))
		if err != nil {
			t.Fatalf("reap: %v", err)
		}
		if reclaimed != 1 {
			t.Fatalf("reclaimed=%d, want 1 (the crashed worker's lease)", reclaimed)
		}
		t.Logf("lease expired → reaper reclaimed %d lease (A's); t3..t9 still pending and still ordered", reclaimed)

		// Worker B drains the rest, in order, starting at t3.
		cb := claimRetry(t, be, "B", time.Minute)
		t.Logf("Worker B claimed the reclaimed unit — redelivery resumes at t3 (not t0)")
		for {
			batch, err := be.Drain(ctx, cb, n)
			if err != nil {
				t.Fatalf("B drain: %v", err)
			}
			if len(batch) == 0 {
				break
			}
			for _, tk := range batch {
				log = append(log, tk.Seq)
			}
			held, err := be.Ack(ctx, cb, batch[len(batch)-1].Seq)
			if err != nil {
				t.Fatalf("B ack: %v", err)
			}
			if !held {
				break
			}
		}

		// The processing log must be exactly t0..t9, once each, in order.
		if len(log) != n {
			t.Fatalf("processed %d tasks, want %d (no dup, no loss): %v", len(log), n, log)
		}
		for i, s := range log {
			if s != int64(i) {
				t.Fatalf("out of order at %d: seq=%d (full: %v)", i, s, log)
			}
		}
		t.Logf("✓ PROVEN: processing log = %v — each task exactly once, in arrival order across the crash (t0..t2 never re-ran; t3 redelivered, never skipped)", log)
	})
}
