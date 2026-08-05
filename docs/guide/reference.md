# Reference: Projects, SQL, Snapshots, Tmux, Layout

Lookup material — not part of the day-to-day session loop, but useful when you need it.

---

## Projects

Endless tracks multiple projects from a single global DB. Every project is a registered directory.

```bash
endless project list                           # all registered projects
endless project status                         # detailed status of current project
endless project status --project <name>        # of a named project
```

### Registering a project

Most repos are already registered. If you `cd` into one and Endless errors with "no project for this cwd", register it:

```bash
endless project register                       # register current directory
endless project register --name <custom-name>  # with an explicit name
```

After registering, `endless project list` should show the project and `endless project status` should work from inside the repo.

---

## SQL queries

`endless sql` runs SQL against the Endless DB. Read-only by default.

```bash
endless sql "SELECT COUNT(*) FROM tasks WHERE status='unverified'"
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

For straightforward task queries, prefer `endless task list --json --status <...> --llm` — it's purpose-built for agent consumption and respects business logic (e.g., `unverified` vs `confirmed` for blocking). Reach for `sql` when you need a count/aggregate or a join the CLI doesn't expose.

---

## Tmux integration

Endless ships a tmux integration that puts the active task ID, project, and status on a second status row, plus popup menus for common actions.

```bash
endless tmux apply              # configure the running tmux server (ephemeral)
endless tmux status-line        # the runtime printer tmux calls per refresh
```

After `apply`, your tmux session shows a second status row like `[E-NNNN] · <project> · underway`.

**This feature is evolving fast.** Menus, hotkeys, layout, permanent install, and theming are all in flight. **Don't memorize the UI** — run `endless tmux --help` for the currently shipping verbs and trust the help over any doc more than a few days old.

`endless tmux apply` configures the running tmux server *ephemerally* — the configuration survives until tmux restarts.

---

## Background jobs

Endless runs background work through a **fire-once runner**: when invoked it executes any *due* jobs and exits. There is no daemon and no timer inside the runner — repetition lives in whatever triggers it. Today the session monitor fires it on each refresh; a long-running daemon will fire it on events later.

```bash
endless jobs list             # registered jobs: cadence, next due, runs, failures
endless jobs run              # fire the runner once, now
endless jobs retry <name>     # clear a job's backoff and make it due immediately
```

Many session monitors may fire the runner at the same moment. Exactly one of them runs any given due job, arbitrated by a compare-and-set lease in the database. That lease is time-boxed rather than held as a lock, so a process that dies mid-run needs no cleanup — its claim simply lapses. The trade-off is that a job which *outruns* its lease can be re-entered, so **every job must be idempotent**.

A job that fails is rescheduled rather than abandoned. Jobs that declare a backoff cap push their next attempt exponentially further out as failures accumulate, so a persistently broken job decays toward that cap instead of retrying at full rate forever. Fixing the cause does not mean waiting the backoff out — `endless jobs retry <name>` makes it due again immediately.

`endless jobs list` printing `no jobs registered` is the expected state today: the runner deliberately ships knowing nothing job-specific.

---

## Errors

Anything that goes wrong in the background is recorded as a classified, clearable **error** with a stable `ERR-NNNN` code. `session status` and `session monitor` show a trailing badge whenever uncleared errors exist — the most severe wins, and `error` outranks `warning`.

```bash
endless errors show                    # open errors
endless errors show --all              # include cleared ones (history)
endless errors show --id N --detail    # one error, with every occurrence's full capture
endless errors clear                   # mark every open error cleared
endless errors codes                   # the documented catalog
```

Two behaviors are worth knowing before you rely on this:

- **Clearing never deletes, and nothing clears itself.** An error stays on the badge until a human dismisses it, even if the job has since been succeeding — an intermittent fault that healed itself out of view would never get fixed. A recurrence after clearing opens a *new* error beside the cleared one, so a problem that came back is visibly distinct from one that never left.
- **Clearing is not retrying.** `errors clear` means "I have seen this"; making a backed-off job due again is `jobs retry`. They are separate verbs so that tidying your error list cannot silently re-arm a job that is still broken.

The database stores only the index — code, source, summary, counts. Each occurrence's full capture goes to `<config-dir>/log/errors.jsonl` and comes back through `--detail`, so the table stays bounded by how many *distinct* things are wrong rather than how often they happen. That file is machine-local: it is not the db-ledger, it is never replayed into the database, and errors emit no ledger events.

Every code's cause and remedy is documented in `docs/errors.md`.

---

## File layout

A quick map of the files and directories Endless manages.

### Per-project (under `<project>/.endless/`)

| Path                                                         | Purpose                                                                                  |
|--------------------------------------------------------------|------------------------------------------------------------------------------------------|
| `.endless/config.json`                                       | Project-local Endless config (tracking mode, custom settings).                            |
| `.endless/db-ledger/db-entries-<node>-<seq>.jsonl`           | Write-ahead log of all DB writes. **Committed to git** — the SQLite DB is rebuilt from these on every clone.  |
| `<worktree>/.endless/plans/E-NNNN.md`                        | Plan file attached to a task. Lives in the task's worktree (written by `task update --text`); rides into main via `worktree land`. The DB's `tasks.text` column is source of truth — the file is the on-disk mirror. |
| `.endless/worktrees/e-<id>/`                                 | The per-task git worktree — one canonical name per task; named alternates are not recognized. Need a second checkout? File a child task. **Gitignored.** |
| `.endless/worktree.json`                                     | Current session's task → worktree mapping (companion file).                              |
| `verbs.jsonl`                                                | Registered action verbs at the project root, one JSON object per line. Auto-committed to main as a global-config artifact. A legacy `verbs.json` array, if present, is migrated to JSONL on first load. |

### Critical: `.endless/db-ledger/` is committed to git

The `.endless/db-ledger/` directory holds the database write-ahead record — JSONL ledger entries that the SQLite DB is rebuilt from on every clone. **Must be committed to git.** Clone-completeness means task state travels with the repo. Do not add `.endless/` or `.endless/db-ledger/` to `.gitignore`.

This directory was previously named `.endless/events/`. The old name biased readers (human and LLM) to treat the files as discardable logs — which they are not. Existing installs auto-migrate.

If you see "Endless: auto-record session activity" commits in `git log`, those are the ledger / verbs.jsonl auto-commits. **Never discard those commits.** They are durable state.

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
