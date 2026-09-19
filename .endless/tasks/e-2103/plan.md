# Plan — a decision's status change reaches the sessions it concerns

Tasks notify. Decisions do not. `tasks_notify_sessions` (E-1917) fires on a task
UPDATE and inserts one `session_notices` row per session involved with that task;
the UserPromptSubmit drain renders each as an FYI line. `decisions` carries only
`decisions_updated_at`. A session files a decision, the user accepts or rejects
it minutes later, and the session never learns — it keeps answering as though
the call is still open.

Observed in the session that wrote this: two decisions filed, one accepted and
one rejected by the user, and neither arrived. The session went on to state one
of their statuses from memory, wrongly.

## 1. Who gets notified

The same derivation tasks already use, one hop further out.

`tasks_notify_sessions` fans to `session_tasks` — the sessions involved with the
changed task. A decision has no sessions of its own, but it has linked tasks:
task→decision rows in `task_deps`, decision→task rows in `decision_relations`
(`target_kind = 'task'`). So the audience is: sessions involved with any task
this decision is linked to, in either direction.

That is the right set and not merely the convenient one — a decision's status is
consequential exactly to whoever is doing the work it governs, which is what the
link records. It also self-limits: an unlinked decision notifies nobody, which is
correct, not a gap.

Suppress the session that made the change, as the task trigger already does.

## 2. What a notice can carry

`session_notices.task_id` is `INTEGER NOT NULL`, so a decision notice cannot use
the table as it stands.

Add a nullable `decision_id`, relax `task_id` to nullable, and constrain exactly
one of the two to be set. Do NOT generalize the column pair into
`(entity_kind, entity_id)`: decisions are on their way back into tasks, and the
generalized form would outlive the problem it solved and have to be un-generalized
later. The narrow pair is shaped to be deleted — when decisions become tasks, the
column drops and the existing task path covers this with no notice logic to
retire.

Fire on `status` at minimum. Include the fields a reader would act on
(`description`, `rationale`, whatever the decision analogue of the task trigger's
watched set is); do not fire on `updated_at`.

## 3. Rendering

The drain in `internal/monitor/session_notices.go` selects, renders and marks
delivered. A decision notice renders in the same one-line FYI shape as a task's,
naming `ED-NNNN` and the transition, so the reader learns nothing new about the
mechanism — only about the decision.

The task-shaped assumptions in that file — the `task_id` join, the removed-task
filter, the cleanup DELETE — each need the decision case handled or explicitly
excluded. A notice that cannot be joined must not silently vanish from the drain,
and must not block delivery of the notices around it.

Exclude `ended` sessions from the fan-out, as the task trigger does.

## Boundaries

- Does NOT change what a decision status means or who may set it.
- Does NOT notify on decision CREATION. A decision is born `proposed` by the
  session that filed it; telling it what it just did is noise.
- Does NOT add a notice surface anywhere but the existing UserPromptSubmit FYI
  line.
- Does NOT block on the decisions-into-tasks move. If that lands first this task
  is obsolete, and it should be closed rather than ported.

## Verify

1. A session holding a task linked to a decision is notified when that
   decision's status changes, on its next turn.
2. The session that MADE the change is not notified.
3. A decision linked to no task notifies nobody, and nothing errors.
4. Links resolve in both directions — `task_deps` task→decision and
   `decision_relations` decision→task.
5. A decision notice and a task notice arriving on the same turn both render.
6. An `ended` session accrues no decision notices.
7. Each notice is delivered exactly once.
8. Creating a decision produces no notice.
9. The existing task-notice path is byte-identical in behaviour — same rows,
   same rendering, same delivery marking.
