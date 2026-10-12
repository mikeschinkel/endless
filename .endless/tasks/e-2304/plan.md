Two edits in two files, plus the deletion of the unreachable handler. The
mechanism that carries the new event to existing installs already exists and is
not touched.

## Approach

1. `src/endless/setup.py`: add `"PreCompact"` to `CLAUDE_HOOK_EVENTS` and to
   `SYNC_EVENTS`, and extend the comment block above `CLAUDE_HOOK_EVENTS` with a
   paragraph in the style of the `Notification` and `StopFailure` ones — when it
   joined, what its handler does, and why it is synchronous.
2. `internal/hookcmd/claude.go`: delete `case "ExitPlanMode"`, the
   `handleExitPlanMode` function and `latestPlanFile`. Leave `isPlanFile` and
   the `Write` branch of `handlePostToolUse` alone.
3. Extend `tests/test_setup_hook_missing_events.py` rather than adding a file.

## What will be true

- `CLAUDE_HOOK_EVENTS` lists `PreCompact`.
- `SYNC_EVENTS` lists `PreCompact`, with a comment saying the parse must finish
  before compaction proceeds.
- The comment block above `CLAUDE_HOOK_EVENTS` records why `PreCompact` is
  installed and what its handler does.
- `case "ExitPlanMode"`, `handleExitPlanMode` and `latestPlanFile` are absent
  from `internal/hookcmd/claude.go`.
- `go build ./...` passes, and a grep of `cmd/` and `internal/` finds no
  definition or reference of `handleExitPlanMode` or `latestPlanFile`.
- `isPlanFile` and the plan-file branch of `handlePostToolUse` still exist and
  still pass `go test ./internal/hookcmd`.
- A test asserts `_repair_missing_hook_events` adds a `PreCompact` entry, with
  `async` false, to a settings dict that has the other events but not this one.
- A test asserts `_repair_hook_async_flags` corrects a `PreCompact` entry that
  was installed with `async` true.
- On a machine whose install predates this change, `endless setup claude-hook`
  names `PreCompact` under "Added missing hook event(s)" and says to restart
  running sessions.
- `endless setup claude-hook` run twice in a row adds nothing the second time.

## Out of scope

- No other event name from the hooks reference is installed. Pairing the
  remaining uninstalled events with handlers is a separate brainstorm's
  question.
- `monitor.ParseTranscript` is not changed. This work causes it to be called at
  compaction; what it does once called is untouched.
- The expected event set stays in Python. Moving it to Go so one list serves
  both install and checking is recorded on E-2285.
- No drift detection. Nothing new reports an event that is missing; that is
  also E-2285.

## Verification

`just test`, `just test-go`, and on this machine `endless setup claude-hook`
followed by reading the installed event list back out of the user-scope Claude
settings file. A real compaction is the end-to-end check: after restarting a
session, `/compact` and confirm the transcript parse ran before the compaction
rather than after.
