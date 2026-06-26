//go:build valkey

package main

import (
	"strings"

	"github.com/philosaether/pl-takehome-technical/internal/config"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
	"github.com/philosaether/pl-takehome-technical/internal/valkey"
)

// newBackend (-tags valkey) wires the Path 2 driver — only this driver is
// compiled into the binary. PLQ_VALKEY_ADDR is comma-split into shards (1 = the
// baseline; 2/4 = the linear-scaling sweep).
func newBackend(c config.Config) (queue.Backend, error) {
	var addrs []string
	for _, a := range strings.Split(c.ValkeyAddr, ",") {
		if a = strings.TrimSpace(a); a != "" {
			addrs = append(addrs, a)
		}
	}
	return valkey.New(valkey.Options{
		Addrs:            addrs,
		DefaultThreshold: c.DefaultThreshold,
		DefaultMaxWait:   c.DefaultMaxWait,
		MaxAttempts:      c.MaxAttempts,
	})
}
