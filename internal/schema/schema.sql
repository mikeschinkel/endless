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

-- AI coding sessions
--
-- active_epic_id (E-1571): nullable FK to tasks(id). When the session is
-- working under an epic, this holds the epic's task id while active_task_id
-- tracks the specific child the user is viewing. NULL for non-epic sessions.
-- The window-name renderer reads both: active_epic_id IS NULL -> [E-<task>];
-- equal to active_task_id -> [E-<epic>] (viewing the epic itself); different
-- -> [E-<epic>:E-<child>].
--
-- kind_id (E-1571): FK to session_kinds. 'tmux' rows are pane-bound (process
-- holds the tmux pane id); 'background' rows are headless agents that
-- legitimately leave process NULL. Defaults to 1 (tmux) for every existing
-- and foreground-spawned row.
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
    process TEXT,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity TEXT,
    transcript_offset INTEGER NOT NULL DEFAULT 0,
    summary TEXT,
    hidden INTEGER NOT NULL DEFAULT 0,
    short_id TEXT,
    UNIQUE (session_id),
    UNIQUE (short_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
    FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (active_epic_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (kind_id) REFERENCES session_kinds(id)
);

-- E-1530 invariant: a session in state='ended' has process IS NULL. Code
-- writes also NULL process at end-of-life (Layer A); these triggers are
-- the schema-level backstop (Layer B). Required because tmux pane ids
-- (`%N`) are reused after a tmux server restart — without NULLing
-- `process` at end-of-life, lookups for new-server panes hit ghost rows
-- from the prior server. SQLite's recursive_triggers is OFF by default,
-- and the WHEN clause short-circuits anyway, so the inner UPDATE doesn't
-- recurse the AFTER UPDATE trigger.
CREATE TRIGGER IF NOT EXISTS sessions_null_process_on_end_update
AFTER UPDATE OF state ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;

CREATE TRIGGER IF NOT EXISTS sessions_null_process_on_end_insert
AFTER INSERT ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;

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
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (parent_id) REFERENCES tasks(id) ON DELETE SET NULL
);

CREATE TRIGGER IF NOT EXISTS tasks_updated_at AFTER UPDATE ON tasks
BEGIN
    UPDATE tasks SET updated_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
    WHERE id = NEW.id AND updated_at != strftime('%Y-%m-%dT%H:%M:%S', 'now');
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
    bounces INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS session_gates_open
    ON session_gates(session_id, kind_id) WHERE cleared_at IS NULL;

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

-- MCP channel plugin port registry
CREATE TABLE IF NOT EXISTS channels (
    process TEXT PRIMARY KEY,
    port INTEGER NOT NULL,
    pid INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

-- Messaging conversations between paired AI sessions
CREATE TABLE IF NOT EXISTS conversations (
    id INTEGER PRIMARY KEY,
    conversation_id TEXT NOT NULL UNIQUE,
    process_a TEXT NOT NULL,
    process_b TEXT,
    project_id INTEGER,
    state TEXT NOT NULL DEFAULT 'beacon',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    connected_at TEXT,
    closed_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
);

-- Message queue for inter-session messaging
CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    sender TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    delivered_at TEXT,
    FOREIGN KEY (conversation_id) REFERENCES conversations(conversation_id) ON DELETE CASCADE
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
CREATE TABLE IF NOT EXISTS task_landings (
    id               INTEGER PRIMARY KEY,
    task_id          INTEGER NOT NULL,
    session_id       INTEGER,
    branch           TEXT,
    merge_commit_sha TEXT    NOT NULL,
    landed_at        TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (task_id)    REFERENCES tasks(id)    ON DELETE CASCADE,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_task_landings_task
    ON task_landings(task_id, landed_at DESC);

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
    (1, 'goal',      'Goal'),
    (2, 'surfaced',  'Surfaced'),
    (3, 'revisited', 'Revisited');

-- Which sessions touched which tasks (E-1322). Query-speed projection of the
-- events ledger. No FKs on session_id/task_id by design: rows must outlive their
-- referenced session/task so the "session N touched task M" record survives
-- deletion.
-- do_order (E-1683): per-session implementation order for this task. NULL =
-- unordered. Equal do_order across rows of the same session = parallelizable.
-- Session-scoped (not tasks.sort_order, which is global): two sessions may
-- order the same task differently. Set by `endless session order` via the
-- session_tasks.ordered event; replace-all (unlisted rows reset to NULL).
-- relation_id (E-1462): how the task entered this session's scope, FK to
-- session_task_relations. Set once at capture time by the task-mutation executors
-- (claim→goal, create/import→surfaced, else→revisited) and never changed on a
-- later touch. NULL = pre-E-1462 historical row (the live side-effect table is
-- not replayed from the ledger, so there is nothing to backfill).
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
