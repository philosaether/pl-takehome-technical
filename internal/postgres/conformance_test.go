package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/philosaether/pl-takehome-technical/internal/conformance"
	"github.com/philosaether/pl-takehome-technical/internal/postgres"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// The Postgres driver must produce identical observable behavior to the oracle.
// Gated behind PLQ_TEST_POSTGRES so the default `go test` stays hermetic.
//
//	PLQ_TEST_POSTGRES=postgres://plq:plq@localhost:5432/plq?sslmode=disable go test ./internal/postgres/
func TestConformance(t *testing.T) {
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
