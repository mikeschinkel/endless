# (a) Create the `project` command group + move commands + hidden stubs — PLAN

Parent epic: the `project` command group. Read it for full context/decisions.

## Scope
- Add a `project` Click group to `src/endless/cli.py`.
- Move these top-level commands under it (behavior unchanged): `register`,
  `unregister`, `rename`, `set`, `status`, `list`, `purge`, `discover`, `scan`.
  → `endless project register`, `endless project list`, etc.
- For each moved command, leave a **hidden hard-error stub** at the old top-level
  name (decided): `hidden=True`, and invoking it exits non-zero with a message
  naming the new command (e.g. `"'endless register' moved — use 'endless project
  register'"`). Not a working alias; not shown in `--help`.
- Update `endless guide`, prompt/handoff templates, and any docs that call the old
  top-level names to the `project` form (coordinate with the docs sweep habit).

## Notes
- `project init` (the unified register+file-setup command) is child (b), not here;
  here `register` just moves as-is to `project register`.
- Decide at implementation whether `setup`/`docs`/`notes` also belong under
  `project` (epic lists them as adjacent — default: leave them out of this move).

## Deliverable — verify script
Generate `tests/tasks/e-<id>-verify.sh` (shape per `tests/tasks/e-1577-verify.sh`):
`endless project <cmd> --help` works for each moved command; each old top-level
name exits non-zero with a message naming the new command; old names do NOT appear
in `endless --help`. On completion, set `unverified` and hand off exactly:
`esu && ./tests/tasks/e-<id>-verify.sh`.
