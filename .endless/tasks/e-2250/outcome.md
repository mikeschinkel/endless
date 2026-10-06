# What an epic requires instead of a plan — synthesis

Settled with Mike on 2026-10-06 (questions EQ-20 to EQ-33).

## Two container types

- **epic**: an initiative, usually a product feature. It carries two
  task_content rows, typically never a plan:
  - `objective`: what is true when the epic is done. It typically stays fixed;
    a nuance may change without making it a different epic.
  - `strategy`: the approach, which is expected to change.
- **group**: a convenience grouping of tasks kept together for tracking. It
  needs only its title and description, and its completion derives from its
  children.
- The type is chosen at filing, when it should usually be clear which kind
  the work is. A group may be changed to an epic when someone realizes it is
  one, but that is happenstance, not the design, and should be rare.
- Neither nests in the other: a group holds no epics, and an epic holds no
  groups. A task has one container, so it cannot sit in a group and under an
  initiative at once. That is accepted.
- Both can be claimed and spawned. The session only coordinates.

## Enforcement

- `task add` accepts an epic with no objective or strategy, just as it accepts
  a todo with no plan, so filing stays cheap.
- Claim and spawn refuse an epic missing either row. This replaces today's
  plan requirement for epics.
- Existing epic plans move into objective and strategy case by case. A plan
  that fits neither stays as a legacy plan row; nothing forces it to move.

## Display

- An epic shows four states: unplanned, submitted, underway, complete.
  Unplanned means the epic itself lacks an objective or strategy, and it
  wears the plan pencil, because filling those in is the epic's own job. It no
  longer means "some child needs a plan".
- A group shows three states: submitted, underway, complete.
- These are display names computed from the container's own rows and its
  children's states, not new statuses. Each gets its own glyph, distinct from
  the do-task glyphs. When Mike mistook an epic for a do-task, he was asking
  for it to be planned or verified (from memory), so the epic glyphs must not
  read as a do-task's plan or verify actions.
- project status and the session monitor show four sections: urgent, epics,
  groups, other.

## Classification

- An agent proposes the epic/group split of the open epics, and Mike approves
  it.
- E-1738 has no children yet but is waiting on future ones, so it stays an
  epic for that classification to decide.
- E-1790 has been retyped to a todo, now standalone and related to E-1596,
  since its analysis calls it general-purpose beyond verify.

## E-1537

E-1537's plan stays as history. ED-1610, ED-1611 and ED-1612 replace its
sections on what an epic is and how its status derives, and all three are
linked to it.

## Decisions

ED-1611 and ED-1612 are accepted; ED-1610 is still proposed.


- ED-1610: an epic carries objective and strategy rows, required to claim or
  spawn. Revised here: objective became its own row, the gate was set at
  claim/spawn, and "never a plan" and "stays fixed" were softened to
  "typically".
- ED-1611: groupings get their own type, group. Extended here with the
  nesting, conversion and coordinator rules.
- ED-1612: epics and groups show a container state ladder, not task actions.

## Follow-up

- E-2254 (under E-1991, cleans up E-2250): one task for the whole change, per
  ED-1550. It covers the group type, the objective and strategy rows with the
  claim/spawn check, the container states and four sections in the status
  views, and sorting the open epics. It implements all three decisions.
  E-2255 and E-2256 were folded into it and superseded.
