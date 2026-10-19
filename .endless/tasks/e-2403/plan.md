# Plan — hand runs of `endless-go hook` get usage; a real empty payload is graded like any failure

Decided with Mike, 2026-10-18/19: option 3. Each installed hook command names its event (`endless-go hook claude <Event>`), so an empty payload can be graded by E-1661's rule; a run with no event named is a hand run and gets usage, never a fault.

## 1. `--help` / `-h`

`endless-go hook --help` and `endless-go hook claude|prompt|codex --help` (and `-h`) print usage to stdout and exit 0, before the supported-agent check and before stdin is read. The usage says what `hook claude` reads (Claude Code's hook payload, JSON on stdin), that the installed command names its event, and what installs it (`endless setup claude-hook`).

## 2. The event on the command line

`runClaude` takes an optional positional event name (`hook claude PreToolUse`). With one:
- the payload parses and its `hook_event_name` matches → handled as today;
- the payload parses and names a different event → a fault (the settings entry is wired to the wrong event), graded by the payload's event;
- the payload is empty (zero bytes or whitespace only) → a fault, graded by the command-line event through `hookExitCode`: exit 2, except exit 1 on Stop and SubagentStop (E-1661);
- the payload is not valid JSON → ERR-0017 as today, graded by the command-line event.

## 3. No event on the command line: a hand run

- stdin is a terminal → usage refusal (`refusal.NoReport`) at once, no read, exit 1, no fault.
- stdin is empty → the same usage refusal, exit 1, no fault.
- a non-empty payload that does not parse → ERR-0017 as today.

## 4. `hook prompt` with no directory

A usage refusal, exit 1, no fault, instead of an error that reaches Run's fault sink.

## 5. Setup writes and repairs the event argument

- `src/endless/setup.py` `_make_hook_entry` writes `<bin> hook claude <Event>` for each event.
- A new repair, run by `endless setup claude-hook` beside `_repair_hook_async_flags` and `_repair_missing_hook_events`, rewrites each existing endless-go `hook claude` entry to name the event it is registered under. Idempotent; foreign hooks untouched.

## 6. After landing (agent, one time)

Run `endless setup claude-hook` on Mike's machine so the user-level Claude Code settings file carries the event on every entry.

## Exit codes

Usage refusals exit 1, like the dispatcher's existing usage refusal: exit 2 from a hook is a blocking error to Claude Code.
