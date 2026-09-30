Proposal: pre-rebase step in 'endless worktree land' that enumerates commits in task..main with subject==LedgerCommitSubject AND not reachable from main, then drops them via rebase --onto.

Cheap to detect, cheap to fix; eliminates manual surgery on every future land that happens to span an amend boundary.
