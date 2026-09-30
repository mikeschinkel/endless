Fix by judging content, not SHA: for each differing path ask whether the branch's blob is reachable anywhere in main's history for that path (stale) or not (genuinely unique); paths absent on the branch just mean main is newer. Fix the probe itself so every consumer inherits it.

A reaper pass or `worktree reconcile` (E-1882) is a separate remedy and cannot substitute for a correct verdict.
