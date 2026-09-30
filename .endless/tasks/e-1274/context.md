endless task spawn creates a new tmux window and binds the spawned session to the task before the session itself does anything.

Result: a half-claimed task (session bound, no worktree, status still ready/needs_plan).

Observed on E-987 spawn 2026-05-11; the misleading error compounded with E-1216 (plan-file-in-main blocking claim) made the failure mode hard to read.
