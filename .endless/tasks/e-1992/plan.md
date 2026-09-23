# Named content slots to replace bespoke task columns

The storage half of E-1991.

## The point

Move task content off a fixed set of nullable columns and onto rows:
`task_content(task_id, name, content)`. A new content kind then costs an INSERT
rather than a migration.

That is the whole return, and it is why deferring is the expensive option:
until it lands, every new content need costs a column, and every content need
that is not worth a column gets crammed into a column meant for something else.

**Everything below the shape is a detail to settle while implementing, and
nothing here is binding until it ships.** This plan records direction, not
commitments.

## Settled

- **Rows, not columns.** The shape above.
- **The convention, never the list** (settled on E-1531): one lowercase
  single-token name per content kind, and the same token used by
  `task_content.name`, the Agent Folio Format `Name` header, and the CLI flag.
  New content kinds get identified on an ongoing basis, so what is settled once
  is the rule a new name inherits — not an enumeration that has to be reopened.

## Not settled, deliberately

The list of names, whether names vary by task type, and the per-type surface
words. E-1531 ships the table carrying today's names with today's semantics and
defers all of it; that separation is what makes E-1531 shippable, and it should
not be undone here.

An earlier draft of this plan called the name set a closed enum versioned per
release, where adding a name is a release rather than a runtime decision. That
contradicts E-1531 and contradicts the point above — in a row store a name is a
row. Withdrawn.

## One name to get right on the way through: `outcome` holds two different facts

`tasks.outcome` is carrying two unrelated things, and a straight lift into a
single `outcome` row would carry the overloading forward into the new storage:

- **A deliverable.** On a research or brainstorm task the outcome IS the work
  product — long, authored before the status flips, and read by whoever
  consumes the findings.
- **A closing reason.** Why a task ended: the reason on `declined`, `obsolete`
  and `superseded`. Short, written at the moment of the decision, and *about*
  the work rather than being it.

They are written at different times by different people for different readers,
and the overloading already costs something concrete. The guard that requires a
reason on every abandonment cannot honour a reason already stored on the row,
because a stored `outcome` might be a research task's findings — and accepting
those as the answer to "why did you abandon this" would be wrong. So the guard
has to demand the flag again even when the row already says it, which is felt
most on exactly the bulk paths where a mandatory field attracts junk values.

Two names rather than one removes that. A stored closing reason can only have
got there by someone writing a closing reason, so honouring it is unambiguous.

Decisions already drew this line: `decision obsolete` has its own reason field
precisely because the existing rejection reason answered a different question.
Tasks never got the same split.

Naming and whether the deliverable keeps the `outcome` token are
implementation calls, subject to the convention above.

## Scope

Content slots only. The open-questions table that an earlier draft carried in
this plan is a different table for a different purpose and now has its own task
under E-1991, which names giving open questions a home as its own strand.

## Children

- **E-1531** — the table, the migration off today's columns, the convention.
- **E-1562** — justification content off the `## Justification` notes heading.
- **E-2109** — description and title caps, which need the same migration pass.
