BUILD side: the projector fails to insert nearly every task_landings row — (1) it skips session events (projector.go), so the temp sessions table is empty and every landing carrying a session_id fails the session_id FK; (2) tasks predating the ledger have no task.created event, so their landings fail the task_id FK.

COPY-BACK side, folded in from E-2065: rebuild-db writes back only tasks, decisions and decision_relations, so even a correctly built task_landings never reaches the real DB — and DELETE FROM tasks cascades the live rows away first.

task_deps has the same copy-back gap without the cascade.

Surfaced by E-1719's historical backfill but affects ALL landings.
