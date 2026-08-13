# Analysis

State of the code as this task begins, gathered by the E-1941 session on
2026-08-12. Prior knowledge only — conclusions belong in the outcome.

## How the gate is implemented today

- `_maybe_auto_sandbox_bind` (`src/endless/worktree_cmd.py`) returns early when
  `config.project_is_self_dev(project_root)` is false, so `task claim` and
  `task spawn` provision a sandbox only for Endless itself.
- `config.py` resolves the sandbox root to `None` for non-self-dev projects
  (`return root if project_is_self_dev(root) else None`).
- `.endless/config.json` carries the flag; only Endless sets it.

A downstream project's worktree therefore gets no sandbox at all — not merely no
sandbox *DB*.

## The two concerns the single flag governs

1. **Per-worktree scratch/config storage.** A worktree needs somewhere to keep
   the configuration a task requires in order to be *verified*. Essentially no
   real software project has zero configuration files, so this is universal.
2. **DB routing.** Endless-specific, and correctly opt-in: a downstream
   project's worktree tasks must land in the real ledger as real audit data, not
   in a throwaway copy. Sandboxing *that* for downstream users would be wrong.

One flag governs both today. Separating them is what this task decides.

## The fault surface is not project-scoped

Relevant because E-1957 (`blocked_by` this task) needs somewhere to persist git
conflict evidence when a land fails, and the existing fault subsystem is the
nearest existing home:

- the `errors` table (`internal/schema/schema.sql:882`) has no `project_id`
  column — columns are id, code, severity, source, fingerprint, summary,
  occurrences, first_seen_at, last_seen_at, cleared_at, cleared_by
- the JSONL `Detail` struct (`internal/faults/detaillog.go`) has no project
  field
- `monitor.ConfigDir()` (`internal/monitor/db.go:61`) returns `dbContextDir`
  when set, else `$XDG_CONFIG_HOME/endless`. A land pins `--db main`, which sets
  `dbContextDir` to the real config dir, so the detail log is one shared
  location for every project: `~/.config/endless/log/`

`internal/faults` is otherwise well shaped for this: `Fault.Code` + `Fault.Source`
discriminate the producer, and `Fault.Detail` / `Fault.Fields` carry long
structured capture to the JSONL log by design, explicitly never the DB.

The missing `errors.project_id` is tracked separately as its own defect; it is
not this task's to fix, and this task does not depend on it.

## Adjacent gap, known but out of scope

Neither the `errors` table nor the JSONL detail log has any retention sweep.
`appendDetail` does `MkdirAll` plus append with no rotation; only manual
`endless errors clear` exists. Deliberately left for when it is needed.
