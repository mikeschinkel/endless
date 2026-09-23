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

## Strand 3 — open questions get a home

A table rather than a content row: a task accumulates many questions across
many rounds, each with its own answer and state.

## Strand 4 — triage becomes a read-through that primes the implementing session

## Structure

Two levels, per ED-1598: these children have no children of their own. Where
several of them are bound together by a shared mechanism, that is a
`blocked_by` relation between siblings, not a container.
