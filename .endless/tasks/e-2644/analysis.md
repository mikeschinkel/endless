## Why these four together

One cause: E-2519 moved pane identity to the task-session server (list-live
scopes pane ids to `monitor.TasksServerUUID()`, `session_cmd._tmux_run`
addresses its socket), and these paths still read the caller's own server.
Merged from E-2644, E-2645, E-2646 and E-2647 per ED-1550. Mike's rule (E-2572
planning): every endless command runs on the named task-session server and/or
the named console server; anything else is a bug.

## Decisions taken in planning (Mike, 2026-11-16)

- The attached-terminal check before auto-spawn and triage asks the
  task-session server, where the window opens. Rejected: either server (a
  window can open unseen on endless-tasks while only the console is attached,
  the failure E-1815 built the check against); no check (sessions start and
  spend tokens while nobody is attached).
- `auto_spawn.target` is removed. `active` meant: open the window in the
  session of the most recently active attached client and skip when none is
  attached; `monitor` meant: open it in the runner's own session and skip
  only when the runner has no pane. Since spawn-window opens every window in
  the task-session server's `tasks` session and drops the computed target
  (E-2240, E-2385), the two values differed only in the skip rule. Rejected:
  keeping the key as a switch for the check, because `monitor` would no
  longer describe what it does.
- `--session N` is the documented spelling of the session override on the
  four event-emitting commands; "less to type, and what other value would
  'session' vs 'session-id' imply?" `errors accept` and `errors decline`
  already spell it `--session N`. `--session-id` is removed from the four, not
  kept as an alias (Mike: "Remove it, and have the plan search and fix any
  breakage"). The Go subcommands' `--session-id` flags are not user-typed and
  stay.
- The pane fallback in event resolution stays, scoped to the task-session
  server. It is the third tier after `--session` and the resolver; dropping
  it would refuse a Claude in an endless-tasks pane whose other two tiers both
  fail.
- Triage runs wherever code runs. Its spawns and resumes already go through
  spawn-window and `session goto --resume --background`, so they land on
  endless-tasks; the client check was its only call on the runner's server.

## Facts checked

- Jobs run from the session-status loop (task-session server), the
  project-status loop (console) and `endless jobs run`, so the runner can be
  on either server.
- `internal/settings`: an unknown key fails open, so a config still naming
  `auto_spawn.target` keeps loading.
