# `worktree check` fails in self-dev worktrees via an unnecessary DB round-trip

## Symptom

Run from a shell inside an endless **self-dev** worktree:

```
$ endless worktree check
resolve project for E-NNNN: sql: no rows in result set   (exit 2)
```

Surfaced while landing E-1759, which rewrote the handoff templates to tell every
spawned session to run `endless worktree check` in its final message. In a
self-dev worktree that instruction now yields this error instead of a clean
(silent) result. Consumer (non-self-dev) projects are unaffected — their task row
and the worktree check share one DB, which is why E-1758's own verify passed.

## Root cause

`endless worktree check` (`src/endless/worktree_cmd.py` `check_worktree()`) already
resolves the worktree from **cwd** (`worktree_root_for_cwd()`) and parses the task
number straight out of the path. It then discards the path and passes only
`--task-id` to the Go core (`session-query worktree-anomalies`), which round-trips
through the DB purely to rediscover what Python already had:

- `SELECT project_id FROM tasks WHERE id=?` (`internal/monitor/worktree_anomalies.go`
  `WorktreeAnomaliesForTask`) → the project,
- → the worktree path (`WorktreePathForTask`) and repo root (`ProjectPath`).

That DB lookup is the ONLY reason a DB is touched. The actual inspection
(`worktreeAnomaliesAt`) is DB-free: it reads git state from the worktree path, and
the expected branch comes from the `.endless/worktree.json` **companion file**, not
the DB. In self-dev the DB context routes to the per-worktree sandbox
(`config.go_db_context_args()`), the task row isn't there, and it errors — even
though everything needed (path from cwd, branch from the companion, root from git)
was available without any DB.

## Why not just `--db main` in the handoff

Rejected on two counts:

1. **Wrong layer** — it papers a prompt over a command that shouldn't read the DB
   for this at all.
2. **Wrong home** — the handoff templates are shared across self-dev and consumer
   projects; `--db main` is a Python-only, self-dev-only concept, so it can't go in
   a universal template without breaking/confusing consumer renders.

## Fix direction

Drop the DB dependency for this command. Have `worktree check` hand the Go core the
worktree path it already resolved (a `--worktree-path` / `--project-root` form of
`session-query worktree-anomalies`), or make the Go side fall back to path-based
resolution when the task isn't in the active DB. `worktreeAnomaliesAt` is already
DB-free and the branch expectation already comes from the companion file, so this is
a plumbing change, not new detection logic. Result: identical behavior in self-dev
and consumer, with no flag and no handoff-template change.

## Verify

```
esu && ./tests/tasks/e-1766-verify.sh
```

Deliver `tests/tasks/e-1766-verify.sh` (self-contained, same shape as the
e-1577/e-1758 prototypes). It builds a self-dev-routed worktree fixture (task in
the "main" DB, cwd routed to a sandbox) and asserts `endless worktree check`
returns clean/silent (exit 0) when the tree is clean and flags real anomalies
(exit 1) — i.e. it no longer errors on the sandbox-DB lookup, matching consumer
behavior. Fold the Go core regression in as check 0. No manual steps.
