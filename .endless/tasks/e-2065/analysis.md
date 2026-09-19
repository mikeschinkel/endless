Split out of E-807 at confirmation (2026-08-25) rather than reopening it: the engine shipped, this is the part that did not. Measured detail lives in E-799's analysis; the load-bearing points, so this task stands alone:

- task_landings is ALSO destroyed, not merely skipped. `DELETE FROM tasks` cascades to it, and nothing restores it. That cascade is currently unreachable only because E-2062 makes rebuild-db --confirm refuse.
- task_deps is skipped and NOT destroyed — no FK reaches it — so it silently persists stale across a rebuild. Different failure, same root.
- Second-hop cascade: session_gates goes too, and takes report_judgments and report_labels with it. Anything that walks only the direct children of tasks will miss those.
- `tasks_notify_sessions` is AFTER UPDATE ON tasks. Today's delete-then-insert never fires it; switching the copy-back to an upsert WOULD, notifying every live session about every task whose projected value differs. Suppress the notice triggers around any copy-back; e-1969's change file shows the drop-then-recreate shape inside a transaction.
- `task_landings_notify_sessions` is AFTER INSERT, so copying landings back announces every historical landing to every live session. The E-2005 schema comment calls that replay harmless ON THE GROUNDS that rebuild copies back only three tables — true today, false the moment this lands. Correct it in the same commit.
- A projected task_landings.session_id can name a session this machine never had: the ledger is shareable, sessions are machine-local. NULL it on copy rather than failing the FK.

Sequencing: this must land before `sessions.task_id` loses its ON DELETE SET NULL, and E-2062's guard should be deleted only once this is done. Both constraints are spelled out in E-2062's plan.

Related but separately owned: E-910 (mutations that never reach the ledger at all), E-1041 (replay-side FK and UNIQUE failures), E-914 (a non-nuclear repair path).