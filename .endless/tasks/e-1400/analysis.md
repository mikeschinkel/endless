Fix in task_cmd.py spawn_plan: when the spawning session's id equals the current claimant's id, auto-release before pre-claim, then proceed with normal spawn flow (SessionStart's @endless_spawned_by hook binds the new session).

Keep the refusal in place when the current claimant is a DIFFERENT live session — that's a real conflict.
