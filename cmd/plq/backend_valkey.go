//go:build valkey

package main

import (
	"github.com/philosaether/pl-takehome-technical/internal/config"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
	"github.com/philosaether/pl-takehome-technical/internal/valkey"
)

// newBackend (-tags valkey) wires the Path 2 driver — only this driver is
// compiled into the binary.
func newBackend(c config.Config) (queue.Backend, error) {
	return valkey.New(valkey.Options{Addr: c.ValkeyAddr})
}
