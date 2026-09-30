(a) Python wrapper: when --db sandbox is set and cwd is a self-dev worktree, exec <worktree>/bin/endless-go instead of PATH lookup.

(b) Justfile 'land' recipe: PATH-prepend $wt/bin before the 'endless db backup' / 'endless db apply-change' calls so subprocesses inherit the worktree binary.
