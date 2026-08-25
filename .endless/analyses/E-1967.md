# Renamed identifiers — read this first (2026-08-25)

E-1969 renamed `sessions.active_task_id` to `sessions.task_id` and
`active_epic_id` to `epic_id`. This analysis has been updated to the current
names so a grep for them lands here.

Two things were deliberately NOT rewritten, because they are evidence rather
than description: the quoted `user-machine.jsonl` lines below still read
`old_active_task_id` / `new_active_task_id`, which is what those log entries
literally say (E-1969 renamed the keys for NEW lines only), and the quoted code
blocks are reproduced as the code stood when this was written — E-1968 has since
deleted the reopen-path release entirely.

The open question this analysis ends on — "is the spawn guard keyed on a
now-reliable task_id, on `session_tasks`, or on both?" — was answered on
2026-08-24: `sessions.task_id` alone. See the plan in `--text`.

---

# Evidence

Gathered 2026-08-13 after Mike accidentally spawned a second session on E-1859
instead of resuming the one that had worked it.

## Session→task binding is treated as 1-to-1 in practice, enforced nowhere

Writers create duplicates freely. `task claim` checks the task's status and
resolves a session; it never asks whether another session already holds the
task. `task bind` sets `task_id` with no ownership check at all.

Readers all assume exactly one and silently pick when there is more. Three
places resolve task → session, each ending `ORDER BY ... LIMIT 1`:

- `monitor.resumeByTask` — `WHERE task_id = ? AND session_id IS NOT NULL
  ORDER BY last_activity DESC LIMIT 1`
- `monitor`'s session_focus query — `JOIN sessions bg ON bg.task_id =
  coord.task_id ... LIMIT 1`
- `monitor`'s tmux_lookup query — same shape

So `endless session resume E-NNNN` takes the most recently active session and
gives no signal that it discarded another.

The only place the invariant is named is `release_item`'s refusal when a
different LIVE session owns the task. One guard, on the release path, protecting
an invariant nothing else maintains.

## Why the existing spawn guard did not fire

`task spawn` already refuses when another session owns the task — but the check
is scoped to LIVE sessions. ES-1046 was `ended`, so there was no live owner and
the spawn proceeded.

Keying the guard on `sessions.task_id` would not fix this either: by the
time the spawn ran, that column was already NULL for E-1859 (an earlier
`task reopen` had released it). The only durable record that ES-1046 had ever
worked the task was `session_tasks`.

## Why session_tasks cannot answer "who owned this" today

`session_tasks` has `UNIQUE(session_id, task_id)` and a `relation_id` FK to
`session_task_relations` (1 goal, 2 surfaced, 3 revisited). Per the schema
comment the relation is "set once at capture time by the task-mutation
executors (claim→goal, create/import→surfaced, else→revisited) and never
changed on a later touch."

So a session that first revisits a task and later claims it keeps `revisited`
forever. Observed on E-1859:

    996 → surfaced;  1029, 1043, 1046, 1070 → revisited

ES-1046 held `task_id = 1859` and is nevertheless recorded as
`revisited`. The table can say who saw a task; it cannot say who owned it.

## Mike's design call

Put `relation_id` in the key — `UNIQUE(session_id, task_id, relation_id)` —
rather than making the existing row's relation upgradeable. Upgrading would
overwrite "saw it" with "owned it"; the composite key keeps both, so the history
reads "saw it, then owned it."

This also serves the deliberate fresh-context case: when a task is
intentionally moved to a new session for a clean context, null the old session's
`task_id` and write a `goal` row for the new session. The old session's
`goal` row survives, so "which sessions ever owned E-NNNN" stays answerable.

Open question for implementation: with the relation in the key, does a `goal`
row mean "owned it at some point" or "owns it now"? Append-only ("at some
point") is what recovery needs.



---

# Correction 2026-08-20: what actually NULLs `task_id`

The description previously asserted that `sessions.task_id` "is NULL once
the binding is released" and treated that as a fixed property to design around.
That framing was wrong twice, and it was traced end to end on E-1917.

## Land does not release

E-1917's ledger, in order:

```
task.status_changed  submitted → underway   actor cli 1048
task.claimed                                                 2026-08-07
task.landed          task/1917-…  86b4740                    2026-08-08
task.status_changed  unverified → assumed                    2026-08-08
task.released                                                2026-08-15T06:48:31Z
task.status_changed  assumed → revisit      (same instant)
task.fields_updated  analysis := "# E-1917 post-land defects…"
```

There is no `task.released` at land time. The binding survived the land by a
week. E-1857's machine-local log agrees:

```
2026-08-07T22:39:31Z  reason=claim-event  new_active_task_id=1917
2026-08-15T06:48:31Z  reason=release      old_active_task_id=1917  new=∅
```

## `task reopen` is what released it

The release, the `assumed → revisit` flip and the defect analysis were written in
one burst by one command. `src/endless/task_cmd.py` (the reopen path) does this
before flipping status:

```python
# Clear any lingering session→task binding before flipping status.
# Rare for terminal tasks (worktree land releases), but the plan calls
# for it explicitly so retrospective queries see a clean handoff.
bound_sessions = db.query(
    "SELECT id AS eid FROM sessions WHERE active_task_id = ?", (item_id,))
for s in bound_sessions:
    emit_event(kind="task.released", ..., session_id=str(s["eid"]))
```

and `events.execTaskReleased` runs:

```sql
UPDATE sessions SET active_task_id = NULL, active_epic_id = NULL
 WHERE id = ? AND active_task_id = ?
```

So reopening a task to record defects in its landed work severs the link to the
session that produced that work — in the same command that writes the defect
report. The stated intent is "so retrospective queries see a clean handoff"; the
effect is that retrospection becomes impossible. That is the case to fix.

## Consequence observed

Resuming E-1917 by task ref, and by session ref, both fail.
`ResolveResumeTarget` → `resumeByTask` matches only
`WHERE task_id = ? AND session_id IS NOT NULL`, so with the column NULLed
there is no path from the task back to its claiming session — whose transcript
is intact and whose `task.claimed` event is right there in the ledger. `session
resume` then reports that the session never claimed a task, which is false: it
cannot distinguish *released* from *never claimed*.

## What this changes for this task

Do not key the fix on the assumption that `task_id` goes NULL. Removing
the reopen-path release is part of the work: per ED-1560, `task_id` is
write-once — set at claim, never NULLed and never repointed — and it IS the
ownership record.

That corrects an earlier revision of this note, which said the append-only
`session_tasks` change would supply the durable ownership history. It would not.
`session_tasks` records which tasks a session was INVOLVED with
(goal/surfaced/revisited); involvement is not ownership and cannot stand in for
it. So this task's original premise needs settling before implementation: is the
spawn guard keyed on a now-reliable `task_id`, on `session_tasks`, or on
both? That is a design call, not a detail.

Independent of it, `relation_id` frozen at first touch remains a defect on its
own terms: E-1917's claiming session is still recorded as `surfaced`, stamped when it
filed the task, never upgraded to `goal` when it claimed it.

## Note on actor attribution

Every one of those E-1917 events carries an `actor.session_id` naming the session
being released, while other sessions were doing unrelated work in the same window. For `task.released` that is by construction (`session_id=str(s["eid"])`
names the released session, not the caller). Worth keeping in mind when reading
the ledger for provenance; see also E-1917's own third post-land defect, where
`ENDLESS_SESSION_ID` conflates "route through this session" with "this agent made
the change".




