package valkey_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
	"github.com/philosaether/pl-takehome-technical/internal/valkey"
)

// TestAckPagesLargePEL guards the paged PEL scan in the ack Lua (review fix): a
// single unit drained in one big batch puts >256 entries (the scan page size) in
// flight at once. A single-page scan would ack only the first page and orphan the
// rest; the paged scan must ack the whole through-seq prefix. Gated on PLQ_TEST_VALKEY.
func TestAckPagesLargePEL(t *testing.T) {
	addr := os.Getenv("PLQ_TEST_VALKEY")
	if addr == "" {
		t.Skip("set PLQ_TEST_VALKEY to run the Valkey large-PEL test")
	}
	ctx := context.Background()
	be, err := valkey.New(valkey.Options{Addrs: []string{addr}, DefaultThreshold: 1, DefaultMaxWait: time.Hour, MaxAttempts: 3})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer be.Close()
	if err := be.(queue.Resetter).Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	const n = 600 // > 2 pages of 256
	key := queue.WorkUnitKey{Workspace: "ws", Session: "s", Peer: "p"}
	for i := 0; i < n; i++ {
		if _, err := be.Enqueue(ctx, key, []byte("x"), 1); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	c, err := be.Claim(ctx, "w", time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim: %v (c=%v)", err, c)
	}
	tasks, err := be.Drain(ctx, c, n) // one big batch → PEL depth n
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if len(tasks) != n {
		t.Fatalf("drained %d, want %d", len(tasks), n)
	}
	held, err := be.Ack(ctx, c, tasks[n-1].Seq) // ack the whole prefix across pages
	if err != nil {
		t.Fatalf("ack: %v", err)
	}
	if held {
		t.Fatal("expected release after acking all tasks")
	}
	// Nothing orphaned: the unit is gone (tombstoned) and no tasks remain pending.
	if s, err := be.(queue.Stater).Stats(ctx); err != nil {
		t.Fatalf("stats: %v", err)
	} else if s.PendingTasks != 0 || s.TotalUnits != 0 {
		t.Fatalf("orphaned state after full ack: pending=%d total_units=%d", s.PendingTasks, s.TotalUnits)
	}
}
