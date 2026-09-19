# E-1803 — enforcement design for the no-ceremony report channel

## Problem (demonstrated live)
`task report` (E-1771) computes the clean facts and bounces ceremonial `--json`
entries, but the agent still authors the surrounding chat message and can append
"helpful" prose. Observed: the agent ran `task report`, relayed its two lines, then
hand-wrote a redundant regression paragraph with no action item for the user — on
top of output the harness had already captured.

## Why NOT a Stop-hook bounce
A Stop hook that rejects a message diverging from the report forces the agent to
REGENERATE the turn — more tokens, more latency, the exact waste this feature exists
to remove. Enforcing an anti-waste feature with a mechanism that multiplies waste is
self-defeating. A text-diff hook also can't tell an ALLOWED open-decision from
editorializing without the semantic check `report` already owns.

## Structural limit (state it plainly in the impl — do not over-promise a gate)
Claude Code always lets the model author the final natural-language message. No
tool's stdout REPLACES that message, and no hook edits it in place. So "report
authors the whole turn" is not achievable as a hard gate; the honest ceiling is a
strong, well-placed nudge.

## Mechanism this task adds (the enforcement arm)
A `PostToolUse` hook matched to the Bash call that runs `endless task report`
returns `hookSpecificOutput.additionalContext` (the recently-added field; also
available on `UserPromptSubmit` / `SessionStart`). Delivery: the harness captures
the command's stdout, fires the hook with that output on the hook's stdin, then
folds the hook's `additionalContext` into the model's context INLINE, tied to that
tool result (surfaced like a `<system-reminder>`) — not a parallel stream. Content:
instruct the model to emit the report output verbatim as its entire reply and add
nothing. This lands at compose time, contextual and harness-authoritative — the
strongest in-harness lever short of a regenerate loop, but a strong nudge, NOT a lock.

## Coverage arm (unchanged from the original filing)
Make `task report` the invoked channel at every in-session user-facing checkpoint,
not just session end/status. Acceptable output defined FUNCTIONALLY: a computed
fact the user cannot derive XOR a genuine open decision/blocker, else nothing —
report's `notes` vs `questions`. Derive from function; do not enumerate situations
(an enumerated list drifts the moment a new surface appears).

## Enforcement-arm ceiling and the bypass hole (session 2026-07-29, Mike + agent)

Both arms above anchor to a tool call — the PostToolUse nudge to the `task
report` invocation, the coverage arm to whatever action defines a checkpoint.
That leaves a **bypass hole**: output at a *toolless* turn (a "here's what I
found" reply that calls nothing) is invisible to any tool-anchored hook. It is
also, notably, the case where verbose prose is most often *legitimate*, so it is
the lowest-value and highest-false-positive place to gate.

Rejected fix — a standing per-turn `UserPromptSubmit` injection of the functional
rule: injecting identical context every turn habituates (boy-who-cried-wolf) and
the model stops attending to it. A functional wording does not rescue it; the
repetition, not the wording, is what kills the signal.

Evidence the residual is compliance, not a missing trigger: in the demonstrated
failure the agent had *already* been nudged — `task update --status unverified`
printed "Report through the command… Relay its output verbatim; add nothing" —
and appended two redundant paragraphs anyway. So the structured checkpoints
(status flip, plan save, commit) already carry a trigger; adding more triggers
does not help, because the binding constraint is the structural ceiling (the
harness always lets the model author the final message), not trigger placement.

The `Stop` hook (verified against the Claude Code hooks docs): fires at the end
of **every** main-agent turn (not `/exit`-only), receives `last_assistant_message`
directly, and supports observe-only (exit 0). It is the *only* hook that sees the
turn's output independent of any tool call, so it is the sole place that could
watch the toolless-turn hole. But its levers are all weak against the live turn:
observe-only logs the message a turn too late (the user has already read it);
`decision:block` forces regeneration — the exact waste this feature rejects;
`additionalContext` at `Stop` only primes the *next* turn. So `Stop`'s honest
role is **measurement, not a gate** — feed the scrivener pattern into the report
persistence / regression signals (E-1777 / E-1778) to keep drift visible and
tunable. A `Stop` detector also needs the same semantic classifier `report`
already owns (narration-that-should-have-been-report vs. a genuine substantive
answer); a dumb "long message + no report call" heuristic false-positives on
legitimate discussion turns and just relocates the cry-wolf problem to the
metrics layer.

Net: there is no hard gate to be had — the ceiling is real and no hook lifts it.
The higher-leverage half is **source removal**, not another nudge: fold the
handoff's "state the regression result in one line" instruction into a computed
`report` fact (E-1778), so the handoff stops *asking* the agent to narrate a
clean result. Removing the prompt that licenses the prose has no ceiling;
nudging against it does.
