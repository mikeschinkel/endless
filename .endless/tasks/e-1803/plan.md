# Plan — E-1803: no-ceremony report channel (enforcement + coverage)

Two arms. Observability (checkpoint recording + session-monitor surfacing) is
split to E-1826. Honest ceiling: a strong nudge, NOT a hard gate — Claude Code
always lets the model author its final message, and no hook can replace it
(verified against the hooks docs). The plan must not promise a lock.

## Arm 1 — Enforcement (PostToolUse reinforcement, baked into the hook binary)

- In `handlePostToolUse` (internal/hookcmd/claude.go), detect a `Bash` tool call
  whose command runs `endless task report` — inspect the payload's
  `tool_input.command` (match the subcommand, including inside `$()` / backticks).
- Detection lives IN the hook binary, NOT as an `if:` matcher in settings.json:
  the config form drifts per machine, and endless-hook already runs on
  `PostToolUse`, so in-code detection adds no new config and no new process spawn.
- On match, return `hookSpecificOutput.additionalContext` instructing: emit the
  report command's output verbatim as the entire reply; add nothing (no preamble,
  sign-off, or remarks about absent categories). This reinforces the report
  command's own stdout at compose time, harness-authoritatively. Reuse the
  existing additionalContext response path (claude.go ~299/337/398).

## Arm 2 — Coverage (functional behavioral rule + per-call reinforcement)

- The rule: at ANY in-session user-facing checkpoint, route the status through
  `endless task report`; acceptable output is a computed fact the user can't
  derive XOR a genuine open decision (report's `notes` vs `questions`), else
  nothing. Defined FUNCTIONALLY — do NOT enumerate checkpoint types (an
  enumerated list drifts the moment a new surface appears).
- Delivery: bake the rule into a SessionStart `additionalContext` injection (same
  in-code, always-delivered mechanism as Arm 1) so every session carries it
  reliably — not solely a guide doc the agent may not re-read. Document it in the
  agent guide as well for completeness.
- Per-call reinforcement IS Arm 1 (the PostToolUse hook fires each time `report`
  runs).
- Confirm `task report` is usable at arbitrary mid-session checkpoints, not only
  session-end/status (today's usage); extend if it assumes end-of-session.

## Verification (tests/tasks/e-1803-verify.sh)

- Hook fires: given a PostToolUse payload for a Bash call running `endless task
  report`, endless-hook returns `hookSpecificOutput.additionalContext` with the
  relay-verbatim instruction; given a non-report Bash command, it does not.
- Non-drift: detection is in the hook binary — no settings.json `if:` matcher
  required for it to work.
- Honest limit: the test asserts only that the reinforcement FIRES; it cannot
  assert the model obeys (that's unenforceable). Compliance *visibility* is
  E-1826's job, not a gate here.

Verification is FULLY SCRIPTED — no manual step. Everything assertable (both
hooks firing) is in the verify script; the nudge's behavioral effect is
unenforceable by design, so there is nothing to check by hand.

## Handoff (verify)

On completion, the implementing session's final message to the user presents
exactly ONE command and nothing else:

    esu && ./tests/tasks/e-1803-verify.sh

## Boundary

Observability — recording checkpoint events, surfacing last/missed checkpoint in
`session monitor`, and the passive Stop-hook turn-end record — is E-1826, not
this task.
