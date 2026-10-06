# Questions to settle

1. **A separate `group` type?** Split today's epic into `epic` (initiative)
   and `group` (grouping), so each gets its own rules, gates and handoffs
   (plan requirement, coordinator session, derived status, auto-spawn,
   triage). Includes how today's 24 open epics get classified.
2. **What each requires instead of a plan.** Objective (outcome when done,
   plus what belongs and what does not) for both? Strategy for initiatives
   only — and is strategy a new task_content row or the existing plan row?
   Either way it lives in task_content.
3. **Session monitor rendering.** Which glyph an epic (and a group, if
   split) shows in place of the plan pencil; whether derived status is shown
   at all; how to make a non-do task unmistakable at a glance so it is not
   mistaken for todo/bugfix work.
4. **Enforcement.** Which of the above `task add` / `update` / approve refuse
   when missing.

# Deliverable

An outcome recording the answers, the decisions to propose, and the
follow-up tasks (type split, task_content rows, monitor glyphs, migration of
existing epics).
