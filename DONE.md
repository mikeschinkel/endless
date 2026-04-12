# Endless — Completed Work

## CLI Commands
- `register` — register a directory as a project (interactive + `--infer`)
- `unregister` — set status=unregistered in config, remove from DB, preserve config on disk. Reconcile won't re-register.
- `list` — table of registered projects (`--status`, `--group`)
- `status` — detailed project view with label, description, language, documents, notes. Auto-detect from cwd.
- `set` — update project fields (`name.field=value` from anywhere, `field=value` in project dir)
- `rename` — change project name (with `--path` disambiguation)
- `discover` — find/register unregistered projects (tiered checkbox picker, ownership filtering, group marking, ignore)
- `scan` — document indexing (implemented but deferred for redesign)
- `docs` — list tracked documents per project (`--type` filter)
- `notes` — show pending notes per project (`--all` for resolved)
- `note add` — add manual note (`--project` or detect from cwd)
- `note resolve` — mark a note as resolved
- `setup prompt-hook` — install ZSH prompt hook for activity monitoring
- `setup remove-prompt-hook` — remove the hook

## Monitors (Go)
- `endless-hook prompt` — Go binary triggered by ZSH precmd hook
  - Records project activity with tmux session/window/pane UUIDs
  - Detects file changes (new/modified/deleted) via mtime comparison
  - Configurable per-project watch patterns (include/exclude in `.endless/config.json`)
  - Throttling (skip if <5 seconds since last run)
  - Schema: `activity` and `file_changes` tables

## Core Infrastructure
- SQLite schema (projects, documents, notes, deps, sessions, activity, file_changes, etc.)
- Global config (`~/.config/endless/config.json` — roots, ignore list, ownership)
- Project config (`.endless/config.json` — name, label, status, watch patterns, etc.)
- Group marking (`.endless/config.json` with `type: group`)
- Reconciliation — filesystem is source of truth, DB auto-heals on list/status/scan. Skips unregistered projects.
- Ownership filtering — `git remote` URL matched against `ownership.mine` glob patterns
- Auto-ignore of non-mine repos during discover
- Document type system — unified in `doc_types.py` with stems, dirs, and pattern matching
- Bash-to-Python rewrite (bash code preserved in `bash/` directory)

## Testing
- 46 automated tests covering config, db, register, reconcile, ownership, docs, notes, CLI smoke tests
- All tests isolated via tmp_path fixtures
