//go:build !postgres && !valkey

package main

import (
	"github.com/philosaether/pl-takehome-technical/internal/config"
	"github.com/philosaether/pl-takehome-technical/internal/memory"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// newBackend (default build) returns the in-memory backend — dev, CI, and the
// proofs' correctness oracle. In-process only; not for the multi-container path.
func newBackend(c config.Config) (queue.Backend, error) {
	return memory.New(memory.Options{
		DefaultThreshold: c.DefaultThreshold,
		DefaultMaxWait:   c.DefaultMaxWait,
		MaxAttempts:      c.MaxAttempts,
	}), nil
}
