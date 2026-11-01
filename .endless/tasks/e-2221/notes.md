## Mike's intent for the duplicate mark (2026-10-31)

Reported from tasks:9 (ES-1394) and tasks:10 (ES-1399, claiming E-2358): both
boards listed E-2519 and neither marked it duplicate. On tasks:9 it was a
touched row; on tasks:10 it was a dependent of the focal task (E-2358 blocks
E-2519). Its filer, ES-1316, had hidden it, so it had no steward.

Mike, who designed the mark:

> I INTENDED the duplication mark to SHOW ME THAT A TASK WAS LISTED IN (AT
> LEAST) ONE OTHER SESSION so that I do not accidentally take action on it
> BOTH sessions.

> A task that is a blocker SHOULD appear in the graph, but NOT if it is a
> duplicate and the OTHER session is the task's STEWARD. In that case the
> task should be SUPPRESSED from the list (but not from the graph.)

> when I explicitly touch a task in a session [it] SHOULD override
> suppression (and display a duplicate icon, if applicable)

This answers the analysis's first open question: a duplicate is a task
listed on another present session's board, whatever the reason it is there,
including a blocker or dependent. The noise concern is handled by
suppression, not by leaving the mark off:

- Listed on another board, and that other session is the steward: drop the
  row from this board's list, keep it in the graph.
- Listed on another board and no other session stewards it: show it, with
  the duplicate mark.
- Touched in this session: never suppressed, and it carries the duplicate
  mark when it is listed on another board. E-2414 P27 already keeps touched
  rows on the board; it does not add the mark.

Board frames (focal, parent, spawner) do not get the duplicate mark — Mike,
asked whether they should: "No".
