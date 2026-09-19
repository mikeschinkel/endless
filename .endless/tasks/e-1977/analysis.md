Surfaced in E-1815 while designing auto-spawn's backpressure cap. The cap was
originally going to count total unverified tasks; measurement showed 57 in
`endless` / 75 machine-wide with 71 of the 75 aged 30-90 days, which is sediment
rather than a queue and would have made the cap unclearable. Auto-spawn's cap
was rescoped to count only auto-spawned unverified, so this backlog no longer
blocks that feature — the two are orthogonal.

Highest-leverage item surfaced in that conversation by ED-1550's own arithmetic:
closing is named there as the only fast lever on the file-to-close ratio, and
most of these probably shipped months ago and simply never got confirmed.

Why the sediment exists (diagnosed in E-1815): it accumulated before the session
monitor existed, when there was no good way to keep track of unverified work,
and two tmux crashes took unverified tasks out of sight.

That diagnosis narrows this task. The PREVENTION half is already in flight — the
session monitor exists, and E-1976 (project monitor) surfaces unverified first by
construction, so new sediment should stop forming. What is unbuilt is the DRAIN.
Scoped here to draining; widen it if that turns out wrong.

Existing coverage checked before filing: E-1881 (continuous auto-merge job for
endless-managed worktree commits) and E-1882 (`worktree reconcile` for stale
endless-managed commits) cover GIT currency — whether a worktree's commits can be
brought current. Neither covers TASK-STATUS currency, which is what this is.
Whatever mechanism this brainstorm lands on should check whether it can lean on
those two for the git-side signal rather than re-deriving it.
