_drop_orphan_amendable_commits in src/endless/worktree_cmd.py uses 'git rebase --onto base_branch last_orphan_sha HEAD'.

Step 5's 'git merge --ff-only branch' then targets the pre-rebase branch tip (an ancestor of main), failing 'diverging branches' regardless of retry count.

Hit during E-1347 land 2026-05-15: 8 retries all failed despite main being stable; required manual recovery.
