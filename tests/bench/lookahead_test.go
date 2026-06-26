// Package bench holds scaling benchmarks (not correctness proofs) — kept in their
// own test binary so a heavy bench never shares a process/DB with a correctness
// proof. Postgres-only, gated by PLQ_TEST_POSTGRES.
package bench_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/philosaether/pl-takehome-technical/internal/postgres"
)

var ctx = context.Background()

// TestLookaheadCost is the headline bench: our maintained-aggregate look-ahead
// (an index lookup over work_units, ~flat as task count grows) vs the naive
// per-poll SUM…GROUP BY over tasks (Honcho's current code — scans, grows). Seeds
// up to 10^6 tasks via COPY (we're benching CLAIM, not enqueue). Postgres-only.
//
//	PLQ_TEST_POSTGRES=postgres://… go test ./tests/proofs/ -run Lookahead -v
//
// Units SCALE with task count (~100 tasks/unit, the brief's 10^4–10^5-unit regime
// at 10^6 tasks) — the honest workload. Ours still stays ~flat because the
// look-ahead is an indexed ORDER BY … LIMIT 1 over work_units (reads the leftmost
// entry, not all units); naive scans every task. Size curve is configurable via
// PLQ_BENCH_SIZES (csv); default is 1/3/7 per decade, 10^4 → 10^7 (~a few min).
func TestLookaheadCost(t *testing.T) {
	dsn := os.Getenv("PLQ_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("set PLQ_TEST_POSTGRES to run the look-ahead bench")
	}
	const (
		tasksPerUnit = 100 // units scale: nUnits = size / tasksPerUnit
		T            = 50  // threshold; with 100 tasks/unit (cost 1) every unit is eligible
		maxWMs       = 60000
		ktimes       = 5
	)
	sizes := benchSizes()

	if be, err := postgres.New(postgres.Options{DSN: dsn, DefaultThreshold: T, DefaultMaxWait: time.Minute, MaxAttempts: 3}); err != nil {
		t.Fatalf("schema: %v", err)
	} else {
		be.Close()
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	defer pool.Close()

	const oursSQL = `SELECT ws,sess,peer FROM work_units
WHERE eligible AND claimed_by IS NULL
ORDER BY flush_deadline NULLS LAST, ws,sess,peer LIMIT 1`
	const naiveSQL = `SELECT ws,sess,peer FROM tasks
GROUP BY ws,sess,peer HAVING sum(cost) >= 50
ORDER BY ws,sess,peer LIMIT 1`

	if err := os.MkdirAll("../../results", 0o755); err != nil {
		t.Fatalf("results dir: %v", err)
	}
	csv, err := os.Create(filepath.Join("..", "..", "results", "lookahead.csv"))
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	defer csv.Close()
	fmt.Fprintln(csv, "tasks,ours_ms,naive_ms,speedup")

	var lastOurs, lastNaive float64
	for _, size := range sizes {
		nUnits := size / tasksPerUnit
		if nUnits < 100 {
			nUnits = 100
		}
		seedBench(t, pool, size, nUnits, T, maxWMs)

		ours := minQueryMs(t, pool, oursSQL, ktimes)
		naive := minQueryMs(t, pool, naiveSQL, ktimes)
		lastOurs, lastNaive = ours, naive
		fmt.Fprintf(csv, "%d,%.3f,%.3f,%.1f\n", size, ours, naive, naive/ours)
		t.Logf("tasks=%d  ours=%.3fms  naive=%.3fms  speedup=%.1fx", size, ours, naive, naive/ours)

		// Plan assertions at every size.
		oursPlan := explain(t, pool, oursSQL)
		naivePlan := explain(t, pool, naiveSQL)
		if strings.Contains(oursPlan, "Seq Scan on tasks") {
			t.Errorf("ours touched tasks with a seq scan — the look-ahead leaked:\n%s", oursPlan)
		}
		if !strings.Contains(naivePlan, "tasks") {
			t.Errorf("naive plan didn't reference tasks?\n%s", naivePlan)
		}
	}

	// At the largest size, ours must decisively beat the naive scan.
	if lastOurs <= 0 || lastNaive/lastOurs < 2 {
		t.Errorf("expected ours ≫ naive at 10^6 (got ours=%.3fms naive=%.3fms); the thesis didn't hold",
			lastOurs, lastNaive)
	}
}

// seedBench wipes and bulk-loads `size` tasks Zipf-distributed across nUnits, with
// matching work_units aggregates. COPY-based — seconds, not minutes.
func seedBench(t *testing.T, pool *pgxpool.Pool, size, nUnits, threshold, maxWMs int) {
	t.Helper()
	if _, err := pool.Exec(ctx, `TRUNCATE work_units, tasks, dead_letters`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	r := rand.New(rand.NewSource(1))
	zipf := rand.NewZipf(r, 1.2, 1, uint64(nUnits-1))
	unitOf := make([]int32, size)
	seqAt := make([]int64, size)
	counts := make([]int64, nUnits)
	for i := 0; i < size; i++ {
		u := int32(zipf.Uint64())
		unitOf[i] = u
		seqAt[i] = counts[u]
		counts[u]++
	}

	payload := []byte("x")
	_, err := pool.CopyFrom(ctx, pgx.Identifier{"tasks"},
		[]string{"ws", "sess", "peer", "seq", "payload", "cost"},
		pgx.CopyFromSlice(size, func(i int) ([]any, error) {
			u := unitOf[i]
			return []any{uname(u), "s", "p", seqAt[i], payload, 1}, nil
		}))
	if err != nil {
		t.Fatalf("copy tasks: %v", err)
	}

	// work_units rows for the units that got tasks.
	live := make([]int32, 0, nUnits)
	for u := 0; u < nUnits; u++ {
		if counts[u] > 0 {
			live = append(live, int32(u))
		}
	}
	now := time.Now()
	_, err = pool.CopyFrom(ctx, pgx.Identifier{"work_units"},
		[]string{"ws", "sess", "peer", "pending_cost", "next_seq", "threshold", "max_wait_ms", "eligible", "oldest_pending_at", "flush_deadline"},
		pgx.CopyFromSlice(len(live), func(i int) ([]any, error) {
			u := live[i]
			pending := counts[u]
			return []any{uname(u), "s", "p", pending, counts[u], threshold, maxWMs,
				pending >= int64(threshold), now, now.Add(time.Duration(maxWMs) * time.Millisecond)}, nil
		}))
	if err != nil {
		t.Fatalf("copy work_units: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE work_units, tasks`); err != nil {
		t.Fatalf("analyze: %v", err)
	}
}

func uname(u int32) string { return fmt.Sprintf("u-%d", u) }

// benchSizes returns the task-count curve: PLQ_BENCH_SIZES (csv) or the default
// 1/3/7-per-decade from 10^4 to 10^7.
func benchSizes() []int {
	if v := os.Getenv("PLQ_BENCH_SIZES"); v != "" {
		var out []int
		for _, p := range strings.Split(v, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n > 0 {
				out = append(out, n)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []int{
		10_000, 30_000, 70_000,
		100_000, 300_000, 700_000,
		1_000_000, 3_000_000, 7_000_000,
		10_000_000,
	}
}

func minQueryMs(t *testing.T, pool *pgxpool.Pool, sql string, k int) float64 {
	t.Helper()
	best := time.Hour
	for i := 0; i < k; i++ {
		start := time.Now()
		rows, err := pool.Query(ctx, sql)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		for rows.Next() {
		}
		rows.Close()
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return float64(best) / float64(time.Millisecond)
}

func explain(t *testing.T, pool *pgxpool.Pool, sql string) string {
	t.Helper()
	rows, err := pool.Query(ctx, "EXPLAIN "+sql)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("explain scan: %v", err)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
