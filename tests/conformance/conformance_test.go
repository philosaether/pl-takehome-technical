package conformance_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/philosaether/pl-takehome-technical/internal/memory"
	"github.com/philosaether/pl-takehome-technical/internal/postgres"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
	"github.com/philosaether/pl-takehome-technical/internal/valkey"
	"github.com/philosaether/pl-takehome-technical/tests/conformance"
)

// The in-memory backend is the reference: it must pass the full conformance suite.
func TestMemory(t *testing.T) {
	conformance.Run(t, func(cfg conformance.Config) queue.Backend {
		return memory.New(memory.Options{
			DefaultThreshold: int64(cfg.Threshold),
			DefaultMaxWait:   cfg.MaxWait,
			MaxAttempts:      cfg.MaxAttempts,
		})
	})
}

// The Postgres driver must produce identical observable behavior to the oracle.
// Gated behind PLQ_TEST_POSTGRES so the default `go test` stays hermetic.
//
//	PLQ_TEST_POSTGRES=postgres://plq:plq@localhost:5433/plq?sslmode=disable go test ./tests/conformance/
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("PLQ_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("set PLQ_TEST_POSTGRES to run the Postgres conformance suite")
	}
	ctx := context.Background()

	// ensure the schema exists, then a shared pool for truncation between scenarios.
	if be, err := postgres.New(postgres.Options{DSN: dsn, DefaultThreshold: 1, DefaultMaxWait: time.Second, MaxAttempts: 3}); err != nil {
		t.Fatalf("schema setup: %v", err)
	} else {
		be.Close()
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	conformance.Run(t, func(cfg conformance.Config) queue.Backend {
		if _, err := pool.Exec(ctx, `TRUNCATE work_units, tasks, dead_letters, tenant_config`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
		be, err := postgres.New(postgres.Options{
			DSN:              dsn,
			DefaultThreshold: cfg.Threshold,
			DefaultMaxWait:   cfg.MaxWait,
			MaxAttempts:      cfg.MaxAttempts,
		})
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		return be
	})
}

// The Valkey driver must produce identical observable behavior to the oracle, at
// N=1 (single primary = the architecture's single-instance design). Gated behind
// PLQ_TEST_VALKEY so the default `go test` stays hermetic.
//
//	PLQ_TEST_VALKEY=localhost:6379 go test ./tests/conformance/
func TestValkey(t *testing.T) {
	addr := os.Getenv("PLQ_TEST_VALKEY")
	if addr == "" {
		t.Skip("set PLQ_TEST_VALKEY to run the Valkey conformance suite")
	}
	conformance.Run(t, func(cfg conformance.Config) queue.Backend {
		be, err := valkey.New(valkey.Options{
			Addrs:            []string{addr},
			DefaultThreshold: int64(cfg.Threshold),
			DefaultMaxWait:   cfg.MaxWait,
			MaxAttempts:      cfg.MaxAttempts,
		})
		if err != nil {
			t.Fatalf("new: %v", err)
		}
		if err := be.(queue.Resetter).Reset(context.Background()); err != nil { // FLUSHDB between scenarios
			t.Fatalf("reset: %v", err)
		}
		return be
	})
}
