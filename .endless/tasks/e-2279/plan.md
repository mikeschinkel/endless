## Request

Study what Claude agents actually put into Endless task content fields, and
propose a structured content model, with plan items as the first concrete
piece.

## Questions to answer

1. What distinct uses appear inside `plan` across filed tasks? For example:
   deliverables, approach, files and functions touched, verification steps,
   rationale, rejected alternatives, constraints, open questions, and grown
   scope recorded after the fact. Count them roughly and give examples by
   task ID.
2. The same question, more briefly, for `context`, `analysis`, `outcome` and
   `notes`. Which uses are misplaced in their current field, and which uses
   recur across fields?
3. Should plan items be rows with stable IDs instead of text? If so, what does
   an item hold? Consider at least: a statement of the deliverable, whether it
   must hold or the approach is flexible, the item's verification, and its
   order or parent.
4. How would a per-item reconciliation reference plan items, so that a gate
   can check deterministically that every item has an entry (done as planned,
   done differently, not done, blocked) and record extra work outside the
   plan?
5. How do itemized plans behave across the lifecycle: material-edit
   detection on a `ready` task, approval, recording grown scope during the
   work, and editing a shipped task's plan?
6. What happens to existing plans: migrate them, grandfather them, or require
   itemization only on new or re-submitted plans?

## Inputs

- Task content in the main database (`endless sql --db main`) and the
  per-task mirror files under `.endless/tasks/`.
- The field table and field-boundary rules in `endless guide tasks`.
- The settled requirements in the brainstorm this cleans up after.

## Deliverable

An outcome holding the catalogue of uses with examples, a proposed content
model with plan items specified concretely enough to plan the
reconciliation-gate task from it, and the open choices left for Mike.
