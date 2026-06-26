package memory_test

import (
	"testing"

	"github.com/philosaether/pl-takehome-technical/internal/conformance"
	"github.com/philosaether/pl-takehome-technical/internal/memory"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// The in-memory backend is the reference: it must pass the full conformance suite.
func TestConformance(t *testing.T) {
	conformance.Run(t, func(cfg conformance.Config) queue.Backend {
		return memory.New(memory.Options{
			DefaultThreshold: int64(cfg.Threshold),
			DefaultMaxWait:   cfg.MaxWait,
			MaxAttempts:      cfg.MaxAttempts,
		})
	})
}
