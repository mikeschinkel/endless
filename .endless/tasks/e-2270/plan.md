All in src/endless/task_cmd.py (already one of the files that reads SQLite; no new SQLite reader is added), plus tests.

1. Helper `_unfinished_predecessors(item_id) -> list[dict]`: every task linked `precedes` INTO this task (equivalently, this task's `preceded_by` links), whose status is not in the terminal set (confirmed, assumed, completed, declined, obsolete, superseded). Each row: id, title, status. Read through the same relation query `show_relations` uses, filtered to the precedes type, so the two cannot disagree about what the links are.

2. Notice at the three start points, after `_require_spawnable` passes and before any work starts: `claim_item` (claim), the spawn path (spawn) and prime. When the helper returns rows, print to stderr, yellow:

       Note: E-2269 should follow tasks that are not finished yet:
         E-2268  submitted  Record which task and session raised each fault
       `precedes` is advisory, so nothing was refused. If the order matters,
       start those first, or link them with `blocks` to enforce it.

   It never refuses and never changes the exit status. An agent running the command sees the same text; no agent_help refusal, because nothing went wrong.

3. Nothing else changes: `precedes` stays advisory everywhere, and `blocks`/`blocked_by` keep their existing enforcement.

4. Tests (tests/, using the existing task-command fixtures): claim, spawn and prime each print the notice naming an unfinished predecessor with its status; a finished predecessor (confirmed, assumed, superseded) is not named; a task with no predecessors prints nothing; the command still succeeds in every case.
