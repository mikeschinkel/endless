# E-1557 — Findings summary

Claude Code's Agent View (v2.1.139, May 2026, research preview) gives
endless a clean bg-agent integration surface.

## Key findings

- **Launch**: `claude --bg --name "<arbitrary>" "<prompt>"` from any shell;
  short ID returned on stdout (~8 hex, parseable from the
  `backgrounded · <id> · <name>` block).
- **Discovery**: `claude agents --json --all` returns live state per
  agent (status, waitingFor, cwd, etc.) — the canonical API.
- **Promote/view**: `claude attach <short-id>` connects a terminal to
  the running supervisor-hosted bg agent. Works in the current pane or
  a fresh tmux window. Detach (`Ctrl+Z`, `/exit`, `←`) leaves the
  agent running; only `claude stop` / `Ctrl+X` ends it.
- **Hook firing**: SessionStart fires for bg agents
  (`source=startup` fresh or `source=resume` backgrounded). No
  bg-specific payload field per issue #59848.
- **`bgIsolation:"none"` REQUIRED** (v2.1.143+): without it, Claude
  creates a nested worktree under `.claude/worktrees/` on top of the
  tracker worktree, defeating pre-claim.
- **Quota**: linear N× burn for N parallel agents; community sweet
  spot 3–5; soft "thread limit" + server burst limiter exist.
- **Survival**: bg agents survive foreground Claude exit, terminal
  close, machine sleep (v2.1.142+). Die on shutdown (recoverable via
  `claude respawn --all`) and 1h idle reaping (`Ctrl+T` pins exempt).
- **WorktreeCreate hook**: long-term alternative to `bgIsolation:"none"`,
  but ~11 open issues including two June 2026 regressions (#68214
  critical isolation failure, #68399 hook-not-firing). Not production-
  ready.

## Full report

Lives at `docs/research-2026-06-12-claude-background-agents.md`
pending the per-task-directory restructure (deferred; will move to
`.endless/tasks/E-1557/research.md` then).

## How findings were used

Fed directly into E-1552's design (ED-1507): bg-agent dispatch via
`--name` carrier (no DB-token needed), `bgIsolation:"none"`
requirement, two attach verbs (`spawn --attach` for new tmux window,
`task attach` for current pane via exec), soft-warning throttling at
3 parallel agents, session lifecycle recovery filed as E-1560
(reboot) and E-1561 (tmux crash). WorktreeCreate hook deferred to
maybe-task E-1559.