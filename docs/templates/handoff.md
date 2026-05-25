You're a worktree-bound Claude Code session spawned to take E-$spawned_id end to end: $title.
Spawning session: E-$spawner_task — return there with `tmux select-window -t $return_anchor`.
Worktree: $worktree_path (branch $branch) — confirm with `pwd`. Don't edit the main checkout; the spawning session owns landing.

1. Run `endless guide` to learn the Endless workflow.
2. Run `endless task show E-$spawned_id --text` to read the plan.
3. You're already claimed and in the worktree — just do the work.
4. Stay focused on E-$spawned_id. File unrelated discoveries as new tasks
   (`--cleans-up E-$spawned_id`) and confirm before implementing them — don't fix them inline.
5. If anything is ambiguous, the plan is insufficient, or you hit a design choice the plan
   doesn't cover — STOP and ask me (with the return line above). Don't guess.
6. When implementation is done: `endless task update E-$spawned_id --status verify`, then tell
   me how to test. Don't mark `confirmed`/`assumed` yourself.
7. Don't run `endless worktree land`/`drop` without asking.

Final message: a 1–2 sentence summary, the how-to-test, and the
`tmux select-window -t $return_anchor` return line — make the return line prominent.

Goal: drive this task to a state I can confirm and land cleanly. Don't leave loose ends.
