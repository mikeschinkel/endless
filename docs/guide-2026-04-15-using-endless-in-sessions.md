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

This signals that implementation is complete and the item needs manual verification by Mike. Do NOT mark items as `confirmed` — only Mike does that after verifying.

When reporting completion to the user (e.g. "Done", "Finished", "Ready for review"), always include the task ID so Mike knows which task you're referring to. For example: "Done — E-752 is ready for verification."

### 5. If Mike confirms the work

```bash
endless task confirm <id>
```

### 6. If you can't easily verify but believe it works

```bash
endless task assume <id>
```

This marks the task as believed complete but not yet verified. It will be confirmed when the feature is used naturally.

## Key Commands Reference

### Viewing Tasks

```bash
# List tasks for current project (flat table sorted by ID)
endless task list

# List tasks for a specific project
endless task list --project <name>

# Show all items including confirmed/assumed/declined
endless task list --all

# Filter by status, phase, or tier
endless task list --status ready
endless task list --status needs_plan,ready     # comma-separated
endless task list --phase now
endless task list --tier 1                      # or --tier auto

# Sort by different columns
endless task list --sort status
endless task list --sort tier
endless task list --sort phase

# Tree view (hierarchical)
endless task list --tree

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

# Remove a task (warns if it has children)
endless task remove <id>
endless task remove <id> --cascade            # also remove all descendants

# Move tasks between parents
endless task move <id> --parent <parent_id>   # move under a new parent
endless task move <id> --root                 # move to root
endless task move --children-of <id> --root   # move all children to root

# Search tasks
endless task search "query"                   # searches ID, title, description
endless task search "query" --text            # also search text field
endless task search "query" --status ready    # with status filter
```

### Session Management

```bash
# Start working on a task (registers your session)
endless task start <id>

# Start a chat-only session (tracked but not tied to a task)
endless task chat

# Mark a task as confirmed (only when Mike approves)
endless task confirm <id>

# Confirm a task and all its descendants
endless task confirm <id> --cascade

# Mark as assumed (believed complete, verify later)
endless task assume <id>
```

### Inter-Session Messaging

Endless supports messaging between concurrent Claude Code sessions via channels. This is useful when multiple sessions are working on related tasks in the same project.

```bash
# Session A: announce availability for messaging
endless channel beacon

# Session B: connect to the beaconing session
endless channel connect                       # auto-detects if one beacon exists
endless channel connect <channel_id>          # explicit ID if multiple beacons

# Send a message to the connected session
endless channel send "Hey — E-839 is done, task update now accepts --phase"

# Check for incoming messages
endless channel inbox

# List active beacons for a project
endless channel list
endless channel list --project <name>

# Close the channel when done
endless channel close
```

**How it works:**
- One session runs `beacon` to advertise itself as available
- Another session runs `connect` to pair with the beacon
- Messages are delivered via MCP notifications — the receiving session sees a channel event and runs `inbox` to read them
- Channels are project-scoped: `connect` auto-finds the beacon for the current project

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
| `needs_plan`  | Not yet planned — needs design work before implementation |
| `ready`       | Planned and ready to implement                            |
| `in_progress` | Someone is actively working on it                         |
| `verify`      | Implementation done, awaiting Mike's verification         |
| `confirmed`   | Verified and done                                         |
| `assumed`     | Believed complete, will verify when used naturally        |
| `blocked`     | Waiting on something else                                 |
| `revisit`     | Was partially planned but needs re-evaluation             |
| `declined`    | Active decision not to do this                            |
| `obsolete`    | Made irrelevant by other changes                          |

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
endless task active                            # shows in_progress + verify tasks
endless task next --project <name>             # top actionable tasks
```

## Important Notes

- **Don't mark items `confirmed`** — set them to `verify` and let Mike confirm, or `assume` if you can't easily verify
- **Always `task start` before writing code** — even if enforcement isn't on, it helps tracking
- **Tasks have hierarchy** — use `--parent` when adding items to keep the tree organized
- **The `text` field is the task body** — `title` is the one-liner, `description` is a short summary, `text` is the full implementation plan
- **If you create a plan in Claude's plan mode**, save it to the task with `endless task update <id> --text <path>`
