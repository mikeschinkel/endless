# Keep a live tmux server's server ID when reading it fails

## Stop the re-stamp

`tmux init` (`internal/tmuxcmd/init.go`, `readServerOption`) and the monitor
(`internal/monitor/processes.go`, `liveTmuxServerUUID`) read `@server_uuid` with
`show-options -gv`. Without `-q`, an unset option and a failed read both exit 1,
so both paths stamp a fresh ID after any failed read. Read with `-gqv`, as the
E-2300 probe already does: exit 0 with no output means unset, and only then
stamp. A failed read stamps nothing. The monitor returns "" (no reachable
server, which callers already handle), and `tmux init` refuses, naming the
failure.

## Aliases: earlier IDs of the same running server

A new local table, `tmux_server_aliases (alias_uuid PRIMARY KEY, server_uuid,
recorded_at)`, added to `schema.sql` with a migration. It is machine-local, like
`tmux_servers`, and is not written through the event ledger.

When `recordTmuxServer` records an ID for the first time, it reads the server's
start time (`display-message -p '#{start_time}'`). Every other `tmux_servers`
row on the same socket whose `first_seen_at` is at or after that start time was
stamped on this same server, so it is recorded as an alias of the new ID. A row
first seen before the start time belonged to an earlier server, and gets no
alias.

## One same-server test

Every place that compares server IDs goes through one function (Go) and one SQL
fragment, which accept an ID or any of its aliases:

- `internal/monitor/live_sessions.go` (`server == here`, and the join on
  `tmux_servers`)
- `internal/monitor/processes.go` (`ProcessIDsForPanes`)
- `internal/monitor/liveness.go` (`socketProbe.proves`: an alias of the ID on
  the socket proves nothing; any other ID still proves the server gone)
- `internal/triagejob/effects.go` (`server != here`)
- `internal/events/session_status.go` (`liveSessionByProcessTx`)

## This machine

Record the alias a6d29860 → 0ae20eaa here once, after checking that the
running server's start time (2026-10-17 00:12:57Z) is before a6d29860's
`first_seen_at`. Then ES-1339 reads live and `endless session goto E-2206`
reaches pane %22.
