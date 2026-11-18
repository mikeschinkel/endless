# Give the console its own status line

## Order

E-2657 writes each server's socket into the tmux command lines Endless
generates for that server, and blocks this task. E-2391 supplies the project
code and precedes this task.

## The row

A console project window's second status row reads:

    endless (E) · backlog 42 · working 2 · needs input 1 · unverified 7

- `endless` is the project's name and `E` its project code (1 to 4 uppercase
  characters, E-2391). A project with no code shows its name alone.
- `backlog` is Total Backlog as ED-1674 defines it: the project's tasks not in
  a terminal status. Use the `taskstatus.Terminal` group, not a second list.
- `working` and `needs input` count the project's live sessions in the
  `sessionstate.Working` and `sessionstate.NeedsInput` states.
- `unverified` counts the project's tasks in status `unverified`. These tasks
  are also in the backlog count, as ED-1674 defines it.

## Approach

- `internal/projectstatuscmd/window.go`: when `OpenProjectWindow` creates a
  project window, set two window options on it, `@endless_window_type` to
  `project` and `@endless_project` to the project's name.
- `internal/tmuxcmd/apply.go`, `buildApplySteps`: choose `status-format[1]` by
  the server's `@endless_server` option. The `tasks` line stays as it is. The
  `console` line runs a console renderer and passes the console's socket and the
  two window options through tmux format substitution.
- `internal/tmuxcmd`: add the console renderer beside `runStatusLine`, with the
  same failure handling: always exit 0, print `placeholder()` on a read error,
  and record the fault as `runStatusLine` does.
- A window with no `@endless_window_type`, or a type other than `project`, gets
  `placeholder()`. Later window types (E-2236's glossary tab is the first) add
  their own rows.
- `docs/guide/reference.md`, "Endless's tmux servers": describe the console row.
