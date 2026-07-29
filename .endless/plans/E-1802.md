# `session status`: surface the session's surfaced/revisited tasks (the You/Agent "Remaining" split)

## Gap

E-1461's own description promises a Remaining list *"split by next-actor — You
(bare endless commands you run) and Agent (this session's own in-progress
work)… Derives from session_tasks plus task status plus phase."* The capture
half shipped and works; the **display half does not render for the no-goal
case, and produces nothing when a session has no claimed task.**

Concretely, this session (E-963, no active task) recorded these rows correctly:

    session_tasks:  963|1801|surfaced      (filed E-1801 via `task add`)
                    963|1776|revisited      (touched via --relates-to)
                    963|1797|revisited      (touched via --relates-to)

…yet `endless session status` prints only:

    no active task — claim or bind one: ...

The three tasks are invisible. Root cause: `internal/monitor/session_status.go`
anchors its whole projection on the focal/active task
(`sfoc = SELECT active_task_id FROM sessions WHERE id = ?`, ~line 169) and the
tree/deps hang off that id. With `active_task_id` NULL there is no anchor, so no
session_tasks rows are surfaced at all.

## Deliverable

`session status` renders the emitting session's `session_tasks` rows classified
`surfaced` and `revisited` — the "Remaining" You/Agent split from E-1461 — even
when there is no claimed goal. `surfaced` = filed this session; `revisited` =
touched this session. `goal` continues to render as today when present.

- Read the rows already present in `session_tasks` (relation_id 2=surfaced,
  3=revisited); the classification is authoritative and set-once — display only
  reads, never reclassifies.
- Make the projection independent of `active_task_id`: when it is NULL, still
  list the session's surfaced/revisited tasks instead of short-circuiting to the
  "no active task" line.
- Preserve current behavior when a goal exists.

## Open item (context, not a subtask)

E-1462 described this rendering as feeding "session next" — a command that has
yet to be implemented (its epic stalled). So this You/Agent rendering was never
wired anywhere; it is unbuilt, not removed. Render it directly in `session
status`; do not couple this task to `session next`, whose fate (implement vs.
retire) is a separate open question the owner is deferring. This task must stand
alone and not depend on that decision.
