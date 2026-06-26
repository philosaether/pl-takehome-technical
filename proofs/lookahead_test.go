package proofs_test

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/philosaether/pl-takehome-technical/internal/postgres"
)

// TestLookaheadCost is the headline bench: our maintained-aggregate look-ahead
// (an index lookup over work_units, ~flat as task count grows) vs the naive
// per-poll SUM…GROUP BY over tasks (Honcho's current code — scans, grows). Seeds
// up to 10^6 tasks via COPY (we're benching CLAIM, not enqueue). Postgres-only.
//
//	PLQ_TEST_POSTGRES=postgres://… go test ./proofs/ -run Lookahead -v
func TestLookaheadCost(t *testing.T) {
	dsn := os.Getenv("PLQ_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("set PLQ_TEST_POSTGRES to run the look-ahead bench")
	}
	const (
		nUnits = 10000 // FIXED: so ours stays flat while tasks/unit (and naive's scan) grow
		T      = 50    // threshold; ~half the units eligible at the larger sizes
		maxWMs = 60000
		ktimes = 5
	)
	sizes := []int{100_000, 1_000_000}

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

	if err := os.MkdirAll("../results", 0o755); err != nil {
		t.Fatalf("results dir: %v", err)
	}
	csv, err := os.Create(filepath.Join("..", "results", "lookahead.csv"))
	if err != nil {
		t.Fatalf("csv: %v", err)
	}
	defer csv.Close()
	fmt.Fprintln(csv, "tasks,ours_ms,naive_ms,speedup")

	var lastOurs, lastNaive float64
	for _, size := range sizes {
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
