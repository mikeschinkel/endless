Primary use case (short term): from a tmux pane that is NOT the Claude pane, get into the cwd / worktree of the Claude session running in a sibling pane so you can run tests, inspect state, or just look around without polluting Claude's working directory.

Resolution rules (in order):
1. Explicit: endless session cd <endless-id> — look up the companion file by endless_session_id, cd to its cwd (or worktree_path if set).
2. Tmux sibling: in tmux with a single sibling Claude pane, auto-resolve to that one. Multiple siblings → list and require disambiguation.
3. Otherwise: error with the list of known companion files so the user can retry with an explicit id.

Implementation notes:
- A child process cannot change its parent shell's cwd. The command therefore prints the target path to stdout and the user wraps it in a shell function (e.g. escd() { cd "$(endless session cd "$@")"; }). Document the function; consider shipping an 'endless shell-init' that prints it.
- Alternative for tmux: when invoked under tmux, optionally tmux send-keys a cd into the calling pane. Cleaner UX but more magic; default off behind a flag.
- Reads .endless/sessions/claude-*.json companion files (E-989). No DB read on the hot path.

Verification:
- Two panes, two sessions, two worktrees: from a third pane, 'endless session cd <id1>' lands in worktree 1; same for id2.
- One Claude pane in tmux: 'endless session cd' (no arg) resolves automatically.
- Two Claude panes, no arg: prints disambiguation list, exits non-zero.
- No tmux, no arg: errors with companion-file list.

Out of scope:
- Auto-cd on session start.
- Cross-window tmux discovery (sibling = same tmux window only, for v1).