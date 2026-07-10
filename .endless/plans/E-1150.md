# E-971 Layer F — Task-command worktree integration

You are picking up the final E-971 slice (parallel-session support via git worktrees in endless). Read `.endless/plans/E-971.md` for full context. This prompt is the short brief.

## Context: what is already shipped

- **Foundation (commit `3954f9a`)**: `endless worktree {list, current, show, for-task}` — read-only inspection.
- **Mutation slice**: `endless worktree {land, drop}` with E-987's auto-commit + rebase + ff-merge + retry algorithm.
- **Layer D (commit `7199f5f`)**: SessionStart claim/adopt/create per-session worktree, PreToolUse worktree↔task match, SessionEnd lock release. Worktrees are now lifecycle-managed automatically.
- **Layer E (commit `c11ba0a`, E-1126)**: UserPromptSubmit handler with Layer 1 reminder + Layer 2 pivot gate. Pivot triggers pause Write/Edit until Claude clears via `task add`/`start`/`confirm`.
- **E-1117 + E-1124**: verbs were extracted from `config.json` into `.endless/verbs.json`. Treat verbs.json as the verb store, not config.json.
- **E-1138 (ready)**: separately covers updating land's auto-commit list (drop config.json, add verbs.json). NOT in your scope.

## Your scope: Layer F — task-command worktree integration

When `endless task start <id>` runs on a **plan-bearing task** (one whose `tasks.plan_file_path` is set per E-969), automatically create a dedicated **task worktree** if one doesn't already exist. This makes the per-task worktree lifecycle complete: per-session worktrees auto-create on SessionStart (Layer D); per-task worktrees auto-create on `task start` (this slice).

**Strictly this slice.** Do NOT modify SessionStart, UserPromptSubmit, PreToolUse, or worktree land/drop. Those are shipped.

### Behavior

In `src/endless/task_cmd.py`'s `start_item` (or wherever `task start` sets `status=in_progress` and binds the session to the task):

1. After the existing event emission for `task.started`, check if the task is **plan-bearing**:
   - `tasks.plan_file_path` is non-NULL AND the file exists at the endless-owned path `.endless/plans/E-NNN.md`.
2. If not plan-bearing: existing behavior, no worktree work.
3. If plan-bearing:
   - Compute branch name: `task/<id>-<slug>` where slug follows the rules in `.endless/plans/E-971.md` "Slug generation" section.
   - Compute worktree path: `<project_root>/.endless/worktrees/e-<id>` (matches the existing convention seen in e-967, e-1126, etc.).
   - Use `endless worktree for-task E-<id>` (or its underlying Python helper) to check if a worktree already exists. If yes: do nothing (the worktree is already there; subsequent SessionStart hooks will adopt it).
   - If no: invoke `git worktree add -b <branch> <path> main` (the existing land logic in `src/endless/worktree_cmd.py` already shells to git; reuse the same `_git_run` helper or factor a shared `add_worktree` function).
   - Write the companion file at `<worktree-root>/.endless/worktree.json`:
     ```json
     {
       "kind": "task",
       "task_id": "E-NNN",
       "base_branch": "main",
       "branch": "task/NNN-<slug>",
       "created_at": "<ISO timestamp>"
     }
     ```
     (No lock file — the lock comes from SessionStart when a session lands in the worktree, not from the task.start trigger.)
   - Print a cd-or-spawn hint to the user:
     ```
     Created task worktree: .endless/worktrees/e-NNN
     Switch sessions to it: cd .endless/worktrees/e-NNN
       or spawn a fresh one:    endless task spawn E-NNN
     ```

### Edge cases

- **Task starts but cwd is already in another worktree**: still create the dedicated task worktree; the user is informed via the hint and chooses whether to switch. Don't try to auto-cd; a child process can't change its parent shell's cwd.
- **Worktree creation fails** (e.g., branch already exists from an aborted attempt): surface the git error verbatim; don't silently retry. The user can `git worktree remove` and retry.
- **Plan file path stored but file missing**: treat as not-plan-bearing for safety. Add a warning to stderr.
- **Race with another session also starting the same task**: rare. The companion-file write should be atomic via `O_EXCL`. If the write loses the race, read the existing companion file and proceed as if you had created it.

### Slug computation

Implement (or reuse) the slug rule from the plan:
- Lowercase the title.
- Strip filler words: `a`, `an`, `the`, `to`, `from`, `of`, `for`, `with`, `in`, `on`, `at`, `by`, `and`, `or`.
- Replace non-alphanumeric with `-`.
- Collapse repeated `-`.
- Truncate to 40 chars at a word boundary.

Example: task E-757 "Move title verbs from hardcoded list to database table" → slug `move-title-verbs-hardcoded-list-database` → branch `task/757-move-title-verbs-hardcoded-list-database` → worktree `.endless/worktrees/e-757`.

Note: existing worktree directory naming convention (e.g., `.endless/worktrees/e-967`) uses just the task ID, NOT `<id>-<slug>`. The branch carries the slug; the directory does not. Match the existing convention.

## Acceptance criteria

- `endless task start E-NNN` on a non-plan-bearing task: no worktree created; existing behavior preserved.
- `endless task start E-NNN` on a plan-bearing task with no existing worktree: creates `.endless/worktrees/e-NNN/` on branch `task/NNN-<slug>` from main, writes the companion file, prints the cd-or-spawn hint.
- `endless task start E-NNN` on a plan-bearing task whose worktree already exists: no-op (no error, no duplicate creation), prints "worktree already exists at <path>".
- The created worktree is immediately discoverable via `endless worktree list` (state=active because companion file exists) and `endless worktree for-task E-NNN`.
- `endless worktree land E-NNN` on the created worktree successfully merges + cleans up (verifies the companion file shape is correct).

## Constraints and conventions

- **Companion file** terminology, not "sidecar".
- **Naming**: snake_case for code/DB identifiers, kebab-case for user-facing strings (CLI flags, branch names, slugs), Go uses PascalCase/camelCase.
- **Discipline**: do this work in a worktree. Layer D is now live, so SessionStart will create one for you if you start in main. Ship via `endless worktree land <task-id>`.
- **Use just install** from main only to refresh the toolchain after landing.
- **Don't run `just install` from your worktree**; use `PYTHONPATH=<worktree>/src endless ...` to test in-progress changes.
- **Don't modify** SessionStart hook, UserPromptSubmit hook, PreToolUse hook, or worktree land/drop logic. Those are shipped and out of your scope.
- Reuse existing helpers (`_git_run`, slug generators in worktree_cmd.py if any) rather than duplicating logic.

## Plan file pointer

`.endless/plans/E-971.md`, particularly the "Worktree creation triggers" → "Task worktree" subsection and the "Slug generation" section.

## When done

Mark the task `verify` (Mike confirms). Use `endless worktree land <task-id>` to ship.
