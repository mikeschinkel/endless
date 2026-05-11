# Common Patterns

Recurring session workflows, distilled.

## Starting a new piece of work

```bash
endless task next                            # see what's actionable
endless task show <id> --text --prompt       # read context
endless task claim <id>                      # claim and create worktree
eswt <id>                                    # cd into the worktree
# ... do the work ...
endless task update <id> --status verify
endless worktree land <id>                   # after the user confirms
```

## Recording a new task discovered during work

```bash
endless task add "Verb-first title" \
    --parent <current_task_id> \
    --description "What needs to happen and why"
```

Use the **literal** task ID printed by `task add`. IDs advance globally; never guess.

## Recording a decision discovered during work

If the discovery is *"we should do X instead of Y"*:

```bash
endless task update <current_task_id> --decision "Why X over Y."
```

This creates a paired decision task linked to the current one. See `endless guide decisions` — especially the **STRONG guidance** about preference vs prohibition.

## Recording that you need to revisit something

```bash
endless task update <id> --status revisit
```

`revisit` says "the plan needs more work before implementation continues." Pair with a note in the task `text` or a follow-up task.

## Checking what's currently in progress

```bash
endless task active                          # in_progress + verify in current project
endless task next --all                      # top actionable across all projects
endless task recent                          # most recently updated
endless worktree list                        # active worktrees
```

## Handing off to another session

If a long-running task is moving from your session to another:

```bash
endless task release <id>                    # release your claim
# Then in the other session:
endless task claim <id>
```

Or for a fresh session entirely:

```bash
endless task spawn <id>
```

(See `endless guide spawn`.)

## Reading a task you didn't claim

Most read operations work without claiming:

```bash
endless task show <id> --text --prompt --children --llm
endless task deps <id>
endless task list --tree --parent <id>
```

You only need to claim before editing files (enforcement) or before `task spawn`.

## Reporting completion

When you finish a task, set status to verify and tell your user the task ID:

> "Done — E-1248 is ready for verification."

This lets your user run `endless task show E-1248` to find your work without ambiguity. Don't say "the rename is done" — say "E-1248 is ready."

## See also

- `endless guide lifecycle` — status semantics
- `endless guide tasks` — full CRUD
- `endless guide worktree` — worktree lifecycle
