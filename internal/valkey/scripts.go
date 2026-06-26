package valkey

import "github.com/redis/rueidis"

// The atomicity-bearing operations are Lua scripts: a script runs to completion on
// the single Redis thread with no other command interleaved (the structural analog
// of M1's unit row lock). Enqueue, claim, ack, release, heartbeat, fail, and the
// reaper are each one script.
//
// Keys are referenced by literal/constructed name inside the scripts (not via
// KEYS[]) because the test topology is N *independent standalone* primaries — the
// driver routes by workspace, so every script already runs against a single
// instance with one keyspace. A Redis Cluster deployment would instead declare the
// per-unit keys and add a per-shard hash tag so coordination state stays slot-local
// (named in the design, not built here).
//
// Per-shard global keys (one instance): eligible / pending_flush / leases ZSETs,
// the dlq stream, the maintained pending_tasks counter.
// Per-unit keys: wu:<member> hash, s:<member> stream, consumer group "g".
// member == "workspace|session|peer" (also the ZSET member).

type scripts struct {
	enqueue   *rueidis.Lua
	claim     *rueidis.Lua
	ack       *rueidis.Lua
	release   *rueidis.Lua
	heartbeat *rueidis.Lua
	fail      *rueidis.Lua
	reap      *rueidis.Lua
}

func loadScripts() scripts {
	return scripts{
		enqueue:   rueidis.NewLuaScript(luaEnqueue),
		claim:     rueidis.NewLuaScript(luaClaim),
		ack:       rueidis.NewLuaScript(luaAck),
		release:   rueidis.NewLuaScript(luaRelease),
		heartbeat: rueidis.NewLuaScript(luaHeartbeat),
		fail:      rueidis.NewLuaScript(luaFail),
		reap:      rueidis.NewLuaScript(luaReap),
	}
}

// Enqueue. ARGV: member, payload, cost, threshold, max_wait_ms, now_ms.
// Maintains pending_cost (I3, O(1)), assigns a 0-based per-unit seq (== M1's
// next_seq), (re)starts the flush clock on an empty/revived unit, and indexes the
// unit into eligible|pending_flush ONLY while it is free (I4 gate). Returns seq.
const luaEnqueue = `
local m = ARGV[1]
local wu = 'wu:'..m
local s = 's:'..m
redis.pcall('XGROUP','CREATE',s,'g','$','MKSTREAM')   -- idempotent; BUSYGROUP ignored
local total = redis.call('HINCRBY', wu, 'pending_cost', ARGV[3])
local seq = redis.call('HINCRBY', wu, 'next_seq', 1) - 1   -- 0-based: oracle/PG parity
redis.call('XADD', s, '*', 'seq', seq, 'ts', ARGV[6], 'p', ARGV[2], 'c', ARGV[3])
redis.call('INCR', 'pending_tasks')
if tonumber(total) == tonumber(ARGV[3]) then              -- unit was empty → start flush clock
  redis.call('HSET', wu, 'oldest_ms', ARGV[6], 'flush_ms', tonumber(ARGV[6])+tonumber(ARGV[5]),
             'threshold', ARGV[4], 'max_wait_ms', ARGV[5])
end
if redis.call('HGET', wu, 'claimed_by') == false then     -- only re-index a FREE unit
  local fd = redis.call('HGET', wu, 'flush_ms')
  if tonumber(total) >= tonumber(ARGV[4]) then
    redis.call('ZADD','eligible', fd, m); redis.call('ZREM','pending_flush', m)
  else
    redis.call('ZADD','pending_flush', fd, m)
  end
end
return seq
`

// Claim. ARGV: worker, lease_token, lease_ms, now_ms.
// ZPOPMIN atomically removes the lowest-flush_deadline unit from eligible and grants
// the lease (I2 exclusivity, age-fair). Returns the member, or "" when none.
const luaClaim = `
local hit = redis.call('ZPOPMIN', 'eligible', 1)
if hit[1] == nil then return '' end
local m = hit[1]
local exp = tonumber(ARGV[4]) + tonumber(ARGV[3])
redis.call('HSET', 'wu:'..m, 'claimed_by', ARGV[1], 'lease_token', ARGV[2], 'lease_ms', ARGV[3], 'lease_exp', exp)
redis.call('ZADD', 'leases', exp, m)
return m
`

// Ack. ARGV: member, lease_token, through_seq, now_ms.
// Stateless seq→stream-ID map: scan this unit's PEL in stream-ID order (== seq
// order, since the single thread assigns next_seq and the XADD id together), XACK +
// XDEL every entry with seq <= through (I5: gone, never redelivered), decrement the
// aggregate by exactly those costs (self-validating, no caller-supplied sum),
// recompute the new head's age + flush clock, then keep | release | tombstone.
// Returns 1 if still held, 0 otherwise.
const luaAck = `
local m = ARGV[1]
local wu = 'wu:'..m
local s = 's:'..m
if redis.call('HGET', wu, 'lease_token') ~= ARGV[2] then return redis.error_reply('LEASELOST') end
local through = tonumber(ARGV[3])
local acked_cost = 0
local acked_n = 0
-- Scan this consumer's PEL in stream-ID (== seq) order and ack/del every entry with
-- seq <= through. Paged so it's correct for ANY in-flight depth (PEL is bounded by
-- the drain batch, but never assume batch < a magic cap); entries are seq-ordered,
-- so we stop the moment we pass the through-seq.
local start = '-'
local page = 256
while true do
  local pend = redis.call('XPENDING', s, 'g', start, '+', page)
  if pend[1] == nil then break end
  local done = false
  for _, p in ipairs(pend) do
    local id = p[1]
    start = '(' .. id   -- exclusive: resume after this id on the next page
    local e = redis.call('XRANGE', s, id, id)
    if e[1] then
      local fv = e[1][2]
      local sq, c
      for i=1,#fv,2 do
        if fv[i]=='seq' then sq=tonumber(fv[i+1]) end
        if fv[i]=='c'   then c =tonumber(fv[i+1]) end
      end
      if sq ~= nil and sq > through then done = true; break end   -- past the prefix: stop
      if sq ~= nil and c ~= nil then
        redis.call('XACK', s, 'g', id)
        redis.call('XDEL', s, id)
        acked_cost = acked_cost + c
        acked_n = acked_n + 1
      end
    else
      redis.call('XACK', s, 'g', id)   -- entry already gone; drop from PEL
    end
  end
  if done or #pend < page then break end
end
local total = redis.call('HINCRBY', wu, 'pending_cost', -acked_cost)
if acked_n > 0 then redis.call('DECRBY', 'pending_tasks', acked_n) end
redis.call('HSET', wu, 'attempts', 0)   -- reset retry counter on successful ack (oracle parity)
local head = redis.call('XRANGE', s, '-', '+', 'COUNT', 1)
if not head[1] then                      -- empty → tombstone via DEL (immediate, oracle parity)
  redis.call('ZREM','leases', m); redis.call('ZREM','eligible', m); redis.call('ZREM','pending_flush', m)
  redis.call('DEL', wu); redis.call('DEL', s)
  return 0
end
local hfv = head[1][2]
local hts
for i=1,#hfv,2 do if hfv[i]=='ts' then hts=tonumber(hfv[i+1]) end end
local T  = tonumber(redis.call('HGET', wu, 'threshold'))
local mw = tonumber(redis.call('HGET', wu, 'max_wait_ms'))
local flush = hts + mw
redis.call('HSET', wu, 'oldest_ms', hts, 'flush_ms', flush)
local keep = (tonumber(total) >= T) or (flush <= tonumber(ARGV[4]))   -- per-head flush, never sticky
if keep then
  local lms = tonumber(redis.call('HGET', wu, 'lease_ms'))
  local exp = tonumber(ARGV[4]) + lms
  redis.call('HSET', wu, 'lease_exp', exp)
  redis.call('ZADD', 'leases', exp, m)
  return 1
end
redis.call('HDEL', wu, 'claimed_by', 'lease_token', 'lease_exp')   -- below T & not aged → re-buffer
redis.call('ZREM', 'leases', m)
redis.call('ZADD', 'pending_flush', flush, m)
return 0
`

// Release. ARGV: member, lease_token, now_ms. Clean give-up: clear the lease and
// re-index the unit free (or tombstone if empty). Returns 1.
const luaRelease = `
local m = ARGV[1]
local wu = 'wu:'..m
local s = 's:'..m
if redis.call('HGET', wu, 'lease_token') ~= ARGV[2] then return redis.error_reply('LEASELOST') end
redis.call('HDEL', wu, 'claimed_by', 'lease_token', 'lease_exp')
redis.call('ZREM', 'leases', m)
local head = redis.call('XRANGE', s, '-', '+', 'COUNT', 1)
if not head[1] then
  redis.call('ZREM','eligible', m); redis.call('ZREM','pending_flush', m)
  redis.call('DEL', wu); redis.call('DEL', s)
  return 1
end
local total = tonumber(redis.call('HGET', wu, 'pending_cost'))
local T     = tonumber(redis.call('HGET', wu, 'threshold'))
local flush = tonumber(redis.call('HGET', wu, 'flush_ms'))
if total >= T or flush <= tonumber(ARGV[3]) then
  redis.call('ZADD','eligible', flush, m)
else
  redis.call('ZADD','pending_flush', flush, m)
end
return 1
`

// Heartbeat. ARGV: member, lease_token, extend_ms, now_ms. Push the lease out so a
// long batch doesn't expire mid-drain. Does NOT change lease_ms (the ack-renew
// window stays the claim's lease, matching the oracle). Returns 1.
const luaHeartbeat = `
local m = ARGV[1]
local wu = 'wu:'..m
if redis.call('HGET', wu, 'lease_token') ~= ARGV[2] then return redis.error_reply('LEASELOST') end
local exp = tonumber(ARGV[4]) + tonumber(ARGV[3])
redis.call('HSET', wu, 'lease_exp', exp)
redis.call('ZADD', 'leases', exp, m)
return 1
`

// Fail. ARGV: member, lease_token, seq, reason, now_ms, max_attempts.
// Increments the unit's attempt counter; at the cap, ONLY the head may be DLQ'd
// (DLQ-ing a middle task would punch a hole in the FIFO) — XACK+XDEL it, route to
// the dlq stream, decrement the aggregate, advance the head. Always releases so the
// unit redelivers in order. Returns 1.
const luaFail = `
local m = ARGV[1]
local wu = 'wu:'..m
local s = 's:'..m
if redis.call('HGET', wu, 'lease_token') ~= ARGV[2] then return redis.error_reply('LEASELOST') end
local fseq = tonumber(ARGV[3])
local a = redis.call('HINCRBY', wu, 'attempts', 1)
local head = redis.call('XRANGE', s, '-', '+', 'COUNT', 1)
local headseq
if head[1] then
  local hfv = head[1][2]
  for i=1,#hfv,2 do if hfv[i]=='seq' then headseq=tonumber(hfv[i+1]) end end
end
if a >= tonumber(ARGV[6]) and headseq == fseq then
  local hid = head[1][1]
  local hfv = head[1][2]
  local pl, c
  for i=1,#hfv,2 do
    if hfv[i]=='p' then pl=hfv[i+1] end
    if hfv[i]=='c' then c =tonumber(hfv[i+1]) end
  end
  redis.call('XADD','dlq','*','member',m,'seq',fseq,'p',pl,'c',c,'reason',ARGV[4])
  redis.call('XACK', s, 'g', hid)
  redis.call('XDEL', s, hid)
  redis.call('HINCRBY', wu, 'pending_cost', -c)
  redis.call('DECR', 'pending_tasks')
  redis.call('HSET', wu, 'attempts', 0)
end
redis.call('HDEL', wu, 'claimed_by', 'lease_token', 'lease_exp')   -- always release
redis.call('ZREM', 'leases', m)
local head2 = redis.call('XRANGE', s, '-', '+', 'COUNT', 1)
if not head2[1] then
  redis.call('ZREM','eligible', m); redis.call('ZREM','pending_flush', m)
  redis.call('DEL', wu); redis.call('DEL', s)
  return 1
end
local hfv2 = head2[1][2]
local hts
for i=1,#hfv2,2 do if hfv2[i]=='ts' then hts=tonumber(hfv2[i+1]) end end
local mw = tonumber(redis.call('HGET', wu, 'max_wait_ms'))
local flush = hts + mw
redis.call('HSET', wu, 'oldest_ms', hts, 'flush_ms', flush)
local total = tonumber(redis.call('HGET', wu, 'pending_cost'))
local T     = tonumber(redis.call('HGET', wu, 'threshold'))
if total >= T or flush <= tonumber(ARGV[5]) then
  redis.call('ZADD','eligible', flush, m)
else
  redis.call('ZADD','pending_flush', flush, m)
end
return 1
`

// Reap. ARGV: now_ms. Two cheap ZSET sweeps over one shard: reclaim expired leases
// (crash recovery, I5) → re-index the unit free; then flush-promote aged units
// (pending_flush → eligible). Returns {reclaimed, flushed}.
const luaReap = `
local now = tonumber(ARGV[1])
local reclaimed = 0
local flushed = 0
local expired = redis.call('ZRANGEBYSCORE', 'leases', 0, now)
for _, m in ipairs(expired) do
  local wu = 'wu:'..m
  redis.call('HDEL', wu, 'claimed_by', 'lease_token', 'lease_exp')
  redis.call('ZREM', 'leases', m)
  local total = tonumber(redis.call('HGET', wu, 'pending_cost')) or 0
  local T     = tonumber(redis.call('HGET', wu, 'threshold')) or 0
  local flush = tonumber(redis.call('HGET', wu, 'flush_ms')) or 0
  if total >= T or flush <= now then
    redis.call('ZADD', 'eligible', flush, m)
  else
    redis.call('ZADD', 'pending_flush', flush, m)
  end
  reclaimed = reclaimed + 1
end
local due = redis.call('ZRANGEBYSCORE', 'pending_flush', 0, now)
for _, m in ipairs(due) do
  local sc = redis.call('ZSCORE', 'pending_flush', m)
  redis.call('ZREM', 'pending_flush', m)
  redis.call('ZADD', 'eligible', sc, m)
  flushed = flushed + 1
end
return {reclaimed, flushed}
`
