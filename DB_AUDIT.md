# Endless SQLite Schema Audit

**Date:** 2026-04-19
**Schema:** `sql/schema.sql` (18 tables)
**Go code:** `internal/monitor/*.go`
**Python code:** `src/endless/*.py`
**Web UI:** `internal/web/queries.go`

---

## Executive Summary

The schema has 18 tables. **6 are dead** (zero code references), **2 should merge**, several columns are derivable at runtime or never queried, and naming is inconsistent. The proposed simplified schema drops to **12 tables** and removes ~15 columns.

---

## Table-by-Table Findings

### 1. `projects` — KEEP (minor cleanup)

**Used by:** Everything. Core entity.

| Column | Verdict | Notes |
|--------|---------|-------|
| id, name, path, status | **Essential** | |
| label | **Keep** | Displayed in CLI and web UI |
| description | **Keep** | Displayed in CLI and web UI |
| group_name | **Keep** | Used for grouping in web dashboard |
| language | **Keep** | Displayed in status/web |
| created_at | **Keep** | Displayed in `endless status` |
| updated_at | **Keep** | Updated by scan; used to track freshness |

**Verdict: No changes needed.** All columns are read and displayed.

---

### 2. `project_deps` — KEEP

**Used by:** `src/endless/status.py:102-133` (CLI status shows dependencies/dependents).

All columns are used. No changes.

---

### 3. `documents` — KEEP

**Used by:** `src/endless/scan.py` (reads/writes all columns), `src/endless/docs_cmd.py` (reads doc_type, relative_path, size_bytes, last_modified), `src/endless/status.py:80` (count query).

All columns are actively queried. No changes.

---

### 4. `doc_dependencies` — DROP

**Used by:** Nobody. Zero references in Go or Python code. The dependency checking in `scan.py` reads rules from `.endless/config.json`, not from this table.

**Action:** Drop table.

---

### 5. `doc_regions` — DROP

**Used by:** Nobody. Zero references in Go or Python code.

**Action:** Drop table.

---

### 6. `notes` — KEEP

**Used by:** `src/endless/scan.py` (creates staleness/sprawl notes), `src/endless/status.py:88` (count query), `internal/web/queries.go:363-388` (full display).

All columns are used. No changes.

---

### 7. `sessions` — DROP

**Used by:** Nobody. Zero SQL references anywhere in Go or Python code. This was the ZSH prompt hook session table, but the prompt hook (`cmd/endless-hook/prompt.go`) writes to `activity` directly, not to `sessions`. The `ai_sessions` table handles Claude Code sessions.

This table is completely dead code.

**Action:** Drop table.

---

### 8. `ai_chats` — DROP

**Used by:** Nobody. Zero references in Go or Python code. This was designed for browser extension chat tracking that was never implemented.

**Action:** Drop table.

---

### 9. `ai_sessions` — KEEP (significant cleanup)

**Used by:** `internal/monitor/session.go` (full lifecycle), `internal/monitor/messaging.go:276-292` (pane→session lookup), `internal/web/queries.go:62-70` (join for current work), `src/endless/channel_cmd.py:50-61` (pane→session lookup).

| Column | Verdict | Notes |
|--------|---------|-------|
| id | **Keep** | PK |
| session_id | **Keep** | Claude Code session identifier |
| project_id | **Keep** | FK to projects |
| platform | **Keep** | Currently only 'claude' but designed for codex and future platforms |
| state | **Keep** | Working/idle/needs_input/ended — actively queried |
| active_goal_id | **Rename → active_task_id** | Inconsistent with rest of schema. FK to tasks. |
| working_dir | **Drop** | Derivable from `$PWD` at runtime. Written but never read in any query. |
| transcript_path | **Drop** | Written during migration but never read by any code. |
| plan_file_path | **Keep** | Actively read/written by `SetPlanFilePath`/`GetPlanFilePath` |
| tmux_pane | **Rename → process** | Used as session-to-pane mapping for messaging. Name should be technology-neutral. |
| started_at | **Keep** | Lifecycle tracking |
| last_activity | **Keep** | Used for expiration checks, ordering |
| ended_at | **Drop** | Written by `EndSession` but never read/queried. State='ended' is sufficient. |

**Rename table → `sessions`** (since the old `sessions` table is being dropped, this can take its name; it's the only session table now).

**FK fix:** `active_goal_id REFERENCES tasks(id)` — currently correct in schema.sql, but the migration code in `db.go:179-217` had to fix a historical reference to `plan_items`. The fix is already in place. After renaming to `active_task_id`, update the FK.

---

### 10. `tasks` — KEEP (minor cleanup)

**Used by:** Everything. Core entity for task management.

| Column | Verdict | Notes |
|--------|---------|-------|
| id | **Keep** | PK |
| project_id | **Keep** | FK |
| phase | **Keep** | Used in queries, display |
| title | **Keep** | Display |
| description | **Keep** | Full text content |
| status | **Keep** | Core workflow |
| type | **Keep** | task/plan/bug/research/spike/chore |
| source_file | **Keep** | Used for --replace import logic |
| sort_order | **Keep** | Ordering |
| task_id | **Audit** | This column exists but is only used in `GetProjectTaskGroups` for grouping. It appears to be a "which plan does this task belong to" field — essentially a duplicate of `parent_id`. Consider dropping if parent_id serves the same purpose. |
| parent_id | **Keep** | Tree hierarchy |
| created_at | **Keep** | Displayed |
| completed_at | **Keep** | Displayed |
| prompt | **Keep** | Used by `endless task spawn` and `endless task detail` |

**Potential drop:** `task_id` — it's used only in `GetProjectTaskGroups` which groups by `task_id`, but this could use `parent_id` or a recursive CTE instead. Worth investigating.

---

### 11. `task_dependencies` — KEEP

**Used by:** `internal/web/queries.go:152-161` (blocked_by display), `internal/web/queries.go:295-334` (dependency view).

**Naming issue:** `source_type`/`target_type` CHECK constraints include `'plan'` which is a leftover from the plans→tasks rename. Should be updated to match current vocabulary.

**Action:** Update CHECK constraint to remove 'plan' or replace with current terminology.

---

### 12. `scan_log` — KEEP

**Used by:** `src/endless/scan.py:218-258` (creates/updates scan records).

All columns used. No changes.

---

### 13. `private_files` — DROP

**Used by:** Nobody. Zero references in Go or Python code. Designed for a private file sync feature that was never implemented.

**Action:** Drop table.

---

### 14. `activity` — KEEP

**Used by:** `internal/monitor/activity.go` (writes), `internal/monitor/task.go:102-118` (context injection check), `internal/web/queries.go:87-112,336-361` (web display), `internal/web/queries.go:36` (last_activity subquery for projects).

| Column | Verdict | Notes |
|--------|---------|-------|
| id | **Keep** | PK |
| project_id | **Keep** | FK |
| source | **Keep** | 'prompt', 'claude', 'codex' |
| working_dir | **Keep** | Displayed in web UI |
| session_context | **Keep** | JSON blob, queried for event type and injection tracking |
| created_at | **Keep** | Ordering, throttle checks |

No changes needed.

---

### 15. `file_changes` — KEEP

**Used by:** `internal/monitor/filewatch.go` (reads for diff baseline, writes new changes).

All columns used. No changes.

---

### 16. `channels` — KEEP (no rename needed)

**Used by:** `internal/monitor/session.go:162-204` (register/unregister/lookup port), `src/endless/channel_cmd.py:70-103` (lookup for notification delivery).

This is the MCP channel plugin port registry. Once `msg_channels` is renamed to `conversations`, the name conflict disappears and `channels` accurately describes what these are: running channel plugin instances.

| Column | Verdict | Notes |
|--------|---------|-------|
| process | **Keep** | PK, typically TMUX_PANE |
| port | **Keep** | HTTP port |
| pid | **Keep** | Used for liveness check |
| created_at | **Keep** | Useful for auditing orphan/stale entries |

---

### 17. `msg_channels` — KEEP (rename + cleanup)

**Used by:** `internal/monitor/messaging.go` (full lifecycle), `src/endless/channel_cmd.py` (full lifecycle).

**Rename → `conversations`** — describes what it actually is (a messaging conversation between two sessions).

| Column | Verdict | Notes |
|--------|---------|-------|
| id | **Keep** | PK |
| channel_id | **Rename → conversation_id** | Unique text identifier |
| session_a | **Keep** | Initiator session_id |
| pane_a | **Rename → process_a** | Technology-neutral |
| session_b | **Keep** | Joiner session_id |
| pane_b | **Rename → process_b** | Technology-neutral |
| project_id | **Keep** | FK |
| state | **Keep** | beacon/connected/closed — all three states are actively queried |
| created_at | **Keep** | Used for ordering |
| connected_at | **Keep** | Used for ORDER BY in queries |
| closed_at | **Keep** | Useful for auditing stale/orphan conversations |

**State machine assessment:** beacon→connected→closed is NOT over-engineered. All three states are actively queried: `WHERE state = 'beacon'` (listing available), `WHERE state = 'connected'` (active messaging), and close sets `state='closed'`. This is appropriate.

---

### 18. `msg_queue` — KEEP (rename)

**Used by:** `internal/monitor/messaging.go` (send/receive), `src/endless/channel_cmd.py` (inbox).

**Rename → `messages`** — simpler, clearer.

| Column | Verdict | Notes |
|--------|---------|-------|
| id | **Keep** | PK |
| channel_id | **Rename → conversation_id** | Match parent table rename |
| sender | **Keep** | Session ID of sender |
| body | **Keep** | Message content |
| status | **Keep** | queued/delivered — both states queried |
| created_at | **Keep** | Used for ordering |
| delivered_at | **Keep** | Useful for auditing stale/orphan messages |

---

### 19. `privacy_rules` — DROP

**Used by:** Nobody. Zero references in Go or Python code. Designed for learned privacy pattern detection that was never implemented.

**Action:** Drop table.

---

## Summary of Changes

### Tables to DROP (6)
| Table | Reason |
|-------|--------|
| `doc_dependencies` | Zero code references |
| `doc_regions` | Zero code references |
| `sessions` | Zero code references (dead; superseded by `ai_sessions`) |
| `ai_chats` | Zero code references (browser extension never built) |
| `private_files` | Zero code references (sync feature never built) |
| `privacy_rules` | Zero code references (ML feature never built) |

### Tables to RENAME (3)
| Old Name | New Name | Reason |
|----------|----------|--------|
| `ai_sessions` | `sessions` | Only session table now; `ai_` prefix unnecessary |
| `msg_channels` | `conversations` | Describes what it is, not the mechanism |
| `msg_queue` | `messages` | Simpler, clearer |

### Columns to DROP (3)
| Table | Column | Reason |
|-------|--------|--------|
| `ai_sessions` | `working_dir` | Written, never read; derivable from `$PWD` |
| `ai_sessions` | `transcript_path` | Never read by any code |
| `ai_sessions` | `ended_at` | Never queried; `state='ended'` is sufficient |

### Columns to RENAME (5)
| Table | Old Name | New Name | Reason |
|-------|----------|----------|--------|
| `ai_sessions` | `active_goal_id` | `active_task_id` | Match tasks table naming |
| `ai_sessions` | `tmux_pane` | `process` | Technology-neutral |
| `msg_channels` | `channel_id` | `conversation_id` | Match table rename |
| `msg_channels` | `pane_a` / `pane_b` | `process_a` / `process_b` | Technology-neutral |

### FK Fixes
| Location | Issue | Fix |
|----------|-------|-----|
| `task_dependencies` CHECK | `source_type`/`target_type` still allow `'plan'` | Remove or keep for backwards compat |
| `ai_sessions.active_goal_id` | Name says "goal" but references tasks | Rename to `active_task_id` |

### Columns to Investigate
| Table | Column | Question |
|-------|--------|----------|
| `tasks` | `task_id` | Appears to duplicate `parent_id` purpose. Only used in one query (`GetProjectTaskGroups`). Can that query use `parent_id` instead? |

---

## Migration Complexity Note

The migration code in `internal/monitor/db.go` is already 100+ lines of incremental migrations. The proposed renames will need careful migration handling:

1. **Table renames** are straightforward: `ALTER TABLE x RENAME TO y`
2. **Column renames** are straightforward in SQLite 3.25+: `ALTER TABLE x RENAME COLUMN a TO b`
3. **Column drops** require the recreate-table pattern in SQLite (CREATE new, INSERT SELECT, DROP old, RENAME)
4. **Table drops** are simple: `DROP TABLE IF EXISTS x`

Recommend batching all changes into a single migration function, ordered: drops first (no dependencies), then renames, then column changes.

---

## Proposed Simplified Schema

```sql
-- Endless: Project Awareness System
-- SQLite Schema v2

PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

-- Projects
CREATE TABLE IF NOT EXISTS projects (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    label TEXT,
    path TEXT NOT NULL UNIQUE,
    group_name TEXT,
    description TEXT,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'paused', 'archived', 'idea', 'unregistered', 'anonymous')),
    language TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

-- Project dependencies
CREATE TABLE IF NOT EXISTS project_deps (
    project_id INTEGER NOT NULL,
    depends_on_id INTEGER NOT NULL,
    dep_type TEXT NOT NULL DEFAULT 'runtime'
        CHECK (dep_type IN ('runtime', 'dev', 'tooling')),
    notes TEXT,
    PRIMARY KEY (project_id, depends_on_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (depends_on_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Documents within projects
CREATE TABLE IF NOT EXISTS documents (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    relative_path TEXT NOT NULL,
    doc_type TEXT NOT NULL DEFAULT 'other',
    content_hash TEXT,
    size_bytes INTEGER,
    last_modified TEXT,
    last_scanned TEXT,
    is_archived INTEGER NOT NULL DEFAULT 0,
    archived_at TEXT,
    UNIQUE (project_id, relative_path),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- Notes (staleness alerts, sprawl warnings, etc.)
CREATE TABLE IF NOT EXISTS notes (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    note_type TEXT NOT NULL
        CHECK (note_type IN ('staleness', 'update_needed', 'sprawl', 'privacy', 'general')),
    message TEXT NOT NULL,
    source TEXT,
    target_doc TEXT,
    resolved INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    resolved_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- AI coding sessions (was: ai_sessions)
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL UNIQUE,
    project_id INTEGER,
    platform TEXT NOT NULL DEFAULT 'claude'
        CHECK (platform IN ('claude', 'codex')),
    state TEXT NOT NULL DEFAULT 'working'
        CHECK (state IN ('working', 'idle', 'needs_input', 'ended')),
    active_task_id INTEGER,
    plan_file_path TEXT,
    process TEXT,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
    FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL
);

-- Task items
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    phase TEXT NOT NULL DEFAULT 'now',
    title TEXT,
    description TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'needs_plan'
        CHECK (status IN ('needs_plan', 'ready', 'in_progress', 'verify', 'completed', 'blocked', 'revisit')),
    type TEXT NOT NULL DEFAULT 'task'
        CHECK (type IN ('task', 'plan', 'bug', 'research', 'spike', 'chore')),
    source_file TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    task_id INTEGER NOT NULL DEFAULT 0,
    parent_id INTEGER,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    completed_at TEXT,
    prompt TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    FOREIGN KEY (parent_id) REFERENCES tasks(id) ON DELETE SET NULL
);

-- Task dependencies (cross-project capable)
CREATE TABLE IF NOT EXISTS task_dependencies (
    id INTEGER PRIMARY KEY,
    source_type TEXT NOT NULL
        CHECK (source_type IN ('task', 'project')),
    source_id INTEGER NOT NULL,
    target_type TEXT NOT NULL
        CHECK (target_type IN ('task', 'project')),
    target_id INTEGER NOT NULL,
    dep_type TEXT NOT NULL DEFAULT 'blocks'
        CHECK (dep_type IN ('blocks', 'needs')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    UNIQUE(source_type, source_id, target_type, target_id)
);

-- Scan history
CREATE TABLE IF NOT EXISTS scan_log (
    id INTEGER PRIMARY KEY,
    scan_type TEXT NOT NULL
        CHECK (scan_type IN ('full', 'incremental', 'documents', 'sessions', 'discover')),
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    completed_at TEXT,
    projects_scanned INTEGER,
    changes_detected INTEGER
);

-- Activity log (from hooks)
CREATE TABLE IF NOT EXISTS activity (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    source TEXT NOT NULL
        CHECK (source IN ('prompt', 'claude', 'codex')),
    working_dir TEXT,
    session_context TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- File change log (from hooks)
CREATE TABLE IF NOT EXISTS file_changes (
    id INTEGER PRIMARY KEY,
    project_id INTEGER NOT NULL,
    relative_path TEXT NOT NULL,
    change_type TEXT NOT NULL
        CHECK (change_type IN ('new', 'modified', 'deleted', 'renamed')),
    old_path TEXT,
    detected_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    source TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);

-- MCP channel plugin port registry
CREATE TABLE IF NOT EXISTS channels (
    process TEXT PRIMARY KEY,
    port INTEGER NOT NULL,
    pid INTEGER NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now'))
);

-- Messaging conversations between paired sessions (was: msg_channels)
CREATE TABLE IF NOT EXISTS conversations (
    id INTEGER PRIMARY KEY,
    conversation_id TEXT NOT NULL UNIQUE,
    session_a TEXT NOT NULL,
    process_a TEXT NOT NULL,
    session_b TEXT,
    process_b TEXT,
    project_id INTEGER,
    state TEXT NOT NULL DEFAULT 'beacon'
        CHECK (state IN ('beacon', 'connected', 'closed')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    connected_at TEXT,
    closed_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL
);

-- Message queue for inter-session messaging (was: msg_queue)
CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    sender TEXT NOT NULL,
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'delivered')),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    delivered_at TEXT,
    FOREIGN KEY (conversation_id) REFERENCES conversations(conversation_id) ON DELETE CASCADE
);
```

**Result: 18 tables → 12 tables, 3 columns removed, 5 columns renamed, 3 tables renamed.**
