package queue

import (
	"context"
	"errors"
	"time"
)

// ErrUnimplemented is returned by the Unimplemented stub methods.
var ErrUnimplemented = errors.New("queue: operation not implemented")

// Unimplemented satisfies Backend with stubs that all return ErrUnimplemented.
// Drivers embed it during incremental build-out (M1/M3) so the type satisfies the
// interface before every method is written; each real method shadows the stub.
type Unimplemented struct{}

func (Unimplemented) Enqueue(context.Context, WorkUnitKey, []byte, int64) (int64, error) {
	return 0, ErrUnimplemented
}
func (Unimplemented) Claim(context.Context, WorkerID, time.Duration) (*ClaimedUnit, error) {
	return nil, ErrUnimplemented
}
func (Unimplemented) Drain(context.Context, *ClaimedUnit, int) ([]Task, error) {
	return nil, ErrUnimplemented
}
func (Unimplemented) Ack(context.Context, *ClaimedUnit, int64) (bool, error) {
	return false, ErrUnimplemented
}
func (Unimplemented) Release(context.Context, *ClaimedUnit) error { return ErrUnimplemented }
func (Unimplemented) Heartbeat(context.Context, *ClaimedUnit, time.Duration) error {
	return ErrUnimplemented
}
func (Unimplemented) Fail(context.Context, *ClaimedUnit, int64, string) error {
	return ErrUnimplemented
}
func (Unimplemented) ReapExpired(context.Context, time.Time) (int, int, error) {
	return 0, 0, ErrUnimplemented
}
func (Unimplemented) Close() error { return nil }
