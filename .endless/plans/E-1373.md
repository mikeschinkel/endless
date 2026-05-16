# E-1373: Session detection mechanism for `endless task claim` / `task bind`

## Decision

When `endless task claim` or `endless task bind` needs to identify "which Claude session is making this call," detection is **filesystem + env + tmux**, not the SQL `sessions` table. The implementation is `src/endless/task_cmd.py:_current_endless_session_id` (line ~1971). Three-layer fallback, first match wins.

## Detection layers

### Layer 1 — env override
Read `ENDLESS_SESSION_ID`. If set to a digit-string, return as int. Explicit override; not normally set.

### Layer 2 — companion JSON keyed by `$TMUX_PANE`
Read `$TMUX_PANE`. Walk `<project-root>/.endless/sessions/claude-*.json`. Find the file whose `pane_id` field equals `$TMUX_PANE`. Return that file's `endless_session_id` (int PK into the `sessions` table).

Companion files are written by `cmd/endless-hook/claude.go:writeClaudeCompanion` (called from SessionStart and refreshed from UserPromptSubmit, post-task-mutation, etc — lines 166, 203, 636, 655, 676). They are removed by `monitor.RemoveCompanion`, called only from the SessionEnd handler at `claude.go:232`.

### Layer 3 — single-sibling-pane fallback
List tmux panes in the same window via `tmux list-panes`. For each non-self pane, look for its companion JSON. If exactly ONE sibling pane has one, return that session's id. 0 or 2+ matches return None — the 2+ case is re-checked in the caller (`_find_sibling_claude_session` at line ~2012) for a louder refusal pointing at E-1244.

## What "no session" actually means

Detection is purely env + filesystem + tmux. The `sessions` table's `state` column is **not consulted here**. The DB row's `state='ended'` is correlated with the failure mode but not its cause: SessionEnd removes the companion file (the upstream cause of detection failure) AND sets `state='ended'` in the same handler. Either signal alone breaks detection differently; in practice they fire together.

## Legitimate failure cases and the right verb for each

| Situation | Right verb |
|---|---|
| Operator running endless from outside any Claude session ("manual work, no Claude assistance") | `endless task claim --force` |
| Claude session alive but companion missing (orphaned via cwd-deletion cascade — see E-1337) | `endless session rebind <task>` (E-1370) |
| Multiple sibling Claude panes | None — caller refuses with E-1244 disambiguation pointer |

## Anti-pattern

Claude reaching for `--force` when the situation is case (b) — an orphaned-but-alive session. The error message documents `--force` as the bypass, which is read as authoritative guidance under task pressure. The gate is **E-1369**: refuse `--force` on `claim` when `CLAUDECODE` env is set, redirecting to `session rebind` or to surfacing the orphan to the user.

## Why this is documented as a decision

The detection mechanism has surprising properties for anyone debugging "why does endless think I'm not in a Claude session":
- It does NOT query the DB's `sessions.state`.
- It does NOT check whether the Claude process is alive.
- It does NOT use `CLAUDECODE` or `CLAUDE_CODE_SESSION_ID` (only `TMUX_PANE` + the per-pane companion JSON).

Three layers + filesystem-cache semantics means several plausible-looking debug paths are dead ends. Recording the decision makes the actual mechanism findable.
