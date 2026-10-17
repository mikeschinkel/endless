`endless session resume` looks for a task's worktree and branch only in the repo of the shell it runs from, not in the task's own project repo.

Seen 2026-10-15 during recovery via `endless start` (46 task windows): E-2206 belongs to go-cfgstore, and its session ran in a go-cfgstore worktree with branch `task/2206` present and intact. Resume looked only in the Endless repo and refused with "never landed, its branch task/NNNN is gone, and no remote has a copy of it ... Stop and ask". That claim was false, and it could alarm a user into thinking work was lost. `claude --resume <uuid>` in the go-cfgstore worktree worked.

This task originally also covered go-cliutil E-2289, whose session ran in the project root with no worktree. That case moved to E-2370 (explicit `--recreate`). Resuming in the project root was rejected.
