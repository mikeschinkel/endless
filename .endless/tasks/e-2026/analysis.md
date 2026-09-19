## The observation

E-1481, E-1482 and E-1484 were each written with line-number citations, symbol
inventories and file maps. Reviewed three months later, every line number was
wrong — one by more than two thousand lines — one cited file had been deleted
outright, and one verification gate was unachievable because the codebase had
moved. Refreshing them only resets a clock.

Mike's standing rule, restated: a plan says WHAT and high-level HOW. The
implementor is as capable as the planner. Line numbers never belong; file names
usually do not either.

## Why prose alone will not hold

The rule already exists — Mike has given it many times. It is not being followed
because a detailed plan LOOKS like thorough planning. The pull is toward showing
work, so the behavior reappears wherever it is not blocked.

That said, a role preface is not the same instrument as a prohibition. A
prohibition competes with an impulse at the moment of acting. A preface sets the
prior at generation time, before there is an impulse. Framing is genuinely
better placed for this, which is why it is worth doing — just not worth
trusting alone.

## Two parts

**1. The preface.** A planner-role framing injected where a planning agent picks
up work. The handoff templates are the existing surface and are already
per-task-type. Precedent for a role line: the report minimizer's prompt opens by
naming its role. Wording is a config surface, not source — tunable without a
task, same as the minimizer prompts.

The content should be positive, not a list of bans: name what a plan owes the
implementor (what must be true when done, which constraints are not negotiable,
what would make the result wrong) and say plainly that details an implementor
would find are not the planner's to supply.

**2. The check.** Refuse a file-and-line reference in durable task content
(description, plan text, analysis). Endless already refuses absolute paths there
for a related reason — non-portable, and meaningless once a worktree drops — so
this extends existing machinery rather than adding a gate. A line reference is
trivially detectable; unlike prose about over-specification, it needs no
judgment.

File NAMES should warn rather than refuse. Naming a file is sometimes the
invariant (the schema lives in one place, and saying so is useful). A hard ban
would make plans worse.

## Open questions for pickup

- Does the preface belong in the handoff templates, in `endless guide`, or both?
  The guide is read once at session start; the handoff is read when the work is
  picked up, which is closer to the moment of authoring.
- Should the check run on `task update --text` only, or on any durable content
  write? Same answer as the absolute-path gate, presumably.
- Is there an escape hatch, and does it need one? A plan legitimately quoting a
  stack trace would trip it. The absolute-path gate's precedent is an explicit
  opt-in flag.

## Honest caveat

The underlying reflex — detail as a proxy for diligence — is the same one behind
unrequested justification in replies (recorded separately). Neither the preface
nor the check removes the reflex; they remove its cheapest outlet. Expect it to
reappear as verbose invariants, and expect that to be less harmful than a map
that goes stale.

## PRODUCT

Every project using Endless has agents authoring plans for other agents, so
every project accumulates the same rot. Both halves ship: the preface as
template content, the check alongside the existing content gates. `self_dev`
changes nothing.
