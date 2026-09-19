## Repro
In a tmux pane already bound to (working) E-1644, from the worktree:

    $ endless session resume E-1644 --db main
    Error: This pane is working E-1644. `session resume` execs in place, so it would replace that session.
      Open the target in a NEW window instead:
          endless session goto E-1644 --resume
      Or pass --force to replace this pane.

## Problem
`session resume` execs in place, so the guard refuses when the pane already has a task
variable assigned — to avoid clobbering a DIFFERENT live session in that pane. But when the
resume TARGET is the SAME task the pane already owns, there is no other session to protect:
resuming yourself replaces nothing meaningful (it is a re-enter of the task you are already on).
The guard should short-circuit that case instead of forcing `session goto --resume` (a new
window) or `--force`.

## Expected
When the pane's bound task == the resume target, `session resume <id>` proceeds without
`--force` — either a friendly no-op ("already here") or a clean re-enter/resume of the current
task. The replace-guard should fire ONLY when the pane's current task differs from the target.

## Fix location (starting point)
The `session resume` command's ownership/replace guard — the check that prints
"This pane is working E-<id>. `session resume` execs in place, so it would replace that
session." Add an equality short-circuit: if the current pane's bound task == the requested
target, bypass the "would replace that session" refusal. Confirm `session goto` and the
`--force` path stay unaffected.

## Verify
From a pane bound to task X: `endless session resume X` exits 0 (no `--force`) and does NOT
print the replace refusal; `endless session resume Y` (Y != X) still refuses without `--force`.

## Encountered
Hit live in session 6e7f46fb (the E-1644 worktree) on 2026-06-29 while trying to resume this
very task after it was completed.
