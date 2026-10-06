# Questions still to settle

1. **A separate `group` type?** Split today's epic into `epic` (initiative)
   and `group` (grouping), so each gets its own rules, gates and handoffs
   (strategy requirement, coordinator session, derived status, auto-spawn,
   triage). Includes how today's open epics get classified.
2. **Session monitor rendering.** Which glyph an epic (and a group, if
   split) shows in place of the plan pencil; whether derived status is shown
   at all; how to make a non-do task unmistakable at a glance so it is not
   mistaken for todo/bugfix work.
3. **Enforcement.** Whether `task add` / `update` / approve refuse an
   initiative epic without a strategy, and what happens to existing epic
   plans (move to strategy, or leave).

Settled already: see the context (ED-1610; groups need nothing more).

# Deliverable

An outcome recording the answers, the decisions to propose, and the
follow-up tasks (type split, strategy row, monitor glyphs, migration of
existing epics).
