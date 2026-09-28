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

**Neither may cite another task by number.** A description that says "informs
E-1993's caps" cannot be understood until the reader goes and reads E-1993. Say
what the other thing IS in words; the relationship itself belongs in a task
link, where tooling already shows it. Count how often past titles and
descriptions reference task (or decision) ids, and include this rule in both
"is NOT" lists.

The rule is for title and description ONLY. Task ids are fine — often the most
precise reference there is — in the plan, analysis, and every other content
row. Text moved out of a description keeps its ids; do not rewrite them away.

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

- **The per-task artifact** (below): a JSONL file of proposed rewrites for
  every task, for a later migration task to apply.

## The per-task artifact: `.endless/tasks/e-2187/rewrites.jsonl`

The classification already reads every task, so it also records what each
task would look like under the proposed rules. One JSON object per line, one
line per task:

```json
{
  "task": "E-1813",
  "updated_at": "<the task's updated_at when read>",
  "source_hash": "<sha256 of title + \"\\n\" + description as read>",
  "title": {"current": "...", "proposed": "..."},
  "description": {"current": "...", "proposed": "..."},
  "segments": [
    {"category": "what", "text": "..."},
    {"category": "current-workflow", "text": "..."}
  ],
  "moves": {"<content name>": "<text proposed for that content row>"},
  "notes": "optional: anything a reviewer should know about this row"
}
```

- **Segments are the durable part.** The content names this task proposes are
  provisional until Mike reads the outcome; if a name is renamed or merged, the
  `segments` labels let `moves` be recomputed mechanically instead of re-running
  the classification. Keep categories stable and documented in the outcome.
- **`updated_at` and `source_hash` make it safe to apply later.** Tasks keep
  changing; whatever applies this file must skip a row whose hash no longer
  matches the task.
- **`moves` merges with, never overwrites, existing content.** If a task already
  has a `plan` or `analysis`, say in `notes` how moved text would be combined.
- **It is a proposal only.** This task changes no task. Applying it is a
  separate migration task (likely part of E-1993's lazy migration), which can
  trial it on a sample Mike reviews first.
- Commit it in the task's own directory, beside `verify.sh` — not under
  `docs/`, and not as a document mirror.

## Method

- **Use subagents; a multi-agent workflow is authorized for this task.** The
  corpus is roughly two thousand tasks, a natural fan-out:
  - partition the tasks into batches of about 100, one subagent per batch;
  - give every subagent the same fixed schema (above) and the same category
    definitions, so batch outputs concatenate without reconciliation;
  - run an independent verification pass that re-classifies a random sample
    (a few percent, at least 50 tasks) and report the agreement rate, with the
    disagreements examined in the outcome.
- Corpus: every task's title and description, including terminal and removed
  ones. Read through the CLI (`endless task list/show --json --db main`), not by
  opening the database file.
- Do not change any task. This task produces findings and the artifact only.
