package proofs_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/memory"
	"github.com/philosaether/pl-takehome-technical/internal/postgres"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

var ctx = context.Background()

// forEachBackend runs fn against the in-memory oracle (always) and Postgres (when
// PLQ_TEST_POSTGRES is set), each freshly reset.
func forEachBackend(t *testing.T, threshold int, maxWait time.Duration, fn func(t *testing.T, be queue.Backend)) {
	t.Run("memory", func(t *testing.T) {
		fn(t, memory.New(memory.Options{DefaultThreshold: int64(threshold), DefaultMaxWait: maxWait, MaxAttempts: 3}))
	})
	dsn := os.Getenv("PLQ_TEST_POSTGRES")
	if dsn == "" {
		t.Log("PLQ_TEST_POSTGRES unset — skipping postgres backend")
		return
	}
	t.Run("postgres", func(t *testing.T) {
		be, err := postgres.New(postgres.Options{DSN: dsn, DefaultThreshold: threshold, DefaultMaxWait: maxWait, MaxAttempts: 3})
		if err != nil {
			t.Fatalf("postgres: %v", err)
		}
		if r, ok := be.(queue.Resetter); ok {
			if err := r.Reset(ctx); err != nil {
				t.Fatalf("reset: %v", err)
			}
		}
		fn(t, be)
	})
}
