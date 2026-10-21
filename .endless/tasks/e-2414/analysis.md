## Decisions taken while planning (with the user, 2026-10-20)

- **The chosen steward is stored (Q1 C).** Two alternatives were rejected. Making a touch outrank filing fails because the user touches a task only to see it, without meaning that session to steward it. Hiding the task on every other candidate leaves no steward once the chosen session ends, because the hides keep the filer out.
- **A claiming session can never steward its own task** (the user, 2026-10-20; ED-1605). `--steward` refuses it, and a stored steward that later claims the task stops counting.
- **A stored steward counts only while its session is present**, has not hidden the task and has not claimed it. Otherwise the steward is worked out as before, so stewardship still corrects itself when the chosen session ends, and comes back to it if that session is resumed.
- **The claiming session alone gives "none" (Q2 A).** The steward and the owner are different things (ED-1605); `session goto E-N` already reaches the owner.
- **`--steward none` clears the stored choice.** The steward is worked out again, as if `--steward` had never been set. A stored "no steward" was considered and not adopted.
- **Pins are retired here (ED-1633).** A touched task is never suppressed by another session's stewardship, so a pin has nothing left to override.

## Why the earlier analysis was wrong

It proposed setting the steward by pinning the task on the chosen session and hiding it everywhere else. A pin never made a session the steward: the steward is worked out only from filing and editing. And a pin cannot add a task to a board, since a board lists only tasks the session already has a relation to. So `task steward` would have reported no steward whenever the chosen session had not filed or edited the task.

## Storage follows the hide pattern

Hides and pins are per-(session, task) rows written through `session_tasks.*` events (`internal/events/session_task_visibility.go`). The stored steward is one row per task, written the same way through its own event, so it replays from the ledger like a hide.
