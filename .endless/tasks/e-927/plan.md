# Plan: Add `decision` command and exclude decisions from task lists (E-927)

## Context

Decisions are stored as tasks with `type='decision'` and `status='confirmed'`. They appear in `task list`, `task recent`, etc., creating confusion because "confirmed" means "this is a fact" for decisions but "work was verified" for tasks. We need to separate them at the CLI level now; the backing store stays the same until E-841 (ADR system) lands.

## Part 1: Exclude type='decision' from all task list queries

Add `AND {alias}.type != 'decision'` to the WHERE clause in these 5 functions in `src/endless/task_cmd.py`:

| Function | Line | Alias | WHERE base |
|---|---|---|---|
| `show_plan()` | ~566 | `pi` | `WHERE pi.project_id = ?` |
| `next_tasks()` | ~717 | `t` | `WHERE t.status NOT IN (...)` |
| `active_tasks()` | ~843 | `t` | `WHERE t.status IN (...)` |
| `recent_tasks()` | ~932 | `t` | `WHERE 1=1` |
| `search_tasks()` | ~1762 | `t` | `WHERE t.project_id = ?` |

Also exclude from Go web queries in `internal/web/queries.go`:
- `GetProjectTasks()` (~line 160)
- Dashboard/detail plan count subqueries (~lines 33, 128)

## Part 2: Add `endless decision` command group

Add to `src/endless/cli.py`:

```
@main.group("decision")
def decision_cmd():
    """Manage project decisions."""
```

### Subcommand: `decision list`

Options: `--project`, `--llm`, `--json`

Queries tasks WHERE `type = 'decision'`, ordered by created_at DESC. Reuses existing `_render_flat_table` or a simpler decision-specific renderer showing: ID, Title, Created.

### Subcommand: `decision add`

Shorthand for `task add --type decision --status confirmed`. Arguments: title. Options: `--description`.

### Subcommand: `decision show`

Delegates to `detail_item()` (same as `task show`). Accepts one or more decision IDs.

## Files to modify

- `src/endless/task_cmd.py` -- add decision exclusion to 5 WHERE clauses, add `list_decisions()` function
- `src/endless/cli.py` -- add `decision` group with `list`, `add`, `show` subcommands
- `internal/web/queries.go` -- add decision exclusion to task queries

## Verification

1. `endless task list` -- should not show any type=decision tasks
2. `endless task recent` -- same
3. `endless decision list` -- should show only decisions
4. `endless decision add "Test decision"` -- should create with type=decision, status=confirmed
5. `endless decision show E-926` -- should display decision detail
