Consolidated 2026-08-25 from two halves of one mechanism.

E-814 (atomic claim primitive): 'Atomic claim sets assignee + transitions to
in_progress in one event. Conflicts resolved by HLC order at projection time.'
E-815 (conflict surfacing): 'Park invariant violations as conflict records.
Surface in dashboard. Expose via CLI + MCP. AI proposes resolution or escalates.
Resolution is a new event.'

Evidence at consolidation time, NOT a scope decision — re-check at pickup:

- The CLAIM half of E-814 looks largely shipped by unrelated work: task.claimed
  is a live event, ED-1560 makes sessions.task_id write-once with a trigger, and
  E-1967 refuses a spawn onto a task any session ever claimed. What is NOT
  shipped is deterministic conflict RESOLUTION — kairos timestamps exist and
  sort causally, but nothing resolves two conflicting writes by them.
- No conflicts table exists in schema.sql. E-815 is unstarted.