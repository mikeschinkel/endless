# E-1540 — Implement endless epic CLI subcommand with add show list update aliases

## 1. Scope

Add `endless epic` as a Click subcommand group parallel to `endless decision`. Subcommands: `add`, `show`, `list`, `update`. Each is a thin wrapper around the existing `endless task ...` machinery with `type=epic` pinned. `endless epic show` defaults to including `--children` (add `--no-children` for scripting).

Out of scope: promotion validation (E-1543), auto-derivation (E-1541), pause-on-revisit hook (E-1542), guide doc updates (E-1545/E-1546).

## 2. Design

### File layout

New module `src/endless/epic_cmd.py` mirroring `src/endless/decision_cmd.py`. Each handler is a thin wrapper that defers to existing `task_cmd` functions. Module stays small (~80 lines).

### Shared helper extension

`show_plan()` in `src/endless/task_cmd.py` does not currently accept a type filter. Add `type_filter: str | None = None` parameter. When set, the SQL joins `task_types` and filters `tt.slug = ?`. This keeps the listing path single-source. The new parameter is internal — `endless task list` does NOT get a `--type` flag in this task.

### Subcommands

- `epic add` — same option set as `task add` minus `--type`. Body calls a helper that wraps `task_cmd.add_item(..., task_type="epic")` plus the link loop.
- `epic list` — same option set as `task list`. Body calls `show_plan(..., type_filter="epic")`.
- `epic show` — same option set as `task show`, but replace `--children` (default off) with `--no-children` (default off, so children show by default). Body calls `detail_item(...)` with `show_children=not no_children`.
- `epic update` — same option set as `task update` minus `--type`. Body calls a helper that wraps `task_cmd.update_plan(..., task_type="epic")`. The `task_type="epic"` argument means any task being updated through this verb is also promoted to epic; existing tasks can be flipped via `endless epic update E-NNN --status ready`. Promotion validation is added by E-1543 separately.

### Tests

New file `tests/test_epic_cmd.py` mirroring `tests/test_decision_cmd.py`. Cases:

- `epic add` creates a row whose `type_id` resolves to slug `epic`
- `epic list` filters to epic-typed rows only; empty case returns the empty message; `--json` round-trips
- `epic show` includes children by default; `--no-children` hides them
- `epic update` on an existing task-typed row promotes its type to `epic`
- `epic update` passes through non-type fields

No tests for promotion validation (E-1543), auto-derivation (E-1541), or research gate (E-1544) — those tasks own their own test surfaces.

## 3. Implementation steps

1. Add `type_filter: str | None = None` parameter to `show_plan()` in `src/endless/task_cmd.py`. Update the SQL builder to join `task_types tt ON tt.id = pi.type_id` and add `AND tt.slug = ?` when set.
2. Create `src/endless/epic_cmd.py` with the four helper functions: `add_epic`, `list_epics`, `show_epic`, `update_epic`. Each is a thin call into `task_cmd` with `task_type="epic"` pinned where applicable.
3. Wire the Click `epic` group into `src/endless/cli.py` immediately after the `decision` group. Copy decorators from corresponding `task_*` Click commands; strip the `--type` option from `epic add` and `epic update`. Replace `--children` with `--no-children` on `epic show`.
4. Add `tests/test_epic_cmd.py` with the cases above.
5. Smoke test: `endless epic --help`, `endless epic list --project endless` (E-1537 itself should appear once promoted to type=epic), `endless epic show E-1537`.

## 4. Critical files

- `src/endless/cli.py`
- `src/endless/task_cmd.py`
- `src/endless/decision_cmd.py` (reference pattern only)
- `src/endless/epic_cmd.py` (new)
- `tests/test_epic_cmd.py` (new)
