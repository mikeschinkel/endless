# Rate the ready backlog

## Why

E-1813 added complexity and risk ratings; E-1814 auto-spawns `ready` tasks rated
low/low. The ready tasks were all approved before ratings existed, so none is
rated and nothing is eligible. Ratings are freely editable after approval (a
choice Mike made in E-1813), so rating them here is the same act one agent
would take on one task, done once for the backlog.

## Method

1. List every `ready` task in `endless` (`task list --status ready --json --db main`).
2. Spawn one subagent per task, five at a time. Each gets the full context of its
   ONE task: `task show E-N --all-fields`, the parent and its siblings, linked
   decisions, the code the task names, and a check of whether the work already
   exists on main. Full context is the point; do not batch tasks per subagent.
3. Each subagent returns: still worth doing (yes / obsolete / superseded by
   E-N / unsure), has a plan (yes/no), proposed complexity and risk, and a
   one-line reason for each rating.
4. Write the ratings with `endless task update E-N --complexity X --risk Y
   --keep-status --db main`. Do NOT change any task's status: a task judged
   obsolete or superseded is reported, not closed. Closing is Mike's call.

## Be conservative

`low`/`low` makes a task auto-spawnable the moment Mike enables auto-spawn for
the project, so give it only when ALL hold:

- the task is clearly still worth doing;
- the change is contained and easy to revert;
- a stranger could implement it without asking a question;
- it touches no durable state (ledger, schema, migrations, config formats) and
  no destructive operation.

When torn between two levels, choose the higher. A task with no plan may still
be rated honestly; E-1814's filter keeps it from spawning until it is planned.

## Deliverable

An outcome with one row per task: id, title, still-worth-doing verdict, has
plan, complexity, risk, and the reasons, grouped so the low/low rows come first.
Mike skims it before turning on auto-spawn for `endless` (E-1814 is opt-in per
project, off by default).
