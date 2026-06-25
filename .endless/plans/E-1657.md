# Add `brainstorm` task type

## Why

`research` and `brainstorm` produce nearly identical artifacts (information, not
code) but run on opposite information flow:

- **`research`** — information flows *inward*: the agent goes to external sources,
  gathers evidence, synthesizes it out to the requester. Agent-led.
- **`brainstorm`** — information flows *outward from the requester*: their head is
  the primary source. The agent interviews, challenges, reflects, captures.
  Requester-led, interview-mode.

The split is "direction of information flow." They are siblings in a "thinking
task" family, not duplicates. The type label's value is the **behavioral
contract** it attaches, not its field shape.

## Field model (mirrors the research-task model, ED-1507)

| Field     | On a brainstorm task holds…                                                                 |
|-----------|---------------------------------------------------------------------------------------------|
| `text`    | The **seed / framing** — the spark, written up front. "I want to explore X; here's what's nagging me." |
| `outcome` | The **synthesis** of what we landed on, PLUS `cleans_up` / `implements` links to the decision / research / do-tasks it spawned. |

Terminal shape: synthesis + spawned follow-ups. A brainstorm is a *precursor* —
it routinely resolves into an `endless decision add` and/or new tasks, linked
from the outcome. Only terminal status is `completed` (like research).

## Gating

**None.** `--type brainstorm` and go. Explicitly NOT mirroring research's
justification gate (ED-1504) — frictionless ideation is the goal.

## The behavioral contract (the heart — ships in `endless guide tasks`)

A cold session reading a brainstorm task must obey:

> Brainstorm task → open by interviewing the requester. Ask questions first. Do
> NOT research autonomously or jump to a plan. Surface tensions, offer options,
> challenge the thinking, capture ideas as they emerge. Conclude with a synthesis
> in `outcome` and spawn the follow-up tasks/decisions it produced.

NOTE (PRODUCT): the shipped contract names "the requester" / "the user", never a
specific person. No internal task IDs in the shipped guide text.

## Implementation surface

1. **DB** — add `brainstorm` to the task-types values table (FK-seeded,
   idempotent insert in `schema.sql`). No CHECK constraint.
2. **Go enum** — add `Brainstorm` (int-backed explicit constant, `String()` →
   `"brainstorm"`, `Parse…` constructor). DB column is integer FK.
3. **CLI** — add `brainstorm` to the `--type` choice list on `task add` /
   `task update`; ungated (skip the research justification gate path).
4. **Guide** — add a brainstorm field-model + behavioral-contract block in
   `endless guide tasks`, beside the research-task field model. Generalize the
   cross-reference table's Topics row.
5. **Handoff** — branch the spawn handoff template on type so a *spawned*
   brainstorm opens in interview mode ("Begin by asking the requester…") instead
   of "read the plan and implement." IN SCOPE for v1: a brainstorm as the first
   child of an epic, spawned as its own session, is a primary use case.
6. **Decision record** — paired ED capturing the rationale (this task is
   `--decides` by that decision).

## Relations

- **relates_to E-1653** (stranded status slugs / adds `unapproved`): both add a
  value to a closed vocabulary (E-1653 a status, E-1657 a type) AND ship a `.sql`
  change-file under `internal/schema/changes/`, editing the same two sites
  (`cli.py` Choice + `task_cmd.py` valid tuple). Landing order needs coordination
  (no line conflict — status set vs type set — but adjacent). E-1653 also delivers
  the `unapproved` status this task's own ledger row wanted.
- **relates_to E-1652** (migrations + rebuild-db upcasting research): E-1657's
  `e-1657-add-brainstorm-task-type.sql` is a clean ADDITIVE values-migration —
  the good case that needs no ledger rewrite, in contrast to E-1252's rename. A
  concrete artifact for that research to examine when weighing extend-vs-replace
  of the change-file approach.
- **E-1654 deliberately NOT linked**: its rebuild-db --confirm guardrail never
  touches this change (applied via `apply-change` at land; `task_types` seeded
  from `schema.sql`, not events). A link would be "same subsystem" noise.

## Verification

- `endless task add "Brainstorm X" --type brainstorm` succeeds with no
  justification.
- `endless guide tasks` renders the brainstorm contract + field model.
- A spawned brainstorm session's handoff opens in interview mode.
- Existing research tasks unaffected; research gate still fires for `--type research`.
