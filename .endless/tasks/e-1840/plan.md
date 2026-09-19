# Idea

Endless provides a mechanism to help populate the agent's memory files, but strictly reactively — in response to an actual trigger (a user correction, a repeated error, a detected friction event), not proactive guessing. This is a fallback for cases where the higher-leverage fix (changing product behavior so the knowledge gap never forms) is not feasible.

# Explore (do not rank)

- Trigger surface: what events count as 'reactive' (explicit user correction, repeated identical CLI error, hook-detected friction).
- Where the memory goes and in what shape (the existing per-agent memory-file convention).
- Human-in-the-loop versus automatic capture; approval before a memory is written.
- Guardrails: dedupe against existing memories, avoid low-signal noise, expiry or review.
- Relationship to product fixes: a reactive memory should be a stopgap that also files a product-improvement task, so the memory is not the terminal state.

# Non-goals

- Proactive or speculative memory writing.
- Replacing product fixes with memories (the sibling brainstorm treats knowledge gaps as product defects first).

# Output

A recommended design for a reactive memory-capture mechanism, or a decision that it is not worth building versus fixing product behavior case by case.