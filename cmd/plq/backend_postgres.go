//go:build postgres

package main

import (
	"github.com/philosaether/pl-takehome-technical/internal/config"
	"github.com/philosaether/pl-takehome-technical/internal/postgres"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// newBackend (-tags postgres) wires the Path 1 driver — only this driver is
// compiled into the binary.
func newBackend(c config.Config) (queue.Backend, error) {
	return postgres.New(postgres.Options{DSN: c.PostgresDSN, Partitions: c.PostgresParts})
}
