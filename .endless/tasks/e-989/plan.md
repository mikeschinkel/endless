# E-989 — Per-session companion file for sibling-pane discovery

## Background

A sibling tmux pane (or a separate terminal/window) needs to know which Claude session is running in another pane to do useful things — cd into its worktree, view its history, show its bound task. Today there is no reliable way:

- **lsof on the claude PID does not show the session JSONL.** Claude appends-and-closes, so the file is not held open. lsof is also unavailable on Windows.
- **"Newest `*.jsonl` in `~/.claude/projects/<encoded-cwd>/`"** works heuristically but is ambiguous when two claude processes share a cwd, and depends on Claude's path-encoding scheme staying stable.
- **Process-tree walks** (tmux `pane_pid` -> claude child) are portable but only get us as far as the PID; there is still no deterministic PID -> Claude session UUID link without one of the above heuristics.

## Solution

Deterministic per-session companion file written by a `SessionStart` hook and removed by `SessionEnd`.

### File path

```
<project-root>/.endless/sessions/<harness>-<session_id>.json
```

The `<harness>-` prefix lets multiple AI harnesses (Claude, future Codex, etc.) coexist in one flat directory without rename churn. Readers filter by prefix to dispatch.

### File contents

```json
{
  "harness": "claude",
  "harness_session_id": "<uuid>",
  "endless_session_id": 247,
  "pane_id": "%53",
  "cwd": "/Users/.../endless",
  "pid": 20545,
  "started_at": "2026-04-29T03:51:23Z",
  "worktree_path": null
}
```

### Lifecycle

- **SessionStart** writes the file (atomic tempfile + rename).
- **SessionEnd** removes the file (idempotent on missing).
- **Stale entries** (pid not alive) cleaned lazily by readers.

### Read path

A sibling enumerates companion files in the project's `.endless/sessions/`, filters by prefix to pick the agent type, then by pid liveness, then by "not me" (`pane_id` mismatch when in tmux).

## Cross-platform constraints

- No `lsof`, no `/proc` walking. Pure filesystem + whatever the terminal multiplexer exposes via env (`TMUX`, `TMUX_PANE` on tmux; nothing required on plain terminals — companion file still works, just no sibling auto-discovery).
- Must work on Windows eventually; companion-file approach is portable. The only OS-specific bit is the liveness check (`kill -0` vs `OpenProcess`).

## Verification

- Two panes, two sessions, two worktrees: each writes a companion file; `endless session cd` from a third pane disambiguates and lands in the right cwd.
- Kill claude with SIGKILL (no SessionEnd fires): next read prunes the stale companion file.
- No tmux: companion file still written; sibling-discovery falls back to "explicit id required."

## Out of scope

- The `endless session cd` command itself (E-990).
- `session show` / `session history` default-resolution (E-991, E-992).
- Windows port (design must not preclude it; implementation can be macOS/Linux first).
- Codex / other-harness writer mechanism (open design item — Codex hook surface unknown).

## Open design item

How the file gets written for non-Claude harnesses that may lack `SessionStart`-equivalent hooks. Not blocking v1; the `<harness>-` prefix in the filename keeps the door open.
