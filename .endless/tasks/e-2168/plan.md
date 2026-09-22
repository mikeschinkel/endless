# Plan: two flags that let `session resume` re-enter a crash-restored window

Mike's decisions, 2026-09-21. No open questions.

## The situation these exist for

tmux crashes. tmux-resurrect restores windows, panes, layouts and working
directories, but NOT the `@endless_*` window options — verified against a real
save file, whose format carries only `pane`, `window`, `state` and
`grouped_session` lines and contains zero `@endless` strings. Upstream issue
tmux-plugins/tmux-resurrect#577 (July, unanswered) covers custom options
generally; waiting on it is not a plan.

So a restored window keeps its endless-set NAME while its window identity is
absent or stale, and `session resume` refuses to re-enter it on two independent
grounds. The standing workaround is `session goto --resume`, which opens a NEW
window and abandons the restored one — losing the layout and the scrollback the
restore just recovered.

Nothing on our side heals it: `setTmuxSessionUUID` rewrites
`@endless_session_uuid` on every hook event, explicitly for the
tmux-server-restart case, but `@endless_task_id`, `@endless_spawned_by` and
`@endless_project_id` have no self-heal and no unset. Their only writers are
`task spawn` and `_bind_pane_window_options`.

## Decision 1 — two flags, each naming one thing it permits

They compose; neither implies the other, and neither touches `--force`.

| flag | permits |
|---|---|
| `--rebind` | the window claims a DIFFERENT task than the resume target |
| `--no-sibling-panes` | the window holds panes besides this one |
| `--force` (exists) | the pane holds LIVE work that the exec would replace |

After a crash the recovery line is
`endless session resume E-NNNN --rebind --no-sibling-panes`. Verbose on purpose:
each flag says exactly what was overridden, and a run that needed only one does
not silently waive the other. A single combined `--recover` was considered and
rejected for that reason.

Both read as `resume --rebind` once subcommands move to top level; nothing here
depends on the current nesting.

## Decision 2 — `--rebind` rewrites the WINDOW, never the session

The flag sets the window's `@endless_task_id` (and `@endless_project_id`) to the
resumed target. It does not touch `sessions.task_id`, which is write-once under
ED-1560 and enforced by the `sessions_task_id_write_once` trigger.

That is not a limitation worked around — it is why the flag is safe. The thing
that is wrong after a crash is the window's claim; the session's own binding is
either correct or absent.

Mechanically this is the write `_bind_pane_window_options` already performs on
every resume (E-2104). `--rebind` does not add the write; it removes the refusal
that stops resume reaching it.

`@endless_spawned_by` stays untouched, as it already does: it records which
session CREATED the window, and resume creates no window.

## Decision 3 — `--rebind` refuses when a LIVE window still claims the target

The invariant is `tmux window == Endless task == one or more Claude sessions`,
in SERIES, never in parallel. Rebinding into window B while window A is live and
claims the same task creates exactly the parallel state that forbids.

So `--rebind` refuses when another window holds a live session on the target
task, names that window and session, and offers `session goto` or closing it.

This check never fires in the case the flag exists for — after a crash there is
no live window — so it costs the recovery path nothing and catches only misuse.

Liveness is DERIVED, not stored (E-1898, `internal/monitor/liveness.go`), so the
check asks the liveness helper rather than trusting a `state` column. A stale
claim from a dead session must not block a recovery.

## Decision 4 — `--no-sibling-panes` skips the refusal, and still lays out

`_require_lone_pane` refuses a multi-pane window because resume "execs in place
and then builds the standard layout around the pane it took over", which in a
populated window resizes panes the user arranged. Its docstring routes to
`session goto --resume` because "the route out is the sibling verb rather than a
flag" — reasoning from when the only crowded window was one you arranged
yourself.

A crash-restored window is crowded with panes nobody arranged, holding dead
shells. `--no-sibling-panes` says so: proceed, and build the standard layout
over them. The layout is not skipped — a recovered window should come back
looking like a spawned one, which is the whole point of recovering it rather
than opening a new one.

The flag is on both `session resume` and `session goto --resume`, for the same
reason `--new-transcript` is on both: the window choice and the debris choice
are independent.

## Decision 5 — the clobber gate is untouched

`--force` still governs replacing a pane that holds live work. It does not fire
in the restored case anyway: restored panes get new tmux pane ids, so
`_current_pane_task()` finds no session row and never asks. Neither new flag
waives it, and neither implies it.

## Work

**Python — `src/endless/session_cmd.py`.** Add both flags to `session resume`
and `--no-sibling-panes` to `session goto --resume`. `_require_lone_pane` takes
the override. The window-claim check and its refusal are new; they sit beside
the existing clobber gate, before `_resolve_resume`, so a refusal cannot leave a
minted container task or worktree behind (the ordering E-1968 established).

**Python — the live-window check.** Resolve windows claiming the target task and
ask the liveness helper; refuse naming the window and session.

**Go — nothing.** `_bind_pane_window_options` already performs the write.

**Docs.** `endless guide orchestration` gains the crash-recovery line, since the
failure is invisible until it happens and the workaround is not discoverable.

## Verification

- `just build`, `just test`, `just test-go` clean.
- A window whose `@endless_task_id` names task B: `session resume E-A` refuses
  and names `--rebind`; with `--rebind` it proceeds and the window option reads
  A afterwards.
- A three-pane window: `session resume` refuses and names
  `--no-sibling-panes`; with it, resume proceeds and the standard layout is
  built.
- Both flags together on a window that is both stale and crowded — the crash
  case end to end.
- `--rebind` into a task another LIVE window claims is refused, and the refusal
  names that window; the same case with that session dead proceeds.
- `sessions.task_id` is unchanged by every one of the above. This is the check
  that proves the flag cannot breach write-once.
- Neither flag waives `--force`: a pane holding live work still refuses without
  it.
