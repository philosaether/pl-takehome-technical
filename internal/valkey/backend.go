// Package valkey is the Path 2 driver (M3): the queue.Backend over Valkey, built on
// Streams + ZSETs + Hashes coordinated by Lua, implementing designs/valkey-driver.md.
// The in-memory backend is the reference; the conformance suite runs the same
// scenarios against both. The atomicity-bearing operations are single Lua scripts
// (single-threaded execution = no interleaving), the structural analog of M1's
// transaction + unit row lock.
//
// Sharding for the head-to-head is N *independent standalone primaries*: the driver
// routes each unit by hash(workspace) to one instance, so every script runs against
// a single keyspace and is trivially atomic. len(Addrs)==1 is the baseline (and the
// conformance config); 2/4 is the linear-scaling sweep. (Redis Cluster is the named
// production deploy — see scripts.go.)
package valkey

import (
	"context"
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/rueidis"

	"github.com/philosaether/pl-takehome-technical/internal/queue"
)

// ErrNoAddr is returned when no Valkey address is configured.
var ErrNoAddr = errors.New("valkey: no address configured (set PLQ_VALKEY_ADDR)")

// Options configures the Valkey driver.
type Options struct {
	Addrs            []string // ≥1 primary; len 1 = baseline, 2/4 = the scaling sweep
	DefaultThreshold int64
	DefaultMaxWait   time.Duration
	MaxAttempts      int
}

// Backend is the Valkey-backed queue (Streams + ZSETs + Lua via rueidis).
type Backend struct {
	shards   []rueidis.Client
	scripts  scripts
	defT     int64
	defWait  time.Duration
	maxTries int
	rr       atomic.Uint64 // round-robin cursor for Claim across shards
	tokens   atomic.Int64  // per-claim lease-token generator (ABA-safe handle)
}

var (
	_ queue.Backend  = (*Backend)(nil)
	_ queue.Stater   = (*Backend)(nil)
	_ queue.Resetter = (*Backend)(nil)
)

// New dials each primary and loads the Lua scripts.
func New(o Options) (queue.Backend, error) {
	if len(o.Addrs) == 0 {
		return nil, ErrNoAddr
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	shards := make([]rueidis.Client, 0, len(o.Addrs))
	for _, addr := range o.Addrs {
		c, err := rueidis.NewClient(rueidis.ClientOption{
			InitAddress:  []string{addr},
			DisableCache: true, // we coordinate via Lua, not client-side caching
		})
		if err != nil {
			for _, s := range shards {
				s.Close()
			}
			return nil, err
		}
		shards = append(shards, c)
	}
	return &Backend{
		shards:   shards,
		scripts:  loadScripts(),
		defT:     o.DefaultThreshold,
		defWait:  o.DefaultMaxWait,
		maxTries: o.MaxAttempts,
	}, nil
}

// shardForWorkspace routes a unit to its owning primary by workspace (A1: a whole
// tenant stays on one shard — clean seam + failure domain). Stable across calls.
func (b *Backend) shardForWorkspace(ws string) rueidis.Client {
	if len(b.shards) == 1 {
		return b.shards[0]
	}
	h := fnv.New32a()
	h.Write([]byte(ws))
	return b.shards[h.Sum32()%uint32(len(b.shards))]
}

func member(k queue.WorkUnitKey) string {
	return k.Workspace + "|" + k.Session + "|" + k.Peer
}

func splitMember(m string) queue.WorkUnitKey {
	parts := strings.SplitN(m, "|", 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return queue.WorkUnitKey{Workspace: parts[0], Session: parts[1], Peer: parts[2]}
}

func nowMs() string { return strconv.FormatInt(time.Now().UnixMilli(), 10) }

// leaseLost maps the Lua LEASELOST error_reply onto the contract sentinel.
func leaseLost(err error) error {
	if err != nil && strings.Contains(err.Error(), "LEASELOST") {
		return queue.ErrLeaseLost
	}
	return err
}

func (b *Backend) Enqueue(ctx context.Context, key queue.WorkUnitKey, payload []byte, cost int64) (int64, error) {
	sh := b.shardForWorkspace(key.Workspace)
	return b.scripts.enqueue.Exec(ctx, sh, nil, []string{
		member(key), string(payload), strconv.FormatInt(cost, 10),
		strconv.FormatInt(b.defT, 10), strconv.FormatInt(b.defWait.Milliseconds(), 10), nowMs(),
	}).AsInt64()
}

func (b *Backend) Claim(ctx context.Context, worker queue.WorkerID, lease time.Duration) (*queue.ClaimedUnit, error) {
	tok := strconv.FormatInt(b.tokens.Add(1), 10) + "-" + string(worker)
	leaseMs := strconv.FormatInt(lease.Milliseconds(), 10)
	start := int(b.rr.Add(1))
	now := time.Now()
	// Consult shards in rotating order; the first eligible unit on any shard wins.
	for i := 0; i < len(b.shards); i++ {
		sh := b.shards[(start+i)%len(b.shards)]
		m, err := b.scripts.claim.Exec(ctx, sh, nil, []string{string(worker), tok, leaseMs, nowMs()}).ToString()
		if err != nil {
			return nil, err
		}
		if m == "" {
			continue // nothing eligible on this shard
		}
		return &queue.ClaimedUnit{
			Key:       splitMember(m),
			Worker:    worker,
			Lease:     queue.LeaseToken(tok),
			LeaseTill: now.Add(lease),
		}, nil
	}
	return nil, nil // nothing eligible anywhere — not an error
}

func entryToTask(e rueidis.XRangeEntry) queue.Task {
	seq, _ := strconv.ParseInt(e.FieldValues["seq"], 10, 64)
	cost, _ := strconv.ParseInt(e.FieldValues["c"], 10, 64)
	return queue.Task{Seq: seq, Payload: []byte(e.FieldValues["p"]), Cost: cost}
}

func (b *Backend) Drain(ctx context.Context, c *queue.ClaimedUnit, max int) ([]queue.Task, error) {
	sh := b.shardForWorkspace(c.Key.Workspace)
	m := member(c.Key)
	s := "s:" + m
	// Lease check first — distinguishes ErrLeaseLost from an empty (clean) drain.
	tok, err := sh.Do(ctx, sh.B().Hget().Key("wu:"+m).Field("lease_token").Build()).ToString()
	if err != nil || tok != string(c.Lease) {
		return nil, queue.ErrLeaseLost
	}

	var tasks []queue.Task
	// 1) Take over any orphaned PEL entries (crash redelivery), in stream-ID order.
	//    min-idle = 0 is safe HERE: the lease (not PEL idle time) is the exclusivity
	//    mechanism, so we only reach a unit after its predecessor's lease was
	//    reclaimed (predecessor deemed dead). XAUTOCLAIM resumes the stream mid-flight.
	ac := sh.B().Arbitrary("XAUTOCLAIM").Keys(s).
		Args("g", string(c.Worker), "0", "0", "COUNT", strconv.Itoa(max)).Build()
	if arr, e := sh.Do(ctx, ac).ToArray(); e == nil && len(arr) >= 2 {
		if ents, e2 := arr[1].AsXRange(); e2 == nil {
			for _, en := range ents {
				tasks = append(tasks, entryToTask(en))
			}
		}
	}
	// 2) Then new (never-delivered) messages, if the batch has room.
	if len(tasks) < max {
		rg := sh.B().Arbitrary("XREADGROUP", "GROUP", "g", string(c.Worker),
			"COUNT", strconv.Itoa(max-len(tasks)), "STREAMS").Keys(s).Args(">").Build()
		if mp, e := sh.Do(ctx, rg).AsXRead(); e == nil {
			for _, en := range mp[s] {
				tasks = append(tasks, entryToTask(en))
			}
		}
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Seq < tasks[j].Seq })
	return tasks, nil
}

func (b *Backend) Ack(ctx context.Context, c *queue.ClaimedUnit, throughSeq int64) (bool, error) {
	sh := b.shardForWorkspace(c.Key.Workspace)
	res := b.scripts.ack.Exec(ctx, sh, nil, []string{
		member(c.Key), string(c.Lease), strconv.FormatInt(throughSeq, 10), nowMs(),
	})
	if err := leaseLost(res.Error()); err != nil {
		return false, err
	}
	held, err := res.AsInt64()
	return held == 1, err
}

func (b *Backend) Release(ctx context.Context, c *queue.ClaimedUnit) error {
	sh := b.shardForWorkspace(c.Key.Workspace)
	return leaseLost(b.scripts.release.Exec(ctx, sh, nil,
		[]string{member(c.Key), string(c.Lease), nowMs()}).Error())
}

func (b *Backend) Heartbeat(ctx context.Context, c *queue.ClaimedUnit, extend time.Duration) error {
	sh := b.shardForWorkspace(c.Key.Workspace)
	return leaseLost(b.scripts.heartbeat.Exec(ctx, sh, nil, []string{
		member(c.Key), string(c.Lease), strconv.FormatInt(extend.Milliseconds(), 10), nowMs(),
	}).Error())
}

func (b *Backend) Fail(ctx context.Context, c *queue.ClaimedUnit, seq int64, reason string) error {
	sh := b.shardForWorkspace(c.Key.Workspace)
	return leaseLost(b.scripts.fail.Exec(ctx, sh, nil, []string{
		member(c.Key), string(c.Lease), strconv.FormatInt(seq, 10), reason, nowMs(),
		strconv.Itoa(b.maxTries),
	}).Error())
}

func (b *Backend) ReapExpired(ctx context.Context, now time.Time) (int, int, error) {
	arg := []string{strconv.FormatInt(now.UnixMilli(), 10)}
	var reclaimed, flushed int
	for _, sh := range b.shards {
		arr, err := b.scripts.reap.Exec(ctx, sh, nil, arg).ToArray()
		if err != nil {
			return reclaimed, flushed, err
		}
		if len(arr) == 2 {
			r, _ := arr[0].AsInt64()
			f, _ := arr[1].AsInt64()
			reclaimed += int(r)
			flushed += int(f)
		}
	}
	return reclaimed, flushed, nil
}

func (b *Backend) Close() error {
	for _, sh := range b.shards {
		sh.Close()
	}
	return nil
}

// Stats sums per-shard depth from the coordination ZSETs + the maintained
// pending_tasks counter — never scans a stream (the look-ahead principle applied to
// the depth metric). OldestBelowT is approximate (derived from the earliest
// pending_flush deadline using the default max_wait); a coarse saturation gauge.
func (b *Backend) Stats(ctx context.Context) (queue.Stats, error) {
	var st queue.Stats
	now := time.Now().UnixMilli()
	for _, sh := range b.shards {
		elig, _ := sh.Do(ctx, sh.B().Zcard().Key("eligible").Build()).AsInt64()
		pf, _ := sh.Do(ctx, sh.B().Zcard().Key("pending_flush").Build()).AsInt64()
		ls, _ := sh.Do(ctx, sh.B().Zcard().Key("leases").Build()).AsInt64()
		dlq, _ := sh.Do(ctx, sh.B().Xlen().Key("dlq").Build()).AsInt64()
		pending, _ := sh.Do(ctx, sh.B().Get().Key("pending_tasks").Build()).AsInt64()
		st.TotalUnits += elig + pf + ls
		st.EligibleUnits += elig
		st.PendingTasks += pending
		st.DeadLetters += dlq
		// earliest pending_flush deadline → oldest below-T age (approx, default max_wait).
		if z, err := sh.Do(ctx, sh.B().Zrange().Key("pending_flush").Min("0").Max("0").Byscore().
			Limit(0, 1).Withscores().Build()).AsZScores(); err == nil && len(z) == 1 {
			oldestMs := int64(z[0].Score) - b.defWait.Milliseconds()
			if age := time.Duration(now-oldestMs) * time.Millisecond; age > st.OldestBelowT {
				st.OldestBelowT = age
			}
		}
	}
	return st, nil
}

// Reset flushes every shard — bench/test only (between conformance scenarios and
// sweep points). Mirrors the Postgres TRUNCATE; not part of the Backend contract.
func (b *Backend) Reset(ctx context.Context) error {
	for _, sh := range b.shards {
		if err := sh.Do(ctx, sh.B().Flushdb().Build()).Error(); err != nil {
			return err
		}
	}
	return nil
}
