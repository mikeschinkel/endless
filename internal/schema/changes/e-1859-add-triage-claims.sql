-- E-1859 (reopened): add the triage_claims table — a per-task, time-boxed claim
-- taken BEFORE the sufficiency model call so the two triage paths cannot both
-- pay for the same task.
--
-- Why a claim is needed at all. Triage runs two ways: `task add` spawns a
-- detached `triage run --task N` (the latency path), and the E-698 job runner
-- sweeps the queue (the correctness path). Sweep-vs-sweep is already safe —
-- internal/jobs claims the JOB with a CAS lease — but the inline child never
-- enters the runner and so holds nothing. A sweep firing during an inline
-- child's model call re-selects the same still-`untriaged` row and pays for a
-- second call. The post-call re-read guards the WRITE but not the SPEND.
--
-- Shape mirrors the jobs lease deliberately: a time-boxed claim rather than an
-- OS lock, so a claimant that dies mid-call needs no cleanup — its claim simply
-- lapses and the next attempt re-claims. Every due/expiry comparison uses
-- SQLite's clock, never the caller's, so N racing processes share one clock.
--
-- No FK to tasks: a claim must be able to outlive a task deleted mid-call
-- rather than cascade away underneath a running model call. Rows are deleted on
-- release, and a lapsed row is overwritten in place by the next claimant, so the
-- table stays bounded by the number of tasks in flight.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records the _schema_version marker after the statements below, so a second
-- `apply-change` is skipped outright. IF NOT EXISTS makes the SQL itself
-- idempotent as well, so applying it to a DB already built from schema.sql
-- (which declares the post-migration shape) is a no-op rather than an error.

CREATE TABLE IF NOT EXISTS triage_claims (
    task_id INTEGER PRIMARY KEY,
    owner TEXT NOT NULL,
    claimed_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_triage_claims_expires
    ON triage_claims(expires_at);
