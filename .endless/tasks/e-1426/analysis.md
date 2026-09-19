# E-1426 verify handoff

Implementation landed on `task/1426-replace-session-companion-json-files`
(3 commits: 0b1bcc0 plan, 8fabf41 core, f844472 tests).

## What's done

- `TouchSession` is the per-event UPSERT helper. Fires at the top of every Claude hook event (`cmd/endless-hook/claude.go`); INSERT-or-UPSERT keyed on `session_id`; COALESCE on `process` so empty `TMUX_PANE` never stomps a known-good value; collision invalidation in-tx marks pane reuse cleanly.
- `ReapDeadTmuxPanes` (`internal/monitor/reap.go`) shells out to `tmux list-panes` at SessionStart and marks rows whose `%`-pane is gone as ended. No-ops cleanly when tmux is unavailable.
- `companion.go` deleted (`CompanionFile` struct, `WriteCompanion`, `RemoveCompanion`, all `*AtRoot` helpers). `WorktreePathForTask` kept in `internal/monitor/worktree.go`.
- New `cmd/endless-session-query` Go binary exposes `ListLiveSessions` as JSON. Added to Justfile build+install and to `endless-sandbox bind`'s wrapper list. Python now shells out to it instead of extending the legacy `db.query` pattern (E-894 direction).
- `src/endless/session_cmd._live_sessions` replaces `_read_live_companions`; `cwd` is derived via `tmux display-message` on demand; `worktree_path` is derived from `active_task_id`. `pid` field gone everywhere.
- Go unit tests for `TouchSession` (insert / E-1408 regression / state-preserved / pane-reattach / collision / empty-no-invalidate) plus `ListLiveSessions`. All 413 Python tests pass; all Go packages pass.

## What I did not verify

End-to-end against a live Claude session. My own session (eid 477) was bound to `/usr/local/bin/endless-hook` via your global `~/.claude/settings.json`, not to the worktree-built binary, so my hook events did NOT exercise the new code. The unit tests cover the SQL/Go logic; what is untested is the on-the-wire hook-fires-TouchSession flow.

## To verify

1. Land on main (or pick the branch up however you prefer to test pre-land):

   ```sh
   cd ~/Projects/endless && git checkout main
   git merge --ff-only task/1426-replace-session-companion-json-files
   just install
   ```

   That symlinks `/usr/local/bin/endless-session-query`, rebuilds `/usr/local/bin/endless-hook` with the new code, and reinstalls the Python CLI editable.

2. Sanity — `list-live` works against your real DB:

   ```sh
   endless-session-query list-live --project-root ~/Projects/endless | head -c 200
   ```

   Expect a JSON array with your live sessions. Session 477 (this pane) should appear with `process="%181"` and `pane_id="%181"`.

3. Spawn a fresh Claude window in tmux. After SessionStart fires, in any pane:

   ```sh
   env -u XDG_CONFIG_HOME sqlite3 ~/.config/endless/endless.db \
     "SELECT id, session_id, state, process, project_id FROM sessions WHERE state != 'ended' ORDER BY id DESC LIMIT 5"
   ```

   Expect the new session to appear with `process` set to its tmux pane id.

4. E-1408 regression — `env -u TMUX_PANE claude` in a tmux pane (forces empty `TMUX_PANE` at SessionStart). After Claude is up and you type one prompt, repeat step 3. The first PreToolUse should repair `process` from NULL to the real pane id.

5. E-1419 regression — `cd` into any worktree (e.g. `.endless/worktrees/e-1426`) and run `endless task id`. It should resolve to your current session id (no companion file involved). Previously this would fail because the worktree-side companion was invisible to the main-DB resolver.

6. Companion files are gone — `ls .endless/sessions/` in any project root should be empty or non-existent. The new hook never creates it.

## Open loose ends

- **E-1408, E-1419, E-1422** are cleans-up targets but I left them in their current status. Per the plan brief, flip them to `assumed` once the corresponding verify step above passes for you, or `confirmed` if you independently reproduce the original symptom on this branch and confirm it's gone.

- **E-1431** filed during planning: "Design and ship Endless session archive subsystem." Status `needs_plan`, inputs in `docs/private/endless-tmux-archive-research.md`. Not a cleans-up of E-1426; surfaced when archived sessions came up as a distinct concern the companion-file retirement does not address.

Holding at `verify` until you confirm.
