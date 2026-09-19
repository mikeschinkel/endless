# Task field shape and the plan-required spawn gate

The mechanical half of E-1991. Blocked by Child 1 (named content slots) — the
gate is expressed in terms of the `plan` and `questions` slots.

The division of labour across this epic: **mechanical enforcement for shape,
judged enforcement for semantics.** This task is the mechanical layer. It must
be cheap, deterministic, and unbeatable. Anything requiring judgment belongs to
the read-through (Child 3).

## 1. Length limits

- `title` → 60 characters. Recognizable, not descriptive.
- `description` → 256 characters. Describes the task; explicitly does not
  substitute for a plan.

Both enforced at `task add` and `task update`, with an error naming the right
destination rather than only the limit — a description that is too long should
say "put this in the plan (`--plan-file`)", not "256 char limit exceeded". The
error message is the teaching surface; treat its wording as part of the work.

Existing rows are grandfathered (see §4). Do not truncate anything.

## 2. A plan is required to spawn

Filing stays cheap: a task may be filed with no plan. What changes is that a
task with no `plan` content row is **not spawnable**.

This is deliberately not "a plan is required to file". Requiring one at filing
forces an agent to plan work it is not doing in an area it may not know, which
produces plan-shaped compliance text — the exact noise the epic exists to
remove. Filing without a plan is legitimate; the task simply parks.

The gate fires in `task claim` and `task spawn`. Its refusal is also the
migration trigger for grandfathered tasks (§4), so the message must offer the
path forward, not just the refusal.

## 3. Open questions park a task

**Any `task_questions` row in state `open`** makes a task non-spawnable, exactly
as a missing plan does. (The table is specified in Child 1 §4.)

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

Surfacing: `task show` renders open questions prominently, `task next` excludes
parked tasks with a visible reason, and parked-on-questions tasks are the
natural feed for E-1976's attention surface and E-1996's approval UI.

Question *volume* is a quality problem, not a schema problem — thirty trivial
questions is a bad read-through, addressed by the challenge facet and by
prompting, not by a blocking/clarifying column.

## 4. Retarget the re-approval reset from description to plan

Today a material `--description` edit resets a pre-work task to `untriaged`,
because the description is the spec. Under this epic it is not — the plan is.

- A material **plan** change on a pre-work task drops approval.
- A **description** change becomes cheap and cosmetic; no reset.

This is a retarget of existing machinery, not new machinery. It also gives
Child 4 its re-approval hook for free: when dispute resolution updates a plan,
approval drops and the user gets to review, which is the required behaviour.

`--keep-status` keeps suppressing all inferences, unchanged.

## 5. Grandfathering

The new rules apply to **new tasks eagerly** and to the ~485 existing pre-work
tasks **lazily**, triggered by the spawn gate in §2.

Retroactive application was considered and rejected: evaluating 485 tasks
produces hundreds of simultaneous open questions, which is precisely the time
sink the epic is meant to eliminate. Phase cannot be the discriminator either —
`now` currently holds 214 tasks and no longer discriminates.

So the first attempt to work a grandfathered task finds no plan, and that
refusal fires the read-through (Child 3). Evaluation happens at the moment of
need, on fresh context, one task at a time. No migration pass, no flood, no
stale primed sessions.

## 6. Docs

Rewrite the field model in `docs/guide/tasks.md` and CLAUDE.md: what each slot
is for, the description-describes / plan-specifies boundary, the spawn gate, and
open questions as a first-class filed state rather than a failure.

## Acceptance

- Title over 60 or description over 256 is refused at add and update, with an
  error naming the correct slot.
- A task with no `plan` row cannot be claimed or spawned; the refusal states how
  to proceed.
- A task with any `open` row in `task_questions` cannot be claimed or spawned,
  is visibly parked in `task show`, and is excluded from `task next` with a
  reason.
- A material plan edit on a pre-work task drops approval; a description edit
  does not.
- `--keep-status` still suppresses every inference.
- Existing over-length tasks are untouched and still readable.
- Guide and CLAUDE.md describe the new model with no stale references to
  `text`, the 100/1000 limits, or description-as-spec.
- `go build/vet/test ./...` and `just test` pass.

## Open questions

**Should the length limits be hard refusals or warnings?** Refusal is
unbeatable and teaches immediately, but it will interrupt an agent mid-flow and
some legitimate titles genuinely need 65 characters. A warning preserves flow
but is ignorable, and ignorable limits are how we got here. Recommend hard
refusal; flagging because it is the kind of friction that is annoying in a way
worth knowing about before it ships.
