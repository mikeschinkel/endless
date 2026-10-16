Upstream of E-1983, now unblocked and otherwise built against the old model, and of the write-once task_id tightening.

Superseded statement: where this task says hooks fail open, or must not block, that is out of date. ED-1357 (accepted 2026-10-15) says every hook error must block (exit 2); E-1358 implements the remainder.


Evidence from E-2206 (2026-10-15): one Claude Code conversation started as Claude session 4c66fb3b (ES-1339, which claimed E-2206). After a `/cd` into a go-cfgstore worktree, the harness reported a new Claude session ID, fc73b577, and Endless created ES-1341 for it with no task. From then on `endless task bind E-2206` failed ("session resolution found none to bind E-2206 to"), and `task update` refused with "Cannot determine the Endless session for this pane". Both worked only with `ENDLESS_SESSION_ID=1339`. Mike ran the same bind from his own shell and it worked, so the failure is specific to the agent's resolution path.



Correction and more evidence from E-2206 (2026-10-16): the resolution failures above were not caused by the session id change. A stray project row for the worktree path (registered by mistake, then left `ignored`) was more specific than go-cfgstore, so `session-query ensure-claude-id` refused every lookup from the worktree with "directory is ignored by Endless" (cause filed as E-2360). Once that row was removed, ES-1339 resolved with no override. Two session-identity defects did show up: (1) a shell pane resolved to ES-1319, an older session whose record still claims that pane, because the pane-match layer of `_current_endless_session_id` runs before the window's `@endless_session_uuid`; (2) ES-1339 is recorded under project endless though it works only on go-cfgstore, because a session's project is fixed by the directory it started in, not the task it claims or binds.
