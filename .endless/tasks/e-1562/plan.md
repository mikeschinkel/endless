# Plan — justification becomes typed content, not a heading in prose

## Why

`--type research` requires `--justification`, and E-1544 stored it by appending
a `## Justification` section to the task's notes. A required, validated field
living inside free-form prose has one detection method — parse the heading —
and that produced a refusal nobody intended: `--justification` is rejected when
a section already exists, so a task that has EVER been research can never be
retyped to research again, and an existing justification can never be reworded.

E-2165 was filed to add `--clear justification` as a stopgap and is DECLINED:
its premise was that typed storage meant a schema change. `task_content` shipped
with E-1531, so a new content kind costs an INSERT (E-1992), and this task is
no larger than the stopgap would have been.

29 tasks carry a `## Justification` section today.

## Decisions (owner, 2026-10-01)

  1. `Justification` is declared FIRST in the content order, before `Context`.
     Only research and brainstorm tasks carry one (decision 2), and those
     deliver findings rather than being implemented — so nothing displaces
     `Context` from its post-description position on an implementation task,
     which is the adjacency E-1993 placed it for.
  2. Research and brainstorm ONLY. The word exists because of the research
     gate; a todo carrying one would mean something narrower with nothing to
     say which. Refuse it elsewhere.
  3. Migrate by BACKFILL EVENTS, and build the backfill to be reused —
     E-1992's remaining slots will need the same move. Not a throwaway script.
  4. A notes row left empty by the move is DELETED. `task_content` holds no
     empty rows.
  5. `--justification` stays its own flag. Its name already matches the content
     name, which is E-1992's rule.

## 1. Declare the content kind

Add `Justification` to `internal/taskcontent`: the constant first in the
declaration order, its slug `justification`, its label `Justification`, and its
place in `all`.

Everything downstream derives from that one declaration and needs no edit:
`taskContentFields` builds from `All()`, so the executor's live apply and the
projector's rebuild both accept the payload key — the single map that exists
because those two paths once disagreed about `notes`, across exactly the
justifications this task is moving. `endless-go task-content names` reports it,
and the mirror file stem follows the slug.

## 2. Write it as content

`task add` and `task update` send `justification` as a payload key instead of
composing notes. Delete `_compose_justification_notes` and
`_JUSTIFICATION_HEADING_RE`; the collision refusal goes with them, because
replacing a content row is an ordinary write.

The research gate reads the row rather than searching prose. Enforce decision 2
where the gate already lives: `--justification` on a task that is not (or is not
becoming) research or brainstorm is refused, naming the two types.

`--clear justification` then falls out of the existing clearable-content
machinery with no special case, which is what retires E-2165.

## 3. Migrate the 29

A reusable content-move backfill: for each task whose notes hold a
`## Justification` section, emit one `task.fields_updated` carrying the
justification content and the remaining notes — the section removed, the rest
of the prose intact — with `--ts` set to the original event's timestamp so the
history reads as when the justification was actually written (E-1719's
record-only path). Where the section was all the notes held, the notes content
is empty, which the executor already treats as a delete.

Ledger-native on purpose: a rebuild replays the move like any other change
rather than re-running a script, and the two write paths stay in agreement
because both route through `taskContentFields`.

Built as a named, repeatable operation rather than a one-off, because the
remaining E-1992 slots will each need the same shape.

## 4. Not in scope

  - Generating the per-content CLI flags from the vocabulary. `endless-go
    task-content names` already exposes the list and `statuses.py` is the
    precedent for a Python client of a Go registry, so `--context/--analysis/
    --plan/--outcome/--reason/--notes/--justification`, their `-file` twins and
    the `--clear` choices could be derived rather than hand-written. Worth its
    own task; it touches every content flag and is not needed to move
    justification.
  - The other E-1992 slots. This task adds the backfill they will reuse.

## 5. Verification

`.endless/tasks/e-1562/verify.sh`, sourcing `_harness.sh`:

  1. Fail-fast gate: `internal/taskcontent`, `internal/events` (both write
     paths through `taskContentFields`), and the Python task tests.
  2. THE DEFECT, as a regression: against a seeded database, create a research
     task with a justification, retype it to todo, retype it back to research
     with a different justification. It must succeed and the stored content must
     be the second text. That round trip is the whole reason this task exists.
  3. Rewording in place: `--justification` twice on one research task replaces
     the content and does not refuse.
  4. Type gate: `--justification` on a todo is refused, naming research and
     brainstorm.
  5. No prose left: no `## Justification` heading remains in any notes content,
     and neither `_compose_justification_notes` nor the heading regex survives
     in the tree.
  6. Notes preserved and pruned: a task whose notes held a justification plus
     other prose keeps the other prose; a task whose notes held only the
     justification has no notes row at all.
  7. Rebuild agreement: rebuilding the projection from the ledger reproduces
     the same justification and notes content the live apply produced — the
     property the one-map design exists to guarantee.
  8. Display order: `task show` renders Justification before Context.
