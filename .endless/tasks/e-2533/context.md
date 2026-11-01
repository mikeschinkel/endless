## Why

Raised by Mike on 2026-11-01 while planning E-2221 (duplicate marking on
session status boards), from the E-2498 session (ES-1394).

A session that plans a to-be-spawned task has no recorded relationship that
says so. Today a board can only tell that a task has a steward (the session
that keeps it in view until it is spawned or rejected) or that some session
focused it (focus moves on any write). Neither says "this task is being
planned now", and focus doesn't move until the planning session first writes
to the task, so planning is invisible at the start.

Mike: "It would be nice for a session to know another is actively planning it
vs. just being its steward but no planning has started, right?"

## Settled with Mike

- Planning is its own relationship, separate from ownership. It is not
  `task bind`: bind is `task claim` without the worktree and makes the session
  the task's owner. Mike: "when a session plans a to-be-spawned task that is a
  different relationship."
- Focus and stewardship stay separate concerns; this does not tie them
  together. Mike was "cautious about ... unintended consequences of tying them
  together", and sessions routinely write notes and links to other sessions'
  tasks.
- A session marks a task as being planned with `endless task plan start`
  (Mike offered `begin` or `start`; `start` already appears in the CLI,
  `begin` does not).
- `/needs-plan` runs it as its first step.
- Submitting the plan clears it.
- Planning works from any session, including one that has claimed its own
  task: that is where most planning happens.

## Open, for this task's plan

- Whether more than one session may mark the same task at once, and what a
  second `plan start` does.
- How a stale marker clears: the session ends, goes idle, or the user releases
  it, and whether an explicit stop command exists.
- How other boards, `task show` and `session status` display "being planned in
  ES-N".
