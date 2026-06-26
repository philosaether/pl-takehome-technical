package proofs_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/memory"
	"github.com/philosaether/pl-takehome-technical/internal/postgres"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
	"github.com/philosaether/pl-takehome-technical/internal/valkey"
)

var ctx = context.Background()

// claimRetry models how a real worker claims: poll until a unit comes back. A
// single Claim can legitimately return nil transiently (e.g. SKIP LOCKED stepping
// over a row another txn briefly holds); the worker loop retries with backoff, so
// the test must too — asserting the *first* claim succeeds is stricter than the
// contract and makes the proof flaky.
func claimRetry(t *testing.T, be queue.Backend, worker queue.WorkerID, lease time.Duration) *queue.ClaimedUnit {
	t.Helper()
	for i := 0; i < 100; i++ {
		c, err := be.Claim(ctx, worker, lease)
		if err != nil {
			t.Fatalf("%s claim: %v", worker, err)
		}
		if c != nil {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: no unit claimable after retries", worker)
	return nil
}

// forEachBackend runs fn against the in-memory oracle (always) and Postgres (when
// PLQ_TEST_POSTGRES is set), each freshly reset.
func forEachBackend(t *testing.T, threshold int, maxWait time.Duration, fn func(t *testing.T, be queue.Backend)) {
	t.Run("memory", func(t *testing.T) {
		fn(t, memory.New(memory.Options{DefaultThreshold: int64(threshold), DefaultMaxWait: maxWait, MaxAttempts: 3}))
	})
	if dsn := os.Getenv("PLQ_TEST_POSTGRES"); dsn == "" {
		t.Log("PLQ_TEST_POSTGRES unset — skipping postgres backend")
	} else {
		t.Run("postgres", func(t *testing.T) {
			be, err := postgres.New(postgres.Options{DSN: dsn, DefaultThreshold: threshold, DefaultMaxWait: maxWait, MaxAttempts: 3})
			if err != nil {
				t.Fatalf("postgres: %v", err)
			}
			resetAndRun(t, be, fn)
		})
	}

	if addr := os.Getenv("PLQ_TEST_VALKEY"); addr == "" {
		t.Log("PLQ_TEST_VALKEY unset — skipping valkey backend")
	} else {
		t.Run("valkey", func(t *testing.T) {
			be, err := valkey.New(valkey.Options{Addrs: []string{addr}, DefaultThreshold: int64(threshold), DefaultMaxWait: maxWait, MaxAttempts: 3})
			if err != nil {
				t.Fatalf("valkey: %v", err)
			}
			resetAndRun(t, be, fn)
		})
	}
}

func resetAndRun(t *testing.T, be queue.Backend, fn func(t *testing.T, be queue.Backend)) {
	if r, ok := be.(queue.Resetter); ok {
		if err := r.Reset(ctx); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
	fn(t, be)
}
