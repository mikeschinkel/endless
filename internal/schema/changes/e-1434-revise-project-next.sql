-- E-1434: align the project_next family with E-1421's 2026-06-01 plan
-- revision. Renames + new columns + new index names. Drop-and-recreate per
-- no-migration-for-unshipped-software: the original V12 shape (shipped via
-- the now-deleted V-framework) holds no production data Mike or anyone else
-- depends on, and the renames touch column AND table names, so a clean
-- rebuild beats a fragile ALTER chain.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE
-- transaction and records this change's _schema_version marker after the
-- statements below. This runs once, at land time (`just land`), against the
-- populated real DB. The sandbox (`endless-sandbox init --mode empty`) and
-- tests build from schema.sql, which already declares the new shape, and
-- never apply change files — so there is no "tables already correct" path
-- to guard.
--
-- Changes:
--   * project_next_items     -> project_next_tasks (matches JSON tasks array)
--   * project_next_revisions -> project_next_events (event-sourced framing)
--   * +added_at, +updated_at on project_next_lanes
--   * +added_at, +updated_at on project_next_tasks
--   * project_next_events.revised_at    -> event_at
--   * project_next_events.change_kind   -> kind
--   * project_next_events.json_snapshot -> payload
--   * +batch_id on project_next_events  (nullable; set for batched ops)
--   * Indexes renamed in lockstep (idx_project_next_events_recent,
--     idx_project_next_tasks_task).

DROP INDEX IF EXISTS idx_project_next_lanes_priority;
DROP INDEX IF EXISTS idx_project_next_revisions_recent;
DROP INDEX IF EXISTS idx_project_next_pending_added;
DROP INDEX IF EXISTS idx_project_next_items_task;

DROP TABLE IF EXISTS project_next_revisions;
DROP TABLE IF EXISTS project_next_items;
DROP TABLE IF EXISTS project_next_pending;
DROP TABLE IF EXISTS project_next_lanes;
DROP TABLE IF EXISTS project_next;

CREATE TABLE project_next (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL UNIQUE,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

CREATE TABLE project_next_lanes (
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

CREATE TABLE project_next_tasks (
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

CREATE TABLE project_next_pending (
    id INTEGER PRIMARY KEY,
    project_next_id INTEGER NOT NULL,
    task_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(project_next_id, task_id),
    FOREIGN KEY (project_next_id) REFERENCES project_next(id) ON DELETE CASCADE
);

CREATE TABLE project_next_events (
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

CREATE INDEX idx_project_next_lanes_priority
    ON project_next_lanes(project_next_id, priority);
CREATE INDEX idx_project_next_events_recent
    ON project_next_events(project_next_id, event_at DESC);
CREATE INDEX idx_project_next_pending_added
    ON project_next_pending(project_next_id, added_at);
CREATE INDEX idx_project_next_tasks_task
    ON project_next_tasks(task_id);
