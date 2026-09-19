# Make `session resume` re-bind the tmux window options on the current pane

## Problem
`resume_session` (src/endless/session_cmd.py:558) resolves the target, then
`os.chdir(worktree)` + `os.execvp("claude", ["claude","--resume",uuid])`
(session_cmd.py:634-637). It sets NO tmux window options. So a pane resumed into
lacks the `@endless_*` window identity that a spawned window carries.

The four options and who sets them today:
- `@endless_task_id`, `@endless_project_id`, `@endless_spawned_by` — written ONLY
  by the spawn launcher (internal/spawnlaunchcmd/spawn_launch.go:39 ->
  tmux_driver.go:92-97). Never on the resume path.
- `@endless_session_uuid` — written by the hook every event
  (internal/hookcmd/claude.go:287, setTmuxSessionUUID), meant to self-heal.

## Why it matters
A resumed pane with no `@endless_task_id` silently loses:
- `_current_pane_task()` — the E-1968 clobber guard in resume itself
  (session_cmd.py:605) can't see the pane's held task.
- `session status` focal-task resolution, which reads the window's
  `@endless_task_id`.

## Fix
On the resume path, before `os.execvp`, set the window options on the current
pane to match a spawned window:
- `@endless_task_id` = resolved task id
- `@endless_project_id` = resolved project id
- `@endless_session_uuid` = resolved session uuid (set it here rather than
  depend on the hook's per-event self-heal, which is more fragile — see below)

Open design question for the implementer: `@endless_spawned_by` means "which
session spawned this window." A resumed pane was NOT spawned by `task spawn`, so
setting it is debatable — likely omit it (or record a resume-origin marker).
Decide during implementation; task_id/project_id/session_uuid are the clear wins.

## Not a regression
Verified via git pickaxe: `git log -S 'set -w' -- src/endless/session_cmd.py`
and `-S '@endless_task_id' -- …` both return zero commits. resume has been
option-less since it was introduced (eb2206ad). None of E-2074/E-2102/E-1918/
E-1851/E-1898/E-1976 touched the resume/hook option writes. This is a
long-standing design gap, not a recent breakage.

## Related but SEPARATE (do not fold in)
A distinct symptom was observed alongside this: `@endless_session_uuid` absent
even though the hook should self-heal it every event. That points at the hook
not reaching claude.go:287 for the affected session (candidates: the
`shouldSkipForWorktree` early return at claude.go:232, or a stale/missing
worktree-local hook wiring in that worktree's .claude/settings.json). That is a
hook-firing/wiring diagnostic, not confirmed as a code defect, and is NOT part
of this task. Setting session_uuid on the resume path (above) makes resume
robust to it regardless.

## Origin
Found while debugging a `session resume` in an unrelated session's worktree; the
task that session happened to be under (E-1834) is NOT related to this bug.
