You are working on **E-1319** (Add `endless session status list / show / latest` read CLI).

E-1319 is **blocked by E-1318** (JSON storage migration). Confirm E-1318 is `confirmed` (or `assumed` and unblocked) before claiming. The read CLI's design depends on the JSON storage shape — implementing this against the pre-E-1318 XML shape would be wasted work.

## 1. Get oriented

```
endless guide
endless guide sessions                         # the session-status subsystem you're building reads for
endless task show E-1319 --text
endless task show E-1318                       # storage shape this depends on
endless task show E-1312                       # original write CLI; mirror its pattern
endless task show E-968                        # parent epic
endless task claim E-1319                      # claim, then cd into the worktree
```

Read the existing implementation:

- `src/endless/session_status_cmd.py` — the `add` command lives here; sibling for read
- `src/endless/cli.py` — look for `session_status_cmd` subgroup (search for `"session.status"`); add subcommands here
- `internal/events/session_status.go` — the renderer (`renderSessionStatusMarkdown`) is reusable for the `show` and `latest` commands
- `internal/monitor/` — where new read functions live (one query helper per read)

Look at the data:

```
endless sql "SELECT id, session_id, active_task_id, headline, created_at FROM session_statuses ORDER BY id DESC LIMIT 5"
endless sql "SELECT * FROM session_status_tasks LIMIT 5"
```

## 2. Do the work

Enter plan mode.

Three subcommands per the task description:

- `endless session status list   [--session N] [--task E-NNN] [--limit N]`
- `endless session status show   <id>`
- `endless session status latest [--session N]`

All reads go through Go (per E-894 / no-Python-SQLite). Python is parse-args + display only. Likely add new monitor functions for each read shape, or one generic `RecentSessionStatuses` + filtering in Go.

The `show` command renders markdown using the same renderer the write side uses (`renderSessionStatusMarkdown`). Reuse it.

Open design questions for plan-mode chat:

- For `latest` with no `--session` arg: which session to default to? The tmux-pane-lookup pattern from `session_status_cmd.add` (TMUX_PANE → Go `GetLiveSessionByProcess`). Same gotchas (filter `state != 'ended'`, etc.).
- For `list --task E-NNN`: that's a JOIN against `session_status_tasks`. Output shape per row?
- Whether `list` shows headlines only or also a few content excerpts.
- JSON output flag (`--json`) for scripting? Almost every other endless verb has it; precedent is strong.

When implementation is complete:

- `endless task update E-1319 --status verify` with how-to-test commands
- Dogfood: use your new `endless session status latest` to display your own work-in-progress row (if you wrote one); use `list` to show recent session_statuses; use `show <id>` on the row you just wrote.

## 3. Closing

Don't run `endless worktree land` without asking Mike. Don't mark `confirmed` — that's Mike's. Use `assumed` if you can't easily verify.

Goal: a read surface that turns the session_statuses table into the queryable cross-session activity log it's meant to be. After this lands, the loop closes: write rich rows on the way in, read them back to see "what's everyone doing / what just happened / what's the latest from session N."
