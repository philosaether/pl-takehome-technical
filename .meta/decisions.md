# Decisions

Append-only log. Don't edit old entries.

---

## 2026-06-25 — Assess the queue design space clean-room (brief only)

Exclude Honcho's *current Postgres implementation* (solution context) from the
initial assessment, per the brief's "from first principles" + "current state ≠
desired state" framing, and to demonstrate our own reasoning to the screen.
Distinction preserved: *problem context* (workload shape) is fair game — the
brief supplies enough (10⁶ tasks, 10⁴–10⁵ units, $20 cap). Open door:
validate self-derived workload numbers against real `~/Development/meta/`
Honcho figures *after* committing to a design, not before.

## 2026-06-25 — git init, backend not yet chosen

Repo initialized on `main`. Backend (Postgres-done-right vs Redis-primitives
vs custom) deliberately left open after assess — it's the graded decision,
settled at /draft after discussion.
