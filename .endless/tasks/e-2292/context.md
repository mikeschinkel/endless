`recreate_dropped_worktree` rebuilds a dropped worktree at a `base` its own
docstring describes as "a git ref/sha" — in practice the task's last landed
commit. It hands that value to `_bootstrap_task_worktree`, which writes it
verbatim into the companion's `base_branch` field. So a recreated worktree can
carry a 40-character commit SHA in a field every reader treats as a branch name.

Observed 2026-10-01 on E-2128, whose worktree was recreated automatically:

    "base_branch": "83ffde6b6e48c86e7b13fbb940ce8214e1fb761e"

`worktree land` then computed `<base>..HEAD` against that commit instead of
`main`. Because the commit was two weeks old, the range was every commit main had
gained since, beginning with a ledger auto-commit — which the orphan detector
correctly classified as an amendable orphan and walked forward from. The land
then ran `rebase --onto <base> <last-orphan>` and tried to replay 4719 commits,
conflicting on a ledger shard that was already an ancestor of both main and the
branch.

The land's own conflict classifier made it worse by being right about the wrong
thing: every conflicting file WAS endless-managed, so it advised restoring them
from the stale base. Following that advice would have discarded two weeks of main's
ledger entries in the worktree and left the cause untouched, so the next land
would fail identically.

Setting the field to `main` reduced `<base>..HEAD` to the branch's own two
commits and the orphan drop to a no-op.
