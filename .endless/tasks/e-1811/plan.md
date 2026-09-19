# Brainstorm — session launch & multiplexer abstraction

Captures the exploration from the session that produced E-1705 and E-1809.
Explores the space; decided slices graduated to E-1705, open design lives here
and in E-1809.

## Problem space

- `endless task spawn` delivered the initial prompt by typing into the tmux
  window (`send-keys` + `load-buffer`/`paste-buffer`), gated by fixed `sleep()`
  waits for the Claude UI. Fragile: a tmux prefix keystroke arriving mid-spawn
  corrupts the input; multi-line / special-char prompts are brittle; the prompt
  lands in interactive history as if a human typed it.
- Broader: tmux is hard-wired across the Go binaries and the Python CLI (window
  creation, window-option plumbing, launch/attach, pane→session/task
  resolution). Endless should be multiplexer-agnostic (tmux, Herdr, a built-in
  multiplexer), which makes `send-keys` keystroke injection a footgun.

## Explored — prompt delivery

- Deliver the handoff as Claude's positional prompt argument instead of typing
  it: a normal interactive session with the first turn pre-filled (NOT
  `-p`/`--print`, NOT `--bg`).
- A positional prompt leaves no interactive turn in which to type the plan
  slash-command, so it forces dropping the forced plan-mode injection (was a
  separately-tracked concern) into the same change.
- Launch Claude as the tmux window's *command*, not typed into a shell. But the
  `@endless_*` window options must be set before Claude boots (SessionStart's
  bind reads them). Resolution: a launcher that sets the options and THEN execs
  Claude — deterministic ordering, no `sleep`, no race.

## Explored — the launcher seam

- Launcher as an `endless-go` subcommand (not a shell script, not Python driving
  tmux), so the multiplexer dependency is encapsulated behind one interface —
  the seam the driver refactor grows from. It may still shell to tmux internally
  for now.
- Two entry points: outer `spawn-window` (creates the window) + inner
  `spawn-launch` (runs in-window, sets options, execs Claude).
- Parameter passing: rejected tmux `-e` env vars (they leak into Claude's env and
  every hook/subprocess for the session's life) in favor of a single JSON
  launch-spec file referenced by one shell-safe path argument.

## Graduated to E-1705 (decided, out of scope for further brainstorming)

CLI-arg positional prompt delivery via the endless-go launcher; drop forced
plan-mode and `--no-plan`; default `--permission-mode auto` (auto everywhere
including self-dev; a `--permission-mode` flag makes it configurable); plumb
`--model` / `--name`; convert `--attach` off `send-keys`; a per-task verify
script as the primary gate with manual dogfood for the interactive residue.

## Open — the driver refactor (E-1809)

- Driver interface shape: which operations it must cover — create-window (cwd,
  name, options, launch command), set-window-option, launch/attach a session,
  query pane→session/task, status-line writes, nav recording.
- A complete inventory of current tmux touchpoints: `internal/tmuxcmd`,
  `internal/monitor/tmux*`, `internal/sessionstatuscmd`, the status-line / nav
  code, and Python `task_cmd` window/option calls.
- Backends: tmux (today), Herdr, a built-in multiplexer — what each can and
  can't do; lowest-common-denominator vs capability negotiation.
- Migration sequencing: builds on E-1705's landed launch seam. The interface
  design is largely independent and can proceed now; the concrete migration
  sequences after E-1705 lands so it builds on the seam as shipped.
- Open: does the `@endless_*` window-option state move to env or another
  transport under a non-tmux backend? Hooks currently read tmux window options
  (with E-1700 race handling); a driver would need an equivalent.

## Known external unknown

Whether Claude Code's positional first turn fires the `UserPromptSubmit` hook is
undocumented (checked against the CLI docs). Endless depends on that hook;
E-1705's dogfood step is the gate.
