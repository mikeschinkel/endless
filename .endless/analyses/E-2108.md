## Scope

Implements ED-1587. Three pieces, in order:

1. **Construction.** `src/endless/worktree_cmd.py:1546` and `:1653` build
   `f"task/{task_id}-{slug}"`. Both become `f"task/{task_id}"`. Whatever
   computes the slug for this purpose goes with them if nothing else uses it.

2. **Migration of the ~139 existing branches.** They are local-only — no
   remote, no PRs, no CI — so `git branch -m task/<id>-<slug> task/<id>` is
   cheap and safe. Each has a live worktree checked out on it, so the rename
   must go through git (which updates the worktree's HEAD), never by editing
   refs. Decide and state whether this is a one-shot `db apply-change`-style
   step or a resolver that accepts both forms during a transition; a one-shot
   rename is simpler and this project has no other clones to coordinate with.
   Sessions are live in some of these worktrees, so the rename must not run
   under them unannounced — gate it the way the reaper gates on
   `WorktreeInUse`, or require it to be run deliberately.

3. **Retire `task_landings.branch`.** Once the name is derivable the column is
   redundant. Two consequences worth doing in the same pass, because they are
   the reason the decision was made:
   - E-1719's record-only landing recorded no branch, which is why
     `maybeReapWorktree` scans it as `sql.NullString`. That case disappears.
   - E-2087 added a git fallback (`git symbolic-ref --short HEAD`) so a
     worktree with no landing row could still have its branch deleted at reap
     time, plus a carve-out that deliberately does NOT apply the fallback to a
     NULL-branch landing row. Both go away: the reaper derives `task/<id>`
     from the task id it already has.

   The column also feeds `internal/monitor/session_notices.go` and the
   `task landed` surface; check every reader before dropping it, and prefer
   deriving over reading rather than leaving a second source of truth.

## Also update

- `ED-1167` — supersede it via `endless decision supersede`, not by editing it.
- The guide's worktree/branch prose, wherever it shows a slug branch name.
- Any test fixture that hardcodes a `task/<id>-<slug>` branch.

## Out of scope

A configurable branch pattern. ED-1587 rejects it explicitly and says why.
