Today sandbox dir name is 'worktree-e-{task_id}' (worktree_cmd.py), built from task_id only - the worktree dir's optional slug suffix is collapsed away.

Multiple worktrees for the same task (e.g., one for production work, one for testing) share a single sandbox and conflate their DB state.

Mike's case: a testing worktree may have different DB needs than the non-testing version.
