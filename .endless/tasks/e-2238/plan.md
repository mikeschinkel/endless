# Plan — make the console's shell act on the task-session server

Agreed with Mike on 2026-10-04. Folds in what was first filed here as "route
spawns from the monitor server to the main tmux server".

## Terms (agreed)

- **console** — the terminal window attached to the console server: tmux
  server `endless-console`, session `console`, one window per use (`projects`:
  the project monitor above a shell prompt; a window listing terms is planned).
- **task-session server** — tmux server `endless-tasks`, session `tasks`
  (configurable), where Claude task sessions live, attached in a second
  terminal window. E-2240 (`endless tmux start`) creates both servers; neither
  is the user's default tmux server, which stays the user's own.

## Behaviour

1. **Recognising the console.** Decided from the tmux server a pane runs on —
   the console server — never from an environment variable a process could
   export. Honoured only when no agent harness is detected. Never honoured on
   the task-session server, even in a window that has no Claude session at the
   moment.
2. **Writes** from the console need no Claude session and record nothing in
   `session_tasks`. They are attributed to a new actor kind,
   `project-monitor`.
3. **Spawner display.** A session spawned from the console shows
   `↩ project monitor` in `session status` / `session monitor` where the ↩ from
   row would otherwise name the spawning session's task.
4. **Target.** Anything run from the console that opens or focuses a tmux
   window targets the `tasks` session on the task-session server. Auto-spawn
   fired by the project monitor lands there too, whatever its target setting.

## Commands, run from the console

**Allowed, no session needed** (writes attributed to `project-monitor`):
- task: `add`, `update`, `clear`, `approve`, `submit`, `assume`, `confirm`,
  `complete`, `decline`, `reopen`, `remove`, `supersede`, `move`, `block`,
  `unblock`, `link`, `unlink`, `list`, `show`, `detail`, `search`, `next`,
  `recent`, `active`, `deps`, `relations`, `landed`, `unlanded`, `unsettled`,
  `verify`
- session: `list`, `history`, `search`, `show <id>`, `hide`, `unhide`,
  `snapshot`, `turn <id>`, `status <id>`

**Allowed, acting on the task-session server:**
- `task spawn` — the Claude window opens in `tasks`.
- `session goto [--resume]` — the task's window comes to the front in the
  task-session terminal (resumed there first with `--resume`). It acts on the
  other terminal, not where the command runs, which is why it belongs here.

**Refused**, each naming the task-session terminal as where to run it:
- task: `claim`, `bind`, `release`, `id`, `continue`, `handoff`, `report` —
  each needs a Claude session; claim and bind are undefined without one.
- session: `resume` (it relaunches a task's session in the current pane, which
  belongs in `tasks`), `back` (after a goto from the console, the console's own
  view never changed; returning to it means raising a desktop window, which
  tmux cannot do), `task`, `use`, `cd`, `id`, `forget`, and `status` / `show` /
  `turn` with no id given.
- `touch`.

Views of "this pane's session" given no id report that the pane is the console
and has no session; they do not guess.

## Verification

A suite on private tmux servers (short TMUX_TMPDIR) with a stubbed Claude:
each command class above behaves as listed from a console pane; the same
commands on the task-session server are unchanged; a write from the console
carries actor kind `project-monitor` and adds no `session_tasks` row; a console
`task spawn` and a monitor-fired auto-spawn open windows in `tasks`; `session
goto` changes the task-session client's window and not the console's; a pane on
the task-session server with no Claude session is not treated as the console.
