# Inventory, measured 2026-09-23 immediately after ED-1598 was accepted

Recorded, not acted on. Re-measure before working this — the shape moves as
tasks close, and a closed row needs no reparenting.

## Scale

Tasks below the two-level limit: 79 at depth 3, 15 at depth 4, 2 at depth 5.
Most are already closed and cost nothing. The live remainder is 29 rows.

## The live remainder, by the intermediate parent that has to go

Eleven middle nodes carry all of it, and three carry most:

| middle node | its status | open rows beneath | deepest |
|---|---|---|---|
| E-799 Migrate to event-sourced architecture | unplanned | 9 | 3 |
| E-1486 Remove all Python SQLite access | submitted | 6 | 3 |
| E-800 Backend integration for Beads, JIRA, GitHub Issues | ready | 6 | 3 |
| E-1696 read-capture gate and referenced/queued relations | assumed | 1 | 4 |
| E-1246 tmux status-line menu labels | unverified | 1 | 3 |
| E-1182 shell-init into endless-sandbox enter subshell | assumed | 1 | 5 |
| E-1170 auto-creation of task worktree on task start | assumed | 1 | 4 |
| E-1136 coordinator command watching now-ready-auto | obsolete | 1 | 3 |
| E-1114 isolate endless-sandbox enter subshell | confirmed | 1 | 4 |
| E-987 event log auto-appends vs clean-main tension | assumed | 1 | 3 |
| E-870 session recap generation Python to Go | assumed | 1 | 3 |

E-799, E-1486 and E-800 account for 21 of the 29. Working those three clears
most of the debt; the remaining eight are one row each.

## Two shapes, and they are not the same job

**A middle node that is still open** (E-799, E-1486, E-800, E-1246) is the real
work. It is a live container holding live children, and dissolving it means
deciding where its own content goes before reparenting — the E-1992 pass is the
worked example: framing merged up into the epic's plan, a detail folded down
into the child that already owned it, one genuinely separate concern lifted out
to its own task, then the container closed with a reason.

**A middle node that is already closed** (E-870, E-987, E-1114, E-1136, E-1170,
E-1182, E-1696) is bookkeeping. The container is finished; only its open
descendants still dangle, and they reparent to the grandparent with no content
decision to make.

## Do not do this on sight

ED-1598 grandfathers existing depth deliberately. Reparenting a live child
changes which epic rolls its status up, and doing that as a drive-by during
unrelated work moves a status without anyone deciding to. These are deliberate
passes or they are nothing.

## Order, if it gets worked

Closed middles first — they are mechanical and shrink the list by eight with no
judgment calls. Then E-800 (ready, so it is about to be touched anyway), then
E-1486, then E-799, which is the largest and the most unplanned.
