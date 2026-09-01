# Plan — a task's tmux window is named for the task, and nothing else

Two windows are created for a task and neither is named usefully. `task spawn`
builds `<project>_<one-or-two-words>[E-NNNN]`, which spends most of a narrow tab
on words the user already knows. `session goto --resume` passes no name at all,
so tmux falls back to the command and every resumed window reads `claude`.

Both become `E-NNNN`.

## 1. `task spawn`

`_spawn_window_name(project_name, title, item_id)` in `src/endless/task_cmd.py`
returns the slug form. It becomes the task id alone. The project name and the
title slug go; the function's remaining job is small enough that it should
either be inlined or keep its name and lose its now-unused parameters — do not
leave a two-argument function that ignores both.

Its docstring records why the separator was `_`: tmux reads `:` as
`session:window` and `.` as `window.pane` in `-t` targets, so either character
breaks targeting. `E-NNNN` contains neither, so the constraint is satisfied by
construction. Keep the reasoning in a comment; it is the kind of thing that gets
re-broken by someone "improving" the format later.

## 2. `session goto --resume`

The `new-window` call in `src/endless/session_cmd.py` passes `-d -c <worktree>
-P -F #{pane_id}` and no `-n`, so tmux names the window after the command.
Add `-n E-NNNN`.

The task id is already in hand on that path — `_resolve_resume(ref)` returns the
worktree and label, and the resume target carries the task. Use that id rather
than re-deriving one from the worktree path.

## 3. The dependency this breaks, which is the reason this is not a one-liner

`internal/sandboxcmd/reapguard.go` reads tmux window names to decide which DB
sandboxes to SPARE:

    var tmuxWindowTaskRe = regexp.MustCompile(`\[E-(\d+)]`)

Its comment is explicit that the bracketed id "is the user's own record that the
task is still in play, and it outlives the worktree dir", and `Protected()`
spares a sandbox whose task id appears in an open window. Renaming windows to
`E-NNNN` removes the brackets, the regex stops matching, and every such sandbox
silently loses that protection — a sandbox reaped while its window is open, with
no error and no signal that the guard stopped guarding.

So the regex moves with the naming, in the same change. Match the bare form, and
keep matching the bracketed form: windows named before this lands are still open
on the user's machine, and a guard that protects only newly-named windows is a
guard with a migration-shaped hole.

Anchor the match so a window merely *mentioning* a task id does not spare a
sandbox by accident.

## Boundaries

- Does NOT change how sessions are addressed. Nothing targets a window by name
  today (`-t` targets are pane ids and session names), and this does not start.
- Does NOT rename windows that already exist. They age out as sessions end, and
  the regex keeps matching them until they do.
- Does NOT touch the tmux status line, which is a separate surface.
- Duplicate window names are accepted, not prevented: a task with both a spawned
  and a resumed window has two, both legitimately that task's.

## Verify

1. `endless task spawn E-NNNN` creates a window named exactly `E-NNNN`.
2. `endless session goto <ref> --resume` creates a window named exactly
   `E-NNNN`, not `claude`.
3. A sandbox whose task has an open window named `E-NNNN` is spared by the reap
   guard, and the reason it reports is the tmux one.
4. The same, for a window still carrying the old `<project>_<slug>[E-NNNN]`
   form.
5. A window whose name merely contains a task id in passing does not spare a
   sandbox.
6. `endless-tmux active-id` and anything else reading the current window still
   resolve the task.
