Upstream of E-1983, now unblocked and otherwise built against the old model, and of the write-once task_id tightening.

Superseded statement: where this task says hooks fail open, or must not block, that is out of date. ED-1357 (accepted 2026-10-15) says every hook error must block (exit 2); E-1358 implements the remainder.


Evidence from E-2206 (2026-10-15): one Claude Code conversation started as Claude session 4c66fb3b (ES-1339, which claimed E-2206). After a `/cd` into a go-cfgstore worktree, the harness reported a new Claude session ID, fc73b577, and Endless created ES-1341 for it with no task. From then on `endless task bind E-2206` failed ("session resolution found none to bind E-2206 to"), and `task update` refused with "Cannot determine the Endless session for this pane". Both worked only with `ENDLESS_SESSION_ID=1339`. Mike ran the same bind from his own shell and it worked, so the failure is specific to the agent's resolution path.
