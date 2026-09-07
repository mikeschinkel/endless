## What was observed

`just land` on E-2107 failed with:

    Error: rebase conflict while rebasing your branch onto main.

    Your commit that failed to replay: d3cafc4b E-2107: attribute the blast
    radius per commit, not per range

    Conflicting files:
      (none reported)

Nothing conflicted. `(none reported)` is `_rebase_conflict_message` finding
zero unmerged paths — which is the signal that this was not a content conflict
at all. Nothing on the base had touched the one file that commit changes since
the branch forked, and re-running `git rebase main` by hand afterwards replayed
all four commits and exited 0.

The report was therefore wrong in three ways at once, and unactionable in a
fourth: it asserted a conflict that did not happen, named a commit as the cause
on evidence that does not establish one, listed no files, and then offered
recovery candidates for a cause it never determined.

## The defect

`worktree_cmd.py` Step 4:

    try:
        _git_run(["rebase", base_branch], cwd=worktree_path)
    except subprocess.CalledProcessError:
        msg = _rebase_conflict_message(...)

Any non-zero exit from `git rebase` is treated as a conflict. `git rebase` also
exits non-zero when a commit becomes empty, when the working tree was dirtied
between picks, when it refuses to overwrite local changes, and on plain
operational failures. None of those produce unmerged paths, and none of them
are fixed by the recoveries the message offers.

Git says which one it was, on stderr, and `CalledProcessError.stderr` is
already holding it. It is discarded. That is the whole bug: the one fact that
would have identified the failure is captured and thrown away, and a guess is
printed in its place.

The Step 3.7 orphan-replay handler at the other call site has the same shape
and needs the same fix.

## A second, smaller wrongness

`REBASE_HEAD` is not cleared when a rebase ends, so it can survive from an
EARLIER rebase in the same worktree. `_rebase_conflict_message` reads it
unconditionally, so "Your commit that failed to replay: <x>" can name a commit
from a previous, unrelated operation. It should only be reported when the
current rebase is actually in progress — a `rebase-merge`/`rebase-apply`
directory under the worktree's git dir — and omitted otherwise.

## Fix direction

- Claim a conflict only when `git diff --diff-filter=U` is non-empty. Otherwise
  report a rebase FAILURE, verbatim from git's stderr, and offer no recovery
  candidates — there is nothing to judge between when the cause is stated.
- Include git's stderr in both cases. In the conflict case it is context; in the
  non-conflict case it is the entire answer.
- Gate the REBASE_HEAD line on a rebase actually being in progress.
- E-1417 built this message to stop `land` misattributing every conflict to an
  auto-file. This is the same failure one layer up: the earlier fix reasoned
  correctly about WHICH conflict and never checked WHETHER one occurred.

## Note on reproducing

The specific trigger was not captured, because the stderr that would have named
it was discarded — which is the defect, not a gap in the report. Do not spend
effort reconstructing this one instance; make the failure self-describing and
the next occurrence will say what it is.
