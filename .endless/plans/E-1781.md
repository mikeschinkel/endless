# E-1781 — Remove the `session activity` command

## Context

`endless session activity` reports what a session did, projected from the event
ledger. It is unused and overlaps `session status` (current state), `session
show`/`history`, and the task ledger. A dependency check found no consumers beyond its
own module + CLI registration + one tmux menu item.

That menu item is **dropped, not reworked**. `session status` has no `--pane` shim, so
it can't be swapped into the popup (a `display-popup` runs in an ephemeral pane with no
session binding — the reason `activity` needed `--pane` at all), and there's no
appetite to invest further in the little-used tmux popup: the direction is toward
decoupling Endless from tmux and favoring direct commands / a future TUI or GUI.

## Deliverables

### 1. Delete the command
- `src/endless/cli.py` — remove the `@session_cmd.command("activity")` block
  (~lines 726–753), including the `run_activity` import and call.
- `src/endless/session_activity.py` — delete the file (262 lines; the cli.py block is
  its only importer).

### 2. Drop the tmux menu item
- `internal/tmuxcmd/show_menu.go` — remove the `{"Session Activity", "a", …}` entry
  (~line 96) that runs `endless session activity --pane=… | less` in a popup. Drop it;
  do not replace. No key renumbering needed (the remaining items and the separator are
  unaffected).

### 3. Reword the stale comment
- `src/endless/event_bridge.py` (~line 98) — the comment cites "per-session activity
  queries" as the reason emitted events carry `actor.session_id`. Keep the
  `session_id`-population behavior (it has general event-attribution use) and reword the
  comment to drop the activity-specific rationale.

### 4. Nothing else
- No tests reference `session_activity`/`run_activity` (verified) — nothing to remove.
- No guide-map/help/index entry: `session activity` inherited the `session` → sessions
  mapping, so the command simply drops out of `walk_commands()`; no orphan, and
  `guide-check` stays green.
- `docs/guide/reference.md:104` is unrelated (it documents the "auto-record session
  activity" *commit message*, not the command) — leave it.

## Verification — `tests/tasks/e-1781-verify.sh`

Self-contained, modeled on `tests/tasks/e-1607-verify.sh`. Build the worktree
`endless-go` and exercise the Python CLI from the worktree source; assert:

- `endless session activity` is gone — `endless session --help` exits 0 and does not
  list `activity`, and invoking `endless session activity` errors as an unknown
  subcommand.
- `src/endless/session_activity.py` does not exist.
- `src/endless/` contains no remaining `session_activity` / `run_activity` reference.
- `internal/tmuxcmd/show_menu.go` contains no `Session Activity` / `session activity`,
  and `go build ./cmd/endless-go` succeeds.
- `event_bridge.py`'s comment no longer references "activity".
- Regression: `go test ./internal/tmuxcmd/...` and a targeted `uv run pytest` over the
  session CLI tests pass (targeted, not full `just test`).

## Verification (handoff to Mike)

Set `unverified`; run `esu && ./tests/tasks/e-1781-verify.sh`.
