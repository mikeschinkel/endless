Adopt ED-1540 vocabulary: rename the union-sense 'Dirty' (taskWorktreeDirty, the row Dirty flag, and AnnotateSessionStatusDirty in the reap-worktrees monitor -- whose comment explicitly collapses 'not landed' and 'dirty since land' -- plus the 'dirty-or-unlanded' meaning in worktree_anomalies) to 'unsettled'. Where 'dirty' means only the uncommitted working-tree sub-state, use 'modified'. Update the orchestration guide's 'dirty, unmerged' wording to the modified, unlanded, unsettled vocabulary.

Scope: the ~11 files matching 'dirty'.
