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
