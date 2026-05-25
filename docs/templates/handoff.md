You're working on E-$spawned_id: $title.
Spawning session: E-$spawner_task — return to it with `tmux select-window -t $return_anchor`.

1. Run `endless guide` to learn the Endless workflow.
2. Run `endless task show E-$spawned_id --text` to read the plan.
3. The task is already claimed and you're in its worktree — just do the work.
4. When implementation is done: `endless task update E-$spawned_id --status verify`,
   tell me how to test, and print the return line above.
5. Don't run `endless worktree land`/`drop` without asking. File drive-by work as
   separate tasks (`--cleans-up E-$spawned_id`) and confirm before implementing.

Goal: drive this task to a state I can confirm and land cleanly. Don't leave loose ends.
