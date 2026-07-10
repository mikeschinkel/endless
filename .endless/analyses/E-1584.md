# Design reasoning — epic status vs session status

## Decision (2026-06-29, Mike)

Chose (a) + (c): **keep `epic status`, re-scoped to not duplicate `session status`,
but defer it** (phase `later`) until a concrete coordinator need arises that
`session status` cannot meet. Surfaced while coordinating E-1537 by hand with
`epic list` / `epic show` / `task bind`.

## The two reads are a "status family" — same presentation, different anchor

`session status` / `session monitor` (the soon-to-be-renamed `session next`, E-1465)
is **session-anchored, bottom-up**: it resolves a *focal task* from the current tmux
window (live session active-task → `@endless_task_id` → most-recent live session) and
shows that task's neighborhood — parent/spawning task, siblings worked by other
sessions on the same focal, cross-session in-flight work, and the focal's direct
dependents. Its row set is driven by which sessions are live and the dependency graph
around the focal. Answers: "what's happening across the swarm around the work I'm on."

`epic status` is **epic-anchored, top-down**: given an explicit epic id, it shows the
epic's own state plus the COMPLETE roster of its children. Answers: "what's the full
state of epic E-NNN so I can decide what to spawn or unblock next."

## Why session status does NOT subsume epic status

Three structural gaps session status can't close:

1. **Targeting any epic from anywhere, no session required.** session status can only
   reach an epic if you're in a session whose focal *is* that epic. epic status takes an
   id — works from a plain shell, for an epic with zero live sessions, for an epic you're
   not currently working in. For a coordinator this is the common case, not the edge.
2. **The complete roster, including what's not moving.** session status's children come
   from live sessions + the focal's graph neighbors, so a not-started / blocked /
   session-less child can be absent. A coordinator's job is exactly seeing those — the
   "what's left."
3. **Next-actionable hint** (ready children, no open blockers) is an epic-roster
   computation, not a session-neighborhood one.

It also must earn its place against `epic show --children` (E-1540), which already prints
a static children table. epic status's differentiator over both siblings: roster +
*live* decorations (blocker / active-session per child) + actionability, targetable by id.

## Scope cut

- IN: epic's own state; complete child roster (every child) with phase, status, blocker
  indicator, active-session indicator; next-actionable hint. Reuse the decoration layer
  `session next` already computes in Go (blocked-by/blocks, active-session) — don't
  re-derive.
- OUT: cross-session in-flight / parent-spawn neighborhood (that's session status's job at
  the keyboard); full child descriptions / plan text / decision history (that's epic show
  / task show).

## Deferral

Committed but parked at `later`: build it when the coordinator need bites and session
status demonstrably can't cover it. The implementation plan is intentionally NOT written
yet — author it at pickup. Was blocked by E-1540 (the `epic` command group), now satisfied
(E-1540 assumed).
