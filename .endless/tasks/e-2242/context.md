On 2026-10-05 a `git pull --rebase` on the main checkout rewrote main again (reflog: `pull (finish): refs/heads/main onto 8aa14c4f`). A task branch forked before it still held 96 copies of main's commits under their old SHAs. `endless worktree land` failed with "rebase conflict while replaying your commits after dropping base auto-amend commits" on a copied `Endless: add decision ED-1604` commit, whose file main had since updated.

Two gaps let that through, both left by the land gate fix for a rewritten main:

1. Land's orphan-stripping step (`_drop_orphan_amendable_commits` in src/endless/worktree_cmd.py) runs `git rebase --onto <base> <last_orphan>`. Git drops a commit only when its change is already in the upstream argument, which here is the orphan, not base. So copies of base's commits are replayed onto base and conflict. A plain `git rebase <base>` in the worktree drops them cleanly, and that cleared this case.

2. The land gate (internal/landgate) refuses a branch holding copies of base's commits with a `base_rewritten` verdict that names `git rebase <base>`. But it runs that check only inside the migration check, which runs only for a project declaring migration dirs in .endless/config.json. Any other project never gets the refusal and meets the conflict instead.
