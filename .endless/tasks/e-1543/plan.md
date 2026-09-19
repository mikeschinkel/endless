# E-1543 — Implement promotion validation for task update --type epic

## 1. Scope

Two write-path validations on `endless task update`:

A. **Promotion to epic** (`task update <id> --type epic`): if the current status is `verify`, block with an explanatory error. All other current statuses pass through. Children stay attached.

B. **Status updates on existing epics** (`task update <epic-id> --status <value>`): accept only sticky-override values (`revisit`, `declined`, `obsolete`, `blocked`). Reject other values with a message explaining derivation handles the rest (per E-1541).

Out of scope: epic demotion (epic → other type). The validator does not handle that direction; current behavior is preserved.

## 2. Where validation lives

Both checks live in the Python CLI write-path, in `update_plan()` in `src/endless/task_cmd.py`, alongside the existing `_require_outcome_for_declined`, `_require_completable_verb_for_completed`, and `_research_gate_check` guards. Per ED-1506, the Go event executor stays mechanical (translates fields to columns, calls `tasktype.Parse`, writes). Promotion semantics are CLI/policy concerns above the event boundary.

Both validations run before the event is emitted. Failure raises `click.ClickException`, which aborts the entire update call (no partial writes). This matches the established behavior for other gates.

## 3. Validation A: promote to epic

New helper `_promotion_gate_check(current_type: str, new_type: str, current_status: str, item_id: int)`:

- Pass when `new_type == current_type` (no actual promotion).
- Pass when `new_type != "epic"` AND `current_type != "epic"` (neither side touches epic).
- When `new_type == "epic"` (task/bug/research → epic):
  - Block if `current_status == "verify"`.
  - All other statuses pass through.

Call site: inside the existing `if task_type is not None:` block in `update_plan` (currently around line 2884), after slug-validity check, before `fields["type"] = task_type`. Current type/status come from `row[0]["type"]` and `row[0]["status"]`.

Error message:

```
Cannot promote E-{id} to type=epic while status is 'verify'. The 'verify'
status is task-only (epics never go through user-testable verification).
Set a different status first, then re-run the promotion:

    endless task update E-{id} --status ready
    endless task update E-{id} --type epic
```

## 4. Validation B: epic status constraint

New helper `_epic_status_constraint_check(current_type: str, new_status: str, item_id: int)`:

- Pass when `current_type != "epic"` (the rule applies only to epics).
- Pass when `new_status` is in the sticky-override set: `{revisit, declined, obsolete, blocked}`.
- Block otherwise (including `needs_plan`, `ready`, `in_progress`, `verify`, `confirmed`, `assumed`, `completed`).

Call site: inside the existing `if status is not None:` block in `update_plan`, after status-validity check, before `fields["status"] = status`.

Error message:

```
Cannot manually set status on epic E-{id} to '{new_status}'. Epic status is
auto-derived from children (per the §4 derivation rules). Manual status
sets are accepted ONLY for sticky-override values:

    revisit, declined, obsolete, blocked

These override derivation until cleared. To clear an override, set a new
sticky-override value or wait for child-state changes to retrigger
derivation (which happens automatically once the override is removed).
```

The promote case (A) and the status case (B) can both fire on the same update call (e.g. `task update <id> --type epic --status ready`). Both must pass.

## 5. Type-filter grep audit

E-1537 §7 calls for a quick grep audit of code that filters on `type=task` assumptively. Run during this task: grep `src/endless/` and `internal/` for `type = 'task'`, `type_id = 1`, `WHERE type`. Note findings in this task's outcome; the fix typically belongs in a sibling task.

## 6. Tests

New file `tests/test_promotion_gate.py`, modeled on `tests/test_research_gate.py`.

Helper-level unit tests for `_promotion_gate_check`:
- pass: task → epic when status is `ready`, `in_progress`, `needs_plan`, `blocked`, `revisit`
- raise: task → epic when status is `verify`
- pass: task → task (no-op)
- pass: epic → epic (no-op)
- pass: task → research / task → bug (validator doesn't fire for non-epic promotion)

Helper-level unit tests for `_epic_status_constraint_check`:
- pass: any status update when type is task/bug/research (non-epic)
- pass: epic status set to `revisit`, `declined`, `obsolete`, `blocked`
- raise: epic status set to `needs_plan`, `ready`, `in_progress`, `verify`, `confirmed`, `assumed`, `completed`

End-to-end via `update_plan` (using the existing `seeded_project_at_cwd` fixture):
- promote a `ready` task to epic → row's type is now epic
- attempt to promote a `verify` task to epic → ClickException; row unchanged
- promote a task with existing children → children remain attached
- set an epic's status to `revisit` → row's status is now revisit
- attempt to set an epic's status to `ready` → ClickException; row unchanged
- combined update (`--type epic --status ready`) on a task → fails on the status check; nothing lands

## 7. Implementation sequence

1. Add `_promotion_gate_check` and `_epic_status_constraint_check` helpers in `task_cmd.py` near `_research_gate_check` (around line 1488).
2. Wire `_promotion_gate_check` into `update_plan` around line 2884.
3. Wire `_epic_status_constraint_check` into `update_plan` at the status-handling site.
4. Run the §5 grep audit; record findings in the task outcome.
5. Add `tests/test_promotion_gate.py`.
6. Run `pytest tests/test_promotion_gate.py tests/test_task_update_type_analysis.py tests/test_research_gate.py` together to confirm no regressions in adjacent gates.

No Go changes. No schema changes. No event payload changes.

## 8. Critical files

- `src/endless/task_cmd.py`
- `src/endless/cli.py`
- `tests/test_promotion_gate.py` (new)
- `tests/test_research_gate.py` (reference pattern)
- `internal/events/executor.go` (read-only confirmation that Go side stays mechanical)
