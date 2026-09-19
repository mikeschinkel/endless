# E-1106: Redesign verb-gate error to suppress agent rationalization

## Problem

The current "Title must start with an actionable verb" error trains agents to
rationalize past the gate in two ways:

1. **"Common verbs:" line lists alternatives** (e.g. "add, fix, implement,
   design, …"). Reads as a hint to substitute. For an agent, rewording is the
   path of least resistance — zero extra tool calls, looks like a normal title
   revision. Result: the gate is bypassed without the agent registering its
   actual verb.

2. **"Or use --force to bypass" advertises an escape hatch.** `--force` is for
   humans who know what they're doing. Putting it in every error trains agents
   to use it whenever the rule feels inconvenient.

3. **Three options compete for attention.** "Add a new verb" gets diluted by
   the two non-action options on either side.

Evidence: the agent maintaining this project violated the "always add the verb"
rule twice (2026-04-24 and 2026-05-02) despite having both a memory entry and a
LESSONS.md entry about the rule. Memory rules don't survive contact with
load. The error message has to do the work.

## But: the simple fix opens a worse failure mode

If the new error just says "run `endless phrase add verb '<word>'`", the agent
shifts from "reword the title" to "register the non-verb." A title like
"Maybe rework auth flow" would happily register "maybe" as a verb. The gate's
purpose — catching non-verb titles — gets neutered. Polluted vocabulary is
worse than rephrased titles because it propagates.

So the redesign has to do TWO things:

- Stop nudging toward substitution.
- Stop nudging toward registering non-verbs.

## Proposed error text

```
Error: Title must start with an actionable verb. 'maybe' is not registered.

  Decide: is 'maybe' a true verb? (Does it answer "what action does this perform?")

  IF YES:  endless phrase add verb 'maybe'
  IF NO:   rewrite the title with a verb. Do not register a non-verb to bypass this gate.

  Registering a non-verb defeats the check for everyone — including future-you.
```

Why each line:

- **Drop "Common verbs:" list entirely.** No alternatives = no substitution
  invitation.
- **Drop `--force` from the error.** Move to `--help` only. Humans who need it
  can find it; agents that find it will abuse it.
- **Force a binary** with `IF YES / IF NO`. Agent must mentally commit before
  running the command — not a hand-wavable preamble.
- **Define "verb" with a one-line test** ("what action does this perform?").
  "maybe" fails in two seconds; "consider" passes.
- **Name the social cost** ("defeats the check for everyone"). Stated stakes
  are likelier to be honored than vague rules.

## Stronger lever (optional, recommended)

Require a definition with the registration:

```
endless phrase add verb 'consider' --definition "to deliberate over"
```

Forces the agent to compose semantic justification, which is much harder to
fake than a yes/no. "Maybe" has no plausible "to ___" definition — the agent
either fabricates one (which a reviewer would catch) or rewrites the title.
Mandatory definition turns willful rationalization into a separate falsifiable
claim.

## Optional: agent-aware variant

Claude Code sets identifiable env vars (`CLAUDE_CODE_*`). The error could
detect agent context and show:

- **Humans**: a shorter error without the anti-rationalization preamble.
- **Agents**: the full binary form above.

Different audiences, different failure modes. Humans don't rationalize past
verb gates; agents do. This is a low-risk feature flag — fall back to the
agent-form variant if detection is uncertain.

## Scope

- Update the error text in the title-validation code path
  (`src/endless/task_cmd.py` — search for "actionable verb").
- Decide on definition-required: yes/no. If yes, add `--definition` flag to
  `endless phrase add verb`, persist it in the phrase store, and surface in
  `endless phrase list verb`.
- Decide on agent-aware variant: yes/no. If yes, add detection helper.

## Verification

1. `endless task add "Maybe X"` produces the new error text.
2. The error contains no "Common verbs:" list and no "--force" mention.
3. The error contains the IF YES / IF NO binary.
4. (If --definition required) `endless phrase add verb 'foo'` without
   `--definition` errors with a prompt to provide one.
5. (If agent-aware) the same command from a non-agent shell produces a
   shorter form without the preamble.

## Out of scope

- Audit and prune existing verbs that may have been registered via past
  rationalization. File as a separate "audit verb vocabulary" task if needed.
- Generalizing this pattern to other Endless gates (status, phase, type).
  Each gate has its own failure modes; design per-gate.
