# An epic's work-product mark reads its children

## The bug

`session status` renders one column between the type letter and the id, and it
answers "is there work product here, and where is it?" — `◆` outstanding, `⊙`
none, blank all landed, `~` not yet determined.

`unsettledMark` picks `⊙` from a single test on the row's own status:
`!taskstatus.Has(taskstatus.Shipped, r.Status)`. An epic never reaches a Shipped
status by doing its own work — its status is derived from its children, and its
own branch is normally empty. So an epic wears `⊙ not started` no matter how
much its children have landed.

Measured on E-1991: five children have landed code, three of them terminal, and
the epic row still renders `E⊙E-1991`.

The label is not the defect and is not changed here. An epic in this state has
work product — plenty of it — so "no work" would be as false as "not started".
What is wrong is the derivation: for an epic, the row's own status and the row's
own branch are both silent about the work the epic represents.

## The fix

An epic's work product is its children's. Derive the mark from them:

- any child unsettled → `◆`
- else any child in `taskstatus.Shipped` → blank
- else → `⊙`

`~` still wins when the underlying verdict is unknown, unchanged.

Non-epic rows keep today's rule exactly. The separate mis-signal on a todo that
landed mid-flight and kept working — `underway`, so not Shipped, despite real
landed code — is deliberately out of scope and has its own task; it needs a
source of truth that does not exist yet, because the unlanded cache cannot tell
"never had work" from "landed all of it" (`settled` covers both) and
`task_landings` was rejected as unreliable when E-2087 measured branches
demonstrably on main with no landing row.

## Where it goes

In `internal/monitor`, beside `AnnotateSessionStatusUnsettled` in
`reap_worktrees.go` — the data layer that already fills `Unsettled` and
`UnsettledKnown`, and the same place a second pass belongs. Not in the renderer:
`unsettledMark` in `internal/sessionstatuscmd/session_status.go` should keep
reading fields off the row and stay free of queries.

Read children the way epic status derivation already does —
`SELECT status FROM task_tree WHERE effective_parent_id = ?` in
`internal/events/epic_derivation.go` — so the two agree on what a child is,
including the effective-parent indirection.

For the `◆` half, a child's unsettled verdict comes from the same cache
`AnnotateSessionStatusUnsettled` already consults, so no new git probe is
introduced.

## Cost, and why it is acceptable here

E-2107 decided the mark by status precisely so that "no git probe or DB read
joins a per-row hot path", and that constraint still binds. This does not breach
it: the added read happens only for rows whose type is `epic`, epics are a small
fraction of any rendered set, and it is one indexed query per epic row against a
table already being read. Do not extend the same lookup to non-epic rows — that
is what would breach it, and it is the other task's problem to solve differently.

`session status` renders on a two-second tick in the monitor pane, so measure
the added cost on a project with many epics before landing, and say what it was.

## Acceptance

- An epic with at least one child in `Shipped` and no unsettled child renders
  blank, not `⊙`.
- An epic with any unsettled child renders `◆`.
- An epic with no child in `Shipped` still renders `⊙`.
- An epic whose children's verdicts are not yet known still renders `~`.
- Non-epic rows render exactly as they do today, including the known todo
  mis-signal, which this task does not touch.
- The legend still derives its entry by calling `unsettledMark`, so it cannot
  disagree with the column.
- `just test` and `just test-go` pass.
