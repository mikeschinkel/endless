## Already done, do not redo

- E-1063 is type `epic` (it was `todo`).
- E-1486, E-894 and this task are its direct children. The tree is deliberately
  FLAT: nested tasks make it hard to see what needs doing, so add depth only
  with a reason worth stating.

## Measured 2026-08-28

- 136 worktrees on disk under `.endless/worktrees`.
- 84 of them unsettled. `endless task unsettled --all` is the survey command and
  it paginates at 20, so pass `--no-limit`.
- Some carry very large unlanded counts: E-1844 at 223 commits, E-1785 at 198,
  E-1853 at 182, E-1829 and E-1834 at 11 each. A high count usually means the
  branch is far behind main rather than that it contains that much work; check
  before treating it as a large change.
- One row reads `(no task row)` — E-1279. A worktree whose task is gone is its
  own small decision.
- 57 open tasks match a Go-or-Python keyword sweep. Treat that as a candidate
  pool to triage, NOT as an answer. Most will be incidental mentions.

## Question 1 is mostly about ORDER, not ownership

The expected outcome is a set of `blocked_by` edges on E-1063, not a pile of new
children. Three buckets, and most candidates land in the third:

- BLOCKER — the port would clobber this work, or this work must exist first.
  Record as `endless task block E-1063 --by <id>`.
- CHILD — this IS part of moving the surface to Go. Expected to be a short list.
- NEITHER — mentions Python or Go incidentally. Leave it exactly where it is.

A fourth bucket worth calling out if you find it: tasks the port SUBSUMES, which
stop existing once Go owns the surface. Those are obsolete-on-land candidates,
not blockers — flag them, do not close them.

## Question 2 method note

The risk is not "unlanded work exists". It is "unlanded work that touches Python
the port will delete". A worktree editing only Go, docs or tests is cheap to land
after the port; one rewriting `src/endless/task_cmd.py` is not. Sort by what the
branch touches, not by how long it has sat.

Also worth checking: whether a worktree branched before E-1891 can still run
`endless` at all. That landing broke worktrees whose stale `endless-go` predated
the status registry, because `cli.py` imports the registry at module level ahead
of the re-exec that would route around it. A worktree that cannot run the CLI
cannot easily be landed by its own session.
