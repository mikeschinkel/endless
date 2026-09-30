E-1929 shipped ED-1547 for TASKS ONLY. Its own description says 'entities whose ids something else refers to — tasks, sessions and decisions — are no longer deleted', but only tasks got the removed column, the live view and the read-path audit.

Verified against the real ledger on 2026-08-20: 'removed' is present on tasks and absent on sessions, decisions and projects.

— 195 sessions and 1,616 activity rows vanished when two junk project rows were unregistered by hand that day.

ED-1547 has since been amended to name projects explicitly and to cover any table added later that meets the same test,
