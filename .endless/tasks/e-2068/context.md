E-1891 relocated every status grouping into internal/taskstatus without changing membership, which surfaced that two sites answer 'does this blocker still block?' with different sets:

Unblocking = {confirmed, assumed, declined, obsolete} (monitor.GetActiveBlockers, matching the guide's blocking-semantics table) versus UnblockingNext = {confirmed, assumed, completed} (task_cmd.next_tasks).

User-visible symptom: a task blocked by work someone explicitly declined never surfaces in `task next`.
