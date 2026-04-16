# Endless — Many projects, all at once.

Endless is a project awareness system for solo developers managing multiple projects with AI assistants. It allows managing a myriad of software projects using AI without losing track of the details.

## Install

```bash
just install
```

This builds Go binaries to `./bin/`, symlinks them to `/usr/local/bin/`, and installs the Python CLI via `uv tool`.

## Build

```bash
just build    # templ generate, tailwind CSS, Go binaries
just test     # run Python tests
```

## CLI Reference

### Project Management

#### Register a project

```bash
endless register \
  [<path>] \
  [--infer] \
  [--name <name>] \
  [--label <label>] \
  [--desc <text>] \
  [--lang <lang>] \
  [--status active|paused|archived|idea]
```

```bash
# Register current directory, auto-detect metadata
endless register --infer

# Register a specific path with explicit fields
endless register ~/Projects/myapp --name myapp --label "My App" --lang Go --status active
```

#### List and inspect projects

```bash
endless  list [--status active|paused|archived|idea] [--group]
endless  status [<name>]
```

```bash
endless list
endless list --status active
endless list --group
endless status myapp
```

#### Modify project fields

```bash
endless set <field>=<value> [--path <partial_path>]
endless set <project>.<field>=<value> [--path <partial_path>]
````
Fields: `name`, `label`, `description`, `status`, `language`, `group_name`

```bash
# From within the project directory
endless set label="My Application"
endless set status=paused

# From anywhere, prefix with project name
endless set myapp.label="My Application"
endless set myapp.lang=Go

# Disambiguate if multiple projects share a name
endless set myapp.lang=Go --path Projects/work
```

#### Other project commands

```bash
endless rename <old_name> <new_name> [--path <partial_path>]
endless discover [<path>] [--all] [--reset]
endless unregister <name>
endless purge <name>
```

```bash
endless rename oldname newname
endless discover ~/Projects
endless unregister myapp
endless purge myapp
```

### Plan Management

Plans are a tree. Each plan can have child plans. The `plans` table stores title, description, full text, and prompt.

#### View plans

```bash
endless plan show [--project <name>] [--all]
endless plan detail <plan_id>
```

```bash
endless plan show
endless plan show --all
endless plan detail 445
```

#### Add plans

```bash
endless plan add <title> \
  [--description <text>] \
  [--parent <plan_id>] \
  [--phase now|next|later] \
  [--project <name>]
```

```bash
endless plan add "Build dashboard" --description "Web dashboard for project status"
endless plan add "Fix login bug" --parent 444 --description "Auth token expires too early"
endless plan add "Refactor DB layer" --phase next
```

#### Import plans from files

```bash
endless plan import \
  [<file>] \ 
  [--project <name>] \
  [--replace] \
  [--parent <plan_id>] \
  [--from-claude]
```

```bash
endless plan import PLAN.md --project endless
endless plan import PLAN.md --project endless --replace
endless plan import subplan.md --project endless --parent 445
endless plan import --from-claude --project endless
```

#### Update a plan

```bash
endless plan update <id> \
  [--status needs_plan|ready|in_progress|verify|completed|blocked|revisit] \
  [--title <title>] \
  [--description <text>] \
  [--text <file>] \
  [--prompt <file>] \
  [--parent <plan_id>]
```

```bash
# Change status
endless plan update 445 --status ready

# Update title and description
endless plan update 441 --title "Dependency Graph" --description "Track cross-project deps"

# Load full plan text from a file
endless plan update 449 --text plan-markdown-component.md

# Move a plan under a different parent (0 = make root)
endless plan update 506 --parent 443
```

#### Track progress

```bash
endless plan start <plan_id>
endless plan complete <plan_id>
endless plan remove <plan_id>
```

```bash
endless plan start 445
endless plan complete 445
endless plan remove 445
```

#### Spawn a session for a plan

```bash
endless plan prompt <plan_id>
endless plan spawn <plan_id> [--project <name>]
endless plan chat
```

```bash
# Review the prompt that will be sent
endless plan prompt 445

# Spawn a new tmux window with Claude working on the plan's prompt
endless plan spawn 445

# Start a chat session without plan tracking
endless plan chat
```

### Documents & Notes

```bash
endless scan [--project <name>] [--docs-only]
endless docs [<name>] [--type <type>]
```

```bash
endless scan
endless scan --project myapp --docs-only
endless docs myapp
endless docs --type readme
```

```bash
endless  notes [<name>] [--all]
endless  note add <message> [--project <name>]
endless  note resolve <note_id>
```

```bash
endless notes myapp
endless notes --all
endless note add "Review auth token expiry" --project myapp
endless note resolve 42
```

### Web Dashboard

```
endless serve                     Start the web dashboard
  --port INTEGER                  Port (default: 8484)
```

Routes:
- `/` — Dashboard homepage
- `/status` — Project status (master-detail with plan tree)
- `/status/<name>` — Project-specific status
- `/project/<name>` — Project detail (plan, activity, notes, deps)
- `/project/<name>/plan` — Full plan detail

### Hooks & Setup

```bash
endless setup prompt-hook         Install ZSH prompt hook
endless setup remove-prompt-hook  Remove ZSH prompt hook
endless setup claude-hook         Install Claude Code hook
endless setup remove-claude-hook  Remove Claude Code hook
```

The Claude Code hook handles:
- **SessionStart**: Injects plan context
- **PreToolUse**: Blocks Write/Edit without active plan session
- **PostToolUse**: Detects file changes, auto-imports plan files, tracks plan file path on session
- **ExitPlanMode**: Imports the accepted plan, using the tracked file path
- **Stop/SessionEnd**: Ends the session, records file changes

## Database

SQLite at `~/.config/endless/endless.db`. Key tables:

- `projects` — registered projects
- `plans` — hierarchical plan tree (title, description, text, prompt, parent_id)
- `ai_sessions` — Claude/Codex session tracking with active_goal_id and plan_file_path
- `activity` — hook-captured activity events
- `file_changes` — detected file modifications
- `notes` — project notes and alerts
- `documents` — tracked document metadata
- `task_dependencies` — cross-item and cross-project dependencies
