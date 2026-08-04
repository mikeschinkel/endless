# Appendix A — User-focused commands

These are commands a **human** runs interactively at a terminal. An agent reads this appendix only to point a user at the right command — never as part of its own working flow. This is a global appendix: user-only commands from any command group are collected here.

## Session navigation, observation, and history

Interactive commands on the `session` group:

- **`endless session goto <target>`** — switch tmux focus to a task's or session's pane, pushing the current pane onto a per-client back-stack. `<target>` is a task id (`E-NNNN`/`NNNN`) or a session id (`ES-NNNN`, a bare integer, or a Claude UUID prefix); a task id resolves to the most-recently-active live session working it. Prefer `ES-NNNN` — the form `task show` prints under `Created:`/`Touched by:` — since a bare integer matching both a session and a task is rejected as ambiguous. Requires tmux.
- **`endless session back`** — return to the previous pane, browser-style: pops the back-stack `goto` built, skipping panes that have since closed. With an empty stack, returns to the spawning session. Requires tmux.
- **`endless session trail`** — show the durable navigation trail (manual focus moves + `goto`), newest-first: `from → to` with a `via` tag and relative time. `--all` covers every tmux client.
- **`endless session monitor`** — the live dashboard: loops the `session status` view, redrawing every 2 seconds and repainting only on change. The top-like pane you keep open all day; Ctrl-C exits. Takes the same `--all`/`--tree` options as `session status`.
- **`endless session history [session]`** — show the conversation history for a session (current by default).
- **`endless session search <query>`** — search across all session messages.
- **`endless session recap [session]`** — generate recap summaries for sessions using Claude.
- **`endless session hide <ids...>`** / **`endless session unhide <ids...>`** — hide sessions from (or restore them to) `session list`.
