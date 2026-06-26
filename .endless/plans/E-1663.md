# Require --outcome by task type (research/brainstorm), not by completed status

## Why

`_require_outcome_for_completed` (E-1240) couples the outcome requirement to the
`completed` *status*. That was a proxy: `completed` is mostly reachable only by
deliverable-shaped types, so "completed ⇒ outcome" stood in for "deliverable-type
⇒ outcome." Express the real intent directly: the outcome IS the deliverable for
`research` and `brainstorm` tasks, so require it when completing one — keyed on
`type`, not status.

## Decided behavior (ED to be recorded)

- **Require `--outcome` when completing a `research` or `brainstorm` task.** These
  are the information-deliverable types; the outcome is the deliverable.
- **Epics never require an outcome.** They self-complete via child-status
  derivation (no interactive `task complete` moment where an outcome could be
  supplied/required).
- **`decline` keeps its own reason requirement** — unchanged. That rule is
  ED-1022/ED-1026 (`_require_outcome_for_declined`), independent of this change.
- **`confirm` / `assume` never require an outcome** — already true in code; the
  guide table wrongly claimed otherwise (fixed by this task).
- **Other types reaching `completed`** (e.g. a `task` with a completable verb)
  are NOT forced to carry an outcome. Such cases are rare and the completable-verb
  gate (E-1240/ED-1511) already governs who may reach `completed`; this task does
  not change that gate.

## Implementation

1. `src/endless/task_cmd.py` — replace the status-based
   `_require_outcome_for_completed(status, outcome)` with a type-based check
   (require outcome when `status == "completed"` AND `task_type in ("research",
   "brainstorm")`). Thread `task_type` into the call sites that currently invoke
   it (the `complete`/`update --status completed` paths). Leave
   `_require_outcome_for_declined` untouched.
2. `docs/guide/tasks.md` — fix the fields table `outcome` row: it currently says
   "Required for `confirm`/`assume`/`decline`." Correct to: required on `decline`
   (the reason; ED-1022) and when completing a `research`/`brainstorm` task. Touch
   the research/brainstorm field-model notes if they restate the old coupling.
3. Tests — research/brainstorm `completed` without outcome refused; with outcome
   succeeds; `task`/`epic` `completed` NOT forced to carry outcome; `decline`
   still requires a reason; `confirm`/`assume` never require outcome.

## Verify

A per-task script (`tests/tasks/e-NNNN-verify.sh`) exercising the matrix above
against the sandbox DB, plus the existing Python suite stays green.
