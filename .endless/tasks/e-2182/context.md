Migrations take the next free integer and goose refuses duplicates and out-of-order versions, so two worktrees adding migrations collide and one must rename by hand.
