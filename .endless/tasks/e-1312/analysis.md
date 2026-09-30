Schema sketch: <session-status><headline/><resolved><task id=... status=...>note</task>...</resolved><pending/><verify/><notes/></session-status>.

v1 stores section contents into existing text columns.

v2 (later) parses <task> elements into a session_status_task child table without changing the author-facing format.

Also: land the session_statuses CREATE TABLE in internal/monitor/db.go so fresh installs get the schema.
