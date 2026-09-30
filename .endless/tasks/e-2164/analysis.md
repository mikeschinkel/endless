'=>' blocks, '->' should-precede, '|' groups tasks sharing both sides (binding tighter than either arrow), '<>' marks two tasks that must not run concurrently because their worktrees touch the same paths, derived from a cache and never computed on the render path.

Tasks appear only with an ordering relation; the viewing session's task, the spawning task, in-flight, parent, hidden and phase=later rows are excluded, but any task blocking a survivor is added even when later or in flight (dim).

Ordering tasks with no relation stays out of scope.
