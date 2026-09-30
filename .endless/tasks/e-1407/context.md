E-1401 found that session_id is the connective tissue between events and per-session materialized tables (session_tasks), but no existing test verifies its end-to-end flow.

Currently the resolver only has unit tests for the sibling finder, not the integrated emit_event path.
