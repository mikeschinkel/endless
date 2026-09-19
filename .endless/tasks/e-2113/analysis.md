## What was observed

2026-09-06 00:20:03 EDT, thirteen seconds after a land: incident 11,
`E-1972: git range-diff failed for its worktree`, detail
`git range-diff: signal: interrupt`. One occurrence.

The probe is healthy. `endless task unsettled E-1972` answers cleanly on
demand (unlanded, 7 commits), and a genuinely broken probe would not have
one occurrence — `session monitor` re-probes every row every two seconds
and the fault dedupes on (worktree, probe), so a real failure drives the
count into the thousands within a minute. The child git was KILLED, not
failed.

## Mechanism

`monitor.runGit` (internal/monitor/reap_worktrees.go) uses a plain
`exec.Command`, so the git child inherits the pane's foreground process
group. Ctrl-C to quit `session monitor` — routine after a land, since the
land replaces the global binary — reaches the in-flight git as well as the
monitor. Process table at the time confirms it: a monitor restarted at
00:19:57 and another at 00:20:04, bracketing the fault.

`worktreeUnsettledAt` (internal/monitor/worktree_unsettled.go) then applies
E-1940's fail-closed rule: any error from the probe means "this worktree
cannot be verified", which marks the row ◆ and calls `recordProbeFault`.
That rule cannot distinguish

  - git could not run (absent, broken repo, permissions) — a real incident, and
  - git was killed because its parent was interrupted — not an incident at all.

so a user quitting a view leaves a permanent error naming an innocent task.

## Fix direction

An `exec.ExitError` whose `ProcessState.Sys().(syscall.WaitStatus)` reports
`Signaled()` is not a probe failure. Return undetermined and record nothing:
the render is being torn down anyway, and nothing is left to display the ◆ to.
SIGINT and SIGTERM are the cases that matter; SIGKILL is ambiguous (an OOM kill
is arguably worth knowing about) and is worth a deliberate decision rather than
being swept in with the rest.

Worth deciding alongside: whether the probe should take a context, so an
interrupt CANCELS the sweep rather than racing it. Today each row's git is
killed independently and the loop keeps going, starting new children as it goes.

## Scope note

The FREQUENCY of this is a property of E-2111's cost problem — with
`git range-diff` on the display path, sampling `ps` once a second for ten
seconds found 4-6 of them in flight at every instant on this machine, so a
Ctrl-C lands on one with near-certainty. That measurement is recorded in
E-2111's analysis.

The misclassification is worth fixing independently of what E-2111 decides.
Any probe can be interrupted, including the cheap ones: making range-diff free
would shrink the window without closing it, and `git status --porcelain` sits
behind the same fail-closed rule.
