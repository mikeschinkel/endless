Implementation would have _land_worktree fork after ff-merge to run _record_landing in a fresh subprocess, sidestepping the import cache —

but this puts self-dev plumbing into product code (against the convention that Justfile is the dev-only layer).

Phase=maybe carries the undecided commitment; promote and implement in this same task if the bare path becomes more commonly used.
