Recover instead: if the branch's only delta from main is the plan file, recreate the worktree fresh from main and re-materialize the plan from tasks.text (reconciling DB-vs-file disagreements); if the branch carries real (non-plan) work, error with actionable guidance.

Every error must name the problem and hand the user a command or two to resolve it.
