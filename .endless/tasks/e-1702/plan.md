# E-1702 — Reword brainstorm handoff opening

## Problem

`internal/templatecmd/templates/handoff/brainstorm.md.tmpl` opens the spawned
session with (line 13):

> **Open by interviewing the requester** — ask questions first.

and reinforces it at line 19:

> You're already claimed and in the worktree — start the interview.

"Interview / ask questions first" reads to Claude as a cue to call the
`AskUserQuestion` tool, which pops a discrete multiple-choice picker. That is
the opposite of what a brainstorm should be: a free-form, prose exchange where
Claude first says what it understands the task to be and what's still open, then
invites the requester to react.

## Scope

Only `brainstorm.md.tmpl`. The `AskUserQuestion` mention in `epic.md.tmpl` is a
deliberate decomposition affordance for a different flow and stays as-is.

## Change

**Line 13** — replace the opening sentence pair. Proposed wording:

> This is a brainstorm: the requester's head is the primary source, not external
> research. **Open with an open-ended discussion, not a questionnaire.** In prose,
> lay out what you understand the task to be and the open questions you see, then
> invite the requester to react and steer. Do NOT use the AskUserQuestion tool or
> lead with a list of discrete questions — that collapses the free-form exchange
> this is meant to be. Do NOT go off and research autonomously, and do NOT jump to
> a plan. Surface tensions, offer options, challenge the thinking, and capture
> ideas as they emerge. The deliverable is the synthesis plus the follow-ups it
> spawns.

**Line 19** — change "start the interview" to "open the discussion".

## Verification

- `git diff` shows only those two edits in `brainstorm.md.tmpl`.
- Render the template (spawn a brainstorm, or exercise whatever `templatecmd`
  path renders handoffs) and confirm the opening now reads as an open-ended
  discussion with no interview/AskUserQuestion cue.

## Notes

- Exact wording above is a proposal open to Mike's edit before implementation.
- No Go logic changes; template text only.
