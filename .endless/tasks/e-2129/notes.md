E-1881's counterpart and the other half of keeping worktrees in sync: E-1881 moves endless-managed metadata commits UP to main, which is what lets a worktree settle and become reclaimable;

Blocked by E-1881 rather than merely related: without it the branch still holds metadata commits main changed independently, so a rebase hits exactly the conflicts E-1882 exists to resolve by hand.
