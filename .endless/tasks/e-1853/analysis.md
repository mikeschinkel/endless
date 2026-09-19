Corrected framing (supersedes the earlier analysis on this task): verb validation exists to stop the AI agent from freelancing title vocabulary that is hard for the human to reason about. The bug is NOT that a legitimate verb can be lost — losing a verb is a non-problem. The bug is that _check_verb_via_haiku (src/endless/task_cmd.py) collapses every non-YES outcome into one (False, None), so a CLASSIFIER ERROR (timeout, non-zero exit incl. auth, missing binary, empty/malformed/hedged reply) is handled identically to a definitive NO: the agent is offered a reword and quietly freelances a different title — bypassing validation on an error the human never sees. Repro: "streamline" was rejected on a transient classifier failure and silently reworded to "Explore streamlining...".

## Behavior — three buckets
- Clean YES: register the verb, pass. (unchanged)
- Clean NO (explicitly matched): reject; the title must start with an actual verb, so the agent rewrites it. (unchanged — this is validation working, and is already handled today.)
- Any error / cannot-determine (everything that is not a clean YES or a clean NO): STOP. Do not reword, do not auto-register, do not guess. Surface the actual error to the human and ask how to proceed. Retrying a few times on a transient failure before declaring cannot-determine is fine; the terminal state is a human decision, never an agent reword.

Error subtype does NOT branch behavior — every error leads to the same action (stop + ask). But the stop-and-ask message SHOULD name the specific failure so the human knows what happened: timeout, malformed/nonsensical reply, missing `claude` binary, or non-zero exit (incl. auth). The granularity is for the report, not for control flow.

## Enforcement (the crux — to be designed in the plan)
A "stop and ask" message is insufficient: the agent can (and did) ignore it and reword anyway. The stop must be an ENFORCED GATE the agent cannot bypass — via the Claude hook — so a classifier error halts the agent and hands the decision to the human instead of letting it silently pick different vocabulary. Likely shape: the CLI refuses on a classifier error AND a hook blocks further task-mutation until the human resolves. Working out that gate mechanism is the substance of the plan.

## Rejected
Fork A (auto-register the verb provisionally on a classifier error) is rejected: auto-admitting unreviewed verbs is exactly the agent-freelancing that validation exists to prevent. "Preserving the verb" is not a goal.

Distinct from E-1837 (punctuation/dedup on the verb WRITE path); this is failure-handling on the CLASSIFY path.
