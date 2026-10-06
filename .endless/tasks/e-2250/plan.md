# Questions still to settle

1. **Introducing the `group` type** (ED-1611): which rules, gates and
   handoffs differ from epic (strategy requirement, coordinator session,
   derived status, auto-spawn, triage), and how today's open epics are
   classified into epic or group.
2. **Session monitor rendering.** Which glyph an epic and a group
   each show in place of the plan pencil; whether derived status is shown
   at all; how to make a non-do task unmistakable at a glance so it is not
   mistaken for todo/bugfix work.
3. **Enforcement.** Whether `task add` / `update` / approve refuse an
   initiative epic without a strategy, and what happens to existing epic
   plans (move to strategy, or leave).

Settled already: see the context (ED-1610, ED-1611).

# Deliverable

An outcome recording the answers, the decisions to propose, and the
follow-up tasks (type split, strategy row, monitor glyphs, migration of
existing epics).
