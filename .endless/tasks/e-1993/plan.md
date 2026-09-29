# Limit task field lengths and require a plan before spawning

The mechanical half of E-1991. Blocked by E-1531 (the `task_content` table)
and E-2176 (the `task_questions` table) — the gate is expressed in terms of
the `plan` content row and the questions table.

The division of labour across this epic: **mechanical enforcement for shape,
judged enforcement for semantics.** This task is the mechanical layer. It must
be cheap, deterministic, and unbeatable. Anything requiring judgment belongs to
the read-through (E-1994).

## 1. Length limits

- `title` → 60 characters. Recognizable, not descriptive.
- `description` → 256 characters. Describes the task; explicitly does not
  substitute for a plan.

Both enforced at `task add` and `task update` as hard refusals (decided), with an error naming the right
destination rather than only the limit — a description that is too long should
say where the excess belongs, not "256 char limit exceeded". The error message is
the teaching surface; treat its wording as part of the work. Per E-2187's survey,
the destinations are: why the task exists, how things work today and the evidence
→ `context` (§1a); how, design, rationale, scope and open questions → `analysis`
(or `plan` once approved work); relationships → a task link, never an id in the
text. A title that is too long should say to drop the how, the why and any
parenthetical.

E-2187 measured the corpus: only 2% of descriptions already fit, but every task
could be rewritten within 60/256 without losing its WHAT (proposed titles p95 59,
proposed descriptions median 158, p95 202). The caps are attainable, not aspirational.

Neither title nor description may cite a task or decision id (59% of descriptions
and 6% of titles do today). Enforce it at add and update alongside the length
limits: an `E-NNN`/`ED-NNN`/`ES-NNN` token in either field is refused with an error
saying to name the thing in words and record the relationship as a task link. Ids
stay allowed in every content row.

The limits apply to the field being written: an update that does not touch an
old over-length title or description is not refused. Nothing is truncated.

## 1a. Add the `context` content name

One new `taskcontent` constant, `context`: why the task exists — how things work
today, what prompted the task, and the evidence for it. Never how the work will be
done. The `taskcontent` constant gives it storage, the heading and the mirror stem
(`context.md`), but the CLI flags are declared by hand in `src/endless/cli.py`, so
this task adds them, matching `analysis` exactly:

- `task add` and `task update`: `--context` (inline) and `--context-file`, with
  the same empty-file refusal and content write gate as the other content flags;
- `task update --clear context`;
- `task show --context` to show it, and render it by default directly after the
  description (`--all-fields` includes it).

E-2187's justification: background, status quo and evidence are 30% of all
description text today, in 63% of tasks, and no existing slot fits them
(`analysis` is design toward a plan, `notes` is miscellany). One name rather than
three because the classifiers could not reliably separate background from
evidence and status quo. No other new names: rationale and open questions were
the least separable categories, and `questions` / `acceptance` wait for a survey
of plans once plans are required (E-2187 outcome, "Other fields considered").

## 2. A plan is required to spawn

Filing stays cheap: a task may be filed with no plan. What changes is that a
task with no `plan` content row is **not spawnable**.

This is deliberately not "a plan is required to file". Requiring one at filing
forces an agent to plan work it is not doing in an area it may not know, which
produces plan-shaped compliance text — the exact noise the epic exists to
remove. Filing without a plan is legitimate; the task simply parks.

The gate fires in `task claim` and `task spawn`. Its refusal is how an older
task with no plan gets one (§5), so the message must offer the path forward, not
just the refusal.

## 3. Open questions park a task

**Any `task_questions` row in state `open`** makes a task non-spawnable, exactly
as a missing plan does. (The table is specified by E-2176.)

There is no blocking-versus-clarifying classification to make, and no classifier
to build. The medium decides: a session talking to a live user asks its
clarifying questions in conversation and they are answered on the spot, so they
never become rows. A paused session has nobody to ask — so every question it
holds goes to the table, and every one of them blocks, because the session has
stopped. A row exists precisely when there was no live human to answer it.

The reason this matters is behavioural and is the core insight of E-1991: today
an agent asked to file a plan will file it *and then* list its open questions,
because ending a turn with nothing filed reads as failing the instruction. When
told "that isn't a plan," the only in-turn way to comply is to decide the
questions — which is the worst outcome. Making "plan drafted, N questions open"
a **legitimate, complete, filed state** removes that pressure entirely: the
agent's turn succeeds, the questions land durably instead of scrolling away in
chat, and filing becomes the way to ask rather than the opposite of asking.

Surfacing: `task show` renders open questions prominently, and
parked-on-questions tasks are the natural feed for E-1976's attention surface and
E-1996's approval UI.

Question *volume* is a quality problem, not a schema problem — thirty trivial
questions is a bad read-through, addressed by the challenge facet and by
prompting, not by a blocking/clarifying column.

## 4. Remove the `untriaged` status and the description triage

`untriaged` exists because a task could once be worked from its description
alone, so every new task needed a judgment: is this description a sufficient
spec? With a plan required to spawn (§2), no description is ever a sufficient
spec, so that judgment and the status that holds tasks waiting for it both go.

- **Statuses:** remove `untriaged`. A new task is filed `unplanned`, or
  `submitted` when it is filed with a plan. Every transition into `untriaged`
  retargets: reconsidering a declined, obsolete or superseded task returns it to
  `unplanned` (`submitted` if it has a plan). The transition table in
  `internal/taskstatus/transitions.go` is the source; regenerate the lifecycle
  diagram from it (`just lifecycle-index`).
- **The description triage:** remove the call that judges description
  sufficiency, its detached spawn on `task add`, and the periodic sweep that
  drains the untriaged queue (`endless triage run`, the triage job and its reads).
  Search for `untriaged` and `triage` across `src/`, `internal/`, `cmd/` and
  `docs/` for the full footprint.
- **Description edits never change status.** The description is no longer the
  spec, so the description-edit reset is removed outright, not retargeted.
- **Plan edits on an approved task drop approval.** A material plan change on a
  `ready` task returns it to `submitted`, because what was approved changed. This
  is the one edit inference that remains, and it is what E-1995's dispute
  resolution relies on.
- **Existing data:** no task in the main database is `untriaged` today. A sandbox
  or another project's database with `untriaged` rows maps them to `unplanned`
  (`submitted` with a plan) in the schema change.
- **README.md:** the workflow section ("New tasks start `untriaged`; triage…")
  and its embedded lifecycle diagram are rewritten for the new model.

Nothing replaces the description triage; its only lasting effect is the initial
status above. Other processes that later need to run on tasks are designed on their
own, each with a trigger chosen for what that process does.

## 5. Existing tasks

There is no grandfathering code. Every rule in this task is enforced where it
naturally fires, and an old task meets it exactly as a new one does:

- **Length and id rules** fire when the field is written, so an old task is
  unaffected until someone edits its title or description.
- **The plan gate** fires on claim and spawn, so an old task with no plan is
  refused like a new task filed without one, and gets its plan the same way (the
  read-through, E-1994).

That is also the PRODUCT behaviour: someone upgrading Endless on their own
project has their existing tasks handled by the same gates, with no migration
step and no Endless-specific code path.

**For this project, titles and descriptions are rewritten in bulk (decided).**
E-2187 produced a proposal for every task: `.endless/tasks/e-2187/rewrites.jsonl`
(proposed title and description, verbatim classified segments, the text each
displaced segment moves to — `context`, `analysis`, `notes` — and per-row
`allow_paths`). It is applied by a one-off script that is not merged:

- skip any row whose `source_hash` no longer matches the task;
- `task update --title … --description-file … --keep-status`;
- append moved text to existing `analysis`/`notes` under `## From the description`,
  never replace;
- pass each `allow_paths` entry as `--allow-path`; moved text already passes the
  line-citation gate;
- run after §1a (`context`) has landed;
- trial on a sample Mike reviews before the full run.

Plans are not bulk-authored: a plan is real planning work on each task, which is
what the read-through does when a task is picked up.

## 6. Docs

Rewrite the field model in `docs/guide/tasks.md` and CLAUDE.md: what each slot
is for, the description-describes / plan-specifies boundary, the spawn gate, and
open questions as a first-class filed state rather than a failure.

Drop the three phrases E-2187 found inviting long descriptions: "*what* and
*why*", "< 200 words", and "max 1024 character". Adopt the "What a title is NOT"
and "What a description is NOT" rules from E-2187's outcome verbatim, with their
before/after examples, and define `context` beside the other content names.

## Acceptance

- Title over 60 or description over 256 is refused at add and update, with an
  error naming the correct slot.
- A task or decision id in a title or description is refused at add and update.
- `context` exists as a content name: `--context` / `--context-file` on add and
  update, `--clear context`, `task show --context`, rendered by default after the
  description, mirrored as `context.md`.
- A task with no `plan` row cannot be claimed or spawned; the refusal states how
  to proceed.
- A task with any `open` row in `task_questions` cannot be claimed or spawned
  and is visibly parked in `task show`.
- `untriaged` no longer exists: new tasks file as `unplanned` (or `submitted`
  with a plan), the description triage and its sweep are gone, and no transition
  targets `untriaged`.
- A description edit never changes status; a material plan edit on a `ready` task
  returns it to `submitted`.
- README.md's workflow and lifecycle diagram match the new model, and
  `just lifecycle-check` passes.
- `--keep-status` still suppresses every inference.
- Nothing is truncated, and an update that does not write an over-length field is
  not refused.
- E-2187's rewrites are applied in bulk after Mike reviews a sample; rows whose
  `source_hash` no longer matches are reported, not applied, and no task's status
  changes.
- Guide and CLAUDE.md describe the new model with no stale references to
  `text`, the 100/1000/1024 limits, "what and why", or description-as-spec, and
  carry E-2187's "is NOT" rules.
- `go build/vet/test ./...` and `just test` pass.

## Scope grown during implementation

Folded in because each followed directly from the plan and was cheaper to do
than to file:

- **`submitted` needs a plan by every route.** With no description ever a
  sufficient spec, `task submit` and `task update --status submitted` refuse a
  task with no plan (the plan-attach promotion already implied one).
- **No planning exemption.** E-1813 (landed first) removed the tier-1
  advance to `ready`; confirmed with that session that neither task keeps an
  exemption, so the plan gate applies to every task and no reconciliation task
  was needed. Rebased onto it: the retire-untriaged migration is 00010.
- **Ledger replay of the retired status.** Historical events carry `untriaged`
  (task.created, the description-edit reset, reconsidering). The projector and
  the executor map it to `submitted` with a plan, else `unplanned`, matching
  migration 00010, so a rebuild agrees with a migrated database.
- **`description-reset-from` stays resolvable, empty.** The global install asks
  a worktree's newer endless-go for it at import time; an unknown group is
  fatal there, an empty one means "never reset", which is the new rule.
- **Triage removal footprint:** `endless triage run`, triage.py, the
  triage-sufficiency job, the triage session-query verbs and monitor reads, the
  `triage_claims` table (dropped in 00010 along with the job row), the
  sufficiency template, `◌ triage` in session status, the `triage` model
  default, and the triage help page. WARN-0009 stays in the catalog, retired,
  since numbers are never reused. `ActorTriager` stays valid for history.
  `errors record` is kept: it is the Python bridge to the fault store.
  E-1813's rating proposal inside triage went with it; ratings are proposed at
  `task submit` and nudged on plan-attach, as E-1813 already does.
- **`context` on epics too** (`epic show --context`, `epic update --context`),
  matching `analysis`.
- **CLAUDE.md:** the Python-reads-SQLite count went from six to five, because
  triage.py was one of them. The field model itself lives in the guide.

The bulk rewrite (§5) is a one-off script kept outside the merge
(`.endless/tmp/e-1993-apply-rewrites.py` in this worktree); it runs after land.
A full dry run against main: 1404 rows apply, 8 are removed tasks, 6 need no
change, 1 is stale by source_hash. Every proposed title and description passes
the new rules.
