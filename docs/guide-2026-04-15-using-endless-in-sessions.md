# Using Endless in Claude Code Sessions

This guide is for Claude Code sessions working on projects tracked by Endless. If Mike gives you a plan item ID, this doc tells you how to work with it.

## Background
This is background IF YOU NEED IT:

- [Endless README.md](/Users/mikeschinkel/Projects/endless/README.md)
- [Endless TODO.md](/Users/mikeschinkel/Projects/endless/TODO.md)
- [Various docs related to Endless; design, research, etc.](/Users/mikeschinkel/Projects/endless/docs)

## Status

Endless is currently in active development and as such we are "paving the cowpaths." Your use of Endless in conjunction with Mike will help him identify how to improve Endless by changing existing functionality and/or adding features. We solicit your proactive opinions on how to improve Endless. This also means there will be bugs, and at times you will need to update the SQL database directly.

## What Is Endless?

Endless is a project awareness tool that tracks what you're working on, why, and whether you declared your intent before making changes. Endless has:

- A **plan tree** — hierarchical items representing what needs to be done
- **Enforcement** — a hook that can block Write/Edit unless you've declared what you're working on (this may be disabled from time-to-time while we are still in development.)
- **Session tracking** — records which Claude session is working on which plan item
- A **web dashboard** at `http://localhost:8484` (when running)

## Quick Start: You've Been Given a Plan Item ID

### 1. See what you're working on

```bash
endless plan detail <id>
```

This shows the title, description, status, and full text (which may contain an implementation plan).

### 2. Start working on it

```bash
endless plan start <id>
```

This registers your session as actively working on that plan item. It sets the item's status to `in_progress` and links your session to it. **If enforcement is enabled for this project, you must do this before you can use Write/Edit tools.**

### 3. Do the work

Implement whatever the plan item describes. The plan item's `text` field may contain a detailed implementation plan — read it with `endless plan detail <id>`.

### 4. When done, set status to verify

```bash
endless plan update <id> --status verify
```

This signals that implementation is complete and the item needs manual verification by Mike. Do NOT mark items as `completed` — only Mike does that after verifying.

### 5. If you need to mark it complete (only if Mike confirms)

```bash
endless plan complete <id>
```

## Key Commands Reference

### Viewing Plans

```bash
# Show full plan tree for current project
endless plan show

# Show plan tree for a specific project
endless plan show --project <name>

# Show all items including completed
endless plan show --all

# Full detail for one item (title, description, text, status, phase)
endless plan detail <id>
```

### Modifying Plans

```bash
# Add a new plan item
endless plan add "Title here" --parent <parent_id> --phase now
endless plan add "Title here" --description "Longer description" --project <name>

# Update fields on an existing item
endless plan update <id> --title "New title"
endless plan update <id> --status ready
endless plan update <id> --text /path/to/file.md    # loads text from file
endless plan update <id> --parent 444               # move under a different parent
endless plan update <id> --parent 0                 # make it a root item

# Remove a plan item
endless plan remove <id>
```

### Session Management

```bash
# Start working on a plan item (registers your session)
endless plan start <id>

# Start a chat-only session (tracked but not tied to a plan item)
endless plan chat

# Mark a plan item complete
endless plan complete <id>
```

### Project Commands

```bash
# List all registered projects
endless list

# Show project status
endless status
endless status --project <name>
```

## Plan Item Statuses

| Status        | Meaning                                                   |
|---------------|-----------------------------------------------------------|
| `ready`       | Planned and ready to implement                            |
| `needs_plan`  | Not yet planned — needs design work before implementation |
| `in_progress` | Someone is actively working on it                         |
| `verify`      | Implementation done, awaiting Mike's verification         |
| `blocked`     | Waiting on something else                                 |
| `completed`   | Verified and done                                         |
| `revisit`     | Was partially planned but needs re-evaluation             |

## Plan Item Phases

| Phase | Meaning |
|-------|---------|
| `now` | Current priority |
| `next` | Up next after current work |
| `later` | Future work, not urgent |

## Things That Are Still Manual

These features are planned but not yet implemented. You'll need workarounds:

### Dependencies between plan items

The `task_dependencies` table exists in the DB but there's no CLI command for it yet (plan #575). If you need to record that one item blocks another, use SQL directly:

```bash
python3 -c "
import sqlite3, pathlib
db = sqlite3.connect(pathlib.Path.home() / '.config/endless/endless.db')
db.execute('INSERT INTO task_dependencies (source_type, source_id, target_type, target_id, dep_type) VALUES (\"task\", <blocked_id>, \"task\", <blocker_id>, \"needs\")')
db.commit()
db.close()
"
```

### Saving plan text from Claude's plan mode

When you exit plan mode and want to save the plan to a plan item's text field:

```bash
endless plan update <id> --text /path/to/plan-file.md
```

The plan file is typically at the path shown when you exit plan mode (e.g., `~/.claude/plans/<name>.md`).

### Enforcement not yet enabled for most projects

Enforcement (blocking Write/Edit without `plan start`) requires `"tracking": "enforce"` in the project's `.endless/config.json`. Most projects don't have this yet. If you want to be a good citizen, run `endless plan start <id>` anyway — it still tracks your session even without enforcement.

## Where Things Live

- **Database**: `~/.config/endless/endless.db` (SQLite)
- **Project config**: `<project>/.endless/config.json`
- **Web dashboard**: `http://localhost:8484` (start with `endless serve`)
- **Hook binary**: `/usr/local/bin/endless-hook`
- **CLI**: installed via `uv tool install` from `/Users/mikeschinkel/Projects/endless`

## Common Patterns

### Starting a new piece of work

1. `endless plan show` — see what's available
2. Pick an item (or Mike gives you one)
3. `endless plan detail <id>` — read the plan
4. `endless plan start <id>` — register your session
5. Do the work
6. `endless plan update <id> --status verify`

### Adding a new plan item you discovered during work

```bash
endless plan add "Title of new item" --parent <parent_id> --description "What needs to happen"
```

### Recording that you need to revisit something

```bash
endless plan update <id> --status revisit
```

### Checking what's currently in progress

```bash
# Workaround until `endless plan show --status` is implemented (#583)
sqlite3 ~/.config/endless/endless.db \
  "SELECT '#' || id, title FROM plans WHERE status = 'in_progress' ORDER BY sort_order"
```

A proper `--status` filter flag is planned (#583).

## Important Notes

- **Don't mark items `completed`** — set them to `verify` and let Mike confirm
- **Always `plan start` before writing code** — even if enforcement isn't on, it helps tracking
- **Plan items have hierarchy** — use `--parent` when adding items to keep the tree organized
- **The `text` field is the plan body** — `title` is the one-liner, `description` is a short summary, `text` is the full implementation plan
- **If you create a plan in Claude's plan mode**, save it to the plan item with `endless plan update <id> --text <path>`
