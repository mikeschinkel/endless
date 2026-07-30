# Plan — spawn: CLI-arg prompt delivery, drop /plan, auto mode

## Scope

Replace `task spawn`'s send-keys/paste prompt delivery with Claude's positional
prompt argument, delivered by launching Claude as the tmux window's *command*
through a new `endless-go` subcommand (the multiplexer seam). Drop the forced
`/plan` injection and the `--no-plan` flag. Default spawned sessions to
`--permission-mode auto`. Plumb `--permission-mode` / `--model` / `--name`
pass-throughs. Convert the `--attach` window to the same launch mechanism so the
foreground spawn path contains zero `send-keys`.

## 1. New endless-go subcommand package `internal/spawnlaunchcmd`

Register in `cmd/endless-go/main.go`'s subcommand switch (alongside `tmux` /
`session-query`). Isolate all tmux calls in a single `tmuxDriver` file so the
future multiplexer-driver refactor can add backends without touching the
argv/exec logic.

Two entry points in the package, dispatched on the first arg:

### `spawn-window` (outer orchestrator — the only thing Python calls)

Flags:
- `--claude-bin` (resolved real claude binary, no shell-function wrapper)
- `--handoff-file` (temp file path)
- `--permission-mode` (default `auto`)
- `--model` (optional), `--name` (optional)
- `--task-id`, `--project-id`, `--spawned-by`
- `--window-name`, `--cwd`
- attach mode: `--attach --short-id <id>` (with `--window-name`, `--cwd`)

Behavior:
- Normal: write a JSON launch-spec file (all params above; use a go-dt struct /
  OrderedMap, not a bare map), then run `tmux new-window -c <cwd> -n
  <window-name> -- <self> spawn-launch --spec <spec-path>`. `<self>` is
  `os.Executable()`. The only data on the command line is the binary, the
  subcommand, and one shell-safe temp path — no handoff text, task title, or
  model string gets shell-quoted onto the command line, and nothing leaks into
  the session environment. Return once the window exists.
- Attach: run `tmux new-window -c <cwd> -n <window-name> -- <claude-bin> attach
  <short-id>`, then set `@endless_attached_short_id` (diagnostic; race harmless).

### `spawn-launch` (inner — runs inside the freshly created window)

- Read the spec file named by `--spec`.
- From `$TMUX_PANE`, set window options `@endless_spawned_by`,
  `@endless_task_id`, `@endless_project_id` via `tmux set-option -w -t
  $TMUX_PANE …` — BEFORE exec, so SessionStart's option reads
  (`internal/hookcmd/claude.go`) never race the keystroke timing they used to.
- Read the handoff file, then `os.Remove` both the handoff file and the spec
  file. Empty/whitespace handoff content → omit the positional argument (bare
  interactive `claude`).
- Build claude argv: `[claudeBin, "--permission-mode", mode, ("--model", m)?,
  ("--name", n)?, prompt?]`.
- `syscall.Exec(claudeBin, argv, os.Environ())`. No fallback — on exec failure,
  exit non-zero with a clear message (never degrade to send-keys).

## 2. Python `spawn_plan` (src/endless/task_cmd.py)

Foreground path (currently ~4832–4899):
- Keep `render_handoff` → temp file. Render already raises on failure, so the
  spawn aborts before any window is created (hard-fail, no fallback).
- Replace the entire new-window / set-options / send-keys / sleep / `/plan` /
  paste / unlink block with ONE subprocess call: `endless-go spawn-window` with
  `--claude-bin` (`_claude_binary()`), `--handoff-file`, `--permission-mode auto`,
  optional `--model`/`--name`, `--task-id`, `--project-id`, `--spawned-by`
  (`spawner_id`), `--window-name`, `--cwd <cd_target>`.
- The launcher deletes the temp file; Python no longer unlinks.

Attach path (currently ~4635–4669):
- Replace the `new-window` + `send-keys "claude attach …"` with `endless-go
  spawn-window --attach --short-id <id> --window-name <n> --cwd <dir>`.

Remove entirely: `--no-plan` handling, the `/plan` block, both `time.sleep`
calls, `load-buffer`/`paste-buffer`, the three `tmux set -w` option calls (now
the launcher's job), and the `os.unlink`.

## 3. CLI flags (spawn command definition, src/endless/cli.py)

- Remove the `--no-plan` option and the `no_plan` param from `task_spawn` and
  `spawn_plan` signatures + all references.
- Add `--permission-mode` (default `auto`), `--model`, `--name` options, threaded
  through `spawn_plan` into the `spawn-window` call.
- Rewrite the spawn docstring/help: drop plan-mode language; describe
  positional-prompt delivery and the auto default.

## 4. Tests

- Go: `internal/spawnlaunchcmd` unit tests — argv construction (with/without
  model/name; empty prompt ⇒ no positional), the env→`set-option` command list,
  and the attach command list. Follow the `spawn_bind_test.go` style.
- Python: update spawn/attach tests (`tests/test_task_attach.py` and any
  spawn-path tests) to assert the single `endless-go spawn-window` invocation
  instead of `new-window` + `send-keys`; drop `--no-plan` / plan-slash-command
  assertions.

## 5. Docs / help

- Update guide/orchestration docs that describe the spawn typing / plan-mode
  behavior (e.g. `docs/guide/orchestration.md`) to the new behavior. No internal
  task IDs in shipped docs.

## 6. Verification

### `tests/tasks/e-1705-verify.sh` (primary, one-shot land-time gate)

With `tmux` and `claude` stubbed by recording shims on PATH, assert:
- A spawn emits exactly one `endless-go spawn-window` invocation and ZERO
  `send-keys` / `load-buffer` / `paste-buffer` calls.
- The spawn command exposes no `--no-plan` flag and never injects the plan
  slash-command.
- Given a spec + a handoff file whose text contains quotes, backticks, `$`, and
  blank lines, `spawn-launch` execs the `claude` stub with the correct argv and
  the prompt bytes intact (the stub records its argv to a file for comparison).
- Empty/whitespace handoff ⇒ `claude` stub invoked with no positional argument.
- After a scripted spawn, the session→task binding row lands in the sandbox DB
  with the expected `kind` (read via the Go query helpers, not Python SQLite).

### Manual dogfood (only the irreducibly-interactive residue)

Spawn a real self-dev task from a worktree and confirm the window lands directly
in an interactive Claude already answering the handoff and that a follow-up
message can be typed with no extra steps. Confirm `UserPromptSubmit` fires for
the positional first turn — this is NOT documented for positional prompts
(checked against the Claude Code CLI docs); if it does not fire, stop and report,
as Endless depends on it. (Assert this from the event log in the verify script if
the scripted spawn surfaces it there; otherwise it stays manual.)

## Boundary / follow-up

This removes send-keys and establishes the endless-go launch seam, but Python
still triggers the operation and passes tmux-window parameters. Fully moving
window creation + option plumbing behind a multiplexer driver (for Herdr and a
built-in multiplexer) remains a separate tmux-driver refactor, not yet filed.
