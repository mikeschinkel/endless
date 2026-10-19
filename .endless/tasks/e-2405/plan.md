# Spawn and claim a task in its own project's directory

## Approach

Every command that creates or locates ONE task's worktree resolves the project
from the task (`tasks.project_id`), not from the current directory. It takes
the repository root from that project's registered path through
`_project_repo_root(project_id)`, and the project name from the same row.

- `spawn_plan`: resolve the task's project and root before any pre-launch work,
  and pass both to `_perform_claim_work` (`project_root=`, `proj_name=`). So the
  status change, the worktree, the `post-worktree-create` hook and the events
  all happen in, and are tagged with, the task's project. The launch already
  passes `--project-id` from the task and `--cwd` from the worktree.
- `prime_task`: create the worktree under the task's project root.
- `claim_item`: when the task's project is not the project the claim is run in,
  refuse before changing anything and name `endless task spawn E-N`. A repeat
  claim by the session that holds the task locates its worktree under the
  task's project root.
- `session goto --resume` (`_recover_dropped_worktree`): recreate a dropped
  worktree under the task's project root.
- `task unsettled E-N` (`unsettled_item`): inspect the task's worktree under the
  task's project root. `task unsettled --all` keeps surveying cwd's project.

When the task's project is not cwd's, spawn's and prime's output names it,
for example `Project: go-cfgstore`, above the worktree line. When the task's
project has no registered path, or that path is not a directory, each command
refuses before changing anything and names the project and the path.

## Ledger repair

An aborted spawn of go-cfgstore's E-2208, run from the endless checkout, wrote
its `task.status_changed` (submitted -> underway) into endless's ledger tagged
`"project":"endless"`. go-cfgstore's ledger has no status change for E-2208.
Move that line: remove it from endless's ledger file, and append it, with
`"project":"go-cfgstore"` and nothing else changed, to go-cfgstore's current
ledger file. Then check that a rebuild of each project's database gives E-2208
the status history it has now.

## Tests

In `tests/`, with two registered projects, each a git repository, and the
command run from the first:

- spawn and prime of the second project's task create the worktree in the
  second repository and none in the first;
- the events they emit carry the second project;
- the second project's `post-worktree-create` hook runs, and the first's does
  not;
- claim of the second project's task refuses, changes nothing and names spawn;
- `task unsettled E-N` and `session goto --resume` resolve the second
  project's worktree;
- a missing project path refuses before anything changes.
