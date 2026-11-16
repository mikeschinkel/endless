Removing an accidentally committed file from git history (with
`git filter-repo`, say) changes the SHA of the first commit that held it and
of every commit after it. The usual reason to do this is a secret committed
by accident, and then there is no choice: the rewrite has to happen.

Endless records commit SHAs in several places, among them a task's land
commit, the names of verify-run reports (`verify-<timestamp>-<sha>.ctrf.json`)
and entries in the event ledger. After a rewrite those SHAs name commits that
no longer exist. The concern is that a rewrite leaves Endless corrupt with no
way to repair it.

What prompted this: in gomion, a 23 MB `cmd/gomion/gomion` binary was found
committed in ten `checkpoint` commits, and removing it from history would
rewrite every commit since the first of them, including landed task commits
Endless has recorded.

This is not limited to secrets in `.endless/`: the file that forces the
rewrite can be anywhere in the project.
