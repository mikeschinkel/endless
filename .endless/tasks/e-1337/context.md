Today 'worktree land' calls 'git worktree remove' which deletes the dir, breaking 'claude --resume' for any session whose cwd was the worktree.

Hit during E-1281's land — had to recover the dir from IntelliJ local history to resume.
