(1) active_task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL — captures the session's bound task at the moment of the status row so joins to tasks are trivial without an extra subquery against sessions.

The CLI populates it from the resolved session's active_task_id at insert time (no XML element needed; pulled from the DB by the Go handler).

(2) summary TEXT — structured list of implementation layers shipped this session, in the same container-of-typed-items pattern as decisions/commits/memory.

XML: <summary><layer name='Schema' files='a, b'>purpose</layer>...</summary>.

Renders as a 3-column markdown table (Layer | Files | Purpose).
