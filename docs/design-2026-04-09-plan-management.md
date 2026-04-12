# Endless Plan Management — Design Document

## Overview

Endless is the plan's co-author. The plan file is a collaboration surface between Claude, the user, and Endless. The DB holds the structured plan state; files are inputs and outputs.

## Continuous Plan Sync

Instead of one-time import, Endless continuously syncs plan files with the DB:

1. Claude writes/edits `~/.claude/plans/*.md` or a project's `PLAN.md`
2. PostToolUse hook detects the write (checks if path matches plan file patterns)
3. Endless parses the file, diffs against DB using stable item IDs
4. DB is source of truth for **status** (pending/in-progress/completed); file is source of truth for **content**

## Hook-Based Formatting + Context Injection

Claude Code hooks can return JSON with `additionalContext` (up to 10,000 chars) that gets injected into Claude's context. This enables:

**SessionStart:**
```json
{
  "additionalContext": "Current plan for endless:\n  Working on: #7 Update claude hook for session tracking\n  Next: #8 Store session mapping in DB\n\nUse `endless plan complete <id>` when done."
}
```

**PreToolUse (Write to plan file):**
```json
{
  "additionalContext": "Plan formatting: Use ## for phases (Now, Next, Later). Use - for items. Don't number items — Endless handles numbering."
}
```

**PostToolUse (after plan write):**
```json
{
  "additionalContext": "Plan synced: 3 new items, 1 removed, 12 unchanged."
}
```

**Exit code 2 + stderr:** Blocks the action and feeds the error back to Claude. Useful for enforcing formatting rules.

## Plan Acceptance Detection

- ExitPlanMode event in Claude Code → hook detects this → marks plan as "active"
- Before acceptance: items are `draft` status
- After acceptance: items become `pending`
- If user stops and modifies: next edit sets items back to `draft`

## Item Identity

Each item gets a **stable short ID** (e.g., `a3f2`) — a hash of task text + creation time. Survives DB rebuilds and reimports.

**Tracking mode** (configurable per-project):
- `"comments"` — `<!-- endless:a3f2 -->` after each item (default)
- `"frontmatter"` — YAML block mapping IDs to items
- `"none"` — no markers; obsolete+reimport on changes

Configure: `endless set <project>.plan_tracking=comments`

## Numbering

- DB stores `sort_order` with gaps (10, 20, 30) for insertion
- Display shows clean sequential numbers (1, 2, 3)
- Claude never manages numbers — just "insert after item X"
- Endless renumbers after every edit

## Item Lifecycle

```
draft → pending → in_progress → completed
                → blocked → pending (unblocked)
                                    → removed (item deleted from plan)
```

## Diff Strategy on Reimport

**With markers:** Match by stable ID. Preserve status of matched items. New items added as pending. Missing items marked removed.

**Without markers:** Mark old plan obsolete, import fresh. Hook warns Claude about in-progress items that need mapping.

## Cross-Project Plans

Plans can span multiple projects. The existing `project_deps` table tracks relationships.

- `plan_items.project_id` = owning project
- `plan_items.related_projects` = JSON array of other project names this item touches
- Dependency changes in one project can flag related projects' plans
- `endless plan show --all-projects` = cross-project view
- Dashboard groups cross-project plan items by project

**Populating dependencies:**
- Manual: `endless set go-tealeaves.deps=go-dt,go-cliutil`
- Auto-detect from `go.mod` replace directives (future)
- Dashboard dependency graph (future)

## Schema

```sql
-- Updated plan_items table
CREATE TABLE IF NOT EXISTS plan_items (
    id INTEGER PRIMARY KEY,
    stable_id TEXT NOT NULL,
    project_id INTEGER NOT NULL,
    phase TEXT NOT NULL DEFAULT 'now',
    task_text TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'pending', 'in_progress',
                          'completed', 'blocked', 'removed')),
    source_file TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    plan_version INTEGER NOT NULL DEFAULT 1,
    related_projects TEXT,  -- JSON array of project names
    created_at TEXT NOT NULL,
    completed_at TEXT,
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE
);
```

## Implementation Order

1. Update schema (add stable_id, draft status, plan_version, related_projects)
2. Update `endless-hook claude` to detect plan file writes and inject context
3. Add plan acceptance detection (ExitPlanMode hook event)
4. Build smart diff with stable ID matching
5. Add `--all-projects` flag to plan show
6. Hook response payloads (SessionStart context, formatting guidance)

## Open Questions

1. Should the hook formatting guidance be project-specific (different projects may have different plan conventions)?
2. How to handle multiple active plans per project (e.g., main plan + a feature branch plan)?
3. Should Endless auto-detect plan files by content analysis, or only track files explicitly imported?
4. How does this interact with Claude's built-in plan mode (`~/.claude/plans/`) vs user-created `PLAN.md` files?
