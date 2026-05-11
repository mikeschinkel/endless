# Plan Snapshots

When Claude writes a plan file (typically via `/plan` mode), the PostToolUse hook captures a snapshot at `.endless/plans/snapshots/<timestamp>-<hash>.md`. Snapshots are read-only history — useful for seeing how a plan evolved.

## Inspecting

```bash
endless snapshots list                          # all snapshots in the current project
endless snapshots show <name>                   # metadata + content of one snapshot
```

The snapshot name is the filename stem (the `<timestamp>-<hash>` portion).

## When snapshots are useful

- Reviewing how a plan was iterated before becoming the final `--text` attached to a task.
- Auditing what was promised at plan time versus what shipped.
- Recovering content if a plan file is accidentally overwritten.

## When snapshots are not the source of truth

- The canonical plan for a task is its `text` field, populated via `task update <id> --text <plan-file>`.
- Snapshots are historical mirrors — write-once (E-1017).

## Layout

Snapshots live at `<project>/.endless/plans/snapshots/`. They are committed to git per project policy (E-1092 — per-project commit choice on first snapshot). Don't delete snapshot files manually; they're append-only history.

## See also

- `endless guide layout` — where the rest of `.endless/` content lives
- `endless guide tasks` — `task update --text` attaches a plan to a task
