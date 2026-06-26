// Package postgres is the Path 1 driver (M1): the queue.Backend over Postgres,
// implementing designs/postgres-driver.md. The in-memory backend is the reference;
// the conformance suite runs the same scenarios against both.
package postgres

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

//go:embed schema.sql
var schemaSQL string

// Options configures the Postgres driver.
type Options struct {
	DSN              string
	DefaultThreshold int
	DefaultMaxWait   time.Duration
	MaxAttempts      int
}

// Backend is the Postgres-backed queue.
type Backend struct {
	pool     *pgxpool.Pool
	maxTries int
	tenants  *tenantCache
}

var _ queue.Backend = (*Backend)(nil)

// New opens a pgx pool and applies the (idempotent) schema.
func New(o Options) (queue.Backend, error) {
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, o.DSN)
	if err != nil {
		return nil, fmt.Errorf("postgres: pool: %w", err)
	}
	if err := applySchema(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: schema: %w", err)
	}
	return &Backend{
		pool:     pool,
		maxTries: o.MaxAttempts,
		tenants:  newTenantCache(pool, o.DefaultThreshold, o.DefaultMaxWait),
	}, nil
}

// applySchema runs schema.sql statement-by-statement (comments stripped) so it
// works regardless of pgx's protocol mode. Every statement is CREATE IF NOT EXISTS.
func applySchema(ctx context.Context, pool *pgxpool.Pool) error {
	for _, stmt := range strings.Split(stripSQLComments(schemaSQL), ";") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("stmt %q: %w", strings.SplitN(s, "\n", 2)[0], err)
		}
	}
	return nil
}

func stripSQLComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func (b *Backend) Enqueue(ctx context.Context, key queue.WorkUnitKey, payload []byte, cost int64) (int64, error) {
	t, wait := b.tenants.get(ctx, key.Workspace)
	var seq int64
	err := b.pool.QueryRow(ctx, `
WITH up AS (
  INSERT INTO work_units AS w (ws, sess, peer, threshold, max_wait_ms, pending_cost, next_seq,
                               eligible, oldest_pending_at, flush_deadline)
  VALUES ($1,$2,$3,$4::int,$5::int,$6::bigint,0, $6::bigint >= $4::int, now(),
          now() + $5::int * interval '1 ms')
  ON CONFLICT (ws, sess, peer) DO UPDATE SET
    pending_cost = w.pending_cost + $6::bigint,
    next_seq     = w.next_seq + 1,
    eligible     = (w.pending_cost + $6::bigint) >= w.threshold OR w.eligible,
    oldest_pending_at = CASE WHEN w.pending_cost = 0 THEN now() ELSE w.oldest_pending_at END,
    flush_deadline    = CASE WHEN w.pending_cost = 0 THEN now() + w.max_wait_ms * interval '1 ms'
                             ELSE w.flush_deadline END
  RETURNING next_seq
)
INSERT INTO tasks (ws, sess, peer, seq, payload, cost)
SELECT $1,$2,$3, next_seq, $7, $6::bigint FROM up
RETURNING seq`,
		key.Workspace, key.Session, key.Peer, t, int64(wait/time.Millisecond), cost, payload).Scan(&seq)
	return seq, err
}

func (b *Backend) Claim(ctx context.Context, worker queue.WorkerID, lease time.Duration) (*queue.ClaimedUnit, error) {
	leaseMs := int64(lease / time.Millisecond)
	var ws, sess, peer, tok string
	var expires time.Time
	err := b.pool.QueryRow(ctx, `
UPDATE work_units SET claimed_by = $1, lease_token = gen_random_uuid(),
                      lease_ms = $2::int, lease_expires = now() + $2::int * interval '1 ms'
WHERE (ws,sess,peer) = (
  SELECT ws,sess,peer FROM work_units
  WHERE eligible AND claimed_by IS NULL
  ORDER BY flush_deadline NULLS LAST, ws, sess, peer
  FOR UPDATE SKIP LOCKED LIMIT 1
)
RETURNING ws, sess, peer, lease_token::text, lease_expires`,
		string(worker), leaseMs).Scan(&ws, &sess, &peer, &tok, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // nothing eligible — not an error
	}
	if err != nil {
		return nil, err
	}
	return &queue.ClaimedUnit{
		Key:       queue.WorkUnitKey{Workspace: ws, Session: sess, Peer: peer},
		Worker:    worker,
		Lease:     queue.LeaseToken(tok),
		LeaseTill: expires,
	}, nil
}

func (b *Backend) Drain(ctx context.Context, c *queue.ClaimedUnit, max int) ([]queue.Task, error) {
	if err := b.checkLease(ctx, b.pool, c); err != nil {
		return nil, err
	}
	rows, err := b.pool.Query(ctx, `
SELECT seq, payload, cost FROM tasks
WHERE ws=$1 AND sess=$2 AND peer=$3 ORDER BY seq LIMIT $4`,
		c.Key.Workspace, c.Key.Session, c.Key.Peer, max)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []queue.Task
	for rows.Next() {
		var task queue.Task
		var cost int
		if err := rows.Scan(&task.Seq, &task.Payload, &cost); err != nil {
			return nil, err
		}
		task.Cost = int64(cost)
		out = append(out, task)
	}
	return out, rows.Err()
}

func (b *Backend) Ack(ctx context.Context, c *queue.ClaimedUnit, throughSeq int64) (bool, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err := b.checkLeaseForUpdate(ctx, tx, c); err != nil {
		return false, err
	}
	// delete-on-ack + recompute in one statement. `head` is computed over the
	// REMAINING tasks (seq > throughSeq) so it's independent of the data-modifying
	// `del` CTE. `elig` computes the recomputed eligibility (= the keep/re-buffer
	// decision) ONCE from the pre-ack row + acked sum + new head, so the four
	// downstream uses (eligible, claimed_by, lease_token, lease_expires) share one
	// source of truth. Per-head flush, never a sticky flag.
	var stillHeld bool
	err = tx.QueryRow(ctx, `
WITH del AS (
  DELETE FROM tasks WHERE ws=$1 AND sess=$2 AND peer=$3 AND seq <= $6 RETURNING cost
), acked AS ( SELECT COALESCE(sum(cost),0) AS c FROM del ),
   head  AS ( SELECT min(enqueued_at) AS min_enq FROM tasks
              WHERE ws=$1 AND sess=$2 AND peer=$3 AND seq > $6 ),
   elig  AS (
     SELECT acked.c AS acked, head.min_enq AS min_enq,
            ((w.pending_cost - acked.c) >= w.threshold
             OR (head.min_enq IS NOT NULL
                 AND head.min_enq + w.max_wait_ms * interval '1 ms' <= now())) AS keep
     FROM work_units w, acked, head
     WHERE w.ws=$1 AND w.sess=$2 AND w.peer=$3
   )
UPDATE work_units w SET
  pending_cost      = w.pending_cost - elig.acked,
  oldest_pending_at = elig.min_enq,
  flush_deadline    = elig.min_enq + w.max_wait_ms * interval '1 ms',
  eligible          = elig.keep,
  claimed_by    = CASE WHEN elig.keep THEN $4       ELSE NULL END,
  lease_token   = CASE WHEN elig.keep THEN $5::uuid ELSE NULL END,
  lease_expires = CASE WHEN elig.keep THEN now() + w.lease_ms * interval '1 ms' ELSE NULL END
FROM elig
WHERE w.ws=$1 AND w.sess=$2 AND w.peer=$3
RETURNING elig.keep`,
		c.Key.Workspace, c.Key.Session, c.Key.Peer, string(c.Worker), string(c.Lease), throughSeq).Scan(&stillHeld)
	if err != nil {
		return false, err
	}
	return stillHeld, tx.Commit(ctx)
}

func (b *Backend) Release(ctx context.Context, c *queue.ClaimedUnit) error {
	ct, err := b.pool.Exec(ctx, `
UPDATE work_units SET claimed_by=NULL, lease_token=NULL, lease_expires=NULL
WHERE ws=$1 AND sess=$2 AND peer=$3 AND claimed_by=$4 AND lease_token=$5::uuid`,
		c.Key.Workspace, c.Key.Session, c.Key.Peer, string(c.Worker), string(c.Lease))
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return queue.ErrLeaseLost
	}
	return nil
}

func (b *Backend) Heartbeat(ctx context.Context, c *queue.ClaimedUnit, extend time.Duration) error {
	ms := int64(extend / time.Millisecond)
	ct, err := b.pool.Exec(ctx, `
UPDATE work_units SET lease_ms=$6::int, lease_expires = now() + $6::int * interval '1 ms'
WHERE ws=$1 AND sess=$2 AND peer=$3 AND claimed_by=$4 AND lease_token=$5::uuid`,
		c.Key.Workspace, c.Key.Session, c.Key.Peer, string(c.Worker), string(c.Lease), ms)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return queue.ErrLeaseLost
	}
	return nil
}

func (b *Backend) Fail(ctx context.Context, c *queue.ClaimedUnit, seq int64, reason string) error {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := b.checkLeaseForUpdate(ctx, tx, c); err != nil {
		return err
	}
	var attempts, cost int
	err = tx.QueryRow(ctx, `
UPDATE tasks SET attempts = attempts + 1
WHERE ws=$1 AND sess=$2 AND peer=$3 AND seq=$4
RETURNING attempts, cost`,
		c.Key.Workspace, c.Key.Session, c.Key.Peer, seq).Scan(&attempts, &cost)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// Only the HEAD task can be DLQ'd at the cap — DLQ-ing a middle task would punch
	// a hole in the FIFO. Matches the oracle's `u.tasks[0].Seq == seq` guard.
	atCap := false
	if !errors.Is(err, pgx.ErrNoRows) && attempts >= b.maxTries {
		var headSeq int64
		if e := tx.QueryRow(ctx, `SELECT min(seq) FROM tasks WHERE ws=$1 AND sess=$2 AND peer=$3`,
			c.Key.Workspace, c.Key.Session, c.Key.Peer).Scan(&headSeq); e != nil {
			return e
		}
		atCap = seq == headSeq
	}

	if atCap {
		// poison head → DLQ, decrement aggregate, recompute eligibility, release.
		if _, err := tx.Exec(ctx, `
WITH moved AS (
  DELETE FROM tasks WHERE ws=$1 AND sess=$2 AND peer=$3 AND seq=$4
  RETURNING ws,sess,peer,seq,payload,cost,attempts,enqueued_at
), ins AS (
  INSERT INTO dead_letters (ws,sess,peer,seq,payload,cost,attempts,enqueued_at,reason)
  SELECT ws,sess,peer,seq,payload,cost,attempts,enqueued_at,$5 FROM moved
), head AS (
  SELECT min(enqueued_at) AS min_enq FROM tasks
  WHERE ws=$1 AND sess=$2 AND peer=$3 AND seq <> $4
)
UPDATE work_units w SET
  pending_cost      = w.pending_cost - $6::bigint,
  oldest_pending_at = head.min_enq,
  flush_deadline    = head.min_enq + w.max_wait_ms * interval '1 ms',
  eligible          = ((w.pending_cost - $6::bigint) >= w.threshold
                       OR (head.min_enq IS NOT NULL
                           AND head.min_enq + w.max_wait_ms * interval '1 ms' <= now())),
  claimed_by=NULL, lease_token=NULL, lease_expires=NULL
FROM head WHERE w.ws=$1 AND w.sess=$2 AND w.peer=$3`,
			c.Key.Workspace, c.Key.Session, c.Key.Peer, seq, reason, cost); err != nil {
			return err
		}
	} else {
		// below the cap (or head already gone): just release → redeliver in order.
		if _, err := tx.Exec(ctx, `
UPDATE work_units SET claimed_by=NULL, lease_token=NULL, lease_expires=NULL
WHERE ws=$1 AND sess=$2 AND peer=$3`,
			c.Key.Workspace, c.Key.Session, c.Key.Peer); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (b *Backend) ReapExpired(ctx context.Context, now time.Time) (int, int, error) {
	r, err := b.pool.Exec(ctx, `
UPDATE work_units SET claimed_by=NULL, lease_token=NULL, lease_expires=NULL
WHERE claimed_by IS NOT NULL AND lease_expires < $1`, now)
	if err != nil {
		return 0, 0, err
	}
	f, err := b.pool.Exec(ctx, `
UPDATE work_units SET eligible=true
WHERE NOT eligible AND claimed_by IS NULL AND flush_deadline <= $1 AND pending_cost > 0`, now)
	if err != nil {
		return int(r.RowsAffected()), 0, err
	}
	return int(r.RowsAffected()), int(f.RowsAffected()), nil
}

func (b *Backend) Close() error {
	b.pool.Close()
	return nil
}

var (
	_ queue.Stater   = (*Backend)(nil)
	_ queue.Resetter = (*Backend)(nil)
)

// Stats reports queue depth cheaply — all over work_units (10^4–10^5 rows) plus a
// small DLQ count, so it's safe to call once per second during a load run.
func (b *Backend) Stats(ctx context.Context) (queue.Stats, error) {
	var total, eligible, dlq int64
	var oldestS float64
	err := b.pool.QueryRow(ctx, `
SELECT count(*),
       count(*) FILTER (WHERE eligible AND claimed_by IS NULL),
       COALESCE(EXTRACT(EPOCH FROM (now() - min(oldest_pending_at)
         FILTER (WHERE NOT eligible AND claimed_by IS NULL))), 0),
       (SELECT count(*) FROM dead_letters)
FROM work_units`).Scan(&total, &eligible, &oldestS, &dlq)
	if err != nil {
		return queue.Stats{}, err
	}
	return queue.Stats{
		TotalUnits:    total,
		EligibleUnits: eligible,
		DeadLetters:   dlq,
		OldestBelowT:  time.Duration(oldestS * float64(time.Second)),
	}, nil
}

// Reset truncates the queue tables (keeps tenant_config) — for resetting between
// sweep points. Not part of the Backend contract; bench/test only.
func (b *Backend) Reset(ctx context.Context) error {
	_, err := b.pool.Exec(ctx, `TRUNCATE work_units, tasks, dead_letters`)
	return err
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (b *Backend) checkLease(ctx context.Context, q querier, c *queue.ClaimedUnit) error {
	var ok bool
	err := q.QueryRow(ctx, `
SELECT true FROM work_units
WHERE ws=$1 AND sess=$2 AND peer=$3 AND claimed_by=$4 AND lease_token=$5::uuid`,
		c.Key.Workspace, c.Key.Session, c.Key.Peer, string(c.Worker), string(c.Lease)).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return queue.ErrLeaseLost
	}
	return err
}

func (b *Backend) checkLeaseForUpdate(ctx context.Context, q querier, c *queue.ClaimedUnit) error {
	var ok bool
	err := q.QueryRow(ctx, `
SELECT true FROM work_units
WHERE ws=$1 AND sess=$2 AND peer=$3 AND claimed_by=$4 AND lease_token=$5::uuid FOR UPDATE`,
		c.Key.Workspace, c.Key.Session, c.Key.Peer, string(c.Worker), string(c.Lease)).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return queue.ErrLeaseLost
	}
	return err
}

// tenantCache resolves (ws → threshold, max_wait) from tenant_config, falling back
// to the configured defaults; cached for the process lifetime to keep enqueue local.
type tenantCache struct {
	pool    *pgxpool.Pool
	defT    int
	defWait time.Duration
	mu      sync.RWMutex
	m       map[string]tenantCfg
}

type tenantCfg struct {
	threshold int
	maxWait   time.Duration
}

func newTenantCache(pool *pgxpool.Pool, defT int, defWait time.Duration) *tenantCache {
	return &tenantCache{pool: pool, defT: defT, defWait: defWait, m: map[string]tenantCfg{}}
}

func (c *tenantCache) get(ctx context.Context, ws string) (int, time.Duration) {
	c.mu.RLock()
	v, ok := c.m[ws]
	c.mu.RUnlock()
	if ok {
		return v.threshold, v.maxWait
	}
	res := tenantCfg{threshold: c.defT, maxWait: c.defWait}
	var t, waitMs int
	if err := c.pool.QueryRow(ctx,
		`SELECT threshold, max_wait_ms FROM tenant_config WHERE ws=$1`, ws).Scan(&t, &waitMs); err == nil {
		res = tenantCfg{threshold: t, maxWait: time.Duration(waitMs) * time.Millisecond}
	}
	c.mu.Lock()
	c.m[ws] = res
	c.mu.Unlock()
	return res.threshold, res.maxWait
}
