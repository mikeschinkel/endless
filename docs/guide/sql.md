# SQL Queries

`endless sql` runs SQL against the Endless DB. Read-only by default. Use it for investigation — counts, filters, joins that the regular CLI doesn't expose.

## Basic usage

```bash
endless sql "SELECT COUNT(*) FROM tasks WHERE status='verify'"
endless sql "SELECT id, title FROM tasks WHERE phase='now' AND status='ready' LIMIT 10"
endless sql "SELECT * FROM tasks WHERE id = 1248" --tsv
```

## Why this exists

Agents instinctively reach for `sqlite3` against speculative paths under `.endless/`. SQLite silently *creates* the file at any path you give it, which leaves ghost DB files. `endless sql` resolves the actual DB path internally so you can't miss.

Per memory: **never invoke `sqlite3` with a guessed path.** Use `endless sql`.

## Flags

```bash
endless sql "SELECT ..." --tsv          # tab-separated output, no header — pipeable
endless sql "DELETE FROM ..." --write   # required for mutations (UPDATE/INSERT/DELETE/PRAGMA)
```

By default, only `SELECT`, `WITH`, and `EXPLAIN` are accepted. `--write` enables mutations — use sparingly and confirm with your user before running destructive statements.

## Schema discovery

```bash
endless sql "SELECT name FROM sqlite_master WHERE type='table'"
endless sql "SELECT name FROM pragma_table_info('tasks') ORDER BY cid" --tsv
```

## Examples

```bash
# How many tasks are in verify status across the current project?
endless sql "SELECT COUNT(*) FROM tasks WHERE status='verify' AND project_id = (SELECT id FROM projects WHERE name='endless')"

# Recently updated tasks
endless sql "SELECT id, title, status FROM tasks ORDER BY updated_at DESC LIMIT 20" --tsv

# Tasks that block E-1248
endless sql "SELECT t.id, t.title, t.status FROM task_deps d JOIN tasks t ON t.id = d.depends_on_id WHERE d.task_id = 1248 AND d.dep_type='blocks'"
```

## When to use `task list --json` instead

For straightforward task queries, prefer `endless task list --json --status <...> --phase <...> --llm` — it's purpose-built for agent consumption and respects business logic (e.g., the difference between `verify` and `confirmed` for blocking).

Reach for `sql` when:

- You need a count, sum, or aggregate the CLI doesn't expose.
- You're investigating a bug in Endless itself.
- You need a join across tasks, decisions, sessions, or events.

## See also

- `endless guide layout` — where the DB file lives
