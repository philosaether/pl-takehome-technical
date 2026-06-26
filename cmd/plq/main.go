// Command plq is the single binary for the queue. Subcommands: worker | loadgen |
// reap. The backend driver is selected at build time via -tags (postgres|valkey;
// default=memory) — newBackend is provided by one of the backend_*.go files.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/philosaether/pl-takehome-technical/internal/config"
	"github.com/philosaether/pl-takehome-technical/internal/loadgen"
	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: plq <worker|loadgen|reap>")
		os.Exit(2)
	}
	cfg := config.Load()
	be, err := newBackend(cfg) // build-tag-selected (memory | postgres | valkey)
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
		if err := loadgen.Run(ctx, be, cfg); err != nil && ctx.Err() == nil {
			log.Fatalf("loadgen: %v", err)
		}
	case "reap":
		runReaper(ctx, be, cfg)
	default:
		fmt.Fprintln(os.Stderr, "unknown subcommand:", os.Args[1])
		os.Exit(2)
	}
}

func runWorker(ctx context.Context, be queue.Backend, cfg config.Config) {
	log.Printf("worker: backend=%s workers=%d lease=%s batch=%d process=%s",
		cfg.Backend, cfg.Workers, cfg.Lease, cfg.Batch, cfg.Process.Kind)
	// The reaper runs in-process alongside the workers (OQ2).
	go reapLoop(ctx, be, cfg)
	_ = queue.NewPool(be, cfg.Workers, cfg.WorkerConfig()).Run(ctx)
}

func runReaper(ctx context.Context, be queue.Backend, cfg config.Config) {
	log.Printf("reaper: standalone, interval=%s", reapInterval(cfg))
	reapLoop(ctx, be, cfg)
}

func reapLoop(ctx context.Context, be queue.Backend, cfg config.Config) {
	t := time.NewTicker(reapInterval(cfg))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if _, _, err := be.ReapExpired(ctx, now); err != nil && ctx.Err() == nil {
				log.Printf("reaper: %v", err)
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
