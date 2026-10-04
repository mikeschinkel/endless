On 2026-10-03 a `git pull` on the main checkout, with `pull.rebase = true` in the repo config, replayed main's 232 unpushed commits onto three README commits made on GitHub. Every replayed commit got a new SHA, and the rewrite was pushed to origin.

Every task branch based on the old main kept the old copies. `git merge-base main <branch>` then fell back to before the rewrite, and the E-2184 migration gate (internal/landgate, `added()`) diffs `merge-base..HEAD` with `--diff-filter=A`. So the branch's copies of main's own migrations (00011_auto_spawn.go, 00012_supersedes_relation.sql) were reported as the branch adding them, and every session's land was refused.

The refusal's remedy made it worse. Step 1 (`git rebase main`) is the whole fix, because git drops patch-identical commits. Step 2 told the agent to rename those files to 00013/00014: renaming main's own migrations, which would ship duplicate migrations.
