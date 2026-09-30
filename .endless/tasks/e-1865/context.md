The flat `session status` view marks a row unsettled with a bare ◆ but never says why, and the two sub-states need opposite fixes (modified means commit-or-discard, unlanded means land), so the user must run git by hand in the worktree to find out

-- E-1537's worktree is the case that prompted this.
