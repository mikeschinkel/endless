Companion to E-987 (auto-commit endless-managed files during land).

## The case

E-987's land flow rebases the worktree onto main after auto-committing endless-managed files. In normal operation, the rebase has nothing to conflict on — per E-972, all auto-managed files (events log, snapshots) are written to main's location, never the worktree's. Worktree branches don't modify these files, so rebase is conflict-free.

But corner cases will hit eventually:
1. A worktree predates E-972 and has stale auto-file writes baked into commits.
2. Someone hand-edited an auto-file in a worktree (prohibited by E-1017 but enforcement isn't perfect).
3. A future endless feature accidentally writes one of these files from a worktree without going through main's location.

When any of these happen, 'endless worktree land' will fail mid-rebase with a git conflict message. The user needs a recovery path that doesn't require deep git knowledge.

## v1 (this task)

Document the recovery procedure. Probably:

  git rebase --abort
  git checkout main -- <auto-files-with-conflicts>
  endless worktree land <task-id>  # retry

Surface this as part of the rebase-failure error message from 'endless worktree land' (a hint, not just a raw git error).

## v2 (later if needed)

Build an 'endless worktree land --resolve-auto' flag that automates the v1 procedure: detects the conflict is on auto-managed files, takes main's version, retries the rebase. Only safe when the conflict IS on auto files (don't auto-resolve user-work conflicts).

## When to escalate to v2

- If we hit this conflict more than 2-3 times in real use.
- If the documented recovery feels unsafe to type from a hot-key state.