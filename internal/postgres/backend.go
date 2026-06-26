// Package postgres is the Path 1 driver (M1). Stubbed in M0: it satisfies
// queue.Backend via the embedded Unimplemented so the build-tagged wiring
// typechecks; New reports that the real implementation lands in M1.
package postgres

import (
	"errors"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// ErrNotImplemented marks the M0 stub.
var ErrNotImplemented = errors.New("postgres backend not implemented yet (M1)")

// Options configures the Postgres driver.
type Options struct {
	DSN        string
	Partitions int
}

// Backend is the Postgres-backed queue (M1).
type Backend struct {
	queue.Unimplemented
}

// New will open a pgx pool and run migrations in M1. For now it reports the stub.
func New(_ Options) (queue.Backend, error) {
	return nil, ErrNotImplemented
}
