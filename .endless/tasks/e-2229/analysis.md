Design settled in the E-2223 brainstorm (decisions ED-1606 and ED-1607):

- Visible means the monitor's window is the active window of some attached client on the Endless tmux server. macOS app focus is irrelevant. Several monitors visible at once is fine.
- Primary signal: tmux hooks (`session-window-changed`, `client-session-changed`, `client-attached`, `client-detached`) defined in the Endless tmux config. Each runs one `endless-go` command that asks tmux which windows are visible (`list-clients -F '#{client_session} #{window_id}'`) and wakes the affected monitors. For example, each monitor registers its pid as a pane option and is woken by a signal.
- Safety net: each monitor polls its own visibility every 15–30s. The poll also checks that the hooks are installed.
- Errors: a hook command that fails, or hooks found missing, is recorded in `endless errors list`, never left silent. A single missed hook (the poll disagrees with the last hook result) is only logged; it corrects itself within one poll interval.
- A hidden monitor blocks and does no work. On becoming visible, it recomputes immediately.
- While visible, change detection is cheap: SQLite `PRAGMA data_version` for the database (no file watcher, so no load on `fseventsd`), and index/HEAD modification time plus `git status --no-optional-locks` for each worktree. This applies to `project monitor` too, which is optional but always on screen when open.
- Visible monitors fire the job runner. Jobs not running while no monitor is visible is accepted.
