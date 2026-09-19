# Seed / framing

Mike is calling the question on the task content structure — title, description, `text`
(plan), analysis, and the `untriaged`/`unplanned` statuses layered on top of them. After
living with it, it is not working, and his read is that the *original* structure was sound
but agents misused it, and each accommodation for that misuse became another layer.

## The failure chain as Mike describes it

1. `description` was meant to be a longer human-readable *description* of the task, read via
   `task show`. `title` was meant to be concise — just enough for a user to recognize the task.
2. Agents wrote descriptions into titles, so titles got capped at 100 characters.
3. Agents then wrote mini-plans into descriptions, so descriptions got capped at 1000 characters.
4. Agents still wrote mini-plans into descriptions. Now it is impossible to tell whether a
   description is a sufficient spec or whether a real plan is still needed.
5. Triage was introduced to answer exactly that: look at tasks with no plan in `text`, decide
   whether the description suffices (→ `submitted`) or not (→ `unplanned`).
6. Even so, Mike constantly has to ask agents to plan tasks that are not planned.
7. Worst of all: agents leave **open questions** inside `text`. By definition it is not a plan
   if it has open questions — and open questions are **not the agent's to decide**. The agent's
   job is to present pros and cons per question and let the user choose.

## Mike's opening proposals (starting points, not decisions)

1. `title` — short, recognizable. ~60 characters.
2. `description` — describes the task, **must not substitute for a plan**. Maybe ~256 characters.
3. Plans are **not optional**. If at filing time you want to describe the plan, write the plan.
4. Rename the `text` field to `plan`. Long overdue; do it with the rest of this.
5. Triage becomes multi-faceted: verify title is not a description and description is not a
   plan; add plans where missing; evaluate whether a plan has open questions.
6. Possibly add a `questions` field for open questions, so post-triage you can tell whether a
   task still has unresolved questions.
7. E-1531 (task_content table for typed content) may belong in the same epic.
8. ED-1550 applies: fewer tasks is better. This needs an epic, but a small number of children.

## Open questions the agent sees going in (for Mike to decide, not the agent)

- Does "plans are mandatory" hold for every task type? Tier 1, bugfix, research, brainstorm,
  and epic all have different deliverable shapes; research/brainstorm already invert the field
  model (`text` = request/seed, `outcome` = deliverable).
- If the plan is mandatory and open questions are a first-class field, does `unplanned` still
  earn a status, or does it collapse into "has no plan" as a derived fact?
- Hard character limits vs. enforced-by-judgment. Limits are cheap and mechanical but reward
  gaming (an agent compresses a mini-plan into 256 characters); a triage judgment catches
  semantics but costs a model call and can be wrong.
- Does the `questions` field block approval — i.e. can a task with open questions ever reach
  `submitted`/`ready`? A gate is the strongest possible enforcement of "you don't decide these."
- Relationship to `analysis`. If `questions` becomes a field and content goes typed (E-1531),
  what survives as a bespoke column and what becomes a content row?
- Sequencing against in-flight work: E-1844 (auto-routing epic, `submitted`), E-1970 (surface
  triage rationale), E-1946 (type-aware triage for brainstorm), E-1948 (routing is the agent's
  job), E-1861 (decisions on tasks), E-1531 (task_content), E-1562 (justification → typed rows).
  Several of these are already specced against the *current* structure.
