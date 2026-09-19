# Per-task post-land script mechanism — PLAN

The *solution* half of "self-completing land": a task commits a script that
`endless worktree land` runs after the merge, so a task whose change needs a post-land
step on main lands cleanly with **no human involvement**.

## Motivation
Surfaced by the `.endless/sessions/` prune. Removing a `.gitignore` entry leaves the
formerly-ignored files on disk as **untracked** — and a commit only moves *tracked*
content, so no land can delete them (deleting untracked files is a filesystem op). The
cleanup fell to a manual `rm` after land that lived only as a prose handoff note and was
easy to miss. Boundary: **tracked files are self-correcting on land** (a tracked deletion
propagates on merge), so the only post-land filesystem problem space is *untracked* files.

## 1. Script location — tracked, in `.endless/hooks/`
- Path: `.endless/hooks/post-land/e-NNN.sh` (lowercase task id, matching the
  `.endless/worktrees/e-NNN` convention). It is **version-controlled**: authored in the
  worktree, committed on the task branch, and lands to main with the task.
- Why tracked (not git-local): the script is a *definition of an action* (like a
  migration), not per-environment state. A tracked copy in main is inert — land runs it
  once at land time and nothing re-execs it — so the marker's "must not travel" constraint
  does not apply. Tracking buys posterity, review in the task's diff, and permanent
  recoverability; the only cost is accumulation of already-run scripts, the accepted
  migrations-dir tradeoff.
- Lives under the existing `.endless/hooks/` convention (home of the project-wide
  `post-worktree-create.sh`). Per-task scripts nest in a `post-land/` subdir so the hooks
  root stays free for project-wide lifecycle hooks (including a possible future
  `post-worktree-land.sh`). Discovery is a single resolved path, so a later consolidation
  to `.endless/tasks/e-NNN/…` is a trivial repoint.

## 2. Land integration
- New `_run_post_land_script(worktree_path, main_root, canonical, merge_sha, base_branch)`
  in `worktree_cmd.py`, modeled on `_run_post_worktree_create_hook`.
- **Call site:** in `land_worktree`, on the success path **after** `_record_landing(...)`
  and the "Landed" echo, **before** the best-effort worktree sweep. Not reached by the
  `record_only` early return.
- **Discovery:** after the ff-merge the script is in main's tree; resolve
  `<main_root>/.endless/hooks/post-land/<task>.sh`. Absent → silent no-op. Present but not
  executable → loud warning (path + `chmod +x` + re-run command), then skip.
- **Contract** (mirrors the create-hook: exec-directly / idempotent / loud):
  - Exec the script directly via its own shebang (no shell-string interpolation).
  - **cwd** = `main_root` (reconciles main's working tree).
  - **argv**: the land process (Python `endless worktree land`) execs `<task>.sh
    <main_root>`, so inside the script `$1` = main checkout root. (The script's own argv,
    not an `endless`/`endless-go` invocation.)
  - **env**: inherits parent + `ENDLESS_TASK_ID`, `ENDLESS_MERGE_SHA`,
    `ENDLESS_WORKTREE_PATH`, `ENDLESS_BASE_BRANCH`.
  - **Failure = non-fatal + loud** (forced: the Step-5 ff-merge already advanced main). On
    non-zero exit, loud error naming script path, exit code, cwd, and the exact re-run
    command; **do not fail the land**. The contract REQUIRES the script be idempotent /
    re-runnable.
  - Output streamed live.

## 3. Agent-protocol phrasing (guide)
Short section in the using-endless-in-sessions guide (no internal task IDs): if a task's
change needs a one-time action on **main** after it lands — most often removing the
untracked files a newly-un-ignored path leaves behind, or a fixup git won't perform on
merge — commit an **idempotent** `.endless/hooks/post-land/e-<task>.sh` (chmod +x) on your
branch. `endless worktree land` runs it after the merge with cwd = the main checkout and
`ENDLESS_TASK_ID` / `ENDLESS_MERGE_SHA` set.

## 4. Verification
Deliverable: `tests/tasks/e-1799-verify.sh`, following the established per-task verify-script
convention (shape per `tests/tasks/e-1577-verify.sh`: `set -u`; the
`section`/`report_pass`/`report_fail`/`summary` scaffold; `cd` to `git rev-parse
--show-toplevel`; CLI wrapped as `endless() { uv run endless "$@" --db sandbox; }`; exit 0
on all-pass, 1 on any failure). Fold this task's checks into that one suite as fail-fast;
hand off exactly `esu && ./tests/tasks/e-1799-verify.sh`. Cases (each drives a real land in
the sandbox):
1. Commit a `.endless/hooks/post-land/e-<t>.sh` that writes a sentinel into main → land →
   assert the sentinel is present with cwd=main and correct `ENDLESS_TASK_ID` /
   `ENDLESS_MERGE_SHA`.
2. Non-executable script → land → loud warning, land still succeeds, script skipped.
3. Absent script → land → clean no-op (no error).
4. Script exits 1 → land → still reports landed; loud post-land error with re-run command.
Plus unit coverage in a new `test_worktree_land_post_land_script.py` (assert call site,
cwd, argv, env, non-fatal handling).
(When E-1596's suite system lands it may absorb this into the formal harness; until then
the e-1577-modeled per-task script is the convention.)

## Independence
Self-contained: needs neither the land-flow guard (safeguard) nor the durable marker.
"If a committed post-land script for the task exists, run it after the merge" is the whole
behavior. The `endless worktree git-path` command is **not** needed here (tracked path) —
it lives with the durable-marker task, which requires git-local storage.
