-- Schema for the Postgres work-unit queue (M1). Idempotent: run on startup.
-- Keyed on the composite natural key (ws, sess, peer) = the contract's WorkUnitKey.

CREATE TABLE IF NOT EXISTS work_units (
  ws            text   NOT NULL,
  sess          text   NOT NULL,
  peer          text   NOT NULL,
  pending_cost  bigint NOT NULL DEFAULT 0,   -- I3: += on enqueue, -= on ack
  next_seq      bigint NOT NULL DEFAULT 0,   -- per-unit monotonic seq generator
  threshold     int    NOT NULL,             -- denormalized tenant T (hot-path local)
  max_wait_ms   int    NOT NULL,             -- denormalized tenant max_wait
  eligible      boolean NOT NULL DEFAULT false,
  claimed_by    text,                        -- worker id; NULL = free
  lease_token   uuid,                        -- per-claim token (ABA-safe lease)
  lease_ms      int,                          -- lease window of the current claim (ack renews from this)
  lease_expires timestamptz,
  oldest_pending_at timestamptz,
  flush_deadline    timestamptz,
  PRIMARY KEY (ws, sess, peer)
);

CREATE TABLE IF NOT EXISTS tasks (
  ws          text   NOT NULL,
  sess        text   NOT NULL,
  peer        text   NOT NULL,
  seq         bigint NOT NULL,               -- per-unit arrival order (I1)
  payload     bytea  NOT NULL,
  cost        int    NOT NULL,
  attempts    int    NOT NULL DEFAULT 0,
  enqueued_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (ws, sess, peer, seq)
) PARTITION BY HASH (ws, sess, peer);         -- spread vacuum churn
CREATE TABLE IF NOT EXISTS tasks_p0 PARTITION OF tasks FOR VALUES WITH (MODULUS 2, REMAINDER 0);
CREATE TABLE IF NOT EXISTS tasks_p1 PARTITION OF tasks FOR VALUES WITH (MODULUS 2, REMAINDER 1);

-- THE look-ahead index: partial, over work_units (10^4-10^5), never tasks (10^6).
CREATE INDEX IF NOT EXISTS wu_claimable ON work_units (flush_deadline NULLS LAST, ws, sess, peer)
  WHERE eligible AND claimed_by IS NULL;
CREATE INDEX IF NOT EXISTS wu_leased ON work_units (lease_expires) WHERE claimed_by IS NOT NULL;
CREATE INDEX IF NOT EXISTS wu_flushable ON work_units (flush_deadline)
  WHERE NOT eligible AND claimed_by IS NULL;

CREATE TABLE IF NOT EXISTS dead_letters (
  ws text, sess text, peer text, seq bigint, payload bytea, cost int, attempts int,
  enqueued_at timestamptz, failed_at timestamptz NOT NULL DEFAULT now(), reason text
);

CREATE TABLE IF NOT EXISTS tenant_config (
  ws text PRIMARY KEY, threshold int NOT NULL, max_wait_ms int NOT NULL
);
