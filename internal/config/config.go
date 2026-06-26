// Package config is the volatility-split knob surface: tunables live here, the
// stable logic in internal/queue depends on the WorkerConfig this produces. All
// values come from PLQ_* environment variables so the same binary is retuned
// without a rebuild.
package config

import (
	"os"
	"strconv"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// Config is the full tunable surface. Backend selection is also compiled-in via
// build tag; Backend here is informational/for logging.
type Config struct {
	Backend string // "memory" | "postgres" | "valkey" (matches the build tag)
	Workers int    // worker-pool size — the sweep knob (1/10/100/1000)

	// Shared tunables — both backends honor these identically (apples-to-apples).
	Lease            time.Duration
	Batch            int
	DefaultThreshold int64
	DefaultMaxWait   time.Duration
	MaxAttempts      int
	PollMin          time.Duration
	PollMax          time.Duration
	Process          queue.ProcessModel // the cutover axis (zero|fixed|cost)

	// Backend-specific (the volatile, per-path part).
	PostgresDSN   string
	PostgresParts int
	ValkeyAddr    string

	// Loadgen knobs (M0 placeholder workload; real Zipfian producers are M2).
	LoadKeys  int
	LoadTasks int
	LoadCost  int64

	// M2 load harness.
	Seed       int64
	Producers  int
	Duration   time.Duration
	Warmup     time.Duration
	WorkingSet int
	ZipfS      float64
	BirthRate  float64
	CostLo     int64
	CostHi     int64
	ResultsDir string
}

// Load reads configuration from PLQ_* env vars, applying defaults.
func Load() Config {
	return Config{
		Backend:          env("PLQ_BACKEND", "memory"),
		Workers:          atoi("PLQ_WORKERS", 10),
		Lease:            dur("PLQ_LEASE", 30*time.Second),
		Batch:            atoi("PLQ_BATCH", 64),
		DefaultThreshold: atoi64("PLQ_THRESHOLD", 1000),
		DefaultMaxWait:   dur("PLQ_MAX_WAIT", 5*time.Second),
		MaxAttempts:      atoi("PLQ_MAX_ATTEMPTS", 3),
		PollMin:          dur("PLQ_POLL_MIN", time.Millisecond),
		PollMax:          dur("PLQ_POLL_MAX", time.Second),
		Process: queue.ProcessModel{
			Kind:    env("PLQ_PROCESS", "zero"),
			Base:    dur("PLQ_PROCESS_BASE", 0),
			PerCost: dur("PLQ_PROCESS_PERCOST", 0),
		},
		PostgresDSN:   env("PLQ_POSTGRES_DSN", ""),
		PostgresParts: atoi("PLQ_POSTGRES_PARTS", 2),
		ValkeyAddr:    env("PLQ_VALKEY_ADDR", ""),
		LoadKeys:      atoi("PLQ_LOAD_KEYS", 100),
		LoadTasks:     atoi("PLQ_LOAD_TASKS", 10000),
		LoadCost:      atoi64("PLQ_LOAD_COST", 100),

		Seed:       atoi64("PLQ_SEED", 1),
		Producers:  atoi("PLQ_PRODUCERS", 8),
		Duration:   dur("PLQ_DURATION", 30*time.Second),
		Warmup:     dur("PLQ_WARMUP", 5*time.Second),
		WorkingSet: atoi("PLQ_WORKING_SET", 1000),
		ZipfS:      atof("PLQ_ZIPF_S", 1.2),
		BirthRate:  atof("PLQ_BIRTH_RATE", 0.02),
		CostLo:     atoi64("PLQ_COST_LO", 10),
		CostHi:     atoi64("PLQ_COST_HI", 50),
		ResultsDir: env("PLQ_RESULTS", "./results"),
	}
}

func atof(k string, def float64) float64 {
	if v, ok := os.LookupEnv(k); ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// WorkerConfig projects the worker-relevant knobs into the queue package, keeping
// queue free of any config import.
func (c Config) WorkerConfig() queue.WorkerConfig {
	return queue.WorkerConfig{
		Lease:   c.Lease,
		Batch:   c.Batch,
		Backoff: queue.BackoffConfig{Min: c.PollMin, Max: c.PollMax},
		Process: c.Process,
	}
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

func atoi(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func atoi64(k string, def int64) int64 {
	if v, ok := os.LookupEnv(k); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func dur(k string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(k); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
