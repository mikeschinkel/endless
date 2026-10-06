Raised by Mike, 2026-10-06, in the E-1883 session.

Epics are used two ways. E-1537 (the epic type) models an INITIATIVE: children
emerge from a strategy, the epic's plan is that strategy, and "not
enforced". Mike now also uses epics as GROUPINGS: a place to keep tasks he
may not spawn soon. A grouping has no strategy to write. Of 24 open endless
epics on 2026-10-06, 11 had a plan and 13 did not.

Session monitor shows an epic with the same pencil (plan) glyph as a task.
An epic's status derives from its children (E-1537 rule 3: any unplanned
child makes the epic unplanned), so on an epic the pencil means "some child
needs a plan" — E-2215 and E-799 showed it while having plans. Mike: that is
not actionable in a task list, and he often misses the 'E' and asks for
things that only apply to do-type tasks (todo, bugfix).

Positions Mike stated:
- Whatever an epic requires instead of a plan (objective, strategy, or
  both) is a row in task_content, not the description.
- At minimum an epic needs a different glyph from a task's plan pencil.

## Settled later the same day (Mike)

- ED-1610 (proposed): an initiative epic carries a `strategy` task_content
  row, named strategy and never plan, because distinct names make an agent
  behave differently and the epic/todo distinction must not depend on the
  agent noticing the type. It opens with the objective (what is true when
  done; fixed), then the approach (expected to change).
- ED-1611 (proposed): groupings get their own task type, `group`. A group
  needs nothing beyond its title and description.
- New content rows are hidden in `task show` unless asked for (E-2252).
