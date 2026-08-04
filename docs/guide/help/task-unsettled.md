section: orchestration
covers: Why a worktree hasn't settled — modified (commit or discard) vs unlanded (land).

Reads the same probe as the ◆ in `session status`, so the two can never disagree. Both are fail-open: a failed git call reads as "settled", which the command flags rather than claiming the tree is clean.
