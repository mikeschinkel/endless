Likely surface: extend 'endless worktree land' (or whatever lands a task branch) to delete the local branch after a successful merge, AND/OR a post-merge git hook installed by 'endless worktree' setup.

Open question: silent-default-on, or require explicit --delete-branch?

Prefer silent-default since the decision is unambiguous; the action mirrors 'git branch -d' safety (refuses unmerged).
