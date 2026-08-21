-- E-1696: add the `referenced` and `queued` session-task relations, extending
-- the E-1462 vocabulary (goal / surfaced / revisited).
--
--   queued     — explicitly promoted to session work by `session task add`.
--   referenced — read-only relevance: the session looked at the task without
--                editing it. Reserved here; no emitter produces it yet. The
--                auto-capture read gate needs the gitignored machine-user
--                ledger that E-1673 routes to, so it ships separately. The
--                value is seeded now so the precedence ladder and the display
--                tier land against a stable enum.
--
-- Ids are APPENDED, never renumbered. They are persisted in
-- session_tasks.relation_id, so inserting a value in the middle would silently
-- reclassify live rows. Prominence and capture precedence are carried by
-- sessiontaskrelation.Relation.Rank() in Go (goal < queued < surfaced <
-- revisited < referenced), NOT by id order — which is why `queued` sorts above
-- `surfaced` despite holding the higher id.
--
-- No DDL: session_tasks.relation_id already exists (E-1462) and this only
-- widens the set of values it may hold. The behavioral half of E-1696 — the
-- upgrade-only precedence ladder replacing E-1462's set-once rule — is executor
-- logic (internal/events/session_tasks.go), not schema, so nothing here
-- rewrites existing rows: they keep whatever relation they were captured with
-- and are upgraded in place by the next capture that outranks it.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records the _schema_version marker after the statement below, so a second
-- `apply-change` is skipped outright. INSERT OR IGNORE makes the SQL itself
-- idempotent as well, so applying it to a DB already built from schema.sql
-- (which declares the post-migration seed set) is a no-op rather than an error.
-- Runs once, at land time; the sandbox and tests build from schema.sql and
-- never apply change files.

INSERT OR IGNORE INTO session_task_relations (id, slug, label) VALUES
    (4, 'referenced', 'Referenced'),
    (5, 'queued',     'Queued');
