// Command plq is the single binary for the queue. Subcommands:
//
//	worker   — run the worker pool (+ in-process reaper, metrics)   [the canonical worker box]
//	loadgen  — run Zipfian-churn producers                          [the canonical producer box]
//	loadrun  — integrated producers+workers+metrics, one sweep point [dev/smoke + the sweep loop]
//	reap     — standalone reaper
//	reset    — truncate the queue (bench helper between sweep points)
//
// The backend driver is selected at build time via -tags (postgres|valkey; default
// = memory) — newBackend is provided by one of the backend_*.go files.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/config"
	"github.com/philosaether/pl-takehome-technical/internal/loadgen"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: plq <worker|loadgen|loadrun|reap|reset>")
		os.Exit(2)
	}
	cfg := config.Load()
	be, err := newBackend(cfg)
	if err != nil {
		log.Fatalf("backend: %v", err)
	}
	defer be.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "worker":
		runWorker(ctx, be, cfg)
	case "loadgen":
		runLoadgen(ctx, be, cfg)
	case "loadrun":
		runLoadrun(ctx, be, cfg)
	case "reap":
		runReaper(ctx, be, cfg)
	case "reset":
		if r, ok := be.(queue.Resetter); ok {
			if err := r.Reset(ctx); err != nil {
				log.Fatalf("reset: %v", err)
			}
			log.Println("reset: ok")
		} else {
			log.Println("reset: backend has no Reset (no-op)")
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", os.Args[1])
		os.Exit(2)
	}
}

func workload(cfg config.Config) loadgen.Workload {
	return loadgen.Workload{
		Seed: cfg.Seed, WorkingSet: cfg.WorkingSet, ZipfS: cfg.ZipfS, BirthRate: cfg.BirthRate,
		Cost: loadgen.CostDist{Kind: "uniform", Lo: cfg.CostLo, Hi: cfg.CostHi}, Workspaces: 8,
	}
}

func runWorker(ctx context.Context, be queue.Backend, cfg config.Config) {
	log.Printf("worker: backend=%s workers=%d lease=%s batch=%d process=%s",
		cfg.Backend, cfg.Workers, cfg.Lease, cfg.Batch, cfg.Process.Kind)
	m := loadgen.NewMetrics()
	wc := cfg.WorkerConfig()
	wc.Recorder = m
	go reapLoopM(ctx, be, m, cfg)
	go func() { // throughput/backlog line each second (the canonical worker-box log)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		var last int64
		stater, _ := be.(queue.Stater)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a := m.Acked.Load()
				backlog := int64(-1)
				if stater != nil {
					if s, err := stater.Stats(ctx); err == nil {
						backlog = s.EligibleUnits
					}
				}
				log.Printf("worker: %d acks/s (total %d) backlog=%d loop_p99=%s", a-last, a, backlog, m.LoopP99())
				last = a
			}
		}
	}()
	_ = queue.NewPool(be, cfg.Workers, wc).Run(ctx)
}

func runLoadgen(ctx context.Context, be queue.Backend, cfg config.Config) {
	log.Printf("loadgen: producers=%d working_set=%d zipf_s=%.2f birth=%.3f",
		cfg.Producers, cfg.WorkingSet, cfg.ZipfS, cfg.BirthRate)
	m := loadgen.NewMetrics()
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		var last int64
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n := m.Enqueued.Load()
				log.Printf("loadgen: %d enq/s (total %d)", n-last, n)
				last = n
			}
		}
	}()
	loadgen.RunProducers(ctx, be, m, workload(cfg), cfg.Producers)
}

func runLoadrun(ctx context.Context, be queue.Backend, cfg config.Config) {
	if err := os.MkdirAll(cfg.ResultsDir, 0o755); err != nil {
		log.Fatalf("results dir: %v", err)
	}
	if r, ok := be.(queue.Resetter); ok {
		if err := r.Reset(ctx); err != nil {
			log.Fatalf("reset: %v", err)
		}
	}
	spec := loadgen.RunSpec{
		Workers: cfg.Workers, Producers: cfg.Producers, Process: cfg.Process,
		Workload: workload(cfg), Lease: cfg.Lease, Batch: cfg.Batch,
		Duration: cfg.Duration, Warmup: cfg.Warmup,
	}
	sf, err := os.Create(filepath.Join(cfg.ResultsDir, fmt.Sprintf("sample-%d-%s.csv", cfg.Workers, cfg.Process.Kind)))
	if err != nil {
		log.Fatalf("sample file: %v", err)
	}
	defer sf.Close()
	spec.SampleCSV = sf

	log.Printf("loadrun: workers=%d process=%s producers=%d duration=%s", cfg.Workers, cfg.Process.Kind, cfg.Producers, cfg.Duration)
	res, err := loadgen.Run(ctx, be, spec)
	if err != nil {
		log.Fatalf("loadrun: %v", err)
	}
	appendSweepRow(cfg.ResultsDir, res)
	log.Printf("result: throughput=%.0f acks/s loop_p99=%s claim_p99=%s saturated=%t backlog=[%d,%d] acked=%d lease_exp=%d",
		res.Throughput, res.LoopP99, res.ClaimP99, res.Saturated, res.MinBacklog, res.MaxBacklog, res.Acked, res.LeaseExp)
	if !res.Saturated {
		log.Printf("WARNING: not saturated — the plateau may be the load generator's, not the backend's. Bump PLQ_PRODUCERS.")
	}
}

func appendSweepRow(dir string, res loadgen.Result) {
	path := filepath.Join(dir, "sweep.csv")
	_, statErr := os.Stat(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Fatalf("sweep.csv: %v", err)
	}
	defer f.Close()
	if os.IsNotExist(statErr) {
		fmt.Fprintln(f, loadgen.SweepHeader())
	}
	fmt.Fprintln(f, res.CSVRow())
}

func runReaper(ctx context.Context, be queue.Backend, cfg config.Config) {
	log.Printf("reaper: standalone, interval=%s", reapInterval(cfg))
	reapLoopM(ctx, be, loadgen.NewMetrics(), cfg)
}

func reapLoopM(ctx context.Context, be queue.Backend, m *loadgen.Metrics, cfg config.Config) {
	t := time.NewTicker(reapInterval(cfg))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if r, _, err := be.ReapExpired(ctx, now); err != nil && ctx.Err() == nil {
				log.Printf("reaper: %v", err)
			} else {
				m.LeaseExpired.Add(int64(r))
			}
		}
	}
}

func reapInterval(cfg config.Config) time.Duration {
	if d := cfg.Lease / 3; d > 0 {
		return d
	}
	return time.Second
}
