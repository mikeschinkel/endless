# (c) Confirm-and-prune the obsolete `.endless/sessions/` companion files — PLAN

Parent epic: the `project` command group. Independent of (a)/(b), but should land
BEFORE (b) freezes the canonical `.gitignore` set (so the obsolete entry is never
scaffolded into new projects).

## Finding (this session)
`.endless/sessions/` is obsolete: nothing WRITES it (the only remaining
companion-file writer is `worktree.json`); the files are 2+ months stale; the sole
code reference is a STALE docstring on `session_show_resolve` in
`src/endless/session_cmd.py` — the data actually comes from the DB `sessions` table
+ tmux, not the files dir.

## Scope
1. Confirm no live writer/reader remains (re-grep for `.endless/sessions`,
   `_live_sessions`, `_resolve_companion` sourcing). If a live reader IS found,
   STOP and re-scope — the finding was wrong.
2. Remove the dead read path and fix/remove the stale docstring.
3. Delete the `.endless/sessions/` dir from the project (and note it's safe to
   `rm -rf` in already-set-up repos).
4. Drop the `.endless/sessions/` entry from this repo's `.gitignore`, and ensure the
   `project init` canonical set (child b) does NOT include it.

## Deliverable — verify script
Generate `tests/tasks/e-<id>-verify.sh` (shape per `tests/tasks/e-1577-verify.sh`):
`grep` shows no live `.endless/sessions` read/write path in `src/endless/`; `endless
session show`/`list` still work (data from DB + tmux); `.endless/sessions/` is not
in `.gitignore`. On completion, set `unverified` and hand off exactly:
`esu && ./tests/tasks/e-<id>-verify.sh`.
