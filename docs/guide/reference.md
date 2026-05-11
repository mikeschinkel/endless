# Reference: Projects, SQL, Snapshots, Tmux, Layout

Lookup material — not part of the day-to-day session loop, but useful when you need it.

---

## Projects

Endless tracks multiple projects from a single global DB. Every project is a registered directory.

```bash
endless list                                   # all registered projects
endless status                                 # detailed status of current project
endless status --project <name>                # of a named project
```

### Registering a project

Most repos are already registered. If you `cd` into one and Endless errors with "no project for this cwd", register it:

```bash
endless register                               # register current directory
endless register --name <custom-name>          # with an explicit name
```

After registering, `endless list` should show the project and `endless status` should work from inside the repo.

---

## SQL queries

`endless sql` runs SQL against the Endless DB. Read-only by default.

```bash
endless sql "SELECT COUNT(*) FROM tasks WHERE status='verify'"
endless sql "SELECT id, title FROM tasks WHERE phase='now' AND status='ready' LIMIT 10"
endless sql "SELECT * FROM tasks WHERE id = 1248" --tsv
```

### Flags

```bash
endless sql "SELECT ..." --tsv          # tab-separated, no header — pipeable
endless sql "DELETE FROM ..." --write   # required for mutations (UPDATE/INSERT/DELETE/PRAGMA)
```

By default only `SELECT`, `WITH`, and `EXPLAIN` are accepted. **Confirm with your user before running `--write`.** Don't run destructive statements unilaterally.

### Why this exists

Agents instinctively reach for `sqlite3` against speculative paths under `.endless/`. SQLite silently *creates* the file at any path you give it, leaving ghost DBs. `endless sql` resolves the actual DB path internally. **Never invoke `sqlite3` with a guessed path** — use `endless sql`.

### Schema discovery

```bash
endless sql "SELECT name FROM sqlite_master WHERE type='table'"
endless sql "SELECT name FROM pragma_table_info('tasks') ORDER BY cid" --tsv
```

### When to use `task list --json` instead

For straightforward task queries, prefer `endless task list --json --status <...> --llm` — it's purpose-built for agent consumption and respects business logic (e.g., `verify` vs `confirmed` for blocking). Reach for `sql` when you need a count/aggregate or a join the CLI doesn't expose.

---

## Plan snapshots

When Claude writes a plan file (typically via `/plan` mode), the PostToolUse hook captures a snapshot at `.endless/plans/snapshots/<timestamp>-<hash>.md`. Snapshots are read-only history.

```bash
endless snapshots list                          # all snapshots in the current project
endless snapshots show <name>                   # metadata + content of one
```

The snapshot name is the filename stem (the `<timestamp>-<hash>` portion).

**When useful:** reviewing how a plan was iterated, auditing what was promised vs what shipped, recovering accidentally-overwritten plan content.

**Not the source of truth:** the canonical plan for a task is its `text` field, populated via `task update <id> --text <plan-file>`. Snapshots are write-once history — don't delete them manually.

---

## Tmux integration

Endless ships a tmux integration that puts the active task ID, project, and status on a second status row, plus popup menus for common actions.

```bash
endless tmux apply              # configure the running tmux server (ephemeral)
endless tmux status-line        # the runtime printer tmux calls per refresh
```

After `apply`, your tmux session shows a second status row like `[E-NNNN] · <project> · in_progress`.

**This feature is evolving fast.** Menus, hotkeys, layout, permanent install, and theming are all in flight. **Don't memorize the UI** — run `endless tmux --help` for the currently shipping verbs and trust the help over any doc more than a few days old.

`endless tmux apply` configures the running tmux server *ephemerally* — the configuration survives until tmux restarts.

---

## File layout

A quick map of the files and directories Endless manages.

### Per-project (under `<project>/.endless/`)

| Path                                                         | Purpose                                                                                  |
|--------------------------------------------------------------|------------------------------------------------------------------------------------------|
| `.endless/config.json`                                       | Project-local Endless config (tracking mode, custom settings).                            |
| `.endless/db-ledger/db-entries-<node>-<seq>.jsonl`           | Write-ahead log of all DB writes. **Committed to git** — the SQLite DB is rebuilt from these on every clone.  |
| `.endless/plans/E-NNNN.md`                                   | Plan files attached to tasks. Committed to git; required before `task claim`.            |
| `.endless/plans/snapshots/<timestamp>-<hash>.md`             | Plan snapshots from the PostToolUse hook. Committed to git per project (choice on first snapshot). |
| `.endless/worktrees/e-<id>/`                                 | Primary per-task git worktree. **Gitignored.**                                           |
| `.endless/worktrees/e-<id>-<slug>/`                          | Ad-hoc additional worktree for a task (testing, alternate experiments). **Gitignored.**  |
| `.endless/worktree.json`                                     | Current session's task → worktree mapping (companion file).                              |
| `verbs.json`                                                 | Registered action verbs at the project root. Auto-committed to main as a global-config artifact. |

### Critical: `.endless/db-ledger/` is committed to git

The `.endless/db-ledger/` directory holds the database write-ahead record — JSONL ledger entries that the SQLite DB is rebuilt from on every clone. **Must be committed to git.** Clone-completeness means task state travels with the repo. Do not add `.endless/` or `.endless/db-ledger/` to `.gitignore`.

This directory was previously named `.endless/events/`. The old name biased readers (human and LLM) to treat the files as discardable logs — which they are not. Existing installs auto-migrate.

If you see "Endless: auto-record session activity" commits in `git log`, those are the ledger / verbs.json / snapshot auto-commits. **Never discard those commits.** They are durable state.

### Global (per-machine)

| Path                                          | Purpose                                                                  |
|-----------------------------------------------|--------------------------------------------------------------------------|
| `~/.config/endless/endless.db`                | SQLite DB (rebuildable projection of all project ledgers).               |
| `~/.config/endless/config.json`               | Per-machine Endless config (node_id, defaults).                          |
| `/usr/local/bin/endless`                      | Python CLI entry point (installed via `uv tool install -e .`).           |
| `/usr/local/bin/endless-hook`                 | Claude Code hook binary (Go).                                            |
| `/usr/local/bin/endless-event`                | Event-write binary (Go).                                                 |
| `/usr/local/bin/endless-tmux`                 | tmux-integration binary (Go).                                            |

### Web dashboard

```bash
endless serve       # starts http://localhost:8484
```

Useful for browsing the task tree visually when the CLI gets unwieldy.
