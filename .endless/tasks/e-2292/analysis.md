## The cause

One field, two readings. `base_branch` names a BRANCH to every reader; the
recreate path supplies a COMMIT. Nothing in between objects.

Fix the writer: `_bootstrap_task_worktree` must record the base BRANCH. The
recreate path still needs its ref/sha to check out at, but that is a different
fact from "the branch this work lands into", and conflating them is the bug.
Whether that means resolving the ref to its branch, or carrying the two as
separate fields, is the design question — a recreated worktree genuinely has both
a starting point and a landing target.

## Defence in depth, in the same change

`land` should refuse a `base_branch` that does not name a branch rather than
computing a range against it. A base that is not a branch is not a slow land, it
is an unanswerable question, and the current behaviour is to replay thousands of
commits and conflict on history that is already present on both sides.

`git rev-parse --verify --quiet refs/heads/<base>` is the whole check. Cheap, and
it turns a four-thousand-commit rebase into a one-line refusal naming the field.

## The misleading recovery advice

Worth a look while here, though it may be its own task: the classifier concluded
"every conflicting file is endless-managed, restoring them from <base> is safe".
That was true of the files and false of the situation. A recovery that restores
auto-files from a base that is itself the problem cannot help, and following it
loses ledger history. At minimum the advice should not fire when the base fails
the branch check above.

## Verification

- A worktree recreated through `session resume --reopen` / `--review` records a
  branch in `base_branch`, not a SHA.
- `land` on a worktree whose companion names a non-branch refuses, names the
  field, and rebases nothing.
- A land from a correctly-recreated worktree computes `<base>..HEAD` as the
  branch's own commits.
