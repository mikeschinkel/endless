Reported by Mike, 2026-10-02: `session status` showed E-1486 on his board while tmux window active:18 showed it too, with no duplicate marker on either.

## How it works today

`AnnotateSessionStatusOwnership` (internal/monitor/session_ownership.go) fills `Focused`, `DuplicateWork` and `OwnedElsewhere`. Two independent exclusions keep a displayed-twice task unmarked:

**1. Ownership, not display.** `liveOwnership` is the only input to `DuplicateWork`. It counts a session as an owner only when a `session_tasks` row exists for the task with `relation_id` in (surfaced, revisited), the session's state is live, and the session's own claimed task is not that task — plus the focusers query. A task on a board for a structural reason (epic child, dependent, blocker) has no such row, so `owner == 0` and the switch takes the branch whose own comment reads: "Nobody live has a claim on it: it belongs to whichever board surfaced it (an epic child, a dependent, a blocker)." Today's behaviour is deliberate and documented, so this is a change of intent rather than a defect against the code's stated contract.

**2. Board frames are never annotated at all.** The candidate loop skips any row where `isBoardFrame` is true — `IsFocal || IsParent || IsFrom`. An epic rendered as the PARENT frame of a board is excluded before ownership is consulted, so it cannot be marked however good the ownership data is. For E-1486, an epic, this is the operative exclusion.

## Evidence, measured on the main database

E-1486 "Remove all Python SQLite access by porting it to Go", type epic, status submitted, parent E-1063.

- Nothing live claims or focuses it: `SELECT id, state, task_id, focus_task_id FROM sessions WHERE task_id=1486 OR focus_task_id=1486` returns no rows.
- Of its `session_tasks` rows, every surfaced/revisited one (relation_id 3) belongs to a session in state `ended`. Its one live session (551, `needs_input`) has a NULL `relation_id`, which the (surfaced, revisited) filter excludes.

So `liveOwnership` correctly reports no owner, and the frame exclusion would have skipped the row regardless.
