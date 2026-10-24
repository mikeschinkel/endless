Priming is meant to be invisible to the user: a primed session (ED-1621) is the task's implementation session, started early. But task spawn on a primed task hits the live-owner check (_check_task_ownership in src/endless/task_cmd.py). That refuses with a generic 'E-N is already active in session X (pane ...). Switch to that session or have it release the task first.' It never says the session is primed, and release is impossible because sessions.task_id is write-once. So the user has to know the task was primed and resume it by hand (session goto, or select its window and type). When the live owner is in state primed, spawn should do what resume does: select that session's window and leave it to the user's next message to wake it and claim. Other live owners keep today's refusal.

Mike, in E-2208 (2026-10-20): "In prior sessions I said spawn needed to work with it because priming should be viewed as a background task from the perspective of spawning. It should not matter if it has been primed or not in order for spawn to work as it has previous worked prior to priming."

Three more gaps found priming E-2349 from go-cfgstore:

- `task show` has no primed indicator. The only signs a task was primed are the ◇ in `session list` and `[primed]` in Touched-by.
- A primed session binds itself through sessions.task_id, so `task show` labels it "Claimed" while the task is still unplanned and nobody has claimed it.
- `task prime` emits no event for the session that ran it, so that session's `session status` does not list the primed task. E-2405 (P16) adds a `spawned` session-task relation that spawn records on the spawning session; prime records nothing comparable. This was meant to be added to E-2405, but E-2405 is unverified and its plan items are frozen.
