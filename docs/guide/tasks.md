# Task Commands

The `endless task` group is the core of session work. This is the full reference.

## Viewing

```bash
# List tasks for the current project (flat, sorted by ID)
endless task list

# Tree view (hierarchical)
endless task list --tree

# Include done items (confirmed/assumed/declined/obsolete)
endless task list --all

# Filter by status, phase, parent, relation
endless task list --status ready
endless task list --status needs_plan,ready          # comma-separated
endless task list --phase now
endless task list --parent E-799
endless task list --parent none                      # roots only
endless task list --related-to E-1248                # tasks related via any link
endless task list --related-to E-1248 --rel-type blocks

# Sort
endless task list --sort status     # or id, phase, tier, created, title

# Token-efficient agent output
endless task list --llm
endless task list --json

# Detail for one task
endless task show <id>
endless task show <id> --text
endless task show <id> --prompt
endless task show <id> --children
endless task show <id> --outcome
endless task show <id> --no-description
endless task show <id> --llm
endless task show <id> --json

# Top actionable tasks, ranked by priority (leaf nodes only)
endless task next
endless task next --limit 5
endless task next --all                              # across all projects
endless task next --tier 1
endless task next --phase now
endless task next --llm

# Most recently updated
endless task recent
endless task recent --limit 5

# In-progress + verify across the current project
endless task active

# Search
endless task search "query"                          # ID, title, description
endless task search "query" --text                   # also search text
endless task search "query" --prompt                 # also search prompt
endless task search "query" --status ready
endless task search "query" --parent E-799
```

## Adding

```bash
endless task add "Title here"
endless task add "Title here" --parent <parent_id>
endless task add "Title here" --description "Brief pitch" --phase now
endless task add "Title here" --text /path/to/plan.md --status ready
endless task add "Title here" --type bug              # task|plan|bug|research|spike|chore|decision
endless task add "Title here" --tier 1                # 1-4 or auto|quick|deep|discuss
endless task add "Title here" --blocked-by E-100      # also: --blocks, --relates-to,
                                                      # --implements, --cleans-up,
                                                      # --cleaned-up-by (all repeatable)
endless task add "Title here" --decision "<rationale>"  # creates paired decision-type task
```

Always use the task ID printed by `task add` literally. IDs advance globally across parallel sessions — never guess the next ID.

## Updating

```bash
endless task update <id> --title "New title"
endless task update <id> --description "..."
endless task update <id> --text /path/to/plan.md
endless task update <id> --prompt /path/to/prompt.md
endless task update <id> --status ready
endless task update <id> --phase later
endless task update <id> --tier 2
endless task update <id> --parent 444                 # move under different parent
endless task update <id> --parent 0                   # make it a root
endless task update <id> --outcome "What was done"
endless task update <id> --decision "<rationale>"     # creates paired decision task
endless task update <id> <id2> ... --status ready     # bulk update
```

Attaching a plan (`--text`) to a `needs_plan` task auto-promotes the status to `ready`.

## Status transitions

```bash
endless task claim <id>                               # ready → in_progress + create worktree
endless task release [<id>]                           # release current session's claim
endless task update <id> --status verify              # work done, awaiting verification
endless task confirm <id> --outcome "..."             # user-only — sessions do not self-confirm
endless task confirm <id> --cascade --outcome "..."   # confirm a task and descendants
endless task assume <id> --outcome "..."              # believed complete, can't verify
endless task decline <id> --reason "..."              # active decision not to do
endless task replace <id> --by <new_id>               # supersede with another task
```

## Removing / moving

```bash
endless task remove <id>                              # warns if it has children
endless task remove <id> --cascade                    # also remove descendants
endless task move <id> --parent <parent_id>
endless task move <id> --root
endless task move --children-of <id> --root
endless task clear <id> --<field>                     # clear a single field
```

## Relations

```bash
endless task block <a> --by <b>                       # A is blocked by B
endless task unblock <a> --by <b>
endless task deps <id>                                # all relations grouped by type
endless task relations <id>                           # alias of deps with grouping
endless task link <a> --to <b> --type implements      # generic typed link
endless task unlink <a> --to <b> --type implements
endless task link <a> --to <b> --type relates_to      # see decision E-958/E-1148/E-1156
```

Available `--type` values include `blocks`, `relates_to`, `implements`, `cleans_up`, `cleaned_up_by`, `documents`, `replaces`. See `endless guide decisions` for documents-link semantics.

## Sessions and chat

```bash
endless task chat                                     # start a chat-only session (no task tracking)
```

## `--llm` flag

Most read commands take `--llm` to produce token-efficient agent output. Reach for it. Examples:

```bash
endless task list --llm
endless task show <id> --llm
endless task next --llm
endless task recent --llm
endless task deps <id> --llm
```

## See also

- `endless guide fields` — what each task field is for
- `endless guide lifecycle` — what statuses mean
- `endless guide spawn` — `task spawn`/`task prompt`
- `endless guide worktree` — what happens at `task claim`
