// Package valkey is the Path 2 driver (M3). Stubbed in M0: it satisfies
// queue.Backend via the embedded Unimplemented so the build-tagged wiring
// typechecks; New reports that the real implementation lands in M3.
package valkey

import (
	"errors"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// ErrNotImplemented marks the M0 stub.
var ErrNotImplemented = errors.New("valkey backend not implemented yet (M3)")

// Options configures the Valkey driver.
type Options struct {
	Addr string
}

// Backend is the Valkey-backed queue (M3): Streams + ZSETs + Lua via rueidis.
type Backend struct {
	queue.Unimplemented
}

// New will dial Valkey and load the Lua scripts in M3. For now it reports the stub.
func New(_ Options) (queue.Backend, error) {
	return nil, ErrNotImplemented
}
