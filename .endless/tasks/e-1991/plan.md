# Redesign task content, plan requirements, and triage

Titles became descriptions and descriptions became mini-plans; each
accommodation added a layer. Four strands, one epic.

Nothing below is binding until it ships. This plan records direction.

## Strand 1 — content storage: rows, not columns

Move task content off a fixed set of nullable columns and onto rows:
`task_content(task_id, name, content)`. A new content kind then costs an INSERT
rather than a migration.

That is the whole return, and it is why deferring is the expensive option:
until it lands, every new content need costs a column, and every content need
not worth a column gets crammed into a column meant for something else.

**Settled:**

- The shape above.
- **The convention, never the list**: one lowercase single-token name per
  content kind, the same token used by `task_content.name`, the Agent Folio
  Format `Name` header, and the CLI flag. New content kinds get identified on
  an ongoing basis, so what is settled once is the rule a new name inherits —
  not an enumeration that has to be reopened.

**Deliberately not settled:** the list of names, whether names vary by task
type, and the per-type surface words. The table ships carrying today's names
with today's semantics and defers all of it; that separation is what makes it
shippable and should not be undone.

An earlier draft called the name set a closed enum versioned per release, where
adding a name is a release rather than a runtime decision. That contradicts the
point above — in a row store a name is a row. Withdrawn.

### One name to get right on the way through: `outcome` holds two facts

`tasks.outcome` carries two unrelated things, and a straight lift into a single
`outcome` row would carry the overloading into the new storage:

- **A deliverable.** On a research or brainstorm task the outcome IS the work
  product — long, authored before the status flips, read by whoever consumes
  the findings.
- **A closing reason.** Why a task ended: the reason on `declined`, `obsolete`
  and `superseded`. Short, written at the moment of the decision, and *about*
  the work rather than being it.

Different authors, different moments, different readers. The overloading
already costs something concrete: the guard requiring a reason on every
abandonment cannot honour a reason already stored on the row, because a stored
`outcome` might be a research task's findings, and accepting those as the
answer to "why did you abandon this" would be wrong. So the guard demands the
flag again even when the row already says it — felt most on the bulk paths
where a mandatory field attracts junk values.

Two names remove that: a stored closing reason can only have got there by
someone writing a closing reason, so honouring it is unambiguous.

Decisions already drew this line — `decision obsolete` has its own reason field
precisely because the existing rejection reason answered a different question.
Tasks never got the same split.

Naming, and whether the deliverable keeps the `outcome` token, are
implementation calls subject to the convention above.

## Strand 2 — field shape and the plan-required spawn gate

E-1993. Title 60, description 256, a plan required to spawn but never to file,
any open question parks the task, and the re-approval reset retargets from
description to plan. Mechanical enforcement for shape; judged enforcement for
semantics belongs to strand 4.

**Settled 2026-09-26: the description cap is 256, E-1993's number.** E-2109 was
filed carrying 384 and has been corrected. The two children are not duplicates
— E-1993 sets the caps and grandfathers every existing row, E-2109 is the
migration that eventually makes them hold everywhere.

## Strand 3 — open questions get a home

E-2176. A table rather than a content row: a task accumulates many questions
across many rounds, each with its own answer and state.

## Strand 4 — triage becomes a read-through that primes the implementing session

E-1994, then E-1995. Triage spawns the real implementation session, which reads
in, records its open questions and holds as primed — no evaluator-implementer
gap to calibrate, and questions arrive while the user's context is still warm.
E-1995 then automates the objection round-trip between sessions, so only genuine
deadlocks reach the user, with both positions already argued.

## Structure

Two levels, per ED-1598: these children have no children of their own. Where
several of them are bound together by a shared mechanism, that is a
`blocked_by` relation between siblings, not a container.

## Repairs made 2026-09-26 while coordinating

Dissolving E-1992 into this plan dropped two things it had been carrying, and
neither was visible from the graph:

- **Its blocking edges.** E-1993 and E-1994 were `blocked_by E-1992`, and a
  terminal blocker releases its dependents — so both read as unblocked while
  their plans still depended on the storage E-1992 was going to ship. The real
  edges are now recorded: E-1531 and E-2176 block E-1993 and E-1994; E-1531 and
  E-1993 block E-2109. The inert E-1992 edges were removed.
- **Its child numbering.** The plans on E-1993, E-1994 and E-1995 referred to
  "Child 1" through "Child 4" — E-1992's internal numbering, which stopped
  resolving to anything. Rewritten to task IDs, with "Child 1" resolved per site
  to E-1531 or E-2176 depending on which half of the old storage child was meant.

The lesson for the remaining dissolutions E-2178 inventories: a container's
plan-internal cross-references and its blocking edges are both content that has
to be placed before it closes. E-1992's outcome accounted for the children and
the prose; these two were still lost.
