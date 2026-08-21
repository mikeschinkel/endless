-- Endless: Project Awareness System
-- Authoritative database schema (single source of truth).
--
-- Every table the codebase relies on is defined here, in its current shape,
-- as CREATE ... IF NOT EXISTS. This file is executed on every connection
-- (monitor.DB()), so it is a no-op on a populated DB and creates everything
-- on a fresh one. Additive change (new tables / nullable columns / indexes)
-- goes here directly. Destructive, one-off change goes in a per-ticket file
-- under internal/schema/changes/, applied once at land time.
--
-- NOTE: No CHECK constraints. SQLite cannot ALTER/DROP them without
-- rebuilding the entire table, which caused catastrophic data loss.
-- All validation is done in application code (Go + Python).

PRAGMA journal_mode=WAL;
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;

-- Schema-change marker. One row per applied per-ticket change file
-- (internal/schema/changes/<name>), keyed by the change's basename. Empty on
-- a fresh DB; populated by `endless db apply-change` at land time. Re-applying
-- a change is gated by the presence of its row.
CREATE TABLE IF NOT EXISTS _schema_version (
    name       TEXT PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

-- Projects
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    label TEXT,
    path TEXT NOT NULL UNIQUE,
    group_name TEXT,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'active',
    language TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

-- Project dependencies
CREATE TABLE IF NOT EXISTS project_deps (
    project_id INTEGER NOT NULL,
    depends_on_id INTEGER NOT NULL,
    dep_type TEXT NOT NULL DEFAULT 'runtime',
    notes TEXT,
    PRIMARY KEY (project_id, depends_on_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (depends_on_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Notes (staleness alerts, sprawl warnings, etc.)
CREATE TABLE IF NOT EXISTS notes (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    note_type TEXT NOT NULL,
    message TEXT NOT NULL,
    source TEXT,
    target_doc TEXT,
    resolved INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    resolved_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Session kinds (E-1571). SQL mirror of the SessionKind Go enum (ED-1506:
-- const-in-code is the source of truth, table exists for FK enforcement and
-- queryability). The startup integrity check fails closed on drift between
-- this table and the sessionkind.All() enum. Adding a value = add an enum
-- constant + add a seed row here. RENAMING a value = edit slug/label here; the
-- upsert below reconciles existing rows to the enum on connect (E-1659 pattern,
-- mirroring task_types): INSERT OR IGNORE could only add new ids, never correct
-- a renamed row. Runs before the integrity check in monitor.DB(), so a rename
-- self-heals with no change-file; safe because E-1818 opens a non-owned real DB
-- schema-passive.
CREATE TABLE IF NOT EXISTS session_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT INTO session_kinds (id, slug, label) VALUES
    (1, 'tmux',       'Tmux'),
    (2, 'background', 'Background')
ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label;

-- Process kinds (E-1898). SQL mirror of the ProcessKind Go enum, same ED-1506
-- rule as session_kinds above: const-in-code is the source of truth, this table
-- exists for FK enforcement and queryability, and the upsert reconciles a
-- rename on connect.
CREATE TABLE IF NOT EXISTS process_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT INTO process_kinds (id, slug, label) VALUES
    (1, 'tmux', 'Tmux pane'),
    (2, 'pid',  'OS process')
ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label;

-- WHERE a session runs, durably (E-1898).
--
-- This table exists because a bare tmux pane id is NOT unique over time: a tmux
-- server restart reissues "%414" to an unrelated pane, so a session row keyed on
-- the pane string alone silently resolves to the wrong session (E-1530) or gets
-- destroyed by a sweep judging it against the wrong server (the 2026-08-05
-- incident, which nulled 59 of 61 live bindings). The IDENTITY is the pair
-- (server_uuid, address); the UNIQUE constraint below is what makes pane-id
-- reuse structurally unable to collide rather than guarded against.
--
--   kind_id = tmux -> server_uuid is the tmux server's @server_uuid,
--                     address is the pane id ("%414")
--   kind_id = pid  -> server_uuid is NULL (no multiplexer),
--                     address is the pid as text
--
-- Rows are append-mostly and are NEVER deleted or rewritten by an observation.
-- "Session 512 was bound to pane %414 on server abc-123" is a fact; it stays
-- true after the pane, the server, and the session are gone. Liveness is not
-- stored here — it is derived at read time by JOINing the per-invocation
-- snapshot (see internal/monitor/liveness.go). Nothing in this table goes stale,
-- because nothing in it claims to describe the present.
--
-- server_uuid is nullable for two legitimate reasons: kind=pid has no server,
-- and the E-1898 migration backfills pre-existing bindings with NULL because the
-- binding server is not knowable retroactively. Those rows read 'unknown' (never
-- 'dead') and self-heal to a real server_uuid on the session's next hook.
CREATE TABLE IF NOT EXISTS processes (
    id            INTEGER PRIMARY KEY,
    kind_id       INTEGER NOT NULL DEFAULT 1,
    server_uuid   TEXT,
    address       TEXT NOT NULL,
    first_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_seen_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (kind_id) REFERENCES process_kinds(id)
);

-- The identity constraint, as an EXPRESSION index rather than an inline
-- UNIQUE (kind_id, server_uuid, address). SQLite treats every NULL as distinct
-- for UNIQUE, so an inline constraint would silently permit duplicate identities
-- for exactly the two cases where server_uuid is legitimately NULL: kind=pid
-- (no multiplexer) and E-1898's migration backfill (binding server unknowable
-- retroactively). ifnull() collapses those to a single '' key so the constraint
-- actually binds. Deterministic builtin, so it is legal in an index.
--
-- Safe to state standalone (cf. the sessions.short_id note below): the whole
-- table is new in E-1898, so the CREATE TABLE above really does run on old DBs
-- rather than no-opping, and the columns this references always exist by now.
CREATE UNIQUE INDEX IF NOT EXISTS processes_identity
    ON processes (kind_id, ifnull(server_uuid, ''), address);

-- AI coding sessions
--
-- active_epic_id (E-1571): nullable FK to tasks(id). When the session is
-- working under an epic, this holds the epic's task id while active_task_id
-- tracks the specific child the user is viewing. NULL for non-epic sessions.
-- The window-name renderer reads both: active_epic_id IS NULL -> [E-<task>];
-- equal to active_task_id -> [E-<epic>] (viewing the epic itself); different
-- -> [E-<epic>:E-<child>].
--
-- kind_id (E-1571): FK to session_kinds. 'tmux' rows are pane-bound (process_id
-- points at the `processes` row identifying the pane AND the server that issued
-- it); 'background' rows are headless agents that legitimately leave process_id
-- NULL. Defaults to 1 (tmux) for every existing and foreground-spawned row.
-- A NULL process_id reads as liveness 'unbound', never 'dead' (E-1898).
--
-- session_id (E-1568): nullable. Background agents (kind_id=2) are dispatched
-- with session_id NULL because `claude --bg` returns only the short_id at
-- dispatch; the real UUID arrives later when the bg agent's SessionStart hook
-- fires and UPDATEs this column (keyed by short_id). UNIQUE treats multiple
-- NULLs as distinct, so concurrent pending bg rows coexist.
--
-- short_id (E-1568): harness-agnostic dispatch handle. For Claude it is the
-- ~8-hex id from `claude --bg` stdout (`claude attach <short_id>`); future
-- harnesses reuse the column with their own format. The discriminator for
-- interpreting it is the existing `platform` column. The UNIQUE (short_id)
-- constraint enforces uniqueness on non-NULL handles while allowing many NULLs
-- (every tmux/foreground row leaves it NULL): SQLite treats each NULL as
-- distinct for UNIQUE. It is an inline table constraint rather than a separate
-- `CREATE UNIQUE INDEX ... WHERE short_id IS NOT NULL` ON PURPOSE — schema.sql
-- is re-applied on every monitor.DB() connection, including against a
-- pre-E-1568 DB (e.g. inside `endless db apply-change` before the e-1568 change
-- file has rebuilt the table). A standalone index statement referencing
-- short_id would error there ("no such column") because the CREATE TABLE IF NOT
-- EXISTS above no-ops on the existing old table. An inline constraint lives
-- entirely inside that skipped CREATE TABLE, so schema.sql stays a clean no-op
-- on old DBs and the change file installs the real constraint at land time.
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY,
    session_id TEXT,
    project_id INTEGER,
    platform TEXT NOT NULL DEFAULT 'claude',
    state TEXT NOT NULL DEFAULT 'working',
    active_task_id INTEGER,
    active_epic_id INTEGER,
    kind_id INTEGER NOT NULL DEFAULT 1,
    plan_file_path TEXT,
    process_id INTEGER,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity TEXT,
    transcript_offset INTEGER NOT NULL DEFAULT 0,
    summary TEXT,
    hidden INTEGER NOT NULL DEFAULT 0,
    short_id TEXT,
    -- Per-turn report state (E-1953). All four are reset when the user speaks
    -- again, because a turn is exactly the span between two user prompts.
    -- last_user_prompt is staged at UserPromptSubmit so `task report` — a
    -- subprocess with no view of the turn — can copy it into the corpus row.
    -- report_bounces is the loop guard for the never-called case, which has no
    -- session_gates row to count on. report_exempt carries the `$FULL` license
    -- across the gap between UserPromptSubmit and Stop, which are separate
    -- processes and so cannot share a flag in memory. report_runs bounds the
    -- appeal at one: the first run is the report, the second is the appeal, a
    -- third is refused — an unbounded appeal is a second bite the agent will
    -- always take.
    last_user_prompt TEXT,
    report_bounces INTEGER NOT NULL DEFAULT 0,
    report_exempt INTEGER NOT NULL DEFAULT 0,
    report_runs INTEGER NOT NULL DEFAULT 0,
    UNIQUE (session_id),
    UNIQUE (short_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
    FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (active_epic_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (kind_id) REFERENCES session_kinds(id),
    FOREIGN KEY (process_id) REFERENCES processes(id)
);

-- E-1530's two `sessions_null_process_on_end_*` triggers were REMOVED by E-1898,
-- and the change file drops them from existing databases.
--
-- They enforced "an ended session has process IS NULL", because a tmux pane id
-- alone is reused after a server restart and an ended row holding "%414" would
-- win a lookup against the live "%414". With processes.(server_uuid, address) as
-- the identity that collision cannot occur: the reissued pane is a DIFFERENT
-- processes row, so the ended row is unreachable from the new pane by
-- construction rather than by erasure.
--
-- Keeping them would now be actively harmful. "Session 512 ran on pane %414 of
-- server abc-123" is a fact that stays true after the session ends, and it is
-- the evidence that diagnosed the 2026-08-05 incident. A trigger that erases a
-- binding at end-of-life destroys exactly the history you need when a binding
-- goes wrong — and it is a destructive write driven by nothing the user did,
-- which is the class of write E-1898 removes.

-- Task types (E-1538). SQL mirror of the TaskType Go enum (ED-1506: const-in-code
-- is the source of truth, table exists for FK enforcement and queryability).
-- The startup integrity check fails closed on drift between this table and the
-- AllTaskTypes() enum. Adding a value = add an enum constant + add a seed row
-- here. RENAMING a value = edit the slug/label here; the upsert below reconciles
-- it (E-1659): the enum is the source of truth, so the seed reconciles every
-- existing row's slug/label to it on connect (INSERT OR IGNORE could only insert
-- new ids, never correct a renamed row — a populated DB seeded under an old name
-- would keep it and trip VerifyIntegrity). Because schema.SQL runs before the
-- integrity check in monitor.DB(), the reconcile self-heals a rename with no
-- change-file. This is safe only because E-1818 opens the real DB schema-passive
-- when a candidate (worktree) binary is pinned to it, so an unlanded binary can
-- never apply this reconcile to a DB it does not own.
CREATE TABLE IF NOT EXISTS task_types (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT INTO task_types (id, slug, label) VALUES
    (1, 'todo',       'Todo'),
    (2, 'bugfix',     'Bugfix'),
    (3, 'research',   'Research'),
    (4, 'epic',       'Epic'),
    (5, 'brainstorm', 'Brainstorm')
ON CONFLICT(id) DO UPDATE SET slug = excluded.slug, label = excluded.label;

-- Task items
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    parent_id INTEGER,
    title TEXT NOT NULL,
    description TEXT,
    text TEXT,
    phase TEXT NOT NULL DEFAULT 'now',
    status TEXT NOT NULL DEFAULT 'unplanned',
    source_file TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    completed_at TEXT,
    type_id INTEGER REFERENCES task_types(id),
    updated_at TEXT NOT NULL DEFAULT '',
    tier INTEGER,
    outcome TEXT,
    analysis TEXT,
    notes TEXT,
    changed_by_session INTEGER,
    removed INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (parent_id) REFERENCES tasks(id) ON DELETE SET NULL
);

-- live_tasks (E-1929, implementing ED-1547) is the read surface for tasks.
-- `task remove` no longer DELETEs: it sets removed = 1, so the id can never be
-- re-minted and the FK-free rows that deliberately outlive their task
-- (session_tasks, session_notices, task_landings) can never resurrect against
-- unrelated work.
--
-- Retaining the row is what makes this view mandatory rather than cosmetic.
-- Every read that used to drop a dangling reference by accident — an inner join
-- to `tasks` that simply matched nothing — now MATCHES the retained row, so a
-- removed task leaks into `task list`, `session status` and the monitor unless
-- the read goes through here. A view rather than an `AND removed = 0` at every
-- site because the filter is a property of "what a task is", and one omission is
-- a silent leak.
--
-- Reads use live_tasks; writes must keep naming `tasks` (SQLite views are not
-- writable). Three deliberate exceptions read `tasks` directly and say so at
-- their call site: the task-id allocator in internal/events/executor.go (see the
-- warning below), the removal path itself (it must see and set what the view
-- hides), and `task show` / `task list --removed`, which exist to render a
-- removed task.
--
-- WARNING: the allocator's `SELECT COALESCE(MAX(id), 0) + 1 FROM tasks` must
-- NEVER be pointed at this view. Through live_tasks, MAX(id) drops back past
-- every removed row and ids get reused again — reintroducing the exact bug this
-- change exists to fix, while appearing to fix it.
--
-- SELECT * is deliberate: the view inherits future tasks columns automatically,
-- so ALTER TABLE does not need a matching edit here.
--
-- NO INDEX ON tasks(removed), and that is load-bearing, not an omission.
--
-- This file is executed on EVERY connection, including the one
-- `endless db apply-change` opens before it dispatches to the change script that
-- adds the column. CREATE VIEW resolves its column names lazily (at PREPARE time
-- of a query against it), so the view above is a no-op on a DB that has no
-- `removed` column yet. CREATE INDEX resolves them EAGERLY, at CREATE time — so
-- `CREATE INDEX ... ON tasks(removed)` here would abort schema application with
-- "no such column: removed" on every populated DB, and the migration that adds
-- the column could never run. It would deadlock its own rollout.
--
-- The index bought nothing anyway: `removed = 0` matches virtually every row, so
-- SQLite would ignore an index for the view's own filter, and `removed = 1`
-- (`task list --removed`) is a rare scan over a small table.
--
-- The rule this encodes for the next column added here: an eagerly-resolved
-- reference (index, generated column, CHECK) to a column that only reaches
-- populated DBs via a change file cannot live in schema.sql.
CREATE VIEW IF NOT EXISTS live_tasks AS
    SELECT * FROM tasks WHERE removed = 0;

CREATE TRIGGER IF NOT EXISTS tasks_updated_at AFTER UPDATE ON tasks
BEGIN
    UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
    WHERE id = NEW.id AND updated_at != strftime('%Y-%m-%dT%H:%M:%S', 'now');
END;

-- Per-session change notices (E-1917). A row means "session S has not yet been
-- told that task T changed". The hook drains them on the session's next
-- UserPromptSubmit, renders one line each, and sets notified = 1.
--
-- Why one-shot rather than re-asserting current state every turn: re-assertion
-- costs tokens on every turn forever, and the cost of a MISSED notice is exactly
-- the status quo (the user corrects the agent by hand, as today). So any delivery
-- rate above zero is a strict improvement bought without per-turn context bloat.
--
-- Rows are immutable snapshots, NOT pointers at the task. By delivery time the
-- task may have moved again; a notice that re-read `tasks` would lose intermediate
-- transitions and could not render "ready → revisit" at all. Same reason an order
-- carries its own shipping address rather than joining to the customer's current
-- one.
--
-- One row per (session, update event) rather than per field: a single
-- `task update` may change status, tier and description at once, and that is one
-- edit — one line, one notified flip, one queryable event.
--
-- Per-session rows (rather than one row + a delivery table) so `notified` stays
-- correct with multiple live sessions holding the same task, and so there is an
-- audit trail of what was actually shown to whom.
--
-- No FKs, matching session_tasks: a notice must be able to outlive its session or
-- task rather than cascade away underneath an undelivered turn.
CREATE TABLE IF NOT EXISTS session_notices (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    changes TEXT NOT NULL,
    changed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    changed_by_session INTEGER,
    notified INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_session_notices_undelivered
    ON session_notices(session_id, notified);

-- tasks_notify_sessions (E-1917) fans a task change out to every session holding
-- that task in session_tasks.
--
-- In a trigger rather than application code because the rule is "changed →
-- notify; unchanged → don't": application code has to remember to compare OLD
-- against NEW at every mutation site and will eventually forget, silently. The
-- WHEN guard below fires only when a watched value actually moved, so a no-op
-- update writes nothing. Same reasoning as tasks_updated_at above.
--
-- `changes` is JSON keyed by field, each value {"before":…,"after":…}. Named keys
-- rather than a 2-element array so json_extract(changes,'$.status.after') reads as
-- what it is, and so a third key can be added later without a breaking change.
--
-- Freeform fields (description/text/analysis/notes) carry the single character
-- '…' (U+2026) in place of content — the notice says a field CHANGED without
-- reproducing it, while still distinguishing added / cleared / emptied / edited.
--
-- json(v) around each object is required: without it json_group_object embeds the
-- nested object as a *string* ({"status":"{\"before\":…}"}).
--
-- Self-suppression: changed_by_session is stamped by the Go executor (ED-903
-- — Go is the single writer) before the field update lands, so a session is not
-- told about a change it made itself. `st.session_id IS NOT NEW.changed_by_session`
-- notifies everyone when the actor is NULL, which is correct: a NULL actor is the
-- user editing from a bare terminal, the case this whole feature exists for.
--
-- DEPENDS ON tasks.changed_by_session, which the CREATE TABLE above declares for
-- fresh DBs and internal/schema/changes/e-1917-add-tasks-changed-by-session.go
-- adds to populated ones at land time. SQLite resolves a trigger body at FIRE
-- time, so on a populated DB this CREATE succeeds and UPDATE tasks then fails
-- with "no such column" until that change is applied — land before installing
-- the new binary. See the change file's ORDERING note.
-- Fan-out excludes sessions in state 'ended' (E-1917 fix): they never take
-- another turn, so their notices are undeliverable by construction and only
-- accumulate — 82% of the table was dead weight before this filter. `idle` and
-- `needs_input` sessions are NOT excluded; they can come back, and a notice
-- surviving until they do is the entire point of one-shot delivery. The JOIN is
-- inner on purpose: session_tasks deliberately has no FK to sessions, so a row
-- whose session is gone entirely has nobody to notify. A session that ends AFTER
-- its notice is written still strands a row, which is what
-- ReapNoticesForEndedSessions cleans up.
CREATE TRIGGER IF NOT EXISTS tasks_notify_sessions AFTER UPDATE ON tasks
WHEN OLD.status      IS NOT NEW.status
  OR OLD.phase       IS NOT NEW.phase
  OR OLD.tier        IS NOT NEW.tier
  OR OLD.description IS NOT NEW.description
  OR OLD.text        IS NOT NEW.text
  OR OLD.analysis    IS NOT NEW.analysis
  OR OLD.notes       IS NOT NEW.notes
BEGIN
    INSERT INTO session_notices
        (session_id, task_id, changes, changed_at, changed_by_session)
    SELECT st.session_id,
           NEW.id,
           (SELECT json_group_object(f, json(v)) FROM (
                SELECT 'status' AS f,
                       json_object('before', OLD.status, 'after', NEW.status) AS v
                 WHERE OLD.status IS NOT NEW.status
                UNION ALL
                SELECT 'phase',
                       json_object('before', OLD.phase, 'after', NEW.phase)
                 WHERE OLD.phase IS NOT NEW.phase
                UNION ALL
                SELECT 'tier',
                       json_object('before', OLD.tier, 'after', NEW.tier)
                 WHERE OLD.tier IS NOT NEW.tier
                UNION ALL
                SELECT 'description',
                       json_object(
                           'before', CASE WHEN OLD.description IS NULL THEN NULL
                                          WHEN OLD.description = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.description IS NULL THEN NULL
                                          WHEN NEW.description = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.description IS NOT NEW.description
                UNION ALL
                SELECT 'text',
                       json_object(
                           'before', CASE WHEN OLD.text IS NULL THEN NULL
                                          WHEN OLD.text = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.text IS NULL THEN NULL
                                          WHEN NEW.text = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.text IS NOT NEW.text
                UNION ALL
                SELECT 'analysis',
                       json_object(
                           'before', CASE WHEN OLD.analysis IS NULL THEN NULL
                                          WHEN OLD.analysis = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.analysis IS NULL THEN NULL
                                          WHEN NEW.analysis = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.analysis IS NOT NEW.analysis
                UNION ALL
                SELECT 'notes',
                       json_object(
                           'before', CASE WHEN OLD.notes IS NULL THEN NULL
                                          WHEN OLD.notes = ''   THEN ''
                                          ELSE '…' END,
                           'after',  CASE WHEN NEW.notes IS NULL THEN NULL
                                          WHEN NEW.notes = ''   THEN ''
                                          ELSE '…' END)
                 WHERE OLD.notes IS NOT NEW.notes
           )),
           strftime('%Y-%m-%dT%H:%M:%S', 'now'),
           NEW.changed_by_session
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
     WHERE st.task_id = NEW.id
       AND st.session_id IS NOT NEW.changed_by_session
       AND s.state != 'ended';
END;

-- Gate kinds (E-1542). SQL mirror of the GateKind Go enum (ED-1506: const-in-code
-- is the source of truth, table exists for FK enforcement and queryability). The
-- startup integrity check fails closed on drift between this table and the
-- gatekind.All() enum. Adding a value = add an enum constant + add a seed row
-- here. Seed inserts below are idempotent on a populated DB.
CREATE TABLE IF NOT EXISTS gate_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT OR IGNORE INTO gate_kinds (id, slug, label) VALUES
    (1, 'revisit', 'Revisit'),
    (2, 'relay', 'Relay');

-- Session gates (E-1542, E-1901). A pending interception for a session: while an
-- open row (cleared_at IS NULL) exists for a (session_id, kind_id) pair, a hook
-- intercepts the session. WHICH hook depends on the kind — 'revisit' blocks the
-- next tool call at PreToolUse; 'relay' blocks turn end at Stop — so the table is
-- "a pending interception", not specifically a tool-call gate.
--
-- kind_id discriminates the gate kind; named per-kind subject columns carry the
-- kind's context ('revisit' sets epic_id; 'relay' sets sanctioned_text and
-- counts bounces). A polymorphic subject_id was rejected because it loses FK
-- ON DELETE CASCADE. cleared_by records how an open row was resolved:
-- revisit_continue, revisit_pause, revisit_resolved (epic left revisit before the
-- user answered), relay_complied, relay_exhausted, relay_superseded, or
-- superseded (a newer gate replaced it). The partial index plus
-- application-level supersede-on-insert keep at most one open row per pair.
CREATE TABLE IF NOT EXISTS session_gates (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind_id INTEGER NOT NULL REFERENCES gate_kinds(id),
    epic_id INTEGER REFERENCES tasks(id) ON DELETE CASCADE,
    triggered_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    cleared_at TEXT,
    cleared_by TEXT,
    -- 'relay' kind (E-1901): the exact text the session owes the user as its
    -- final message, and how many times the Stop gate has bounced it. The
    -- bounce counter is the loop guard — Claude Code's stop_hook_active flag is
    -- undocumented, so blocking is capped on a value we control.
    sanctioned_text TEXT,
    bounces INTEGER NOT NULL DEFAULT 0,
    -- 'relay' kind (E-1953): the rest of the eval-corpus triple. sanctioned_text
    -- above is the minimized output; these two are the raw draft the agent
    -- submitted and the user message that prompted the turn. raw_draft is also
    -- what `task report --raw` prints, which is what lets the minimizer be
    -- aggressive at zero risk — nothing it cuts is destroyed, only hidden.
    --
    -- label / label_text arrive on the FOLLOWING turn ($CUT/$BLOAT/$WRONG/$GOOD
    -- as the first token of a user prompt), so they are nullable and written by
    -- a second statement against an already-closed row. task_id is attribution
    -- only — an id-less report is legitimate, so the corpus keys on the session.
    raw_draft TEXT,
    user_prompt TEXT,
    task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL,
    label TEXT,
    label_text TEXT,
    -- 'relay' kind (E-1975): the rest of what a paired replay needs to re-run a
    -- turn without re-fetching live state.
    --
    -- fetched_context is the JSON record of what the minimizer's fetch policy
    -- ASKED FOR and what came back. It is what turns the corpus row from a
    -- triple into a replayable unit: a tool-using minimizer is nondeterministic
    -- in its INPUTS, so an A/B over a frozen corpus is meaningless unless replay
    -- serves context from the record instead of re-fetching state that has since
    -- moved. NOT BACKFILLABLE — rows written before E-1975 can never gain it.
    --
    -- variant_hash attributes the row to the exact (prompt, fetch policy, bypass
    -- threshold) bundle that produced it, and task_type records which per-type
    -- bucket that bundle was drawn from. Without both, a promotion cannot tell
    -- which of its own outputs it is being judged on.
    --
    -- bypassed marks a draft that skipped the minimizer because it fell under
    -- the variant's bypass threshold. The row is still corpus: "we let this one
    -- through untouched" is exactly the evidence the threshold axis is tuned on.
    --
    -- pair_id / pair_slot group the two rows of an A/B presentation. pair_id is
    -- the id of the A row (the A row points at itself), so a pair is one query.
    -- picked records the user's `$A` / `$B` — the only real counterfactual in
    -- the corpus, and the exact judgment promotion requires.
    fetched_context TEXT,
    variant_hash TEXT,
    task_type TEXT,
    bypassed INTEGER NOT NULL DEFAULT 0,
    pair_id INTEGER,
    pair_slot TEXT,
    picked INTEGER NOT NULL DEFAULT 0,
    -- What `task report` actually PRINTED, when that differs from this row's
    -- sanctioned_text. On an A/B turn the two rows each hold their own variant
    -- while the A row holds the combined presentation the agent owes the user,
    -- so the Stop gate can accept ANY OF N sanctioned texts rather than exactly
    -- one. NULL means "same as sanctioned_text", which is every ordinary turn.
    --
    -- Any-of-N is the only thing standing between the shipped shape and both
    -- later ones (an AskUserQuestion preview where the agent sends the winner,
    -- and a left/right TUI), so it is bought now while it is one comparison.
    emitted_text TEXT
);

CREATE INDEX IF NOT EXISTS session_gates_open
    ON session_gates(session_id, kind_id) WHERE cleared_at IS NULL;

-- The corpus scan the judge and the optimizer both start from: every relay row
-- that carries a raw draft, newest first.
CREATE INDEX IF NOT EXISTS session_gates_corpus
    ON session_gates(kind_id, id) WHERE raw_draft IS NOT NULL;

-- Nav via kinds (E-1682). SQL mirror of the NavVia Go enum (ED-1506:
-- const-in-code is the source of truth, the table exists for FK enforcement
-- and queryability). The startup integrity check fails closed on drift between
-- this table and the navvia.All() enum. Adding a value = add an enum constant
-- + add a seed row here. Seed inserts below are idempotent on a populated DB.
CREATE TABLE IF NOT EXISTS nav_via_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT OR IGNORE INTO nav_via_kinds (id, slug, label) VALUES
    (1, 'manual', 'Manual'),
    (2, 'goto',   'Goto');

-- Durable session-navigation trail (E-1682). One row per focus change between
-- Claude sessions/panes, keyed by tmux client (the navigator). A global tmux
-- hook records every manual move (client-session-changed / session-window-changed)
-- and `endless session goto` (tagged via_id=goto). This is the MUTABLE runtime
-- tier — the same non-committed tier as `sessions`, NOT the committed JSONL
-- ledger; high-churn nav state must never pollute the shared multi-dev ledger.
--
-- Endpoints are stored as session ids (resolved from the focused pane) when the
-- location is a tracked session, plus the raw pane id for untracked locations:
-- from_session_id/from_pane carry the prior focus (the client's last to_*),
-- to_session_id/to_pane the new focus. to_pane is always set; the *_session_id
-- columns are nullable. Retention is unbounded append in v1 (pruning deferred).
CREATE TABLE IF NOT EXISTS session_navigations (
    id              INTEGER PRIMARY KEY,
    client          TEXT NOT NULL,
    project_id      INTEGER,
    from_session_id INTEGER,
    from_pane       TEXT,
    to_session_id   INTEGER,
    to_pane         TEXT NOT NULL,
    via_id          INTEGER NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
    FOREIGN KEY (from_session_id) REFERENCES sessions(id) ON DELETE SET NULL,
    FOREIGN KEY (to_session_id) REFERENCES sessions(id) ON DELETE SET NULL,
    FOREIGN KEY (via_id) REFERENCES nav_via_kinds(id)
);

CREATE INDEX IF NOT EXISTS session_navigations_client
    ON session_navigations(client, id);

-- Task dependencies (cross-project capable)
CREATE TABLE IF NOT EXISTS task_deps (
    id INTEGER PRIMARY KEY,
    source_type TEXT NOT NULL,
    source_id INTEGER NOT NULL,
    target_type TEXT NOT NULL,
    target_id INTEGER NOT NULL,
    dep_type TEXT NOT NULL DEFAULT 'blocks',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(source_type, source_id, target_type, target_id, dep_type)
);

-- Decisions (E-1378). Lifecycle: proposed (initial) -> accepted | rejected
-- (both terminal). status validation enforced in application code.
CREATE TABLE IF NOT EXISTS decisions (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    text TEXT,
    status TEXT NOT NULL DEFAULT 'proposed',
    origin_task_id INTEGER,
    origin_session_id INTEGER,
    notes TEXT,
    rejection_reason TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (origin_task_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (origin_session_id) REFERENCES sessions(id) ON DELETE SET NULL
);

CREATE TRIGGER IF NOT EXISTS decisions_updated_at AFTER UPDATE ON decisions
BEGIN
    UPDATE decisions SET updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
    WHERE id = NEW.id AND updated_at != strftime('%Y-%m-%dT%H:%M:%S', 'now');
END;

-- Decision-sourced relations (E-1378). Source-table mapping: rows where the
-- source is a decision live here; task-sourced rows (incl. task->decision)
-- stay in task_deps until E-1389 renames. target_kind in {'task','decision'};
-- validation in application code.
CREATE TABLE IF NOT EXISTS decision_relations (
    id INTEGER PRIMARY KEY,
    source_decision_id INTEGER NOT NULL,
    target_kind TEXT NOT NULL,
    target_id INTEGER NOT NULL,
    relation_type TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(source_decision_id, target_kind, target_id, relation_type),
    FOREIGN KEY (source_decision_id) REFERENCES decisions(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_decision_relations_target
    ON decision_relations(target_kind, target_id);

-- Activity log (from hooks)
CREATE TABLE IF NOT EXISTS activity (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    source TEXT NOT NULL,
    working_dir TEXT,
    session_context TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Session conversation messages (captured from JSONL transcripts via hooks)
CREATE TABLE IF NOT EXISTS session_messages (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    tool_name TEXT,
    message_uuid TEXT UNIQUE,
    created_at TEXT NOT NULL,
    FOREIGN KEY (session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_session_messages_session
    ON session_messages(session_id, created_at DESC);

-- Full-text search across session messages
CREATE VIRTUAL TABLE IF NOT EXISTS session_messages_fts USING fts5(
    content,
    content=session_messages,
    content_rowid=id
);

CREATE TRIGGER IF NOT EXISTS session_messages_ai AFTER INSERT ON session_messages BEGIN
    INSERT INTO session_messages_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER IF NOT EXISTS session_messages_ad AFTER DELETE ON session_messages BEGIN
    INSERT INTO session_messages_fts(session_messages_fts, rowid, content) VALUES('delete', old.id, old.content);
END;

-- Per-task landing history (E-1337). Append-only: every successful
-- `endless worktree land` writes one row. The reaper queries
-- MAX(landed_at) per task_id to decide when a worktree dir is eligible
-- for removal. Re-landing (post-land bug fix) appends a second row;
-- the first row is preserved.
-- branch is nullable: a historical/record-only landing (E-1719) has no
-- surviving branch to name (the worktree is long gone), so it records NULL
-- rather than a fabricated name. A normal live land still records its branch.
-- base_branch (E-2005) is the branch the work landed ON; `branch` is the one it
-- landed FROM. Nullable for the same reason `branch` is: a record-only backfill
-- has no base branch to name, and "main" written there would be a guess stored
-- as a fact.
--
-- landed_by_harness (E-2005) names the agent harness that ran the land — an
-- agentenv.ID, or NULL when a PERSON ran it. It is the axis session_id cannot
-- supply: session_id answers "which session is this about" and its resolver
-- deliberately credits a bare shell in a sibling tmux pane to the agent beside
-- it, so a human's land routinely arrives carrying an agent's session id.
-- Written by the Go executor from the event envelope's actor.harness, observed
-- in the emitting process rather than declared by its caller.
CREATE TABLE IF NOT EXISTS task_landings (
    id                INTEGER PRIMARY KEY,
    task_id           INTEGER NOT NULL,
    session_id        INTEGER,
    branch            TEXT,
    base_branch       TEXT,
    merge_commit_sha  TEXT    NOT NULL,
    landed_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    landed_by_harness TEXT,
    FOREIGN KEY (task_id)    REFERENCES tasks(id)    ON DELETE CASCADE,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_task_landings_task
    ON task_landings(task_id, landed_at DESC);

-- task_landings_notify_sessions (E-2005) tells a session its work reached the
-- base branch.
--
-- Landing changes no field tasks_notify_sessions watches, so before this trigger
-- a session was never told its own task had landed — the notice a user most
-- wants after landing from another terminal simply did not exist.
--
-- It writes into session_notices rather than a parallel table on purpose: the
-- one-shot drain, the `notified` flag, ReapNoticesForEndedSessions, the
-- .endless/logs/session-notices.jsonl delivery log, the ordering after the
-- active-task line, and E-2001's framing fix (without which none of it reaches
-- the agent) all come free. The synthetic `landed` key is not a task field, so
-- monitor.RenderNotice handles it ahead of noticeFieldOrder.
--
-- SUPPRESSION, and why it is spelled on the harness rather than the session:
-- an AGENT is not told about a land it performed itself, and a PERSON's land is
-- announced to every holder INCLUDING the session the land was attributed to.
-- That second half is the 99th-percentile case and is inexpressible from
-- session_id alone, because a human running `endless worktree land` in a
-- sibling pane produces the adjacent agent's session id (E-1294) — suppressing
-- on `st.session_id = NEW.session_id` alone would silence exactly the session
-- that needed to hear. NULL harness therefore notifies everybody, which is the
-- direction a missed detection must fail in: one redundant FYI, never silence.
--
-- Fan-out excludes sessions in state 'ended' for the same reason
-- tasks_notify_sessions does: they never take another turn, so their notices
-- are undeliverable by construction and only accumulate.
--
-- A full-ledger replay fires this too, because ProjectToTempDB applies this
-- whole file to its scratch DB. Harmless: that DB has no sessions and no
-- session_tasks (neither is rebuilt from the ledger), and `event rebuild` copies
-- back only tasks, decisions and decision_relations — never session_notices.
--
-- changed_by_session records the acting session ONLY when an agent acted,
-- matching what tasks.changed_by_session means for the sibling trigger: a
-- human's land stamps NULL there even though session_id on the landing row is
-- populated.
--
-- json() around the inner object is required: without it json_object embeds the
-- nested object as a *string*. Same trap as tasks_notify_sessions above.
--
-- DEPENDS ON task_landings.base_branch and .landed_by_harness, which the CREATE
-- TABLE above declares for fresh DBs and
-- internal/schema/changes/e-2005-add-task-landing-notice-columns.go adds to
-- populated ones at land time. SQLite resolves a trigger body at FIRE time, so
-- on a populated DB this CREATE succeeds and INSERT INTO task_landings then
-- fails with "no such column" until that change is applied — land before
-- installing the new binary. See the change file's ORDERING note.
CREATE TRIGGER IF NOT EXISTS task_landings_notify_sessions
AFTER INSERT ON task_landings
BEGIN
    INSERT INTO session_notices
        (session_id, task_id, changes, changed_at, changed_by_session)
    SELECT st.session_id,
           NEW.task_id,
           json_object('landed', json(json_object(
               'before', NULL,
               'after', CASE
                            WHEN NEW.base_branch IS NULL OR NEW.base_branch = ''
                            THEN substr(NEW.merge_commit_sha, 1, 7)
                            ELSE NEW.base_branch || '@' ||
                                 substr(NEW.merge_commit_sha, 1, 7)
                        END))),
           NEW.landed_at,
           CASE WHEN NEW.landed_by_harness IS NOT NULL THEN NEW.session_id END
      FROM session_tasks st
      JOIN sessions s ON s.id = st.session_id
     WHERE st.task_id = NEW.task_id
       AND s.state != 'ended'
       AND NOT (NEW.landed_by_harness IS NOT NULL
                AND st.session_id IS NEW.session_id);
END;

-- Session status snapshots (E-1312 / E-1314). Latest row by created_at is the
-- current status. `tasks` holds all <task> elements; `summary` holds <layer>
-- children; active_task_id joins to tasks.id.
CREATE TABLE IF NOT EXISTS session_statuses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id INTEGER,
    active_task_id INTEGER,
    created_at TEXT DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    headline TEXT,
    summary TEXT,
    tasks TEXT,
    decisions TEXT,
    commits TEXT,
    memory TEXT,
    notes TEXT,
    FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS session_statuses_session_recent_idx
    ON session_statuses (session_id, created_at DESC);

-- Session-task relations (E-1462). SQL mirror of the sessiontaskrelation.Relation
-- Go enum (ED-1506: const-in-code is the source of truth, the table exists for FK
-- enforcement and queryability). Classifies HOW a task entered a session's scope:
-- goal (the claimed task), surfaced (created during the session), revisited
-- (pre-existing, touched but not claimed). The startup integrity check fails
-- closed on drift between this table and sessiontaskrelation.All(). Adding a
-- value = add an enum constant + add a seed row here. Seeds are idempotent.
CREATE TABLE IF NOT EXISTS session_task_relations (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);

INSERT OR IGNORE INTO session_task_relations (id, slug, label) VALUES
    (1, 'goal',       'Goal'),
    (2, 'surfaced',   'Surfaced'),
    (3, 'revisited',  'Revisited'),
    -- E-1696. Ids are APPENDED, never renumbered: they are persisted in
    -- session_tasks.relation_id, so inserting in the middle would reclassify
    -- live rows. Prominence order is Relation.Rank() in Go, not the id.
    (4, 'referenced', 'Referenced'),
    (5, 'queued',     'Queued');

-- Which sessions touched which tasks (E-1322). Query-speed projection of the
-- events ledger. No FKs on session_id/task_id by design: rows must outlive their
-- referenced session/task so the "session N touched task M" record survives
-- deletion.
-- do_order (E-1683): per-session implementation order for this task. NULL =
-- unordered. Equal do_order across rows of the same session = parallelizable.
-- Session-scoped (not tasks.sort_order, which is global): two sessions may
-- order the same task differently. Set by `endless session order` via the
-- session_tasks.ordered event; replace-all (unlisted rows reset to NULL).
-- relation_id (E-1462, revised E-1696): how the task entered this session's
-- scope, FK to session_task_relations. Written at capture time by the
-- task-mutation executors (claim→goal, create/import→surfaced, else→revisited)
-- and by the session-task verbs (`session task add`→queued).
--
-- E-1696 replaced E-1462's set-once rule with an UPGRADE-ONLY ladder
-- (sessiontaskrelation.Relation.Rank(): goal < queued < surfaced < revisited <
-- referenced). A later capture may strengthen a row's relation but never weaken
-- it. Set-once was correct only while `referenced` did not exist: the documented
-- happy path reads a task before claiming it, so the read gate would otherwise
-- pin every session's own goal task at `referenced` permanently.
--
-- NULL = pre-E-1462 historical row (the live side-effect table is not replayed
-- from the ledger, so there is nothing to backfill). A NULL row is treated as
-- weaker than every real relation, so the first capture after the upgrade fills
-- it in.
CREATE TABLE IF NOT EXISTS session_tasks (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    do_order INTEGER,
    relation_id INTEGER REFERENCES session_task_relations(id),
    UNIQUE(session_id, task_id)
);
CREATE INDEX IF NOT EXISTS idx_session_tasks_task
    ON session_tasks(task_id);

-- Per-session display suppression for a task (E-1914). A row means "session S
-- has hidden task T from ITS OWN `session status` / `session monitor` listing".
-- Presence is the whole state; hidden_at exists so `--only-hidden` can order by
-- how long something has been suppressed.
--
-- Deliberately its OWN table rather than a hidden_at column on session_tasks
-- (ED-1545, revising E-1912's design). `session status` renders rows that have
-- no session_tasks row at all — read-time children, dependents and upstream
-- blockers (E-1685/E-1691/E-1795) — so a column would have forced hide to
-- fabricate a session_tasks row, and `task show`'s "Touched by:" block would
-- then report a touch that never happened. Hiding is a display act, not a
-- scope-entry, so it gets its own storage and pollutes nothing.
--
-- Hidden-ness belongs to the (session, task) PAIR and is never a property of the
-- task alone: another session's view of the same task is untouched. A hide never
-- expires — no status transition clears it, only `session unhide --task`.
--
-- No FKs, matching session_tasks: the row must be able to outlive its session or
-- task rather than cascade away underneath a live listing.
CREATE TABLE IF NOT EXISTS session_hidden_tasks (
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    hidden_at TEXT NOT NULL,
    PRIMARY KEY (session_id, task_id)
);
CREATE INDEX IF NOT EXISTS idx_session_hidden_tasks_task
    ON session_hidden_tasks(task_id);

-- Per-task triage claims (E-1859). Taken BEFORE the sufficiency model call so
-- the inline file-time path and the background sweep cannot both pay for the
-- same task; the post-call status re-read guards the write, but only a claim
-- guards the spend.
--
-- Time-boxed like the jobs lease rather than an OS lock, so a claimant that dies
-- mid-call needs no cleanup: its claim lapses and the next attempt re-claims.
-- Every due/expiry comparison uses SQLite's clock so racing processes agree.
--
-- No FK to tasks: a claim must outlive a task deleted mid-call rather than
-- cascade away underneath a running model call.
CREATE TABLE IF NOT EXISTS triage_claims (
    task_id INTEGER PRIMARY KEY,
    owner TEXT NOT NULL,
    claimed_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_triage_claims_expires
    ON triage_claims(expires_at);

-- Curated, persistent per-project "next" list (E-1421). Five tables: header,
-- lanes, tasks, auto-added pending tasks awaiting curation, and an event-
-- sourced audit log of every mutation.
CREATE TABLE IF NOT EXISTS project_next (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL UNIQUE,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_next_lanes (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    lane_id TEXT NOT NULL,
    priority INTEGER NOT NULL,
    rationale TEXT NOT NULL,
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT,
    UNIQUE(project_next_id, lane_id),
    FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_next_tasks (
    id INTEGER PRIMARY KEY,
    project_next_lane_id INTEGER NOT NULL,
    task_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    position INTEGER NOT NULL,
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT,
    UNIQUE(project_next_lane_id, task_id),
    UNIQUE(project_next_lane_id, position),
    FOREIGN KEY (project_next_lane_id) REFERENCES project_next_lanes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_next_pending (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    task_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(project_next_id, task_id),
    FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS project_next_events (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    session_id INTEGER NOT NULL,
    event_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    kind TEXT NOT NULL,
    payload TEXT,
    batch_id INTEGER,
    FOREIGN KEY (project_next_id) REFERENCES project_next(id),
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

CREATE INDEX IF NOT EXISTS idx_project_next_lanes_priority
    ON project_next_lanes(project_next_id, priority);
CREATE INDEX IF NOT EXISTS idx_project_next_events_recent
    ON project_next_events(project_next_id, event_at DESC);
CREATE INDEX IF NOT EXISTS idx_project_next_pending_added
    ON project_next_pending(project_next_id, added_at);
CREATE INDEX IF NOT EXISTS idx_project_next_tasks_task
    ON project_next_tasks(task_id);

-- Background jobs (E-698). One row per registered job, holding ONLY its
-- scheduling state — the job's identity and behavior live in Go code
-- (internal/jobs), never here. Rows are upserted by the runner on first sight
-- of a registered job; a row whose job is no longer registered is inert.
--
-- next_due_at is the "DB ticker": every comparison against it uses SQLite's
-- datetime('now'), never a Go clock, so the many session monitors that may fire
-- the runner simultaneously share one clock and cannot disagree about whether a
-- job is due.
--
-- lease_owner / lease_expires_at are the compare-and-set lease. Claiming is a
-- single conditional UPDATE whose WHERE clause IS the mutual exclusion (see
-- internal/jobs/claim.go); RowsAffected == 1 means this invocation owns the job.
-- The lease is time-boxed rather than an OS lock so a process that dies mid-run
-- needs no cleanup: its claim simply lapses and the next invocation re-claims.
-- The corollary is that a merely SLOW job can be re-claimed once its lease
-- expires, which is why jobs must be idempotent and LeaseTTL must generously
-- exceed expected runtime.
CREATE TABLE IF NOT EXISTS jobs (
    name             TEXT PRIMARY KEY,
    next_due_at      TEXT NOT NULL,
    lease_owner      TEXT,
    lease_expires_at TEXT,
    last_run_at      TEXT,
    last_ok_at       TEXT,
    last_error       TEXT,
    run_count        INTEGER NOT NULL DEFAULT 0,
    fail_count       INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_jobs_due ON jobs(next_due_at);

-- Errors (E-698). The machine-local fault record: a short, classified,
-- clearable index of things that went wrong. Written by internal/faults, whose
-- Go package is named `faults` ONLY because `errors` collides with the stdlib
-- package name — every user-facing surface (this table, the CLI verb, the docs)
-- says "errors".
--
-- This table is the INDEX ONLY. Each occurrence's full detail is appended to
-- <ConfigDir>/log/errors.jsonl, carrying this row's id and the occurrence
-- number, so diagnosis loses nothing while the table stays bounded by distinct
-- fingerprint count rather than by failure count. Modeled on
-- internal/monitor/usermachinelog.go: append-only, best-effort, never replayed
-- into the DB, and NOT the shareable ledger — faults emit no db-ledger events,
-- because they are machine-local observation, not shareable project history.
--
-- Incident model: at most ONE open row per (source, code, fingerprint), enforced
-- by the partial unique index below. Repeats of an open incident bump
-- occurrences in place. Clearing closes the row, which then becomes immutable
-- history; a recurrence AFTER clearing opens a NEW row rather than resurrecting
-- the old one, so "failed 40x last week, cleared, came back Tuesday" reads as
-- two incidents with distinct windows — the signal that pinpoints a regression.
--
-- severity is denormalized from the code catalog (internal/faults/codes.go) at
-- write time. Severity is a property of the CODE, never of the call site, so two
-- sites raising the same condition cannot disagree about whether it is a warning
-- or an error.
CREATE TABLE IF NOT EXISTS errors (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    code          TEXT NOT NULL,
    severity      TEXT NOT NULL,
    source        TEXT NOT NULL,
    fingerprint   TEXT NOT NULL,
    summary       TEXT NOT NULL,
    occurrences   INTEGER NOT NULL DEFAULT 1,
    first_seen_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_seen_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    cleared_at    TEXT,
    cleared_by    TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_errors_open_uniq
    ON errors(source, code, fingerprint) WHERE cleared_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_errors_open
    ON errors(cleared_at, severity);

-- ─── The minimizer autoresearch loop (E-1975) ────────────────────────────────
--
-- Five tables behind one idea: the minimizer prompt is not a string someone
-- edits, it is a POINTER into a content-addressed store, moved by evidence.
--
-- Rollback of a bad auto-promotion is therefore a pointer move, and that is what
-- makes removing the user's approval step safe (ED-1556) rather than reckless.

-- Free-form span-scoped labels (ED-1555). The user writes `$TOKEN "quoted span"`
-- in the ordinary flow of a reply; each span becomes one row.
--
-- The token vocabulary is NOT constrained, and that is the design rather than a
-- shortcut. A vocabulary the user cannot recall in the moment produces no label
-- at all, and label supply is upstream of everything else in the loop — so
-- consistency is leaned on (the loop asks, in band, whether a new token should
-- merge with a neighbour) and never enforced. Synonym grouping is the judge's
-- job. Drift becomes observable instead of silent.
--
-- span is NULL for an unscoped token ("$GOOD"), which is a verdict on the whole
-- reply. note is whatever free text rode along with it.
CREATE TABLE IF NOT EXISTS report_labels (
    id         INTEGER PRIMARY KEY,
    gate_id    INTEGER NOT NULL REFERENCES session_gates(id) ON DELETE CASCADE,
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    token      TEXT NOT NULL,
    span       TEXT,
    note       TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_report_labels_gate ON report_labels(gate_id);
CREATE INDEX IF NOT EXISTS idx_report_labels_token ON report_labels(token);

-- The variant store. A variant is the whole tunable bundle — prompt text, fetch
-- policy, bypass threshold — because the three axes interact: a prompt told to
-- deduplicate against the task plan is only as good as a policy that fetches it.
-- Tuning them separately would credit one axis for another's win.
--
-- Content-addressed, and deliberately NOT an embedded git repo. Every eval run
-- has to reference its variant inside this store regardless, so adding git would
-- create two identities for one object plus a sync problem. parent_hash supplies
-- the only thing git was wanted for — lineage — and diffs are computed at read
-- time.
--
-- task_type splits the space NOW rather than later: coordinating a follow-up
-- task to split it costs the user more than carrying the column from the start.
-- '' is the bucket for a turn with no claimed task.
CREATE TABLE IF NOT EXISTS minimizer_variants (
    hash             TEXT PRIMARY KEY,
    task_type        TEXT NOT NULL DEFAULT '',
    prompt_text      TEXT NOT NULL,
    fetch_policy     TEXT NOT NULL,
    bypass_threshold INTEGER NOT NULL,
    parent_hash      TEXT,
    origin           TEXT,
    note             TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_minimizer_variants_type
    ON minimizer_variants(task_type, created_at);

-- The promotion pointer — one champion per task type. Promotion writes here and
-- nowhere else, which is what makes rollback a single UPDATE.
CREATE TABLE IF NOT EXISTS minimizer_champions (
    task_type   TEXT PRIMARY KEY,
    hash        TEXT NOT NULL REFERENCES minimizer_variants(hash),
    promoted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    promoted_by TEXT,
    note        TEXT
);

-- One judgment of one corpus row.
--
-- The scores are kept apart on purpose: they are VETOES, not a weighted sum. A
-- variant that buys compression at the cost of one invariant violation is not
-- better, and an average is exactly the instrument that would say it was. So
-- invariants_ok and invented gate, fidelity gates, and only then is compression
-- maximized.
--
-- predicted_* are committed BEFORE the user reacts, and `blind` records whether
-- that was actually true for this row (a sweep that judges an old row whose
-- labels already landed is not a prediction). Only blind rows count toward the
-- calibration number, which is the loop's honesty check on its own judge.
CREATE TABLE IF NOT EXISTS report_judgments (
    id               INTEGER PRIMARY KEY,
    gate_id          INTEGER NOT NULL REFERENCES session_gates(id) ON DELETE CASCADE,
    variant_hash     TEXT,
    invariants_ok    INTEGER NOT NULL DEFAULT 1,
    invariant_detail TEXT,
    invented         INTEGER NOT NULL DEFAULT 0,
    fidelity         INTEGER,
    fidelity_detail  TEXT,
    compression      REAL,
    predicted_pick   TEXT,
    predicted_flag   INTEGER,
    actual_pick      TEXT,
    actual_flag      INTEGER,
    agreed           INTEGER,
    blind            INTEGER NOT NULL DEFAULT 1,
    judged_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE (gate_id)
);

-- One paired-replay verdict: a challenger against the champion over a FROZEN
-- corpus slice, item difficulty cancelled by pairing.
--
-- This table is the only thing allowed to gate a promotion. Rolling metrics over
-- live traffic are monitoring — an alarm, confounded by corpus drift — and the
-- distinction is not pedantry: a rolling mean improves when the work gets easier.
-- corpus_ids records exactly which rows were replayed, so a verdict stays
-- auditable after the corpus has grown past it.
CREATE TABLE IF NOT EXISTS minimizer_evals (
    id               INTEGER PRIMARY KEY,
    task_type        TEXT NOT NULL,
    challenger_hash  TEXT NOT NULL,
    champion_hash    TEXT NOT NULL,
    corpus_ids       TEXT NOT NULL,
    wins             INTEGER NOT NULL DEFAULT 0,
    losses           INTEGER NOT NULL DEFAULT 0,
    ties             INTEGER NOT NULL DEFAULT 0,
    vetoes           INTEGER NOT NULL DEFAULT 0,
    promoted         INTEGER NOT NULL DEFAULT 0,
    verdict          TEXT,
    detail           TEXT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_minimizer_evals_type
    ON minimizer_evals(task_type, created_at);

-- Loop scratch state: the A/B sample rate the user tunes in band, and the
-- optimizer's last-round timestamp.
--
-- A key/value table rather than config keys, because none of this is
-- configuration — it is state the loop moves in response to the user, and a
-- config file the loop rewrites is a file the user can no longer trust they own.
CREATE TABLE IF NOT EXISTS minimizer_state (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);
