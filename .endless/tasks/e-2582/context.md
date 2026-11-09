Observed while landing a task whose verify the user had just passed. The
first `endless worktree land` rebased the task branch onto main, then
refused because main's working tree held an uncommitted file: a CTRF
report that another task's verify run had written onto main at that
moment and had not yet committed. By the time the user looked, that run
had committed its report and main was clean.

The second `endless worktree land` then refused for a different reason:
the branch had changed since the verify passed. Nothing had been edited.
The first attempt's rebase had rewritten the branch's commits, so its
head SHA no longer matched the one the passing verify recorded, and the
task fell from unlanded back to unverified. The code tree at the new head
was byte-identical to the verified one. The user had to rerun the verify
and land again.

Two things combine here. The land rebases before it checks main's working
tree, so a refusal that has nothing to do with the branch still leaves
the branch moved. And the verified-SHA check compares commit ids, while
the reconciliation gate already treats a clean rebase as not a change.
Either ordering the checks so nothing is rewritten before every refusal
has had its chance, or comparing the verified tree rather than the
commit, would have let the second attempt land.

The transient file was another session's verify report landing on main,
which is routine when several sessions run at once, so the refusal is
expected to recur.
