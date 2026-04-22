# Using Endless in Claude Code Sessions

This guide is for Claude Code sessions working on projects tracked by Endless. If Mike gives you a task ID, this doc tells you how to work with it.

## Background
This is background IF YOU NEED IT:

- [Endless README.md](/Users/mikeschinkel/Projects/endless/README.md)
- [Endless TODO.md](/Users/mikeschinkel/Projects/endless/TODO.md)
- [Various docs related to Endless; design, research, etc.](/Users/mikeschinkel/Projects/endless/docs)

## Status

Endless is currently in active development and as such we are "paving the cowpaths." Your use of Endless in conjunction with Mike will help him identify how to improve Endless by changing existing functionality and/or adding features. We solicit your proactive opinions on how to improve Endless. This also means there will be bugs, and at times you will need to update the SQL database directly.

## What Is Endless?

Endless is a project awareness tool that tracks what you're working on, why, and whether you declared your intent before making changes. Endless has:

- A **task tree** — hierarchical items representing what needs to be done
- **Enforcement** — a hook that can block Write/Edit unless you've declared what you're working on (this may be disabled from time-to-time while we are still in development.)
- **Session tracking** — records which Claude session is working on which task
- A **web dashboard** at `http://localhost:8484` (when running)

## Quick Start: You've Been Given a Task ID

### 1. See what you're working on

```bash
endless task show <id>
```

This shows the title, description, status, and full text (which may contain an implementation plan).

### 2. Start working on it

```bash
endless task start <id>
```

This registers your session as actively working on that task. It sets the item's status to `in_progress` and links your session to it. **If enforcement is enabled for this project, you must do this before you can use Write/Edit tools.**

### 3. Do the work

Implement whatever the task describes. The task's `text` field may contain a detailed implementation plan — read it with `endless task show <id> --text`.

### 4. When done, set status to verify

```bash
endless task update <id> --status verify
```

This signals that implementation is complete and the item needs manual verification by Mike. Do NOT mark items as `completed` — only Mike does that after verifying.

### 5. If you need to mark it complete (only if Mike confirms)

```bash
endless task complete <id>
```

## Key Commands Reference

### Viewing Tasks

```bash
# Show task tree for current project
endless task list

# Show task tree for a specific project
endless task list --project <name>

# Show all items including completed
endless task list --all

# Filter by status or phase
endless task list --status ready
endless task list --phase now

# Flat sorted list
endless task list --sort id
endless task list --sort status

# Detail for one task
endless task show <id>
endless task show <id> --children           # include direct children
endless task show <id> --text               # include text field
endless task show <id> --prompt             # include prompt field

# Top actionable tasks, ranked by priority (leaf nodes only)
endless task next
endless task next --limit 5
endless task next --all                     # all projects
endless task next --llm                     # token-efficient output for LLMs

# Most recently updated tasks
endless task recent
endless task recent --limit 5
```

### Modifying Tasks

```bash
# Add a new task
endless task add "Title here" --parent <parent_id> --phase now
endless task add "Title here" --description "Longer description" --project <name>

# Update fields on an existing item
endless task update <id> --title "New title"
endless task update <id> --status ready
endless task update <id> --text /path/to/file.md    # loads text from file
endless task update <id> --parent 444               # move under a different parent
endless task update <id> --parent 0                 # make it a root item

# Remove a task
endless task remove <id>
```

### Session Management

```bash
# Start working on a task (registers your session)
endless task start <id>

# Start a chat-only session (tracked but not tied to a task)
endless task chat

# Mark a task complete
endless task complete <id>

# Complete a task and all its descendants
endless task complete <id> --cascade
```

### Project Commands

```bash
# List all registered projects
endless list

# Show project status
endless status
endless status --project <name>
```

## Task Statuses

| Status        | Meaning                                                   |
|---------------|-----------------------------------------------------------|
| `ready`       | Planned and ready to implement                            |
| `needs_plan`  | Not yet planned — needs design work before implementation |
| `in_progress` | Someone is actively working on it                         |
| `verify`      | Implementation done, awaiting Mike's verification         |
| `blocked`     | Waiting on something else                                 |
| `completed`   | Verified and done                                         |
| `revisit`     | Was partially planned but needs re-evaluation             |

## Task Phases

| Phase | Meaning |
|-------|---------|
| `now` | Current priority |
| `next` | Up next after current work |
| `later` | Future work, not urgent |

## Things That Are Still Manual

These features are planned but not yet implemented. You'll need workarounds:

### Dependencies between tasks

The `task_deps` table exists in the DB but there's no CLI command for it yet (E-575). If you need to record that one item blocks another, use SQL directly:

```bash
python3 -c "
import sqlite3, pathlib
db = sqlite3.connect(pathlib.Path.home() / '.config/endless/endless.db')
db.execute('INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES (\"task\", <blocked_id>, \"task\", <blocker_id>, \"needs\")')
db.commit()
db.close()
"
```

### Saving plan text from Claude's plan mode

When you exit plan mode and want to save the plan to a task's text field:

```bash
endless task update <id> --text /path/to/plan-file.md
```

The plan file is typically at the path shown when you exit plan mode (e.g., `~/.claude/plans/<name>.md`).

### Enforcement not yet enabled for most projects

Enforcement (blocking Write/Edit without `task start`) requires `"tracking": "enforce"` in the project's `.endless/config.json`. Most projects don't have this yet. If you want to be a good citizen, run `endless task start <id>` anyway — it still tracks your session even without enforcement.

## Where Things Live

- **Database**: `~/.config/endless/endless.db` (SQLite)
- **Project config**: `<project>/.endless/config.json`
- **Web dashboard**: `http://localhost:8484` (start with `endless serve`)
- **Hook binary**: `/usr/local/bin/endless-hook`
- **CLI**: installed via `uv tool install` from `/Users/mikeschinkel/Projects/endless`

## Common Patterns

### Starting a new piece of work

1. `endless task next` — see what's actionable
2. Pick an item (or Mike gives you one)
3. `endless task show <id>` — read the detail
4. `endless task start <id>` — register your session
5. Do the work
6. `endless task update <id> --status verify`

### Adding a new task you discovered during work

```bash
endless task add "Title of new item" --parent <parent_id> --description "What needs to happen"
```

### Recording that you need to revisit something

```bash
endless task update <id> --status revisit
```

### Checking what's currently in progress

```bash
endless task show --status in_progress
endless task next --project <name>
```

## Important Notes

- **Don't mark items `completed`** — set them to `verify` and let Mike confirm
- **Always `task start` before writing code** — even if enforcement isn't on, it helps tracking
- **Tasks have hierarchy** — use `--parent` when adding items to keep the tree organized
- **The `text` field is the task body** — `title` is the one-liner, `description` is a short summary, `text` is the full implementation plan
- **If you create a plan in Claude's plan mode**, save it to the task with `endless task update <id> --text <path>`
