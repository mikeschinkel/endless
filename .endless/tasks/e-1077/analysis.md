# Concrete call site: the post-land reap sweep

Added 2026-08-21 from ES-1127 (E-1696), which hit this live.

`_reap_stale_worktrees` (src/endless/worktree_cmd.py:947) resolves its binary
with `shutil.which("endless-go")` — one of the "other shutil.which sites" this
task's description names for E-1039's refactor. It is worth calling out
specifically because it is the one where the stale-global choice is GUARANTEED
to be wrong, not merely possibly wrong.

## Why this site is special

The land already pins its DB-touching steps to the worktree binary
(`_resolve_land_endless_go`, E-1664): for a self_dev land, apply-change and
record-landing must use the build whose embedded schema and enums match the
rows they are writing. The reap sweep runs from inside `endless worktree land`
AFTER apply-change, against the same real DB — and was never pinned.

So on any land carrying an enum-seeding migration, the sequence is:

    endless worktree land  → apply migration      (real DB now ahead)
                           → reap sweep           ✗ integrity check fails
    just land wrapper      → main moved → just build
                                                  (window closes)

Observed on the E-1696 land:

    endless-go event: reap-worktrees: db: session_task_relations integrity
    check on ~/.config/endless/endless.db: sessiontaskrelation:
    session_task_relations row id=4 slug="referenced" has no matching enum
    constant

Non-fatal (the call site catches and prints), self-healing (the wrapper's
`just build` closes the window seconds later), and harmless (the sweep is
TTL-based garbage collection with four triggers, so a missed sweep only defers
reaping). But it prints an alarming failure on exactly the lands where a user is
least equipped to dismiss it.

## Why it must not be fixed locally

Bolting an `endless_go_bin` parameter onto `_reap_stale_worktrees` would work —
`endless_go_bin` is already in scope at the call site (computed at
worktree_cmd.py:2241, reap at :2461) and the other three callers
(two post-claim paths in task_cmd.py, plus `endless worktree reap`) legitimately
want the global. But that is a hand-rolled exception at precisely one of the
sites this helper exists to make uniform, and E-1039 would then have to unpick
it rather than replace a plain `shutil.which` call.

## What this implies for the helper's shape

`resolve_endless_bin_dir()` as described detects worktree-local `bin/` and falls
back to the PATH first match. This site needs slightly more than a cwd-derived
answer: during a land the correct binary belongs to the worktree being landed,
which is not necessarily the caller's cwd, and for a NON-self_dev project the
global is correct and no worktree binary exists at all
(`_resolve_land_endless_go` returns None for that case). Either the helper takes
an explicit worktree hint, or the land keeps its own resolver and the helper
covers the cwd-derived majority — worth deciding before E-1039 sweeps the call
sites, since this one cannot be answered from cwd alone.

## Sequencing note

Under E-2020 (E-1944 decision 3) a binary opening a DB ahead of it HALTS, for
any binary, permanently. So this failure is not something to suppress — it is
the designed guard working. The only real fix is never invoking a stale binary
in that window, which is binary selection, which is this task.
