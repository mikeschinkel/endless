# Where Things Live

A quick map of the files and directories Endless manages.

## Per-project (under `<project>/.endless/`)

| Path                                                         | Purpose                                                                                  |
|--------------------------------------------------------------|------------------------------------------------------------------------------------------|
| `.endless/config.json`                                       | Project-local Endless config (tracking mode, custom settings).                            |
| `.endless/db-ledger/db-entries-<node>-<seq>.jsonl`           | Write-ahead log of all DB writes (event-sourced). **Committed to git** (E-1198).          |
| `.endless/plans/E-NNNN.md`                                   | Plan files attached to tasks. Committed to git; required before `task claim`.            |
| `.endless/plans/snapshots/<timestamp>-<hash>.md`             | Plan snapshots written by the PostToolUse hook. Committed to git (per-project, E-1092).  |
| `.endless/worktrees/<slug>/`                                 | Per-task git worktrees. **Gitignored** (E-975).                                          |
| `.endless/worktree.json`                                     | Current session's task → worktree mapping (companion file).                              |
| `verbs.json`                                                 | Registered action verbs at the project root. Auto-committed on `worktree land` (E-1141). |

## Critical: `.endless/db-ledger/` is committed to git

The `.endless/db-ledger/` directory holds the database write-ahead record — JSONL ledger entries that the SQLite DB is rebuilt from on every clone. **Must be committed to git.** Clone-completeness means task state travels with the repo. Do not add `.endless/` or `.endless/db-ledger/` to `.gitignore`.

This directory was renamed from `.endless/events/` in E-1197/E-1198 because the old name biased readers (human and LLM) to treat the files as discardable logs — which they are not. They are durable database state. Existing installs auto-migrate on first event write; the legacy directory is removed once all entries are line-count-verified at the new location.

If you see "Endless: auto-record session activity" commits in `git log`, those are the ledger / verbs.json / snapshot auto-commits. **Never discard those commits.** They are durable state.

## Global (per-machine)

| Path                                          | Purpose                                                                  |
|-----------------------------------------------|--------------------------------------------------------------------------|
| `~/.config/endless/endless.db`                | SQLite DB (rebuildable projection of all project ledgers).               |
| `~/.config/endless/config.json`               | Per-machine Endless config (node_id, defaults).                          |
| `/usr/local/bin/endless`                      | Python CLI entry point (installed via `uv tool install -e .`).           |
| `/usr/local/bin/endless-hook`                 | Claude Code hook binary (Go).                                            |
| `/usr/local/bin/endless-event`                | Event-write binary (Go).                                                 |
| `/usr/local/bin/endless-tmux`                 | tmux-integration binary (Go).                                            |

## Web dashboard

```bash
endless serve       # starts http://localhost:8484
```

Useful for browsing the task tree visually when the CLI gets unwieldy.

## See also

- `endless guide worktree` — how `.endless/worktrees/` works
- `endless guide snapshots` — how `.endless/plans/snapshots/` is populated
- `endless guide projects` — per-project registration
