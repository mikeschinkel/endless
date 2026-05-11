# Spawning Another Claude Session for a Task

`endless task spawn` opens a new tmux window, launches Claude inside it, and pastes the task's prompt as the opening input. Use it to delegate independent work to a fresh session without juggling tmux yourself.

## Prerequisite: set the prompt

The task must have a `prompt` field set. Without one, `spawn` errors:

```bash
endless task update <id> --prompt /path/to/prompt.md
```

The prompt is the *opening input* to the spawned session — typically a directive like:

```text
You are working on E-NNNN. Run `endless task show E-NNNN --text` to read the
plan. Then claim the task and implement it. When done, set status to verify.
```

You can see what's stored with:

```bash
endless task prompt <id>           # raw output, suitable for piping
endless task show <id> --prompt    # decorated output
```

## Spawn

```bash
endless task spawn <id>
```

What it does:

1. Validates that tmux is running and you're in a tmux session (spawn fails otherwise).
2. Reads the task's prompt from the DB.
3. Creates a new tmux window named `<project>_<slug>[E-NNNN]`.
4. Sets tmux window variables `@endless_task_id` and `@endless_project_id`.
5. `cd`s to the project's main checkout (or `--worktree <path>` if given).
6. Launches `~/.local/bin/claude` (falls back to `claude` on PATH).
7. Waits ~5s for Claude to start, then enters `/plan` mode (unless `--no-plan`).
8. Pastes the prompt into the Claude input box and presses Enter.

The spawned session sees the prompt as if you'd typed it.

## Options

```bash
endless task spawn <id>
endless task spawn <id> --no-plan                # skip /plan mode; send prompt directly
endless task spawn <id> --worktree <path>        # cd to <path> before launching claude
                                                 # (e.g. the task's own worktree, so the
                                                 # spawned session reads the worktree's
                                                 # .claude/settings.json)
```

## What the spawned session needs to do

Spawn does *not* auto-claim the task. The spawned session must claim explicitly:

```bash
endless task claim <id>
```

This is by design — the spawn workflow is "here's a fresh Claude, here are its instructions"; the session then acts on them. A typical prompt tells the session to run `endless task show <id> --text --prompt`, then claim, then work.

The session can discover its task ID from the tmux window variable:

```bash
tmux show-window-options -v @endless_task_id    # prints "1248" or similar
```

## Authoring the prompt

A good task prompt:

- States the task ID up front.
- Tells the session to read context (`endless task show`, `endless guide`).
- Tells it to claim the task.
- Describes the work briefly (the full plan lives in `text`).
- Says what completion looks like (set `--status verify`, what to verify).

Keep it short — the bulk of the plan should be in `--text`, not duplicated in the prompt.

## Inspecting from outside

```bash
endless task prompt <id>            # print the raw prompt
endless task show <id> --prompt     # show prompt with task metadata
```

## See also

- `endless guide worktree` — claim creates a worktree; combine `task spawn --worktree` with the task's worktree
- `endless guide channels` — for live messaging between concurrent sessions
- `endless guide fields` — `prompt` vs `text` vs `description`
