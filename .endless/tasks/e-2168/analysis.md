# Verified facts

- **tmux-resurrect saves no `@endless_*` options.** Its format is four line kinds (pane, window, state, grouped_session); a window line carries name, index, active flag, layout and flags. Checked directly against a real save file: zero matches for `@endless`. Upstream issue tmux-plugins/tmux-resurrect#577 (July, no response) covers custom options generally. Not a Resurrect defect for our purposes — the options are outside its format — and waiting on upstream is not a plan.
- **Nothing on our side heals the gap.** `setTmuxSessionUUID` rewrites `@endless_session_uuid` on every hook event, explicitly for the tmux-server-restart case. `@endless_task_id`, `@endless_spawned_by` and `@endless_project_id` have NO self-heal and no unset. Their only writers are `task spawn` (internal/spawnlaunchcmd/tmux_driver.go) and `_bind_pane_window_options` (both resume surfaces).
- **`_require_lone_pane` refuses a restored window.** Resurrect restores the full layout, so every restored window is multi-pane. Its docstring routes to `session goto --resume` because 'the route out is the sibling verb rather than a flag' — reasoning from when the only crowded window was one you arranged yourself.
- **The clobber gate is NOT what fires here.** Restored panes get new tmux pane ids, so `_current_pane_task()` finds no session row and never asks for `--force`.

# Why --rebind cannot breach the invariant

It rewrites the tmux window's `@endless_task_id`, not `sessions.task_id`. Write-once (ED-1560, enforced by the `sessions_task_id_write_once` trigger) is untouched. The invariant a rebind could threaten is 'one ACTIVE tmux window per task', so the flag should refuse when another LIVE window still claims the target task. After a crash there is no such window — which is exactly why the flag is safe in the case it exists for.

# Placement

Requester's call: the flag goes on `session resume`, not on `task bind` and not on a new `tmux` verb. Resume is where the user already is when the window refuses them.

# Related, NOT to be folded in

E-1983 covers a DIFFERENT window/task disagreement: a fresh session silently binding to a leftover spawn window's task. Its decision — error, with a flag to update the window, worktree dir authoritative — is the same shape of escape hatch, so the two should agree on the mechanism for rewriting a window's task. Separate causes, separate tasks.

# Open: the wrong-task-id sighting is NOT explained

Mike observes a restored window bound to a task 250-750 LOWER than its worktree's. Eliminated: ~/.tmux.conf (contains no `@endless` anything, so source-file writes nothing), Resurrect (saves none), and argument order in `_resume_decision` (all three call sites pass uuid, worktree, label, eid, task, project correctly). Suggestive but unproven: session ids top out at 1228 against task ids at 2167, so ANY place a session id lands in a task-id slot yields a value a few hundred lower — right direction, right magnitude. One real defect found while looking, possibly unrelated: `monitor.ResolveResumeTarget` (internal/monitor/resume.go) takes a bare number, tries it as a task, and on a miss SILENTLY reinterprets the same number as a session id. That can only fire for refs <= 1228 so it cannot explain a 2157 window, but it can for older worktrees, and it is silent either way. Needs a live failure to settle: capture `tmux show-options -w`, the pane cwd, and the sessions row before repairing.