## The pass is not just misfiled — it is silent

Observed on E-2128 while trying to land it. The sandbox database held no row for
the task at all, not a stale one, so `record_pass` never reached the misfiling
this task was filed about. It took its early return for an empty read instead,
which prints nothing:

    states = verify_states(task_num)
    if not states:
        return

So the whole sequence looks like success to whoever ran it. The suite passes, the
runner prints its green summary, a CTRF report is written at the branch's current
sha, and the command exits 0 — while nothing was recorded anywhere and no line
says so. The only way to find out is to try to land and read a refusal that names
a status the verify appeared to have changed.

That early return is load-bearing elsewhere, so it should stay a return. What it
must not stay is quiet: an empty read means the pinned database does not know the
task, which is a fact about the invocation and worth the same one-line
explanation the status-gate branch beside it already prints.

Two things to settle, and they are separable:

1. The routing — `just verify` pinning `--db sandbox` (`justfile`, the verify
   recipe), which is what this task is named for.

2. The silence — an empty read returning without a word. Worth fixing even after
   the routing is right, because any other caller that pins a database the task
   is absent from lands in the same place, and the next person debugging it pays
   the same cost. Verified today: both status-gate branches below it explain
   themselves, and this one does not.

A fix for 1 alone would close the symptom on this machine and leave the trap set.

## Decisions taken when this was planned

**The pass reaches main by removing the pin, not by replacing it.** The verify
recipe drops its database argument rather than naming main, because
`run_verify` already calls `config.default_db_to_main`, which pins main inside a
self-dev worktree precisely so a forced-main operation does not fall foul of the
no-database gate. Naming main at the call site would restate that and give a
change to where a pass is recorded two places to be made.

**The invocation is refused before the suite runs, not after.** The alternative
was to let the suite run and then explain that nothing could be recorded. Both
tell the truth, but only one of them tells it while the invocation can still be
corrected, and the other spends a full suite run first.

The refusal is conditioned on a read that SUCCEEDS and finds nothing. A read
that cannot answer at all lets the verify proceed: a suite is a proof about a
checkout and keeps its value even when the bookkeeping cannot be filed, so a
database this cannot reach must not become a reason to refuse to verify. The
cost of that choice is accepted — an unreachable database still loses the
recording, exactly as today.

**Raising from the empty read loses nothing.** It was assumed that a raise there
would cost the passing run its report. It does not: `run_verify` writes the
report through `_record_passing_run` before it calls `record_pass`, so by then
the report is already on disk. A raise changes one thing, the command's exit
code — which is the whole of what was being traded. With the pre-flight in
place that branch is a backstop, so reaching it means something upstream is
wrong and a non-zero exit is the honest answer.

**Deleting the recipe is a separate task.** Everything the recipe does beyond the
pin is now done by the command: it derives the task id from the session and then
the cwd, and resolves the task's worktree to run in. So the recipe is redundant
and should go — but only once `endless verify` exists, because `just verify` is
shorter than `endless task verify` and is typed constantly. Splitting it keeps
the fix that is blocking sessions out from behind a new command's verify suite.
