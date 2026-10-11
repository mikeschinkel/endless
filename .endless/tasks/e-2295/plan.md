1. Check the abort. Make the rebase --abort in land's rebase-failure path
   (and the sibling abort sites in worktree_cmd.py that follow the same shape)
   verify its own result: retry on index.lock contention with the same backoff
   E-2174 uses for the rebase, then confirm no rebase is in progress and HEAD is
   back on the task branch.
2. Tell the truth when it still fails. If the abort cannot be confirmed, the
   refusal must say the worktree is MID-REBASE with HEAD detached, name the
   recovery (`git rebase --abort` in that worktree, then re-run the land), and
   not prescribe `worktree diagnose`, which refuses on a detached HEAD.
3. Consider letting `worktree diagnose` recognise an in-progress rebase instead
   of refusing on the detached HEAD it implies.
4. Test: a land whose abort is made to fail (simulated lock) leaves a refusal
   that names the in-progress rebase, and a land whose abort succeeds still
   reports the branch unchanged.
