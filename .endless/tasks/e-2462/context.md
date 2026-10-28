`endless task spawn` refuses a task that has no plan. On 2026-10-28 Mike tried to spawn gomion's E-2454 ("Bring go-pkgs CI and Release workflows to go-dt's model"), which is unplanned, and was refused. The usual route around it is process theatre: planning a gomion task from an unrelated session (here a go-cliutil one) puts the planning in the wrong project's context, and the alternative is to open a task-less session in gomion, have it file a brainstorm, and plan there.

What Mike wants instead: an `--allow-no-plan` flag on `task spawn`, following the precedent of `--allow-open-questions`. It starts the session on the unplanned task, and the handoff tells the spawned session that a plan is needed: its first job is to write the plan, with its items, and submit it for Mike's review before implementing anything.

The status lifecycle already lets a session claim an unplanned task (unplanned → underway), so the gap is in spawn's gate and its handoff, not in the lifecycle.
