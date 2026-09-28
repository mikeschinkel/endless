# Research brief: how titles and descriptions are actually used

Parent: E-1991. Blocks E-1993, whose caps (title 60, description 256) should
not land until content that does not belong in those fields has a named place
to go. With task_content in place (E-1531), a new content name is one
taskcontent enum constant — it gets a flag, a heading and a mirror for free —
so the cost of adding the right names is low and this research is where the
deferred vocabulary question gets answered from data rather than anecdote.

## What Mike wants these fields to be

**Title** — a scannable handle that lets him tell one task from another AFTER
he already understands the task from its description. Short, distinctive, no
"how". Example, E-1813:
- current: "Replace tasks.tier with complexity and risk rating axes proposed at
  submit and ratified at approve"
- better: "Replace tasks.tier with complexity and risk rating"
Titles are already verb-first; do not check or report on that.

**Description** — just enough that he recognizes what the task is after reading
it: a blurb, as it would appear on a web page listing tasks. WHAT, not HOW.
Example, E-1813's better description:
"Replace tasks.tier with two rating axes to be agent-proposed at submit and
user-ratified at approve. The two axes will be complexity and risk and both
will use rating values of 'low', 'medium' and 'high'."
If renaming the field (e.g. `blurb`) would reliably produce better content than
`description`, say so and argue it; Mike is open to it.

## Questions to answer

1. What do descriptions contain besides the WHAT? Classify by content, not by
   task — one description can mix several. Starting categories (refine them
   from the data): what/pitch, background or scenario, current workflow or
   status quo, how/approach, reproduction or evidence, rationale or
   alternatives considered, acceptance criteria, status updates or history.
2. What do titles contain beyond a distinguishing handle? (The "how", scope
   qualifiers, mechanism detail, parentheticals, ids.)
3. For each non-WHAT category: how often, how many characters, and where it
   belongs — an existing content name (plan, analysis, notes), or a NEW one.
4. Which new content names, if any, are justified? Propose each with a
   one-token name (the convention: the name is the flag, heading and mirror
   stem), a definition, and the volume in the data that justifies it. Do not
   propose a name the data does not support.
5. Is 256 the right description cap? Mike is confident it is. Recommend a
   different number only on VERY strong evidence — e.g. a large share of
   descriptions that are pure WHAT and still cannot fit.

## Deliverables (as the outcome)

- "What a title is NOT" and "what a description is NOT" — short, concrete rules
  ready to drop into `docs/guide/tasks.md`, each with a before/after example
  drawn from real tasks.
- The classification results with counts and representative examples.
- The proposed content names (or a justified "none needed").
- The cap recommendation (expected: keep 256).
- A before/after rewrite of a handful of real titles and descriptions under the
  proposed rules, so the rules can be judged by their output.

## Method notes

- Corpus: every task's title and description, including terminal and removed
  ones. Read through the CLI (`endless task list/show --json --db main`), not by
  opening the database file.
- Roughly two thousand tasks: a model-assisted classification pass with a
  hand-checked sample is fine; report the sample size and agreement.
- Do not change any task. This task produces findings only.
