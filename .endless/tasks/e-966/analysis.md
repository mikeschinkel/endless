Add 'endless session summary [<session-id>]' command that lists tasks touched in a session, grouped by parent task ID.

Decision (2026-04-28):
- Wait for E-799 (event-sourced architecture) to fully land. Session attribution will query the event log filtered by session_id rather than time-window intersection (cross-contaminates when sessions overlap) or a new session_task_touches join table (extra schema/write path).
- v1 grouping is parent_id only. No LLM/topical clustering in v1.
- If parent grouping proves too sparse for sessions with many root-level tasks, file a follow-up task for AI-based grouping (e.g., apfel for cheap local clustering). Do not build it preemptively.

Defaults to current session if no ID given.