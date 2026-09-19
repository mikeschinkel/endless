Interaction with E-2095 part 2: both this and the not-on-main report need the
same underlying probe — "does this branch hold any commit main lacks?" — and it
should be one implementation, not two. E-2089 established that the probe must use
`git range-diff` rather than `git cherry`, because `worktree land` rebases and
conflict resolution changes the patch-id, after which the branch copy reads as
unlanded forever. Whichever of the two lands first should own the probe.

Do not weaken the existing refusals. The reaper already declines a worktree with
a live session in it, and that stays. "Settled" here means the same thing
`task unsettled` means — no uncommitted changes, and nothing on the branch that
main does not already have — not merely "the task looks finished".
