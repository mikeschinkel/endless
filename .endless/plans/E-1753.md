# Introduce a `project` command group — EPIC

## Context

Endless's CLI grew organically. The first commands it needed were project-related
(`register`, `list`, `status`, …) and landed as TOP-LEVEL commands. As the command
count exploded, later families were namespaced under groups (`task`, `decision`,
`epic`, `channel`, `session`, `worktree`, `verb`, `tmux`, …). The original project
commands were never grouped, so they sit inconsistently at the top level. This epic
introduces a `project` group and moves the project-management commands under it, so
the CLI is consistent (`endless project register`, `endless project list`, …).

## Scope — commands to move under `project`

Currently top-level, project-scoped (confirm the exact set with Mike):
- `register`   → `project register` (or fold into `project init` — see design Q)
- `unregister` → `project unregister`
- `rename`     → `project rename`
- `set`        → `project set`
- `status`     → `project status`
- `list`       → `project list`
- `purge`      → `project purge`
- `discover`   → `project discover`
- `scan`       → `project scan`

Adjacent / decide whether they belong under `project`: `setup` (hooks/integration —
more machine than project), `docs`, `notes` (project-scoped content, but their own
families).

## Decision (Mike) — `project init`, unified, `register` as alias

`init` is a familiar entry point across CLIs (`git init`, `npm init`). Two
responsibilities exist today: **DB registration** (insert the project row — today
`register`) and **file setup** (scaffold `.endless/config.json` + `.gitignore`
entries — today partly in `register`; gitignore scaffolding added by the
`/tmp`→`.endless/tmp` task).

DECIDED: unify. ONE idempotent `project init` does BOTH — scaffolds missing files
AND ensures the DB row — because a project is unusable without both, and splitting
them creates a half-set-up trap. `project init` is the primary verb; `project
register` is an alias for the same command. Re-running is always safe (idempotent).

## Back-compat — decided (Mike): hidden hard-error stubs

The old top-level names (`endless register`, `endless list`, …) become **hidden
hard-error stubs**: invoking one exits non-zero with a message naming the new
command (`use 'endless project register'`). They do NOT work as aliases, and they
do NOT appear in `--help` (hidden). This surfaces the move loudly to anyone (agent
or human) still calling the old name, without silently papering over it. The stubs
are a temporary migration aid — a later task removes them entirely.

## Relation to the `/tmp`→`.endless/tmp` task

That task adds `.gitignore` scaffolding to `register` NOW (near-term; it unblocks
the scratch-dir durability work). That scaffolding is the "file setup" half — when
this epic reorganizes `register` → `project init`, the scaffolding moves with it.
Link: this epic relates_to that task.

## Separate investigation (do NOT bake a guess into `init`)

The canonical `.endless/` ignore set (which `project init` will scaffold):
- **Live, keep**: `.endless/worktrees/` and the new `.endless/tmp/`.
- **Live, keep, but distinct by design** — `.endless/worktree.json` (write-once
  worktree IDENTITY: kind/base_branch/branch/created_at) and
  `.endless/worktree.lock` (write-often OWNERSHIP: session_id/pid/pane/claimed_at,
  deleted on release). Separate files because their lifecycles differ — releasing
  the lock is an `rm` that must never risk the identity; keeping them apart is
  correct, not merge-able. Both live inside worktrees (covered by
  `.endless/worktrees/`); the top-level entries are defensive for a root-level
  occurrence (a stale empty root `worktree.lock` exists today).
- **Obsolete — prune** (corroborated): `.endless/sessions/`. Nothing WRITES it
  (the only writer in code is `worktree.json`); its 7 files are all 2+ months
  stale; the lone reference is a stale docstring at
  `session_cmd.py::session_show_resolve` (data actually comes from the DB
  `sessions` table + tmux, not the files dir). Child (c): remove the dead read/
  docstring path, delete the dir, and DROP the `.endless/sessions/` ignore entry —
  do this before `project init` freezes the canonical set.

## Children (filed — decisions locked)

- (a) Create the `project` group + move the 9 commands under it; leave hidden
  hard-error top-level stubs.
- (b) Build the unified `project init` (idempotent files + DB); `register` alias.
  Blocked by (a).
- (c) Confirm-and-prune the obsolete `.endless/sessions/` files. Should land before
  (b) freezes the canonical `.gitignore` set.

Related ADR: ED-1530 (why worktree.json and worktree.lock stay separate).
