# W1 — Plan-file Path Stability

> Part of E-968 (parallel-session support). See `.endless/plans/E-968.md` for full context.

## Problem

`tasks.plan_file_path` today points at `~/.claude/plans/<conversation-name>.md`. The harness names plan files per-conversation, not per-task, and Claude overwrites them when entering plan mode for unrelated work. References go stale silently. This blocks W3, which uses plan-file presence as the worktree-creation trigger.

## Fix

Endless takes ownership of plan content the moment it's attached to a task.

- `endless task update <id> --text <source-path>` reads the source, writes a copy to `.endless/plans/<task-id>.md`, and stores that endless-owned path in `tasks.plan_file_path`. The harness path is treated as a transient draft buffer.
- Re-running `--text` overwrites the endless copy. User controls when updates happen.
- The directory `.endless/plans/` is checked into the repo (these are decisions; they belong with the codebase).

## Critical files

- `src/endless/task_cmd.py` — modify `task update --text` to copy-and-rewrite into `.endless/plans/<task-id>.md` and store that path.
- `internal/schema/schema.sql` — no change; the column already exists.

## Bootstrap note

This task fixes the very problem we hit while planning E-968. The four plan files attached to E-968/E-969/E-970/E-971 were copied manually as a one-time workaround. After W1 ships, future plans flow through `task update --text` automatically.

## Verification

- Attach a plan to a task via `task update --text`; verify `.endless/plans/<id>.md` exists and `tasks.plan_file_path` points there.
- Overwrite the harness draft file at the original path; verify the task's plan content is unchanged.
- Re-run `task update --text <new-path>`; verify endless copy is overwritten with new content.
