# Plan — hand runs of `endless-go hook` get usage, not a fault

Decided with Mike, 2026-10-18 (option C): a run that cannot be Claude Code's — `--help`, a terminal on stdin, or an empty stdin — is a usage answer, never a fault.

## 1. `--help` / `-h`

`endless-go hook --help` and `endless-go hook claude|prompt|codex --help` (and `-h`) print usage to stdout and exit 0, before the supported-agent check and before stdin is read. The usage says what the command reads (Claude Code's hook payload, JSON on stdin) and what installs it (`endless setup claude-hook`).

## 2. A run that is not Claude Code's

In `runClaude`, before reading:
- stdin is a terminal → usage refusal (`refusal.NoReport`), no read, no fault.

After reading:
- the payload is empty (zero bytes, or only whitespace) → the same usage refusal, no fault.
- anything else that fails to parse still records ERR-0017 as today.

## 3. `hook prompt` with no directory

Becomes the same kind of usage refusal instead of an error that reaches Run's fault sink.

## Exit codes

Usage refusals exit 1, like the dispatcher's existing "Usage: endless-go hook <command>" refusal. Not 2: to Claude Code, exit 2 from a hook is a blocking error, and a refusal that ever reached it from a real event must not block the session.
