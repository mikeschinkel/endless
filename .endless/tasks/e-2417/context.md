E-2349 was spawned into go-cfgstore although the task belongs to endless, so its worktree (`go-cfgstore/.endless/worktrees/e-2349`, branch `task/2349`, plus a sandbox database) was cut from the wrong repository. E-2405 now stops spawn and claim from doing this, but a worktree already created in the wrong repository has no supported way out: `worktree drop` leaves the sandbox (E-1332), and nothing re-creates the worktree in the right repository.

Mike proposed `endless worktree move <task_id> --to=<project_slug>`.

Limits found while filing:
- A git worktree belongs to one repository, so a "move" is: create the worktree in the target repository, carry the work over, then retire the old worktree, branch and sandbox. It can be done reliably when the source branch has no commits beyond its base and no uncommitted changes. When it has work, that work is commits on another repository's history; carrying it over is a patch apply that can fail, so the safe default is to refuse and name what is there.
- Task events the wrong project already recorded in its own ledger (as E-2405's P11 found for E-2208) would also need to move, and the ledger is never hand-edited, so the command needs an event-pipeline way to do it, or it refuses when such events exist.
- With E-2405 in place, the target is the task's own project, so `--to` may be unnecessary unless the command should also re-home the task itself.
