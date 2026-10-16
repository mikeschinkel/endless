The verify recipe in `justfile` is a wrapper around `endless task verify`. When
it was written the command could do less than it does now: the recipe derived the
task id, located the task's worktree and changed into it, and pinned a database.

The command has since grown all of that except the pin. `_resolve_task_id`
derives the id from the session and then from the cwd — the same two sources, in
the same order, as the land recipe — and `_run_dir` resolves the task's worktree
and runs the suite there, with the stated intent of making one command sufficient
from anywhere in the project.

The pin is the part that did not age well. It named the worktree sandbox, which
holds no task rows at all, so a pass recorded there was recorded nowhere while
`worktree land` read main and refused. That is being fixed on its own.

What remains is that the recipe is redundant, and that redundancy is what let it
drift in the first place: nobody re-read it when the command caught up. It is
nonetheless the spelling people actually type, because it is shorter than
`endless task verify` and verifying happens constantly — so deleting it needs a
command that is just as short.

Flat top-level commands already exist beside the groups: `touch`, `start`,
`notes` and `sql` are each registered directly on the root group.
