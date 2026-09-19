# Show a claim/bind hint instead of an unrelated task list when `session status` can't resolve the pane's active task

## Problem
In a tmux window whose `endless tmux status-line` resolves **no** active task
(renders the placeholder `·`), `endless session status` / `monitor` still renders
a task list — the what's-next rows of an **unrelated** task (currently E-1461's,
because it is the most-recently-active session). The window has nothing to do
with that task, yet shows its remaining-work list. This is wrong and dangerously
misleading if the user isn't looking closely.

Observed in window `endless_verify_toml[E-1596]`: its status line outputs only
`#[fg=colour240]·#[default]` (due to a separate status-line bug under repair),
and `session status` there shows E-1461 / E-1695 / E-1697.

## Root cause
`internal/monitor/session_status.go::ResolveSessionStatusFocal` resolves the
focal task in three steps, ending in a **machine-wide last resort** (E-1465 /
ED-1523):

1. `GetActiveTaskForPane(tmuxPane)` — pane/window-scoped.
2. the window's `@endless_task_id` tmux option.
3. **the most-recently-active live session's `active_task_id`, machine-wide.**

Step 3 returns an unrelated task whenever the pane genuinely has no active task,
and `session status` renders that task's rows. The `tmux status-line` has no such
fallback — it uses `monitor.GetPaneStatus`, which returns a placeholder/hint
(`PaneStatusNoTask` → "claim a task", `PaneStatusClaudeNoSession` → "register",
`PaneStatusNone` → `·`). So the two surfaces disagree: the bar correctly says
"nothing here," the list invents an unrelated task.

## Desired behavior
`session status` / `monitor` must be **consistent with the status line**: when the
pane/window has no active task (the same condition that makes the bar show `·`/a
hint), render **no task list** and instead print a short message telling the user
to **claim or bind a task**. Never fall back to an unrelated machine-wide task for
the list view.

## Fix
- Route `session status`/`monitor` focal resolution through the same pane-scoped
  path the status line uses (`GetActiveTaskForPane` / `GetPaneStatus`) and **drop
  the machine-wide most-recent fallback (step 3)** for the list view.
- When no active task resolves, mirror the status line's `PaneStatusKind`:
  - Claude in the pane but no session row → "no Endless session" hint (as
    `hintNoSession`).
  - session present but no active task, or nothing → "no active task — claim or
    bind one:  `endless task claim <id>`  /  `endless task bind <id>`".
  - Render **zero** rows in every no-active-task case.
- Keep the explicit `--task <id>` (headless/test) path unchanged.
- Outside tmux (no pane context): print the same claim/bind hint; do not invent a
  task.

This reverses the ED-1523 last-resort **for the list surface** — that last-resort
is exactly what produces the misleading output, and the status line already
proves the hint-based behavior is the correct one.

## Tests
- `session status` with a pane that has no active task (no session row for the
  pane/window, or a session with NULL `active_task_id`) → emits the claim/bind
  hint and **zero** rows; does NOT show any other session's tasks.
- The `--task <id>` path still renders that task's rows.

## Verify (manual)
In a tmux window whose status line shows `·` (no active task), run
`endless session status` → it shows the claim/bind message, not another window's
task list.

## Note / dependency
The `verify_toml[E-1596]` window's `·` is itself the **E-1697** status-line
sandbox-DB bug. E-1698 is independent of that fix: even once status lines resolve
correctly, a pane can legitimately have no active task, and this is the correct
behavior for that case.
