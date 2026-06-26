package loadgen

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// Metrics is the shared telemetry sink. It implements queue.Recorder (worker side)
// and is also incremented by the producers (Enqueued) and reaper (LeaseExpired).
type Metrics struct {
	Enqueued     atomic.Int64
	Acked        atomic.Int64
	Claims       atomic.Int64
	ClaimEmpty   atomic.Int64
	LeaseExpired atomic.Int64

	claimLat *hist
	loopLat  *hist
}

var _ queue.Recorder = (*Metrics)(nil)

func NewMetrics() *Metrics {
	return &Metrics{claimLat: newHist(), loopLat: newHist()}
}

// queue.Recorder
func (m *Metrics) Claim(d time.Duration, gotUnit bool) {
	m.Claims.Add(1)
	if !gotUnit {
		m.ClaimEmpty.Add(1)
	}
	m.claimLat.add(d)
}
func (m *Metrics) Ack(tasks int)        { m.Acked.Add(int64(tasks)) }
func (m *Metrics) Loop(d time.Duration) { m.loopLat.add(d) }

func (m *Metrics) ClaimP99() time.Duration { return m.claimLat.quantile(0.99) }
func (m *Metrics) LoopP99() time.Duration  { return m.loopLat.quantile(0.99) }
func (m *Metrics) LoopSamples() int64      { return m.loopLat.total() }

// hist is a coarse bucketed latency histogram (no external dependency). p99 lands
// to within a bucket — plenty for the throughput/latency graphs.
type hist struct {
	mu     sync.Mutex
	counts []int64
	n      int64
}

// bucket upper bounds, in seconds (50µs … 10s).
var histBounds = []float64{
	50e-6, 100e-6, 200e-6, 500e-6,
	1e-3, 2e-3, 5e-3, 10e-3, 20e-3, 50e-3, 100e-3, 200e-3, 500e-3,
	1, 2, 5, 10,
}

func newHist() *hist { return &hist{counts: make([]int64, len(histBounds)+1)} }

func (h *hist) add(d time.Duration) {
	s := d.Seconds()
	i := 0
	for i < len(histBounds) && s > histBounds[i] {
		i++
	}
	h.mu.Lock()
	h.counts[i]++
	h.n++
	h.mu.Unlock()
}

func (h *hist) quantile(q float64) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n == 0 {
		return 0
	}
	target := int64(q * float64(h.n))
	var cum int64
	for i, c := range h.counts {
		cum += c
		if cum >= target {
			if i >= len(histBounds) {
				return time.Duration(histBounds[len(histBounds)-1] * 2 * float64(time.Second))
			}
			return time.Duration(histBounds[i] * float64(time.Second))
		}
	}
	return time.Duration(histBounds[len(histBounds)-1] * float64(time.Second))
}

func (h *hist) total() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}
