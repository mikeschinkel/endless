## Repro from E-1450 land (2026-05-23): plan-file/DB divergence caused non-deterministic land-guard dirt

While landing E-1450, `just land` blocked at the worktree dirty-guard
(`worktree_cmd.py` `_guard_dirty_worktree`) reporting `.endless/plans/E-1450.md`
as "uncommitted user changes" — even though the file was committed on the task
branch and `git status --porcelain` in the worktree was empty (verified directly,
file tracked at HEAD with no diff).

State at the time of the failing land:
- `.endless/plans/E-1450.md` was committed on the branch, matched HEAD, clean.
- DB `tasks.text` for E-1450 DIVERGED from the committed file: the file had a
  post-approval "landing note" added by an editor, while the DB still held the
  pre-note version attached earlier via `task update --text`.

The land succeeded immediately after re-syncing the DB to the committed file
(`endless task update E-1450 --text <committed-file>`). Nothing else changed —
the worktree file was already committed/clean before and after; only the DB
content changed.

Mechanism NOT identified. Ruled out: no git hooks (core.hooksPath default,
.git/hooks empty); no land step writes the plan file (`land_worktree` never calls
the materializer at `task_cmd.py:479`); orphan-drop was a no-op (companion
base_branch="main", neither branch commit amendable); the endless-hook only reads
`~/.claude/plans`, never writes `.endless/plans`. That the exact path turning a
DB/file divergence into transient worktree dirt could not be found is itself
consistent with this task's premise that plan-storage is non-deterministic.

Design takeaway: the two-copy model (plan in DB `tasks.text` AND as a committed
`.endless/plans/E-NNN.md`) can drift, and the drift surfaces non-deterministically
at land time. Deterministic rules should make one copy the single source of truth
(or have land explicitly detect/reconcile divergence) rather than leaving
plan-storage state ambiguous. Related contested decision: 71 plan files are
currently tracked on main vs. E-1202's proposal to forbid them in main.
