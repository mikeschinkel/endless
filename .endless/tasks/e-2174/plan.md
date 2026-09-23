# Land's rebase must survive a held index lock

## Observed, 2026-09-23

`just land` on E-2137, from a clean worktree with no conflict:

```
Error: rebase failed while rebasing your branch onto main.
This was NOT a content conflict — no files are in conflict...
git said:
  error: Unable to create '.git/worktrees/e-2137/index.lock': File exists.
  Another git process seems to be running in this repository...
  error: could not detach HEAD
```

The lock holder was found still alive at diagnosis time: PID 43112, running
`git -C <worktree> status --porcelain`. It exited on its own moments later, the
lock vanished with it, and the identical land then succeeded with no change to
the branch. Nothing was wrong with the tree, the branch, or the commits.

## Why it recurs, and why it is Endless's own doing

The lock was not taken by the user. `git status --porcelain` against every
worktree is what Endless's own surfaces run constantly:

- the session monitor repaints on a short cycle;
- `worktree-unlanded` runs every minute over every worktree;
- `worktree check`, `worktree sync` and the land's own Step 1 all shell out to
  git against worktrees.

So the probability of a land colliding with Endless scales with the number of
worktrees a project has — which is one per active task, by design. This is not
a rare race on a quiet machine; it is a structural collision between the land
path and the observation path, and it gets worse exactly as a project gets
busier.

PRODUCT: this is not specific to Endless's own repository. Any project with a
handful of active tasks has several worktrees, a monitor polling them, and the
unlanded job sweeping them. The same collision is available to every user, and
the message they get — "could not detach HEAD" — reads like repository damage
rather than a transient lock.

## The gap, precisely

`land_worktree` already runs its whole body inside a retry loop:

    for attempt in range(1, LAND_MAX_RETRIES + 1):   # src/endless/worktree_cmd.py

and `_is_retryable_ff_merge_error` exists for exactly this class of "a
concurrent writer got there first" failure. But:

1. That predicate matches only `uncommitted`, `would be overwritten`,
   `diverging`, `not possible to fast-forward`. Lock contention — `index.lock`,
   `Another git process seems to be running` — matches none of them.
2. It is consulted at ONE call site, the Step 5 ff-merge. Step 4's rebase
   raises `click.ClickException` directly on any `CalledProcessError`, so it
   leaves the retry loop it is already standing inside.

The machinery is all present. The rebase step simply does not reach it.

## Scope

Step 4 is where it was observed, but it is not the only git call in the loop
that can lose this race. Step 3.7's orphan-drop rebase, Step 1's
`_git_status_partition` on main, and the auto-commit of endless-managed files
are all plain `_git_run` calls inside the same loop, each able to fail on a
lock held by a monitor probe. Fix the class, not the one instance.

## Suggested shape

- A `_is_lock_contention(err_text)` predicate matching `index.lock` and
  `Another git process seems to be running`. Keep it SEPARATE from
  `_is_retryable_ff_merge_error`: the two answer different questions, and the
  ff-merge predicate's matches describe a diverged repository rather than a
  busy one.
- At each git call inside the loop that can lose the race, treat contention as
  "retry this attempt" rather than a hard failure — `continue` the existing
  loop, with a short backoff so eight attempts do not all land inside one
  monitor tick.
- Do NOT delete a lock file. The lock is a live process's, not garbage; the
  observed holder was still running when the land failed. Waiting is correct
  and removing is how an index gets corrupted.
- On genuine exhaustion, the existing "failed after N retries" message should
  say the repository was busy, and name lock contention, so the reader is not
  sent hunting for a conflict that does not exist.

Precedent is in the codebase on both sides of this gap: `events.commitPaths`
gained an `index.lock` retry with backoff under E-2137, and `land` has carried
`LAND_MAX_RETRIES` for concurrent writers since E-987.

## Verification

- A land whose Step 4 rebase fails once with `index.lock` and succeeds on the
  next attempt completes, with the branch landed and no operator action.
- A land against a lock that never clears still terminates, within the retry
  cap, with a message naming contention rather than a conflict.
- A rebase failing on a REAL conflict is unaffected: still no retry, still the
  conflict report with its candidates.
- The two predicates stay disjoint — a diverged-branch error is not treated as
  contention, and a lock error is not treated as divergence.
- `just test`, `just test-go`.
