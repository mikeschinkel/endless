# E-1111: Fix project-config writes from worktrees writing to wrong .endless/config.json

## Problem

`endless phrase add verb` (and any other command writing project-layer
config) writes to whatever `.endless/config.json` is highest up from cwd.
From inside a git worktree, that's the worktree's own copy — not the main
checkout's. Machine-layer write is correct; project-layer write goes to the
wrong file. Symptoms:

- Project-layer config silently diverges between main checkout and worktrees.
- Entries are lost when the worktree is removed (`git worktree remove`).
- Discovered while cleaning up after E-1088: `endless phrase add verb 'consider'`
  and `'re-emit'` had been run from the e-1088 worktree, leaving those verbs
  only in the worktree's project config. Caught by hand before the worktree
  was dropped; a `--force` removal would have lost them silently.

## Root cause

`matchers.project_config_path()` walks up from cwd to find the nearest
`.endless/config.json`. Worktrees are full filesystem checkouts, so they
have their own `.endless/` directory by default. The walk-up finds the
worktree-local copy first.

## Fix

Use `git rev-parse --git-common-dir` to locate the main checkout's `.git`
directory; its parent is the main checkout's working dir. Look for
`.endless/config.json` there.

Why this works:
- From the main checkout, `--git-common-dir` returns `.git` (relative);
  parent = cwd. Behavior unchanged.
- From a worktree, `--git-common-dir` returns the absolute path to the
  main checkout's `.git`; parent = main checkout. Project config resolves
  there, not in the worktree.

If cwd is not in any git repo (or git is unavailable), fall back to the
prior walk-up logic to preserve behavior for non-git projects.

## Scope

- One function in `src/endless/matchers.py` (`project_config_path()`)
  plus a small helper `_git_main_worktree_root()`.
- All callers of `project_config_path()` benefit transparently — both reads
  (`load_all_matchers`) and writes (`add_match_value`, `_add_to_file`).
- No other files need to change.

## Migration / cleanup of pre-existing leakage

Out of scope for this task. Any verb / pivot / etc. entries that landed in
worktree-local `.endless/config.json` files before this fix landed are still
covered by the machine-layer write (machine config is shared via
`~/.config/endless/config.json`). On the next `endless phrase add` from the
main checkout, the project layer will be re-anchored cleanly. Manual cleanup
of stale worktree configs is unnecessary — they are dropped along with the
worktree.

## Verification

Tests in `tests/test_project_config_path.py`:

1. From main checkout: returns main's config.
2. From worktree: returns *main's* config (not worktree's local copy).
3. After `add_match_value` from a worktree: verb lands in main's config and
   does not leak into the worktree's local config.
4. Non-git project: walk-up fallback still works.

Tests use real `git init` + `git worktree add` setup in tmp_path because the
function shells out to git; mocking subprocess would not catch path-handling
regressions.
