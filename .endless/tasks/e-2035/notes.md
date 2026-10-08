# Second sighting, and a writer beyond files (2026-10-08, from E-2270; folded in from E-2274)

From the e-2270 worktree, `endless --db sandbox task add ... --plan '# Plan'` wrote
`.endless/tasks/e-1/plan.md` into the MAIN checkout — not the worktree — and committed
it on main (bb1d1b5c6, c5377300d for E-1 and E-2), though neither task exists in the
main database. So a sandbox doc mirror can land in, and commit to, main itself, not
only the current branch as on E-1920.

New beyond mirrors: `endless --db sandbox task claim E-2 --out-of-order --unattended`
created a REAL git worktree at `.endless/worktrees/e-2` on branch `task/2` in the main
repo and ran the post-worktree-create hook. Worktree creation belongs on step 2's
writer list: a sandbox claim should create no git worktree or branch in a real repo.

The verify runner's temp-HOME throwaway project does not show either problem.
Cleanup of both was done by hand.
