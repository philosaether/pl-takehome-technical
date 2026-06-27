//go:build postgres

package main

import (
	"strings"

	"github.com/philosaether/pl-takehome-technical/internal/config"
	"github.com/philosaether/pl-takehome-technical/internal/postgres"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// newBackend (-tags postgres) wires the Path 1 driver — only this driver is
// compiled into the binary. PLQ_POSTGRES_DSN is comma-split into shards (1 = the
// baseline; 2/4/8 = the sharded sweep), same shape as the Valkey addr list.
func newBackend(c config.Config) (queue.Backend, error) {
	var dsns []string
	for _, d := range strings.Split(c.PostgresDSN, ",") {
		if d = strings.TrimSpace(d); d != "" {
			dsns = append(dsns, d)
		}
	}
	return postgres.New(postgres.Options{
		DSNs:             dsns,
		DefaultThreshold: int(c.DefaultThreshold),
		DefaultMaxWait:   c.DefaultMaxWait,
		MaxAttempts:      c.MaxAttempts,
	})
}
