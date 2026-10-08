Decision (Mike, 2026-10-07): an unfinished preceding task REFUSES claim, spawn and prime; `--out-of-order` overrides the refusal. Flag name recommended by the agent; rename freely.

All in src/endless/task_cmd.py and src/endless/cli.py (task_cmd.py already reads SQLite; no new SQLite reader is added), plus tests.

1. Helper `_unfinished_predecessors(item_id) -> list[dict]`: every task linked `precedes` into this task (this task's `preceded_by` links) whose status is not terminal (confirmed, assumed, completed, declined, obsolete, superseded). Each row: id, title, status. Read through the same relation query `show_relations` uses, so the two cannot disagree about what the links are.

2. The refusal. `_require_spawnable` gains this check, so claim, spawn and prime all refuse the same way and before any work starts. When the helper returns rows, and `--out-of-order` was not passed, nothing changes and the command exits with:

       Cannot spawn E-2269: tasks that should come first are not finished:
         E-2268  submitted  Record which task and session raised each fault
       Start those first, or run it anyway:
           endless task spawn E-2269 --out-of-order

   It is an agent_help.report refusal: running work out of order is the user's call, so an agent stops and asks rather than passing the flag itself.

3. The flag. `--out-of-order` on `task claim`, `task spawn` and `task prime`. With it, the predecessors are still listed (one yellow line each, on stderr) and the command proceeds. It overrides only this check; it does not touch the plan, open-question or prior-claim refusals.

4. `blocks`/`blocked_by` keep their existing behaviour. The difference between the two relations becomes: `precedes` refuses but can be overridden with `--out-of-order`; `blocks` is the hard dependency.

5. Docs: the relation table in `endless guide tasks` (and the precedes entry in task_cmd.py, which says "advisory, never blocks") updated to say precedes now refuses unless overridden.

6. Tests: claim, spawn and prime each refuse with an unfinished predecessor, naming it and its status, and change nothing; each succeeds with --out-of-order and lists the predecessor; a finished predecessor (confirmed, assumed, superseded) does not refuse; a task with no predecessors is unaffected; the plan and open-question refusals still fire with --out-of-order passed.



7. Grown scope (found during implementation): the auto-spawn and auto-prime jobs (internal/autospawnjob) mirror spawn's refusals so they never pick a task spawn would refuse — otherwise they re-pick it every run. Their eligibility queries now treat an unfinished `precedes` source like an unfinished `blocks` source (the jobs never pass --out-of-order), with an exclusion case in each job's table test and a settled-predecessor positive control.
