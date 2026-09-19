# Plan — Bracket an agent's CLI refusal with a repeated one-line verdict

## The failure this fixes, measured

`endless task update E-2093 --title <104 chars>` refuses with 17 lines on
stderr. The verdict is line 2:

    1
    2   Error: Title is 104 characters; max is 100.
    3
    4-16  (fourteen lines of guidance on how to shape a title)
    17

An agent piping `2>&1 | tail -3` keeps lines 15-17 — the tail of the guidance
block. The one line that names the constraint is the one line a small window
cannot see.

What that cost, in one session, across `task add` and `task update` (both call
`validate_title`, task_cmd.py:312, from :2252 and :4624):

1. `task add`, title 107 chars — refused. Read the fragment as a SHAPE
   complaint, reworded, and passed at 94 by accident while believing the shape
   was the problem.
2. `task add`, description 1032 > 1024 — refused.
3. `task update --title`, 141 chars — refused.
4. `task update --title`, 107 chars — refused. Thirty-four characters shaved
   between attempts 3 and 4 without ever reading the number the error stated
   both times. That is editing toward a constraint rather than reading it.
5. `--title "test"` written to a LIVE task to probe the validator — a mutation
   of real state to learn what the error had already said twice.
6. `--description-file` with newlines — refused.

Five refusals and one junk write, all against a message that was correct,
complete, and on stderr the whole time.

## The change

The bracket renders when EITHER an agent is running the command or a human
asked to see what one sees:

    agent_env.present() or agent_help.agent_view_requested()

Both, not one. An earlier draft of this plan said "NOT the `--agent-view` flag,
because an agent will never pass a flag it does not know it needs." The premise
is true and the conclusion is wrong: `--agent-view` is not a detection
mechanism, it is a HUMAN's override for previewing agent-facing output while
debugging. Excluding it would take that away, and the whole point of the flag is
to make output like this inspectable.

`agent_help._should_augment()` already composes exactly this predicate, for
exactly this reason — its docstring reads "An agent is reading this help, or a
human asked to see what one sees." Reuse or extract it; do NOT write a fourth
spelling of the question. That module's history is two consolidations of this
same duplication (E-1966 folded in a private `is_claude_code_agent()`, E-2006
folded in the last `!= UNKNOWN` comparison), and adding another would undo
them.

So refusals are emitted as:

    <verdict line>
    <blank>
    … today's guidance block, unchanged …
    <blank>
    <verdict line>          ← byte-identical to the first

The two lines are IDENTICAL, deliberately. Split them — verdict first, remedy
last — and `head -N` yields the problem without the fix while `tail -N` yields
the fix without the problem. Identical means whichever end survives is
sufficient alone, and seeing both reads as one repeat rather than two findings.

Length is unconstrained: `head`/`tail` are line-based, so a 200-character line
survives whole. Keep it to one dense line rather than a paragraph — a paragraph
invites the same skimming that caused this.

## What the verdict line must carry

Ordered by what would have stopped the failure above at attempt 3:

1. **Measured numbers, never an adjective.** `title 107>100 chars`, not "too
   long". The number is what makes the correction computable.
2. **A fixed sentinel plus the command and field**: `ENDLESS-ERROR task add:`.
   A bare error line read out of context does not say what produced it.
3. **Where the overflow goes**, not merely "shorten": `long-form goes in
   --analysis (rationale) or --text (plan); the title names WHAT`. Without
   this an agent mangles the field to fit instead of relocating the content —
   which is what happened.
4. **Whether anything changed**: `Nothing was created.` A refusal leaves an
   agent wondering whether it partially applied; that uncertainty is part of
   what produced the junk write in step 5.
5. **Every problem at once**, not the first. See below.

Deliberately NOT in it: the template, the HOW-vs-WHAT rationale, the examples.
Those are worth having and worth losing.

Not JSON, not key=value. A stable prefix and a plain sentence parses fine and
stays readable when a human hits it.

Worked example, ~170 characters:

    ENDLESS-ERROR task add: title 107>100 chars; description 1032>1024 chars. Long-form goes in --analysis (rationale) or --text (plan); the title names WHAT. Nothing was created.

## Collect every problem before refusing

Today `validate_title` raises on the first failure, so the sequence is: fix the
title, resubmit, discover the description. Steps 1-2 and 3-6 above are both
that shape. The validators for title, description and analysis on `task add`
and `task update` should accumulate their findings and refuse once, with all of
them in the verdict line.

Scope this to those validators on those two verbs. It is not a project-wide
refactor of every ClickException.

## Shape it as a shared helper

A rule that reaches two messages is not a convention. Add one helper — e.g.
`agent_error(summary: str, guidance: str)` beside the existing agent-facing
machinery — that renders the bracketed form when `agent_env.present()` and
today's plain form otherwise. Adopt it first in the title/description
validators, and document it so a new refusal reaches for it.

## Humans see no change

The duplication is agent-only. A person reading the full message in a terminal
gets today's output byte for byte. This is a requirement, not a nicety: a
repeated long line is noise to a reader who was never going to truncate it.

## Boundaries

- Does NOT touch which stream errors go to. They are already on stderr, already
  complete; the agent truncated them. Nothing about stdout/stderr changes.
- Does NOT add a PreToolUse matcher against `2>&1 | tail`. That was considered
  and rejected: it targets a correlate, would not catch `| tail -N` over a long
  stdout report, and would fire on `go test ./... 2>&1 | tail -30`, which is
  correct usage (a failing `go test` writes to stdout; a failing `endless`
  leaves stdout empty — measured).
- Does NOT reorder the existing guidance. The bracket makes order irrelevant.

## PRODUCT

Nothing here is Endless-specific. Every project using Endless is driven by
agents that pipe and truncate CLI output, and every CLI puts its verdict first
and its teaching after. The agent-gated bracket belongs in the shipped CLI, and
the detection signal (`agent_env.present()`) already ships.

## Verify — .endless/tasks/e-<id>/verify.sh

1. Fail-fast unit gate: the Python tests this task adds.
2. Under a simulated agent env, a refusal's first and last lines are
   byte-identical.
3. That line carries the measured numbers, the command, the field, the
   destination for long-form content, and the no-change statement.
4. `… 2>&1 | tail -3` and `… 2>&1 | head -3` of the same refusal EACH contain
   the verdict — the two ends of the property.
5. With no agent env AND no --agent-view, the message is byte-identical to
   today's: no duplication, no sentinel.
6. With no agent env but --agent-view passed, the bracket IS rendered — the
   flag's purpose is to let a human see what an agent sees, so a version that
   only fires on detection would make agent-facing output the one thing
   --agent-view cannot show.
7. A call that violates title AND description limits at once names both in one
   verdict line and refuses once.
8. A refused call mutated nothing.




