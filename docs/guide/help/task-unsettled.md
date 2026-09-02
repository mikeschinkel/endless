section: orchestration
covers: Why a worktree hasn't settled — modified (commit or discard) vs unlanded (land).

Reads the same probe as the ◆ in `session status` and the worktree reaper, so none of the three can disagree. Unlanded is measured by CONTENT — `worktree land` rebases, so a landed commit's SHA on the branch is not the one on the base. A git call that cannot run makes the verdict undetermined, which marks the row rather than reporting a tree nobody could inspect as clean.
