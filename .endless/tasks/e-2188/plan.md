# Plan — show each session's focused task and who owns a shared task

## Why

With many sessions open, `session monitor` is how Mike keeps track of what each
session is working on. Two gaps:

1. A session is bound to ONE claimed task (`sessions.task_id`), but we often
   discuss, file or plan other tasks in it. Those land in `session_tasks`, and
   nothing shows which of them the conversation is on right now.
2. The same task appears on many sessions' boards, so it gets worked in two
   sessions at once: duplicated effort, and at times conflicts.

This replaces E-1031 (obsolete), which planned a much larger prose "focus"
entity for a model where one session worked many tasks.

## Part 1 — `sessions.focus_task_id`

- New nullable column `sessions.focus_task_id`, a foreign key to tasks with
  ON DELETE SET NULL, through a migration.
- Exactly one focused task per session. It is a visual aid only, so an
  occasional wrong focus is acceptable.
- **Where it is set:** in `upsertSessionTask` (internal/events/executor.go),
  the single write path for `session_tasks`. It is set on EVERY touch, first or
  repeat, so going back to a task already on the board moves focus back to it.
- **What moves it:** claimed, surfaced (filed) and revisited (updated) only.
  Queued and referenced do not, so merely viewing a neighbour does not steal
  focus. If that proves wrong in use, file a follow-up.
- **Display** in `session status` and `session monitor`: the focused task's ID
  is rendered in inverse video, and the column-4 icon (between task type and
  task ID) is `◼︎` (U+25FC U+FE0E) — the plain-text fallback when colour is off.
  Agent output carries `focus=E-NNN`.
- **Focus overrides hidden:** a focused task is shown even if hidden in that
  session, including by a manual `session hide --task`.

## Part 2 — ownership of a task on more than one board, computed at display time

No new command, and no hide rows written. Ownership is decided each time a
board is drawn, so it fixes itself when a session ends. (Mike asked for a
command first, and agreed to try without one.)

**Never hidden:** the session's claimed task, its parents and its spawners.
Only auto-added entries (epic children, queued, referenced, and the like) are
candidates.

**Only live sessions count.** A session with no active Claude session neither
owns a task nor loses one. A dead session never removes a task from a live
board.

**Owner**, in order:

1. The live session that surfaced (filed) the task.
2. Otherwise, the ONLY live session that revisited (updated) it.
3. Otherwise (no live surfacer, two or more live updaters): ambiguous — the task
   stays on every such board with the duplicate marker.

A non-owner's board omits the task, unless focus overrides that (Part 1).

**An update never takes ownership.** Two sessions touching one task is often
expected — one implements, another adds a few lines of analysis — and adding a
note must not take the task from the session that filed it.

**The duplicate marker** shows on BOTH the owner's and the other session's
board when:

- a task is in focus in one session and owned by another, or
- the task is ambiguous (rule 3).

It warns that another session may already have done significant work, so the
user can stop and decide which session keeps the task. It is rendered as the
task ID in bright white on a black background, with column-4 icon `◫` (U+25EB)
as the plain-text fallback.

**Worked case.** E-100's session filed E-150, and E-101's session updates it.
E-150 stays owned by E-100's session. The update focuses E-150 in E-101's
session, and focus overrides the hide, so E-150 shows on E-101's board with the
duplicate marker — and on E-100's board too. When E-101's focus moves on,
E-150 drops off E-101's board.

## Boundaries

- Display only: `task next`, blocking, claims and `session_tasks` rows do not
  change.
- `session hide --task` stays the manual escape hatch.
- "Live Claude session" reuses the liveness `session monitor` already uses.

## Verify

A suite that builds several sessions with overlapping `session_tasks` rows and
asserts, for each board: focus follows writes but not reads; focus overrides
hidden; the owner is chosen per the ladder; the duplicate marker is shown in
both boards in the focus-elsewhere case and in the ambiguous case; an ended
session's tasks come back on live boards; and claimed tasks, parents and
spawners are never hidden.
