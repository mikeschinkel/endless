1. A periodic job (internal/jobs) that, for each project with sync enabled, runs on the main checkout only:
   a. `git fetch <remote>` for main's upstream.
   b. If origin has commits main lacks and main has none origin lacks: `git merge --ff-only @{u}`.
   c. If main has commits origin lacks and origin has none main lacks: `git push <remote> main` (never --force).
   d. If both sides have commits the other lacks (diverged): change nothing; record a fault naming the divergence, the counts each side, and the choices (merge origin into main, or rebase — with the rebase's cost: every open task branch must `git rebase main`). Never pick rebase or merge for the user.
   e. A failed fetch or push (network, credentials, rejected) records a fault; it never silently retries forever (use the jobs backoff).
   Never invoke bare `git pull`, so the user's pull.rebase / pull.ff cannot change what Endless does.
2. Config: an enable/disable option (off by default for PRODUCT until proven), plus the interval, in the project/machine config layers like the other job settings.
3. Rewrite detection: when main's history no longer contains commits that open task branches were based on (patch-equivalent copies exist under new SHAs), report it — in the job's fault and in `endless worktree check` — with the remedy `git rebase main` per branch, before any land fails. Reuse E-2232's detector rather than writing a second one: `ownCommits` in internal/landgate (its `equivalent` side is the branch's copies of main's commits under old SHAs). Export it, or move it to a package both the land gate and this job import, so the two can never disagree about what a rewrite is.
4. Research step, recorded in the task's analysis before implementing 1d's merge option: what in Endless assumes main is linear (land's rebase/fast-forward, the land gate's merge-base, the ledger amend's reachability test, anything walking main's history) and whether a merge commit on main breaks any of it.
5. Tests: fast-forward, push, diverged-refuses-and-records, failed-push fault, disabled-does-nothing, and that a user's `pull.rebase=true` has no effect on the job.
