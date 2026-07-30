# Analysis — decisions and rationale

## Why the delivery mechanism is a launcher-as-window-command, not send-keys

The original ask (from a Claude Web draft) was "pass the prompt as claude's
positional arg instead of typing it." But the deeper goal is to eliminate
`send-keys` entirely: keystroke injection is fragile (a tmux prefix keystroke
arriving mid-spawn corrupts the input) and is a footgun for the planned
multiplexer-driver refactor (Herdr / a built-in multiplexer). So Claude is
launched as the tmux window's *command*, not typed into a bare shell.

## Why an endless-go subcommand owns the launch

Making `endless-go` the interface (rather than a shell script, or Python driving
tmux directly) is the multiplexer seam: the tmux specifics live behind one Go
command and can be swapped for another backend later. Internally it may still
shell out to `tmux`; the point is the interface, not the current implementation.

## Why the launcher must set the @endless_* window options (ordering)

SessionStart's spawn-bind reads `@endless_spawned_by` / `@endless_task_id` from
the tmux window (`internal/hookcmd/claude.go`). If Claude is the window's launch
command it boots immediately and races any option-setting done afterward. Having
the launcher set the options in-process *before* `exec claude` makes the ordering
deterministic and removes the old `sleep(5)`/`sleep(1)` timing heuristics. (The
cwd-based bind fallback, E-1700, still exists as a backstop but is no longer the
happy path.)

## Why parameters travel in a launch-spec file (not tmux `-e`)

`tmux new-window` runs its command through a shell, so parameters with spaces or
special chars (notably the task title in `--name`) would need shell-quoting if
passed as flags. `tmux new-window -e VAR=value` avoids that quoting, but every
such var is inherited by Claude and all its hooks/subprocesses for the whole
session — leaking transient values like the handoff path into the session
environment, where they can be mistaken for real signals. So instead the outer
`endless-go spawn-window` writes one JSON launch-spec file and the window command
is just `endless-go spawn-launch --spec <path>`: a single shell-safe path arg, no
quoting, no env leak. The handoff *text* never touches a command line either (the
launcher reads it from its file). The launcher deletes both files after reading.
(Mike, this session — chose the spec file over `-e`.)

## Confirmed decisions

- Fold into E-1705 (expand scope) rather than file a second task: the delivery
  change and the `/plan` removal edit the same ~60 lines of `spawn_plan`, and
  positional-prompt delivery inherently removes the interactive turn `/plan` was
  typed into — so they are one change. (Mike, this session.)
- Default `--permission-mode auto` for ALL spawns, including Endless self-dev:
  worktree isolation + sandbox DB make auto-accept low-risk. Making the mode
  configurable / flag-settable is wanted for the product; a `--permission-mode`
  flag is added now, defaulting to `auto`. (Mike, this session.)
- Launcher implemented as an `endless-go` subcommand even though it currently
  shells to tmux, so the multiplexer dependency is encapsulated. (Mike, this
  session.)

## Known risk / open verification

Claude Code's docs confirm `plan`/`auto` are valid `--permission-mode` values and
that a positional prompt starts an interactive session, but they do NOT document
whether the positional first turn fires the `UserPromptSubmit` hook. Endless
relies on that hook. The dogfood step treats this as a hard gate: if it does not
fire, stop and report rather than work around it.
