-- E-2291: add the `bound` session-task relation and backfill one ownership row
-- per session that holds a task.
--
-- sessions.task_id is the ownership record (ED-1560), but session_tasks learned
-- about it only through the task.claimed executor. Spawned sessions are bound
-- by the SessionStart cwd auto-bind, which wrote sessions.task_id with no
-- session_tasks row at all, so most sessions' own task read `revisited` or was
-- missing from the list. From E-2291 on the auto-bind writes the row itself;
-- this step fills in the sessions bound before it.
--
-- The rule is the task's status now: `claimed` once the task has reached
-- underway or anything after it, `bound` while it is still pre-work
-- (unplanned, submitted, ready). Upgrade-only, through the ladder as it stood
-- at E-2291 — claimed 0 < bound 1 < touched 2 < filed 3 < revisited 4 <
-- referenced 5, NULL or unknown 6 — so a stronger stored row is left alone and
-- a `revisited` or `filed` row is upgraded, which is the point. The ranks are
-- written out rather than generated from the Go enum: a migration records what
-- the ladder was when it ran, and must not change meaning when the enum does.
--
-- session_tasks is a live side-effect table, never replayed from the ledger, so
-- there is no rebuild for this to agree with. It is safe to run again: a second
-- pass finds every row already at its relation or stronger.
--
-- The relation row is inserted here, not left to seeds.sql: seeds run after
-- every migration, and the backfill below needs row 6 to satisfy
-- session_tasks.relation_id's foreign key. The seed upsert then reconciles its
-- slug and label as it does every row's. The 2 and 5 renames (surfaced→filed,
-- queued→touched) need no step of their own: they are label changes the seed
-- upserts onto existing rows.
--
-- Data only: no DDL changes, so TestMigrate_MatchesSchemaSQL is unaffected.
--
-- No Down section: goose never rolls this set back in production.

-- +goose Up

INSERT OR IGNORE INTO session_task_relations (id, slug, label) VALUES (6, 'bound', 'Bound');

INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
SELECT s.id,
       s.task_id,
       CASE WHEN t.status IN ('unplanned', 'submitted', 'ready') THEN 6 ELSE 1 END,
       COALESCE(s.started_at, strftime('%Y-%m-%dT%H:%M:%S', 'now')),
       COALESCE(s.last_activity, s.started_at, strftime('%Y-%m-%dT%H:%M:%S', 'now'))
  FROM sessions s
  JOIN tasks t ON t.id = s.task_id
 WHERE s.task_id IS NOT NULL
ON CONFLICT(session_id, task_id) DO UPDATE SET
    relation_id = CASE
        WHEN (CASE excluded.relation_id
                WHEN 1 THEN 0 WHEN 6 THEN 1 WHEN 5 THEN 2
                WHEN 2 THEN 3 WHEN 3 THEN 4 WHEN 4 THEN 5 ELSE 6 END)
           < (CASE session_tasks.relation_id
                WHEN 1 THEN 0 WHEN 6 THEN 1 WHEN 5 THEN 2
                WHEN 2 THEN 3 WHEN 3 THEN 4 WHEN 4 THEN 5 ELSE 6 END)
        THEN excluded.relation_id
        ELSE session_tasks.relation_id
    END;
