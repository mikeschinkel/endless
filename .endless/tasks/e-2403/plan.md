# Plan — hand runs of `endless-go hook` get usage; every hook entry names its event

Decided with Mike, 2026-10-18/19. Each installed hook command names its event (`endless-go hook claude <Event>`), and the argument is required. A cause the user can recognize and fix is a fault, so they see it; a cause that is Claude Code's is ERR-0017 with a remedy that says so; a hand run is usage, never a fault.

## 1. `--help` / `-h`

`endless-go hook --help` and `endless-go hook claude|prompt|codex --help` (and `-h`) print usage to stdout and exit 0, before the supported-agent check and before stdin is read. The usage says what `hook claude` reads (Claude Code's hook payload, JSON on stdin), that it takes the event as a required argument, and that `endless setup claude-hook` installs it.

## 2. `hook claude <Event>` — how each case is graded

"Graded by E-1661" means through `hookExitCode`: exit 2, except exit 1 when the event is Stop or SubagentStop.

| Event argument | stdin | Result |
|---|---|---|
| none | terminal | usage refusal at once, exit 1, no fault (no read) |
| none | empty / whitespace | usage refusal, exit 1, no fault |
| none | a payload | fault: the entry predates the event argument; remedy `endless setup claude-hook`; graded by the payload's event |
| named | payload naming the same event | handled as today |
| named | payload naming a different event | fault: the entry is wired to the wrong event; remedy `endless setup claude-hook`; graded by the payload's event |
| named | empty / whitespace | ERR-0017, graded by E-1661 on the named event |
| named | not valid JSON | ERR-0017, graded by E-1661 on the named event |

The two new user-fixable causes get their own fault codes, each with the `endless setup claude-hook` remedy, distinct from ERR-0017.

## 3. ERR-0017's remedy

Rewritten to work in order: run `endless setup claude-hook`, which repairs or reports a non-standard entry (one that wraps or pipes the hook). If it reports every entry standard and the fault continues, Claude Code is sending a payload Endless cannot read: report it to Anthropic, and consider a Claude Code version that works. It also says that while this fault is active, tool calls are blocked, and names `hooks.dangerously_continue`.

## 4. `hooks.dangerously_continue`

A machine-file opt-in, default off, registered in the settings registry. When on, ERR-0017 exits 1 instead of 2 on every event; nothing else changes, and the fault is still recorded and counted. Its remedy line says that while it is on, every PreToolUse gate is inactive whenever the payload is unreadable.

## 5. `hook prompt` with no directory

A usage refusal, exit 1, no fault.

## 6. Setup

- `_make_hook_entry` writes `<bin> hook claude <Event>`.
- A new repair, beside `_repair_hook_async_flags` and `_repair_missing_hook_events`, rewrites each endless-go entry that is exactly the old form (`<bin> hook claude`) to name the event it is registered under. Idempotent; foreign hooks untouched.
- Any other endless-go entry that is not exactly the command setup writes (wrapped, piped, extra arguments) is reported by name and event, not rewritten.

## 7. After landing (agent, one time)

Run `endless setup claude-hook` on Mike's machine so every entry in the user-level Claude Code settings file names its event.

## Exit codes

Usage refusals exit 1, like the dispatcher's existing usage refusal; exit 2 from a hook is a blocking error to Claude Code.
